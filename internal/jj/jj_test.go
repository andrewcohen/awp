package jj

import (
	"context"
	"errors"
	"reflect"
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
