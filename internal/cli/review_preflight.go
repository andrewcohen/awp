package cli

import (
	"fmt"
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
	// right and left map a path to the line numbers commentable on that side.
	right map[string]map[int]bool
	left  map[string]map[int]bool
	// unknown holds paths the check has nothing to say about: a file GitHub
	// included with no patch (binary, or too large). Distinguished from a path that
	// is simply absent from the diff, which is a real finding.
	unknown map[string]bool
}

// parseCommentable reads the per-file patches into the commentable sets.
//
// The patch body is walked rather than only its hunk headers, because the two
// sides advance at different rates: a run of added lines moves the new-side number
// and not the old one. Walking is also what makes context lines commentable, which
// they are — GitHub's rule is "in the diff", not "changed".
func parseCommentable(files []github.PRFile) commentableLines {
	out := commentableLines{
		right:   map[string]map[int]bool{},
		left:    map[string]map[int]bool{},
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
		out.right[path] = map[int]bool{}
		out.left[path] = map[int]bool{}
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
			marker := byte(' ')
			if raw != "" {
				marker = raw[0]
			}
			switch marker {
			case '+':
				out.right[path][newLine] = true
				newLine++
			case '-':
				out.left[path][oldLine] = true
				oldLine++
			case ' ':
				out.right[path][newLine] = true
				out.left[path][oldLine] = true
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
}

// blocks reports whether this verdict should stop the run. Only a definite
// negative does: a check that cannot tell must not refuse a publish GitHub would
// have accepted.
func (v anchorVerdict) blocks() bool {
	return v.State == anchorMissingFile || v.State == anchorMissingLine
}

// checkAnchor decides whether GitHub will accept this comment's anchor.
//
// A range is checked at both ends, and at every line between: GitHub requires the
// whole range to sit inside one hunk, so a range that straddles the gap between two
// hunks covers lines that are in no diff at all. Reported at the first line that
// fails, since that is the one to look at.
func (c commentableLines) checkAnchor(a review.Anchor) anchorVerdict {
	path := strings.TrimSpace(a.Path)
	if path == "" {
		// A review-level remark has no line to check; it becomes the review body.
		return anchorVerdict{State: anchorOK}
	}
	if c.unknown[path] {
		return anchorVerdict{State: anchorUnknown, Note: "no patch from GitHub — cannot check"}
	}
	side := c.right
	if a.Side == review.SideOld {
		side = c.left
	}
	lines, inDiff := side[path]
	if !inDiff {
		if _, right := c.right[path]; !right {
			if _, left := c.left[path]; !left {
				return anchorVerdict{State: anchorMissingFile, Note: "file is not in the PR's diff"}
			}
		}
	}
	if len(lines) == 0 {
		// The path is in the diff but has nothing on this side: a file that was only
		// added has no old side to anchor a LEFT comment to. Worth its own wording —
		// "line 1 is not in the diff" would send you looking at line 1 when the side is
		// what is wrong.
		return anchorVerdict{
			State: anchorMissingLine,
			Note:  fmt.Sprintf("nothing on the %s side of this file", githubSide(a.Side)),
		}
	}
	first, last := commentStartLine(a), commentEndLine(a)
	for line := first; line <= last; line++ {
		if lines[line] {
			continue
		}
		note := fmt.Sprintf("line %d is not in the diff", line)
		if first != last {
			note = fmt.Sprintf("line %d of %d-%d is not in the diff", line, first, last)
		}
		return anchorVerdict{State: anchorMissingLine, Note: note}
	}
	return anchorVerdict{State: anchorOK}
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

// preflight checks every inline comment. A nil result means the check did not run,
// which callers report as a note rather than as a pass.
func preflight(inline []review.Comment, c commentableLines) []anchorVerdict {
	if c.knowsNothing() {
		return nil
	}
	out := make([]anchorVerdict, 0, len(inline))
	for _, cm := range inline {
		out = append(out, c.checkAnchor(cm.Anchor))
	}
	return out
}

// blockedAnchors is the comments a run must not send, with the reason.
func blockedAnchors(inline []review.Comment, verdicts []anchorVerdict) []string {
	var out []string
	for i, v := range verdicts {
		if i >= len(inline) || !v.blocks() {
			continue
		}
		out = append(out, fmt.Sprintf("%s:%s — %s",
			inline[i].Anchor.Path, inline[i].Anchor.LineRange(), v.Note))
	}
	return out
}
