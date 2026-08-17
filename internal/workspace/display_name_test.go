package workspace_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// DisplayName is presentation only, and this is what keeps it that way.
//
// The invariant is that nothing resolves anything from a label — not a directory, not
// a session, not a bookmark, not a PR. It is exactly the kind of rule that holds
// until someone needs a name, finds one to hand, and uses it: the code compiles, the
// tests pass, and a workspace whose label happens to differ from its name addresses
// the wrong thing. That failure does not announce itself, which is why the guard is a
// list of the files allowed to mention the field at all rather than a test of
// behaviour.
//
// This is the shape internal/github/dir_test.go uses for the same class of problem —
// an invariant that is only as strong as every call site remembering it.
//
// Adding a file here is a decision, not a formality. Ask: does this code *render* the
// label, or does it *use* it? Rendering is fine. Using it to look something up is the
// thing this exists to prevent.
func TestOnlyRenderersMentionDisplayName(t *testing.T) {
	// The files allowed to mention DisplayName, and why.
	allowed := map[string]string{
		// Where it is declared, on the entry and on the list row.
		"internal/workspace/service.go": "declares the field, and SetDisplayName writes it",
		// The two places that turn a row into text on screen. Two rather than one
		// because the strip answers the question differently: DisplayLabel falls back
		// to the PR title, and sidebarLabel deliberately does not, since at 36 columns
		// the line below already carries the PR number.
		"internal/deckdata/view.go":  "DisplayLabel — the row list's renderer",
		"internal/deckui/sidebar.go": "sidebarLabel — the strip's renderer",
		// The read model's row type carries it to that renderer.
		"internal/deckdata/types.go": "declares it on the row",
		// Populating a row from the store, and the two verbs that set it.
		"internal/cli/deck.go":            "copies it from the store onto the row",
		"internal/cli/workspace_label.go": "the `awp w label` verb",
		"internal/cli/workspace_new.go":   "--label at create time",
	}

	root, err := repoRoot()
	if err != nil {
		t.Skipf("cannot find the repo root: %v", err)
	}

	var offenders []string
	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // an unreadable tree is not this test's business
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", ".jj", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		if _, ok := allowed[filepath.ToSlash(rel)]; ok {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		if strings.Contains(string(body), "DisplayName") {
			offenders = append(offenders, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	for _, f := range offenders {
		t.Errorf("%s mentions DisplayName. It is presentation only — if this renders the label, add it to the allow-list in this test with the reason; if it resolves anything from it (a path, a session, a bookmark, a PR), use Name instead.", f)
	}
}

// repoRoot walks up from the test's directory to the module root.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}
