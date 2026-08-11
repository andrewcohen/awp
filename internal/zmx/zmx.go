// Package zmx talks to the zmx session store.
//
// zmx keeps a process alive on a PTY and lets a client attach to it. Unlike
// tmux it has no windows, no panes and no status bar, so a client *is* the
// program — attaching renders the bare process, and the session's size follows
// whatever single client is looking at it. That is what lets awp own layout
// instead of negotiating for it.
//
// This package is the only thing in awp that knows the session substrate is
// zmx. Everything above it deals in names and commands.
package zmx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/creack/pty"

	"github.com/andrewcohen/awp/internal/vterm"
)

// RunFunc runs a command and returns its combined output, matching the shape
// the rest of awp uses for injectable command execution.
type RunFunc func(ctx context.Context, dir, name string, args ...string) (string, error)

// Client issues zmx commands.
type Client struct{ run RunFunc }

// New builds a Client over the given runner.
func New(run RunFunc) Client { return Client{run: run} }

// Session is one entry from `zmx ls`.
type Session struct {
	Name     string
	PID      int
	Clients  int
	StartDir string
	// Ended is true once the session's command has exited. zmx keeps the
	// session listed afterwards so its output can still be read, so "listed"
	// and "running" are different questions.
	Ended    bool
	ExitCode int
	// Created is when zmx started the session, and Cmd the command it is
	// running. Both are only interesting to something displaying the session
	// to a human — `zmx ls` prints created as a unix stamp, which is not a
	// thing to show anyone.
	Created time.Time
	Cmd     string
	Labels  map[string]string
}

// Live reports whether the session's process is still running.
func (s Session) Live() bool { return !s.Ended }

// SessionName is the only way to name an awp session, so that every call site
// spells it the same and `zmx ls` stays readable.
//
// zmx rejects a name containing a slash — measured, it silently declines to
// create the session — so segments are joined with dots and anything outside
// a conservative set is replaced.
func SessionName(project, workspace, kind string) string {
	kind = SessionKind(kind)
	// The stem gets whatever the kind does not need, so a name only loses
	// anything when it would not otherwise exist. Every session in the socket
	// directory today is one that fits, and a spelling that changed for no reason
	// would leave all of them unrecognised — awp would start a second of each.
	return shortenTo(SessionStem(project, workspace), MaxSessionName-1-len(kind)) + "." + kind
}

// SessionKind is the spelling of a kind inside a session name.
//
// Bounded like the stem, and for the same reason — but the reduction matters more
// here, because a kind is read back and acted on: the sessions overlay reopens a
// pane from it, and a user action's pane finds its command by matching it against
// the config. So a caller holding a kind must compare against this rather than
// against the kind it started with, which is what keeps a shortened one resolving
// to the action it came from.
func SessionKind(kind string) string { return shortenTo(Sanitize(kind), maxKind) }

// SessionStem is the part of the name every one of a workspace's sessions
// shares: awp, the project and the workspace, with only the kind still to come.
//
// Named because it is how a session is matched back to the deck row that owns
// it. Going the other way — splitting a name into parts — cannot be relied on,
// since a name too long for zmx has to be shortened to exist and a shortened
// segment is no longer the workspace's name. Generating the stem from the row is
// right by construction either way.
// Unshortened: this is the stem a name would have if it fit, and SessionName
// shortens it against the room the kind leaves. A caller matching a session back
// to a workspace uses StemMatches rather than comparing against this, because
// the stem it holds may be a shortened one.
func SessionStem(project, workspace string) string {
	return "awp." + Sanitize(project) + "." + Sanitize(workspace)
}

// StemMatches reports whether stem — read off a session name — is the one this
// workspace would produce.
//
// Two spellings can be right, and which one depends on how much room the kind
// left, which the stem alone does not say. So the comparison reproduces the
// shortening at the length the stem actually has: shortenTo is deterministic
// given an input and a budget, so a stem this workspace generated matches at
// exactly one length and no other workspace's does.
//
// This is why the deck asks per row rather than looking a stem up in a map. With
// a handful of workspaces the loop costs nothing, and it means the name generator
// is free to spend the budget however it likes without a second place having to
// agree on the arithmetic.
func StemMatches(project, workspace, stem string) bool {
	full := SessionStem(project, workspace)
	if stem == full {
		return true
	}
	return len(stem) < len(full) && stem == shortenTo(full, len(stem))
}

// The budget a session name has, and how it is split.
//
// zmx turns a name into a socket path, so the name is bounded by what a unix
// socket address can hold: sun_path is 104 bytes on darwin, and the daemon's
// socket directory under a macOS per-user TMPDIR spends 56 of them. Measured
// against the real daemon, which reports the number itself — 47 bytes is refused
// with "max 46 for socket directory /var/folders/…/T/zmx-502".
//
// 46 is the floor rather than a guess at every machine: that TMPDIR is a fixed
// width by construction, and a Linux socket dir (/run/user/<uid>, /tmp/zmx-<uid>)
// is shorter, so a name that fits here fits there. If some environment is tighter
// still, zmx's own error names its max and the directory, which is a better
// message than any check here would write.
//
// The kind is bounded on its own and the stem gets the rest. The kind has to be
// the fixed one because it is compared without reference to a workspace — a pane
// resolves its user action by matching kinds — whereas a stem is only ever
// compared against a workspace that can reproduce it.
const (
	// MaxSessionName is the longest name zmx will accept.
	MaxSessionName = 46
	// maxKind leaves room for `action_` plus a nine-character action name, which
	// covers the ones anybody writes; longer ones are shortened like a stem. No
	// kind in use today is anywhere near it, so nothing existing is renamed.
	maxKind = 16
)

// shortenHashLen is how much of a name's fingerprint survives shortening: 4 hex
// characters, 65536 buckets. It is not there to make collisions impossible, only
// to make them not happen between the handful of workspaces one project has —
// what it replaces is a plain truncation, under which every workspace sharing a
// prefix collapses onto one session, and two agents would be one agent.
const shortenHashLen = 4

// shortenTo bounds a name segment, keeping the front of it and a fingerprint of
// the whole.
//
// Truncation alone cannot be used: two workspaces named after the same PR (say
// pr-2336-dev-mlwzqyrmxslo and pr-2336-dev-qqtnvbdlrxzz) share every character
// the budget has room for, and would address one session — so one workspace's
// `a` would open the other's agent. The fingerprint is of the untruncated input,
// so it differs exactly when the inputs do.
//
// Deterministic, because the name is an address: the same row and kind have to
// resolve to the same session on every pass, across restarts, from the deck and
// from a detached create alike.
func shortenTo(s string, max int) string {
	if len(s) <= max {
		return s
	}
	sum := sha256.Sum256([]byte(s))
	fingerprint := hex.EncodeToString(sum[:])[:shortenHashLen]
	// The separator is what stops a truncated name from reading as a real one
	// that happens to end in hex.
	keep := max - shortenHashLen - 1
	if keep < 1 {
		// No room to keep anything recognisable; the fingerprint alone is still a
		// unique address, which is the part that cannot be given up.
		return fingerprint
	}
	return strings.TrimRight(s[:keep], "-_.") + "-" + fingerprint
}

// SplitSessionName separates a name into the stem and the kind, for a caller
// holding a name and a set of stems it knows.
//
// The kind is whatever followed the last dot, so this is safe on a shortened
// name: shortening only ever touches the stem, because the kind is what reopens
// a pane and what resolves a user action's command.
func SplitSessionName(name string) (stem, kind string, ok bool) {
	name = strings.TrimSpace(name)
	i := strings.LastIndex(name, ".")
	if i <= 0 || !strings.HasPrefix(name, "awp.") {
		return "", "", false
	}
	return name[:i], name[i+1:], true
}

// The labels awp writes on every session it creates, holding the identity the
// name used to be read for.
//
// A name cannot keep carrying it. zmx's names are bounded by the socket path
// they become — measured at 46 bytes for a socket directory under a macOS
// per-user TMPDIR — and awp.<project>.<workspace>.<kind> passes that for
// ordinary input: a workspace named after a PR's head branch spends 24 of those
// bytes on its own, and awp.alpha.pr-2336-dev-mlwzqyrmxslo.action_dev is 47.
// A name that has to be shortened to exist can no longer be split back into the
// parts it was made from, so the parts are stated separately.
//
// Labels rather than a file awp keeps: they live and die with the session, so
// there is nothing to reconcile when one is killed from outside awp, and
// `zmx ls` prints them inline — the read costs no extra call.
const (
	LabelProject   = "awp_project"
	LabelWorkspace = "awp_workspace"
	LabelKind      = "awp_kind"
)

// IdentityLabels is what awp sets on a session so it can be recognised later.
//
// Unsanitized, deliberately: a label is data rather than an address, so it can
// hold the workspace's real name — which is what has to be matched against a
// deck row, and what a human reading `zmx ls` wants to see.
func IdentityLabels(project, workspace, kind string) map[string]string {
	return map[string]string{
		LabelProject:   strings.TrimSpace(project),
		LabelWorkspace: strings.TrimSpace(workspace),
		LabelKind:      strings.TrimSpace(kind),
	}
}

// Identity says which workspace and kind this session belongs to, and whether
// it is awp's at all.
//
// Labels first, then the name. The fallback is not only for sessions that
// pre-date the labels — a session is created by an attach awp does not run
// itself (the deck hands the command to a pane), so there is a window between
// the session existing and its labels being set, and during it the name is all
// there is.
//
// The name's answer is lossy in two ways worth knowing: a project or workspace
// whose real name contained a dot comes back with an underscore, and one whose
// name was shortened to fit comes back shortened. Both match no deck row, which
// is why anything that must find the row should generate the name it expects
// rather than read the name it got.
func (s Session) Identity() (project, workspace, kind string, ok bool) {
	if p, w := s.Labels[LabelProject], s.Labels[LabelWorkspace]; p != "" && w != "" {
		return p, w, s.Labels[LabelKind], true
	}
	return ParseSessionName(s.Name)
}

// ParseSessionName reads a name SessionName produced back into its parts, and
// reports whether it was one of ours at all — `zmx ls` lists every session on
// the machine, including ones awp did not create.
//
// The split is safe because sanitize replaces a dot with an underscore, so no
// segment can contain one. Prefer Session.Identity, which asks the labels
// first; this is the fallback for a session that has none.
func ParseSessionName(name string) (project, workspace, kind string, ok bool) {
	parts := strings.Split(name, ".")
	if len(parts) != 4 || parts[0] != "awp" {
		return "", "", "", false
	}
	return parts[1], parts[2], parts[3], true
}

// Sanitize is the spelling of a name segment that survives being written into a
// session name and read back out of one.
//
// Exported because a caller that has to recognise a segment coming back — a
// user-action pane matching its kind against the configured actions — has to
// compare against the same reduction, not against the original name.
func Sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}

// List returns every session zmx knows about.
func (c Client) List(ctx context.Context) ([]Session, error) {
	out, err := c.run(ctx, "", "zmx", "ls")
	if err != nil {
		return nil, fmt.Errorf("list zmx sessions (is zmx installed and on PATH?): %w", err)
	}
	var sessions []Session
	for _, line := range strings.Split(out, "\n") {
		if s, ok := parseSession(line); ok {
			sessions = append(sessions, s)
		}
	}
	return sessions, nil
}

// named checks that an operation addressing one session was given one to
// address, and names the operation when it wasn't.
//
// Every method below takes a name a caller computed, usually from SessionName
// over an Item's fields — and an Item arriving with a field missing is a thing
// this codebase has been bitten by more than once. A name that came out empty
// is a bug upstream, and the useful behaviour is to say so rather than to hand
// a process manager an empty argument and find out what it decides that means.
func named(op, name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%s a zmx session: no name given", op)
	}
	return nil
}

// Lookup finds one session by name.
func (c Client) Lookup(ctx context.Context, name string) (Session, bool, error) {
	if err := named("look up", name); err != nil {
		return Session{}, false, err
	}
	all, err := c.List(ctx)
	if err != nil {
		return Session{}, false, err
	}
	for _, s := range all {
		if s.Name == name {
			return s, true, nil
		}
	}
	return Session{}, false, nil
}

// parseSession reads one `zmx ls` line: tab-separated key=value pairs, with
// anything unrecognised kept as a label.
func parseSession(line string) (Session, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return Session{}, false
	}
	s := Session{Labels: map[string]string{}}
	for _, field := range strings.Split(line, "\t") {
		key, value, ok := strings.Cut(strings.TrimSpace(field), "=")
		if !ok {
			continue
		}
		switch key {
		case "name":
			s.Name = value
		case "pid":
			s.PID, _ = strconv.Atoi(value)
		case "clients":
			s.Clients, _ = strconv.Atoi(value)
		case "start_dir":
			s.StartDir = value
		case "ended":
			s.Ended = true
		case "exit_code":
			s.ExitCode, _ = strconv.Atoi(value)
		case "created":
			// A unix stamp. Parsed here so nothing downstream has to know that,
			// and so a display can show an age instead of a number.
			if secs, err := strconv.ParseInt(value, 10, 64); err == nil && secs > 0 {
				s.Created = time.Unix(secs, 0)
			}
		case "cmd":
			s.Cmd = value
		default:
			s.Labels[key] = value
		}
	}
	if s.Name == "" {
		return Session{}, false
	}
	return s, true
}

// Reap clears the way for a fresh session called name, and reports whether it
// had to remove one.
//
// A session that is listed but whose command has exited is not reusable:
// attaching to it would render a dead program's last screen. A live session is
// left alone — attaching to that one is the point.
//
// Creating the session is AttachCmd's job, not this one's. zmx has no way to
// start a session detached with a given command as its own process (see
// AttachCmd), so the only correct order is reap, then attach.
func (c Client) Reap(ctx context.Context, name string) (removed bool, err error) {
	if err := named("reap", name); err != nil {
		return false, err
	}
	existing, found, err := c.Lookup(ctx, name)
	if err != nil {
		return false, err
	}
	if !found || existing.Live() {
		return false, nil
	}
	if _, err := c.run(ctx, "", "zmx", "kill", name, "--force"); err != nil {
		return false, fmt.Errorf("remove the finished zmx session %q: %w", name, err)
	}
	return true, nil
}

// Label sets key=value metadata on a session, which `zmx ls` reports back.
func (c Client) Label(ctx context.Context, name string, pairs map[string]string) error {
	if len(pairs) == 0 {
		return nil
	}
	if err := named("label", name); err != nil {
		return err
	}
	args := []string{"set", name}
	for k, v := range pairs {
		args = append(args, k+"="+v)
	}
	if _, err := c.run(ctx, "", "zmx", args...); err != nil {
		return fmt.Errorf("label zmx session %q: %w", name, err)
	}
	return nil
}

// pasteSettle gives the receiving program time to finish ingesting a
// bracketed paste before the submit that follows it. Without the gap a large
// paste swallows the Enter instead of being submitted by it — the same
// hazard, and the same remedy, as the tmux path this mirrors.
const pasteSettle = 150 * time.Millisecond

// Paste delivers text to the session's program as one bracketed paste and
// then submits it.
//
// Bracketed rather than typed: a prompt with newlines sent as raw input is a
// stream of submits, so an agent would receive the first line as a message and
// the rest as separate ones. The markers tell it the whole block arrived at
// once.
func (c Client) Paste(ctx context.Context, name, text string) error {
	if err := named("paste to", name); err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("paste to zmx session %q: nothing to send", name)
	}
	if _, err := c.run(ctx, "", "zmx", "send", name, "\x1b[200~"+text+"\x1b[201~"); err != nil {
		return fmt.Errorf("paste into zmx session %q: %w", name, err)
	}
	time.Sleep(pasteSettle)
	if _, err := c.run(ctx, "", "zmx", "send", name, "\r"); err != nil {
		return fmt.Errorf("submit the paste in zmx session %q: %w", name, err)
	}
	return nil
}

// SetLabels records key=value pairs on a session.
//
// Nothing here waits for the session to exist. The one caller that can be sure
// it does is the detached start, which polls; the pane path hands its attach to
// the deck to run, so its labels are written on a later pass once the session is
// listed. That is why the labels are a refinement of the identity rather than
// the only source of it — see Session.Identity.
//
// An empty value removes the label, which is zmx's own convention (`k=`), so a
// caller does not need a second method to unset one.
func (c Client) SetLabels(ctx context.Context, name string, labels map[string]string) error {
	if err := named("label", name); err != nil {
		return err
	}
	if len(labels) == 0 {
		return nil
	}
	// Sorted, so the command is the same every time it is built from the same
	// map — a test can state what it expects, and a log line does not shuffle.
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	args := make([]string, 0, len(keys)+2)
	args = append(args, "set", name)
	for _, k := range keys {
		args = append(args, k+"="+labels[k])
	}
	if _, err := c.run(ctx, "", "zmx", args...); err != nil {
		return fmt.Errorf("label zmx session %q: %w", name, err)
	}
	return nil
}

// Kill ends a session and everything attached to it.
func (c Client) Kill(ctx context.Context, name string) error {
	if err := named("kill", name); err != nil {
		return err
	}
	if _, err := c.run(ctx, "", "zmx", "kill", name, "--force"); err != nil {
		return fmt.Errorf("kill zmx session %q: %w", name, err)
	}
	return nil
}

// History returns the session's scrollback, with escape sequences, without
// attaching to it — a peek that costs nothing and needs no client.
func (c Client) History(ctx context.Context, name string) (string, error) {
	if err := named("read the scrollback of", name); err != nil {
		return "", err
	}
	out, err := c.run(ctx, "", "zmx", "history", name, "--vt")
	if err != nil {
		return "", fmt.Errorf("read the scrollback of zmx session %q: %w", name, err)
	}
	return out, nil
}

// AttachCmd is the command that hosts a session in a terminal awp owns,
// creating it to run argv in dir if it is not there yet. An empty argv asks
// zmx for a login $SHELL instead.
//
// This is `zmx attach`, and deliberately not `zmx run`. run spawns a login
// bash and *types the command at its prompt*: the session's process is the
// shell, so the pane shows bash's banner, bash's prompt, the command echoed
// back, and an exit-code marker after it — none of which is the program you
// asked for. attach makes argv the session's own process, with zmx as its
// parent and nothing in between.
//
// argv is ignored when the session already exists, which is what makes one
// call both "create it" and "attach to it".
func AttachCmd(dir, name string, argv, env []string) *exec.Cmd {
	args := append([]string{"attach", name}, argv...)
	cmd := exec.Command("zmx", args...) //nolint:gosec // name comes from SessionName, argv from awp's own config
	cmd.Dir = dir
	cmd.Env = vterm.Env(env)
	return cmd
}

// The size of the pty a detached start allocates, and how long it waits for the
// daemon to report the session.
//
// A session takes its size from the single client looking at it, and this client
// exists for about a tenth of a second — so the numbers here are what the
// program's first output is laid out at, until the first real client resizes it.
// A common terminal shape rather than the 80x24 default, because that is what
// makes the reflow on first attach small; there is no size that avoids one.
const (
	detachedCols       = 120
	detachedRows       = 40
	detachedPoll       = 100 * time.Millisecond
	detachedAppearWait = 5 * time.Second
)

// StartDetached creates a session running argv and leaves it running with no
// client attached.
//
// This is how a process gets started for someone who is not watching: a
// workspace created with a prompt should have its agent working on it before
// anyone opens a pane, which is what the tmux path does by starting the agent in
// a session and switching to it.
//
// It goes through attach and a pty because the two obvious shortcuts are both
// wrong. `zmx run -d` creates the session detached, but its process is a login
// bash with the command typed at the prompt — so "is the agent still running"
// stops being answerable (see AttachCmd), which is the property the deck reads a
// row's agent state from. And attach needs a tty on stdin: measured, `setsid zmx
// attach <name> <cmd> </dev/null` creates nothing at all.
//
// So: allocate a pty, attach on it, wait for the daemon to list the session, and
// throw the client away. Losing a client is not losing the session — that is what
// long-lived means, and it is what closing a pane does every day.
//
// The client is always ended before this returns, on every path. A detached
// create runs in a subprocess that exits moments later, and an attach client
// left behind reparents to init holding a pty nobody will ever read.
func (c Client) StartDetached(ctx context.Context, dir, name string, argv, env []string) error {
	if err := named("start", name); err != nil {
		return err
	}
	if len(argv) == 0 {
		return fmt.Errorf("start zmx session %q: no command to run in it", name)
	}
	cmd := AttachCmd(dir, name, argv, env)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: detachedCols, Rows: detachedRows})
	if err != nil {
		return fmt.Errorf("start zmx session %q on a pty: %w", name, err)
	}
	defer func() { _ = ptmx.Close() }()
	// Read the pty and drop what comes out. Without a reader the program blocks
	// on a full buffer as soon as it prints more than a pipe's worth, which for an
	// agent's banner is immediately.
	go func() { _, _ = io.Copy(io.Discard, ptmx) }()

	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	appeared := time.After(detachedAppearWait)
	tick := time.NewTicker(detachedPoll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			endClient(cmd)
			return fmt.Errorf("start zmx session %q: %w", name, ctx.Err())
		case err := <-exited:
			// The client is gone and the session never appeared, so the reason went
			// to the pty we are discarding. Name the likeliest cause instead.
			return fmt.Errorf("start zmx session %q: the attach exited before the session appeared (%v) — is the zmx daemon running?", name, err)
		case <-appeared:
			endClient(cmd)
			return fmt.Errorf("start zmx session %q: the daemon did not list it within %s", name, detachedAppearWait)
		case <-tick.C:
			// Live, not merely listed: a session whose command has already exited is
			// one this failed to start, and reporting success would leave the caller
			// thinking an agent is working.
			s, found, err := c.Lookup(ctx, name)
			if err != nil || !found || !s.Live() {
				continue
			}
			endClient(cmd)
			return nil
		}
	}
}

// endClient stops the attach client without waiting on the session it made.
func endClient(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	// Reaped by the Wait already running in StartDetached, so nothing here has to
	// collect it — and nothing here may, since two Waits on one process is an
	// error.
}

// Command is a process awp hosts directly, with no session behind it, for
// panes that should die when you stop looking at them.
func Command(dir string, argv []string, env []string) *exec.Cmd {
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec // argv is awp's own configuration
	cmd.Dir = dir
	cmd.Env = vterm.Env(env)
	return cmd
}
