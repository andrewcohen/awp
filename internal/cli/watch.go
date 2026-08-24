package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/andrewcohen/awp/internal/config"
	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/watch"
	"github.com/andrewcohen/awp/internal/workspace"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// codingAgentInvocation returns the agent launch command for a coding
// workspace. For a Claude agent it appends `--append-system-prompt-file` with
// this repo's preamble — what its workspace is and how to title it, plus the
// dev loop where one is configured (see agentPreamble) — in the system prompt,
// which persists across the session and works even with no task prompt, rather
// than as a one-shot prompt prefix.
func codingAgentInvocation(repoRoot string) string {
	return agentInvocation(repoRoot, agentPreambleFile)
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
	return agentArgv(repoRoot, agentPreambleFile)
}

// reviewAgentInvocation and reviewAgentArgv launch an agent whose job is to read
// someone else's change. Same two forms, same reason; only the preamble differs.
//
// A reviewer gets the workspace section and not the loop one. Its row has a title
// like any other and is the better for being titled — a review workspace called
// `pr-2320-jordan-survey-s-a5f9` says nothing a person wants to read. What it must
// not be told is to work in units, run gates and commit: a reviewer given that
// starts doing the author's job on their PR.
//
// The two flavors used to differ by having a preamble at all, which is why the
// reviewer had none. Splitting the text is what lets the distinction be about
// content rather than about presence.
func reviewAgentInvocation(repoRoot string) string {
	return agentInvocation(repoRoot, reviewPreambleFile)
}

func reviewAgentArgv(repoRoot string) []string {
	return agentArgv(repoRoot, reviewPreambleFile)
}

// agentInvocation and agentArgv are the two renderings of "start Claude here with
// this preamble", so which preamble a flavor gets is the one thing its own
// function says.
func agentInvocation(repoRoot string, preamble func(string) (string, bool)) string {
	inv := config.AgentInvocation(repoRoot)
	path, ok := preamble(repoRoot)
	if !ok {
		return inv
	}
	return inv + " " + appendPreambleFlag + " " + shellSingleQuote(path)
}

func agentArgv(repoRoot string, preamble func(string) (string, bool)) []string {
	argv := fields(config.AgentInvocation(repoRoot))
	if path, ok := preamble(repoRoot); ok {
		argv = append(argv, appendPreambleFlag, path)
	}
	return argv
}

// appendPreambleFlag is how Claude is told to read the agent preamble.
const appendPreambleFlag = "--append-system-prompt-file"

// agentPreambleFile writes this repo's coding preamble and returns its path;
// reviewPreambleFile writes the reviewer's, which is the workspace section alone.
//
// ok is false for an agent that is not Claude, for an empty preamble, or when the
// file could not be written — in which case the agent starts without one rather
// than not at all.
//
// Neither is gated on a dev_loop. The preamble is two sections now (see
// agentPreamble), and the workspace one holds for every repo awp has: a project
// with no loop configured used to get no preamble at all, so its agents were
// never told the one thing every awp agent can do.
//
// The preamble goes by file path rather than inline because it is multi-line:
// Claude reads the file directly, so the launch command stays short and there
// is no shell-quoting of the content, which inline embedding garbles.
func agentPreambleFile(repoRoot string) (string, bool) {
	return preambleFile(repoRoot, agentPreamble(repoRoot), "")
}

func reviewPreambleFile(repoRoot string) (string, bool) {
	return preambleFile(repoRoot, workspacePreamble(), "review")
}

// preambleFile is the shared half: refuse for an agent that cannot read one, then
// write it where that flavor's file lives.
//
// A flavor of its own rather than one file rewritten per launch, because Claude
// reads the path at startup rather than when we hand it over: a coding agent
// starting a moment after a reviewer would replace the file under it, and the
// reviewer would come up told to work in units and commit — the one thing this
// distinction exists to prevent, arriving as a race.
func preambleFile(repoRoot, text, flavor string) (string, bool) {
	cfg, _ := config.Load(repoRoot)
	agent := strings.TrimSpace(cfg.Agent)
	if agent == "" {
		agent = config.DefaultAgent
	}
	if !strings.Contains(agent, "claude") {
		return "", false // --append-system-prompt is Claude-specific
	}
	if strings.TrimSpace(text) == "" {
		return "", false
	}
	path, err := writeAgentPreamble(repoRoot, text, flavor)
	if err != nil {
		return "", false
	}
	return path, true
}

// writeAgentPreamble persists the generated preamble to a stable per-repo
// path under ~/.awp so the agent launch command can `cat` it instead of
// carrying the whole text inline.
func writeAgentPreamble(repoRoot, preamble, flavor string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	name := strings.ReplaceAll(strings.Trim(repoRoot, "/"), "/", "-")
	if name == "" {
		name = "default"
	}
	if flavor != "" {
		name += "." + flavor
	}
	dir := filepath.Join(home, ".awp", "agent-preamble")
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
  awp watch --preamble         Print what an agent started here is told (its system-prompt append)
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
		if suggest {
			fmt.Fprintln(a.out, watch.SuggestConfigPrompt(root))
		} else {
			// What an agent started here is actually told, dev loop and all, rather
			// than the loop section alone — the question this answers is "what is in
			// my agent's system prompt", and half of it is not an answer.
			fmt.Fprintln(a.out, agentPreamble(root))
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
		case deckui.PaneLeaveKey:
			// The watch view is what `W` runs, so under a deck that hands the
			// terminal over it is the program holding the pane — and the deck,
			// suspended, is not there to intercept its own leave key. Quitting
			// here is what makes the pane come back, and it is only spellable
			// in programs awp wrote: a third-party one in raw mode swallows the
			// key with nobody in front of it to notice.
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
