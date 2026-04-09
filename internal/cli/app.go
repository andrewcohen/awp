package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/andrewcohen/awp/internal/workspace"
)

type workspacePicker func(title string, options []string) (string, error)

type App struct {
	svc    workspace.Service
	out    io.Writer
	in     io.Reader
	picker workspacePicker
}

func NewApp(svc workspace.Service, out io.Writer) *App {
	return &App{
		svc:    svc,
		out:    out,
		in:     os.Stdin,
		picker: pickWorkspaceWithCharm,
	}
}

func (a *App) Run(args []string) error {
	if len(args) == 0 {
		return a.usage()
	}
	if args[0] != "workspace" && args[0] != "w" {
		return fmt.Errorf("unknown command %q", args[0])
	}
	return a.runWorkspace(args[1:])
}

func (a *App) runWorkspace(args []string) error {
	if len(args) == 0 {
		return a.workspaceUsage()
	}

	switch args[0] {
	case "start":
		return a.runStart(args[1:])
	case "list":
		return a.runList(args[1:])
	case "info":
		return a.runInfo(args[1:])
	case "open":
		return a.runOpen(args[1:])
	case "rename":
		return a.runRename(args[1:])
	case "delete", "remove":
		return a.runDelete(args[1:])
	default:
		return fmt.Errorf("unknown workspace subcommand %q", args[0])
	}
}

func (a *App) runStart(args []string) error {
	if isHelpArgSlice(args) {
		_, _ = fmt.Fprintln(a.out, "Usage: awp w start [--name <name>|<name>] [--bookmark|-b <bookmark>]")
		return nil
	}

	var name string
	var bookmark string
	positionals := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--name":
			if i+1 >= len(args) {
				return errors.New("--name requires a value")
			}
			name = args[i+1]
			i++
		case strings.HasPrefix(arg, "--name="):
			name = strings.TrimPrefix(arg, "--name=")
		case arg == "--bookmark" || arg == "-b":
			if i+1 >= len(args) {
				return fmt.Errorf("%s requires a value", arg)
			}
			bookmark = args[i+1]
			i++
		case strings.HasPrefix(arg, "--bookmark="):
			bookmark = strings.TrimPrefix(arg, "--bookmark=")
		case strings.HasPrefix(arg, "-"):
			return fmt.Errorf("unknown flag %q", arg)
		default:
			positionals = append(positionals, arg)
		}
	}

	if name == "" && len(positionals) > 0 {
		name = positionals[0]
	}
	if len(positionals) > 1 {
		return errors.New("workspace start accepts at most one positional name")
	}

	return a.svc.Start(name, bookmark)
}

func (a *App) runList(args []string) error {
	if isHelpArgSlice(args) {
		_, _ = fmt.Fprintln(a.out, "Usage: awp w list")
		return nil
	}
	if len(args) != 0 {
		return errors.New("workspace list takes no arguments")
	}
	entries, err := a.svc.List()
	if err != nil {
		return err
	}

	for _, e := range entries {
		fmt.Fprintln(a.out, e.Name)
	}
	return nil
}

func (a *App) runInfo(args []string) error {
	if isHelpArgSlice(args) {
		_, _ = fmt.Fprintln(a.out, "Usage: awp w info <workspace>")
		return nil
	}
	if len(args) != 1 {
		return errors.New("workspace info requires exactly one workspace name")
	}
	info, err := a.svc.Info(args[0])
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(a.out, 0, 0, 2, ' ', 0)
	window := info.TmuxWindow
	if window == "" {
		window = "-"
	}
	active := "no"
	if info.ActiveWindow {
		active = "yes"
	}
	managed := "no"
	if info.Managed {
		managed = "yes"
	}
	jjExists := "no"
	if info.JJExists {
		jjExists = "yes"
	}
	tmuxExists := "no"
	if info.TmuxExists {
		tmuxExists = "yes"
	}
	fmt.Fprintln(tw, "FIELD\tVALUE")
	fmt.Fprintf(tw, "name\t%s\n", info.Name)
	fmt.Fprintf(tw, "path\t%s\n", info.Path)
	fmt.Fprintf(tw, "managed\t%s\n", managed)
	fmt.Fprintf(tw, "jj-workspace\t%s\n", jjExists)
	fmt.Fprintf(tw, "tmux-window\t%s\n", window)
	fmt.Fprintf(tw, "tmux-window-exists\t%s\n", tmuxExists)
	fmt.Fprintf(tw, "active\t%s\n", active)
	return tw.Flush()
}

func (a *App) runOpen(args []string) error {
	if isHelpArgSlice(args) {
		_, _ = fmt.Fprintln(a.out, "Usage: awp w open [workspace] [--bookmark|-b <bookmark>]\nIf no workspace is provided: read from stdin pipe, else open picker.\nIf bookmark is provided and workspace does not exist, start from bookmark.")
		return nil
	}
	var bookmark string
	positionals := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--bookmark" || arg == "-b":
			if i+1 >= len(args) {
				return fmt.Errorf("%s requires a value", arg)
			}
			bookmark = args[i+1]
			i++
		case strings.HasPrefix(arg, "--bookmark="):
			bookmark = strings.TrimPrefix(arg, "--bookmark=")
		case strings.HasPrefix(arg, "-"):
			return fmt.Errorf("unknown flag %q", arg)
		default:
			positionals = append(positionals, arg)
		}
	}
	if strings.TrimSpace(bookmark) != "" && len(positionals) == 0 {
		return a.svc.Open("", bookmark)
	}
	name, err := a.resolveWorkspaceTarget("open", positionals)
	if err != nil {
		return err
	}
	return a.svc.Open(name, bookmark)
}

func (a *App) runRename(args []string) error {
	if isHelpArgSlice(args) {
		_, _ = fmt.Fprintln(a.out, "Usage: awp w rename <old> <new>")
		return nil
	}
	if len(args) != 2 {
		return errors.New("workspace rename requires old and new names")
	}
	return a.svc.Rename(args[0], args[1])
}

func (a *App) runDelete(args []string) error {
	if isHelpArgSlice(args) {
		_, _ = fmt.Fprintln(a.out, "Usage: awp w delete|remove [--force] [workspace]\nIf no workspace is provided: read from stdin pipe, else open picker.")
		return nil
	}

	force := false
	positionals := make([]string, 0, len(args))
	for _, arg := range args {
		switch arg {
		case "--force":
			force = true
		default:
			positionals = append(positionals, arg)
		}
	}

	name, err := a.resolveWorkspaceTarget("delete", positionals)
	if err != nil {
		return err
	}
	return a.svc.Delete(name, force)
}

func (a *App) resolveWorkspaceTarget(verb string, args []string) (string, error) {
	if len(args) == 1 {
		name := strings.TrimSpace(args[0])
		if name == "" {
			return "", fmt.Errorf("workspace %s requires exactly one workspace name", verb)
		}
		return name, nil
	}
	if len(args) > 1 {
		return "", fmt.Errorf("workspace %s requires exactly one workspace name", verb)
	}

	if isPipedInput(a.in) {
		reader := bufio.NewReader(a.in)
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		name := strings.TrimSpace(line)
		if name == "" {
			return "", fmt.Errorf("workspace %s requires exactly one workspace name", verb)
		}
		return name, nil
	}

	entries, err := a.svc.List()
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", errors.New("no workspaces available")
	}
	options := make([]string, 0, len(entries))
	for _, entry := range entries {
		options = append(options, entry.Name)
	}
	if a.picker == nil {
		return "", errors.New("workspace picker is not configured")
	}
	selected, err := a.picker("Select workspace", options)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(selected), nil
}

func (a *App) usage() error {
	_, _ = fmt.Fprintln(a.out, "Usage: awp <workspace|w> <start|list|info|open|rename|delete|remove> [args]")
	return nil
}

func (a *App) workspaceUsage() error {
	_, _ = fmt.Fprintln(a.out, "Usage: awp <workspace|w> <start|list|info|open|rename|delete|remove>")
	return nil
}

func isPipedInput(in io.Reader) bool {
	f, ok := in.(*os.File)
	if !ok {
		return false
	}
	stat, err := f.Stat()
	if err != nil {
		return false
	}
	return stat.Mode()&os.ModeCharDevice == 0
}

func isHelpArgSlice(args []string) bool {
	return len(args) == 1 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help")
}
