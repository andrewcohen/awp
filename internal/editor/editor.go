package editor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Resolve returns the configured editor command.
func Resolve() string {
	if e := strings.TrimSpace(os.Getenv("EDITOR")); e != "" {
		return e
	}
	if e := strings.TrimSpace(os.Getenv("VISUAL")); e != "" {
		return e
	}
	return "vi"
}

// BuildArgs returns the argv for opening a file at a line.
func BuildArgs(editorCmd, filePath string, line int) []string {
	if strings.TrimSpace(editorCmd) == "" {
		editorCmd = Resolve()
	}
	base := filepath.Base(strings.Fields(editorCmd)[0])
	name := strings.ToLower(strings.TrimSuffix(base, filepath.Ext(base)))

	switch name {
	case "code", "codium":
		if line > 0 {
			return append(strings.Fields(editorCmd), "--goto", fmt.Sprintf("%s:%d", filePath, line))
		}
	case "vim", "nvim", "vi", "gvim", "mvim":
		if line > 0 {
			return append(strings.Fields(editorCmd), fmt.Sprintf("+%d", line), filePath)
		}
	case "emacs", "emacsclient", "nano":
		if line > 0 {
			return append(strings.Fields(editorCmd), fmt.Sprintf("+%d", line), filePath)
		}
	case "hx", "helix":
		if line > 0 {
			return append(strings.Fields(editorCmd), fmt.Sprintf("%s:%d", filePath, line))
		}
	}
	return append(strings.Fields(editorCmd), filePath)
}

// OpenExecCmd builds an exec.Cmd to open the file in the editor, running in dir.
//
// dir comes first and is required, rather than being an optional extra a caller
// can leave off — which is what it was, and every caller left it off, so the
// editor inherited the directory awp happened to be started in. The file opened
// (the path is absolute) and nothing else did: reviewing a row in another repo
// from a deck launched in this one gave nvim a cwd in *this* one, so :Explore, the
// fuzzy finder, :grep and the LSP root all addressed the wrong project.
//
// An empty dir still means "inherit", because that is what exec means by it and
// there is no better answer for a caller that genuinely has no directory. The
// point of the parameter is that a caller has to say so out loud.
func OpenExecCmd(dir, editorCmd, filePath string, line int) *exec.Cmd {
	args := BuildArgs(editorCmd, filePath, line)
	cmd := exec.Command(args[0], args[1:]...) //nolint:gosec
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}
