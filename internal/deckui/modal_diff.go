package deckui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/andrewcohen/awp/internal/ui"
)

// DiffLoader returns git-format diff text for a workspace's working change
// (`jj diff --git` in the workspace). Installed by the CLI layer via
// WithDiffViewer so the deck package doesn't shell out itself.
type DiffLoader func(item Item) (string, error)

// DiffOpener returns the command that opens filePath at line for a
// workspace — an external $EDITOR process, which tea.ExecProcess handles.
type DiffOpener func(item Item, filePath string, line int) tea.Cmd

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
	// Styles are cached here rather than built per frame — view and
	// footerHelp are render paths.
	muted  lipgloss.Style
	danger lipgloss.Style
	panel  lipgloss.Style
}

// newDiffModal builds the modal and returns the command that loads the
// first diff.
func newDiffModal(item Item, load DiffLoader, open DiffOpener) (*diffModal, tea.Cmd) {
	inner := ui.New(item.Path,
		func() (string, error) { return load(item) },
		func(filePath string, line int) tea.Cmd {
			if open == nil {
				return nil
			}
			return open(item, filePath, line)
		},
	)
	dm := &diffModal{
		inner:  inner,
		label:  item.ProjectName + "/" + item.WorkspaceName,
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
	hint := " · j/k scroll · h/l pan · {/} hunk · tab pane · e open in $EDITOR · w wrap · r refresh · / filter · esc close"
	return style.Render(dm.label + " · " + status + hint)
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
