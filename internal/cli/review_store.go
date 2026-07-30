package cli

import (
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
// This replaces shelling out to `tuicr review add --session <abs-path>`, where
// the session path had to be reverse-engineered from tuicr's private state files
// with a discovery timeout and a window-launch-order hack. Here the review is
// resolved from the workspace, which awp already knows.

// reviewTargetFor resolves which review the current directory belongs to.
//
// Keyed by workspace for now. PR-targeted reviews arrive with PR mode in phase 6
// of the review-surface spec; until then a workspace-keyed review is effectively
// the PR's review anyway, because `awp review <pr>` creates a dedicated
// workspace per PR. Re-keying later is safe: comments anchor to content, so
// nothing about them depends on the review's identity (D1).
func reviewTargetFor(svc workspace.Service, cwd string) review.Target {
	return review.Target{Kind: review.TargetWorking, Workspace: workspaceNameForPath(svc, cwd)}
}

// workspaceNameForPath is the workspace containing cwd, longest match first so a
// nested workspace beats its parent.
func workspaceNameForPath(svc workspace.Service, cwd string) string {
	if svc == nil {
		return ""
	}
	entries, err := svc.List()
	if err != nil {
		return ""
	}
	best, name := "", ""
	for _, e := range entries {
		if e.Path == "" {
			continue
		}
		if cwd == e.Path || strings.HasPrefix(cwd, e.Path+string(os.PathSeparator)) {
			if len(e.Path) > len(best) {
				best, name = e.Path, e.Name
			}
		}
	}
	return name
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
	repoRoot, err := jj.New(runner).RepoRoot()
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

func runReviewAdd(runner Runner, svc workspace.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("review add", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var (
		path   = fs.String("file", "", "path of the file being commented on (repo-relative)")
		line   = fs.Int("line", 0, "line number the comment attaches to")
		side   = fs.String("side", "new", "which side of the diff the line is on: new or old")
		body   = fs.String("body", "", "the comment text")
		author = fs.String("author", "", "who is filing this (defaults to the agent name, or 'agent')")
		text   = fs.String("text", "", "the anchored line's text, so the comment survives the line moving")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*path) == "" || strings.TrimSpace(*body) == "" {
		return errors.New("review add requires --file and --body")
	}
	if *line <= 0 {
		return errors.New("review add requires --line")
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
		Anchor: review.Anchor{
			Path:     strings.TrimSpace(*path),
			Side:     anchorSide,
			LineHint: *line,
			Text:     *text,
		},
	})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "added finding %s on %s:%d\n", c.ID, c.Anchor.Path, c.Anchor.LineHint)
	return nil
}

func runReviewReply(runner Runner, svc workspace.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("review reply", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var (
		to     = fs.String("to", "", "id of the comment being replied to")
		body   = fs.String("body", "", "the reply text")
		author = fs.String("author", "", "who is replying (defaults to 'agent')")
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
	c, err := store.Reply(r, *to, review.Comment{Author: who, Body: *body})
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
		_, _ = fmt.Fprintf(out, "%s\t%s\t%s:%d\t%s\n", c.ID, c.State, c.Anchor.Path, c.Anchor.LineHint, oneLine(c.Body))
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
func sendCommentToAgentFor(tmuxClient *tmux.Client, svc workspace.Service) deckui.CommentSender {
	return func(item deckui.Item, c review.Comment) error {
		// noopReporter, not nil: sendPromptToAgent calls reporter.Step on every
		// path, so a nil interface panics rather than sending anything.
		if err := sendPromptToAgent(tmuxClient, svc, item, commentPromptFor(c), noopReporter{}); err != nil {
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

// reviewStoreWithSend is the full store seam: load, save, and hand to the agent.
func reviewStoreWithSend(runner Runner, tmuxClient *tmux.Client, svc workspace.Service) deckui.CommentStore {
	cs := reviewStoreFor(runner)
	cs.Send = sendCommentToAgentFor(tmuxClient, svc)
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
		gh := github.New(runner)
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
