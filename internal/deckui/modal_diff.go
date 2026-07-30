package deckui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/andrewcohen/awp/internal/review"
	"github.com/andrewcohen/awp/internal/ui"
)

// DiffScope is what a review is of. The surface is the same either way; only the
// revision range the diff comes from differs.
type DiffScope int

const (
	// ScopeWorking is the workspace's uncommitted change — what `c` shows.
	ScopeWorking DiffScope = iota
	// ScopeStackBase is the whole change against its stack base — what `C`
	// shows. Previously this opened a tuicr window; it is the same diff, read on
	// the same surface.
	ScopeStackBase
)

func (s DiffScope) String() string {
	if s == ScopeStackBase {
		return "vs stack base"
	}
	return "working copy"
}

// DiffLoader returns git-format diff text for a workspace at the given scope
// (`jj diff --git` with the matching revision). Installed by the CLI layer via
// WithDiffViewer so the deck package doesn't shell out itself.
type DiffLoader func(item Item, scope DiffScope) (string, error)

// DiffOpener returns the command that opens filePath at line for a
// workspace — an external $EDITOR process, which tea.ExecProcess handles.
type DiffOpener func(item Item, filePath string, line int) tea.Cmd

// CommentStore is the review store seam. The deck package neither knows nor
// cares where findings live; the CLI layer supplies these.
type CommentStore struct {
	// Load returns the findings already anchored in this workspace's review.
	Load func(item Item) ([]review.Comment, error)
	// Save persists a newly written comment.
	Save func(item Item, c review.Comment) error
	// LoadReviewed returns the reviewed-file marks (path → content hash).
	LoadReviewed func(item Item) (map[string]string, error)
	// SaveReviewed persists one mark; an empty hash clears it.
	SaveReviewed func(item Item, path, hash string) error
	// Send hands a saved comment to the workspace's agent. Nil leaves the
	// send-to-agent exit unavailable, which the editor reports rather than
	// silently doing nothing.
	Send CommentSender
}

// CommentSender delivers a comment to a workspace's agent.
type CommentSender func(item Item, c review.Comment) error

// diffModalChrome is the rows the deck's own chrome takes around a body
// modal: the panel's Padding(1, 1, 1, 1) plus the footer block.
const diffModalChrome = 8

// diffModal is the `c` overlay: awp's own diff viewer (internal/ui, the
// same one `awp diff` runs) rendered in place of the row list, scoped to
// the selected workspace's working change.
//
// It wraps ui.Model rather than reimplementing it, and renders ui.Body so
// the deck keeps ownership of the header and footer. Close keys are
// intercepted here before forwarding, so the inner model never reaches its
// standalone `q` → tea.Quit path and take the whole deck down with it.
type diffModal struct {
	inner ui.Model
	label string
	scope DiffScope
	// Styles are cached here rather than built per frame — view and
	// footerHelp are render paths.
	muted  lipgloss.Style
	danger lipgloss.Style
	panel  lipgloss.Style
}

// newDiffModal builds the modal and returns the command that loads the
// first diff.
func newDiffModal(item Item, scope DiffScope, load DiffLoader, open DiffOpener, comments CommentStore) (*diffModal, tea.Cmd) {
	inner := ui.New(item.Path,
		func() (string, error) { return load(item, scope) },
		func(filePath string, line int) tea.Cmd {
			if open == nil {
				return nil
			}
			return open(item, filePath, line)
		},
	)
	if comments.Save != nil {
		inner.SaveComment = func(c review.Comment) error { return comments.Save(item, c) }
	}
	if comments.Send != nil {
		inner.SendComment = func(c review.Comment) error { return comments.Send(item, c) }
	}
	if comments.SaveReviewed != nil {
		inner.MarkReviewed = func(path, hash string) error { return comments.SaveReviewed(item, path, hash) }
	}
	if comments.LoadReviewed != nil {
		if marks, err := comments.LoadReviewed(item); err == nil {
			inner.SetReviewed(marks)
		}
	}
	if comments.Load != nil {
		// Best-effort: a review that cannot be read should still open as a
		// readable diff rather than refusing to open at all.
		if existing, err := comments.Load(item); err == nil {
			inner.SetComments(existing)
		}
	}
	dm := &diffModal{
		inner:  inner,
		label:  item.ProjectName + "/" + item.WorkspaceName,
		scope:  scope,
		muted:  lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)),
		danger: lipgloss.NewStyle().Foreground(lipgloss.Color(colDanger)),
		panel:  lipgloss.NewStyle().Padding(1, 1, 1, 1),
	}
	return dm, dm.inner.Init()
}

func (dm *diffModal) footerHelp() string {
	status, isErr := dm.inner.Status()
	style := dm.muted
	if isErr {
		style = dm.danger
	}
	hint := " · j/k scroll · c comment · r reviewed · {/} hunk · g/G ends · h/l/0/$ pan · tab pane · e $EDITOR · w wrap · / filter · esc close"
	return style.Render(dm.label + " · " + dm.scope.String() + " · " + status + hint)
}

func (dm *diffModal) update(m *Model, msg tea.Msg) tea.Cmd {
	// While the viewer's filter has focus every key belongs to it —
	// including the ones that would otherwise close the modal.
	if key, ok := msg.(tea.KeyMsg); ok && !dm.inner.Filtering() {
		switch key.String() {
		case "c", "esc", "q", "ctrl+c":
			m.active = nil
			return tea.ClearScreen
		}
	}
	updated, cmd := dm.inner.Update(msg)
	if inner, ok := updated.(ui.Model); ok {
		dm.inner = inner
	}
	return cmd
}

func (dm *diffModal) view(m *Model) (string, string) {
	// Panel padding matches every other deck body panel; the inner width
	// accounts for the 1 col of padding on each side.
	innerWidth := m.width - 2
	if innerWidth < 1 {
		return "", ""
	}
	bodyHeight := m.height - diffModalChrome
	dm.inner.SetSize(innerWidth, bodyHeight)
	return dm.panel.Render(dm.inner.Body(innerWidth, bodyHeight)), ""
}
