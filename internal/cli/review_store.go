package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"

	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/github"
	"github.com/andrewcohen/awp/internal/jj"
	"github.com/andrewcohen/awp/internal/review"
	"github.com/andrewcohen/awp/internal/tmux"
	"github.com/andrewcohen/awp/internal/workspace"
)

// `awp review add` and friends: the agent-facing way to file a finding.
//
// This replaces shelling out to an external review TUI, where the session path
// had to be reverse-engineered from that tool's private state files with a
// discovery timeout and a window-launch-order hack. Here the review is resolved
// from the workspace, which awp already knows.

// reviewTargetFor resolves which review the current directory belongs to.
//
// Keyed by workspace, deliberately, even when the workspace is pinned to a PR.
// A review's Target is its identity, and PR presence is not stable across a
// workspace's life: comments accumulate while you work, then opening a PR would
// move the identity from work-<ws> to pr-<n> and split the store in half
// mid-life. Where the PR number is actually needed — publishing — it is read
// from the workspace entry instead (see pinnedPRForPath). Comments anchor to
// content, so nothing about them depends on the review's identity (D1).
func reviewTargetFor(svc workspace.Service, cwd string) review.Target {
	e, _ := workspaceEntryForPath(svc, cwd)
	return review.Target{Kind: review.TargetWorking, Workspace: e.Name}
}

// pinnedPRForPath is the PR the workspace containing cwd is pinned to, or 0.
// Set by `awp review <n>` and by the deck's `p #` link, so it is the number the
// user already told awp about — publishing should not make them retype it.
func pinnedPRForPath(svc workspace.Service, cwd string) int {
	e, _ := workspaceEntryForPath(svc, cwd)
	return e.PRNumber
}

// workspaceEntryForPath is the workspace containing cwd, longest match first so
// a nested workspace beats its parent.
func workspaceEntryForPath(svc workspace.Service, cwd string) (workspace.ListEntry, bool) {
	if svc == nil {
		return workspace.ListEntry{}, false
	}
	entries, err := svc.List()
	if err != nil {
		return workspace.ListEntry{}, false
	}
	best := ""
	var found workspace.ListEntry
	ok := false
	for _, e := range entries {
		if e.Path == "" {
			continue
		}
		if cwd == e.Path || strings.HasPrefix(cwd, e.Path+string(os.PathSeparator)) {
			if len(e.Path) > len(best) {
				best, found, ok = e.Path, e, true
			}
		}
	}
	return found, ok
}

// runReviewSubcommand handles `awp review add|list`, leaving the bare
// `awp review [pr#]` form to the existing setup flow.
func runReviewSubcommand(runner Runner, svc workspace.Service, args []string, out io.Writer) error {
	switch args[0] {
	case "add":
		return runReviewAdd(runner, svc, args[1:], out)
	case "list":
		return runReviewList(runner, svc, args[1:], out)
	case "publish":
		return runReviewPublish(runner, svc, args[1:], out)
	case "reply":
		return runReviewReply(runner, svc, args[1:], out)
	}
	return fmt.Errorf("unknown review subcommand %q", args[0])
}

// isReviewSubcommand reports whether the first argument names a subcommand
// rather than a PR number. Keeps `awp review 123` working unchanged.
func isReviewSubcommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "add", "list", "publish", "reply":
		return true
	}
	return false
}

func openReviewForCwd(runner Runner, svc workspace.Service) (review.Store, review.Review, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return review.Store{}, review.Review{}, err
	}
	if runner == nil {
		runner = NewExecRunner()
	}
	// SourceRepoRoot, not RepoRoot: inside a jj workspace `jj root` returns the
	// *workspace* root, while the deck keys reviews by the source repo. Using the
	// workspace path here filed an agent's findings into a directory the deck
	// never reads — both sides reporting success while nothing showed up.
	repoRoot, err := jj.New(runner).SourceRepoRoot()
	if err != nil {
		return review.Store{}, review.Review{}, fmt.Errorf("not a jj repository: %w", err)
	}
	store := review.Store{}
	r, err := store.Open(repoRoot, reviewTargetFor(svc, cwd))
	if err != nil {
		return review.Store{}, review.Review{}, err
	}
	return store, r, nil
}

// rangeEnd is the end line to record for a --line / --end-line pair: zero unless
// the end is genuinely below the start.
func rangeEnd(line, end int) int {
	if end > line {
		return end
	}
	return 0
}

func runReviewAdd(runner Runner, svc workspace.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("review add", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var (
		path    = fs.String("file", "", "path of the file being commented on (repo-relative)")
		line    = fs.Int("line", 0, "line number the comment attaches to")
		endLine = fs.Int("end-line", 0, "last line, for a comment about a block rather than a line")
		side    = fs.String("side", "new", "which side of the diff the line is on: new or old")
		body    = fs.String("body", "", "the comment text")
		author  = fs.String("author", "", "who is filing this (defaults to the agent name, or 'agent')")
		text    = fs.String("text", "", "the anchored line's text, so the comment survives the line moving")
		endText = fs.String("end-text", "", "the last line's text, the same way --text anchors the first")
		kind    = fs.String("type", string(review.KindComment), "what the comment is asking for: comment, suggestion, or question")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*body) == "" {
		return errors.New("review add requires --body")
	}
	// No --file is a review-level remark: something about the change as a whole,
	// which the diff shows in its own section above the first file rather than
	// pinned to a line. A line without a file is a mistake, though — there is
	// nothing for the number to mean.
	if strings.TrimSpace(*path) == "" && *line > 0 {
		return errors.New("review add: --line needs --file")
	}
	if strings.TrimSpace(*path) != "" && *line <= 0 {
		return errors.New("review add requires --line with --file")
	}
	// An end above the start describes no block. Rejected rather than quietly
	// dropped, because the difference between "line 12" and "lines 12-18" is the
	// whole content of the flag.
	if *endLine > 0 && *endLine < *line {
		return errors.New("review add: --end-line must be at or after --line")
	}
	anchorSide := review.SideNew
	if *side == string(review.SideOld) {
		anchorSide = review.SideOld
	}
	store, r, err := openReviewForCwd(runner, svc)
	if err != nil {
		return err
	}
	who := strings.TrimSpace(*author)
	if who == "" {
		who = "agent"
	}
	c, err := store.AddComment(r, review.Comment{
		Author: who,
		Body:   *body,
		// An unrecognised type falls back to a plain comment rather than failing:
		// a finding is worth keeping even when the label on it is wrong.
		Kind: review.ParseKind(*kind),
		Anchor: review.Anchor{
			Path:     strings.TrimSpace(*path),
			Side:     anchorSide,
			LineHint: *line,
			Text:     *text,
			// Equal to --line is one line, so it is left unset: Multiline() reads an
			// end at the start as "not a range", and a record that says so twice
			// invites the two to disagree.
			EndLineHint: rangeEnd(*line, *endLine),
			EndText:     *endText,
		},
	})
	if err != nil {
		return err
	}
	if c.Anchor.Path == "" {
		_, _ = fmt.Fprintf(out, "added %s %s on the review\n", c.Kind.OrDefault(), c.ID)
		return nil
	}
	_, _ = fmt.Fprintf(out, "added %s %s on %s:%s\n", c.Kind.OrDefault(), c.ID, c.Anchor.Path, c.Anchor.LineRange())
	return nil
}

func runReviewReply(runner Runner, svc workspace.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("review reply", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var (
		to     = fs.String("to", "", "id of the comment being replied to")
		body   = fs.String("body", "", "the reply text")
		author = fs.String("author", "", "who is replying (defaults to 'agent')")
		kind   = fs.String("type", string(review.KindComment), "what the reply is asking for: comment, suggestion, or question")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*to) == "" || strings.TrimSpace(*body) == "" {
		return errors.New("review reply requires --to and --body")
	}
	store, r, err := openReviewForCwd(runner, svc)
	if err != nil {
		return err
	}
	who := strings.TrimSpace(*author)
	if who == "" {
		who = "agent"
	}
	c, err := store.Reply(r, *to, review.Comment{Author: who, Body: *body, Kind: review.ParseKind(*kind)})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "replied to %s (%s)\n", *to, c.ID)
	return nil
}

func runReviewList(runner Runner, svc workspace.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("review list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "emit JSON")
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
	if *asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(comments)
	}
	if len(comments) == 0 {
		_, _ = fmt.Fprintln(out, "no findings")
		return nil
	}
	for _, c := range comments {
		_, _ = fmt.Fprintf(out, "%s\t%s\t%s\t%s:%s\t%s\n", c.ID, c.Kind.OrDefault(), c.State, c.Anchor.Path, c.Anchor.LineRange(), oneLine(c.Body))
	}
	return nil
}

// reviewStoreFor wires the deck's diff modal to the review store. Load and Save
// resolve the review from the workspace the row points at, so the deck never
// needs to know a review id.
// lastSaved carries the most recently written comment back to the send path,
// which needs the store-assigned id.
var lastSaved atomic.Pointer[review.Comment]

func reviewStoreFor(runner Runner) deckui.CommentStore {
	open := func(item deckui.Item) (review.Store, review.Review, error) {
		store := review.Store{}
		r, err := store.Open(item.RepoRoot, review.Target{
			Kind:      review.TargetWorking,
			Workspace: item.WorkspaceName,
		})
		return store, r, err
	}
	return deckui.CommentStore{
		Load: func(item deckui.Item) ([]review.Comment, error) {
			store, r, err := open(item)
			if err != nil {
				return nil, err
			}
			return store.Comments(r)
		},
		Save: func(item deckui.Item, c review.Comment) error {
			store, r, err := open(item)
			if err != nil {
				return err
			}
			saved, err := store.AddComment(r, c)
			if err != nil {
				return err
			}
			// Remember what was written, id included: the send-to-agent prompt
			// needs it so the agent can reply on this thread rather than filing a
			// second comment beside it.
			lastSaved.Store(&saved)
			return nil
		},
		Update: func(item deckui.Item, c review.Comment) error {
			store, r, err := open(item)
			if err != nil {
				return err
			}
			// Read the stored record first so a revise keeps everything the
			// editor does not own — state, timestamps, publish record.
			existing, err := store.Comments(r)
			if err != nil {
				return err
			}
			for _, e := range existing {
				if e.ID == c.ID {
					e.Body = c.Body
					return store.UpdateComment(r, e)
				}
			}
			return store.UpdateComment(r, c)
		},
		Reply: func(item deckui.Item, parentID string, c review.Comment) error {
			store, r, err := open(item)
			if err != nil {
				return err
			}
			// store.Reply reopens the parent, so the badge counts the thread as
			// needing attention again.
			_, err = store.Reply(r, parentID, c)
			return err
		},
		Delete: func(item deckui.Item, id string) error {
			store, r, err := open(item)
			if err != nil {
				return err
			}
			return store.DeleteComment(r, id)
		},
	}
}

// sendCommentToAgentFor wires the diff modal's send-to-agent exit. The comment
// is already saved by the time this runs, so a send failure leaves a durable
// record rather than losing what the reviewer wrote.
// runnerOrExec defaults a nil runner to the real one.
func runnerOrExec(r Runner) Runner {
	if r == nil {
		return NewExecRunner()
	}
	return r
}

func sendCommentToAgentFor(tmuxClient *tmux.Client, svc workspace.Service) deckui.CommentSender {
	return func(item deckui.Item, c review.Comment) error {
		// noopReporter, not nil: sendPromptToAgent calls reporter.Step on every
		// path, so a nil interface panics rather than sending anything.
		// Name the revision so the agent knows which version of the file the
		// comment was written against. Best-effort: an unresolvable change id
		// falls back to "your working copy", which is correct for a
		// workspace-scoped review anyway.
		revision, _, _ := jj.New(runnerOrExec(nil)).HeadDescription(item.Path)
		if err := sendPromptToAgent(tmuxClient, svc, item, commentPromptFor(c, revision), noopReporter{}); err != nil {
			return err
		}
		store := review.Store{}
		r, err := store.Open(item.RepoRoot, review.Target{
			Kind:      review.TargetWorking,
			Workspace: item.WorkspaceName,
		})
		if err != nil {
			// The prompt went out; failing to update the record would be
			// misleading but is not worth undoing the send.
			return nil
		}
		return markCommentSent(store, r, c)
	}
}

// publishReviewFor wires the viewer's `P` to the same publish path
// `awp review publish` runs.
//
// The PR comes from the workspace row rather than from the working directory: the
// deck runs in the source repo, so resolving it the way the command does would
// find the wrong review — or none. Everything after that is the command's own
// code, so a publish from the viewer and a publish from a shell cannot drift.
func publishReviewFor(runner Runner) func(deckui.Item, string, bool) (string, error) {
	return func(item deckui.Item, verdict string, dryRun bool) (string, error) {
		event, err := parseVerdict(verdict)
		if err != nil {
			return "", err
		}
		store := review.Store{}
		r, err := store.Open(item.RepoRoot, review.Target{
			Kind:      review.TargetWorking,
			Workspace: item.WorkspaceName,
		})
		if err != nil {
			return "", err
		}
		comments, err := store.Comments(r)
		if err != nil {
			return "", err
		}
		if item.PRNumber <= 0 {
			return "", errors.New("this workspace isn't linked to a PR (link one with `p #`)")
		}
		var buf bytes.Buffer
		perr := publishReview(runner, publishRequest{
			Store:    store,
			Review:   r,
			Comments: comments,
			PR:       item.PRNumber,
			Event:    event,
			Verdict:  verdict,
			DryRun:   dryRun,
		}, &buf)
		// The report is worth having even when part of the run failed — it says what
		// did land, which is exactly what a reviewer needs in order to retry. Handed
		// back whole (not squashed) so the viewer can show the plan a line per call;
		// the footer does its own squashing.
		return buf.String(), perr
	}
}

// reviewStoreWithSend is the full store seam: load, save, and hand to the agent.
func reviewStoreWithSend(runner Runner, tmuxClient *tmux.Client, svc workspace.Service) deckui.CommentStore {
	cs := reviewStoreFor(runner)
	cs.Send = sendCommentToAgentFor(tmuxClient, svc)
	cs.Publish = publishReviewFor(runner)
	cs.LoadReviewed, cs.SaveReviewed = reviewedMarksFor()
	cs.LoadThreads, cs.Resolve = threadActionsFor(runner)
	cs.LastSaved = lastSavedComment
	return cs
}

// reviewedMarksFor wires the reviewed-file marks to the store's review.json.
func reviewedMarksFor() (
	load func(deckui.Item) (map[string]string, error),
	save func(deckui.Item, string, string) error,
) {
	open := func(item deckui.Item) (review.Store, review.Review, error) {
		store := review.Store{}
		r, err := store.Open(item.RepoRoot, review.Target{
			Kind:      review.TargetWorking,
			Workspace: item.WorkspaceName,
		})
		return store, r, err
	}
	load = func(item deckui.Item) (map[string]string, error) {
		_, r, err := open(item)
		if err != nil {
			return nil, err
		}
		return r.ReviewedFile, nil
	}
	save = func(item deckui.Item, path, hash string) error {
		store, r, err := open(item)
		if err != nil {
			return err
		}
		if r.ReviewedFile == nil {
			r.ReviewedFile = map[string]string{}
		}
		if hash == "" {
			delete(r.ReviewedFile, path)
		} else {
			r.ReviewedFile[path] = hash
		}
		return store.Save(r)
	}
	return load, save
}

// mirrorReviewThreads caches a PR's review threads into the workspace's review,
// converting GitHub's diff-side vocabulary into ours.
func mirrorReviewThreads(repoRoot, workspaceName string, threads []github.ReviewThread) error {
	store := review.Store{}
	r, err := store.Open(repoRoot, review.Target{Kind: review.TargetWorking, Workspace: workspaceName})
	if err != nil {
		return err
	}
	out := make([]review.Thread, 0, len(threads))
	for _, t := range threads {
		side := review.SideNew
		if strings.EqualFold(t.Side, "LEFT") {
			side = review.SideOld
		}
		mirrored := review.Thread{
			ID: t.ID, Path: t.Path, Side: side, Line: t.Line, StartLine: t.StartLine,
			Resolved: t.Resolved, Outdated: t.Outdated,
		}
		for _, c := range t.Comments {
			mirrored.Comments = append(mirrored.Comments, review.ThreadComment{Author: c.Author, Body: c.Body})
		}
		out = append(out, mirrored)
	}
	return store.SaveThreads(r, out)
}

// threadActionsFor wires thread loading and resolution into the diff modal.
func threadActionsFor(runner Runner) (
	load func(deckui.Item) ([]review.Thread, error),
	resolve func(deckui.Item, string, bool) error,
) {
	open := func(item deckui.Item) (review.Store, review.Review, error) {
		store := review.Store{}
		r, err := store.Open(item.RepoRoot, review.Target{
			Kind:      review.TargetWorking,
			Workspace: item.WorkspaceName,
		})
		return store, r, err
	}
	load = func(item deckui.Item) ([]review.Thread, error) {
		store, r, err := open(item)
		if err != nil {
			return nil, err
		}
		return store.Threads(r), nil
	}
	resolve = func(item deckui.Item, threadID string, want bool) error {
		// In the workspace's repo, not in whatever directory the deck was launched
		// from: which repository a gh call is about comes from where gh runs.
		gh := github.New(runner).In(item.RepoRoot)
		if want {
			if err := gh.ResolveReviewThread(threadID); err != nil {
				return err
			}
		} else if err := gh.UnresolveReviewThread(threadID); err != nil {
			return err
		}
		// Mirror the new state locally so the diff reflects it without a refetch.
		store, r, err := open(item)
		if err != nil {
			return nil
		}
		threads := store.Threads(r)
		for i := range threads {
			if threads[i].ID == threadID {
				threads[i].Resolved = want
			}
		}
		return store.SaveThreads(r, threads)
	}
	return load, resolve
}

// lastSavedComment reports the most recently written comment, so the send path
// can name its id in the agent prompt.
func lastSavedComment() (review.Comment, bool) {
	if c := lastSaved.Load(); c != nil {
		return *c, true
	}
	return review.Comment{}, false
}
