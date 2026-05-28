package deckui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// activityExpireDelay is how long a finished activity flashes `✓ <label>`
// in the bar before being dropped.
const activityExpireDelay = 500 * time.Millisecond

// Activity is one in-flight background operation surfaced in the bottom
// status bar. ID is the stable key callers use to start/tick/finish;
// Label is what the user sees. Total=0 means "no progress fraction —
// just show the spinner."
//
// Glyph and Color override the default spinner-driven rendering for
// activities representing attention-needing terminal jobs (failed,
// orphaned). When both are zero-valued the renderer falls back to the
// model's spinner (or "✓" once FinishedAt is set).
type Activity struct {
	ID         string
	Label      string
	Done       int
	Total      int
	StartedAt  time.Time
	FinishedAt time.Time
	Glyph      string
	Color      string
}

// activityExpireMsg drops a finished activity from the slice after the
// post-finish flash. Scheduled via tea.Tick from finishActivity.
type activityExpireMsg struct{ id string }

// startActivity adds (or refreshes) an activity by ID. When an entry
// with the same ID is still in-flight (FinishedAt zero), the new
// batch's Total is *accumulated* into the existing one and Done is
// left alone — so a force-refresh that lands mid-fetch grows the
// denominator instead of stomping the in-progress counter. When the
// existing entry has already finished (in the post-finish flash
// window), Done and Total are reset for the new batch. The singleton
// enrich / pr-status pattern still holds — repeat dispatches don't
// pile up new chips — but the counter no longer regresses.
func (m Model) startActivity(id, label string, total int) Model {
	if strings.TrimSpace(id) == "" {
		return m
	}
	now := time.Now()
	for i, a := range m.activities {
		if a.ID == id {
			if a.FinishedAt.IsZero() {
				a.Total += total
				m.activities[i] = a
				return m
			}
			m.activities[i] = Activity{
				ID:        id,
				Label:     label,
				Total:     total,
				StartedAt: now,
			}
			return m
		}
	}
	m.activities = append(m.activities, Activity{
		ID:        id,
		Label:     label,
		Total:     total,
		StartedAt: now,
	})
	return m
}

// tickActivity increments Done on the named activity. A tick for an
// unknown ID is a no-op (the activity may have finished already).
func (m Model) tickActivity(id string, delta int) Model {
	for i, a := range m.activities {
		if a.ID != id {
			continue
		}
		a.Done += delta
		if a.Total > 0 && a.Done > a.Total {
			a.Done = a.Total
		}
		m.activities[i] = a
		return m
	}
	return m
}

// finishActivity marks the named activity finished. The entry stays in
// the slice for activityExpireDelay so the bar can flash `✓ <label>`;
// the caller batches the returned tea.Cmd to drop it. A finish for an
// unknown ID returns a nil cmd.
func (m Model) finishActivity(id string) (Model, tea.Cmd) {
	for i, a := range m.activities {
		if a.ID != id {
			continue
		}
		a.FinishedAt = time.Now()
		m.activities[i] = a
		cmd := tea.Tick(activityExpireDelay, func(time.Time) tea.Msg {
			return activityExpireMsg{id: id}
		})
		return m, cmd
	}
	return m, nil
}

// dropActivity removes the named activity from the slice.
func (m Model) dropActivity(id string) Model {
	for i, a := range m.activities {
		if a.ID != id {
			continue
		}
		m.activities = append(m.activities[:i], m.activities[i+1:]...)
		return m
	}
	return m
}

// hasActivity reports whether an activity with the given ID is currently
// tracked (running or in its post-finish flash).
func (m Model) hasActivity(id string) bool {
	for _, a := range m.activities {
		if a.ID == id {
			return true
		}
	}
	return false
}

// jobActivityIDPrefix marks activities derived from async jobs. The
// prefix lets the jobs sync helper distinguish "job activities" from
// explicit ones (pr-status, enrich, …) when reconciling.
const jobActivityIDPrefix = "job:"

// prStatusJobAction mirrors jobs.ActionPRStatus as a plain string so
// the deckui package doesn't need to import internal/jobs (which would
// create a dependency cycle via internal/cli). The actual constant
// lives in internal/jobs/types.go.
const prStatusJobAction = "pr-status"

// syncJobActivities reconciles m.activities with the current job list:
// running/pending jobs are added (or updated) as activities; terminal
// jobs that need attention (error, orphaned) stay visible with a
// distinctive glyph until dismissed; clean-terminal jobs (done,
// cancelled) get a 500 ms ✓ flash and disappear.
//
// Returns the new model and a (possibly nil) tea.Cmd that schedules the
// expiry ticks for any newly-finished entries.
func (m Model) syncJobActivities(jobs []Job) (Model, tea.Cmd) {
	known := make(map[string]Job, len(jobs))
	for _, j := range jobs {
		// pr-status jobs are surfaced by the legacy `pr-status`
		// activity (with N/M progress); skipping them here keeps the
		// activity bar from showing two entries for the same fetch.
		// They still appear in the J overlay because that reads the
		// jobs list directly.
		if j.Action == string(prStatusJobAction) {
			continue
		}
		known[jobActivityIDPrefix+j.ID] = j
	}

	var cmds []tea.Cmd
	// First pass: update or finish activities that already exist.
	for i, a := range m.activities {
		if !strings.HasPrefix(a.ID, jobActivityIDPrefix) {
			continue
		}
		j, ok := known[a.ID]
		if !ok {
			continue
		}
		switch j.Status {
		case JobDone, JobCancelled:
			if a.FinishedAt.IsZero() {
				a.FinishedAt = time.Now()
				m.activities[i] = a
				id := a.ID
				cmds = append(cmds, tea.Tick(activityExpireDelay, func(time.Time) tea.Msg {
					return activityExpireMsg{id: id}
				}))
			}
		case JobError:
			a.Glyph = "⚠"
			a.Color = colDanger
			a.FinishedAt = time.Time{}
			a.Label = jobActivityLabel(j)
			m.activities[i] = a
		case JobOrphaned:
			a.Glyph = "☠"
			a.Color = colWarning
			a.FinishedAt = time.Time{}
			a.Label = jobActivityLabel(j)
			m.activities[i] = a
		default:
			a.Glyph = ""
			a.Color = ""
			a.FinishedAt = time.Time{}
			a.Label = jobActivityLabel(j)
			m.activities[i] = a
		}
	}

	// Second pass: add activities for jobs we haven't seen yet.
	for id, j := range known {
		if m.hasActivity(id) {
			continue
		}
		a := Activity{
			ID:        id,
			Label:     jobActivityLabel(j),
			StartedAt: j.StartedAt,
		}
		switch j.Status {
		case JobError:
			a.Glyph = "⚠"
			a.Color = colDanger
		case JobOrphaned:
			a.Glyph = "☠"
			a.Color = colWarning
		case JobDone, JobCancelled:
			// Terminal-and-clean job we hadn't tracked: skip entirely
			// rather than flash for something the user never saw start.
			continue
		}
		if a.StartedAt.IsZero() {
			a.StartedAt = time.Now()
		}
		m.activities = append(m.activities, a)
	}

	// Third pass: drop activities whose backing job has been deleted
	// (e.g. dismissed from the J overlay).
	out := m.activities[:0]
	for _, a := range m.activities {
		if strings.HasPrefix(a.ID, jobActivityIDPrefix) {
			if _, ok := known[a.ID]; !ok {
				continue
			}
		}
		out = append(out, a)
	}
	m.activities = out

	if len(cmds) == 0 {
		return m, nil
	}
	return m, tea.Batch(cmds...)
}

// jobActivityLabel returns the short label rendered in the activity
// segment for an async-job-backed activity. Prefers the job's title
// (already "<action> · <workspace>") when set, falling back to the
// raw action name.
func jobActivityLabel(j Job) string {
	if t := strings.TrimSpace(j.Title); t != "" {
		return t
	}
	if a := strings.TrimSpace(j.Action); a != "" {
		return a
	}
	return j.ID
}

// renderActivitiesCompact builds the inline activity segment used in
// the bottom status bar. Empty when no activities are tracked.
// spinnerGlyph is the leading glyph for in-flight activities without
// an explicit Glyph override; finished activities flash `✓` until
// their expiry tick lands.
func renderActivitiesCompact(activities []Activity, spinnerGlyph string) string {
	if len(activities) == 0 {
		return ""
	}
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted))
	doneStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colSuccess)).Bold(true)
	parts := make([]string, 0, len(activities))
	for _, a := range activities {
		var glyph string
		switch {
		case !a.FinishedAt.IsZero():
			glyph = doneStyle.Render("✓")
		case a.Glyph != "":
			color := a.Color
			if color == "" {
				color = colMuted
			}
			glyph = lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(true).Render(a.Glyph)
		default:
			glyph = spinnerGlyph
		}
		body := a.Label
		if a.Total > 0 {
			body = fmt.Sprintf("%s %d/%d", a.Label, a.Done, a.Total)
		}
		parts = append(parts, glyph+" "+dim.Render(body))
	}
	return strings.Join(parts, dim.Render(" · "))
}
