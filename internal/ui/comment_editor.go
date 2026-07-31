package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/andrewcohen/awp/internal/review"
)

// The comment editor: press `c` on a line, type, save.
//
// Two distinct exits rather than one with a mode — `enter` saves locally, and a
// separate key also hands the comment to the agent (phase 4). Typing a comment
// and only afterwards discovering where it went is the failure worth designing
// against, so the two are separate keystrokes with separate confirmations.

// commentEditor is the inline compose box, anchored to the line the cursor was
// on when it opened.
type commentEditor struct {
	area   textarea.Model
	anchor review.Anchor
	// editing is the id of the comment being revised, empty when composing a new
	// one. Carried here so saving updates that record instead of appending a
	// near-duplicate.
	editing string
	// replyTo is the id of the comment being replied to, empty otherwise.
	replyTo string
	// kind is what the remark is asking for, cycled with tab while composing.
	kind review.Kind
}

func newCommentEditor(a review.Anchor, width int) commentEditor {
	return newCommentEditorFor(review.Comment{Anchor: a}, width)
}

// newReplyEditor opens an empty box that will thread under parentID.
func newReplyEditor(parentID string, a review.Anchor, width int) commentEditor {
	e := newCommentEditor(a, width)
	e.replyTo = parentID
	return e
}

// newCommentEditorFor opens the box on an existing comment, pre-filled.
func newCommentEditorFor(c review.Comment, width int) commentEditor {
	ta := textarea.New()
	ta.Placeholder = "comment…"
	ta.ShowLineNumbers = false
	// textarea's default prompt is `┃ `, which is this app's selection marker
	// (see the design system in CLAUDE.md). Inside a bordered box it reads as a
	// selected row rather than as a line of the thing you are typing, so the
	// prompt is a plain space aligning the text with the box's header.
	ta.Prompt = " "
	ta.SetWidth(editorAreaWidth(width))
	ta.SetHeight(commentEditorHeight)
	ta.CharLimit = 0
	ta.SetValue(c.Body)
	ta.Focus()
	ta.CursorEnd()
	return commentEditor{area: ta, anchor: c.Anchor, editing: c.ID, kind: c.Kind.OrDefault()}
}

// commentEditorHeight is how many rows the text area gets. Enough for a real
// remark, small enough that the code being commented on stays visible.
const commentEditorHeight = 4

// commentEditorRows is the box's total height in the stream: the text area plus
// a header, a key hint, and the two border rows.
//
// It has to be a constant, because the stream's geometry is computed before
// anything is rendered — a box that turned out taller than this would leave the
// row count disagreeing with what is drawn. view() holds up its end by
// truncating its header and hint rather than letting them wrap.
const commentEditorRows = commentEditorHeight + 4

// editorAction is what the editor wants the host to do next.
type editorAction int

const (
	editorContinue editorAction = iota
	editorCancel
	editorSave
	editorSaveAndSend
)

func (e commentEditor) update(msg tea.Msg) (commentEditor, tea.Cmd, editorAction) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		var cmd tea.Cmd
		e.area, cmd = e.area.Update(msg)
		return e, cmd, editorContinue
	}
	switch key.String() {
	case "esc":
		return e, nil, editorCancel
	case "enter":
		// A body-only newline would make `enter` ambiguous, so multi-line
		// comments use alt+enter and `enter` always means "done".
		if strings.TrimSpace(e.area.Value()) == "" {
			return e, nil, editorCancel
		}
		return e, nil, editorSave
	case "ctrl+s":
		if strings.TrimSpace(e.area.Value()) == "" {
			return e, nil, editorCancel
		}
		return e, nil, editorSaveAndSend
	case "alt+enter":
		e.area.InsertString("\n")
		return e, nil, editorContinue
	case "tab":
		// The box owns every key while it is open, so tab is free here — it is the
		// pane switch only in the diff. Cycling rather than three separate keys
		// keeps the gesture next to the label it changes.
		e.kind = e.kind.Next()
		return e, nil, editorContinue
	}
	var cmd tea.Cmd
	e.area, cmd = e.area.Update(msg)
	return e, cmd, editorContinue
}

// view renders the box at exactly commentEditorRows rows. Both the header and
// the hint are truncated to the inner width rather than allowed to wrap: a wrap
// would add a row the stream's geometry did not account for, and every row index
// after the box would then be off by one.
func (e commentEditor) view(width int) string {
	inner := max(20, width-2) - 2
	// Leading space on both the header and the hint so they line up with the text
	// area's own one-column prompt.
	hint := " enter save · tab kind · ctrl+s save & send to agent · alt+enter newline · esc cancel"
	if lipgloss.Width(hint) > inner {
		hint = " enter save · tab kind · ctrl+s send · esc cancel"
	}
	verb := " " + string(e.kind.OrDefault()) + " on "
	switch {
	case e.replyTo != "":
		verb = " reply (" + string(e.kind.OrDefault()) + ") on "
	case e.editing != "":
		verb = " editing " + string(e.kind.OrDefault()) + " on "
	}
	head := verb + e.anchor.Path + ":" + lineNoText(e.anchor.LineHint)
	// Border and header take the kind's hue, so tab's effect is visible
	// immediately rather than only once the comment is saved.
	headStyle := kindStyles(e.kind)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(kindColor(e.kind))).
		Width(max(20, width-2)).
		Render(lipgloss.JoinVertical(lipgloss.Left,
			headStyle.Render(truncate(head, inner)),
			e.area.View(),
			styleDim.Render(truncate(hint, inner)),
		))
}

// lines is the box's display rows, for the stream to draw one at a time.
func (e commentEditor) lines(width int) []string {
	return strings.Split(e.view(width), "\n")
}

// setWidth re-lays the text area for a new pane width.
//
// The area's width has to track the width the box is rendered at. Left behind
// after a resize, an area wider than the box wraps inside it — which makes the
// box taller than commentEditorRows and desyncs every row index after it.
func (e *commentEditor) setWidth(width int) {
	e.area.SetWidth(editorAreaWidth(width))
}

// editorAreaWidth is the text area's width inside a box rendered at width:
// two columns of border, and the area's own two-column prompt/padding.
func editorAreaWidth(width int) int {
	return max(4, max(20, width-2)-2-2)
}

// editorAnchorRow is the stream row the compose box hangs under, resolved
// against the given rows by content rather than remembered as an index — the
// diff reloads on a timer, so an index recorded when the box opened may point at
// different code by the next frame.
//
// Attaching to the *last* row of a conversation rather than its first is what
// makes a reply read as appended to the exchange instead of wedged into the
// middle of it.
func (m Model) editorAnchorRow(idx streamIndex) int {
	if target := m.editor.replyTo; target != "" {
		if row := lastRowOfThread(idx, target); row >= 0 {
			return row
		}
	}
	if target := m.editor.editing; target != "" {
		// The last of the comment's rows. withEditor drops the whole span and puts
		// the box at the first of them, so this only has to resolve to *somewhere*
		// in the comment; it stays the last row so the fallthrough below — reached
		// when the span cannot be found — behaves like a reply.
		if _, last := rowsOfComment(idx, target); last >= 0 {
			return last
		}
	}
	// A new comment attaches under the line it is about, found the same way a
	// saved comment's own anchor is (see locateComment) so the box opens exactly
	// where the comment will end up.
	if row, ok := m.locateComment(idx.rows, review.Comment{Anchor: m.editor.anchor}); ok {
		return row
	}
	// Nothing resolved — the code may have been edited out from under the box
	// mid-sentence. Fall back to the cursor, which is where the box was opened
	// from, so it stays on screen and keeps taking input.
	return m.cursorRow
}

// lastRowOfThread is the final display row of a whole conversation — the parent
// and every reply beneath it.
func lastRowOfThread(idx streamIndex, parentID string) int {
	found := -1
	for i, r := range idx.rows {
		if r.kind != rowComment && r.kind != rowOrphan {
			continue
		}
		if r.comment < 0 || r.comment >= len(idx.comments) {
			continue
		}
		if c := idx.comments[r.comment]; c.ID == parentID || c.ReplyTo == parentID {
			found = i
		}
	}
	return found
}

// comment builds the record from the editor's contents.
func (e commentEditor) comment() review.Comment {
	return review.Comment{
		ID:     e.editing,
		Author: review.AuthorHuman,
		Body:   strings.TrimRight(e.area.Value(), "\n"),
		Kind:   e.kind.OrDefault(),
		State:  review.Open,
		Anchor: e.anchor,
	}
}

// startComment opens the compose box on the cursor's line. Silently does nothing
// when there is nowhere to attach a comment or no store to save it to, rather
// than opening an editor whose contents would be discarded.
func (m Model) startComment() (tea.Model, tea.Cmd) {
	if m.SaveComment == nil {
		m.status = "commenting unavailable: no review store"
		return m, nil
	}
	// On a comment, `c` replies to it — the common thing to do with a remark is
	// answer it. Revising your own wording is `i`.
	if c, ok := m.localCommentAtCursor(); ok {
		if m.ReplyComment == nil {
			m.status = "replying unavailable here"
			return m, nil
		}
		// A reply threads under the top of the conversation, not under another
		// reply: one exchange, one thread.
		parent := c.ID
		if c.ReplyTo != "" {
			parent = c.ReplyTo
		}
		m.editing = true
		m.editor = newReplyEditor(parent, c.Anchor, m.hunkWidth)
		// The box is a run of stream rows, so opening it changes the row count.
		m.rebuildStream()
		return m, textarea.Blink
	}
	if _, isThread := m.threadAtCursor(); isThread {
		m.status = "that is a GitHub thread — resolve it with R"
		return m, nil
	}
	a, ok := m.AnchorAtCursor()
	if !ok {
		m.status = "put the cursor on a diff line to comment"
		return m, nil
	}
	m.editing = true
	m.editor = newCommentEditor(a, m.hunkWidth)
	m.rebuildStream()
	return m, textarea.Blink
}

// startEdit opens your own comment for revision.
func (m Model) startEdit() (tea.Model, tea.Cmd) {
	c, ok := m.localCommentAtCursor()
	if !ok {
		m.status = "put the cursor on one of your comments to edit it"
		return m, nil
	}
	if m.UpdateComment == nil {
		m.status = "editing unavailable here"
		return m, nil
	}
	m.editing = true
	m.editor = newCommentEditorFor(c, m.hunkWidth)
	m.rebuildStream()
	return m, textarea.Blink
}

// handleEditorKey routes input to the compose box and applies its outcome.
func (m Model) handleEditorKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	editor, cmd, action := m.editor.update(msg)
	m.editor = editor
	switch action {
	case editorCancel:
		m.editing = false
		// Closing the box removes its rows from the stream.
		m.rebuildStream()
		// Cancellation is self-evident — the box is gone — so it prints nothing.
		return m, nil
	case editorSave, editorSaveAndSend:
		m.editing = false
		// Every exit below either rebuilds after mutating the comment set or
		// returns on an error; rebuild up front so the box's rows are gone on
		// every path rather than only the successful ones.
		m.rebuildStream()
		c := m.editor.comment()
		if parent := m.editor.replyTo; parent != "" {
			if err := m.ReplyComment(parent, c); err != nil {
				m.status = "reply: " + err.Error()
				m.statusErr = true
				return m, nil
			}
			c.ReplyTo = parent
			// A reply reopens the thread for its author too, matching what the
			// store does — the record and the view must not disagree.
			for i := range m.comments {
				if m.comments[i].ID == parent {
					m.comments[i].State = review.Open
				}
			}
			m.comments = append(m.comments, c)
			m.rebuildStream()
			m.status = "reply saved"
			return m, nil
		}
		if c.ID != "" {
			// Revising: update in place rather than appending a near-duplicate.
			if err := m.UpdateComment(c); err != nil {
				m.status = "comment: " + err.Error()
				m.statusErr = true
				return m, nil
			}
			for i := range m.comments {
				if m.comments[i].ID == c.ID {
					m.comments[i].Body = c.Body
				}
			}
			m.rebuildStream()
			m.status = "comment updated"
			if action == editorSave {
				return m, nil
			}
		} else {
			if err := m.SaveComment(c); err != nil {
				m.status = "comment: " + err.Error()
				m.statusErr = true
				return m, nil
			}
			// SaveComment assigns the id; without reading it back the prompt has
			// no id to name and the agent cannot reply on the thread.
			if m.LastSavedComment != nil {
				if saved, ok := m.LastSavedComment(); ok {
					c = saved
				}
			}
			m.comments = append(m.comments, c)
			m.rebuildStream()
			m.status = "comment saved"
		}
		if action == editorSaveAndSend {
			if m.SendComment == nil {
				m.status = "comment saved (sending unavailable here)"
				return m, nil
			}
			if err := m.SendComment(c); err != nil {
				m.status = "comment saved, send failed: " + err.Error()
				m.statusErr = true
				return m, nil
			}
			m.status = "comment saved and sent to the agent"
		}
		return m, nil
	}
	return m, cmd
}
