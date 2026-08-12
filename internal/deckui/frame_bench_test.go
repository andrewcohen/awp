package deckui

import (
	"encoding/json"
	"os"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/andrewcohen/awp/internal/review"
)

// Measuring a whole deck frame with the diff modal open.
//
// A trace of a real session (AWP_TRACE=1) says the median frame is 18.7ms, of
// which the viewer's body is 2.0ms and the modal's panel pad 0.3ms — leaving
// 16.4ms in this package's View, with a 0.1ms gap between frames, so it is CPU
// here and not the terminal. Row-list frames are 2.3ms through the same footer
// and padding code, which points at the size and escape-density of the body being
// handed to those steps rather than at the steps themselves.
//
// Colour needs no forcing under lipgloss v2: Render always emits full-fidelity
// escapes and downsampling happens at the output layer, so the escape density
// the benchmark is measuring is present with or without a TTY.

const (
	frameDiffEnv    = "AWP_BENCH_DIFF"
	frameThreadsEnv = "AWP_BENCH_THREADS"
)

func frameDiffText(tb testing.TB) string {
	tb.Helper()
	path := os.Getenv(frameDiffEnv)
	if path == "" {
		return diffModalSample
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("read %s=%s: %v", frameDiffEnv, path, err)
	}
	return string(raw)
}

func frameThreads(tb testing.TB) []review.Thread {
	tb.Helper()
	path := os.Getenv(frameThreadsEnv)
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("read %s=%s: %v", frameThreadsEnv, path, err)
	}
	var out []review.Thread
	if err := json.Unmarshal(raw, &out); err != nil {
		tb.Fatalf("parse %s: %v", path, err)
	}
	return out
}

// frameDeck opens the diff modal on the fixture and scrolls into the
// conversations, driving it through real key messages so the state it ends in is
// one the app can actually be in.
func frameDeck(tb testing.TB, showThreads bool) Model {
	tb.Helper()
	text := frameDiffText(tb)
	threads := frameThreads(tb)
	m := New([]Item{{
		ProjectName:   "alpha",
		WorkspaceName: "404-alert",
		RepoRoot:      "/repo",
		Path:          "/repo/ws",
		PRNumber:      2335,
	}}, func(ActionRequest) error { return nil }).
		WithDiffViewer(func(Item, DiffScope, int) (string, error) { return text, nil }, nil).
		WithReviewStore(CommentStore{
			LoadThreads: func(Item) ([]review.Thread, error) { return threads, nil },
		})
	m.width, m.height = 200, 60
	m, cmd := pressKey(m, "c")
	m = drain(m, cmd)
	if _, ok := m.active.(*diffModal); !ok {
		tb.Fatal("expected the diff modal open")
	}
	if showThreads {
		// `T` cycles unresolved → all, which is the state the slowness is in.
		m, _ = pressKey(m, "T")
		// Then down into them. The threads sit a few pages into the first files.
		for i := 0; i < 40; i++ {
			m, _ = pressKey(m, "ctrl+d")
		}
	}
	return m
}

// BenchmarkDeckFrameOverCode is the case reported as fast: a frame with no
// conversation on screen.
func BenchmarkDeckFrameOverCode(b *testing.B) {
	m := frameDeck(b, false)
	b.ReportMetric(float64(len(m.render())), "bytes")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.render()
	}
}

// BenchmarkDeckFrameOverThreads is the case reported as slow.
func BenchmarkDeckFrameOverThreads(b *testing.B) {
	m := frameDeck(b, true)
	b.ReportMetric(float64(len(m.render())), "bytes")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.render()
	}
}

// BenchmarkFrameSteps times the individual full-frame lipgloss passes View makes
// after the body is composed, which is where the trace says the time goes.
func BenchmarkFrameSteps(b *testing.B) {
	m := frameDeck(b, true)
	bm, ok := m.active.(bodyModal)
	if !ok {
		b.Fatal("expected a body modal")
	}
	body, _ := bm.view(&m)
	footer := lipgloss.NewStyle().Padding(1, 1, 1, 1).Render(
		composeStatusBar(m.activities, m.spinner.View(), "", "", m.width-2))
	b.ReportMetric(float64(len(body)), "body_bytes")

	b.Run("widthpad", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = lipgloss.NewStyle().Width(m.width).Render(body)
		}
	})
	padded := lipgloss.NewStyle().Width(m.width).Render(body)
	b.Run("height", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = lipgloss.Height(padded)
		}
	})
	b.Run("joinvertical", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = lipgloss.JoinVertical(lipgloss.Left, padded, footer)
		}
	})
}
