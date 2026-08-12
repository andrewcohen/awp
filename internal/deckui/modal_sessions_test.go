package deckui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// sessionBackend is a pane backend that also holds sessions.
type sessionBackend struct {
	fakePanes
	sessions []PaneSession
	err      error
	sawItems []Item
	// ended records the names EndSession was asked for, and endErr makes it fail.
	ended  []string
	endErr error
}

func (b *sessionBackend) Sessions(items []Item) ([]PaneSession, error) {
	b.sawItems = items
	return b.sessions, b.err
}

func (b *sessionBackend) EndSession(name string) error {
	if b.endErr != nil {
		return b.endErr
	}
	b.ended = append(b.ended, name)
	// The row goes when the session does, which is what a reload would show.
	kept := b.sessions[:0]
	for _, s := range b.sessions {
		if s.Name != name {
			kept = append(kept, s)
		}
	}
	b.sessions = kept
	return nil
}

func sessionDeck(t *testing.T, b PaneBackend) Model {
	t.Helper()
	m := New([]Item{
		{ProjectName: "awp", WorkspaceName: "test", Path: "/w/a", RepoRoot: "/r/a"},
		{ProjectName: "alpha", WorkspaceName: "probe", Path: "/w/b", RepoRoot: "/r/b"},
	}, func(ActionRequest) error { return nil }).WithPaneBackend(b)
	m.width, m.height = 120, 40
	return m
}

func openSessions(t *testing.T, m Model) (Model, *sessionPicker) {
	t.Helper()
	next, cmd := m.Update(runeKey("z"))
	got := next.(Model)
	p, ok := got.active.(*sessionPicker)
	if !ok {
		t.Fatalf("z did not open the sessions overlay (active=%T)", got.active)
	}
	if cmd != nil {
		if msg := cmd(); msg != nil {
			next, _ = got.Update(msg)
			got = next.(Model)
		}
	}
	return got, p
}

// The key only exists where something hosts sessions. The tmux deck has none,
// so z there must stay free rather than opening an overlay that is always empty.
func TestZDoesNothingWithoutASessionBackend(t *testing.T) {
	m := sessionDeck(t, allKinds()) // a PaneBackend, but not a PaneSessioner
	next, _ := m.Update(runeKey("z"))
	if got := next.(Model); got.active != nil {
		t.Errorf("z opened %T on a backend that hosts no sessions", got.active)
	}

	plain := New([]Item{{ProjectName: "p", WorkspaceName: "w"}}, nil)
	plain.width, plain.height = 120, 40
	next, _ = plain.Update(runeKey("z"))
	if got := next.(Model); got.active != nil {
		t.Errorf("z opened %T on a deck with no pane backend at all", got.active)
	}
}

func TestTheOverlayListsWhatTheBackendHolds(t *testing.T) {
	b := &sessionBackend{fakePanes: *allKinds(), sessions: []PaneSession{
		{Label: "awp/test", Kind: "agent", Live: true, Attached: true, HasItem: true,
			Item: Item{ProjectName: "awp", WorkspaceName: "test"}, Started: time.Now().Add(-90 * time.Minute)},
		{Label: "alpha/probe", Kind: "editor", Live: true, HasItem: true,
			Item: Item{ProjectName: "alpha", WorkspaceName: "probe"}, Started: time.Now().Add(-30 * time.Second)},
	}}
	m, p := openSessions(t, sessionDeck(t, b))

	if len(p.list.Items()) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(p.list.Items()))
	}
	// The deck's rows are handed down so the backend can resolve them itself.
	if len(b.sawItems) != 2 {
		t.Errorf("the backend was given %d rows to match against, want 2", len(b.sawItems))
	}
	// Newest first: the session you just started is the one you are looking for.
	if first, _ := p.list.Items()[0].(sessionItem); first.s.Label != "alpha/probe" {
		t.Errorf("first row is %q, want the newest (alpha/probe)", first.s.Label)
	}

	left, _ := p.view(&m, m.childBox())
	out := ansi.Strip(left)
	for _, want := range []string{"awp/test", "agent", "alpha/probe", "editor", "1h", "30s"} {
		if !strings.Contains(out, want) {
			t.Errorf("the overlay does not show %q:\n%s", want, out)
		}
	}
	// A unix stamp is not a thing to show anyone.
	if strings.Contains(out, "17861") {
		t.Error("the overlay is printing a raw unix timestamp")
	}
}

// A session can outlive the workspace it was started for. Attaching to one has
// nowhere to put the pane, so it has to say so rather than open against a zero
// Item whose path is "".
func TestASessionWithNoRowRefusesToAttach(t *testing.T) {
	b := &sessionBackend{fakePanes: *allKinds(), sessions: []PaneSession{
		{Label: "gone/ws", Kind: "agent", Live: true, HasItem: false, Started: time.Now()},
	}}
	m, p := openSessions(t, sessionDeck(t, b))
	p.attachSelected(&m)
	if _, opened := m.active.(*panePopover); opened {
		t.Error("it opened a pane for a session with no workspace row")
	}
	if m.active != p {
		t.Error("a refused attach closed the overlay instead of staying put")
	}
	if !strings.Contains(m.status, "gone/ws") {
		t.Errorf("the status does not name the session: %q", m.status)
	}
}

// zmx keeps a session listed after its command exits, so "listed" and
// "running" are different questions and attaching to a dead one would show a
// dead program's last screen.
func TestAnExitedSessionRefusesToAttach(t *testing.T) {
	b := &sessionBackend{fakePanes: *allKinds(), sessions: []PaneSession{
		{Label: "awp/test", Kind: "agent", Live: false, HasItem: true,
			Item: Item{ProjectName: "awp", WorkspaceName: "test"}, Started: time.Now()},
	}}
	m, p := openSessions(t, sessionDeck(t, b))
	p.attachSelected(&m)
	if _, opened := m.active.(*panePopover); opened {
		t.Error("it attached to a session whose command had exited")
	}
	if !strings.Contains(m.status, "exited") {
		t.Errorf("the status does not say the session exited: %q", m.status)
	}
}

func TestABackendErrorIsShownNotSwallowed(t *testing.T) {
	b := &sessionBackend{fakePanes: *allKinds(), err: errors.New("zmx is not on PATH")}
	m, p := openSessions(t, sessionDeck(t, b))
	left, _ := p.view(&m, m.childBox())
	if out := ansi.Strip(left); !strings.Contains(out, "zmx is not on PATH") {
		t.Errorf("the error is not on screen:\n%s", out)
	}
}

func TestAnEmptySessionListSaysSo(t *testing.T) {
	b := &sessionBackend{fakePanes: *allKinds()}
	m, p := openSessions(t, sessionDeck(t, b))
	left, _ := p.view(&m, m.childBox())
	if out := ansi.Strip(left); !strings.Contains(out, "No sessions") {
		t.Errorf("an empty list renders nothing useful:\n%s", out)
	}
}

// endableDeck is the overlay open on one live agent, ready for an x.
func endableDeck(t *testing.T) (Model, *sessionPicker, *sessionBackend) {
	t.Helper()
	b := &sessionBackend{fakePanes: *allKinds(), sessions: []PaneSession{
		{Name: "awp.awp.test.agent", Label: "awp/test", Kind: "agent", Live: true, HasItem: true,
			Item: Item{ProjectName: "awp", WorkspaceName: "test"}, Started: time.Now()},
	}}
	m, p := openSessions(t, sessionDeck(t, b))
	return m, p, b
}

// x asks before it ends anything: a live agent's session is its whole context —
// the conversation, what it has read, what it was part-way through — and none of
// it comes back.
func TestEndingASessionAsksFirst(t *testing.T) {
	m, p, b := endableDeck(t)

	if cmd := p.update(&m, runeKey("x")); cmd != nil {
		t.Error("x ran something before the question was answered")
	}
	if p.pendingEnd == nil {
		t.Fatal("x did not ask")
	}
	if len(b.ended) != 0 {
		t.Errorf("x ended %v without asking", b.ended)
	}
	for _, want := range []string{"awp/test", "agent", "[y/N]"} {
		if !strings.Contains(m.status, want) {
			t.Errorf("the question %q does not mention %q", m.status, want)
		}
	}
	if !strings.Contains(m.status, "context is lost") {
		t.Errorf("the question does not say what is at stake: %q", m.status)
	}
}

func TestConfirmingEndsTheSession(t *testing.T) {
	m, p, b := endableDeck(t)
	p.update(&m, runeKey("x"))

	cmd := p.update(&m, runeKey("y"))
	if cmd == nil {
		t.Fatal("y scheduled nothing, so the session is never ended")
	}
	// Ending runs as a command, not inline: it is a subprocess and a frame must
	// not wait on one.
	msg := cmd()
	if len(b.ended) != 1 || b.ended[0] != "awp.awp.test.agent" {
		t.Fatalf("the backend was asked to end %v, want [awp.awp.test.agent]", b.ended)
	}

	// The outcome reloads the list, so the row goes without a manual refresh.
	if reload := p.update(&m, msg); reload == nil {
		t.Error("the list was not reloaded after a session ended")
	} else if next := reload(); next != nil {
		p.update(&m, next)
	}
	if n := len(p.list.Items()); n != 0 {
		t.Errorf("the ended session is still listed (%d rows)", n)
	}
	if !strings.Contains(m.status, "ended awp/test agent") {
		t.Errorf("the status does not report the outcome: %q", m.status)
	}
}

// The question names one row, so anything but a yes is a no — including the keys
// that would otherwise move the cursor, which would answer it about a different
// session than the one it asked about.
func TestAnythingButAYesCancelsTheEnd(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  tea.KeyPressMsg
	}{
		{"n", runeKey("n")},
		{"q", runeKey("q")},
		{"j", runeKey("j")},
		{"k", runeKey("k")},
		{"esc", tea.KeyPressMsg{Code: tea.KeyEscape}},
	} {
		m, p, b := endableDeck(t)
		p.update(&m, runeKey("x"))
		p.update(&m, tc.key)
		if len(b.ended) != 0 {
			t.Errorf("%s ended %v", tc.name, b.ended)
		}
		if p.pendingEnd != nil {
			t.Errorf("%s left the question hanging", tc.name)
		}
		if m.status != "" {
			t.Errorf("%s left %q in the status bar instead of clearing it", tc.name, m.status)
		}
		// And a cancel is not a close: the overlay is still where you were.
		if m.active != p {
			t.Errorf("%s closed the overlay as well as cancelling", tc.name)
		}
	}
}

// An exited session holds no agent, so there is nothing to lose. It still asks —
// the row goes either way — but saying the same thing about both would make the
// warning meaningless.
func TestEndingAnExitedSessionSaysThereIsNothingToLose(t *testing.T) {
	b := &sessionBackend{fakePanes: *allKinds(), sessions: []PaneSession{
		{Name: "awp.awp.test.agent", Label: "awp/test", Kind: "agent", Live: false, HasItem: true,
			Item: Item{ProjectName: "awp", WorkspaceName: "test"}, Started: time.Now()},
	}}
	m, p := openSessions(t, sessionDeck(t, b))
	p.update(&m, runeKey("x"))
	if !strings.Contains(m.status, "already exited") {
		t.Errorf("the question warns about context on a dead session: %q", m.status)
	}
	if strings.Contains(m.status, "context is lost") {
		t.Errorf("the question claims a dead session has context to lose: %q", m.status)
	}
}

// A session that outlived its workspace is exactly the one nothing else will
// ever clean up, so x has to reach it.
func TestASessionWithNoRowCanStillBeEnded(t *testing.T) {
	b := &sessionBackend{fakePanes: *allKinds(), sessions: []PaneSession{
		{Name: "awp.gone.ws.agent", Label: "gone/ws", Kind: "agent", Live: true, HasItem: false,
			Started: time.Now()},
	}}
	m, p := openSessions(t, sessionDeck(t, b))
	p.update(&m, runeKey("x"))
	cmd := p.update(&m, runeKey("y"))
	if cmd == nil {
		t.Fatal("a session with no workspace row could not be ended")
	}
	cmd()
	if len(b.ended) != 1 || b.ended[0] != "awp.gone.ws.agent" {
		t.Errorf("the backend was asked to end %v", b.ended)
	}
}

// A kill that failed must say so. The session is still there and the user's next
// move depends on knowing that.
func TestAFailedEndIsReported(t *testing.T) {
	m, p, b := endableDeck(t)
	b.endErr = errors.New("zmx is not answering")
	p.update(&m, runeKey("x"))
	msg := p.update(&m, runeKey("y"))()
	p.update(&m, msg)
	for _, want := range []string{"awp/test", "zmx is not answering"} {
		if !strings.Contains(m.status, want) {
			t.Errorf("the status %q does not mention %q", m.status, want)
		}
	}
}

// The footer has to name the key, or the only way to find it is reading the
// source.
func TestTheOverlayFooterOffersTheEndKey(t *testing.T) {
	m, p, _ := endableDeck(t)
	if got := ansi.Strip(p.footerHelp()); !strings.Contains(got, "x") || !strings.Contains(got, "end") {
		t.Errorf("the footer does not offer x: %q", got)
	}
	// While a question is pending the status bar is what is asking, and offering
	// the ordinary keys would suggest they still work.
	p.update(&m, runeKey("x"))
	if got := p.footerHelp(); got != "" {
		t.Errorf("the footer still shows keys while the question is up: %q", got)
	}
}

func TestAgeReadsAsADuration(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{3 * time.Second, "3s"},
		{4 * time.Minute, "4m"},
		{2 * time.Hour, "2h"},
		{72 * time.Hour, "3d"},
	} {
		if got := age(now.Add(-tc.d), now); got != tc.want {
			t.Errorf("age(-%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
	if got := age(time.Time{}, now); got != "?" {
		t.Errorf("an unknown start time rendered %q", got)
	}
}
