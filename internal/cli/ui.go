package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

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
	return func(item deckui.Item, scope deckui.DiffScope) (string, error) {
		if runner == nil {
			runner = NewExecRunner()
		}
		return jj.New(runner).DiffGit(item.Path, scopeRevset(runner, item, scope))
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
	out := make([]ui.ScopeOption, 0, len(scopes))
	for _, s := range scopes {
		it, sc := item, s.scope
		if strings.TrimSpace(dir) != "" {
			it.Path = dir
		}
		out = append(out, ui.ScopeOption{
			Key:   s.key,
			Label: sc.String(),
			Load:  func() (string, error) { return load(it, sc) },
			Base:  func() string { return base(it, sc) },
		})
	}
	return out
}

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

// openDiffFileInEditor opens a file at a line from the diff modal.
// tea.ExecProcess
// is the right tool here — $EDITOR is an external program, not a nested
// Bubble Tea program (see the deckui package doc).
func openDiffFileInEditor(_ deckui.Item, filePath string, line int) tea.Cmd {
	return tea.ExecProcess(editor.OpenExecCmd("", filePath, line), func(err error) tea.Msg {
		if err != nil {
			return err
		}
		return nil
	})
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
	subject := diffSubjectFor(svc, repoRoot, cwd)
	openEditor := func(filePath string, line int) tea.Cmd {
		return tea.ExecProcess(editor.OpenExecCmd("", filePath, line), func(err error) tea.Msg {
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
		model = ui.New(repoRoot, func() (string, error) { return j.DiffGit(cwd, revset) }, openEditor)
		// Named as the revset rather than as a resolved commit: that is what the
		// reader will recognise, and it stays true after the change is rewritten.
		model.ResolveBase = func() string { return revset }
	} else {
		// No arguments: resolve itself exactly the way `c` does. Same scope list, so
		// the default range and the `-` menu are one decision made in one place —
		// `awp diff` in a workspace and `c` on its deck row show the same change.
		scopes := scopeOptionsFor(runner, subject, subject.Path)
		model = ui.New(repoRoot, scopes[0].Load, openEditor)
		model.ResolveBase = scopes[0].Base
		model.WithScopes(scopes)
	}
	// The chrome says what it is a review of, the way the deck's footer does:
	// which workspace, which PR, and what the diff is read against.
	model.Subject = ui.Subject{
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
	deckui.ApplyCommentStore(&model, subject, reviewStoreWithSend(runner, tmux.New(runner), svc))
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithInput(in), tea.WithOutput(out))
	_, err = program.Run()
	return err
}
