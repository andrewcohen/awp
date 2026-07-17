package cli

import (
	"context"
	"strings"
	"testing"
)

// reviewBaseRunner fakes jj for resolveReviewStackBase: it answers the
// `-r trunk()` probe with a trunk name and the `-r heads(...)` probe with a
// (possibly empty) parent bookmark, recording each revset it saw.
type reviewBaseRunner struct {
	trunk  string
	parent string
	revs   []string
}

func (r *reviewBaseRunner) Run(_ context.Context, _ string, _ string, args ...string) (string, error) {
	revset := ""
	for i, a := range args {
		if a == "-r" && i+1 < len(args) {
			revset = args[i+1]
		}
	}
	r.revs = append(r.revs, revset)
	switch {
	case revset == "trunk()":
		return r.trunk + "\n", nil
	case strings.HasPrefix(revset, "heads("):
		if r.parent == "" {
			return "\n", nil
		}
		return r.parent + "\n", nil
	}
	return "", nil
}

func TestResolveReviewStackBaseFindsStackParent(t *testing.T) {
	r := &reviewBaseRunner{trunk: "main", parent: "andrew/useexperiment-ssr"}
	got := resolveReviewStackBase(r, "/ws/homepage-abc", "andrew/homepage-abc")
	if got != "andrew/useexperiment-ssr" {
		t.Fatalf("base = %q, want andrew/useexperiment-ssr", got)
	}
	// The stack-parent revset must exclude the trunk bookmark AND the
	// workspace's own bookmark by name.
	var stackRevset string
	for _, rv := range r.revs {
		if strings.HasPrefix(rv, "heads(") {
			stackRevset = rv
		}
	}
	if !strings.Contains(stackRevset, `bookmarks(exact:"main")`) {
		t.Errorf("revset should exclude trunk 'main': %q", stackRevset)
	}
	if !strings.Contains(stackRevset, `bookmarks(exact:"andrew/homepage-abc")`) {
		t.Errorf("revset should exclude own bookmark: %q", stackRevset)
	}
}

func TestResolveReviewStackBaseFallsBackToTrunk(t *testing.T) {
	// No parent bookmark between trunk and @ → not stacked → trunk().
	r := &reviewBaseRunner{trunk: "main", parent: ""}
	if got := resolveReviewStackBase(r, "/ws/x", "andrew/x"); got != "trunk()" {
		t.Fatalf("base = %q, want trunk()", got)
	}
}

func TestResolveReviewStackBaseOmitsOwnExclusionWhenNoBookmark(t *testing.T) {
	r := &reviewBaseRunner{trunk: "main", parent: ""}
	resolveReviewStackBase(r, "/ws/x", "")
	var stackRevset string
	for _, rv := range r.revs {
		if strings.HasPrefix(rv, "heads(") {
			stackRevset = rv
		}
	}
	// With no own bookmark, only the trunk exclusion is present.
	if strings.Count(stackRevset, "bookmarks(exact:") != 1 {
		t.Errorf("expected exactly one exact-exclusion (trunk), got %q", stackRevset)
	}
}

func TestResolveReviewStackBaseEmptyDirIsTrunk(t *testing.T) {
	r := &reviewBaseRunner{trunk: "main", parent: "should-not-be-used"}
	if got := resolveReviewStackBase(r, "", "andrew/x"); got != "trunk()" {
		t.Fatalf("empty dir should short-circuit to trunk(), got %q", got)
	}
	if len(r.revs) != 0 {
		t.Errorf("empty dir should not invoke jj, got calls %v", r.revs)
	}
}
