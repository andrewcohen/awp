package deckui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// prNumberModal is the `p s` chord's numeric input popover: pin the
// selected workspace to a specific PR number (blank/0 clears).
type prNumberModal struct {
	target Item
	input  textinput.Model
	err    string
}

// newPRNumberModal builds the modal for item, returning it plus the
// command and status line to show.
func newPRNumberModal(item Item) (*prNumberModal, tea.Cmd, string) {
	ti := textinput.New()
	ti.Placeholder = "PR # (blank or 0 to clear)"
	ti.CharLimit = 12
	if item.PRNumber > 0 {
		ti.SetValue(strconv.Itoa(item.PRNumber))
	}
	ti.Focus()
	return &prNumberModal{target: item, input: ti},
		batchCmds(tea.ClearScreen, textinput.Blink),
		fmt.Sprintf("set PR # for %s/%s — enter saves · esc cancels", item.ProjectName, item.WorkspaceName)
}

func (p *prNumberModal) update(m *Model, msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		var cmd tea.Cmd
		p.input, cmd = p.input.Update(msg)
		return cmd
	}
	switch key.String() {
	case "esc", "ctrl+c":
		m.active = nil
		m.status = ""
		return tea.ClearScreen
	case "enter":
		typed := strings.TrimSpace(p.input.Value())
		prNumber := 0
		if typed != "" {
			n, err := strconv.Atoi(typed)
			if err != nil || n < 0 {
				p.err = "enter a non-negative integer (or blank to clear)"
				return nil
			}
			prNumber = n
		}
		if m.prNumberLinkHandler == nil {
			m.active = nil
			m.status = "pr: set PR # handler not configured"
			return tea.ClearScreen
		}
		target := p.target
		if err := m.prNumberLinkHandler(target, prNumber); err != nil {
			p.err = err.Error()
			return nil
		}
		m.active = nil
		if prNumber == 0 {
			m.status = fmt.Sprintf("pr: cleared PR # override on %s/%s", target.ProjectName, target.WorkspaceName)
		} else {
			m.status = fmt.Sprintf("pr: pinned %s/%s → PR #%d", target.ProjectName, target.WorkspaceName, prNumber)
		}
		// Force a PR-status refetch alongside the row refresh: the override
		// may point at a PR not in the cache yet (cold start, stale cache,
		// or a PR that appeared after the last gh poll). Bypassing the 60s
		// throttle ensures the pinned PR's status shows up on the next
		// paint instead of waiting up to a minute.
		var prCmd tea.Cmd
		*m, prCmd = m.forcePRStatusRefresh(target.RepoRoot)
		var refreshCmd tea.Cmd
		*m, refreshCmd = m.requestRefresh(true)
		return batchCmds(tea.ClearScreen, refreshCmd, prCmd)
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(key)
	p.err = ""
	return cmd
}

func (p *prNumberModal) footerHelp() string { return "" }

func (p *prNumberModal) renderPopover(m *Model) string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(colAccent)).
		Padding(1, 2).
		Width(60)
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colAccent))
	mutedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted))
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colDanger)).Bold(true)

	target := strings.TrimSpace(p.target.WorkspaceName)
	if target == "" {
		target = "this workspace"
	}
	current := "none"
	if p.target.PRNumber > 0 {
		current = fmt.Sprintf("#%d", p.target.PRNumber)
	}
	lines := []string{
		titleStyle.Render("Pin PR # for " + target),
		"",
		mutedStyle.Render("Pins this workspace to a specific PR so the deck"),
		mutedStyle.Render("resolves status directly by number."),
		"",
		mutedStyle.Render("Current PR: " + current),
		"",
		mutedStyle.Render("PR number (blank or 0 clears):"),
		p.input.View(),
	}
	if p.err != "" {
		lines = append(lines, "", errStyle.Render(p.err))
	}
	lines = append(lines, "", hintStyle.Render("enter save · esc cancel"))
	return box.Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
}

// pinAliasModal is the `gR` alias-rename popover: set the display label
// for a pin register (blank clears).
type pinAliasModal struct {
	target string // register key being renamed
	input  textinput.Model
	err    string
}

// newPinAliasModal builds the modal for the register key, seeding the
// input with the current alias.
func newPinAliasModal(m *Model, key string) (*pinAliasModal, tea.Cmd, string) {
	ti := textinput.New()
	ti.Placeholder = "group name (blank clears)"
	ti.CharLimit = 40
	ti.SetValue(strings.TrimSpace(m.pinGroupAliases[key]))
	ti.Focus()
	return &pinAliasModal{target: key, input: ti},
		batchCmds(tea.ClearScreen, textinput.Blink),
		fmt.Sprintf("name group %s — enter saves · esc cancels", pinGroupChordLetter(key))
}

func (p *pinAliasModal) update(m *Model, msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		var cmd tea.Cmd
		p.input, cmd = p.input.Update(msg)
		return cmd
	}
	switch key.String() {
	case "esc", "ctrl+c":
		m.active = nil
		m.status = ""
		return tea.ClearScreen
	case "enter":
		alias := strings.TrimSpace(p.input.Value())
		if m.pinGroupAliasHandler != nil {
			if err := m.pinGroupAliasHandler(p.target, alias); err != nil {
				p.err = err.Error()
				return nil
			}
		}
		// Update the in-memory map so the section header re-renders with the
		// new label on the next paint without a reload.
		if m.pinGroupAliases == nil {
			m.pinGroupAliases = map[string]string{}
		}
		if alias == "" {
			delete(m.pinGroupAliases, p.target)
		} else {
			m.pinGroupAliases[p.target] = alias
		}
		m.active = nil
		if alias == "" {
			m.status = fmt.Sprintf("pin: cleared name for group %s", pinGroupChordLetter(p.target))
		} else {
			m.status = fmt.Sprintf("pin: group %s → %s", pinGroupChordLetter(p.target), alias)
		}
		return tea.ClearScreen
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(key)
	p.err = ""
	return cmd
}

func (p *pinAliasModal) footerHelp() string { return "" }

func (p *pinAliasModal) renderPopover(m *Model) string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(colAccent)).
		Padding(1, 2).
		Width(60)
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colAccent))
	mutedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted))
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colDanger)).Bold(true)

	current := "none"
	if a := strings.TrimSpace(m.pinGroupAliases[p.target]); a != "" {
		current = a
	}
	lines := []string{
		titleStyle.Render("Name pin group " + pinGroupChordLetter(p.target)),
		"",
		mutedStyle.Render("Sets the display label for this register in the"),
		mutedStyle.Render("pinned section headers. Cosmetic — the register"),
		mutedStyle.Render("key stays the letter you pin with."),
		"",
		mutedStyle.Render("Current name: " + current),
		"",
		mutedStyle.Render("Name (blank clears):"),
		p.input.View(),
	}
	if p.err != "" {
		lines = append(lines, "", errStyle.Render(p.err))
	}
	lines = append(lines, "", mutedStyle.Render("enter save · esc cancel"))
	return box.Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
}
