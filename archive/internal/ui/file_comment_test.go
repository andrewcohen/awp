package ui

import (
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/review"
)

// fileComment is a remark about a file as a whole: a path and no line.
func fileComment(id, path, body string) review.Comment {
	return review.Comment{
		ID: id, Author: review.AuthorHuman, Body: body, State: review.Open,
		Anchor: review.Anchor{Path: path, Side: review.SideNew},
	}
}

// A remark about the whole file is not an unplaceable one. It used to land in the
// detached section — under a heading saying the anchor could not be found, about
// the one anchor that cannot fail to be found — because every search in
// locateAnchorStart keys off a line and this anchor has none by design.
func TestAFileCommentIsNotDetached(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.SetComments([]review.Comment{fileComment("c1", "a.go", "this belongs in internal/review")})

	if got := rowsOfKind(m, rowOrphanHeader); got != 0 {
		t.Errorf("a file comment produced a detached section (%d header rows)", got)
	}
	if got := rowsOfKind(m, rowOrphan); got != 0 {
		t.Errorf("a file comment was rendered as detached (%d rows)", got)
	}
	view := stripANSI(m.renderStreamPanel(80, 20))
	if !strings.Contains(view, "this belongs in internal/review") {
		t.Fatalf("the comment is not in the stream at all:\n%s", view)
	}
	if strings.Contains(view, "detached") {
		t.Errorf("the stream still shows a detached section:\n%s", view)
	}
}

// It hangs under the file's divider, above the first hunk — where a reader looks
// for something said about the file rather than about a place in it.
func TestAFileCommentSitsUnderTheDivider(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta", "gamma"))
	m.SetComments([]review.Comment{fileComment("c1", "a.go", "wrong package")})

	rows := m.stream.rows
	var header, comment = -1, -1
	for i, r := range rows {
		if r.kind == rowFileHeader && header < 0 {
			header = i
		}
		if isCommentRow(r.kind) && comment < 0 {
			comment = i
		}
	}
	if header < 0 || comment < 0 {
		t.Fatalf("expected a file header and a comment row; header=%d comment=%d", header, comment)
	}
	if comment < header {
		t.Errorf("the comment is above its file's divider (comment row %d, header row %d)", comment, header)
	}
	// Above the code: every line row of the file comes after it.
	for i := header; i < comment; i++ {
		if rows[i].kind == rowLine {
			t.Errorf("a line row at %d sits between the divider and the file comment — it should be above the hunk", i)
			break
		}
	}
}

// The two scopes coexist: a remark about the file heads it, a remark about a line
// stays on its line.
func TestAFileCommentAndALineCommentBothLand(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.SetComments([]review.Comment{
		fileComment("cf", "a.go", "the whole file is misnamed"),
		commentOn("a.go", 2, "beta", "this line is off by one"),
	})

	if got := rowsOfKind(m, rowOrphan); got != 0 {
		t.Errorf("something was detached (%d rows)", got)
	}
	view := stripANSI(m.renderStreamPanel(90, 24))
	fileAt := strings.Index(view, "the whole file is misnamed")
	lineAt := strings.Index(view, "this line is off by one")
	if fileAt < 0 || lineAt < 0 {
		t.Fatalf("expected both comments in the stream:\n%s", view)
	}
	if fileAt > lineAt {
		t.Errorf("the file comment renders below the line comment:\n%s", view)
	}
}

// A folded file hides a remark about the file itself along with everything else
// inside it — a comment about the file is inside the file, and folding is how you
// put the whole thing away. It must not read as detached on the way, and the index
// has to keep it: hidden is a fact about the stream, not about the remark.
func TestAFileCommentIsHiddenWithItsFile(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.SetComments([]review.Comment{fileComment("c1", "a.go", "wrong package")})
	m.ReviewedFiles = map[string]string{"a.go": fileContentHash(m.filtered[0])}
	m.rebuildStream()

	if got := rowsOfKind(m, rowOrphan); got != 0 {
		t.Errorf("folding the file detached its file comment (%d rows)", got)
	}
	view := stripANSI(m.renderStreamPanel(80, 20))
	if strings.Contains(view, "wrong package") {
		t.Errorf("the file comment is still rendered inside its folded file:\n%s", view)
	}
	if !strings.Contains(view, commentChip+" 1") {
		t.Errorf("the divider does not say a comment is hidden with the file:\n%s", view)
	}
	if len(m.commentIndex) != 1 || !m.commentIndex[0].folded {
		t.Errorf("the index dropped the hidden file comment: %+v", m.commentIndex)
	}

	// Unfolding shows it again, under the divider it is about.
	m.ReviewedFiles = nil
	m.rebuildStream()
	if !strings.Contains(stripANSI(m.renderStreamPanel(80, 20)), "wrong package") {
		t.Error("unfolding the file did not bring its file comment back")
	}
}

// A file comment naming a file the change no longer touches has nowhere to go, and
// that is the case the detached section is for.
func TestAFileCommentOnAMissingFileIsDetached(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.SetComments([]review.Comment{fileComment("c1", "vanished.go", "this file should not exist")})

	if got := rowsOfKind(m, rowOrphan); got == 0 {
		t.Error("expected a comment on a file outside the change to be detached")
	}
}

// Two mirrored threads with the same shape — a path and no line — mean opposite
// things, and the difference is the outdated flag, not the anchor.
//
// An outdated thread is a remark about a line the change removed; GitHub reports
// its line as null. Reading that as "about the whole file" would present a settled
// conversation about vanished code as a standing comment on the file, which is a
// claim nobody made. A thread with no line that is *not* outdated is genuinely
// file-level on GitHub's side, and does belong on the divider.
func TestOutdatedAndFileLevelThreadsAreToldApart(t *testing.T) {
	m := commentModel(t, fileWithDeletion("a.go", "kept", "gone"))
	m.threadVisibility = ThreadsAll

	outdated := remoteThread("T1", "a.go", 0, false, "about the line that went away")
	outdated.Outdated = true
	fileLevel := remoteThread("T2", "a.go", 0, false, "this file is in the wrong package")
	m.SetThreads([]review.Thread{outdated, fileLevel})

	placed, detached := map[string]bool{}, map[string]bool{}
	for _, e := range m.commentIndex {
		if e.detached {
			detached[e.id] = true
		} else {
			placed[e.id] = true
		}
	}
	outdatedID, fileLevelID := review.RemoteThreadID("T1"), review.RemoteThreadID("T2")
	if !detached[outdatedID] {
		t.Errorf("the outdated thread is not detached — it is about a line, not the file: %+v", m.commentIndex)
	}
	if !placed[fileLevelID] {
		t.Errorf("the file-level thread is detached — its anchor is a file, which is right there: %+v", m.commentIndex)
	}
}
