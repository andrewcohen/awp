package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/andrewcohen/awp/internal/charm"
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
}

func newCommentEditor(a review.Anchor, width int) commentEditor {
	ta := textarea.New()
	ta.Placeholder = "comment…"
	ta.ShowLineNumbers = false
	ta.SetWidth(max(20, width-4))
	ta.SetHeight(commentEditorHeight)
	ta.CharLimit = 0
	ta.Focus()
	return commentEditor{area: ta, anchor: a}
}

// commentEditorHeight is how many rows the compose box gets. Enough for a real
// remark, small enough that the code being commented on stays visible.
const commentEditorHeight = 4

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
	}
	var cmd tea.Cmd
	e.area, cmd = e.area.Update(msg)
	return e, cmd, editorContinue
}

func (e commentEditor) view(width int) string {
	hint := styleDim.Render("enter save · ctrl+s save & send to agent · alt+enter newline · esc cancel")
	head := styleCommentHead.Render(" comment on " + e.anchor.Path + ":" + lineNoText(e.anchor.LineHint))
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(charm.Info)).
		Width(max(20, width-2)).
		Render(lipgloss.JoinVertical(lipgloss.Left, head, e.area.View(), hint))
}

// comment builds the record from the editor's contents.
func (e commentEditor) comment() review.Comment {
	return review.Comment{
		Author: review.AuthorHuman,
		Body:   strings.TrimRight(e.area.Value(), "\n"),
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
	a, ok := m.AnchorAtCursor()
	if !ok {
		m.status = "put the cursor on a diff line to comment"
		return m, nil
	}
	m.editing = true
	m.editor = newCommentEditor(a, m.hunkWidth)
	return m, textarea.Blink
}

// handleEditorKey routes input to the compose box and applies its outcome.
func (m Model) handleEditorKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	editor, cmd, action := m.editor.update(msg)
	m.editor = editor
	switch action {
	case editorCancel:
		m.editing = false
		// Cancellation is self-evident — the box is gone — so it prints nothing.
		return m, nil
	case editorSave, editorSaveAndSend:
		m.editing = false
		c := m.editor.comment()
		if err := m.SaveComment(c); err != nil {
			m.status = "comment: " + err.Error()
			m.statusErr = true
			return m, nil
		}
		m.comments = append(m.comments, c)
		m.rebuildStream()
		m.status = "comment saved"
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
