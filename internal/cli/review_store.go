package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"time"

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
// from the workspace entry instead (see reviewScope.entry). Comments anchor to
// content, so nothing about them depends on the review's identity (D1).
func reviewTargetFor(svc workspace.Service, cwd string) review.Target {
	e, _ := workspaceEntryForPath(svc, cwd)
	return review.Target{Kind: review.TargetWorking, Workspace: e.Name}
}

// workspaceEntryForPath is the workspace containing cwd, longest match first so
// a nested workspace beats its parent.
//
// A tie on path length goes to the entry that is not `default`. Two entries should
// never share a path, but when they did — see the note in workspace.List — the tie
// was broken by name order instead, and `default` sorts first. That silently
// answered "which workspace is this?" with the one workspace that is definitionally
// somewhere else, so findings were filed against the wrong review and the real
// entry's PR number was never seen. Preferring the named workspace makes the
// remaining ambiguity harmless rather than actively wrong.
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
		if cwd != e.Path && !strings.HasPrefix(cwd, e.Path+string(os.PathSeparator)) {
			continue
		}
		switch {
		case len(e.Path) > len(best):
		case len(e.Path) == len(best) && ok && found.Name == "default" && e.Name != "default":
		default:
			continue
		}
		best, found, ok = e.Path, e, true
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

// reviewScope is the review a command resolved, with enough about how it got there
// to say so out loud.
//
// Saying so is the point. `review add` used to report "added suggestion c7 on
// x.go:12" and never name the destination, so the one way to get this wrong was
// also invisible: an agent run from the source repo rather than from the
// workspace resolves to that repo's own review, which is not the review the
// person reading has open. Both sides report success and the finding is nowhere.
// That is not hypothetical — seven findings on a real PR went that way, and the
// only reason it was ever noticed is that the reviewer expected them. Now every
// write names where it went, so a mismatch shows up in the agent's own transcript
// at the moment it happens.
type reviewScope struct {
	store     review.Store
	review    review.Review
	workspace string
	// entry is the workspace's row, when awp knows it. Publishing reads two things
	// off it — the directory whose commit the comments were read against, and the PR
	// the workspace is pinned to — so that both follow the review rather than the
	// process's own directory.
	entry workspace.ListEntry
}

// label is the destination, for the line a command prints on success.
//
// The review id and the workspace both, because the id is a slug of the name —
// they differ whenever a workspace name has a character a slug drops, and the id
// is what to look for on disk while the name is what to pass to --workspace.
func (s reviewScope) label() string {
	if s.workspace == "" {
		// The id is "work-" with nothing after it: no deck row points at that review,
		// so nothing will ever read it. Said plainly rather than left to be inferred
		// from a trailing dash.
		return fmt.Sprintf("review %s (no awp workspace contains this directory — pass --workspace)", s.review.ID)
	}
	return fmt.Sprintf("review %s (workspace %s)", s.review.ID, s.workspace)
}

// openReviewFor resolves the review a command should write to: the one belonging
// to wsName, or — when that is empty — to the workspace containing the current
// directory.
//
// The explicit name exists because running an agent from the source repo is a
// normal thing to do, and until now the only way to reach a workspace's review
// was to be standing in it.
func openReviewFor(runner Runner, svc workspace.Service, wsName string) (reviewScope, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return reviewScope{}, err
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
		return reviewScope{}, fmt.Errorf("not a jj repository: %w", err)
	}
	entry, err := resolveReviewWorkspace(svc, cwd, wsName)
	if err != nil {
		return reviewScope{}, err
	}
	store := review.Store{}
	r, err := store.Open(repoRoot, review.Target{Kind: review.TargetWorking, Workspace: entry.Name})
	if err != nil {
		return reviewScope{}, err
	}
	return reviewScope{store: store, review: r, workspace: entry.Name, entry: entry}, nil
}

// dir is where the reviewed change lives: the workspace's own directory, or the
// process's when awp does not know the workspace.
func (s reviewScope) dir(cwd string) string {
	if strings.TrimSpace(s.entry.Path) != "" {
		return s.entry.Path
	}
	return cwd
}

// resolveReviewWorkspace decides which workspace's review a command is about.
//
// A name that matches nothing is an error rather than a new review: a typo would
// otherwise open an empty store under the misspelling and report success, which is
// the same silent loss --workspace exists to prevent. Unmatchable for want of a
// workspace list is not the same thing — the caller was explicit, and there is
// nothing to check the name against.
func resolveReviewWorkspace(svc workspace.Service, cwd, wsName string) (workspace.ListEntry, error) {
	name := strings.TrimSpace(wsName)
	if name == "" {
		e, _ := workspaceEntryForPath(svc, cwd)
		return e, nil
	}
	if svc == nil {
		return workspace.ListEntry{Name: name}, nil
	}
	entries, err := svc.List()
	if err != nil {
		return workspace.ListEntry{Name: name}, nil
	}
	known := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Name == name {
			return e, nil
		}
		known = append(known, e.Name)
	}
	sort.Strings(known)
	return workspace.ListEntry{}, fmt.Errorf("no workspace named %q in this repo (have: %s)", name, strings.Join(known, ", "))
}

// workspaceFlag registers --workspace on a review subcommand.
func workspaceFlag(fs *flag.FlagSet) *string {
	return fs.String("workspace", "",
		"file into this workspace's review instead of the one containing the current directory")
}

// rangeEnd is the end line to record for a --line / --end-line pair: zero unless
// the end is genuinely below the start.
func rangeEnd(line, end int) int {
	if end > line {
		return end
	}
	return 0
}

// commentBody resolves the two ways to hand a body to the CLI. A file wins over
// --body: passing both is a mistake, and the file is the more deliberate of the two.
//
// --body-file exists because argv is a bad channel for markdown. A review body is
// full of backticks, and an agent composing a shell command escapes them for a
// quoting context it has to guess — guess wrong and awp stores the escapes. That is
// not hypothetical: seven findings on a real PR were published reading
// "Pin the \`graphql_client\` git dep", because a backslash-backtick inside single
// quotes is two literal characters rather than an escaped backtick. A file has no
// quoting at all, so nothing can be mis-escaped into it.
func commentBody(body, bodyFile string, stdin io.Reader) (string, error) {
	if strings.TrimSpace(bodyFile) == "" {
		// Only the argv path is de-escaped: a file went through no shell, so a
		// backslash in one is what the author meant.
		return unescapeShellBackticks(body), nil
	}
	read := func() ([]byte, error) {
		if bodyFile == "-" {
			if stdin == nil {
				return nil, errors.New("review: --body-file - needs something on stdin")
			}
			return io.ReadAll(stdin)
		}
		return os.ReadFile(bodyFile)
	}
	b, err := read()
	if err != nil {
		return "", fmt.Errorf("review: reading the body: %w", err)
	}
	return string(b), nil
}

// unescapeShellBackticks repairs a body whose every backtick arrived escaped.
//
// Deliberately conditional. A body that escapes *every* backtick and uses none
// plainly cannot have meant it — markdown escapes a backtick to display one
// literally, which is a thing you do occasionally, not uniformly. A body that mixes
// the two is expressing intent and is left exactly as written.
func unescapeShellBackticks(body string) string {
	if !strings.Contains(body, "\\`") {
		return body
	}
	if strings.Contains(strings.ReplaceAll(body, "\\`", ""), "`") {
		// Some backticks are plain, so the escaping is a choice rather than an accident.
		return body
	}
	return strings.ReplaceAll(body, "\\`", "`")
}

func runReviewAdd(runner Runner, svc workspace.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("review add", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var (
		path     = fs.String("file", "", "path of the file being commented on (repo-relative)")
		line     = fs.Int("line", 0, "line number the comment attaches to")
		endLine  = fs.Int("end-line", 0, "last line, for a comment about a block rather than a line")
		side     = fs.String("side", "new", "which side of the diff the line is on: new or old")
		body     = fs.String("body", "", "the comment text")
		bodyFile = fs.String("body-file", "", "read the comment text from a file, or - for stdin (preferred for anything with markdown in it)")
		author   = fs.String("author", "", "who is filing this (defaults to the agent name, or 'agent')")
		text     = fs.String("text", "", "the anchored line's text, so the comment survives the line moving")
		endText  = fs.String("end-text", "", "the last line's text, the same way --text anchors the first")
		kind     = fs.String("type", string(review.KindComment), "what the comment is asking for: comment, suggestion, question, or praise")
		wsName   = workspaceFlag(fs)
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	bodyText, err := commentBody(*body, *bodyFile, os.Stdin)
	if err != nil {
		return err
	}
	if strings.TrimSpace(bodyText) == "" {
		return errors.New("review add requires --body or --body-file")
	}
	// The three scopes, each spelled by what is passed (see review.Anchor.Scope):
	// --file and --line is a line, --file alone is the file as a whole, neither is
	// the change as a whole. A --line without a --file is the one incoherent
	// combination — there is nothing for the number to mean — and it is refused
	// rather than read as one of the three, because either flag could be the
	// mistake and guessing which files the remark somewhere nobody asked for.
	if strings.TrimSpace(*path) == "" && *line > 0 {
		return errors.New("review add: --line needs --file")
	}
	// --end-line describes a block of lines, so it needs lines. Caught here because
	// the alternative is a file-level anchor silently carrying an end that nothing
	// reads, which would then publish as a comment on the file with a range in the
	// record and no range in the remark.
	if *line <= 0 && *endLine > 0 {
		return errors.New("review add: --end-line needs --line")
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
	scope, err := openReviewFor(runner, svc, *wsName)
	if err != nil {
		return err
	}
	who := strings.TrimSpace(*author)
	if who == "" {
		who = "agent"
	}
	c, err := scope.store.AddComment(scope.review, review.Comment{
		Author: who,
		Body:   bodyText,
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
	_, _ = fmt.Fprintf(out, "added %s %s to %s on %s\n",
		c.Kind.OrDefault(), c.ID, scope.label(), c.Anchor.Where())
	// After the write, deliberately. See warnAnchorOutsideDiff.
	warnAnchorOutsideDiff(runner, scope, c.Anchor, out)
	return nil
}

// warnAnchorOutsideDiff says so when a finding names a file the review's own
// diff does not touch.
//
// The signal: a path that is not in the change usually means the wrong review
// was picked. #84 made that failure *visible* — every write names the review it
// landed in — but naming it only helps a reader who reads it, and an agent
// filing a dozen findings in a row is not reading twelve confirmation lines.
//
// A warning, never a refusal, and printed after the comment is already on disk.
// A finding is worth keeping even when its anchor looks wrong: the words are the
// valuable part and an anchor can be repaired. Writing first also means a slow or
// broken check can never cost the caller the finding.
//
// Silent when it cannot tell — no workspace directory, or jj failing. #94's rule:
// a check that does not know must not manufacture a complaint, or the warning
// stops meaning anything and starts being scrolled past.
//
// File membership only, not the line. The line-level answer needs the whole patch
// rendered and parsed — parseCommentable does exactly that on the publish side,
// where it runs once per publish. This runs on every `review add`, so it asks jj
// for names alone (jj.ChangedPaths, `--name-only`). A stale line inside a file
// that really is in the change is a different question, and #111's.
func warnAnchorOutsideDiff(runner Runner, scope reviewScope, a review.Anchor, out io.Writer) {
	path := strings.TrimSpace(a.Path)
	// A remark about the change as a whole names no file, so there is nothing that
	// could be outside it.
	if path == "" {
		return
	}
	dir := strings.TrimSpace(scope.entry.Path)
	if dir == "" {
		return
	}
	// The same range the viewer opens this workspace on, so the diff being checked
	// against is the diff the reviewer would be looking at.
	base := resolveReviewStackBase(runner, dir, scope.entry.Bookmark)
	revset := base + "..@"
	paths, err := jj.New(fixedDirRunner{base: runner, dir: dir}).ChangedPaths(dir, revset)
	if err != nil {
		return
	}
	// An empty diff is counted as "cannot tell" rather than "nothing is in it".
	// Both readings are available and the wrong one is much more expensive: a range
	// that resolved badly reports every finding as misfiled, which is a warning on
	// every call, which is a warning nobody reads by the third one. And ranges do
	// resolve badly — a change was resolving as its own base until recently, and
	// that produced exactly this, an empty diff that looked authoritative.
	if len(paths) == 0 {
		return
	}
	for _, p := range paths {
		if p == path {
			return
		}
	}
	_, _ = fmt.Fprintf(out, "warning: %s is not in this review's diff (%s) — check --workspace, or the path\n", path, revset)
}

func runReviewReply(runner Runner, svc workspace.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("review reply", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var (
		to       = fs.String("to", "", "id of the comment being replied to")
		body     = fs.String("body", "", "the reply text")
		bodyFile = fs.String("body-file", "", "read the reply text from a file, or - for stdin (preferred for anything with markdown in it)")
		author   = fs.String("author", "", "who is replying (defaults to 'agent')")
		kind     = fs.String("type", string(review.KindComment), "what the reply is asking for: comment, suggestion, question, or praise")
		proposal = fs.Bool("proposal", false, "the reply is a change you intend to make, and needs approval before you make it")
		wsName   = workspaceFlag(fs)
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	bodyText, err := commentBody(*body, *bodyFile, os.Stdin)
	if err != nil {
		return err
	}
	if strings.TrimSpace(*to) == "" || strings.TrimSpace(bodyText) == "" {
		return errors.New("review reply requires --to and one of --body / --body-file")
	}
	scope, err := openReviewFor(runner, svc, *wsName)
	if err != nil {
		return err
	}
	who := strings.TrimSpace(*author)
	if who == "" {
		who = "agent"
	}
	reply := review.Comment{Author: who, Body: bodyText, Kind: review.ParseKind(*kind)}
	if *proposal {
		reply.Proposal = review.ProposalPending
	}
	c, err := scope.store.Reply(scope.review, *to, reply)
	if err != nil {
		return err
	}
	// A proposal says so on the way out, because the two replies do different
	// things to the caller: an ordinary one is said and done, and this one means
	// stop and wait. The agent that just ran the command is the reader here.
	if c.AwaitingApproval() {
		_, _ = fmt.Fprintf(out, "proposed to %s (%s) in %s — awaiting approval\n", *to, c.ID, scope.label())
		return nil
	}
	_, _ = fmt.Fprintf(out, "replied to %s (%s) in %s\n", *to, c.ID, scope.label())
	return nil
}

func runReviewList(runner Runner, svc workspace.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("review list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "emit JSON")
	wsName := workspaceFlag(fs)
	if err := fs.Parse(args); err != nil {
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
	if *asJSON {
		// Left a bare array: this is the machine channel, and callers parse it as a
		// list of comments. The review it came from is named in the human form, which
		// is what an agent should use to check it is writing where it thinks.
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(comments)
	}
	// Named first, and even when there is nothing in it: "no findings" from the wrong
	// review is the reading that sends someone looking for a bug in the store.
	_, _ = fmt.Fprintf(out, "%s\n", scope.label())
	if len(comments) == 0 {
		_, _ = fmt.Fprintln(out, "no findings")
		return nil
	}
	for _, c := range comments {
		_, _ = fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%s\t%s\n",
			c.ID, c.Kind.OrDefault(), c.State, proposalColumn(c), c.Anchor.Where(), bodyPreview(c.Body))
	}
	return nil
}

// proposalColumn is where a proposal stands, for the listing. This is the whole
// answer to "was I approved" — there is no dedicated query command, so an agent
// told to stop and wait comes back here to find out.
//
// A column of its own rather than folded into the state column, which holds a
// review.State: one column, two vocabularies is how a reader ends up matching
// "approved" against the wrong field. Present on every row, `-` where there is no
// proposal, so the columns line up and a parser can index them.
func proposalColumn(c review.Comment) string {
	if !c.IsProposal() {
		return "-"
	}
	return string(c.Proposal)
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
		// MarkPublished is the write Update deliberately refuses to make.
		//
		// Update keeps the stored state so a revise cannot clobber a publish record —
		// which is right, and is exactly why recording a publish needs its own way in.
		// A reply posted from the viewer went out, came back through Update, and had its
		// State=Published dropped here; the reply then sat on the PR while the diff went
		// on labelling it unsent.
		MarkPublished: func(item deckui.Item, id, remoteID string) error {
			store, r, err := open(item)
			if err != nil {
				return err
			}
			existing, err := store.Comments(r)
			if err != nil {
				return err
			}
			for _, e := range existing {
				if e.ID != id {
					continue
				}
				e.State = review.Published
				e.Publish = &review.PublishRecord{ThreadID: remoteID, At: time.Now()}
				return store.UpdateComment(r, e)
			}
			// Nothing to mark. Not an error: the record may have been deleted between
			// the post and its answer coming back, and the comment is on GitHub either
			// way — there is nothing here for the caller to do about it.
			return nil
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
		Approve: func(item deckui.Item, id string) (review.Comment, error) {
			store, r, err := open(item)
			if err != nil {
				return review.Comment{}, err
			}
			// store.Approve also moves the finding it answers to sent, so the badge
			// stops asking you to triage a question you have just answered.
			return store.Approve(r, id)
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
func publishReviewFor(runner Runner) func(deckui.Item, string, string, bool) (string, error) {
	return func(item deckui.Item, verdict, summary string, dryRun bool) (string, error) {
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
			// The workspace's own directory and the commit its bookmark points at. The
			// deck reads the latter for every row anyway (it compares against the PR's
			// head to spot a stale workspace), so the usual path resolves the reviewed
			// commit without running jj at all.
			Dir:      item.Path,
			HeadHint: item.BookmarkCommitID,
			Summary:  summary,
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
	cs.LoadThreads, cs.Resolve, cs.ReplyToThread = threadActionsFor(runner)
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
			mirrored.Comments = append(mirrored.Comments, review.ThreadComment{ID: c.ID, Author: c.Author, Body: c.Body})
		}
		out = append(out, mirrored)
	}
	return store.SaveThreads(r, out)
}

// threadActionsFor wires thread loading, resolution and replying into the diff
// modal.
func threadActionsFor(runner Runner) (
	load func(deckui.Item) ([]review.Thread, error),
	resolve func(deckui.Item, string, bool) error,
	reply func(deckui.Item, string, string) (string, error),
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
		gh := github.New(runner, item.RepoRoot)
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
	reply = func(item deckui.Item, threadID, body string) (string, error) {
		// In the workspace's repo, for the same reason resolving is: which repository a
		// gh call is about comes from where gh runs, and the deck runs somewhere else.
		gh := github.New(runner, item.RepoRoot)
		id, err := gh.ReplyToReviewThread(threadID, body)
		if err != nil {
			return "", err
		}
		// Mirrored locally, the same as a resolve: the mirror is what the diff draws,
		// and the job that refreshes it from GitHub runs on its own schedule. Without
		// this the reply would vanish from the conversation on the viewer's next refresh
		// tick and reappear minutes later.
		//
		// Best-effort past this point. The reply is posted; failing to cache it is a
		// display lag, and reporting it as an error would have the viewer treat a
		// delivered reply as undelivered and offer to send it again.
		store, r, oerr := open(item)
		if oerr != nil {
			return id, nil
		}
		threads := store.Threads(r)
		for i := range threads {
			if threads[i].ID != threadID {
				continue
			}
			threads[i].Comments = append(threads[i].Comments, review.ThreadComment{
				// "you", matching how a comment of ours is labelled everywhere else. The next
				// mirror refresh replaces it with the login GitHub reports, which costs a
				// round trip to know and says the same thing.
				ID: id, Author: "you", Body: body,
			})
			_ = store.SaveThreads(r, threads)
			break
		}
		return id, nil
	}
	return load, resolve, reply
}

// lastSavedComment reports the most recently written comment, so the send path
// can name its id in the agent prompt.
func lastSavedComment() (review.Comment, bool) {
	if c := lastSaved.Load(); c != nil {
		return *c, true
	}
	return review.Comment{}, false
}
