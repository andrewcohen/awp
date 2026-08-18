package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A transcript is someone else's format, appended to by a program that keeps
// changing, so the parser's job is to get the conversation out and to survive
// everything else in the file rather than to model all of it.
func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("writing the transcript: %v", err)
	}
	return path
}

func TestAToolResultLandsOnTheCallThatAskedForIt(t *testing.T) {
	// The result arrives on a later line than the call, and an agent runs the
	// same command more than once — so matching on anything but the id
	// eventually staples the wrong output to the wrong call.
	path := writeTranscript(t,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"a","name":"Bash","input":{"command":"go test ./..."}},{"type":"tool_use","id":"b","name":"Bash","input":{"command":"go test ./..."}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"b","content":"second","is_error":true}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"a","content":"first"}]}}`,
	)

	turns, err := readTurns(path)
	if err != nil {
		t.Fatalf("readTurns: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("want one turn holding both calls, got %d", len(turns))
	}
	if got := turns[0].Tools[0].Detail; got != "first" {
		t.Errorf("first call got %q, want the result with its own id", got)
	}
	if got := turns[0].Tools[1].Detail; got != "second" {
		t.Errorf("second call got %q", got)
	}
	if !turns[0].Tools[1].IsError || turns[0].Tools[0].IsError {
		t.Error("the error flag followed the wrong call")
	}
}

func TestAHarnessReplyIsNotAMessage(t *testing.T) {
	// A user line carrying only tool results is the harness answering the agent.
	// Rendered as a turn it puts an empty bubble between every call and its
	// output, which is most of a coding session.
	path := writeTranscript(t,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"a","name":"Read","input":{"file_path":"main.go"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"a","content":"package main"}]}}`,
		`{"type":"user","message":{"content":"and now the real question"}}`,
	)

	turns, err := readTurns(path)
	if err != nil {
		t.Fatalf("readTurns: %v", err)
	}
	if len(turns) != 2 {
		t.Fatalf("want the call and the question, got %d turns", len(turns))
	}
	if turns[1].Text != "and now the real question" {
		t.Errorf("a plain string message did not parse: %q", turns[1].Text)
	}
}

func TestLinesThisProjectionDoesNotModelAreSkipped(t *testing.T) {
	// Transcripts carry summaries, meta entries and whatever Claude Code adds
	// next. Refusing to parse the file over one unknown line would mean the chat
	// breaks on an upgrade nobody made.
	path := writeTranscript(t,
		`{"type":"summary","summary":"whatever this is"}`,
		`not json at all`,
		`{"type":"system","message":{"content":"noise"}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"still here"}]}}`,
	)

	turns, err := readTurns(path)
	if err != nil {
		t.Fatalf("readTurns: %v", err)
	}
	if len(turns) != 1 || turns[0].Text != "still here" {
		t.Fatalf("want the one real turn, got %+v", turns)
	}
}

func TestAnEditBecomesAPatchOfWhatChanged(t *testing.T) {
	// The transcript stores an edit as the two strings the agent supplied. Shown
	// whole, a one-line change reads as a rewrite of the file; the point of the
	// patch is that the unchanged head and tail fall away.
	path := writeTranscript(t,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"e","name":"Edit","input":`+
			`{"file_path":"pane.go","old_string":"keep one\nold middle\nkeep two","new_string":"keep one\nnew middle\nkeep two"}}]}}`,
	)

	turns, err := readTurns(path)
	if err != nil {
		t.Fatalf("readTurns: %v", err)
	}
	patch := turns[0].Tools[0].Patch
	for _, want := range []string{"--- a/pane.go", "+++ b/pane.go", "-old middle", "+new middle", " keep one", " keep two"} {
		if !strings.Contains(patch, want) {
			t.Errorf("patch is missing %q:\n%s", want, patch)
		}
	}
	if strings.Contains(patch, "-keep one") {
		t.Errorf("an unchanged line was reported as removed:\n%s", patch)
	}
}

func TestAWriteIsADiffAgainstNothing(t *testing.T) {
	path := writeTranscript(t,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"w","name":"Write","input":`+
			`{"file_path":"new.txt","content":"first\nsecond"}}]}}`,
	)

	turns, err := readTurns(path)
	if err != nil {
		t.Fatalf("readTurns: %v", err)
	}
	patch := turns[0].Tools[0].Patch
	if !strings.Contains(patch, "+first") || !strings.Contains(patch, "+second") {
		t.Errorf("a new file should be all additions:\n%s", patch)
	}
	if strings.Contains(patch, "\n-") {
		t.Errorf("a new file removed something:\n%s", patch)
	}
}

func TestTheConversationIsTrimmedToItsTail(t *testing.T) {
	// A long session is tens of thousands of lines and all of it would cross the
	// bridge on every change.
	lines := make([]string, 0, maxTurns+50)
	for i := 0; i < maxTurns+50; i++ {
		lines = append(lines, `{"type":"assistant","message":{"content":[{"type":"text","text":"turn"}]}}`)
	}
	turns, err := readTurns(writeTranscript(t, lines...))
	if err != nil {
		t.Fatalf("readTurns: %v", err)
	}
	if len(turns) != maxTurns {
		t.Errorf("kept %d turns, want the last %d", len(turns), maxTurns)
	}
}
