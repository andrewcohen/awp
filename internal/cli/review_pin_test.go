package cli

import (
	"path/filepath"
	"testing"

	"github.com/andrewcohen/awp/internal/review"
	"github.com/andrewcohen/awp/internal/workspace"
)

// The pin is the only thing that makes publish reachable: reviews are keyed by
// workspace, so no review's target ever names a PR.
func TestResolvePublishPRFallsBackToThePin(t *testing.T) {
	working := review.Target{Kind: review.TargetWorking, Workspace: "review-430"}
	if got := resolvePublishPR(0, working, 430); got != 430 {
		t.Fatalf("expected the workspace's pin, got %d", got)
	}
}

func TestResolvePublishPRPrefersTheFlag(t *testing.T) {
	working := review.Target{Kind: review.TargetWorking, Workspace: "review-430"}
	if got := resolvePublishPR(77, working, 430); got != 77 {
		t.Fatalf("--pr must win over the pin, got %d", got)
	}
	prKeyed := review.Target{Kind: review.TargetPR, Value: "430"}
	if got := resolvePublishPR(77, prKeyed, 0); got != 77 {
		t.Fatalf("--pr must win over the target, got %d", got)
	}
}

// A PR-keyed target still resolves, even though nothing creates one today —
// review.TargetPR is part of the store's vocabulary and publish should not
// ignore it if a caller ever opens one.
func TestResolvePublishPRReadsAPRKeyedTarget(t *testing.T) {
	if got := resolvePublishPR(0, review.Target{Kind: review.TargetPR, Value: "430"}, 0); got != 430 {
		t.Fatalf("expected 430 from the target, got %d", got)
	}
	// An unparseable value must not be mistaken for a PR; the pin still applies.
	garbled := review.Target{Kind: review.TargetPR, Value: "not-a-number"}
	if got := resolvePublishPR(0, garbled, 12); got != 12 {
		t.Fatalf("expected the pin when the target is garbled, got %d", got)
	}
}

func TestResolvePublishPRReportsNothingToPublishTo(t *testing.T) {
	working := review.Target{Kind: review.TargetWorking, Workspace: "scratch"}
	if got := resolvePublishPR(0, working, 0); got != 0 {
		t.Fatalf("expected 0 so the caller can explain itself, got %d", got)
	}
}

func TestPinnedPRForPathFindsTheContainingWorkspace(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "ws", "review-430")
	svc := &fakeService{listEntries: []workspace.ListEntry{
		{Name: "other", Path: filepath.Join(root, "ws", "other"), PRNumber: 99},
		{Name: "review-430", Path: ws, PRNumber: 430},
	}}

	// From the workspace root, and from a subdirectory of it — an agent runs
	// `awp review publish` from wherever it happens to be working.
	for _, cwd := range []string{ws, filepath.Join(ws, "internal", "cli")} {
		if got := pinnedPRForPath(svc, cwd); got != 430 {
			t.Fatalf("cwd %s: expected PR 430, got %d", cwd, got)
		}
	}
}

// Longest match wins, so a workspace nested inside another reports its own PR
// rather than its parent's.
func TestPinnedPRForPathPrefersTheNestedWorkspace(t *testing.T) {
	root := t.TempDir()
	outer := filepath.Join(root, "ws", "outer")
	inner := filepath.Join(outer, "inner")
	svc := &fakeService{listEntries: []workspace.ListEntry{
		{Name: "outer", Path: outer, PRNumber: 1},
		{Name: "inner", Path: inner, PRNumber: 2},
	}}
	if got := pinnedPRForPath(svc, inner); got != 2 {
		t.Fatalf("expected the nested workspace's PR 2, got %d", got)
	}
	if got := pinnedPRForPath(svc, outer); got != 1 {
		t.Fatalf("expected the outer workspace's PR 1, got %d", got)
	}
}

func TestPinnedPRForPathIsZeroWhenNothingMatches(t *testing.T) {
	root := t.TempDir()
	svc := &fakeService{listEntries: []workspace.ListEntry{
		{Name: "ws", Path: filepath.Join(root, "ws"), PRNumber: 430},
	}}
	if got := pinnedPRForPath(svc, filepath.Join(root, "elsewhere")); got != 0 {
		t.Fatalf("expected 0 outside any workspace, got %d", got)
	}
	if got := pinnedPRForPath(nil, root); got != 0 {
		t.Fatalf("expected 0 with no service, got %d", got)
	}
	// A prefix of a workspace path that is not a path component must not match:
	// .../ws-notes is not inside .../ws.
	if got := pinnedPRForPath(svc, filepath.Join(root, "ws-notes")); got != 0 {
		t.Fatalf("expected 0 for a sibling whose name shares a prefix, got %d", got)
	}
}

// The review's identity must not shift when the workspace gains a PR — that
// would split a store in half mid-life. reviewTargetFor keys by workspace only;
// the PR travels separately, via the pin.
func TestReviewTargetIgnoresThePin(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "ws", "feature")
	withPR := &fakeService{listEntries: []workspace.ListEntry{
		{Name: "feature", Path: ws, PRNumber: 430},
	}}
	withoutPR := &fakeService{listEntries: []workspace.ListEntry{
		{Name: "feature", Path: ws},
	}}
	before := reviewTargetFor(withoutPR, ws)
	after := reviewTargetFor(withPR, ws)
	if before != after {
		t.Fatalf("opening a PR changed the review's identity: %+v then %+v", before, after)
	}
	if after.Kind != review.TargetWorking || after.Workspace != "feature" {
		t.Fatalf("expected a workspace-keyed target, got %+v", after)
	}
	if review.ID(before) != review.ID(after) {
		t.Fatalf("review id moved: %q then %q", review.ID(before), review.ID(after))
	}
}
