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
		if !c.right["a.go"].lines[line] {
			t.Errorf("expected new-side line %d commentable", line)
		}
	}
	if c.right["a.go"].lines[25] {
		t.Error("line 25 is past the hunk and must not be commentable")
	}
	// Old side: the context lines and the two removals. It stops at 13 because the
	// additions do not advance the old numbering — the bug a header-only parse makes.
	for _, line := range []int{10, 11, 12, 13} {
		if !c.left["a.go"].lines[line] {
			t.Errorf("expected old-side line %d commentable", line)
		}
	}
	if c.left["a.go"].lines[14] {
		t.Error("old-side line 14 is past the hunk")
	}
	// An added line exists only on the new side; a removed one only on the old.
	if c.left["a.go"].lines[21] && c.right["a.go"].lines[11] {
		t.Error("the two sides must not share numbering")
	}
}

// The content index is built from the same walk as the line set, so a line's
// number and its text agree by construction — that is the whole reason they share
// a struct. Indexed per side: "gone one" is a line on the old side and nowhere on
// the new, and finding it on the new side would relocate a comment onto a line
// that does not exist there.
func TestParseCommentableIndexesEachSideByContent(t *testing.T) {
	c := mixedFiles()
	if got, ok := c.right["a.go"].lineOf("added two"); !ok || got != 22 {
		t.Errorf(`new side: lineOf("added two") = %d, %v; want 22, true`, got, ok)
	}
	if got, ok := c.left["a.go"].lineOf("gone two"); !ok || got != 12 {
		t.Errorf(`old side: lineOf("gone two") = %d, %v; want 12, true`, got, ok)
	}
	if _, ok := c.right["a.go"].lineOf("gone two"); ok {
		t.Error("a removed line was found on the new side")
	}
	// A context line is on both, at each side's own number.
	if got, _ := c.left["a.go"].lineOf("context one"); got != 10 {
		t.Errorf("old-side context line is at %d, want 10", got)
	}
	if got, _ := c.right["a.go"].lineOf("context one"); got != 20 {
		t.Errorf("new-side context line is at %d, want 20", got)
	}
}

// Two lines saying the same thing cannot locate anything, and neither can a blank
// one. Both are refusals rather than a nearest-match guess: relocating onto the
// wrong one of three identical lines would be wrong silently, on someone else's PR.
func TestLineOfRefusesWhatItCannotTellApart(t *testing.T) {
	c := parseCommentable([]github.PRFile{{Filename: "a.go", Patch: "" +
		"@@ -1,4 +1,4 @@\n+\tdefer f.Close()\n+\n+one\n+\tdefer f.Close()\n"}})
	side := c.right["a.go"]
	if got, ok := side.lineOf("defer f.Close()"); ok {
		t.Errorf("two identical lines resolved to %d", got)
	}
	if _, ok := side.lineOf("   "); ok {
		t.Error("a blank line resolved to something")
	}
	// A line that is unique still resolves, so the fixture is not simply broken. It is
	// found by its trimmed text, which is what lets a reindented line be found at all.
	if got, ok := side.lineOf("one"); !ok || got != 3 {
		t.Errorf(`lineOf("one") = %d, %v; want 3, true`, got, ok)
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
		sending, got := preflight(inline, parseCommentable(files))
		if got != nil {
			t.Fatalf("expected the check to decline, got %#v", got)
		}
		// And the threads come back as they went in. With no knowledge of the diff
		// there is nothing to relocate against, so a check that declined must not have
		// moved anything on its way to declining.
		if len(sending) != 1 || sending[0].Anchor.LineHint != inline[0].Anchor.LineHint {
			t.Fatalf("the threads were altered by a check that did not run: %+v", sending)
		}
	}
}

// A comment whose line has drifted is published against the line its text sits on
// now, rather than refused.
//
// The store keeps the number a comment was filed against, and the agent goes on
// editing the file underneath it — a finding filed at error-boundary.tsx:47 came
// back from GitHub reported at 53. The viewer already relocates by text at render
// time, so the reviewer sees it in the right place locally and has no reason to
// expect the publish to disagree.
func TestAStaleAnchorIsRelocatedByItsText(t *testing.T) {
	c := mixedFiles()
	// "added two" is on the new side at 22; the comment still claims 99, which is
	// past the hunk and so not commentable at all.
	v := c.checkAnchor(review.Anchor{
		Path: "a.go", Side: review.SideNew, LineHint: 99, Text: "added two",
	})
	if v.State != anchorOK {
		t.Fatalf("expected the stale anchor relocated, got %v (%s)", v.State, v.Note)
	}
	if v.Anchor.LineHint != 22 {
		t.Errorf("sent against line %d, want 22", v.Anchor.LineHint)
	}
	// The reviewer is told, on the thread it happened to. They are about to confirm an
	// irreversible send, and the comment is not going where they filed it.
	if !strings.Contains(v.Note, "99") || !strings.Contains(v.Note, "22") {
		t.Errorf("the note does not say where it moved from and to: %q", v.Note)
	}
	if v.blocks() {
		t.Error("a relocated anchor must not block the run")
	}
	// And it is not dressed as a failure: the plan's ⚠ means "this run will be
	// refused", and a relocation is the opposite of that.
	if v.mark() == "⚠" {
		t.Error("a relocation is marked as a refusal")
	}
}

// The dangerous kind of stale hint: one that is still in the diff, so GitHub takes
// it without complaint, but no longer names the code the comment was about.
//
// The line check cannot see this — "in the diff" and "still the right code" are
// different facts, and only the first is what GitHub validates. Left alone, the
// remark publishes silently onto whatever moved into that position, which is worse
// than the refusal the not-commentable case gets: nothing anywhere says it
// happened.
func TestAHintOnTheWrongCodeIsRelocated(t *testing.T) {
	c := mixedFiles()
	// 21 is a real commentable line — it is "added one". The comment was written
	// about "added three", which now sits at 23.
	v := c.checkAnchor(review.Anchor{
		Path: "a.go", Side: review.SideNew, LineHint: 21, Text: "added three",
	})
	if v.State != anchorOK {
		t.Fatalf("expected the anchor accepted, got %v (%s)", v.State, v.Note)
	}
	if v.Anchor.LineHint != 23 {
		t.Errorf("sent against line %d, want 23 — the line its text is on", v.Anchor.LineHint)
	}
	if !strings.Contains(v.Note, "21 → 23") {
		t.Errorf("the move was not reported: %q", v.Note)
	}
}

// An anchor with no text recorded is trusted as filed. There is nothing to check
// it against, and a record written before the text was carried must not be read as
// evidence that its line is wrong.
func TestAnAnchorWithNoTextKeepsItsLine(t *testing.T) {
	v := mixedFiles().checkAnchor(review.Anchor{Path: "a.go", Side: review.SideNew, LineHint: 21})
	if v.State != anchorOK || v.Anchor.LineHint != 21 {
		t.Fatalf("got %v at line %d (%s), want anchorOK at 21", v.State, v.Anchor.LineHint, v.Note)
	}
	if v.Note != "" {
		t.Errorf("an anchor that could not be second-guessed reported %q", v.Note)
	}
}

// A hint on the wrong code whose text cannot be placed keeps its line and goes up
// as filed. Refusing it would be a new refusal for a comment GitHub accepts, over a
// suspicion the check cannot resolve — and the local store is not wrong enough to
// justify blocking a review the reviewer has finished writing.
func TestAnUnplaceableHintIsStillSentAsFiled(t *testing.T) {
	c := mixedFiles()
	v := c.checkAnchor(review.Anchor{
		Path: "a.go", Side: review.SideNew, LineHint: 21, Text: "nothing in this diff says this",
	})
	if v.State != anchorOK || v.blocks() {
		t.Fatalf("expected it sent as filed, got %v (%s)", v.State, v.Note)
	}
	if v.Anchor.LineHint != 21 {
		t.Errorf("the anchor moved to %d", v.Anchor.LineHint)
	}
}

// Both ends of a range are located independently, the same way the viewer does it:
// an edit inside a comment's range moves its end and not its start.
func TestARangeRelocatesBothEnds(t *testing.T) {
	c := mixedFiles()
	v := c.checkAnchor(review.Anchor{
		Path: "a.go", Side: review.SideNew,
		LineHint: 40, Text: "added one",
		EndLineHint: 42, EndText: "added three",
	})
	if v.State != anchorOK {
		t.Fatalf("expected the range relocated, got %v (%s)", v.State, v.Note)
	}
	if v.Anchor.LineHint != 21 || v.Anchor.EndLineHint != 23 {
		t.Fatalf("the range landed at %d-%d, want 21-23", v.Anchor.LineHint, v.Anchor.EndLineHint)
	}
	if !strings.Contains(v.Note, "40-42") || !strings.Contains(v.Note, "21-23") {
		t.Errorf("the note does not name both ranges: %q", v.Note)
	}
}

// Relocation is attempted only once the filed line has failed. A hint that still
// lands in the diff is the answer — searching for text that also appears elsewhere
// would turn a good anchor into an ambiguous one and refuse it.
func TestAGoodAnchorIsNotSecondGuessed(t *testing.T) {
	// Two identical lines, and the comment is on the second of them. By text alone
	// that is ambiguous, which relocate refuses.
	c := parseCommentable([]github.PRFile{{Filename: "a.go", Patch: "" +
		"@@ -1,3 +1,3 @@\n+\tdefer f.Close()\n+middle\n+\tdefer f.Close()\n"}})
	v := c.checkAnchor(review.Anchor{
		Path: "a.go", Side: review.SideNew, LineHint: 3, Text: "\tdefer f.Close()",
	})
	if v.State != anchorOK {
		t.Fatalf("a line that is in the diff was refused: %v (%s)", v.State, v.Note)
	}
	if v.Anchor.LineHint != 3 {
		t.Errorf("the anchor moved to %d; it was already right", v.Anchor.LineHint)
	}
	if v.Note != "" {
		t.Errorf("an anchor that did not move reported %q", v.Note)
	}
}

// Text the file no longer carries, or carries more than once, stays refused —
// with the original complaint, not a new one about the text. The reviewer's move
// is the same either way: go and look at the anchor.
func TestAnAnchorThatCannotBePlacedStaysRefused(t *testing.T) {
	c := parseCommentable([]github.PRFile{{Filename: "a.go", Patch: "" +
		"@@ -1,3 +1,3 @@\n+\tdefer f.Close()\n+middle\n+\tdefer f.Close()\n"}})
	for name, a := range map[string]review.Anchor{
		"gone":      {Path: "a.go", Side: review.SideNew, LineHint: 99, Text: "nothing like this"},
		"ambiguous": {Path: "a.go", Side: review.SideNew, LineHint: 99, Text: "\tdefer f.Close()"},
		"no text":   {Path: "a.go", Side: review.SideNew, LineHint: 99},
	} {
		v := c.checkAnchor(a)
		if v.State != anchorMissingLine || !v.blocks() {
			t.Errorf("%s: expected the anchor refused, got %v (%s)", name, v.State, v.Note)
		}
		if !strings.Contains(v.Note, "99") {
			t.Errorf("%s: the refusal should name the line the comment claims: %q", name, v.Note)
		}
		if v.Anchor.LineHint != 99 {
			t.Errorf("%s: a refused anchor was moved to %d", name, v.Anchor.LineHint)
		}
	}
}

// A comment relocated by the check is the comment the run sends. The preview and
// the mutation read one slice, which is what makes it safe to move a comment at
// all — the reviewer confirms the send having been shown where it is going.
func TestPreflightHandsBackTheThreadsAsTheyWillBeSent(t *testing.T) {
	threads := []review.Comment{
		{ID: "moved", Anchor: review.Anchor{
			Path: "a.go", Side: review.SideNew, LineHint: 99, Text: "added two"}},
		{ID: "fine", Anchor: review.Anchor{
			Path: "a.go", Side: review.SideNew, LineHint: 20, Text: "context one"}},
	}
	sending, verdicts := preflight(threads, mixedFiles())
	if len(sending) != 2 || len(verdicts) != 2 {
		t.Fatalf("got %d threads and %d verdicts, want 2 of each", len(sending), len(verdicts))
	}
	if sending[0].Anchor.LineHint != 22 {
		t.Errorf("the relocated thread would be sent to line %d, want 22", sending[0].Anchor.LineHint)
	}
	if sending[1].Anchor.LineHint != 20 {
		t.Errorf("the good thread moved to %d", sending[1].Anchor.LineHint)
	}
	// The caller's own slice is untouched, so a dry run cannot leave the reviewer's
	// comments rewritten behind its back.
	if threads[0].Anchor.LineHint != 99 {
		t.Errorf("preflight wrote through to the caller's slice: %d", threads[0].Anchor.LineHint)
	}
	if blocked := blockedAnchors(sending, verdicts); len(blocked) > 0 {
		t.Errorf("the run would be refused: %v", blocked)
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
	sending, verdicts := preflight(inline, mixedFiles())
	blocked := blockedAnchors(sending, verdicts)
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
