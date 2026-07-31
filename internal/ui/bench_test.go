package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/andrewcohen/awp/internal/diff"
	"github.com/andrewcohen/awp/internal/review"
)

// Measuring the viewer, so "this diff is slow" can be answered with a number.
//
// Point it at the diff that is actually slow rather than trusting the synthetic
// one below — the shape of a real change (file count, hunk count, line lengths,
// how many comments and mirrored threads land on it) is what decides which of
// these paths dominates:
//
//	jj diff --git -r 'trunk()..@' > /tmp/slow.diff     # in the slow workspace
//	AWP_BENCH_DIFF=/tmp/slow.diff mise exec -- go test ./internal/ui/ \
//	    -run XXX -bench . -benchmem
//
// And for a profile of whichever one is worst:
//
//	AWP_BENCH_DIFF=/tmp/slow.diff mise exec -- go test ./internal/ui/ \
//	    -run XXX -bench Rebuild -cpuprofile /tmp/cpu.out
//	mise exec -- go tool pprof -top -nodecount=25 /tmp/cpu.out
//
// AWP_BENCH_COMMENTS=<n> sets how many comments are placed (default 20), which is
// the axis to push if placement is the suspect: locateComment scans the row set
// per comment, so its cost is comments × rows.
// AWP_BENCH_FILES / _HUNKS / _LINES resize the synthetic change, for measuring
// how each path scales rather than what it costs once.
const (
	benchDiffEnv     = "AWP_BENCH_DIFF"
	benchCommentsEnv = "AWP_BENCH_COMMENTS"
	benchFilesEnv    = "AWP_BENCH_FILES"
	benchHunksEnv    = "AWP_BENCH_HUNKS"
	benchLinesEnv    = "AWP_BENCH_LINES"
)

// benchInt reads a size knob, defaulting when unset.
func benchInt(tb testing.TB, name string, def int) int {
	tb.Helper()
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		tb.Fatalf("%s=%s: %v", name, v, err)
	}
	return n
}

// Benchmarks run without a TTY, where lipgloss strips every colour — so an
// uninstrumented frame here contains no escape sequences at all and is nothing
// like the one the terminal actually receives. Every Render in the chain has to
// parse those sequences, and comment rows carry far more of them than code rows
// (a full-width background fill, several style runs each), so measuring without
// colour measures the wrong thing entirely.
//
// ANSI256 rather than TrueColor: the palette is ANSI 16 plus one 256-colour
// cursorline, which is what the app emits.
func init() {
	lipgloss.SetColorProfile(termenv.ANSI256)
}

// benchFiles is the fixture: the captured diff when one is named, otherwise a
// synthetic change big enough to show the scaling.
func benchFiles(tb testing.TB) []diff.FileDiff {
	tb.Helper()
	if path := os.Getenv(benchDiffEnv); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			tb.Fatalf("read %s=%s: %v", benchDiffEnv, path, err)
		}
		files := diff.ParseGitDiff(string(raw))
		if len(files) == 0 {
			tb.Fatalf("%s parsed to no files — is it `jj diff --git` output?", path)
		}
		return files
	}
	return syntheticFiles(
		benchInt(tb, benchFilesEnv, 40),
		benchInt(tb, benchHunksEnv, 12),
		benchInt(tb, benchLinesEnv, 30),
	)
}

// syntheticFiles builds a change of the given shape. Line contents vary per line
// so text-anchored placement cannot short-circuit on a repeat, and are long
// enough to wrap at a normal pane width.
func syntheticFiles(files, hunks, linesPerHunk int) []diff.FileDiff {
	out := make([]diff.FileDiff, 0, files)
	for f := 0; f < files; f++ {
		hs := make([]diff.Hunk, 0, hunks)
		for h := 0; h < hunks; h++ {
			lines := make([]diff.HunkLine, 0, linesPerHunk)
			for l := 0; l < linesPerHunk; l++ {
				kind := byte(' ')
				switch l % 5 {
				case 1:
					kind = '+'
				case 3:
					kind = '-'
				}
				lines = append(lines, diff.HunkLine{
					Type: kind,
					Content: fmt.Sprintf(
						"\tif err := doSomething(ctx, %d, %d, %d); err != nil { return fmt.Errorf(\"wrap %d: %%w\", err) }",
						f, h, l, l),
				})
			}
			start := 1 + h*(linesPerHunk+20)
			hs = append(hs, diff.Hunk{
				OldStart: start, NewStart: start,
				OldCount: len(lines), NewCount: len(lines),
				Lines: lines,
			})
		}
		out = append(out, diff.FileDiff{
			NewPath: fmt.Sprintf("internal/pkg%d/file%d.go", f/8, f),
			Status:  "M",
			Hunks:   hs,
		})
	}
	return out
}

// benchCommentCount is how many conversations to place.
func benchCommentCount(tb testing.TB) int {
	tb.Helper()
	if v := os.Getenv(benchCommentsEnv); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			tb.Fatalf("%s=%s: %v", benchCommentsEnv, v, err)
		}
		return n
	}
	return 20
}

// benchComments spreads n comments over the change, anchored by content the way a
// real one is — placement's expensive path is the text ladder, not the line-number
// shortcut a remote thread takes.
func benchComments(files []diff.FileDiff, n int) []review.Comment {
	if n <= 0 || len(files) == 0 {
		return nil
	}
	out := make([]review.Comment, 0, n)
	for i := 0; i < n; i++ {
		f := files[(i*7)%len(files)]
		if len(f.Hunks) == 0 || len(f.Hunks[0].Lines) == 0 {
			continue
		}
		h := f.Hunks[i%len(f.Hunks)]
		at := i % len(h.Lines)
		out = append(out, review.Comment{
			ID:     fmt.Sprintf("bench-%d", i),
			Author: review.AuthorHuman,
			Body:   "a finding worth a couple of lines of prose, about this line",
			State:  review.Open,
			Anchor: review.Anchor{
				Path:     pathOf(f),
				Side:     review.SideNew,
				LineHint: h.NewStart + at,
				Text:     h.Lines[at].Content,
			},
		})
	}
	return out
}

// benchModel is a viewer sized like a full-screen deck modal, holding the fixture.
func benchModel(tb testing.TB, comments []review.Comment) Model {
	tb.Helper()
	files := benchFiles(tb)
	m := New("/repo", func() (string, error) { return "", nil }, nil)
	m.SetSize(160, 46)
	m.focus = FocusHunks
	m.files = files
	m.applyFilter()
	m.comments = comments
	if os.Getenv(benchNoCacheEnv) != "" {
		// Reproduce the pre-cache behaviour, for before/after on the same fixture:
		// with no cache installed, every row is rendered from scratch every frame.
		m.cache = nil
	}
	m.rebuildStream()
	return m
}

// BenchmarkBuildStream is the geometry pass alone: every line of every hunk,
// wrapped to the pane width. Called on every rebuild.
func BenchmarkBuildStream(b *testing.B) {
	m := benchModel(b, nil)
	b.ReportMetric(float64(len(m.stream.rows)), "rows")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = buildStream(m.filtered, m.hunkWidth, m.wrap, m.isCollapsed)
	}
}

// BenchmarkPlaceComments is the placement pass: locateComment per comment,
// scanning the row set each time.
func BenchmarkPlaceComments(b *testing.B) {
	m := benchModel(b, nil)
	m.comments = benchComments(m.filtered, benchCommentCount(b))
	idx := buildStream(m.filtered, m.hunkWidth, m.wrap, m.isCollapsed)
	b.ReportMetric(float64(len(idx.rows)), "rows")
	b.ReportMetric(float64(len(m.comments)), "comments")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.placeComments(idx.rows)
	}
}

// BenchmarkRebuildStream is what 23 call sites do: geometry, placement, and the
// comment index, plus the cursor bookkeeping after it.
func BenchmarkRebuildStream(b *testing.B) {
	m := benchModel(b, nil)
	m.comments = benchComments(m.filtered, benchCommentCount(b))
	b.ReportMetric(float64(len(m.stream.rows)), "rows")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.rebuildStream()
	}
}

// BenchmarkCommentEntries is the left column's index, rebuilt with the stream.
func BenchmarkCommentEntries(b *testing.B) {
	m := benchModel(b, nil)
	m.comments = benchComments(m.filtered, benchCommentCount(b))
	m.rebuildStream()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.commentEntries(m.stream)
	}
}

// BenchmarkRenderBody is one frame. Expected to be flat in the size of the
// change — only visible rows are styled — so a big number here means that
// property has been lost somewhere.
func BenchmarkRenderBody(b *testing.B) {
	m := benchModel(b, benchComments(benchFiles(b), benchCommentCount(b)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.Body(160, 46)
	}
}

// BenchmarkRefreshTick is the two-second tick with nothing changed: the case that
// has to cost nothing, since it is by far the most common thing the viewer does.
func BenchmarkRefreshTick(b *testing.B) {
	comments := benchComments(benchFiles(b), benchCommentCount(b))
	m := benchModel(b, comments)
	m.LoadComments = func() ([]review.Comment, error) { return comments, nil }
	m.LoadThreads = func() ([]review.Thread, error) { return nil, nil }
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.reloadComments()
		m.reloadThreads()
	}
}

// BenchmarkScrollHalfPage is a keypress: the clamping and follow work that runs
// on the update path, without a rebuild.
func BenchmarkScrollHalfPage(b *testing.B) {
	m := benchModel(b, benchComments(benchFiles(b), benchCommentCount(b)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.moveCursor(23)
	}
}

// benchThreads is n mirrored GitHub threads with bodies long enough to occupy
// many rows each — what `T` → "all threads" puts on screen.
func benchThreads(files []diff.FileDiff, n int) []review.Thread {
	if n <= 0 || len(files) == 0 {
		return nil
	}
	body := "This looks wrong to me. The error is swallowed here, so a failure " +
		"upstream shows up much later as a nil dereference somewhere unrelated, " +
		"which is the worst kind of bug to chase. Suggest wrapping it and " +
		"returning, and adding a test that exercises the failure path."
	out := make([]review.Thread, 0, n)
	for i := 0; i < n; i++ {
		f := files[i%len(files)]
		if len(f.Hunks) == 0 {
			continue
		}
		h := f.Hunks[0]
		out = append(out, review.Thread{
			ID: fmt.Sprintf("T%d", i), Path: pathOf(f), Side: review.SideNew,
			Line: h.NewStart + (i % max(1, len(h.Lines))),
			Comments: []review.ThreadComment{
				{Author: "reviewer", Body: body},
				{Author: "andrewcohen", Body: "Fair — will fix."},
			},
		})
	}
	return out
}

// BenchmarkRenderBodyOverThreads is a frame whose visible rows are mostly comment
// rows, which is what a diff looks like with every thread shown.
//
// This is the scroll cost the user feels, and it is where the quadratic lives:
// renderStreamRow asks commentLines for a comment's *whole* block and then keeps
// one line of it, so a conversation H rows tall is rendered H times per screen.
func BenchmarkRenderBodyOverThreads(b *testing.B) {
	m := benchModel(b, nil)
	m.SetThreads(benchThreads(m.filtered, benchInt(b, benchCommentsEnv, 20)))
	m.threadVisibility = ThreadsAll
	m.rebuildStream()
	// Park the viewport on the first conversation, so the visible rows are the
	// comment block rather than code.
	for i, r := range m.stream.rows {
		if isCommentRow(r.kind) {
			m.cursorRow = i
			m.followCursor()
			break
		}
	}
	commentRowsOnScreen := 0
	end := min(len(m.stream.rows), m.streamScroll+46)
	for i := m.streamScroll; i < end; i++ {
		if isCommentRow(m.stream.rows[i].kind) {
			commentRowsOnScreen++
		}
	}
	b.ReportMetric(float64(commentRowsOnScreen), "comment_rows_visible")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.Body(160, 46)
	}
}

// BenchmarkScrollOverThreads is the keypress itself, repeated down through a
// conversation — cursor move plus the frame it forces.
func BenchmarkScrollOverThreads(b *testing.B) {
	m := benchModel(b, nil)
	m.SetThreads(benchThreads(m.filtered, benchInt(b, benchCommentsEnv, 20)))
	m.threadVisibility = ThreadsAll
	m.rebuildStream()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.moveCursor(1)
		_ = m.Body(160, 46)
	}
}

// BenchmarkRenderBodyInsideALongThread parks the viewport in the middle of one
// long conversation, so every visible row belongs to the same comment block.
//
// This is the shape that exposes the quadratic: renderStreamRow asks
// commentLines to build the comment's *entire* block — wrapping and styling every
// row of it — and then keeps one line. A conversation H rows tall therefore costs
// H work per visible row, or H × screen-height per frame. Long reviewer threads
// (a bot dumping a suggestion, a back-and-forth of five messages) are exactly
// where H gets big, and `T` → all threads is what puts them all on screen at once.
func BenchmarkRenderBodyInsideALongThread(b *testing.B) {
	m := benchModel(b, nil)
	files := m.filtered
	if len(files) == 0 || len(files[0].Hunks) == 0 {
		b.Skip("fixture has no hunks")
	}
	para := "The error is swallowed here, so a failure upstream surfaces much " +
		"later as a nil dereference somewhere unrelated. Wrap it and return."
	msgs := make([]review.ThreadComment, 0, 6)
	for i := 0; i < 6; i++ {
		body := para
		for j := 0; j < 4; j++ {
			body += "\n\n" + para
		}
		msgs = append(msgs, review.ThreadComment{Author: "reviewer", Body: body})
	}
	h := files[0].Hunks[0]
	m.SetThreads([]review.Thread{{
		ID: "T-long", Path: pathOf(files[0]), Side: review.SideNew,
		Line: h.NewStart, Comments: msgs,
	}})
	m.threadVisibility = ThreadsAll
	m.rebuildStream()

	first, count := -1, 0
	for i, r := range m.stream.rows {
		if isCommentRow(r.kind) {
			if first < 0 {
				first = i
			}
			count++
		}
	}
	if first < 0 {
		b.Fatal("fixture is wrong: the thread did not place")
	}
	b.ReportMetric(float64(count), "thread_rows")
	m.cursorRow = first + count/2
	m.followCursor()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.Body(160, 46)
	}
}

// AWP_BENCH_THREADS names a mirrored threads.json — the real one, from
// ~/.awp/reviews/<repo>/work-<workspace>/remote/threads.json — so a benchmark can
// hold the conversation volume that is actually on screen.
const (
	benchThreadsEnv = "AWP_BENCH_THREADS"
	// benchNoCacheEnv disables the per-frame block cache, so the same fixture can
	// be measured with and without it.
	benchNoCacheEnv = "AWP_BENCH_NO_CACHE"
)

func benchThreadsFromEnv(tb testing.TB) []review.Thread {
	tb.Helper()
	path := os.Getenv(benchThreadsEnv)
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("read %s=%s: %v", benchThreadsEnv, path, err)
	}
	var out []review.Thread
	if err := json.Unmarshal(raw, &out); err != nil {
		tb.Fatalf("parse %s: %v", path, err)
	}
	return out
}

// BenchmarkFrameWithRealThreads is the reported case: the captured diff, the
// captured threads, every one of them shown.
//
// It reports bytes per frame as well as time, because those are two different
// costs and only one of them is ours. A frame that is quick to build but large to
// write is bound by the terminal — every comment row is painted the full width, so
// it carries a styled run whether or not it has text, and scrolling changes every
// line on screen, which is precisely when Bubble Tea's line diffing can skip
// nothing.
func BenchmarkFrameWithRealThreads(b *testing.B) {
	m := benchModel(b, nil)
	if ts := benchThreadsFromEnv(b); len(ts) > 0 {
		m.SetThreads(ts)
	} else {
		m.SetThreads(benchThreads(m.filtered, 16))
	}
	m.threadVisibility = ThreadsAll
	m.rebuildStream()

	comment, total := 0, 0
	for _, r := range m.stream.rows {
		total++
		if isCommentRow(r.kind) {
			comment++
		}
	}
	b.ReportMetric(float64(total), "rows")
	b.ReportMetric(float64(comment), "comment_rows")

	// Park where the conversations are, which is where the scrolling hurts.
	for i, r := range m.stream.rows {
		if isCommentRow(r.kind) {
			m.cursorRow = i
			m.followCursor()
			break
		}
	}
	b.ReportMetric(float64(len(m.Body(160, 46))), "bytes/frame")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.Body(160, 46)
	}
}

// BenchmarkScrollThroughRealThreads walks the cursor down through them, one
// keypress and one frame per iteration — the gesture that is reported as slow.
func BenchmarkScrollThroughRealThreads(b *testing.B) {
	m := benchModel(b, nil)
	if ts := benchThreadsFromEnv(b); len(ts) > 0 {
		m.SetThreads(ts)
	} else {
		m.SetThreads(benchThreads(m.filtered, 16))
	}
	m.threadVisibility = ThreadsAll
	m.rebuildStream()
	for i, r := range m.stream.rows {
		if isCommentRow(r.kind) {
			m.cursorRow = i
			m.followCursor()
			break
		}
	}
	bytes := 0
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.moveCursor(1)
		bytes += len(m.Body(160, 46))
	}
	b.StopTimer()
	if b.N > 0 {
		b.ReportMetric(float64(bytes)/float64(b.N), "bytes/frame")
	}
}

// BenchmarkDeckBodyPad measures what the deck does to the viewer's body on every
// frame: pads it out to the terminal width with lipgloss.
//
//	body = lipgloss.NewStyle().Width(m.width).Render(body)   // deckui/model.go
//
// That is a lipgloss Render over the entire frame, so it re-parses every escape
// sequence in it. Its cost is therefore a function of how ANSI-dense the body is
// rather than of how many rows it has — and a comment row is far denser than a
// code row, being painted the full width with a background fill. Which is the
// shape of the reported symptom exactly: fast on a huge diff, slow as soon as
// conversations are on screen.
func BenchmarkDeckBodyPad(b *testing.B) {
	const width = 160
	build := func(b *testing.B, withThreads bool) string {
		m := benchModel(b, nil)
		if withThreads {
			if ts := benchThreadsFromEnv(b); len(ts) > 0 {
				m.SetThreads(ts)
			} else {
				m.SetThreads(benchThreads(m.filtered, 16))
			}
			m.threadVisibility = ThreadsAll
			m.rebuildStream()
			for i, r := range m.stream.rows {
				if isCommentRow(r.kind) {
					m.cursorRow = i
					m.followCursor()
					break
				}
			}
		}
		return m.Body(width, 46)
	}
	for _, tc := range []struct {
		name    string
		threads bool
	}{{"code", false}, {"threads", true}} {
		b.Run(tc.name, func(b *testing.B) {
			body := build(b, tc.threads)
			b.ReportMetric(float64(len(body)), "bytes/body")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = lipgloss.NewStyle().Width(width).Render(body)
			}
		})
	}
}
