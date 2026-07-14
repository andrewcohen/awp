package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/andrewcohen/awp/internal/config"
	"github.com/andrewcohen/awp/internal/watch"
	"github.com/andrewcohen/awp/internal/workspace"
)

// ErrGateBlocked signals that the completion gate denied a TaskUpdate. main
// maps it to exit code 2 so Claude blocks the tool call and feeds the reason
// (already written to stderr) back to the agent.
var ErrGateBlocked = errors.New("gate blocked")

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
		return runGateCheck(args[1:], a.out, os.Stderr)
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

// gateHookPayload is the subset of a Claude hook payload the gate commands
// read. hook_event_name is the verdict source for `gate record`: Claude fires
// PostToolUse only after a tool *succeeds* and PostToolUseFailure after it
// *fails*, so the event itself — not any exit-code field — is the reliable
// pass/fail signal. (The Bash tool_response carries no exit status on the
// builds we tested, which is why reading it was unreliable.)
type gateHookPayload struct {
	HookEventName string          `json:"hook_event_name"`
	ToolName      string          `json:"tool_name"`
	ToolInput     json.RawMessage `json:"tool_input"`
}

// bashCommand pulls the shell command out of a Bash tool_input payload.
func (p gateHookPayload) bashCommand() string {
	var in struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(p.ToolInput, &in)
	return in.Command
}

// gateVerdict resolves pass/fail for a gate command. The explicit --result
// flag wins (that's what the hook commands pass); otherwise the payload's
// hook_event_name decides (PostToolUseFailure → fail). Anything else defaults
// to "pass" — a matched gate command that completed via the success event.
func gateVerdict(resultFlag, hookEvent string) string {
	switch strings.ToLower(strings.TrimSpace(resultFlag)) {
	case "fail":
		return "fail"
	case "pass":
		return "pass"
	}
	if hookEvent == "PostToolUseFailure" {
		return "fail"
	}
	return "pass"
}

// runGateRecord implements `awp gate record`: the PostToolUse(Bash) and
// PostToolUseFailure(Bash) hook. It matches the run command against the repo's
// dev_loop gates and records pass/fail into the in-progress unit's snapshot.
// The verdict comes from which event fired (PostToolUse → pass,
// PostToolUseFailure → fail), passed explicitly via --result on the hook
// command with the hook_event_name payload field as a fallback. It always
// exits 0 so a misconfigured hook never breaks an agent turn; on a gate
// transition it may print a PostToolUse nudge (rung 2) to stdout.
func runGateRecord(args []string, out io.Writer) error {
	jsonOut := false
	resultFlag := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json":
			jsonOut = true
		case arg == "--result":
			if i+1 >= len(args) {
				return fmt.Errorf("--result requires a value")
			}
			resultFlag = args[i+1]
			i++
		case strings.HasPrefix(arg, "--result="):
			resultFlag = strings.TrimPrefix(arg, "--result=")
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
	result := gateVerdict(resultFlag, payload.HookEventName)
	report.matched = gate.Name
	report.Result = result

	// Read the current snapshot first and only write when this gate's result
	// actually changes. store.Update rewrites the whole state file (and trips
	// the deck's fsnotify watcher → a refresh) unconditionally, so recording
	// an unchanged result — e.g. an agent re-running a passing `test` — would
	// otherwise churn the file and add flock contention across every hook
	// process.
	//
	// We record regardless of whether a unit is in progress. Agents routinely
	// run gates without ever calling TaskUpdate(in_progress) (so UnitKey stays
	// empty); gating recording on UnitKey meant those runs were silently
	// dropped and the completion check then blocked on gates that had actually
	// passed. Per-unit isolation still holds for agents that DO mark
	// in_progress: resetGateUnit clears the gate set when a new unit begins.
	snap := currentDevLoop(root, wsName)
	if snap == nil {
		snap = &workspace.DevLoopSnapshot{}
	}
	report.recorded = true
	report.Unit = snap.Task
	// A sealed unit (completed with all gates green) keeps its results only so
	// a re-marked completion stays idempotent; the first gate run afterward
	// begins a new unit, so start from an empty gate set. This resets gates
	// across a unit boundary even when the agent never marked the next unit
	// in_progress.
	existing := snap.Gates
	if snap.GatesSealed {
		existing = nil
	}
	prevResult := existing[gate.Name]
	prevReady := gatesAllGreen(loop, existing)
	newGates := make(map[string]string, len(existing)+1)
	for k, v := range existing {
		newGates[k] = v
	}
	newGates[gate.Name] = result
	nowReady := gatesAllGreen(loop, newGates)
	nudge := gateNudge(gateNudgeMode(root), gate.Name, snap.Task, result, prevResult, prevReady, nowReady, loop, newGates)
	report.UnitGates = unitGates(loop, newGates)
	report.ReadyToComplete = nowReady

	if snap.GatesSealed || prevResult != result {
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
			// Re-check the seal under the store lock: a new unit's first gate
			// clears the prior unit's results before recording this one.
			if s.GatesSealed {
				s.Gates = map[string]string{}
				s.GatesSealed = false
			}
			if s.Gates == nil {
				s.Gates = map[string]string{}
			}
			s.Gates[gate.Name] = result
			entry.DevLoop = s
			entries[name] = entry
			return entries
		})
	}
	return emitGateRecord(out, jsonOut, report, nudge)
}

// runGateCheck implements `awp gate check`. In --hook mode it is the
// PreToolUse(TaskUpdate) hook: a status of in_progress resets the unit's gate
// results (a fresh unit begins), and completed is denied unless every
// configured gate is green. Without --hook it is a self-check the agent (or a
// human) can run: exit 0 when the current unit is ready to complete, else a
// non-zero exit with an actionable reason.
func runGateCheck(args []string, out, errOut io.Writer) error {
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
		return runGateCheckHook(errOut)
	}
	return runGateCheckSelf(wsFlag, out)
}

// runGateCheckHook handles the PreToolUse(TaskUpdate) payload: reset on
// in_progress, deny-or-allow on completed. A denial is expressed by writing
// the reason to stderr and returning ErrGateBlocked, which main maps to exit
// code 2 — the exit code Claude treats as "block this tool call and feed
// stderr back to the agent". Every other path returns nil (allow); a hook
// error never wedges the agent because only exit 2 blocks.
func runGateCheckHook(errOut io.Writer) error {
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
			// Seal the unit so the next recorded gate starts a fresh set even
			// if the agent skips marking the next unit in_progress.
			sealGates(root, wsName)
			return nil // ready → allow
		}
		_, _ = fmt.Fprintln(errOut, gateDenyReason(loop, gates, currentUnitLabel(root, wsName)))
		return ErrGateBlocked
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
	// Skip the write when this unit is already active — an idempotent re-mark
	// of in_progress shouldn't rewrite the state file (and trip a deck
	// refresh) for no change.
	if cur := currentDevLoop(root, wsName); cur != nil && cur.UnitKey == taskID && taskID != "" {
		return
	}
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
		snap.GatesSealed = false
		snap.Task = strings.TrimSpace(subject)
		entry.DevLoop = snap
		entries[name] = entry
		return entries
	})
}

// sealGates marks the current unit's gates as sealed after a green completion:
// the results stay so a re-marked completion is idempotent, but the next
// recorded gate (runGateRecord) clears them as a new unit begins. Skips the
// write when there's no snapshot or it's already sealed, so an idempotent
// re-complete doesn't churn the state file.
func sealGates(root, wsName string) {
	if cur := currentDevLoop(root, wsName); cur == nil || cur.GatesSealed {
		return
	}
	_ = stateUpdater().Update(root, func(entries map[string]workspace.Entry) map[string]workspace.Entry {
		name := resolveLiveWorkspaceName(entries, wsName)
		entry, ok := entries[name]
		if !ok || entry.DevLoop == nil || entry.DevLoop.GatesSealed {
			return entries
		}
		entry.DevLoop.GatesSealed = true
		entries[name] = entry
		return entries
	})
}

// currentUnitLabel returns a quotable noun phrase for the in-progress unit,
// e.g. "unit 'prompt plumbing'", falling back to "the current unit" when the
// unit's content isn't known. The full phrase (not just the name) is returned
// so callers can drop it straight into a sentence without double-wording.
func currentUnitLabel(root, wsName string) string {
	entries, err := stateStore().Load(root)
	if err != nil {
		return "the current unit"
	}
	name := resolveLiveWorkspaceName(entries, wsName)
	if e, ok := entries[name]; ok && e.DevLoop != nil && strings.TrimSpace(e.DevLoop.Task) != "" {
		return "unit '" + e.DevLoop.Task + "'"
	}
	return "the current unit"
}

// gateDenyReason builds an actionable, quotable reason for blocking
// completion: it names the unit, the first blocking gate (a red one before a
// pending one), and the command to run. unitLabel is a full noun phrase from
// currentUnitLabel ("unit 'X'" or "the current unit").
func gateDenyReason(loop watch.Loop, gates map[string]string, unitLabel string) string {
	var pending *watch.Gate
	for i := range loop.Gates {
		g := loop.Gates[i]
		if g.Marker {
			continue
		}
		switch gates[g.Name] {
		case "fail":
			return fmt.Sprintf("%s can't be marked complete: gate '%s' is red (last run failed). Run `%s` and re-check.",
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
		return fmt.Sprintf("%s can't be marked complete: gate '%s' hasn't run yet. Run `%s` and re-check.",
			unitLabel, pending.Name, pending.DisplayCommand())
	}
	// Shouldn't reach here (caller checks gatesAllGreen first), but be safe.
	return fmt.Sprintf("%s can't be marked complete: its gates are not all green.", unitLabel)
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
	if snap := currentDevLoop(root, wsName); snap != nil {
		return snap.Gates
	}
	return nil
}

// currentDevLoop loads the workspace's persisted dev-loop snapshot fresh from
// the store, resolving a possibly-renamed workspace name. Returns nil when the
// workspace or its snapshot isn't found. Callers read only — the returned
// pointer aliases the loaded copy, so build a new map before mutating.
func currentDevLoop(root, wsName string) *workspace.DevLoopSnapshot {
	entries, err := stateStore().Load(root)
	if err != nil {
		return nil
	}
	name := resolveLiveWorkspaceName(entries, wsName)
	if e, ok := entries[name]; ok {
		return e.DevLoop
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
