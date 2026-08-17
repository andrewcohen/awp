package ship

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeRunner records the commands a ship ran and answers the conflict query
// however the test wants.
type fakeRunner struct {
	calls     []call
	conflict  bool
	failOn    string // substring of the args that should return an error
	failErr   error
	failOut   string
	conflictQ int
}

type call struct {
	dir  string
	name string
	args []string
}

func (f *fakeRunner) Run(_ context.Context, dir string, name string, args ...string) (string, error) {
	joined := strings.Join(args, " ")
	f.calls = append(f.calls, call{dir: dir, name: name, args: args})
	if f.failOn != "" && strings.Contains(joined, f.failOn) {
		err := f.failErr
		if err == nil {
			err = errors.New("exit status 1")
		}
		return f.failOut, err
	}
	if strings.Contains(joined, "if(conflict") {
		f.conflictQ++
		if f.conflict {
			return "conflict\n", nil
		}
		return "clean\n", nil
	}
	return "", nil
}

func (f *fakeRunner) commands() []string {
	out := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		out = append(out, c.name+" "+strings.Join(c.args, " "))
	}
	return out
}

func testTarget() Target {
	return Target{
		WorkspacePath:        "/ws/feature",
		DefaultWorkspacePath: "/repo",
		Revision:             "abcdef",
		Trunk:                "main",
	}
}

func TestStyleForResolvesMainAndDistinguishesUnsetFromUnknown(t *testing.T) {
	s, err := StyleFor("main")
	if err != nil {
		t.Fatalf("main style: %v", err)
	}
	if !s.Implemented() {
		t.Fatal("the main style should be implemented")
	}
	if s.GatePolicy != PolicyStop {
		t.Fatalf("main gate policy: got %v, want stop", s.GatePolicy)
	}

	// Unset and unknown are different answers with different fixes, so they
	// must not collapse into one error.
	_, unsetErr := StyleFor("  ")
	if unsetErr == nil || !strings.Contains(unsetErr.Error(), "has not said what shipping means") {
		t.Fatalf("unset style error: %v", unsetErr)
	}
	_, unknownErr := StyleFor("mian")
	if unknownErr == nil || !strings.Contains(unknownErr.Error(), `unknown style "mian"`) {
		t.Fatalf("unknown style error: %v", unknownErr)
	}
}

// The pull-request style is the seam: named, carrying its own gate policy, and
// refusing rather than falling back to the style that is built.
func TestPullRequestStyleIsANamedSeamNotAFallback(t *testing.T) {
	s, err := StyleFor(StylePullRequest)
	if err != nil {
		t.Fatalf("pull_request style: %v", err)
	}
	if s.Implemented() {
		t.Fatal("pull_request is not built yet; Implemented should be false")
	}
	if s.GatePolicy != PolicyReportAndAllow {
		t.Fatalf("pull_request gate policy: got %v, want report-and-allow", s.GatePolicy)
	}
	runner := &fakeRunner{}
	if _, err := s.Run(runner, testTarget(), nil); err == nil || !strings.Contains(err.Error(), "not implemented yet") {
		t.Fatalf("running an unbuilt style: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("an unbuilt style must run nothing, ran: %v", runner.commands())
	}
}

func TestMainStyleRebasesMovesTrunkThenMovesTheDefaultWorkspace(t *testing.T) {
	runner := &fakeRunner{}
	s, err := StyleFor(StyleMain)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Run(runner, testTarget(), nil)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	got := runner.commands()
	want := []string{
		"jj rebase -s abcdef -d main",
		`jj --ignore-working-copy log --no-graph -r abcdef -T if(conflict, "conflict", "clean")`,
		"jj bookmark set main -r abcdef",
		"jj new main",
	}
	if len(got) != len(want) {
		t.Fatalf("commands: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("command %d: got %q, want %q", i, got[i], want[i])
		}
	}
	// The last move belongs to the default workspace, not the shipping one —
	// pointing it at the workspace would move the wrong working copy.
	if dir := runner.calls[3].dir; dir != "/repo" {
		t.Errorf("`jj new main` ran in %q, want the default workspace /repo", dir)
	}
	for _, c := range runner.calls[:3] {
		if c.dir != "/ws/feature" {
			t.Errorf("%v ran in %q, want the shipping workspace", c.args, c.dir)
		}
	}
	if len(res.Steps) != 3 {
		t.Errorf("reported steps: got %v", res.Steps)
	}
}

// Conflicts stop before the bookmark moves. This is the ordering the style
// exists to guarantee: a conflicted revision must never be what trunk points at.
func TestMainStyleLeavesTrunkAloneWhenTheRebaseConflicts(t *testing.T) {
	runner := &fakeRunner{conflict: true}
	s, _ := StyleFor(StyleMain)
	_, err := s.Run(runner, testTarget(), nil)
	if !errors.Is(err, ErrConflicts) {
		t.Fatalf("conflicted rebase: got %v, want ErrConflicts", err)
	}
	for _, c := range runner.commands() {
		if strings.Contains(c, "bookmark set") {
			t.Fatalf("trunk was moved onto a conflicted revision: %v", runner.commands())
		}
		if strings.Contains(c, "jj new") {
			t.Fatalf("the default workspace was moved after a conflicted rebase: %v", runner.commands())
		}
	}
}

func TestMainStyleReportsWhichStepFailed(t *testing.T) {
	runner := &fakeRunner{failOn: "bookmark set", failOut: "nothing to do"}
	s, _ := StyleFor(StyleMain)
	if _, err := s.Run(runner, testTarget(), nil); err == nil || !strings.Contains(err.Error(), "move main onto abcdef") {
		t.Fatalf("failed bookmark move: %v", err)
	}
}

func TestGateConditionNamesRedGatesEmptyAndWipDescriptions(t *testing.T) {
	if c := GateCondition(nil, false, "ship: the thing"); !c.Shippable() {
		t.Fatalf("a green, described, non-empty change should be shippable: %v", c.Summary())
	}

	c := GateCondition([]string{"test", "lint"}, false, "feat: x")
	if c.Shippable() || !c.Has(BlockerGateRed) {
		t.Fatalf("red gates should block: %+v", c)
	}
	if !strings.Contains(c.Summary(), "test, lint") {
		t.Errorf("summary should name the gates: %q", c.Summary())
	}

	if c := GateCondition(nil, true, "feat: x"); !c.Has(BlockerEmpty) {
		t.Error("an empty revision should block")
	}
	if c := GateCondition(nil, false, "  "); !c.Has(BlockerNoDescription) {
		t.Error("a missing description should block")
	}
	// The dev loop writes `wip:` while work is in progress, so it is a
	// description that means the opposite of done.
	if c := GateCondition(nil, false, "WIP: still going"); !c.Has(BlockerNoDescription) {
		t.Error("a wip: description should block")
	}
}

// recordingReporter is the deck's progress modal, standing in.
type recordingReporter struct{ steps []string }

func (r *recordingReporter) Step(s string) { r.steps = append(r.steps, s) }
func (r *recordingReporter) Log(string)    {}

// Each move announces itself before it runs. The deck's progress modal is the
// caller that needs this: a rebase against a large repo takes long enough that a
// modal saying nothing reads as a hang.
func TestMainStyleNarratesEachMoveBeforeItRuns(t *testing.T) {
	rep := &recordingReporter{}
	s, _ := StyleFor(StyleMain)
	if _, err := s.Run(&fakeRunner{}, testTarget(), rep); err != nil {
		t.Fatalf("ship: %v", err)
	}
	want := []string{"Rebase abcdef onto main", "Move main onto abcdef", "Move the default workspace onto main"}
	if len(rep.steps) != len(want) {
		t.Fatalf("steps: got %v, want %v", rep.steps, want)
	}
	for i := range want {
		if rep.steps[i] != want[i] {
			t.Errorf("step %d: got %q, want %q", i, rep.steps[i], want[i])
		}
	}
}

// A nil reporter is the CLI verb, which prints the steps once at the end.
func TestANilReporterIsFine(t *testing.T) {
	s, _ := StyleFor(StyleMain)
	if _, err := s.Run(&fakeRunner{}, testTarget(), nil); err != nil {
		t.Fatalf("ship with no reporter: %v", err)
	}
}

func TestConflictPromptTellsTheAgentTrunkDidNotMove(t *testing.T) {
	got := ConflictPrompt(testTarget())
	for _, want := range []string{"abcdef", "main", "was not moved", "awp ship"} {
		if !strings.Contains(got, want) {
			t.Errorf("conflict prompt missing %q in: %s", want, got)
		}
	}
}
