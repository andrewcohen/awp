package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/review"
	"github.com/andrewcohen/awp/internal/workspace"
)

// Which review a finding went into, and saying so.
//
// The failure this guards against is silent by construction: an agent run from
// the source repo rather than from the workspace resolves to that repo's own
// review, files successfully, and the reviewer sees nothing. Seven findings on a
// real PR went that way. Nothing detected it because `review add` never named its
// destination.

// rootRunner answers `jj root` with a fixed directory, so the review store
// resolves without a real repo.
type rootRunner struct{ root string }

func (r rootRunner) Run(context.Context, string, string, ...string) (string, error) {
	return r.root + "\n", nil
}

// chdir moves the process into dir for the duration of the test. The review
// commands read the cwd to decide which workspace they are in.
func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

// tempRoot is a repo root for a review test: a temp directory with symlinks
// resolved, and a temp HOME so the review store lands under it.
//
// Symlinks resolved because the lookup that maps a directory to a workspace
// compares path prefixes: on macOS t.TempDir() hands back /var/folders/… while
// os.Getwd() reports /private/var/folders/… once you are in it, and the entry then
// matches nothing.
//
// HOME redirected because these tests drive the real command, which builds a
// review.Store with no Root and so resolves ~/.awp/reviews — writing into the
// developer's own review store. Worse, that store is keyed by the repo root's
// *basename*, and every t.TempDir() basename is "001", so two tests also shared one
// review and saw each other's comments.
func tempRoot(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolving the temp dir: %v", err)
	}
	return real
}

// The whole point of the fix: the line the agent reads back names the review it
// wrote to, so filing into the wrong one is visible in its own transcript.
func TestReviewAddNamesTheReviewItWroteTo(t *testing.T) {
	root := tempRoot(t)
	ws := filepath.Join(root, "ws", "pr-54-coworker")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	svc := &fakeService{listEntries: []workspace.ListEntry{
		{Name: "default", Path: root},
		{Name: "pr-54-coworker", Path: ws, PRNumber: 54},
	}}
	chdir(t, ws)

	var out bytes.Buffer
	err := runReviewAdd(rootRunner{root: root}, svc,
		[]string{"--file", "x.go", "--line", "12", "--body", "leaks", "--type", "suggestion"}, &out)
	if err != nil {
		t.Fatalf("review add: %v", err)
	}
	got := out.String()
	for _, want := range []string{"work-pr-54-coworker", "workspace pr-54-coworker", "x.go:12", "suggestion"} {
		if !strings.Contains(got, want) {
			t.Errorf("output does not mention %q: %q", want, got)
		}
	}
}

// The same command run from the source repo files somewhere else — which is
// correct behaviour and the reason the destination has to be said out loud.
func TestReviewAddFromTheSourceRepoNamesTheOtherReview(t *testing.T) {
	root := tempRoot(t)
	ws := filepath.Join(root, "ws", "pr-54-coworker")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	svc := &fakeService{listEntries: []workspace.ListEntry{
		{Name: "default", Path: root},
		{Name: "pr-54-coworker", Path: ws, PRNumber: 54},
	}}
	chdir(t, root)

	var out bytes.Buffer
	if err := runReviewAdd(rootRunner{root: root}, svc,
		[]string{"--file", "x.go", "--line", "12", "--body", "leaks"}, &out); err != nil {
		t.Fatalf("review add: %v", err)
	}
	if !strings.Contains(out.String(), "work-default") {
		t.Fatalf("expected the default workspace's review named, got %q", out.String())
	}

	// …and --workspace reaches the right one without moving.
	out.Reset()
	if err := runReviewAdd(rootRunner{root: root}, svc,
		[]string{"--file", "x.go", "--line", "12", "--body", "leaks", "--workspace", "pr-54-coworker"}, &out); err != nil {
		t.Fatalf("review add --workspace: %v", err)
	}
	if !strings.Contains(out.String(), "work-pr-54-coworker") {
		t.Fatalf("--workspace did not retarget the review: %q", out.String())
	}
}

// A --workspace that matches nothing is an error, not a new empty review under
// the misspelling — that would be the same silent loss the flag exists to stop.
func TestReviewAddRejectsAnUnknownWorkspace(t *testing.T) {
	root := tempRoot(t)
	svc := &fakeService{listEntries: []workspace.ListEntry{
		{Name: "default", Path: root},
		{Name: "pr-54-coworker", Path: filepath.Join(root, "ws", "pr-54-coworker")},
	}}
	chdir(t, root)

	var out bytes.Buffer
	err := runReviewAdd(rootRunner{root: root}, svc,
		[]string{"--file", "x.go", "--line", "12", "--body", "leaks", "--workspace", "pr-54-cowarker"}, &out)
	if err == nil {
		t.Fatal("expected an unknown workspace refused")
	}
	// The known names are listed, because the usual cause is a typo and the fix is
	// then visible in the error itself.
	for _, want := range []string{"pr-54-cowarker", "pr-54-coworker", "default"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

// `review list` names the review before listing it, including when it is empty:
// "no findings" from the wrong review is the reading that sends someone looking
// for a bug in the store.
func TestReviewListNamesTheReviewEvenWhenEmpty(t *testing.T) {
	root := tempRoot(t)
	svc := &fakeService{listEntries: []workspace.ListEntry{{Name: "default", Path: root}}}
	chdir(t, root)

	var out bytes.Buffer
	if err := runReviewList(rootRunner{root: root}, svc, nil, &out); err != nil {
		t.Fatalf("review list: %v", err)
	}
	if !strings.Contains(out.String(), "work-default") || !strings.Contains(out.String(), "no findings") {
		t.Fatalf("expected the review named above an empty list, got %q", out.String())
	}
}

// --json is the machine channel and stays a bare array, so a caller parsing it
// does not have to cope with a header line.
func TestReviewListJSONStaysABareArray(t *testing.T) {
	root := tempRoot(t)
	svc := &fakeService{listEntries: []workspace.ListEntry{{Name: "default", Path: root}}}
	chdir(t, root)

	var out bytes.Buffer
	if err := runReviewList(rootRunner{root: root}, svc, []string{"--json"}, &out); err != nil {
		t.Fatalf("review list --json: %v", err)
	}
	if trimmed := strings.TrimSpace(out.String()); !strings.HasPrefix(trimmed, "[") && trimmed != "null" {
		t.Fatalf("expected JSON with no header, got %q", out.String())
	}
}

func TestResolveReviewWorkspace(t *testing.T) {
	root := tempRoot(t)
	ws := filepath.Join(root, "ws", "feature")
	svc := &fakeService{listEntries: []workspace.ListEntry{
		{Name: "default", Path: root},
		{Name: "feature", Path: ws, PRNumber: 430},
	}}

	// No name: the workspace containing the directory, with its pin carried along —
	// publish reads the PR off this entry.
	got, err := resolveReviewWorkspace(svc, filepath.Join(ws, "internal"), "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Name != "feature" || got.PRNumber != 430 {
		t.Fatalf("expected the containing workspace with its pin, got %+v", got)
	}

	// An explicit name wins over the directory.
	if got, err = resolveReviewWorkspace(svc, root, "feature"); err != nil || got.Name != "feature" {
		t.Fatalf("expected --workspace to win, got %+v (%v)", got, err)
	}

	// With no service there is nothing to check the name against, and the caller was
	// explicit — so it passes through rather than failing.
	if got, err = resolveReviewWorkspace(nil, root, "feature"); err != nil || got.Name != "feature" {
		t.Fatalf("expected the name passed through with no service, got %+v (%v)", got, err)
	}
}

// The directory whose commit the comments were read against follows the review,
// not the process — otherwise publishing another workspace's review from the
// source repo would read the wrong head.
func TestReviewScopeDirFollowsTheWorkspace(t *testing.T) {
	scoped := reviewScope{entry: workspace.ListEntry{Name: "feature", Path: "/repo/ws/feature"}}
	if got := scoped.dir("/repo"); got != "/repo/ws/feature" {
		t.Errorf("expected the workspace's directory, got %q", got)
	}
	// A workspace awp does not know has no path, and the process's own directory is
	// then the right answer — which is what it was before any of this.
	unknown := reviewScope{}
	if got := unknown.dir("/repo/elsewhere"); got != "/repo/elsewhere" {
		t.Errorf("expected the cwd fallback, got %q", got)
	}
}

// A directory in no workspace resolves to review "work-" — a review no deck row
// points at. Said plainly rather than left to be inferred from a trailing dash.
func TestReviewScopeLabelSaysWhenNoWorkspaceResolved(t *testing.T) {
	orphan := reviewScope{}
	orphan.review.ID = "work-"
	label := orphan.label()
	for _, want := range []string{"work-", "no awp workspace", "--workspace"} {
		if !strings.Contains(label, want) {
			t.Errorf("label does not mention %q: %q", want, label)
		}
	}
}

// Every review subcommand takes --workspace. A flag on some of them and not
// others is a trap: the agent learns it on `add` and loses its findings on
// `publish`.
func TestEveryReviewSubcommandAcceptsWorkspace(t *testing.T) {
	chdir(t, tempRoot(t))
	for _, args := range [][]string{
		{"add", "--file", "a.go", "--line", "1", "--body", "x", "--workspace", "ws"},
		{"reply", "--to", "abc", "--body", "x", "--workspace", "ws"},
		{"list", "--workspace", "ws"},
		{"publish", "--workspace", "ws"},
	} {
		var out bytes.Buffer
		// Outside a repo every one of these fails at review resolution, which is past
		// the flag parsing this asserts.
		err := runReviewSubcommand(failingRunner{}, nil, args, &out)
		if err == nil {
			t.Fatalf("%v: expected the command to fail outside a repo", args)
		}
		if strings.Contains(err.Error(), "flag provided but not defined") {
			t.Fatalf("%v: --workspace is not wired: %v", args, err)
		}
	}
}

// The prompt has to tell the agent where to run and how to check, not just state
// the assumption. It used to say only "the review is resolved from the workspace
// you are in", which is a fact about the mechanism rather than an instruction.
func TestReviewPromptSaysWhereToRunAndHowToCheck(t *testing.T) {
	for _, want := range []string{
		"Run these from the workspace directory",
		"--workspace",
		"awp review list",
	} {
		if !strings.Contains(reviewPromptTemplate, want) {
			t.Errorf("the review prompt never mentions %q", want)
		}
	}
}

// The replier the diff surfaces get: it posts in the review's own repo, and it
// caches the reply in the mirror the diff draws from.
func TestThreadReplyPostsInTheReposDirectoryAndMirrorsTheReply(t *testing.T) {
	root := tempRoot(t)
	store := review.Store{}
	r, err := store.Open(root, review.Target{Kind: review.TargetWorking, Workspace: "ws"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := store.SaveThreads(r, []review.Thread{{
		ID: "PRRT_1", Path: "a.go", Side: review.SideNew, Line: 3,
		Comments: []review.ThreadComment{{ID: "PRRC_a", Author: "alice", Body: "why?"}},
	}}); err != nil {
		t.Fatalf("seed the mirror: %v", err)
	}

	runner := &dirRecordingRunner{}
	_, _, reply := threadActionsFor(runner)
	id, err := reply(deckui.Item{RepoRoot: root, WorkspaceName: "ws"}, "PRRT_1", "because of X")
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	if id != "PRRC_reply" {
		t.Fatalf("expected the new comment's id back, got %q", id)
	}
	if len(runner.replyThreads) != 1 || runner.replyThreads[0] != "PRRT_1" {
		t.Fatalf("expected one post into PRRT_1, got %v", runner.replyThreads)
	}
	// The review's own repo, not wherever the deck was launched from — the same
	// requirement resolving has, and for the same reason.
	for _, dir := range runner.ghDirs {
		if dir != root {
			t.Fatalf("gh ran in %q, not the review's repo %q", dir, root)
		}
	}
	// Cached, so the diff shows the reply as part of the conversation instead of
	// losing it on the next refresh tick and finding it again minutes later.
	threads := store.Threads(r)
	if len(threads) != 1 || len(threads[0].Comments) != 2 {
		t.Fatalf("expected the reply mirrored onto the thread, got %+v", threads)
	}
	last := threads[0].Comments[1]
	if last.ID != "PRRC_reply" || last.Body != "because of X" {
		t.Fatalf("unexpected mirrored reply: %+v", last)
	}
}

// A post that failed is an error the viewer has to see: it keeps the draft and
// says so, rather than marking a reply nobody received as sent.
func TestThreadReplyReportsAFailedPost(t *testing.T) {
	root := tempRoot(t)
	runner := &failingGHRunner{}
	_, _, reply := threadActionsFor(runner)
	if _, err := reply(deckui.Item{RepoRoot: root, WorkspaceName: "ws"}, "PRRT_1", "hello"); err == nil {
		t.Fatal("expected the failure to surface")
	}
}

// failingGHRunner answers every gh call with a GraphQL error.
type failingGHRunner struct{}

func (failingGHRunner) Run(_ context.Context, _ string, _ string, _ ...string) (string, error) {
	return `{"errors":[{"message":"thread is gone"}]}`, nil
}
