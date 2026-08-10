package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/andrewcohen/awp/internal/charm"
	"github.com/andrewcohen/awp/internal/diff"
	"github.com/andrewcohen/awp/internal/highlight"
)

// The hue each class wears, asserted on the mapping rather than on output: with no
// TTY lipgloss strips colour, so a rendered row cannot say which token it used.
func TestEveryTokenClassHasADecidedHue(t *testing.T) {
	for _, tc := range []struct {
		tok  highlight.Token
		want string
	}{
		{highlight.Keyword, charm.SyntaxKeyword},
		{highlight.Type, charm.SyntaxType},
		{highlight.Func, charm.SyntaxFunc},
		{highlight.String, charm.SyntaxString},
		{highlight.Number, charm.SyntaxNumber},
		{highlight.Comment, charm.SyntaxComment},
		// Both keep the colour of the line they are on. Punctuation given a hue of its
		// own turned every `(){},.` on the screen into another colour to read past.
		{highlight.Plain, ""},
		{highlight.Punct, ""},
	} {
		if got := syntaxHue(tc.tok); got != tc.want {
			t.Errorf("token %d is hue %q, want %q", tc.tok, got, tc.want)
		}
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
		if got := ansi.Strip(paintCode(line, spans, false)); got != line {
			t.Errorf("painting %q produced %q", line, got)
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

	if got, want := paintCode(line, whole, false), styleCode.Render(line); got != want {
		t.Errorf("painted %q, lipgloss renders %q", got, want)
	}
	if got, want := paintCode(line, whole, true), styleCodeCursor.Render(line); got != want {
		t.Errorf("on a cursor row painted %q, lipgloss renders %q", got, want)
	}
}

func TestTheFlagIsOffUnlessItIsSet(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want syntaxMode
	}{
		{"", 0},
		// Not recognised, so today's rendering — a typo that silently picked a treatment
		// would be indistinguishable from the flag not working.
		{"yes", 0},
		{"true", 0},
		{"1", syntaxAll},
		{"on", syntaxAll},
		{"all", syntaxAll},
		{"changed", syntaxChanged},
	} {
		t.Setenv(SyntaxEnv, tc.env)
		h := newHighlighter()
		switch {
		case tc.want == 0 && h != nil:
			t.Errorf("%q built a highlighter in mode %d, want off", tc.env, h.mode)
		case tc.want != 0 && h == nil:
			t.Errorf("%q is off, want mode %d", tc.env, tc.want)
		case tc.want != 0 && h.mode != tc.want:
			t.Errorf("%q is mode %d, want %d", tc.env, h.mode, tc.want)
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
		t.Setenv(SyntaxEnv, "")
		plain := goFileModel(t, layout.sideBySide)
		t.Setenv(SyntaxEnv, "all")
		painted := goFileModel(t, layout.sideBySide)

		if len(plain.stream.rows) != len(painted.stream.rows) {
			t.Fatalf("%s: %d rows unpainted, %d painted — highlighting changed the geometry",
				layout.name, len(plain.stream.rows), len(painted.stream.rows))
		}
		for i := range plain.stream.rows {
			want := plain.renderStreamRowAt(i, 100)
			got := painted.renderStreamRowAt(i, 100)
			if ansi.StringWidth(got) != ansi.StringWidth(want) {
				t.Errorf("%s row %d: painted is %d columns, unpainted %d",
					layout.name, i, ansi.StringWidth(got), ansi.StringWidth(want))
			}
			if ansi.Strip(got) != ansi.Strip(want) {
				t.Errorf("%s row %d: painted reads %q, unpainted %q",
					layout.name, i, ansi.Strip(got), ansi.Strip(want))
			}
		}
	}
}

// Panning right cuts from the left. Painted, that cut lands in a styled string, and
// ansi.TruncateLeft keeps the escapes it skips past — so the text still reads the
// same as the unpainted pan of the same line.
func TestPanningAPaintedLineCutsTheSameText(t *testing.T) {
	t.Setenv(SyntaxEnv, "")
	plain := goFileModel(t, false)
	plain.hunkHScroll = 8
	t.Setenv(SyntaxEnv, "all")
	painted := goFileModel(t, false)
	painted.hunkHScroll = 8

	for i := range plain.stream.rows {
		want := ansi.Strip(plain.renderStreamRowAt(i, 100))
		got := ansi.Strip(painted.renderStreamRowAt(i, 100))
		if got != want {
			t.Errorf("row %d panned to %q, want %q", i, got, want)
		}
	}
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
