package deckui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/andrewcohen/awp/internal/review"
)

// Replying to a GitHub thread, through the deck.
//
// The viewer posts the reply with a tea.Cmd and records the outcome when the
// resulting message comes back. Inside the deck that message has to travel out
// through the modal to Bubble Tea and back in through the deck's own Update — so a
// reply can reach GitHub and still leave the local record saying "unsent", which is
// exactly what happened on a real PR. The viewer's own tests cannot see that: they
// hand the message straight back to the model that produced it.

// The diff the fixture reviews, with a line for the thread to hang on.
const threadReplyDiff = `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1,2 +1,2 @@
 alpha
-beta
+gamma
`

func TestAReplyPostedFromTheDeckIsRecordedAsPublished(t *testing.T) {
	var saved []review.Comment
	var updated []review.Comment
	var marked []review.Comment
	postedTo := ""

	store := CommentStore{
		Load: func(Item) ([]review.Comment, error) { return saved, nil },
		Save: func(_ Item, c review.Comment) error {
			c.ID = "local-1"
			saved = append(saved, c)
			return nil
		},
		// What the CLI wires: the record the store just wrote, id included. The reply's
		// outcome is recorded against that id, so a seam that loses it loses the record.
		LastSaved: func() (review.Comment, bool) {
			if len(saved) == 0 {
				return review.Comment{}, false
			}
			return saved[len(saved)-1], true
		},
		// Faithful to what the CLI actually does: a revise keeps everything the editor
		// does not own, so only the body is copied off the incoming record. Anything
		// that needs to change a comment's *state* cannot go through here — which is
		// the whole reason this test exists.
		Update: func(_ Item, c review.Comment) error {
			for i := range saved {
				if saved[i].ID == c.ID {
					saved[i].Body = c.Body
					updated = append(updated, saved[i])
					return nil
				}
			}
			updated = append(updated, c)
			return nil
		},
		MarkPublished: func(_ Item, id, remoteID string) error {
			for i := range saved {
				if saved[i].ID != id {
					continue
				}
				saved[i].State = review.Published
				saved[i].Publish = &review.PublishRecord{ThreadID: remoteID}
				marked = append(marked, saved[i])
				return nil
			}
			return nil
		},
		LoadThreads: func(Item) ([]review.Thread, error) {
			return []review.Thread{{
				ID: "PRRT_1", Path: "a.go", Side: review.SideNew, Line: 1,
				Comments: []review.ThreadComment{{ID: "PRRC_a", Author: "alice", Body: "why?"}},
			}}, nil
		},
		ReplyToThread: func(_ Item, threadID, _ string) (string, error) {
			postedTo = threadID
			return "PRRC_new", nil
		},
	}

	m := diffModalModel(t, func(Item, DiffScope) (string, error) { return threadReplyDiff, nil })
	m = m.WithReviewStore(store)
	m, cmd := pressKey(m, "c")
	m = drain(m, cmd)
	if _, ok := m.active.(*diffModal); !ok {
		t.Fatalf("expected the diff modal, got %T", m.active)
	}

	// Onto the thread's row, then reply to it. Found by trying the gesture rather
	// than by counting rows: the box names where it will post, which is the only
	// signal here that does not depend on the stream's exact geometry.
	m, opened := seekAndOpenThreadReply(t, m)
	if !opened {
		t.Fatal("never reached the mirrored thread's row")
	}
	for _, r := range "fixed" {
		m, _ = pressKey(m, string(r))
	}
	up, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = up.(Model)
	if cmd == nil {
		t.Fatal("expected saving the reply to produce the post")
	}
	if len(saved) != 1 || !saved[0].ThreadReply() {
		t.Fatalf("expected the draft filed against the thread, got %+v", saved)
	}

	// The post's result, delivered the way Bubble Tea delivers it: back into the
	// deck's Update, not into the viewer's.
	msg := cmd()
	up, _ = m.Update(msg)
	m = up.(Model)

	if postedTo != "PRRT_1" {
		t.Fatalf("expected the reply posted into PRRT_1, got %q", postedTo)
	}
	// The record has to say it went out. Left open, the reply reads as unsent
	// forever — beside a thread on GitHub that already has it.
	if len(marked) != 1 {
		t.Fatalf("expected the reply marked published once, got %d: %+v", len(marked), marked)
	}
	if marked[0].State != review.Published {
		t.Fatalf("expected the reply marked published, got %q", marked[0].State)
	}
	if marked[0].Publish == nil || marked[0].Publish.ThreadID != "PRRC_new" {
		t.Fatalf("expected GitHub's id recorded, got %+v", marked[0].Publish)
	}
	// Not through the revise seam, which by design keeps the stored state and would
	// throw the publish record away.
	for _, u := range updated {
		if u.State == review.Published {
			t.Fatal("the publish record went through the revise path, which discards it")
		}
	}
	// The record on disk is what the next frame reads, so that is what must say it.
	if saved[0].State != review.Published || saved[0].Publish == nil {
		t.Fatalf("the stored record still reads unsent: %+v", saved[0])
	}
	// And the status says it landed, rather than reporting a failure.
	dm, ok := m.active.(*diffModal)
	if !ok {
		t.Fatalf("expected the modal still open, got %T", m.active)
	}
	if status, isErr := dm.inner.Status(); isErr || !strings.Contains(status, "replied") {
		t.Fatalf("expected a posted reply reported, got %q (isErr=%v)", status, isErr)
	}
}

// seekAndOpenThreadReply walks the cursor down until `c` opens a reply-to-GitHub box.
//
// By what the box says rather than by a row count: "reply on github" is the header
// only a thread reply gets, so this cannot be fooled by landing on a diff line or on
// a local comment.
func seekAndOpenThreadReply(t *testing.T, m Model) (Model, bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		m, _ = pressKey(m, "c")
		if strings.Contains(stripStyle(m.render()), "reply on github") {
			return m, true
		}
		// Whatever it opened instead, close it and move on.
		up, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
		m = up.(Model)
		m, _ = pressKey(m, "j")
	}
	return m, false
}

// stripStyle drops ANSI so the probe matches on text.
func stripStyle(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
