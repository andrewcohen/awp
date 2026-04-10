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
type openWorkflow func(initial openRequest, workspaces []string, in io.Reader, out io.Writer) (openRequest, error)

type doctorService interface {
	Run() error
}

type App struct {
	svc           workspace.Service
	doctor        doctorService
	out           io.Writer
	in            io.Reader
	picker        workspacePicker
	openForm      openWorkflow
	isPiped       func(io.Reader) bool
	isInteractive func(io.Reader) bool
}

func NewApp(svc workspace.Service, out io.Writer) *App {
	return &App{
		svc:           svc,
		out:           out,
		in:            os.Stdin,
		picker:        pickWorkspaceWithCharm,
		openForm:      runOpenWithCharm,
		isPiped:       isPipedInput,
		isInteractive: isInteractiveInput,
	}
}

func (a *App) Run(args []string) error {
	if len(args) == 0 {
		return a.usage()
	}
	switch args[0] {
	case "workspace", "w":
		return a.runWorkspace(args[1:])
	case "doctor":
		return a.runDoctor(args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func (a *App) SetDoctor(svc doctorService) {
	a.doctor = svc
}

func (a *App) runWorkspace(args []string) error {
	if len(args) == 0 {
		return a.workspaceUsage()
	}

	switch args[0] {
	case "list":
		return a.runList(args[1:])
	case "info":
		return a.runInfo(args[1:])
	case "open":
		return a.runOpen(args[1:])
	case "rename":
		return a.runRename(args[1:])
	case "delete", "remove", "rm":
		return a.runDelete(args[1:])
	default:
		return fmt.Errorf("unknown workspace subcommand %q", args[0])
	}
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
		_, _ = fmt.Fprintln(a.out, "Usage: awp w open [workspace] [--bookmark|-b <bookmark>] [--prompt|-p <prompt>] [--yes|-y]\nIf no workspace is provided: read from stdin pipe, else open interactive form/picker.")
		return nil
	}
	req := openRequest{}
	positionals := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--bookmark" || arg == "-b":
			if i+1 >= len(args) {
				return fmt.Errorf("%s requires a value", arg)
			}
			req.Bookmark = args[i+1]
			i++
		case strings.HasPrefix(arg, "--bookmark="):
			req.Bookmark = strings.TrimPrefix(arg, "--bookmark=")
		case arg == "--prompt" || arg == "-p":
			if i+1 >= len(args) {
				return fmt.Errorf("%s requires a value", arg)
			}
			req.Prompt = args[i+1]
			i++
		case strings.HasPrefix(arg, "--prompt="):
			req.Prompt = strings.TrimPrefix(arg, "--prompt=")
		case arg == "--yes" || arg == "-y":
			req.Yes = true
		case strings.HasPrefix(arg, "-"):
			return fmt.Errorf("unknown flag %q", arg)
		default:
			positionals = append(positionals, arg)
		}
	}
	if len(positionals) > 1 {
		return errors.New("workspace open requires exactly one workspace name")
	}
	if len(positionals) == 1 {
		req.Name = positionals[0]
		return a.svc.Open(req.Name, req.Bookmark, req.Prompt, req.Yes)
	}
	if a.isPiped != nil && a.isPiped(a.in) {
		name, err := a.resolveWorkspaceTarget("open", nil)
		if err != nil {
			return err
		}
		req.Name = name
		return a.svc.Open(req.Name, req.Bookmark, req.Prompt, req.Yes)
	}
	if a.isInteractive != nil && a.isInteractive(a.in) && a.openForm != nil {
		entries, err := a.svc.List()
		if err != nil {
			return err
		}
		options := make([]string, 0, len(entries))
		for _, entry := range entries {
			options = append(options, entry.Name)
		}
		updated, err := a.openForm(req, options, a.in, a.out)
		if err != nil {
			return err
		}
		updated.Yes = true
		return a.svc.Open(updated.Name, updated.Bookmark, updated.Prompt, updated.Yes)
	}
	if strings.TrimSpace(req.Bookmark) != "" {
		return a.svc.Open("", req.Bookmark, req.Prompt, req.Yes)
	}
	name, err := a.resolveWorkspaceTarget("open", nil)
	if err != nil {
		return err
	}
	return a.svc.Open(name, req.Bookmark, req.Prompt, req.Yes)
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
		_, _ = fmt.Fprintln(a.out, "Usage: awp w delete|remove|rm [--force] [workspace]\nIf no workspace is provided: read from stdin pipe, else open picker.")
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

	if a.isPiped != nil && a.isPiped(a.in) {
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
		if verb == "delete" && workspace.IsProtected(entry.Name) {
			continue
		}
		options = append(options, entry.Name)
	}
	if len(options) == 0 {
		if verb == "delete" {
			return "", errors.New("no removable workspaces available")
		}
		return "", errors.New("no workspaces available")
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

func (a *App) runDoctor(args []string) error {
	if isHelpArgSlice(args) {
		_, _ = fmt.Fprintln(a.out, "Usage: awp doctor")
		return nil
	}
	if len(args) != 0 {
		return errors.New("doctor takes no arguments")
	}
	if a.doctor == nil {
		return errors.New("doctor is not configured")
	}
	return a.doctor.Run()
}

func (a *App) usage() error {
	_, _ = fmt.Fprintln(a.out, "Usage: awp <doctor|workspace|w> ...")
	return nil
}

func (a *App) workspaceUsage() error {
	_, _ = fmt.Fprintln(a.out, "Usage: awp <workspace|w> <list|info|open|rename|delete|remove|rm>")
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

func isInteractiveInput(in io.Reader) bool {
	f, ok := in.(*os.File)
	if !ok {
		return false
	}
	stat, err := f.Stat()
	if err != nil {
		return false
	}
	return stat.Mode()&os.ModeCharDevice != 0
}

func isHelpArgSlice(args []string) bool {
	return len(args) == 1 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help")
}
