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

	tea "github.com/charmbracelet/bubbletea"

	"github.com/andrewcohen/awp/internal/charm"
	"github.com/andrewcohen/awp/internal/config"
	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/jj"
	"github.com/andrewcohen/awp/internal/state"
	"github.com/andrewcohen/awp/internal/tmux"
	"github.com/andrewcohen/awp/internal/workspace"
)

// DeckSessionName returns the tmux session name for a workspace: "[awp]<repo>__<workspace>".
func DeckSessionName(repo, workspace string) string {
	return "[awp]" + repo + "__" + workspace
}

const deckSessionPrefix = "[awp]"

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

type deckOpenCommand struct {
	runner   Runner
	repoRoot string
	stdin    io.Reader
	stdout   io.Writer
	stderr   io.Writer
}

func (c *deckOpenCommand) SetStdin(r io.Reader)  { c.stdin = r }
func (c *deckOpenCommand) SetStdout(w io.Writer) { c.stdout = w }
func (c *deckOpenCommand) SetStderr(w io.Writer) { c.stderr = w }

func (c *deckOpenCommand) Run() error {
	fr := fixedDirRunner{base: c.runner, dir: c.repoRoot}
	svc := newDeckActionServiceWithIO(c.runner, c.repoRoot, c.stdin, c.stdout)
	entries, err := svc.List()
	if err != nil {
		return err
	}
	options := make([]string, 0, len(entries))
	for _, entry := range entries {
		options = append(options, entry.Name)
	}
	req, err := runOpenWithCharm(openRequest{}, options, c.stdin, c.stdout)
	if err != nil {
		if err.Error() == "open cancelled" {
			return ErrOpenCancelled
		}
		return err
	}
	req.Yes = true
	return openWorkspaceInDeckMode(fr, svc, req)
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
	if err != nil {
		return fmt.Errorf("not a jj repository: %w", err)
	}
	projectName := filepath.Base(repoRoot)
	items, allItems, err := loadDeckItems(j, tmuxClient, svc, repoRoot, projectName, in, out)
	if err != nil {
		return err
	}

	handler := func(req deckui.ActionRequest) error {
		actionSvc := svc
		if strings.TrimSpace(req.Item.RepoRoot) != "" {
			actionSvc = newDeckActionService(runner, req.Item.RepoRoot, in)
		}
		return handleDeckAction(tmuxClient, actionSvc, runner, req)
	}
	launcher := func(repoRoot string) tea.Cmd {
		cmd := &deckOpenCommand{runner: runner, repoRoot: repoRoot}
		return tea.Exec(cmd, func(err error) tea.Msg {
			if err != nil && errors.Is(err, ErrOpenCancelled) {
				return deckui.NewWorkspaceDoneMsg{Cancelled: true}
			}
			return deckui.NewWorkspaceDoneMsg{Err: err}
		})
	}
	refresher := func() tea.Cmd {
		return func() tea.Msg {
			items, allItems, err := loadDeckItems(j, tmuxClient, svc, repoRoot, projectName, in, out)
			return deckui.RefreshDoneMsg(items, allItems, err)
		}
	}
	model := deckui.NewScoped(items, allItems, projectName, handler).WithNewWorkspaceLauncher(launcher).WithRefresher(refresher)
	program := tea.NewProgram(model, tea.WithInput(in), tea.WithOutput(out))
	_, err = program.Run()
	return err
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
			ActivePrompt: e.ActivePrompt, Status: e.Status,
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
		items = append(items, deckui.Item{
			ProjectName:   entry.ProjectName,
			WorkspaceName: entry.Name,
			Path:          entry.Path,
			RepoRoot:      entry.RepoRoot,
			Status:        status,
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
		allItems = append(allItems, deckui.Item{
			ProjectName:   entry.ProjectName,
			WorkspaceName: entry.Name,
			Path:          entry.Path,
			RepoRoot:      entry.RepoRoot,
			Status:        status,
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

func handleDeckAction(tmuxClient *tmux.Client, svc workspace.Service, runner Runner, req deckui.ActionRequest) error {
	item := req.Item
	sessionName := DeckSessionName(item.ProjectName, item.WorkspaceName)
	switch req.Action {
	case deckui.ActionSummon:
		return summonWorkspaceSession(tmuxClient, svc, item)
	case deckui.ActionOpenWindow:
		return openNamedWindow(tmuxClient, svc, item, req.Arg)
	case deckui.ActionCI:
		return openCIWindow(tmuxClient, svc, runner, item)
	case deckui.ActionDelete:
		if err := svc.Delete(item.WorkspaceName, true); err != nil {
			return err
		}
		if id, err := tmuxClient.SessionIDByName(sessionName); err != nil {
			return err
		} else if id != "" {
			if err := tmuxClient.KillSession(sessionName); err != nil {
				return err
			}
		}
		return nil
	case deckui.ActionRelink:
		return relinkSession(tmuxClient, svc, item)
	}
	return fmt.Errorf("unknown action: %q session=%q", req.Action, sessionName)
}

func summonWorkspaceSession(tmuxClient *tmux.Client, svc workspace.Service, item deckui.Item) error {
	sessionName := DeckSessionName(item.ProjectName, item.WorkspaceName)
	id, err := tmuxClient.SessionIDByName(sessionName)
	if err != nil {
		return err
	}
	if id == "" {
		path := resolvePath(svc, item)
		if err := tmuxClient.NewSession(sessionName, path, "agent"); err != nil {
			return err
		}
		id, _ = tmuxClient.SessionIDByName(sessionName)
	}
	_ = svc.RecordSession(item.WorkspaceName, id, sessionName)
	return tmuxClient.SwitchClient(sessionName)
}

// openNamedWindow ensures the workspace session exists, then switches to the
// named window, creating it (with an optional default command) if missing.
// Finally, it switches the tmux client to the session so the user lands there.
func openNamedWindow(tmuxClient *tmux.Client, svc workspace.Service, item deckui.Item, windowName string) error {
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

	// Empty windowName = fresh shell window, no dedupe, tmux picks title.
	if strings.TrimSpace(windowName) == "" {
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
		if cmd := defaultWindowCommand(windowName); cmd != "" {
			if err := tmuxClient.SendCommand(target, cmd); err != nil {
				return err
			}
		}
	}
	if err := tmuxClient.SwitchToWindow(target); err != nil {
		return err
	}
	return tmuxClient.SwitchClient(sessionName)
}

func defaultWindowCommand(windowName string) string {
	switch windowName {
	case "editor":
		return "$EDITOR ."
	case "tuicr":
		return "tuicr -r main..@"
	case "vcs":
		return "jjui"
	}
	return ""
}

// openCIWindow opens (or reuses) a `ci` tmux window in the workspace and runs
// `gh run watch` with a fallback to `gh run view`. gh resolves the repo and
// branch from the workspace's cwd.
func openCIWindow(tmuxClient *tmux.Client, svc workspace.Service, _ Runner, item deckui.Item) error {
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
	if err := tmuxClient.SendCommand(target, cmd); err != nil {
		return err
	}
	if err := tmuxClient.SwitchToWindow(target); err != nil {
		return err
	}
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
