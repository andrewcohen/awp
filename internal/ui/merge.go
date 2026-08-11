package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/andrewcohen/awp/internal/charm"
)

// Ending a review the other way: `M` merges the PR.
//
// The deck has offered this under `p m` from the row list all along, which meant
// deciding to merge while reading the change cost a trip back out to the deck and
// a hunt for the row you came from. The review surface is where the decision
// actually gets made — the same argument that put `P` here.
//
// Two screens, the same two publishing has: the exact call, then what happened.
// A merge is irreversible and outward-facing, so it does not go from a keystroke
// straight to gh, and its outcome is more than one line — a squash that falls
// back to the merge queue says so in what gh printed and nowhere else.

// mergeStage is where the flow has got to.
type mergeStage int

const (
	// mergeConfirming shows the call and waits for the one key that makes it.
	mergeConfirming mergeStage = iota
	// mergeReporting is what happened, which the confirm box becomes.
	mergeReporting
)

// beginMerge opens the confirm screen, asking the host for the plan without
// making any of its calls — the same dry run the publish flow previews.
//
// Reports what it will not do rather than opening a box whose only outcome is an
// error. A nil seam covers both reasons there is nothing to merge: a review with
// no PR, and a host that offers no merging, which from the keyboard are the same
// fact — this key does nothing here.
func (m *Model) beginMerge() tea.Cmd {
	if m.MergePR == nil {
		m.fail("no PR to merge here")
		return nil
	}
	if m.mergeBusy {
		// A second `enter` while gh is still running would issue a second merge.
		m.status = "already merging…"
		return nil
	}
	m.merging = true
	m.mergeStage = mergeConfirming
	m.mergeReport = []string{"reading the PR…"}
	m.mergeScroll = 0
	m.status = ""
	m.statusErr = false
	return mergeCmd(m.MergePR, true)
}

// endMerge closes the prompt, saying nothing — the prompt disappearing is the
// message, the same as cancelling a publish.
func (m *Model) endMerge() {
	m.merging = false
	m.mergeStage = mergeConfirming
	m.mergeReport = nil
	m.status = ""
}

// handleMergeKey drives the two screens.
func (m Model) handleMergeKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.mergeStage {
	case mergeConfirming:
		switch strings.ToLower(key) {
		case "y", "enter":
			if m.mergeBusy {
				return m, nil
			}
			// The one key in this flow that talks to GitHub.
			m.mergeStage = mergeReporting
			m.mergeReport = []string{"merging…"}
			m.mergeBusy = true
			m.status = "merging…"
			m.statusErr = false
			return m, mergeCmd(m.MergePR, false)
		case "n", "esc", "q":
			m.endMerge()
			return m, nil
		}
		return m, m.scrollMergeReport(msg)
	case mergeReporting:
		if m.mergeBusy {
			// Nothing to dismiss yet, and a close here would leave the merge running
			// with nowhere to report to.
			return m, m.scrollMergeReport(msg)
		}
		switch key {
		case "esc", "q", "enter", "M":
			// The footer keeps the one-line outcome, so dismissing does not lose it.
			m.merging = false
			m.mergeStage = mergeConfirming
			m.mergeReport = nil
			return m, nil
		}
		return m, m.scrollMergeReport(msg)
	}
	return m, nil
}

// scrollMergeReport lets a long report be read, the same j/k the publish report
// takes.
func (m *Model) scrollMergeReport(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "j", "down":
		m.mergeScroll++
	case "k", "up":
		m.mergeScroll = max(0, m.mergeScroll-1)
	}
	return nil
}

// mergeDoneMsg carries the outcome back. dry marks the preview — the plan,
// with nothing merged.
type mergeDoneMsg struct {
	report string
	dry    bool
	err    error
}

// mergeCmd runs the merge off the update loop: gh forks a process and waits on
// the network, and doing that inline would stop the view redrawing and take the
// keyboard with it.
func mergeCmd(fn func(dryRun bool) (string, error), dry bool) tea.Cmd {
	return func() tea.Msg {
		report, err := fn(dry)
		return mergeDoneMsg{report: report, dry: dry, err: err}
	}
}

// applyMergeDone reports the outcome.
//
// Nothing local is re-read. A merge changes the PR rather than the review: the
// comments, the threads and the reviewed marks all still say what they said, and
// the next pr-status pass is what notices the PR is closed.
func (m Model) applyMergeDone(msg mergeDoneMsg) (tea.Model, tea.Cmd) {
	m.mergeScroll = 0
	if msg.dry {
		if msg.err != nil {
			// A plan that cannot even be built is the answer — there is nothing left to
			// confirm, so this goes straight to the report.
			m.mergeStage = mergeReporting
			m.mergeReport = publishReportLines("cannot merge: " + msg.err.Error())
			m.fail("merge: %v", msg.err)
			return m, nil
		}
		m.mergeReport = publishReportLines(msg.report)
		return m, nil
	}
	m.mergeBusy = false
	m.mergeStage = mergeReporting
	if msg.err != nil {
		// Kept on screen rather than folded into the footer: gh's refusal is usually
		// several lines and names the condition — not up to date with base, a required
		// check still running — which is the part worth reading.
		m.mergeReport = append(append(publishReportLines(msg.report), ""),
			publishReportLines("failed: "+msg.err.Error())...)
		m.fail("merge: %v", msg.err)
		return m, nil
	}
	m.mergeReport = publishReportLines(msg.report)
	m.status = publishStatusText(msg.report)
	if strings.TrimSpace(m.status) == "" {
		m.status = "merged"
	}
	m.statusErr = false
	return m, nil
}

// renderMergeOverlay draws the prompt in place of the panes, the way the publish
// and help overlays do — so the body keeps the height the host budgeted and the
// footer stays where it was.
func (m Model) renderMergeOverlay(width, height int) string {
	inner := publishOverlayInner(width)
	rows := m.mergeReportRows(inner, height)
	switch {
	case m.mergeStage == mergeReporting && m.mergeBusy:
		rows = append(rows, "", styleDim.Render(truncate(" merging — gh is running", inner)))
	case m.mergeStage == mergeReporting:
		rows = append(rows, "", styleDim.Render(truncate(" j/k scroll · enter close", inner)))
	default:
		rows = append(rows, "", styleSelected.Render(truncate(" y MERGES IT · n / esc cancel", inner)))
	}
	head := []string{lipgloss.NewStyle().Bold(true).Render(truncate("Merge PR", inner)), ""}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(charm.Muted)).
		Padding(0, 2).
		Width(max(1, width)).
		Height(height).
		Render(strings.Join(append(head, rows...), "\n"))
}

// mergeReportRows is the plan or the report, windowed to what fits and scrolled
// by j/k.
func (m Model) mergeReportRows(inner, height int) []string {
	// The chrome this overlay spends on its title, blank rows and key hint.
	const overhead = 5
	room := max(1, height-overhead)
	lines := m.mergeReport
	start := min(max(0, m.mergeScroll), max(0, len(lines)-1))
	end := min(len(lines), start+room)
	rows := make([]string, 0, room+1)
	for _, line := range lines[start:end] {
		rows = append(rows, truncate(line, inner))
	}
	return rows
}
