package deckui

import (
	"image/color"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// The deck has to ask, because nothing else can: a pane needs the answer the
// moment it opens, and a round trip to the terminal takes longer than that.
//
// Compared by function pointer rather than by name so this measures the cmd the
// deck actually returns. Forgetting to ask is silent — every pane simply goes
// back to telling programs it is white on black.
func TestTheDeckAsksItsTerminalWhatItLooksLike(t *testing.T) {
	m := paneModel(t, allKinds())
	batch, ok := m.Init()().(tea.BatchMsg)
	if !ok {
		t.Fatal("Init no longer returns a batch; this guard is measuring nothing")
	}

	want := map[uintptr]string{
		reflect.ValueOf(tea.RequestForegroundColor).Pointer(): "foreground",
		reflect.ValueOf(tea.RequestBackgroundColor).Pointer(): "background",
		reflect.ValueOf(tea.RequestCursorColor).Pointer():     "cursor",
	}
	for _, cmd := range batch {
		delete(want, reflect.ValueOf(cmd).Pointer())
	}
	for _, which := range want {
		t.Errorf("Init never asks the terminal for its %s colour, so a pane answers that query with x/vt's default", which)
	}
}

func TestTheDeckRecordsWhatItsTerminalAnswered(t *testing.T) {
	fg := color.RGBA{R: 0xca, G: 0xd3, B: 0xf5, A: 0xff}
	bg := color.RGBA{R: 0x24, G: 0x27, B: 0x3a, A: 0xff}
	cur := color.RGBA{R: 0xf4, G: 0xdb, B: 0xd6, A: 0xff}

	m := paneModel(t, allKinds())
	for _, msg := range []tea.Msg{
		tea.ForegroundColorMsg{Color: fg},
		tea.BackgroundColorMsg{Color: bg},
		tea.CursorColorMsg{Color: cur},
	} {
		next, _ := m.Update(msg)
		m = next.(Model)
	}

	if m.hostColors.Fg != color.Color(fg) {
		t.Errorf("the foreground is %v, want %v", m.hostColors.Fg, fg)
	}
	if m.hostColors.Bg != color.Color(bg) {
		t.Errorf("the background is %v, want %v", m.hostColors.Bg, bg)
	}
	if m.hostColors.Cursor != color.Color(cur) {
		t.Errorf("the cursor colour is %v, want %v", m.hostColors.Cursor, cur)
	}
}

// End to end: a program inside a pane asks what its background is and gets the
// deck's real one. Read back through the pane's own screen via `cat -v`, which is
// the only place the emulator's reply is observable.
func TestAHostedProgramIsToldTheDecksRealBackground(t *testing.T) {
	bg := color.RGBA{R: 0x24, G: 0x27, B: 0x3a, A: 0xff}

	backend := allKinds()
	backend.script = `printf '\033]11;?\007'; exec cat -v`
	m := paneModel(t, backend)
	next, _ := m.Update(tea.BackgroundColorMsg{Color: bg})
	m = next.(Model)

	opened, _ := m.trigger(ActionOpenWindow, "agent")
	m = opened.(Model)
	p, ok := m.active.(*panePopover)
	if !ok {
		t.Fatalf("no pane opened; status %q", m.status)
	}
	t.Cleanup(func() { p.close(&m) })

	want := ansi.XRGBColor{Color: bg}.String()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(ansi.Strip(p.term.View()), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("the pane never reported %s as its background; its screen says:\n%s", want, ansi.Strip(p.term.View()))
}
