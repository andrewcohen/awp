package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/andrewcohen/awp/internal/config"
	"github.com/andrewcohen/awp/internal/jj"
)

const projectConfigTemplate = `{
  "agent": "",
  "agent_options": "",
  "actions": {},
  "hooks": {
    "bootstrap": []
  },
  "deck": {
    "project_roots": []
  }
}
`

const globalConfigTemplate = `{
  "agent": "",
  "agent_options": "",
  "actions": {},
  "hooks": {
    "bootstrap": []
  },
  "deck": {
    "project_roots": []
  }
}
`

func (a *App) runConfig(args []string) error {
	if len(args) == 0 || isHelpArgSlice(args) {
		_, _ = fmt.Fprintln(a.out, "Usage: awp config <init|edit> [--global]")
		if len(args) == 0 {
			return errors.New("config requires a subcommand")
		}
		return nil
	}
	switch args[0] {
	case "init":
		return a.runConfigInit(args[1:])
	case "edit":
		return a.runConfigEdit(args[1:])
	default:
		return fmt.Errorf("unknown config subcommand %q", args[0])
	}
}

func (a *App) runConfigInit(args []string) error {
	if isHelpArgSlice(args) {
		_, _ = fmt.Fprintln(a.out, "Usage: awp config init\nCreates <repo>/.awp/config.json. Must be run from the repo root.")
		return nil
	}
	if len(args) != 0 {
		return errors.New("config init takes no arguments")
	}

	repoRoot, err := jj.New(a.runner).RepoRoot()
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if !samePath(cwd, repoRoot) {
		return fmt.Errorf("config init must be run from the repo root (%s)", repoRoot)
	}

	path := config.ProjectConfigPath(repoRoot)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("config already exists at %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(projectConfigTemplate), 0o644); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(a.out, "Created %s\n", path)
	return nil
}

func (a *App) runConfigEdit(args []string) error {
	if isHelpArgSlice(args) {
		_, _ = fmt.Fprintln(a.out, "Usage: awp config edit [--global]\nOpens the project (or global with --global) config in $EDITOR.")
		return nil
	}
	global := false
	for _, arg := range args {
		switch arg {
		case "--global", "-g":
			global = true
		default:
			return fmt.Errorf("unknown config edit flag %q", arg)
		}
	}

	var path string
	if global {
		path = config.GlobalConfigPath()
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(globalConfigTemplate), 0o644); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	} else {
		repoRoot, err := jj.New(a.runner).RepoRoot()
		if err != nil {
			return err
		}
		path = config.ProjectConfigPath(repoRoot)
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("no project config at %s (try: awp config init)", path)
		} else if err != nil {
			return err
		}
	}

	return openInEditor(path, a.out)
}

func openInEditor(path string, out io.Writer) error {
	editor := strings.TrimSpace(os.Getenv("EDITOR"))
	if editor == "" {
		return errors.New("$EDITOR is not set")
	}
	cmd := exec.Command("sh", "-c", `exec "$EDITOR" "$1"`, "sh", path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				return fmt.Errorf("$EDITOR exited with status %d", status.ExitStatus())
			}
		}
		return err
	}
	return nil
}

func samePath(a, b string) bool {
	ar, err := filepath.EvalSymlinks(a)
	if err != nil {
		ar = a
	}
	br, err := filepath.EvalSymlinks(b)
	if err != nil {
		br = b
	}
	ar, _ = filepath.Abs(ar)
	br, _ = filepath.Abs(br)
	return filepath.Clean(ar) == filepath.Clean(br)
}
