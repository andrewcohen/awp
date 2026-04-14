package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/andrewcohen/awp/internal/charm"
	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/jj"
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
	listOne, err := svc.List()
	if err != nil {
		if jj.IsStaleWorkingCopyError(err) {
			updated, updateErr := maybeUpdateStaleWorkingCopy(j, in, out, err)
			if updateErr != nil {
				return updateErr
			}
			if updated {
				listOne, err = svc.List()
			}
		}
		if err != nil {
			return err
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
	liveByName := map[string]string{}
	liveByID := map[string]struct{}{}
	adoptable := map[string]string{} // sessionName -> sessionID for [awp] sessions not yet in entries
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
			Stale:         stale,
		})
	}

	// Adopt orphan [awp] sessions not in store: render read-only rows so user sees them.
	for name, id := range adoptable {
		repo, ws, ok := parseAwpSession(name)
		if !ok {
			continue
		}
		_ = id
		items = append(items, deckui.Item{
			ProjectName:   repo,
			WorkspaceName: ws,
			Path:          "",
			Status:        "unmanaged",
			PromptPreview: "(live tmux session, not in store)",
			TmuxWindow:    name,
			SessionName:   name,
			Active:        true,
		})
	}

	// Build all-projects list too for the P scope toggle.
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
			Stale:         stale,
		})
	}

	handler := func(req deckui.ActionRequest) error {
		return handleDeckAction(tmuxClient, svc, req)
	}
	launcher := func(repoRoot string) tea.Cmd {
		exe, exeErr := os.Executable()
		if exeErr != nil {
			exe = "awp"
		}
		cmd := exec.Command(exe, "w", "open")
		cmd.Dir = repoRoot
		return tea.ExecProcess(cmd, func(err error) tea.Msg {
			var exitErr *exec.ExitError
			if err != nil && errors.As(err, &exitErr) && exitErr.ExitCode() == 2 {
				return deckui.NewWorkspaceDoneMsg{Cancelled: true}
			}
			return deckui.NewWorkspaceDoneMsg{Err: err}
		})
	}
	model := deckui.NewScoped(items, allItems, projectName, handler).WithNewWorkspaceLauncher(launcher)
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithInput(in), tea.WithOutput(out))
	_, err = program.Run()
	return err
}

func handleDeckAction(tmuxClient *tmux.Client, svc workspace.Service, req deckui.ActionRequest) error {
	item := req.Item
	sessionName := DeckSessionName(item.ProjectName, item.WorkspaceName)
	switch req.Action {
	case deckui.ActionSummon:
		return summonWorkspaceSession(tmuxClient, svc, item)
	case deckui.ActionLogs:
		return ensureAuxWindow(tmuxClient, svc, item, "logs")
	case deckui.ActionTests:
		return ensureAuxWindow(tmuxClient, svc, item, "tests")
	case deckui.ActionShell:
		return splitAgentShell(tmuxClient, svc, item)
	case deckui.ActionRelink:
		return relinkSession(tmuxClient, svc, item)
	case deckui.ActionSetPrompt:
		return svc.UpdatePrompt(item.WorkspaceName, req.Arg)
	case deckui.ActionSetStatus:
		return svc.UpdateStatus(item.WorkspaceName, req.Arg)
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

func ensureAuxWindow(tmuxClient *tmux.Client, svc workspace.Service, item deckui.Item, role string) error {
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
	windows, _ := tmuxClient.ListWindowsInSession(sessionName)
	for _, w := range windows {
		if w.Name == role {
			return nil
		}
	}
	return tmuxClient.NewWindowInSession(sessionName, role, path)
}

func splitAgentShell(tmuxClient *tmux.Client, svc workspace.Service, item deckui.Item) error {
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
	return tmuxClient.SplitPaneInSession(sessionName, "agent", path, false)
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

