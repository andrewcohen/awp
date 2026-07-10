package deckui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// confirmDeleteModal is the delete confirmation popover. A normal
// workspace delete is a y/n prompt; deleting the "default" workspace is
// reinterpreted as deleting the whole project and requires typing the
// project name to confirm (isProject + input).
type confirmDeleteModal struct {
	target    Item
	isProject bool
	input     textinput.Model
	err       string
}

// newConfirmDelete builds the modal for target. Returns the modal, the
// command to run, and a status line for the deck to show.
func newConfirmDelete(target Item) (*confirmDeleteModal, tea.Cmd, string) {
	c := &confirmDeleteModal{target: target}
	if strings.TrimSpace(target.WorkspaceName) == "default" {
		// "Deleting" the default workspace deletes the whole project from
		// the deck: every non-default workspace under this repo is removed
		// and the project is dropped from workspace state. The default jj
		// workspace itself stays. Require typing the project name to
		// confirm — it removes more than a single-workspace delete.
		c.isProject = true
		ti := textinput.New()
		ti.Placeholder = target.ProjectName
		ti.CharLimit = 128
		ti.Focus()
		c.input = ti
		return c, textinput.Blink, fmt.Sprintf("delete project %q?", target.ProjectName)
	}
	return c, nil, fmt.Sprintf("delete %s? [y/N]", target.WorkspaceName)
}

func (c *confirmDeleteModal) update(m *Model, msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		if c.isProject {
			var cmd tea.Cmd
			c.input, cmd = c.input.Update(msg)
			return cmd
		}
		return nil
	}
	if c.isProject {
		switch key.String() {
		case "esc", "ctrl+c":
			m.active = nil
			m.status = ""
			return tea.ClearScreen
		case "enter":
			if strings.TrimSpace(c.input.Value()) != c.target.ProjectName {
				c.err = "project name didn't match"
				return nil
			}
			m.active = nil
			if m.handler == nil {
				m.status = "delete project: handler not configured"
				return tea.ClearScreen
			}
			m.deleteTarget = c.target
			updated, cmd := m.startAction(ActionDeleteProject, c.target, "")
			*m = updated.(Model)
			return batchCmds(cmd, tea.ClearScreen)
		}
		var cmd tea.Cmd
		c.input, cmd = c.input.Update(key)
		c.err = ""
		return cmd
	}
	switch strings.ToLower(key.String()) {
	case "y", "enter":
		m.active = nil
		if m.handler == nil {
			m.status = "delete: handler not configured"
			return nil
		}
		m.deleteTarget = c.target
		updated, cmd := m.startAction(ActionDelete, c.target, "")
		*m = updated.(Model)
		return cmd
	case "n", "esc", "q":
		m.active = nil
		m.status = ""
	}
	return nil
}

func (c *confirmDeleteModal) footerHelp() string { return "" }

func (c *confirmDeleteModal) renderPopover(m *Model) string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(colDanger)).
		Padding(1, 2).
		Width(60)
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colDanger))
	mutedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted))
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colDanger)).Bold(true)

	if c.isProject {
		project := strings.TrimSpace(c.target.ProjectName)
		if project == "" {
			project = "this project"
		}
		lines := []string{
			titleStyle.Render("Delete project " + project + "?"),
			"",
			mutedStyle.Render("Removes every non-default workspace under this repo and"),
			mutedStyle.Render("drops the project from the deck. The default workspace"),
			mutedStyle.Render("itself is left intact."),
			"",
			mutedStyle.Render("Type the project name to confirm:"),
			c.input.View(),
		}
		if c.err != "" {
			lines = append(lines, "", errStyle.Render(c.err))
		}
		lines = append(lines, "", hintStyle.Render("enter confirm · esc cancel"))
		return box.Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
	}

	name := strings.TrimSpace(c.target.WorkspaceName)
	if name == "" {
		name = "this workspace"
	}
	lines := []string{
		titleStyle.Render("Delete workspace " + name + "?"),
		"",
		hintStyle.Render("y confirm · n / esc cancel"),
	}
	return box.Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
}

// confirmMergeModal is the merge-PR confirmation popover (y/N).
type confirmMergeModal struct {
	target Item
	status PRStatus
}

// newConfirmMerge builds the modal and the status line to show.
func newConfirmMerge(target Item, status PRStatus) (*confirmMergeModal, string) {
	return &confirmMergeModal{target: target, status: status},
		fmt.Sprintf("merge PR #%d? [y/N]", status.Number)
}

func (c *confirmMergeModal) update(m *Model, msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch strings.ToLower(key.String()) {
	case "y", "enter":
		m.active = nil
		if m.handler == nil {
			m.status = "merge: handler not configured"
			return nil
		}
		updated, cmd := m.startAction(ActionMergePR, c.target, strconv.Itoa(c.status.Number))
		*m = updated.(Model)
		return cmd
	case "n", "esc", "q":
		m.active = nil
		m.status = ""
	}
	return nil
}

func (c *confirmMergeModal) footerHelp() string { return "" }

func (c *confirmMergeModal) renderPopover(m *Model) string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(colAccent)).
		Padding(1, 2).
		Width(64)
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colAccent))
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted))
	prStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colInfo)).Bold(true)
	cmdStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colStrong))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted))

	s := c.status
	cmd := fmt.Sprintf("gh pr merge %d --squash", s.Number)

	lines := []string{
		titleStyle.Render(fmt.Sprintf("Merge PR %s?", prStyle.Render("#"+strconv.Itoa(s.Number)))),
		"",
	}
	if title := strings.TrimSpace(s.Title); title != "" {
		lines = append(lines, truncate(title, 58))
	}
	lines = append(lines,
		"",
		labelStyle.Render("Runs:"),
		cmdStyle.Render("  "+cmd),
	)
	if s.IsInMergeQueue {
		// The rocket is showing for this row — the PR is already in the
		// repo's merge queue.
		lines = append(lines, labelStyle.Render(fmt.Sprintf("  %s already in the merge queue — re-adds it to the queue", prGlyphInQueue)))
	} else {
		lines = append(lines, labelStyle.Render("  squash by default; falls back to the merge queue if required"))
	}
	lines = append(lines,
		"",
		hintStyle.Render("y confirm · n / esc cancel"),
	)
	return box.Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
}
