package deckui

import (
	"errors"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/andrewcohen/awp/internal/charm"
)

// ErrWorkspaceFormCancelled is returned when the user cancels the open
// workspace form (esc or "Cancel" on the action row). Callers map this
// to the CLI's silent-cancel exit code.
var ErrWorkspaceFormCancelled = errors.New("workspace form cancelled")

// RunWorkspaceForm runs the unified new-workspace form as a standalone
// tea.Program, suitable for CLI use. The form's "pick a bookmark…"
// branch surfaces an inline picker built from listBookmarks; on pick the
// program returns to the form with the selected value recorded.
//
// Returns the submitted request, ErrWorkspaceFormCancelled, or any
// terminal/program error. listBookmarks may be nil — the picker
// branch then reports the unavailable state and reverts to "main".
func RunWorkspaceForm(
	in io.Reader,
	out io.Writer,
	initial NewWorkspaceInitial,
	bookmarkPrefix string,
	trunkName string,
	listBookmarks func() ([]string, error),
) (NewWorkspaceRequest, error) {
	if charm.IsDumbTerminal() {
		return NewWorkspaceRequest{}, errors.New("interactive workspace form not available in dumb terminal")
	}
	model := newWorkspaceFormProgram(initial, bookmarkPrefix, trunkName, listBookmarks)
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithInput(in), tea.WithOutput(out))
	final, err := program.Run()
	if err != nil {
		return NewWorkspaceRequest{}, err
	}
	result, ok := final.(workspaceFormProgram)
	if !ok {
		return NewWorkspaceRequest{}, errors.New("unexpected workspace form state")
	}
	if result.cancelled {
		return NewWorkspaceRequest{}, ErrWorkspaceFormCancelled
	}
	return result.form.request(), nil
}

// workspaceFormProgram is the tea.Model wrapper that drives the
// unified workspace form for the CLI. It transitions between two
// states — form and bookmark picker — within a single program, the
// same pattern the deck uses to avoid a nested tea.Program.
type workspaceFormProgram struct {
	form          newWorkspaceForm
	formInit      tea.Cmd
	listBookmarks func() ([]string, error)

	pickerMode  bool
	pickerList  list.Model
	pickerErr   string
	pickerReady bool

	width  int
	height int

	cancelled bool
	submitted bool
}

func newWorkspaceFormProgram(
	initial NewWorkspaceInitial,
	prefix string,
	trunkName string,
	listBookmarks func() ([]string, error),
) workspaceFormProgram {
	form, initCmd := newNewWorkspaceForm(initial, prefix, trunkName)
	return workspaceFormProgram{
		form:          form,
		formInit:      initCmd,
		listBookmarks: listBookmarks,
		pickerList:    newBookmarkList(),
	}
}

func (p workspaceFormProgram) Init() tea.Cmd { return p.formInit }

type workspaceFormBookmarksMsg struct {
	names []string
	err   error
}

func (p workspaceFormProgram) loadBookmarks() tea.Cmd {
	if p.listBookmarks == nil {
		return func() tea.Msg {
			return workspaceFormBookmarksMsg{err: errors.New("bookmark lister not configured")}
		}
	}
	return func() tea.Msg {
		names, err := p.listBookmarks()
		return workspaceFormBookmarksMsg{names: names, err: err}
	}
}

func (p workspaceFormProgram) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		p.width = m.Width
		p.height = m.Height
		p.pickerList.SetSize(m.Width-4, m.Height-6)
		return p, nil
	case workspaceFormBookmarksMsg:
		p.pickerReady = true
		if m.err != nil {
			p.pickerErr = m.err.Error()
			p.pickerList.SetItems(nil)
			return p, nil
		}
		items := make([]list.Item, 0, len(m.names))
		for _, n := range m.names {
			n = strings.TrimSpace(n)
			if n == "" {
				continue
			}
			items = append(items, bookmarkItem{name: n})
		}
		p.pickerErr = ""
		p.pickerList.SetItems(items)
		p.pickerList.ResetSelected()
		return p, nil
	}

	if p.pickerMode {
		return p.updatePicker(msg)
	}
	return p.updateForm(msg)
}

func (p workspaceFormProgram) updateForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	form, cmd, action := p.form.update(msg)
	p.form = form
	switch action {
	case newFormActionCancel:
		p.cancelled = true
		return p, tea.Quit
	case newFormActionSubmit:
		p.submitted = true
		return p, tea.Quit
	case newFormActionOpenPicker:
		p.pickerMode = true
		p.pickerReady = false
		p.pickerErr = ""
		p.pickerList.Title = "start from: pick a bookmark"
		p.pickerList.SetShowStatusBar(false)
		p.pickerList.SetItems([]list.Item{loadingItem{label: "loading bookmarks..."}})
		return p, tea.Batch(cmd, p.loadBookmarks())
	}
	return p, cmd
}

func (p workspaceFormProgram) updatePicker(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		if !p.pickerReady && p.pickerErr == "" {
			switch keyMsg.String() {
			case "esc", "ctrl+c":
				p.pickerMode = false
				p.form.RevertStartFrom()
				return p, nil
			}
			return p, nil
		}
		filtering := p.pickerList.FilterState() == list.Filtering
		switch keyMsg.String() {
		case "enter":
			if filtering {
				break
			}
			if it, ok := p.pickerList.SelectedItem().(bookmarkItem); ok && strings.TrimSpace(it.name) != "" {
				p.pickerMode = false
				p.form.SetPickedBookmark(it.name)
				return p, nil
			}
			return p, nil
		case "esc", "ctrl+c":
			if !filtering && p.pickerList.FilterState() != list.FilterApplied {
				p.pickerMode = false
				p.form.RevertStartFrom()
				return p, nil
			}
		}
	}
	var cmd tea.Cmd
	p.pickerList, cmd = p.pickerList.Update(msg)
	return p, cmd
}

func (p workspaceFormProgram) View() string {
	if p.pickerMode {
		if p.pickerErr != "" {
			errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colDanger))
			help := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)).
				Render("esc — back to form")
			return errStyle.Render("Error: "+p.pickerErr) + "\n\n" + help
		}
		return p.pickerList.View()
	}
	return p.form.view(p.width, p.height)
}

