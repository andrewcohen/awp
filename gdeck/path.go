package main

import (
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// A GUI app does not inherit your shell's PATH, and everything gdeck runs lives
// in it.
//
// Launched from a terminal, the app inherits that terminal's environment and
// everything works — which is why this went unnoticed through a day of
// development. Launched from Finder or the Dock, macOS gives a process
// /usr/bin:/bin:/usr/sbin:/sbin and nothing else: no mise shims, no
// ~/.local/bin, no Homebrew. Every binary this surface depends on is in the part
// that disappears — zmx, claude, jj, gh — so the packaged app fails at the first
// thing it tries to run, with a "file not found" that names a program the
// developer can run fine from their own shell.
//
// The fix is to ask the login shell what the PATH is, which is what editors do
// for the same reason. It costs one subprocess at startup and is cached for the
// life of the process.
//
// Deliberately additive: entries already in the environment are kept and the
// shell's are appended. An explicitly-set PATH — from a wrapper script, a test,
// or `wails3 dev` — should not be thrown away by this.

// loginShell is the shell the user actually logs in with, which is not
// necessarily $SHELL.
//
// $SHELL describes the shell of whatever process happened to spawn this one. On
// this machine it reads /bin/zsh inside a tool-run subshell while the account's
// real login shell is fish — and the two have entirely different PATHs, because
// the version manager is configured in one and not the other. Asking $SHELL
// found 1 of 4 tools; asking the user record found all of them.
//
// The user database is the authority, so ask it, and keep $SHELL as the
// fallback for the case where dscl is missing or answers strangely.
func loginShell() string {
	if runtime.GOOS == "darwin" {
		out, err := exec.Command("/usr/bin/dscl", ".", "-read", os.Getenv("HOME"), "UserShell").Output()
		if err == nil {
			if _, value, found := strings.Cut(strings.TrimSpace(string(out)), ":"); found {
				if shell := strings.TrimSpace(value); shell != "" {
					return shell
				}
			}
		}
	}
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	return "/bin/sh"
}

var loginPath = sync.OnceValue(func() string {
	shell := loginShell()

	// -l *and* -i, which is not belt and braces: a login shell reads .zprofile
	// but not .zshrc, and version managers activate in .zshrc. Measured on this
	// machine, -l alone found 1 of the 4 tools gdeck needs — the login files
	// carry Homebrew, and mise's shims come from the interactive file.
	//
	// The PATH is fenced in markers because an interactive shell prints things:
	// a greeting, a version-manager notice, whatever is in the rc file. Reading
	// the whole of stdout as a PATH would take that with it.
	const script = `printf "__AWP_PATH_BEGIN__%s__AWP_PATH_END__" "$PATH"`
	cmd := exec.Command(shell, "-l", "-i", "-c", script) //nolint:gosec // the shell is the user's own, from the environment.
	cmd.Env = append(os.Environ(), "TERM=dumb")
	done := make(chan string, 1)
	go func() {
		// CombinedOutput would fold stderr in; an interactive shell writes its
		// noise there and the markers keep stdout honest either way.
		out, err := cmd.Output()
		if err != nil && len(out) == 0 {
			done <- ""
			return
		}
		text := string(out)
		start := strings.Index(text, "__AWP_PATH_BEGIN__")
		end := strings.Index(text, "__AWP_PATH_END__")
		if start < 0 || end < start {
			done <- ""
			return
		}
		done <- strings.TrimSpace(text[start+len("__AWP_PATH_BEGIN__") : end])
	}()

	select {
	case got := <-done:
		slog.Info("gdeck login shell probe", "shell", shell, "path_entries", len(strings.Split(got, ":")))
		return got
	case <-time.After(10 * time.Second):
		// Ten, not three: an interactive login shell runs the user's whole
		// config — version managers, completions, greetings — and three seconds
		// is inside the range a cold one legitimately takes.
		slog.Warn("gdeck login shell probe timed out", "shell", shell)
		_ = cmd.Process.Kill()
		return ""
	}
})

// restorePath puts the login shell's PATH back into this process's environment.
//
// Called once at startup, before anything is spawned. Every exec in gdeck
// inherits os.Environ(), so fixing it here fixes all of them rather than each
// call site remembering.
func restorePath() {
	shellPath := loginPath()
	if shellPath == "" {
		return
	}

	seen := map[string]bool{}
	var merged []string
	for _, dir := range append(strings.Split(os.Getenv("PATH"), ":"), strings.Split(shellPath, ":")...) {
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		merged = append(merged, dir)
	}
	_ = os.Setenv("PATH", strings.Join(merged, ":"))
}

// toolsNeeded is what gdeck shells out to, and what a packaged launch silently
// loses. Named here so a missing one is a line in the log rather than a failed
// call somewhere later with no context.
var toolsNeeded = []string{"zmx", "claude", "jj", "gh"}

func reportTools() {
	var missing []string
	found := map[string]string{}
	for _, tool := range toolsNeeded {
		path, err := exec.LookPath(tool)
		if err != nil {
			missing = append(missing, tool)
			continue
		}
		found[tool] = path
	}
	if len(missing) > 0 {
		slog.Warn("gdeck cannot find tools it needs",
			"missing", strings.Join(missing, ", "),
			"hint", "a GUI launch does not inherit the shell PATH; see restorePath")
	}
	slog.Info("gdeck tools", "resolved", len(found), "of", len(toolsNeeded))
}
