package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/state"
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

// paneSpec is how zdeck hosts one window kind: what to call its session, whether
// that session outlives the pane, and the process to put in it.
type paneSpec struct {
	label    string
	lifetime paneLifetime
	argv     func(it deckui.Item) []string
}

// panes maps a deck window kind to how zdeck hosts it. Kinds absent from this
// map fall through to the ordinary tmux window — which is how the review and
// PR-description windows keep working.
//
// It is not the whole answer: a user action's pane is spelled by the workspace's
// own config rather than here, so kinds are resolved through specFor and not by
// indexing this directly.
//
// Andrew's call on lifetimes: the agent and the editor are worth keeping alive
// between glances; a shell, jjui, CI and the watch view are not, since you open
// those to do one thing and the next one can start fresh. CI and watch are the
// clearest ephemerals of the set — each runs one foreground program you watch
// until it says something, and a stale one is worse than no one at all.
var panes = map[string]paneSpec{
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
	// actionsFor reads the user actions configured for a repo. Per-repo for the
	// same reason as svcFor: a cross-project deck resolves `x` against the
	// selected row's config, not the one the deck started in.
	actionsFor func(repoRoot string) []deckui.UserAction
}

// specFor resolves a window kind to the pane that hosts it.
//
// Two sources, because a user action is named by whoever wrote the config: the
// fixed kinds are in the panes map, and an action's command can only come from
// the workspace's own config. Hence a method rather than a map lookup.
func (z zmxPanes) specFor(item deckui.Item, kind string) (paneSpec, error) {
	if _, ok := deckui.ActionFromPaneKind(kind); ok {
		return z.actionSpec(item, kind)
	}
	spec, ok := panes[kind]
	if !ok {
		return paneSpec{}, fmt.Errorf("zdeck has no pane for %q", kind)
	}
	return spec, nil
}

// actionSpec is the pane for the user action named by a kind.
//
// Long-lived, and that is the point of it: the case this exists for is a dev
// server, which you start once and leave running while you work in the agent
// pane. It also makes "is it still up?" answerable — the command is the
// session's own process, so a live session *is* a running server, the same
// property that lets the deck read an agent's state off its session.
//
// Matched as a whole kind reduced by zmx.SessionKind, on both sides, because a
// kind that came back out of a session name has already been through that
// reduction: an action called "dev server" is `action_dev_server` there, and one
// with a name long enough to need shortening comes back shortened. Comparing
// against the config's own spelling would miss both. Reducing the config's kind
// the same way is what makes a shortened one still resolve to the action it was
// made from — the rule holds by construction rather than by the two sides
// agreeing to truncate identically.
func (z zmxPanes) actionSpec(item deckui.Item, kind string) (paneSpec, error) {
	name, _ := deckui.ActionFromPaneKind(kind)
	root := strings.TrimSpace(item.RepoRoot)
	if z.actionsFor == nil || root == "" {
		return paneSpec{}, fmt.Errorf("run the %q action for %q: this deck cannot read the workspace's user actions", name, item.WorkspaceName)
	}
	want := zmx.SessionKind(kind)
	for _, a := range z.actionsFor(root) {
		if zmx.SessionKind(deckui.PaneKindForAction(a.Name)) != want {
			continue
		}
		command := strings.TrimSpace(a.Command)
		if command == "" {
			return paneSpec{}, fmt.Errorf("run the %q action for %q: it has no command — give it one under \"actions\" in %s/.awp/config.json", a.Name, item.WorkspaceName, root)
		}
		return paneSpec{
			label:    deckui.PaneKindForAction(a.Name),
			lifetime: longLived,
			// A shell, unlike every other pane here. A user action's command has
			// always been a shell line: the tmux path types it at a prompt and a
			// background action runs it under `sh -c`, so splitting it on
			// whitespace instead would make one config field mean two different
			// things depending on which deck read it.
			argv: func(deckui.Item) []string { return []string{"sh", "-c", command} },
		}, nil
	}
	return paneSpec{}, fmt.Errorf("run the %q action for %q: no user action by that name in %s — add it under \"actions\" in .awp/config.json", name, item.WorkspaceName, root)
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

// markRead clears the workspace's unread mark, because the user is now looking
// at the thing it was pointing at.
//
// Every tmux window switch does this — openWorkspaceSession, openNamedWindow,
// the CI window, a focused user action — so under tmux reading the agent's
// output clears the badge. No pane path did, so the dot and the attention count
// survived reading the very output they were about.
//
// Narrower than the tmux rule on purpose: only the agent pane. The mark means
// the agent produced output you have not seen, and the agent's pane is the only
// one that shows it. Under tmux any window switch cleared it because a switch
// puts you in the session with the agent one key away; here the panes are
// separate surfaces, so opening jjui is not evidence you read anything.
//
// Best-effort, and deliberately silent: the pane is already opening, there is
// nowhere to report to, and an unread dot that outlives its output is a smaller
// problem than a pane that refuses to open over a state write.
func (z zmxPanes) markRead(item deckui.Item) {
	if z.svcFor == nil || strings.TrimSpace(item.RepoRoot) == "" {
		return
	}
	_ = z.svcFor(item.RepoRoot).MarkRead(item.WorkspaceName)
}

// Describes claims a kind for the pane path rather than tmux.
//
// Every user action is claimed on the strength of the prefix alone, without
// checking that one is configured: Describes is asked before there is a row in
// hand, so the config it would have to read is not reachable here. An action
// that does not resolve fails in Open, which can say which repo it looked in —
// a better answer than falling through to tmux, where the key would start a
// second agent in a server nobody is looking at.
func (zmxPanes) Describes(kind string) bool {
	if _, ok := deckui.ActionFromPaneKind(kind); ok {
		return true
	}
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
		// And the workspace's user actions, which are kinds like any other. Left
		// out, a dev server's session still listed — it is awp's by name — but as
		// one belonging to no row, so `enter` on it could not reopen it.
		if z.actionsFor != nil && strings.TrimSpace(it.RepoRoot) != "" {
			for _, a := range z.actionsFor(it.RepoRoot) {
				byName[zmx.SessionName(it.ProjectName, it.WorkspaceName, deckui.PaneKindForAction(a.Name))] = it
			}
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
			Name:     s.Name,
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

// EndSession kills a zmx session by the name the deck was given in Sessions.
//
// --force, which zmx.Client.Kill supplies: the whole point of ending a session
// from the deck is that its agent is not going to be asked nicely, and a kill
// that waits for a program to agree would hang on exactly the session you most
// want gone.
func (z zmxPanes) EndSession(name string) error {
	return z.client.Kill(context.Background(), name)
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
	// No fallback. A pane's directory is the workspace's working copy or it is
	// nothing — the previous `if dir == "" { dir = item.RepoRoot }` is what made
	// a wrong directory silent instead of an error, and a program started in the
	// source repo instead of the workspace looks entirely normal until you read
	// what it wrote. The two rows that reach here without a Path are a workspace
	// still being created (#243 stops that upstream) and an unmanaged row, which
	// under a pane host is a leftover tmux session a zmx pane has no business
	// guessing about.
	//
	// Asked before the kind is resolved, because a row with no working copy has
	// nowhere to run anything and that is the more useful thing to say — it holds
	// for every kind, including one the config turns out not to name.
	dir := strings.TrimSpace(item.Path)
	if dir == "" {
		return nil, nil, fmt.Errorf("open the %s pane for %q: the workspace has no working copy on disk yet — wait for it to finish setting up, or press enter to create it", deckui.PaneLabel(kind), item.WorkspaceName)
	}
	spec, err := z.specFor(item, kind)
	if err != nil {
		return nil, nil, err
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
		// Opening the agent is reading what it said, so the mark it said it with
		// goes. Last, after the delivery that can still fail: a pane that did not
		// open has shown nobody anything.
		z.markRead(item)
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
	//
	// Leaving the pane is the other half of reading it. An agent that goes idle
	// while you sit in its pane marks the workspace unread from the hook, because
	// the write-time suppression that would have stopped it asks tmux whether a
	// client is attached and under this deck the answer is always nobody — and
	// entry is already behind us, so nothing else would ever clear that mark.
	// Closing the pane is the moment the reading finished.
	onClose := func() {}
	if kind == deckui.PaneKindAgent {
		onClose = func() { z.markRead(item) }
	}
	return zmx.AttachCmd(dir, name, argv, env), onClose, nil
}

// SendPrompt delivers text to the workspace's agent — the same process `a`
// shows, and the reason this exists.
//
// Without it the deck sends prompts through tmux, to a session zdeck never
// opens, so a workspace ends up with two agents: the zmx one on screen and an
// invisible tmux one receiving everything you type at it.
//
// The send itself is shared with the surfaces that have no host object to ask —
// see agentPromptSender, which is what decides that this is the agent to send to.
func (z zmxPanes) SendPrompt(item deckui.Item, text string, reporter deckui.Reporter) error {
	return sendPromptToZmxAgent(z.client, item, text, reporter)
}

// runZdeck is `awp deck` with zmx behind the window keys instead of tmux.
// sessionSource is the substrate zdeck's long-lived processes live on, so the
// deck reads zmx rather than tmux. Same client, so a row cannot describe one
// substrate while the keys act on the other.
func (z zmxPanes) sessionSource() deckSessions {
	return zmxSessions{client: z.client, rows: knownWorkspaceRefs}
}

// knownWorkspaceRefs is every workspace in the global store, as the deck's own
// (project, workspace) pairs.
//
// The same read loadDeckItems does. Duplicating it costs one JSON file per
// refresh and buys the session source the one thing it cannot get from a session
// name: which workspaces exist, so a name can be generated and matched instead
// of parsed. A read that fails returns nothing, which leaves every session to
// the name-reading fallback rather than emptying the deck.
func knownWorkspaceRefs() []workspaceRef {
	byRepo, err := state.NewJSONStore().LoadAll()
	if err != nil {
		return nil
	}
	var refs []workspaceRef
	for root, entries := range byRepo {
		project := strings.TrimSpace(filepath.Base(filepath.Clean(root)))
		for _, e := range entries {
			refs = append(refs, workspaceRef{project: project, workspace: e.Name})
		}
	}
	return refs
}

// zmxSessions answers the deck's session questions from `zmx ls`.
//
// Deliberately not a merge with tmux. A workspace whose agent is in a leftover
// tmux session — started earlier by `awp deck` — reads as having no session
// here, and that is the honest answer: zdeck cannot show it to you, `a` will
// not take you to it, and calling it active is exactly the stale read the
// session-truth seam exists to remove.
type zmxSessions struct {
	client zmx.Client
	// rows names the workspaces the deck knows about, so a session can be
	// matched to one by generating the name that workspace would have rather
	// than by reading the name it got.
	//
	// The direction matters because a name too long for zmx has to be shortened
	// to exist, and a shortened name no longer contains the workspace's name —
	// so reading it back would fail to match exactly the workspaces the
	// shortening is for, and their agents would read as having no session at
	// all. Generating is right whether or not the name was shortened.
	//
	// A func rather than a value: workspaces come and go while the deck is open,
	// and a snapshot taken at construction would stop including new ones. Nil
	// falls back to reading the name, which is also what happens for a session
	// no row claims — that is how a leftover session with no workspace behind it
	// is still recognised as awp's.
	rows func() []workspaceRef
}

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
	rows := z.knownRows()
	for _, s := range list {
		ref, kind, ok := z.refFor(s, rows)
		if !ok {
			// Not a session awp made. The deck has nothing to say about the
			// rest of the user's zmx.
			continue
		}
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

// roots is each live session's own process, bucketed by workspace.
//
// One PID per session rather than per pane, because a zmx session *is* the
// process — there are no panes under it to enumerate. A dev server started in
// one is a descendant of that PID, which is what the discoverer walks, so the
// answer is the same shape as tmux's for a substrate shaped differently.
//
// Ended sessions are skipped. zmx keeps a session listed after its command
// exits so the output can still be read, and a PID that has been reused by the
// kernel would hand the discoverer somebody else's sockets.
func (z zmxSessions) roots() map[workspaceRef][]int {
	list, err := z.client.List(context.Background())
	if err != nil {
		return nil
	}
	rows := z.knownRows()
	out := map[workspaceRef][]int{}
	for _, s := range list {
		if !s.Live() || s.PID <= 0 {
			continue
		}
		ref, _, ok := z.refFor(s, rows)
		if !ok {
			continue
		}
		out[ref] = append(out[ref], s.PID)
	}
	return out
}

// knownRows is the deck's workspaces, or nothing when none are wired — which
// leaves every session to the name-reading fallback.
func (z zmxSessions) knownRows() []workspaceRef {
	if z.rows == nil {
		return nil
	}
	return z.rows()
}

// rowForStem finds the workspace whose sessions carry this stem.
//
// A loop rather than a map lookup because a stem can be a shortened one, and
// which shortening it is depends on how much room the kind left — so the question
// is "could this row have produced it", which only the row can answer. See
// zmx.StemMatches.
func rowForStem(rows []workspaceRef, stem string) (workspaceRef, bool) {
	for _, ref := range rows {
		if zmx.StemMatches(ref.project, ref.workspace, stem) {
			return ref, true
		}
	}
	return workspaceRef{}, false
}

// refFor says which workspace a session belongs to, and which kind it is.
//
// Three answers in order of how much they can be trusted. A stem this deck
// generated is exact — the row is where the name came from. The session's own
// labels are next, and hold the workspace's real name even when the stem was
// shortened, which covers a session whose row has since been renamed or
// deleted. Reading the name is last: lossy for a shortened or dot-containing
// name, but it is what recognises a session as awp's at all, and being wrong
// about which workspace a leftover belongs to is better than not seeing it.
func (z zmxSessions) refFor(s zmx.Session, rows []workspaceRef) (workspaceRef, string, bool) {
	stem, kind, ok := zmx.SplitSessionName(s.Name)
	if !ok {
		return workspaceRef{}, "", false
	}
	if ref, found := rowForStem(rows, stem); found {
		return ref, kind, true
	}
	project, workspace, labelKind, ok := s.Identity()
	if !ok {
		return workspaceRef{}, "", false
	}
	if labelKind != "" {
		kind = labelKind
	}
	return workspaceRef{project: project, workspace: workspace}, kind, true
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

// hostedAgent is a workspace's agent, and the prompt it is being started for.
type hostedAgent struct {
	project   string
	workspace string
	repoRoot  string
	// dir is the workspace's working copy — where the agent runs, and never the
	// source repo. Same rule as a pane's directory, and for the same reason.
	dir    string
	prompt string
	// review says the recipient reviews someone else's change, so it must start
	// without the dev-loop preamble. A reviewer told to work in units, run gates
	// and commit starts doing the author's job on their PR.
	review bool
}

// startHostedAgent starts the workspace's agent on a session of its own and
// delivers the prompt as its argument.
//
// This is the pane host's answer to the last thing the tmux create does: start
// the agent and switch to it. A create runs detached, so there is no terminal to
// hand over — but there does not need to be one, since the agent's session is not
// where the user is looking (see zmx.StartDetached). Without it the prompt sits
// parked until someone opens the pane, so "create five workspaces with prompts
// and come back to five agents working" held under tmux and not here.
//
// Only ever called with a prompt. A workspace created without one has nothing for
// an agent to do yet, and starting an idle one per create would spend a process
// and a row's worth of "running" on a workspace nobody has asked anything of.
//
// The prompt goes in as argv rather than a paste for the reason the pane path
// documents: the session is being created, so the agent's own argument is the one
// delivery that cannot race its input box.
func startHostedAgent(runner Runner, a hostedAgent, reporter deckui.Reporter) error {
	if reporter == nil {
		reporter = noopReporter{}
	}
	prompt := strings.TrimSpace(a.prompt)
	if prompt == "" {
		return fmt.Errorf("start the agent for %q: no prompt to start it with", a.workspace)
	}
	dir := strings.TrimSpace(a.dir)
	if dir == "" {
		return fmt.Errorf("start the agent for %q: the workspace has no working copy to run it in", a.workspace)
	}
	// On disk, not merely named. An agent started in a directory that is not there
	// fails inside the attach, where the reason goes to a pty nobody reads — and
	// this runs at the end of a create, which is exactly when a path can be
	// predicted but not yet real.
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return fmt.Errorf("start the agent for %q: %s is not a directory to run it in (%v)", a.workspace, dir, err)
	}
	if _, err := exec.LookPath("zmx"); err != nil {
		return fmt.Errorf("start the agent for %q: zmx is not on PATH: %w", a.workspace, err)
	}
	argv := codingAgentArgv(a.repoRoot)
	if a.review {
		argv = reviewAgentArgv(a.repoRoot)
	}
	argv = append(argv, prompt)
	// The same env a pane gives an agent. Without it the hooks report nothing, so
	// the deck shows the row idle while the agent works.
	env := append(os.Environ(), workspaceEnvPairs(a.project, a.workspace, a.repoRoot)...)
	name := zmx.SessionName(a.project, a.workspace, deckui.PaneKindAgent)
	reporter.Step("Start the agent in " + name)
	client := zmxClientFor(runner)
	if err := client.StartDetached(context.Background(), dir, name, argv, env); err != nil {
		return err
	}
	// The session exists by here — StartDetached waits for the daemon to list it
	// — so this is the one creation path that can state the identity immediately.
	// Best-effort: a session whose labels did not land is still found by its
	// name, and failing the create over bookkeeping would throw away a working
	// agent.
	if err := client.SetLabels(context.Background(), name, zmx.IdentityLabels(a.project, a.workspace, deckui.PaneKindAgent)); err != nil {
		reporter.Log(fmt.Sprintf("could not label %s (%v) — it is still addressable by name", name, err))
	}
	return nil
}

// liveZmxAgent names the workspace's zmx agent session if its process is still
// running, and "" if there is none. It is the zmx half of the question the
// rename guard asks tmux with PaneCurrentCommand.
//
// Simpler than the tmux half, and for the reason that keeps recurring: a zmx
// session runs the agent as its own process, so "is the agent alive" is the
// session being live. There is no window to have fallen back to a shell.
func liveZmxAgent(runner Runner, project, workspaceName string) (string, error) {
	if runner == nil {
		return "", fmt.Errorf("check for a live agent in workspace %q: no runner", workspaceName)
	}
	if _, err := exec.LookPath("zmx"); err != nil {
		return "", nil
	}
	name := agentSessionName(project, workspaceName)
	s, found, err := zmxClientFor(runner).Lookup(context.Background(), name)
	if err != nil {
		// Refusing to guess: a rename that cannot tell whether an agent is
		// running is the case this guard exists for.
		return "", fmt.Errorf("check for a live agent in workspace %q: %w", workspaceName, err)
	}
	if !found || !s.Live() {
		return "", nil
	}
	return name, nil
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
		// The same reader the deck's own action menu uses, so the pane that opens
		// is the action the menu offered.
		actionsFor: userActionsForRepo,
	}
	return runDeckWithCharm(runner, svc, in, out, rememberedScope(deckui.ScopeAll), backend)
}
