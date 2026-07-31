package ui

import (
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/andrewcohen/awp/internal/diff"
)

// The file list as a tree.
//
// A flat list spends its whole width on paths that mostly repeat: in a change
// touching internal/ui/model.go, internal/ui/comments.go and
// internal/ui/stream.go, "internal/ui/" is written three times and the part that
// distinguishes the rows is what gets truncated away. Naming each directory once
// and listing basenames under it gives the width back to the filenames, which is
// the part you are reading.
//
// Directory rows are not selectable. The cursor is still an index into the file
// set — every seek, the reviewed marker, and the stream's own file cursor all
// speak that language — so making a directory something you could land on would
// mean inventing a second kind of selection and teaching every one of them about
// it. j/k move over files, as before; the directory rows are structure, not
// destinations.

// fileTreeRow is one rendered row of the tree: either a directory heading or a
// file.
type fileTreeRow struct {
	// depth is how far to indent, in tree levels.
	depth int
	// label is the directory's own segment (with a trailing slash) or the file's
	// basename.
	label string
	// file indexes the file set, or -1 for a directory heading.
	file int
}

// isDir reports whether this row is a heading rather than a file.
func (r fileTreeRow) isDir() bool { return r.file < 0 }

// fileTreeRows lays the file set out as a tree, in the file set's own order.
//
// Order is preserved rather than sorted: the file list is an index into the
// stream, and the stream is the diff in the order the diff gives it. Sorting here
// would make moving down the list jump around the change. Directory headings are
// therefore emitted where the path first diverges from the previous row's, which
// groups a sorted diff (what jj and git produce) into the tree you would draw by
// hand.
func fileTreeRows(files []diff.FileDiff) []fileTreeRow {
	var out []fileTreeRow
	var prev []string
	for i, f := range files {
		segs := dirSegments(diff.DisplayPath(f))
		// Headings only for the part of the path that is new. A file in the same
		// directory as the one above it needs no heading at all.
		shared := commonPrefixLen(prev, segs)
		for d := shared; d < len(segs); d++ {
			out = append(out, fileTreeRow{depth: d, label: segs[d] + "/", file: -1})
		}
		out = append(out, fileTreeRow{
			depth: len(segs),
			label: filepath.Base(diff.DisplayPath(f)),
			file:  i,
		})
		prev = segs
	}
	return out
}

// dirSegments is a path's directory part split into its segments, empty for a
// file at the root.
//
// A rename displays as "old → new"; its directory is the new side's, which is
// where the file lives now. Splitting on the arrow would produce a heading named
// after two paths.
func dirSegments(display string) []string {
	path := display
	if at := strings.Index(path, " → "); at >= 0 {
		path = path[at+len(" → "):]
	}
	dir := filepath.Dir(path)
	if dir == "." || dir == string(filepath.Separator) {
		return nil
	}
	return strings.Split(dir, string(filepath.Separator))
}

func commonPrefixLen(a, b []string) int {
	n := min(len(a), len(b))
	for i := range n {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// treeRowOf is the tree row showing a given file, so the scroll window can be
// computed over rows while the cursor stays a file index.
func treeRowOf(rows []fileTreeRow, file int) int {
	for i, r := range rows {
		if r.file == file {
			return i
		}
	}
	return 0
}

// treeIndentWidth is how many columns a row at this depth is inset by. Two per
// level: enough to read as nesting, cheap enough that a deep path still leaves
// room for its name.
//
// Capped so the name always has somewhere to go. On a narrow pane a deep chain's
// indent can be wider than the pane itself, and a row indented off the right edge
// says nothing at all — so nesting is the first thing to give up. The cap is the
// same for headings and files so the two stay aligned with each other however
// narrow it gets.
func treeIndentWidth(depth, width int) int {
	room := width - lipgloss.Width(selectionPrefixBlank) - minTreeNameColumns
	return min(depth*2, max(0, room))
}

// minTreeNameColumns is what a row keeps for its badge, its gap and at least one
// column of name.
const minTreeNameColumns = 6

func treeIndent(depth, width int) string {
	return strings.Repeat(" ", treeIndentWidth(depth, width))
}
