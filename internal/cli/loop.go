package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/andrewcohen/awp/internal/workspace"
)

// runLoop dispatches `awp internal loop <subcommand>`. Today only `track`
// exists: the PostToolUse hook that keeps the dev-loop snapshot's phase live in
// the state cache so the deck renders the current phase on the fast first paint
// without a transcript scan.
func (a *App) runLoop(args []string) error {
	if len(args) == 0 || isHelpArgSlice(args) {
		_, _ = fmt.Fprintln(a.out, loopUsage)
		return nil
	}
	switch args[0] {
	case "track":
		return runLoopTrack()
	default:
		return fmt.Errorf("unknown loop subcommand %q", args[0])
	}
}

const loopUsage = `awp internal loop — dev-loop status cache hook

Usage:
  awp internal loop track   PostToolUse hook (all tools): update the cached
                            dev-loop Phase from the tool that just ran, so the
                            deck renders the current phase on the fast first
                            paint without a transcript scan.

Reads the Claude hook JSON payload on stdin and resolves the workspace from
$AWP_WORKSPACE / $AWP_REPO_ROOT (tmux fallback). No-ops silently when the repo
has no dev_loop configured. Always exits 0.`

// runLoopTrack is the PostToolUse hook installed for all tools. It updates the
// workspace's cached dev-loop Phase (and the per-unit Started flag that drives
// phase derivation) from the tool that just ran, using the same
// watch.Loop.PhaseForTool the transcript scan uses — one source of truth, no
// drift. A TaskUpdate to in_progress clears the phase so the new unit
// re-derives it fresh (mirrors the scan's resetUnit).
//
// Division of labor with the other dev-loop hooks: gate pass/fail stays in
// `gate record`, unit reset / completion gating in `gate check`, and done/total
// with the deck's transcript reconciler. This hook owns only Phase + Started.
//
// It writes only when the phase or started flag changes (compare-and-skip) so a
// per-tool-call hook doesn't churn the state file, and always returns nil (exits
// 0) so a hook error never breaks an agent turn.
func runLoopTrack() error {
	payload, _ := readGatePayload()
	if payload.ToolName == "" {
		return nil
	}
	loop, root, wsName, ok := resolveGateLoop()
	if !ok {
		return nil
	}

	phase, hasTasks := "", false
	if cur := currentDevLoop(root, wsName); cur != nil {
		phase, hasTasks = cur.Phase, cur.HasTasks
	}

	newPhase, newHasTasks := phase, hasTasks
	switch payload.ToolName {
	case "TaskCreate":
		var in struct {
			Subject string `json:"subject"`
			Content string `json:"content"`
		}
		_ = json.Unmarshal(payload.ToolInput, &in)
		if strings.TrimSpace(in.Subject) != "" || strings.TrimSpace(in.Content) != "" {
			newHasTasks = true // a task list now exists → past explore
		}
	case "TaskUpdate":
		var in struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal(payload.ToolInput, &in)
		switch in.Status {
		case "in_progress":
			newHasTasks = true
			newPhase = "" // new unit — reset to the loop's start (implement)
		case "completed":
			// Finishing a unit resets the loop toward implement for the next
			// one (this hook only fires for an allowed completion — a denied
			// one blocks the tool, so PostToolUse never runs).
			newPhase = ""
		}
	default:
		command := ""
		if payload.ToolName == "Bash" {
			command = payload.bashCommand()
		}
		if p := loop.PhaseForTool(payload.ToolName, command, hasTasks); p != "" {
			newPhase = p
		}
	}

	// Crossing into the loop (the first task list) leaves explore behind.
	if newHasTasks && !hasTasks {
		newPhase = ""
	}
	// Resolve the phase against the task-list boundary (shared with the scan).
	newPhase = loop.ResolvePhase(newHasTasks, newPhase)

	if newPhase == phase && newHasTasks == hasTasks {
		return nil // no change → skip the write
	}
	_ = stateUpdater().Update(root, func(entries map[string]workspace.Entry) map[string]workspace.Entry {
		name := resolveLiveWorkspaceName(entries, wsName)
		entry, ok := entries[name]
		if !ok {
			return entries
		}
		s := entry.DevLoop
		if s == nil {
			s = &workspace.DevLoopSnapshot{}
		}
		s.Phase = newPhase
		s.HasTasks = newHasTasks
		entry.DevLoop = s
		entries[name] = entry
		return entries
	})
	return nil
}
