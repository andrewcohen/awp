package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/andrewcohen/awp/internal/charm"
	"github.com/andrewcohen/awp/internal/github"
	"github.com/andrewcohen/awp/internal/review"
)

// Finishing a review: `P` publishes it to the PR, with a verdict.
//
// Everything up to this point was local. Publishing existed only as
// `awp review publish`, so the loop was read here, comment here, then leave the
// view and find a shell — and the one moment you are most certain about what you
// think of a change is the moment you have just finished reading it.
//
// It asks for the verdict rather than assuming one, because that is the decision
// being made: approve, comment, or request changes are GitHub's own three, and
// which one you pick is the whole point of having read the change. "Post the
// comments only" is the fourth, for putting remarks up mid-review without
// pronouncing on anything.

// publishChoice is one row of the prompt.
type publishChoice struct {
	// label is what the reviewer is choosing to do.
	label string
	// verdict is the word `awp review publish --verdict` takes, empty for posting
	// the comments without a verdict at all.
	verdict string
	// hint says what the choice means on GitHub, since "comment" as a verdict and
	// "comment" as a thing you leave on a line are easy to confuse.
	hint string
}

// publishChoices is the prompt's rows, in the order a reviewer is most likely to
// want them: approving is the common ending, and the two that hold a change up
// come next.
func publishChoices() []publishChoice {
	return []publishChoice{
		{label: "approve", verdict: "approve", hint: "approve the PR"},
		{label: "request changes", verdict: "request-changes", hint: "blocks the merge · needs a review-level remark"},
		{label: "comment", verdict: "comment", hint: "a review with no verdict · needs a review-level remark"},
		{label: "post the comments only", verdict: "", hint: "no review submitted; remarks go up as PR comments"},
	}
}

// publishStage is where the flow has got to. Publishing is irreversible and
// outward-facing, so it does not go straight from a menu choice to posting: you
// choose a verdict, you read exactly which calls that will make, and then you
// confirm. The result then stays on screen instead of being a segment in a footer
// competing with the workspace name and the base.
type publishStage int

const (
	// publishChoosing is the verdict menu.
	publishChoosing publishStage = iota
	// publishSummary is the review body — a remark about the change as a whole,
	// which `request changes` and `comment` require and an approval may want. It
	// lives here because this is where the need arises: the flow used to dead-end on
	// its own requirement, since nothing in the viewer could write one.
	publishSummary
	// publishPreviewing is the list of calls, awaiting confirmation.
	publishPreviewing
	// publishReporting is what happened, awaiting dismissal.
	publishReporting
)

// beginPublish opens the prompt. Reports what it will not do rather than opening
// a prompt whose only outcome is an error.
func (m *Model) beginPublish() {
	if m.PublishReview == nil {
		m.status = "publishing unavailable here"
		m.statusErr = true
		return
	}
	if m.publishBusy {
		// A second submission while the first is in flight would post everything
		// twice: the store is only marked published once GitHub answers.
		m.status = "already publishing…"
		return
	}
	m.publishing = true
	m.publishStage = publishChoosing
	m.publishCursor = 0
	m.publishReport = nil
}

// endPublish closes the prompt, saying nothing — the prompt disappearing is the
// message.
func (m *Model) endPublish() {
	m.publishing = false
	m.publishStage = publishChoosing
	m.publishReport = nil
	m.status = ""
}

// handlePublishKey drives the prompt.
func (m Model) handlePublishKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.publishStage {
	case publishSummary:
		return m.handlePublishSummaryKey(msg)
	case publishPreviewing:
		switch key {
		case "esc":
			// Back one step rather than out: the usual reason to reject a preview is
			// that something in it is wrong — most often the verdict, sometimes the
			// summary — not that you have changed your mind about publishing.
			m.publishStage = publishSummary
			m.publishReport = nil
			return m, nil
		case "q", "P":
			m.endPublish()
			return m, nil
		case "enter":
			// The one key in this flow that talks to GitHub.
			m.publishStage = publishReporting
			m.publishReport = []string{"publishing…"}
			m.publishBusy = true
			m.status = "publishing…"
			m.statusErr = false
			return m, publishCmd(m.PublishReview, m.publishVerdict(), false)
		}
		return m, m.scrollPublishReport(msg)
	case publishReporting:
		switch key {
		case "esc", "q", "enter", "P":
			// The footer keeps the one-line summary, so dismissing the report does not
			// lose what happened.
			m.publishing = false
			m.publishStage = publishChoosing
			m.publishReport = nil
			return m, nil
		}
		return m, m.scrollPublishReport(msg)
	}
	choices := publishChoices()
	switch key {
	case "esc", "q", "P":
		m.endPublish()
		return m, nil
	case "j", "down":
		m.publishCursor = min(len(choices)-1, m.publishCursor+1)
	case "k", "up":
		m.publishCursor = max(0, m.publishCursor-1)
	case "enter":
		// The summary next. Nothing in this stage reaches GitHub.
		return m.beginPublishSummary()
	}
	return m, nil
}

// beginPublishSummary opens the review-body box.
func (m Model) beginPublishSummary() (tea.Model, tea.Cmd) {
	m.publishStage = publishSummary
	// An empty anchor is what makes a comment review-level: about the change as a
	// whole rather than about a line of it (see review.Anchor).
	m.summaryEditor = newCommentEditor(review.Anchor{}, m.hunkWidth)
	m.status = ""
	m.statusErr = false
	return m, textarea.Blink
}

// handlePublishSummaryKey routes keys to the review-body box.
//
// The compose box rather than a bespoke field, so the conventions are the ones
// already learnt in this view: enter accepts, alt+enter is a newline, ctrl+g goes
// out to $EDITOR — which a summary, being the longest thing anyone writes in a
// review, wants more than a line comment does.
func (m Model) handlePublishSummaryKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// The two exits are handled here rather than read off the editor's action,
	// because one of them means something different in this flow: the compose box
	// treats enter-on-an-empty-box as "never mind", and here an empty box is a skip
	// — there is a next step to go to. Everything else (typing, tab, ctrl+g, the
	// cursor blink) is the editor's.
	switch msg.String() {
	case "enter":
		return m.saveSummaryThenPreview()
	case "esc":
		// Back to the verdicts, not out. The way out of publishing is from the menu.
		m.publishStage = publishChoosing
		return m, nil
	}
	editor, cmd, action := m.summaryEditor.update(msg)
	m.summaryEditor = editor
	if action == editorSave || action == editorSaveAndSend {
		// ctrl+s. There is no agent to send a review summary to, so it means the same
		// thing as enter rather than nothing at all.
		return m.saveSummaryThenPreview()
	}
	return m, cmd
}

// saveSummaryThenPreview files what was typed as a review-level comment, then asks
// for the plan.
//
// Saved as a record rather than passed straight into the submission: a remark about
// the change as a whole *is* a review-level comment, which the store, the stream's
// review section and the publish path already understand. Special-casing it here
// would make the one remark that summarises a review the only one that leaves no
// trace in it.
func (m Model) saveSummaryThenPreview() (tea.Model, tea.Cmd) {
	if strings.TrimSpace(m.summaryEditor.area.Value()) == "" {
		// A skip — publishing an approval stays two keystrokes.
		return m.previewPublish()
	}
	if m.SaveComment == nil {
		m.status = "commenting unavailable: no review store"
		m.statusErr = true
		return m, nil
	}
	c := m.summaryEditor.comment()
	if err := m.SaveComment(c); err != nil {
		// Stay in the box. The text is still in it, and losing a written summary to a
		// failed write would be the worst outcome available here.
		m.status = "summary: " + err.Error()
		m.statusErr = true
		return m, nil
	}
	if m.LastSavedComment != nil {
		if saved, ok := m.LastSavedComment(); ok {
			c = saved
		}
	}
	m.comments = append(m.comments, c)
	// It belongs in the review section from now on, not only in this submission.
	m.rebuildStream()
	return m.previewPublish()
}

// previewPublish asks for the plan without making any of its calls.
func (m Model) previewPublish() (tea.Model, tea.Cmd) {
	m.publishStage = publishPreviewing
	m.publishReport = []string{"reading the review…"}
	m.status = ""
	m.statusErr = false
	return m, publishCmd(m.PublishReview, m.publishVerdict(), true)
}

// verdictEvent maps a choice's verdict word onto GitHub's event constant, empty
// for "post the comments only".
//
// The viewer needs this to know whether a summary is required, which is a fact
// about GitHub rather than about awp — so it reads the same constants the publish
// path does instead of keeping its own list of which verdicts need a body.
func verdictEvent(verdict string) string {
	switch verdict {
	case "approve":
		return github.EventApprove
	case "comment":
		return github.EventComment
	case "request-changes":
		return github.EventRequestChanges
	}
	return ""
}

// publishVerdict is the selected choice's verdict.
func (m Model) publishVerdict() string {
	choices := publishChoices()
	return choices[min(max(0, m.publishCursor), len(choices)-1)].verdict
}

// scrollPublishReport lets a long plan or report be read. It is a list of lines
// rather than a viewport because it is at most a few dozen and only exists while
// the overlay is up.
func (m *Model) scrollPublishReport(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "j", "down":
		m.publishScroll++
	case "k", "up":
		m.publishScroll = max(0, m.publishScroll-1)
	}
	return nil
}

// publishDoneMsg carries the outcome back. summary is whatever the publish path
// reported, which is the same text `awp review publish` prints. dry marks the
// preview, which is the same call with nothing posted.
type publishDoneMsg struct {
	summary string
	dry     bool
	err     error
}

// publishCmd runs the publish off the update loop. It talks to GitHub once per
// comment, which is far too slow to do inline — the view would stop redrawing
// mid-run and take the keyboard with it.
func publishCmd(fn func(verdict string, dryRun bool) (string, error), verdict string, dry bool) tea.Cmd {
	return func() tea.Msg {
		summary, err := fn(verdict, dry)
		return publishDoneMsg{summary: summary, dry: dry, err: err}
	}
}

// applyPublishDone reports the outcome and re-reads the comments, whose states the
// publish just changed.
func (m Model) applyPublishDone(msg publishDoneMsg) (tea.Model, tea.Cmd) {
	m.publishScroll = 0
	if msg.dry {
		// The preview. Nothing was posted, so nothing is reported to the footer —
		// only the plan the overlay is waiting on.
		m.publishReport = publishReportLines(msg.summary)
		if msg.err != nil {
			// A plan that cannot even be built is the answer: the reason it refused is
			// what the reviewer needs, and there is nothing to confirm.
			m.publishStage = publishReporting
			m.publishReport = append([]string{"cannot publish: " + msg.err.Error()}, m.publishReport...)
			m.status = "publish: " + msg.err.Error()
			m.statusErr = true
		}
		return m, nil
	}
	m.publishBusy = false
	if msg.err != nil {
		// Kept on screen rather than folded into the footer: a run that posted six of
		// eight has to say which two, and one status segment cannot.
		m.publishReport = append(publishReportLines(msg.summary), "", "failed: "+msg.err.Error())
		m.publishStage = publishReporting
		m.status = "publish: " + msg.err.Error()
		m.statusErr = true
		return m, nil
	}
	m.publishReport = publishReportLines(msg.summary)
	m.publishStage = publishReporting
	m.status = publishStatusText(msg.summary)
	if strings.TrimSpace(m.status) == "" {
		m.status = "published"
	}
	m.statusErr = false
	// Every published comment's state changed on disk, and the view is what says
	// which findings are still open — so re-read rather than waiting for the next
	// refresh tick to correct the count. A local store read, the same one the
	// refresh tick makes, so it does not need a command of its own.
	m.reloadComments()
	return m, nil
}

// pendingPublish counts what a publish would send: comments anchored to a line,
// and remarks about the change as a whole.
//
// Counted from what the view is holding rather than asked of the store, so the
// prompt describes the review you have been reading. The store is still the
// authority when the publish runs — it re-partitions there — which is why this
// only ever appears in a sentence about what is about to happen.
func (m Model) pendingPublish() (inline, changeWide int) {
	for _, c := range m.comments {
		if c.State == review.Published || c.Publish != nil {
			continue
		}
		if strings.TrimSpace(c.Body) == "" {
			continue
		}
		if strings.TrimSpace(c.Anchor.Path) == "" {
			changeWide++
			continue
		}
		inline++
	}
	return inline, changeWide
}

// publishPrompt is the sentence above the choices: exactly what is about to go
// where, so the verdict is not being chosen blind.
func (m Model) publishPrompt() string {
	inline, changeWide := m.pendingPublish()
	// "the PR" rather than its number: the host's footer is already showing which
	// PR this review is pinned to, right under this prompt.
	const target = "the PR"
	switch {
	case inline == 0 && changeWide == 0:
		// Not an error: a verdict is worth submitting on its own, and approving a PR
		// whose comments went up earlier is a normal thing to want.
		return fmt.Sprintf("Nothing unpublished — finish the review on %s?", target)
	case changeWide == 0:
		return fmt.Sprintf("Publish %d comment%s to %s?", inline, plural(inline), target)
	case inline == 0:
		return fmt.Sprintf("Publish %d review-level remark%s to %s?", changeWide, plural(changeWide), target)
	}
	return fmt.Sprintf("Publish %d comment%s and %d review-level remark%s to %s?",
		inline, plural(inline), changeWide, plural(changeWide), target)
}

// publishReportLines splits a report into display rows, dropping the blank ones
// the CLI's own formatting leaves behind.
func publishReportLines(report string) []string {
	var out []string
	for _, line := range strings.Split(report, "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	if len(out) == 0 {
		out = []string{"nothing to do"}
	}
	return out
}

// publishStatusText squashes a report onto the footer's single row. Every line is
// kept — the part that says what failed is the part worth reading — so this is a
// join rather than a truncation.
func publishStatusText(report string) string {
	return strings.Join(publishReportLines(report), " · ")
}

// renderPublishOverlay draws the prompt in place of the panes, the same way the
// help overlay does — so the body keeps the height the host budgeted and the
// footer stays where it was.
func (m Model) renderPublishOverlay(width, height int) string {
	inner := max(20, width-helpBoxHOverhead)
	title, rows := "Publish review", []string{}
	switch m.publishStage {
	case publishSummary:
		title = "Publish review — say something about the change as a whole"
		note := "Optional. Left empty, only the comments go up."
		if github.EventNeedsBody(verdictEvent(m.publishVerdict())) {
			// GitHub's rule, and its own UI's: a verdict that asks for something has to
			// say what. Said here rather than left for the plan to refuse over.
			note = m.publishVerdict() + " needs one — GitHub rejects a verdict with no summary."
		}
		rows = append(rows,
			styleDim.Render(truncate(note, inner)),
			"",
			m.summaryEditor.view(inner),
			"",
			styleDim.Render(truncate(" enter continue · alt+enter newline · tab kind · ctrl+g $EDITOR · esc back", inner)),
		)
	case publishPreviewing:
		// Named for what the reviewer is about to authorise, not for the feature.
		title = "Publish review — this is what will be sent"
		rows = m.publishReportRows(inner, height)
		rows = append(rows, "", styleSelected.Render(truncate(" enter SENDS IT · esc back to the verdict · q cancel", inner)))
	case publishReporting:
		title = "Publish review — what happened"
		rows = m.publishReportRows(inner, height)
		rows = append(rows, "", styleDim.Render(truncate(" j/k scroll · enter close", inner)))
	default:
		rows = append(rows, styleDim.Render(truncate(m.publishPrompt(), inner)), "")
		for i, c := range publishChoices() {
			prefix := selectionPrefixBlank
			style := lipgloss.NewStyle()
			if i == m.publishCursor {
				prefix = styleSelected.Render(selectionPrefixBar)
				style = styleSelected
			}
			row := prefix + style.Render(c.label)
			if hint := " · " + c.hint; lipgloss.Width(row+hint) <= inner {
				row += styleDim.Render(hint)
			}
			rows = append(rows, truncate(row, inner))
		}
		// "review" rather than "publish": enter here shows the calls, it does not
		// make them. Saying "publish" on the key that only previews is how a reviewer
		// ends up surprised by an irreversible action.
		rows = append(rows, "", styleDim.Render(truncate(" j/k choose · enter review what will be sent · esc cancel", inner)))
	}
	head := []string{lipgloss.NewStyle().Bold(true).Render(truncate(title, inner)), ""}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(charm.Muted)).
		Padding(0, 2).
		Width(max(1, width-2)).
		Height(height).
		Render(strings.Join(append(head, rows...), "\n"))
}

// publishReportRows is the plan or the report, windowed to what fits and scrolled
// by j/k. The first line is the summary the CLI prints; the rest are one call each.
func (m Model) publishReportRows(inner, height int) []string {
	// The chrome this overlay spends on its title, blank rows and key hint.
	const overhead = 5
	room := max(1, height-overhead)
	lines := m.publishReport
	start := min(max(0, m.publishScroll), max(0, len(lines)-1))
	end := min(len(lines), start+room)
	rows := make([]string, 0, room+1)
	for _, line := range lines[start:end] {
		rows = append(rows, truncate(line, inner))
	}
	if end < len(lines) {
		rows = append(rows, styleDim.Render(truncate(fmt.Sprintf(" … %d more", len(lines)-end), inner)))
	}
	return rows
}
