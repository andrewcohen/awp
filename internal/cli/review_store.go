package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/andrewcohen/awp/internal/deckui"
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
	case "add", "list":
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
			_, err = store.AddComment(r, c)
			return err
		},
	}
}

// sendCommentToAgentFor wires the diff modal's send-to-agent exit. The comment
// is already saved by the time this runs, so a send failure leaves a durable
// record rather than losing what the reviewer wrote.
func sendCommentToAgentFor(tmuxClient *tmux.Client, svc workspace.Service) deckui.CommentSender {
	return func(item deckui.Item, c review.Comment) error {
		if err := sendPromptToAgent(tmuxClient, svc, item, commentPromptFor(c), nil); err != nil {
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
