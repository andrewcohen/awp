package github

import "testing"

// A thread with no line is a remark about the whole file, and GitHub reads that
// from the *absence* of the field. Sending line: 0 is not the same statement —
// it is a line number, and zero is not one.
//
// Settled against a real PR rather than off the schema: both fields are nullable
// there, but nullable in the schema is not the same as accepted by the resolver,
// and GraphQL has no subject_type field at all — that is REST's spelling for the
// same idea. Staged PENDING against a live PR it came back subjectType FILE. This
// test is what keeps the shape that probe established from drifting, since
// nothing in CI will ever ask GitHub again.
func TestAThreadWithNoLineOmitsTheLineEntirely(t *testing.T) {
	got := draftThreadVars([]DraftThread{{Path: "a.go", Side: "RIGHT", Body: "wrong package"}})
	if len(got) != 1 {
		t.Fatalf("got %d threads, want 1", len(got))
	}
	if _, ok := got[0]["line"]; ok {
		t.Errorf("a file-level thread sent a line: %v", got[0])
	}
	// side survives, and is also accepted without a line. It is what lets a remark
	// about a deleted file say which side it is about.
	if got[0]["side"] != "RIGHT" {
		t.Errorf("the side was dropped: %v", got[0])
	}
	if got[0]["path"] != "a.go" || got[0]["body"] != "wrong package" {
		t.Errorf("unexpected thread: %v", got[0])
	}
	// A range's start is keyed off the line, so a thread with no line must not
	// somehow acquire one.
	if _, ok := got[0]["startLine"]; ok {
		t.Errorf("a file-level thread sent a startLine: %v", got[0])
	}
}

// A thread with a line still sends it, which is every other comment on the PR.
func TestAThreadWithALineStillSendsIt(t *testing.T) {
	got := draftThreadVars([]DraftThread{{Path: "a.go", Line: 12, Side: "LEFT", Body: "off by one"}})
	if got[0]["line"] != 12 {
		t.Errorf("the line was dropped: %v", got[0])
	}
	if got[0]["side"] != "LEFT" {
		t.Errorf("the side was dropped: %v", got[0])
	}
}

// And a range keeps both ends, with the start's side spelled out — GitHub
// defaults startSide to the pull request's side rather than to the side already
// given for the end, so a range on the old side would otherwise lose its start.
func TestARangeSendsBothEndsAndBothSides(t *testing.T) {
	got := draftThreadVars([]DraftThread{{Path: "a.go", Line: 18, StartLine: 12, Side: "LEFT", Body: "this block"}})
	if got[0]["line"] != 18 || got[0]["startLine"] != 12 {
		t.Errorf("the range did not survive: %v", got[0])
	}
	if got[0]["startSide"] != "LEFT" {
		t.Errorf("the start lost its side: %v", got[0])
	}
}

// An unset side defaults to the new one, which is where a remark about code as it
// now stands belongs.
func TestAThreadWithNoSideDefaultsToTheNewSide(t *testing.T) {
	for _, th := range []DraftThread{
		{Path: "a.go", Line: 12, Body: "b"},
		{Path: "a.go", Body: "b"},
	} {
		if got := draftThreadVars([]DraftThread{th}); got[0]["side"] != "RIGHT" {
			t.Errorf("line=%d: side is %v, want RIGHT", th.Line, got[0]["side"])
		}
	}
}
