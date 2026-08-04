package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/workspace"
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

// The label is what a reader sees, so the trunk fallback has to name the branch
// rather than hand back the revset that finds it.
func TestResolveReviewStackBaseNamedSpellsOutTrunk(t *testing.T) {
	r := &reviewBaseRunner{trunk: "main", parent: ""}
	revset, label := resolveReviewStackBaseNamed(r, "/ws/x", "andrew/x")
	if revset != "trunk()" {
		t.Fatalf("revset = %q, want trunk()", revset)
	}
	if label != "main" {
		t.Fatalf("label = %q, want main — %q is not something to show a reader", label, revset)
	}
}

// A stacked change is read against its parent, and says so.
func TestResolveReviewStackBaseNamedUsesTheParentBookmark(t *testing.T) {
	r := &reviewBaseRunner{trunk: "main", parent: "andrew/parent-change"}
	revset, label := resolveReviewStackBaseNamed(r, "/ws/child", "andrew/child")
	if revset != "andrew/parent-change" || label != "andrew/parent-change" {
		t.Fatalf("got revset=%q label=%q, want both andrew/parent-change", revset, label)
	}
}

// With no directory to ask in there is no honest label — the caller falls back
// to its own wording rather than printing a guess.
func TestResolveReviewStackBaseNamedHasNoLabelWithoutADir(t *testing.T) {
	r := &reviewBaseRunner{trunk: "main", parent: "andrew/p"}
	revset, label := resolveReviewStackBaseNamed(r, "", "andrew/x")
	if revset != "trunk()" {
		t.Fatalf("revset = %q, want trunk()", revset)
	}
	if label != "" {
		t.Fatalf("label = %q, want empty", label)
	}
}

// Only the scopes that read against something have a base worth naming; the
// working copy is diffed against @ itself, which its own wording already says.
func TestDiffBaseResolverOnlyAnswersForTheScopesWithABase(t *testing.T) {
	r := &reviewBaseRunner{trunk: "main", parent: ""}
	resolve := diffBaseResolverFor(r)
	item := deckuiItemForBase("/ws/x", "andrew/x")
	if got := resolve(item, deckui.ScopeStackBase); got != "main" {
		t.Fatalf("stack scope label = %q, want main", got)
	}
	if got := resolve(item, deckui.ScopeWorking); got != "" {
		t.Fatalf("working scope should have no base label, got %q", got)
	}
	// The trunk scope names the branch too, not the literal "trunk()".
	if got := resolve(item, deckui.ScopeTrunk); got != "main" {
		t.Fatalf("trunk scope label = %q, want main", got)
	}
}

// Each scope's revision, which is the only thing that differs between them.
func TestScopeRevset(t *testing.T) {
	item := deckuiItemForBase("/ws/child", "andrew/child")
	cases := []struct {
		scope deckui.DiffScope
		// parent is the stacked-parent bookmark jj reports, empty for none.
		parent string
		want   string
	}{
		// Empty: `jj diff`'s own default is the working copy, so there is nothing
		// to pass.
		{deckui.ScopeWorking, "andrew/parent", ""},
		{deckui.ScopeTrunk, "andrew/parent", "trunk()..@"},
		{deckui.ScopeStackBase, "andrew/parent", "andrew/parent..@"},
		// Nothing stacked, so the stack base and trunk coincide.
		{deckui.ScopeStackBase, "", "trunk()..@"},
	}
	for _, c := range cases {
		r := &reviewBaseRunner{trunk: "main", parent: c.parent}
		if got := scopeRevset(r, item, c.scope); got != c.want {
			t.Errorf("scopeRevset(%v, parent=%q) = %q, want %q", c.scope, c.parent, got, c.want)
		}
	}
	// The working copy resolves nothing, so it must not spend a jj call finding a
	// base it will not use.
	r := &reviewBaseRunner{trunk: "main", parent: "andrew/parent"}
	scopeRevset(r, item, deckui.ScopeWorking)
	if len(r.revs) != 0 {
		t.Errorf("the working-copy scope should not invoke jj, got %v", r.revs)
	}
}

func deckuiItemForBase(path, bookmark string) deckui.Item {
	return deckui.Item{Path: path, Bookmark: bookmark}
}

// The `-` menu, and with it the range a bare `awp diff` opens on. One list feeds
// both the deck's `c` and standalone `awp diff`, so a key cannot mean one thing in
// one and nothing in the other.
func TestScopeOptionsFor(t *testing.T) {
	r := &reviewBaseRunner{trunk: "main", parent: "andrew/parent-change"}
	item := deckuiItemForBase("/ws/child", "andrew/child")
	opts := scopeOptionsFor(r, item, "/ws/child")
	if len(opts) != 3 {
		t.Fatalf("expected three scopes, got %d", len(opts))
	}
	// The first is the default the view opens on: the whole change against its
	// stack base, which is what a review is normally of.
	if opts[0].Key != "c" || opts[0].Label != "vs stack base" {
		t.Fatalf("expected the stack base first, got %q/%q", opts[0].Key, opts[0].Label)
	}
	want := []struct{ key, label string }{
		{"c", "vs stack base"},
		{"w", "working copy"},
		{"t", "vs trunk"},
	}
	for i, w := range want {
		if opts[i].Key != w.key || opts[i].Label != w.label {
			t.Errorf("scope %d = %q/%q, want %q/%q", i, opts[i].Key, opts[i].Label, w.key, w.label)
		}
		if opts[i].Load == nil {
			t.Errorf("scope %q has no loader", w.key)
		}
	}
	// Each closure captures its own scope rather than sharing the loop variable —
	// otherwise every entry would read whichever range came last.
	if base := opts[0].Base(); base != "andrew/parent-change" {
		t.Errorf("stack-base scope names %q, want andrew/parent-change", base)
	}
	if base := opts[1].Base(); base != "" {
		t.Errorf("the working copy has no base to name, got %q", base)
	}
}

// The subject carries the workspace's own bookmark, which resolving the stack base
// has to exclude. Without it the nearest bookmarked ancestor of @ *is* the
// workspace's own bookmark, so the base resolved to the change itself and the
// default diff came back all but empty.
func TestDiffSubjectCarriesTheOwnBookmark(t *testing.T) {
	svc := listOnlyService{entries: []workspace.ListEntry{{
		Name: "pr-2336", Path: "/ws/pr-2336", PRNumber: 2336, Bookmark: "andrew/pr-2336",
	}}}
	item := diffSubjectFor(svc, "/src/alpha", "/ws/pr-2336/app")
	if item.Bookmark != "andrew/pr-2336" {
		t.Fatalf("Bookmark = %q, want the workspace's own", item.Bookmark)
	}
	if item.WorkspaceName != "pr-2336" || item.PRNumber != 2336 {
		t.Fatalf("unexpected subject: %+v", item)
	}
	// The workspace root, not wherever in it you were standing.
	if item.Path != "/ws/pr-2336" {
		t.Fatalf("Path = %q, want the workspace root", item.Path)
	}
}
