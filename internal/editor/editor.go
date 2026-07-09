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

// OpenExecCmd builds an exec.Cmd to open the file in the editor.
func OpenExecCmd(editorCmd, filePath string, line int) *exec.Cmd {
	args := BuildArgs(editorCmd, filePath, line)
	cmd := exec.Command(args[0], args[1:]...) //nolint:gosec
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}
