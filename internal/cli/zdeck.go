package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/workspace"
	"github.com/andrewcohen/awp/internal/zmx"
)

// paneLifetime says whether a pane's process outlives the view.
//
// This is the only axis that needs a mechanism. Whether you are glancing at a
// pane or working in it is a size; whether the process is still there when you
// look away is not.
type paneLifetime int

const (
	// longLived runs in a zmx session and survives the pane, and awp.
	longLived paneLifetime = iota
	// ephemeral is spawned straight onto a pty awp owns and dies with the pane.
	ephemeral
)

// panes maps a deck window kind to how zdeck hosts it. Kinds absent from this
// map fall through to the ordinary tmux window — which is how the review and
// PR-description windows keep working.
//
// Andrew's call on lifetimes: the agent and the editor are worth keeping alive
// between glances; a shell and jjui are not, since you open those to do one
// thing and the next one can start fresh.
var panes = map[string]struct {
	label    string
	lifetime paneLifetime
	argv     func(it deckui.Item) []string
}{
	// codingAgentArgv, not config.AgentInvocation: the latter omits the
	// dev-loop preamble, so a pane agent would not know to work in units, run
	// gates or commit — the same instruction the tmux path has always sent.
	"agent":  {"agent", longLived, func(it deckui.Item) []string { return codingAgentArgv(it.RepoRoot) }},
	"editor": {"editor", longLived, func(deckui.Item) []string { return append(fields(envOr("EDITOR", "vi")), ".") }},
	"vcs":    {"vcs", ephemeral, func(deckui.Item) []string { return []string{"jjui"} }},
	"":       {"shell", ephemeral, func(deckui.Item) []string { return fields(envOr("SHELL", "sh")) }},
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// fields splits a configured command on whitespace. Deliberately not a shell
// parse: the value comes from awp's own config, and handing it to a shell
// would turn a config file into arbitrary execution.
func fields(s string) []string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return []string{"sh"}
	}
	return f
}

// zmxPanes hosts the deck's window kinds on ptys awp owns, backed by zmx for
// the ones that have to survive.
type zmxPanes struct{ client zmx.Client }

func (zmxPanes) Describes(kind string) bool {
	_, ok := panes[kind]
	return ok
}

// Sessions reports every awp session zmx is holding, each tied to the deck row
// it belongs to.
//
// The matching happens here rather than in the deck because it needs the naming
// scheme: SessionName sanitizes each segment, so a workspace called "foo.bar"
// is "foo_bar" in its session name and comparing the raw strings would never
// match. Naming a session and un-naming one are the same knowledge, and it
// lives in internal/zmx.
//
// Sessions zmx knows about that awp did not create are skipped — `zmx ls` lists
// everything on the machine, and the deck has nothing to say about the rest.
func (z zmxPanes) Sessions(items []deckui.Item) ([]deckui.PaneSession, error) {
	all, err := z.client.List(context.Background())
	if err != nil {
		return nil, err
	}
	// Index the rows by the session name each kind would have, so the lookup is
	// against the same function that created the name.
	byName := map[string]deckui.Item{}
	for _, it := range items {
		if it.Virtual || strings.TrimSpace(it.WorkspaceName) == "" {
			continue
		}
		for _, spec := range panes {
			byName[zmx.SessionName(it.ProjectName, it.WorkspaceName, spec.label)] = it
		}
	}

	out := make([]deckui.PaneSession, 0, len(all))
	for _, s := range all {
		project, workspace, kind, ok := zmx.ParseSessionName(s.Name)
		if !ok {
			continue
		}
		item, hasItem := byName[s.Name]
		out = append(out, deckui.PaneSession{
			Item:     item,
			HasItem:  hasItem,
			Label:    project + "/" + workspace,
			Kind:     kind,
			Live:     s.Live(),
			Attached: s.Clients > 0,
			PID:      s.PID,
			Started:  s.Created,
			Cmd:      s.Cmd,
		})
	}
	return out, nil
}

func (z zmxPanes) Open(item deckui.Item, kind string, _, _ int) (*exec.Cmd, func(), error) {
	spec, ok := panes[kind]
	if !ok {
		return nil, nil, fmt.Errorf("zdeck has no pane for %q", kind)
	}
	dir := item.Path
	if dir == "" {
		dir = item.RepoRoot
	}
	argv := spec.argv(item)

	// The same env a tmux workspace session carries. It is what tells an agent
	// which workspace it belongs to, and every awp hook opens by asking (see
	// agenthooks.InAwpWorkspace) — without it a hosted agent reports no status,
	// so the deck shows it idle forever, and records no gates.
	env := append(os.Environ(), workspaceEnvPairs(item.ProjectName, item.WorkspaceName, item.RepoRoot)...)

	if spec.lifetime == ephemeral {
		// Nothing to keep, nothing to restore.
		return zmx.Command(dir, argv, env), func() {}, nil
	}

	// Reap first: a session whose command has already exited would otherwise
	// be attached to and render a dead program's last screen.
	name := zmx.SessionName(item.ProjectName, item.WorkspaceName, spec.label)
	if _, err := z.client.Reap(context.Background(), name); err != nil {
		return nil, nil, err
	}
	// One command both creates and attaches. Nothing labels the session,
	// because SessionName already spells the project, the workspace and the
	// kind into the name that `zmx ls` prints.
	//
	// Closing the pane kills this client, not the session; that is what
	// long-lived means.
	//
	// The session's env is fixed at creation, so a workspace renamed later
	// leaves AWP_WORKSPACE stale here — the tmux path re-reads it from the
	// session, and there is no zmx equivalent yet.
	return zmx.AttachCmd(dir, name, argv, env), func() {}, nil
}

// SendPrompt delivers text to the workspace's agent — the same process `a`
// shows, and the reason this exists.
//
// Without it the deck sends prompts through tmux, to a session zdeck never
// opens, so a workspace ends up with two agents: the zmx one on screen and an
// invisible tmux one receiving everything you type at it.
//
// It will not start an agent that is not running. zmx has no way to create a
// session detached with a real command as its own process (see zmx.AttachCmd),
// so the honest answer is to say so and name the key that fixes it.
func (z zmxPanes) SendPrompt(item deckui.Item, text string, reporter deckui.Reporter) error {
	if reporter == nil {
		reporter = noopReporter{}
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("send prompt: nothing to send")
	}
	ws := strings.TrimSpace(item.WorkspaceName)
	if ws == "" {
		return errors.New("send prompt: select a workspace row first")
	}

	name := zmx.SessionName(item.ProjectName, ws, panes["agent"].label)
	session, found, err := z.client.Lookup(context.Background(), name)
	if err != nil {
		return err
	}
	if !found || !session.Live() {
		return fmt.Errorf("send prompt: no agent running for %s — press a to start one", ws)
	}

	reporter.Step("Send prompt to agent")
	return z.client.Paste(context.Background(), name, text)
}

// runZdeck is `awp deck` with zmx behind the window keys instead of tmux.
func runZdeck(runner Runner, svc workspace.Service, in io.Reader, out io.Writer) error {
	if _, err := exec.LookPath("zmx"); err != nil {
		return fmt.Errorf("zdeck needs zmx on PATH — install it, or use `awp deck` (%w)", err)
	}
	backend := zmxPanes{client: zmx.New(func(ctx context.Context, dir, name string, args ...string) (string, error) {
		return runner.Run(ctx, dir, name, args...)
	})}
	return runDeckWithCharm(runner, svc, in, out, deckui.ScopeAll, backend)
}
