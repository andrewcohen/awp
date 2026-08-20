package jj

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseWorkspaceNamesLegacyOutput(t *testing.T) {
	out := "default: abcdef12 main\nfeature-one: 12345678 message\n\n"
	got := parseWorkspaceNames(out)
	want := []string{"default", "feature-one"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseWorkspaceNames() = %#v, want %#v", got, want)
	}
}

func TestParseWorkspaceNamesTemplateOutput(t *testing.T) {
	out := "default\nfeature-one\n"
	got := parseWorkspaceNames(out)
	want := []string{"default", "feature-one"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseWorkspaceNames() = %#v, want %#v", got, want)
	}
}

type fakeRunner struct {
	lastDir  string
	lastName string
	lastArgs []string
	out      string
	err      error
}

func (f *fakeRunner) Run(_ context.Context, dir string, name string, args ...string) (string, error) {
	f.lastDir = dir
	f.lastName = name
	f.lastArgs = append([]string(nil), args...)
	return f.out, f.err
}

type runStep struct {
	out string
	err error
}

type sequenceRunner struct {
	calls [][]string
	steps []runStep
}

func (s *sequenceRunner) Run(_ context.Context, _ string, _ string, args ...string) (string, error) {
	s.calls = append(s.calls, append([]string(nil), args...))
	if len(s.steps) == 0 {
		return "", nil
	}
	step := s.steps[0]
	s.steps = s.steps[1:]
	return step.out, step.err
}

func TestRepoRootFormatsCommandErrors(t *testing.T) {
	r := &fakeRunner{out: "Error: not in a repo\n", err: errors.New("exit status 1")}
	c := New(r)

	_, err := c.RepoRoot()
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "resolve repo root: exit status 1\nError: not in a repo" {
		t.Fatalf("unexpected error: %q", got)
	}
}

func TestListWorkspaceNamesFormatsCommandErrors(t *testing.T) {
	r := &fakeRunner{out: "Error: The working copy is stale\nHint: Run `jj workspace update-stale`\n", err: errors.New("exit status 1")}
	c := New(r)

	_, err := c.ListWorkspaceNames()
	if err == nil {
		t.Fatal("expected error")
	}
	got := err.Error()
	if !strings.Contains(got, "list workspaces: exit status 1") {
		t.Fatalf("unexpected error: %q", got)
	}
	if !strings.Contains(got, "working copy is stale") {
		t.Fatalf("expected stale hint in error: %q", got)
	}
}

func TestAllBookmarksByRecencyOrdersAndDedupes(t *testing.T) {
	// Two entries for "andrew/foo" (local + remote@origin) and a stale
	// local "main" — expect andrew/foo first (most-recent timestamp),
	// then qa, then main, with the @origin duplicate folded out.
	r := &fakeRunner{out: "1715000000\tandrew/foo\n1714000000\tqa\n1715000000\tandrew/foo@origin\n1700000000\tmain\n"}
	got, err := New(r).AllBookmarksByRecency()
	if err != nil {
		t.Fatalf("AllBookmarksByRecency err: %v", err)
	}
	want := []string{"andrew/foo", "qa", "main"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d: got %q, want %q", i, got[i], want[i])
		}
	}
	// Confirm the command used the bookmark-list path with a template arg.
	joined := strings.Join(r.lastArgs, " ")
	if !strings.Contains(joined, "bookmark") || !strings.Contains(joined, "-T") {
		t.Errorf("unexpected command args: %v", r.lastArgs)
	}
}

func TestListWorkspaceNamesUsesIgnoreWorkingCopy(t *testing.T) {
	r := &fakeRunner{out: "default\nqa\n"}
	c := New(r)

	names, err := c.ListWorkspaceNames()
	if err != nil {
		t.Fatalf("ListWorkspaceNames returned error: %v", err)
	}
	wantNames := []string{"default", "qa"}
	if !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("names = %#v, want %#v", names, wantNames)
	}
	wantArgs := []string{"--ignore-working-copy", "workspace", "list", "-T", "name ++ \"\\n\""}
	if !reflect.DeepEqual(r.lastArgs, wantArgs) {
		t.Fatalf("unexpected args: got %#v want %#v", r.lastArgs, wantArgs)
	}
}

func TestIsStaleWorkingCopyError(t *testing.T) {
	err := errors.New("list workspaces: exit status 1\nError: The working copy is stale\nHint: Run `jj workspace update-stale`")
	if !IsStaleWorkingCopyError(err) {
		t.Fatal("expected stale working copy error to be detected")
	}
	if IsStaleWorkingCopyError(nil) {
		t.Fatal("expected nil error to not be stale")
	}
}

func TestUpdateStaleFormatsCommandErrors(t *testing.T) {
	r := &fakeRunner{out: "boom\n", err: errors.New("exit status 1")}
	c := New(r)
	if err := c.UpdateStale(); err == nil || !strings.Contains(err.Error(), "update stale working copy") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWorkspaceExistsChecksRegistry(t *testing.T) {
	// Regression: WorkspaceExists used to run `jj log -r <name>@`
	// which reports "no revisions to show" for orphaned workspaces
	// (registered with jj but with a broken @). That made
	// PrepareWorkspace think the workspace was gone and try to create
	// it again, only for jj to reject the create with "already
	// exists." The registry view (`jj workspace list`) is what
	// reflects collision risk and what we now use.
	r := &fakeRunner{out: "default\nqa\nreview\n"}
	c := New(r)

	exists, err := c.WorkspaceExists("qa")
	if err != nil {
		t.Fatalf("WorkspaceExists returned error: %v", err)
	}
	if !exists {
		t.Fatal("expected workspace to exist")
	}
	wantArgs := []string{"--ignore-working-copy", "workspace", "list", "-T", "name ++ \"\\n\""}
	if !reflect.DeepEqual(r.lastArgs, wantArgs) {
		t.Fatalf("unexpected args: got %#v want %#v", r.lastArgs, wantArgs)
	}
}

func TestWorkspaceExistsReturnsFalseWhenAbsentFromRegistry(t *testing.T) {
	r := &fakeRunner{out: "default\nother\n"}
	c := New(r)

	exists, err := c.WorkspaceExists("qa")
	if err != nil {
		t.Fatalf("WorkspaceExists returned error: %v", err)
	}
	if exists {
		t.Fatal("expected workspace to be missing")
	}
}

func TestAddWorkspaceUsesRequestedBaseRevision(t *testing.T) {
	r := &fakeRunner{}
	c := New(r)

	if err := c.AddWorkspace("qa", "/tmp/qa", "feature/bookmark"); err != nil {
		t.Fatalf("AddWorkspace returned error: %v", err)
	}
	if r.lastName != "jj" {
		t.Fatalf("expected command name jj, got %q", r.lastName)
	}
	wantArgs := []string{"workspace", "add", "--name", "qa", "-r", "feature/bookmark", "/tmp/qa"}
	if !reflect.DeepEqual(r.lastArgs, wantArgs) {
		t.Fatalf("unexpected args: got %#v want %#v", r.lastArgs, wantArgs)
	}
}

func TestTrackBookmarkPrefersOriginName(t *testing.T) {
	r := &fakeRunner{}
	c := New(r)

	if err := c.TrackBookmark("my-bookmark"); err != nil {
		t.Fatalf("TrackBookmark returned error: %v", err)
	}
	// Modern jj syntax: `--remote=origin` instead of the deprecated `@origin`.
	wantArgs := []string{"bookmark", "track", "my-bookmark", "--remote=origin"}
	if !reflect.DeepEqual(r.lastArgs, wantArgs) {
		t.Fatalf("unexpected args: got %#v want %#v", r.lastArgs, wantArgs)
	}
}

func TestTrackBookmarkPrefersOriginThenFallsBackLocal(t *testing.T) {
	r := &sequenceRunner{steps: []runStep{
		{out: "bookmark not found", err: errors.New("exit status 1")},
		{},
	}}
	c := New(r)

	if err := c.TrackBookmark("feature/foo"); err != nil {
		t.Fatalf("TrackBookmark returned error: %v", err)
	}
	want := [][]string{
		{"bookmark", "track", "feature/foo", "--remote=origin"},
		{"bookmark", "track", "feature/foo"},
	}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("unexpected calls: got %#v want %#v", r.calls, want)
	}
}

func TestTrackBookmarkFallsThroughOnNoMatchingRemoteBookmarks(t *testing.T) {
	// Regression: jj exits 0 with a "No matching remote bookmarks"
	// warning when the tracked name doesn't exist on the remote. The
	// old TrackBookmark treated that as success and never tried the
	// bare-name fallback, leaving callers downstream to fail with the
	// real "revision doesn't exist" surprise. Now we scan the output
	// and re-classify it as a failure so the next candidate runs.
	r := &sequenceRunner{steps: []runStep{
		{out: "Warning: No matching remote bookmarks for names: feature/foo\nNothing changed.\n"},
		{},
	}}
	c := New(r)

	if err := c.TrackBookmark("feature/foo"); err != nil {
		t.Fatalf("TrackBookmark returned error: %v", err)
	}
	if len(r.calls) != 2 {
		t.Fatalf("expected fall-through to bare-name candidate; got %#v", r.calls)
	}
	if !reflect.DeepEqual(r.calls[1], []string{"bookmark", "track", "feature/foo"}) {
		t.Fatalf("unexpected second-attempt args: %#v", r.calls[1])
	}
}

func TestWorkspaceRevisionUsesCommitIDTemplate(t *testing.T) {
	r := &fakeRunner{out: "abc123\n"}
	c := New(r)

	rev, err := c.WorkspaceRevision("qa")
	if err != nil {
		t.Fatalf("WorkspaceRevision returned error: %v", err)
	}
	if rev != "abc123" {
		t.Fatalf("revision = %q, want abc123", rev)
	}
	wantArgs := []string{"--ignore-working-copy", "log", "-r", "qa@", "--no-graph", "-T", "commit_id.short() ++ \"\\n\""}
	if !reflect.DeepEqual(r.lastArgs, wantArgs) {
		t.Fatalf("unexpected args: got %#v want %#v", r.lastArgs, wantArgs)
	}
}

func TestBookmarksAtRevisionUsesTemplate(t *testing.T) {
	r := &fakeRunner{out: "foo\nbar\n"}
	c := New(r)

	names, err := c.BookmarksAtRevision("abc123")
	if err != nil {
		t.Fatalf("BookmarksAtRevision returned error: %v", err)
	}
	wantNames := []string{"foo", "bar"}
	if !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("names = %#v, want %#v", names, wantNames)
	}
	wantArgs := []string{"--ignore-working-copy", "bookmark", "list", "-r", "abc123", "-T", "name ++ \"\\n\""}
	if !reflect.DeepEqual(r.lastArgs, wantArgs) {
		t.Fatalf("unexpected args: got %#v want %#v", r.lastArgs, wantArgs)
	}
}

func TestTrunkUsesIgnoreWorkingCopy(t *testing.T) {
	r := &fakeRunner{out: "main\n"}
	c := New(r)

	name, err := c.Trunk()
	if err != nil {
		t.Fatalf("Trunk returned error: %v", err)
	}
	if name != "main" {
		t.Fatalf("trunk = %q, want main", name)
	}
	if len(r.lastArgs) == 0 || r.lastArgs[0] != "--ignore-working-copy" {
		t.Fatalf("expected --ignore-working-copy first, got %#v", r.lastArgs)
	}
}

func TestBookmarkNameAtReturnsNameAndPassesRevset(t *testing.T) {
	r := &fakeRunner{out: "andrew/useexperiment-ssr\n"}
	c := New(r)

	name, err := c.BookmarkNameAt(`heads((trunk()..@) & bookmarks())`)
	if err != nil {
		t.Fatalf("BookmarkNameAt returned error: %v", err)
	}
	if name != "andrew/useexperiment-ssr" {
		t.Fatalf("name = %q, want andrew/useexperiment-ssr", name)
	}
	// The revset must be passed through -r, with --ignore-working-copy so the
	// query never locks or snapshots the workspace.
	joined := strings.Join(r.lastArgs, " ")
	if !strings.Contains(joined, "--ignore-working-copy") {
		t.Errorf("expected --ignore-working-copy, got %#v", r.lastArgs)
	}
	if !strings.Contains(joined, "heads((trunk()..@) & bookmarks())") {
		t.Errorf("revset not passed through, got %#v", r.lastArgs)
	}
}

func TestBookmarkNameAtEmptyWhenNoBookmark(t *testing.T) {
	r := &fakeRunner{out: "\n"}
	c := New(r)
	name, err := c.BookmarkNameAt(`heads(none())`)
	if err != nil {
		t.Fatalf("BookmarkNameAt returned error: %v", err)
	}
	if name != "" {
		t.Fatalf("name = %q, want empty (no bookmark on the resolved commit)", name)
	}
}

// TestTrunkSkipsSnapshotWarning guards the regression where jj's
// "Refused to snapshot some files" warning (merged into stdout by the
// CombinedOutput runner) was returned verbatim as the trunk bookmark
// name, surfacing in the new-workspace "Start from" picker.
func TestTrunkSkipsSnapshotWarning(t *testing.T) {
	r := &fakeRunner{out: "Warning: Refused to snapshot some files:\n  big.bin: 50.0MiB exceeds the maximum size allowed by config (1.0MiB)\nHint: increase snapshot.max-new-file-size\nmain\n"}
	c := New(r)

	name, err := c.Trunk()
	if err != nil {
		t.Fatalf("Trunk returned error: %v", err)
	}
	if name != "main" {
		t.Fatalf("trunk = %q, want main (warning lines must be skipped)", name)
	}
}

// TestParseWorkspaceNamesSkipsSnapshotWarning guards the same leak for
// every picker fed through parseWorkspaceNames (AllBookmarks, etc.).
func TestParseWorkspaceNamesSkipsSnapshotWarning(t *testing.T) {
	out := "Warning: Refused to snapshot some files:\n  big.bin: 50.0MiB exceeds the maximum size allowed by config (1.0MiB)\nHint: increase snapshot.max-new-file-size\nmain\nandrew/feature\n"
	got := parseWorkspaceNames(out)
	want := []string{"main", "andrew/feature"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseWorkspaceNames = %#v, want %#v", got, want)
	}
}

func TestBookmarkCommitIDReturnsCommitID(t *testing.T) {
	r := &fakeRunner{out: "deadbeefcafef00d1234567890abcdef12345678\n"}
	c := New(r)

	commit, err := c.BookmarkCommitID(context.Background(), "/repo", "andrew/foo")
	if err != nil {
		t.Fatalf("BookmarkCommitID returned error: %v", err)
	}
	if commit != "deadbeefcafef00d1234567890abcdef12345678" {
		t.Errorf("commit-id: got %q", commit)
	}
	// Revset must scope to the origin remote-tracking ref (exact match on
	// both name and remote) so we resolve "last-fetched origin tip" — the
	// honest anchor for "behind remote" — even when the workspace has no
	// true local bookmark of this name (typical for collaborator PRs).
	var revset string
	for i, a := range r.lastArgs {
		if a == "-r" && i+1 < len(r.lastArgs) {
			revset = r.lastArgs[i+1]
		}
	}
	if !strings.Contains(revset, `remote_bookmarks(exact:"andrew/foo", exact:"origin")`) {
		t.Errorf("revset: got %q want it to scope to remote_bookmarks(exact:NAME, exact:\"origin\")", revset)
	}
}

func TestBookmarkCommitIDEmptyNameReturnsEmpty(t *testing.T) {
	r := &fakeRunner{}
	commit, err := New(r).BookmarkCommitID(context.Background(), "/repo", "")
	if err != nil {
		t.Fatalf("BookmarkCommitID returned error: %v", err)
	}
	if commit != "" {
		t.Errorf("expected empty result for empty name, got %q", commit)
	}
	if r.lastName != "" {
		t.Errorf("expected no jj invocation, got name=%q args=%v", r.lastName, r.lastArgs)
	}
}

func TestBookmarkCommitIDEmptyOutputForUnknownBookmark(t *testing.T) {
	// jj prints nothing (and exits 0) when the revset matches no commits.
	r := &fakeRunner{out: ""}
	commit, err := New(r).BookmarkCommitID(context.Background(), "/repo", "andrew/does-not-exist")
	if err != nil {
		t.Fatalf("BookmarkCommitID returned error: %v", err)
	}
	if commit != "" {
		t.Errorf("expected empty result for missing bookmark, got %q", commit)
	}
}

func TestForgetBookmarkIncludesRemotes(t *testing.T) {
	r := &fakeRunner{}
	c := New(r)

	if err := c.ForgetBookmark("feature/foo"); err != nil {
		t.Fatalf("ForgetBookmark returned error: %v", err)
	}
	wantArgs := []string{"bookmark", "forget", "--include-remotes", "feature/foo"}
	if !reflect.DeepEqual(r.lastArgs, wantArgs) {
		t.Fatalf("unexpected args: got %#v want %#v", r.lastArgs, wantArgs)
	}
}

func TestDiffGitUsesRevisionWhenProvided(t *testing.T) {
	r := &fakeRunner{out: "diff output"}
	c := New(r)

	out, err := c.DiffGit("/repo", "qa@", DiffContextDefault)
	if err != nil {
		t.Fatalf("DiffGit returned error: %v", err)
	}
	if out != "diff output" {
		t.Fatalf("unexpected output: %q", out)
	}
	if r.lastDir != "/repo" {
		t.Fatalf("unexpected dir: %q", r.lastDir)
	}
	wantArgs := []string{"diff", "--git", "--context", "3", "-r", "qa@"}
	if !reflect.DeepEqual(r.lastArgs, wantArgs) {
		t.Fatalf("unexpected args: got %#v want %#v", r.lastArgs, wantArgs)
	}
}

func TestDiffGitWithoutRevision(t *testing.T) {
	r := &fakeRunner{out: "diff output"}
	c := New(r)

	if _, err := c.DiffGit("/repo", "", DiffContextDefault); err != nil {
		t.Fatalf("DiffGit returned error: %v", err)
	}
	wantArgs := []string{"diff", "--git", "--context", "3"}
	if !reflect.DeepEqual(r.lastArgs, wantArgs) {
		t.Fatalf("unexpected args: got %#v want %#v", r.lastArgs, wantArgs)
	}
}

// TestDiffGitPassesTheContextItWasGiven is the whole point of the argument: the
// viewer's + and - are only worth having if the number reaches jj.
func TestDiffGitPassesTheContextItWasGiven(t *testing.T) {
	for _, tc := range []struct {
		lines int
		want  string
	}{
		{0, "0"},
		{1, "1"},
		{24, "24"},
		// A negative count is not a request that can be honoured, and is clamped
		// rather than passed on for jj to reject.
		{-5, "0"},
	} {
		r := &fakeRunner{out: "diff output"}
		if _, err := New(r).DiffGit("/repo", "", tc.lines); err != nil {
			t.Fatalf("DiffGit(%d) returned error: %v", tc.lines, err)
		}
		wantArgs := []string{"diff", "--git", "--context", tc.want}
		if !reflect.DeepEqual(r.lastArgs, wantArgs) {
			t.Errorf("DiffGit(%d) ran %#v, want %#v", tc.lines, r.lastArgs, wantArgs)
		}
	}
}

// jj's complaint is what makes a failure actionable, so it has to be in the
// message — once. awp's runners already quote the command's output, and the
// duplicate is what a one-row status bar spends its width on.
func TestTheCommandsOutputAppearsOnce(t *testing.T) {
	const complaint = "Error: Cannot diff revsets with gaps in.\nHint: Revision 7a8f6e8b90f2 would need to be in the set."
	runnerErr := errors.New(`"jj" exited 1:` + "\n" + complaint)

	got := formatCommandError("load diff", runnerErr, complaint).Error()
	if n := strings.Count(got, "Cannot diff revsets"); n != 1 {
		t.Fatalf("jj's complaint appears %d times, want 1:\n%s", n, got)
	}
	if !strings.Contains(got, "load diff") || !strings.Contains(got, "would need to be in the set") {
		t.Fatalf("expected the action and the whole complaint, got:\n%s", got)
	}
}

// A runner that says nothing about the output still gets it attached — that is
// the case the attachment exists for.
func TestOutputIsAttachedWhenTheErrorDoesNotCarryIt(t *testing.T) {
	long := strings.Repeat("jj is unhappy about this particular revset. ", 20)
	got := formatCommandError("load diff", errors.New("exit status 1"), long).Error()
	if !strings.Contains(got, "jj is unhappy") {
		t.Fatalf("expected the output attached, got:\n%s", got)
	}
}

// And a capped snippet counts as carrying it: the runners ellipsise long output,
// so the message holds a prefix of itself rather than the whole thing.
func TestATruncatedSnippetCountsAsCarried(t *testing.T) {
	long := strings.Repeat("jj is unhappy about this particular revset. ", 40)
	runnerErr := errors.New(`"jj" exited 1:` + "\n" + long[:800] + "…")
	if n := strings.Count(formatCommandError("load diff", runnerErr, long).Error(), "jj is unhappy"); n > 20 {
		t.Fatalf("the output was attached again on top of the runner's snippet (%d mentions)", n)
	}
}

// A base whose range to @ has a gap in it is one jj will refuse to diff, so the
// question has to be answerable before the diff is attempted.
func TestConnectedToWorkingCopy(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
		want bool
	}{
		{name: "an ancestor of @ leaves nothing outside ::@", out: "\n", want: true},
		{name: "a bookmark that moved is outside it", out: "7a8f6e8b90f2\n", want: false},
		{
			// A leaked stderr line read as a commit would call every base disconnected
			// and flatten every review to trunk.
			name: "a leaked diagnostic is not a commit",
			out:  "Warning: Refused to snapshot some files:\n  some/path\n",
			want: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &fakeRunner{out: tc.out}
			got, err := New(r).ConnectedToWorkingCopy("andrew/parent")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("connected = %t, want %t", got, tc.want)
			}
			// Asked as "what part of the base is not an ancestor of @", so a divergent
			// bookmark is judged on every commit it resolves to.
			if want := `(andrew/parent) ~ ::@`; !slicesContain(r.lastArgs, want) {
				t.Fatalf("revset asked for = %v, want %q", r.lastArgs, want)
			}
		})
	}
}

func slicesContain(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}
