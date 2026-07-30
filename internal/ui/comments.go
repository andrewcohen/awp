package ui

import (
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/andrewcohen/awp/internal/diff"
	"github.com/andrewcohen/awp/internal/review"
)

// Comments in the diff stream.
//
// A comment is stored against content (path + line text + context), never
// against a row index, so placing it means *locating* it in the current diff.
// That is the same job restoring the cursor does across a reload, and it uses
// the same ladder — see anchor.go. A comment that cannot be located is not
// dropped; it becomes orphaned and is still shown, because silently hiding
// something a reviewer wrote is the worst failure this surface has.

// CommentSink is how the viewer persists a comment the user just wrote. The
// viewer never touches the filesystem itself, so it stays testable and the
// storage decision stays in one place.
type CommentSink func(review.Comment) error

// ThreadVisibility controls which remote threads are shown. Resolved threads are
// hidden by default: they are settled conversation, and showing them by default
// buries the ones that still need attention.
type ThreadVisibility int

const (
	ThreadsUnresolved ThreadVisibility = iota
	ThreadsAll
	ThreadsNone
)

func (v ThreadVisibility) String() string {
	switch v {
	case ThreadsAll:
		return "all threads"
	case ThreadsNone:
		return "threads hidden"
	default:
		return "unresolved threads"
	}
}

// SetThreads installs the mirrored remote threads.
func (m *Model) SetThreads(ts []review.Thread) {
	m.threads = ts
	m.rebuildStream()
}

// visibleThreads is the thread set the current visibility admits.
func (m Model) visibleThreads() []review.Thread {
	if m.threadVisibility == ThreadsNone || len(m.threads) == 0 {
		return nil
	}
	if m.threadVisibility == ThreadsAll {
		return m.threads
	}
	out := make([]review.Thread, 0, len(m.threads))
	for _, t := range m.threads {
		if !t.Resolved {
			out = append(out, t)
		}
	}
	return out
}

// threadAsComment adapts a remote thread into the same display shape local
// comments use, so one renderer covers both. The distinction the reader needs —
// this is already on GitHub — is carried in the author label rather than by a
// separate row kind.
func threadAsComment(t review.Thread) review.Comment {
	var b strings.Builder
	for i, c := range t.Comments {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(c.Author + ": " + c.Body)
	}
	author := "github"
	if t.Resolved {
		author = "github · resolved"
	} else if t.Outdated {
		author = "github · outdated"
	}
	return review.Comment{
		ID:     "thread-" + t.ID,
		Author: author,
		Body:   b.String(),
		State:  review.Published,
		Anchor: review.Anchor{Path: t.Path, Side: t.Side, LineHint: t.Line},
	}
}

// SetComments replaces the comment set and rebuilds the stream so they appear
// in place.
func (m *Model) SetComments(cs []review.Comment) {
	m.comments = cs
	m.rebuildStream()
}

// Comments returns the current comment set.
func (m Model) Comments() []review.Comment { return m.comments }

// placeComments resolves each comment to the stream row it belongs under,
// returning a row-index → comments map plus the comments that could not be
// placed at all.
//
// Called during the geometry pass, so it must not render anything.
func (m Model) placeComments(rows []rowRef) (map[int][]review.Comment, []review.Comment) {
	// Remote threads render through the same path as local comments; they differ
	// in their label, not in how they are placed or anchored. Their line numbers
	// are GitHub's, against a particular commit, so they drift exactly the way
	// ours do and want the same relocation ladder.
	all := m.comments
	for _, t := range m.visibleThreads() {
		all = append(all, threadAsComment(t))
	}
	if len(all) == 0 {
		return nil, nil
	}
	placed := make(map[int][]review.Comment, len(all))
	var orphans []review.Comment
	for _, c := range all {
		if row, ok := m.locateComment(rows, c); ok {
			placed[row] = append(placed[row], c)
			continue
		}
		orphans = append(orphans, c)
	}
	return placed, orphans
}

// locateComment finds the row a comment attaches to, weakening the match the
// same way findAnchor does: exact line, then same text elsewhere in the file,
// then the same text with matching context.
func (m Model) locateComment(rows []rowRef, c review.Comment) (int, bool) {
	var inFile []int
	for i, r := range rows {
		if r.kind != rowLine || r.file < 0 || r.file >= len(m.filtered) {
			continue
		}
		if pathOf(m.filtered[r.file]) != c.Anchor.Path {
			continue
		}
		inFile = append(inFile, i)
	}
	if len(inFile) == 0 {
		return 0, false
	}
	lineNo := func(r rowRef) int {
		if c.Anchor.Side == review.SideOld {
			return r.oldNo
		}
		return r.newNo
	}

	// The line is where it was, with the text it had.
	for _, i := range inFile {
		r := rows[i]
		if r.seg == 0 && lineNo(r) == c.Anchor.LineHint && m.lineTextIn(rows, i) == c.Anchor.Text {
			return i, true
		}
	}
	// The text moved: prefer a match whose surrounding context also agrees, so a
	// duplicate line elsewhere doesn't capture the comment.
	var textMatches []int
	for _, i := range inFile {
		if rows[i].seg == 0 && m.lineTextIn(rows, i) == c.Anchor.Text {
			textMatches = append(textMatches, i)
		}
	}
	if len(textMatches) == 1 {
		return textMatches[0], true
	}
	if best, ok := m.bestByContext(rows, textMatches, c.Anchor); ok {
		return best, true
	}
	// Ambiguous but present: nearest to where it used to be.
	if len(textMatches) > 0 {
		best, bestDist := textMatches[0], 0
		for n, i := range textMatches {
			d := lineNo(rows[i]) - c.Anchor.LineHint
			if d < 0 {
				d = -d
			}
			if n == 0 || d < bestDist {
				best, bestDist = i, d
			}
		}
		return best, true
	}
	return 0, false
}

// bestByContext picks the candidate whose neighbouring lines agree most with the
// anchor's recorded context. Requires a strict winner: a tie means we genuinely
// cannot tell, and guessing would attach the comment to the wrong code.
func (m Model) bestByContext(rows []rowRef, candidates []int, a review.Anchor) (int, bool) {
	if len(candidates) == 0 || (len(a.ContextBefore) == 0 && len(a.ContextAfter) == 0) {
		return 0, false
	}
	best, bestScore, tied := 0, -1, false
	for _, i := range candidates {
		score := 0
		for n, want := range a.ContextBefore {
			if got, ok := m.rowTextAt(rows, i-len(a.ContextBefore)+n); ok && got == want {
				score++
			}
		}
		for n, want := range a.ContextAfter {
			if got, ok := m.rowTextAt(rows, i+1+n); ok && got == want {
				score++
			}
		}
		switch {
		case score > bestScore:
			best, bestScore, tied = i, score, false
		case score == bestScore:
			tied = true
		}
	}
	if tied || bestScore <= 0 {
		return 0, false
	}
	return best, true
}

func (m Model) rowTextAt(rows []rowRef, i int) (string, bool) {
	if i < 0 || i >= len(rows) || rows[i].kind != rowLine {
		return "", false
	}
	return m.lineTextIn(rows, i), true
}

// lineTextIn reads a line row's content from an arbitrary row slice, so it works
// during the geometry pass before m.stream has been replaced.
func (m Model) lineTextIn(rows []rowRef, i int) string {
	r := rows[i]
	if r.file < 0 || r.file >= len(m.filtered) {
		return ""
	}
	f := m.filtered[r.file]
	if r.hunk < 0 || r.hunk >= len(f.Hunks) {
		return ""
	}
	h := f.Hunks[r.hunk]
	if r.line < 0 || r.line >= len(h.Lines) {
		return ""
	}
	return h.Lines[r.line].Content
}

// AnchorAtCursor describes the line under the cursor, for attaching a new
// comment. Reports false when the cursor isn't on a diff line.
func (m Model) AnchorAtCursor() (review.Anchor, bool) {
	if len(m.stream.rows) == 0 || m.cursorRow >= len(m.stream.rows) {
		return review.Anchor{}, false
	}
	r := m.stream.rows[m.cursorRow]
	if r.kind != rowLine || r.file < 0 || r.file >= len(m.filtered) {
		return review.Anchor{}, false
	}
	f := m.filtered[r.file]
	h := f.Hunks[r.hunk]
	line := h.Lines[r.line]

	// New side for added and context lines; old side only for a removed line,
	// which exists nowhere else. Keeping to the new side means relocation reads
	// current file content, the same source live refresh uses.
	side, hint := review.SideNew, r.newNo
	if line.Type == '-' {
		side, hint = review.SideOld, r.oldNo
	}
	a := review.Anchor{
		Path:     pathOf(f),
		Side:     side,
		LineHint: hint,
		Text:     line.Content,
	}
	for i := r.line - anchorContextLines; i < r.line; i++ {
		if i >= 0 {
			a.ContextBefore = append(a.ContextBefore, h.Lines[i].Content)
		}
	}
	for i := r.line + 1; i <= r.line+anchorContextLines && i < len(h.Lines); i++ {
		a.ContextAfter = append(a.ContextAfter, h.Lines[i].Content)
	}
	return a, true
}

// anchorContextLines is how much surrounding text an anchor records. Enough to
// disambiguate a repeated line, few enough that an edit nearby doesn't
// invalidate the anchor.
const anchorContextLines = 3

// commentLines renders a comment into display rows.
func commentLines(c review.Comment, width int) []string {
	label := c.Author
	if label == review.AuthorHuman {
		label = "you"
	}
	head := "  ▌ " + label
	if c.State != review.Open {
		head += " · " + string(c.State)
	}
	out := []string{styleCommentHead.Render(truncate(head, max(1, width)))}
	for _, line := range strings.Split(strings.TrimRight(c.Body, "\n"), "\n") {
		out = append(out, styleCommentBody.Render(truncate("  ▌ "+line, max(1, width))))
	}
	return out
}

// Reviewed files collapse out of the way.
//
// The flag is keyed to the file's *content*, not just its path: an edit after
// you marked it reviewed must bring it back. That matters far more here than in
// a conventional review tool, because the agent is editing while you read — a
// change hidden behind a stale reviewed flag is the one outcome this surface
// must never produce.

// fileContentHash fingerprints a file's diff body, so a reviewed mark can tell
// "unchanged since I looked" from "edited since I looked".
func fileContentHash(f diff.FileDiff) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(f.Status))
	for _, hunk := range f.Hunks {
		_, _ = fmt.Fprintf(h, "@%d,%d,%d,%d;", hunk.OldStart, hunk.OldCount, hunk.NewStart, hunk.NewCount)
		for _, l := range hunk.Lines {
			_, _ = h.Write([]byte{l.Type})
			_, _ = h.Write([]byte(l.Content))
			_, _ = h.Write([]byte{'\n'})
		}
	}
	return strconv.FormatUint(h.Sum64(), 16)
}

// isCollapsed reports whether a file is currently hidden: reviewed, and
// unchanged since it was reviewed.
func (m Model) isCollapsed(path string) bool {
	want, ok := m.ReviewedFiles[path]
	if !ok {
		return false
	}
	for _, f := range m.filtered {
		if pathOf(f) == path {
			return fileContentHash(f) == want
		}
	}
	return false
}

// toggleReviewed marks the file at the cursor reviewed, or un-marks it.
func (m Model) toggleReviewed() (tea.Model, tea.Cmd) {
	f, ok := m.cursorFile()
	if !ok {
		m.status = "no file at the cursor"
		return m, nil
	}
	path := pathOf(f)
	if m.ReviewedFiles == nil {
		m.ReviewedFiles = map[string]string{}
	}
	hash := ""
	if !m.isCollapsed(path) {
		hash = fileContentHash(f)
	}
	if hash == "" {
		delete(m.ReviewedFiles, path)
		m.status = path + ": unreviewed"
	} else {
		m.ReviewedFiles[path] = hash
		m.status = path + ": reviewed"
	}
	if m.MarkReviewed != nil {
		if err := m.MarkReviewed(path, hash); err != nil {
			m.status = "reviewed: " + err.Error()
			m.statusErr = true
			return m, nil
		}
	}
	// Collapsing changes the row count, so the geometry has to be rebuilt and
	// the cursor re-clamped against it.
	m.rebuildStream()
	m.clampCursor()
	m.followCursor()
	m.syncFileCursorToCursor()
	return m, nil
}

// cursorFile is the file the cursor is in.
func (m Model) cursorFile() (diff.FileDiff, bool) {
	if len(m.stream.rows) == 0 || m.cursorRow >= len(m.stream.rows) {
		return diff.FileDiff{}, false
	}
	fi := m.stream.rows[m.cursorRow].file
	if fi < 0 || fi >= len(m.filtered) {
		return diff.FileDiff{}, false
	}
	return m.filtered[fi], true
}

// SetReviewed installs the reviewed-file marks loaded from the store.
func (m *Model) SetReviewed(marks map[string]string) {
	m.ReviewedFiles = marks
	m.rebuildStream()
}
