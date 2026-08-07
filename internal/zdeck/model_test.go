package zdeck

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/andrewcohen/awp/internal/vterm"
	"github.com/andrewcohen/awp/internal/zmx"
)

func quietZmx() zmx.Client {
	return zmx.New(func(context.Context, string, string, ...string) (string, error) {
		return "", nil
	})
}

func testItems() []Item {
	return []Item{
		{ProjectName: "awp", WorkspaceName: "portal", Path: "/tmp", RepoRoot: "/tmp"},
		{ProjectName: "awp", WorkspaceName: "review-ux", Path: "/tmp", RepoRoot: "/tmp"},
		{ProjectName: "etl", WorkspaceName: "backfill", Path: "/tmp", RepoRoot: "/tmp"},
	}
}

func sized(t *testing.T, w, h int) Model {
	t.Helper()
	m := New(testItems(), quietZmx())
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return next.(Model)
}

// harmless is a pane kind that runs something quiet and long-lived enough to
// still be there when the assertions run.
func harmless(lifetime Lifetime) Kind {
	return Kind{
		Key: "z", Label: "probe", Lifetime: lifetime,
		argv: func(Item) []string { return []string{"sh", "-c", "echo PANE-UP; sleep 30"} },
	}
}

func openProbe(t *testing.T, m Model, lifetime Lifetime) Model {
	t.Helper()
	next, cmd := m.openPane(harmless(lifetime))
	out := next.(Model)
	if out.pane == nil {
		t.Fatalf("the pane did not open; status was %q", out.status)
	}
	if cmd == nil {
		t.Error("opening a pane scheduled no work, so it will never repaint")
	}
	t.Cleanup(func() { out.closePane() })
	return out
}

func press(s string) tea.KeyPressMsg {
	if r := []rune(s); len(r) == 1 {
		return tea.KeyPressMsg{Code: r[0], Text: s}
	}
	return tea.KeyPressMsg{Text: s}
}

func leavePress() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: '\\', Mod: tea.ModCtrl}
}

// The lifetimes are the design, not an implementation detail: they decide
// whether anything has to exist behind the pane at all.
func TestKindLifetimesAreTheOnesAgreed(t *testing.T) {
	want := map[string]Lifetime{
		"a": LongLived, // agent — worth keeping alive between glances
		"e": LongLived, // editor — keeps its buffers
		"s": Ephemeral, // shell — next one can start fresh
		"v": Ephemeral, // jjui
		"c": Native,    // the diff viewer is not a process
	}
	if len(Kinds) != len(want) {
		t.Fatalf("%d kinds defined, %d expected", len(Kinds), len(want))
	}
	for _, k := range Kinds {
		w, ok := want[k.Key]
		if !ok {
			t.Errorf("unexpected kind %q", k.Key)
			continue
		}
		if k.Lifetime != w {
			t.Errorf("%s (%s) has lifetime %v, want %v", k.Key, k.Label, k.Lifetime, w)
		}
		if (k.Lifetime == Native) != (k.argv == nil) {
			t.Errorf("%s: a native kind must have no command and a process kind must have one", k.Key)
		}
	}
}

// An ephemeral pane needs no session behind it — that is the whole reason to
// call it ephemeral, and it means zmx is never consulted.
func TestAnEphemeralPaneRunsWithoutASession(t *testing.T) {
	var calls int
	client := zmx.New(func(context.Context, string, string, ...string) (string, error) {
		calls++
		return "", nil
	})
	m := New(testItems(), client)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = next.(Model)

	out, _ := m.openPane(harmless(Ephemeral))
	got := out.(Model)
	t.Cleanup(func() { got.closePane() })
	if got.pane == nil {
		t.Fatalf("no pane; status %q", got.status)
	}
	if got.pane.session != "" {
		t.Errorf("an ephemeral pane recorded session %q", got.pane.session)
	}
	if calls != 0 {
		t.Errorf("zmx was called %d times for an ephemeral pane", calls)
	}
}

// A long-lived pane goes through zmx, and the session it names is the one
// SessionName would produce, so a second open finds it rather than making
// another.
func TestALongLivedPaneEnsuresASession(t *testing.T) {
	var ran []string
	client := zmx.New(func(_ context.Context, _, name string, args ...string) (string, error) {
		ran = append(ran, name+" "+strings.Join(args, " "))
		return "", nil
	})
	m := New(testItems(), client)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = next.(Model)

	out, _ := m.openPane(harmless(LongLived))
	got := out.(Model)
	t.Cleanup(func() { got.closePane() })
	if got.pane == nil {
		t.Fatalf("no pane; status %q", got.status)
	}
	want := zmx.SessionName("awp", "portal", "probe")
	if got.pane.session != want {
		t.Errorf("session is %q, want %q", got.pane.session, want)
	}
	joined := strings.Join(ran, " | ")
	if !strings.Contains(joined, "zmx run "+want+" -d") {
		t.Errorf("no detached run for the session; calls were %v", ran)
	}
}

// Opening focuses the pane, one key gives the keyboard back, and everything
// else belongs to the program — esc, q and ctrl+c all mean something to an
// agent, so none of them may leak out.
func TestFocusMovesOnlyOnTheLeaveKey(t *testing.T) {
	m := openProbe(t, sized(t, 120, 40), Ephemeral)
	if m.focus != focusPane {
		t.Fatal("opening a pane did not focus it")
	}

	for _, k := range []tea.KeyPressMsg{
		{Code: tea.KeyEsc}, press("q"), {Code: 'c', Mod: tea.ModCtrl}, press("a"), press("x"),
	} {
		next, _ := m.Update(k)
		m = next.(Model)
		if m.focus != focusPane {
			t.Fatalf("%q took focus out of the pane; only %s may", k.String(), leaveKey)
		}
		if m.pane == nil {
			t.Fatalf("%q closed the pane", k.String())
		}
	}

	next, _ := m.Update(leavePress())
	m = next.(Model)
	if m.focus != focusList {
		t.Errorf("%s did not return the keyboard to the list", leaveKey)
	}
	if m.pane == nil {
		t.Error("leaving the pane closed it; it should stay open and visible")
	}
}

// Closing a long-lived pane says the session is still running, because that is
// the difference the user cannot otherwise see.
func TestClosingSaysWhetherAnythingSurvives(t *testing.T) {
	for _, tc := range []struct {
		lifetime Lifetime
		want     string
	}{
		{LongLived, "session is still running"},
		{Ephemeral, "probe closed"},
	} {
		m := openProbe(t, sized(t, 120, 40), tc.lifetime)
		next, _ := m.Update(leavePress())
		next, _ = next.(Model).Update(press("x"))
		got := next.(Model)
		if got.pane != nil {
			t.Errorf("%v: x did not close the pane", tc.lifetime)
		}
		if !strings.Contains(got.status, tc.want) {
			t.Errorf("%v: status is %q, want it to mention %q", tc.lifetime, got.status, tc.want)
		}
	}
}

// A pane that has been replaced can still have a frame in flight; painting it
// would put the previous program's screen inside the current one.
func TestAStalePanesFrameIsIgnored(t *testing.T) {
	m := openProbe(t, sized(t, 120, 40), Ephemeral)
	gen := m.pane.term.Gen()

	next, cmd := m.Update(vtermOutput(gen - 1))
	if cmd != nil {
		t.Error("a frame from an older pane was accepted")
	}
	next, cmd = next.(Model).Update(vtermOutput(gen))
	if cmd == nil {
		t.Error("the live pane's frame did not re-arm the wait, so it repaints once and stops")
	}
	if next.(Model).pane == nil {
		t.Error("the pane vanished")
	}
}

// The process the pane hosts really is running and really is rendered.
func TestThePaneShowsItsProcess(t *testing.T) {
	m := openProbe(t, sized(t, 120, 40), Ephemeral)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(m.pane.term.View(), "PANE-UP") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("waited 5s for the process output; screen was:\n%s", m.pane.term.View())
}

func TestOpeningExplainsWhyItCannot(t *testing.T) {
	m := sized(t, 120, 40)
	m.items = []Item{{ProjectName: "p", WorkspaceName: "w", Virtual: true}}
	out, _ := m.openPane(harmless(Ephemeral))
	if got := out.(Model); got.pane != nil || !strings.Contains(got.status, "no workspace yet") {
		t.Errorf("a virtual row opened a pane; status %q", got.status)
	}

	empty := New(nil, quietZmx())
	empty.width, empty.height = 120, 40
	out, _ = empty.openPane(harmless(Ephemeral))
	if got := out.(Model); got.pane != nil || !strings.Contains(got.status, "select a workspace") {
		t.Errorf("an empty list opened a pane; status %q", got.status)
	}
}

// The frame must be exactly the terminal it was given, at every size — this is
// the arithmetic that a border change silently broke elsewhere in the repo.
func TestTheFrameIsExactlyTheTerminal(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 40}, {200, 60}, {60, 20}, {240, 80}} {
		w, h := size[0], size[1]
		m := sized(t, w, h)
		lines := strings.Split(m.render(), "\n")
		if len(lines) != h {
			t.Errorf("%dx%d: rendered %d rows, want %d", w, h, len(lines), h)
			continue
		}
		for i, l := range lines {
			if got := lipgloss.Width(l); got != w {
				t.Errorf("%dx%d: row %d is %d wide, want %d", w, h, i, got, w)
				break
			}
		}
	}
}

// The pane's terminal has to fill the space beside the list exactly, or the
// program lays out for one width while the frame reserves another.
func TestThePaneGetsTheSpaceTheFrameLeaves(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 40}, {200, 60}} {
		w, h := size[0], size[1]
		m := sized(t, w, h)
		pw, ph := m.paneDims()
		if pw+listCols(w)+dividerCols != w {
			t.Errorf("%dx%d: list %d + divider %d + pane %d != %d",
				w, h, listCols(w), dividerCols, pw, w)
		}
		if ph+chromeRows != h {
			t.Errorf("%dx%d: pane height %d + chrome %d != %d", w, h, ph, chromeRows, h)
		}
	}
}

// Both status lines are load-carrying, so they must say something at all times.
func TestTheTopAndBottomLinesAlwaysSaySomething(t *testing.T) {
	m := sized(t, 120, 40)
	top, bottom := stripANSI(m.renderTop()), stripANSI(m.renderBottom())
	if !strings.Contains(top, "awp/portal") {
		t.Errorf("the top line does not name the selection: %q", top)
	}
	if !strings.Contains(top, "no pane") {
		t.Errorf("the top line does not say nothing is open: %q", top)
	}
	for _, k := range Kinds {
		if !strings.Contains(bottom, k.Label) {
			t.Errorf("the bottom line omits %q: %q", k.Label, bottom)
		}
	}

	m = openProbe(t, m, LongLived)
	top = stripANSI(m.renderTop())
	if !strings.Contains(top, "session") {
		t.Errorf("the top line does not say a long-lived pane has a session: %q", top)
	}
	bottom = stripANSI(m.renderBottom())
	if !strings.Contains(bottom, leaveKey) {
		t.Errorf("a focused pane's bottom line does not name the way out: %q", bottom)
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// vtermOutput builds the message a live pane sends when its screen changes.
func vtermOutput(gen int) tea.Msg { return vterm.OutputMsg{Gen: gen} }
