package ui

import (
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/andrewcohen/awp/internal/charm"
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

// CommentDeleter removes a comment by id.
type CommentDeleter func(id string) error

// localCommentAtCursor is the comment under the cursor, if it is one of ours.
// Remote GitHub threads are excluded: they are GitHub's records, and editing or
// deleting them from here would be a lie about what happened.
func (m Model) localCommentAtCursor() (review.Comment, bool) {
	if len(m.stream.rows) == 0 || m.cursorRow >= len(m.stream.rows) {
		return review.Comment{}, false
	}
	r := m.stream.rows[m.cursorRow]
	if r.kind != rowComment && r.kind != rowOrphan {
		return review.Comment{}, false
	}
	if r.comment < 0 || r.comment >= len(m.stream.comments) {
		return review.Comment{}, false
	}
	c := m.stream.comments[r.comment]
	if strings.HasPrefix(c.ID, "thread-") {
		return review.Comment{}, false
	}
	// Resolve against the live set: the placed copy is a snapshot.
	for _, own := range m.comments {
		if own.ID == c.ID {
			return own, true
		}
	}
	return review.Comment{}, false
}

// deleteCommentAtCursor removes the comment under the cursor.
func (m Model) deleteCommentAtCursor() (tea.Model, tea.Cmd) {
	if _, isThread := m.threadAtCursor(); isThread {
		m.status = "that is a GitHub thread — resolve it with R instead"
		return m, nil
	}
	c, ok := m.localCommentAtCursor()
	if !ok {
		m.status = "put the cursor on one of your comments to delete it"
		return m, nil
	}
	if m.DeleteComment == nil {
		m.status = "deleting unavailable here"
		return m, nil
	}
	if err := m.DeleteComment(c.ID); err != nil {
		m.status = "delete: " + err.Error()
		m.statusErr = true
		return m, nil
	}
	kept := make([]review.Comment, 0, len(m.comments))
	for _, own := range m.comments {
		if own.ID != c.ID {
			kept = append(kept, own)
		}
	}
	m.comments = kept
	// Removing rows can leave the cursor past the end.
	m.rebuildStream()
	m.clampCursor()
	m.followCursor()
	m.syncFileCursorToCursor()
	m.status = "comment deleted"
	return m, nil
}

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

// ThreadResolver toggles a remote thread's resolved state on GitHub. Nil leaves
// resolving unavailable, which the viewer reports rather than silently ignoring.
type ThreadResolver func(threadID string, resolve bool) error

// threadAtCursor is the remote thread the cursor is on, if any. Resolving acts on
// the thread under the cursor rather than a separate selection, so there is only
// ever one notion of "this one".
func (m Model) threadAtCursor() (review.Thread, bool) {
	if len(m.stream.rows) == 0 || m.cursorRow >= len(m.stream.rows) {
		return review.Thread{}, false
	}
	r := m.stream.rows[m.cursorRow]
	if r.kind != rowComment && r.kind != rowOrphan {
		return review.Thread{}, false
	}
	if r.comment < 0 || r.comment >= len(m.stream.comments) {
		return review.Thread{}, false
	}
	id := strings.TrimPrefix(m.stream.comments[r.comment].ID, "thread-")
	if id == m.stream.comments[r.comment].ID {
		return review.Thread{}, false // a local comment, not a remote thread
	}
	for _, t := range m.threads {
		if t.ID == id {
			return t, true
		}
	}
	return review.Thread{}, false
}

// toggleResolved resolves or unresolves the thread under the cursor.
func (m Model) toggleResolved() (tea.Model, tea.Cmd) {
	t, ok := m.threadAtCursor()
	if !ok {
		m.status = "put the cursor on a GitHub thread to resolve it"
		return m, nil
	}
	if m.ResolveThread == nil {
		m.status = "resolving unavailable here"
		return m, nil
	}
	want := !t.Resolved
	if err := m.ResolveThread(t.ID, want); err != nil {
		m.status = "resolve: " + err.Error()
		m.statusErr = true
		return m, nil
	}
	for i := range m.threads {
		if m.threads[i].ID == t.ID {
			m.threads[i].Resolved = want
		}
	}
	if want {
		m.status = "thread resolved"
	} else {
		m.status = "thread reopened"
	}
	m.rebuildStream()
	m.clampCursor()
	m.followCursor()
	return m, nil
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
	// Group into conversations first, then place each parent followed by its
	// replies. Placing replies independently would scatter an exchange across the
	// diff wherever each message's anchor happened to resolve.
	all := make([]review.Comment, 0, len(m.comments))
	for _, th := range review.Threads(m.comments) {
		all = append(all, th.Parent)
		all = append(all, th.Replies...)
	}
	for _, t := range m.visibleThreads() {
		all = append(all, threadAsComment(t))
	}
	if len(all) == 0 {
		return nil, nil
	}
	placed := make(map[int][]review.Comment, len(all))
	var orphans []review.Comment
	// A reply goes wherever its parent went, so a thread stays intact even if the
	// reply's own anchor would resolve elsewhere (or nowhere).
	parentRow := make(map[string]int, len(all))
	for _, c := range all {
		if c.ReplyTo != "" {
			if row, ok := parentRow[c.ReplyTo]; ok {
				placed[row] = append(placed[row], c)
				continue
			}
			orphans = append(orphans, c)
			continue
		}
		if row, ok := m.locateComment(rows, c); ok {
			placed[row] = append(placed[row], c)
			parentRow[c.ID] = row
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

	// An anchor with no recorded text can only be placed by line number. Remote
	// GitHub threads arrive this way — GitHub gives a line, not the line's
	// content — so without this they would all land in the detached section
	// despite pointing at code that is right there.
	if c.Anchor.Text == "" {
		for _, i := range inFile {
			if r := rows[i]; r.seg == 0 && lineNo(r) == c.Anchor.LineHint {
				return i, true
			}
		}
		return 0, false
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

// commentRows is the plain text of every row a comment occupies, gutter included
// and wrapped to width.
//
// Geometry and rendering both go through this, which is what keeps them in
// agreement: a comment's row count depends on the width it wraps at, and if the
// counter and the renderer disagreed the stream's row indices would stop matching
// what is drawn — the same desync the diff's own wrap accounting avoids.
//
// Comments wrap rather than truncate, and always — independent of the `w` wrap
// mode, which governs code. A review remark is prose written to be read; clipping
// it at the pane edge hides the half that explains the point, and there is no
// reason to make the reader ask for that.
//
// Word wrap, not the hard character wrap code uses. Breaking mid-word is right for
// code — reflowing at spaces misrepresents where a token ends — and wrong for
// prose, where it just makes sentences hard to read. ansi.Wrap still hard-breaks a
// word longer than the line, so a URL or a long identifier cannot overflow.
func commentRows(c review.Comment, width int) []string {
	// A reply sits one space in — the least that reads as nested. The bar matches
	// the parent's, so the indent alone carries the nesting.
	gutter := "  ▌ "
	if c.ReplyTo != "" {
		gutter = "   ▌ "
	}
	label := c.Author
	if label == review.AuthorHuman {
		label = "you"
	}
	title := gutter + label
	if k := c.Kind.OrDefault(); k != review.KindComment {
		title += " · " + string(k)
	}
	if c.State != review.Open {
		title += " · " + string(c.State)
	}

	avail := width - len([]rune(gutter))
	out := []string{truncate(title, max(1, width))}
	for _, line := range strings.Split(strings.TrimRight(c.Body, "\n"), "\n") {
		if avail < 1 {
			out = append(out, truncate(gutter+line, max(1, width)))
			continue
		}
		if strings.TrimSpace(line) == "" {
			// A deliberate blank line in a comment is a paragraph break; wrapping
			// would swallow it.
			out = append(out, gutter)
			continue
		}
		for _, wrapped := range strings.Split(ansi.Wrap(line, avail, ""), "\n") {
			out = append(out, gutter+wrapped)
		}
	}
	return out
}

// commentStyles picks the styling for a comment row.
//
// Keyed off the *kind* — what the remark is asking for — rather than off its
// author or whether it is a reply. Authorship is carried by the 🤖 marker on the
// body instead, which leaves the hue free to say the thing you cannot get from
// reading a label at a glance: whether this wants a change, an answer, or
// nothing.
//
// Factored out so the choice is assertable; lipgloss strips colour with no TTY,
// so it cannot be observed in rendered output.
func commentStyles(kind review.Kind, cursor bool) (head, body, fill lipgloss.Style) {
	head, body = kindStyles(kind)
	if cursor {
		// The cursorline has to be carried by every style on the row — an
		// enclosing style cannot supply it, since each inner style ends with a
		// reset that would clear it mid-row.
		return head.Background(cursorlineBg), body.Background(cursorlineBg), styleCursorFill
	}
	return head.Background(commentBg), body.Background(commentBg), styleCommentFill
}

// kindColor is the palette token for a kind, for surfaces that need the colour
// rather than a style — the compose box's border, which is how tab's effect is
// visible before there is any saved comment to look at.
func kindColor(kind review.Kind) string {
	switch kind.OrDefault() {
	case review.KindSuggestion:
		return charm.Danger
	case review.KindQuestion:
		return charm.Warning
	default:
		return charm.Info
	}
}

// kindStyles is the unfilled head/body pair for a kind. Separate from
// commentStyles so the index can reuse the hue without the block's background.
func kindStyles(kind review.Kind) (head, body lipgloss.Style) {
	switch kind.OrDefault() {
	case review.KindSuggestion:
		return styleSuggestionHead, styleSuggestionBody
	case review.KindQuestion:
		return styleQuestionHead, styleQuestionBody
	default:
		return styleCommentHead, styleCommentBody
	}
}

// commentLines renders a comment into styled display rows, painted across the
// full width so it reads as a block set into the diff. Each style carries the
// background itself — an enclosing style cannot supply it, since every inner
// style ends with a reset that would clear it mid-row.
func commentLines(c review.Comment, width int, cursor bool) []string {
	head, body, fill := commentStyles(c.Kind, cursor)
	rows := commentRows(c, width)
	out := make([]string, 0, len(rows))
	for i, text := range rows {
		style := body
		if i == 0 {
			style = head
		}
		rendered := style.Render(text)
		if n := width - lipgloss.Width(text); n > 0 {
			rendered += fill.Render(strings.Repeat(" ", n))
		}
		out = append(out, rendered)
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

// reloadComments re-reads the store and rebuilds only when something actually
// changed. Called on every refresh tick, so the unchanged case has to be cheap
// and has to leave the cursor exactly where it was.
func (m *Model) reloadComments() {
	if m.LoadComments == nil {
		return
	}
	fresh, err := m.LoadComments()
	if err != nil {
		// A store read failure is not worth interrupting a review over; the next
		// tick tries again.
		return
	}
	if sameComments(m.comments, fresh) {
		return
	}
	// Rebuilding changes the row count, so the cursor is re-anchored by content
	// the same way a diff reload does rather than by index.
	anchor, hadAnchor := m.captureAnchor()
	offset := m.cursorRow - m.streamScroll
	m.comments = fresh
	if hadAnchor {
		m.restoreAnchor(anchor, offset)
		return
	}
	m.rebuildStream()
	m.clampCursor()
	m.followCursor()
}

// sameComments reports whether two comment sets are equivalent for display.
// Compares the fields that affect rendering or placement — a timestamp bump on
// its own must not cost a rebuild.
func sameComments(a, b []review.Comment) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID ||
			a[i].Body != b[i].Body ||
			a[i].Kind != b[i].Kind ||
			a[i].State != b[i].State ||
			a[i].Author != b[i].Author ||
			a[i].ReplyTo != b[i].ReplyTo ||
			a[i].Anchor.Path != b[i].Anchor.Path ||
			a[i].Anchor.LineHint != b[i].Anchor.LineHint ||
			a[i].Anchor.Side != b[i].Anchor.Side {
			return false
		}
	}
	return true
}
