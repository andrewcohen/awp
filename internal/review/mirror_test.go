package review

import (
	"testing"
	"time"
)

// MirrorOf is how anything reading both records knows which pairs are one
// conversation. The diff drew every published comment twice before it existed,
// and `awp review list` reported four resolved-and-replied threads as four open
// findings.

func published(id, ghID string) Comment {
	return Comment{
		ID: id, Author: AuthorHuman, Body: "this leaks", State: Published,
		Anchor:  Anchor{Path: "a.go", Side: SideNew, LineHint: 2, Text: "beta"},
		Publish: &PublishRecord{ThreadID: ghID, At: time.Unix(0, 0)},
	}
}

func mirrored(threadID, ghCommentID string) Thread {
	return Thread{
		ID: threadID, Path: "a.go", Side: SideNew, Line: 2,
		Comments: []ThreadComment{{ID: ghCommentID, Author: "andrewcohen", Body: "this leaks"}},
	}
}

func TestMirrorOfPairsOnTheNodeID(t *testing.T) {
	got := MirrorOf([]Comment{published("c1", "PRRC_1")}, []Thread{mirrored("T1", "PRRC_1")})
	if len(got) != 1 || got["c1"].ID != "T1" {
		t.Fatalf("expected c1 → T1, got %v", got)
	}
}

// Matched on the id, not the body or the line: GitHub recomputes a thread's line
// as the PR moves (one filed against 47 came back at 53), and editing a published
// comment locally changes its text.
func TestMirrorOfIgnoresTheBodyAndLine(t *testing.T) {
	c := published("c1", "PRRC_1")
	c.Body = "edited since publishing"
	th := mirrored("T1", "PRRC_1")
	th.Line = 99
	th.Comments[0].Body = "what was published"

	if got := MirrorOf([]Comment{c}, []Thread{th}); len(got) != 1 {
		t.Fatalf("expected the pairing to survive a differing body and line, got %v", got)
	}
}

// A mirror written before the ids were carried says nothing about identity, so
// nothing is matched — showing a duplicate beats hiding a remark on a guess.
func TestMirrorOfIgnoresAMirrorWithoutIDs(t *testing.T) {
	if got := MirrorOf([]Comment{published("c1", "PRRC_1")}, []Thread{mirrored("T1", "")}); len(got) != 0 {
		t.Fatalf("expected no match without ids, got %v", got)
	}
}

// Parents only. A reply is never published, so it has no id to match on and
// belongs to whichever copy of the conversation ends up being drawn.
func TestMirrorOfSkipsReplies(t *testing.T) {
	reply := published("c2", "PRRC_1")
	reply.ReplyTo = "c1"
	if got := MirrorOf([]Comment{reply}, []Thread{mirrored("T1", "PRRC_1")}); len(got) != 0 {
		t.Fatalf("expected a reply left unpaired, got %v", got)
	}
}

// The review summary's publish record holds the *review* id, which is not a
// comment id and can never match a thread's — so a summary is never mistaken for
// an echo of one.
func TestMirrorOfDoesNotPairAReviewSummary(t *testing.T) {
	summary := Comment{
		ID: "s1", Author: "agent", Body: "reviewed the publish path", State: Published,
		Publish: &PublishRecord{ThreadID: "PRR_1", At: time.Unix(0, 0)},
	}
	if got := MirrorOf([]Comment{summary}, []Thread{mirrored("T1", "PRRC_1")}); len(got) != 0 {
		t.Fatalf("expected no match for a review-level remark, got %v", got)
	}
}

// An unpublished comment has no counterpart, so nothing is paired with it.
func TestMirrorOfLeavesADraftAlone(t *testing.T) {
	draft := Comment{ID: "c1", Author: AuthorHuman, Body: "still a draft", State: Open}
	if got := MirrorOf([]Comment{draft}, []Thread{mirrored("T1", "PRRC_1")}); len(got) != 0 {
		t.Fatalf("expected a draft left unpaired, got %v", got)
	}
}

// A published comment the mirror does not hold — the fetch failed, or the mirror
// is behind — pairs with nothing rather than being paired on a guess.
func TestMirrorOfHandlesAMissingThread(t *testing.T) {
	if got := MirrorOf([]Comment{published("c1", "PRRC_1")}, nil); len(got) != 0 {
		t.Fatalf("expected no pairing with no mirror, got %v", got)
	}
}
