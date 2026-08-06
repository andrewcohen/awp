package cli

import (
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/github"
	"github.com/andrewcohen/awp/internal/review"
)

// A file that removes two lines and adds three, so the two sides advance at
// different rates — which is the whole reason the patch body is walked rather than
// only its hunk headers.
//
//	old  new
//	 10   20   context
//	 11   --   removed
//	 12   --   removed
//	 --   21   added
//	 --   22   added
//	 --   23   added
//	 13   24   context
const mixedPatch = "@@ -10,4 +20,5 @@\n" +
	" context one\n" +
	"-gone one\n" +
	"-gone two\n" +
	"+added one\n" +
	"+added two\n" +
	"+added three\n" +
	" context two\n"

func mixedFiles() commentableLines {
	return parseCommentable([]github.PRFile{{Filename: "a.go", Patch: mixedPatch}})
}

func TestParseCommentableTracksBothSidesSeparately(t *testing.T) {
	c := mixedFiles()
	// New side: the context lines and the three additions.
	for _, line := range []int{20, 21, 22, 23, 24} {
		if !c.right["a.go"][line] {
			t.Errorf("expected new-side line %d commentable", line)
		}
	}
	if c.right["a.go"][25] {
		t.Error("line 25 is past the hunk and must not be commentable")
	}
	// Old side: the context lines and the two removals. It stops at 13 because the
	// additions do not advance the old numbering — the bug a header-only parse makes.
	for _, line := range []int{10, 11, 12, 13} {
		if !c.left["a.go"][line] {
			t.Errorf("expected old-side line %d commentable", line)
		}
	}
	if c.left["a.go"][14] {
		t.Error("old-side line 14 is past the hunk")
	}
	// An added line exists only on the new side; a removed one only on the old.
	if c.left["a.go"][21] && c.right["a.go"][11] {
		t.Error("the two sides must not share numbering")
	}
}

// A context line is commentable: GitHub's rule is "in the diff", not "changed".
func TestContextLinesAreCommentable(t *testing.T) {
	c := mixedFiles()
	v := c.checkAnchor(review.Anchor{Path: "a.go", Side: review.SideNew, LineHint: 20})
	if v.State != anchorOK {
		t.Fatalf("expected a context line accepted, got %v (%s)", v.State, v.Note)
	}
}

func TestCheckAnchorRejectsALineOutsideTheDiff(t *testing.T) {
	c := mixedFiles()
	v := c.checkAnchor(review.Anchor{Path: "a.go", Side: review.SideNew, LineHint: 99})
	if v.State != anchorMissingLine {
		t.Fatalf("expected the line rejected, got %v", v.State)
	}
	if !strings.Contains(v.Note, "99") || !strings.Contains(v.Note, "not in the diff") {
		t.Fatalf("expected the note to name the line, got %q", v.Note)
	}
	if !v.blocks() {
		t.Fatal("a line that is definitely not in the diff must block the run")
	}
}

func TestCheckAnchorRejectsAFileOutsideTheDiff(t *testing.T) {
	c := mixedFiles()
	v := c.checkAnchor(review.Anchor{Path: "elsewhere.go", Side: review.SideNew, LineHint: 1})
	if v.State != anchorMissingFile {
		t.Fatalf("expected the file rejected, got %v", v.State)
	}
	if !strings.Contains(v.Note, "not in the PR's diff") {
		t.Fatalf("unexpected note %q", v.Note)
	}
}

// GitHub requires a range to sit inside one hunk, so every line between the ends
// has to be in the diff — not just the ends themselves. A range that straddles the
// gap between two hunks covers lines that are in no diff at all.
func TestCheckAnchorChecksEveryLineOfARange(t *testing.T) {
	two := parseCommentable([]github.PRFile{{Filename: "a.go", Patch: "" +
		"@@ -1,2 +1,2 @@\n+one\n+two\n" +
		"@@ -50,2 +50,2 @@\n+fifty\n+fifty-one\n"}})
	// Both ends are in the diff; the 47 lines between them are not.
	v := two.checkAnchor(review.Anchor{
		Path: "a.go", Side: review.SideNew, LineHint: 1, EndLineHint: 51,
	})
	if v.State == anchorOK {
		t.Fatal("expected a range spanning two hunks refused")
	}
	if !strings.Contains(v.Note, "of 1-51") {
		t.Fatalf("expected the note to name the range, got %q", v.Note)
	}
	// A range inside one hunk is fine.
	if got := two.checkAnchor(review.Anchor{
		Path: "a.go", Side: review.SideNew, LineHint: 1, EndLineHint: 2,
	}); got.State != anchorOK {
		t.Fatalf("expected a single-hunk range accepted, got %v (%s)", got.State, got.Note)
	}
}

// A review-level remark has no line to check — it becomes the review's body.
func TestCheckAnchorPassesAReviewLevelRemark(t *testing.T) {
	if got := mixedFiles().checkAnchor(review.Anchor{}); got.State != anchorOK {
		t.Fatalf("expected a review-level remark to pass, got %v", got.State)
	}
}

// A remark about the whole file is checked for its file and nothing else.
//
// The failure this closes: a file-scoped anchor has no LineHint, so it fell into
// the line walk with first == last == 0 and came back "line 0 is not in the
// diff" — a refusal naming a line the author never chose. partitionForPublish
// currently routes file-scoped comments away from the preflight entirely to
// avoid it, which is a bypass rather than an answer; sending them needs the same
// check every other anchor gets.
func TestCheckAnchorChecksAFileScopedRemarkAgainstItsFile(t *testing.T) {
	c := mixedFiles()
	fileScoped := review.Anchor{Path: "a.go", Side: review.SideNew}
	if got := fileScoped.Scope(); got != review.FileScope {
		t.Fatalf("the fixture is wrong: scope is %v, want FileScope", got)
	}
	v := c.checkAnchor(fileScoped)
	if v.State != anchorOK {
		t.Fatalf("a remark about a file in the diff was refused: %v (%s)", v.State, v.Note)
	}
	if strings.Contains(v.Note, "line") {
		t.Errorf("the verdict talks about a line the author never chose: %q", v.Note)
	}
}

// And it is still refused when the file itself is not in the PR.
func TestCheckAnchorRejectsAFileScopedRemarkOnAFileOutsideTheDiff(t *testing.T) {
	v := mixedFiles().checkAnchor(review.Anchor{Path: "elsewhere.go", Side: review.SideNew})
	if v.State != anchorMissingFile {
		t.Fatalf("expected the file rejected, got %v (%s)", v.State, v.Note)
	}
	// The same phrase a line-scoped remark on that file would get: a reviewer reading
	// the preview is being told one thing, and it does not depend on what the remark
	// was about.
	if !strings.Contains(v.Note, "not in the PR's diff") {
		t.Errorf("unexpected note %q", v.Note)
	}
}

// A file present only on one side still takes a whole-file remark. It has no line,
// so it is not about a side, and refusing it for the side it did not pick would be
// refusing a question it never asked.
func TestAFileScopedRemarkIgnoresTheSide(t *testing.T) {
	// Added file: new side only.
	c := parseCommentable([]github.PRFile{{Filename: "new.go", Patch: "@@ -0,0 +1,2 @@\n+one\n+two\n"}})
	for _, side := range []review.Side{review.SideNew, review.SideOld} {
		if got := c.checkAnchor(review.Anchor{Path: "new.go", Side: side}); got.State != anchorOK {
			t.Errorf("side %v: a whole-file remark was refused: %v (%s)", side, got.State, got.Note)
		}
	}
	// A line-scoped remark on the missing side is still refused, and still says the
	// side is what is wrong rather than blaming the line.
	v := c.checkAnchor(review.Anchor{Path: "new.go", Side: review.SideOld, LineHint: 1})
	if v.State != anchorMissingLine || !strings.Contains(v.Note, "side") {
		t.Errorf("a LEFT line on an added file gave %v (%q)", v.State, v.Note)
	}
}

// A binary or over-large file comes back with no patch. That is "cannot tell", not
// "not in the diff", and it must never block a publish GitHub would have accepted.
func TestAFileWithNoPatchCannotBeChecked(t *testing.T) {
	c := parseCommentable([]github.PRFile{{Filename: "logo.png", Patch: ""}})
	v := c.checkAnchor(review.Anchor{Path: "logo.png", Side: review.SideNew, LineHint: 1})
	if v.State != anchorUnknown {
		t.Fatalf("expected unknown, got %v", v.State)
	}
	if v.blocks() {
		t.Fatal("a check that cannot tell must not block the run")
	}
	if !strings.Contains(v.Note, "cannot check") {
		t.Fatalf("expected the note to say so, got %q", v.Note)
	}
}

// A LEFT comment on a file that was only added has no old side to sit on.
func TestALeftAnchorOnAnAddedFileIsRefused(t *testing.T) {
	c := parseCommentable([]github.PRFile{{Filename: "new.go", Patch: "@@ -0,0 +1,2 @@\n+one\n+two\n"}})
	v := c.checkAnchor(review.Anchor{Path: "new.go", Side: review.SideOld, LineHint: 1})
	if v.State != anchorMissingLine {
		t.Fatalf("expected the LEFT anchor refused, got %v (%s)", v.State, v.Note)
	}
	if !strings.Contains(v.Note, "LEFT") {
		t.Fatalf("expected the note to name the side, got %q", v.Note)
	}
	// The same file's new side is fine.
	if got := c.checkAnchor(review.Anchor{Path: "new.go", Side: review.SideNew, LineHint: 2}); got.State != anchorOK {
		t.Fatalf("expected the new side accepted, got %v (%s)", got.State, got.Note)
	}
}

// An answer with no readable filename in it teaches us nothing, so the check
// declines to run rather than refusing every anchor.
func TestPreflightDeclinesWhenItLearnedNothing(t *testing.T) {
	inline := []review.Comment{{Anchor: review.Anchor{Path: "a.go", LineHint: 1}}}
	for _, files := range [][]github.PRFile{
		nil,
		{{Filename: "", Patch: "@@ -1 +1 @@\n+x\n"}},
	} {
		if got := preflight(inline, parseCommentable(files)); got != nil {
			t.Fatalf("expected the check to decline, got %#v", got)
		}
	}
}

// blockedAnchors names the target and the reason, per comment, so the refusal is a
// list you can act on rather than GitHub's first complaint.
func TestBlockedAnchorsNamesEachOne(t *testing.T) {
	inline := []review.Comment{
		{Anchor: review.Anchor{Path: "a.go", Side: review.SideNew, LineHint: 20}},
		{Anchor: review.Anchor{Path: "a.go", Side: review.SideNew, LineHint: 99}},
		{Anchor: review.Anchor{Path: "gone.go", Side: review.SideNew, LineHint: 1}},
	}
	blocked := blockedAnchors(inline, preflight(inline, mixedFiles()))
	if len(blocked) != 2 {
		t.Fatalf("expected the two bad anchors, got %v", blocked)
	}
	if !strings.Contains(blocked[0], "a.go:99") || !strings.Contains(blocked[1], "gone.go:1") {
		t.Fatalf("expected each named by its target, got %v", blocked)
	}
}

// The hunk header's two starting numbers, including the shapes with no count.
func TestParseHunkStarts(t *testing.T) {
	cases := []struct {
		header             string
		oldStart, newStart int
		ok                 bool
	}{
		{"@@ -10,4 +20,5 @@ func foo() {", 10, 20, true},
		{"@@ -1 +1 @@", 1, 1, true},
		// A file created from nothing starts the old side at 0; clamped to 1 so the
		// walk numbers from a real line.
		{"@@ -0,0 +1,3 @@", 1, 1, true},
		{"not a hunk header", 0, 0, false},
		{"@@ garbage @@", 0, 0, false},
	}
	for _, c := range cases {
		o, n, ok := parseHunkStarts(c.header)
		if ok != c.ok || (ok && (o != c.oldStart || n != c.newStart)) {
			t.Errorf("parseHunkStarts(%q) = %d, %d, %v; want %d, %d, %v",
				c.header, o, n, ok, c.oldStart, c.newStart, c.ok)
		}
	}
}
