package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/andrewcohen/awp/internal/config"
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
	"agent":  {"agent", longLived, func(it deckui.Item) []string { return fields(config.AgentInvocation(it.RepoRoot)) }},
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

	if spec.lifetime == ephemeral {
		// Nothing to keep, nothing to restore.
		return zmx.Command(dir, argv, os.Environ()), func() {}, nil
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
	return zmx.AttachCmd(dir, name, argv, os.Environ()), func() {}, nil
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
