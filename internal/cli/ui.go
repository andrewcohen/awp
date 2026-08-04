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
	repoRoot, err := j.RepoRoot()
	if err != nil {
		return fmt.Errorf("not a jj repository: %w", err)
	}
	revset = strings.TrimSpace(revset)
	model := ui.New(repoRoot,
		// Read on every refresh tick, so the revset is resolved by jj each time
		// rather than pinned to a commit id here: `-r @-` should keep meaning "the
		// change before this one" as the stack moves under it.
		func() (string, error) { return j.DiffGit(cwd, revset) },
		func(filePath string, line int) tea.Cmd {
			return tea.ExecProcess(editor.OpenExecCmd("", filePath, line), func(err error) tea.Msg {
				if err != nil {
					return err
				}
				return nil
			})
		},
	)
	if revset != "" {
		// The chrome says what it is showing. Named as the revset the user typed,
		// not as a resolved commit — that is what they will recognise, and it is
		// still true after the change is rewritten.
		model.ResolveBase = func() string { return revset }
	}
	// The same review seams the deck's modal gets. Without them this was a diff
	// you could only read: no commenting, no comment index, no mirrored GitHub
	// threads, no reviewed marks, no send-to-agent, no publish — which is most of
	// what the surface is for.
	//
	// One wiring function shared with the deck (deckui.ApplyCommentStore), so a
	// seam cannot be present in one surface and quietly missing in the other.
	deckui.ApplyCommentStore(&model, diffSubjectFor(svc, repoRoot, cwd),
		reviewStoreWithSend(runner, tmux.New(runner), svc))
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithInput(in), tea.WithOutput(out))
	_, err = program.Run()
	return err
}
