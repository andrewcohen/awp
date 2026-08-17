package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

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

// diffWorkflow runs the standalone diff viewer. revset is empty for the working
// copy, or any jj revset from `-r`.
type diffWorkflow func(runner Runner, svc workspace.Service, revset string, in io.Reader, out io.Writer) error
type deckWorkflow func(runner Runner, svc workspace.Service, in io.Reader, out io.Writer, initialScope deckui.Scope, panes paneHost) error
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
	// shipSvc overrides the service `awp ship` lists workspaces through. Unset
	// in production, where it is built from the resolved repo root — see
	// App.shipService for why the ambient svc is the wrong one there.
	shipSvc workspace.Service
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
	case "zdeck":
		return a.runZdeck(args[1:])
	case "deck-cleanup":
		return runDeckCleanup(a.runner, a.out)
	case "run-job":
		return runRunJob(a.svc, a.runner, args[1:])
	case "review":
		return a.runReview(args[1:])
	case "ship":
		return a.runShip(args[1:])
	case "logs":
		return runLogs(args[1:], a.out)
	case "watch":
		return a.runWatch(args[1:])
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
	case "gate":
		return a.runGate(args[1:])
	case "require-task":
		return runRequireTask(args[1:], os.Stderr)
	case "loop":
		return a.runLoop(args[1:])
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
	case "new":
		return a.runWorkspaceNew(args[1:])
	case "send":
		return a.runWorkspaceSend(args[1:])
	case "repair":
		return a.runWorkspaceRepair(args[1:])
	case "attention":
		return a.runWorkspaceAttention(args[1:])
	case "label":
		return a.runWorkspaceLabel(args[1:])
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
	// Fetch before anchoring on an existing bookmark so the new workspace
	// starts at the current origin tip — and so a branch that lives only
	// on origin (your PR pushed from another machine, or a collaborator's
	// branch) is present locally for PrepareWorkspace to track and check
	// out. Mirrors the review flow (review.go) and the bookmark picker
	// (deck.go), which both fetch before touching remote state.
	//
	// Best-effort: a fetch failure (offline, auth, etc.) is logged but
	// doesn't block creation — the branch may already be local, and an
	// origin-only branch will surface a clearer error at the track/anchor
	// step below. Skipped when there's no bookmark to anchor on: a fresh
	// or new-bookmark workspace starts from the local working copy, so a
	// fetch wouldn't change where it lands.
	if strings.TrimSpace(req.Bookmark) != "" {
		step("jj git fetch")
		if out, fErr := runner.Run(context.Background(), "", "jj", "git", "fetch"); fErr != nil && reporter != nil {
			reporter.Log(fmt.Sprintf("jj git fetch (continuing): %v: %s", fErr, strings.TrimSpace(out)))
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
	// Bookmark to create + record as Entry.Bookmark (drives the deck's PR
	// glyph). Only runs when the caller (typically the new-workspace form)
	// explicitly named a bookmark to create — we never auto-link the
	// anchor revision as Entry.Bookmark, which would record trunk
	// ("main", "master", …) for callers that pass --bookmark and would
	// also expose trunk to the workspace-delete cleanup path.
	//
	// Best-effort throughout: any failure is logged but does not fail the
	// workspace creation, since the workspace itself is already created
	// and usable.
	if toCreate := strings.TrimSpace(req.BookmarkToCreate); toCreate != "" {
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
	}
	// Pin the workspace to its PR when the caller created it from a known
	// PR. Done before the host split because it is a property of the
	// workspace, not of where its agent lives. Best-effort: the workspace is
	// already usable if this fails.
	if req.PRNumber > 0 {
		if err := svc.RecordPROverride(normalized, req.PRNumber); err != nil && reporter != nil {
			reporter.Log(fmt.Sprintf("pin PR #%d: %v", req.PRNumber, err))
		} else if reporter != nil {
			reporter.Log(fmt.Sprintf("linked to PR #%d", req.PRNumber))
		}
	}

	// Everything below this line is the tmux half: a session, an agent
	// running in it, and a client switched to it. A caller that hosts the
	// workspace's processes itself has none of those, and starting a tmux
	// agent for it would give the workspace a second one that nothing in
	// that deck can see.
	if req.PaneHosted {
		if promptArg := strings.TrimSpace(req.Prompt); promptArg != "" {
			// Start the agent rather than only parking the prompt: this is what the
			// tmux half below ends with, and a create with a prompt is a request for
			// work to be under way, not for it to be waiting.
			err := startHostedAgent(runner, hostedAgent{
				project:   filepath.Base(repoRoot),
				workspace: normalized,
				repoRoot:  repoRoot,
				dir:       wsPath,
				prompt:    promptArg,
			}, reporter)
			if err != nil {
				// Parking is the fallback, and it is a complete one: the agent pane
				// delivers a parked prompt on first open, which is what happened before
				// anything started an agent here. Say why it fell back, because a prompt
				// that waits when it should not is otherwise indistinguishable from one
				// that arrived.
				if reporter != nil {
					reporter.Log(fmt.Sprintf("could not start the agent (%v) — parking the prompt for the first pane instead", err))
				}
				step("Park prompt for the agent")
				if perr := svc.RecordPendingPrompt(normalized, workspace.PendingPrompt{Text: promptArg}); perr != nil {
					return fmt.Errorf("park the prompt for %s: %w", normalized, perr)
				}
			}
		}
		if err := invalidatePRStatusCacheRepo(repoRoot); err != nil && reporter != nil {
			reporter.Log(fmt.Sprintf("pr-status cache invalidate: %v", err))
		}
		return nil
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
		invocation := codingAgentInvocation(repoRoot)
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
		_, _ = fmt.Fprintln(a.out, "Usage: awp diff [-r <revset>]")
		_, _ = fmt.Fprintln(a.out, "")
		_, _ = fmt.Fprintln(a.out, "With no -r, shows the working copy. -r takes any jj revset:")
		_, _ = fmt.Fprintln(a.out, "  awp diff -r @-           the change before this one")
		_, _ = fmt.Fprintln(a.out, "  awp diff -r 'main..@'    the whole stack against main")
		_, _ = fmt.Fprintln(a.out, "  awp diff -r andrew/thing a bookmark")
		return nil
	}
	revset, err := parseDiffRevset(args)
	if err != nil {
		return err
	}
	if a.diff == nil {
		return errors.New("diff is not configured")
	}
	return a.diff(a.runner, a.svc, revset, a.in, a.out)
}

// parseDiffRevset reads `-r <revset>` (or `-r=<revset>`, and the --revision
// spellings) off `awp diff`'s arguments.
//
// Hand-parsed rather than through a FlagSet because a revset routinely starts
// with a character the flag package treats as its own: `-r -3` is a real thing to
// ask for, and `awp diff -r @-` is the common case. Reading the value as the next
// argument whatever it looks like is what makes those work.
func parseDiffRevset(args []string) (string, error) {
	if len(args) == 0 {
		return "", nil
	}
	// Only the first argument is examined, because every accepted form consumes
	// the whole line: `awp diff` takes one revset and nothing else, so anything
	// left over is a mistake rather than a second flag to look for.
	arg := args[0]
	switch {
	case arg == "-r" || arg == "--revision" || arg == "--revisions":
		if len(args) < 2 {
			return "", errors.New("diff: -r needs a revset")
		}
		if rest := args[2:]; len(rest) > 0 {
			return "", fmt.Errorf("diff: unexpected argument %q", rest[0])
		}
		return strings.TrimSpace(args[1]), nil
	case strings.HasPrefix(arg, "-r="), strings.HasPrefix(arg, "--revision="), strings.HasPrefix(arg, "--revisions="):
		_, value, _ := strings.Cut(arg, "=")
		if rest := args[1:]; len(rest) > 0 {
			return "", fmt.Errorf("diff: unexpected argument %q", rest[0])
		}
		return strings.TrimSpace(value), nil
	}
	return "", fmt.Errorf("diff: unexpected argument %q (did you mean -r %s?)", arg, arg)
}

func (a *App) runDeck(args []string) error {
	if isHelpArgSlice(args) {
		_, _ = fmt.Fprintln(a.out, "Usage: awp deck [--scope=all|attention|inbox]")
		_, _ = fmt.Fprintln(a.out, "")
		_, _ = fmt.Fprintln(a.out, "Intended invocation: tmux popup overlay. Add this to ~/.tmux.conf:")
		_, _ = fmt.Fprintln(a.out, "  bind a display-popup -E -w 90% -h 90% awp deck \\; run-shell \"awp deck-cleanup\"")
		_, _ = fmt.Fprintln(a.out, "")
		_, _ = fmt.Fprintln(a.out, "Selecting a workspace summons or focuses session [awp]<repo>__<workspace>.")
		_, _ = fmt.Fprintln(a.out, "")
		_, _ = fmt.Fprintln(a.out, "Flags:")
		_, _ = fmt.Fprintln(a.out, "  --scope <all|attention|inbox>    initial scope (default: all). `P` still")
		_, _ = fmt.Fprintln(a.out, "                                    cycles through all scopes in the deck.")
		return nil
	}
	// The remembered scope is the default, so the deck opens where you left it.
	// An explicit --scope wins: a flag naming a scope is an instruction about this
	// run, not a preference, and it does not overwrite the remembered one either.
	scope := rememberedScope(deckui.ScopeAll)
	for _, arg := range args {
		raw, ok := strings.CutPrefix(arg, "--scope=")
		if !ok {
			return fmt.Errorf("deck: unexpected argument %q (try --scope=all|attention|inbox)", arg)
		}
		s, ok := deckui.ParseScope(raw)
		if !ok {
			return fmt.Errorf("deck: invalid --scope value %q (want all, attention, or inbox)", raw)
		}
		scope = s
	}
	if a.deck == nil {
		return errors.New("deck is not configured")
	}
	return a.deck(a.runner, a.svc, a.in, a.out, scope, nil)
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

// reviewUsage is `awp review --help`.
//
// Every subcommand isReviewSubcommand accepts has a line here, and
// TestReviewUsageNamesEverySubcommand checks that it does. `reply` was missing
// for as long as it has existed: an agent following the review prompt uses it, so
// the one surface that says what the command can do disagreed with the prompt
// telling it to. A subcommand that runs and is not documented is a subcommand
// nobody outside the prompt can find.
const reviewUsage = `Usage: awp review [pr#] [--project <name|path>] [--no-attach]
       awp review add [--file <path> --line <n> [--end-line <n>]] [--side new|old] [--text <line>] [--end-text <line>] (--body <text> | --body-file <path>) [--type comment|suggestion|question|praise] [--workspace <name>]
       awp review reply --to <comment-id> (--body <text> | --body-file <path>) [--type comment|suggestion|question|praise] [--proposal] [--workspace <name>]
       awp review list [--json] [--workspace <name>]
       awp review publish [--pr <n>] [--verdict approve|comment|request-changes] [--summary <text> | --summary-file <path>] [--dry-run] [--workspace <name>]

With no argument, opens an interactive picker over ` + "`gh pr list`" + `.

  --project <name|path>   review a PR in that project rather than the repo you are
                          standing in. Requires a PR number.
  --no-attach             prepare the review workspace, start its reviewing agent,
                          and return — no tmux session and no switch. Requires a PR
                          number. This is the form to use from a script or an agent:
                          nothing it does can stop and wait for an answer.`

func (a *App) runReview(args []string) error {
	if isHelpArgSlice(args) {
		_, _ = fmt.Fprintln(a.out, reviewUsage)
		return nil
	}
	if isReviewSubcommand(args) {
		return runReviewSubcommand(a.runner, a.svc, args, a.out)
	}
	// The two flags that make this callable by something with nobody at its stdin.
	// Parsed before the arity check so `awp review 123 --no-attach` is one PR number
	// and a flag rather than two arguments.
	project, args, err := takeProjectFlag(args)
	if err != nil {
		return err
	}
	noAttach := false
	kept := args[:0:0]
	for _, arg := range args {
		if arg == "--no-attach" {
			noAttach = true
			continue
		}
		kept = append(kept, arg)
	}
	args = kept

	if len(args) > 1 {
		return errors.New("review takes at most one PR number")
	}
	if len(args) == 0 && (noAttach || strings.TrimSpace(project) != "") {
		// Refusing rather than picking. The picker is a terminal UI over `gh pr
		// list`; opening one for a caller that asked for the non-interactive form
		// would hang it on a list nobody is looking at, and a hang is worse than an
		// error because it reads as work in progress.
		return errors.New("review: give a PR number — --project and --no-attach are the non-interactive form, and a picker would have nobody to ask")
	}
	if noAttach || strings.TrimSpace(project) != "" {
		n, err := parsePRNumberArg(args[0])
		if err != nil {
			return err
		}
		return a.reviewDetached(project, n, noAttach)
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
	_, _ = fmt.Fprintln(a.out, "Usage: awp <deck|mini-deck|diff|doctor|review|ship|logs|config|workspace|w> ...")
	return nil
}

func (a *App) workspaceUsage() error {
	_, _ = fmt.Fprintln(a.out, "Usage: awp <workspace|w> <list|info|attention|new|open|send|repair|label|bootstrap|rename|delete|remove|rm|prune>")
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

// runZdeck opens the deck with zmx behind it instead of tmux: the same UI,
// with the agent, editor, vcs and shell keys rendering a live pane inside the
// deck rather than handing off to a tmux window.
func (a *App) runZdeck(args []string) error {
	if isHelpArgSlice(args) {
		_, _ = fmt.Fprintln(a.out, "Usage: awp zdeck")
		_, _ = fmt.Fprintln(a.out, "")
		_, _ = fmt.Fprintln(a.out, "The same deck, with a different backend. Where `awp deck` opens a tmux")
		_, _ = fmt.Fprintln(a.out, "window for a, e, v and s, zdeck renders the process as a live pane inside")
		_, _ = fmt.Fprintln(a.out, "the deck, on a pty awp owns. Every other key behaves identically.")
		_, _ = fmt.Fprintln(a.out, "")
		_, _ = fmt.Fprintln(a.out, "  a  agent    long-lived: a zmx session, survives closing the pane")
		_, _ = fmt.Fprintln(a.out, "  e  editor   long-lived: same")
		_, _ = fmt.Fprintln(a.out, "  s  shell    ephemeral: spawned by awp, dies with the pane")
		_, _ = fmt.Fprintln(a.out, "  v  vcs      ephemeral: same")
		_, _ = fmt.Fprintln(a.out, "  c  review   unchanged \u2014 the deck already shows the diff in place")
		_, _ = fmt.Fprintln(a.out, "")
		_, _ = fmt.Fprintln(a.out, "  enter      the agent pane \u2014 there is no other client to switch to")
		_, _ = fmt.Fprintln(a.out, "")
		_, _ = fmt.Fprintln(a.out, "ctrl+\\ leaves a pane. Requires zmx on PATH. Unlike `awp deck`, zdeck hosts")
		_, _ = fmt.Fprintln(a.out, "its panes, so it does not need to run inside tmux \u2014 though C and p D still")
		_, _ = fmt.Fprintln(a.out, "open tmux windows and need a client to switch to.")
		return nil
	}
	if len(args) > 0 {
		return fmt.Errorf("zdeck: unexpected argument %q", args[0])
	}
	return runZdeck(a.runner, a.svc, a.in, a.out)
}
