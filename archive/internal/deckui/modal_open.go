package deckui

import (
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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
	key, ok := msg.(tea.KeyPressMsg)
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
		it, ok := p.list.SelectedItem().(projectItem)
		if !ok {
			return nil
		}
		if m.hostsAgents() {
			return p.importAndOpen(m, it.project)
		}
		if m.projectOpener == nil {
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

// importAndOpen is the pane-hosting deck's pick: record the project's
// default workspace, close the picker, open that row's agent pane, and ask
// for a refresh so the row itself shows up in the list behind it.
//
// The tmux branch above quits the deck and lets switch-client do the moving.
// There is no client here to move, so the deck does the whole gesture itself.
func (p *openPicker) importAndOpen(m *Model, pr ProjectItem) tea.Cmd {
	if m.projectImporter == nil {
		m.status = "open: this deck cannot import a project — no project importer wired"
		return nil
	}
	item, err := m.projectImporter(pr)
	if err != nil {
		m.status = "open: " + err.Error()
		return nil
	}
	m.active = nil
	m.status = ""
	paneCmd, ok := m.openPaneOrArrangement(item, PaneKindAgent)
	if !ok {
		m.status = "open: imported " + item.ProjectName + " but could not open its agent pane"
	}
	var refreshCmd tea.Cmd
	*m, refreshCmd = m.requestRefresh(false)
	return tea.Batch(paneCmd, refreshCmd)
}

func (p *openPicker) view(m *Model, b box) (left, right string) {
	leftW, rightW := pickerSplit(b.w, b.stacked())
	left = p.renderList(m, box{w: leftW, h: b.h})
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

func (p *openPicker) renderList(m *Model, b box) string {
	return renderPickerPanel(m, &p.list, b)
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
