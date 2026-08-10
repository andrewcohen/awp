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
// between glances; a shell, jjui, CI and the watch view are not, since you open
// those to do one thing and the next one can start fresh. CI and watch are the
// clearest ephemerals of the set — each runs one foreground program you watch
// until it says something, and a stale one is worse than no one at all.
var panes = map[string]struct {
	label    string
	lifetime paneLifetime
	argv     func(it deckui.Item) []string
}{
	// codingAgentArgv, not config.AgentInvocation: the latter omits the
	// dev-loop preamble, so a pane agent would not know to work in units, run
	// gates or commit — the same instruction the tmux path has always sent.
	deckui.PaneKindAgent: {"agent", longLived, func(it deckui.Item) []string { return codingAgentArgv(it.RepoRoot) }},
	"editor":             {"editor", longLived, func(deckui.Item) []string { return append(fields(envOr("EDITOR", "vi")), ".") }},
	"vcs":                {"vcs", ephemeral, func(deckui.Item) []string { return []string{"jjui"} }},
	// bash, not $SHELL: ciWatchScript is the tmux window's command verbatim,
	// and it is written in POSIX-ish bash. Running it under whatever shell the
	// user prefers would make `i` mean something different per machine.
	deckui.PaneKindCI: {"ci", ephemeral, func(deckui.Item) []string { return []string{"bash", "-c", ciWatchScript} }},
	// The running awp rather than whatever PATH resolves, so a deck you built
	// to a temp path opens that build's watch view and not an older install's.
	deckui.PaneKindWatch: {"watch", ephemeral, func(deckui.Item) []string { return []string{awpSelf(), "watch"} }},
	"":                   {"shell", ephemeral, func(deckui.Item) []string { return fields(envOr("SHELL", "sh")) }},
}

// awpSelf is the awp binary to spawn for awp's own subcommands.
//
// os.Executable can fail (a deleted or unreadable binary); "awp" is the honest
// fallback, and the pane will say so itself if PATH has none.
func awpSelf() string {
	if exe, err := os.Executable(); err == nil && strings.TrimSpace(exe) != "" {
		return exe
	}
	return "awp"
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
type zmxPanes struct {
	client zmx.Client
	// svcFor resolves the workspace service for a repo, so a cross-project
	// deck reads each row's state file rather than the one it started in.
	// Used for the prompt a workspace was created with and has not delivered
	// yet — see Entry.PendingPrompt.
	svcFor func(repoRoot string) workspace.Service
}

// takePendingPrompt returns the prompt parked for this workspace, once.
//
// It is best-effort: a state file we cannot read is not a reason to refuse to
// open the agent. A prompt lost that way is recoverable by typing it; a pane
// that will not open is not.
func (z zmxPanes) takePendingPrompt(item deckui.Item) workspace.PendingPrompt {
	if z.svcFor == nil || strings.TrimSpace(item.RepoRoot) == "" {
		return workspace.PendingPrompt{}
	}
	p, err := z.svcFor(item.RepoRoot).TakePendingPrompt(item.WorkspaceName)
	if err != nil {
		return workspace.PendingPrompt{}
	}
	p.Text = strings.TrimSpace(p.Text)
	return p
}

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

// deliverPending routes a parked prompt to the agent, and returns the argv to
// start it with.
//
// Which route depends on whether the session is already there. zmx ignores
// argv for a session that exists (see zmx.AttachCmd), so for a live agent the
// prompt has to be pasted; for one we are about to create it can be the
// agent's own argument, which is better — no waiting for it to boot, and no
// paste racing its input box.
//
// A paste that fails re-parks the prompt. A prompt still parked can be
// delivered next time; one we dropped is gone.
func (z zmxPanes) deliverPending(item deckui.Item, name string, argv []string, p workspace.PendingPrompt) ([]string, error) {
	session, found, err := z.client.Lookup(context.Background(), name)
	if err != nil {
		_ = z.svcFor(item.RepoRoot).RecordPendingPrompt(item.WorkspaceName, p)
		return nil, err
	}
	if !found || !session.Live() {
		// Creating the session, so this is the one moment the agent's flavor
		// is still ours to choose — see reviewAgentArgv.
		if p.Review {
			argv = reviewAgentArgv(item.RepoRoot)
		}
		return append(argv, p.Text), nil
	}
	// A live agent is whatever it already is; the flavor was decided when it
	// started. Same as the tmux path's pre-existing-window branch, which also
	// just pastes.
	if err := z.client.Paste(context.Background(), name, p.Text); err != nil {
		_ = z.svcFor(item.RepoRoot).RecordPendingPrompt(item.WorkspaceName, p)
		return nil, err
	}
	return argv, nil
}

func (z zmxPanes) Open(item deckui.Item, kind string, _, _ int) (*exec.Cmd, func(), error) {
	spec, ok := panes[kind]
	if !ok {
		return nil, nil, fmt.Errorf("zdeck has no pane for %q", kind)
	}
	// No fallback. A pane's directory is the workspace's working copy or it is
	// nothing — the previous `if dir == "" { dir = item.RepoRoot }` is what made
	// a wrong directory silent instead of an error, and a program started in the
	// source repo instead of the workspace looks entirely normal until you read
	// what it wrote. The two rows that reach here without a Path are a workspace
	// still being created (#243 stops that upstream) and an unmanaged row, which
	// under a pane host is a leftover tmux session a zmx pane has no business
	// guessing about.
	dir := strings.TrimSpace(item.Path)
	if dir == "" {
		return nil, nil, fmt.Errorf("open the %s pane for %q: the workspace has no working copy on disk yet — wait for it to finish setting up, or press enter to create it", spec.label, item.WorkspaceName)
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

	// A workspace created under a pane host has its prompt parked rather than
	// delivered — at creation time there was no agent to deliver it to, and
	// the create may well have run in a detached subprocess with no terminal
	// to start one on. This is where the agent comes into existence, so this
	// is where the prompt arrives.
	//
	// Only looked up when the kind is the agent: nothing else is a recipient,
	// and the Lookup below is a subprocess we should not spend on every pane.
	if kind == deckui.PaneKindAgent {
		if p := z.takePendingPrompt(item); !p.Empty() {
			var err error
			if argv, err = z.deliverPending(item, name, argv, p); err != nil {
				return nil, nil, err
			}
		}
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
// sessionSource is the substrate zdeck's long-lived processes live on, so the
// deck reads zmx rather than tmux. Same client, so a row cannot describe one
// substrate while the keys act on the other.
func (z zmxPanes) sessionSource() deckSessions { return zmxSessions{client: z.client} }

// zmxSessions answers the deck's session questions from `zmx ls`.
//
// Deliberately not a merge with tmux. A workspace whose agent is in a leftover
// tmux session — started earlier by `awp deck` — reads as having no session
// here, and that is the honest answer: zdeck cannot show it to you, `a` will
// not take you to it, and calling it active is exactly the stale read the
// session-truth seam exists to remove.
type zmxSessions struct{ client zmx.Client }

// sessions reads every awp session and folds it onto the workspace it names.
//
// The fast path costs nothing at all: which workspace the user is looking at is
// in the environment, so the first paint needs no subprocess. known stays false
// so the deck keeps suppressing caution glyphs until the full read lands.
func (z zmxSessions) sessions(fast bool) deckSessionSnapshot {
	snap := deckSessionSnapshot{byWorkspace: map[workspaceRef]sessionFacts{}}
	snap.current, snap.hasCurrent = zmxWorkspaceRef(os.Getenv("ZMX_SESSION"))
	if fast {
		return snap
	}
	list, err := z.client.List(context.Background())
	if err != nil {
		// A daemon that is not answering is not the same as no sessions.
		// Leaving known false says "unread", so nothing renders a workspace as
		// dead on the strength of a failed call.
		return snap
	}
	snap.known = true
	for _, s := range list {
		project, ws, kind, ok := zmx.ParseSessionName(s.Name)
		if !ok {
			// Not a session awp made. The deck has nothing to say about the
			// rest of the user's zmx.
			continue
		}
		ref := workspaceRef{project: project, workspace: ws}
		f := snap.byWorkspace[ref]
		f.present = true
		switch {
		case kind == deckui.PaneKindAgent:
			// The agent runs as the session's own process, so whether it is
			// gone is simply whether the session's command has exited. The
			// tmux path has to sniff for a window that fell back to a bare
			// shell, because a tmux window outlives the process in it; there
			// is nothing to carry across.
			f.name, f.agentGone = s.Name, !s.Live()
		case f.name == "":
			// An editor session is a session — the workspace is present — but
			// it says nothing about the agent. agentGone stays false, so a
			// workspace that never ran an agent does not read as "exited".
			f.name = s.Name
		}
		snap.byWorkspace[ref] = f
	}
	return snap
}

// zmxWorkspaceRef reads the workspace out of a zmx session name, for the
// session awp is itself running in. Inverse of zmx.SessionName, and inherits
// its sanitizing: a project or workspace whose real name contained a dot comes
// back with an underscore and will not match its row. Names that survive
// sanitizing round-trip, which is what the deck's own sessions are.
func zmxWorkspaceRef(name string) (workspaceRef, bool) {
	project, ws, _, ok := zmx.ParseSessionName(strings.TrimSpace(name))
	if !ok {
		return workspaceRef{}, false
	}
	return workspaceRef{project: project, workspace: ws}, true
}

// zmxClientFor is the one way to build a zmx client over a deck runner, so the
// deck, its panes and the detached jobs all talk to the same daemon the same
// way.
func zmxClientFor(runner Runner) zmx.Client {
	return zmx.New(func(ctx context.Context, dir, name string, args ...string) (string, error) {
		return runner.Run(ctx, dir, name, args...)
	})
}

// killWorkspaceSessions ends every zmx session belonging to a workspace, and
// reports what it killed.
//
// Called on delete regardless of which deck is running, and deliberately not
// gated on the deck hosting panes. A delete happens in a detached subprocess
// with no terminal and therefore no pane host to ask, so gating would mean
// threading the answer through the job spec — a fourth way to say which
// substrate is real, on a flow that has already been wrong twice for exactly
// that reason. Killing sessions that do not exist is a no-op, so `awp deck`
// users are unaffected and delete comes to mean the same thing from either
// deck.
//
// Matched by parsing names rather than by walking the pane kinds, so a kind
// added later — including a user-defined one — is reaped without anyone
// remembering to add it here.
func killWorkspaceSessions(runner Runner, project, workspaceName string, reporter deckui.Reporter) error {
	if runner == nil {
		// Named rather than left to panic: this runs inside a detached job, where
		// a segfault is the least visible way for a delete to half-finish.
		return fmt.Errorf("kill the zmx sessions of workspace %q: no runner", workspaceName)
	}
	if _, err := exec.LookPath("zmx"); err != nil {
		// Not installed: there is nothing of ours in a substrate that isn't here.
		return nil
	}
	client := zmxClientFor(runner)
	sessions, err := client.List(context.Background())
	if err != nil {
		return fmt.Errorf("find the zmx sessions of workspace %q: %w", workspaceName, err)
	}
	want := workspaceRef{project: project, workspace: workspaceName}
	var failed []string
	for _, s := range sessions {
		ref, ok := zmxWorkspaceRef(s.Name)
		if !ok || ref != want {
			continue
		}
		if reporter != nil {
			reporter.Step(fmt.Sprintf("Kill zmx session %s", s.Name))
		}
		if err := client.Kill(context.Background(), s.Name); err != nil {
			failed = append(failed, s.Name)
		}
	}
	if len(failed) > 0 {
		// The workspace is already gone by the time we get here, so this is not
		// a reason to fail the delete — but a surviving agent in a deleted tree
		// is exactly the bug this exists to prevent, so say which one and how to
		// finish the job by hand.
		return fmt.Errorf("workspace %q was deleted but its zmx session(s) %s survived — run `zmx kill %s --force`",
			workspaceName, strings.Join(failed, ", "), failed[0])
	}
	return nil
}

func runZdeck(runner Runner, svc workspace.Service, in io.Reader, out io.Writer) error {
	if _, err := exec.LookPath("zmx"); err != nil {
		return fmt.Errorf("zdeck needs zmx on PATH — install it, or use `awp deck` (%w)", err)
	}
	backend := zmxPanes{
		client: zmxClientFor(runner),
		// Per-repo, not the deck's own service: zdeck opens at ScopeAll, so
		// the row whose agent you are starting may belong to another project
		// and keep its pending prompt in that project's state file.
		svcFor: func(repoRoot string) workspace.Service {
			return newDeckActionService(runner, repoRoot, nil)
		},
	}
	return runDeckWithCharm(runner, svc, in, out, deckui.ScopeAll, backend)
}
