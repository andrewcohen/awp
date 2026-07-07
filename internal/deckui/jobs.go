package deckui

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/andrewcohen/awp/internal/charm"
)

// AsyncJobSpec describes the work the deck wants to dispatch to a
// detached subprocess. The deck UI builds this; the wiring layer
// (internal/cli/deck.go) translates it into an internal/jobs.Spec
// and calls the jobs store. Keeping deckui free of that dependency
// preserves the package boundary.
type AsyncJobSpec struct {
	Action           string // "create-workspace", "review", "ci", "custom"
	RepoRoot         string
	Title            string
	Name             string
	Bookmark         string // the revision to anchor the new workspace on
	BookmarkToCreate string // the new jj bookmark to create on @ (blank = skip)
	Prompt           string
	PRNumber         int // pin the created workspace to this PR (0 = none)
	Arg              string
	WorkspaceName    string
	WorkspacePath    string
}

// AsyncJobLauncher dispatches an async job. Returns immediately after
// the subprocess is spawned; long-running work happens out-of-band.
type AsyncJobLauncher func(AsyncJobSpec) error

// JobStatus mirrors the lifecycle states from internal/jobs without
// importing that package. Strings match the on-disk JSON to make
// translation trivial.
type JobStatus string

const (
	JobPending   JobStatus = "pending"
	JobRunning   JobStatus = "running"
	JobDone      JobStatus = "done"
	JobError     JobStatus = "error"
	JobCancelled JobStatus = "cancelled"
	JobOrphaned  JobStatus = "orphaned"
)

// IsTerminal reports whether the status is final.
func (s JobStatus) IsTerminal() bool {
	switch s {
	case JobDone, JobError, JobCancelled, JobOrphaned:
		return true
	}
	return false
}

// JobStep is one named phase of an async job, mirrored from the
// subprocess's reporter output.
type JobStep struct {
	Label string
	Done  bool
	Error bool
}

// Job is the deckui-side projection of an internal/jobs.Job. The
// wiring layer fills these in on every refresh tick.
type Job struct {
	ID            string
	Title         string
	Action        string
	Status        JobStatus
	StartedAt     time.Time
	EndedAt       time.Time
	Steps         []JobStep
	LogsTail      []string
	ErrMsg        string
	// ErrorKind tags failures the overlay can offer typed recovery for.
	// Empty for generic failures. Currently the only kind is
	// "stale_workspace" (mirrors jobs.ErrorKindStaleWorkspace) — when
	// set, the overlay surfaces a `D` key to delete the workspace
	// named by ErrorWorkspace and re-spawn the job in one step.
	ErrorKind string
	// ErrorWorkspace is the workspace the failure attached to —
	// populated from the typed error rather than the spec. For review
	// jobs this is the `pr-N-<branch>` workspace; for create/delete
	// jobs it matches Spec.WorkspaceName. The deck's `D` action
	// targets THIS, not Spec.WorkspaceName, so we don't accidentally
	// nuke `default` when the user was sitting on that row.
	ErrorWorkspace string
	LogPath        string
	PID           int
	WorkspaceName string
	WorkspacePath string
	RepoRoot      string
}

// JobsListRefresher returns the current set of async jobs, ordered
// newest-first. Called on every refresh tick.
type JobsListRefresher func() []Job

// JobCancelHandler is invoked when the user presses `c` on a running
// job in the overlay. The wiring layer signals SIGTERM via the jobs
// store. Returns an error if the cancel couldn't be issued.
type JobCancelHandler func(jobID string) error

// JobDismissHandler is invoked when the user presses `x` on a
// finished/failed/orphaned job in the overlay. The wiring layer
// deletes the job record + log file.
type JobDismissHandler func(jobID string) error

// JobLogOpener returns a tea.Cmd that suspends the deck and opens
// the job's sidecar log file in $PAGER.
type JobLogOpener func(jobID string) tea.Cmd

// JobRetryHandler is invoked when the user presses `r` on a
// terminal-but-not-successful job in the overlay. The wiring layer
// re-spawns a new job from the original Spec. Returns an error if
// the retry couldn't be dispatched (e.g. unknown id, store error).
type JobRetryHandler func(jobID string) error

// JobDeleteWorkspaceRetryHandler is invoked when the user presses `D`
// on a job that failed with ErrorKind "stale_workspace". The wiring
// layer deletes the workspace referenced by the spec, then re-spawns
// a fresh job from the original Spec. One-shot recovery for the
// "review attached to a workspace that's in a state we can't reconcile"
// case — the underlying workspace gets nuked, so callers must only
// pass this through when the user explicitly opts in.
type JobDeleteWorkspaceRetryHandler func(jobID string) error

// hasActiveJobs reports whether any cached job is non-terminal. Used to
// gate the periodic jobs refresh so terminal records don't keep
// triggering disk reads.
func hasActiveJobs(jobs []Job) bool {
	for _, j := range jobs {
		if !j.Status.IsTerminal() {
			return true
		}
	}
	return false
}

// workspaceJobJustFinished reports whether a workspace-producing job
// (create-workspace or review — both write a new workspace into
// workspace-state.json) transitioned to done between prev and cur. A
// job counts as "just finished" if it is done now but was either
// running/pending in prev or absent from prev entirely (prev can be nil
// on the first jobs poll). Callers use this to fire an immediate row
// refresh so the new workspace appears without waiting for the periodic
// poll. Only "done" qualifies — error/cancelled/orphaned jobs leave no
// new workspace to surface.
func workspaceJobJustFinished(prev, cur []Job) bool {
	prevStatus := make(map[string]JobStatus, len(prev))
	for _, j := range prev {
		prevStatus[j.ID] = j.Status
	}
	for _, j := range cur {
		if j.Action != "create-workspace" && j.Action != "review" {
			continue
		}
		if j.Status != JobDone {
			continue
		}
		if was, ok := prevStatus[j.ID]; !ok || was != JobDone {
			return true
		}
	}
	return false
}

type jobsListMsg struct{ jobs []Job }

// JobActionDoneMsg signals the result of a c/x action initiated from
// the overlay so the deck can refresh itself.
type JobActionDoneMsg struct {
	JobID string
	Err   error
	Kind  string // "cancel" | "dismiss" | "retry"
}

func refreshJobsListCmd(r JobsListRefresher) tea.Cmd {
	if r == nil {
		return nil
	}
	return func() tea.Msg {
		return jobsListMsg{jobs: r()}
	}
}

// composeStatusBar lays out a single bottom line:
//
//	activities   right segment   hint
//
// Activities are the unified surface for both in-flight background
// work (pr-status, enrich, workspace rename/link) and async jobs
// (workspace create/delete via the jobs subsystem) — callers project
// jobs into activities via Model.syncJobActivities before rendering.
//
// hint is the right-edge text (typically "? help" in row mode). Pass
// an empty string to omit it — modal/picker screens drop the hint
// because the deck help overlay describes row-mode bindings that
// don't apply there.
//
// Width-aware: drop order under width pressure is hint → activities
// → right segment, so the filter / find-mode input on the right
// always stays visible.
func composeStatusBar(activities []Activity, spinnerGlyph, right, hint string, width int) string {
	left := renderActivitiesCompact(activities, spinnerGlyph)
	var hintRendered string
	if hint != "" {
		hintRendered = lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)).Render(hint)
	}
	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)
	hintW := lipgloss.Width(hintRendered)
	gap := 3
	gapCount := 1
	if hintRendered != "" {
		gapCount++
	}
	used := leftW + rightW + hintW + gapCount*gap
	if width <= 0 || used <= width {
		fill := width - used
		if fill < 0 {
			fill = 0
		}
		segs := []string{}
		if left != "" {
			segs = append(segs, left)
		}
		if right != "" {
			segs = append(segs, right)
		}
		if hintRendered != "" {
			segs = append(segs, strings.Repeat(" ", fill)+hintRendered)
		} else if fill > 0 {
			segs = append(segs, strings.Repeat(" ", fill))
		}
		return strings.Join(segs, strings.Repeat(" ", gap))
	}
	// Tight: drop the hint first, then activities.
	if leftW+rightW+gap <= width {
		return left + strings.Repeat(" ", gap) + right
	}
	return right
}

// jobItem wraps a Job for list.Model. FilterValue feeds the built-in
// fuzzy filter; jobItemDelegate owns row rendering so the
// glyph + selection-bar layout matches the previous hand-rolled view.
type jobItem struct{ job Job }

func (j jobItem) FilterValue() string {
	title := j.job.Title
	if title == "" {
		title = j.job.ID
	}
	return title + " " + j.job.Action + " " + string(j.job.Status)
}

type jobItemDelegate struct{}

func (jobItemDelegate) Height() int                                { return 1 }
func (jobItemDelegate) Spacing() int                               { return 0 }
func (jobItemDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd    { return nil }
func (jobItemDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	item, ok := listItem.(jobItem)
	if !ok {
		return
	}
	j := item.job
	selected := index == m.Index()
	width := m.Width()

	rowStyle := lipgloss.NewStyle().Width(width)
	glyph, color := jobStatusGlyph(j.Status)
	glyphStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(true)
	barStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colWarning)).Bold(true)

	title := j.Title
	if title == "" {
		title = j.ID
	}
	prefix := "  "
	if selected {
		prefix = barStyle.Render("┃") + " "
	}
	row := fmt.Sprintf("%s%s %s", prefix, glyphStyle.Render(glyph), title)
	if selected {
		row = lipgloss.NewStyle().Width(width).Foreground(lipgloss.Color(colWarning)).Bold(true).Render(row)
	} else {
		row = rowStyle.Render(row)
	}
	fmt.Fprint(w, row)
}

// jobsGotoTopKey / jobsGotoBottomKey are explicit g/G bindings layered
// on top of list.Model's built-in nav so the existing overlay shortcuts
// survive the migration.
var (
	jobsGotoTopKey    = key.NewBinding(key.WithKeys("g"), key.WithHelp("g", "top"))
	jobsGotoBottomKey = key.NewBinding(key.WithKeys("G"), key.WithHelp("G", "bottom"))

	// Action bindings surfaced in the overlay footer via the same
	// help.Model as the bookmark/review/open pickers — so the colors
	// (accent keys + muted descriptions + muted separators) match.
	jobsCloseKey   = key.NewBinding(key.WithKeys("esc", "J"), key.WithHelp("esc/J", "close"))
	jobsCancelKey  = key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "cancel"))
	jobsRetryKey   = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "retry"))
	jobsDismissKey = key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "dismiss"))
	jobsOpenLogKey = key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open log"))
	jobsYankKey    = key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "yank"))
	jobsScrollKey  = key.NewBinding(key.WithKeys("pgup", "pgdown"), key.WithHelp("pgup/pgdn", "scroll"))
	jobsDeleteKey  = key.NewBinding(key.WithKeys("D"), key.WithHelp("D", "delete workspace + retry"))
)

// jobsShortHelp returns the binding list rendered in the overlay's
// footer help line. Mirrors pickerShortHelp's role for the picker
// modes — same shape (a slice of key.Binding fed to
// help.Model.ShortHelpView) so the rendered colors are identical.
func jobsShortHelp(l *list.Model) []key.Binding {
	bindings := []key.Binding{
		jobsCloseKey,
		jobsCancelKey,
		jobsRetryKey,
		jobsDismissKey,
		jobsOpenLogKey,
		jobsYankKey,
		jobsScrollKey,
	}
	// Surface the typed-recovery affordance only when the selected job
	// actually qualifies — same gating as the previous title-string did.
	if it, ok := l.SelectedItem().(jobItem); ok && it.job.ErrorKind == "stale_workspace" {
		bindings = append(bindings, jobsDeleteKey)
	}
	return bindings
}

func newJobsList() list.Model {
	l := list.New(nil, jobItemDelegate{}, 0, 0)
	l.SetShowTitle(false)
	l.SetStatusBarItemName("job", "jobs")
	l.SetShowHelp(false)
	l.DisableQuitKeybindings()
	charm.ApplyListTheme(&l, nil)
	return l
}

func newJobsViewport() viewport.Model {
	v := viewport.New(0, 0)
	v.KeyMap = viewport.KeyMap{
		PageDown: key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgdn", "scroll log")),
		PageUp:   key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "scroll log")),
		HalfPageDown: key.NewBinding(
			key.WithKeys("ctrl+d"), key.WithHelp("ctrl+d", "½ pg dn"),
		),
		HalfPageUp: key.NewBinding(
			key.WithKeys("ctrl+u"), key.WithHelp("ctrl+u", "½ pg up"),
		),
	}
	return v
}

// renderJobsOverlay renders the centered popover containing the jobs
// list (left) and the selected-job details viewport (right). Both
// bubbles are owned by the caller (Model); this function just lays out
// the chrome around them.
func renderJobsOverlay(width, height int, l *list.Model, v *viewport.Model, empty bool) string {
	const boxOverhead = 6
	boxWidth := width - 4
	if boxWidth < 44 {
		boxWidth = 44
	}
	innerWidth := boxWidth - boxOverhead
	if innerWidth < 38 {
		innerWidth = 38
	}
	// Box chrome eats 6 rows (border + padding); title + blank below it
	// is 2; blank + help footer below the body is 2.
	bodyHeight := height - 6 - 2 - 2
	if bodyHeight < 4 {
		bodyHeight = 4
	}

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colAccent)).Width(innerWidth)
	title := titleStyle.Render("awp deck — jobs")

	// Help footer goes through the same themed help.Model the picker
	// modes use (charm.ApplyListTheme wires it onto l.Help). That gets
	// us accent-colored keys + muted descriptions + muted separators —
	// matching the bookmark/review/open footer styling exactly.
	help := lipgloss.NewStyle().Width(innerWidth).Render(l.Help.ShortHelpView(jobsShortHelp(l)))

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(colMuted)).
		Padding(1, 2).
		Width(boxWidth)

	if empty {
		emptyMsg := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)).Width(innerWidth).
			Render("No jobs in flight. Press n to create a workspace.")
		body := lipgloss.JoinVertical(lipgloss.Left, title, "", emptyMsg, "", help)
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, boxStyle.Render(body))
	}

	const gutter = 2
	listWidth := (innerWidth - gutter) / 2
	detailsWidth := innerWidth - gutter - listWidth
	l.SetSize(listWidth, bodyHeight)
	v.Width = detailsWidth
	v.Height = bodyHeight

	body := lipgloss.JoinHorizontal(lipgloss.Top, l.View(), "  ", v.View())
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center,
		boxStyle.Render(lipgloss.JoinVertical(lipgloss.Left, title, "", body, "", help)))
}

func renderJobDetails(j Job, width int) string {
	glyph, color := jobStatusGlyph(j.Status)
	glyphStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(true)
	headerStyle := lipgloss.NewStyle().Bold(true).Width(width)
	mutedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)).Width(width)
	logStyle := lipgloss.NewStyle().Width(width)
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colDanger)).Width(width)

	lines := []string{
		headerStyle.Render(fmt.Sprintf("%s  %s", glyphStyle.Render(glyph), j.Title)),
		mutedStyle.Render(fmt.Sprintf("id %s · status %s · pid %d", j.ID, j.Status, j.PID)),
	}
	if !j.StartedAt.IsZero() {
		lines = append(lines, mutedStyle.Render("started "+j.StartedAt.Format("15:04:05")))
	}
	if !j.EndedAt.IsZero() {
		lines = append(lines, mutedStyle.Render("ended   "+j.EndedAt.Format("15:04:05")))
	}
	if j.LogPath != "" {
		lines = append(lines, mutedStyle.Render("log     "+j.LogPath))
	}
	if j.ErrMsg != "" {
		lines = append(lines, "", errStyle.Render("error: "+j.ErrMsg))
	}
	if len(j.Steps) > 0 {
		lines = append(lines, "", headerStyle.Render("Steps"))
		for _, st := range j.Steps {
			marker := "•"
			switch {
			case st.Error:
				marker = "✗"
			case st.Done:
				marker = "✓"
			}
			lines = append(lines, mutedStyle.Render(fmt.Sprintf("  %s %s", marker, st.Label)))
		}
	}
	if len(j.LogsTail) > 0 {
		lines = append(lines, "", headerStyle.Render("Recent log"))
		// Keep the tail bounded — too much scrolls the popover.
		const maxTail = 12
		tail := j.LogsTail
		if len(tail) > maxTail {
			tail = tail[len(tail)-maxTail:]
		}
		for _, ln := range tail {
			lines = append(lines, logStyle.Render("  "+strings.TrimRight(ln, "\n")))
		}
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// jobDetailsForCopy renders a plain-text version of a job's details
// suitable for the system clipboard. Mirrors what the user sees in
// renderJobDetails minus the lipgloss styling so paste targets get
// readable text. Recent log lines are included verbatim — the inline
// buffer is capped at MaxInlineLogLines (200) upstream so this stays
// bounded.
func jobDetailsForCopy(j Job) string {
	var b strings.Builder
	if j.Title != "" {
		fmt.Fprintf(&b, "%s\n", j.Title)
	}
	fmt.Fprintf(&b, "id %s · status %s · pid %d\n", j.ID, j.Status, j.PID)
	if !j.StartedAt.IsZero() {
		fmt.Fprintf(&b, "started %s\n", j.StartedAt.Format("2006-01-02 15:04:05"))
	}
	if !j.EndedAt.IsZero() {
		fmt.Fprintf(&b, "ended   %s\n", j.EndedAt.Format("2006-01-02 15:04:05"))
	}
	if j.LogPath != "" {
		fmt.Fprintf(&b, "log     %s\n", j.LogPath)
	}
	if j.ErrMsg != "" {
		fmt.Fprintf(&b, "\nerror:\n%s\n", j.ErrMsg)
	}
	if len(j.Steps) > 0 {
		b.WriteString("\nSteps:\n")
		for _, st := range j.Steps {
			marker := "•"
			switch {
			case st.Error:
				marker = "✗"
			case st.Done:
				marker = "✓"
			}
			fmt.Fprintf(&b, "  %s %s\n", marker, st.Label)
		}
	}
	if len(j.LogsTail) > 0 {
		b.WriteString("\nRecent log:\n")
		for _, ln := range j.LogsTail {
			fmt.Fprintf(&b, "  %s\n", strings.TrimRight(ln, "\n"))
		}
	}
	return b.String()
}

// writeSystemClipboard writes text to the system clipboard using the
// most reliable mechanism available. On macOS it shells out to
// `pbcopy`; on Linux it tries `wl-copy` then `xclip -selection
// clipboard`. If no native helper is found (or it fails) it falls back
// to OSC 52 via the terminal.
//
// Native helpers are preferred because OSC 52 inside tmux additionally
// requires both `allow-passthrough on` and `set-clipboard on`, neither
// of which is the default — so OSC 52 silently no-ops on most setups.
func writeSystemClipboard(text string) error {
	if cmd, ok := clipboardCommand(); ok {
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err == nil {
			return nil
		} else if !nativeMissing(err) {
			return fmt.Errorf("%s: %w", cmd.Path, err)
		}
	}
	return writeOSC52Clipboard(text)
}

// clipboardCommand returns the preferred native clipboard helper for
// the current platform, if one is on PATH. The bool reports whether a
// helper was found.
func clipboardCommand() (*exec.Cmd, bool) {
	switch runtime.GOOS {
	case "darwin":
		if path, err := exec.LookPath("pbcopy"); err == nil {
			return exec.Command(path), true
		}
	case "linux":
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			if path, err := exec.LookPath("wl-copy"); err == nil {
				return exec.Command(path), true
			}
		}
		if path, err := exec.LookPath("xclip"); err == nil {
			return exec.Command(path, "-selection", "clipboard"), true
		}
	}
	return nil, false
}

// nativeMissing reports whether the error from running a native
// helper indicates the binary itself was missing, in which case
// callers should try the next fallback instead of surfacing the error.
func nativeMissing(err error) bool {
	return err != nil && (errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist))
}

// writeOSC52Clipboard writes text to the host terminal's clipboard via
// the OSC 52 escape sequence. Works inside a tmux popup (where copy-
// mode is unavailable) provided tmux has `set -g set-clipboard on`
// and the host terminal supports OSC 52 (iTerm2, Ghostty, Kitty,
// WezTerm, modern xterm, foot, etc. all do).
//
// The sequence is wrapped in tmux's DCS passthrough (`\x1bPtmux;…\x1b\`)
// so tmux forwards it instead of swallowing it. Non-tmux terminals
// just see the inner OSC 52 directly because the DCS wrapping happens
// to be a no-op in many emulators — we detect $TMUX and only emit the
// wrapper when it's set.
func writeOSC52Clipboard(text string) error {
	enc := base64.StdEncoding.EncodeToString([]byte(text))
	var seq string
	if os.Getenv("TMUX") != "" {
		// Inside tmux: DCS passthrough. Inner ESCs must be doubled.
		inner := fmt.Sprintf("\x1b]52;c;%s\x07", enc)
		inner = strings.ReplaceAll(inner, "\x1b", "\x1b\x1b")
		seq = "\x1bPtmux;" + inner + "\x1b\\"
	} else {
		seq = fmt.Sprintf("\x1b]52;c;%s\x1b\\", enc)
	}
	// Write to /dev/tty so the escape reaches the terminal even when
	// stdout is captured/piped.
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("open tty: %w", err)
	}
	defer tty.Close()
	if _, err := tty.WriteString(seq); err != nil {
		return fmt.Errorf("write tty: %w", err)
	}
	return nil
}

// jobStatusGlyph returns the glyph and lipgloss color for an async
// job status. Kept here (not in the existing statusGlyph helper) so
// the deck row colors and the tray colors can diverge if needed.
func jobStatusGlyph(s JobStatus) (string, string) {
	switch s {
	case JobRunning, JobPending:
		return "▶", colInfo
	case JobDone:
		return "✓", colSuccess
	case JobError:
		return "⚠", colDanger
	case JobCancelled:
		return "⊘", colMuted
	case JobOrphaned:
		return "☠", colWarning
	}
	return "·", colMuted
}

