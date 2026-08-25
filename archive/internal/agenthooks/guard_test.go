package agenthooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stubAwp writes an executable that records each invocation, and returns its
// path. Running the snippets for real is the point: these are shell one-liners
// whose `&&` / `||` chaining is easy to get subtly wrong, and a string
// comparison would happily pass on a snippet that never fires.
func stubAwp(t *testing.T) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	bin = filepath.Join(dir, "awp")
	log = filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$@\" >> " + log + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	return bin, log
}

// fired reports whether running the snippet under env invoked awp.
func fired(t *testing.T, command, bin, log string, env []string) bool {
	t.Helper()
	_ = os.Remove(log)
	cmd := exec.Command("sh", "-c", command)
	cmd.Env = append([]string{"PATH=/usr/bin:/bin", "AWP_BIN=" + bin}, env...)
	_ = cmd.Run() // exit codes are the hooks' own signalling, not a test failure
	_, err := os.Stat(log)
	return err == nil
}

// managedCommands is every snippet the installer writes, by name.
func managedCommands() map[string]string {
	return map[string]string{
		"status/UserPromptSubmit": HookCommand("UserPromptSubmit", "working"),
		"status/PreToolUse":       HookCommand("PreToolUse", "working"),
		"status/Stop":             HookCommand("Stop", "idle"),
		"gate-record":             GateRecordHookCommand("pass"),
		"gate-check":              GateCheckHookCommand(),
		"loop-track":              LoopTrackHookCommand(),
	}
}

// The bug: every one of these guarded on $TMUX, and vterm strips TMUX so a
// nested client will start. An agent in a deck pane fired none of them — the
// deck saw it idle forever and recorded none of its gates.
func TestAHookFiresForAnAgentInAPane(t *testing.T) {
	bin, log := stubAwp(t)
	pane := []string{"AWP_WORKSPACE=ws", "AWP_REPO=proj"} // a pane: no TMUX
	for name, command := range managedCommands() {
		if !fired(t, command, bin, log, pane) {
			t.Errorf("%s did not fire with AWP_WORKSPACE set and no TMUX:\n  %s", name, command)
		}
	}
}

// The tmux arm has to stay: report-status reads the workspace from the tmux
// session env for processes that predate env injection, and those carry no
// AWP_WORKSPACE of their own.
func TestAHookStillFiresInATmuxSessionWithNoWorkspaceEnv(t *testing.T) {
	bin, log := stubAwp(t)
	legacy := []string{"TMUX=/tmp/tmux-501/default,123,0"}
	for name, command := range managedCommands() {
		if !fired(t, command, bin, log, legacy) {
			t.Errorf("%s stopped firing in tmux, orphaning sessions that predate env injection:\n  %s", name, command)
		}
	}
}

// These install globally, so Claude used anywhere that is not an awp workspace
// must not reach awp at all.
func TestHooksStayInertOutsideAWorkspace(t *testing.T) {
	bin, log := stubAwp(t)
	for name, command := range managedCommands() {
		if fired(t, command, bin, log, nil) {
			t.Errorf("%s ran awp outside any workspace:\n  %s", name, command)
		}
	}
}

// require-task is deliberately ungated at the shell level — it self-gates in
// Go on the repo having a dev_loop — so it is the one snippet that runs
// everywhere awp is resolvable.
func TestRequireTaskIsNotGuarded(t *testing.T) {
	bin, log := stubAwp(t)
	if !fired(t, RequireTaskHookCommand(), bin, log, nil) {
		t.Error("require-task stopped running; it self-gates in Go and must reach awp to do so")
	}
	if strings.Contains(RequireTaskHookCommand(), "AWP_WORKSPACE") {
		t.Error("require-task grew a shell-level workspace guard, which would skip its own Go-side gating")
	}
}
