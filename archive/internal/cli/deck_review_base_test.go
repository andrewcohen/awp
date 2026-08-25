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
//
// atCursor is the bookmark sitting on @, if any. The fake has to model this much
// of jj because `trunk()..@` includes @, so whether the query excludes @ decides
// whether the change is offered as its own parent — which is the thing under
// test, not a string the query happens to contain.
//
// parentMoved makes the parent bookmark resolve to a commit that is not an
// ancestor of @ — a parent rewritten after @ branched off it — which is jj's
// "gaps in" case and the reason the resolver asks before returning a base.
type reviewBaseRunner struct {
	trunk       string
	parent      string
	atCursor    string
	parentMoved bool
	revs        []string
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
	case strings.HasSuffix(revset, "~ ::@"):
		if r.parentMoved {
			return "7a8f6e8b90f2\n", nil
		}
		return "\n", nil
	case strings.HasPrefix(revset, "heads("):
		// @ is in trunk()..@ and is the nearest thing to @ there is, so a bookmark on
		// it wins heads() outright — unless the query took @ out of the set.
		if r.atCursor != "" && !strings.Contains(revset, "~ @") {
			return r.atCursor + "\n", nil
		}
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
	stackRevset := stackQuery(t, r)
	// With no own bookmark, only the trunk exclusion is present by name.
	if strings.Count(stackRevset, "bookmarks(exact:") != 1 {
		t.Errorf("expected exactly one exact-exclusion (trunk), got %q", stackRevset)
	}
	// But @ is still excluded, and that is the exclusion doing the work here — see
	// TestResolveReviewStackBaseWillNotUseTheBookmarkAtTheCursor.
	if !strings.Contains(stackRevset, "~ @") {
		t.Errorf("the query does not exclude @, so a bookmarked @ becomes its own base: %q", stackRevset)
	}
}

// stackQuery is the last heads(...) revset the resolver asked jj for.
func stackQuery(t *testing.T, r *reviewBaseRunner) string {
	t.Helper()
	for i := len(r.revs) - 1; i >= 0; i-- {
		if strings.HasPrefix(r.revs[i], "heads(") {
			return r.revs[i]
		}
	}
	t.Fatalf("the resolver never asked for a stack parent: %v", r.revs)
	return ""
}

// A change must not be its own base.
//
// The failure: in a repo's default workspace, `trunk()..@` was one commit — @
// itself, carrying a bookmark — so the "nearest stacked parent" query answered
// with the bookmark at @, the diff was that change against itself, and the
// viewer opened on "no changes" with the footer confidently naming a base.
//
// It only ever showed up in the default workspace because everywhere else the
// workspace's own bookmark is excluded by name, which happened to remove @ too.
// The default workspace never gets a recorded bookmark, so the guard that was
// doing the work simply was not there. Excluding @ cannot be forgotten that way.
func TestResolveReviewStackBaseWillNotUseTheBookmarkAtTheCursor(t *testing.T) {
	// Both spellings of the workspace: one with its bookmark recorded, one without.
	// The second is the default workspace, and is where this actually bit.
	for _, own := range []string{"", "andrew/hil-spec"} {
		r := &reviewBaseRunner{trunk: "main", atCursor: "andrew/hil-spec"}
		if got := resolveReviewStackBase(r, "/p/alpha", own); got != "trunk()" {
			t.Errorf("own=%q: base = %q, want trunk() — the change is being diffed against itself", own, got)
		}
	}
}

// And a change genuinely stacked on another still gets its parent: excluding @
// must not flatten every review to trunk.
func TestResolveReviewStackBaseStillFindsARealParent(t *testing.T) {
	r := &reviewBaseRunner{trunk: "main", atCursor: "andrew/child", parent: "andrew/parent"}
	if got := resolveReviewStackBase(r, "/ws/child", "andrew/child"); got != "andrew/parent" {
		t.Fatalf("base = %q, want andrew/parent", got)
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
	// Three ranges and the revision picker.
	if len(opts) != 4 {
		t.Fatalf("expected three ranges plus the revision picker, got %d", len(opts))
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
	// The revision picker is last, and it is a submenu rather than a range: it has
	// choices to resolve and no loader of its own, because which revision it reads
	// is the question it exists to ask. Last because it is the one that asks a
	// second question, and because the three ranges all end at @ — so it is the
	// only entry that reaches a change that has already landed.
	pick := opts[len(opts)-1]
	if pick.Key != "r" {
		t.Errorf("the last scope is %q, want the revision picker on r", pick.Key)
	}
	if pick.Choices == nil {
		t.Error("the revision picker has no choices to resolve")
	}
	if pick.Load != nil {
		t.Error("the revision picker should have no loader of its own — it stands for whichever revision is picked")
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

// A base is picked as a name, and the name is resolved again when the diff is
// read. A parent rewritten in between resolves to a commit @ never descended
// from, so `parent..@` has a gap in it and jj refuses the whole call:
//
//	load diff: "jj" exited 1: Error: Cannot diff revsets with gaps in.
//	Hint: Revision 7a8f6e8b90f2 would need to be in the set.
//
// Pressing `c` then failed outright rather than falling back — a review that
// cannot be opened at all, for a base awp chose itself.
func TestResolveReviewStackBaseWillNotReturnABaseWithNoRangeToAt(t *testing.T) {
	r := &reviewBaseRunner{trunk: "main", parent: "andrew/parent", parentMoved: true}
	if got := resolveReviewStackBase(r, "/ws/child", "andrew/child"); got != "trunk()" {
		t.Fatalf("base = %q, want trunk() — `%s..@` is a range jj will not diff", got, got)
	}
}

// And the label follows the base it fell back to: naming the rewritten parent
// while diffing against trunk is the footer confidently describing another diff.
func TestResolveReviewStackBaseNamedFallsBackWithTrunksName(t *testing.T) {
	r := &reviewBaseRunner{trunk: "main", parent: "andrew/parent", parentMoved: true}
	revset, label := resolveReviewStackBaseNamed(r, "/ws/child", "andrew/child")
	if revset != "trunk()" || label != "main" {
		t.Fatalf("base = %q labelled %q, want trunk() labelled main", revset, label)
	}
}

// A parent still in @'s ancestry is used, checked and all: the guard must not
// flatten every stacked review to trunk.
func TestResolveReviewStackBaseKeepsAConnectedParent(t *testing.T) {
	r := &reviewBaseRunner{trunk: "main", parent: "andrew/parent"}
	if got := resolveReviewStackBase(r, "/ws/child", "andrew/child"); got != "andrew/parent" {
		t.Fatalf("base = %q, want andrew/parent", got)
	}
	var asked bool
	for _, rv := range r.revs {
		if strings.HasSuffix(rv, "~ ::@") {
			asked = true
		}
	}
	if !asked {
		t.Fatalf("the resolver never checked the range exists: %v", r.revs)
	}
}
