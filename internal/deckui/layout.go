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
	// panelPadX is the horizontal inset of every body-area panel, so content
	// is not flush against the terminal's left edge.
	panelPadX = 1
	// panelPadY is 0: the deck is a full-screen alt-screen program, so a blank
	// row above the title is a workspace row the list does not get. Horizontal
	// padding is cheap — a column is not a row — and vertical padding is not.
	// The one gap the deck keeps is inside the panel, under the title row,
	// where it separates the attention badge from the first project header;
	// the badge sits on the rows' own text column and without a gap it reads
	// as a row.
	panelPadY = 0

	// panelRows is what a body panel spends on its own vertical padding.
	panelRows = 2 * panelPadY
	// panelCols is what it spends horizontally.
	panelCols = 2 * panelPadX
	// footerRows is the whole footer block: the status bar plus its padding,
	// which matches the panels'.
	footerRows = 1 + panelRows
	// deckHeaderRows is the row list's own header: the title row and the blank
	// under it.
	deckHeaderRows = 2
	// deckTitleRowIndex is which line of renderList's output the title row is —
	// past the panel's top padding, and nothing else. Tests that reach for the
	// title index by this rather than by a literal, so a padding change moves
	// them with it instead of failing them.
	deckTitleRowIndex = panelPadY
)
