package deckui

import (
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// openPicker is the project "open" picker (the `o` key): a fuzzy-filterable
// list of projects discovered from deck.project_roots. Selecting one
// summons (or creates) its default workspace and quits the deck so tmux
// switches to it. It is the first modal migrated onto Model.active.
type openPicker struct {
	list    list.Model
	loading bool
}

// newOpenPicker builds the picker in its loading state, showing the
// scanning placeholder until projectFinder returns a ProjectsDoneMsg.
func newOpenPicker(glyph string) *openPicker {
	l := newOpenList()
	l.SetShowStatusBar(false)
	l.SetItems([]list.Item{loadingItem{label: glyph + " scanning project roots..."}})
	return &openPicker{list: l, loading: true}
}

// setProjects populates the list from a completed project scan.
func (p *openPicker) setProjects(projects []ProjectItem) {
	items := make([]list.Item, 0, len(projects))
	for _, pr := range projects {
		items = append(items, projectItem{project: pr})
	}
	p.loading = false
	p.list.SetShowStatusBar(true)
	p.list.SetItems(items)
	p.list.ResetSelected()
}

// tickLoading refreshes the animated spinner glyph on the placeholder row
// while the scan is in flight.
func (p *openPicker) tickLoading(glyph string) {
	p.list.SetItems([]list.Item{loadingItem{label: glyph + " scanning project roots..."}})
}

func (p *openPicker) update(m *Model, msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		// Non-key messages (filter matches, cursor blink) drive the
		// list's own async machinery so filtering applies as you type.
		var cmd tea.Cmd
		p.list, cmd = p.list.Update(msg)
		return cmd
	}
	if p.loading {
		switch key.String() {
		case "esc", "ctrl+c":
			m.active = nil
			m.status = ""
		}
		return nil
	}
	filtering := p.list.FilterState() == list.Filtering
	switch key.String() {
	case "enter":
		// enter during filter commits the filter; a second enter picks
		// (see the picker convention shared with bookmark/review).
		if filtering {
			break
		}
		if m.projectOpener == nil {
			return nil
		}
		it, ok := p.list.SelectedItem().(projectItem)
		if !ok {
			return nil
		}
		if err := m.projectOpener(it.project); err != nil {
			m.status = "open: " + err.Error()
			return nil
		}
		return tea.Quit
	case "esc", "ctrl+c":
		if !filtering && p.list.FilterState() != list.FilterApplied {
			m.active = nil
			m.status = ""
			return nil
		}
	}
	var cmd tea.Cmd
	p.list, cmd = p.list.Update(msg)
	return cmd
}

func (p *openPicker) view(m *Model) (left, right string) {
	leftW, rightW := pickerSplit(m.width, m.deckStacked())
	left = p.renderList(m, leftW)
	if rightW > 0 {
		right = p.renderDetails(rightW)
	}
	return left, right
}

func (p *openPicker) footerHelp() string {
	if p.loading {
		return ""
	}
	return p.list.Help.ShortHelpView(pickerShortHelp(p.list))
}

func (p *openPicker) renderList(m *Model, width int) string {
	containerStyle := lipgloss.NewStyle().Width(width).Padding(2, 1, 1, 1)
	listWidth := width - 2
	if listWidth < 8 {
		listWidth = 8
	}
	listHeight := m.height - 5
	if listHeight < 3 {
		listHeight = 3
	}
	p.list.SetSize(listWidth, listHeight)
	return containerStyle.Render(p.list.View())
}

func (p *openPicker) renderDetails(width int) string {
	title := lipgloss.NewStyle().Bold(true).Render("open")
	lines := []string{title, ""}
	if it, ok := p.list.SelectedItem().(projectItem); ok {
		lines = append(lines,
			"Selection: "+it.project.Name,
			"Path:      "+it.project.Path,
		)
	} else {
		lines = append(lines, "Pick a project to summon (or create) its default workspace.")
	}
	lines = append(lines, "",
		"Keys:",
		"/        fuzzy filter",
		"↑/↓      navigate",
		"enter    open",
		"esc      cancel",
	)
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}
