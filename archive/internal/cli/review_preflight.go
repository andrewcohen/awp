package cli

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/andrewcohen/awp/internal/github"
	"github.com/andrewcohen/awp/internal/review"
)

// Checking anchors against the PR's own diff before anything is sent.
//
// GitHub accepts a review comment only on a line that is part of the diff, and it
// says so as a 422 per comment — a wall of validation errors after the fact, with
// the offending line named in GitHub's terms rather than yours. Worse now that the
// whole review goes up as one atomic mutation: one bad anchor fails every comment
// with it, and the message names the first problem only.
//
// So the check moves to the front. `pulls/{n}/files` carries the patch GitHub will
// validate against, which is exactly the question being asked — a local read can
// disagree with it over a merge base that moved or a file GitHub decided was too
// large to include. The preview then gives every thread a verdict, and a run
// refuses rather than sending an anchor nobody verified.
//
// It refuses only when the check is *confident*. A files fetch that failed, or a
// file whose patch GitHub omitted, means "cannot tell" — and a check that cannot
// tell must not block a publish that would have worked. GitHub arbitrates in that
// case, exactly as it did before.

// commentableLines is the set of positions GitHub will accept a review comment on,
// per side.
//
// Both sides are recorded because a comment carries its own: a remark on a removed
// line is anchored LEFT against the old numbering, and checking it against the new
// side would reject it for being absent from a file it was never in.
type commentableLines struct {
	// right and left map a path to what the patch says about that side of it.
	right map[string]diffSide
	left  map[string]diffSide
	// unknown holds paths the check has nothing to say about: a file GitHub
	// included with no patch (binary, or too large). Distinguished from a path that
	// is simply absent from the diff, which is a real finding.
	unknown map[string]bool
}

// diffSide is everything the check knows about one side of one file's patch:
// which lines are in the diff, and which line a given piece of content sits on.
//
// The two facts live in one value rather than in a second pair of path-keyed maps
// beside right and left, because they are always wanted together and always about
// the same side. Kept apart, a caller could take membership from the new side and
// content from the old and get a coherent-looking answer about nothing.
type diffSide struct {
	// lines is the line numbers commentable on this side.
	lines map[int]bool
	// byText maps a line's content to every line carrying it, which is how a
	// comment whose number has drifted is found again (see relocate). A slice
	// rather than a single line: two identical lines is the case that must be
	// refused, and it can only be recognised by having counted them.
	byText map[string][]int
}

func newDiffSide() diffSide {
	return diffSide{lines: map[int]bool{}, byText: map[string][]int{}}
}

// add records a line of the patch and what it says.
//
// Content is indexed under its trimmed form. Trimming is what lets a line the
// agent reindented still be found — and it can only ever turn one match into
// several, which relocate refuses, so it cannot move a comment somewhere it does
// not belong. A line with nothing but whitespace on it is not indexed at all:
// blank lines have no identity, and matching one would locate a comment by an
// accident of the file's shape.
func (s diffSide) add(line int, content string) {
	s.lines[line] = true
	if key := strings.TrimSpace(content); key != "" {
		s.byText[key] = append(s.byText[key], line)
	}
}

// lineOf is the line this content occupies now, when exactly one line carries it.
//
// Ambiguity is a refusal rather than a guess. Picking the nearest of three
// identical lines would be right often enough to be trusted and wrong silently,
// which is worse than saying the anchor cannot be placed.
func (s diffSide) lineOf(text string) (int, bool) {
	key := strings.TrimSpace(text)
	if key == "" {
		return 0, false
	}
	at := s.byText[key]
	if len(at) != 1 {
		return 0, false
	}
	return at[0], true
}

// side is the half of the diff an anchor is about. One selector, because the two
// questions asked of a side — is this line in the diff, and where is this text —
// have to be asked of the same one.
func (c commentableLines) side(s review.Side) map[string]diffSide {
	if s == review.SideOld {
		return c.left
	}
	return c.right
}

// parseCommentable reads the per-file patches into the commentable sets.
//
// The patch body is walked rather than only its hunk headers, because the two
// sides advance at different rates: a run of added lines moves the new-side number
// and not the old one. Walking is also what makes context lines commentable, which
// they are — GitHub's rule is "in the diff", not "changed".
func parseCommentable(files []github.PRFile) commentableLines {
	out := commentableLines{
		right:   map[string]diffSide{},
		left:    map[string]diffSide{},
		unknown: map[string]bool{},
	}
	for _, f := range files {
		path := strings.TrimSpace(f.Filename)
		if path == "" {
			continue
		}
		if strings.TrimSpace(f.Patch) == "" {
			// In the PR, but GitHub told us nothing about which lines are commentable.
			// Recorded so the check reports "cannot tell" rather than "not in the diff".
			out.unknown[path] = true
			continue
		}
		right, left := newDiffSide(), newDiffSide()
		out.right[path], out.left[path] = right, left
		oldLine, newLine := 0, 0
		body := strings.Split(f.Patch, "\n")
		// A patch usually ends with a newline, and splitting on it leaves a trailing
		// empty element. Treated as a line it invented one past the end of the last
		// hunk on both sides, which is exactly the kind of phantom that makes a bad
		// anchor look fine.
		if n := len(body); n > 0 && body[n-1] == "" {
			body = body[:n-1]
		}
		for _, raw := range body {
			if strings.HasPrefix(raw, "@@") {
				o, n, ok := parseHunkStarts(raw)
				if !ok {
					continue
				}
				oldLine, newLine = o, n
				continue
			}
			if oldLine == 0 && newLine == 0 {
				// Before the first hunk header there is nothing to number against.
				continue
			}
			// An empty line inside a patch is a context line whose content is empty; the
			// leading space is often stripped in transit, so it is treated as context
			// rather than skipped.
			marker, content := byte(' '), ""
			if raw != "" {
				marker, content = raw[0], raw[1:]
			}
			switch marker {
			case '+':
				right.add(newLine, content)
				newLine++
			case '-':
				left.add(oldLine, content)
				oldLine++
			case ' ':
				right.add(newLine, content)
				left.add(oldLine, content)
				oldLine++
				newLine++
			default:
				// "\ No newline at end of file" and anything else GitHub emits: no line of
				// its own, so neither counter moves.
			}
		}
	}
	return out
}

// parseHunkStarts reads the two starting line numbers out of `@@ -a,b +c,d @@`.
func parseHunkStarts(header string) (oldStart, newStart int, ok bool) {
	fields := strings.Fields(header)
	if len(fields) < 3 || !strings.HasPrefix(fields[1], "-") || !strings.HasPrefix(fields[2], "+") {
		return 0, 0, false
	}
	first := func(s string) int {
		if i := strings.IndexByte(s, ','); i >= 0 {
			s = s[:i]
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return 0
		}
		return n
	}
	oldStart, newStart = first(fields[1][1:]), first(fields[2][1:])
	if oldStart <= 0 && newStart <= 0 {
		return 0, 0, false
	}
	// A hunk that adds to an empty file starts the old side at 0, and vice versa.
	// Clamped to 1 so the walk numbers from a real line either way.
	return max(oldStart, 1), max(newStart, 1), true
}

// hasFile reports whether the PR's diff touches this path at all, on either
// side. Distinct from "the path has commentable lines on the side I want": a
// file that was only added is in the diff but has no old side, and those two
// answers want different words in the preview.
func (c commentableLines) hasFile(path string) bool {
	if _, ok := c.right[path]; ok {
		return true
	}
	_, ok := c.left[path]
	return ok
}

// anchorState is what the check concluded about one comment.
type anchorState int

const (
	// anchorOK means every line the comment covers is in the diff.
	anchorOK anchorState = iota
	// anchorUnknown means the check could not tell — no patch for the file, or the
	// files fetch failed. Never blocks a publish.
	anchorUnknown
	// anchorMissingFile means the path is not in the PR's diff at all.
	anchorMissingFile
	// anchorMissingLine means the file is in the diff but the anchored line is not.
	anchorMissingLine
)

// anchorVerdict is the check's answer for one comment, with a phrase for the
// preview.
type anchorVerdict struct {
	State anchorState
	// Note is what the preview shows next to the thread, empty when it is fine.
	Note string
	// Anchor is where the comment should actually be sent — the filed anchor,
	// except when its line had drifted and the text located a new one.
	//
	// Carried on the verdict rather than worked out again at send time. The
	// preview and the mutation have to name the same line, and the only way to
	// guarantee that is for there to be one answer that both read (see preflight,
	// which hands back the threads as they will be sent).
	Anchor review.Anchor
}

// blocks reports whether this verdict should stop the run. Only a definite
// negative does: a check that cannot tell must not refuse a publish GitHub would
// have accepted.
func (v anchorVerdict) blocks() bool {
	return v.State == anchorMissingFile || v.State == anchorMissingLine
}

// mark is what the note is prefixed with in the publish preview.
//
// Two marks, because notes now say two different kinds of thing. A relocated
// anchor is a success with a detail worth reading; an anchor not in the diff is
// the reason the whole run will be refused. Under one glyph the reviewer could see
// that a refusal existed — the plan's first line counts them — without being able
// to tell which row it was.
func (v anchorVerdict) mark() string {
	if v.blocks() {
		return "⚠"
	}
	return "·"
}

// checkAnchor decides whether GitHub will accept this comment's anchor, and says
// which anchor it decided about.
//
// What there is to check differs by scope, and the line walk is only one of the
// three answers — so the scope is asked once, up front, rather than inferred
// from which fields happen to be filled in at each step.
//
// A line whose number no longer lands in the diff is not refused outright. The
// store keeps the number a comment was filed against, and awp's own agent edits
// files while you are reading them, so a hint going stale is the normal case
// rather than a mistake — the text is what identifies the line, which is why an
// anchor records it (see review.Anchor). So the text is asked where it lives now,
// and only an anchor that cannot be placed that way is refused.
func (c commentableLines) checkAnchor(a review.Anchor) anchorVerdict {
	path := strings.TrimSpace(a.Path)
	if a.Scope() == review.ChangeScope {
		// A review-level remark has no line to check; it becomes the review body.
		return anchorVerdict{State: anchorOK, Anchor: a}
	}
	if c.unknown[path] {
		return anchorVerdict{State: anchorUnknown, Anchor: a, Note: "no patch from GitHub — cannot check"}
	}
	if !c.hasFile(path) {
		return anchorVerdict{State: anchorMissingFile, Anchor: a, Note: "file is not in the PR's diff"}
	}
	if a.Scope() == review.FileScope {
		// A remark about the whole file, so the file being in the diff is the entire
		// question. Deliberately not checked against a side: it has no line, and
		// refusing it for having nothing on the old side would refuse a remark that was
		// never about a side. Without this arm it fell into the walk below, where a
		// LineHint of 0 came back as "line 0 is not in the diff".
		return anchorVerdict{State: anchorOK, Anchor: a}
	}
	v := c.checkLines(a)
	if !v.blocks() && c.hintStillFits(a) {
		return v
	}
	// The hint is stale, in one of the two ways it can be: it names a line that is
	// not in the diff at all, or one that is but no longer says what the comment was
	// written about. The second is the dangerous one — GitHub accepts it, so it
	// publishes silently onto whatever code has moved into that position.
	//
	// Asked only once the anchor has failed one of those. An anchor that still fits
	// is the answer, and looking its text up anyway would let a line that appears
	// twice in the file refuse an anchor that was never in doubt.
	moved, ok := c.relocate(a)
	if !ok {
		// Nothing better to offer. A stale-but-commentable hint keeps its verdict and
		// goes up as filed, which is what happened before any of this existed —
		// refusing it here would be new refusals for comments GitHub takes.
		return v
	}
	// Re-checked rather than assumed. relocate places the ends, and for a range the
	// lines between them still have to be in the same hunk — the check every anchor
	// gets, applied to the one this run would actually send.
	if got := c.checkLines(moved); got.blocks() {
		return v
	}
	return anchorVerdict{
		State:  anchorOK,
		Anchor: moved,
		// Said out loud, on the thread it is about. The comment is going somewhere
		// other than where it was filed, and a reviewer confirming an irreversible send
		// should see that rather than discover it on the PR.
		Note: fmt.Sprintf("relocated: %s → %s", a.LineRange(), moved.LineRange()),
	}
}

// checkLines is the anchor's lines against the side it names.
//
// A range is checked at both ends, and at every line between: GitHub requires the
// whole range to sit inside one hunk, so a range that straddles the gap between two
// hunks covers lines that are in no diff at all. Reported at the first line that
// fails, since that is the one to look at.
func (c commentableLines) checkLines(a review.Anchor) anchorVerdict {
	side := c.side(a.Side)[strings.TrimSpace(a.Path)]
	if len(side.lines) == 0 {
		// The path is in the diff but has nothing on this side: a file that was only
		// added has no old side to anchor a LEFT comment to. Worth its own wording —
		// "line 1 is not in the diff" would send you looking at line 1 when the side is
		// what is wrong.
		return anchorVerdict{
			State:  anchorMissingLine,
			Anchor: a,
			Note:   fmt.Sprintf("nothing on the %s side of this file", githubSide(a.Side)),
		}
	}
	first, last := commentStartLine(a), commentEndLine(a)
	for line := first; line <= last; line++ {
		if side.lines[line] {
			continue
		}
		note := fmt.Sprintf("line %d is not in the diff", line)
		if first != last {
			note = fmt.Sprintf("line %d of %d-%d is not in the diff", line, first, last)
		}
		return anchorVerdict{State: anchorMissingLine, Anchor: a, Note: note}
	}
	return anchorVerdict{State: anchorOK, Anchor: a}
}

// hintStillFits reports whether the anchor's line is still the line its text is
// on — the question a passing line check does not answer.
//
// "In the diff" and "still the right code" are different facts, and only the first
// is what GitHub validates. A comment filed at line 47 whose file gained six lines
// above it still names a line inside a hunk, so it publishes without complaint,
// onto whatever moved into position 47.
//
// An anchor with no text recorded fits by default. There is nothing to compare it
// against, and a record written before the text was carried must not be treated as
// evidence that the line is wrong.
func (c commentableLines) hintStillFits(a review.Anchor) bool {
	if strings.TrimSpace(a.Text) == "" {
		return true
	}
	side := c.side(a.Side)[strings.TrimSpace(a.Path)]
	if !side.holds(a.Text, a.LineHint) {
		return false
	}
	// A range's end is checked only when it was recorded, for the same reason.
	if a.Multiline() && strings.TrimSpace(a.EndText) != "" {
		return side.holds(a.EndText, a.EndLineHint)
	}
	return true
}

// holds reports whether this line is one of the lines carrying that content.
func (s diffSide) holds(text string, line int) bool {
	key := strings.TrimSpace(text)
	return key != "" && slices.Contains(s.byText[key], line)
}

// relocate moves an anchor onto the lines its text occupies in the PR's diff now,
// and reports whether it could.
//
// Both ends of a range are located independently, the same way the viewer
// relocates them at render time — an edit above a comment moves its start and its
// end by the same amount, but an edit inside it does not. An end that lands before
// its start is not a range at all, so it is refused rather than swapped: a result
// like that means the two ends matched different code, and the anchor no longer
// describes anything.
func (c commentableLines) relocate(a review.Anchor) (review.Anchor, bool) {
	side, ok := c.side(a.Side)[strings.TrimSpace(a.Path)]
	if !ok {
		return a, false
	}
	start, ok := side.lineOf(a.Text)
	if !ok {
		return a, false
	}
	moved := a
	moved.LineHint = start
	if a.Multiline() {
		end, ok := side.lineOf(a.EndText)
		if !ok || end < start {
			return a, false
		}
		moved.EndLineHint = end
	}
	if moved.LineHint == a.LineHint && moved.EndLineHint == a.EndLineHint {
		// It was already where it says it is, so there is nothing to report. Reached
		// only when the line check failed for some other reason — a range straddling
		// two hunks, say — and calling that "relocated: 12 → 12" would explain nothing.
		return a, false
	}
	return moved, true
}

// commentStartLine is the first line a comment covers.
//
// LineHint is that line whether or not the anchor is a range — a range records its
// *end* separately, in EndLineHint, which is GitHub's convention too. Falls through
// to the end for a record whose start was never filled in.
func commentStartLine(a review.Anchor) int {
	if a.LineHint > 0 {
		return a.LineHint
	}
	return commentEndLine(a)
}

// knowsNothing reports whether the parse produced no usable paths at all.
//
// Distinct from "the diff is empty", which is not a state a PR is in: it means the
// answer had no filename we could read. Refusing every anchor on that basis would
// be refusing on ignorance, so the check declines to run instead.
func (c commentableLines) knowsNothing() bool {
	return len(c.right) == 0 && len(c.left) == 0 && len(c.unknown) == 0
}

// preflight checks every thread's anchor and returns the threads as they will be
// sent, since checking is also where an anchor whose line has drifted is put back
// on the line its text occupies.
//
// Both together rather than a check followed by a separate relocation pass. The
// preview and the mutation read this one slice, so they cannot name different
// lines — which is the only thing that makes it safe to move a comment at all: the
// reviewer confirms the send having been shown where it is going.
//
// A nil verdict slice means the check did not run, which callers report as a note
// rather than as a pass. The threads come back untouched in that case: with no
// knowledge of the diff there is nothing to relocate against.
func preflight(threads []review.Comment, c commentableLines) ([]review.Comment, []anchorVerdict) {
	if c.knowsNothing() {
		return threads, nil
	}
	out := make([]anchorVerdict, 0, len(threads))
	sending := make([]review.Comment, 0, len(threads))
	for _, cm := range threads {
		v := c.checkAnchor(cm.Anchor)
		cm.Anchor = v.Anchor
		out = append(out, v)
		sending = append(sending, cm)
	}
	return sending, out
}

// blockedAnchors is the comments a run must not send, with the reason.
func blockedAnchors(inline []review.Comment, verdicts []anchorVerdict) []string {
	var out []string
	for i, v := range verdicts {
		if i >= len(inline) || !v.blocks() {
			continue
		}
		out = append(out, fmt.Sprintf("%s — %s", inline[i].Anchor.Where(), v.Note))
	}
	return out
}
