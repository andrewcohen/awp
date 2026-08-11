package deckui

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/andrewcohen/awp/internal/vterm"
)

// fakePanes hosts a harmless process and records what it was asked for.
type fakePanes struct {
	kinds   []string
	handles map[string]bool
	// script is what the hosted process runs, so a test can decide what the
	// pane's program asks its terminal for. Empty means a quiet sleeper that
	// asks for nothing.
	script   string
	opened   string
	restored int
	err      error
}

func (f *fakePanes) Describes(kind string) bool { return f.handles[kind] }

func (f *fakePanes) Open(_ Item, kind string, _, _ int) (*exec.Cmd, func(), error) {
	f.kinds = append(f.kinds, kind)
	if f.err != nil {
		return nil, nil, f.err
	}
	f.opened = kind
	script := f.script
	if script == "" {
		script = "echo PANE-UP; sleep 30"
	}
	return exec.Command("sh", "-c", script), func() { f.restored++ }, nil
}

// openedPane opens the agent pane and hands back the model and the popover.
func openedPane(t *testing.T, backend *fakePanes) (Model, *panePopover) {
	t.Helper()
	m := paneModel(t, backend)
	next, _ := m.trigger(ActionOpenWindow, "agent")
	m = next.(Model)
	p, ok := m.active.(*panePopover)
	if !ok {
		t.Fatalf("no pane opened; status %q", m.status)
	}
	t.Cleanup(func() { p.close(&m) })
	return m, p
}

// eventually polls until cond holds. The hosted process runs for real, so
// anything it tells its terminal arrives on its own schedule.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("waited 2s for %s", what)
}

func paneModel(t *testing.T, backend PaneBackend) Model {
	t.Helper()
	m := New([]Item{{ProjectName: "proj", WorkspaceName: "ws", Path: "/tmp", RepoRoot: "/tmp"}}, func(ActionRequest) error { return nil }).
		WithPaneBackend(backend)
	m.width, m.height = 120, 40
	return m
}

func allKinds() *fakePanes {
	return &fakePanes{handles: map[string]bool{"agent": true, "editor": true, "vcs": true, "": true}}
}

// The whole point of the backend: the deck's UI is unchanged and only where
// the process lives differs. With one wired in, a window key hosts a pane
// instead of reaching the tmux handler.
func TestAWindowKeyHostsAPaneWhenABackendIsWired(t *testing.T) {
	var handlerCalls []Action
	backend := allKinds()
	m := New([]Item{{ProjectName: "proj", WorkspaceName: "ws", Path: "/tmp", RepoRoot: "/tmp"}},
		func(r ActionRequest) error { handlerCalls = append(handlerCalls, r.Action); return nil }).
		WithPaneBackend(backend)
	m.width, m.height = 120, 40

	next, cmd := m.trigger(ActionOpenWindow, "agent")
	got := next.(Model)
	p, ok := got.active.(*panePopover)
	if !ok {
		t.Fatalf("no pane opened; status %q", got.status)
	}
	t.Cleanup(func() { p.close(&got) })
	if cmd == nil {
		t.Error("opening a pane scheduled no work, so it will never repaint")
	}
	if len(handlerCalls) != 0 {
		t.Errorf("the tmux handler was still called: %v", handlerCalls)
	}
	if backend.opened != "agent" {
		t.Errorf("the backend was asked for %q, want agent", backend.opened)
	}
}

// TestClosingAPaneRunsTheBackendsCloseHook. The func the backend hands back with
// the command is its only notice that the pane is over, and it carries work now
// rather than only bookkeeping — under zdeck the agent pane's hook is what marks
// the workspace read on the way out, which is the only thing that clears a badge
// the agent raised while you were watching it. Leaving it uncalled loses that
// silently: the pane still closes and the deck still refreshes.
func TestClosingAPaneRunsTheBackendsCloseHook(t *testing.T) {
	backend := allKinds()
	m, p := openedPane(t, backend)
	if backend.restored != 0 {
		t.Fatalf("the close hook ran %d times while the pane was still open", backend.restored)
	}
	p.close(&m)
	if backend.restored != 1 {
		t.Errorf("the close hook ran %d times on close, want once", backend.restored)
	}
}

// Without a backend the deck is exactly what it was: the key reaches the
// handler and opens a tmux window.
func TestWithoutABackendTheDeckIsUnchanged(t *testing.T) {
	var got []ActionRequest
	m := New([]Item{{ProjectName: "proj", WorkspaceName: "ws", Path: "/tmp", RepoRoot: "/tmp"}},
		func(r ActionRequest) error { got = append(got, r); return nil })
	m.width, m.height = 120, 40

	next, cmd := m.trigger(ActionOpenWindow, "agent")
	if next.(Model).active != nil {
		t.Error("a pane opened with no backend wired")
	}
	// The handler runs inside the returned command, not during trigger.
	runCmd(cmd)
	if len(got) != 1 || got[0].Action != ActionOpenWindow || got[0].Arg != "agent" {
		t.Errorf("the handler saw %+v, want one ActionOpenWindow/agent", got)
	}
}

// A backend that declines a kind must fall through to tmux, which is what
// keeps the review and PR-description windows working.
func TestAnUnhandledKindFallsThroughToTmux(t *testing.T) {
	var got []string
	backend := &fakePanes{handles: map[string]bool{"agent": true}}
	m := New([]Item{{ProjectName: "proj", WorkspaceName: "ws", Path: "/tmp", RepoRoot: "/tmp"}},
		func(r ActionRequest) error { got = append(got, r.Arg); return nil }).
		WithPaneBackend(backend)
	m.width, m.height = 120, 40

	next, cmd := m.trigger(ActionOpenWindow, ReviewStackArg)
	if next.(Model).active != nil {
		t.Error("a declined kind opened a pane anyway")
	}
	runCmd(cmd)
	if len(got) != 1 || got[0] != ReviewStackArg {
		t.Errorf("the handler saw %v, want the review window to fall through", got)
	}
}

// Every key belongs to the program except the one that leaves — esc, q and
// ctrl+c all mean something to an agent.
func TestOnlyTheLeaveKeyIsInterceptedInAPane(t *testing.T) {
	m := paneModel(t, allKinds())
	next, _ := m.trigger(ActionOpenWindow, "agent")
	m = next.(Model)
	p := m.active.(*panePopover)
	t.Cleanup(func() { p.close(&m) })

	for _, k := range []tea.KeyPressMsg{
		{Code: tea.KeyEsc}, {Code: 'q', Text: "q"}, {Code: 'c', Mod: tea.ModCtrl}, {Code: '?', Text: "?"},
	} {
		p.update(&m, k)
		if m.active == nil {
			t.Fatalf("%q closed the pane; only %s may", k.String(), paneLeaveKey)
		}
	}

	p.update(&m, tea.KeyPressMsg{Code: '\\', Mod: tea.ModCtrl})
	if m.active != nil {
		t.Errorf("%s did not close the pane", paneLeaveKey)
	}
}

// A pane that has closed can still have a frame in flight; painting it would
// put the previous process's screen inside the current one.
func TestAStalePanesFrameIsIgnored(t *testing.T) {
	m := paneModel(t, allKinds())
	next, _ := m.trigger(ActionOpenWindow, "agent")
	m = next.(Model)
	p := m.active.(*panePopover)
	t.Cleanup(func() { p.close(&m) })

	if cmd := p.update(&m, vterm.OutputMsg{Gen: p.term.Gen() - 1}); cmd != nil {
		t.Error("a frame from an older pane was accepted")
	}
	if cmd := p.update(&m, vterm.OutputMsg{Gen: p.term.Gen()}); cmd == nil {
		t.Error("the live pane's frame did not re-arm the wait, so it repaints once and stops")
	}
	p.update(&m, vterm.ExitMsg{Gen: p.term.Gen() - 1})
	if m.active == nil {
		t.Error("an older pane's exit closed the current one")
	}
}

func TestAFailedOpenReportsWhyAndOpensNothing(t *testing.T) {
	backend := allKinds()
	backend.err = errors.New("zmx session is gone")
	m := paneModel(t, backend)

	next, _ := m.trigger(ActionOpenWindow, "agent")
	got := next.(Model)
	if got.active != nil {
		t.Fatal("a pane opened despite the backend refusing")
	}
	if !strings.Contains(got.status, "zmx session is gone") {
		t.Errorf("status is %q, want the backend's reason", got.status)
	}
}

// The popover has to fit the deck at every size it agrees to open at — the
// arithmetic a border change silently broke elsewhere in this repo.
func TestThePanePopoverFitsTheDeck(t *testing.T) {
	for w := 20; w <= 220; w++ {
		for h := 6; h <= 70; h++ {
			if !paneFits(w, h) {
				continue
			}
			boxW, boxH := paneBox(paneDims(w, h))
			if boxW > w {
				t.Fatalf("at %dx%d the pane box is %d wide, past the deck's %d", w, h, boxW, w)
			}
			if boxH > h {
				t.Fatalf("at %dx%d the pane box is %d tall, past the deck's %d", w, h, boxH, h)
			}
		}
	}
}

// Every cell of chrome is one the hosted program does not get, and a pane
// shows someone else's full-screen program rather than a fixed amount of awp's
// own text. This pins the cost so padding cannot creep back in.
func TestThePaneCostsOnlyABorderAndAHeader(t *testing.T) {
	if paneChromeW != borderCells {
		t.Errorf("a pane costs %d columns, want just the border (%d) — no horizontal padding",
			paneChromeW, borderCells)
	}
	if want := borderCells + 1; paneChromeH != want {
		t.Errorf("a pane costs %d rows, want the border plus one header row (%d)", paneChromeH, want)
	}

	// And the rendered box has to actually match those numbers, or the cursor
	// arithmetic in screenCursor is placing against a box that isn't there.
	m, p := openedPane(t, allKinds())
	eventually(t, "the pane to paint", func() bool { return strings.Contains(p.term.View(), "PANE-UP") })

	box := p.renderPopover(&m)
	tw, th := paneDims(m.width, m.height)
	wantW, wantH := paneBox(tw, th)
	if got := lipgloss.Width(box); got != wantW {
		t.Errorf("the box rendered %d columns wide, want %d", got, wantW)
	}
	if got := lipgloss.Height(box); got != wantH {
		t.Errorf("the box rendered %d rows tall, want %d", got, wantH)
	}
}

// Nothing sits outside the pane's border.
//
// The border is the pane's whole frame, so a column of canvas around it is a
// column the hosted program does not get, spent on a second edge next to the
// one already there. This is asserted through the deck's own render rather
// than through renderPopover, because the padding it would catch is added by
// the lipgloss.Place that centres the box — not by the box.
func TestNothingSitsOutsideThePanesBorder(t *testing.T) {
	m, p := openedPane(t, allKinds())
	eventually(t, "the pane to paint", func() bool { return strings.Contains(p.term.View(), "PANE-UP") })

	lines := strings.Split(m.render(), "\n")
	if len(lines) != m.height {
		t.Fatalf("the frame is %d rows, the terminal is %d", len(lines), m.height)
	}
	top, bottom := ansi.Strip(lines[0]), ansi.Strip(lines[len(lines)-1])
	for _, tc := range []struct {
		what string
		line string
	}{{"top", top}, {"bottom", bottom}} {
		if lipgloss.Width(tc.line) != m.width {
			t.Errorf("the %s border row is %d columns, the terminal is %d",
				tc.what, lipgloss.Width(tc.line), m.width)
		}
		if strings.HasPrefix(tc.line, " ") || strings.HasSuffix(tc.line, " ") {
			t.Errorf("the %s border row has canvas beside it: %q", tc.what, tc.line)
		}
	}
}

// The hint shares the header row with the label rather than costing two more
// rows of its own — but it is what tells you how to get out, so it survives
// even when the label has to go.
func TestTheHeaderKeepsTheLeaveKeyEvenWhenNarrow(t *testing.T) {
	m, p := openedPane(t, allKinds())
	for _, w := range []int{200, 80, 40, 24, 12, 4} {
		header := p.header(&m, w)
		if strings.Contains(header, "\n") {
			t.Errorf("at %d columns the header wrapped onto a second row: %q", w, header)
		}
		if !strings.Contains(header, paneLeaveKey) {
			t.Errorf("at %d columns the header dropped the leave key: %q", w, header)
		}
	}
}

func TestATinyDeckRefusesAPane(t *testing.T) {
	m := paneModel(t, allKinds())
	// Chrome is the border plus one header row, so the smallest workable deck
	// is paneMin plus that. 20x6 is under it on both axes.
	m.width, m.height = 20, 6
	next, _ := m.trigger(ActionOpenWindow, "agent")
	got := next.(Model)
	if got.active != nil {
		t.Fatal("a 20x6 deck opened a pane it cannot draw")
	}
	if !strings.Contains(got.status, "too small") {
		t.Errorf("status is %q, want it to say the terminal is too small", got.status)
	}
}

// runCmd drains a tea.Cmd, including the batch trigger returns, so the
// handler dispatch inside it actually runs.
func runCmd(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			runCmd(c)
		}
	}
}

// Summoning has nowhere to hand off to when awp is hosting the panes, and
// `tmux switch-client` from outside tmux exits 0 having done nothing. So enter
// brings the workspace's agent into the deck instead of silently no-opping.
func TestSummonOpensTheAgentPaneWhenABackendIsWired(t *testing.T) {
	backend := allKinds()
	m := paneModel(t, backend)

	next, _ := m.trigger(ActionSummon, "")
	got := next.(Model)
	p, ok := got.active.(*panePopover)
	if !ok {
		t.Fatalf("enter opened no pane; status %q", got.status)
	}
	t.Cleanup(func() { p.close(&got) })
	if backend.opened != "agent" {
		t.Errorf("enter asked the backend for %q, want agent", backend.opened)
	}
}

// And without a backend, enter is the tmux handoff it has always been.
func TestSummonIsUnchangedWithoutABackend(t *testing.T) {
	var got []Action
	m := New([]Item{{ProjectName: "proj", WorkspaceName: "ws", Path: "/tmp", RepoRoot: "/tmp"}},
		func(r ActionRequest) error { got = append(got, r.Action); return nil })
	m.width, m.height = 120, 40

	next, cmd := m.trigger(ActionSummon, "")
	if next.(Model).active != nil {
		t.Error("a pane opened with no backend wired")
	}
	runCmd(cmd)
	if len(got) != 1 || got[0] != ActionSummon {
		t.Errorf("the handler saw %v, want one ActionSummon", got)
	}
}

// A hosted pane is the only thing in the deck that wants the terminal's mouse
// and cursor, and it must be the only thing that asks for them: requesting
// mouse tracking all the time costs drag-to-select on every other screen.
func TestTheDeckAsksForNothingWithNoPaneOpen(t *testing.T) {
	m := paneModel(t, allKinds())

	plain := m.View()
	if plain.MouseMode != tea.MouseModeNone {
		t.Errorf("the deck asked for mouse mode %v with no pane open", plain.MouseMode)
	}
	if plain.Cursor != nil {
		t.Error("the deck placed a cursor with no pane open")
	}
}

// A pane whose program has enabled mouse reporting has to have the events
// forwarded to it. Without that the outer terminal, in alt-screen with no
// tracking asked for, turns the wheel into arrow keys and scrolling types at
// the agent.
func TestAPaneAsksForTheMouseWhenItsProgramDoes(t *testing.T) {
	backend := allKinds()
	backend.script = `printf '\033[?1000h'; sleep 30`
	m, p := openedPane(t, backend)

	eventually(t, "the program to enable mouse reporting", p.term.WantsMouse)
	if got := m.View().MouseMode; got == tea.MouseModeNone {
		t.Error("a pane did not ask for mouse events, so the wheel arrives as arrow keys")
	}
}

// The other half, and the one that was wrong: taking the mouse for a program
// that never asked is a pure loss. The emulator drops the events, and the user
// loses the terminal's own drag-to-select in exchange for nothing.
func TestAPaneLeavesTheMouseAloneWhenItsProgramIgnoresIt(t *testing.T) {
	m, p := openedPane(t, allKinds())

	eventually(t, "the pane to paint", func() bool { return strings.Contains(p.term.View(), "PANE-UP") })
	if p.term.WantsMouse() {
		t.Fatal("a plain sleeper somehow enabled mouse reporting")
	}
	if got := m.View().MouseMode; got != tea.MouseModeNone {
		t.Errorf("the deck took the mouse (%v) for a program that does not want it, "+
			"which costs the terminal's native selection and gains nothing", got)
	}
}

// A pane's program owns whether there is a cursor at all.
func TestAPaneShowsACursorWhenItsProgramWantsOne(t *testing.T) {
	m, p := openedPane(t, allKinds())

	eventually(t, "the pane to paint", func() bool { return strings.Contains(p.term.View(), "PANE-UP") })
	if m.View().Cursor == nil {
		t.Error("a pane did not place a cursor, so the hosted program has none")
	}
}

// A full-screen program hides its cursor and then parks it wherever suits its
// own rendering. Drawing one anyway puts a blinking block at an arbitrary spot
// on its screen — which is what jjui looked like.
func TestNoCursorWhenTheProgramHidesIt(t *testing.T) {
	backend := allKinds()
	backend.script = `printf '\033[?25l'; sleep 30`
	m, p := openedPane(t, backend)

	eventually(t, "the program to hide its cursor", func() bool {
		_, _, visible := p.term.Cursor()
		return !visible
	})
	if _, _, ok := p.screenCursor(m.width, m.height); ok {
		t.Error("screenCursor placed a cursor the program had hidden")
	}
	if m.View().Cursor != nil {
		t.Error("the deck drew a cursor the program had hidden")
	}
}

// The cursor has to land inside the terminal region of the popover, at every
// size the pane agrees to open at — the same arithmetic the box itself uses.
func TestTheCursorLandsInsideTheTerminal(t *testing.T) {
	_, p := openedPane(t, allKinds())

	for _, size := range [][2]int{{80, 24}, {120, 40}, {200, 60}, {32, 16}} {
		w, h := size[0], size[1]
		if !paneFits(w, h) {
			continue
		}
		x, y, ok := p.screenCursor(w, h)
		if !ok {
			continue // the program's cursor is off its own screen
		}
		tw, th := paneDims(w, h)
		boxW, boxH := tw+4+borderCells, th+2+borderCells+4
		minX, minY := (w-boxW)/2+paneInsetX, (h-boxH)/2+paneInsetY
		if x < minX || x >= minX+tw {
			t.Errorf("%dx%d: cursor x=%d outside the terminal's columns [%d,%d)", w, h, x, minX, minX+tw)
		}
		if y < minY || y >= minY+th {
			t.Errorf("%dx%d: cursor y=%d outside the terminal's rows [%d,%d)", w, h, y, minY, minY+th)
		}
		if x >= w || y >= h {
			t.Errorf("%dx%d: cursor (%d,%d) is off screen", w, h, x, y)
		}
	}
}

// A deck too small for a pane must not place a cursor at a negative or
// off-screen position.
func TestNoCursorWhenThePaneDoesNotFit(t *testing.T) {
	_, p := openedPane(t, allKinds())

	if _, _, ok := p.screenCursor(10, 4); ok {
		t.Error("a deck too small for a pane still reported a cursor position")
	}
}

// The deck behind a pane has to keep polling. A pane is open for as long as
// you are working in it, and the agent inside reports status the whole time —
// so a deck that pauses its refresh while one is up shows you the state from
// before you opened it. Every other modal is a picker whose list a refresh
// would rebuild under the cursor; a pane owns a pty and nothing else.
func TestTheDeckKeepsRefreshingBehindAPane(t *testing.T) {
	m, _ := openedPane(t, allKinds())
	m.refresher = func() tea.Cmd { return nil }
	if !m.canBackgroundRefresh() {
		t.Error("the deck stops polling while a pane is open, so status goes stale behind it")
	}
}

// A picker is the case the pause exists for: refreshing rebuilds the very
// items its list is showing.
func TestTheDeckStillPausesBehindAPicker(t *testing.T) {
	m := paneModel(t, allKinds())
	m.refresher = func() tea.Cmd { return nil }
	m.active = &bookmarkPicker{}
	if m.canBackgroundRefresh() {
		t.Error("refreshing behind a picker moves the list under the cursor")
	}
}

// Leaving a pane must catch the deck up at once, not on the next 5s tick.
func TestClosingAPaneAsksForAFreshRead(t *testing.T) {
	m, p := openedPane(t, allKinds())
	reads := 0
	m.refresher = func() tea.Cmd { reads++; return func() tea.Msg { return nil } }

	if cmd := p.close(&m); cmd == nil {
		t.Error("closing a pane returned no command, so the row list stays as it was until the next poll")
	}
	if m.active != nil {
		t.Fatal("the pane did not close")
	}
	if reads != 1 {
		t.Errorf("closing a pane asked for %d fresh reads, want 1", reads)
	}
}
