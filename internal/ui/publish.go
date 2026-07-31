package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/andrewcohen/awp/internal/charm"
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
	m.publishCursor = 0
}

// endPublish closes the prompt, saying nothing — the prompt disappearing is the
// message.
func (m *Model) endPublish() {
	m.publishing = false
	m.status = ""
}

// handlePublishKey drives the prompt.
func (m Model) handlePublishKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	choices := publishChoices()
	switch msg.String() {
	case "esc", "q", "P":
		m.endPublish()
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	case "j", "down":
		m.publishCursor = min(len(choices)-1, m.publishCursor+1)
	case "k", "up":
		m.publishCursor = max(0, m.publishCursor-1)
	case "enter":
		choice := choices[min(m.publishCursor, len(choices)-1)]
		m.publishing = false
		m.publishBusy = true
		m.status = "publishing…"
		m.statusErr = false
		return m, publishCmd(m.PublishReview, choice.verdict)
	}
	return m, nil
}

// publishDoneMsg carries the outcome back. summary is whatever the publish path
// reported, which is the same text `awp review publish` prints.
type publishDoneMsg struct {
	summary string
	err     error
}

// publishCmd runs the publish off the update loop. It talks to GitHub once per
// comment, which is far too slow to do inline — the view would stop redrawing
// mid-run and take the keyboard with it.
func publishCmd(fn func(verdict string) (string, error), verdict string) tea.Cmd {
	return func() tea.Msg {
		summary, err := fn(verdict)
		return publishDoneMsg{summary: summary, err: err}
	}
}

// applyPublishDone reports the outcome and re-reads the comments, whose states the
// publish just changed.
func (m Model) applyPublishDone(msg publishDoneMsg) (tea.Model, tea.Cmd) {
	m.publishBusy = false
	if msg.err != nil {
		m.status = "publish: " + msg.err.Error()
		m.statusErr = true
		return m, nil
	}
	m.status = msg.summary
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

// renderPublishOverlay draws the prompt in place of the panes, the same way the
// help overlay does — so the body keeps the height the host budgeted and the
// footer stays where it was.
func (m Model) renderPublishOverlay(width, height int) string {
	inner := max(20, width-helpBoxHOverhead)
	rows := []string{
		lipgloss.NewStyle().Bold(true).Render(truncate("Publish review", inner)),
		"",
		styleDim.Render(truncate(m.publishPrompt(), inner)),
		"",
	}
	for i, c := range publishChoices() {
		prefix, label := selectionPrefixBlank, c.label
		style := lipgloss.NewStyle()
		if i == m.publishCursor {
			prefix = styleSelected.Render(selectionPrefixBar)
			style = styleSelected
		}
		row := prefix + style.Render(label)
		if hint := " · " + c.hint; lipgloss.Width(row+hint) <= inner {
			row += styleDim.Render(hint)
		}
		rows = append(rows, truncate(row, inner))
	}
	rows = append(rows, "", styleDim.Render(truncate(" j/k choose · enter publish · esc cancel", inner)))
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(charm.Muted)).
		Padding(0, 2).
		Width(max(1, width-2)).
		Height(height).
		Render(strings.Join(rows, "\n"))
}
