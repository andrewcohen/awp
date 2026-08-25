package watch

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// appendLines adds lines to an existing transcript, the way an agent does.
func appendLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		t.Fatal(err)
	}
}

// session is a transcript with enough in it to exercise every axis the fold
// carries across a resume: tasks created and updated (so the unit boundary and
// its reset fire), a gate that passed and one that failed, and prose.
func session() []string {
	return []string{
		line("assistant", txt("Looking around first.")),
		line("assistant", tu("TaskCreate", "c1", map[string]any{"subject": "first unit"})),
		line("assistant", tu("TaskCreate", "c2", map[string]any{"subject": "second unit"})),
		line("assistant", tu("TaskUpdate", "u1", map[string]any{"taskId": "1", "status": "in_progress"})),
		line("assistant", tu("Edit", "e1", map[string]any{"file_path": "/tmp/x.go"})),
		line("assistant", tu("Bash", "b1", map[string]any{"command": "go test ./..."})),
		line("user", tr("b1", false)),
		line("assistant", tu("Bash", "b2", map[string]any{"command": "go vet ./..."})),
		line("user", tr("b2", true)),
	}
}

// TestAResumedFoldSaysWhatAFullOneDoes is the invariant the whole change rests
// on: folding a transcript in pieces, as the deck does across refreshes, has to
// land on the same state as reading it once from the top. If it does not, the
// meta line quietly reports a different unit or a different gate depending only
// on when you happened to open the deck.
func TestAResumedFoldSaysWhatAFullOneDoes(t *testing.T) {
	lines := session()
	now := time.Now()
	loop := DefaultLoop()

	// Fold it a line at a time, re-deriving after each — which is also the
	// check that deriving does not disturb the fold.
	path := transcript(t, lines[0])
	r := NewReader(path)
	var incremental State
	for i, ln := range lines {
		if i > 0 {
			appendLines(t, path, ln)
		}
		var err error
		incremental, err = r.State(loop, "working", now)
		if err != nil {
			t.Fatalf("State after line %d: %v", i, err)
		}
	}

	whole, err := BuildState(loop, transcript(t, lines...), "working", now)
	if err != nil {
		t.Fatalf("BuildState: %v", err)
	}
	if !reflect.DeepEqual(incremental, whole) {
		t.Errorf("a resumed fold and a full one disagree:\n resumed %+v\n full    %+v", incremental, whole)
	}
}

// TestDerivingTwiceSaysTheSameThing. The derivation appends fallback todos and
// the gate list, so doing it on the accumulator rather than a copy would double
// them the second time the deck asked — which is every five seconds.
func TestDerivingTwiceSaysTheSameThing(t *testing.T) {
	path := transcript(t, session()...)
	r := NewReader(path)
	now := time.Now()
	first, err := r.State(DefaultLoop(), "working", now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.State(DefaultLoop(), "working", now)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("asking twice changed the answer:\n first  %+v\n second %+v", first, second)
	}
}

// BenchmarkRefresh is the deck's actual question — "what has this agent done?",
// asked every few seconds — against a transcript long enough for the difference
// to be the whole story. Run both to see it:
//
//	go test ./internal/watch -bench Refresh -benchtime 20x
//
// Resumed is one appended line's worth of work. Full is the file.
func BenchmarkRefresh(b *testing.B) {
	lines := make([]string, 0, 20000)
	for i := range 20000 {
		lines = append(lines, line("assistant", txt(strings.Repeat("some prose about the work ", 20)+string(rune('a'+i%26)))))
	}
	dir := b.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		b.Fatal(err)
	}
	loop := DefaultLoop()
	now := time.Now()

	b.Run("resumed", func(b *testing.B) {
		r := NewReader(path)
		if _, err := r.State(loop, "working", now); err != nil {
			b.Fatal(err)
		}
		one := lines[0] + "\n"
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			b.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		for b.Loop() {
			if _, err := f.WriteString(one); err != nil {
				b.Fatal(err)
			}
			if _, err := r.State(loop, "working", now); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("full", func(b *testing.B) {
		for b.Loop() {
			if _, err := BuildState(loop, path, "working", now); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// TestAHalfWrittenLineIsNotSkipped. The agent appends as it goes, so a read can
// land mid-line. Folding that fragment and moving the offset past it would drop
// the event permanently — the tool call would never be seen, and the row would
// be wrong for the rest of the session.
func TestAHalfWrittenLineIsNotSkipped(t *testing.T) {
	full := line("assistant", tu("TaskCreate", "c1", map[string]any{"subject": "only unit"}))
	path := filepath.Join(t.TempDir(), "session.jsonl")
	// Everything but the newline and the last few bytes.
	if err := os.WriteFile(path, []byte(full[:len(full)-5]), 0o600); err != nil {
		t.Fatal(err)
	}
	r := NewReader(path)
	st, err := r.State(DefaultLoop(), "working", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Todos) != 0 {
		t.Fatalf("a partial line was folded: %+v", st.Todos)
	}
	// The agent finishes the line.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(full[len(full)-5:] + "\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	st, err = r.State(DefaultLoop(), "working", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Todos) != 1 || st.Todos[0].Content != "only unit" {
		t.Errorf("the completed line was never folded: %+v", st.Todos)
	}
}

// TestATruncatedTranscriptIsFoldedAgain. A file shorter than the offset is not
// the file that offset was measured against, so resuming into it would fold new
// bytes on top of state describing content that is gone.
func TestATruncatedTranscriptIsFoldedAgain(t *testing.T) {
	path := transcript(t, session()...)
	r := NewReader(path)
	if _, err := r.State(DefaultLoop(), "working", time.Now()); err != nil {
		t.Fatal(err)
	}
	replacement := line("assistant", tu("TaskCreate", "c1", map[string]any{"subject": "a new session"}))
	if err := os.WriteFile(path, []byte(replacement+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := r.State(DefaultLoop(), "working", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Todos) != 1 || st.Todos[0].Content != "a new session" {
		t.Errorf("the fold did not restart on a truncated transcript: %+v", st.Todos)
	}
}

// TestNothingNewCostsNoRead. The reason the deck is quiet between an agent's
// writes: the size is compared to the offset and the file is not read at all.
func TestNothingNewCostsNoRead(t *testing.T) {
	path := transcript(t, session()...)
	r := NewReader(path)
	if _, err := r.State(DefaultLoop(), "working", time.Now()); err != nil {
		t.Fatal(err)
	}
	at := r.offset
	if at == 0 {
		t.Fatal("the first fold consumed nothing")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.State(DefaultLoop(), "working", time.Now()); err != nil {
		t.Errorf("a second pass over an unchanged transcript failed: %v", err)
	}
	if r.offset != at {
		t.Errorf("the offset moved from %d to %d over an unchanged file", at, r.offset)
	}
	if r.offset != info.Size() {
		t.Errorf("folded to offset %d, file is %d bytes", r.offset, info.Size())
	}
}
