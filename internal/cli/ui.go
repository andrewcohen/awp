package cli

import (
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/andrewcohen/awp/internal/charm"
	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/editor"
	"github.com/andrewcohen/awp/internal/jj"
	"github.com/andrewcohen/awp/internal/ui"
)

// diffLoaderFor backs the deck's in-deck diff modal (`c`): the git-format
// diff of a workspace's working change, the same source `awp diff` reads.
func diffLoaderFor(runner Runner) deckui.DiffLoader {
	return func(item deckui.Item, scope deckui.DiffScope) (string, error) {
		if runner == nil {
			runner = NewExecRunner()
		}
		revision := ""
		if scope == deckui.ScopeStackBase {
			// The whole change against its stack base. Base resolution:
			// nearest stacked-parent bookmark, falling back to trunk().
			base := resolveReviewStackBase(runner, item.Path, item.Bookmark)
			revision = base + "..@"
		}
		return jj.New(runner).DiffGit(item.Path, revision)
	}
}

// diffBaseResolverFor names what a stack-base diff is read against, for the
// modal's footer. Same resolution the loader uses — the label is whatever that
// picked, with the trunk fallback spelled out as the branch it names rather than
// left as the literal "trunk()".
//
// Only ScopeStackBase has a base worth naming: the working-copy scope is diffed
// against @ itself, which "working copy" already says.
func diffBaseResolverFor(runner Runner) deckui.DiffBaseResolver {
	return func(item deckui.Item, scope deckui.DiffScope) string {
		if scope != deckui.ScopeStackBase {
			return ""
		}
		if runner == nil {
			runner = NewExecRunner()
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

func runDiffWithCharm(runner Runner, in io.Reader, out io.Writer) error {
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
	model := ui.New(repoRoot,
		func() (string, error) { return j.DiffGit(cwd, "") },
		func(filePath string, line int) tea.Cmd {
			return tea.ExecProcess(editor.OpenExecCmd("", filePath, line), func(err error) tea.Msg {
				if err != nil {
					return err
				}
				return nil
			})
		},
	)
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithInput(in), tea.WithOutput(out))
	_, err = program.Run()
	return err
}
