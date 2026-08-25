package highlight

import (
	"strings"
	"testing"
)

// The property the renderer depends on. It paints span by span, so a gap is a byte
// range that never reaches the screen — and since the gaps would usually be
// whitespace, nothing would look wrong, the code would just be missing a space.
func TestTheSpansTileTheLine(t *testing.T) {
	lex := For("main.go")
	if !lex.Ok() {
		t.Fatal("no lexer for a .go file")
	}
	lines := []string{
		`func main() { fmt.Println("hi", 42) } // trailing`,
		`	x := "héllo → wörld" // a comment with café in it`,
		`}`,
		`   `,
		`if err != nil {`,
	}
	for _, line := range lines {
		spans := lex.Spans(line)
		if len(spans) == 0 {
			t.Errorf("no spans for %q", line)
			continue
		}
		var got strings.Builder
		at := 0
		for i, s := range spans {
			if s.Start != at {
				t.Errorf("%q: span %d starts at %d, want %d — the tiling has a gap or an overlap", line, i, s.Start, at)
				break
			}
			if s.End <= s.Start {
				t.Errorf("%q: span %d is empty (%d..%d)", line, i, s.Start, s.End)
				break
			}
			got.WriteString(line[s.Start:s.End])
			at = s.End
		}
		if got.String() != line {
			t.Errorf("the spans reconstruct %q, want %q", got.String(), line)
		}
	}
}

func TestAGoLineIsClassified(t *testing.T) {
	line := `func main() { return nil }`
	spans := For("main.go").Spans(line)

	for _, tc := range []struct {
		text string
		want Token
	}{
		{"func", Keyword},
		{"main", Func},
		{"return", Keyword},
		{"nil", Keyword},
		{"{", Punct},
	} {
		if got := tokenOf(t, line, spans, tc.text); got != tc.want {
			t.Errorf("%q is %v, want %v", tc.text, got, tc.want)
		}
	}
}

func TestACommentIsAComment(t *testing.T) {
	line := `x := 1 // why`
	spans := For("main.go").Spans(line)

	if got := tokenOf(t, line, spans, "// why"); got != Comment {
		t.Errorf("the comment is %v, want %v", got, Comment)
	}
	if got := tokenOf(t, line, spans, "1"); got != Number {
		t.Errorf("the number is %v, want %v", got, Number)
	}
}

// The quotes belong to the string. Painting the text but not its delimiters is
// the sort of thing that reads as a rendering bug rather than as a choice.
func TestAStringKeepsItsQuotes(t *testing.T) {
	line := `s := "hello"`
	spans := For("main.go").Spans(line)

	if got := tokenOf(t, line, spans, `"`); got != String {
		t.Errorf("the opening quote is %v, want %v", got, String)
	}
	if got := tokenOf(t, line, spans, "hello"); got != String {
		t.Errorf("the body is %v, want %v", got, String)
	}
}

// An extension nothing claims renders the way it does today. A guessed language
// colours the line confidently and wrongly, which is worse than not colouring it.
func TestAnUnrecognisedExtensionHighlightsNothing(t *testing.T) {
	lex := For("notes.xyzzy")
	if lex.Ok() {
		t.Fatal("something claimed .xyzzy")
	}
	if spans := lex.Spans(`func main() {`); spans != nil {
		t.Errorf("a lexerless Lexer returned %d spans", len(spans))
	}
}

func TestAnEmptyLineHasNoSpans(t *testing.T) {
	if spans := For("main.go").Spans(""); spans != nil {
		t.Errorf("an empty line produced %d spans", len(spans))
	}
}

// Spans lexes line+"\n" so that end-of-line rules fire, and has to clamp the
// result back. Without the clamp the last span reaches one byte past the line and
// every slice of it panics.
func TestNoSpanRunsPastTheLine(t *testing.T) {
	line := `x := 1 // a comment runs to the end of the line`
	spans := For("main.go").Spans(line)
	if len(spans) == 0 {
		t.Fatal("no spans")
	}
	if last := spans[len(spans)-1]; last.End != len(line) {
		t.Errorf("the last span ends at %d, want %d (len %d line)", last.End, len(line), len(line))
	}
	for _, s := range spans {
		if s.End > len(line) || s.Start < 0 {
			t.Errorf("span %d..%d is outside a %d-byte line", s.Start, s.End, len(line))
		}
	}
}

// No state carried between calls. It is what makes a span cache keyed on the line
// text correct, and what makes the block-comment limitation in the doc comment a
// bounded one rather than an order-dependent one.
func TestEachLineIsLexedOnItsOwn(t *testing.T) {
	lex := For("main.go")
	line := `n := 2`

	first := lex.Spans(line)
	lex.Spans(`/* an unterminated block comment`)
	second := lex.Spans(line)

	if len(first) != len(second) {
		t.Fatalf("the same line lexed to %d spans then %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("span %d was %+v, then %+v", i, first[i], second[i])
		}
	}
}

// tokenOf is the class the spans give to the first occurrence of want in line.
func tokenOf(t *testing.T, line string, spans []Span, want string) Token {
	t.Helper()
	at := strings.Index(line, want)
	if at < 0 {
		t.Fatalf("%q is not in %q", want, line)
	}
	for _, s := range spans {
		if at >= s.Start && at < s.End {
			return s.Tok
		}
	}
	t.Fatalf("no span covers %q at byte %d of %q", want, at, line)
	return Plain
}
