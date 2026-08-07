package deckui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// sessionBackend is a pane backend that also holds sessions.
type sessionBackend struct {
	fakePanes
	sessions []PaneSession
	err      error
	sawItems []Item
}

func (b *sessionBackend) Sessions(items []Item) ([]PaneSession, error) {
	b.sawItems = items
	return b.sessions, b.err
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

	left, _ := p.view(&m)
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
	left, _ := p.view(&m)
	if out := ansi.Strip(left); !strings.Contains(out, "zmx is not on PATH") {
		t.Errorf("the error is not on screen:\n%s", out)
	}
}

func TestAnEmptySessionListSaysSo(t *testing.T) {
	b := &sessionBackend{fakePanes: *allKinds()}
	m, p := openSessions(t, sessionDeck(t, b))
	left, _ := p.view(&m)
	if out := ansi.Strip(left); !strings.Contains(out, "No sessions") {
		t.Errorf("an empty list renders nothing useful:\n%s", out)
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
