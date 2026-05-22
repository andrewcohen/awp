package workspace

import (
	"regexp"
	"strings"
	"testing"
)

func TestRandomNameShape(t *testing.T) {
	pattern := regexp.MustCompile(`^[a-z]+-[a-z]+-[a-z]+$`)
	for i := 0; i < 64; i++ {
		name, err := RandomName()
		if err != nil {
			t.Fatalf("RandomName: %v", err)
		}
		if !pattern.MatchString(name) {
			t.Fatalf("RandomName produced unexpected shape: %q", name)
		}
		normalized, err := NormalizeName(name)
		if err != nil {
			t.Fatalf("RandomName output %q failed NormalizeName: %v", name, err)
		}
		if normalized != name {
			t.Fatalf("RandomName output %q is not already normalized (got %q)", name, normalized)
		}
		if strings.Count(name, "-") != 2 {
			t.Fatalf("RandomName output %q does not have exactly three words", name)
		}
	}
}
