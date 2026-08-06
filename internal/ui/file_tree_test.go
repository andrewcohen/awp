package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/andrewcohen/awp/internal/diff"
)

func fileAt(path string) diff.FileDiff {
	return diff.FileDiff{NewPath: path, Status: "M"}
}

// labels flattens the tree to "indent + label" strings, which is what the layout
// is actually about.
func labels(rows []fileTreeRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		// A wide width, so the layout is the logical one rather than a clamped one.
		out = append(out, treeIndent(r.depth, 200)+r.label)
	}
	return out
}

// The whole point: a directory is named once and its files are basenames under
// it, instead of every row spending its width on the same prefix.
func TestTreeNamesEachDirectoryOnce(t *testing.T) {
	rows := fileTreeRows([]diff.FileDiff{
		fileAt("internal/ui/model.go"),
		fileAt("internal/ui/comments.go"),
		fileAt("internal/cli/deck.go"),
	})
	want := []string{
		"internal/",
		"  ui/",
		"    model.go",
		"    comments.go",
		"  cli/",
		"    deck.go",
	}
	got := labels(rows)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("tree layout:\ngot:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Only the part of the path that differs gets a heading — "internal/" is not
// repeated for the second package.
func TestTreeOnlyHeadsTheDivergingPart(t *testing.T) {
	rows := fileTreeRows([]diff.FileDiff{
		fileAt("a/b/c/one.go"),
		fileAt("a/b/c/two.go"),
		fileAt("a/b/d/three.go"),
	})
	heads := 0
	for _, r := range rows {
		if r.isDir() {
			heads++
		}
	}
	// a/, b/, c/, then only d/ — four, not six.
	if heads != 4 {
		t.Fatalf("expected 4 headings, got %d: %v", heads, labels(rows))
	}
}

// A file at the repo root has no directory to head.
func TestTreeRootFilesGetNoHeading(t *testing.T) {
	rows := fileTreeRows([]diff.FileDiff{fileAt("README.md"), fileAt("go.mod")})
	if got := labels(rows); strings.Join(got, ",") != "README.md,go.mod" {
		t.Fatalf("expected bare root files, got %v", got)
	}
}

// The file list is an index into the stream, and the stream is the diff in the
// diff's own order. Sorting here would make moving down the list jump around the
// change.
func TestTreePreservesTheFileSetsOrder(t *testing.T) {
	rows := fileTreeRows([]diff.FileDiff{
		fileAt("z/last.go"),
		fileAt("a/first.go"),
	})
	var files []int
	for _, r := range rows {
		if !r.isDir() {
			files = append(files, r.file)
		}
	}
	if len(files) != 2 || files[0] != 0 || files[1] != 1 {
		t.Fatalf("expected the file set's own order, got %v (%v)", files, labels(rows))
	}
}

// A rename displays as "old → new"; it lives where it lives now, and splitting on
// the arrow would head it with two paths at once.
func TestTreePlacesARenameByItsNewPath(t *testing.T) {
	rows := fileTreeRows([]diff.FileDiff{{
		OldPath: "old/place/name.go", NewPath: "new/place/name.go", Status: "R",
	}})
	for _, r := range rows {
		if r.isDir() && strings.Contains(r.label, "→") {
			t.Fatalf("heading built from a rename arrow: %q", r.label)
		}
	}
	got := labels(rows)
	if got[0] != "new/" {
		t.Fatalf("expected the new side's directory to head it, got %v", got)
	}
}

// The scroll window counts tree rows, not files — headings take rows of their
// own, so counting files would leave the cursor's row off screen.
func TestTreeRowOfFindsTheCursorsRow(t *testing.T) {
	files := []diff.FileDiff{
		fileAt("a/one.go"),
		fileAt("b/two.go"),
		fileAt("c/three.go"),
	}
	rows := fileTreeRows(files)
	for i := range files {
		at := treeRowOf(rows, i)
		if rows[at].file != i {
			t.Fatalf("file %d: expected its own row, got %+v", i, rows[at])
		}
		// Each file is preceded by its heading, so its row index outruns its file
		// index — which is exactly why the window cannot be computed in files.
		if at <= i {
			t.Fatalf("file %d sits at row %d; the window would be short", i, at)
		}
	}
}

// A deep path costs a couple of columns of indent instead of its whole prefix,
// which is the width this change is buying back.
func TestTreeRowIsShorterThanTheFullPath(t *testing.T) {
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	m.SetSize(120, 20)
	m = loadWith(m, 1, fileAt("app/lib/navigation/menu-item.server.ts"))

	rows := fileTreeRows(m.filtered)
	at := treeRowOf(rows, 0)
	row := stripANSI(m.renderFileRow(m.filtered[0], rows[at], 60, false, false))
	if strings.Contains(row, "app/lib/navigation") {
		t.Fatalf("expected the row to carry only the basename, got %q", row)
	}
	if !strings.Contains(row, "menu-item.server.ts") {
		t.Fatalf("expected the basename shown in full, got %q", row)
	}
}

// Every row has to fit the pane, headings included: a deep directory name cannot
// push the column open.
func TestTreeRowsFitTheirWidth(t *testing.T) {
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	m.SetSize(120, 20)
	m = loadWith(m, 1,
		fileAt("some/very/deeply/nested/directory/chain/with/a/long/name/file.go"),
		fileAt("short.go"),
	)
	rows := fileTreeRows(m.filtered)
	for _, width := range []int{12, 20, 40} {
		for _, r := range rows {
			var out string
			if r.isDir() {
				out = renderTreeDir(r, width)
			} else {
				out = m.renderFileRow(m.filtered[r.file], r, width, false, false)
			}
			if got := lipgloss.Width(out); got > width {
				t.Fatalf("width %d: row %q is %d cells", width, stripANSI(out), got)
			}
		}
	}
}
