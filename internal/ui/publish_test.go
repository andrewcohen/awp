package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/andrewcohen/awp/internal/review"
)

// publishModel is a viewer with a publish seam that records what it was asked for.
func publishModel(t *testing.T, summary string, err error) (Model, *[]string) {
	t.Helper()
	var asked []string
	m := commentModel(t, fileWith("a.go", 1, "one", "two"))
	m.PublishReview = func(verdict string) (string, error) {
		asked = append(asked, verdict)
		return summary, err
	}
	return m, &asked
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
		updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = updated.(Model)
		if m.publishing {
			t.Fatal("expected the prompt closed once a choice was made")
		}
		m = run(m, cmd)
		if len(*asked) != 1 || (*asked)[0] != tc.want {
			t.Fatalf("%d down: expected verdict %q, got %v", tc.down, tc.want, *asked)
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
	m = press(m, "P")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = run(updated.(Model), cmd)
	if len(*asked) != 1 || (*asked)[0] != "approve" {
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
	m = press(m, "P")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = run(updated.(Model), cmd)
	if !m.statusErr {
		t.Fatalf("expected an error status, got %q", m.status)
	}
	if !strings.Contains(m.status, "422") {
		t.Fatalf("expected the reason in the status, got %q", m.status)
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
	m = press(m, "P")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if !m.publishBusy {
		t.Fatal("expected the model to know a publish is in flight")
	}
	m = press(m, "P")
	if m.publishing {
		t.Fatal("expected the second P refused while the first is running")
	}
	// And once it lands, P works again.
	m = run(m, cmd)
	if m.publishBusy {
		t.Fatal("expected the flight to end when the outcome arrived")
	}
	if !press(m, "P").publishing {
		t.Fatal("expected P to work again after the publish finished")
	}
	if len(*asked) != 1 {
		t.Fatalf("expected exactly one publish, got %v", *asked)
	}
}

// The keymap and the help are one surface: a key nobody can find is a key nobody
// has.
func TestPublishKeyIsInTheHelp(t *testing.T) {
	if !strings.Contains(helpContent(100), "publish the review") {
		t.Fatal("`P` is missing from the key reference")
	}
}
