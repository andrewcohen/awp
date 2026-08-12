package vterm

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Hosted is what a pane needs from a live terminal.
//
// It exists so which emulator interprets a pane's output can be a decision
// rather than an assumption. The surface is small and already in awp's own
// vocabulary — tea keys, tea mouse events, a string to place in a layout — so a
// second implementation shares no types with the first, which is the whole
// point: the two must be swappable without the pane knowing.
//
// *Term, the x/vt implementation, satisfies it.
type Hosted interface {
	// Gen is the generation this terminal was started with, echoed back on every
	// message so a stale terminal's frames can be discarded.
	Gen() int
	// Size is the current geometry.
	Size() (w, h int)

	// AwaitOutput blocks until the screen changes; AwaitExit until the hosted
	// process ends.
	AwaitOutput() tea.Cmd
	AwaitExit() tea.Cmd

	// View is the current screen as a block of lines, ANSI intact.
	View() string
	// LastLine is the lowest non-blank line, for reporting why a process died.
	LastLine() string

	// Send writes bytes as if typed. SendKey and SendText encode a key press and
	// printable text the way a real terminal would.
	Send(b []byte) error
	SendKey(k tea.KeyPressMsg)
	SendText(s string)
	SendMouse(msg tea.MouseMsg)

	// Cursor is where the hosted program put its cursor and whether it wants one
	// drawn; WantsMouse whether it asked for mouse reporting at all.
	Cursor() (x, y int, visible bool)
	WantsMouse() bool

	Resize(w, h int) error
	Close() error
}

// VTEnv names the environment variable that picks a pane's emulator.
//
// Deliberately not AWP_PANE_EXEC, which already means something else — hand the
// real terminal to the child and run no emulator at all. One variable answering
// both "which emulator" and "no emulator" would be two questions wearing one
// name.
const VTEnv = "AWP_PANE_VT"

// Emulator names are the accepted values of AWP_PANE_VT.
const (
	// EmulatorXVT is github.com/charmbracelet/x/vt, the default.
	EmulatorXVT = "x-vt"
	// EmulatorGhostty is libghostty-vt, available only in a binary built with
	// -tags ghosttyvt. See internal/vterm/ghostty.go.
	EmulatorGhostty = "ghostty"
)

// Open starts a hosted terminal, using whichever emulator AWP_PANE_VT names.
//
// The arguments are Start's, and an unset AWP_PANE_VT is exactly Start.
//
// A value naming an emulator this binary was not built with is an error rather
// than a fall back to the default. Falling back would be the worse failure: the
// point of choosing is to compare the two, and a comparison that silently ran
// the same emulator twice would report that they agree.
func Open(gen, w, h int, c *exec.Cmd, host HostColors) (Hosted, error) {
	switch name := strings.TrimSpace(os.Getenv(VTEnv)); name {
	case "", EmulatorXVT:
		return Start(gen, w, h, c, host)
	case EmulatorGhostty:
		return startGhostty(gen, w, h, c, host)
	default:
		return nil, fmt.Errorf("vterm: %s=%q is not an emulator this build knows (want %q or %q)",
			VTEnv, name, EmulatorXVT, EmulatorGhostty)
	}
}
