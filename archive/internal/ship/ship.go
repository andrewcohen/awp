// Package ship holds what a repository does with a finished change.
//
// The verb is one word — `awp ship` — because the agent that finished the work
// should not have to know which convention the repository follows. It knows the
// change is done; the repo knows what done means. That answer is config
// (`"ship": "main"`), and this package is where a style name becomes steps.
//
// Two styles, one of which exists:
//
//	main          rebase the change onto the trunk bookmark, move the bookmark
//	              onto it, and move the default workspace on. Local only —
//	              nothing is pushed, so a wrong ship is one `jj bookmark set`
//	              from undone.
//	pull_request  push the bookmark, open or update the PR. Named here and not
//	              implemented, so a repo that says it gets told so rather than
//	              silently getting the other one.
//
// Gate policy belongs to the style, not to the caller. The two styles disagree
// about what a red gate means — pushing a branch with failing CI so a human can
// look at it is legitimate, writing trunk with failing tests is not — so the
// policy sits in the same table as the steps and cannot be set by whoever calls
// ship.
package ship

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Runner is the command runner, matching internal/cli's.
type Runner interface {
	Run(ctx context.Context, dir string, name string, args ...string) (string, error)
}

// Style names, as spelled in a repo's `"ship"` config field.
const (
	StyleMain        = "main"
	StylePullRequest = "pull_request"
)

// GatePolicy is what a style does about a change that is not in shippable
// condition.
type GatePolicy int

const (
	// PolicyStop refuses to ship. The effect is not retractable enough to be
	// worth a loud warning instead — trunk with failing tests on it is
	// everyone's problem, not just the shipper's.
	PolicyStop GatePolicy = iota
	// PolicyReportAndAllow ships anyway, having said what is red. A branch
	// pushed with a red gate so a person can look at it is a real workflow.
	PolicyReportAndAllow
)

func (p GatePolicy) String() string {
	if p == PolicyReportAndAllow {
		return "report-and-allow"
	}
	return "stop"
}

// BlockerKind names one way a change can fail to be in shippable condition.
//
// Kinds rather than sentences because two different callers render them
// differently: `ship` prints them, and a conflict turns into a prompt for the
// workspace's agent. A pre-rendered string would force the prompt to
// re-derive what it was about.
type BlockerKind string

const (
	// BlockerGateRed: a required dev_loop gate is not green. The condition
	// `awp gate` records and `awp watch` renders — ship reads the same
	// records rather than growing a second idea of "checks passed".
	BlockerGateRed BlockerKind = "gate-red"
	// BlockerEmpty: nothing to ship. An empty revision reaching trunk is a
	// no-op commit in the history and almost always means ship was pointed at
	// the wrong revision.
	BlockerEmpty BlockerKind = "empty"
	// BlockerNoDescription: the revision has no description, or still carries
	// the `wip:` marker the dev loop writes while work is in progress.
	BlockerNoDescription BlockerKind = "no-description"
	// BlockerConflicts: the rebase onto trunk left conflicts. Unlike the
	// others this one is only knowable by attempting the rebase, so it is
	// found mid-ship rather than before it.
	BlockerConflicts BlockerKind = "conflicts"
)

// Blocker is one reason a change is not in shippable condition.
type Blocker struct {
	Kind BlockerKind
	// Label is the human-facing phrase, in the same terms `awp workspace
	// repair` reports a PR's problems: a noun phrase naming what is wrong.
	Label string
}

// Condition is everything known to be wrong with a change, in the order it
// should be reported.
type Condition struct {
	Blockers []Blocker
}

// Shippable reports whether nothing is wrong. Note that a style's gate policy
// decides what to do about a false — it does not decide the answer.
func (c Condition) Shippable() bool { return len(c.Blockers) == 0 }

// Has reports whether a blocker of that kind is present.
func (c Condition) Has(kind BlockerKind) bool {
	for _, b := range c.Blockers {
		if b.Kind == kind {
			return true
		}
	}
	return false
}

// Summary is the blockers as one sentence, for a status line or an error.
func (c Condition) Summary() string {
	labels := make([]string, 0, len(c.Blockers))
	for _, b := range c.Blockers {
		labels = append(labels, b.Label)
	}
	return strings.Join(labels, "; ")
}

// GateCondition assesses a change from what is knowable before touching the
// repository: the dev loop's recorded gates, and whether the revision is
// something worth putting on a trunk at all.
//
// redGates are the required gates that are not green, in loop order — the
// caller reads them from the records `awp gate record` keeps, so ship and the
// task-completion check answer "are the checks green" the same way.
func GateCondition(redGates []string, empty bool, description string) Condition {
	var c Condition
	if len(redGates) > 0 {
		c.Blockers = append(c.Blockers, Blocker{
			Kind:  BlockerGateRed,
			Label: "gates that have not passed: " + strings.Join(redGates, ", "),
		})
	}
	if empty {
		c.Blockers = append(c.Blockers, Blocker{Kind: BlockerEmpty, Label: "an empty revision, so there is nothing to ship"})
	}
	desc := strings.TrimSpace(description)
	switch {
	case desc == "":
		c.Blockers = append(c.Blockers, Blocker{Kind: BlockerNoDescription, Label: "no commit description"})
	case strings.HasPrefix(strings.ToLower(desc), "wip:"):
		c.Blockers = append(c.Blockers, Blocker{Kind: BlockerNoDescription, Label: "a description still marked wip:"})
	}
	return c
}

// Target is the change being shipped and where it lives.
type Target struct {
	// WorkspacePath is the directory the change's own jj commands run in.
	WorkspacePath string
	// DefaultWorkspacePath is the repo root — the default workspace, which the
	// main style moves onto the new trunk.
	DefaultWorkspacePath string
	// Revision is the change to ship, as a jj revset (a change id in practice).
	Revision string
	// Trunk is the bookmark the main style moves. Resolved by the caller from
	// jj's own `trunk()` revset so a repo with a differently-named integration
	// branch is not assumed to call it main.
	Trunk string
}

// Result is what a ship did, for reporting.
type Result struct {
	Style string
	// Steps are the commands run, in order, in the form a person could retype.
	Steps []string
}

// ErrConflicts is returned when the rebase onto trunk left conflicts.
//
// A distinct error rather than a message because the caller does something with
// it beyond printing: conflicts turn into the repair path, where the workspace's
// agent is asked to resolve them. Trunk is deliberately left where it was.
var ErrConflicts = errors.New("the rebase onto trunk left conflicts")

// Reporter narrates a ship's steps as they happen.
//
// The deck's progress modal wants to say which of the three moves is under way
// — a rebase against a large repo is long enough that a frozen modal reads as a
// hang — while the CLI verb prints the steps once at the end. Both go through
// this; nil is fine and means nobody is listening.
type Reporter interface {
	Step(string)
	Log(string)
}

func step(r Reporter, s string) {
	if r != nil {
		r.Step(s)
	}
}

// Style is one repository convention: what shipping does, and what it does
// about gates that have not passed.
type Style struct {
	Name       string
	GatePolicy GatePolicy
	// run performs the ship. nil marks a style that is named but not built —
	// the seam the pull-request style lands in.
	run func(runner Runner, t Target, rep Reporter) (Result, error)
}

// Implemented reports whether the style can actually ship.
func (s Style) Implemented() bool { return s.run != nil }

// Run performs the ship, narrating its steps to rep (which may be nil).
func (s Style) Run(runner Runner, t Target, rep Reporter) (Result, error) {
	if s.run == nil {
		return Result{}, fmt.Errorf("ship: the %q style is not implemented yet — this repo's config says %q, and only %q is built", s.Name, s.Name, StyleMain)
	}
	return s.run(runner, t, rep)
}

// styles is the whole table. A new style is an entry here and nothing else.
var styles = map[string]Style{
	StyleMain: {
		Name:       StyleMain,
		GatePolicy: PolicyStop,
		run:        shipToMain,
	},
	StylePullRequest: {
		Name: StylePullRequest,
		// Report-and-allow, decided with the steps rather than when the steps
		// are written: a branch with a red check pushed for a human to read is
		// a thing people do on purpose.
		GatePolicy: PolicyReportAndAllow,
		run:        nil,
	},
}

// StyleFor resolves a repo's configured style name.
//
// An unset name is its own error, and a different one from an unknown name.
// Unset means the repo has not said what shipping means there, and the fix is
// to say; unknown means it said something misspelled.
func StyleFor(name string) (Style, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return Style{}, fmt.Errorf(`ship: this repo has not said what shipping means — set "ship" in .awp/config.json to %q (rebase onto trunk and move it) or %q (push the bookmark and open a PR)`, StyleMain, StylePullRequest)
	}
	s, ok := styles[trimmed]
	if !ok {
		return Style{}, fmt.Errorf("ship: unknown style %q — the styles are %q and %q", trimmed, StyleMain, StylePullRequest)
	}
	return s, nil
}

// shipToMain lands a change on the trunk bookmark, locally.
//
// Three moves, in an order chosen so a failure leaves something recoverable:
// rebase first, then check for conflicts, and only move the bookmark once the
// rebase came out clean. Moving the bookmark first would put a conflicted
// revision on trunk, which is the one state here that other workspaces would
// pick up.
//
// Nothing is pushed. That is the whole reason this style is the one built
// first: every step is undoable from the local repo, so a wrong ship costs a
// `jj bookmark set` rather than a force-push and an apology.
func shipToMain(runner Runner, t Target, rep Reporter) (Result, error) {
	res := Result{Style: StyleMain}
	ctx := context.Background()
	step(rep, fmt.Sprintf("Rebase %s onto %s", t.Revision, t.Trunk))

	// -s rather than -r: the change's descendants come with it. A workspace
	// commonly has an empty working copy sitting on top of the described
	// commit, and leaving it behind on the old base would strand it.
	rebase := []string{"rebase", "-s", t.Revision, "-d", t.Trunk}
	if out, err := runner.Run(ctx, t.WorkspacePath, "jj", rebase...); err != nil {
		return res, fmt.Errorf("rebase %s onto %s: %w: %s", t.Revision, t.Trunk, err, strings.TrimSpace(out))
	}
	res.Steps = append(res.Steps, "jj "+strings.Join(rebase, " "))

	conflicted, err := hasConflict(runner, t.WorkspacePath, t.Revision)
	if err != nil {
		return res, err
	}
	if conflicted {
		// The rebase stays. The conflicts have to be resolved on the rebased
		// revision, so undoing it would throw away the only thing that
		// records what conflicts with what.
		return res, ErrConflicts
	}

	step(rep, fmt.Sprintf("Move %s onto %s", t.Trunk, t.Revision))
	setBookmark := []string{"bookmark", "set", t.Trunk, "-r", t.Revision}
	if out, err := runner.Run(ctx, t.WorkspacePath, "jj", setBookmark...); err != nil {
		return res, fmt.Errorf("move %s onto %s: %w: %s", t.Trunk, t.Revision, err, strings.TrimSpace(out))
	}
	res.Steps = append(res.Steps, "jj "+strings.Join(setBookmark, " "))

	// The default workspace is where the next piece of work starts from, so it
	// moves onto the new trunk rather than being left on the old one — a stale
	// default workspace is how the next change gets based on yesterday's trunk.
	step(rep, "Move the default workspace onto "+t.Trunk)
	newOnTrunk := []string{"new", t.Trunk}
	if out, err := runner.Run(ctx, t.DefaultWorkspacePath, "jj", newOnTrunk...); err != nil {
		return res, fmt.Errorf("move the default workspace onto %s: %w: %s", t.Trunk, err, strings.TrimSpace(out))
	}
	res.Steps = append(res.Steps, "jj "+strings.Join(newOnTrunk, " ")+"  (in the default workspace)")

	return res, nil
}

// hasConflict asks jj whether a revision came out of the rebase conflicted.
//
// Asked of the revision rather than parsed out of the rebase's own output: jj's
// wording about conflicts has changed across versions and is addressed to a
// person, where the template is a boolean addressed to a program.
func hasConflict(runner Runner, dir, revision string) (bool, error) {
	out, err := runner.Run(context.Background(), dir, "jj", "--ignore-working-copy", "log", "--no-graph", "-r", revision, "-T", `if(conflict, "conflict", "clean")`)
	if err != nil {
		return false, fmt.Errorf("check %s for conflicts: %w: %s", revision, err, strings.TrimSpace(out))
	}
	return strings.Contains(out, "conflict"), nil
}

// ConflictPrompt is what the workspace's agent is told when a ship hits
// conflicts.
//
// Ship turning into the repair path is the same move `awp workspace repair`
// makes for a PR, and for the same reason: the useful output of a blocked
// action is not an error message to the person who typed it, it is a job for
// the agent standing in the workspace. Built here beside the steps that
// produce the conflict so the two cannot drift.
func ConflictPrompt(t Target) string {
	return fmt.Sprintf("`awp ship` rebased %s onto %s and the rebase left conflicts, so %s was not moved and nothing has been shipped. "+
		"The rebase is still in place — resolve the conflicts on %s (`jj status` names the conflicted files, `jj resolve --list` lists them), "+
		"make sure the gates still pass afterwards, then run `awp ship` again.",
		t.Revision, t.Trunk, t.Trunk, t.Revision)
}
