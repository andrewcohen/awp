// Package watch turns a running agent's Claude Code transcript into a
// combined view of task progress: the agent's todo list (breadth — which
// unit of work) coupled with its position in the project's development loop
// (depth — where in fixture→test→implement→gates→commit the current unit
// is, and whether it is thrashing on a gate). It is read-only: awp observes
// the transcript, it does not run gates or steer the agent.
package watch

import (
	"regexp"
	"strings"

	"github.com/andrewcohen/awp/internal/config"
)

// Gate is a named check awp recognizes in the transcript, tied to the phase
// it belongs to. Match is tested against the bash command the agent ran.
type Gate struct {
	Name  string
	Phase string
	// Command is the human-facing command shown in the generated preamble
	// (falls back to the first alternative of the match regex when empty).
	Command string
	// Marker gates detect a phase transition (e.g. commit) but have no
	// pass/fail outcome and are excluded from the gate-lights row.
	Marker bool
	re     *regexp.Regexp
	notRe  *regexp.Regexp
}

// DisplayCommand returns the human-facing command for this gate: the
// configured Command, falling back to the first alternative of the match
// pattern (a readable stand-in for what the gate detects). Used in the
// preamble and the completion-gate deny reason so both quote the same
// invocation the agent should run.
func (g Gate) DisplayCommand() string {
	if strings.TrimSpace(g.Command) != "" {
		return g.Command
	}
	if g.re != nil {
		return firstAlt(g.re.String())
	}
	return g.Name
}

// Matches reports whether the given bash command invokes this gate — it must
// match the gate's pattern and not match its exclude pattern (if any).
func (g Gate) Matches(command string) bool {
	if g.re == nil || !g.re.MatchString(command) {
		return false
	}
	if g.notRe != nil && g.notRe.MatchString(command) {
		return false
	}
	return true
}

// Loop is a resolved development loop: an ordered list of phases and the
// gates that awp watches for.
type Loop struct {
	Phases []string
	Gates  []Gate
}

// DefaultLoop is the inferred loop used when a project's config carries no
// dev_loop section. It encodes the generic Go "validation before handoff"
// gate list (gofmt, vet, lint, test, build) plus a commit phase, arranged
// as explore → implement → verify → commit.
func DefaultLoop() Loop {
	return Loop{
		Phases: []string{"explore", "implement", "verify", "commit"},
		Gates: compile([]config.DevLoopGate{
			{Name: "fmt", Phase: "verify", Match: `gofmt|go fmt`},
			{Name: "vet", Phase: "verify", Match: `go vet`},
			{Name: "lint", Phase: "verify", Match: `golangci-lint|golint`},
			{Name: "build", Phase: "verify", Match: `go build`},
			{Name: "test", Phase: "verify", Match: `go test`},
			{Name: "commit", Phase: "commit", Match: `jj (commit|describe|squash)|jj git push|git commit`, NotMatch: `wip:`, Marker: true},
		}),
	}
}

// IsConfigured reports whether the project has an explicit dev_loop
// definition (vs. falling back to the inferred DefaultLoop).
func IsConfigured(cfg config.Config) bool {
	return len(cfg.DevLoop.Gates) > 0 || len(cfg.DevLoop.Phases) > 0
}

// Resolve turns a project config into a Loop, falling back to DefaultLoop
// when the config carries no dev_loop definition. A config that sets gates
// but no phases still gets the default phase order.
func Resolve(cfg config.Config) Loop {
	if !IsConfigured(cfg) {
		return DefaultLoop()
	}
	loop := Loop{Phases: cfg.DevLoop.Phases, Gates: compile(cfg.DevLoop.Gates)}
	if len(loop.Phases) == 0 {
		loop.Phases = DefaultLoop().Phases
	}
	if len(loop.Gates) == 0 {
		loop.Gates = DefaultLoop().Gates
	}
	return loop
}

// MatchGate returns the first gate whose command pattern matches the bash
// command, or nil if none do. It is the exported entry point the `awp gate`
// hooks use to map a run command onto a named gate.
func (l Loop) MatchGate(command string) *Gate { return l.gateFor(command) }

// GateNames returns the names of the loop's non-marker gates in loop order —
// the set the completion check requires green. Marker gates (e.g. commit)
// have no pass/fail outcome and are excluded.
func (l Loop) GateNames() []string {
	out := make([]string, 0, len(l.Gates))
	for _, g := range l.Gates {
		if g.Marker {
			continue
		}
		out = append(out, g.Name)
	}
	return out
}

// gateFor returns the gate whose command pattern matches the bash command,
// or nil if none do.
func (l Loop) gateFor(command string) *Gate {
	for i := range l.Gates {
		if l.Gates[i].Matches(command) {
			return &l.Gates[i]
		}
	}
	return nil
}

// PhaseForTool derives the dev-loop phase a tool invocation implies and
// whether it marks implementation as started for the current unit. It returns
// ("", started) when the tool implies no phase change. command is the Bash
// command (empty for non-Bash tools). Shared by the transcript scan
// (handleToolUse) and the PostToolUse phase hook so the two agree on how a tool
// maps to a phase — one source of truth, no drift.
func (l Loop) PhaseForTool(tool, command string, started bool) (phase string, nowStarted bool) {
	set := func(p string) string {
		if l.hasPhase(p) {
			return p
		}
		return ""
	}
	switch tool {
	case "Bash":
		if g := l.gateFor(command); g != nil {
			return set(g.Phase), true
		}
		if !started && isExploreCommand(command) {
			return set("explore"), started
		}
	case "Edit", "Write", "MultiEdit":
		// Any edit — including a test file — is implementation work; there's
		// no separate test phase.
		return set("implement"), true
	case "Read", "Grep", "Glob", "LS", "NotebookRead":
		// Read-only investigation counts as exploration only until the agent
		// starts implementing; reads mid-implementation don't regress the phase.
		if !started {
			return set("explore"), started
		}
	}
	return "", started
}

// hasPhase reports whether name is one of the loop's phases.
func (l Loop) hasPhase(name string) bool {
	for _, p := range l.Phases {
		if p == name {
			return true
		}
	}
	return false
}

func compile(specs []config.DevLoopGate) []Gate {
	out := make([]Gate, 0, len(specs))
	for _, s := range specs {
		re, err := regexp.Compile(s.Match)
		if err != nil {
			// A bad pattern in config shouldn't kill the view; skip it.
			continue
		}
		var notRe *regexp.Regexp
		if s.NotMatch != "" {
			// A bad exclude pattern just means "no exclusion", not a dead gate.
			notRe, _ = regexp.Compile(s.NotMatch)
		}
		out = append(out, Gate{Name: s.Name, Phase: s.Phase, Command: s.Command, Marker: s.Marker, re: re, notRe: notRe})
	}
	return out
}
