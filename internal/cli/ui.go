package cli

import (
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/andrewcohen/awp/internal/editor"
	"github.com/andrewcohen/awp/internal/jj"
	"github.com/andrewcohen/awp/internal/ui"
)

func runUIWithCharm(runner Runner, in io.Reader, out io.Writer) error {
	if osTermDumb() {
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
