package cli

import (
	"strings"
	"testing"
)

func TestWrapTextWrapsInsteadOfTruncating(t *testing.T) {
	got := wrapText("one two three four", 7)
	if got != "one two\nthree\nfour" {
		t.Fatalf("unexpected wrap: %q", got)
	}
}

func TestIndentWrappedPrefixesEachLine(t *testing.T) {
	got := indentWrapped("one two three four", "   ", 10)
	lines := strings.Split(got, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected multiple lines, got %q", got)
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "   ") {
			t.Fatalf("expected indented line, got %q", line)
		}
	}
}
