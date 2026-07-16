// Package agenthooks installs and inspects the global agent integrations
// (Claude Code hooks, pi.dev extension) that report agent status to awp.
package agenthooks

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed assets/awp-status.ts
var piAwpStatusExtension []byte

// HookMarkerVersion bumps when the hook block schema changes; the installer
// rewrites entries whose version differs.
const HookMarkerVersion = 10

// BlockingTools lists tool names that block on user input. When a
// PreToolUse hook fires for one of these, awp reports "waiting" instead of
// "working" so the deck row reflects that the agent is paused on the user
// — not actively producing output.
var BlockingTools = []string{"AskUserQuestion"}

// HookCommand returns the shell snippet each Claude hook runs. It gates on
// $TMUX so global installation never affects non-tmux Claude usage, and
// honors $AWP_BIN for users running a non-PATH awp binary. The awp CLI
// itself falls back to reading session env from tmux when its own env is
// missing, so this works for processes that predate env injection.
//
// For UserPromptSubmit we add --prompt-stdin so report-status pulls the
// user's prompt text from the hook's JSON payload and stores it as the
// workspace's ActivePrompt. Other events leave ActivePrompt alone (empty
// prompt means "no update"), so the last user message keeps showing on the
// deck across Stop/idle transitions.
//
// For PreToolUse we add --waiting-when-tool so report-status flips the
// state to "waiting" when the tool being invoked blocks on user input
// (e.g. AskUserQuestion). Without this the row would stay "working" while
// the agent is actually paused on a question to the user.
func HookCommand(event, state string) string {
	extra := ""
	switch event {
	case "UserPromptSubmit":
		extra = " --prompt-stdin"
	case "PreToolUse":
		if len(BlockingTools) > 0 {
			extra = " --waiting-when-tool " + strings.Join(BlockingTools, ",")
		}
	}
	return `[ -n "$TMUX" ] && "${AWP_BIN:-awp}" internal report-status --state ` + state + extra + ` >/dev/null 2>&1 || true`
}

// GateRecordHookCommand returns the shell snippet for the gate-record hook
// (`awp internal gate record --result <verdict>`). stdout is NOT redirected —
// Claude reads it as the hook's result (the PostToolUse nudge). stderr is
// dropped and a non-zero exit is swallowed so recording never breaks a turn;
// record always exits 0 anyway. It gates on $TMUX and honors $AWP_BIN.
func GateRecordHookCommand(verdict string) string {
	return `[ -n "$TMUX" ] && "${AWP_BIN:-awp}" internal gate record --result ` + verdict + ` 2>/dev/null || true`
}

// GateCheckHookCommand returns the shell snippet for the gate-check hook
// (`awp internal gate check --hook`). Unlike the other hooks it must NOT
// swallow stderr or the exit code: the completion block is signalled by exit
// code 2 with the reason on stderr, which Claude feeds back to the agent. It
// exits 0 cleanly outside tmux so it never emits a spurious hook error there.
func GateCheckHookCommand() string {
	return `[ -n "$TMUX" ] || exit 0; "${AWP_BIN:-awp}" internal gate check --hook`
}

// RequireTaskHookCommand returns the shell snippet for the require-task hook
// (`awp internal require-task --hook`). Like gate check it must NOT swallow
// stderr or the exit code — a block is signalled by exit code 2 with the
// reason on stderr, which Claude feeds back to the agent. Unlike the other
// hooks it is NOT gated on $TMUX: the check reads the session's task list
// (~/.claude/tasks/<session>), which is meaningful in every session, not just
// awp-managed tmux ones. It guards on awp being resolvable so a session
// without awp on PATH fails open (exit 0) rather than emitting a hook error.
func RequireTaskHookCommand() string {
	return `command -v "${AWP_BIN:-awp}" >/dev/null 2>&1 || exit 0; "${AWP_BIN:-awp}" internal require-task --hook`
}

// LoopTrackHookCommand returns the shell snippet for the loop-track hook
// (`awp internal loop track`). Like gate record it swallows stdout/stderr and
// the exit code so tracking never breaks a turn (it always exits 0 anyway),
// gates on $TMUX, and honors $AWP_BIN. It carries no --result: the phase it
// records doesn't depend on the tool's pass/fail outcome.
func LoopTrackHookCommand() string {
	return `[ -n "$TMUX" ] && "${AWP_BIN:-awp}" internal loop track >/dev/null 2>&1 || true`
}

// hookSpec is one awp-managed hook entry: the event it lives under, an
// optional tool-name matcher, a stable id (so multiple awp entries can
// coexist under one event and each be upserted independently), and the
// command it runs. state is carried in the marker only for the status
// entries (empty for gate entries).
type hookSpec struct {
	event   string
	matcher string
	id      string
	state   string
	command string
}

// gateHookSpecs are the dev-loop enforcement hooks: a PostToolUse(Bash) hook
// that records gate results, and a PreToolUse(TaskUpdate) hook that resets a
// unit's gates / blocks completion. They coexist with the matcher-less status
// entries on the same events. The commands self-gate on the repo having a
// dev_loop configured, so installing them globally is a no-op elsewhere.
func gateHookSpecs() []hookSpec {
	return []hookSpec{
		// PostToolUse fires only on success, PostToolUseFailure only on
		// failure — so the event carries the pass/fail verdict; we pass it
		// explicitly via --result.
		{event: "PostToolUse", matcher: "Bash", id: "gate-record", command: GateRecordHookCommand("pass")},
		{event: "PostToolUseFailure", matcher: "Bash", id: "gate-record", command: GateRecordHookCommand("fail")},
		{event: "PreToolUse", matcher: "TaskUpdate", id: "gate-check", command: GateCheckHookCommand()},
		// Matcher-less: the loop-track hook fires for every tool so it can
		// derive the current dev-loop phase from edits/reads/bash and reset it
		// on a TaskUpdate→in_progress. Runs on both the success and failure
		// PostToolUse events so a failed command still advances the phase.
		{event: "PostToolUse", id: "loop-track", command: LoopTrackHookCommand()},
		{event: "PostToolUseFailure", id: "loop-track", command: LoopTrackHookCommand()},
	}
}

// taskHookSpecs are the task-discipline enforcement hooks. Today just one: a
// PreToolUse(Edit|Write|NotebookEdit) hook that denies editing a non-markdown
// file unless a task is in_progress. Unlike the gate hooks it is not gated on
// a dev_loop or on tmux — it enforces in every session.
func taskHookSpecs() []hookSpec {
	return []hookSpec{
		{event: "PreToolUse", matcher: "Edit|Write|NotebookEdit", id: "require-task", command: RequireTaskHookCommand()},
	}
}

// desiredHookSpecs is the full set of awp-managed hook entries: the status
// reporters (one matcher-less entry per event), the gate hooks, and the
// task-discipline hooks.
func desiredHookSpecs() []hookSpec {
	var specs []hookSpec
	for event, state := range DesiredClaudeHooks() {
		specs = append(specs, hookSpec{event: event, id: "status", state: state, command: HookCommand(event, state)})
	}
	specs = append(specs, gateHookSpecs()...)
	specs = append(specs, taskHookSpecs()...)
	return specs
}

// specsByEvent groups the desired specs by event.
func specsByEvent() map[string][]hookSpec {
	out := map[string][]hookSpec{}
	for _, s := range desiredHookSpecs() {
		out[s.event] = append(out[s.event], s)
	}
	return out
}

// DesiredClaudeHooks returns the event → state mapping awp installs into
// Claude's global settings. SessionStart marks the workspace idle as soon
// as Claude attaches, so summoned-but-not-yet-prompted agents stop showing
// the previous run's state.
//
// PostToolUse → working flips the row back to "working" once a blocking
// tool (e.g. AskUserQuestion) returns, so the deck doesn't linger in
// "waiting" while the agent is generating its follow-up response.
//
// The "waiting" events cover every way Claude genuinely blocks on the user:
//   - PreToolUse with AskUserQuestion (via --waiting-when-tool below) —
//     Claude's own multiple-choice question.
//   - PermissionRequest — a permission dialog is up (e.g. approve a Bash
//     command or file write in default permission mode). This is the
//     dedicated event; it fires regardless of whether the user has
//     desktop Notifications configured.
//   - Elicitation — an MCP server is requesting form input.
//
// We deliberately do NOT map the Notification event to "waiting". Claude
// fires Notification both for permission prompts (already covered by the
// dedicated PermissionRequest event) and for its ~60s idle ping — and the
// idle ping fires for an agent that already finished its turn (Stop →
// idle) and is just sitting there. Mapping it to "waiting" flooded the
// tmux unread summary with ▲ triangles for agents that weren't actually
// blocked on anything. ObsoleteClaudeHooks removes any stale awp-managed
// Notification entry left over from when we did install it.
//
// Each "waiting" event resolves back to "working" on the next PreToolUse /
// PostToolUse, or to "idle" on Stop, so the row never sticks in waiting.
//
// Unknown events are ignored by older Claude Code versions, so listing
// PermissionRequest / Elicitation here is safe even if the installed
// client predates them.
func DesiredClaudeHooks() map[string]string {
	return map[string]string{
		"SessionStart":      "idle",
		"UserPromptSubmit":  "working",
		"PreToolUse":        "working",
		"PostToolUse":       "working",
		"Stop":              "idle",
		"PermissionRequest": "waiting",
		"Elicitation":       "waiting",
	}
}

// ObsoleteClaudeHooks lists events awp installed in the past but no longer
// wants. InstallClaude strips the awp-managed entry for each on the next
// sync so upgrades clean up after themselves; non-awp entries for the same
// event are left untouched.
func ObsoleteClaudeHooks() []string {
	return []string{"Notification"}
}

// InstallClaude merges (or refreshes) awp-managed hook entries into
// ~/.claude/settings.json. Returns true if the file was written.
func InstallClaude() (bool, error) {
	path, err := claudeSettingsPath()
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}

	var settings map[string]any
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if len(data) > 0 {
			if err := json.Unmarshal(data, &settings); err != nil {
				return false, fmt.Errorf("parse %s: %w", path, err)
			}
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return false, err
	}
	if settings == nil {
		settings = map[string]any{}
	}

	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}

	changed := false
	for event, specs := range specsByEvent() {
		entries, _ := hooks[event].([]any)
		entries, evtChanged := syncEventEntries(entries, specs)
		if evtChanged {
			changed = true
		}
		hooks[event] = entries
	}
	// Strip awp-managed entries for events we no longer install (e.g. the
	// old Notification → waiting hook). Leave any user-defined entries for
	// the same event in place, and drop the event key entirely once only
	// awp's entry was there.
	for _, event := range ObsoleteClaudeHooks() {
		entries, ok := hooks[event].([]any)
		if !ok {
			continue
		}
		entries, evtChanged := removeAwpEntry(entries)
		if !evtChanged {
			continue
		}
		changed = true
		if len(entries) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = entries
		}
	}
	if !changed {
		return false, nil
	}
	settings["hooks"] = hooks

	encoded, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return false, err
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// IsClaudeInstalled reports whether the awp hook entries are present in
// ~/.claude/settings.json for every event in DesiredClaudeHooks. Missing
// file or partial install returns false.
func IsClaudeInstalled() (bool, error) {
	path, err := claudeSettingsPath()
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if len(data) == 0 {
		return false, nil
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return false, nil
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		return false, nil
	}
	for event, specs := range specsByEvent() {
		entries, _ := hooks[event].([]any)
		if !eventAwpEntriesMatch(entries, specs) {
			return false, nil
		}
	}
	return true, nil
}

// PiExtensionPath returns the on-disk path where the pi.dev extension lives.
func PiExtensionPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".pi", "agent", "extensions", "awp-status.ts"), nil
}

// InstallPi writes the pi.dev extension to its global location. Returns true
// if the file was created or replaced.
func InstallPi() (bool, error) {
	path, err := PiExtensionPath()
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("create pi extensions dir: %w", err)
	}
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if string(existing) == string(piAwpStatusExtension) {
		return false, nil
	}
	if err := os.WriteFile(path, piAwpStatusExtension, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// IsPiInstalled reports whether the pi.dev extension at the canonical path
// matches the embedded content.
func IsPiInstalled() (bool, error) {
	path, err := PiExtensionPath()
	if err != nil {
		return false, err
	}
	existing, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return string(existing) == string(piAwpStatusExtension), nil
}

func claudeSettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

// awpCommandSignatures are stable substrings the awp-installed hook commands
// contain, across every version and binary-path variant (`awp …` vs
// `"${AWP_BIN:-awp}" …`). Recognizing entries by them lets us reclaim hooks
// written by awp versions that predate the x-awp marker (or the id field).
var awpCommandSignatures = []string{"internal report-status", "gate record", "gate check", "loop track", "require-task"}

// entryCommand returns the first command string in a hook entry's hooks list.
func entryCommand(entry map[string]any) string {
	hooks, _ := entry["hooks"].([]any)
	for _, raw := range hooks {
		if h, ok := raw.(map[string]any); ok {
			if cmd, _ := h["command"].(string); cmd != "" {
				return cmd
			}
		}
	}
	return ""
}

// isAwpEntry reports whether a hook entry was authored by awp — either tagged
// with the x-awp marker (current installs) or recognizable by one of the
// commands awp installs (entries written before the marker existed).
func isAwpEntry(entry map[string]any) bool {
	if _, ok := entry["x-awp"]; ok {
		return true
	}
	cmd := entryCommand(entry)
	for _, sig := range awpCommandSignatures {
		if strings.Contains(cmd, sig) {
			return true
		}
	}
	return false
}

// awpEntryID resolves an awp entry's stable id — from the x-awp marker when
// present, otherwise inferred from its command so pre-id installs still map
// onto the right desired spec. Returns "" for an unrecognized awp entry.
func awpEntryID(entry map[string]any) string {
	if x, ok := entry["x-awp"].(map[string]any); ok {
		if id, _ := x["id"].(string); id != "" {
			return id
		}
	}
	cmd := entryCommand(entry)
	switch {
	case strings.Contains(cmd, "gate record"):
		return "gate-record"
	case strings.Contains(cmd, "gate check"):
		return "gate-check"
	case strings.Contains(cmd, "require-task"):
		return "require-task"
	case strings.Contains(cmd, "loop track"):
		return "loop-track"
	case strings.Contains(cmd, "internal report-status"):
		return "status"
	}
	return ""
}

// syncEventEntries reconciles one event's entries against the awp specs
// desired for it. Each awp entry is matched to a spec by id and rewritten in
// place if stale; awp entries whose id is no longer desired (or duplicates)
// are dropped; missing specs are appended. Non-awp entries keep their place
// and order.
func syncEventEntries(entries []any, specs []hookSpec) ([]any, bool) {
	desiredByID := map[string]hookSpec{}
	for _, s := range specs {
		desiredByID[s.id] = s
	}
	out := make([]any, 0, len(entries)+len(specs))
	seen := map[string]bool{}
	changed := false
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok || !isAwpEntry(entry) {
			out = append(out, raw)
			continue
		}
		id := awpEntryID(entry)
		spec, want := desiredByID[id]
		if !want || seen[id] {
			// No longer desired, unrecognized, or a duplicate awp entry.
			changed = true
			continue
		}
		seen[id] = true
		desired := desiredEntry(spec)
		if !jsonEqual(entry, desired) {
			changed = true
		}
		out = append(out, desired)
	}
	for _, s := range specs {
		if seen[s.id] {
			continue
		}
		out = append(out, desiredEntry(s))
		seen[s.id] = true
		changed = true
	}
	return out, changed
}

// removeAwpEntry drops every awp-authored entry, preserving order and any
// non-awp entries. Returns the filtered slice and whether anything was
// removed.
func removeAwpEntry(entries []any) ([]any, bool) {
	out := entries[:0:0]
	removed := false
	for _, raw := range entries {
		if entry, ok := raw.(map[string]any); ok && isAwpEntry(entry) {
			removed = true
			continue
		}
		out = append(out, raw)
	}
	return out, removed
}

// eventAwpEntriesMatch reports whether the event carries exactly the desired
// awp entries in canonical shape (each spec present once, equal to desired,
// no stray awp entries). Drift makes IsClaudeInstalled report "not installed"
// so the next InstallClaude reconciles.
func eventAwpEntriesMatch(entries []any, specs []hookSpec) bool {
	desiredByID := map[string]hookSpec{}
	for _, s := range specs {
		desiredByID[s.id] = s
	}
	seen := map[string]bool{}
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok || !isAwpEntry(entry) {
			continue
		}
		id := awpEntryID(entry)
		spec, want := desiredByID[id]
		if !want || seen[id] {
			return false
		}
		if !jsonEqual(entry, desiredEntry(spec)) {
			return false
		}
		seen[id] = true
	}
	return len(seen) == len(desiredByID)
}

func desiredEntry(s hookSpec) map[string]any {
	marker := map[string]any{
		"version": float64(HookMarkerVersion),
		"id":      s.id,
	}
	if s.state != "" {
		marker["state"] = s.state
	}
	entry := map[string]any{
		"x-awp": marker,
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": s.command,
			},
		},
	}
	if s.matcher != "" {
		entry["matcher"] = s.matcher
	}
	return entry
}

func jsonEqual(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}
