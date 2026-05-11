package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/andrewcohen/awp/internal/state"
	"github.com/andrewcohen/awp/internal/workspace"
)

// validReportStates is the closed set of states agents may report.
var validReportStates = map[string]struct{}{
	"working": {},
	"idle":    {},
	"waiting": {},
	"exited":  {},
}

// runReportStatus is the entry point for `awp internal report-status`.
//
// It is invoked by per-agent hooks/extensions installed globally via
// `awp init hooks`. The hook command resolves the workspace via
// $AWP_WORKSPACE (workspace name) and one of:
//   - $AWP_REPO_ROOT (preferred, absolute repo root path)
//   - $AWP_REPO      (project basename; ambiguous if multiple repos share it)
//
// When env vars are missing the command exits 0 silently so a misconfigured
// hook never breaks an agent turn.
//
// Optional flags capture the active prompt alongside the state transition:
//   - --prompt <text>     persist the literal text as the workspace's ActivePrompt.
//   - --prompt-stdin      read a Claude-style hook payload JSON from stdin and
//                         extract its top-level "prompt" field. Empty/missing
//                         is treated as "no prompt update" rather than an error.
func runReportStatus(args []string, out io.Writer) error {
	if isHelpArgSlice(args) {
		_, _ = fmt.Fprintln(out, "Usage: awp internal report-status --state <working|idle|waiting|exited> [--prompt <text>|--prompt-stdin]")
		return nil
	}
	state := ""
	prompt := ""
	promptStdin := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--state":
			if i+1 >= len(args) {
				return fmt.Errorf("--state requires a value")
			}
			state = args[i+1]
			i++
		case strings.HasPrefix(arg, "--state="):
			state = strings.TrimPrefix(arg, "--state=")
		case arg == "--prompt":
			if i+1 >= len(args) {
				return fmt.Errorf("--prompt requires a value")
			}
			prompt = args[i+1]
			i++
		case strings.HasPrefix(arg, "--prompt="):
			prompt = strings.TrimPrefix(arg, "--prompt=")
		case arg == "--prompt-stdin":
			promptStdin = true
		default:
			return fmt.Errorf("unknown argument %q", arg)
		}
	}
	state = strings.ToLower(strings.TrimSpace(state))
	if state == "" {
		return errors.New("--state is required")
	}
	if _, ok := validReportStates[state]; !ok {
		return fmt.Errorf("invalid --state %q (want working|idle|waiting|exited)", state)
	}
	if promptStdin {
		// Best-effort: a malformed payload should never break the agent turn.
		// Silently drop errors and fall through with prompt="".
		if stdinPrompt, ok := readPromptFromStdin(reportStatusStdin()); ok {
			prompt = stdinPrompt
		}
	}
	prompt = strings.TrimSpace(prompt)

	workspaceName, repoName, repoRoot := resolveWorkspaceIdent()
	if workspaceName == "" {
		return nil
	}
	return writeWorkspaceStatus(workspaceName, repoName, repoRoot, state, prompt)
}

// readPromptFromStdin parses Claude's UserPromptSubmit hook payload and
// returns the "prompt" string. Returns (_, false) when stdin is empty, not
// JSON, or the field is missing — callers should treat that as "no prompt
// update", not an error.
func readPromptFromStdin(r io.Reader) (string, bool) {
	data, err := io.ReadAll(r)
	if err != nil || len(data) == 0 {
		return "", false
	}
	var payload struct {
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", false
	}
	if payload.Prompt == "" {
		return "", false
	}
	return payload.Prompt, true
}

// resolveWorkspaceIdent returns (AWP_WORKSPACE, AWP_REPO, AWP_REPO_ROOT) with
// a tmux fallback. When the process env is empty (e.g. the calling Claude/pi
// was launched before the tmux session env was injected), we ask tmux for the
// session-level values. This makes hooks robust against stale process
// environments.
func resolveWorkspaceIdent() (workspace, repo, repoRoot string) {
	workspace = strings.TrimSpace(os.Getenv("AWP_WORKSPACE"))
	repo = strings.TrimSpace(os.Getenv("AWP_REPO"))
	repoRoot = strings.TrimSpace(os.Getenv("AWP_REPO_ROOT"))
	if workspace != "" {
		return
	}
	if strings.TrimSpace(os.Getenv("TMUX")) == "" {
		return
	}
	if v := tmuxLocalEnv("AWP_WORKSPACE"); v != "" {
		workspace = v
	}
	if repo == "" {
		repo = tmuxLocalEnv("AWP_REPO")
	}
	if repoRoot == "" {
		repoRoot = tmuxLocalEnv("AWP_REPO_ROOT")
	}
	return
}

// tmuxLocalEnv reads a single session-level env var from the tmux server.
// We resolve the current session via display-message and pin show-environment
// with `-t` rather than relying on tmux's implicit "current session" — that
// inference depends on $TMUX_PANE being set and the pane still existing,
// which isn't always true for hook child processes.
//
// Returns empty on any error, when TMUX is unset, or when the var is unset
// or explicitly removed (`-KEY` form).
func tmuxLocalEnv(key string) string {
	if strings.TrimSpace(os.Getenv("TMUX")) == "" {
		return ""
	}
	sessionOut, err := exec.Command("tmux", "display-message", "-p", "#{session_name}").Output()
	if err != nil {
		return ""
	}
	session := strings.TrimSpace(string(sessionOut))
	if session == "" {
		return ""
	}
	out, err := exec.Command("tmux", "show-environment", "-t", session, key).Output()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(out))
	if line == "" || strings.HasPrefix(line, "-") {
		return ""
	}
	if idx := strings.IndexByte(line, '='); idx >= 0 {
		return strings.TrimSpace(line[idx+1:])
	}
	return ""
}

// writeWorkspaceStatus mutates Status (and optionally ActivePrompt) on the
// matching entry. It prefers repoRoot (absolute path) for an exact match;
// falls back to repoName (basename of each known repo root) when the root
// is unknown. It also flips Unread=true on transitions into "attention"
// states so the tmux badge surfaces the change.
//
// ActivePrompt lifecycle: a non-empty prompt argument overwrites the field
// (UserPromptSubmit / before_agent_start path). When the new status is
// "idle" or "exited" we clear ActivePrompt because the agent is no longer
// acting on that prompt. "working" and "waiting" leave it alone so the
// deck keeps showing the prompt while the agent is mid-task or pinging
// for attention.
func writeWorkspaceStatus(workspaceName, repoName, repoRoot, status, prompt string) error {
	store := stateStore()

	// Suppress the badge when the user is literally looking at this
	// workspace's session — same logic as the deck's auto-clear, applied
	// at write time so the tmux status bar stays accurate without waiting
	// for a deck refresh.
	viewing := sessionHasAttachedClient(repoName, workspaceName)
	apply := func(entries map[string]workspace.Entry) map[string]workspace.Entry {
		entry, ok := entries[workspaceName]
		if !ok {
			return entries
		}
		entry.Status = status
		switch {
		case prompt != "":
			entry.ActivePrompt = prompt
		case status == "idle" || status == "exited":
			entry.ActivePrompt = ""
		}
		if workspace.WantsAttention(status) {
			if viewing {
				entry.Unread = false
			} else {
				entry.Unread = true
			}
		}
		entries[workspaceName] = entry
		return entries
	}

	if repoRoot != "" {
		if u, ok := store.(updater); ok {
			return u.Update(repoRoot, apply)
		}
		entries, err := store.Load(repoRoot)
		if err != nil {
			return err
		}
		entries = apply(entries)
		return store.Save(repoRoot, entries)
	}

	if repoName == "" {
		return nil
	}
	all, err := store.LoadAll()
	if err != nil {
		return err
	}
	// Deterministic basename fallback: collect every repo whose basename
	// matches, sort for stability, then prefer one that actually has the
	// named workspace as an entry. If exactly one match has the entry, we
	// write to it. If multiple have it (basename collision across repos
	// that both happen to have a same-named workspace), we no-op rather
	// than silently route to an arbitrary pick — better to drop a status
	// than to badge the wrong workspace.
	var candidates []string
	for root := range all {
		if filepath.Base(root) == repoName {
			candidates = append(candidates, root)
		}
	}
	sort.Strings(candidates)
	var matches []string
	for _, root := range candidates {
		if _, ok := all[root][workspaceName]; ok {
			matches = append(matches, root)
		}
	}
	switch len(matches) {
	case 0:
		// Fall back to first basename match — preserves prior behavior
		// when there's no entry collision (most common case: one repo
		// shares the basename and the entry hasn't been created yet,
		// e.g. status arrives during workspace setup).
		if len(candidates) == 0 {
			return nil
		}
		root := candidates[0]
		if u, ok := store.(updater); ok {
			return u.Update(root, apply)
		}
		entries, err := store.Load(root)
		if err != nil {
			return err
		}
		entries = apply(entries)
		return store.Save(root, entries)
	case 1:
		root := matches[0]
		if u, ok := store.(updater); ok {
			return u.Update(root, apply)
		}
		entries, err := store.Load(root)
		if err != nil {
			return err
		}
		entries = apply(entries)
		return store.Save(root, entries)
	default:
		return nil
	}
}

type updater interface {
	Update(repoRoot string, fn func(map[string]workspace.Entry) map[string]workspace.Entry) error
}

// sessionHasAttachedClient reports whether at least one tmux client is
// currently attached to the workspace's session — i.e. the user is looking
// at it. Best-effort: any tmux/exec error returns false (we'd rather badge
// than silently miss).
func sessionHasAttachedClient(repoName, workspaceName string) bool {
	repoName = strings.TrimSpace(repoName)
	workspaceName = strings.TrimSpace(workspaceName)
	if repoName == "" || workspaceName == "" {
		return false
	}
	if strings.TrimSpace(os.Getenv("TMUX")) == "" {
		return false
	}
	session := DeckSessionName(repoName, workspaceName)
	out, err := exec.Command("tmux", "list-clients", "-t", session, "-F", "#{client_name}").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

// stateStore returns a JSONStore. Indirection exists so tests can swap it.
var stateStore = func() reportStatusStore { return state.NewJSONStore() }

// reportStatusStdin returns the reader used by --prompt-stdin. Indirection
// exists so tests can stub in a buffer without touching os.Stdin.
var reportStatusStdin = func() io.Reader { return os.Stdin }

type reportStatusStore interface {
	Load(repoRoot string) (map[string]workspace.Entry, error)
	LoadAll() (map[string]map[string]workspace.Entry, error)
	Save(repoRoot string, entries map[string]workspace.Entry) error
}
