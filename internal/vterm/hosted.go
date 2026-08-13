package vterm

import (
	"os/exec"

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
// ghosttyTerm, the libghostty-vt implementation, satisfies it. It was written
// against this interface as the second of two, which is what kept the pane from
// knowing which one it had; the first has since been deleted, and the interface
// stays because a pane asking a terminal for a screen is the right seam whether
// or not there is a choice to make behind it.
type Hosted interface {
	// Emulator names which implementation this is, one of the Emulator constants.
	//
	// Asked of the terminal rather than read back out of the environment, because
	// what a surface wants to report is what is running — and the variable is read
	// once, at Open, while a frame is drawn thousands of times after.
	Emulator() string

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
	//
	// x is a column of the string View returns, which is not always the emulator's
	// cell column: a grapheme's cell footprint and its rendered width can differ,
	// and the caller places the cursor against the rendered string. Whoever knows
	// the cell widths owns the translation — see ghosttyTerm.Cursor.
	Cursor() (x, y int, visible bool)
	WantsMouse() bool

	// CursorShape is what the program asked its cursor to look like — DECSCUSR,
	// which is how an editor says which mode it is in.
	//
	// Separate from Cursor because it answers a different question and is allowed
	// to be cheaper: where the cursor is changes constantly, what it looks like
	// changes when you press `i`. An emulator that cannot report a shape returns
	// the block, which is the terminal default and what a program that never asked
	// gets anyway.
	//
	// blink is carried alongside because DECSCUSR encodes the two in one
	// parameter: 5 is a blinking bar and 6 is a steady one, so reading the shape
	// without the blink is reading half of what the program said.
	CursorShape() (shape tea.CursorShape, blink bool)

	// SelectionText is the text of the cells between two points of the screen,
	// inclusive, as it should land on a clipboard: plain, soft-wrapped lines
	// rejoined, trailing blanks dropped. Endpoints in either order describe the
	// same range.
	//
	// The columns are the ones View renders and Cursor answers in, not the
	// emulator's cells — see Cursor.
	//
	// Here rather than in the caller because the caller has a string and no cells,
	// and the difference matters: what a range of cells says is a question about
	// the grid, and the string has already lost the soft wraps and the padding that
	// answering it needs. Which cells are selected is the caller's own business —
	// it owns the pointer and knows where its panes are, which is the whole reason
	// the host terminal cannot do this for us.
	SelectionText(x0, y0, x1, y1 int) string

	Resize(w, h int) error
	Close() error
}

// EmulatorGhostty is libghostty-vt, the emulator behind every pane.
//
// It is available only in a binary built with -tags ghosttyvt, because it is cgo
// against an archive built by Zig. See internal/vterm/ghostty.go for how, and
// ghostty_off.go for what a build without it does instead.
const EmulatorGhostty = "ghostty"

// Open starts a hosted terminal.
//
// There is one emulator, and which one is a property of the build rather than of
// the environment. AWP_PANE_VT used to choose between libghostty-vt and x/vt while
// the two were being compared; a variable whose every accepted value names the
// same emulator is a question the reader has to answer and cannot get wrong,
// which is worse than no question. It went with x/vt.
//
// A build without the tag refuses here rather than falling back, because there is
// nothing left to fall back to — see ghostty_off.go, which is what says so and
// says how to get one.
func Open(gen, w, h int, c *exec.Cmd, host HostColors) (Hosted, error) {
	return startGhostty(gen, w, h, c, host)
}
