package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/andrewcohen/awp/internal/review"
)

// publishAsk records one call to the publish seam.
type publishAsk struct {
	verdict string
	summary string
	dry     bool
}

// publishModel is a viewer with a publish seam that records what it was asked for.
func publishModel(t *testing.T, report string, err error) (Model, *[]publishAsk) {
	t.Helper()
	var asked []publishAsk
	m := commentModel(t, fileWith("a.go", 1, "one", "two"))
	m.PublishReview = func(verdict, summary string, dry bool) (string, error) {
		asked = append(asked, publishAsk{verdict: verdict, summary: summary, dry: dry})
		// A dry run reports the plan; the real run reports what happened.
		if dry {
			return "2 call(s) to PR #7 (0 already published)\nPOST pulls/7/comments  a.go:1  commit=abc123def456  x", nil
		}
		return report, err
	}
	return m, &asked
}

// enter drives the overlay one step and runs whatever command it returned.
func enter(m Model) Model {
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return run(updated.(Model), cmd)
}

// escape sends esc without running any command.
func escape(m Model) Model {
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	return updated.(Model)
}

// tab cycles the verdict on the compose screen.
func tab(m Model) Model {
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	return updated.(Model)
}

// preview goes from the compose screen to the plan. One step — the verdict and the
// summary share a screen, so there is nothing between them and the plan.
func preview(m Model) Model { return enter(m) }

// composed fills in a summary, which the default verdict requires. Tests that are
// about something other than the summary requirement start here.
func composed(m Model) Model { return typeInto(m, "a summary") }

// sendIt goes all the way: compose, then confirm the plan.
func sendIt(m Model) Model { return enter(preview(m)) }

// verdicts is what the seam was asked to publish for real, ignoring previews.
func verdicts(asked []publishAsk) []string {
	var out []string
	for _, a := range asked {
		if !a.dry {
			out = append(out, a.verdict)
		}
	}
	return out
}

// run drives a command the way the program would, feeding its message back in.
func run(m Model, cmd tea.Cmd) Model {
	if cmd == nil {
		return m
	}
	updated, _ := m.Update(cmd())
	return updated.(Model)
}

// Two screens, not four. `P` opens one that carries both the verdict and the review
// body, because choosing one and writing the other are a single thought — and the
// verdict used to be its own screen ahead of the box, putting a step between a
// decision and the sentence explaining it.
func TestPublishOpensOnOneComposeScreen(t *testing.T) {
	m, asked := publishModel(t, "posted 1", nil)
	m = press(m, "P")
	if !m.publishing {
		t.Fatalf("expected the prompt open, status %q", m.status)
	}
	if m.publishStage != publishComposing {
		t.Fatalf("expected the compose stage, got %v", m.publishStage)
	}
	if len(*asked) != 0 {
		t.Fatalf("expected nothing published before a choice, got %v", *asked)
	}
	body := stripANSI(m.Body(80, 18))
	// The verdict as one cycled row, and the body box, together.
	for _, want := range []string{"verdict", "comment", "summary", "tab verdict"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the compose screen is missing %q:\n%s", want, body)
		}
	}
}

// The default verdict claims the least. Approving first meant a stray `enter`
// `enter` put an approval on someone else's PR; the cost of not defaulting to it is
// two taps of tab.
func TestPublishDoesNotDefaultToApprove(t *testing.T) {
	m, _ := publishModel(t, "posted 1", nil)
	if got := press(m, "P").publishVerdict(); got == "approve" {
		t.Fatal("approve must not be the default verdict")
	}
	if got := press(m, "P").publishVerdict(); got != "comment" {
		t.Fatalf("expected the neutral verdict by default, got %q", got)
	}
}

// GitHub's three verdicts and no fourth. "Post the comments only" described the same
// submission `comment` makes while implying the comments went up outside a review.
func TestPublishOffersThreeVerdicts(t *testing.T) {
	if got := len(publishChoices()); got != 3 {
		t.Fatalf("expected three verdicts, got %d", got)
	}
	for _, c := range publishChoices() {
		if c.verdict == "" {
			t.Fatalf("a verdict with no event survives: %+v", c)
		}
	}
}

// tab cycles the verdict, wrapping. The box below keeps the keyboard, so there is no
// second selection for j/k to drive.
func TestPublishTabCyclesTheVerdict(t *testing.T) {
	m, _ := publishModel(t, "posted 1", nil)
	m = press(m, "P")
	for _, want := range []string{"request-changes", "approve", "comment"} {
		m = tab(m)
		if got := m.publishVerdict(); got != want {
			t.Fatalf("expected tab to reach %q, got %q", want, got)
		}
	}
	// And backwards.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if m = updated.(Model); m.publishVerdict() != "approve" {
		t.Fatalf("expected shift+tab to go back, got %q", m.publishVerdict())
	}
}

// The box has the keyboard throughout, so the movement keys type rather than moving a
// selection. This is the whole reason the verdict is a cycler and not a list.
func TestPublishComposeLetsYouTypeMovementKeys(t *testing.T) {
	m, _ := publishModel(t, "posted 1", nil)
	m = typeInto(press(m, "P"), "jkjk")
	if got := m.summaryEditor.area.Value(); got != "jkjk" {
		t.Fatalf("expected j/k to be typed, got %q", got)
	}
	if m.publishVerdict() != "comment" {
		t.Fatalf("typing moved the verdict to %q", m.publishVerdict())
	}
}

// The verdict under the cursor is the one that goes up, and it has to survive the
// plan step.
func TestPublishSendsTheChosenVerdict(t *testing.T) {
	for _, tc := range []struct {
		tabs int
		want string
	}{
		{0, "comment"},
		{1, "request-changes"},
		{2, "approve"},
	} {
		m, asked := publishModel(t, "posted 1", nil)
		m = press(m, "P")
		for i := 0; i < tc.tabs; i++ {
			m = tab(m)
		}
		// The two that ask for something need a body, so give every case one.
		m = sendIt(typeInto(m, "the summary"))
		if got := verdicts(*asked); len(got) != 1 || got[0] != tc.want {
			t.Fatalf("%d tabs: expected verdict %q posted, got %v", tc.tabs, tc.want, *asked)
		}
		if m.publishStage != publishReporting {
			t.Fatalf("%d tabs: expected the report after sending, got stage %v", tc.tabs, m.publishStage)
		}
	}
}

// Nothing pending is not an error: a verdict is worth submitting on its own.
func TestPublishOffersToFinishWithNothingPending(t *testing.T) {
	m, asked := publishModel(t, "submitted the review as approve", nil)
	m = sendIt(tab(tab(press(m, "P")))) // approve, which needs no summary
	if got := verdicts(*asked); len(got) != 1 || got[0] != "approve" {
		t.Fatalf("expected an approval submitted anyway, got %v", *asked)
	}
	if !strings.Contains(m.status, "approve") {
		t.Fatalf("expected the outcome reported, got %q", m.status)
	}
}

// esc means "don't publish", and it has to say nothing — the prompt disappearing is
// the message. From the compose screen it goes straight out: there is no screen
// behind it any more, which is the point of there being two.
func TestPublishCancels(t *testing.T) {
	m, asked := publishModel(t, "", nil)
	m = escape(press(m, "P"))
	if m.publishing {
		t.Fatal("expected esc to close the prompt")
	}
	if m.status != "" {
		t.Fatalf("expected a silent cancel, got %q", m.status)
	}
	if len(*asked) != 0 {
		t.Fatalf("expected nothing published, got %v", *asked)
	}
}

// Backing out must not leave the summary behind. It used to: the text was filed on
// the way out of the box, so four abandoned attempts became four review-level
// comments on a real PR.
func TestPublishCancelDoesNotFileTheSummary(t *testing.T) {
	m, asked := publishModel(t, "", nil)
	saved := 0
	m.SaveComment = func(review.Comment) error { saved++; return nil }
	m = typeInto(press(m, "P"), "a half-formed thought")
	// All the way to the plan, then out.
	m = escape(escape(preview(m)))
	if m.publishing {
		t.Fatal("expected the flow closed")
	}
	if saved != 0 {
		t.Fatalf("a cancelled publish filed %d comment(s)", saved)
	}
	if got := verdicts(*asked); len(got) != 0 {
		t.Fatalf("expected nothing posted, got %v", *asked)
	}
}

// While the prompt is up the host must not get `esc` or `q` — it would close the
// whole view on someone who was declining to publish.
func TestPublishPromptOwnsTheKeyboard(t *testing.T) {
	m, _ := publishModel(t, "", nil)
	if press(m, "P").Filtering() != true {
		t.Fatal("expected the prompt to claim the keyboard from its host")
	}
}

// A failure has to be reported, not swallowed: the reviewer needs to know the
// review did not land.
func TestPublishReportsAFailure(t *testing.T) {
	m, _ := publishModel(t, "posted 1, failed 1", errors.New("a.go:12: 422"))
	m = sendIt(composed(press(m, "P")))
	if !m.statusErr {
		t.Fatalf("expected an error status, got %q", m.status)
	}
	if !strings.Contains(m.status, "422") {
		t.Fatalf("expected the reason in the status, got %q", m.status)
	}
	// And on screen, not only in the footer: a run that posted some of the comments
	// has to say which ones failed, and one status row cannot.
	body := stripANSI(m.Body(80, 16))
	for _, want := range []string{"posted 1, failed 1", "422"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the report does not show %q:\n%s", want, body)
		}
	}
}

// With no store to publish through, `P` says so rather than opening a prompt
// whose only outcome is an error.
func TestPublishUnavailableSaysSo(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "one"))
	m = press(m, "P")
	if m.publishing {
		t.Fatal("expected no prompt without a publish seam")
	}
	if !m.statusErr || !strings.Contains(m.status, "unavailable") {
		t.Fatalf("expected it to say publishing is unavailable, got %q", m.status)
	}
}

// A second submission while one is in flight would post everything twice: a
// comment is only marked published once GitHub has answered for it.
func TestPublishRefusesASecondRunWhileOneIsInFlight(t *testing.T) {
	m, asked := publishModel(t, "posted 1", nil)
	m = preview(composed(press(m, "P")))
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if !m.publishBusy {
		t.Fatal("expected the model to know a publish is in flight")
	}
	// A second P while it is running must not start another: a comment is only
	// marked published once GitHub answers, so a concurrent run posts twice.
	m.publishing = false
	m = press(m, "P")
	if m.publishing {
		t.Fatal("expected the second P refused while the first is running")
	}
	m = run(m, cmd)
	if m.publishBusy {
		t.Fatal("expected the flight to end when the outcome arrived")
	}
	if got := verdicts(*asked); len(got) != 1 {
		t.Fatalf("expected exactly one publish, got %v", *asked)
	}
}

// The keymap and the help are one surface: a key nobody can find is a key nobody
// has.
func TestPublishKeyIsInTheHelp(t *testing.T) {
	// The description's wording changes as the flow grows a step; that it is listed
	// as publishing is the part that has to hold.
	if !strings.Contains(helpContent(100), "publish:") {
		t.Fatal("`P` is missing from the key reference")
	}
}

// The step that matters: composing does not publish. It shows the calls that would
// be made, and only a second, explicitly-labelled confirmation sends them.
// Publishing is irreversible and outward-facing; one screen must not be the last
// thing between reading a diff and posting to someone's PR.
func TestPublishPreviewsBeforePosting(t *testing.T) {
	m, asked := publishModel(t, "posted 1", nil)
	m = preview(composed(press(m, "P")))
	if got := verdicts(*asked); len(got) != 0 {
		t.Fatalf("composing posted something: %v", *asked)
	}
	if len(*asked) != 1 || !(*asked)[0].dry {
		t.Fatalf("expected exactly one dry run, got %v", *asked)
	}
	if m.publishStage != publishConfirming {
		t.Fatalf("expected the confirm stage, got %v", m.publishStage)
	}
	// The plan, as calls: an endpoint and a target either look right or they do
	// not, which is the only diagnostic there is when a publish seems to do nothing.
	body := stripANSI(m.Body(80, 16))
	for _, want := range []string{"POST pulls/7/comments", "a.go:1", "2 call(s)"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the preview does not show %q:\n%s", want, body)
		}
	}
	// And the key that sends is labelled as the one that sends.
	if !strings.Contains(body, "enter SENDS IT") {
		t.Fatalf("the preview does not say which key posts:\n%s", body)
	}
}

// esc on the plan steps back to compose rather than out — and now lands where its
// own label says, since the verdict and the summary are both one screen back. It
// used to promise the verdict and deliver the summary box.
func TestPublishPreviewEscReturnsToCompose(t *testing.T) {
	m, asked := publishModel(t, "posted 1", nil)
	m = preview(typeInto(press(m, "P"), "worth keeping"))
	m = escape(m)
	if !m.publishing || m.publishStage != publishComposing {
		t.Fatalf("expected esc to return to compose, got publishing=%v stage=%v", m.publishing, m.publishStage)
	}
	// With the text still in the box: stepping back to fix the verdict must not cost
	// you the sentence you wrote.
	if got := m.summaryEditor.area.Value(); !strings.Contains(got, "worth keeping") {
		t.Fatalf("the summary was lost on the way back: %q", got)
	}
	if got := verdicts(*asked); len(got) != 0 {
		t.Fatalf("expected nothing posted, got %v", *asked)
	}
	// And a different verdict can then be chosen and previewed — one tab, one enter.
	m = preview(tab(m))
	if len(*asked) < 2 || (*asked)[len(*asked)-1].verdict != "request-changes" {
		t.Fatalf("expected the second verdict previewed, got %v", *asked)
	}
}

// The result stays on screen until dismissed. A run that posted eight comments
// and submitted a review is more than one footer segment can carry.
func TestPublishReportStaysUpUntilDismissed(t *testing.T) {
	m, _ := publishModel(t, "posted 2, skipped 1, failed 0\nsubmitted the review as approve", nil)
	m = sendIt(composed(press(m, "P")))
	if m.publishStage != publishReporting || !m.publishing {
		t.Fatalf("expected the report on screen, got publishing=%v stage=%v", m.publishing, m.publishStage)
	}
	body := stripANSI(m.Body(80, 16))
	for _, want := range []string{"posted 2", "submitted the review as approve"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the report does not show %q:\n%s", want, body)
		}
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m = updated.(Model); m.publishing {
		t.Fatal("expected enter to dismiss the report")
	}
	// The footer keeps the summary, so dismissing does not lose what happened.
	if !strings.Contains(m.status, "posted 2") {
		t.Fatalf("expected the summary kept in the status, got %q", m.status)
	}
}

// typeInto feeds a body into whichever box is open.
func typeInto(m Model, body string) Model {
	for _, r := range body {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(Model)
	}
	return m
}

// A verdict that GitHub requires a body for is refused on the screen that holds the
// box, not one screen later by the plan — and not two later by GitHub. The flow used
// to dead-end on its own requirement, since nothing in the viewer could write one.
func TestPublishRefusesAVerdictWithNoSummary(t *testing.T) {
	m, asked := publishModel(t, "posted 1", nil)
	m = enter(press(m, "P")) // comment, the default, with an empty box
	if m.publishStage != publishComposing {
		t.Fatalf("expected to stay on compose, got stage %v", m.publishStage)
	}
	if !m.statusErr || !strings.Contains(m.status, "needs a summary") {
		t.Fatalf("expected it to say why, got %q", m.status)
	}
	if len(*asked) != 0 {
		t.Fatalf("expected not even a dry run, got %v", *asked)
	}
	// The verdict row says so too, while it is still fixable.
	if body := stripANSI(m.Body(80, 18)); !strings.Contains(body, "needs a summary below") {
		t.Fatalf("the verdict row does not flag it:\n%s", body)
	}
	// Type one and it goes through.
	m = preview(typeInto(m, "Scope: internal/cli only."))
	if m.publishStage != publishConfirming {
		t.Fatalf("expected the plan once a summary exists, got stage %v", m.publishStage)
	}
}

// A review-level remark already on record becomes the review's body, so an empty box
// is not the same as having nothing to say.
func TestPublishAcceptsAFiledRemarkAsTheSummary(t *testing.T) {
	m, asked := publishModel(t, "posted 1", nil)
	m.comments = []review.Comment{
		{ID: "c1", Author: review.AuthorHuman, Body: "about the whole change", State: review.Open},
	}
	m.rebuildStream()
	m = preview(press(m, "P")) // comment, the default, with an empty box
	if m.publishStage != publishConfirming {
		t.Fatalf("expected the plan, got stage %v (status %q)", m.publishStage, m.status)
	}
	if len(*asked) != 1 {
		t.Fatalf("expected the dry run to have run, got %v", *asked)
	}
}

// The summary reaches the publish path as an argument rather than being filed first,
// which is what lets a cancelled run leave nothing behind. The path files it once
// GitHub has accepted it.
func TestPublishPassesTheSummaryToThePublishPath(t *testing.T) {
	m, asked := publishModel(t, "posted 1", nil)
	saved := 0
	m.SaveComment = func(review.Comment) error { saved++; return nil }
	m = sendIt(typeInto(press(m, "P"), "Reviewed internal/cli."))
	if saved != 0 {
		t.Fatalf("the viewer filed the summary itself (%d) instead of handing it over", saved)
	}
	if len(*asked) != 2 {
		t.Fatalf("expected a dry run and a real one, got %v", *asked)
	}
	for _, a := range *asked {
		if a.summary != "Reviewed internal/cli." {
			t.Fatalf("expected the summary passed through (dry=%v), got %q", a.dry, a.summary)
		}
	}
}

// An empty box is a skip for a verdict that does not need one: approving stays two
// keystrokes plus a confirmation.
func TestPublishSummaryCanBeSkipped(t *testing.T) {
	m, asked := publishModel(t, "posted 1", nil)
	m = preview(tab(tab(press(m, "P")))) // approve
	if m.publishStage != publishConfirming {
		t.Fatalf("expected the plan, got stage %v", m.publishStage)
	}
	m = enter(m)
	if got := verdicts(*asked); len(got) != 1 || got[0] != "approve" {
		t.Fatalf("expected the approval published, got %v", *asked)
	}
	if (*asked)[len(*asked)-1].summary != "" {
		t.Fatalf("expected no summary, got %q", (*asked)[len(*asked)-1].summary)
	}
}

// A plan that cannot be built is the answer, with nothing left to confirm.
func TestPublishRefusalIsShownInsteadOfAPlan(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "one"))
	m.PublishReview = func(verdict, summary string, dry bool) (string, error) {
		return "", errors.New("this workspace isn't pinned to a PR")
	}
	m = preview(tab(tab(press(m, "P")))) // approve, so nothing local refuses first
	if m.publishStage != publishReporting {
		t.Fatalf("expected the refusal shown rather than a plan, got stage %v", m.publishStage)
	}
	if body := stripANSI(m.Body(80, 16)); !strings.Contains(body, "isn't pinned to a PR") {
		t.Fatalf("the refusal is not on screen:\n%s", body)
	}
}

// The box opens on the summary the review already has. They are one thing: what is in
// the box is the review's body, and the stream's review section shows the same text.
// An empty box beside a summary sitting at the top of the diff invited a second one to
// be written, and both would have gone up.
func TestPublishPrefillsTheBoxWithTheReviewSummary(t *testing.T) {
	m, asked := publishModel(t, "posted 1", nil)
	m.comments = []review.Comment{
		{ID: "s1", Author: review.AuthorHuman, Body: "Scope: internal/cli only.", State: review.Open},
	}
	m.rebuildStream()
	m = press(m, "P")
	if got := m.summaryEditor.area.Value(); got != "Scope: internal/cli only." {
		t.Fatalf("expected the box prefilled, got %q", got)
	}
	// And it is what gets sent, rather than an empty summary alongside the remark.
	m = sendIt(m)
	for _, a := range *asked {
		if a.summary != "Scope: internal/cli only." {
			t.Fatalf("expected the prefilled body sent (dry=%v), got %q", a.dry, a.summary)
		}
	}
}

// An agent's summary carries its marker into the box, because that is the text that
// will be sent — and because publishing its words under your own account is the moment
// to see that a robot wrote them.
func TestPublishPrefillShowsTheRobotMarker(t *testing.T) {
	m, _ := publishModel(t, "posted 1", nil)
	m.comments = []review.Comment{
		{ID: "s1", Author: "agent", Body: "Reviewed all three files.", State: review.Open},
	}
	m.rebuildStream()
	m = press(m, "P")
	if got := m.summaryEditor.area.Value(); !strings.HasPrefix(got, review.RobotMarker) {
		t.Fatalf("expected the marker in the box, got %q", got)
	}
}

// Several summary remarks become one body, a paragraph each, in order.
func TestPublishPrefillJoinsSeveralSummaries(t *testing.T) {
	m, _ := publishModel(t, "posted 1", nil)
	m.comments = []review.Comment{
		{ID: "s1", Author: review.AuthorHuman, Body: "First.", State: review.Open},
		{ID: "s2", Author: review.AuthorHuman, Body: "Second.", State: review.Open},
	}
	m.rebuildStream()
	m = press(m, "P")
	if got := m.summaryEditor.area.Value(); got != "First.\n\nSecond." {
		t.Fatalf("expected both joined, got %q", got)
	}
}

// A summary already on GitHub must not come back into the box: it would be sent a
// second time as part of the next review's body.
func TestPublishPrefillSkipsPublishedSummaries(t *testing.T) {
	m, _ := publishModel(t, "posted 1", nil)
	m.comments = []review.Comment{
		{ID: "s1", Author: review.AuthorHuman, Body: "went up last time", State: review.Published},
		{ID: "s2", Author: review.AuthorHuman, Body: "still a draft", State: review.Open},
	}
	m.rebuildStream()
	m = press(m, "P")
	if got := m.summaryEditor.area.Value(); got != "still a draft" {
		t.Fatalf("expected only the unpublished summary, got %q", got)
	}
}

// Editing the prefill sends what you edited it to, not the original.
func TestPublishSendsTheEditedPrefill(t *testing.T) {
	m, asked := publishModel(t, "posted 1", nil)
	m.comments = []review.Comment{
		{ID: "s1", Author: review.AuthorHuman, Body: "draft", State: review.Open},
	}
	m.rebuildStream()
	m = sendIt(typeInto(press(m, "P"), " plus more"))
	last := (*asked)[len(*asked)-1]
	if last.summary != "draft plus more" {
		t.Fatalf("expected the edited body, got %q", last.summary)
	}
}
