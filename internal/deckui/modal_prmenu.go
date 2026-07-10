package deckui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// prMenuModal is the PR action chord (the `p` key): a keys-only menu
// (o/d/r/m/s) with a status-bar hint. It renders no overlay of its own —
// the row list stays visible beneath — so it is a bodyModal whose view is
// the row list, and its footerHelp is empty so the footer falls back to
// the status hint. It holds no state; each action re-reads the selected
// row.
type prMenuModal struct{}

func (prMenuModal) update(m *Model, msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch key.String() {
	case "esc", "q", "ctrl+c":
		m.active = nil
		m.status = "pr: cancelled"
		return nil
	case "o":
		m.active = nil
		item, ok := m.selected()
		if !ok {
			return nil
		}
		status, _, ok := m.prStatusLabelForItem(item)
		if !ok {
			m.status = "pr: no PR for this workspace"
			return nil
		}
		url := strings.TrimSpace(status.URL)
		if url == "" {
			m.status = "pr: no URL on cached PR (re-open the deck to refresh)"
			return nil
		}
		if err := openBrowser(url); err != nil {
			m.status = "pr: " + err.Error()
		} else {
			m.status = "pr: opened " + url
		}
		return nil
	case "d":
		m.active = nil
		item, ok := m.selected()
		if !ok {
			return nil
		}
		status, _, ok := m.prStatusLabelForItem(item)
		if !ok {
			m.status = "pr: no PR for this workspace"
			return nil
		}
		if status.Number <= 0 {
			m.status = "pr: description unavailable (no PR number cached — try p s)"
			return nil
		}
		// Open the description the way a review opens: a dedicated tmux
		// window in the workspace's session. gh renders the body with TTY
		// formatting; less keeps it scrollable and searchable, and q drops
		// back to a shell in the window.
		winCmd := fmt.Sprintf("env GH_FORCE_TTY=100%% gh pr view %d | less -R", status.Number)
		updated, cmd := m.trigger(ActionOpenWindow, "pr:"+winCmd)
		*m = updated.(Model)
		return cmd
	case "r":
		m.active = nil
		item, ok := m.selected()
		if !ok {
			return nil
		}
		status, label, ok := m.prStatusLabelForItem(item)
		if !ok {
			m.status = "pr: no PR for this workspace"
			return nil
		}
		prompt := prRepairPrompt(status, item.BookmarkCommitID, itemIsMyPR(item, m.bookmarkPrefix))
		if prompt == "" {
			m.status = "pr: nothing to repair (" + label + ")"
			return nil
		}
		// Don't dispatch the repair prompt straight to the agent. Hand it
		// to the send-prompt form prepopulated, so the user can review and
		// edit it before sending. Same form/flow as the `A` dialog.
		m.promptMode = true
		var initCmd tea.Cmd
		m.promptForm, initCmd = newPromptForm(item, prompt)
		m.status = "repair: review prompt · enter send · ctrl+g $EDITOR · esc cancel"
		return batchCmds(initCmd, tea.ClearScreen)
	case "m":
		m.active = nil
		item, ok := m.selected()
		if !ok {
			return nil
		}
		status, _, ok := m.prStatusLabelForItem(item)
		if !ok {
			m.status = "pr: no PR for this workspace"
			return nil
		}
		if status.Number <= 0 {
			m.status = "pr: merge unavailable (no PR number cached — try p s)"
			return nil
		}
		if status.State != PRStateOpen {
			m.status = fmt.Sprintf("pr: #%d is %s — nothing to merge", status.Number, strings.ToLower(string(status.State)))
			return nil
		}
		var mergeModal *confirmMergeModal
		mergeModal, m.status = newConfirmMerge(item, status)
		m.active = mergeModal
		return tea.ClearScreen
	case "s":
		m.active = nil
		item, ok := m.selected()
		if !ok || strings.TrimSpace(item.WorkspaceName) == "" {
			m.status = "pr: select a workspace row"
			return nil
		}
		var prModal *prNumberModal
		var prCmd tea.Cmd
		prModal, prCmd, m.status = newPRNumberModal(item)
		m.active = prModal
		return prCmd
	}
	return nil
}

func (prMenuModal) view(m *Model) (left, right string) { return m.renderList(m.width), "" }

func (prMenuModal) footerHelp() string { return "" }
