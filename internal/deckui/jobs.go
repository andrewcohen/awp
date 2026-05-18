package deckui

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// AsyncJobSpec describes the work the deck wants to dispatch to a
// detached subprocess. The deck UI builds this; the wiring layer
// (internal/cli/deck.go) translates it into an internal/jobs.Spec
// and calls the jobs store. Keeping deckui free of that dependency
// preserves the package boundary.
type AsyncJobSpec struct {
	Action        string // "create-workspace", "review", "ci", "custom"
	RepoRoot      string
	Title         string
	Name          string
	Bookmark      string
	Prompt        string
	Arg           string
	WorkspaceName string
	WorkspacePath string
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
	LogPath       string
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
//	activities   right segment   ? help
//
// Activities are the unified surface for both in-flight background
// work (pr-status, enrich, workspace rename/link) and async jobs
// (workspace create/delete via the jobs subsystem) — callers project
// jobs into activities via Model.syncJobActivities before rendering.
//
// Width-aware: drop order under width pressure is hint → activities
// → right segment, so the filter / find-mode input on the right
// always stays visible.
func composeStatusBar(activities []Activity, spinnerGlyph, right string, width int) string {
	left := renderActivitiesCompact(activities, spinnerGlyph)
	hint := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)).Render("? help")
	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)
	hintW := lipgloss.Width(hint)
	gap := 3
	used := leftW + rightW + hintW + 2*gap
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
		segs = append(segs, strings.Repeat(" ", fill)+hint)
		return strings.Join(segs, strings.Repeat(" ", gap))
	}
	// Tight: drop the hint first, then activities.
	if leftW+rightW+gap <= width {
		return left + strings.Repeat(" ", gap) + right
	}
	return right
}

// renderJobsOverlay renders the full-screen jobs overlay at the
// supplied dimensions. Uses lipgloss styles, no ad-hoc spacing per
// CLAUDE.md. Returns a centered popover string; caller is
// responsible for placing it within the viewport.
func renderJobsOverlay(jobs []Job, cursor, width, height int) string {
	// Box overhead: 2 cols of border + 2*2 cols of horizontal padding.
	const boxOverhead = 6
	boxWidth := width - 4 // leave a small margin so the border never clips the viewport edge
	if boxWidth < 44 {
		boxWidth = 44
	}
	innerWidth := boxWidth - boxOverhead
	if innerWidth < 38 {
		innerWidth = 38
	}

	// Vertical overhead: 2 rows of border + 2 rows of padding (Padding(1,2)
	// applies 1 row top + 1 row bottom) + 1 row title + 1 blank line below
	// the title = 6 rows. Reserve a 1-row bottom margin so a tall list
	// never crowds against the viewport edge.
	bodyHeight := height - 6 - 1
	if bodyHeight < 4 {
		bodyHeight = 4
	}

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colAccent)).Width(innerWidth)
	title := titleStyle.Render("awp deck — jobs (esc/J close · c cancel · r retry · x dismiss · o open log · y yank)")

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(colMuted)).
		Padding(1, 2).
		Width(boxWidth)

	if len(jobs) == 0 {
		empty := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)).Width(innerWidth).
			Render("No jobs in flight. Press n to create a workspace.")
		body := lipgloss.JoinVertical(lipgloss.Left, title, "", empty)
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, boxStyle.Render(body))
	}

	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(jobs) {
		cursor = len(jobs) - 1
	}

	// 50/50 split, with a 2-col gutter between the columns.
	const gutter = 2
	listWidth := (innerWidth - gutter) / 2
	detailsWidth := innerWidth - gutter - listWidth

	list := renderJobsList(jobs, cursor, listWidth, bodyHeight)
	details := clampLines(renderJobDetails(jobs[cursor], detailsWidth), bodyHeight)

	body := lipgloss.JoinHorizontal(lipgloss.Top, list, "  ", details)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center,
		boxStyle.Render(lipgloss.JoinVertical(lipgloss.Left, title, "", body)))
}

// clampLines truncates a pre-rendered, newline-separated block to at
// most maxLines lines so a tall column can't push the popover past the
// viewport edge (the deck runs inline — overflow scrolls the host pane).
func clampLines(s string, maxLines int) string {
	if maxLines <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= maxLines {
		return s
	}
	return strings.Join(lines[:maxLines], "\n")
}

func renderJobsList(jobs []Job, cursor, width, maxRows int) string {
	rowStyle := lipgloss.NewStyle().Width(width)
	selStyle := rowStyle.Foreground(lipgloss.Color(colWarning)).Bold(true)
	mutedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted))
	barStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colWarning)).Bold(true)

	// Reserve 2 rows at the bottom for the blank separator + footer.
	rowsArea := maxRows - 2
	if rowsArea < 1 {
		rowsArea = 1
	}

	// Window the rows around the cursor so it stays visible when the
	// list overflows the available height. Without this the popover
	// grows past the viewport and (because the deck renders inline,
	// no alt-screen) scrolls the entire UI off the top of the pane.
	start, end := 0, len(jobs)
	if rowsArea < len(jobs) {
		start = cursor - rowsArea/2
		if start < 0 {
			start = 0
		}
		end = start + rowsArea
		if end > len(jobs) {
			end = len(jobs)
			start = end - rowsArea
		}
	}

	lines := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		j := jobs[i]
		glyph, color := jobStatusGlyph(j.Status)
		glyphStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(true)
		title := j.Title
		if title == "" {
			title = j.ID
		}
		prefix := "  "
		if i == cursor {
			prefix = barStyle.Render("┃") + " "
		}
		row := fmt.Sprintf("%s%s %s", prefix, glyphStyle.Render(glyph), title)
		if i == cursor {
			lines = append(lines, selStyle.Render(row))
		} else {
			lines = append(lines, rowStyle.Render(row))
		}
	}
	footerText := fmt.Sprintf("%d job(s)", len(jobs))
	if start > 0 || end < len(jobs) {
		footerText = fmt.Sprintf("%d–%d / %d", start+1, end, len(jobs))
	}
	footer := mutedStyle.Render(footerText)
	return lipgloss.JoinVertical(lipgloss.Left, append(lines, "", footer)...)
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

