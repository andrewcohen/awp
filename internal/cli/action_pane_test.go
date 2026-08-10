package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/zmx"
)

// devHost is a pane host whose workspace has one user action: the case this
// exists for, a dev server you start once and leave up.
func devHost(actions ...deckui.UserAction) zmxPanes {
	if len(actions) == 0 {
		actions = []deckui.UserAction{{Name: "dev", Command: "pnpm dev", Alias: "d"}}
	}
	return zmxPanes{
		client:     zmx.New((&fakeZmx{}).run),
		actionsFor: func(string) []deckui.UserAction { return actions },
	}
}

// TestAUserActionRunsItsConfiguredCommand. The command is the whole content of a
// user action, and it lives only in the config — so a pane that could not read it
// would be a pane that opened a shell and waited to be told what to do.
func TestAUserActionRunsItsConfiguredCommand(t *testing.T) {
	cmd, _, err := devHost().Open(paneItem(), deckui.PaneKindForAction("dev"), 80, 24)
	if err != nil {
		t.Fatalf("open the dev action's pane: %v", err)
	}
	if !slices.Contains(cmd.Args, "pnpm dev") {
		t.Errorf("the pane runs %v, which does not carry the configured command", cmd.Args)
	}
	if cmd.Dir != paneItem().Path {
		t.Errorf("the pane opened in %q, want the workspace's %q", cmd.Dir, paneItem().Path)
	}
}

// TestAUserActionsCommandGetsAShell: it has always been a shell line — the tmux
// path types it at a prompt, and a background action runs it under `sh -c`. Split
// on whitespace instead and one config field would mean two different things
// depending on which deck read it, so `pnpm dev | tee log` would work under one
// and be handed to pnpm as arguments under the other.
func TestAUserActionsCommandGetsAShell(t *testing.T) {
	cmd, _, err := devHost(deckui.UserAction{Name: "dev", Command: "pnpm dev 2>&1 | tee dev.log"}).
		Open(paneItem(), deckui.PaneKindForAction("dev"), 80, 24)
	if err != nil {
		t.Fatalf("open the dev action's pane: %v", err)
	}
	// zmx attach <session> sh -c <command>
	i := slices.Index(cmd.Args, "sh")
	if i < 0 || len(cmd.Args) < i+3 || cmd.Args[i+1] != "-c" {
		t.Fatalf("the pane runs %v, want the command under `sh -c`", cmd.Args)
	}
	if cmd.Args[i+2] != "pnpm dev 2>&1 | tee dev.log" {
		t.Errorf("the shell was given %q, want the command verbatim", cmd.Args[i+2])
	}
}

// TestAUserActionOutlivesItsPane is the point of the kind being long-lived. The
// case is a dev server: you start it, leave the pane, and work in the agent's —
// so the session has to survive the client, which is what `zmx attach` gives it
// and what running the process on awp's own pty would not.
//
// It is also what makes "is it still up?" answerable. The command is the
// session's own process, so a live session is a running server — the same
// property the deck reads an agent's state from.
func TestAUserActionOutlivesItsPane(t *testing.T) {
	cmd, _, err := devHost().Open(paneItem(), deckui.PaneKindForAction("dev"), 80, 24)
	if err != nil {
		t.Fatalf("open the dev action's pane: %v", err)
	}
	if len(cmd.Args) < 2 || cmd.Args[0] != "zmx" || cmd.Args[1] != "attach" {
		t.Fatalf("the pane runs %v, want a zmx attach so the session outlives it", cmd.Args)
	}
	want := zmx.SessionName(paneItem().ProjectName, paneItem().WorkspaceName, deckui.PaneKindForAction("dev"))
	if !slices.Contains(cmd.Args, want) {
		t.Errorf("the pane's session is not %q: %v", want, cmd.Args)
	}
}

// TestAnActionsSessionCannotBeTheAgents. The name is the user's, so an action
// called "agent" would otherwise address the session the workspace's coding agent
// runs in — starting its command there, under the name the deck reads agent state
// from and the name `a` attaches to.
func TestAnActionsSessionCannotBeTheAgents(t *testing.T) {
	cmd, _, err := devHost(deckui.UserAction{Name: "agent", Command: "pnpm dev"}).
		Open(paneItem(), deckui.PaneKindForAction("agent"), 80, 24)
	if err != nil {
		t.Fatalf("open the agent action's pane: %v", err)
	}
	agentSession := zmx.SessionName(paneItem().ProjectName, paneItem().WorkspaceName, deckui.PaneKindAgent)
	if slices.Contains(cmd.Args, agentSession) {
		t.Fatalf("an action called \"agent\" took the coding agent's session %q: %v", agentSession, cmd.Args)
	}
}

// TestAnActionKindSurvivesTheSessionName: the sessions overlay reopens a pane
// from the kind it parsed out of a session name, which has been through the
// substrate's sanitizing — so an action whose name is not spellable there has to
// resolve from the spelling that came back, or `z` on a running dev server would
// say the deck has no pane for it.
func TestAnActionKindSurvivesTheSessionName(t *testing.T) {
	host := devHost(deckui.UserAction{Name: "dev server", Command: "pnpm dev"})
	name := zmx.SessionName(paneItem().ProjectName, paneItem().WorkspaceName, deckui.PaneKindForAction("dev server"))
	_, _, kind, ok := zmx.ParseSessionName(name)
	if !ok {
		t.Fatalf("%q does not parse as one of ours", name)
	}
	if _, _, err := host.Open(paneItem(), kind, 80, 24); err != nil {
		t.Fatalf("open the pane for the kind %q read back out of %q: %v", kind, name, err)
	}
}

// TestAnUnknownActionSaysWhereItLooked. Every other way to reach this kind goes
// through the menu, so arriving here means the config changed under a session
// that is still listed — and the answer has to name the action and the file, not
// report that the deck has no pane for some namespaced string.
func TestAnUnknownActionSaysWhereItLooked(t *testing.T) {
	_, _, err := devHost().Open(paneItem(), deckui.PaneKindForAction("nope"), 80, 24)
	if err == nil {
		t.Fatal("an action nobody configured opened a pane")
	}
	for _, want := range []string{"nope", paneItem().WorkspaceName, paneItem().RepoRoot, "config.json"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error %q does not mention %q", err, want)
		}
	}
}

// TestAnActionWithNoCommandSaysSo: the field is optional in JSON, so an action
// can exist with nothing to run. Opening a shell instead would look like the
// action ran and did nothing.
func TestAnActionWithNoCommandSaysSo(t *testing.T) {
	_, _, err := devHost(deckui.UserAction{Name: "dev", Command: "   "}).
		Open(paneItem(), deckui.PaneKindForAction("dev"), 80, 24)
	if err == nil {
		t.Fatal("an action with no command opened a pane anyway")
	}
	for _, want := range []string{"dev", "no command"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error %q does not mention %q", err, want)
		}
	}
}

// TestTheHostClaimsEveryUserAction: Describes is asked before there is a row in
// hand, so it cannot check that an action is configured. Claiming it anyway is
// deliberate — falling through to tmux is the failure this whole path replaced,
// where the key started a second agent in a server nobody was looking at.
func TestTheHostClaimsEveryUserAction(t *testing.T) {
	for _, name := range []string{"dev", "nobody-configured-this"} {
		if !devHost().Describes(deckui.PaneKindForAction(name)) {
			t.Errorf("the host declined the user action %q, which sends it back to tmux", name)
		}
	}
	if devHost().Describes("not-a-kind") {
		t.Error("the host claimed a kind that is neither fixed nor a user action")
	}
}

// TestAnActionsSessionBelongsToItsRow: `z` lists every awp session, and enter on
// one reopens its pane — which needs the row it belongs to. Left out of the name
// index, a dev server's session listed as belonging to nobody.
func TestAnActionsSessionBelongsToItsRow(t *testing.T) {
	item := paneItem()
	host := devHost()
	session := zmx.SessionName(item.ProjectName, item.WorkspaceName, deckui.PaneKindForAction("dev"))
	host.client = zmx.New((&lsRunner{lines: []string{
		"name=" + session + "\tpid=91\tclients=0\tcreated=1786124270",
	}}).run)

	sessions, err := host.Sessions([]deckui.Item{item})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	if !sessions[0].HasItem || sessions[0].Item.WorkspaceName != item.WorkspaceName {
		t.Errorf("the dev action's session resolved to %+v, want the %q row", sessions[0].Item, item.WorkspaceName)
	}
}
