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
	dry     bool
}

// publishModel is a viewer with a publish seam that records what it was asked for.
func publishModel(t *testing.T, summary string, err error) (Model, *[]publishAsk) {
	t.Helper()
	var asked []publishAsk
	m := commentModel(t, fileWith("a.go", 1, "one", "two"))
	m.PublishReview = func(verdict string, dry bool) (string, error) {
		asked = append(asked, publishAsk{verdict: verdict, dry: dry})
		// A dry run reports the plan; the real run reports what happened.
		if dry {
			return "2 call(s) to PR #7 (0 already published)\nPOST pulls/7/comments  a.go:1  commit=abc123def456  x", nil
		}
		return summary, err
	}
	return m, &asked
}

// enter drives the overlay one step and runs whatever command it returned.
func enter(m Model) Model {
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return run(updated.(Model), cmd)
}

// preview drives the flow from the verdict menu to the plan: choose the verdict,
// skip the summary. Neither step touches GitHub.
func preview(m Model) Model { return enter(enter(m)) }

// sendIt drives it all the way: choose, skip the summary, confirm the plan.
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

// `P` asks for the verdict rather than assuming one: which of the three you pick
// is the whole point of having read the change.
func TestPublishAsksForTheVerdict(t *testing.T) {
	m, asked := publishModel(t, "posted 1", nil)
	m = press(m, "P")
	if !m.publishing {
		t.Fatalf("expected the prompt open, status %q", m.status)
	}
	if len(*asked) != 0 {
		t.Fatalf("expected nothing published before a choice, got %v", *asked)
	}
	body := stripANSI(m.Body(80, 14))
	for _, want := range []string{"approve", "request changes", "comment", "post the comments only"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the prompt does not offer %q:\n%s", want, body)
		}
	}
}

// The verdict under the cursor is the one that goes up.
func TestPublishSendsTheChosenVerdict(t *testing.T) {
	for _, tc := range []struct {
		down int
		want string
	}{
		{0, "approve"},
		{1, "request-changes"},
		{2, "comment"},
		// The fourth is "post the comments only": no review submitted.
		{3, ""},
	} {
		m, asked := publishModel(t, "posted 1", nil)
		m = pressTimes(press(m, "P"), "j", tc.down)
		// Choose, skip the summary, read the plan, send. The verdict has to survive
		// every one of those steps.
		m = sendIt(m)
		if got := verdicts(*asked); len(got) != 1 || got[0] != tc.want {
			t.Fatalf("%d down: expected verdict %q posted, got %v", tc.down, tc.want, *asked)
		}
		if m.publishStage != publishReporting {
			t.Fatalf("%d down: expected the report after sending, got stage %v", tc.down, m.publishStage)
		}
	}
}

// The prompt says what is about to go where, so a verdict is not chosen blind.
func TestPublishPromptCountsWhatIsPending(t *testing.T) {
	m, _ := publishModel(t, "", nil)
	m.comments = []review.Comment{
		commentOn("a.go", 1, "one", "a line remark"),
		{ID: "c2", Author: review.AuthorHuman, Body: "about the whole change", State: review.Open},
		// Already on GitHub: not pending, so not counted.
		{ID: "c3", Author: review.AuthorHuman, Body: "sent earlier", State: review.Published,
			Anchor: review.Anchor{Path: "a.go", LineHint: 2}},
	}
	m.rebuildStream()
	got := m.publishPrompt()
	if !strings.Contains(got, "1 comment") || !strings.Contains(got, "1 review-level remark") {
		t.Fatalf("expected the prompt to count both kinds, got %q", got)
	}
	if strings.Contains(got, "2 comment") {
		t.Fatalf("a published comment was counted as pending: %q", got)
	}
}

// Nothing pending is not an error: a verdict is worth submitting on its own.
func TestPublishOffersToFinishWithNothingPending(t *testing.T) {
	m, asked := publishModel(t, "submitted the review as approve", nil)
	if got := m.publishPrompt(); !strings.Contains(got, "Nothing unpublished") {
		t.Fatalf("expected the prompt to say so, got %q", got)
	}
	m = sendIt(press(m, "P"))
	if got := verdicts(*asked); len(got) != 1 || got[0] != "approve" {
		t.Fatalf("expected an approval submitted anyway, got %v", *asked)
	}
	if !strings.Contains(m.status, "approve") {
		t.Fatalf("expected the outcome reported, got %q", m.status)
	}
}

// esc means "don't publish", and it has to say nothing — the prompt disappearing
// is the message.
func TestPublishCancels(t *testing.T) {
	m, asked := publishModel(t, "", nil)
	m = press(m, "P")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
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
	m = sendIt(press(m, "P"))
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
	m = preview(press(m, "P"))
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

// The step that matters: choosing a verdict does not publish. It shows the calls
// that would be made, and only a second, explicitly-labelled confirmation sends
// them. Publishing is irreversible and outward-facing; a menu choice must not be
// the last thing between reading a diff and posting to someone's PR.
func TestPublishPreviewsBeforePosting(t *testing.T) {
	m, asked := publishModel(t, "posted 1", nil)
	m = preview(press(m, "P"))
	if got := verdicts(*asked); len(got) != 0 {
		t.Fatalf("choosing a verdict posted something: %v", *asked)
	}
	if len(*asked) != 1 || !(*asked)[0].dry {
		t.Fatalf("expected exactly one dry run, got %v", *asked)
	}
	if m.publishStage != publishPreviewing {
		t.Fatalf("expected the preview stage, got %v", m.publishStage)
	}
	// The plan, as calls: an endpoint and a target either look right or they do
	// not, which is the only diagnostic there is when a publish seems to do nothing.
	body := stripANSI(m.Body(80, 16))
	for _, want := range []string{"POST pulls/7/comments", "a.go:1", "will be sent"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the preview does not show %q:\n%s", want, body)
		}
	}
	// And the key that sends is labelled as the one that sends.
	if !strings.Contains(body, "enter SENDS IT") {
		t.Fatalf("the preview does not say which key posts:\n%s", body)
	}
}

// esc on the preview steps back rather than out: the usual reason to reject a plan
// is that something in it is wrong — the verdict, or the summary — not that you
// have changed your mind about publishing.
func TestPublishPreviewEscStepsBack(t *testing.T) {
	m, asked := publishModel(t, "posted 1", nil)
	m = preview(press(m, "P"))
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if !m.publishing || m.publishStage != publishSummary {
		t.Fatalf("expected esc to return to the summary, got publishing=%v stage=%v", m.publishing, m.publishStage)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m = updated.(Model); m.publishStage != publishChoosing {
		t.Fatalf("expected a second esc to reach the verdicts, got stage %v", m.publishStage)
	}
	if got := verdicts(*asked); len(got) != 0 {
		t.Fatalf("expected nothing posted, got %v", *asked)
	}
	// And a different verdict can then be chosen and previewed.
	m = preview(press(m, "j"))
	if len(*asked) < 2 || (*asked)[len(*asked)-1].verdict != "request-changes" {
		t.Fatalf("expected the second verdict previewed, got %v", *asked)
	}
}

// The result stays on screen until dismissed. A run that posted eight comments
// and submitted a review is more than one footer segment can carry.
func TestPublishReportStaysUpUntilDismissed(t *testing.T) {
	m, _ := publishModel(t, "posted 2, skipped 1, failed 0\nsubmitted the review as approve", nil)
	m = sendIt(press(m, "P"))
	if m.publishStage != publishReporting || !m.publishing {
		t.Fatalf("expected the report on screen, got publishing=%v stage=%v", m.publishing, m.publishStage)
	}
	body := stripANSI(m.Body(80, 16))
	for _, want := range []string{"what happened", "posted 2", "submitted the review as approve"} {
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

// A plan that cannot be built is the answer: "request changes needs a summary"
// has to arrive before anything is posted, with nothing left to confirm.
func TestPublishRefusalIsShownInsteadOfAPlan(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "one"))
	m.PublishReview = func(verdict string, dry bool) (string, error) {
		return "", errors.New("--verdict request-changes needs a summary")
	}
	m = preview(pressTimes(press(m, "P"), "j", 1)) // request changes, then the plan
	if m.publishStage != publishReporting {
		t.Fatalf("expected the refusal shown rather than a plan, got stage %v", m.publishStage)
	}
	if body := stripANSI(m.Body(80, 16)); !strings.Contains(body, "needs a summary") {
		t.Fatalf("the refusal is not on screen:\n%s", body)
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

// The flow used to dead-end on its own requirement: `request changes` needs a
// summary and nothing in the viewer could write one. Now the menu asks for it.
func TestPublishAsksForASummary(t *testing.T) {
	m, _ := publishModel(t, "posted 1", nil)
	var saved []review.Comment
	m.SaveComment = func(c review.Comment) error { saved = append(saved, c); return nil }
	// request changes — the verdict that requires a body.
	m = enter(pressTimes(press(m, "P"), "j", 1))
	if m.publishStage != publishSummary {
		t.Fatalf("expected the summary step, got stage %v", m.publishStage)
	}
	body := stripANSI(m.Body(80, 20))
	// It says why it is asking, rather than leaving the plan to refuse later.
	if !strings.Contains(body, "needs one") {
		t.Fatalf("the summary step does not say the verdict requires it:\n%s", body)
	}
	// A textarea, so it can be edited — and headed as what it is about, since there
	// is no file to name.
	if !strings.Contains(body, "the whole change") {
		t.Fatalf("expected the box headed as review-level:\n%s", body)
	}
	m = typeInto(m, "Scope: internal/cli only.")
	m = enter(m)
	// Saved as a real review-level comment — an anchor with no path — so it lives in
	// the review afterwards rather than only in this submission.
	if len(saved) != 1 {
		t.Fatalf("expected the summary saved as a comment, got %d", len(saved))
	}
	if got := saved[0]; got.Anchor.Path != "" || got.Body != "Scope: internal/cli only." {
		t.Fatalf("expected a review-level record, got %+v", got.Anchor)
	}
	if m.publishStage != publishPreviewing {
		t.Fatalf("expected the plan after the summary, got stage %v", m.publishStage)
	}
}

// An empty box is a skip: publishing an approval stays two keystrokes plus a
// confirmation, not three plus a confirmation.
func TestPublishSummaryCanBeSkipped(t *testing.T) {
	m, asked := publishModel(t, "posted 1", nil)
	saved := 0
	m.SaveComment = func(review.Comment) error { saved++; return nil }
	m = enter(enter(press(m, "P"))) // choose approve, skip the summary
	if saved != 0 {
		t.Fatalf("an empty summary was saved as a comment (%d)", saved)
	}
	if m.publishStage != publishPreviewing {
		t.Fatalf("expected the plan, got stage %v", m.publishStage)
	}
	m = enter(m)
	if got := verdicts(*asked); len(got) != 1 || got[0] != "approve" {
		t.Fatalf("expected the approval published, got %v", *asked)
	}
}

// The summary appears in the review afterwards, not only in the submission.
func TestPublishSummaryShowsUpInTheReview(t *testing.T) {
	m, _ := publishModel(t, "posted 1", nil)
	m.SaveComment = func(review.Comment) error { return nil }
	m = enter(press(m, "P"))
	m = enter(typeInto(m, "Reviewed internal/cli."))
	// Dismiss the flow and look at the stream.
	m.publishing = false
	if len(m.commentIndex) != 1 {
		t.Fatalf("expected the summary in the comment index, got %d entries", len(m.commentIndex))
	}
	if got := entryLocation(m.commentIndex[0]); !strings.Contains(got, "review") {
		t.Fatalf("expected it listed as review-level, got %q", got)
	}
}

// A failed write keeps the box and its text: losing a written summary is the worst
// outcome available here.
func TestPublishSummaryKeepsTheTextWhenSavingFails(t *testing.T) {
	m, asked := publishModel(t, "posted 1", nil)
	m.SaveComment = func(review.Comment) error { return errors.New("disk full") }
	m = enter(press(m, "P"))
	m = enter(typeInto(m, "worth keeping"))
	if m.publishStage != publishSummary {
		t.Fatalf("expected to stay in the box, got stage %v", m.publishStage)
	}
	if !strings.Contains(m.summaryEditor.area.Value(), "worth keeping") {
		t.Fatalf("the text was lost: %q", m.summaryEditor.area.Value())
	}
	if !m.statusErr || !strings.Contains(m.status, "disk full") {
		t.Fatalf("expected the reason reported, got %q", m.status)
	}
	if len(*asked) != 0 {
		t.Fatalf("expected nothing published, got %v", *asked)
	}
}

// esc from the summary goes back to the verdicts, so a wrong choice is one step
// away rather than a restart.
func TestPublishSummaryEscGoesBackToTheVerdicts(t *testing.T) {
	m, _ := publishModel(t, "posted 1", nil)
	m = enter(press(m, "P"))
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if !m.publishing || m.publishStage != publishChoosing {
		t.Fatalf("expected the verdicts again, got publishing=%v stage=%v", m.publishing, m.publishStage)
	}
}
