package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/andrewcohen/awp/internal/config"
	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/jj"
	"github.com/andrewcohen/awp/internal/tmux"
	"github.com/andrewcohen/awp/internal/workspace"
)

type workspacePicker func(title string, options []string) (string, error)
type openWorkflow func(initial openRequest, runner Runner, in io.Reader, out io.Writer) (openRequest, error)

type doctorService interface {
	Run() error
	RunGlobal(fix bool) error
	RunRepo(fix bool) error
}

type diffWorkflow func(runner Runner, in io.Reader, out io.Writer) error
type deckWorkflow func(runner Runner, svc workspace.Service, in io.Reader, out io.Writer, initialScope deckui.Scope) error
type miniDeckWorkflow func(runner Runner, in io.Reader, out io.Writer) error
type reviewWorkflow func(runner Runner, svc workspace.Service, prNumber int, in io.Reader, out io.Writer) error

type App struct {
	svc           workspace.Service
	doctor        doctorService
	out           io.Writer
	in            io.Reader
	runner        Runner
	picker        workspacePicker
	openForm      openWorkflow
	diff          diffWorkflow
	deck          deckWorkflow
	miniDeck      miniDeckWorkflow
	review        reviewWorkflow
	isPiped       func(io.Reader) bool
	isInteractive func(io.Reader) bool
}

func NewApp(svc workspace.Service, out io.Writer) *App {
	return &App{
		svc:           svc,
		out:           out,
		in:            os.Stdin,
		runner:        NewExecRunner(),
		picker:        pickWorkspaceWithCharm,
		openForm:      runOpenWithCharm,
		diff:          runDiffWithCharm,
		deck:          runDeckWithCharm,
		miniDeck:      runMiniDeck,
		review:        runReviewWithCharm,
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
	case "diff":
		return a.runDiff(args[1:])
	case "deck":
		return a.runDeck(args[1:])
	case "mini-deck":
		return a.runMiniDeck(args[1:])
	case "deck-cleanup":
		return runDeckCleanup(a.runner, a.out)
	case "run-job":
		return runRunJob(a.svc, a.runner, args[1:])
	case "review":
		return a.runReview(args[1:])
	case "internal":
		return a.runInternal(args[1:])
	case "init":
		return a.runInit(args[1:])
	case "config":
		return a.runConfig(args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func (a *App) runInternal(args []string) error {
	if len(args) == 0 {
		return errors.New("internal requires a subcommand")
	}
	switch args[0] {
	case "report-status":
		return runReportStatus(args[1:], a.out)
	case "unread-summary":
		return runUnreadSummary(a.out)
	case "mark-read":
		return runMarkRead(args[1:])
	default:
		return fmt.Errorf("unknown internal subcommand %q", args[0])
	}
}

func (a *App) runInit(args []string) error {
	if len(args) == 0 {
		return errors.New("init requires a subcommand (try: awp init hooks)")
	}
	switch args[0] {
	case "hooks":
		return runInitHooks(args[1:], a.out)
	default:
		return fmt.Errorf("unknown init subcommand %q", args[0])
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
	case "bootstrap":
		return a.runBootstrap(args[1:])
	case "prune":
		return a.runPrune(args[1:])
	default:
		return fmt.Errorf("unknown workspace subcommand %q", args[0])
	}
}

func (a *App) runBootstrap(args []string) error {
	if isHelpArgSlice(args) {
		_, _ = fmt.Fprintln(a.out, "Usage: awp w bootstrap [--all | workspace]\nRe-runs built-in + user bootstrap hooks. Infers workspace from cwd when omitted.\n--all bootstraps every tracked workspace in the current source repo (continues on failure).")
		return nil
	}
	all := false
	positional := args[:0:0]
	for _, arg := range args {
		switch arg {
		case "--all", "-a":
			all = true
		default:
			positional = append(positional, arg)
		}
	}
	if all {
		if len(positional) > 0 {
			return errors.New("bootstrap --all does not take a workspace name")
		}
		return a.svc.BootstrapAll()
	}
	if len(positional) > 1 {
		return errors.New("bootstrap takes at most one workspace name")
	}
	name := ""
	if len(positional) == 1 {
		name = positional[0]
	}
	return a.svc.Bootstrap(name)
}

func (a *App) runPrune(args []string) error {
	if isHelpArgSlice(args) {
		_, _ = fmt.Fprintln(a.out, "Usage: awp w prune [--dry-run] [--force]\nRemoves orphaned workspace directories under ~/.awp/workspaces that are not tracked in awp state.")
		return nil
	}
	dryRun := false
	force := false
	for _, arg := range args {
		switch arg {
		case "--dry-run", "-n":
			dryRun = true
		case "--force", "-f":
			force = true
		default:
			return fmt.Errorf("unknown prune flag %q", arg)
		}
	}
	if !dryRun && !force {
		paths, err := a.svc.PruneOrphans(true)
		if err != nil {
			return err
		}
		if len(paths) == 0 {
			_, _ = fmt.Fprintln(a.out, "No orphan workspace directories found.")
			return nil
		}
		_, _ = fmt.Fprintln(a.out, "Would remove:")
		for _, p := range paths {
			_, _ = fmt.Fprintf(a.out, "  %s\n", p)
		}
		_, _ = fmt.Fprintf(a.out, "\nRemove %d orphan(s)? [y/N]: ", len(paths))
		reader := bufio.NewReader(a.in)
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		answer := strings.TrimSpace(strings.ToLower(line))
		if answer != "y" && answer != "yes" {
			return errors.New("prune cancelled")
		}
	}
	paths, err := a.svc.PruneOrphans(dryRun)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		_, _ = fmt.Fprintln(a.out, "No orphan workspace directories found.")
		return nil
	}
	verb := "Removed"
	if dryRun {
		verb = "Would remove"
	}
	for _, p := range paths {
		_, _ = fmt.Fprintf(a.out, "%s %s\n", verb, p)
	}
	return nil
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

// ErrOpenCancelled signals a user-initiated cancel of the interactive open form.
// main() maps this to a silent exit code 2 so callers (e.g. the deck) can
// distinguish cancel from success without surfacing an error to the user.
var ErrOpenCancelled = errors.New("open cancelled")

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
		case arg == "--deck":
			// Deprecated: workspace open now always uses deck/session semantics.
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
		return a.openInDeckMode(req)
	}
	if a.isPiped != nil && a.isPiped(a.in) {
		name, err := a.resolveWorkspaceTarget("open", nil)
		if err != nil {
			return err
		}
		req.Name = name
		return a.openInDeckMode(req)
	}
	if a.isInteractive != nil && a.isInteractive(a.in) && a.openForm != nil {
		updated, err := a.openForm(req, a.runner, a.in, a.out)
		if err != nil {
			if errors.Is(err, ErrOpenCancelled) || errors.Is(err, deckui.ErrWorkspaceFormCancelled) {
				return ErrOpenCancelled
			}
			return err
		}
		updated.Yes = true
		return a.openInDeckMode(updated)
	}
	if strings.TrimSpace(req.Bookmark) != "" {
		return a.openInDeckMode(req)
	}
	name, err := a.resolveWorkspaceTarget("open", nil)
	if err != nil {
		return err
	}
	req.Name = name
	return a.openInDeckMode(req)
}

func openWorkspaceInDeckMode(runner Runner, svc workspace.Service, req openRequest) error {
	return openWorkspaceWithReporter(runner, svc, req, nil)
}

// openWorkspaceWithReporter performs the create-or-attach + tmux setup with
// optional progress reporting. Used both by `awp open` (no reporter) and by
// the deck's create action (with the in-deck progress reporter).
func openWorkspaceWithReporter(runner Runner, svc workspace.Service, req openRequest, reporter interface {
	Step(string)
	Log(string)
}) error {
	step := func(s string) {
		if reporter != nil {
			reporter.Step(s)
		}
	}
	step("Prepare jj workspace")
	normalized, wsPath, err := svc.PrepareWorkspace(req.Name, req.Bookmark, true)
	if err != nil {
		return err
	}
	j := jj.New(runner)
	repoRoot, err := j.RepoRoot()
	if err != nil {
		return err
	}
	// Bookmark to record on the workspace entry — drives the deck's PR
	// glyph via Entry.Bookmark. Two cases:
	//
	//   1. BookmarkToCreate is set: create that bookmark on @ (best-effort)
	//      and record it.
	//   2. BookmarkToCreate is blank but Bookmark (the anchor) is itself an
	//      existing bookmark the user picked: record the anchor.
	//
	// Best-effort throughout: any failure is logged but does not fail the
	// workspace creation, since the workspace itself is already created and
	// usable.
	toCreate := strings.TrimSpace(req.BookmarkToCreate)
	switch {
	case toCreate != "":
		if rev, revErr := j.WorkspaceRevision(normalized); revErr == nil && strings.TrimSpace(rev) != "" {
			if createErr := j.CreateBookmark(toCreate, rev); createErr != nil {
				if reporter != nil {
					reporter.Log(fmt.Sprintf("create bookmark %q: %v", toCreate, createErr))
				}
			} else if reporter != nil {
				reporter.Log("bookmark created: " + toCreate)
			}
			if recordErr := svc.RecordBookmark(normalized, toCreate); recordErr != nil {
				if reporter != nil {
					reporter.Log(fmt.Sprintf("link bookmark %q to workspace: %v", toCreate, recordErr))
				}
			}
		} else if reporter != nil {
			reporter.Log(fmt.Sprintf("bookmark skipped: cannot resolve workspace revision (%v)", revErr))
		}
	case strings.TrimSpace(req.Bookmark) != "":
		// The anchor is an existing bookmark the user picked. Record it
		// so the deck's PR glyph matches without a manual `B` link step.
		if recordErr := svc.RecordBookmark(normalized, strings.TrimSpace(req.Bookmark)); recordErr != nil && reporter != nil {
			reporter.Log(fmt.Sprintf("link bookmark %q to workspace: %v", req.Bookmark, recordErr))
		}
	}
	projectName := filepath.Base(repoRoot)
	sessionName := DeckSessionName(projectName, normalized)
	tmuxClient := tmux.New(runner)
	id, err := tmuxClient.SessionIDByName(sessionName)
	if err != nil {
		return err
	}
	env := workspaceEnvPairs(projectName, normalized, repoRoot)
	sessionWasNew := id == ""
	if sessionWasNew {
		step("Create tmux session " + sessionName)
		if err := tmuxClient.NewSession(sessionName, wsPath, "agent", env); err != nil {
			return err
		}
		id, _ = tmuxClient.SessionIDByName(sessionName)
	}
	stale, envErr := ensureWorkspaceSessionEnv(tmuxClient, sessionName, projectName, normalized, repoRoot, sessionName+":agent")
	if envErr != nil && reporter != nil {
		reporter.Log(fmt.Sprintf("warning: failed to set session env: %v", envErr))
	}
	if stale && reporter != nil {
		reporter.Log("agent missing AWP_WORKSPACE — restart agent to enable status reporting")
	}
	if err := svc.RecordSession(normalized, id, sessionName); err != nil {
		return err
	}
	// Agent launch / prompt delivery splits on whether we own the
	// freshly-created session:
	//   • New session: pane is a shell — type "<invocation> '<prompt>'"
	//     so the shell execs the agent CLI with the prompt as argv[1].
	//   • Existing session: the agent is already running (the deck's
	//     summon path runs createWorkspaceSession which launches it).
	//     Sending the invocation again would just type "claude
	//     --dangerously-skip-permissions 'prompt'" into the running
	//     agent's input box as a literal user message — definitely not
	//     what we want. Paste just the prompt instead so the agent
	//     receives it as one bracketed-paste user message.
	promptArg := strings.TrimSpace(req.Prompt)
	switch {
	case sessionWasNew:
		invocation := config.AgentInvocation(repoRoot)
		cmd := invocation
		if promptArg != "" {
			step("Send prompt to agent")
			cmd += " " + shellSingleQuote(promptArg)
		} else {
			step("Launch agent")
		}
		if err := tmuxClient.SendCommand(sessionName+":agent", cmd); err != nil {
			return err
		}
	case promptArg != "":
		step("Send prompt to agent")
		if err := tmuxClient.PasteText(sessionName+":agent", promptArg); err != nil {
			return err
		}
	}
	// Invalidate the repo's PR-status cache entry so the next deck open
	// fetches fresh data instead of reusing the previous fetch's cache
	// inside the 60s throttle window. The deck quits after a workspace
	// create / review-open, so the user lands in the new tmux session
	// first; reopening the deck immediately afterwards is the common
	// path that benefits from this.
	if sessionWasNew {
		if err := invalidatePRStatusCacheRepo(repoRoot); err != nil && reporter != nil {
			reporter.Log(fmt.Sprintf("pr-status cache invalidate: %v", err))
		}
	}
	if req.NoSwitch {
		return nil
	}
	step("Switch to " + sessionName)
	return tmuxClient.SwitchClient(sessionName)
}

func (a *App) openInDeckMode(req openRequest) error {
	if a.runner == nil {
		a.runner = NewExecRunner()
	}
	return openWorkspaceInDeckMode(a.runner, a.svc, req)
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
		_, _ = fmt.Fprintln(a.out, "Usage: awp doctor [--global] [--fix]")
		_, _ = fmt.Fprintln(a.out, "  --global  skip checks that require a jj repo (scans all live awp tmux sessions)")
		_, _ = fmt.Fprintln(a.out, "  --fix     attempt to repair issues (reinstall hooks, inject missing tmux env vars)")
		return nil
	}
	global, fix := false, false
	for _, arg := range args {
		switch arg {
		case "--global":
			global = true
		case "--fix":
			fix = true
		default:
			return fmt.Errorf("unknown doctor flag %q", arg)
		}
	}
	if a.doctor == nil {
		return errors.New("doctor is not configured")
	}
	if global {
		return a.doctor.RunGlobal(fix)
	}
	return a.doctor.RunRepo(fix)
}

func (a *App) runDiff(args []string) error {
	if isHelpArgSlice(args) {
		_, _ = fmt.Fprintln(a.out, "Usage: awp diff")
		return nil
	}
	if len(args) != 0 {
		return errors.New("diff takes no arguments")
	}
	if a.diff == nil {
		return errors.New("diff is not configured")
	}
	return a.diff(a.runner, a.in, a.out)
}

func (a *App) runDeck(args []string) error {
	if isHelpArgSlice(args) {
		_, _ = fmt.Fprintln(a.out, "Usage: awp deck [--scope=all|attention|open-pr]")
		_, _ = fmt.Fprintln(a.out, "")
		_, _ = fmt.Fprintln(a.out, "Intended invocation: tmux popup overlay. Add this to ~/.tmux.conf:")
		_, _ = fmt.Fprintln(a.out, "  bind a display-popup -E -w 90% -h 90% awp deck \\; run-shell \"awp deck-cleanup\"")
		_, _ = fmt.Fprintln(a.out, "")
		_, _ = fmt.Fprintln(a.out, "Selecting a workspace summons or focuses session [awp]<repo>__<workspace>.")
		_, _ = fmt.Fprintln(a.out, "")
		_, _ = fmt.Fprintln(a.out, "Flags:")
		_, _ = fmt.Fprintln(a.out, "  --scope <all|attention|open-pr>  initial scope (default: all). `P` still")
		_, _ = fmt.Fprintln(a.out, "                                    cycles through all scopes in the deck.")
		return nil
	}
	scope := deckui.ScopeAll
	for _, arg := range args {
		raw, ok := strings.CutPrefix(arg, "--scope=")
		if !ok {
			return fmt.Errorf("deck: unexpected argument %q (try --scope=all|attention|open-pr)", arg)
		}
		s, ok := deckui.ParseScope(raw)
		if !ok {
			return fmt.Errorf("deck: invalid --scope value %q (want all, attention, or open-pr)", raw)
		}
		scope = s
	}
	if a.deck == nil {
		return errors.New("deck is not configured")
	}
	return a.deck(a.runner, a.svc, a.in, a.out, scope)
}

func (a *App) runMiniDeck(args []string) error {
	if isHelpArgSlice(args) {
		_, _ = fmt.Fprintln(a.out, "Usage: awp mini-deck")
		_, _ = fmt.Fprintln(a.out, "")
		_, _ = fmt.Fprintln(a.out, "Quick-jump list of workspaces with an active agent or an unread")
		_, _ = fmt.Fprintln(a.out, "notification. j/k to move, enter to summon, q/esc to quit.")
		_, _ = fmt.Fprintln(a.out, "")
		_, _ = fmt.Fprintln(a.out, "Suggested tmux binding (capital A):")
		_, _ = fmt.Fprintln(a.out, "  bind A display-popup -E -w 50% -h 60% awp mini-deck")
		return nil
	}
	if len(args) != 0 {
		return errors.New("mini-deck takes no arguments")
	}
	if a.miniDeck == nil {
		return errors.New("mini-deck is not configured")
	}
	return a.miniDeck(a.runner, a.in, a.out)
}

func (a *App) runReview(args []string) error {
	if isHelpArgSlice(args) {
		_, _ = fmt.Fprintln(a.out, "Usage: awp review [pr#]\nWith no argument, opens an interactive picker over `gh pr list`.")
		return nil
	}
	if len(args) > 1 {
		return errors.New("review takes at most one PR number")
	}
	if a.review == nil {
		return errors.New("review is not configured")
	}
	if len(args) == 1 {
		n, err := parsePRNumber(args[0])
		if err != nil {
			return err
		}
		return a.review(a.runner, a.svc, n, a.in, a.out)
	}
	if a.picker == nil {
		return errors.New("picker is not configured")
	}
	n, err := pickPRNumber(a.runner, a.picker)
	if err != nil {
		return err
	}
	return a.review(a.runner, a.svc, n, a.in, a.out)
}

func (a *App) usage() error {
	_, _ = fmt.Fprintln(a.out, "Usage: awp <deck|mini-deck|diff|doctor|review|config|workspace|w> ...")
	return nil
}

func (a *App) workspaceUsage() error {
	_, _ = fmt.Fprintln(a.out, "Usage: awp <workspace|w> <list|info|open|bootstrap|rename|delete|remove|rm|prune>")
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

