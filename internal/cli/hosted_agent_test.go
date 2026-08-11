package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// zmxShim puts a fake `zmx` first on PATH and returns the file it records its
// argv into.
//
// A shim rather than an injected starter because the thing worth testing is the
// attach awp actually execs: StartDetached allocates a pty and runs the binary it
// finds on PATH, so a seam above that would test the wiring and skip the part
// that has to be right. It also keeps these tests off the developer's own daemon
// — see requireRealZmx in internal/zmx for what a test that drives the real one
// costs.
func zmxShim(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	argvLog := filepath.Join(dir, "argv")
	// Records what it was asked to run, then holds the pty open the way a real
	// attach client does until it is killed.
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> " + argvLog + "; done\nsleep 5\n"
	if err := os.WriteFile(filepath.Join(dir, "zmx"), []byte(script), 0o755); err != nil { //nolint:gosec // a test shim has to be executable
		t.Fatalf("write the zmx shim: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argvLog
}

// liveSessionRunner answers `zmx ls` with one live session by name, so the poll
// in StartDetached finishes without a daemon.
//
// Only once the shim has actually run, which `after` is the evidence of. A fake
// that answers immediately can report a session before the attach has been
// scheduled at all — the first exec in a process is slower than the poll interval
// — and StartDetached would then end a client that had not started anything. The
// real daemon cannot lie that way: it lists a session because zmx made one.
type liveSessionRunner struct {
	name  string
	after string
}

func (r *liveSessionRunner) Run(_ context.Context, _, name string, args ...string) (string, error) {
	if name != "zmx" || len(args) == 0 || args[0] != "ls" {
		return "", nil
	}
	if r.after != "" {
		if _, err := os.Stat(r.after); err != nil {
			return "", nil
		}
	}
	return "name=" + r.name + "\tpid=4242\tclients=1\tcreated=1786124270\n", nil
}

func shimArgv(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the attach never ran: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// TestStartingTheAgentGivesItThePrompt. The gesture is "create a workspace with
// a prompt", and until this the prompt was only parked — so nothing worked on it
// until somebody opened the pane, however much later that was. Under tmux the
// create ends by starting the agent and switching to it, which is why the same
// gesture there means the work is already under way.
func TestStartingTheAgentGivesItThePrompt(t *testing.T) {
	argvLog := zmxShim(t)
	dir := t.TempDir()
	err := startHostedAgent(&liveSessionRunner{name: "awp.repo.qa.agent", after: argvLog}, hostedAgent{
		project:   "repo",
		workspace: "qa",
		repoRoot:  dir,
		dir:       dir,
		prompt:    "fix the tests",
	}, nil)
	if err != nil {
		t.Fatalf("start the agent: %v", err)
	}
	argv := shimArgv(t, argvLog)
	if len(argv) < 2 || argv[0] != "attach" {
		t.Fatalf("the agent was started with %v, want an attach", argv)
	}
	if argv[1] != "awp.repo.qa.agent" {
		t.Errorf("started session %q, want awp.repo.qa.agent", argv[1])
	}
	// Last, as the agent's own positional argument: the session is being created,
	// so argv is the one delivery that cannot race the agent's input box.
	if argv[len(argv)-1] != "fix the tests" {
		t.Errorf("the agent's last argument is %q, want the prompt", argv[len(argv)-1])
	}
}

// TestAReviewerStartsWithoutTheDevLoopPreamble. The two flavors are not
// interchangeable and the difference is not visible in the prompt text: a
// reviewer told to work in units, run gates and commit starts doing the author's
// job on someone else's PR. The pane path makes the same distinction from the
// parked prompt's Review flag; this is the same decision one step earlier.
func TestAReviewerStartsWithoutTheDevLoopPreamble(t *testing.T) {
	repo := writeRepoConfig(t, `{
		"agent": "claude",
		"dev_loop": {"phases": ["implement"], "gates": [{"name": "test", "phase": "implement", "match": "go test"}]}
	}`)

	argvFor := func(review bool) []string {
		argvLog := zmxShim(t)
		if err := startHostedAgent(&liveSessionRunner{name: "awp.repo.qa.agent", after: argvLog}, hostedAgent{
			project:   "repo",
			workspace: "qa",
			repoRoot:  repo,
			dir:       repo,
			prompt:    "read the change",
			review:    review,
		}, nil); err != nil {
			t.Fatalf("start the agent (review=%v): %v", review, err)
		}
		return shimArgv(t, argvLog)
	}

	coding := argvFor(false)
	// The comparison only means something if the coding flavor does get a preamble
	// from this fixture — otherwise both sides are trivially equal and this would
	// pass with the distinction deleted.
	if !slicesContains(coding, appendPreambleFlag) {
		t.Fatalf("the coding agent started without a preamble (%v), so this test proves nothing", coding)
	}
	if reviewer := argvFor(true); slicesContains(reviewer, appendPreambleFlag) {
		t.Errorf("the reviewer started with the dev-loop preamble: %v", reviewer)
	}
}

// TestTheAgentStartsInTheWorkspace, not the source repo. Same rule as a pane's
// directory: an agent editing the tree every workspace shares looks entirely
// normal until you read what it wrote.
func TestTheAgentStartsInTheWorkspace(t *testing.T) {
	repo := t.TempDir()
	ws := filepath.Join(repo, "..", filepath.Base(t.TempDir()))
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatalf("make the workspace dir: %v", err)
	}
	marker := filepath.Join(ws, "cwd")
	dir := t.TempDir()
	script := "#!/bin/sh\npwd > " + marker + "\nsleep 5\n"
	if err := os.WriteFile(filepath.Join(dir, "zmx"), []byte(script), 0o755); err != nil { //nolint:gosec // a test shim has to be executable
		t.Fatalf("write the zmx shim: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := startHostedAgent(&liveSessionRunner{name: "awp.repo.qa.agent", after: marker}, hostedAgent{
		project: "repo", workspace: "qa", repoRoot: repo, dir: ws, prompt: "go",
	}, nil); err != nil {
		t.Fatalf("start the agent: %v", err)
	}
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("the attach never ran: %v", err)
	}
	if resolved, _ := filepath.EvalSymlinks(strings.TrimSpace(string(got))); resolved != mustEval(t, ws) {
		t.Errorf("the agent started in %q, want the workspace's %q", resolved, mustEval(t, ws))
	}
}

func mustEval(t *testing.T, path string) string {
	t.Helper()
	out, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve %q: %v", path, err)
	}
	return out
}

// TestStartingTheAgentRefusesADirectoryThatIsNotThere. This runs at the end of a
// create, which is exactly when a path can be predicted but not yet real — and a
// failure inside the attach would report to a pty nobody reads. The caller's
// fallback is to park the prompt, so refusing here is what keeps it deliverable.
func TestStartingTheAgentRefusesADirectoryThatIsNotThere(t *testing.T) {
	zmxShim(t)
	err := startHostedAgent(&liveSessionRunner{name: "awp.repo.qa.agent"}, hostedAgent{
		project: "repo", workspace: "qa", repoRoot: t.TempDir(),
		dir: filepath.Join(t.TempDir(), "not-created-yet"), prompt: "go",
	}, nil)
	if err == nil {
		t.Fatal("started an agent in a directory that does not exist")
	}
	for _, want := range []string{"qa", "not-created-yet"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error %q does not mention %q", err, want)
		}
	}
}

// TestStartingTheAgentNeedsAPrompt: an agent is started here because there is
// work for it. Starting an idle one per create would spend a process, and a row
// that reads as running, on a workspace nobody has asked anything of.
func TestStartingTheAgentNeedsAPrompt(t *testing.T) {
	zmxShim(t)
	dir := t.TempDir()
	if err := startHostedAgent(&liveSessionRunner{name: "awp.repo.qa.agent"}, hostedAgent{
		project: "repo", workspace: "qa", repoRoot: dir, dir: dir, prompt: "   ",
	}, nil); err == nil {
		t.Fatal("started an agent with no prompt to start it for")
	}
}

func slicesContains(haystack []string, want string) bool {
	for _, s := range haystack {
		if s == want {
			return true
		}
	}
	return false
}
