package highlight

import (
	"testing"
)

// The guard that matters more than the mapping.
//
// classify was written against Go and checked against Go, and TypeScript came back
// nearly all Plain — because every lexer spells the same idea differently and Plain
// is a legitimate answer, so nothing looked broken. A grey diff is the failure mode,
// and it is silent.
//
// So: one representative line per language this repo and its users actually read,
// asserting the classes that line has to produce. A language added to chroma, or a
// mapping narrowed by a later edit, fails here instead of on someone's screen.
func TestEachLanguageGetsItsColours(t *testing.T) {
	for _, tc := range []struct {
		path string
		line string
		want []Token
	}{
		{
			"main.go",
			`func New(n int) (*Store, error) { return &Store{n: n}, nil } // ctor`,
			[]Token{Keyword, Type, Func, Comment},
		},
		{
			// The report that started this. TypeScript's lexer calls almost every
			// identifier NameOther, so there is genuinely less to colour than in Go — but
			// a generic argument is a NameTag and used to come back Plain.
			"api.ts",
			`export async function load(id: string): Promise<User> {`,
			[]Token{Keyword, Type},
		},
		{
			"App.tsx",
			`const App = () => <div className="row">{items.length}</div>;`,
			[]Token{Keyword, Type, Attr, String},
		},
		{
			// A YAML key is a NameTag and its value is a bare Literal with no
			// subcategory. Both were Plain, which is every meaningful token on the line.
			"deploy.yaml",
			`  image: nginx:1.25   # the web tier`,
			[]Token{Type, String, Comment},
		},
		{
			"package.json",
			`  "name": "awp", "port": 8080`,
			[]Token{String, Number},
		},
		{
			"load.py",
			`def load(path: str) -> dict: return json.loads(path)  # read it`,
			[]Token{Keyword, Func, Comment},
		},
		{
			"run.sh",
			`for f in *.go; do echo "$f"; done  # every file`,
			[]Token{Keyword, Attr, String, Comment},
		},
		{
			"main.zig",
			`pub fn main() !void { try stdout.print("hi", .{}); }`,
			[]Token{Keyword, Type, Func, String},
		},
		{
			"lib.rs",
			`pub fn new(n: usize) -> Store { Store { n } }`,
			[]Token{Keyword, Type, Func},
		},
		{
			// A markdown lexer has no notion of code, so headings are all there is to
			// colour — and without them a README diff comes back entirely grey.
			"README.md",
			`## Heading`,
			[]Token{Type},
		},
	} {
		lex := For(tc.path)
		if !lex.Ok() {
			t.Errorf("%s: no lexer", tc.path)
			continue
		}
		got := map[Token]bool{}
		for _, s := range lex.Spans(tc.line) {
			got[s.Tok] = true
		}
		for _, want := range tc.want {
			if !got[want] {
				t.Errorf("%s: no %v span in %q — got %v", tc.path, want, tc.line, classes(got))
			}
		}
	}
}

// Every class a mapping can produce must be reachable from some real line, or it is
// a hue nobody will ever see and a rule nobody is checking.
func TestEveryTokenClassIsReachable(t *testing.T) {
	lines := map[string]string{
		"main.go":     `func New(n int) error { return nil } // c`,
		"App.tsx":     `const App = () => <div className="row">{n}</div>;`,
		"deploy.yaml": `  image: nginx:1.25`,
		"load.py":     `x = 42`,
	}
	got := map[Token]bool{}
	for path, line := range lines {
		for _, s := range For(path).Spans(line) {
			got[s.Tok] = true
		}
	}
	for tok := Token(0); int(tok) < TokenCount; tok++ {
		if !got[tok] {
			t.Errorf("no line in this test produces %v, so nothing checks its hue", tok)
		}
	}
}

func classes(got map[Token]bool) []Token {
	var out []Token
	for tok := Token(0); int(tok) < TokenCount; tok++ {
		if got[tok] {
			out = append(out, tok)
		}
	}
	return out
}
