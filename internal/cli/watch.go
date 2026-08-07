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

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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
	path, ok := devLoopPreambleFile(repoRoot)
	if !ok {
		return inv
	}
	return inv + " " + appendPreambleFlag + " " + shellSingleQuote(path)
}

// codingAgentArgv is codingAgentInvocation for callers that exec the agent
// directly rather than handing a line to a shell — a hosted pane, say.
//
// The two cannot be one. Going argv-first and joining for the shell callers
// would mean putting agent_options through fields(), which splits on
// whitespace and is deliberately not a shell parse; anyone whose options carry
// shell syntax would have it broken up and re-quoted into nonsense. Going
// string-first and splitting for this one would hand Claude a preamble path
// with literal quote characters in it.
//
// So they render differently because their targets do, and everything they
// could disagree about — whether a preamble applies, where it is, what the
// flag is called — is single-sourced.
func codingAgentArgv(repoRoot string) []string {
	argv := fields(config.AgentInvocation(repoRoot))
	if path, ok := devLoopPreambleFile(repoRoot); ok {
		argv = append(argv, appendPreambleFlag, path)
	}
	return argv
}

// appendPreambleFlag is how Claude is told to read the dev-loop preamble.
const appendPreambleFlag = "--append-system-prompt-file"

// devLoopPreambleFile writes this repo's dev-loop preamble and returns its
// path. ok is false when no preamble applies — no dev_loop configured, an
// agent that is not Claude, an empty preamble — or when it could not be
// written, in which case the agent starts without one rather than not at all.
//
// The preamble goes by file path rather than inline because it is multi-line:
// Claude reads the file directly, so the launch command stays short and there
// is no shell-quoting of the content, which inline embedding garbles.
func devLoopPreambleFile(repoRoot string) (string, bool) {
	cfg, _ := config.Load(repoRoot)
	if !watch.IsConfigured(cfg) {
		return "", false
	}
	agent := strings.TrimSpace(cfg.Agent)
	if agent == "" {
		agent = config.DefaultAgent
	}
	if !strings.Contains(agent, "claude") {
		return "", false // --append-system-prompt is Claude-specific
	}
	preamble := watch.GeneratePreamble(watch.Resolve(cfg))
	if strings.TrimSpace(preamble) == "" {
		return "", false
	}
	path, err := writeDevLoopPreamble(repoRoot, preamble)
	if err != nil {
		return "", false
	}
	return path, true
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
		// lipgloss.Fprintln rather than fmt: lipgloss v2 renders at full
		// fidelity and downsamples at the writer, so `--once` piped to a
		// file or a pager would otherwise carry raw escapes. v1 stripped
		// them inside Render by detecting the profile globally.
		_, _ = lipgloss.Fprintln(a.out, watch.Render(loop, label, st))
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
	_, err := tea.NewProgram(m).Run()
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
	case tea.KeyPressMsg:
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

// View satisfies tea.Model. Bubble Tea v2 asks the view to declare the
// terminal features it wants, so alt-screen is stated here rather than
// as a tea.NewProgram option. The content itself comes from render, which
// stays a plain string so tests and the panel helpers can call it.
func (m watchModel) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

func (m watchModel) render() string {
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
