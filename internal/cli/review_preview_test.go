package cli

import (
	"bytes"
	"strings"
	"testing"
)

// The bug, in one case: a nine-line proposal listed as one ribbon. The listing
// is a table, and a row that wraps the terminal four times stops being one.
func TestBodyPreviewKeepsAMultiLineBodyToOneLine(t *testing.T) {
	body := "wrap it in m.fail\n\nThe caller already has the error, so the\nonly thing missing is the report.\n\nSay yes and I will do it."
	got := bodyPreview(body)
	if strings.ContainsAny(got, "\n\r") {
		t.Fatalf("the preview is still several lines: %q", got)
	}
	if !strings.HasPrefix(got, "wrap it in m.fail") {
		t.Errorf("the preview is not the body's first line: %q", got)
	}
	// The old join is the thing being replaced; if it comes back this catches it.
	if strings.Contains(got, " / ") {
		t.Errorf("the preview still joins the body's lines: %q", got)
	}
}

// A preview that dropped something says so. This matters most on the publish
// preview, which lists bodies next to the calls that will carry them: a
// reviewer confirming that plan has to be able to tell a whole body from the
// top of one.
func TestBodyPreviewMarksWhatItLeftOut(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"more lines below", "the first line\nand a second one"},
		{"one line, too long", strings.Repeat("x", previewWidth+20)},
		{"a blank line, then more", "the first line\n\nand a third one"},
	} {
		if got := bodyPreview(tc.body); !strings.HasSuffix(got, previewEllipsis) {
			t.Errorf("%s: the preview does not say it is partial: %q", tc.name, got)
		}
	}
}

// And a body that fits is shown whole — no ellipsis on a one-line finding,
// which is still what most of them are.
func TestBodyPreviewLeavesAShortBodyAlone(t *testing.T) {
	for _, body := range []string{
		"this drops the error",
		"  this drops the error  ",
		"\n\nthis drops the error\n",
	} {
		if got := bodyPreview(body); got != "this drops the error" {
			t.Errorf("bodyPreview(%q) = %q", body, got)
		}
	}
	if got := bodyPreview("   \n\t\n"); got != "" {
		t.Errorf("an empty body previewed as %q", got)
	}
}

// Never wider than the budget, whatever the body is. The columns in front of it
// only fit beside a bounded field.
func TestBodyPreviewStaysInsideItsWidth(t *testing.T) {
	for _, body := range []string{
		strings.Repeat("x", 500),
		strings.Repeat("word ", 200),
		strings.Repeat("日", 500),
	} {
		if got := []rune(bodyPreview(body)); len(got) > previewWidth {
			t.Errorf("a %d-rune body previewed %d wide, budget is %d", len([]rune(body)), len(got), previewWidth)
		}
	}
}

// Counted in runes. Slicing bytes at the budget cuts a multi-byte character in
// half and the terminal draws the pieces as replacement boxes.
func TestBodyPreviewDoesNotCutACharacterInHalf(t *testing.T) {
	got := bodyPreview(strings.Repeat("日", 500))
	if strings.ContainsRune(got, '\uFFFD') {
		t.Errorf("the preview cut a character in half: %q", got)
	}
}

// A carriage return alone ends the line too. A body is text we did not write,
// and a lone \r left inside a "single line" reprints over everything before it
// — so the visible row would not be the row we checked.
func TestBodyPreviewStopsAtACarriageReturn(t *testing.T) {
	got := bodyPreview("the real first line\rand an overprint")
	if strings.ContainsRune(got, '\r') {
		t.Fatalf("a carriage return survived into the preview: %q", got)
	}
	if !strings.HasPrefix(got, "the real first line") {
		t.Errorf("the preview is %q", got)
	}
}

// End to end: the human listing gives a long body one row, and --json still
// carries the whole thing. The machine channel is where an agent reads a body
// back, so the truncation must not reach it.
func TestReviewListGivesALongBodyOneRow(t *testing.T) {
	runner, svc, _ := proposalCLI(t)
	long := "wrap it in m.fail\nthe caller already has the error\nsay yes and I will do it"
	var out bytes.Buffer
	if err := runReviewAdd(runner, svc, []string{"--file", "b.go", "--line", "3", "--body", long}, &out); err != nil {
		t.Fatalf("review add: %v", err)
	}

	rows := strings.Split(strings.TrimSpace(listing(t, runner, svc)), "\n")
	// One header row naming the review, then one row per comment.
	if got, want := len(rows), 3; got != want {
		t.Fatalf("the listing is %d rows, want %d:\n%s", got, want, strings.Join(rows, "\n"))
	}
	for _, r := range rows {
		if strings.Contains(r, "say yes and I will do it") {
			t.Errorf("the listing carries the body's last line: %q", r)
		}
	}

	for _, c := range listComments(t, runner, svc) {
		if c.Anchor.Path == "b.go" && c.Body != long {
			t.Errorf("--json truncated the body:\n got %q\nwant %q", c.Body, long)
		}
	}
}
