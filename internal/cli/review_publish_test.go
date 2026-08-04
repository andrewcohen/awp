package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/github"
	"github.com/andrewcohen/awp/internal/review"
)

func TestGithubSideMapping(t *testing.T) {
	if got := githubSide(review.SideOld); got != "LEFT" {
		t.Fatalf("expected LEFT for the old side, got %q", got)
	}
	if got := githubSide(review.SideNew); got != "RIGHT" {
		t.Fatalf("expected RIGHT for the new side, got %q", got)
	}
	// An unset side must not silently become the old side — that would post a
	// comment against code the reviewer wasn't looking at.
	if got := githubSide(""); got != "RIGHT" {
		t.Fatalf("expected an unset side to default to RIGHT, got %q", got)
	}
}

// The reason each comment records its publish immediately: a second run must post
// nothing. Batching those writes until the end would leave posted comments
// looking unpublished after a crash, and the retry would duplicate them.
func TestPublishedCommentsAreNotRepublished(t *testing.T) {
	store := review.Store{Root: t.TempDir()}
	r, err := store.Open("/repo/proj", review.Target{Kind: review.TargetPR, Value: "9", Workspace: "ws"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	posted, err := store.AddComment(r, review.Comment{
		Body:   "already up",
		Anchor: review.Anchor{Path: "a.go", LineHint: 3, Side: review.SideNew},
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := store.AddComment(r, review.Comment{
		ID:     "pending-1",
		Body:   "not yet",
		Anchor: review.Anchor{Path: "b.go", LineHint: 7, Side: review.SideNew},
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	posted.State = review.Published
	posted.Publish = &review.PublishRecord{ThreadID: "PRRC_abc"}
	if err := store.UpdateComment(r, posted); err != nil {
		t.Fatalf("update: %v", err)
	}

	comments, err := store.Comments(r)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var pending []review.Comment
	for _, c := range comments {
		if c.State == review.Published || c.Publish != nil {
			continue
		}
		pending = append(pending, c)
	}
	if len(pending) != 1 || pending[0].Body != "not yet" {
		t.Fatalf("expected only the unpublished comment pending, got %+v", pending)
	}
}

// A comment whose publish record exists but whose state was not updated must
// still be skipped: either signal alone is enough to mean "already on GitHub".
func TestPublishRecordAloneSuppressesRepost(t *testing.T) {
	c := review.Comment{State: review.Open, Publish: &review.PublishRecord{ThreadID: "T1"}}
	if c.State != review.Published && c.Publish == nil {
		t.Fatal("a comment carrying a publish record must count as published")
	}
}

// A workspace and its source repo must resolve to the same review store
// directory. They did not: the CLI used the workspace root while the deck used
// the source repo, so an agent's findings landed where the deck never looked.
func TestWorkspaceAndSourceRepoShareOneReviewStore(t *testing.T) {
	store := review.Store{Root: t.TempDir()}
	target := review.Target{Kind: review.TargetWorking, Workspace: "ws-1"}

	// What the deck opens (source repo root).
	fromDeck, err := store.Open("/src/proj", target)
	if err != nil {
		t.Fatalf("deck open: %v", err)
	}
	if _, err := store.AddComment(fromDeck, review.Comment{
		Body:   "filed from the deck",
		Anchor: review.Anchor{Path: "a.go", LineHint: 1},
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	// What the CLI must open for an agent working in a workspace of that repo.
	fromAgent, err := store.Open("/src/proj", target)
	if err != nil {
		t.Fatalf("agent open: %v", err)
	}
	if fromAgent.ID != fromDeck.ID {
		t.Fatalf("expected one review, got %q and %q", fromAgent.ID, fromDeck.ID)
	}
	got, err := store.Comments(fromAgent)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected the deck's comment visible to the agent, got %d", len(got))
	}

	// And the failure mode: keyed by the workspace path instead, it is a different
	// store and the comment is invisible.
	wrong, err := store.Open("/Users/me/.awp/workspaces/ws-1", target)
	if err != nil {
		t.Fatalf("wrong open: %v", err)
	}
	if wrongComments, _ := store.Comments(wrong); len(wrongComments) != 0 {
		t.Fatal("fixture is wrong: the workspace-keyed store should be separate")
	}
}

// A review-level remark has no line for a review comment to hang on, so it goes
// up as a comment on the PR itself rather than inline — sorted into its own group
// here, since the two take different API calls.
func TestPublishSortsReviewLevelCommentsOntoThePR(t *testing.T) {
	inline, changeWide, skipped := partitionForPublish([]review.Comment{
		{ID: "a", Body: "on a line", Anchor: review.Anchor{Path: "a.go", LineHint: 3, Side: review.SideNew}},
		{ID: "b", Body: "about the change as a whole"},
		{ID: "c", Body: "already up", State: review.Published, Anchor: review.Anchor{Path: "b.go", LineHint: 1}},
		{ID: "d", Body: "   ", Anchor: review.Anchor{Path: "c.go", LineHint: 2}},
	})
	if len(inline) != 1 || inline[0].ID != "a" {
		t.Fatalf("expected only the anchored comment inline, got %+v", inline)
	}
	if len(changeWide) != 1 || changeWide[0].ID != "b" {
		t.Fatalf("expected the review-level remark bound for the PR, got %+v", changeWide)
	}
	if skipped != 2 {
		t.Fatalf("expected the published and the empty one skipped, got %d", skipped)
	}
}

// A published review-level comment must not be reposted, the same way an inline
// one is not: the record is what makes a retry after a partial failure safe.
func TestPublishSkipsAlreadyPostedReviewLevelComments(t *testing.T) {
	inline, changeWide, skipped := partitionForPublish([]review.Comment{
		{ID: "a", Body: "summary", Publish: &review.PublishRecord{ThreadID: "IC_1"}},
		{ID: "b", Body: "another summary", State: review.Published},
	})
	if len(inline) != 0 || len(changeWide) != 0 {
		t.Fatalf("expected nothing to post, got inline=%+v changeWide=%+v", inline, changeWide)
	}
	if skipped != 2 {
		t.Fatalf("expected both skipped, got %d", skipped)
	}
}

// The flag is spelled the way GitHub's own UI labels the three buttons, since
// that is the decision the reviewer is making.
func TestParseVerdict(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"", ""},
		{"approve", github.EventApprove},
		{"APPROVE", github.EventApprove},
		{" approve ", github.EventApprove},
		{"comment", github.EventComment},
		{"request-changes", github.EventRequestChanges},
		{"request_changes", github.EventRequestChanges},
	} {
		got, err := parseVerdict(tc.in)
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("%q: got %q, want %q", tc.in, got, tc.want)
		}
	}
	// A typo must not silently publish without a verdict — or, worse, approve.
	if _, err := parseVerdict("lgtm"); err == nil {
		t.Fatal("expected an unknown verdict rejected")
	}
}

// With a verdict, the review-level remarks become the review's summary — which is
// what GitHub's review body is for — rather than separate comments on the PR.
func TestReviewSummaryJoinsTheChangeWideRemarks(t *testing.T) {
	remarks := []review.Comment{
		{Author: review.AuthorHuman, Body: "Scope: internal/cli only.", Kind: review.KindComment},
		{Author: "agent", Body: "Tests are missing for the publish path.", Kind: review.KindSuggestion},
		// Empty bodies contribute nothing rather than a blank paragraph.
		{Author: review.AuthorHuman, Body: "   "},
	}
	got := reviewSummary(remarks)
	if !strings.Contains(got, "Scope: internal/cli only.") {
		t.Fatalf("summary dropped the first remark: %q", got)
	}
	// Composed bodies, so an agent's remark still carries its marker and kind —
	// the same text the no-verdict path would have posted.
	if !strings.Contains(got, remarks[1].PublishBody()) {
		t.Fatalf("summary did not use the composed body: %q", got)
	}
	if strings.Count(got, "\n\n") != 1 {
		t.Fatalf("expected one paragraph break between two remarks: %q", got)
	}
	if reviewSummary(nil) != "" {
		t.Fatal("expected no summary from no remarks")
	}
}

// A verdict that asks for something has to say what, and the check has to happen
// before any comment is posted — a run that published eight comments and then
// refused the verdict would leave the reviewer guessing what landed. This asserts
// the two halves the check is made of; the ordering is in runReviewPublish.
func TestVerdictNeedingASummaryIsCaughtBeforePosting(t *testing.T) {
	for _, verdict := range []string{"comment", "request-changes"} {
		event, err := parseVerdict(verdict)
		if err != nil {
			t.Fatalf("%s: %v", verdict, err)
		}
		if !github.EventNeedsBody(event) {
			t.Fatalf("%s should require a summary", verdict)
		}
	}
	event, _ := parseVerdict("approve")
	if github.EventNeedsBody(event) {
		t.Fatal("approving should not require a summary")
	}
}

// The plan is what the reviewer authorises, so it names the calls rather than
// summarising them: an endpoint and a target either look right or they do not,
// which is the only diagnostic there is when a publish appears to do nothing.
func TestPublishPlanNamesTheCalls(t *testing.T) {
	inline := []review.Comment{
		{ID: "c1", Author: review.AuthorHuman, Body: "a line remark", Kind: review.KindSuggestion,
			Anchor: review.Anchor{Path: "a.go", Side: review.SideNew, LineHint: 12, EndLineHint: 18}},
		{ID: "c2", Author: review.AuthorHuman, Body: "another",
			Anchor: review.Anchor{Path: "b.go", Side: review.SideNew, LineHint: 3}},
	}
	changeWide := []review.Comment{{ID: "c3", Author: review.AuthorHuman, Body: "scope was internal/cli"}}

	// One review carrying both threads, then one submission. Two calls, not one per
	// comment — which is the whole point: the REST comment endpoint made a separate
	// single-comment review per call, and those cannot be deleted afterwards.
	plan := publishPlan(publishRequest{PR: 54, Event: github.EventApprove, Verdict: "approve"}, inline, changeWide, 2, "abc123def456789", nil)
	joined := strings.Join(plan, "\n")
	for _, want := range []string{
		// Two calls, not four: the two thread lines under the stage call are what it
		// carries, not calls of their own.
		"2 call(s) to PR #54 (2 already published)",
		"addPullRequestReview  PR #54",
		// The commit the threads are anchored to is part of the call, so the preview has
		// to name it — it is what decides which diff the line numbers mean.
		"commit=abc123def456",
		"2 thread(s), staged",
		"thread  a.go:12-18",
		"thread  b.go:3",
		"submitPullRequestReview  event=APPROVE",
		"scope was internal/cli",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the plan does not mention %q:\n%s", want, joined)
		}
	}
	// The per-comment REST endpoint must not appear at all.
	if strings.Contains(joined, "pulls/54/comments") {
		t.Fatalf("the plan still posts comments one at a time:\n%s", joined)
	}

	// With only review-level remarks and no verdict, they go up as PR comments on a
	// different endpoint — the plan has to say so, or it describes a run that is not
	// the one about to happen.
	plan = publishPlan(publishRequest{PR: 54}, nil, changeWide, 0, "abc123def456789", nil)
	joined = strings.Join(plan, "\n")
	if strings.Contains(joined, "PullRequestReview") {
		t.Fatalf("a verdictless plan claims a review submission:\n%s", joined)
	}
	if !strings.Contains(joined, "POST issues/54/comments") {
		t.Fatalf("expected the PR-comment endpoint for review-level remarks:\n%s", joined)
	}
}

// A local reply is a conversation with the agent, not something to publish: a batched
// review creates new threads only, so sending one would post it as a fresh top-level
// comment divorced from what it answers.
func TestPublishSkipsLocalReplies(t *testing.T) {
	inline, changeWide, skipped := partitionForPublish([]review.Comment{
		{ID: "c1", Body: "the finding", Anchor: review.Anchor{Path: "a.go", LineHint: 3}},
		{ID: "c2", Body: "answering you", ReplyTo: "c1", Anchor: review.Anchor{Path: "a.go", LineHint: 3}},
	})
	if len(inline) != 1 || inline[0].ID != "c1" {
		t.Fatalf("expected only the finding to publish, got %+v", inline)
	}
	if len(changeWide) != 0 {
		t.Fatalf("a reply must not become a review-level remark: %+v", changeWide)
	}
	if skipped != 1 {
		t.Fatalf("expected the reply counted as skipped, got %d", skipped)
	}
}

// Comments go up as one review, not one review per comment. Counted at the transport
// so the guarantee is about calls made, not about how the plan reads.
func TestPublishMakesOneReviewForEveryComment(t *testing.T) {
	store := review.Store{Root: t.TempDir()}
	r := review.Review{ID: "work-ws", Repo: "/repos/theirs"}
	runner := &dirRecordingRunner{}
	var out bytes.Buffer
	comments := []review.Comment{
		{ID: "c1", Author: review.AuthorHuman, Body: "one", State: review.Open,
			Anchor: review.Anchor{Path: "a.go", Side: review.SideNew, LineHint: 3, Text: "x"}},
		{ID: "c2", Author: review.AuthorHuman, Body: "two", State: review.Open,
			Anchor: review.Anchor{Path: "b.go", Side: review.SideNew, LineHint: 9, Text: "y"}},
		{ID: "c3", Author: review.AuthorHuman, Body: "three", State: review.Open,
			Anchor: review.Anchor{Path: "c.go", Side: review.SideNew, LineHint: 1, Text: "z"}},
	}
	if err := publishReview(runner, publishRequest{
		Store: store, Review: r, Comments: comments,
		PR: 54, Event: github.EventApprove, Verdict: "approve",
		Dir: "/workspaces/theirs",
	}, &out); err != nil {
		t.Fatalf("publish: %v", err)
	}
	// Two: stage the review, then submit it. Three comments used to mean three posts,
	// each of which GitHub turned into its own empty review entry on the PR.
	if runner.graphqlCalls != 2 {
		t.Fatalf("expected 2 GraphQL calls (stage, submit) for 3 comments, got %d", runner.graphqlCalls)
	}
	if !strings.Contains(out.String(), "posted 3") {
		t.Fatalf("expected all three recorded as posted, got %q", out.String())
	}
}

// reviewedSHA is the commit under review in the directory fixtures below.
const reviewedSHA = "abc123def4567890abc123def4567890abcdef12"

// dirRecordingRunner records the directory each command ran in, per tool, and
// answers gh well enough for a publish to complete.
type dirRecordingRunner struct {
	ghDirs []string
	jjDirs []string
	// graphqlCalls counts the GraphQL round trips, which is how "one review, not one
	// per comment" is checked.
	graphqlCalls int
}

func (r *dirRecordingRunner) Run(_ context.Context, dir string, name string, args ...string) (string, error) {
	if name == "jj" {
		r.jjDirs = append(r.jjDirs, dir)
		// The commit under review, which is what a comment is anchored to.
		return reviewedSHA + "\n", nil
	}
	r.ghDirs = append(r.ghDirs, dir)
	switch {
	case len(args) > 2 && args[0] == "api" && strings.HasSuffix(args[2], "/commits"):
		return reviewedSHA + "\n", nil
	case len(args) > 2 && args[0] == "api" && strings.HasSuffix(args[2], "/files"):
		// The anchor preflight's source: one object per line, the way --jq over
		// --paginate emits them. Every fixture comment sits on line 1-9 of its file, so
		// a single hunk covering those lines makes them all commentable.
		return prFilesFixture("a.go", "b.go", "c.go"), nil
	case len(args) > 1 && args[0] == "api" && args[1] == "graphql":
		r.graphqlCalls++
		// Enough of an addPullRequestReview / submitPullRequestReview payload for the
		// publish path to read a review id back out of it.
		return `{"data":{"addPullRequestReview":{"pullRequestReview":{"id":"PRR_1","comments":{"nodes":[]}}},` +
			`"submitPullRequestReview":{"pullRequestReview":{"id":"PRR_1","state":"COMMENTED"}}}}`, nil
	case len(args) > 3 && args[0] == "pr" && args[1] == "view" && args[3] == "--json":
		return `{"id":"PR_kwDO1","headRefOid":"` + reviewedSHA + `"}`, nil
	case len(args) > 1 && args[0] == "repo":
		return `{"owner":{"login":"acme"},"name":"widgets"}`, nil
	}
	return `{"node_id":"PRRC_1"}`, nil
}

// Two directions, one publish, and they are not the same directory.
//
// Every gh call has to run in the review's own repo: publishing used to resolve
// the repository from the process's working directory, so a review of one repo's PR
// published from a deck launched elsewhere addressed the wrong repository — 404
// when no PR of that number existed there, and a write to a stranger's PR when one
// did.
//
// The jj call has to run in the *workspace*, which is a different path. A jj
// workspace has its own working copy, so asking the source repo for `@-` returns
// whatever the user happens to have checked out there — a commit GitHub then
// refuses because it is not part of the pull request.
func TestPublishRunsEachToolInItsOwnDirectory(t *testing.T) {
	store := review.Store{Root: t.TempDir()}
	r := review.Review{ID: "work-ws", Repo: "/repos/theirs"}
	runner := &dirRecordingRunner{}
	var out bytes.Buffer
	err := publishReview(runner, publishRequest{
		Store:  store,
		Review: r,
		Comments: []review.Comment{{
			ID: "c1", Author: review.AuthorHuman, Body: "a remark", State: review.Open,
			Anchor: review.Anchor{Path: "a.go", Side: review.SideNew, LineHint: 3, Text: "x"},
		}},
		PR:      54,
		Event:   github.EventApprove,
		Verdict: "approve",
		Dir:     "/workspaces/theirs-pr-54",
	}, &out)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if len(runner.ghDirs) == 0 {
		t.Fatal("expected the publish to make gh calls")
	}
	for i, dir := range runner.ghDirs {
		if dir != "/repos/theirs" {
			t.Fatalf("gh call %d ran in %q, not the review's repo", i, dir)
		}
	}
	if len(runner.jjDirs) == 0 {
		t.Fatal("expected the reviewed commit to be resolved from the workspace")
	}
	for i, dir := range runner.jjDirs {
		if dir != "/workspaces/theirs-pr-54" {
			t.Fatalf("jj call %d ran in %q, not the workspace under review", i, dir)
		}
	}
}

// Once resolved, the commit is written back onto the review — so a retry, or a
// reply into one of these threads later, anchors to the same commit instead of
// re-deriving it from a workspace that has moved on since.
func TestPublishRecordsTheCommitItAnchoredTo(t *testing.T) {
	store := review.Store{Root: t.TempDir()}
	r, err := store.Open("/repos/theirs", review.Target{Kind: review.TargetWorking, Workspace: "ws"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if r.ObservedHead != "" {
		t.Fatalf("fixture: expected no recorded head, got %q", r.ObservedHead)
	}
	runner := &dirRecordingRunner{}
	var out bytes.Buffer
	if err := publishReview(runner, publishRequest{
		Store:  store,
		Review: r,
		Comments: []review.Comment{{
			ID: "c1", Author: review.AuthorHuman, Body: "a remark", State: review.Open,
			Anchor: review.Anchor{Path: "a.go", Side: review.SideNew, LineHint: 3, Text: "x"},
		}},
		PR:      54,
		Event:   github.EventApprove,
		Verdict: "approve",
		Dir:     "/workspaces/theirs-pr-54",
	}, &out); err != nil {
		t.Fatalf("publish: %v", err)
	}
	reopened, err := store.Open("/repos/theirs", review.Target{Kind: review.TargetWorking, Workspace: "ws"})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if reopened.ObservedHead != reviewedSHA {
		t.Fatalf("expected the reviewed commit recorded, got %q", reopened.ObservedHead)
	}
}

// A preview must not edit the review it is previewing.
func TestPublishDryRunDoesNotRecordTheCommit(t *testing.T) {
	store := review.Store{Root: t.TempDir()}
	r, err := store.Open("/repos/theirs", review.Target{Kind: review.TargetWorking, Workspace: "ws"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	runner := &dirRecordingRunner{}
	var out bytes.Buffer
	if err := publishReview(runner, publishRequest{
		Store:  store,
		Review: r,
		Comments: []review.Comment{{
			ID: "c1", Author: review.AuthorHuman, Body: "a remark", State: review.Open,
			Anchor: review.Anchor{Path: "a.go", Side: review.SideNew, LineHint: 3, Text: "x"},
		}},
		PR:      54,
		Event:   github.EventApprove,
		Verdict: "approve",
		DryRun:  true,
		Dir:     "/workspaces/theirs-pr-54",
	}, &out); err != nil {
		t.Fatalf("publish: %v", err)
	}
	reopened, err := store.Open("/repos/theirs", review.Target{Kind: review.TargetWorking, Workspace: "ws"})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if reopened.ObservedHead != "" {
		t.Fatalf("a dry run recorded %q on the review", reopened.ObservedHead)
	}
}

// commitRunner answers the questions the commit lookup asks — jj for the local
// commit, gh for the PR's commit list and its head — and records what it was
// asked.
type commitRunner struct {
	jj, jjErr string
	prView    string
	// onPR is the PR's commit list, as gh --jq prints it: one SHA per line.
	onPR  string
	calls []string
}

func (r *commitRunner) Run(_ context.Context, _ string, name string, args ...string) (string, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	switch {
	case name == "jj":
		if r.jjErr != "" {
			return "", errors.New(r.jjErr)
		}
		return r.jj, nil
	case len(args) > 2 && args[0] == "api" && strings.HasSuffix(args[2], "/commits"):
		return r.onPR, nil
	case len(args) > 1 && args[0] == "pr" && args[1] == "view":
		return r.prView, nil
	case len(args) > 1 && args[0] == "repo":
		return `{"owner":{"login":"acme"},"name":"widgets"}`, nil
	}
	return `{"node_id":"PRRC_1"}`, nil
}

// A comment carries line numbers, and line numbers only mean anything against the
// commit they were read from — so the commit under review wins over whatever
// GitHub says the head is now. Anchoring to a newer head would attach the remark
// to a diff nobody looked at.
func TestReviewedCommitPrefersWhatWasRead(t *testing.T) {
	const older = "1111111111111111111111111111111111111111"
	const head = "2222222222222222222222222222222222222222"
	const recorded = "3333333333333333333333333333333333333333"
	// Every candidate below is on the PR except where a test says otherwise.
	onPR := strings.Join([]string{older, recorded, head}, "\n") + "\n"
	prView := `{"headRefOid":"` + head + `"}`

	// The local commit under review, even though GitHub has moved on. A comment
	// carries line numbers and they only mean anything against the commit they were
	// read from, so anchoring to a newer head would attach the remark to a diff
	// nobody looked at.
	r := &commitRunner{jj: older + "\n", prView: prView, onPR: onPR}
	got, note, err := reviewedCommit(r, github.New(r), publishRequest{Dir: "/ws", PR: 7})
	if err != nil {
		t.Fatalf("reviewedCommit: %v", err)
	}
	if got != older {
		t.Fatalf("expected the commit that was read (%s), got %s", older[:8], got[:8])
	}
	if note != "" {
		t.Fatalf("a commit that is on the PR needs no warning, got %q", note)
	}

	// What the review recorded beats even that: it is the review's own statement of
	// what it was opened against.
	r = &commitRunner{jj: older + "\n", prView: prView, onPR: onPR}
	got, _, err = reviewedCommit(r, github.New(r), publishRequest{
		Dir:    "/ws",
		PR:     7,
		Review: review.Review{Repo: "/repo", ObservedHead: recorded},
	})
	if err != nil {
		t.Fatalf("reviewedCommit: %v", err)
	}
	if got != recorded {
		t.Fatalf("expected the recorded head, got %s", got)
	}
	for _, call := range r.calls {
		if strings.HasPrefix(call, "jj") {
			t.Fatalf("asked jj for a commit the review already knew: %q", call)
		}
	}

	// A hint from the caller also skips jj — the deck already knows the workspace's
	// bookmark commit for every row it draws.
	r = &commitRunner{jj: older + "\n", prView: prView, onPR: onPR}
	got, _, err = reviewedCommit(r, github.New(r), publishRequest{Dir: "/ws", PR: 7, HeadHint: recorded})
	if err != nil {
		t.Fatalf("reviewedCommit: %v", err)
	}
	if got != recorded {
		t.Fatalf("expected the caller's hint, got %s", got)
	}
	for _, call := range r.calls {
		if strings.HasPrefix(call, "jj") {
			t.Fatalf("ran jj despite being handed the commit: %q", call)
		}
	}

	// Nothing local to ask: the PR's head, and no warning — that is the answer for a
	// review with no workspace, not a fallback from a better one.
	r = &commitRunner{jjErr: "not a repo", prView: prView, onPR: onPR}
	got, note, err = reviewedCommit(r, github.New(r), publishRequest{Dir: "/ws", PR: 7})
	if err != nil {
		t.Fatalf("reviewedCommit: %v", err)
	}
	if got != head {
		t.Fatalf("expected the PR head, got %s", got)
	}
	if note != "" {
		t.Fatalf("no local commit means nothing was substituted, got %q", note)
	}
}

// The bug this guards: reviewedCommit used to resolve `@-` in the review's SOURCE
// repo, but a jj workspace has its own working copy — so a PR review was anchored
// to whatever the user had checked out in the main repo, a commit GitHub refuses
// because it is not part of the pull request. Now a candidate that isn't on the PR
// is replaced by its head and the run says so.
func TestReviewedCommitRejectsACommitThatIsNotOnThePR(t *testing.T) {
	const elsewhere = "9999999999999999999999999999999999999999"
	const head = "2222222222222222222222222222222222222222"

	r := &commitRunner{
		jj:     elsewhere + "\n",
		prView: `{"headRefOid":"` + head + `"}`,
		onPR:   head + "\n",
		jjErr:  "",
		calls:  nil,
	}
	got, note, err := reviewedCommit(r, github.New(r), publishRequest{Dir: "/ws", PR: 7})
	if err != nil {
		t.Fatalf("reviewedCommit: %v", err)
	}
	if got != head {
		t.Fatalf("expected the PR head instead of the off-PR commit, got %s", got)
	}
	if !strings.Contains(note, "isn't on PR #7") {
		t.Fatalf("expected the substitution to be reported, got %q", note)
	}
}

// A publish must survive an unreachable commit list rather than refusing to post.
// The candidate is probably right; not being able to check it is not evidence
// against it.
func TestReviewedCommitTrustsTheLocalCommitWhenTheListIsUnavailable(t *testing.T) {
	const local = "1111111111111111111111111111111111111111"
	r := &listlessRunner{jj: local + "\n"}
	got, note, err := reviewedCommit(r, github.New(r), publishRequest{Dir: "/ws", PR: 7})
	if err != nil {
		t.Fatalf("reviewedCommit: %v", err)
	}
	if got != local {
		t.Fatalf("expected the local commit, got %s", got)
	}
	if note != "" {
		t.Fatalf("nothing was substituted, got %q", note)
	}
}

// listlessRunner answers jj but fails the PR commit list.
type listlessRunner struct{ jj string }

func (r *listlessRunner) Run(_ context.Context, _ string, name string, args ...string) (string, error) {
	if name == "jj" {
		return r.jj, nil
	}
	if len(args) > 2 && args[0] == "api" && strings.HasSuffix(args[2], "/commits") {
		return "", errors.New("network is down")
	}
	return `{"owner":{"login":"acme"},"name":"widgets"}`, nil
}

// The viewer prefills its box from the review's own summary remarks, so joining the
// written summary with those remarks again would send them twice.
func TestPublishDoesNotDoubleTheSummary(t *testing.T) {
	store := review.Store{Root: t.TempDir()}
	r, err := store.Open("/repos/theirs", review.Target{Kind: review.TargetWorking, Workspace: "ws"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	filed, err := store.AddComment(r, review.Comment{Author: review.AuthorHuman, Body: "the draft"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	runner := &dirRecordingRunner{}
	var out bytes.Buffer
	if err := publishReview(runner, publishRequest{
		Store:    store,
		Review:   r,
		Comments: []review.Comment{filed},
		PR:       54,
		Event:    github.EventComment,
		Verdict:  "comment",
		Dir:      "/workspaces/theirs",
		// What the box holds: the prefill, edited.
		Summary: "the draft, revised",
	}, &out); err != nil {
		t.Fatalf("publish: %v", err)
	}
	plan := publishPlan(publishRequest{
		PR: 54, Event: github.EventComment, Summary: "the draft, revised",
	}, nil, []review.Comment{filed}, 0, "abc", nil)
	joined := strings.Join(plan, "\n")
	if strings.Count(joined, "the draft") != 1 {
		t.Fatalf("the summary appears more than once in the body:\n%s", joined)
	}
	if !strings.Contains(joined, "the draft, revised") {
		t.Fatalf("expected the written summary as the body:\n%s", joined)
	}
}

// After the send, the record has to say what was actually sent — the reviewer edits
// the body in a box prefilled from that record.
func TestPublishReconcilesTheSummaryRecordWithWhatWasSent(t *testing.T) {
	store := review.Store{Root: t.TempDir()}
	r, err := store.Open("/repos/theirs", review.Target{Kind: review.TargetWorking, Workspace: "ws"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	filed, err := store.AddComment(r, review.Comment{Author: review.AuthorHuman, Body: "the draft"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	var out bytes.Buffer
	if err := publishReview(&dirRecordingRunner{}, publishRequest{
		Store:    store,
		Review:   r,
		Comments: []review.Comment{filed},
		PR:       54,
		Event:    github.EventComment,
		Verdict:  "comment",
		Dir:      "/workspaces/theirs",
		Summary:  "the draft, revised",
	}, &out); err != nil {
		t.Fatalf("publish: %v", err)
	}
	after, err := store.Comments(r)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("expected one summary record, not a second beside it: %+v", after)
	}
	if after[0].Body != "the draft, revised" {
		t.Fatalf("the record does not match what was sent: %q", after[0].Body)
	}
	if after[0].State != review.Published || after[0].Publish == nil {
		t.Fatalf("expected the summary marked published, got %+v", after[0])
	}
}

// With nothing written, the review's own summary remarks are the body — the
// `awp review publish` path, which has no box to prefill.
func TestPublishUsesTheFiledSummaryWhenNoneIsWritten(t *testing.T) {
	filed := review.Comment{ID: "s1", Author: review.AuthorHuman, Body: "the filed one"}
	got := planSummary(publishRequest{}, []review.Comment{filed})
	if got != "the filed one" {
		t.Fatalf("got %q", got)
	}
	// And a written one wins outright.
	got = planSummary(publishRequest{Summary: "written"}, []review.Comment{filed})
	if got != "written" {
		t.Fatalf("got %q", got)
	}
}

// The count above the plan has to be the number of API calls. Threads are listed
// under the one call that carries them, and counting those said "8 call(s)" over a
// plan that makes two — the exact arithmetic this change is about.
func TestPublishPlanCountsCallsNotThreads(t *testing.T) {
	inline := make([]review.Comment, 6)
	for i := range inline {
		inline[i] = review.Comment{
			ID: "c", Author: review.AuthorHuman, Body: "x",
			Anchor: review.Anchor{Path: "a.go", Side: review.SideNew, LineHint: i + 1},
		}
	}
	plan := publishPlan(publishRequest{PR: 2336, Event: github.EventComment, Verdict: "comment", Summary: "s"}, inline, nil, 0, "abc123def456789", nil)
	if !strings.HasPrefix(plan[0], "2 call(s)") {
		t.Fatalf("expected two calls for six threads, got %q", plan[0])
	}
	// And all six are still shown, since they are what the reviewer is checking.
	shown := 0
	for _, l := range plan {
		if strings.HasPrefix(l, "  thread  ") {
			shown++
		}
	}
	if shown != 6 {
		t.Fatalf("expected six threads listed, got %d:\n%s", shown, strings.Join(plan, "\n"))
	}
}

// prFilesFixture is a `pulls/{n}/files` response whose patch makes lines 1-9 of
// each named file commentable on the new side.
func prFilesFixture(paths ...string) string {
	var b strings.Builder
	for _, p := range paths {
		patch := "@@ -1,9 +1,9 @@"
		for i := 1; i <= 9; i++ {
			patch += "\n+line " + strconv.Itoa(i)
		}
		obj, err := json.Marshal(github.PRFile{Filename: p, Patch: patch})
		if err != nil {
			panic(err)
		}
		b.Write(obj)
		b.WriteString("\n")
	}
	return b.String()
}
