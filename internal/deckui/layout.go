package deckui

// The deck's frame budget, in one place.
//
// Every body-area panel and the footer are inset by the same amounts, and
// several renderers have to subtract that inset from m.height to size a
// viewport or a list. Those subtractions used to be written out as literals at
// each site — `m.height - 5` in three pickers, `const chrome = 2 + 2 + 3` in
// deckBodyCapacity, `diffModalChrome = 6` — so changing the inset meant finding
// all of them, and missing one produces a band of dead rows rather than an
// error. diffModalChrome's own comment records the last time that happened
// ("This was 8, two rows too many, which is what put a visible gap under the
// diff"). Derive from these instead.
const (
	// The deck spends nothing on its own inset, in either direction.
	//
	// panelPadX was 1, to keep content off the terminal's left edge. But the
	// deck is the outermost program in its terminal — there is no surrounding
	// surface for a margin to separate it from, so the column bought a gap
	// against the edge of the world. Rows still read as rows because their own
	// content indents them: the selection's `┃ ` prefix and the status dot
	// occupy the columns the pad was holding, and they belong to the row
	// rather than to the frame.
	//
	// panelPadY was always 0. A blank row above the title is a workspace row
	// the list does not get. The one gap the deck keeps is inside the panel,
	// under the title row, where it separates the attention badge from the
	// first project header; the badge sits on the rows' own text column and
	// without a gap it reads as a row.
	//
	// Kept as named constants rather than deleted. Every panel and the footer
	// derive from them, and several renderers subtract them from m.width and
	// m.height — so reintroducing an inset is one edit here rather than a hunt
	// through the call sites this file exists to have replaced.
	panelPadX = 0
	panelPadY = 0

	// panelRows is what a body panel spends on its own vertical padding.
	panelRows = 2 * panelPadY
	// panelCols is what it spends horizontally.
	panelCols = 2 * panelPadX
	// footerRows is the whole footer block: the status bar plus its padding,
	// which matches the panels'.
	footerRows = 1 + panelRows
	// deckHeaderRows is the row list panel's own header: the blank under the
	// deck's top row, and nothing else.
	//
	// It was 2 — the title row and the blank — until the title row became the
	// deck's own top row, rendered above this panel and above a pane alike so it
	// sits in the same cells on both. The row it used to cost here is now
	// topRowRows, one level up, so the frame's height is unchanged and
	// deckFrameRows is what says so.
	deckHeaderRows = 1
	// deckFrameRows is every row the deck spends on itself around the row list:
	// its top row, the blank under it, the panel's padding and the footer.
	//
	// One name because the number is asked for twice — once to size the body and
	// once by the test that pins it — and a body sized against a different total
	// than the renderer uses leaves a band of dead rows above the footer.
	deckFrameRows = topRowRows + deckHeaderRows + panelRows + footerRows
	// deckTopRowIndex is which line of the deck's frame the top row is: the
	// first. Tests reach for it by name so a change of mind about the frame moves
	// them with it instead of failing them.
	deckTopRowIndex = 0
)
