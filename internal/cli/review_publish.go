package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
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

// runReviewPublish implements `awp review publish`.
func runReviewPublish(runner Runner, svc workspace.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("review publish", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dryRun := fs.Bool("dry-run", false, "show what would be posted without posting it")
	prFlag := fs.Int("pr", 0, "PR number to publish to (defaults to the review's target)")
	if err := fs.Parse(args); err != nil {
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
	prNumber := *prFlag
	if prNumber == 0 && r.Target.Kind == review.TargetPR {
		prNumber, _ = parsePRNumber(r.Target.Value)
	}
	if prNumber == 0 {
		return errors.New("review publish: no PR for this review; pass --pr")
	}

	pending := make([]review.Comment, 0, len(comments))
	skipped := 0
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
		pending = append(pending, c)
	}

	if *dryRun {
		_, _ = fmt.Fprintf(out, "would post %d comment(s) to PR #%d (%d already published)\n", len(pending), prNumber, skipped)
		for _, c := range pending {
			// The composed body, not the stored one: a dry run is only useful if it
			// shows what will actually land on GitHub, prefixes included.
			_, _ = fmt.Fprintf(out, "  %s:%d\t%s\n", c.Anchor.Path, c.Anchor.LineHint, oneLine(c.PublishBody()))
		}
		return nil
	}
	if len(pending) == 0 {
		_, _ = fmt.Fprintf(out, "nothing to publish (%d already published)\n", skipped)
		return nil
	}

	if runner == nil {
		runner = NewExecRunner()
	}
	gh := github.New(runner)
	head := r.ObservedHead
	res := publishResult{Skipped: skipped}
	var failures []error
	for _, c := range pending {
		threadID, perr := gh.PostReviewComment(prNumber, github.NewComment{
			Path: c.Anchor.Path,
			Line: c.Anchor.LineHint,
			Side: githubSide(c.Anchor.Side),
			// Kind and the robot marker are composed at publish time, not stored:
			// the stored body is what the author typed, so baking prefixes in
			// would double them on a re-publish.
			Body:      c.PublishBody(),
			CommitID:  head,
			InReplyTo: c.ReplyTo,
		})
		if perr != nil {
			res.Failed++
			failures = append(failures, fmt.Errorf("%s:%d: %w", c.Anchor.Path, c.Anchor.LineHint, perr))
			continue
		}
		// Record immediately, per comment. Batching these updates until the end
		// would mean a crash mid-run leaves posted comments looking unpublished,
		// and the next retry would duplicate them on GitHub.
		c.State = review.Published
		c.Publish = &review.PublishRecord{ThreadID: threadID, At: time.Now()}
		if uerr := store.UpdateComment(r, c); uerr != nil {
			failures = append(failures, fmt.Errorf("%s:%d posted but not recorded: %w", c.Anchor.Path, c.Anchor.LineHint, uerr))
		}
		res.Posted++
	}
	_, _ = fmt.Fprintf(out, "posted %d, skipped %d, failed %d\n", res.Posted, res.Skipped, res.Failed)
	if len(failures) > 0 {
		// Report every failure rather than the first: a run that posted 6 of 8
		// needs to say which 2 to look at.
		return errors.Join(failures...)
	}
	return nil
}

// githubSide maps our anchor side onto GitHub's diff-side vocabulary.
func githubSide(s review.Side) string {
	if s == review.SideOld {
		return "LEFT"
	}
	return "RIGHT"
}
