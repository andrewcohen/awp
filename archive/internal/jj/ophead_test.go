package jj

import (
	"os"
	"path/filepath"
	"testing"
)

// writeOpHead lays out the part of a jj repo OpHead reads: a repo directory
// with op_heads/heads holding one file per head.
func writeOpHead(t *testing.T, repo string, heads ...string) {
	t.Helper()
	dir := filepath.Join(repo, "op_heads", "heads")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, h := range heads {
		if err := os.WriteFile(filepath.Join(dir, h), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpHeadReadsTheRepoInPlace(t *testing.T) {
	dir := t.TempDir()
	writeOpHead(t, filepath.Join(dir, ".jj", "repo"), "abc123")

	got, err := OpHead(dir)
	if err != nil {
		t.Fatalf("OpHead: %v", err)
	}
	if got != "abc123" {
		t.Fatalf("OpHead = %q, want %q", got, "abc123")
	}
}

// A workspace added with `jj workspace add` has a .jj/repo *file* naming the
// real repo, relative to the .jj directory it sits in. Resolving it against the
// workspace directory instead lands one level off and reads nothing.
func TestOpHeadFollowsAWorkspacesRepoFile(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "main", ".jj", "repo")
	writeOpHead(t, repo, "deadbeef")

	ws := filepath.Join(root, "ws")
	if err := os.MkdirAll(filepath.Join(ws, ".jj"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".jj", "repo"), []byte("../../main/.jj/repo\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := OpHead(ws)
	if err != nil {
		t.Fatalf("OpHead: %v", err)
	}
	if got != "deadbeef" {
		t.Fatalf("OpHead = %q, want %q", got, "deadbeef")
	}
}

// Two heads is a repo mid-divergence, and ReadDir's order is the filesystem's.
// The same state has to produce the same string or every read looks like a
// change.
func TestOpHeadIsStableAcrossConcurrentHeads(t *testing.T) {
	dir := t.TempDir()
	writeOpHead(t, filepath.Join(dir, ".jj", "repo"), "bbb", "aaa")

	got, err := OpHead(dir)
	if err != nil {
		t.Fatalf("OpHead: %v", err)
	}
	if got != "aaa bbb" {
		t.Fatalf("OpHead = %q, want %q", got, "aaa bbb")
	}
}

// A directory with no .jj is not a failure — it is the deck's own common case,
// a workspace deleted out from under the state file — and it has to be
// distinguishable from a repo that would not read.
func TestOpHeadSaysNothingForANonRepo(t *testing.T) {
	got, err := OpHead(t.TempDir())
	if err != nil {
		t.Fatalf("OpHead: %v", err)
	}
	if got != "" {
		t.Fatalf("OpHead = %q, want empty", got)
	}
	if got, err := OpHead(""); err != nil || got != "" {
		t.Fatalf("OpHead(\"\") = %q, %v; want empty, nil", got, err)
	}
}

// A .jj that is there but whose op log is not readable is the "assume it
// changed" case, and the caller can only tell by the error.
func TestOpHeadErrorsWhenTheRepoIsThereButUnreadable(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".jj", "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := OpHead(dir); err == nil {
		t.Fatal("OpHead: want an error for a repo with no op_heads, got nil")
	}
}
