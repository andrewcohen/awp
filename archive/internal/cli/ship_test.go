package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/config"
	"github.com/andrewcohen/awp/internal/watch"
	"github.com/andrewcohen/awp/internal/workspace"
)

// `awp ship`. What matters here is the wiring: the style comes from the repo's
// config and nothing else, the gates are re-read as part of shipping, and a
// conflicted rebase turns into a prompt for the workspace's agent rather than
// into a moved trunk.

// shipRunner answers the two jj reads ship does before shipping, records
// everything, and can be told to report the rebase as conflicted.
type shipRunner struct {
	calls    [][]string
	empty    bool
	desc     string
	trunk    string
	conflict bool
	noTrunk  bool
}

func (r *shipRunner) Run(_ context.Context, dir string, name string, args ...string) (string, error) {
	r.calls = append(r.calls, append([]string{name, "in:" + dir}, args...))
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "trunk()"):
		if r.noTrunk {
			return "\n", nil
		}
		return r.trunk + "\n", nil
	case strings.Contains(joined, "if(empty"):
		state := "nonempty"
		if r.empty {
			state = "empty"
		}
		return "abc12345\t" + state + "\t" + r.desc + "\n", nil
	case strings.Contains(joined, "if(conflict"):
		if r.conflict {
			return "conflict\n", nil
		}
		return "clean\n", nil
	}
	return "", nil
}

func (r *shipRunner) ran(substr string) bool {
	for _, c := range r.calls {
		if strings.Contains(strings.Join(c, " "), substr) {
			return true
		}
	}
	return false
}

// shipApp builds an App standing in a workspace of a repo whose config says
// what shipping means there.
func shipApp(t *testing.T, runner Runner, shipStyle string) (*App, *bytes.Buffer, string) {
	t.Helper()
	repo := t.TempDir()
	ws := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AWP_WORKSPACE", "feature")
	t.Setenv("AWP_REPO", "repo")
	t.Setenv("AWP_REPO_ROOT", repo)
	t.Setenv("TMUX", "")
	if shipStyle != "" {
		writeShipConfig(t, repo, shipStyle)
	}
	out := &bytes.Buffer{}
	svc := &fakeService{listEntries: []workspace.ListEntry{{Name: "feature", Path: ws}}}
	return &App{runner: runner, shipSvc: svc, out: out}, out, repo
}

func writeShipConfig(t *testing.T, repo, style string) {
	t.Helper()
	dir := filepath.Join(repo, ".awp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"ship": "`+style+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A repo that has not said what shipping means gets told so, and nothing runs.
// Guessing a style for it is the wrong-by-default the verb exists to prevent.
func TestShipRefusesWhenTheRepoHasNotSaidWhatShippingMeans(t *testing.T) {
	runner := &shipRunner{trunk: "main", desc: "feat: x"}
	app, _, _ := shipApp(t, runner, "")
	err := app.runShip(nil)
	if err == nil || !strings.Contains(err.Error(), "has not said what shipping means") {
		t.Fatalf("unset ship style: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("nothing should run before the style resolves, ran: %v", runner.calls)
	}
}

// The pull-request style is configured-but-unbuilt, and says so before checking
// anything — a repo waiting on that half should not watch its gates get read.
func TestShipSaysPullRequestStyleIsNotBuiltYet(t *testing.T) {
	runner := &shipRunner{trunk: "main", desc: "feat: x"}
	app, _, _ := shipApp(t, runner, "pull_request")
	err := app.runShip(nil)
	if err == nil || !strings.Contains(err.Error(), "not implemented yet") {
		t.Fatalf("pull_request style: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("an unbuilt style must run nothing, ran: %v", runner.calls)
	}
}

func TestShipMainStyleRebasesMovesTrunkAndReports(t *testing.T) {
	runner := &shipRunner{trunk: "main", desc: "feat: the thing"}
	app, out, repo := shipApp(t, runner, "main")
	if err := app.runShip(nil); err != nil {
		t.Fatalf("ship: %v", err)
	}
	if !runner.ran("rebase -s abc12345 -d main") {
		t.Errorf("no rebase onto trunk: %v", runner.calls)
	}
	if !runner.ran("bookmark set main -r abc12345") {
		t.Errorf("trunk was not moved: %v", runner.calls)
	}
	if !runner.ran("jj in:" + repo + " new main") {
		t.Errorf("the default workspace was not moved onto the new trunk: %v", runner.calls)
	}
	if got := out.String(); !strings.Contains(got, "shipped abc12345") || !strings.Contains(got, "onto main") {
		t.Errorf("output does not say what was shipped: %q", got)
	}
}

// The trunk bookmark comes from jj's trunk() revset, so a repo that integrates
// on something not called main is not assumed to.
func TestShipShipsOntoWhateverTrunkResolvesTo(t *testing.T) {
	runner := &shipRunner{trunk: "trunk", desc: "feat: x"}
	app, _, _ := shipApp(t, runner, "main")
	if err := app.runShip(nil); err != nil {
		t.Fatalf("ship: %v", err)
	}
	if !runner.ran("bookmark set trunk -r abc12345") {
		t.Errorf("did not ship onto the resolved trunk bookmark: %v", runner.calls)
	}
}

func TestShipRefusesWithNoTrunkBookmark(t *testing.T) {
	runner := &shipRunner{noTrunk: true, desc: "feat: x"}
	app, _, _ := shipApp(t, runner, "main")
	err := app.runShip(nil)
	if err == nil || !strings.Contains(err.Error(), "no bookmark at trunk()") {
		t.Fatalf("missing trunk bookmark: %v", err)
	}
	if runner.ran("rebase") {
		t.Errorf("rebased with nowhere to ship onto: %v", runner.calls)
	}
}

// Ship is a precondition, not an assertion: a wip: description means the work
// says of itself that it is not done.
func TestShipStopsOnAWipDescription(t *testing.T) {
	runner := &shipRunner{trunk: "main", desc: "wip: still going"}
	app, _, _ := shipApp(t, runner, "main")
	err := app.runShip(nil)
	if err == nil || !strings.Contains(err.Error(), "wip:") {
		t.Fatalf("wip description: %v", err)
	}
	if runner.ran("bookmark set") {
		t.Errorf("trunk moved onto a wip change: %v", runner.calls)
	}
}

// The main style's gate policy is stop, and it is the style that says so.
func TestMainStyleGatePolicyIsStop(t *testing.T) {
	// Red required gates are what the completion check reads, through the same
	// predicate — see redRequiredGates.
	loop := watch.Resolve(config.Config{DevLoop: struct {
		Phases []string             `json:"phases,omitempty"`
		Gates  []config.DevLoopGate `json:"gates,omitempty"`
		Nudge  string               `json:"nudge,omitempty"`
	}{Gates: []config.DevLoopGate{{Name: "test", Match: "go test"}, {Name: "lint", Match: "golangci-lint"}}}})
	red := redRequiredGates(loop, map[string]string{"test": "pass"})
	if len(red) != 1 || red[0] != "lint" {
		t.Fatalf("redRequiredGates: got %v, want [lint]", red)
	}
	// gatesAllGreen is defined in terms of it, so the two cannot disagree.
	if gatesAllGreen(loop, map[string]string{"test": "pass"}) {
		t.Error("gatesAllGreen should be false while a required gate is red")
	}
	if !gatesAllGreen(loop, map[string]string{"test": "pass", "lint": "pass"}) {
		t.Error("gatesAllGreen should be true once every required gate is green")
	}
}

// A conflicted rebase leaves trunk alone and hands the job to the workspace's
// agent, which is the same turn-into-repair `awp workspace repair` makes.
func TestShipConflictSendsTheAgentAPromptAndLeavesTrunkAlone(t *testing.T) {
	runner := &shipRunner{trunk: "main", desc: "feat: x", conflict: true}
	app, _, _ := shipApp(t, runner, "main")
	err := app.runShip(nil)
	if err == nil {
		t.Fatal("a conflicted ship should be an error: nothing was shipped")
	}
	if !strings.Contains(err.Error(), "left conflicts") || !strings.Contains(err.Error(), "not moved") {
		t.Errorf("conflict error does not say trunk stayed put: %v", err)
	}
	if runner.ran("bookmark set") {
		t.Fatalf("trunk was moved onto a conflicted revision: %v", runner.calls)
	}
	// The prompt goes out over the same tmux path repair uses.
	if !runner.ran("tmux") {
		t.Errorf("no attempt to reach the workspace's agent: %v", runner.calls)
	}
}

// Ship must not resolve the workspace through the ambient service.
//
// The ambient one is built from `jj root`, and inside a jj workspace that is the
// workspace's own directory rather than the repo it belongs to — so it derives
// the workspace base one level too deep and hands back a path like
// `…/ship-verb/ship-verb`, which does not exist. Pinned by poisoning it: a ship
// that reads it fails, and one that goes through the repo root does not.
func TestShipDoesNotResolveThroughTheAmbientService(t *testing.T) {
	runner := &shipRunner{trunk: "main", desc: "feat: x"}
	app, _, _ := shipApp(t, runner, "main")
	app.svc = &fakeService{listErr: errors.New("the ambient service was built from the workspace, not the repo")}
	if err := app.runShip(nil); err != nil {
		t.Fatalf("ship went through the ambient service: %v", err)
	}
	if !runner.ran("bookmark set main -r abc12345") {
		t.Errorf("trunk was not moved: %v", runner.calls)
	}
}

func TestShipDryRunChangesNothing(t *testing.T) {
	runner := &shipRunner{trunk: "main", desc: "feat: x"}
	app, out, _ := shipApp(t, runner, "main")
	if err := app.runShip([]string{"--dry-run"}); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	for _, forbidden := range []string{"rebase", "bookmark set", "new main"} {
		if runner.ran(forbidden) {
			t.Errorf("--dry-run ran %q: %v", forbidden, runner.calls)
		}
	}
	if got := out.String(); !strings.Contains(got, "would ship") || !strings.Contains(got, "jj rebase -s abc12345 -d main") {
		t.Errorf("dry run does not print the plan: %q", got)
	}
}

func TestShipRefusesOutsideAWorkspace(t *testing.T) {
	runner := &shipRunner{trunk: "main"}
	app, _, _ := shipApp(t, runner, "main")
	t.Setenv("AWP_WORKSPACE", "")
	t.Setenv("AWP_REPO_ROOT", "")
	// The env is only two of three ways a workspace is identified — the third is
	// the working directory, and this repo's own checkout is a workspace, so the
	// test has to stand somewhere that is not one.
	t.Chdir(t.TempDir())
	err := app.runShip(nil)
	if err == nil || !strings.Contains(err.Error(), "not an awp workspace") {
		t.Fatalf("outside a workspace: %v", err)
	}
}

func TestShipRejectsUnknownArguments(t *testing.T) {
	runner := &shipRunner{trunk: "main"}
	app, _, _ := shipApp(t, runner, "main")
	// No workspace argument on purpose: ship is what a workspace does to its
	// own change, so a name here would be a way to ship someone else's.
	err := app.runShip([]string{"other-workspace"})
	if err == nil || !strings.Contains(err.Error(), "unexpected argument") {
		t.Fatalf("positional argument: %v", err)
	}
}
