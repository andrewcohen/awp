package deckui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// watchFixture writes a workspace with a transcript the watch modal can find,
// and returns the Item pointing at it.
func watchFixture(t *testing.T, lines string) (Item, string) {
	t.Helper()
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".awp"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"dev_loop":{"gates":[{"name":"test","command":"go test ./..."}]}}`
	if err := os.WriteFile(filepath.Join(repo, ".awp", "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(transcript, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	return Item{
		ProjectName: "proj", WorkspaceName: "ws",
		Path: repo, RepoRoot: repo, Status: "working",
	}, transcript
}

// Opening the modal must not read the transcript on the way in. The parse is
// hundreds of milliseconds on a long session — measured at 675ms for a 97MB
// transcript — and doing it inline froze the deck on every `w`.
//
// Asserted as "the first frame is the placeholder": if the constructor went to
// the filesystem, the body would already be a rendered dev loop.
func TestOpeningTheWatchModalDoesNotParseInline(t *testing.T) {
	item, _ := watchFixture(t, "")
	wm := newWatchModal(item)
	if got := wm.vp.GetContent(); !strings.Contains(got, "reading the agent's transcript") {
		t.Errorf("the modal opened showing %q, want the placeholder — the constructor did the work inline", got)
	}
	if wm.transcript != "" {
		t.Errorf("the constructor resolved a transcript (%q); locating is the Cmd's job", wm.transcript)
	}
}

// The refresh Cmd is what does the reading, and it hands the result back as a
// message rather than writing to the modal — otherwise it would be mutating
// state the main loop is rendering from.
func TestTheRefreshCmdReportsRatherThanMutates(t *testing.T) {
	item, transcript := watchFixture(t, "")
	wm := newWatchModal(item)
	wm.transcript = transcript

	msg, ok := wm.refresh()().(watchFrameMsg)
	if !ok {
		t.Fatal("refresh did not produce a watchFrameMsg")
	}
	if wm.stamp != (watchStamp{}) {
		t.Error("refresh wrote the stamp onto the modal; apply is the only writer")
	}
	wm.apply(msg)
	if wm.stamp == (watchStamp{}) {
		t.Error("apply did not record the stamp, so the next tick cannot skip an idle transcript")
	}
}

// A transcript the agent has not touched is not re-parsed. Without this the
// modal burns a full parse every second for as long as it is open, whether or
// not anything changed.
func TestAnIdleTranscriptIsNotReparsed(t *testing.T) {
	item, transcript := watchFixture(t, "")
	wm := newWatchModal(item)
	wm.transcript = transcript
	wm.apply(wm.refresh()().(watchFrameMsg))

	second, ok := wm.refresh()().(watchFrameMsg)
	if !ok {
		t.Fatal("the second refresh did not produce a watchFrameMsg")
	}
	if !second.unchanged {
		t.Error("an untouched transcript was parsed again; the stamp check is not working")
	}

	// And an appended line brings it back.
	f, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{\"type\":\"user\"}\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	// Filesystem mtime can be coarse; make the size difference decisive.
	if err := os.Chtimes(transcript, time.Now(), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	third, ok := wm.refresh()().(watchFrameMsg)
	if !ok {
		t.Fatal("the third refresh did not produce a watchFrameMsg")
	}
	if third.unchanged {
		t.Error("the transcript grew and the modal still skipped the parse")
	}
}

// The tick re-arms only after a frame lands, so a parse slower than the
// interval cannot queue a second one behind it.
func TestTheTickReArmsOnlyAfterAFrame(t *testing.T) {
	item, transcript := watchFixture(t, "")
	wm := newWatchModal(item)
	wm.transcript = transcript
	m := New(nil, nil)

	if cmd := wm.update(&m, watchTickMsg(time.Now())); cmd == nil {
		t.Fatal("a tick produced no command at all")
	} else if _, isFrame := cmd().(watchFrameMsg); !isFrame {
		t.Error("a tick scheduled another tick instead of a rebuild")
	}
	if cmd := wm.update(&m, watchFrameMsg{header: "h", body: "b"}); cmd == nil {
		t.Error("a delivered frame did not re-arm the tick")
	}
}
