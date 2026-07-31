package charm

import (
	"strings"
	"testing"
)

func TestKeyHelpViewRendersEveryBindingOnItsOwnLine(t *testing.T) {
	out := KeyHelpView([]KeyGroup{
		{Title: "Navigate", Keys: [][2]string{
			{"j/k", "move cursor"},
			{"g/G", "ends"},
		}},
		{Title: "Act", Keys: [][2]string{
			{"enter", "open"},
		}},
	})
	for _, want := range []string{"Navigate", "j/k", "move cursor", "g/G", "ends", "Act", "enter", "open"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in the help view:\n%s", want, out)
		}
	}
	// One line per binding is what makes the key column scannable; a
	// multi-column layout would put "j/k" and "g/G" side by side.
	jk, gG := lineOf(out, "j/k"), lineOf(out, "g/G")
	if jk < 0 || gG < 0 {
		t.Fatalf("bindings not found as lines:\n%s", out)
	}
	if jk == gG {
		t.Fatalf("expected separate lines per binding, both on line %d:\n%s", jk, out)
	}
	// Titles head their own sections, above the bindings they describe.
	if lineOf(out, "Navigate") >= jk {
		t.Fatalf("expected the group title above its bindings:\n%s", out)
	}
	if lineOf(out, "Act") <= gG {
		t.Fatalf("expected the second group below the first:\n%s", out)
	}
}

// A blank line separates one group from the next, so sections read as sections.
func TestKeyHelpViewSeparatesGroups(t *testing.T) {
	out := KeyHelpView([]KeyGroup{
		{Title: "One", Keys: [][2]string{{"a", "first"}}},
		{Title: "Two", Keys: [][2]string{{"b", "second"}}},
	})
	lines := strings.Split(out, "\n")
	blank := false
	for i := lineOf(out, "first") + 1; i < lineOf(out, "Two"); i++ {
		if strings.TrimSpace(lines[i]) == "" {
			blank = true
		}
	}
	if !blank {
		t.Fatalf("expected a blank line between groups:\n%s", out)
	}
}

func TestKeyHelpViewHandlesNoGroups(t *testing.T) {
	if out := KeyHelpView(nil); strings.TrimSpace(out) != "" {
		t.Fatalf("expected nothing for no groups, got %q", out)
	}
}

// lineOf is the index of the first line containing want, or -1.
func lineOf(s, want string) int {
	for i, line := range strings.Split(s, "\n") {
		if strings.Contains(line, want) {
			return i
		}
	}
	return -1
}
