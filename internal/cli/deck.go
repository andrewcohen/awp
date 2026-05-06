package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/andrewcohen/awp/internal/charm"
	"github.com/andrewcohen/awp/internal/config"
	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/github"
	"github.com/andrewcohen/awp/internal/jj"
	"github.com/andrewcohen/awp/internal/jobs"
	"github.com/andrewcohen/awp/internal/state"
	"github.com/andrewcohen/awp/internal/tmux"
	"github.com/andrewcohen/awp/internal/workspace"
)

// timeNowForJobs is a small indirection so a future test can stub
// the clock used by orphan detection / GC. Production callers always
// receive time.Now().
var timeNowForJobs = func() time.Time { return time.Now() }

func itoa(i int) string { return strconv.Itoa(i) }

// DeckSessionName returns the tmux session name for a workspace: "[awp]<repo>__<workspace>".
func DeckSessionName(repo, workspace string) string {
	return "[awp]" + repo + "__" + workspace
}

const deckSessionPrefix = "[awp]"

type noopReporter struct{}

func (noopReporter) Step(string) {}
func (noopReporter) Log(string)  {}

// recoverRepoRootFromSession returns the repo root for the workspace whose
// awp tmux session the deck was launched from. Used when the popup's CWD
// doesn't resolve to a jj repo (e.g. it landed in $HOME because the parent
// pane was running `less` and tmux couldn't read its cwd). Returns false
// if we're not in an [awp]... session or no matching workspace is known.
func recoverRepoRootFromSession(tmuxClient *tmux.Client, svc workspace.Service) (string, bool) {
	sessionName, err := tmuxClient.CurrentSessionName()
	if err != nil {
		return "", false
	}
	repo, ws, ok := parseAwpSession(strings.TrimSpace(sessionName))
	if !ok {
		return "", false
	}
	all, err := svc.ListAll()
	if err != nil {
		return "", false
	}
	for _, e := range all {
		if e.ProjectName == repo && e.Name == ws && strings.TrimSpace(e.RepoRoot) != "" {
			return e.RepoRoot, true
		}
	}
	return "", false
}

// parseAwpSession parses "[awp]<repo>__<workspace>" into (repo, workspace, true).
func parseAwpSession(name string) (string, string, bool) {
	if !strings.HasPrefix(name, deckSessionPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(name, deckSessionPrefix)
	idx := strings.Index(rest, "__")
	if idx < 0 {
		return "", "", false
	}
	return rest[:idx], rest[idx+2:], true
}

type fixedDirRunner struct {
	base Runner
	dir  string
}

func (r fixedDirRunner) Run(ctx context.Context, dir string, name string, args ...string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		dir = r.dir
	}
	return r.base.Run(ctx, dir, name, args...)
}

func newDeckActionServiceWithIO(runner Runner, repoRoot string, in io.Reader, out io.Writer) workspace.Service {
	fr := fixedDirRunner{base: runner, dir: repoRoot}
	return workspace.NewService(workspace.Dependencies{
		JJ:            jj.New(fr),
		Tmux:          tmux.New(runner),
		Store:         state.NewJSONStore(),
		Hooks:         config.NewFileHookProvider(),
		Runner:        fr,
		InvocationDir: repoRoot,
		Input:         in,
		Out:           out,
	})
}

func newDeckActionService(runner Runner, repoRoot string, in io.Reader) workspace.Service {
	return newDeckActionServiceWithIO(runner, repoRoot, in, io.Discard)
}

func runDeckWithCharm(runner Runner, svc workspace.Service, in io.Reader, out io.Writer) error {
	if os.Getenv("TMUX") == "" {
		return fmt.Errorf("awp deck must run inside tmux (hint: bind a display-popup -E awp deck)")
	}
	if charm.IsDumbTerminal() {
		return fmt.Errorf("awp deck not available in dumb terminal")
	}
	if runner == nil {
		runner = NewExecRunner()
	}
	j := jj.New(runner)
	tmuxClient := tmux.New(runner)

	repoRoot, err := j.RepoRoot()
	rootBad := err != nil || workspace.IsHomeDir(repoRoot)
	if rootBad {
		// The popup spawned with a CWD that isn't a project (e.g. tmux's
		// `pane_current_path` resolved to $HOME because the parent pane
		// was running `less` and proc lookup couldn't read its cwd).
		// If we're inside an [awp]<repo>__<workspace> session, recover
		// the repo root from the cross-repo state instead of giving up.
		if recovered, ok := recoverRepoRootFromSession(tmuxClient, svc); ok {
			repoRoot = recovered
			runner = fixedDirRunner{base: runner, dir: repoRoot}
			j = jj.New(runner)
			svc = newDeckActionServiceWithIO(runner, repoRoot, in, out)
		} else if err != nil {
			return fmt.Errorf("not a jj repository: %w", err)
		} else {
			return fmt.Errorf("refusing to open deck at $HOME — cd into a project first")
		}
	}
	projectName := filepath.Base(repoRoot)
	items, allItems, err := loadDeckItems(j, tmuxClient, svc, repoRoot, projectName, in, out)
	if err != nil {
		return err
	}

	cfg, _ := config.Load(repoRoot)
	var userActions []deckui.UserAction
	for name, act := range cfg.Actions {
		focus := true
		if act.Focus != nil {
			focus = *act.Focus
		}
		userActions = append(userActions, deckui.UserAction{
			Name:       name,
			Command:    act.Command,
			Alias:      act.Alias,
			Background: act.Background,
			Focus:      focus,
		})
	}
	actionsByName := make(map[string]deckui.UserAction, len(userActions))
	for _, a := range userActions {
		actionsByName[a.Name] = a
	}
	handler := func(req deckui.ActionRequest) error {
		if req.Action == deckui.ActionCreateWorkspace {
			dir := strings.TrimSpace(req.Item.RepoRoot)
			if dir == "" {
				dir = repoRoot
			}
			fr := fixedDirRunner{base: runner, dir: dir}
			actionSvc := newDeckActionService(runner, dir, in)
			reporter := req.Reporter
			if reporter == nil {
				reporter = noopReporter{}
			}
			return openWorkspaceWithReporter(fr, actionSvc, openRequest{
				Name:     req.Workspace.Name,
				Bookmark: req.Workspace.Bookmark,
				Prompt:   req.Workspace.Prompt,
				Yes:      true,
			}, reporter)
		}
		if req.Action == deckui.ActionReview {
			n, err := strconv.Atoi(req.Arg)
			if err != nil {
				return fmt.Errorf("review: invalid PR number %q", req.Arg)
			}
			dir := strings.TrimSpace(req.Item.RepoRoot)
			if dir == "" {
				dir = repoRoot
			}
			fr := fixedDirRunner{base: runner, dir: dir}
			reviewSvc := newDeckActionServiceWithIO(runner, dir, nil, io.Discard)
			reporter := req.Reporter
			if reporter == nil {
				reporter = noopReporter{}
			}
			return runReviewWithReporter(fr, reviewSvc, n, nil, reporter)
		}
		reporter := req.Reporter
		if reporter == nil {
			reporter = noopReporter{}
		}
		if req.Action == deckui.ActionCustom {
			ua, ok := actionsByName[req.Arg]
			if !ok {
				return fmt.Errorf("unknown user action %q", req.Arg)
			}
			actionSvc := svc
			if strings.TrimSpace(req.Item.RepoRoot) != "" {
				actionSvc = newDeckActionService(runner, req.Item.RepoRoot, in)
			}
			return openCustomActionWindow(tmuxClient, actionSvc, req.Item, ua, reporter)
		}
		actionSvc := svc
		if strings.TrimSpace(req.Item.RepoRoot) != "" {
			actionSvc = newDeckActionService(runner, req.Item.RepoRoot, in)
		}
		return handleDeckAction(tmuxClient, actionSvc, runner, req, reporter)
	}
	bookmarkFetcher := func(itemRepoRoot string) tea.Cmd {
		return func() tea.Msg {
			dir := strings.TrimSpace(itemRepoRoot)
			if dir == "" {
				dir = repoRoot
			}
			fr := fixedDirRunner{base: runner, dir: dir}
			if out, err := fr.Run(context.Background(), dir, "jj", "git", "fetch"); err != nil {
				return deckui.BookmarksDoneMsg{Err: fmt.Errorf("jj git fetch: %w: %s", err, out)}
			}
			names, err := jj.New(fr).AllBookmarks()
			if err != nil {
				return deckui.BookmarksDoneMsg{Err: err}
			}
			return deckui.BookmarksDoneMsg{Bookmarks: names}
		}
	}
	refresher := func() tea.Cmd {
		return func() tea.Msg {
			items, allItems, err := loadDeckItems(j, tmuxClient, svc, repoRoot, projectName, in, out)
			return deckui.RefreshDoneMsg(items, allItems, err)
		}
	}
	prFetcher := func(itemRepoRoot string) tea.Cmd {
		return func() tea.Msg {
			dir := strings.TrimSpace(itemRepoRoot)
			if dir == "" {
				dir = repoRoot
			}
			gh := github.New(fixedDirRunner{base: runner, dir: dir})
			prs, err := gh.ListPRs()
			if err != nil {
				return deckui.PRFetchDoneMsg{Err: err}
			}
			items := make([]deckui.PRItem, len(prs))
			for i, pr := range prs {
				author := pr.Author.Login
				if author == "" {
					author = "?"
				}
				items[i] = deckui.PRItem{
					Number:  pr.Number,
					Title:   pr.Title,
					HeadRef: pr.HeadRef,
					Author:  author,
					IsDraft: pr.IsDraft,
				}
			}
			return deckui.PRFetchDoneMsg{PRs: items}
		}
	}
	stateEditor := func() tea.Cmd {
		path, err := state.GlobalStorePath()
		if err != nil {
			return func() tea.Msg { return deckui.StateEditDoneMsg{Err: err} }
		}
		editor := strings.TrimSpace(os.Getenv("EDITOR"))
		if editor == "" {
			return func() tea.Msg { return deckui.StateEditDoneMsg{Err: fmt.Errorf("$EDITOR is not set")} }
		}
		c := exec.Command("sh", "-c", `exec "$EDITOR" "$1"`, "sh", path)
		return tea.ExecProcess(c, func(err error) tea.Msg {
			return deckui.StateEditDoneMsg{Err: err}
		})
	}
	asyncLauncher, asyncList, asyncCancel, asyncDismiss, asyncLog := buildAsyncJobs(repoRoot)
	if asyncLauncher != nil {
		go runJobsStartupCleanup()
	}

	model := deckui.NewScoped(items, allItems, projectName, handler).
		WithRefresher(refresher).
		WithPRFetcher(prFetcher).WithBookmarkFetcher(bookmarkFetcher).
		WithStateEditor(stateEditor).WithUserActions(userActions).
		WithScope(loadDeckScope()).
		WithScopeChanged(saveDeckScope).
		WithStateChangeWatcher(newDeckStateChangeWatcher()).
		WithProjectFinder(projectFinderFromRoots(cfg.Deck.ProjectRoots, 4)).
		WithProjectOpener(openProjectViaTmux(runner)).
		WithAsyncJobLauncher(asyncLauncher).
		WithJobsListRefresher(asyncList).
		WithJobCancelHandler(asyncCancel).
		WithJobDismissHandler(asyncDismiss).
		WithJobLogOpener(asyncLog)
	program := tea.NewProgram(model, tea.WithInput(in), tea.WithOutput(out), tea.WithAltScreen())
	_, err = program.Run()
	return err
}

// buildAsyncJobs returns the deck-side glue to the jobs subsystem:
// a launcher that translates a deckui.AsyncJobSpec into an
// internal/jobs.Spec + spawns a detached subprocess, a list
// refresher that powers the tray and the J overlay, and three
// per-action handlers (cancel via SIGTERM, dismiss = delete record,
// open log in $PAGER). Returns nil-valued functions if the jobs
// store can't be initialized; the deck silently falls back to the
// synchronous path.
func buildAsyncJobs(repoRoot string) (deckui.AsyncJobLauncher, deckui.JobsListRefresher, deckui.JobCancelHandler, deckui.JobDismissHandler, deckui.JobLogOpener) {
	store, err := jobs.NewStore()
	if err != nil {
		return nil, nil, nil, nil, nil
	}
	launcher := func(spec deckui.AsyncJobSpec) error {
		root := strings.TrimSpace(spec.RepoRoot)
		if root == "" {
			root = repoRoot
		}
		jspec := jobs.Spec{
			Action:        jobs.JobAction(spec.Action),
			RepoRoot:      root,
			Name:          spec.Name,
			Bookmark:      spec.Bookmark,
			Prompt:        spec.Prompt,
			Arg:           spec.Arg,
			WorkspaceName: spec.WorkspaceName,
			WorkspacePath: spec.WorkspacePath,
		}
		_, err := store.Spawn(jspec, spec.Title, jobs.SpawnOptions{})
		return err
	}
	listRefresher := func() []deckui.Job {
		return projectJobs(store)
	}
	cancel := func(id string) error {
		return store.SignalCancel(jobs.JobID(id))
	}
	dismiss := func(id string) error {
		return store.Delete(jobs.JobID(id))
	}
	logOpen := func(id string) tea.Cmd {
		path := store.LogPath(jobs.JobID(id))
		pager := strings.TrimSpace(os.Getenv("PAGER"))
		if pager == "" {
			pager = "less"
		}
		c := exec.Command("sh", "-c", `exec "$PAGER" "$1"`, "sh", path)
		c.Env = append(os.Environ(), "PAGER="+pager)
		return tea.ExecProcess(c, func(err error) tea.Msg {
			return deckui.JobActionDoneMsg{JobID: id, Kind: "log", Err: err}
		})
	}
	return launcher, listRefresher, cancel, dismiss, logOpen
}

// projectJobs builds the deckui-side projection of the jobs
// directory: runs orphan detection in-line, then converts each
// internal/jobs.Job into a deckui.Job record. Sorted newest-first.
func projectJobs(store *jobs.Store) []deckui.Job {
	all, err := store.List()
	if err != nil {
		return nil
	}
	now := timeNowForJobs()
	out := make([]deckui.Job, 0, len(all))
	for _, j := range all {
		if !j.Status.IsTerminal() && jobs.IsOrphan(j, now) {
			_ = store.Update(j.ID, func(rec *jobs.Job) error {
				if rec.Status.IsTerminal() {
					return nil
				}
				rec.Status = jobs.StatusOrphaned
				rec.ErrMsg = "subprocess died (pid " + itoa(rec.PID) + ")"
				ended := now
				rec.EndedAt = &ended
				return nil
			})
			j.Status = jobs.StatusOrphaned
		}
		out = append(out, toDeckJob(j, store))
	}
	// Newest first — most recently-started jobs at the top of the
	// overlay so users see what they just dispatched.
	sort.Slice(out, func(i, k int) bool {
		return out[i].StartedAt.After(out[k].StartedAt)
	})
	return out
}

func toDeckJob(j jobs.Job, store *jobs.Store) deckui.Job {
	steps := make([]deckui.JobStep, 0, len(j.Steps))
	for _, st := range j.Steps {
		steps = append(steps, deckui.JobStep{
			Label: st.Label,
			Done:  st.State == jobs.StepDone,
			Error: st.State == jobs.StepError,
		})
	}
	ended := time.Time{}
	if j.EndedAt != nil {
		ended = *j.EndedAt
	}
	return deckui.Job{
		ID:            string(j.ID),
		Title:         j.Title,
		Action:        string(j.Spec.Action),
		Status:        deckui.JobStatus(j.Status),
		StartedAt:     j.StartedAt,
		EndedAt:       ended,
		Steps:         steps,
		LogsTail:      j.LogsInline,
		ErrMsg:        j.ErrMsg,
		LogPath:       store.LogPath(j.ID),
		PID:           j.PID,
		WorkspaceName: j.Spec.WorkspaceName,
		WorkspacePath: j.Spec.WorkspacePath,
		RepoRoot:      j.Spec.RepoRoot,
	}
}

// runJobsStartupCleanup sweeps terminal records older than their
// retention threshold. Runs in a goroutine on deck startup; failures
// are silent (the next deck launch will retry). Orphan detection is
// handled by countActiveJobs on every refresh tick.
func runJobsStartupCleanup() {
	store, err := jobs.NewStore()
	if err != nil {
		return
	}
	all, err := store.List()
	if err != nil {
		return
	}
	now := timeNowForJobs()
	for _, j := range all {
		if !j.Status.IsTerminal() {
			continue
		}
		if j.EndedAt == nil {
			continue
		}
		retention := jobs.RetentionDone
		if j.Status == jobs.StatusOrphaned {
			retention = jobs.RetentionOrphaned
		}
		if now.Sub(*j.EndedAt) > retention {
			_ = store.Delete(j.ID)
		}
	}
}

func loadDeckItems(j *jj.Client, tmuxClient *tmux.Client, svc workspace.Service, repoRoot, projectName string, in io.Reader, out io.Writer) ([]deckui.Item, []deckui.Item, error) {
	listOne, err := svc.List()
	if err != nil {
		if jj.IsStaleWorkingCopyError(err) {
			updated, updateErr := maybeUpdateStaleWorkingCopy(j, in, out, err)
			if updateErr != nil {
				return nil, nil, updateErr
			}
			if updated {
				listOne, err = svc.List()
			}
		}
		if err != nil {
			return nil, nil, err
		}
	}
	entries := make([]workspace.CrossRepoEntry, 0, len(listOne))
	for _, e := range listOne {
		entries = append(entries, workspace.CrossRepoEntry{
			RepoRoot: repoRoot, ProjectName: projectName, Name: e.Name, Path: e.Path,
			SessionID: e.SessionID, SessionName: e.SessionName,
			ActivePrompt: e.ActivePrompt, Status: e.Status, Unread: e.Unread,
		})
	}

	liveSessions, _ := tmuxClient.ListSessions()
	currentSession, _ := tmuxClient.CurrentSessionName()
	liveByName := map[string]string{}
	liveByID := map[string]struct{}{}
	adoptable := map[string]string{}
	for _, s := range liveSessions {
		liveByName[s.Name] = s.ID
		liveByID[s.ID] = struct{}{}
		if repo, _, ok := parseAwpSession(s.Name); ok && repo == projectName {
			adoptable[s.Name] = s.ID
		}
	}

	items := make([]deckui.Item, 0, len(entries))
	for _, entry := range entries {
		sessionName := DeckSessionName(entry.ProjectName, entry.Name)
		_, nameMatch := liveByName[sessionName]
		delete(adoptable, sessionName)
		// If the user is literally looking at this workspace's session,
		// clear the unread badge — by definition they've seen it.
		if entry.Unread && sessionName == currentSession {
			if err := svc.MarkRead(entry.Name); err == nil {
				entry.Unread = false
			}
		}
		status := entry.Status
		if strings.TrimSpace(status) == "" {
			status = "idle"
		}
		stale := false
		if entry.SessionID != "" {
			if _, ok := liveByID[entry.SessionID]; !ok && !nameMatch {
				stale = true
			}
		}
		if nameMatch && agentPaneIsShell(tmuxClient, sessionName) {
			status = "exited"
		}
		items = append(items, deckui.Item{
			ProjectName:   entry.ProjectName,
			WorkspaceName: entry.Name,
			Path:          entry.Path,
			RepoRoot:      entry.RepoRoot,
			Status:        status,
			Unread:        entry.Unread,
			PromptPreview: entry.ActivePrompt,
			TmuxWindow:    sessionName,
			SessionName:   sessionName,
			Active:        nameMatch,
			Current:       sessionName == currentSession,
			Stale:         stale,
		})
	}
	for name := range adoptable {
		repo, ws, ok := parseAwpSession(name)
		if !ok {
			continue
		}
		items = append(items, deckui.Item{
			ProjectName:   repo,
			WorkspaceName: ws,
			Path:          "",
			Status:        "unmanaged",
			PromptPreview: "(live tmux session, not in store)",
			TmuxWindow:    name,
			SessionName:   name,
			Active:        true,
			Current:       name == currentSession,
		})
	}

	allEntries, _ := svc.ListAll()
	allItems := make([]deckui.Item, 0, len(allEntries))
	for _, entry := range allEntries {
		sessionName := DeckSessionName(entry.ProjectName, entry.Name)
		_, nameMatch := liveByName[sessionName]
		// Same auto-clear as the per-repo loop, but only suppress in-UI
		// here — writing the cleared state requires a service rooted at
		// `entry.RepoRoot`, which the writer-side check in report-status
		// handles. The deck's own loadDeckItems for that repo will sync.
		if entry.Unread && sessionName == currentSession {
			entry.Unread = false
		}
		status := entry.Status
		if strings.TrimSpace(status) == "" {
			status = "idle"
		}
		stale := false
		if entry.SessionID != "" {
			if _, ok := liveByID[entry.SessionID]; !ok && !nameMatch {
				stale = true
			}
		}
		if nameMatch && agentPaneIsShell(tmuxClient, sessionName) {
			status = "exited"
		}
		allItems = append(allItems, deckui.Item{
			ProjectName:   entry.ProjectName,
			WorkspaceName: entry.Name,
			Path:          entry.Path,
			RepoRoot:      entry.RepoRoot,
			Status:        status,
			Unread:        entry.Unread,
			PromptPreview: entry.ActivePrompt,
			TmuxWindow:    sessionName,
			SessionName:   sessionName,
			Active:        nameMatch,
			Current:       sessionName == currentSession,
			Stale:         stale,
		})
	}
	return items, allItems, nil
}

func handleDeckAction(tmuxClient *tmux.Client, svc workspace.Service, runner Runner, req deckui.ActionRequest, reporter deckui.Reporter) error {
	if reporter == nil {
		reporter = noopReporter{}
	}
	item := req.Item
	sessionName := DeckSessionName(item.ProjectName, item.WorkspaceName)
	switch req.Action {
	case deckui.ActionSummon:
		return summonWorkspaceSession(tmuxClient, svc, item, reporter)
	case deckui.ActionOpenWindow:
		return openNamedWindow(tmuxClient, svc, item, req.Arg, reporter)
	case deckui.ActionCI:
		return openCIWindow(tmuxClient, svc, runner, item, reporter)
	case deckui.ActionLastSession:
		reporter.Step("Switch to last tmux session")
		return tmuxClient.SwitchClientLast()
	case deckui.ActionDelete:
		reporter.Step(fmt.Sprintf("Delete workspace %s", item.WorkspaceName))
		opts := workspace.DeleteOptions{Force: true}
		var queuePath string
		if sessionID, err := tmuxClient.CurrentSessionID(); err == nil {
			if path, ok := pendingKillsPath(sessionID); ok {
				queuePath = path
				if item.Current {
					_ = appendPendingAction(path, "switch", DeckSessionName(item.ProjectName, "default"))
				}
				opts.DeferTmuxKill = func(window string) {
					_ = appendPendingKill(path, window)
				}
			}
		}
		if err := svc.DeleteWithOptions(item.WorkspaceName, opts); err != nil {
			return err
		}
		id, err := tmuxClient.SessionIDByName(sessionName)
		if err != nil {
			return err
		}
		if id != "" {
			if queuePath != "" {
				reporter.Step(fmt.Sprintf("Queue tmux session removal %s", sessionName))
				_ = appendPendingAction(queuePath, "session", sessionName)
			} else {
				reporter.Step(fmt.Sprintf("Kill tmux session %s", sessionName))
				if err := tmuxClient.KillSession(sessionName); err != nil {
					return err
				}
			}
		}
		return nil
	case deckui.ActionRelink:
		reporter.Step("Relink session")
		return relinkSession(tmuxClient, svc, item)
	}
	return fmt.Errorf("unknown action: %q session=%q", req.Action, sessionName)
}

func summonWorkspaceSession(tmuxClient *tmux.Client, svc workspace.Service, item deckui.Item, reporter deckui.Reporter) error {
	sessionName := DeckSessionName(item.ProjectName, item.WorkspaceName)
	id, err := tmuxClient.SessionIDByName(sessionName)
	if err != nil {
		return err
	}
	if id == "" {
		reporter.Step(fmt.Sprintf("Create tmux session %s", sessionName))
		path := resolvePath(svc, item)
		if err := tmuxClient.NewSession(sessionName, path, "agent"); err != nil {
			return err
		}
		id, _ = tmuxClient.SessionIDByName(sessionName)
	}
	if stale := ensureWorkspaceSessionEnvForItem(tmuxClient, sessionName, item.ProjectName, item.WorkspaceName, item.RepoRoot); stale {
		reporter.Log("agent missing AWP_WORKSPACE — restart agent to enable status reporting")
	}
	_ = svc.RecordSession(item.WorkspaceName, id, sessionName)
	_ = svc.MarkRead(item.WorkspaceName)
	reporter.Step(fmt.Sprintf("Switch to %s", sessionName))
	return tmuxClient.SwitchClient(sessionName)
}

// openNamedWindow ensures the workspace session exists, then switches to the
// named window, creating it (with an optional default command) if missing.
// Finally, it switches the tmux client to the session so the user lands there.
func openNamedWindow(tmuxClient *tmux.Client, svc workspace.Service, item deckui.Item, arg string, reporter deckui.Reporter) error {
	windowName, cmdOverride := arg, ""
	if idx := strings.IndexByte(arg, ':'); idx >= 0 {
		windowName = arg[:idx]
		cmdOverride = arg[idx+1:]
	}

	sessionName := DeckSessionName(item.ProjectName, item.WorkspaceName)
	id, err := tmuxClient.SessionIDByName(sessionName)
	if err != nil {
		return err
	}
	path := resolvePath(svc, item)
	if id == "" {
		reporter.Step(fmt.Sprintf("Create tmux session %s", sessionName))
		if err := tmuxClient.NewSession(sessionName, path, "agent"); err != nil {
			return err
		}
		id, _ = tmuxClient.SessionIDByName(sessionName)
		_ = svc.RecordSession(item.WorkspaceName, id, sessionName)
	}
	_ = ensureWorkspaceSessionEnvForItem(tmuxClient, sessionName, item.ProjectName, item.WorkspaceName, item.RepoRoot)

	// Empty windowName = fresh shell window, no dedupe, tmux picks title.
	if strings.TrimSpace(windowName) == "" {
		reporter.Step("Open shell window")
		target, err := tmuxClient.NewShellWindowInSession(sessionName, path)
		if err != nil {
			return err
		}
		if err := tmuxClient.SwitchToWindow(target); err != nil {
			return err
		}
		return tmuxClient.SwitchClient(sessionName)
	}

	target := sessionName + ":" + windowName
	exists := false
	justCreated := false
	windows, _ := tmuxClient.ListWindowsInSession(sessionName)
	for _, w := range windows {
		if w.Name == windowName {
			exists = true
			break
		}
	}
	if !exists {
		reporter.Step(fmt.Sprintf("Open %s window", windowName))
		if err := tmuxClient.NewWindowInSession(sessionName, windowName, path); err != nil {
			return err
		}
		justCreated = true
	}
	winCmd := cmdOverride
	if winCmd == "" {
		winCmd = defaultWindowCommandWithRepo(windowName, item.RepoRoot)
	}
	if winCmd != "" && (justCreated || paneIsShell(tmuxClient, target)) {
		reporter.Step(fmt.Sprintf("Run %s", winCmd))
		if err := tmuxClient.SendCommand(target, winCmd); err != nil {
			return err
		}
	}
	reporter.Step(fmt.Sprintf("Switch to %s", target))
	if err := tmuxClient.SwitchToWindow(target); err != nil {
		return err
	}
	_ = svc.MarkRead(item.WorkspaceName)
	return tmuxClient.SwitchClient(sessionName)
}

// agentPaneIsShell returns true when the agent window of a workspace session
// has no agent process running (i.e. the pane has dropped back to a shell).
// Used to mark a workspace as "exited" in the deck even when the workspace
// state file still claims an older status.
func agentPaneIsShell(tmuxClient *tmux.Client, sessionName string) bool {
	return paneIsShell(tmuxClient, sessionName+":agent")
}

func paneIsShell(tmuxClient *tmux.Client, target string) bool {
	cmd, err := tmuxClient.PaneCurrentCommand(target)
	if err != nil {
		return false
	}
	switch strings.TrimSpace(cmd) {
	case "bash", "zsh", "fish", "sh", "dash":
		return true
	default:
		return false
	}
}

func defaultWindowCommand(windowName string) string {
	return defaultWindowCommandWithRepo(windowName, "")
}

// defaultWindowCommandWithRepo returns the default command to run in a freshly
// created (or shell-reset) named window. Pulls the agent command from
// per-repo + global config; defaults to "pi" when nothing is configured.
func defaultWindowCommandWithRepo(windowName, repoRoot string) string {
	switch windowName {
	case "editor":
		return "$EDITOR"
	case "review":
		return "tuicr -r @"
	case "vcs":
		return "jjui"
	case "agent":
		return config.AgentCommand(repoRoot)
	}
	return ""
}

// openCIWindow opens (or reuses) a `ci` tmux window in the workspace and runs
// `gh run watch` with a fallback to `gh run view`. gh resolves the repo and
// branch from the workspace's cwd.
func openCIWindow(tmuxClient *tmux.Client, svc workspace.Service, _ Runner, item deckui.Item, reporter deckui.Reporter) error {
	reporter.Step("Open ci window")
	path := resolvePath(svc, item)
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("ci: no path for workspace %q", item.WorkspaceName)
	}

	sessionName := DeckSessionName(item.ProjectName, item.WorkspaceName)
	id, err := tmuxClient.SessionIDByName(sessionName)
	if err != nil {
		return err
	}
	if id == "" {
		if err := tmuxClient.NewSession(sessionName, path, "agent"); err != nil {
			return err
		}
		id, _ = tmuxClient.SessionIDByName(sessionName)
		_ = svc.RecordSession(item.WorkspaceName, id, sessionName)
	}
	_ = ensureWorkspaceSessionEnvForItem(tmuxClient, sessionName, item.ProjectName, item.WorkspaceName, item.RepoRoot)

	target := sessionName + ":ci"
	exists := false
	windows, _ := tmuxClient.ListWindowsInSession(sessionName)
	for _, w := range windows {
		if w.Name == "ci" {
			exists = true
			break
		}
	}
	if !exists {
		if err := tmuxClient.NewWindowInSession(sessionName, "ci", path); err != nil {
			return err
		}
	}
	cmd := `bash -c 'b=$(jj log --no-graph -r "latest(::@ & bookmarks())" -T "local_bookmarks.map(|b| b.name()).join(\"\n\") ++ \"\n\"" | head -n1); id=$(gh run list --branch "$b" --limit 1 --json databaseId -q ".[0].databaseId"); gh run watch "$id" --compact --exit-status || gh run view "$id"'`
	if !exists || paneIsShell(tmuxClient, target) {
		if err := tmuxClient.SendCommand(target, cmd); err != nil {
			return err
		}
	}
	if err := tmuxClient.SwitchToWindow(target); err != nil {
		return err
	}
	_ = svc.MarkRead(item.WorkspaceName)
	return tmuxClient.SwitchClient(sessionName)
}

func openCustomActionWindow(tmuxClient *tmux.Client, svc workspace.Service, item deckui.Item, ua deckui.UserAction, reporter deckui.Reporter) error {
	reporter.Step(fmt.Sprintf("Run user action %s", ua.Name))
	sessionName := DeckSessionName(item.ProjectName, item.WorkspaceName)
	id, err := tmuxClient.SessionIDByName(sessionName)
	if err != nil {
		return err
	}
	path := resolvePath(svc, item)
	if id == "" {
		if err := tmuxClient.NewSession(sessionName, path, "agent"); err != nil {
			return err
		}
		id, _ = tmuxClient.SessionIDByName(sessionName)
		_ = svc.RecordSession(item.WorkspaceName, id, sessionName)
	}
	_ = ensureWorkspaceSessionEnvForItem(tmuxClient, sessionName, item.ProjectName, item.WorkspaceName, item.RepoRoot)

	windowName := ua.Name
	target := sessionName + ":" + windowName
	exists := false
	windows, _ := tmuxClient.ListWindowsInSession(sessionName)
	for _, w := range windows {
		if w.Name == windowName {
			exists = true
			break
		}
	}
	if !exists {
		if err := tmuxClient.NewWindowInSession(sessionName, windowName, path); err != nil {
			return err
		}
	}
	if !exists || paneIsShell(tmuxClient, target) {
		if err := tmuxClient.SendCommand(target, ua.Command); err != nil {
			return err
		}
	}
	if !ua.Focus {
		// Window is created and the command is running; deliberately
		// don't pull the user away from the deck.
		return nil
	}
	if err := tmuxClient.SwitchToWindow(target); err != nil {
		return err
	}
	_ = svc.MarkRead(item.WorkspaceName)
	return tmuxClient.SwitchClient(sessionName)
}

func relinkSession(tmuxClient *tmux.Client, svc workspace.Service, item deckui.Item) error {
	sessionName := DeckSessionName(item.ProjectName, item.WorkspaceName)
	id, err := tmuxClient.SessionIDByName(sessionName)
	if err != nil {
		return err
	}
	if id != "" {
		return svc.RecordSession(item.WorkspaceName, id, sessionName)
	}
	return svc.ClearSession(item.WorkspaceName)
}

func resolvePath(svc workspace.Service, item deckui.Item) string {
	if strings.TrimSpace(item.Path) != "" {
		return item.Path
	}
	info, err := svc.Info(item.WorkspaceName)
	if err != nil {
		return ""
	}
	return info.Path
}

func maybeUpdateStaleWorkingCopy(j *jj.Client, in io.Reader, out io.Writer, cause error) (bool, error) {
	if !isInteractiveInput(in) {
		return false, cause
	}
	if out != nil {
		_, _ = fmt.Fprintf(out, "Detected stale jj working copy:\n\n%s\n\nRun `jj workspace update-stale` now? [y/N]: ", cause)
	}
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	answer := strings.TrimSpace(strings.ToLower(line))
	if answer != "y" && answer != "yes" {
		return false, cause
	}
	if out != nil {
		_, _ = fmt.Fprintln(out, "Updating stale working copy...")
	}
	if err := j.UpdateStale(); err != nil {
		return false, err
	}
	if out != nil {
		_, _ = fmt.Fprintln(out, "Working copy updated. Reloading deck...")
	}
	return true, nil
}
