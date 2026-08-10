package ui

import (
	"image/color"
	"os"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/andrewcohen/awp/internal/charm"
	"github.com/andrewcohen/awp/internal/diff"
	"github.com/andrewcohen/awp/internal/highlight"
)

// SyntaxEnv selects the diff body's syntax treatment. Unset means on.
//
// It is the escape hatch rather than the switch: `off` for a terminal this reads
// badly on, or for not wanting it, and `changed` for the other treatment. Nobody
// should have to set anything to get the ordinary rendering.
const SyntaxEnv = "AWP_DIFF_SYNTAX"

// syntaxMode is where a diff line's colour comes from.
type syntaxMode uint8

const (
	// syntaxAll paints every code line, context included, and is the default.
	//
	// The change type comes off the body entirely and is carried by the background
	// tint and the gutter. That tint is also why this beats syntaxChanged: muting
	// context existed to make the change read as foreground against it, and marking
	// the changed lines directly does that better than dimming everything else. With
	// nothing left for the muting to buy, colouring the surrounding code is the point
	// of reading a diff in context rather than a patch.
	syntaxAll syntaxMode = iota + 1
	// syntaxChanged paints the added and removed lines only, leaving context Muted
	// the way an unhighlighted diff has it. Kept reachable because it is a taste
	// question, and it never lexes the majority of a diff's lines.
	syntaxChanged
)

// highlighter is the diff body's syntax colour, absent when the flag is unset.
//
// A nil *highlighter is off. That way every call site is a nil check rather than a
// mode comparison, and the unpainted path — the one this pane has always taken and
// the one #63–#65 tuned — cannot be reached through the painted code by accident.
type highlighter struct {
	mode syntaxMode
	// lexers is resolved per path because chroma's Match "iterates over all file
	// patterns in all lexers, so is not fast" — per line it would cost more than the
	// lexing it precedes.
	lexers map[string]highlight.Lexer
	// spans is keyed on the line's text rather than on its position, so the cache
	// survives everything that renumbers rows — filtering, folding, a reload that
	// shifts a hunk — none of which can change what a given line of a given language
	// lexes to. That is what makes it safe to keep across a rebuildStream, which
	// drops every other cache here; lexing is the expensive half and a fold toggle is
	// no reason to redo it.
	spans map[spanKey][]highlight.Span
}

// spanKey is one line of one file.
type spanKey struct{ path, line string }

// newHighlighter reads the escape hatch.
//
// Anything unrecognised gets the default rather than nothing: the failure mode of
// a typo should be the ordinary rendering, and the ordinary rendering is now
// highlighted. Turning it off has to be spelled correctly, which is the right way
// round — a misspelled `off` that silently kept highlighting on is a puzzle, where
// a misspelled `changed` is just the default.
func newHighlighter() *highlighter {
	mode := syntaxAll
	switch os.Getenv(SyntaxEnv) {
	case "off", "0", "false", "none":
		return nil
	case "changed":
		mode = syntaxChanged
	}
	return &highlighter{
		mode:   mode,
		lexers: map[string]highlight.Lexer{},
		spans:  map[spanKey][]highlight.Span{},
	}
}

// spansFor is the syntax spans of one diff line, or nil when it should not be
// painted — the flag is off, this treatment leaves context alone, or nothing
// claims the file's extension.
func (h *highlighter) spansFor(path string, l diff.HunkLine) []highlight.Span {
	if h == nil || l.Content == "" {
		return nil
	}
	if h.mode == syntaxChanged && l.Type != '+' && l.Type != '-' {
		return nil
	}
	lex, ok := h.lexers[path]
	if !ok {
		lex = highlight.For(path)
		h.lexers[path] = lex
	}
	if !lex.Ok() {
		return nil
	}
	key := spanKey{path: path, line: l.Content}
	if spans, ok := h.spans[key]; ok {
		// Including a nil, which is a line the lexer had nothing to say about. Cached
		// as readily as any other answer, or it is re-lexed on every frame.
		return spans
	}
	spans := lex.Spans(l.Content)
	h.spans[key] = spans
	return spans
}

// lexPath is the filename a file's language is resolved from.
//
// Not diff.DisplayPath, which renders a rename as "old → new": a lexer lookup
// wants a filename, and the new side is where the code is.
func lexPath(f diff.FileDiff) string {
	if f.NewPath != "" {
		return f.NewPath
	}
	return f.OldPath
}

// spanPaint is the escape pair one token class's text is wrapped in.
type spanPaint struct{ prefix, suffix string }

// linePaint is everything needed to draw one painted line: its spans' escapes, and
// the styles for the columns either side of the code.
//
// One of these per background a line can sit on, which is why the number and glyph
// styles live here rather than being derived at the call site: a lipgloss Style
// carrying a background is built once, not per row.
type linePaint struct {
	// spans is indexed by highlight.Token.
	spans []spanPaint
	// number and glyph are the line-number columns and the +/-/│ marker. They carry
	// the change tint too, so the backwash starts at the row's left edge — begun at
	// the code instead, it left the gutter as an untinted notch that read as a gap
	// rather than as a zone boundary.
	number, glyph lipgloss.Style
	// fill pads the row out to the pane's width. The tint has to reach the edge or it
	// reads as a highlight on the code rather than a property of the line, and a row
	// whose tint stops where its text happens to end looks like a rendering fault.
	fill lipgloss.Style
}

// syntaxPaint is the six of them: three change types, cursor or not.
//
// Six rather than one because the backwash moved the change type into the
// background — see charm.AddedBg — so everything on a row depends on the kind of
// line it is as well as on its own role.
type syntaxPaint struct {
	context, added, removed          linePaint
	contextCur, addedCur, removedCur linePaint
}

// line is the styles for a line of this change type, cursor or not.
func (p syntaxPaint) line(lineType byte, cursor bool) linePaint {
	switch lineType {
	case '+':
		if cursor {
			return p.addedCur
		}
		return p.added
	case '-':
		if cursor {
			return p.removedCur
		}
		return p.removed
	}
	if cursor {
		return p.contextCur
	}
	return p.context
}

// paintTable is captured once, on first use.
//
// Lazily rather than at package init because lipgloss resolves a colour against
// the output's profile at Render time, and at init nothing has looked at the
// terminal yet — a table built then could bake in a profile the program has not
// chosen.
var paintTable = sync.OnceValue(buildPaintTable)

func buildPaintTable() syntaxPaint {
	return syntaxPaint{
		// A nil background is unchanged code with no cursor on it — the majority of a
		// diff, and the one case that paints no background at all.
		context:    lineFor(nil, styleContext, styleLineNo),
		contextCur: lineFor(cursorlineBg, styleContext, styleCursorLineNo),
		added:      lineFor(charm.AddedBg, styleAdded, styleLineNo),
		addedCur:   lineFor(charm.AddedBgCursor, styleAdded, styleCursorLineNo),
		removed:    lineFor(charm.RemovedBg, styleDeleted, styleLineNo),
		removedCur: lineFor(charm.RemovedBgCursor, styleDeleted, styleCursorLineNo),
	}
}

// lineFor builds one row's styles over a shared background.
//
// glyph and number are passed in for their *foregrounds* — the gutter marker keeps
// the change type's hue and the numbers keep theirs, including the cursor row's
// Warning tint. Only the background is imposed here, which is what makes the whole
// row one continuous field.
func lineFor(bg color.Color, glyph, number lipgloss.Style) linePaint {
	base := withBg(styleCode, bg)
	return linePaint{
		spans:  paintRow(base),
		number: withBg(number, bg),
		glyph:  withBg(glyph, bg),
		fill:   base,
	}
}

// withBg is s over bg, or s unchanged when there is no background to impose.
func withBg(s lipgloss.Style, bg color.Color) lipgloss.Style {
	if bg == nil {
		return s
	}
	return s.Background(bg)
}

// syntaxHue is a token class's colour, nil for the one class that keeps the colour
// of the line it is on.
//
// Factored out so the mapping is assertable. lipgloss strips colour when there is
// no TTY, so which hue a class ended up wearing cannot be read back out of
// rendered output — the same reason selectionBarStyle exists.
func syntaxHue(t highlight.Token) color.Color {
	switch t {
	case highlight.Keyword:
		return charm.SyntaxKeyword
	case highlight.Type:
		return charm.SyntaxType
	case highlight.Func:
		return charm.SyntaxFunc
	case highlight.Attr:
		return charm.SyntaxAttr
	case highlight.String:
		return charm.SyntaxString
	case highlight.Number:
		return charm.SyntaxNumber
	case highlight.Comment:
		return charm.SyntaxComment
	case highlight.Operator:
		return charm.SyntaxOperator
	case highlight.Punct:
		return charm.SyntaxPunct
	}
	// Plain — a bare identifier — wears the base, which is the terminal's own
	// foreground. Under this theme that is already Catppuccin's Text, so stating it
	// would only take the choice away from a terminal set to something else.
	return nil
}

// paintRow is base combined with each token class's hue, reduced to the escapes
// lipgloss would emit for it.
func paintRow(base lipgloss.Style) []spanPaint {
	out := make([]spanPaint, highlight.TokenCount)
	for tok := range out {
		style := base
		if hue := syntaxHue(highlight.Token(tok)); hue != nil {
			style = base.Foreground(hue)
		}
		out[tok] = capture(style)
	}
	return out
}

// capture reduces a style to the escapes it puts either side of its text.
//
// Style.Render allocates an ansi parser buffer of about 20KB whatever it is given
// (see the note on renderCache), and a painted line has one span per token — so
// rendering per span would put back exactly the per-frame garbage #71 removed.
// Asking lipgloss once what it emits, and concatenating after that, costs a string
// append per span instead.
//
// Read out of a Render rather than assembled here on purpose: it is lipgloss that
// knows the colour profile, whether the terminal has colour at all, and how an
// adaptive colour resolves. paint_test.go pins that the two agree.
func capture(style lipgloss.Style) spanPaint {
	const sentinel = "\x00"
	out := style.Render(sentinel)
	i := strings.Index(out, sentinel)
	if i < 0 {
		// lipgloss did something other than wrap the text. Nothing to capture, so the
		// span renders unstyled rather than corrupt.
		return spanPaint{}
	}
	return spanPaint{prefix: out[:i], suffix: out[i+len(sentinel):]}
}

// paintCode styles text span by span.
//
// Each chunk carries its own escapes rather than the line being wrapped in one
// enclosing style. That is the same constraint the cursorline already has — see the
// note on cursorlineBg — because every inner style ends with a reset, which would
// take an enclosing background with it from the first span onwards.
//
// spans must tile text, which is highlight.Spans' documented guarantee: this walks
// them and writes nothing else, so a gap would be text that never reaches the
// screen.
func paintCode(text string, spans []highlight.Span, lineType byte, cursor bool) string {
	row := paintTable().line(lineType, cursor).spans
	var b strings.Builder
	// The escapes roughly double a short line; one allocation beats growing.
	b.Grow(len(text) * 2)
	for _, s := range spans {
		if s.Start < 0 || s.End > len(text) || s.End <= s.Start {
			continue
		}
		p := row[highlight.Plain]
		if int(s.Tok) < len(row) {
			p = row[s.Tok]
		}
		b.WriteString(p.prefix)
		b.WriteString(text[s.Start:s.End])
		b.WriteString(p.suffix)
	}
	return b.String()
}

// visibleSlice is the part of a line this row shows: a wrap segment, or the text
// past the horizontal pan.
//
// ANSI-aware, so it takes painted text as readily as plain — segmentText goes
// through ansi.Cut, and ansi.TruncateLeft writes the escapes it skips past rather
// than dropping them, so style state survives the cut. That is what lets the
// painted path style the whole line first and cut after, with the byte offsets its
// spans are expressed in still valid.
func (m Model) visibleSlice(text string, seg, avail int) string {
	if m.wrap {
		return segmentText(text, seg, avail)
	}
	if m.hunkHScroll > 0 {
		return ansi.TruncateLeft(text, m.hunkHScroll, "")
	}
	return text
}
