package deckui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// The PR badges refresh themselves, and how.
//
// Not on a tick of their own. The 5s row refresh ends in a refreshDoneMsg, that
// asks prStatusRefreshCmd whether anything is due, and prStatusMinInterval answers
// no for a minute at a time — so the effective cadence is one gh fan-out per repo
// per minute, driven by a poll that is already running.
//
// Worth pinning end to end rather than at the policy function, which is where the
// throttle is already tested. The two halves are in different places: the throttle
// says "not yet", and the *trigger* is a line in the refreshDoneMsg arm. Delete
// that line and every policy test still passes while a badge goes stale until the
// row set changes or the deck restarts. That is exactly the state this was
// mistakenly reported to be in (#363).

// prPolled drives one refresh and answers how many fan-outs have been asked for in
// total.
func prPolled(m Model) (Model, tea.Cmd) {
	updated, cmd := m.Update(refreshDoneMsg{items: []Item{
		{ProjectName: "p", WorkspaceName: "ws", RepoRoot: "/r", PRNumber: 7},
	}})
	return updated.(Model), cmd
}

// landPRStatus finishes a fan-out the way the fetcher's own messages do, so the
// in-flight guard clears and the repo's fetched-at is recorded.
func landPRStatus(t *testing.T, m Model, at time.Time) Model {
	t.Helper()
	updated, _ := m.Update(PRStatusRepoDoneMsg{Repo: "/r", ByHead: map[string]PRStatus{}})
	m = updated.(Model)
	updated, _ = m.Update(PRStatusDoneMsg{FetchedAt: at})
	m = updated.(Model)
	if m.prStatusFetchedAt["/r"].IsZero() {
		t.Fatal("a completed fan-out recorded no fetch time, so the throttle has nothing to throttle")
	}
	return m
}

// TestTheRefreshPollIsWhatRefreshesThePRBadges. The trigger, in the arm that would
// stop firing if someone decided the refresh had nothing to do with PR state.
func TestTheRefreshPollIsWhatRefreshesThePRBadges(t *testing.T) {
	fetches := 0
	m := New(nil, nil).WithPRStatusFetcher(func([]string) tea.Cmd {
		fetches++
		return func() tea.Msg { return PRStatusDoneMsg{FetchedAt: time.Now()} }
	})

	m, _ = prPolled(m)
	if fetches != 1 {
		t.Fatalf("the first refresh asked for %d fan-outs, want 1", fetches)
	}
	m = landPRStatus(t, m, time.Now())

	// Straight away again: the throttle's whole job. A refresh every 5s must not be
	// a gh call every 5s.
	m, _ = prPolled(m)
	if fetches != 1 {
		t.Fatalf("a refresh inside the cooldown asked for a fan-out anyway (%d)", fetches)
	}

	// And past the cooldown it goes again, which is the half that makes this a poll
	// rather than a one-off at startup.
	m.prStatusFetchedAt["/r"] = time.Now().Add(-prStatusMinInterval - time.Second)
	if _, _ = prPolled(m); fetches != 2 {
		t.Errorf("a refresh past the cooldown asked for %d fan-outs, want 2", fetches)
	}
}
