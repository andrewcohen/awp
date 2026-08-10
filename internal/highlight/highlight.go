// Package highlight turns a line of source code into spans over its own bytes.
//
// Spans rather than a styled string, and that is the whole design. The diff
// renderer cuts a line before it styles it — a wrap segment, a horizontal pan, a
// side-by-side cell — because measuring styled text counts escape bytes as
// columns, which is what pushes a row past its border. Handing back a
// pre-coloured string would put escapes inside every one of those cuts and inside
// every width calculation. Byte offsets survive both: the caller cuts the plain
// text exactly as it does today, then paints the range it kept.
//
// Colour is deliberately absent. A Token is a semantic class; which palette hue
// it wears is the renderer's decision, so this package has no opinion about the
// design system and does not import lipgloss.
package highlight

import (
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// Token is a class of source text.
//
// The set is deliberately small. The palette this ends up rendered in is ANSI 16,
// so there are about eight hues to spend, and distinctions finer than these
// cannot be seen in one.
type Token uint8

const (
	// Plain is an identifier, or anything the lexer had no opinion about.
	Plain Token = iota
	Keyword
	Type
	Func
	String
	Number
	Comment
	Punct
)

// Span is a run of one token class, as byte offsets into the line it came from.
//
// Half-open, so line[s.Start:s.End] is the text. Byte offsets rather than runes
// because every cut the renderer makes is on the byte string too.
type Span struct {
	Start, End int
	Tok        Token
}

// Lexer is one file's language, resolved from its path.
//
// Resolved per file rather than per line on chroma's own advice: its Match
// "iterates over all file patterns in all lexers, so is not fast". A per-line
// lookup would cost more than the lexing it was looking a lexer up for.
//
// The zero Lexer highlights nothing, which is what an unrecognised extension
// gets. Its lines render exactly as they do today rather than as a guess about
// what language they might be.
type Lexer struct {
	impl chroma.Lexer
}

// For resolves the language of the file at path.
func For(path string) Lexer {
	l := lexers.Match(path)
	if l == nil {
		return Lexer{}
	}
	// Coalesce merges adjacent tokens of the same type, which is a straight
	// reduction in the spans the renderer has to paint — chroma emits a run of
	// identical types as one token per match.
	return Lexer{impl: chroma.Coalesce(l)}
}

// Ok reports whether this Lexer knows a language. A false one returns no spans.
func (l Lexer) Ok() bool { return l.impl != nil }

// Spans is line's token runs, or nil when there is nothing to say about it.
//
// When Ok, the spans tile the line exactly: they are in order, none is empty, and
// concatenating line[s.Start:s.End] over all of them reproduces line. The renderer
// depends on that — a gap would be a byte range it never painted and so never
// printed, and it would go unnoticed because the missing bytes are usually spaces.
//
// One line at a time, with no state carried between calls. A hunk's lines are the
// old and the new interleaved, so joining them is not valid source in the first
// place and lexing the join would be lexing something that does not exist. The
// cost is worth stating plainly: a line in the middle of a block comment or a
// multi-line string is lexed as though it were not, so it comes back as code.
// delta has the same limitation, for the same reason.
func (l Lexer) Spans(line string) []Span {
	if l.impl == nil || line == "" {
		return nil
	}
	// Tokenised with a newline appended, then clamped back to the line. Rules that
	// end a state at end-of-line — a `//` comment, an unterminated string — match on
	// the newline, and without one the last token on the line can come back as an
	// error token rather than as what it is.
	it, err := l.impl.Tokenise(nil, line+"\n")
	if err != nil {
		// Nothing to say beats saying something wrong: the line renders unhighlighted.
		return nil
	}

	var out []Span
	at := 0
	for t := it(); t != chroma.EOF; t = it() {
		if at >= len(line) {
			// The appended newline, and anything a lexer emitted after it.
			break
		}
		end := min(at+len(t.Value), len(line))
		tok := classify(t.Type)
		// Merged here as well as by chroma's Coalesce, because the merge has to happen
		// after the mapping: Operator and Punctuation are distinct types to chroma and
		// are both Punct to us.
		if n := len(out); n > 0 && out[n-1].Tok == tok {
			out[n-1].End = end
		} else {
			out = append(out, Span{Start: at, End: end, Tok: tok})
		}
		at = end
	}
	if at < len(line) {
		// The lexer did not account for the whole line. Not expected, but the tiling
		// property above is what the renderer relies on, and a silent gap is bytes
		// that never reach the screen.
		if n := len(out); n > 0 && out[n-1].Tok == Plain {
			out[n-1].End = len(line)
		} else {
			out = append(out, Span{Start: at, End: len(line), Tok: Plain})
		}
	}
	return out
}

// classify maps chroma's token type onto the small set above.
//
// By category where it can be, because chroma has hundreds of types and matching
// them one by one is how a language nobody tested comes back entirely Plain.
func classify(t chroma.TokenType) Token {
	switch {
	// Before the Keyword category, which KeywordType is in: a type name is a
	// different thing to read than `if`.
	case t == chroma.KeywordType, t == chroma.NameClass:
		return Type
	// Builtins are language-provided names, so they read as the same family as
	// keywords — and the alternative is worse, since a language's builtins are a
	// mix of functions and constants (Go's `append` and `nil` are both this).
	case t.InCategory(chroma.Keyword), t.InSubCategory(chroma.NameBuiltin):
		return Keyword
	case t.InSubCategory(chroma.NameFunction):
		return Func
	case t.InSubCategory(chroma.LiteralString):
		return String
	case t.InSubCategory(chroma.LiteralNumber):
		return Number
	case t.InCategory(chroma.Comment):
		return Comment
	case t.InCategory(chroma.Operator), t.InCategory(chroma.Punctuation):
		return Punct
	}
	return Plain
}
