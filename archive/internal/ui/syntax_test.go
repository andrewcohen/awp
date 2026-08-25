package ui

import (
	"image/color"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/andrewcohen/awp/internal/charm"
	"github.com/andrewcohen/awp/internal/diff"
	"github.com/andrewcohen/awp/internal/highlight"
)

// The hue each class wears, asserted on the mapping rather than on output: with no
// TTY lipgloss strips colour, so a rendered row cannot say which token it used.
func TestEveryTokenClassHasADecidedHue(t *testing.T) {
	table := []struct {
		tok  highlight.Token
		want color.Color
	}{
		{highlight.Keyword, charm.SyntaxKeyword},
		{highlight.Type, charm.SyntaxType},
		{highlight.Func, charm.SyntaxFunc},
		{highlight.Attr, charm.SyntaxAttr},
		{highlight.String, charm.SyntaxString},
		{highlight.Number, charm.SyntaxNumber},
		{highlight.Comment, charm.SyntaxComment},
		{highlight.Operator, charm.SyntaxOperator},
		{highlight.Punct, charm.SyntaxPunct},
		// A bare identifier keeps the colour of the line it is on. Stating it would
		// override a terminal set to something other than this theme, for no gain — the
		// theme's own Text is what the terminal's default already is.
		{highlight.Plain, nil},
	}
	// A class added to highlight without a hue decided for it renders in whatever the
	// zero style produces, which is indistinguishable from a deliberate "no hue".
	if len(table) != highlight.TokenCount {
		t.Errorf("this table covers %d classes, highlight has %d", len(table), highlight.TokenCount)
	}
	for _, tc := range table {
		if got := syntaxHue(tc.tok); got != tc.want {
			t.Errorf("token %d is hue %v, want %v", tc.tok, got, tc.want)
		}
	}
}

// Every class that has a hue must have a *distinct* one, or two of them are the
// same colour on screen and one of the two rules is doing nothing.
func TestNoTwoClassesShareAHue(t *testing.T) {
	seen := map[color.Color]highlight.Token{}
	for tok := highlight.Token(0); int(tok) < highlight.TokenCount; tok++ {
		hue := syntaxHue(tok)
		if hue == nil {
			continue
		}
		if prev, dup := seen[hue]; dup {
			t.Errorf("tokens %d and %d are both %v", prev, tok, hue)
		}
		seen[hue] = tok
	}
}

// Painting must not add or lose a byte of code. It writes span by span and nothing
// else, so a hole in the tiling would be text that never reaches the screen —
// invisible, because the holes would mostly be spaces.
func TestPaintingKeepsEveryByte(t *testing.T) {
	lex := highlight.For("main.go")
	for _, line := range []string{
		`func main() { fmt.Println("hi", 42) } // trailing`,
		`	if err != nil { return fmt.Errorf("wörld: %w", err) }`,
		`}`,
	} {
		spans := lex.Spans(line)
		if len(spans) == 0 {
			t.Fatalf("no spans for %q", line)
		}
		// Every background a painted line can sit on, since each is a different set of
		// escapes wrapped around the same bytes.
		for _, lineType := range []byte{' ', '+', '-'} {
			for _, cursor := range []bool{false, true} {
				if got := ansi.Strip(paintCode(line, spans, lineType, cursor)); got != line {
					t.Errorf("painting %q as %q (cursor %v) produced %q", line, lineType, cursor, got)
				}
			}
		}
	}
}

// capture reads the escapes out of a lipgloss Render once and concatenates after
// that, to avoid a 20KB parser buffer per span. This is what pins the two agreeing:
// a single Plain span over the whole line has to come out byte-identical to letting
// lipgloss render it.
func TestPaintingASpanIsWhatLipglossWouldRender(t *testing.T) {
	line := "some plain code"
	whole := []highlight.Span{{Start: 0, End: len(line), Tok: highlight.Plain}}

	for _, tc := range []struct {
		name     string
		lineType byte
		cursor   bool
		style    lipgloss.Style
	}{
		{"context", ' ', false, styleCode},
		{"context, cursor", ' ', true, styleCodeCursor},
		{"added", '+', false, styleCodeAdded},
		{"added, cursor", '+', true, styleCodeAddedCursor},
		{"removed", '-', false, styleCodeRemoved},
		{"removed, cursor", '-', true, styleCodeRemovedCursor},
	} {
		got := paintCode(line, whole, tc.lineType, tc.cursor)
		if want := tc.style.Render(line); got != want {
			t.Errorf("%s: painted %q, lipgloss renders %q", tc.name, got, want)
		}
	}
}

// The backwash: highlighting spends the foreground on the lexer, so a + line and a
// - line would otherwise differ only in the gutter glyph. Six distinct backgrounds,
// because the cursor's row is a step brighter than the row beneath it whichever kind
// of line it is — letting the cursorline simply win means a + row loses its tint for
// as long as the cursor sits on it, which blinks down the file as you scroll.
func TestEveryKindOfPaintedLineHasItsOwnBackground(t *testing.T) {
	line := "code"
	whole := []highlight.Span{{Start: 0, End: len(line), Tok: highlight.Plain}}

	seen := map[string]string{}
	for _, tc := range []struct {
		name     string
		lineType byte
		cursor   bool
	}{
		{"context", ' ', false},
		{"context, cursor", ' ', true},
		{"added", '+', false},
		{"added, cursor", '+', true},
		{"removed", '-', false},
		{"removed, cursor", '-', true},
	} {
		got := paintCode(line, whole, tc.lineType, tc.cursor)
		if prev, dup := seen[got]; dup {
			t.Errorf("%s renders identically to %s — the two are indistinguishable on screen", tc.name, prev)
		}
		seen[got] = tc.name
	}
}

// The backwash covers the whole row, gutter and line numbers included. Starting it
// at the code left those columns as an untinted notch, which reads as a gap rather
// than as a zone boundary.
func TestThePaintedGutterSharesTheLinesBackground(t *testing.T) {
	for _, lineType := range []byte{' ', '+', '-'} {
		for _, cursor := range []bool{false, true} {
			lp := paintTable().line(lineType, cursor)
			want := lp.fill.GetBackground()
			for name, col := range map[string]lipgloss.Style{"numbers": lp.number, "glyph": lp.glyph} {
				if got := col.GetBackground(); got != want {
					t.Errorf("%q (cursor %v): the %s column is on %v, the line is on %v",
						lineType, cursor, name, got, want)
				}
			}
			// Only the background is imposed. The glyph keeps the change type's hue and the
			// numbers keep theirs, or the columns stop saying anything of their own.
			if lineType == ' ' {
				continue
			}
			if lp.glyph.GetForeground() == lp.number.GetForeground() {
				t.Errorf("%q (cursor %v): the glyph and the numbers are the same colour", lineType, cursor)
			}
		}
	}
}

// The tint has to reach the pane's edge. A background that stops where the code
// happens to end is not a property of the line, and reads as a rendering fault —
// the length of a line is not something the reader is meant to notice.
func TestAPaintedChangeFillsThePane(t *testing.T) {
	t.Setenv(SyntaxEnv, "all")
	m := goFileModel(t, false)

	const width = 100
	painted := 0
	for i := range m.stream.rows {
		lineType, ok := m.paintedLine(i)
		if !ok || (lineType != '+' && lineType != '-') {
			continue
		}
		painted++
		if got := ansi.StringWidth(m.renderStreamRowAt(i, width)); got != width {
			t.Errorf("row %d is a painted %q line but spans %d of %d columns", i, lineType, got, width)
		}
	}
	if painted == 0 {
		t.Fatal("the fixture has no painted added or removed lines, so this proves nothing")
	}
}

// Unpainted, nothing changes: the change type is already the foreground of every
// character on the line, so a context row still ends where its text does.
func TestAnUnpaintedRowIsNotFilled(t *testing.T) {
	t.Setenv(SyntaxEnv, "off")
	m := goFileModel(t, false)

	for i, r := range m.stream.rows {
		// Only code lines. A file divider and a hunk header are full-width bands by
		// design, and the cursorline band has always filled the pane.
		if r.kind != rowLine || m.rowBanded(i) {
			continue
		}
		if got := ansi.StringWidth(m.renderStreamRowAt(i, 100)); got == 100 {
			t.Errorf("row %d fills the pane with the flag off", i)
		}
	}
}

func TestHighlightingIsOnUnlessTurnedOff(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want syntaxMode
	}{
		// The default, and what a typo gets. A misspelled `off` that silently kept
		// highlighting on would be a puzzle; a misspelled `changed` is just the default.
		{"", syntaxAll},
		{"yes", syntaxAll},
		{"chnaged", syntaxAll},
		{"changed", syntaxChanged},
		{"off", 0},
		{"0", 0},
		{"false", 0},
		{"none", 0},
	} {
		t.Setenv(SyntaxEnv, tc.env)
		h := newHighlighter()
		switch {
		case tc.want == 0 && h != nil:
			t.Errorf("%s=%q built a highlighter in mode %d, want off", SyntaxEnv, tc.env, h.mode)
		case tc.want != 0 && h == nil:
			t.Errorf("%s=%q is off, want mode %d", SyntaxEnv, tc.env, tc.want)
		case tc.want != 0 && h.mode != tc.want:
			t.Errorf("%s=%q is mode %d, want %d", SyntaxEnv, tc.env, h.mode, tc.want)
		}
	}
}

// The two treatments differ in exactly one thing: whether unchanged code is
// painted. changed leaves it Muted, which is what the pane does today, so the
// change keeps reading as foreground.
func TestTheTreatmentsDifferOnContext(t *testing.T) {
	added := diff.HunkLine{Type: '+', Content: `x := 1`}
	context := diff.HunkLine{Type: ' ', Content: `y := 2`}

	t.Setenv(SyntaxEnv, "changed")
	h := newHighlighter()
	if len(h.spansFor("a.go", added)) == 0 {
		t.Error("changed did not paint an added line")
	}
	if got := h.spansFor("a.go", context); got != nil {
		t.Errorf("changed painted a context line with %d spans", len(got))
	}

	t.Setenv(SyntaxEnv, "all")
	h = newHighlighter()
	if len(h.spansFor("a.go", context)) == 0 {
		t.Error("all did not paint a context line")
	}
}

// Lexing is the expensive half, so a line the pane has already seen must not be
// lexed again. Asserted on the backing array: the same array means the cache
// answered rather than the lexer.
func TestALineIsLexedOnce(t *testing.T) {
	t.Setenv(SyntaxEnv, "all")
	h := newHighlighter()
	l := diff.HunkLine{Type: '+', Content: `func main() {}`}

	first := h.spansFor("a.go", l)
	second := h.spansFor("a.go", l)
	if len(first) == 0 {
		t.Fatal("no spans")
	}
	if &first[0] != &second[0] {
		t.Error("the line was lexed twice — the second answer is a different slice")
	}
}

// chroma's Match iterates every file pattern of every lexer, so a file whose
// extension nothing claims must still only be looked up once.
func TestAnUnrecognisedFileIsResolvedOnce(t *testing.T) {
	t.Setenv(SyntaxEnv, "all")
	h := newHighlighter()
	l := diff.HunkLine{Type: '+', Content: `some prose`}

	if got := h.spansFor("notes.xyzzy", l); got != nil {
		t.Errorf(".xyzzy produced %d spans", len(got))
	}
	h.spansFor("notes.xyzzy", l)
	if _, ok := h.lexers["notes.xyzzy"]; !ok {
		t.Error("the failed lookup was not remembered, so it repeats per line")
	}
	if n := len(h.lexers); n != 1 {
		t.Errorf("%d lexers resolved for one path", n)
	}
}

// diff.DisplayPath renders a rename as "old → new", which is a label, not a
// filename. A lexer lookup needs the new side.
func TestLexPathIgnoresTheRenameArrow(t *testing.T) {
	f := diff.FileDiff{Status: "R", OldPath: "old/a.py", NewPath: "new/b.go"}
	if got := lexPath(f); got != "new/b.go" {
		t.Errorf("lexPath is %q, want the new path", got)
	}
	if got := lexPath(diff.FileDiff{Status: "D", OldPath: "gone.go"}); got != "gone.go" {
		t.Errorf("a deleted file lexes as %q, want its old path", got)
	}
}

// The corruption #152 names for side-by-side: a cell is cut to width, and cutting a
// styled string mid-escape puts the row past its border. Every row has to measure
// the same painted as unpainted, in both layouts.
func TestAPaintedRowIsTheSameWidth(t *testing.T) {
	for _, layout := range []struct {
		name       string
		sideBySide bool
	}{
		{"unified", false},
		{"side by side", true},
	} {
		t.Setenv(SyntaxEnv, "off")
		plain := goFileModel(t, layout.sideBySide)
		t.Setenv(SyntaxEnv, "all")
		painted := goFileModel(t, layout.sideBySide)

		if len(plain.stream.rows) != len(painted.stream.rows) {
			t.Fatalf("%s: %d rows unpainted, %d painted — highlighting changed the geometry",
				layout.name, len(plain.stream.rows), len(painted.stream.rows))
		}
		const width = 100
		for i := range plain.stream.rows {
			want := plain.renderStreamRowAt(i, width)
			got := painted.renderStreamRowAt(i, width)
			// Never wider than the pane. An escape cut through the middle leaks its bytes
			// into the visible text, which is what puts a row past its border.
			if w := ansi.StringWidth(got); w > width {
				t.Errorf("%s row %d: painted spans %d of %d columns", layout.name, i, w, width)
			}
			// Trailing space only, since a painted change fills the pane and its unpainted
			// counterpart stops at its text — see TestAPaintedChangeFillsThePane.
			if g, w := trimEnd(got), trimEnd(want); g != w {
				t.Errorf("%s row %d: painted reads %q, unpainted %q", layout.name, i, g, w)
			}
		}
	}
}

// Panning right cuts from the left. Painted, that cut lands in a styled string, and
// ansi.TruncateLeft keeps the escapes it skips past — so the text still reads the
// same as the unpainted pan of the same line.
func TestPanningAPaintedLineCutsTheSameText(t *testing.T) {
	t.Setenv(SyntaxEnv, "off")
	plain := goFileModel(t, false)
	plain.hunkHScroll = 8
	t.Setenv(SyntaxEnv, "all")
	painted := goFileModel(t, false)
	painted.hunkHScroll = 8

	for i := range plain.stream.rows {
		want := trimEnd(plain.renderStreamRowAt(i, 100))
		got := trimEnd(painted.renderStreamRowAt(i, 100))
		if got != want {
			t.Errorf("row %d panned to %q, want %q", i, got, want)
		}
	}
}

// trimEnd is a row's visible text without its styling or its fill, which is what
// two rows have to agree on when only one of them is painted.
func trimEnd(row string) string {
	return strings.TrimRight(ansi.Strip(row), " ")
}

// goFileModel is a viewer over one Go file with real code in it, so there is
// something for a lexer to find.
func goFileModel(t *testing.T, sideBySide bool) Model {
	t.Helper()
	lines := []diff.HunkLine{
		{Type: ' ', Content: `package main`},
		{Type: '-', Content: `func old(n int) string { return "was" }`},
		{Type: '+', Content: `func New(n int) string { return "is" } // changed`},
		{Type: ' ', Content: `	var total = 42`},
	}
	f := diff.FileDiff{
		NewPath: "main.go",
		Status:  "M",
		Hunks:   []diff.Hunk{{OldStart: 1, OldCount: 3, NewStart: 1, NewCount: 3, Lines: lines}},
	}
	m := streamModel(t, f)
	if sideBySide {
		m.sideBySide = true
		m.rebuildStream()
	}
	if !strings.Contains(m.filtered[0].NewPath, ".go") {
		t.Fatalf("the fixture is not a Go file: %q", m.filtered[0].NewPath)
	}
	return m
}
