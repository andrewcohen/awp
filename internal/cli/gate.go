package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/andrewcohen/awp/internal/config"
	"github.com/andrewcohen/awp/internal/watch"
	"github.com/andrewcohen/awp/internal/workspace"
)

// runGate dispatches `awp internal gate <subcommand>`. The subcommands are
// the dev-loop enforcement hooks: `record` (a PostToolUse(Bash) hook that
// writes the current unit's gate results into the workspace snapshot) and
// `check` (a PreToolUse(TaskUpdate) hook that resets gates on a new unit and
// denies completion until the unit's gates are green). They live under
// `internal` because they're agent/hook automation, not human-facing —
// alongside `internal report-status`.
func (a *App) runGate(args []string) error {
	if len(args) == 0 || isHelpArgSlice(args) {
		_, _ = fmt.Fprintln(a.out, gateUsage)
		return nil
	}
	switch args[0] {
	case "record":
		return runGateRecord(args[1:], a.out)
	case "check":
		return runGateCheck(args[1:], a.out)
	default:
		return fmt.Errorf("unknown gate subcommand %q", args[0])
	}
}

const gateUsage = `awp internal gate — dev-loop gate enforcement hooks

Usage:
  awp internal gate record [--json]   PostToolUse(Bash) hook: record a gate's
                                      pass/fail into the current unit's
                                      snapshot. Silent unless --json (debug)
                                      or a nudge fires.
  awp internal gate check [--hook]     PreToolUse(TaskUpdate) hook: on status
        [--workspace <ws>]             in_progress reset the unit's gates; on
                                       completed deny unless the unit's gates
                                       are all green. Without --hook, a
                                       self-check: exit 0 if ready, else
                                       non-zero + reason on stderr.

Both read the Claude hook JSON payload on stdin and resolve the workspace
from $AWP_WORKSPACE / $AWP_REPO_ROOT (tmux fallback). They no-op silently
when the repo has no dev_loop configured.`

// gateHookPayload is the subset of a Claude PreToolUse/PostToolUse hook
// payload the gate commands read. tool_response is the tool's result object;
// for Bash its shape is not authoritatively documented, so bashOutcome parses
// it defensively.
type gateHookPayload struct {
	ToolName     string          `json:"tool_name"`
	ToolInput    json.RawMessage `json:"tool_input"`
	ToolResponse json.RawMessage `json:"tool_response"`
	ToolOutput   json.RawMessage `json:"tool_output"`
	IsError      *bool           `json:"is_error"`
}

// bashCommand pulls the shell command out of a Bash tool_input payload.
func (p gateHookPayload) bashCommand() string {
	var in struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(p.ToolInput, &in)
	return in.Command
}

// bashOutcome derives pass/fail for a completed Bash command from the hook
// payload. It returns ("", false) when the command was interrupted or when
// the payload carries no reliable success signal, so callers can decide how
// to treat an unknown outcome.
//
// The Bash tool_response schema isn't guaranteed across Claude Code versions,
// so we probe a superset of the fields builds have used to express an exit
// status: a numeric exit code, an is_error/success boolean, or interruption.
func (p gateHookPayload) bashOutcome() (result string, known bool) {
	resp := p.ToolResponse
	if len(resp) == 0 {
		resp = p.ToolOutput
	}
	var r struct {
		ExitCode    *int  `json:"exit_code"`
		ExitCodeAlt *int  `json:"exitCode"`
		Code        *int  `json:"code"`
		ReturnCode  *int  `json:"returnCode"`
		IsError     *bool `json:"is_error"`
		IsErrorAlt  *bool `json:"isError"`
		Success     *bool `json:"success"`
		Interrupted bool  `json:"interrupted"`
	}
	_ = json.Unmarshal(resp, &r)

	if r.Interrupted {
		// A killed/interrupted command didn't finish the gate — record
		// nothing and let the reconciler settle it from the transcript.
		return "", false
	}
	for _, code := range []*int{r.ExitCode, r.ExitCodeAlt, r.Code, r.ReturnCode} {
		if code != nil {
			return passFail(*code == 0), true
		}
	}
	for _, ok := range []*bool{boolNot(r.IsError), boolNot(r.IsErrorAlt), boolNot(p.IsError), r.Success} {
		if ok != nil {
			return passFail(*ok), true
		}
	}
	return "", false
}

func boolNot(b *bool) *bool {
	if b == nil {
		return nil
	}
	v := !*b
	return &v
}

func passFail(ok bool) string {
	if ok {
		return "pass"
	}
	return "fail"
}

// runGateRecord implements `awp gate record`: the PostToolUse(Bash) hook. It
// matches the run command against the repo's dev_loop gates and records the
// pass/fail into the in-progress unit's snapshot. It always exits 0 so a
// misconfigured hook never breaks an agent turn; on a gate transition it may
// print a PostToolUse nudge (rung 2) to stdout for the agent to read.
func runGateRecord(args []string, out io.Writer) error {
	jsonOut := false
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOut = true
		default:
			return fmt.Errorf("unknown argument %q", arg)
		}
	}

	payload, _ := readGatePayload()
	report := gateRecordReport{UnitGates: map[string]string{}}

	// tool_name may be absent when invoked by hand; the settings matcher
	// already scopes the hook to Bash, so only bail when it's explicitly not.
	if payload.ToolName != "" && payload.ToolName != "Bash" {
		return emitGateRecord(out, jsonOut, report, "")
	}

	loop, root, wsName, ok := resolveGateLoop()
	if !ok {
		return emitGateRecord(out, jsonOut, report, "")
	}
	report.Workspace = wsName

	command := payload.bashCommand()
	gate := loop.MatchGate(command)
	if gate == nil || gate.Marker {
		// Not a gate (or a phase marker with no pass/fail) — record nothing.
		return emitGateRecord(out, jsonOut, report, "")
	}
	result, known := payload.bashOutcome()
	if !known {
		// No reliable signal in the payload: assume the completed command
		// passed. A genuine failure is corrected by the transcript reconciler
		// (which reads is_error) on the next deck open — we bias toward not
		// producing a false completion block here.
		result = "pass"
	}
	report.matched = gate.Name
	report.Result = result

	var nudge string
	_ = stateUpdater().Update(root, func(entries map[string]workspace.Entry) map[string]workspace.Entry {
		name := resolveLiveWorkspaceName(entries, wsName)
		entry, ok := entries[name]
		if !ok {
			return entries
		}
		snap := entry.DevLoop
		// Record only while a unit is in progress (its gates were reset by the
		// TaskUpdate→in_progress hook, which sets UnitKey). Gates run during
		// exploration, with no unit yet, are ignored.
		if snap == nil || snap.UnitKey == "" {
			return entries
		}
		if snap.Gates == nil {
			snap.Gates = map[string]string{}
		}
		prevResult := snap.Gates[gate.Name]
		prevReady := gatesAllGreen(loop, snap.Gates)
		snap.Gates[gate.Name] = result
		report.recorded = true
		report.Unit = snap.Task
		nowReady := gatesAllGreen(loop, snap.Gates)
		nudge = gateNudge(gateNudgeMode(root), gate.Name, snap.Task, result, prevResult, prevReady, nowReady, loop, snap.Gates)
		entry.DevLoop = snap
		entries[name] = entry
		return entries
	})

	if report.recorded {
		report.UnitGates = unitGates(loop, currentGates(root, wsName))
		report.ReadyToComplete = gatesAllGreen(loop, report.UnitGates)
	}
	return emitGateRecord(out, jsonOut, report, nudge)
}

// runGateCheck implements `awp gate check`. In --hook mode it is the
// PreToolUse(TaskUpdate) hook: a status of in_progress resets the unit's gate
// results (a fresh unit begins), and completed is denied unless every
// configured gate is green. Without --hook it is a self-check the agent (or a
// human) can run: exit 0 when the current unit is ready to complete, else a
// non-zero exit with an actionable reason.
func runGateCheck(args []string, out io.Writer) error {
	hook := false
	wsFlag := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--hook":
			hook = true
		case arg == "--workspace":
			if i+1 >= len(args) {
				return fmt.Errorf("--workspace requires a value")
			}
			wsFlag = args[i+1]
			i++
		case strings.HasPrefix(arg, "--workspace="):
			wsFlag = strings.TrimPrefix(arg, "--workspace=")
		case arg == "--task":
			// Accepted for forward-compatibility; v1 checks the current unit.
			if i+1 >= len(args) {
				return fmt.Errorf("--task requires a value")
			}
			i++
		case strings.HasPrefix(arg, "--task="):
		default:
			return fmt.Errorf("unknown argument %q", arg)
		}
	}
	if hook {
		return runGateCheckHook(out)
	}
	return runGateCheckSelf(wsFlag, out)
}

// runGateCheckHook handles the PreToolUse(TaskUpdate) payload: reset on
// in_progress, deny-or-allow on completed. It always returns nil — a denial
// is expressed by printing a permissionDecision, not a non-zero exit — so a
// hook error never wedges the agent.
func runGateCheckHook(out io.Writer) error {
	payload, _ := readGatePayload()
	if payload.ToolName != "" && payload.ToolName != "TaskUpdate" {
		return nil // not our tool → allow
	}
	var in struct {
		TaskID  string `json:"taskId"`
		Status  string `json:"status"`
		Subject string `json:"subject"`
	}
	_ = json.Unmarshal(payload.ToolInput, &in)

	loop, root, wsName, ok := resolveGateLoop()
	if !ok {
		return nil // no dev_loop → allow
	}

	switch in.Status {
	case "in_progress":
		resetGateUnit(root, wsName, in.TaskID, in.Subject)
		return nil
	case "completed":
		gates := currentGates(root, wsName)
		if gatesAllGreen(loop, gates) {
			return nil // ready → allow
		}
		printPreToolUseDeny(out, gateDenyReason(loop, gates, currentUnitLabel(root, wsName)))
		return nil
	default:
		return nil // pending / deleted / unset → allow
	}
}

// runGateCheckSelf is the by-hand self-check: exit 0 when the current unit is
// ready to complete, otherwise a non-zero exit whose error message names the
// blocking gate.
func runGateCheckSelf(wsFlag string, out io.Writer) error {
	loop, root, wsName, ok := resolveGateLoop()
	if !ok {
		// No dev_loop / unresolved workspace: nothing to gate, treat as ready.
		_, _ = fmt.Fprintln(out, "gate check: no dev_loop configured — nothing to check")
		return nil
	}
	if strings.TrimSpace(wsFlag) != "" {
		wsName = wsFlag
	}
	gates := currentGates(root, wsName)
	if gatesAllGreen(loop, gates) {
		_, _ = fmt.Fprintf(out, "gate check: %s ready to complete (all gates green)\n", currentUnitLabel(root, wsName))
		return nil
	}
	return fmt.Errorf("gate check: %s", gateDenyReason(loop, gates, currentUnitLabel(root, wsName)))
}

// resetGateUnit clears the recorded gate results and rebinds the snapshot to a
// new unit (taskID) when a different unit goes in_progress. Re-marking the
// same unit in_progress is idempotent (gates are kept). Done/Total/Phase are
// left to the deck's reconciler.
func resetGateUnit(root, wsName, taskID, subject string) {
	_ = stateUpdater().Update(root, func(entries map[string]workspace.Entry) map[string]workspace.Entry {
		name := resolveLiveWorkspaceName(entries, wsName)
		entry, ok := entries[name]
		if !ok {
			return entries
		}
		snap := entry.DevLoop
		if snap == nil {
			snap = &workspace.DevLoopSnapshot{}
		}
		if snap.UnitKey == taskID && taskID != "" {
			return entries // same unit — don't wipe its gates
		}
		snap.UnitKey = taskID
		snap.Gates = map[string]string{}
		snap.Task = strings.TrimSpace(subject)
		entry.DevLoop = snap
		entries[name] = entry
		return entries
	})
}

// currentUnitLabel returns a quotable label for the in-progress unit, e.g.
// "'prompt plumbing'", falling back to "the current unit".
func currentUnitLabel(root, wsName string) string {
	entries, err := stateStore().Load(root)
	if err != nil {
		return "the current unit"
	}
	name := resolveLiveWorkspaceName(entries, wsName)
	if e, ok := entries[name]; ok && e.DevLoop != nil && strings.TrimSpace(e.DevLoop.Task) != "" {
		return "'" + e.DevLoop.Task + "'"
	}
	return "the current unit"
}

// gateDenyReason builds an actionable, quotable reason for blocking
// completion: it names the unit, the first blocking gate (a red one before a
// pending one), and the command to run.
func gateDenyReason(loop watch.Loop, gates map[string]string, unitLabel string) string {
	var pending *watch.Gate
	for i := range loop.Gates {
		g := loop.Gates[i]
		if g.Marker {
			continue
		}
		switch gates[g.Name] {
		case "fail":
			return fmt.Sprintf("unit %s can't be marked complete: gate '%s' is red (last run failed). Run `%s` and re-check.",
				unitLabel, g.Name, g.DisplayCommand())
		case "pass":
			// green — keep looking
		default:
			if pending == nil {
				pending = &loop.Gates[i]
			}
		}
	}
	if pending != nil {
		return fmt.Sprintf("unit %s can't be marked complete: gate '%s' hasn't run yet. Run `%s` and re-check.",
			unitLabel, pending.Name, pending.DisplayCommand())
	}
	// Shouldn't reach here (caller checks gatesAllGreen first), but be safe.
	return fmt.Sprintf("unit %s can't be marked complete: its gates are not all green.", unitLabel)
}

// printPreToolUseDeny prints a PreToolUse hook result denying the tool call
// with a reason the agent can act on.
func printPreToolUseDeny(out io.Writer, reason string) {
	payload := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "deny",
			"permissionDecisionReason": reason,
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintln(out, string(data))
}

// gateRecordReport is the --json debug shape for `awp gate record`.
type gateRecordReport struct {
	Workspace       string            `json:"workspace"`
	Unit            string            `json:"unit"`
	MatchedGate     *string           `json:"matched_gate"`
	Result          string            `json:"result,omitempty"`
	UnitGates       map[string]string `json:"unit_gates"`
	ReadyToComplete bool              `json:"ready_to_complete"`

	matched  string // gate name matched (empty = no match)
	recorded bool   // whether a result was actually written
}

// emitGateRecord prints the debug JSON (--json) or the PostToolUse nudge (hook
// mode), then returns nil — record is side-effect-only and always exits 0.
func emitGateRecord(out io.Writer, jsonOut bool, report gateRecordReport, nudge string) error {
	if jsonOut {
		if report.matched != "" {
			report.MatchedGate = &report.matched
		}
		data, err := json.Marshal(report)
		if err != nil {
			return nil
		}
		_, _ = fmt.Fprintln(out, string(data))
		return nil
	}
	if nudge != "" {
		printPostToolUseContext(out, nudge)
	}
	return nil
}

// printPostToolUseContext prints a PostToolUse hook result feeding text back
// into the agent's context.
func printPostToolUseContext(out io.Writer, text string) {
	payload := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":     "PostToolUse",
			"additionalContext": text,
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintln(out, string(data))
}

// gateNudge returns the rung-2 reminder for a just-recorded gate, or "" to
// stay silent. mode is "off" | "transitions" | "verbose".
func gateNudge(mode, gateName, unit, result, prevResult string, prevReady, nowReady bool, loop watch.Loop, gates map[string]string) string {
	if mode == "off" {
		return ""
	}
	label := unit
	if strings.TrimSpace(label) == "" {
		label = "the current unit"
	} else {
		label = "'" + label + "'"
	}
	switch {
	case result == "fail" && prevResult != "fail":
		return fmt.Sprintf("[dev-loop] gate '%s' is red for %s — fix it and re-run before marking the unit complete.", gateName, label)
	case nowReady && !prevReady:
		return fmt.Sprintf("[dev-loop] all gates green for %s — mark it completed and commit.", label)
	case mode == "verbose" && result == "pass":
		green, total := gateProgress(loop, gates)
		return fmt.Sprintf("[dev-loop] gate '%s' passed for %s (%d/%d gates green).", gateName, label, green, total)
	}
	return ""
}

// gateNudgeMode resolves the configured nudge verbosity for the repo,
// defaulting to "transitions".
func gateNudgeMode(root string) string {
	cfg, _ := config.Load(root)
	switch strings.ToLower(strings.TrimSpace(cfg.DevLoop.Nudge)) {
	case "off":
		return "off"
	case "verbose":
		return "verbose"
	default:
		return "transitions"
	}
}

// gateProgress counts how many of the loop's non-marker gates are green.
func gateProgress(loop watch.Loop, gates map[string]string) (green, total int) {
	for _, name := range loop.GateNames() {
		total++
		if gates[name] == "pass" {
			green++
		}
	}
	return green, total
}

// gatesAllGreen reports whether every non-marker gate in the loop is "pass".
// An empty loop (no gates) is not "green" — there's nothing to have passed.
func gatesAllGreen(loop watch.Loop, gates map[string]string) bool {
	names := loop.GateNames()
	if len(names) == 0 {
		return false
	}
	for _, name := range names {
		if gates[name] != "pass" {
			return false
		}
	}
	return true
}

// unitGates projects the loop's non-marker gates onto their recorded status,
// defaulting to "pending" for gates not yet run in the current unit.
func unitGates(loop watch.Loop, gates map[string]string) map[string]string {
	out := map[string]string{}
	for _, name := range loop.GateNames() {
		if v := gates[name]; v != "" {
			out[name] = v
		} else {
			out[name] = "pending"
		}
	}
	return out
}

// currentGates loads the workspace's recorded gate map fresh from the store.
func currentGates(root, wsName string) map[string]string {
	entries, err := stateStore().Load(root)
	if err != nil {
		return nil
	}
	name := resolveLiveWorkspaceName(entries, wsName)
	if e, ok := entries[name]; ok && e.DevLoop != nil {
		return e.DevLoop.Gates
	}
	return nil
}

// resolveGateLoop resolves the workspace identity, the owning repo root, and
// the repo's dev loop. ok is false — meaning "no-op" — when the workspace
// can't be resolved or the repo has no dev_loop configured (the hooks only
// enforce on repos that opted in, matching the preamble injection).
func resolveGateLoop() (loop watch.Loop, root, wsName string, ok bool) {
	wsName, repoName, repoRoot := resolveWorkspaceIdent()
	if wsName == "" {
		return watch.Loop{}, "", "", false
	}
	root, ok = resolveGateRepoRoot(repoName, repoRoot, wsName)
	if !ok {
		return watch.Loop{}, "", "", false
	}
	cfg, _ := config.Load(root)
	if !watch.IsConfigured(cfg) {
		return watch.Loop{}, "", "", false
	}
	return watch.Resolve(cfg), root, wsName, true
}

// resolveGateRepoRoot resolves the concrete repo root that owns wsName. It
// prefers an explicit repoRoot; otherwise it falls back to a basename match
// among known repos, preferring one that actually contains the workspace
// (mirrors report-status's resolution).
func resolveGateRepoRoot(repoName, repoRoot, wsName string) (string, bool) {
	if strings.TrimSpace(repoRoot) != "" {
		return repoRoot, true
	}
	if repoName == "" {
		return "", false
	}
	all, err := stateStore().LoadAll()
	if err != nil {
		return "", false
	}
	var candidates []string
	for r := range all {
		if filepath.Base(r) == repoName {
			candidates = append(candidates, r)
		}
	}
	sort.Strings(candidates)
	var matches []string
	for _, r := range candidates {
		if _, ok := all[r][wsName]; ok {
			matches = append(matches, r)
		}
	}
	switch {
	case len(matches) == 1:
		return matches[0], true
	case len(matches) == 0 && len(candidates) == 1:
		return candidates[0], true
	default:
		return "", false
	}
}

// readGatePayload reads and parses the Claude hook JSON from stdin. A missing
// or malformed payload yields a zero value — callers treat that as "nothing
// to record" rather than an error.
func readGatePayload() (gateHookPayload, error) {
	data, err := io.ReadAll(reportStatusStdin())
	if err != nil || len(data) == 0 {
		return gateHookPayload{}, err
	}
	var p gateHookPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return gateHookPayload{}, err
	}
	return p, nil
}

// stateUpdater returns the store as an updater; falls back to a no-op when the
// store doesn't support atomic Update (only the JSON store does today).
func stateUpdater() updater {
	if u, ok := stateStore().(updater); ok {
		return u
	}
	return noopUpdater{}
}

type noopUpdater struct{}

func (noopUpdater) Update(string, func(map[string]workspace.Entry) map[string]workspace.Entry) error {
	return nil
}
