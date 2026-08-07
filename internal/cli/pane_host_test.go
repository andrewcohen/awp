package cli

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/deckui"
)

var errNoAgent = errors.New("send prompt: no agent running for ws — press a to start one")

// fakeHost is a pane host that records what it was asked to deliver.
type fakeHost struct {
	item deckui.Item
	text string
	sent int
	err  error
}

func (h *fakeHost) Describes(string) bool { return true }
func (h *fakeHost) Open(deckui.Item, string, int, int) (*exec.Cmd, func(), error) {
	return exec.Command("true"), func() {}, nil
}
func (h *fakeHost) SendPrompt(item deckui.Item, text string, _ deckui.Reporter) error {
	h.item, h.text, h.sent = item, text, h.sent+1
	return h.err
}

var promptItem = deckui.Item{ProjectName: "proj", WorkspaceName: "ws", Path: "/tmp", RepoRoot: "/tmp"}

// The bug this prevents: the deck sent prompts through tmux to a session zdeck
// never opens, so a workspace ran two agents — the zmx one on screen and an
// invisible tmux one receiving everything typed at it.
func TestThePromptGoesToTheAgentYouCanSee(t *testing.T) {
	host := &fakeHost{}
	err, handled := paneHostAction(host,
		deckui.ActionRequest{Item: promptItem, Action: deckui.ActionSendPrompt, Arg: "ship it"}, nil)
	if !handled {
		t.Fatal("the prompt fell through to tmux, which addresses a different agent")
	}
	if err != nil {
		t.Fatal(err)
	}
	if host.sent != 1 || host.text != "ship it" {
		t.Errorf("host saw %d prompts, last %q; want 1 of %q", host.sent, host.text, "ship it")
	}
	if host.item.WorkspaceName != "ws" {
		t.Errorf("the prompt was aimed at workspace %q, want ws", host.item.WorkspaceName)
	}
}

// awp deck is unchanged: with no host every action still goes to tmux.
func TestWithoutAHostEveryActionStillGoesToTmux(t *testing.T) {
	for _, a := range []deckui.Action{deckui.ActionSendPrompt, deckui.ActionLastSession, deckui.ActionSummon} {
		if err, handled := paneHostAction(nil, deckui.ActionRequest{Item: promptItem, Action: a}, nil); handled || err != nil {
			t.Errorf("action %v was intercepted with no host wired (err=%v)", a, err)
		}
	}
}

// Actions a host does not own must fall through, or the deck loses them.
func TestAHostOnlyClaimsTheActionsItOwns(t *testing.T) {
	for _, a := range []deckui.Action{deckui.ActionSummon, deckui.ActionOpenWindow, deckui.ActionDelete, deckui.ActionCI} {
		if _, handled := paneHostAction(&fakeHost{}, deckui.ActionRequest{Item: promptItem, Action: a}, nil); handled {
			t.Errorf("the host swallowed action %v, which it does not handle", a)
		}
	}
}

// Switching to the last tmux session means nothing when the deck is the
// outermost program, and `tmux switch-client -l` exits 0 having done nothing —
// so saying nothing would leave the key looking broken rather than unavailable.
func TestLastSessionSaysThereIsNothingToSwitchTo(t *testing.T) {
	err, handled := paneHostAction(&fakeHost{},
		deckui.ActionRequest{Item: promptItem, Action: deckui.ActionLastSession}, nil)
	if !handled {
		t.Fatal("last session fell through to tmux, where it silently does nothing")
	}
	if err == nil || !strings.Contains(err.Error(), "last session") {
		t.Errorf("got %v, want an error naming what failed", err)
	}
}

// An error from the host has to reach the deck, which is what puts it in the
// status bar. A swallowed one looks like a prompt that was delivered.
func TestAHostsFailureReachesTheDeck(t *testing.T) {
	host := &fakeHost{err: errNoAgent}
	err, handled := paneHostAction(host,
		deckui.ActionRequest{Item: promptItem, Action: deckui.ActionSendPrompt, Arg: "hi"}, nil)
	if !handled || err == nil {
		t.Fatalf("handled=%v err=%v; want the failure surfaced", handled, err)
	}
}
