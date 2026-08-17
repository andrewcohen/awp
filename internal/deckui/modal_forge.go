package deckui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// forgeMenuModal is the deck's code-forge hub (the `C` key): the one-off things you
// can do to this row's change and its PR, in one keys-only menu.
//
// One hub rather than a key each. These verbs have nothing in common but their
// subject — a browser, a description, a merge — so each would otherwise want its
// own top-level letter, and the deck has run out of letters that mean anything.
// What they do share is that you reach for exactly one at a time and then go back
// to what you were doing, which is what a menu is for.
//
// It renders no overlay of its own — the row list stays visible beneath — so it is
// a bodyModal whose view is the row list, and a chordModal whose menu floats over
// it, where every other menu in the deck goes (menu.go). That is the same popover
// the pane menu opens, deliberately: which key opened a menu should not change what
// a menu looks like. It holds no state; each action re-reads the selected row.
//
// This was the `p` menu until `C` took it over, and `p` is unbound rather than kept
// as a second door — one way to say a thing.
//
// `C` itself used to open the review in a tmux window. Nothing replaced that: the
// window has no meaning under a pane host (#206), and `| c` already puts the change
// beside the agent, which is what it was for.
type forgeMenuModal struct{}

// forgeMenu is the hub's verbs for this row.
func forgeMenu() deckMenu {
	return menu(
		// The review first: it is the verb you want most often, and the only one here
		// that opens a surface rather than doing a thing and closing.
		[2]string{"c", "review the change"},
		[2]string{"o", "open the PR in a browser"},
		[2]string{"d", "read the description here"},
		[2]string{"D", "read the description in a window"},
		[2]string{"r", "repair the PR"},
		// `m` was handled and unlisted, which the ribbon hid better than a list does:
		// six verbs in a row read as a sentence you skim, where a column of them reads
		// as the complete set.
		[2]string{"m", "merge the PR"},
		[2]string{"s", "set the PR number"},
		menuCancelVerb,
	)
}

func (forgeMenuModal) chordMenu() deckMenu { return forgeMenu() }

// prDescWindow is the tmux window `C D` opens the description into. Named for
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
		m.status = "pr: " + what + " unavailable (no PR number cached — try C s)"
		return Item{}, 0, false
	}
	return item, status.Number, true
}

func (forgeMenuModal) update(m *Model, msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch key.String() {
	case "esc", "q", "ctrl+c":
		// Cleared rather than echoed: you pressed esc, so you know. Per the design
		// system, a cancellation leaves no message behind.
		m.active = nil
		m.status = ""
		return nil
	case "c":
		// The deck's own `c`, from here. Not a duplicate of it: `c` is the key you
		// reach for when reading a change is the thing you came to do, and this row
		// is for when you opened the hub and then decided the answer was to look.
		m.active = nil
		updated, cmd := m.openDiffModal(ScopeStackBase)
		*m = updated.(Model)
		return cmd
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
			m.status = "pr: reading descriptions in the deck unavailable — C D opens it in a window"
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
			m.status = "pr: merge unavailable (no PR number cached — try C s)"
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

func (forgeMenuModal) view(m *Model, b box) (left, right string) { return m.renderList(b.w), "" }

func (forgeMenuModal) footerHelp() string { return "" }
