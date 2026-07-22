package deckui

import (
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// reviewPicker is the PR "review" picker (the `r` key / new-menu review
// flow): a filterable list of open PRs in the selected row's repo.
// Selecting one refreshes that repo's PR status and dispatches an
// ActionReview. It carries its own reviewItemDelegate for the per-field
// PR row coloring.
type reviewPicker struct {
	list     list.Model
	delegate *reviewItemDelegate
	loading  bool
}

// newReviewPicker builds the picker in its loading state, showing the
// placeholder until prFetcher returns a PRFetchDoneMsg.
func newReviewPicker(glyph string) *reviewPicker {
	l, d := newReviewList()
	l.SetShowStatusBar(false)
	l.SetItems([]list.Item{loadingItem{label: glyph + " loading PRs..."}})
	return &reviewPicker{list: l, delegate: d, loading: true}
}

// setPRs populates the list from a completed PR fetch and returns the
// status line to show (empty unless there were no PRs).
func (p *reviewPicker) setPRs(prs []PRItem) string {
	items := make([]list.Item, 0, len(prs))
	for _, pr := range prs {
		items = append(items, reviewItem{pr: pr})
	}
	p.loading = false
	p.list.SetShowStatusBar(true)
	p.list.SetItems(items)
	p.list.ResetSelected()
	p.delegate.recompute(items)
	if len(prs) == 0 {
		return "review: no open PRs (esc to cancel)"
	}
	return ""
}

// tickLoading refreshes the animated spinner glyph while the fetch runs.
func (p *reviewPicker) tickLoading(glyph string) {
	p.list.SetItems([]list.Item{loadingItem{label: glyph + " loading PRs..."}})
}

func (p *reviewPicker) update(m *Model, msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		var cmd tea.Cmd
		p.list, cmd = p.list.Update(msg)
		return cmd
	}
	if p.loading {
		switch key.String() {
		case "esc", "q", "ctrl+c":
			m.active = nil
			m.status = ""
		}
		return nil
	}
	filtering := p.list.FilterState() == list.Filtering
	// Typing a digit while unfiltered jumps straight into filter mode with
	// that digit — the usual flow is `r` then the PR number, so we skip the
	// explicit `/`.
	if p.list.FilterState() == list.Unfiltered && isDigitKey(key) {
		var startCmd tea.Cmd
		p.list, startCmd = p.list.Update(filterStartMsg(p.list))
		var typeCmd tea.Cmd
		p.list, typeCmd = p.list.Update(msg)
		return batchCmds(startCmd, typeCmd)
	}
	switch key.String() {
	case "enter":
		// enter during filter commits the filter; a second enter picks.
		if filtering {
			break
		}
		if m.handler == nil {
			return nil
		}
		it, ok := p.list.SelectedItem().(reviewItem)
		if !ok {
			return nil
		}
		pr := it.pr
		item, _ := m.selected()
		m.active = nil
		var prCmd tea.Cmd
		*m, prCmd = m.forcePRStatusRefresh(item.RepoRoot)
		// pr.HeadRef lets startReview predict the review workspace name and
		// show it as an optimistic "setting up" row right away.
		updated, dispatchCmd := m.startReview(item, pr.Number, pr.HeadRef)
		*m = updated.(Model)
		return batchCmds(prCmd, dispatchCmd)
	case "esc", "ctrl+c":
		if !filtering && p.list.FilterState() != list.FilterApplied {
			m.active = nil
			m.status = ""
			return nil
		}
	case "q":
		if !filtering {
			m.active = nil
			m.status = ""
			return nil
		}
	}
	var cmd tea.Cmd
	p.list, cmd = p.list.Update(msg)
	return cmd
}

// isDigitKey reports whether the key press is a single 0-9 digit rune.
func isDigitKey(k tea.KeyMsg) bool {
	if k.Type != tea.KeyRunes || len(k.Runes) != 1 {
		return false
	}
	r := k.Runes[0]
	return r >= '0' && r <= '9'
}

// filterStartMsg builds the key press that puts a bubbles list into its
// filtering state, derived from the list's own Filter binding so it stays
// correct if the binding is ever rethemed.
func filterStartMsg(l list.Model) tea.KeyMsg {
	if keys := l.KeyMap.Filter.Keys(); len(keys) > 0 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(keys[0])}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}}
}

func (p *reviewPicker) view(m *Model) (left, right string) {
	return p.renderList(m, m.width), ""
}

func (p *reviewPicker) footerHelp() string {
	if p.loading {
		return ""
	}
	return p.list.Help.ShortHelpView(pickerShortHelp(p.list))
}

func (p *reviewPicker) renderList(m *Model, width int) string {
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
