package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/andrewcohen/awp/internal/config"
	"github.com/andrewcohen/awp/internal/watch"
	"github.com/andrewcohen/awp/internal/workspace"

	tea "github.com/charmbracelet/bubbletea"
)

// codingAgentInvocation returns the agent launch command for a coding
// workspace. When the repo has a configured dev_loop and the agent is Claude,
// it appends `--append-system-prompt` with the generated loop instruction, so
// a new agent starts already following the loop that `awp watch` observes —
// in the system prompt (persists across the session, works even with no task
// prompt) rather than a one-shot prompt prefix. The preamble is flattened to
// a single line because tmux send-keys can't carry embedded newlines. The
// review flow intentionally uses config.AgentInvocation directly (a reviewer
// shouldn't be told to work in units / run gates / commit).
func codingAgentInvocation(repoRoot string) string {
	inv := config.AgentInvocation(repoRoot)
	cfg, _ := config.Load(repoRoot)
	if !watch.IsConfigured(cfg) {
		return inv
	}
	agent := strings.TrimSpace(cfg.Agent)
	if agent == "" {
		agent = config.DefaultAgent
	}
	if !strings.Contains(agent, "claude") {
		return inv // --append-system-prompt is Claude-specific
	}
	preamble := watch.GeneratePreamble(watch.Resolve(cfg))
	if strings.TrimSpace(preamble) == "" {
		return inv
	}
	// Pass the preamble by file path (--append-system-prompt-file) rather
	// than embedding the text inline: Claude reads it directly, so the launch
	// command stays short and there's no shell-quoting of the multi-line
	// content (embedding it inline floods/garbles the command line).
	path, err := writeDevLoopPreamble(repoRoot, preamble)
	if err != nil {
		return inv
	}
	return inv + " --append-system-prompt-file " + shellSingleQuote(path)
}

// writeDevLoopPreamble persists the generated preamble to a stable per-repo
// path under ~/.awp so the agent launch command can `cat` it instead of
// carrying the whole text inline.
func writeDevLoopPreamble(repoRoot, preamble string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	name := strings.ReplaceAll(strings.Trim(repoRoot, "/"), "/", "-")
	if name == "" {
		name = "default"
	}
	dir := filepath.Join(home, ".awp", "dev-loop")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name+".md")
	if err := os.WriteFile(path, []byte(preamble), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

const watchUsage = `awp watch — read-only live view of an agent's dev-loop progress

Usage:
  awp watch [workspace]        Watch a workspace (picker if omitted)
  awp watch --once             Print one frame and exit (no live UI)
  awp watch --transcript PATH  Replay a specific transcript file
  awp watch --repo PATH        Repo root to resolve dev_loop config from
  awp watch --suggest          Print a prompt to configure dev_loop in .awp/config.json
  awp watch --preamble         Print the dev-loop instruction to give an agent
  awp watch --help             Show this help

The view shows the agent's units of work (from its task/todo list, or a
markdown checklist / "Unit N:" prose) alongside the current unit's position in
the dev loop (explore → implement → test → gates → commit), per-unit gate
pass/fail, and a stall signal. Configure the loop under "dev_loop" in
.awp/config.json (see 'awp watch --suggest').
`

// runWatch implements `awp watch [workspace]`: a read-only live view of an
// agent's task progress — its todo list coupled with its position in the
// project's development loop. With no argument it shows a picker.
func (a *App) runWatch(args []string) error {
	var once, suggest, preamble bool
	var transcriptFlag, repoRoot string
	var positional []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--help", "-h", "help":
			fmt.Fprint(a.out, watchUsage)
			return nil
		case "--once":
			once = true
		case "--suggest":
			suggest = true
		case "--preamble":
			preamble = true
		case "--transcript":
			if i+1 < len(args) {
				i++
				transcriptFlag = args[i]
			}
		case "--repo":
			if i+1 < len(args) {
				i++
				repoRoot = args[i]
			}
		default:
			positional = append(positional, args[i])
		}
	}

	// --suggest / --preamble are repo-level, not workspace-level: resolve the
	// config from --repo (or the current dir) and print, no picker.
	if suggest || preamble {
		root := repoRoot
		if root == "" {
			root, _ = os.Getwd()
		}
		cfg, _ := config.Load(root)
		if suggest {
			fmt.Fprintln(a.out, watch.SuggestConfigPrompt(root))
		} else {
			fmt.Fprintln(a.out, watch.GeneratePreamble(watch.Resolve(cfg)))
		}
		return nil
	}

	var transcript, workspacePath, label, agentStatus string

	if transcriptFlag != "" {
		// Simulation mode: replay a specific transcript directly, no
		// workspace resolution required.
		transcript = transcriptFlag
		label = transcriptFlag
	} else {
		entries, err := a.svc.ListAll()
		if err != nil {
			return fmt.Errorf("list workspaces: %w", err)
		}
		if len(entries) == 0 {
			return fmt.Errorf("no workspaces to watch")
		}
		entry, err := a.resolveWatchTarget(positional, entries)
		if err != nil {
			return err
		}
		workspacePath = entry.Path
		label = entry.ProjectName + "/" + entry.Name
		agentStatus = entry.Status
		repoRoot = entry.RepoRoot
	}

	cfg, _ := config.Load(repoRoot)
	loop := watch.Resolve(cfg)
	configured := watch.IsConfigured(cfg)
	if !configured {
		// No dev_loop → don't watch with a guessed default loop; point the
		// user at the setup prompt instead.
		fmt.Fprintln(a.out, unconfiguredHint)
		return nil
	}

	if once {
		// One-shot: the transcript must already exist.
		if transcript == "" {
			located, err := watch.Locate(workspacePath)
			if err != nil {
				return err
			}
			transcript = located
		}
		st, err := watch.BuildState(loop, transcript, agentStatus, time.Now())
		if err != nil {
			return err
		}
		fmt.Fprintln(a.out, watch.Render(loop, label, st))
		return nil
	}

	// Live: the transcript may not exist yet (the agent hasn't started its
	// session). The model re-locates on each tick until it appears.
	m := watchModel{
		loop:          loop,
		transcript:    transcript,
		workspacePath: workspacePath,
		workspace:     label,
		agentStatus:   agentStatus,
		configured:    configured,
	}
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

// resolveWatchTarget picks the workspace to watch: from the argument when
// given (matched by name or project/name), otherwise via the picker.
func (a *App) resolveWatchTarget(args []string, entries []workspace.CrossRepoEntry) (workspace.CrossRepoEntry, error) {
	byLabel := map[string]workspace.CrossRepoEntry{}
	labels := make([]string, 0, len(entries))
	for _, e := range entries {
		label := fmt.Sprintf("%s/%s", e.ProjectName, e.Name)
		byLabel[label] = e
		status := e.Status
		if status == "" {
			status = "idle"
		}
		labels = append(labels, fmt.Sprintf("%-40s %s", label, status))
	}
	sort.Strings(labels)

	if len(args) > 0 {
		want := strings.TrimSpace(args[0])
		for _, e := range entries {
			if e.Name == want || fmt.Sprintf("%s/%s", e.ProjectName, e.Name) == want {
				return e, nil
			}
		}
		return workspace.CrossRepoEntry{}, fmt.Errorf("no workspace matching %q", want)
	}

	// No positional: fall back to the workspace named by the session env
	// (AWP_WORKSPACE), so `awp watch` inside a workspace session picks it up
	// without a picker.
	if wsName, _, _ := resolveWorkspaceIdent(); wsName != "" {
		for _, e := range entries {
			if e.Name == wsName {
				return e, nil
			}
		}
	}

	choice, err := a.picker("Watch which workspace?", labels)
	if err != nil {
		return workspace.CrossRepoEntry{}, err
	}
	// The picker returns the padded label; recover the entry by its prefix.
	label := strings.TrimSpace(strings.SplitN(choice, "  ", 2)[0])
	if e, ok := byLabel[label]; ok {
		return e, nil
	}
	return workspace.CrossRepoEntry{}, fmt.Errorf("could not resolve selection %q", choice)
}

// --- Bubble Tea model -------------------------------------------------------

const unconfiguredHint = "⚠ no dev_loop configured for this repo — gates are a generic guess. Run `awp watch --suggest` for a setup prompt."

type watchModel struct {
	loop          watch.Loop
	transcript    string
	workspacePath string
	workspace     string
	agentStatus   string
	configured    bool
	state         watch.State
	haveState     bool
	err           error
	width         int
}

type watchTickMsg time.Time
type watchStateMsg struct {
	transcript string
	st         watch.State
}
type watchWaitingMsg struct{}
type watchErrMsg struct{ err error }

func (m watchModel) Init() tea.Cmd { return tea.Batch(m.refresh, watchTick()) }

func (m watchModel) refresh() tea.Msg {
	transcript := m.transcript
	// For a workspace target, re-locate every tick so we always follow the
	// newest session file — the agent may not have started yet, or may start
	// a fresh session mid-watch. (A fixed --transcript has no workspacePath.)
	if m.workspacePath != "" {
		if located, err := watch.LocateSticky(m.workspacePath, m.transcript, time.Now()); err == nil {
			transcript = located
		}
	}
	if transcript == "" {
		return watchWaitingMsg{}
	}
	st, err := watch.BuildState(m.loop, transcript, m.agentStatus, time.Now())
	if err != nil {
		return watchErrMsg{err}
	}
	return watchStateMsg{transcript: transcript, st: st}
}

func watchTick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return watchTickMsg(t) })
}

func (m watchModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case watchTickMsg:
		return m, tea.Batch(m.refresh, watchTick())
	case watchStateMsg:
		m.transcript = msg.transcript
		m.state = msg.st
		m.haveState = true
		m.err = nil
	case watchWaitingMsg:
		m.err = nil
	case watchErrMsg:
		m.err = msg.err
	}
	return m, nil
}

func (m watchModel) View() string {
	if m.err != nil {
		return fmt.Sprintf("watch error: %v\n\nq to quit\n", m.err)
	}
	if !m.haveState {
		return fmt.Sprintf("awp watch · %s\n\n  waiting for the agent to start its session…\n\n  q quit\n", m.workspace)
	}
	body := watch.Render(m.loop, m.workspace, m.state)
	footer := "  q quit · repaints every 1s"
	if !m.configured {
		footer = "  " + unconfiguredHint + "\n" + footer
	}
	return body + "\n" + footer + "\n"
}
