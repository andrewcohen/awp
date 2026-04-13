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

func TestWorkspaceExistsChecksWorkingCopyRev(t *testing.T) {
	r := &fakeRunner{out: "abc123\n"}
	c := New(r)

	exists, err := c.WorkspaceExists("qa")
	if err != nil {
		t.Fatalf("WorkspaceExists returned error: %v", err)
	}
	if !exists {
		t.Fatal("expected workspace to exist")
	}
	wantArgs := []string{"log", "-r", "qa@", "--no-graph", "-T", "commit_id.short() ++ \"\\n\""}
	if !reflect.DeepEqual(r.lastArgs, wantArgs) {
		t.Fatalf("unexpected args: got %#v want %#v", r.lastArgs, wantArgs)
	}
}

func TestWorkspaceExistsReturnsFalseForMissingRevision(t *testing.T) {
	r := &fakeRunner{out: "Error: Revision `qa` doesn't exist\n", err: context.DeadlineExceeded}
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
	wantArgs := []string{"bookmark", "track", "my-bookmark@origin"}
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
		{"bookmark", "track", "feature/foo@origin"},
		{"bookmark", "track", "feature/foo"},
	}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("unexpected calls: got %#v want %#v", r.calls, want)
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
	wantArgs := []string{"log", "-r", "qa@", "--no-graph", "-T", "commit_id.short() ++ \"\\n\""}
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
	wantArgs := []string{"bookmark", "list", "-r", "abc123", "-T", "name ++ \"\\n\""}
	if !reflect.DeepEqual(r.lastArgs, wantArgs) {
		t.Fatalf("unexpected args: got %#v want %#v", r.lastArgs, wantArgs)
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

	out, err := c.DiffGit("/repo", "qa@")
	if err != nil {
		t.Fatalf("DiffGit returned error: %v", err)
	}
	if out != "diff output" {
		t.Fatalf("unexpected output: %q", out)
	}
	if r.lastDir != "/repo" {
		t.Fatalf("unexpected dir: %q", r.lastDir)
	}
	wantArgs := []string{"diff", "--git", "-r", "qa@"}
	if !reflect.DeepEqual(r.lastArgs, wantArgs) {
		t.Fatalf("unexpected args: got %#v want %#v", r.lastArgs, wantArgs)
	}
}

func TestDiffGitWithoutRevision(t *testing.T) {
	r := &fakeRunner{out: "diff output"}
	c := New(r)

	if _, err := c.DiffGit("/repo", ""); err != nil {
		t.Fatalf("DiffGit returned error: %v", err)
	}
	wantArgs := []string{"diff", "--git"}
	if !reflect.DeepEqual(r.lastArgs, wantArgs) {
		t.Fatalf("unexpected args: got %#v want %#v", r.lastArgs, wantArgs)
	}
}
