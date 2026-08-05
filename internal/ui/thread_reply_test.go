package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/andrewcohen/awp/internal/review"
)

// Replying to a GitHub thread from the viewer.
//
// The gesture is the same `c` that replies to one of our own remarks, because from
// the reader's side it is the same act: answer the thing under the cursor. What
// differs is where the answer goes — straight to the PR — and these check that it
// gets there, that it is not lost when it cannot, and that it is drawn once rather
// than twice afterwards.

// replyRecorder stands in for the GitHub call.
type replyRecorder struct {
	threads []string
	bodies  []string
	id      string
	err     error
}

func (r *replyRecorder) reply(threadID, body string) (string, error) {
	r.threads = append(r.threads, threadID)
	r.bodies = append(r.bodies, body)
	return r.id, r.err
}

// threadReplyModel is a viewer sitting on a mirrored thread's row, with the store
// callbacks a reply needs.
func threadReplyModel(t *testing.T, rec *replyRecorder) (Model, []review.Comment) {
	t.Helper()
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	var saved []review.Comment
	m.SaveComment = func(c review.Comment) error {
		c.ID = "local-1"
		saved = append(saved, c)
		return nil
	}
	m.LastSavedComment = func() (review.Comment, bool) {
		if len(saved) == 0 {
			return review.Comment{}, false
		}
		return saved[len(saved)-1], true
	}
	m.UpdateComment = func(c review.Comment) error {
		saved = append(saved, c)
		return nil
	}
	m.ReplyToThread = rec.reply
	m.threadVisibility = ThreadsAll
	m.SetThreads([]review.Thread{remoteThread("T1", "a.go", 1, false, "why is this here?")})
	return m, saved
}

// cursorToComment parks the cursor on the first comment row.
func cursorToComment(t *testing.T, m Model) Model {
	t.Helper()
	for m.stream.rows[m.cursorRow].kind != rowComment {
		before := m.cursorRow
		m = press(m, "j")
		if m.cursorRow == before {
			t.Fatal("never reached the comment row")
		}
	}
	return m
}

// cursorToCommentID parks the cursor on a particular comment's first row.
//
// By id rather than by counting comment rows: a comment occupies several of them,
// so "the second comment row" is usually still the first comment — which is how a
// test meant to act on our own reply ends up acting on the thread above it.
func cursorToCommentID(t *testing.T, m Model, id string) Model {
	t.Helper()
	for i, r := range m.stream.rows {
		if !isCommentRow(r.kind) || r.comment < 0 || r.comment >= len(m.stream.comments) {
			continue
		}
		if m.stream.comments[r.comment].ID == id {
			m.cursorRow = i
			return m
		}
	}
	t.Fatalf("no row for comment %q", id)
	return m
}

// typeInto types a body into the open box and saves it, returning the command the
// save produced — which is the network call.
func typeAndSave(m Model, body string) (Model, tea.Cmd) {
	for _, r := range body {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(Model)
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return updated.(Model), cmd
}

func TestCOnAGitHubThreadPostsAReplyToIt(t *testing.T) {
	rec := &replyRecorder{id: "PRRC_new"}
	m, _ := threadReplyModel(t, rec)
	m = cursorToComment(t, m)
	m = press(m, "c")
	if !m.editing {
		t.Fatal("expected c on a GitHub thread to open a reply box")
	}
	if m.editor.replyToThread != "T1" {
		t.Fatalf("expected the box aimed at thread T1, got %q", m.editor.replyToThread)
	}
	// The box has to say where this is going: it is the one place in the viewer
	// whose text leaves for a public conversation on save.
	if head := stripANSI(m.editor.view(80)); !strings.Contains(head, "reply on github") {
		t.Fatalf("expected the box to name GitHub:\n%s", head)
	}

	m, cmd := typeAndSave(m, "fixed")
	if cmd == nil {
		t.Fatal("expected saving the reply to produce the post")
	}
	// Filed before it is sent, so a failed post leaves the words somewhere.
	var draft review.Comment
	for _, c := range m.comments {
		if c.ThreadReply() {
			draft = c
		}
	}
	if draft.ID == "" || draft.Body != "fixed" || draft.ReplyToThread != "T1" {
		t.Fatalf("expected the draft filed against the thread, got %+v", draft)
	}

	msg := cmd()
	done, ok := msg.(threadReplyDoneMsg)
	if !ok {
		t.Fatalf("expected a reply outcome, got %T", msg)
	}
	if len(rec.threads) != 1 || rec.threads[0] != "T1" || rec.bodies[0] != "fixed" {
		t.Fatalf("expected one post of \"fixed\" into T1, got %v %v", rec.threads, rec.bodies)
	}
	updated, _ := m.Update(done)
	m = updated.(Model)
	if m.statusErr || !strings.Contains(m.status, "replied") {
		t.Fatalf("expected a posted reply reported, got %q", m.status)
	}
	// Recorded against the comment GitHub created: that id is what stops a later
	// publish sending it again.
	for _, c := range m.comments {
		if !c.ThreadReply() {
			continue
		}
		if c.State != review.Published {
			t.Fatalf("expected the reply marked published, got %q", c.State)
		}
		if c.Publish == nil || c.Publish.ThreadID != "PRRC_new" {
			t.Fatalf("expected GitHub's id recorded, got %+v", c.Publish)
		}
	}
	// And it reads as part of the conversation immediately, rather than after
	// whatever refreshes the mirror next.
	if len(m.threads[0].Comments) != 2 || m.threads[0].Comments[1].Body != "fixed" {
		t.Fatalf("expected the reply appended to the thread, got %+v", m.threads[0].Comments)
	}
}

// The reply must appear exactly once. Both records describe it after a successful
// post — ours and the mirror's — and drawing both is what showed every published
// comment twice before echoedByThread existed.
func TestAPostedReplyIsDrawnOnceNotTwice(t *testing.T) {
	rec := &replyRecorder{id: "PRRC_new"}
	m, _ := threadReplyModel(t, rec)
	m = cursorToComment(t, m)
	m = press(m, "c")
	m, cmd := typeAndSave(m, "fixed")
	updated, _ := m.Update(cmd())
	m = updated.(Model)
	view := stripANSI(m.renderStreamPanel(80, 30))
	if n := strings.Count(view, "fixed"); n != 1 {
		t.Fatalf("expected the reply drawn once, got %d:\n%s", n, view)
	}
}

// A reply GitHub never received must not read as one that landed. It stays, it is
// labelled, and the publish path will offer to send it again.
func TestAFailedReplyIsKeptAndLabelledUnsent(t *testing.T) {
	rec := &replyRecorder{err: errors.New("thread is gone")}
	m, _ := threadReplyModel(t, rec)
	m = cursorToComment(t, m)
	m = press(m, "c")
	m, cmd := typeAndSave(m, "fixed")
	updated, _ := m.Update(cmd())
	m = updated.(Model)
	if !m.statusErr || !strings.Contains(m.status, "thread is gone") {
		t.Fatalf("expected GitHub's own reason reported, got %q", m.status)
	}
	var draft review.Comment
	for _, c := range m.comments {
		if c.ThreadReply() {
			draft = c
		}
	}
	if draft.Body != "fixed" {
		t.Fatalf("the reply was lost with the failed post: %+v", m.comments)
	}
	if draft.State == review.Published || draft.Publish != nil {
		t.Fatalf("a failed post must not be recorded as published: %+v", draft)
	}
	view := stripANSI(m.renderStreamPanel(80, 30))
	if !strings.Contains(view, "unsent") {
		t.Fatalf("expected the reply labelled unsent:\n%s", view)
	}
	if !strings.Contains(view, "fixed") {
		t.Fatalf("expected the reply still shown:\n%s", view)
	}
}

// With no way to post, `c` says so rather than opening a box whose contents have
// nowhere to go.
func TestCOnAThreadSaysWhenReplyingIsUnavailable(t *testing.T) {
	m, _ := threadReplyModel(t, &replyRecorder{})
	m.ReplyToThread = nil
	m = cursorToComment(t, m)
	m = press(m, "c")
	if m.editing {
		t.Fatal("expected no box with nowhere to post it")
	}
	if !strings.Contains(m.status, "unavailable") {
		t.Fatalf("expected the refusal explained, got %q", m.status)
	}
}

// Answering our own posted reply answers the conversation, not the message: one
// exchange, one thread — and here the exchange is the one on the PR.
func TestReplyingToOurOwnThreadReplyStaysOnTheThread(t *testing.T) {
	rec := &replyRecorder{}
	m, _ := threadReplyModel(t, rec)
	m.SetComments([]review.Comment{{
		ID: "local-1", Author: review.AuthorHuman, Body: "an unsent answer", State: review.Open,
		ReplyToThread: "T1",
		Anchor:        review.Anchor{Path: "a.go", Side: review.SideNew, LineHint: 1, Text: "alpha"},
	}})
	m = cursorToCommentID(t, m, "local-1")
	m = press(m, "c")
	if !m.editing {
		t.Fatal("expected a box")
	}
	if m.editor.replyToThread != "T1" {
		t.Fatalf("expected the reply aimed at the thread, got %q", m.editor.replyToThread)
	}
	if m.editor.replyTo != "" {
		t.Fatalf("expected no local parent, got %q", m.editor.replyTo)
	}
}

// Words that are already on the PR cannot be taken back from here. Editing or
// deleting the local record would leave it disagreeing with what everyone else
// reads, and the mirror — which is what gets drawn — would show the original.
func TestAPostedReplyCannotBeEditedOrDeletedLocally(t *testing.T) {
	for _, key := range []string{"i", "D"} {
		t.Run(key, func(t *testing.T) {
			m, _ := threadReplyModel(t, &replyRecorder{})
			var deleted []string
			m.DeleteComment = func(id string) error { deleted = append(deleted, id); return nil }
			m.SetComments([]review.Comment{{
				ID: "local-1", Author: review.AuthorHuman, Body: "already said", State: review.Published,
				ReplyToThread: "T1", Publish: &review.PublishRecord{ThreadID: "PRRC_old"},
				Anchor: review.Anchor{Path: "a.go", Side: review.SideNew, LineHint: 1, Text: "alpha"},
			}})
			m = cursorToCommentID(t, m, "local-1")
			m = press(m, key)
			if m.editing {
				t.Fatal("expected no editor on a reply that is already on GitHub")
			}
			if len(deleted) != 0 {
				t.Fatalf("expected nothing deleted, got %v", deleted)
			}
			if !strings.Contains(m.status, "on github") {
				t.Fatalf("expected the refusal to say where the reply lives, got %q", m.status)
			}
		})
	}
}

// tab cycles the kind in a comment box, and must not pretend to here: a reply's
// published body drops the kind, so the label would promise something the post
// does not carry.
func TestAThreadReplyHasNoKindToCycle(t *testing.T) {
	m, _ := threadReplyModel(t, &replyRecorder{})
	m = cursorToComment(t, m)
	m = press(m, "c")
	before := m.editor.kind
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	if m.editor.kind != before {
		t.Fatalf("expected the kind fixed on a thread reply, got %q", m.editor.kind)
	}
	// And the body that would be posted carries no kind prefix.
	c := review.Comment{Author: review.AuthorHuman, Body: "answer", Kind: review.KindQuestion, ReplyToThread: "T1"}
	if got := c.PublishBody(); got != "answer" {
		t.Fatalf("expected a reply body with no kind prefix, got %q", got)
	}
}

// A body from GitHub arrives with CRLF line endings, and a bare carriage return in
// a rendered row is not a character — it moves the cursor back to the start of the
// line, so the row overwrites itself and every row measured after it lands
// somewhere else. One mirrored thread with Windows line endings mangled the whole
// pane: fragments of the diff, and of the left column, drawn over each other.
func TestCommentRowsCarryNoControlCharacters(t *testing.T) {
	bodies := map[string]string{
		"github crlf": "Values are all confirmed!\r\n\r\n### Monday Request\r\n```\r\n- Over 3 million happy customers\r\n- 102,000 5-star reviews\r\n```",
		"lone cr":     "first\rsecond\rthird",
		"tabs":        "col\tanother\tthird",
		"escape":      "innocent \x1b[41;97m NOT A REAL WARNING \x1b[0m text",
		"nul and bel": "ping\x07 and \x00 gone",
	}
	for name, body := range bodies {
		for _, collapsed := range []bool{false, true} {
			rows := commentRows(review.Comment{Author: review.AuthorHuman, Body: body}, 40, true, collapsed)
			for i, r := range rows {
				for _, ru := range r.text {
					if isControl(ru) {
						t.Errorf("%s (collapsed=%v): row %d carries %q: %q",
							name, collapsed, i, ru, r.text)
					}
				}
			}
		}
	}
}

// The pane's own rows must be clean too, since that is what actually reaches the
// terminal — and a mirrored thread's body goes through one more hop (threadAsComment
// joins every message into one string) before it gets there.
func TestTheRenderedPaneCarriesNoControlCharacters(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.threadVisibility = ThreadsAll
	m.SetThreads([]review.Thread{{
		ID: "T1", Path: "a.go", Side: review.SideNew, Line: 1,
		Comments: []review.ThreadComment{
			{ID: "c1", Author: "alice", Body: "why is this here?\r\n\r\nsee the table\r\n"},
			{ID: "c2", Author: "bob", Body: "because\r\n```\r\n- one\r\n- two\r\n```"},
		},
	}})
	m.threadFold = map[string]bool{"T1": true}
	m.rebuildStream()
	out := stripANSI(m.renderStreamPanel(80, 24))
	for _, ru := range out {
		if isControl(ru) && ru != '\n' {
			t.Fatalf("the rendered pane carries %q:\n%q", ru, out)
		}
	}
}

// displayText keeps the text and drops only what the terminal would act on.
func TestDisplayTextKeepsTheWordsAndDropsTheControls(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"plain", "plain"},
		{"a\r\nb", "a\nb"},
		{"a\rb", "a\nb"},
		{"a\tb", "a    b"},
		{"a\x1b[31mb", "a[31mb"},
		{"a\x00\x07b", "ab"},
		// A body with nothing to fix comes back byte-identical, which is the path
		// almost every comment takes on every frame.
		{"keeps\nnewlines\n", "keeps\nnewlines\n"},
	} {
		if got := displayText(tc.in); got != tc.want {
			t.Errorf("displayText(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Hidden is not the same as absent.
//
// Resolved threads are hidden by default, which is right — settled conversation
// buries what still needs attention. But hiding without saying so means any wrong
// flag reads as a missing comment: a mirror that wrongly believed one thread was
// resolved made three messages of an open conversation simply not exist, on a real
// PR, with nothing on screen to suggest otherwise.
func TestTheIndexSaysHowManyConversationsAreHidden(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.SetThreads([]review.Thread{
		remoteThread("T1", "a.go", 1, false, "still open"),
		remoteThread("T2", "a.go", 2, true, "settled"),
		remoteThread("T3", "a.go", 2, true, "also settled"),
	})
	// The default: unresolved only, so two are held back.
	if got := m.hiddenThreads(); got != 2 {
		t.Fatalf("expected 2 hidden, got %d", got)
	}
	view := stripANSI(m.renderLeftColumn(40, 24))
	if !strings.Contains(view, "2 hidden") {
		t.Fatalf("expected the count in the index header:\n%s", view)
	}
	// And the key that shows them, since a list that merely lacks rows cannot say
	// which keystroke would bring them back.
	if !strings.Contains(view, "T") {
		t.Fatalf("expected the key named:\n%s", view)
	}

	// Cycling to all hides nothing, and the notice goes.
	m.threadVisibility = ThreadsAll
	m.rebuildStream()
	if got := m.hiddenThreads(); got != 0 {
		t.Fatalf("expected nothing hidden with all shown, got %d", got)
	}
	if view := stripANSI(m.renderLeftColumn(40, 24)); strings.Contains(view, "hidden") {
		t.Fatalf("expected no notice when nothing is hidden:\n%s", view)
	}
}

// The count is rendered from a cached string, and `T` changes it. It nearly always
// moves the entry count too, which would have hidden a missing cache key until the
// one arrangement where it does not — so the key is asserted directly.
func TestTheHiddenCountIsInTheLeftColumnsCacheKey(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.SetThreads([]review.Thread{remoteThread("T1", "a.go", 1, true, "settled")})
	before := stripANSI(m.renderLeftColumn(40, 24))
	m.threadVisibility = ThreadsAll
	m.rebuildStream()
	after := stripANSI(m.renderLeftColumn(40, 24))
	if before == after {
		t.Fatalf("the column did not re-render when T changed what is hidden:\n%s", after)
	}
	if strings.Contains(after, "hidden") {
		t.Fatalf("expected the notice gone after showing everything:\n%s", after)
	}
}

// The notice earns the pane even with nothing to list — otherwise the one case
// where every conversation is hidden is also the case with nowhere to say so.
func TestAPaneOfNothingButHiddenThreadsStillSaysSo(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.threadVisibility = ThreadsNone
	m.SetThreads([]review.Thread{remoteThread("T1", "a.go", 1, false, "open")})
	if len(m.commentIndex) != 0 {
		t.Fatalf("fixture is wrong: expected an empty index, got %+v", m.commentIndex)
	}
	view := stripANSI(m.renderLeftColumn(40, 24))
	if !strings.Contains(view, "1 hidden") {
		t.Fatalf("expected the notice with an empty index:\n%s", view)
	}
	// But it is a notice, not a list: tab must not park the keyboard on it.
	if m.commentPaneVisible() {
		t.Fatal("expected the empty index out of the tab rotation")
	}
}

// A conversation reads as a conversation: one header per message, each naming who
// wrote that message.
//
// Flattened into one card the whole thing was attributed to whoever spoke first,
// with later authors marked inline on the first line of their own remark — so a
// multi-line reply ran on under the previous speaker and a reply you had just
// posted arrived crammed onto the end of somebody else's.
func TestEveryMessageOfAThreadIsHeadedByItsOwnAuthor(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.threadVisibility = ThreadsAll
	m.SetThreads([]review.Thread{{
		ID: "T1", Path: "a.go", Side: review.SideNew, Line: 1,
		Comments: []review.ThreadComment{
			{ID: "c1", Author: "alice", Body: "why is this here?\n\nsecond paragraph"},
			{ID: "c2", Author: "bob", Body: "because of X"},
			{ID: "c3", Author: "you", Body: "ok"},
		},
	}})
	view := stripANSI(m.renderStreamPanel(90, 40))
	for _, want := range []string{"alice", "bob", "you"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q named in the conversation:\n%s", want, view)
		}
	}
	// No inline attribution: that is what a header is for, and it only ever marked
	// the first line of a message anyway.
	for _, unwanted := range []string{"bob: because", "you: ok"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("expected no inline author prefix, found %q:\n%s", unwanted, view)
		}
	}
	// Each message's text still under its own header, in order.
	iAlice := strings.Index(view, "alice")
	iBob := strings.Index(view, "bob")
	iOK := strings.Index(view, " ok")
	if iAlice >= iBob || iBob >= iOK {
		t.Fatalf("expected the conversation in order, got alice=%d bob=%d ok=%d:\n%s",
			iAlice, iBob, iOK, view)
	}
	// One entry in the index, not three: jumping to a reply and to the remark it
	// answers are the same jump.
	if len(m.commentIndex) != 1 {
		t.Fatalf("expected one index entry for the conversation, got %d", len(m.commentIndex))
	}
	if m.commentIndex[0].replies != 2 {
		t.Fatalf("expected 2 replies counted, got %d", m.commentIndex[0].replies)
	}
}

// "published" is our word for one of *our* comments having reached GitHub. Every
// message of a GitHub thread is there by definition, and the card's own header says
// `github` — so stamping it on each row repeated the least interesting fact about
// the conversation on every line of it.
func TestMirroredMessagesDoNotClaimOurPublishedState(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.threadVisibility = ThreadsAll
	m.SetThreads([]review.Thread{{
		ID: "T1", Path: "a.go", Side: review.SideNew, Line: 1,
		Comments: []review.ThreadComment{
			{ID: "c1", Author: "alice", Body: "why?"},
			{ID: "c2", Author: "bob", Body: "because"},
		},
	}})
	view := stripANSI(m.renderStreamPanel(90, 40))
	if strings.Contains(view, "published") {
		t.Fatalf("expected no published chip on a mirrored conversation:\n%s", view)
	}
	// The marker that says where it lives is still there.
	if !strings.Contains(view, "github") {
		t.Fatalf("expected the github marker kept:\n%s", view)
	}
}
