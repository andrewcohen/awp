package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/andrewcohen/awp/internal/github"
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

// partitionForPublish sorts a review's comments into the ones to post inline, the
// ones to post on the PR itself, and a count already on GitHub (or empty, which
// there is nothing to post).
//
// Its own function so the rules are testable without a GitHub round-trip: what
// gets reposted is the one thing here that must never be wrong.
func partitionForPublish(comments []review.Comment) (inline, changeWide []review.Comment, skipped int) {
	inline = make([]review.Comment, 0, len(comments))
	for _, c := range comments {
		// Already on GitHub: skip rather than repost. This is what makes a retry
		// after a partial failure safe.
		if c.State == review.Published || c.Publish != nil {
			skipped++
			continue
		}
		if strings.TrimSpace(c.Body) == "" {
			skipped++
			continue
		}
		// A remark about the change as a whole has no line to hang a review
		// comment on, so it goes up as a comment on the PR instead. Sending it
		// inline with an empty path is what GitHub rejects, and reporting that as
		// a failure gave the user nothing to act on.
		if strings.TrimSpace(c.Anchor.Path) == "" {
			changeWide = append(changeWide, c)
			continue
		}
		inline = append(inline, c)
	}
	return inline, changeWide, skipped
}

// runReviewPublish implements `awp review publish`.
func runReviewPublish(runner Runner, svc workspace.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("review publish", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dryRun := fs.Bool("dry-run", false, "show what would be posted without posting it")
	prFlag := fs.Int("pr", 0, "PR number to publish to (defaults to the review's target)")
	verdict := fs.String("verdict", "", "submit the comments as a review: approve, comment, or request-changes")
	if err := fs.Parse(args); err != nil {
		return err
	}
	event, err := parseVerdict(*verdict)
	if err != nil {
		return err
	}
	store, r, err := openReviewForCwd(runner, svc)
	if err != nil {
		return err
	}
	comments, err := store.Comments(r)
	if err != nil {
		return err
	}
	pinned := 0
	if cwd, cerr := os.Getwd(); cerr == nil {
		pinned = pinnedPRForPath(svc, cwd)
	}
	prNumber := resolvePublishPR(*prFlag, r.Target, pinned)
	if prNumber == 0 {
		return errors.New("review publish: this workspace isn't pinned to a PR; pass --pr")
	}
	return publishReview(runner, publishRequest{
		Store:    store,
		Review:   r,
		Comments: comments,
		PR:       prNumber,
		Event:    event,
		Verdict:  *verdict,
		DryRun:   *dryRun,
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
}

func publishReview(runner Runner, req publishRequest, out io.Writer) error {
	store, r, event, prNumber := req.Store, req.Review, req.Event, req.PR
	inline, changeWide, skipped := partitionForPublish(req.Comments)

	// A verdict changes where the review-level remarks go: they become the
	// review's summary, which is what GitHub's review body is for, instead of
	// separate comments on the PR. Checked before anything is posted — a run that
	// published eight inline comments and then refused the verdict would leave the
	// reviewer to work out what landed.
	summary := reviewSummary(changeWide)
	if github.EventNeedsBody(event) && summary == "" {
		return fmt.Errorf("--verdict %s needs a summary; file a review-level remark and it becomes the review body", req.Verdict)
	}

	if req.DryRun {
		total := len(inline) + len(changeWide)
		_, _ = fmt.Fprintf(out, "would post %d comment(s) to PR #%d (%d already published)\n", total, prNumber, skipped)
		for _, c := range inline {
			// The composed body, not the stored one: a dry run is only useful if it
			// shows what will actually land on GitHub, prefixes included.
			_, _ = fmt.Fprintf(out, "  %s:%s\t%s\n", c.Anchor.Path, c.Anchor.LineRange(), oneLine(c.PublishBody()))
		}
		for _, c := range changeWide {
			// Named as where it will actually go, which the verdict decides: a dry
			// run that said "on the PR" while the real run made it the review body
			// would be describing a different command.
			where := "on the PR"
			if event != "" {
				where = "review summary"
			}
			_, _ = fmt.Fprintf(out, "  %s\t%s\n", where, oneLine(c.PublishBody()))
		}
		if event != "" {
			_, _ = fmt.Fprintf(out, "  and submit the review as %s\n", req.Verdict)
		}
		return nil
	}
	if len(inline) == 0 && len(changeWide) == 0 {
		// A verdict is worth submitting on its own: approving a PR whose comments
		// all went up on an earlier run is a normal thing to want.
		if event == "" {
			_, _ = fmt.Fprintf(out, "nothing to publish (%d already published)\n", skipped)
			return nil
		}
	}

	if runner == nil {
		runner = NewExecRunner()
	}
	gh := github.New(runner)
	res := publishResult{Skipped: skipped}
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

	for _, c := range inline {
		where := c.Anchor.Path + ":" + c.Anchor.LineRange()
		threadID, perr := gh.PostReviewComment(prNumber, github.NewComment{
			Path: c.Anchor.Path,
			// GitHub's `line` is the *last* line of a comment, so a range sends its
			// end here and its start as StartLine. A single-line anchor has no end,
			// which is why this is not simply EndLineHint.
			Line:      commentEndLine(c.Anchor),
			StartLine: rangeStartLine(c.Anchor),
			Side:      githubSide(c.Anchor.Side),
			// Kind and the robot marker are composed at publish time, not stored:
			// the stored body is what the author typed, so baking prefixes in
			// would double them on a re-publish.
			Body:      c.PublishBody(),
			CommitID:  r.ObservedHead,
			InReplyTo: c.ReplyTo,
		})
		if perr != nil {
			res.Failed++
			failures = append(failures, fmt.Errorf("%s: %w", where, perr))
			continue
		}
		record(c, threadID, where)
	}

	if event != "" {
		// One review submission carrying the verdict and the summary, after the
		// inline comments so it reads as the conclusion of what they say. The
		// remarks that made up the summary are recorded against the review's id, so
		// a re-publish does not send them again.
		id, perr := gh.SubmitReview(prNumber, event, summary)
		if perr != nil {
			res.Failed++
			failures = append(failures, fmt.Errorf("submitting the review: %w", perr))
		} else {
			for _, c := range changeWide {
				record(c, id, "review summary")
			}
			_, _ = fmt.Fprintf(out, "submitted the review as %s\n", req.Verdict)
		}
	} else {
		// No verdict: review-level remarks go up as comments on the PR itself.
		// Posted after the inline ones so a closing summary lands under the
		// specifics it refers to, which is the order a reader encounters them in.
		for _, c := range changeWide {
			id, perr := gh.PostPRComment(prNumber, c.PublishBody())
			if perr != nil {
				res.Failed++
				failures = append(failures, fmt.Errorf("on the PR: %w", perr))
				continue
			}
			record(c, id, "on the PR")
		}
	}

	_, _ = fmt.Fprintf(out, "posted %d, skipped %d, failed %d\n", res.Posted, res.Skipped, res.Failed)
	if len(failures) > 0 {
		// Report every failure rather than the first: a run that posted 6 of 8
		// needs to say which 2 to look at.
		return errors.Join(failures...)
	}
	return nil
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
