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
// Two screens, not four. Compose — the verdict and the summary together, since
// choosing one and writing the other are one thought — then confirm, which shows
// the exact calls and sends them. The verdict used to be its own screen ahead of
// the summary box, which put a step between a decision and the sentence explaining
// it, and made `esc` from the preview land somewhere its own label denied.

// publishChoice is one verdict the reviewer can pick.
type publishChoice struct {
	// label is what the reviewer is choosing to do.
	label string
	// verdict is the word `awp review publish --verdict` takes.
	verdict string
	// hint says what the choice means on GitHub, since "comment" as a verdict and
	// "comment" as a thing you leave on a line are easy to confuse.
	hint string
}

// publishChoices is the three verdicts, neutral first.
//
// `comment` is the default deliberately, even though approving is the more common
// ending. The default on an irreversible outward action should be the one that claims
// the least: approving first meant a stray `enter` `enter` put an approval on someone
// else's PR, and the cost of not defaulting to it is two taps of tab. The order then
// escalates — say something, ask for changes, sign off — so where the cycle goes is
// predictable from what the verdicts mean.
//
// GitHub's three and no fourth. "Post the comments only" used to be here, and it
// described the same submission `comment` makes — a review with no verdict — while
// implying the comments went up outside a review. One less thing to choose between
// on a screen whose job is to be quick.
func publishChoices() []publishChoice {
	return []publishChoice{
		{label: "comment", verdict: "comment", hint: "a review with no verdict · needs a summary"},
		{label: "request changes", verdict: "request-changes", hint: "blocks the merge · needs a summary"},
		{label: "approve", verdict: "approve", hint: "approve the PR"},
	}
}

// publishStage is where the flow has got to. Publishing is irreversible and
// outward-facing, so it does not go from a keystroke straight to posting: you say
// what you are doing, you read exactly which calls that will make, and then you
// confirm. The result then stays on screen instead of being a segment in a footer
// competing with the workspace name and the base.
type publishStage int

const (
	// publishComposing is the verdict and the review body, on one screen. The body is
	// a remark about the change as a whole, which `request changes` and `comment`
	// require and an approval may want; the verdict is a single row cycled with tab,
	// so the text area keeps the keyboard and j/k just type.
	publishComposing publishStage = iota
	// publishConfirming is the list of calls, awaiting the one key that sends them.
	publishConfirming
	// publishReporting is what happened. Not a third step so much as what the confirm
	// box becomes: the reviewer navigates two screens and then reads an outcome.
	publishReporting
)

// beginPublish opens the compose screen. Reports what it will not do rather than
// opening a prompt whose only outcome is an error.
func (m *Model) beginPublish() {
	if m.PublishReview == nil {
		m.fail("publishing unavailable here")
		return
	}
	if m.publishBusy {
		// A second submission while the first is in flight would post everything
		// twice: the store is only marked published once GitHub answers.
		m.status = "already publishing…"
		return
	}
	m.publishing = true
	m.publishStage = publishComposing
	m.publishCursor = 0
	m.publishReport = nil
	// An empty anchor is what makes a comment the review summary rather than a remark
	// about a line (see review.Anchor). The box is built here so the screen opens with
	// the keyboard already in it.
	m.summaryEditor = newCommentEditor(review.Anchor{}, m.hunkWidth)
	m.summaryEditor.area.Placeholder = "review summary…"
	// Sized to the screen it is on. The 4 rows newCommentEditor gives it are right
	// in the stream, where the box is a guest above the code you are commenting on
	// — here it is the only thing on the screen, and a review body written through
	// a 4-row letterbox is why summaries came out shorter than the review deserved.
	m.resizeSummaryBox()
	// Opened on the summary the review already has, rather than empty. They are one
	// thing: what is in this box is the review's body, and the review section of the
	// stream shows the same text. An empty box beside a summary sitting at the top of
	// the diff invited a second one to be written, and both would have gone up.
	m.summarySources = m.unpublishedSummaries()
	if len(m.summarySources) > 0 {
		m.summaryEditor.setBody(joinSummaries(m.summarySources))
	}
	m.status = ""
	m.statusErr = false
}

// publishSummaryChrome is every row the composing screen spends around the
// summary box's text area: the overlay's title and the blank under it (2), the
// verdict row and the blank under it (2), the box's own border rows and its
// header (3), and the blank plus key hint at the foot (2).
//
// A constant that has to be kept true by hand, the same bargain
// commentEditorRows makes. Nothing detects it drifting except
// TestThePublishScreenFillsItsBudget, which is the reason that test asserts an
// exact height rather than a lower bound — a wrong number here does not crash,
// it silently gives the box two rows too few or pushes the key hint off the
// bottom, and neither is visible until someone is typing into it.
const publishSummaryChrome = 9

// summaryAreaHeight is how many rows the summary's text area gets in a body of
// this height: everything the chrome does not need.
//
// Floored rather than allowed to go negative or vanish. On a terminal too short
// to fit the chrome the box overflows, which is what a fixed height did at every
// size anyway — a box you can see three lines of beats one you cannot see at all.
func summaryAreaHeight(bodyHeight int) int {
	return max(3, max(minBodyHeight, bodyHeight)-publishSummaryChrome)
}

// resizeSummaryBox re-lays the summary box for the current viewport.
//
// On the model rather than on the render copy, because neither dimension is only
// about drawing: the height is the viewport the cursor is kept inside, and the
// width is where the text wraps. Set at render time the box would draw twenty
// rows while scrolling as if it had four, so typing past the fourth line would
// jump the view for no reason the writer could see.
//
// The width is the overlay's, not m.hunkWidth. The stream's compose box is sized
// to the right-hand pane because that is where it sits; this screen is not the
// stream, and inheriting that width left the text wrapping short of a border
// drawn at the full width — a visible strip of unused box down the right.
func (m *Model) resizeSummaryBox() {
	if !m.publishing {
		return
	}
	m.summaryEditor.setWidth(publishOverlayInner(m.width))
	m.summaryEditor.area.SetHeight(summaryAreaHeight(m.bodyHeight))
}

// publishOverlayInner is the width inside the publish overlay's border and
// padding — what renderPublishOverlay hands to the rows it composes.
func publishOverlayInner(width int) int { return max(20, width-helpBoxHOverhead) }

// unpublishedSummaries is the review's own summary remarks — the ones a publish
// would carry — oldest first.
func (m Model) unpublishedSummaries() []review.Comment {
	var out []review.Comment
	for _, c := range m.comments {
		if c.OnGitHub() {
			continue
		}
		if strings.TrimSpace(c.Body) == "" || c.ReplyTo != "" {
			continue
		}
		if c.Anchor.Scope() == review.ChangeScope {
			out = append(out, c)
		}
	}
	return out
}

// joinSummaries is several summary remarks as one body, a paragraph each.
//
// Their *published* bodies, marker and kind included, because this text is the review
// body that will be sent — the box and the plan must not be able to disagree. It also
// means an agent's 🤖 is visible before you publish its words under your own account,
// which is the moment that fact matters.
func joinSummaries(cs []review.Comment) string {
	parts := make([]string, 0, len(cs))
	for _, c := range cs {
		parts = append(parts, c.PublishBody())
	}
	return strings.Join(parts, "\n\n")
}

// endPublish closes the prompt, saying nothing — the prompt disappearing is the
// message.
func (m *Model) endPublish() {
	m.publishing = false
	m.publishStage = publishComposing
	m.publishReport = nil
	m.status = ""
}

// handlePublishKey drives the two screens.
func (m Model) handlePublishKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.publishStage {
	case publishConfirming:
		switch key {
		case "esc":
			// Back to compose rather than out: the usual reason to reject a plan is that
			// something in it is wrong — the verdict, or what the summary says — not that
			// you have changed your mind about publishing. Both live one screen back now,
			// so this lands where the hint says it does.
			m.publishStage = publishComposing
			m.publishReport = nil
			return m, textarea.Blink
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
			return m, publishCmd(m.PublishReview, m.publishVerdict(), m.publishSummaryText(), false)
		}
		return m, m.scrollPublishReport(msg)
	case publishReporting:
		switch key {
		case "esc", "q", "enter", "P":
			// The footer keeps the one-line summary, so dismissing the report does not
			// lose what happened.
			m.publishing = false
			m.publishStage = publishComposing
			m.publishReport = nil
			return m, nil
		}
		return m, m.scrollPublishReport(msg)
	}
	return m.handlePublishComposeKey(msg)
}

// publishSummaryText is what is in the review-body box.
func (m Model) publishSummaryText() string {
	return strings.TrimSpace(m.summaryEditor.area.Value())
}

// handlePublishComposeKey drives the compose screen: the verdict cycler and the
// review-body box, which share it.
//
// The box keeps the keyboard the whole time, so j/k and the arrows type rather than
// moving a selection, and the verdict is cycled with tab — the same gesture that
// cycles a comment's kind in the box this one is built from. The kind itself is not
// offered here: a review body is the review's body, and every other kind would be a
// claim about a remark that has no line to make it about.
func (m Model) handlePublishComposeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "tab":
		// Intercepted ahead of the editor, which would otherwise cycle the kind.
		m.publishCursor = (m.publishCursor + 1) % len(publishChoices())
		return m, nil
	case "shift+tab":
		n := len(publishChoices())
		m.publishCursor = (m.publishCursor - 1 + n) % n
		return m, nil
	case "enter":
		return m.confirmPublish()
	case "esc":
		// Out. There is no screen behind this one any more, which is the point.
		m.endPublish()
		return m, nil
	}
	editor, cmd, action := m.summaryEditor.update(msg)
	m.summaryEditor = editor
	if action == editorSave || action == editorSaveAndSend {
		// ctrl+s. There is no agent to send a review summary to, so it means the same
		// thing as enter rather than nothing at all.
		return m.confirmPublish()
	}
	return m, cmd
}

// confirmPublish asks for the plan, refusing early when the verdict needs a summary
// and there is none — the plan would only come back with GitHub's version of the
// same complaint, one screen later.
func (m Model) confirmPublish() (tea.Model, tea.Cmd) {
	if github.EventNeedsBody(verdictEvent(m.publishVerdict())) && m.publishSummaryText() == "" {
		m.fail("%s needs a summary — GitHub rejects a verdict with no body", m.publishVerdict())
		return m, nil
	}
	return m.previewPublish()
}

// previewPublish asks for the plan without making any of its calls, and without
// writing anything either.
//
// The summary rides along as an argument rather than being filed first. It used to be
// saved on the way out of the compose box, which meant a plan you then declined left
// the remark behind — four abandoned attempts became four review-level comments on a
// real PR. It is filed by the publish path once GitHub has accepted it, so the record
// still exists afterwards without a cancelled run creating one.
func (m Model) previewPublish() (tea.Model, tea.Cmd) {
	m.publishStage = publishConfirming
	m.publishReport = []string{"reading the review…"}
	m.status = ""
	m.statusErr = false
	return m, publishCmd(m.PublishReview, m.publishVerdict(), m.publishSummaryText(), true)
}

// verdictEvent maps a choice's verdict word onto GitHub's event constant.
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
func publishCmd(fn func(verdict, summary string, dryRun bool) (string, error), verdict, summary string, dry bool) tea.Cmd {
	return func() tea.Msg {
		report, err := fn(verdict, summary, dry)
		return publishDoneMsg{summary: report, dry: dry, err: err}
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
			m.publishReport = append(
				publishReportLines("cannot publish: "+msg.err.Error()), m.publishReport...)
			m.fail("publish: %v", msg.err)
		}
		return m, nil
	}
	m.publishBusy = false
	if msg.err != nil {
		// Kept on screen rather than folded into the footer: a run that posted six of
		// eight has to say which two, and one status segment cannot. Split rather than
		// appended whole — a refusal names one anchor per line, and as a single element
		// the overlay drew all of them as one unreadable row.
		m.publishReport = append(append(publishReportLines(msg.summary), ""),
			publishReportLines("failed: "+msg.err.Error())...)
		m.publishStage = publishReporting
		m.fail("publish: %v", msg.err)
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
	inner := publishOverlayInner(width)
	// Both are set by every branch below — each stage names itself.
	var title string
	var rows []string
	switch m.publishStage {
	case publishConfirming:
		title = "Publish review"
		rows = m.publishReportRows(inner, height)
		rows = append(rows, "", styleSelected.Render(truncate(" enter SENDS IT · esc back · q cancel", inner)))
	case publishReporting:
		title = "Publish review"
		rows = m.publishReportRows(inner, height)
		rows = append(rows, "", styleDim.Render(truncate(" j/k scroll · enter close", inner)))
	default:
		// Every stage is titled the same. Which one you are on is said by the keys
		// underneath it — "enter SENDS IT" is not a thing the confirm screen needed a
		// subtitle to explain, and the counts belong next to the calls they describe.
		title = "Publish review"
		rows = append(rows, m.verdictRow(inner), "", m.summaryBoxView(inner), "")
		hint := " enter continue · tab verdict · alt+enter newline · ctrl+g $EDITOR · esc cancel"
		if lipgloss.Width(hint) > inner {
			hint = " enter continue · tab verdict · ctrl+g $EDITOR · esc cancel"
		}
		rows = append(rows, styleDim.Render(truncate(hint, inner)))
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

// verdictRow is the verdict as one cycled row: the choice, then what it means on
// GitHub, then whether a body is required.
//
// A row rather than a list of three. The text area below keeps the keyboard, so
// there is no second selection for j/k to drive — and the verdict a reviewer wants is
// nearly always the first one, which a cycler puts under no keystrokes at all.
func (m Model) verdictRow(inner int) string {
	c := publishChoices()[min(max(0, m.publishCursor), len(publishChoices())-1)]
	label := "‹ " + c.label + " ›"
	hint := "  " + c.hint
	if github.EventNeedsBody(verdictEvent(c.verdict)) && m.publishSummaryText() == "" {
		// Said while it is still fixable, on the screen holding the box that fixes it,
		// rather than left for the plan — or GitHub — to refuse over one screen later.
		hint = "  needs a summary below"
	}
	// Measured on the plain text and truncated with the ANSI-aware helper: styling
	// first and cutting afterwards counts escape bytes as characters, which chopped
	// this row mid-word at a width it comfortably fitted.
	row := " " + styleDim.Render("verdict  ") + styleSelected.Render(label)
	if lipgloss.Width(" verdict  "+label+hint) <= inner {
		row += styleDim.Render(hint)
	}
	return truncateStyled(row, inner)
}

// summaryBoxView renders the review-body box for this screen.
//
// Not commentEditor.view: that box heads itself with the comment's kind and hints
// "enter save · tab kind", which are both wrong here — tab cycles the verdict on this
// screen, enter goes on to the plan, and a review body has no kind to choose. The
// text area itself is shared, so typing, alt+enter and ctrl+g behave identically.
func (m Model) summaryBoxView(width int) string {
	inner := max(20, width-2) - 2
	head := " review summary"
	if !github.EventNeedsBody(verdictEvent(m.publishVerdict())) {
		head = " review summary — optional"
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(charm.Muted)).
		Width(max(20, width-2)).
		Render(lipgloss.JoinVertical(lipgloss.Left,
			styleDim.Render(truncate(head, inner)),
			m.summaryEditor.area.View(),
		))
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
