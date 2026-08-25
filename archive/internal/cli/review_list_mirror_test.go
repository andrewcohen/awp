package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/andrewcohen/awp/internal/review"
	"github.com/andrewcohen/awp/internal/workspace"
)

// `review list` read the local record and nothing else, so it reported what the
// last publish knew and was blind to everything GitHub did afterwards. On alpha
// #2348 four resolved-and-replied conversations all listed as `published`,
// indistinguishable from the one still genuinely open.

// publishedFinding marks the review's finding as published under ghID, so the
// mirror has something to pair it with.
func publishedFinding(t *testing.T, runner rootRunner, svc workspace.Service, c review.Comment, ghID string) review.Comment {
	t.Helper()
	store, r := reviewFor(t, runner, svc)
	c.State = review.Published
	c.Publish = &review.PublishRecord{ThreadID: ghID, At: time.Unix(0, 0)}
	if err := store.UpdateComment(r, c); err != nil {
		t.Fatalf("update: %v", err)
	}
	return c
}

// mirror writes the PR's conversations where the pr-status pass puts them.
func mirror(t *testing.T, runner rootRunner, svc workspace.Service, threads ...review.Thread) {
	t.Helper()
	store, r := reviewFor(t, runner, svc)
	if err := store.SaveThreads(r, threads); err != nil {
		t.Fatalf("save threads: %v", err)
	}
}

// row is the listing line for a finding, split into its columns.
func row(t *testing.T, runner rootRunner, svc workspace.Service, id string) []string {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(listing(t, runner, svc)), "\n") {
		if strings.HasPrefix(line, id) {
			return strings.Split(line, "\t")
		}
	}
	t.Fatalf("no row for %s in:\n%s", id, listing(t, runner, svc))
	return nil
}

// The whole complaint: a settled conversation must not read as an open finding.
func TestReviewListSaysAThreadIsResolved(t *testing.T) {
	runner, svc, finding := proposalCLI(t)
	publishedFinding(t, runner, svc, finding, "PRRC_1")
	mirror(t, runner, svc, review.Thread{
		ID: "T1", Path: "a.go", Side: review.SideNew, Line: 12, Resolved: true,
		Comments: []review.ThreadComment{
			{ID: "PRRC_1", Author: "andrewcohen", Body: "this drops the error"},
			{ID: "PRRC_2", Author: "someone-else", Body: "fixed in 4f2a1c"},
		},
	})

	got := row(t, runner, svc, finding.ID)[5]
	if !strings.Contains(got, "resolved") {
		t.Fatalf("the thread column does not say it is settled: %q", got)
	}
	// The reply count, because "resolved" alone does not say whether anyone
	// answered — and an answer is the thing that stops a point being re-raised.
	if !strings.Contains(got, "1 reply") {
		t.Fatalf("the thread column does not count the reply: %q", got)
	}
}

// Outdated is GitHub's separate fact — the code moved out from under the thread —
// and a conversation is usually both, since settling a point is what precedes the
// code changing.
func TestReviewListSaysAThreadIsOutdated(t *testing.T) {
	runner, svc, finding := proposalCLI(t)
	publishedFinding(t, runner, svc, finding, "PRRC_1")
	mirror(t, runner, svc, review.Thread{
		ID: "T1", Path: "a.go", Line: 12, Resolved: true, Outdated: true,
		Comments: []review.ThreadComment{{ID: "PRRC_1", Author: "andrewcohen", Body: "x"}},
	})

	got := row(t, runner, svc, finding.ID)[5]
	for _, want := range []string{"resolved", "outdated"} {
		if !strings.Contains(got, want) {
			t.Errorf("the thread column does not say %q: %q", want, got)
		}
	}
}

// An open conversation says so, so the one that still needs an answer is the one
// that stands out.
func TestReviewListSaysAThreadIsOpen(t *testing.T) {
	runner, svc, finding := proposalCLI(t)
	publishedFinding(t, runner, svc, finding, "PRRC_1")
	mirror(t, runner, svc, review.Thread{
		ID: "T1", Path: "a.go", Line: 12,
		Comments: []review.ThreadComment{{ID: "PRRC_1", Author: "andrewcohen", Body: "x"}},
	})

	if got := row(t, runner, svc, finding.ID)[5]; !strings.HasPrefix(got, "open") {
		t.Fatalf("expected the conversation reported open, got %q", got)
	}
}

// The stored line is frozen on purpose — an anchor is located by its text — but
// GitHub recomputes a thread's line as the PR moves. Printing only ours is what
// had the listing report 346-350 for a thread sitting at 438-442.
func TestReviewListShowsWhereTheThreadIsNow(t *testing.T) {
	runner, svc, finding := proposalCLI(t)
	publishedFinding(t, runner, svc, finding, "PRRC_1")
	mirror(t, runner, svc, review.Thread{
		ID: "T1", Path: "a.go", Line: 53,
		Comments: []review.ThreadComment{{ID: "PRRC_1", Author: "andrewcohen", Body: "x"}},
	})

	got := row(t, runner, svc, finding.ID)[6]
	if !strings.Contains(got, "a.go:12") || !strings.Contains(got, "53") {
		t.Fatalf("expected both the filed line and GitHub's, got %q", got)
	}
}

// Agreement is not drift, so a thread GitHub still has where we filed it prints
// one number rather than an arrow pointing at itself.
func TestReviewListDoesNotReportAnUnmovedThreadAsMoved(t *testing.T) {
	runner, svc, finding := proposalCLI(t)
	publishedFinding(t, runner, svc, finding, "PRRC_1")
	mirror(t, runner, svc, review.Thread{
		ID: "T1", Path: "a.go", Line: 12,
		Comments: []review.ThreadComment{{ID: "PRRC_1", Author: "andrewcohen", Body: "x"}},
	})

	if got := row(t, runner, svc, finding.ID)[6]; strings.Contains(got, "→") {
		t.Fatalf("expected no drift reported, got %q", got)
	}
}

// A finding the mirror has nothing for — never published, or the mirror is behind
// — keeps its column and its location, so the fields stay indexable.
func TestReviewListMarksAFindingWithNoMirror(t *testing.T) {
	runner, svc, finding := proposalCLI(t)
	mirror(t, runner, svc, review.Thread{
		ID: "T1", Path: "a.go", Line: 99, Resolved: true,
		Comments: []review.ThreadComment{{ID: "PRRC_other", Author: "someone-else", Body: "unrelated"}},
	})

	fields := row(t, runner, svc, finding.ID)
	if fields[5] != "-" {
		t.Fatalf("expected `-` with no mirror, got %q", fields[5])
	}
	if fields[6] != "a.go:12" {
		t.Fatalf("expected the filed location alone, got %q", fields[6])
	}
}

// The replies are why this is worth joining at all: what the author already
// answered is what stops a re-reviewing agent raising a closed point again. They
// go through the machine channel, where an agent reads them.
func TestTheAuthorsRepliesReachTheJSONChannel(t *testing.T) {
	runner, svc, finding := proposalCLI(t)
	publishedFinding(t, runner, svc, finding, "PRRC_1")
	mirror(t, runner, svc, review.Thread{
		ID: "T1", Path: "a.go", Line: 12, Resolved: true,
		Comments: []review.ThreadComment{
			{ID: "PRRC_1", Author: "andrewcohen", Body: "this drops the error"},
			{ID: "PRRC_2", Author: "someone-else", Body: "fixed in 4f2a1c"},
		},
	})

	var rows []listedComment
	if err := json.Unmarshal([]byte(listingJSON(t, runner, svc)), &rows); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, r := range rows {
		if r.ID != finding.ID {
			continue
		}
		if r.Thread == nil {
			t.Fatal("expected the mirrored thread joined onto the finding")
		}
		if !r.Thread.Resolved {
			t.Error("expected the thread reported resolved")
		}
		// Ours is not a reply to itself: the message this finding *became* is
		// excluded, so what is left is what arrived after we posted.
		if len(r.Thread.Replies) != 1 || r.Thread.Replies[0].Body != "fixed in 4f2a1c" {
			t.Fatalf("expected the author's reply alone, got %+v", r.Thread.Replies)
		}
		return
	}
	t.Fatalf("no row for %s", finding.ID)
}

// Additive: every key the machine channel carried before is still at the top
// level, and it is still a bare array. An agent parsing it must not have to
// change to keep reading what it already read.
func TestTheJoinedListingKeepsItsOldShape(t *testing.T) {
	runner, svc, finding := proposalCLI(t)
	publishedFinding(t, runner, svc, finding, "PRRC_1")
	mirror(t, runner, svc, review.Thread{
		ID: "T1", Path: "a.go", Line: 12,
		Comments: []review.ThreadComment{{ID: "PRRC_1", Author: "andrewcohen", Body: "x"}},
	})

	var raw []map[string]any
	if err := json.Unmarshal([]byte(listingJSON(t, runner, svc)), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("expected the findings listed")
	}
	for _, key := range []string{"id", "author", "body", "state", "anchor"} {
		if _, ok := raw[0][key]; !ok {
			t.Errorf("the embedded comment lost %q from the top level: %v", key, raw[0])
		}
	}
	// Omitted rather than written null on a finding with no mirror, so a reader can
	// test for its presence.
	runnerB, svcB, findingB := proposalCLI(t)
	var bare []map[string]any
	if err := json.Unmarshal([]byte(listingJSON(t, runnerB, svcB)), &bare); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, r := range bare {
		if r["id"] == findingB.ID {
			if _, ok := r["thread"]; ok {
				t.Errorf("expected `thread` omitted with no mirror, got %v", r)
			}
		}
	}
}

// The two sets of numbers on a row are relative to different commits — ours to
// the commit the reviewer read, GitHub's to the PR head — so the listing says
// which is which once, at the top, rather than per row.
func TestReviewListNamesTheCommitItsLinesAreRelativeTo(t *testing.T) {
	runner, svc, _ := proposalCLI(t)
	store, r := reviewFor(t, runner, svc)
	r.ObservedHead = "6fe2f75dabc1234567890"
	if err := store.Save(r); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := listing(t, runner, svc); !strings.Contains(got, "6fe2f75dabc1") {
		t.Fatalf("the listing does not name the commit its lines mean:\n%s", got)
	}
}

// listingJSON is `review list --json`, the machine channel.
func listingJSON(t *testing.T, runner rootRunner, svc workspace.Service) string {
	t.Helper()
	var out bytes.Buffer
	if err := runReviewList(runner, svc, []string{"--json"}, &out); err != nil {
		t.Fatalf("review list --json: %v", err)
	}
	return out.String()
}
