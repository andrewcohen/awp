package review

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnchorScope(t *testing.T) {
	cases := []struct {
		name   string
		anchor Anchor
		want   Scope
		where  string
	}{
		{"a line", Anchor{Path: "a.go", LineHint: 12}, LineScope, "a.go:12"},
		{"a range", Anchor{Path: "a.go", LineHint: 12, EndLineHint: 18}, LineScope, "a.go:12-18"},
		{"a file", Anchor{Path: "a.go"}, FileScope, "a.go"},
		{"the change", Anchor{}, ChangeScope, "the whole change"},

		// Padding is not a path. A record written with a stray space would
		// otherwise be a file-level comment on a file called " ".
		{"a blank path", Anchor{Path: "   "}, ChangeScope, "the whole change"},

		// The incoherent combination: a line with nothing to be a line of. It
		// reads as "about the change" rather than getting a fourth case, which
		// loses the number and keeps the body — the part somebody wrote.
		{"a line with no path", Anchor{LineHint: 12}, ChangeScope, "the whole change"},

		// An end at or before the start is not a range (see Multiline), so this
		// is still one line and Where must not print "12-12" or "12-4".
		{"an end equal to the start", Anchor{Path: "a.go", LineHint: 12, EndLineHint: 12}, LineScope, "a.go:12"},
		{"an end before the start", Anchor{Path: "a.go", LineHint: 12, EndLineHint: 4}, LineScope, "a.go:12"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.anchor.Scope(); got != tc.want {
				t.Errorf("Scope() = %v, want %v", got, tc.want)
			}
			if got := tc.anchor.Where(); got != tc.where {
				t.Errorf("Where() = %q, want %q", got, tc.where)
			}
		})
	}
}

// Where never trails off into a bare colon or an empty string. Every surface that
// names a location prints it into a sentence — "comment on %s", "added … on %s" —
// and a scope that renders as "" or ":" reads as a bug in the sentence rather than
// as a deliberate absence of a file.
func TestWhereAlwaysNamesSomething(t *testing.T) {
	for _, a := range []Anchor{
		{Path: "a.go", LineHint: 1},
		{Path: "a.go"},
		{},
		{LineHint: 9},
		{Path: "  "},
	} {
		got := a.Where()
		if strings.TrimSpace(got) == "" || strings.HasSuffix(got, ":") {
			t.Errorf("Where() = %q for %+v — must name a scope", got, a)
		}
	}
}

// The scope is implied by what the anchor carries, and that rule belongs in one
// place. Deriving it at the call site is how a third scope gets added here and
// read as the second one in four other packages — the shape that had six copies
// of the working-status rule before internal/workspace named it.
func TestOnlyThisPackageDerivesTheScope(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve the repo root: %v", err)
	}
	allowed := filepath.Join(root, "internal", "review")

	checked := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".jj", "node_modules", "vendor":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") ||
			strings.HasPrefix(path, allowed+string(filepath.Separator)) {
			return nil
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		checked++
		for i, line := range strings.Split(string(b), "\n") {
			// The signature of a re-derivation: testing an anchor's path for
			// emptiness to decide what the comment is about.
			if !strings.Contains(line, "Anchor.Path") {
				continue
			}
			if !strings.Contains(line, `== ""`) && !strings.Contains(line, `!= ""`) {
				continue
			}
			rel, _ := filepath.Rel(root, path)
			t.Errorf("%s:%d decides a comment's scope from its path — ask Anchor.Scope() instead:\n\t%s",
				rel, i+1, strings.TrimSpace(line))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the repo: %v", err)
	}
	if checked < 50 {
		t.Fatalf("expected to scan the repo's sources, only read %d files", checked)
	}
}
