package workspace

import "testing"

func TestSlugFromText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain sentence", "fix the sidebar cursor bug", "fix-the-sidebar-cursor-bug"},
		{"truncates to slugWords", "spike a jj backed undo for the deck rows", "spike-a-jj-backed-undo"},
		{"keeps digits", "look at PR 2320", "look-at-pr-2320"},
		{"punctuation collapses", "fix: the *deck's* cursor!", "fix-the-deck-s-cursor"},
		{"empty is not an error", "", "workspace"},
		{"punctuation only", "!!! ???", "workspace"},
		{"leading and trailing space", "   tidy up   ", "tidy-up"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SlugFromText(tc.in); got != tc.want {
				t.Errorf("SlugFromText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// A single very long word must still yield a usable directory name, and one
// that does not end in the separator NormalizeName trims everywhere else.
func TestSlugFromTextBoundsLength(t *testing.T) {
	long := ""
	for range 100 {
		long += "abcdefghij"
	}
	got := SlugFromText(long)
	if len(got) > slugMaxLen {
		t.Errorf("SlugFromText(long) = %q (len %d), want at most %d", got, len(got), slugMaxLen)
	}
	if got == "" {
		t.Error("SlugFromText(long) is empty")
	}
}

// Whatever SlugFromText returns has to survive the normalizer the creation
// path runs it through, or the fallback produces a name that fails at
// PrepareWorkspace instead of at the box.
func TestSlugFromTextIsAlreadyNormalized(t *testing.T) {
	for _, in := range []string{"fix the sidebar cursor bug", "", "!!!", "look at PR 2320", "UPPER Case Words Here"} {
		slug := SlugFromText(in)
		norm, err := NormalizeName(slug)
		if err != nil {
			t.Errorf("NormalizeName(SlugFromText(%q)=%q) failed: %v", in, slug, err)
			continue
		}
		if norm != slug {
			t.Errorf("SlugFromText(%q) = %q, not normalized (NormalizeName gives %q)", in, slug, norm)
		}
	}
}
