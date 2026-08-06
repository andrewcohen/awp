package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/andrewcohen/awp/internal/github"
	"github.com/andrewcohen/awp/internal/jj"
	"github.com/andrewcohen/awp/internal/review"
	"github.com/andrewcohen/awp/internal/workspace"
)

// Publishing local findings to a GitHub PR.
//
// Idempotency is the whole design constraint here: publishing twice must not
// double-post. Each comment records the thread it produced, in its own file, at
// the moment it succeeds — so a run that fails halfway leaves precise state and a
// retry posts only what is still unpublished. That is also why comments go up one
// at a time rather than as one batched review submission: a partial failure
// inside a batch is unrecoverable, because you cannot tell which comments landed.

// publishResult summarises a publish run for the caller to report.
type publishResult struct {
	Posted  int
	Skipped int
	Failed  int
}

// resolvePublishPR decides which PR a publish run posts to, in precedence
// order: an explicit --pr, then the review's own target if it is PR-keyed, then
// the workspace's pin.
//
// The pin is what makes publish usable. Reviews are keyed by workspace — see
// reviewTargetFor for why that is deliberate — so no review's target names a PR,
// and without the pin publish rejected every review and asked the user to retype
// a number `awp review <n>` had already recorded on the workspace.
func resolvePublishPR(flagPR int, target review.Target, pinned int) int {
	if flagPR > 0 {
		return flagPR
	}
	if target.Kind == review.TargetPR {
		if n, err := parsePRNumber(target.Value); err == nil && n > 0 {
			return n
		}
	}
	return pinned
}

// publishBuckets is a review's comments sorted by the call that sends each one.
//
// A struct rather than a row of return values, because the bug it exists to
// prevent is a comment scope landing in a bucket nobody meant it to. File-level
// comments were routed as inline threads simply by being the case nobody added —
// and since an inline thread must have a line, the anchor preflight then refused
// the entire publish over a comment that has no line by design. Positional
// returns are what let that happen without anyone writing it down; a named field
// per destination means a new scope has to be put somewhere on purpose.
type publishBuckets struct {
	// Inline is one thread per comment, staged into a single review.
	Inline []review.Comment
	// ChangeWide is a remark about the change as a whole: the review's body when
	// there is a verdict, a PR comment otherwise.
	ChangeWide []review.Comment
	// Replies go back into a GitHub thread they answer, by their own mutation.
	Replies []review.Comment
	// FileLevel is a remark about a file as a whole. It has no line, so it cannot
	// ride in the staged review — GitHub spells this subject_type=file on the REST
	// comment endpoint, which is not wired up yet.
	FileLevel []review.Comment
	// Skipped is what is already on GitHub, or empty, and so has nothing to post.
	Skipped int
}

// partitionForPublish sorts a review's comments by the call that sends each one.
//
// Its own function so the rules are testable without a GitHub round-trip: what
// gets reposted is the one thing here that must never be wrong.
func partitionForPublish(comments []review.Comment) publishBuckets {
	var b publishBuckets
	b.Inline = make([]review.Comment, 0, len(comments))
	for _, c := range comments {
		// Already on GitHub: skip rather than repost. This is what makes a retry
		// after a partial failure safe.
		if c.OnGitHub() {
			b.Skipped++
			continue
		}
		if strings.TrimSpace(c.Body) == "" {
			b.Skipped++
			continue
		}
		// A reply into a GitHub thread goes back into that thread, by its own mutation.
		// Checked before the anchor rules below, because a reply carries the thread's
		// anchor and would otherwise look exactly like a fresh inline comment — which is
		// how it would land: a new top-level thread on the same line, divorced from the
		// question it answers.
		if c.ThreadReply() {
			b.Replies = append(b.Replies, c)
			continue
		}
		// A reply to one of our own comments is a local conversation — you and the
		// agent working a finding out between you (see review.Store.Reply). There is
		// nothing to publish it into: a batched review creates new threads only.
		if c.ReplyTo != "" {
			b.Skipped++
			continue
		}
		// Every remaining comment goes by its scope, so a scope that is not handled
		// here is a compile-time hole rather than a comment quietly treated as
		// something it is not.
		switch c.Anchor.Scope() {
		case review.ChangeScope:
			// No line to hang a review comment on, so it goes up as a comment on the
			// PR instead. Sending it inline with an empty path is what GitHub rejects,
			// and reporting that as a failure gave the user nothing to act on.
			b.ChangeWide = append(b.ChangeWide, c)
		case review.FileScope:
			// Not inline: a staged thread must have a line, and this one has none by
			// design. Routed here so the anchor preflight never sees it — as an inline
			// comment it was reported as "line 0 is not in the diff", which refuses the
			// whole run and takes every good comment beside it down too.
			b.FileLevel = append(b.FileLevel, c)
		case review.LineScope:
			b.Inline = append(b.Inline, c)
		}
	}
	return b
}

// runReviewPublish implements `awp review publish`.
func runReviewPublish(runner Runner, svc workspace.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("review publish", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dryRun := fs.Bool("dry-run", false, "show what would be posted without posting it")
	prFlag := fs.Int("pr", 0, "PR number to publish to (defaults to the review's target)")
	verdict := fs.String("verdict", "", "submit the comments as a review: approve, comment, or request-changes")
	// Without this, `--verdict comment` dead-ended: GitHub requires a body, and the
	// only way to supply one was to go and file a review-level remark first — which
	// the error message asked for without saying how.
	summary := fs.String("summary", "", "the review's body, which comment and request-changes require")
	summaryFile := fs.String("summary-file", "", "read the review body from a file, or - for stdin")
	wsName := workspaceFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	summaryText, err := commentBody(*summary, *summaryFile, os.Stdin)
	if err != nil {
		return err
	}
	event, err := parseVerdict(*verdict)
	if err != nil {
		return err
	}
	scope, err := openReviewFor(runner, svc, *wsName)
	if err != nil {
		return err
	}
	comments, err := scope.store.Comments(scope.review)
	if err != nil {
		return err
	}
	// The workspace under review, not the process's own directory: usually the same
	// place, but not when --workspace named another one. Its directory is where the
	// commit the comments were read against lives, and its PR is what publishing
	// defaults to. cwd remains the fallback for a workspace awp does not know about.
	cwd, _ := os.Getwd()
	prNumber := resolvePublishPR(*prFlag, scope.review.Target, scope.entry.PRNumber)
	if prNumber == 0 {
		return fmt.Errorf("review publish: %s isn't pinned to a PR; pass --pr", scope.label())
	}
	// Named before anything is sent. A publish that resolved the wrong review
	// reports "0 comments" and looks like a store bug; saying which review it read
	// makes that one line of output instead of an investigation.
	_, _ = fmt.Fprintf(out, "publishing %s to PR #%d\n", scope.label(), prNumber)
	return publishReview(runner, publishRequest{
		Store:    scope.store,
		Review:   scope.review,
		Comments: comments,
		PR:       prNumber,
		Event:    event,
		Verdict:  *verdict,
		DryRun:   *dryRun,
		Dir:      scope.dir(cwd),
		Summary:  summaryText,
	}, out)
}

// publishRequest is one publish run, fully resolved: which review, which PR, and
// what verdict to finish it with.
//
// Separated from the flag parsing so the deck's own publish key runs exactly this
// code rather than a second implementation of it. The command resolves the review
// from the working directory; the deck resolves it from the workspace row you are
// reading — and neither difference belongs in the part that talks to GitHub.
type publishRequest struct {
	Store    review.Store
	Review   review.Review
	Comments []review.Comment
	PR       int
	// Event is GitHub's verdict constant, empty for none. Verdict is the word the
	// user typed, for messages.
	Event   string
	Verdict string
	DryRun  bool
	// Dir is the working directory of the thing under review — a jj workspace, not
	// the source repo it belongs to. It is where the commit the reviewer read is
	// resolved from, and the distinction is the whole point: a workspace has its own
	// working copy, so the source repo's answer describes a different change
	// entirely (see reviewedCommit).
	Dir string
	// HeadHint is the commit under review when the caller already knows it, which
	// saves resolving Dir at all. The deck does: it reads every workspace's bookmark
	// commit to compare against the PR's head, so the answer is already in hand.
	HeadHint string
	// Summary is a remark about the change as a whole, written for *this*
	// submission and not yet part of the review.
	//
	// Carried here rather than filed first so that previewing does not write
	// anything: the viewer used to save the summary on the way out of its compose
	// box, which meant backing out of a publish left the remark behind — four
	// abandoned attempts became four review-level comments on a real PR. It is filed
	// once the publish succeeds instead (see publishReview), so the record still
	// exists afterwards without a cancelled run creating one.
	Summary string
}

func publishReview(runner Runner, req publishRequest, out io.Writer) error {
	store, r, event, prNumber := req.Store, req.Review, req.Event, req.PR
	b := partitionForPublish(req.Comments)
	if runner == nil {
		runner = NewExecRunner()
	}
	// In the review's own repo. The deck is a tmux popup launched from wherever you
	// happen to be, so resolving the repository from the process's directory sent a
	// review of one repo's PR to whatever repo that directory belonged to — 404 if it
	// had no PR with that number, and a write to the wrong PR if it did.
	gh := github.New(runner, r.Repo)

	// A verdict changes where the review-level remarks go: they become the
	// review's summary, which is what GitHub's review body is for, instead of
	// separate comments on the PR. Checked before anything is posted — a run that
	// published eight inline comments and then refused the verdict would leave the
	// reviewer to work out what landed.
	// The body is what the caller wrote, if anything. Not that joined with the
	// review's own summary remarks: the viewer prefills its box from exactly those, so
	// joining would send them twice. With nothing written — `awp review publish` and
	// no --summary — they are the body, as before.
	summary := strings.TrimSpace(req.Summary)
	if summary == "" {
		summary = reviewSummary(b.ChangeWide)
	}
	// Comments ride in a review, and a review has to be submitted with a verdict —
	// so publishing them without one is refused rather than guessed at. Leaving the
	// review pending instead would put the comments on GitHub where only the author can
	// see them, and a retry would then stage a second copy of every one.
	if len(b.Inline) > 0 && event == "" {
		return errors.New("publishing comments needs a verdict; pass --verdict approve|comment|request-changes")
	}
	if github.EventNeedsBody(event) && summary == "" {
		return fmt.Errorf("--verdict %s needs a review summary; pass --summary", req.Verdict)
	}

	// The commit every inline comment is anchored against. GitHub requires it and
	// refuses the whole request without one, so it is resolved once, up front — a run
	// that cannot find it says so once rather than failing per comment.
	head := ""
	if len(b.Inline) > 0 {
		resolved, note, herr := reviewedCommit(runner, gh, req)
		if herr != nil {
			return herr
		}
		head = resolved
		if note != "" {
			_, _ = fmt.Fprintln(out, "note: "+note)
		}
		// Remembered so every later run — a retry, a reply into one of these threads —
		// anchors to the same commit rather than re-deriving it from a workspace that
		// has moved on. Best-effort: failing to record it must not stop the publish.
		// Not on a dry run: a preview that edits the review is not a preview.
		if r.ObservedHead != head && !req.DryRun {
			r.ObservedHead = head
			if serr := store.Save(r); serr != nil {
				_, _ = fmt.Fprintf(out, "note: couldn't record the reviewed commit: %v\n", serr)
			}
		}
	}

	// Check every anchor against the diff GitHub itself will validate against,
	// before the preview is printed and before anything is sent. A comment on a line
	// that is not in the diff used to be discoverable only as a 422 — and now that
	// the whole review goes up as one atomic mutation, one bad anchor fails every
	// comment with it and the message names only the first problem.
	var verdicts []anchorVerdict
	if len(b.Inline) > 0 {
		files, ferr := gh.PRFiles(req.PR)
		if ferr != nil {
			// The check is an improvement on GitHub's error message, not a gate the
			// publish depends on. Say why it did not run and let GitHub arbitrate, which
			// is what happened before this existed.
			_, _ = fmt.Fprintf(out, "note: couldn't check the anchors against the PR's diff: %v\n", ferr)
		} else if len(files) > 0 {
			verdicts = preflight(b.Inline, parseCommentable(files))
		}
		// No files at all is treated the same way as a failed fetch. A PR whose diff
		// touches nothing is not a real state, so an empty answer means the call told
		// us nothing — a shape we did not understand, a permission we do not have —
		// and refusing every anchor on the strength of that would be refusing on
		// ignorance.
	}

	if req.DryRun {
		for _, line := range publishPlan(req, b, head, verdicts) {
			_, _ = fmt.Fprintln(out, line)
		}
		return nil
	}
	// Refuse rather than send an anchor GitHub is going to reject. The mutation is
	// atomic, so this loses nothing that would otherwise have landed — it replaces a
	// wall of validation errors naming GitHub's first complaint with the full list,
	// in the reviewer's own terms, before anything is attempted.
	if blocked := blockedAnchors(b.Inline, verdicts); len(blocked) > 0 {
		return fmt.Errorf("%d comment(s) are not on lines in PR #%d's diff:\n  %s",
			len(blocked), req.PR, strings.Join(blocked, "\n  "))
	}
	// Said out loud, every run, before anything is posted. These are not published
	// yet, and a comment the reviewer wrote and believes is on the PR is worse than
	// one they know is still local — silence here is the exact failure this store
	// keeps getting fixed for.
	if len(b.FileLevel) > 0 {
		_, _ = fmt.Fprintf(out, "note: %d comment(s) not published — a comment on a whole file needs an endpoint awp doesn't call yet\n",
			len(b.FileLevel))
		for _, c := range b.FileLevel {
			_, _ = fmt.Fprintf(out, "      %s  %s\n", c.Anchor.Where(), oneLine(c.PublishBody()))
		}
	}
	if len(b.Inline) == 0 && len(b.ChangeWide) == 0 && len(b.Replies) == 0 {
		// A verdict is worth submitting on its own: approving a PR whose comments
		// all went up on an earlier run is a normal thing to want.
		if event == "" {
			_, _ = fmt.Fprintf(out, "nothing to publish (%d already published)\n", b.Skipped)
			return nil
		}
	}

	res := publishResult{Skipped: b.Skipped}
	var failures []error

	// record marks a comment published and writes it back immediately, per
	// comment. Batching these updates until the end would mean a crash mid-run
	// leaves posted comments looking unpublished, and the next retry would
	// duplicate them on GitHub.
	record := func(c review.Comment, threadID, where string) {
		c.State = review.Published
		c.Publish = &review.PublishRecord{ThreadID: threadID, At: time.Now()}
		if uerr := store.UpdateComment(r, c); uerr != nil {
			failures = append(failures, fmt.Errorf("%s posted but not recorded: %w", where, uerr))
		}
		res.Posted++
	}

	switch {
	case len(b.Inline) > 0:
		// One review carrying every comment, staged and then submitted. Not one call per
		// comment: the REST comment endpoint creates a single-comment *review* per call,
		// so eight comments appeared as eight empty review entries on the PR plus one for
		// the verdict — and GitHub does not allow deleting a submitted review, so those
		// are permanent (see alpha #2329, app-main #54).
		staged, cerr := stageReview(gh, prNumber, head, b.Inline)
		if cerr != nil {
			// Nothing was created: the mutation is atomic, and it is refused before
			// anything becomes visible. So this is a clean failure with nothing to undo.
			res.Failed += len(b.Inline)
			failures = append(failures, cerr)
		} else if serr := gh.SubmitStagedReview(staged.ID, event, summary); serr != nil {
			// The comments are staged but invisible. Discard them rather than leaving a
			// pending review behind: a retry would stage a second copy of every comment,
			// and the reviewer has lost nothing — it is all still in the local store.
			res.Failed += len(b.Inline)
			failures = append(failures, serr)
			if derr := gh.DeleteStagedReview(staged.ID); derr != nil {
				failures = append(failures, fmt.Errorf(
					"the staged review could not be discarded either, so it is still pending on the PR — submit or delete it there before retrying: %w", derr))
			}
		} else {
			for _, c := range b.Inline {
				where := c.Anchor.Where()
				// The thread's own id when GitHub named it, so a record points at the
				// conversation it produced; the review's id otherwise, which is still enough
				// to know the comment went up.
				id := staged.ThreadID(c.Anchor.Path, commentEndLine(c.Anchor))
				if id == "" {
					id = staged.ID
				}
				record(c, id, where)
			}
			recordSummaries(store, r, req, b.ChangeWide, staged.ID, record, &failures)
			_, _ = fmt.Fprintf(out, "submitted the review as %s\n", req.Verdict)
		}
	case event != "":
		// A verdict on its own — every comment went up earlier. GitHub has no way to
		// submit a review with no comments through the staging path, since there is
		// nothing to stage, so this is the plain submission.
		id, perr := gh.SubmitReview(prNumber, event, summary)
		if perr != nil {
			res.Failed++
			failures = append(failures, fmt.Errorf("submitting the review: %w", perr))
		} else {
			recordSummaries(store, r, req, b.ChangeWide, id, record, &failures)
			_, _ = fmt.Fprintf(out, "submitted the review as %s\n", req.Verdict)
		}
	default:
		// No verdict and nothing inline: the review summary goes up as a comment on the
		// PR, which is where a remark with no line to attach to belongs.
		if written := strings.TrimSpace(req.Summary); written != "" {
			id, perr := gh.PostPRComment(prNumber, written)
			if perr != nil {
				res.Failed++
				failures = append(failures, fmt.Errorf("the summary, on the PR: %w", perr))
			} else {
				fileSummary(store, r, written, id, &failures)
				res.Posted++
			}
		}
		for _, c := range b.ChangeWide {
			id, perr := gh.PostPRComment(prNumber, c.PublishBody())
			if perr != nil {
				res.Failed++
				failures = append(failures, fmt.Errorf("on the PR: %w", perr))
				continue
			}
			record(c, id, "on the PR")
		}
	}

	// Replies last, and outside the switch, because they are not part of the review:
	// each goes into a conversation that already exists, needs no verdict, and is
	// unaffected by whether the review above it went up. Usually there are none —
	// the viewer posts a reply as you write it — so these are the ones whose post
	// failed at the time and are being retried.
	for _, c := range b.Replies {
		id, perr := gh.ReplyToReviewThread(c.ReplyToThread, c.PublishBody())
		if perr != nil {
			res.Failed++
			failures = append(failures, fmt.Errorf("replying in the thread at %s: %w",
				c.Anchor.Where(), perr))
			continue
		}
		record(c, id, "the reply")
	}

	_, _ = fmt.Fprintf(out, "posted %d, skipped %d, failed %d\n", res.Posted, res.Skipped, res.Failed)
	if len(failures) > 0 {
		// Report every failure rather than the first: a run that posted 6 of 8
		// needs to say which 2 to look at.
		return errors.Join(failures...)
	}
	return nil
}

// stageReview turns the review's inline comments into one pending review.
func stageReview(gh *github.Client, prNumber int, head string, inline []review.Comment) (github.CreatedReview, error) {
	prID, err := gh.PRNodeID(prNumber)
	if err != nil {
		return github.CreatedReview{}, err
	}
	threads := make([]github.DraftThread, 0, len(inline))
	for _, c := range inline {
		threads = append(threads, github.DraftThread{
			Path: c.Anchor.Path,
			// GitHub's `line` is the *last* line of a comment, so a range sends its end
			// here and its start as StartLine. A single-line anchor has no end, which is
			// why this is not simply EndLineHint.
			Line:      commentEndLine(c.Anchor),
			StartLine: rangeStartLine(c.Anchor),
			Side:      githubSide(c.Anchor.Side),
			// Kind and the robot marker are composed at publish time, not stored: the
			// stored body is what the author typed, so baking prefixes in would double
			// them on a re-publish.
			Body: c.PublishBody(),
		})
	}
	return gh.CreatePendingReview(prID, head, threads)
}

// recordSummaries reconciles the review's summary records with the body that was
// actually sent, then marks them published against the review.
//
// The reviewer edits that body in a box prefilled from these remarks, so the first of
// them becomes the edited text; the rest are marked as they stand, since they were part
// of what the body was built from.
func recordSummaries(
	store review.Store, r review.Review, req publishRequest,
	changeWide []review.Comment, reviewID string,
	record func(review.Comment, string, string), failures *[]error,
) {
	written := strings.TrimSpace(req.Summary)
	if written != "" && len(changeWide) > 0 {
		changeWide[0].Body = written
	}
	for _, c := range changeWide {
		record(c, reviewID, "review summary")
	}
	// Nothing to reconcile against, so the summary becomes the review's first. Filed
	// now rather than before the send, so a run the reviewer backed out of leaves
	// nothing behind — and published in the same breath, since it is on GitHub as the
	// review's body and a later run must not post it again.
	if written != "" && len(changeWide) == 0 {
		fileSummary(store, r, written, reviewID, failures)
	}
}

// fileSummary records a summary that went up but had no local record of its own.
func fileSummary(store review.Store, r review.Review, body, threadID string, failures *[]error) {
	saved, aerr := store.AddComment(r, review.Comment{
		Author: review.AuthorHuman,
		Body:   body,
		State:  review.Published,
	})
	if aerr != nil {
		*failures = append(*failures, fmt.Errorf("the review was submitted but its summary was not saved: %w", aerr))
		return
	}
	saved.Publish = &review.PublishRecord{ThreadID: threadID, At: time.Now()}
	if uerr := store.UpdateComment(r, saved); uerr != nil {
		*failures = append(*failures, fmt.Errorf("summary saved but not marked published: %w", uerr))
	}
}

// publishPlan is what a run would do, one line per call it would make to GitHub.
//
// Written as the calls rather than as a summary because this is what a reviewer
// checks *before* an irreversible outward action, and because it is the only
// diagnostic there is when a publish appears to do nothing: an endpoint and a
// target either look right or they do not. The viewer shows exactly this text
// before posting (see the publish overlay), so a preview cannot describe a
// different run than the one it is previewing.
func publishPlan(req publishRequest, b publishBuckets, head string, verdicts []anchorVerdict) []string {
	// The calls are collected first and counted afterwards, so the count cannot
	// disagree with the list under it. Counting the inputs instead was wrong the
	// moment a verdict folded the review-level remarks into one review body: it
	// promised four calls and listed three.
	//
	// Threads are listed but not counted: they are what one call carries, not calls of
	// their own. Counting them said "8 call(s)" above a plan that makes two, which is
	// exactly the arithmetic this whole change is about.
	var lines []string
	calls := 0
	call := func(format string, args ...any) {
		calls++
		lines = append(lines, fmt.Sprintf(format, args...))
	}
	switch {
	case len(b.Inline) > 0:
		// One review, staged with every thread, then submitted. The threads are listed
		// under it because they are what the reviewer is checking — a target and a body
		// either look right or they do not.
		call("addPullRequestReview  PR #%d  commit=%s  %d thread(s), staged",
			req.PR, shortSHA(head), len(b.Inline))
		for i, c := range b.Inline {
			line := fmt.Sprintf("  thread  %s  %s",
				c.Anchor.Where(), oneLine(c.PublishBody()))
			// The anchor's verdict rides on the thread it is about. A reviewer checking
			// this plan is checking targets, and "not in the diff" belongs next to the
			// target it refers to rather than in a separate list underneath.
			if i < len(verdicts) && verdicts[i].Note != "" {
				line += "  ⚠ " + verdicts[i].Note
			}
			lines = append(lines, line)
		}
		line := fmt.Sprintf("submitPullRequestReview  event=%s", req.Event)
		if summary := planSummary(req, b.ChangeWide); summary != "" {
			line += "  body=" + oneLine(summary)
		}
		call("%s", line)
	case req.Event != "":
		line := fmt.Sprintf("POST pulls/%d/reviews  event=%s", req.PR, req.Event)
		if summary := planSummary(req, b.ChangeWide); summary != "" {
			line += "  body=" + oneLine(summary)
		}
		call("%s", line)
	default:
		if written := strings.TrimSpace(req.Summary); written != "" {
			call("POST issues/%d/comments  %s", req.PR, oneLine(written))
		}
		for _, c := range b.ChangeWide {
			call("POST issues/%d/comments  %s", req.PR, oneLine(c.PublishBody()))
		}
	}
	// Outside the switch, the same as the run itself: a reply is its own call into a
	// conversation that already exists, whatever else this run is doing.
	for _, c := range b.Replies {
		call("addPullRequestReviewThreadReply  %s  %s",
			c.Anchor.Where(), oneLine(c.PublishBody()))
	}
	// Listed as what it is — a comment this run will not send. A preview whose
	// whole job is naming the calls has to name the omissions too, or the reviewer
	// confirms a plan and finds a comment still sitting local afterwards.
	for _, c := range b.FileLevel {
		lines = append(lines, fmt.Sprintf("  not sent  %s  %s  ⚠ a comment on a whole file needs an endpoint awp doesn't call yet",
			c.Anchor.Where(), oneLine(c.PublishBody())))
	}
	head1 := fmt.Sprintf("%d call(s) to PR #%d (%d already published)", calls, req.PR, b.Skipped)
	// A refusal, said before the calls rather than discovered after them. The plan
	// still lists everything: which anchors are wrong is the point, and hiding the
	// good ones would not help fix the bad.
	if blocked := blockedAnchors(b.Inline, verdicts); len(blocked) > 0 {
		head1 += fmt.Sprintf(" — %d anchor(s) would be refused, so this run would send nothing", len(blocked))
	}
	return append([]string{head1}, lines...)
}

// reviewedCommit is the commit a review's inline comments should be anchored to:
// the one whose diff the reviewer actually read, provided GitHub agrees it belongs
// to the PR.
//
// Order matters, and it is not "whatever GitHub says the head is now". A comment
// carries line numbers, and line numbers only mean anything against the commit they
// were read from — so anchoring them to a head that has moved since would attach
// them to a diff nobody looked at. Newest-is-best is exactly wrong here.
//
//  1. What the review recorded the last time this resolved.
//  2. The hint the caller already has. The deck knows the workspace's bookmark
//     commit for every row it draws, so this costs no subprocess.
//  3. `@-` in req.Dir — the *workspace* under review. Not the repo root: a jj
//     workspace has its own working copy, so asking the source repo returns
//     whatever the user happens to have checked out there. That bug anchored a
//     PR review to a trunk commit and GitHub refused all of it.
//  4. The PR's current head, so a review with no local workspace can still publish.
//
// Whatever comes out is then checked against the PR's own commit list, because
// GitHub rejects a commit that is not part of the pull request and there is no
// shortage of local commits that look right. A candidate that is not on the PR
// falls back to its head, and says so — the comments may land on lines that have
// moved, which is worth a sentence rather than a silent substitution.
//
// GitHub marks a comment against an older commit as outdated, which is the honest
// outcome: the remark was written against that code.
func reviewedCommit(runner Runner, gh *github.Client, req publishRequest) (sha, note string, err error) {
	onPR, listErr := gh.PRCommits(req.PR)
	// offered counts candidates we actually had, which is what decides whether
	// falling back to the head is a substitution worth reporting or simply the answer.
	offered := 0
	accept := func(candidate string) (string, bool) {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			return "", false
		}
		offered++
		// Without the PR's commit list there is nothing to check against. Trust the
		// candidate rather than refusing to publish: an unreachable API is not evidence
		// against a commit that is probably right.
		return candidate, listErr != nil || slices.Contains(onPR, candidate)
	}

	// The two the caller already has, before anything that starts a subprocess.
	for _, cheap := range []string{req.Review.ObservedHead, req.HeadHint} {
		if got, ok := accept(cheap); ok {
			return got, "", nil
		}
	}
	if dir := strings.TrimSpace(req.Dir); dir != "" {
		if local, lerr := jj.New(runnerOrExec(runner)).ReviewedCommitID(dir); lerr == nil {
			if got, ok := accept(local); ok {
				return got, "", nil
			}
		}
	}

	if listErr != nil {
		return "", "", fmt.Errorf("resolving the commit to anchor comments to (PR #%d): %w", req.PR, listErr)
	}
	// Asked for rather than read off the end of the list above: that endpoint stops
	// at 250 commits, so on a long PR its tail is not the head.
	head, err := gh.PRHeadSHA(req.PR)
	if err != nil {
		return "", "", fmt.Errorf("resolving the commit to anchor comments to (PR #%d): %w", req.PR, err)
	}
	if offered == 0 {
		// Nothing local to compare against — a review with no workspace. The head is
		// the answer rather than a fallback from a better one, so there is nothing to
		// warn about.
		return head, "", nil
	}
	return head, fmt.Sprintf(
		"the commit under review isn't on PR #%d, so comments are anchored to its head %s; lines that moved since may land in the wrong place",
		req.PR, shortSHA(head),
	), nil
}

// shortSHA is a commit as a reader recognises it. Full length in a preview line
// would push the body it belongs to off the edge.
func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	if sha == "" {
		return "(unresolved)"
	}
	return sha
}

// parseVerdict reads the --verdict flag into GitHub's event vocabulary, empty for
// no verdict at all.
//
// Spelled the way GitHub's own UI labels the three buttons rather than in its
// API's shouting constants: the reviewer is choosing between "approve", "comment"
// and "request changes", which is the decision, not the wire format.
func parseVerdict(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return "", nil
	case "approve", "approved":
		return github.EventApprove, nil
	case "comment":
		return github.EventComment, nil
	case "request-changes", "request_changes", "changes":
		return github.EventRequestChanges, nil
	}
	return "", fmt.Errorf("review publish: unknown --verdict %q (approve, comment, or request-changes)", s)
}

// planSummary is the body a run would send, for the preview to show. Same rule the
// run itself uses — see publishReview — so the two cannot describe different bodies.
func planSummary(req publishRequest, changeWide []review.Comment) string {
	if written := strings.TrimSpace(req.Summary); written != "" {
		return written
	}
	return reviewSummary(changeWide)
}

// reviewSummary is the review body built from the change-wide remarks: their
// composed bodies, in order, one paragraph each.
//
// Composed rather than stored (PublishBody), the same as any other published
// body, so an agent's remark still carries its marker and its kind.
func reviewSummary(changeWide []review.Comment) string {
	parts := make([]string, 0, len(changeWide))
	for _, c := range changeWide {
		// Emptiness is a property of what the author wrote, not of the composed
		// body: PublishBody prefixes the kind, so a blank remark composes to
		// "(comment) -" and would pass a check made on the result.
		if strings.TrimSpace(c.Body) == "" {
			continue
		}
		parts = append(parts, c.PublishBody())
	}
	return strings.Join(parts, "\n\n")
}

// commentEndLine and rangeStartLine translate an anchor into GitHub's way of
// describing the same thing: it names a comment by its last line, with a
// start_line above that when there is a range. Ours names the first line, with an
// end below it — the reverse — because that is the line the comment is located
// by (see review.Anchor).
func commentEndLine(a review.Anchor) int {
	if a.Multiline() {
		return a.EndLineHint
	}
	return a.LineHint
}

func rangeStartLine(a review.Anchor) int {
	if a.Multiline() {
		return a.LineHint
	}
	return 0
}

// githubSide maps our anchor side onto GitHub's diff-side vocabulary.
func githubSide(s review.Side) string {
	if s == review.SideOld {
		return "LEFT"
	}
	return "RIGHT"
}
