package cli

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
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

// A host has to say which substrate it reads. This one hosts nothing real, so
// it reports nothing rather than answering from tmux — a fake that read the
// developer's actual tmux would make these tests depend on it.
func (h *fakeHost) sessionSource() deckSessions { return noSessions{} }

type noSessions struct{}

func (noSessions) sessions(bool) deckSessionSnapshot {
	return deckSessionSnapshot{byWorkspace: map[workspaceRef]sessionFacts{}}
}

func (noSessions) roots() map[workspaceRef][]int { return nil }

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

// Actions that are nobody's window business must fall through, or the deck
// loses them. Delete, rename and merge are workspace operations, not windows —
// they mean the same thing on either substrate.
func TestAHostOnlyClaimsTheActionsItOwns(t *testing.T) {
	for _, a := range []deckui.Action{deckui.ActionDelete, deckui.ActionRename, deckui.ActionMergePR, deckui.ActionCreateWorkspace} {
		if _, handled := paneHostAction(&fakeHost{}, deckui.ActionRequest{Item: promptItem, Action: a}, nil); handled {
			t.Errorf("the host swallowed action %v, which it does not handle", a)
		}
	}
}

// TestNoWindowActionFallsThroughToATmuxSessionOnAnAgentHostingDeck.
//
// The bug: openNamedWindow and openCustomActionWindow both open with "no tmux
// session for this workspace? make one" — createWorkspaceSession, which runs
// `tmux new-session` and sends the coding agent's invocation into it. On a deck
// that hosts the agent there is never a tmux session, so every one of these keys
// forked a second agent, in a tmux server started from nothing, invisible to the
// deck, with the same AWP_WORKSPACE. Then SwitchClient no-opped, so nothing
// appeared to happen.
//
// Kinds the host handles never reach here — openPane claims them in
// Model.trigger. These are the declined ones, and the answer for them is to say
// so rather than to quietly fork an agent.
func TestNoWindowActionFallsThroughToATmuxSessionOnAnAgentHostingDeck(t *testing.T) {
	for _, tc := range []struct {
		what string
		req  deckui.ActionRequest
	}{
		{"C", deckui.ActionRequest{Item: promptItem, Action: deckui.ActionOpenWindow, Arg: "review"}},
		{"p D", deckui.ActionRequest{Item: promptItem, Action: deckui.ActionOpenWindow, Arg: "pr-description"}},
		{"x", deckui.ActionRequest{Item: promptItem, Action: deckui.ActionCustom, Arg: "dev-server"}},
		{"enter", deckui.ActionRequest{Item: promptItem, Action: deckui.ActionSummon}},
		{"i", deckui.ActionRequest{Item: promptItem, Action: deckui.ActionCI}},
	} {
		err, handled := paneHostAction(&fakeHost{}, tc.req, nil)
		if !handled {
			t.Errorf("%s fell through to tmux, where it would start a second agent", tc.what)
			continue
		}
		if err == nil {
			t.Errorf("%s was swallowed silently; a key that does nothing reads as broken", tc.what)
		}
	}
}

// TestAHostThatDoesNotRunAgentsStillUsesTmuxForWindows. The refusal exists
// because a tmux session would carry a duplicate agent. A host with no agent to
// duplicate has nothing to protect, so the tmux path is still right for it —
// and this is what keeps the rule about agents rather than about panes.
func TestAHostThatDoesNotRunAgentsStillUsesTmuxForWindows(t *testing.T) {
	host := &noAgentHost{}
	for _, a := range []deckui.Action{deckui.ActionOpenWindow, deckui.ActionCustom, deckui.ActionCI} {
		if err, handled := paneHostAction(host, deckui.ActionRequest{Item: promptItem, Action: a, Arg: "review"}, nil); handled {
			t.Errorf("action %v was refused by a host that runs no agents (err=%v)", a, err)
		}
	}
}

// noAgentHost hosts panes but not the agent — an editor-only host, say.
type noAgentHost struct{ fakeHost }

func (h *noAgentHost) Describes(kind string) bool { return kind != deckui.PaneKindAgent }

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

// TestEveryActionIsOfferedToTheHostBeforeTmux walks the whole Action enum:
// each one either stops at the host or reaches tmux, and which of those it does
// is paneHostAction's decision alone. An action the wrapper never offered would
// show up here as one that reached tmux against that verdict.
//
// It walks deckui.AllActions() rather than a list written out here, because a
// list is exactly as complete as whoever last added an action remembered to
// make it — and the ordering bug this guards was itself an action nobody
// remembered.
func TestEveryActionIsOfferedToTheHostBeforeTmux(t *testing.T) {
	actions := deckui.AllActions()
	if len(actions) < 2 {
		t.Fatalf("AllActions returned %d actions, so this guard is not walking the enum", len(actions))
	}
	for _, a := range actions {
		req := deckui.ActionRequest{Item: promptItem, Action: a, Arg: "x"}
		_, claimed := paneHostAction(&fakeHost{}, req, noopReporter{})

		var reachedTmux bool
		err := hostFirst(&fakeHost{}, func(deckui.ActionRequest) error {
			reachedTmux = true
			return nil
		})(req)
		if reachedTmux == claimed {
			t.Errorf("action %v: host claimed=%v but it reached tmux=%v (err=%v)", a, claimed, reachedTmux, err)
		}
	}
}

// TestWithNoHostEveryActionReachesTmux is the other half: awp deck has no pane
// host, and the wrapper must be invisible to it rather than swallowing keys.
func TestWithNoHostEveryActionReachesTmux(t *testing.T) {
	for _, a := range deckui.AllActions() {
		var reachedTmux bool
		if err := hostFirst(nil, func(deckui.ActionRequest) error {
			reachedTmux = true
			return nil
		})(deckui.ActionRequest{Item: promptItem, Action: a}); err != nil {
			t.Errorf("action %v: %v", a, err)
		}
		if !reachedTmux {
			t.Errorf("action %v never reached tmux on a deck with no pane host", a)
		}
	}
}

// TestTheHostIsConsultedInExactlyOnePlace is what actually pins the bug shut.
//
// The interception used to sit inside the action handler, below two early
// returns; `x` took one of them and the host was never asked. Moving it into
// hostFirst fixes today's ordering, but nothing stops a future branch from
// asking the host itself and re-creating a second, earlier answer — so this
// asserts the question is asked in one place, the wrapper that runs before the
// handler at all. Same shape as internal/github/dir_test.go, and for the same
// reason: the invariant is only as strong as every call site remembering it.
func TestTheHostIsConsultedInExactlyOnePlace(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob the package: %v", err)
	}
	fset := token.NewFileSet()
	var sites []string
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "paneHostAction" {
					sites = append(sites, fmt.Sprintf("%s at %s", fn.Name.Name, fset.Position(call.Pos())))
				}
				return true
			})
		}
	}
	if len(sites) != 1 || !strings.HasPrefix(sites[0], "hostFirst at ") {
		t.Fatalf("paneHostAction must be called from hostFirst and nowhere else; found %d call sites: %v", len(sites), sites)
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
