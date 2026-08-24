package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/andrewcohen/awp/internal/charm"
	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/editor"
	"github.com/andrewcohen/awp/internal/github"
	"github.com/andrewcohen/awp/internal/jj"
	"github.com/andrewcohen/awp/internal/tmux"
	"github.com/andrewcohen/awp/internal/ui"
	"github.com/andrewcohen/awp/internal/workspace"
)

// diffLoaderFor backs the deck's in-deck diff viewer (`c`): the git-format diff
// of a workspace at the requested scope, the same source `awp diff` reads.
func diffLoaderFor(runner Runner) deckui.DiffLoader {
	return func(item deckui.Item, scope deckui.DiffScope, contextLines int) (string, error) {
		if runner == nil {
			runner = NewExecRunner()
		}
		return jj.New(runner).DiffGit(item.Path, scopeRevset(runner, item, scope), contextLines)
	}
}

// prDescriptionLoader backs the deck's in-deck PR description (`p d`).
//
// Read in the *workspace's* directory rather than wherever the deck process was
// started, so the PR number is resolved against the repo the row belongs to. A
// deck spanning several projects would otherwise hand gh a number and let it
// find whichever repo the process happened to be in — the class of bug #88 was
// about, which is why github.New takes the directory as an argument.
func prDescriptionLoader(runner Runner) deckui.PRDescriptionLoader {
	return func(item deckui.Item, number int) (deckui.PRDescription, error) {
		if runner == nil {
			runner = NewExecRunner()
		}
		dir := strings.TrimSpace(item.RepoRoot)
		if dir == "" {
			dir = item.Path
		}
		info, err := github.New(runner, dir).FetchPR(number)
		if err != nil {
			return deckui.PRDescription{}, err
		}
		return deckui.PRDescription{
			Number: info.Number,
			Title:  info.Title,
			Author: info.Author,
			State:  string(info.State),
			URL:    info.URL,
			Body:   info.Body,
		}, nil
	}
}

// scopeOptionsFor is the `-` menu, built once and installed on whichever viewer is
// being opened. Both surfaces get the same list from here: the deck's modal and
// standalone `awp diff` are the same view, and a key that means one thing in one
// and nothing in the other is the bug this replaced.
//
// dir is the directory diffs are read in — the workspace root, which is not
// necessarily where the process was started.
func scopeOptionsFor(runner Runner, item deckui.Item, dir string) []ui.ScopeOption {
	if runner == nil {
		runner = NewExecRunner()
	}
	load := diffLoaderFor(runner)
	base := diffBaseResolverFor(runner)
	// The order is the order the menu offers them, and the first is the default the
	// view opens on: the whole change against its stack base, which is what a review
	// is normally of.
	scopes := []struct {
		key   string
		scope deckui.DiffScope
	}{
		{"c", deckui.ScopeStackBase},
		{"w", deckui.ScopeWorking},
		{"t", deckui.ScopeTrunk},
	}
	out := make([]ui.ScopeOption, 0, len(scopes)+1)
	for _, s := range scopes {
		it, sc := item, s.scope
		if strings.TrimSpace(dir) != "" {
			it.Path = dir
		}
		out = append(out, ui.ScopeOption{
			Key:   s.key,
			Label: sc.String(),
			Load:  func(contextLines int) (string, error) { return load(it, sc, contextLines) },
			Base:  func() string { return base(it, sc) },
		})
	}
	// And one entry standing for every individual commit, which is the answer the
	// three fixed ranges cannot give: they are ranges ending at @, so a change that
	// has already landed is not reachable through any of them. Last in the menu
	// because it is the one that asks a second question.
	readIn := item.Path
	if strings.TrimSpace(dir) != "" {
		readIn = dir
	}
	out = append(out, ui.ScopeOption{
		Key:     "r",
		Label:   "a revision…",
		Choices: revisionChoicesFor(runner, readIn),
	})
	return out
}

// revisionChoicesFor lists the repo's recent changes as scope options, read when
// the picker is opened rather than when the menu is built.
//
// One option per change, keyed by its change id — which is what the picker shows
// as the entry's name and what `/` filters on, so a half-remembered id finds its
// change. Each one loads exactly that revision: DiffGit with a single revset is
// the same call the fixed ranges make, so a picked commit reads through the same
// path and relocates comments and the cursor the same way.
func revisionChoicesFor(runner Runner, dir string) func() ([]ui.ScopeOption, error) {
	return func() ([]ui.ScopeOption, error) {
		if runner == nil {
			runner = NewExecRunner()
		}
		client := jj.New(runner)
		ctx, cancel := context.WithTimeout(context.Background(), revisionPickerTimeout)
		defer cancel()
		changes, err := client.RecentChanges(ctx, dir, revisionPickerLimit)
		if err != nil {
			return nil, err
		}
		out := make([]ui.ScopeOption, 0, len(changes))
		for _, ch := range changes {
			rev, desc := ch.ID, ch.Description
			if desc == "" {
				// A change with no description is still worth offering — it is usually
				// the one being written — so it gets said rather than shown blank.
				desc = "(no description)"
			}
			out = append(out, ui.ScopeOption{
				Key:   rev,
				Label: desc,
				Load: func(contextLines int) (string, error) {
					return client.DiffGit(dir, rev, contextLines)
				},
				// A single revision is read against its own parent, which is what the
				// diff of a commit means. Naming it would repeat the entry's own label.
				Base: func() string { return "" },
			})
		}
		return out, nil
	}
}

// How much history the revision picker offers, and how long it will wait for it.
// The list is filtered with `/` rather than paged, so the limit is about what is
// plausible to scroll rather than what fits; the timeout is short because this
// runs while someone is holding a menu open.
const (
	revisionPickerLimit   = 50
	revisionPickerTimeout = 3 * time.Second
)

// scopeRevset is the revision a scope reads. Empty for the working copy, which
// is `jj diff`'s own default.
func scopeRevset(runner Runner, item deckui.Item, scope deckui.DiffScope) string {
	switch scope {
	case deckui.ScopeWorking:
		return ""
	case deckui.ScopeTrunk:
		// The whole stack, however deep, against the repo's default branch.
		// trunk() resolves that itself, so nothing has to be hardcoded.
		return "trunk()..@"
	default:
		// The whole change against its stack base: nearest stacked-parent
		// bookmark, falling back to trunk().
		return resolveReviewStackBase(runner, item.Path, item.Bookmark) + "..@"
	}
}

// diffBaseResolverFor names what a scope's diff is read against, for the
// viewer's footer. Same resolution the loader uses — the label is whatever that
// picked, with the trunk fallback spelled out as the branch it names rather than
// left as the literal "trunk()".
//
// The working-copy scope has no base worth naming: it is diffed against @ itself,
// which "working copy" already says.
func diffBaseResolverFor(runner Runner) deckui.DiffBaseResolver {
	return func(item deckui.Item, scope deckui.DiffScope) string {
		if scope == deckui.ScopeWorking {
			return ""
		}
		if runner == nil {
			runner = NewExecRunner()
		}
		if scope == deckui.ScopeTrunk {
			// Name the branch rather than "trunk()", which jj resolves but which
			// means nothing to a reader.
			trunk, err := jj.New(fixedDirRunner{base: runner, dir: item.Path}).Trunk()
			if err != nil || strings.TrimSpace(trunk) == "" {
				return "trunk"
			}
			return trunk
		}
		_, label := resolveReviewStackBaseNamed(runner, item.Path, item.Bookmark)
		return label
	}
}

// openDiffFileInEditor is the $EDITOR process for a file at a line, for the deck
// to run wherever the diff it was asked from is: the whole terminal via
// tea.ExecProcess when the diff fills it, a pane in the diff's own half when it
// is half of a split. $EDITOR is an external program either way, never a nested
// Bubble Tea program (see the deckui package doc).
//
// dir comes from the viewer, which is the only thing that knows it: it is the root
// the diff's paths were resolved against. The row is not consulted for it — a host
// deriving the directory a second way is how the two would come to disagree.
func openDiffFileInEditor(_ deckui.Item, dir, filePath string, line int) *exec.Cmd {
	return editor.OpenExecCmd(dir, "", filePath, line)
}

// diffSubjectFor resolves what a standalone `awp diff` is a review of: the
// workspace the working directory is in, its repo, and the PR it is pinned to.
//
// The deck knows all of this from the row you selected. Standalone there is no
// row, so it comes from the same lookups `awp review add` and
// `awp review publish` use — which is the point: a comment filed from the viewer
// and a comment filed by an agent in the same directory have to land in the same
// review.
//
// A directory that is not a tracked workspace still yields a subject. Its
// workspace name is empty, which is the review the CLI would resolve there too,
// so reading a plain repo with `awp diff` still gets a working review rather than
// no commenting at all.
func diffSubjectFor(svc workspace.Service, repoRoot, cwd string) deckui.Item {
	item := deckui.Item{
		RepoRoot:    repoRoot,
		Path:        cwd,
		ProjectName: filepath.Base(repoRoot),
	}
	if e, ok := workspaceEntryForPath(svc, cwd); ok {
		item.WorkspaceName = e.Name
		item.PRNumber = e.PRNumber
		// The workspace's own bookmark, which resolving the stack base has to
		// exclude. Without it the nearest bookmarked ancestor of @ *is* the
		// workspace's own bookmark, so the base resolved to the change itself and
		// the default diff came back all but empty.
		item.Bookmark = e.Bookmark
		if strings.TrimSpace(e.Path) != "" {
			// The workspace's own root, not wherever in it you happen to be standing:
			// send-to-agent and the review both key off the workspace, not the cwd.
			item.Path = e.Path
		}
	}
	return item
}

func runDiffWithCharm(runner Runner, svc workspace.Service, revset string, in io.Reader, out io.Writer) error {
	if charm.IsDumbTerminal() {
		return fmt.Errorf("diff ui not available in dumb terminal")
	}
	if runner == nil {
		runner = NewExecRunner()
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}
	j := jj.New(runner)
	// SourceRepoRoot, not RepoRoot: inside a secondary jj workspace `jj root`
	// answers with the *workspace* path, and the review store is keyed by the
	// owning repo. Reading the wrong root here opened a different review than the
	// deck's `c` does — so an agent's findings were simply absent, with nothing
	// reported, which is the worst shape a wrong answer can take on this surface.
	repoRoot, err := j.SourceRepoRoot()
	if err != nil {
		return fmt.Errorf("not a jj repository: %w", err)
	}
	// And the workspace root, which is a different question with a different
	// answer: `jj diff --git` prints paths relative to the workspace it ran in, so
	// this is what the viewer joins them onto — and, since #291, the directory it
	// hands $EDITOR.
	//
	// Both roots are needed and neither can stand in for the other. Using the source
	// repo as the viewer's root meant that inside a secondary workspace — every PR
	// workspace — `e` opened the *source repo's* copy of the file: same relative
	// path, different working copy, so an edit landed somewhere the review was not
	// and nothing said so. Using the workspace root for the store would open a
	// different review than the deck's `c` does, which is the failure the comment
	// above is about.
	viewerRoot, err := j.RepoRoot()
	if err != nil {
		return fmt.Errorf("resolve the workspace root: %w", err)
	}
	subject := diffSubjectFor(svc, repoRoot, cwd)
	openEditor := func(dir, filePath string, line int) tea.Cmd {
		return tea.ExecProcess(editor.OpenExecCmd(dir, "", filePath, line), func(err error) tea.Msg {
			if err != nil {
				return err
			}
			return nil
		})
	}
	var model ui.Model
	if revset = strings.TrimSpace(revset); revset != "" {
		// An explicit -r is one range and only that one: the user named what they
		// want, so there is nothing for `-` to offer and nothing to resolve.
		//
		// Read on every refresh tick rather than pinned to a commit id here, so
		// `-r @-` keeps meaning "the change before this one" as the stack moves
		// under it.
		model = ui.New(viewerRoot, func(contextLines int) (string, error) { return j.DiffGit(cwd, revset, contextLines) }, openEditor)
		// Named as the revset rather than as a resolved commit: that is what the
		// reader will recognise, and it stays true after the change is rewritten.
		model.ResolveBase = func() string { return revset }
	} else {
		// No arguments: resolve itself exactly the way `c` does. Same scope list, so
		// the default range and the `-` menu are one decision made in one place —
		// `awp diff` in a workspace and `c` on its deck row show the same change.
		scopes := scopeOptionsFor(runner, subject, subject.Path)
		model = ui.New(viewerRoot, scopes[0].Load, openEditor)
		model.ResolveBase = scopes[0].Base
		model.WithScopes(scopes)
	}
	// The chrome says what it is a review of, the way the deck's footer does:
	// which workspace, which PR, and what the diff is read against.
	model.Subject = ui.Subject{
		// The project, said out loud, because the viewer's root is now the workspace
		// and its directory name is the workspace's. Inside a PR workspace the header
		// would otherwise name `pr-2336-dev` twice and the repo not at all.
		Project:   strings.TrimSpace(subject.ProjectName),
		Workspace: strings.TrimSpace(subject.WorkspaceName),
		PR:        deckui.PRLabel(subject),
	}
	// The same review seams the deck's modal gets. Without them this was a diff
	// you could only read: no commenting, no comment index, no mirrored GitHub
	// threads, no reviewed marks, no send-to-agent, no publish — which is most of
	// what the surface is for.
	//
	// One wiring function shared with the deck (deckui.ApplyCommentStore), so a
	// seam cannot be present in one surface and quietly missing in the other.
	// nil for the pane host: standalone `awp diff` has no deck to ask, and may well
	// be running *inside* a zdeck pane on a workspace whose agent is a zmx session.
	// agentPromptSender resolves that by looking for the session rather than
	// assuming tmux, which is what used to send a reviewer's remark to an agent
	// started for the occasion and never seen again.
	send := agentPromptSender(nil, runner, tmux.New(runner), svc)
	deckui.ApplyCommentStore(&model, subject, reviewStoreWithSend(runner, send))
	program := tea.NewProgram(model, tea.WithInput(in), tea.WithOutput(out))
	_, err = program.Run()
	return err
}
