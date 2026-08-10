package ui

import (
	"os"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/andrewcohen/awp/internal/charm"
	"github.com/andrewcohen/awp/internal/diff"
	"github.com/andrewcohen/awp/internal/highlight"
)

// SyntaxEnv turns syntax highlighting on for the diff body.
//
// A flag rather than a setting because the question it exists to answer is which
// of the two treatments below is worth having, and that needs both of them
// reachable on the same diff.
const SyntaxEnv = "AWP_DIFF_SYNTAX"

// syntaxMode is where a diff line's colour comes from.
type syntaxMode uint8

const (
	// syntaxAll paints every code line, context included. The change type comes off
	// the body entirely and is carried by the gutter glyph and the line numbers,
	// which are already tinted by it.
	syntaxAll syntaxMode = iota + 1
	// syntaxChanged paints the added and removed lines only. Context stays uniformly
	// Muted, as it is today, so the change keeps reading as foreground against
	// unchanged code as background — and the majority of a diff's lines are never
	// lexed at all.
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

// newHighlighter reads the flag. Anything it does not recognise is off, so a typo
// gets today's rendering rather than a silently different treatment.
func newHighlighter() *highlighter {
	var mode syntaxMode
	switch os.Getenv(SyntaxEnv) {
	case "1", "on", "all":
		mode = syntaxAll
	case "changed":
		mode = syntaxChanged
	default:
		return nil
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

// syntaxPaint is the token classes' escapes, one set per background a painted
// line can sit on: its change type, and whether the cursorline is behind it.
//
// Six rather than two because the backwash moved the change type into the
// background — see charm.AddedBg — so a span's escapes now depend on the kind of
// line it is on as well as on its own token class.
type syntaxPaint struct {
	context, added, removed          []spanPaint
	contextCur, addedCur, removedCur []spanPaint
}

// row is the escapes for a line of this change type, cursor or not.
func (p syntaxPaint) row(lineType byte, cursor bool) []spanPaint {
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
		context:    paintRow(styleCode),
		added:      paintRow(styleCodeAdded),
		removed:    paintRow(styleCodeRemoved),
		contextCur: paintRow(styleCodeCursor),
		addedCur:   paintRow(styleCodeAddedCursor),
		removedCur: paintRow(styleCodeRemovedCursor),
	}
}

// fillStyle is what pads a painted line out to the width it should span.
//
// The tint has to reach the edge of the pane, or it reads as a highlight on the
// code rather than as a property of the line — and a row whose tint stops where
// its text does looks like a rendering fault, since the length of the code is not
// something the reader is meant to notice.
func fillStyle(lineType byte, cursor bool) lipgloss.Style {
	switch lineType {
	case '+':
		if cursor {
			return styleCodeAddedCursor
		}
		return styleCodeAdded
	case '-':
		if cursor {
			return styleCodeRemovedCursor
		}
		return styleCodeRemoved
	}
	if cursor {
		return styleCursorFill
	}
	return styleCode
}

// syntaxHue is a token class's palette token, empty for the classes that keep the
// colour of the line they are on.
//
// Factored out so the mapping is assertable. lipgloss strips colour when there is
// no TTY, so which hue a class ended up wearing cannot be read back out of
// rendered output — the same reason selectionBarStyle exists.
func syntaxHue(t highlight.Token) string {
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
	}
	// Plain and Punct: they wear the base, which is the terminal's own foreground.
	// See the note in charm.palette on why neither gets a hue of its own.
	return ""
}

// paintRow is base combined with each token class's hue, reduced to the escapes
// lipgloss would emit for it.
func paintRow(base lipgloss.Style) []spanPaint {
	out := make([]spanPaint, highlight.TokenCount)
	for tok := range out {
		style := base
		if hue := syntaxHue(highlight.Token(tok)); hue != "" {
			style = base.Foreground(lipgloss.Color(hue))
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
	row := paintTable().row(lineType, cursor)
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
