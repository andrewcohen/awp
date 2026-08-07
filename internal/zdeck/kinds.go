package zdeck

import (
	"os"
	"os/exec"
	"strings"

	"github.com/andrewcohen/awp/internal/config"
	"github.com/andrewcohen/awp/internal/deckdata"
	"github.com/andrewcohen/awp/internal/zmx"
)

// Item is a workspace row, shared with the deck's data layer.
type Item = deckdata.Item

// Lifetime says whether a pane's process outlives the view.
//
// This is the axis the whole design turns on. "Peek versus summon" was only
// ever a size, but whether a process survives you looking away is a real
// difference and it decides what has to be behind the pane.
type Lifetime int

const (
	// LongLived runs in a zmx session. Closing the pane, or awp itself,
	// leaves it running.
	LongLived Lifetime = iota
	// Ephemeral is a process awp spawns straight onto a PTY it owns, with no
	// session behind it. It dies with the pane, which is the point.
	Ephemeral
	// Native is not a process at all — a Bubble Tea program drawn directly in
	// the pane. Always ephemeral; nothing to outlive anything.
	Native
)

// Kind is one of the things a workspace row can show.
type Kind struct {
	// Key is the keystroke that opens it, and its identity.
	Key      string
	Label    string
	Lifetime Lifetime
	// argv builds the command to run, in the workspace directory. nil for
	// Native kinds.
	argv func(it Item) []string
}

// Kinds is the full set, in the order the footer lists them.
//
// Andrew's call on lifetimes: the agent and the editor are worth keeping
// alive between glances, a shell and jjui are not — you open those to do one
// thing and the next one can start fresh.
var Kinds = []Kind{
	{Key: "a", Label: "agent", Lifetime: LongLived, argv: agentArgv},
	{Key: "s", Label: "shell", Lifetime: Ephemeral, argv: shellArgv},
	{Key: "v", Label: "vcs", Lifetime: Ephemeral, argv: staticArgv("jjui")},
	{Key: "e", Label: "editor", Lifetime: LongLived, argv: editorArgv},
	{Key: "c", Label: "review", Lifetime: Native},
}

// KindForKey finds the kind a keystroke opens.
func KindForKey(key string) (Kind, bool) {
	for _, k := range Kinds {
		if k.Key == key {
			return k, true
		}
	}
	return Kind{}, false
}

func staticArgv(args ...string) func(Item) []string {
	return func(Item) []string { return args }
}

func agentArgv(it Item) []string {
	return splitCommand(config.AgentInvocation(it.RepoRoot))
}

func shellArgv(Item) []string {
	if sh := strings.TrimSpace(os.Getenv("SHELL")); sh != "" {
		return []string{sh, "-l"}
	}
	return []string{"sh"}
}

func editorArgv(Item) []string {
	editor := strings.TrimSpace(os.Getenv("EDITOR"))
	if editor == "" {
		editor = "vi"
	}
	return append(splitCommand(editor), ".")
}

// splitCommand splits a configured command string on whitespace. Deliberately
// not a shell parse: the value comes from awp's own config, and running it
// through a shell would be a way to turn a config file into arbitrary
// execution with quoting rules nobody wants to reason about.
func splitCommand(s string) []string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return []string{"sh"}
	}
	return fields
}

// command builds the process for a kind, and the session name behind it when
// there is one.
//
// The only difference between a long-lived pane and an ephemeral one is what
// awp runs: a zmx client, or the program itself. Everything downstream — the
// emulator, the keys, the rendering — is identical.
func (k Kind) command(it Item, client zmx.Client) (cmd *exec.Cmd, session string, err error) {
	if k.argv == nil {
		return nil, "", nil // Native: no process
	}
	argv := k.argv(it)
	dir := it.Path
	if dir == "" {
		dir = it.RepoRoot
	}
	if k.Lifetime == Ephemeral {
		return zmx.Command(dir, argv, os.Environ()), "", nil
	}
	name := zmx.SessionName(it.ProjectName, it.WorkspaceName, k.Label)
	if _, err := client.Ensure(ctx(), name, dir, argv); err != nil {
		return nil, "", err
	}
	// Best effort: labels make `zmx ls` legible from outside awp. A session
	// that runs but is not labelled is still a working pane.
	_ = client.Label(ctx(), name, map[string]string{
		"awp": "1", "kind": k.Label, "project": it.ProjectName, "workspace": it.WorkspaceName,
	})
	return zmx.AttachCmd(name, os.Environ()), name, nil
}
