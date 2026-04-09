package jj

import (
	"context"
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

func TestSetBookmarkTargetsWorkspaceWorkingCopy(t *testing.T) {
	r := &fakeRunner{}
	c := New(r)

	if err := c.SetBookmark("my-bookmark", "qa"); err != nil {
		t.Fatalf("SetBookmark returned error: %v", err)
	}
	wantArgs := []string{"bookmark", "set", "--allow-backwards", "my-bookmark", "-r", "qa@"}
	if !reflect.DeepEqual(r.lastArgs, wantArgs) {
		t.Fatalf("unexpected args: got %#v want %#v", r.lastArgs, wantArgs)
	}
}
