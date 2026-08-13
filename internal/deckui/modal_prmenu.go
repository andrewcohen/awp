package deckui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// prMenuModal is the PR action chord (the `p` key): a keys-only menu
// (o/d/r/m/s). It renders no overlay of its own — the row list stays visible
// beneath — so it is a bodyModal whose view is the row list, and a chordModal
// whose menu goes on the deck's top row, where every other menu goes. It holds no
// state; each action re-reads the selected row.
type prMenuModal struct{}

// prMenuHint is the menu as the top row shows it.
func prMenuHint() string {
	return "pr: o open in browser · d description · D description in a window · " +
		"r repair · s set PR # · esc cancel"
}

func (prMenuModal) chordMenu() string { return prMenuHint() }

// prDescWindow is the tmux window `p D` opens the description into. Named for
// what is in it: the session already has `agent`, `editor`, `review` and `vcs`,
// and this used to join them as `pr`, which said which subject rather than which
// of the several PR things you might have open.
const prDescWindow = "pr description"

// prNumberForAction is the selected row's PR number, or the reason there isn't
// one.
//
// Shared by `d` and `D` because they are the same request with two destinations,
// and the three ways it can fail — no row, no PR, a PR whose number was never
// cached — are the same three either way. Split out when the second key arrived
// rather than copied, since a refusal that only one of them knows how to give is
// how the two would start disagreeing about when they work.
func prNumberForAction(m *Model, what string) (Item, int, bool) {
	item, ok := m.selected()
	if !ok {
		return Item{}, 0, false
	}
	status, _, ok := m.prStatusLabelForItem(item)
	if !ok {
		m.status = "pr: no PR for this workspace"
		return Item{}, 0, false
	}
	if status.Number <= 0 {
		m.status = "pr: " + what + " unavailable (no PR number cached — try p s)"
		return Item{}, 0, false
	}
	return item, status.Number, true
}

func (prMenuModal) update(m *Model, msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
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
		item, num, ok := prNumberForAction(m, "description")
		if !ok {
			return nil
		}
		if m.prDescLoad == nil {
			m.status = "pr: reading descriptions in the deck unavailable — p D opens it in a window"
			return nil
		}
		// In the deck, because reading what a PR says is a glance and this used to
		// cost a tmux window each time — one to make, one to switch to, one to come
		// back from. The window is still there under `D` for when you want the
		// description open beside something else.
		var descModal *prDescModal
		var descCmd tea.Cmd
		descModal, descCmd = newPRDescModal(item, num, m.prDescLoad)
		m.active = descModal
		return descCmd
	case "D":
		m.active = nil
		_, num, ok := prNumberForAction(m, "description")
		if !ok {
			return nil
		}
		// A dedicated tmux window in the workspace's session, the way a review opens.
		// gh renders the body with TTY formatting; less keeps it scrollable and
		// searchable, and q drops back to a shell in the window.
		winCmd := fmt.Sprintf("env GH_FORCE_TTY=100%% gh pr view %d | less -R", num)
		updated, cmd := m.trigger(ActionOpenWindow, prDescWindow+":"+winCmd)
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
		mine := itemIsMyPR(item, m.bookmarkPrefix)
		prompt := prRepairPrompt(status, item.BookmarkCommitID, mine)
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
		return initCmd
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
		return nil
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

func (prMenuModal) view(m *Model, b box) (left, right string) { return m.renderList(b.w), "" }

func (prMenuModal) footerHelp() string { return "" }
