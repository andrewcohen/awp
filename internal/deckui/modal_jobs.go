package deckui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// jobsModal is the `J` jobs overlay: a centered popover with the job list
// (left) and the selected job's details/log viewport (right). It owns both
// bubbles; job actions (cancel/dismiss/retry/…) run against the *Model's
// handlers. Rendered via renderJobsOverlay.
type jobsModal struct {
	list list.Model
	vp   viewport.Model
}

// newJobsModal builds the overlay from the current job list, seeding the
// list items and the details pane.
func newJobsModal(jobs []Job) *jobsModal {
	jm := &jobsModal{list: newJobsList(), vp: newJobsViewport()}
	jm.list.ResetSelected()
	jm.sync(jobs)
	jm.refreshViewport()
	return jm
}

// sync projects jobs into the list items, preserving the cursor on the
// same Job ID when possible.
func (jm *jobsModal) sync(jobs []Job) {
	var selectedID string
	if it, ok := jm.list.SelectedItem().(jobItem); ok {
		selectedID = it.job.ID
	}
	items := make([]list.Item, 0, len(jobs))
	keepIdx := -1
	for i, j := range jobs {
		items = append(items, jobItem{job: j})
		if j.ID == selectedID {
			keepIdx = i
		}
	}
	jm.list.SetItems(items)
	if keepIdx >= 0 {
		jm.list.Select(keepIdx)
	}
}

// refreshViewport re-renders the details pane from the selected job.
func (jm *jobsModal) refreshViewport() {
	width := jm.vp.Width
	if width <= 0 {
		width = 40
	}
	if j, ok := jm.selectedJob(); ok {
		jm.vp.SetContent(renderJobDetails(j, width))
	} else {
		jm.vp.SetContent("")
	}
}

func (jm *jobsModal) selectedJob() (Job, bool) {
	it, ok := jm.list.SelectedItem().(jobItem)
	if !ok {
		return Job{}, false
	}
	return it.job, true
}

func (jm *jobsModal) footerHelp() string { return "" }

func (jm *jobsModal) renderPopover(m *Model) string {
	return renderJobsOverlay(m.width, m.height, &jm.list, &jm.vp, len(m.jobs) == 0)
}

// update handles keypresses while the overlay is active. Selection +
// scroll are owned by the bubbles; this intercepts the close keys and
// job-action shortcuts (c/x/r/o/y/D), then delegates the rest. Actions
// only fire when the list isn't filtering so the letter keys can be typed
// into the filter input. Non-key messages drive the list's async
// machinery (filter matches, blink).
func (jm *jobsModal) update(m *Model, msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		var cmd tea.Cmd
		jm.list, cmd = jm.list.Update(msg)
		return cmd
	}
	filtering := jm.list.FilterState() == list.Filtering
	if !filtering {
		switch key.String() {
		case "esc", "q", "J":
			if jm.list.FilterState() == list.FilterApplied {
				jm.list.ResetFilter()
				return nil
			}
			m.active = nil
			return nil
		case "g":
			jm.list.Select(0)
			jm.refreshViewport()
			return nil
		case "G":
			if n := len(jm.list.Items()); n > 0 {
				jm.list.Select(n - 1)
				jm.refreshViewport()
			}
			return nil
		case "c":
			j, ok := jm.selectedJob()
			if !ok || m.jobCancelHandler == nil {
				return nil
			}
			if j.Status.IsTerminal() {
				m.status = "cancel: job already finished"
				return nil
			}
			handler := m.jobCancelHandler
			id := j.ID
			return func() tea.Msg {
				return JobActionDoneMsg{JobID: id, Kind: "cancel", Err: handler(id)}
			}
		case "x":
			j, ok := jm.selectedJob()
			if !ok || m.jobDismissHandler == nil {
				return nil
			}
			if !j.Status.IsTerminal() {
				m.status = "dismiss: cancel a running job first"
				return nil
			}
			handler := m.jobDismissHandler
			id := j.ID
			return func() tea.Msg {
				return JobActionDoneMsg{JobID: id, Kind: "dismiss", Err: handler(id)}
			}
		case "r":
			j, ok := jm.selectedJob()
			if !ok || m.jobRetryHandler == nil {
				return nil
			}
			if !j.Status.IsTerminal() {
				m.status = "retry: job is still running"
				return nil
			}
			if j.Status == JobDone {
				m.status = "retry: job already succeeded"
				return nil
			}
			handler := m.jobRetryHandler
			id := j.ID
			return func() tea.Msg {
				return JobActionDoneMsg{JobID: id, Kind: "retry", Err: handler(id)}
			}
		case "D":
			// Delete-workspace-and-retry. Only meaningful for jobs whose
			// ErrorKind tags them as recoverable via this affordance.
			j, ok := jm.selectedJob()
			if !ok || m.jobDeleteWorkspaceRetry == nil {
				return nil
			}
			if j.ErrorKind != "stale_workspace" {
				m.status = "D: only applies to jobs failed with a stale workspace"
				return nil
			}
			if strings.TrimSpace(j.ErrorWorkspace) == "" {
				m.status = "D: job has no error workspace recorded"
				return nil
			}
			handler := m.jobDeleteWorkspaceRetry
			id := j.ID
			return func() tea.Msg {
				return JobActionDoneMsg{JobID: id, Kind: "delete-and-retry", Err: handler(id)}
			}
		case "o":
			j, ok := jm.selectedJob()
			if !ok || m.jobLogOpener == nil {
				return nil
			}
			return m.jobLogOpener(j.ID)
		case "y":
			j, ok := jm.selectedJob()
			if !ok {
				return nil
			}
			text := jobDetailsForCopy(j)
			if err := writeSystemClipboard(text); err != nil {
				m.status = "copy: " + err.Error()
			} else {
				m.status = fmt.Sprintf("copied %d bytes to clipboard", len(text))
			}
			return nil
		}
	} else if key.String() == "esc" {
		// While actively filtering, esc cancels the filter (list owns that)
		// — never closes the overlay.
		var cmd tea.Cmd
		jm.list, cmd = jm.list.Update(key)
		jm.refreshViewport()
		return cmd
	}
	// Route pgup/pgdn/ctrl+u/ctrl+d to the details viewport so the user can
	// scroll the log without moving the list selection.
	switch key.String() {
	case "pgup", "pgdown", "ctrl+u", "ctrl+d":
		var cmd tea.Cmd
		jm.vp, cmd = jm.vp.Update(key)
		return cmd
	}
	priorIdx := jm.list.Index()
	var cmd tea.Cmd
	jm.list, cmd = jm.list.Update(key)
	if jm.list.Index() != priorIdx {
		jm.refreshViewport()
	}
	return cmd
}
