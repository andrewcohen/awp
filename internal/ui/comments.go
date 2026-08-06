package ui

import (
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/andrewcohen/awp/internal/charm"
	"github.com/andrewcohen/awp/internal/diff"
	"github.com/andrewcohen/awp/internal/review"
)

// Comments in the diff stream.
//
// A comment is stored against content (path + line text + context), never
// against a row index, so placing it means *locating* it in the current diff.
// That is the same job restoring the cursor does across a reload, and it uses
// the same ladder — see anchor.go. A comment that cannot be located is not
// dropped; it becomes orphaned and is still shown, because silently hiding
// something a reviewer wrote is the worst failure this surface has.

// CommentSink is how the viewer persists a comment the user just wrote. The
// viewer never touches the filesystem itself, so it stays testable and the
// storage decision stays in one place.
type CommentSink func(review.Comment) error

// CommentDeleter removes a comment by id.
type CommentDeleter func(id string) error

// A comment adapted from a mirrored GitHub thread is GitHub's record — other
// people's words — so it is excluded from anything that treats a comment as ours:
// editing, deleting, and the robot marker. `review.Comment.Mirrored` is the one
// place that answers which it is, and review owns the id scheme that lets it (see
// RemoteThreadID there).

// localCommentAtCursor is the comment under the cursor, if it is one of ours.
// Remote GitHub threads are excluded: they are GitHub's records, and editing or
// deleting them from here would be a lie about what happened.
func (m Model) localCommentAtCursor() (review.Comment, bool) {
	if len(m.stream.rows) == 0 || m.cursorRow >= len(m.stream.rows) {
		return review.Comment{}, false
	}
	r := m.stream.rows[m.cursorRow]
	if !isCommentRow(r.kind) {
		return review.Comment{}, false
	}
	if r.comment < 0 || r.comment >= len(m.stream.comments) {
		return review.Comment{}, false
	}
	c := m.stream.comments[r.comment]
	if c.Mirrored() {
		return review.Comment{}, false
	}
	// Resolve against the live set: the placed copy is a snapshot.
	for _, own := range m.comments {
		if own.ID == c.ID {
			return own, true
		}
	}
	return review.Comment{}, false
}

// onGitHubRefusal is why `i` and `D` say no to a record that has left.
//
// Both keys refuse the same set (review.Comment.Mutable) and want to say the same
// thing about it, so they say it in one place — the two used to disagree about
// which records were even refusable, and shared wording is the cheapest way to
// keep them from drifting apart again. It names the record, because "that reply"
// pointed at a finding reads as the wrong row and sends the reader hunting for a
// reply they never wrote.
func onGitHubRefusal(c review.Comment, verb string) string {
	what := "comment"
	if c.ThreadReply() {
		what = "reply"
	}
	return fmt.Sprintf("that %s is on github — %s it there", what, verb)
}

// deleteCommentAtCursor removes the comment under the cursor.
func (m Model) deleteCommentAtCursor() (tea.Model, tea.Cmd) {
	if _, isThread := m.threadAtCursor(); isThread {
		m.status = "that is a GitHub thread — resolve it with R instead"
		return m, nil
	}
	c, ok := m.localCommentAtCursor()
	if !ok {
		m.status = "put the cursor on one of your comments to delete it"
		return m, nil
	}
	if !c.Mutable() {
		// Deleting the record would not delete anything on the PR, and the mirror would
		// go on drawing it — so this would look like a delete that did nothing, while
		// quietly losing our own record of having said it.
		m.status = onGitHubRefusal(c, "delete")
		return m, nil
	}
	if m.DeleteComment == nil {
		m.status = "deleting unavailable here"
		return m, nil
	}
	// Resolve the closure before deleting, while the set still holds the replies.
	doomed := make(map[string]bool)
	for _, id := range review.CommentAndReplies(m.comments, c.ID) {
		doomed[id] = true
	}
	if err := m.DeleteComment(c.ID); err != nil {
		m.fail("delete: %v", err)
		return m, nil
	}
	// Prune the same closure the store removed. Dropping only the parent would
	// leave its replies here until the next reload, and Threads would show each of
	// them as a conversation in its own right in the meantime.
	kept := make([]review.Comment, 0, len(m.comments))
	for _, own := range m.comments {
		if !doomed[own.ID] {
			kept = append(kept, own)
		}
	}
	m.comments = kept
	// Removing rows can leave the cursor past the end.
	m.rebuildStream()
	m.clampCursor()
	m.followCursor()
	m.syncFileCursorToCursor()
	if n := len(doomed) - 1; n > 0 {
		// Say how many replies went with it: a cascade that deleted more than the
		// reviewer pointed at has to report what it took.
		m.status = fmt.Sprintf("comment and %d repl%s deleted", n, map[bool]string{true: "y", false: "ies"}[n == 1])
	} else {
		m.status = "comment deleted"
	}
	return m, nil
}

// ThreadVisibility controls which remote threads are shown. Resolved threads are
// hidden by default: they are settled conversation, and showing them by default
// buries the ones that still need attention.
type ThreadVisibility int

const (
	ThreadsUnresolved ThreadVisibility = iota
	ThreadsAll
	ThreadsNone
)

func (v ThreadVisibility) String() string {
	switch v {
	case ThreadsAll:
		return "all threads"
	case ThreadsNone:
		return "threads hidden"
	default:
		return "unresolved threads"
	}
}

// ThreadResolver toggles a remote thread's resolved state on GitHub. Nil leaves
// resolving unavailable, which the viewer reports rather than silently ignoring.
type ThreadResolver func(threadID string, resolve bool) error

// ThreadReplier posts a reply into a remote thread and returns the id of the
// comment GitHub created.
//
// The id is what the reply is recorded against, and it is not optional: it is how
// the local record recognises itself in the mirror. Without it the same reply
// would be drawn twice, once as ours and once as GitHub's copy of it.
type ThreadReplier func(threadID, body string) (string, error)

// threadAtCursor is the remote thread the cursor is on, if any. Resolving acts on
// the thread under the cursor rather than a separate selection, so there is only
// ever one notion of "this one".
func (m Model) threadAtCursor() (review.Thread, bool) {
	if len(m.stream.rows) == 0 || m.cursorRow >= len(m.stream.rows) {
		return review.Thread{}, false
	}
	r := m.stream.rows[m.cursorRow]
	if !isCommentRow(r.kind) {
		return review.Thread{}, false
	}
	if r.comment < 0 || r.comment >= len(m.stream.comments) {
		return review.Thread{}, false
	}
	return m.threadFor(m.stream.comments[r.comment].ID)
}

// threadFor recovers the mirrored thread an adapted comment came from, by the id
// threadAsComment prefixed. Returns false for a local comment.
//
// The stream holds remote threads as ordinary comments so one renderer covers
// both, which means a thread's state survives only in its display label. Anything
// that needs the state itself — resolving, and the index's chips — comes back
// through here rather than reading that label back apart.
func (m Model) threadFor(commentID string) (review.Thread, bool) {
	id, ok := review.ThreadIDOf(commentID)
	if !ok {
		return review.Thread{}, false // a local comment, not a remote thread
	}
	for _, t := range m.threads {
		if t.ID == id {
			return t, true
		}
	}
	return review.Thread{}, false
}

// toggleResolved resolves or unresolves the thread under the cursor.
func (m Model) toggleResolved() (tea.Model, tea.Cmd) {
	t, ok := m.threadAtCursor()
	if !ok {
		// Worded for the pane the key was pressed in: from the index there is no
		// cursor to move, only a selection that is a local comment — which has
		// nothing to resolve, since resolving is a thing GitHub records.
		if m.focus == FocusComments {
			m.status = "only a GitHub thread can be resolved"
		} else {
			m.status = "put the cursor on a GitHub thread to resolve it"
		}
		return m, nil
	}
	if m.ResolveThread == nil {
		m.status = "resolving unavailable here"
		return m, nil
	}
	want := !t.Resolved
	if err := m.ResolveThread(t.ID, want); err != nil {
		m.fail("resolve: %v", err)
		return m, nil
	}
	for i := range m.threads {
		if m.threads[i].ID == t.ID {
			m.threads[i].Resolved = want
		}
	}
	if want {
		m.status = "thread resolved"
	} else {
		m.status = "thread reopened"
	}
	m.rebuildStream()
	m.clampCursor()
	m.followCursor()
	return m, nil
}

// threadReplyDoneMsg is what came back from posting a reply. localID is the
// record it was filed as, so the outcome can be recorded against it.
type threadReplyDoneMsg struct {
	localID   string
	threadID  string
	commentID string
	err       error
	// sent is whether the reply also went to the agent, which is ctrl+s rather than
	// enter. Carried through the round trip so the line the reader is left with
	// confirms both halves. Without it the final status said only "replied on
	// github", which is how a send-to-agent that never happened at all went
	// unnoticed: the confirmation for the half that worked was also the whole
	// confirmation.
	sent bool
}

// replyToThreadCmd posts off the update loop. A gh round trip is most of a second,
// and doing it inline stops the view redrawing and takes the keyboard with it.
func replyToThreadCmd(post ThreadReplier, localID, threadID, body string, sent bool) tea.Cmd {
	return func() tea.Msg {
		id, err := post(threadID, body)
		return threadReplyDoneMsg{localID: localID, threadID: threadID, commentID: id, err: err, sent: sent}
	}
}

// postThreadReply files the reply, then sends it.
//
// Filed first, always, and that order is the point: a post can fail — the thread
// can be gone, the token can be stale, the network can be down — and a reply that
// existed only in a text area would be gone with it. On disk first, the failure is
// a status line and a draft you can send again.
// alsoSend is ctrl+s rather than enter: the reply goes to the PR either way, and
// additionally to the workspace's agent. Passed in rather than checked by the
// caller after the fact, because the comment the agent needs is the one the store
// assigned an id to — which only exists inside here.
func (m Model) postThreadReply(c review.Comment, alsoSend bool) (tea.Model, tea.Cmd) {
	if m.ReplyToThread == nil {
		m.fail("replying to GitHub threads unavailable here")
		return m, nil
	}
	if c.ID != "" {
		// Revising a reply that never went out. Replaced rather than appended, the same
		// as revising any other comment.
		if m.UpdateComment == nil {
			m.fail("editing unavailable here")
			return m, nil
		}
		if err := m.UpdateComment(c); err != nil {
			m.fail("reply: %v", err)
			return m, nil
		}
		for i := range m.comments {
			if m.comments[i].ID == c.ID {
				m.comments[i].Body = c.Body
			}
		}
	} else {
		if err := m.SaveComment(c); err != nil {
			m.fail("reply: %v", err)
			return m, nil
		}
		// The store assigns the id, and the outcome has to be recorded against it. With
		// no way to read it back the reply still posts; it just cannot be marked
		// published from here, and the next reload picks up the record as written.
		if m.LastSavedComment != nil {
			if saved, ok := m.LastSavedComment(); ok {
				c = saved
			}
		}
		m.comments = append(m.comments, c)
	}
	m.rebuildStream()
	m.clampCursor()
	m.followCursor()
	m.status = "posting the reply…"
	m.statusErr = false
	// Before the post, not after it: the post is a tea.Cmd that finishes later, so
	// doing this on its way back would make sending to the agent depend on GitHub
	// accepting the reply — and the reason to hand a reply to the agent is usually
	// that it has work in it, which is true whether or not the PR took the message.
	sentToAgent := false
	if alsoSend {
		if m.SendComment == nil {
			m.status = "posting the reply… (sending to the agent unavailable here)"
		} else if err := m.SendComment(c); err != nil {
			// Through fail() like every other failure in this surface, so the reason
			// reaches the log rather than only a status line the post's own answer is
			// about to overwrite. It names the half that failed: the reply is still on
			// its way, and "reply failed" would be the wrong thing to have believed.
			m.fail("the reply is posting, but sending it to the agent failed: %v", err)
		} else {
			sentToAgent = true
			m.status = "posting the reply… and sent to the agent"
		}
	}
	// PublishBody rather than the stored text, so a reply carries the same markers
	// every other published body does — an agent's 🤖 above all, since this posts
	// under the authenticated user's account.
	return m, replyToThreadCmd(m.ReplyToThread, c.ID, c.ReplyToThread, c.PublishBody(), sentToAgent)
}

// applyThreadReplyDone records what GitHub did with the reply.
func (m Model) applyThreadReplyDone(msg threadReplyDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		// The draft stays exactly where it is, labelled unsent (see commentRows), and
		// `P` will offer to send it again.
		m.fail("reply: %v", msg.err)
		return m, nil
	}
	var posted review.Comment
	for i := range m.comments {
		if m.comments[i].ID != msg.localID {
			continue
		}
		m.comments[i].State = review.Published
		// Against the comment GitHub created. This id is what stops a later publish
		// sending the reply a second time, and what lets the mirror recognise these
		// words as ours.
		m.comments[i].Publish = &review.PublishRecord{ThreadID: msg.commentID, At: time.Now()}
		posted = m.comments[i]
	}
	m.status = "replied on github"
	if msg.sent {
		m.status = "replied on github and sent to the agent"
	}
	m.statusErr = false
	// Through RecordPublished, not UpdateComment: a revision carries a body and the
	// store keeps the state it already had, so sending this through there wrote the
	// reply back exactly as unsent — which is what a reply already sitting on the PR
	// then claimed to be, in the diff, indefinitely.
	if m.RecordPublished != nil && posted.ID != "" {
		if err := m.RecordPublished(posted.ID, msg.commentID); err != nil {
			// It posted; only the record does not say so. Worth reporting rather than
			// swallowing: the next publish would read that record and send it again.
			m.fail("replied, but the record didn't save: %v", err)
		}
	}
	// Appended to the mirrored conversation so the reply reads as part of it now,
	// rather than after whatever refreshes the mirror next — the same reason
	// resolving writes the new state locally instead of refetching.
	for i := range m.threads {
		if m.threads[i].ID != msg.threadID {
			continue
		}
		m.threads[i].Comments = append(m.threads[i].Comments, review.ThreadComment{
			// "you" for the same reason a comment of ours is labelled that way. The next
			// mirror refresh replaces it with the login GitHub reports.
			ID: msg.commentID, Author: "you", Body: posted.PublishBody(),
		})
	}
	m.rebuildStream()
	m.clampCursor()
	m.followCursor()
	return m, nil
}

// SetThreads installs the mirrored remote threads.
func (m *Model) SetThreads(ts []review.Thread) {
	m.threads = ts
	m.rebuildStream()
}

// cycleThreadVisibility steps unresolved → all → none.
//
// Pane-independent: which conversations the diff shows is a property of the
// view, not of whichever list happens to hold the keyboard. Reaching it only
// from the diff pane meant scanning the comment index — the surface the setting
// changes most — and having to tab away to change it.
// The rebuild covers the rest: it re-places every conversation, rebuilds the
// index, and re-clamps both cursors — which matters here because cycling to
// "none" can empty the index while it holds focus.
func (m *Model) cycleThreadVisibility() {
	m.threadVisibility = (m.threadVisibility + 1) % 3
	m.status = m.threadVisibility.String()
	m.rebuildStream()
}

// hiddenThreads is how many mirrored conversations the current visibility is
// keeping off screen.
//
// Reported rather than left to be noticed, because the alternative is what actually
// happened: a thread the mirror wrongly believed was resolved vanished from the
// diff, and a conversation sitting open on the PR — three messages of it — read as
// simply not existing. Hiding settled conversation is the right default; hiding it
// without saying so turns any wrong flag, any stale mirror, into missing comments.
func (m Model) hiddenThreads() int {
	return len(m.threads) - len(m.visibleThreads())
}

// visibleThreads is the thread set the current visibility admits.
func (m Model) visibleThreads() []review.Thread {
	if m.threadVisibility == ThreadsNone || len(m.threads) == 0 {
		return nil
	}
	if m.threadVisibility == ThreadsAll {
		return m.threads
	}
	out := make([]review.Thread, 0, len(m.threads))
	for _, t := range m.threads {
		if !t.Resolved {
			out = append(out, t)
		}
	}
	return out
}

// The two states GitHub reports about a thread, named the way they are shown.
// Settled and stale are independent — a thread can be both, and usually is, since
// resolving a point is often what precedes the code moving out from under it.
const (
	chipResolved = "resolved"
	chipOutdated = "outdated"
)

// The two things a proposal can say about itself. A pending one is the message in
// a change that is waiting on *you* — the agent has stopped — so it has to be
// legible without opening the conversation, and an approved one has to read
// differently or a proposal you already answered goes on looking live.
//
// "awaiting approval" rather than "awaiting your ok": the CLI already prints that
// phrase when a proposal is filed, and a chip that renamed it would make the two
// surfaces look like they were describing different states.
const (
	chipAwaitingApproval = "awaiting approval"
	chipApproved         = "approved"
)

// proposalChip is what a comment's header says about its proposal, empty for
// everything that is not one.
func proposalChip(c review.Comment) string {
	switch {
	case c.AwaitingApproval():
		return chipAwaitingApproval
	case c.Approved():
		return chipApproved
	default:
		return ""
	}
}

// remoteThreadLabel is the author label a mirrored thread renders under: where it
// came from, then whatever GitHub says about it.
//
// Both chips, not the first that applies. This was an if/else, which meant a
// resolved thread never admitted to being outdated — so the nine settled,
// stale threads on a real PR were indistinguishable from settled ones still
// pointing at live code.
func remoteThreadLabel(t review.Thread) string {
	label := threadSource
	if t.Resolved {
		label += " · " + chipResolved
	}
	if t.Outdated {
		label += " · " + chipOutdated
	}
	return label
}

// threadSource is the chip that marks a message as GitHub's record rather than one
// of ours. On every message of a conversation, not only its first: a card can be
// several screens long, and "whose words are these, and where do they live" is the
// question a reader asks at the message they are looking at rather than at the one
// that happened to open the thread.
const threadSource = "github"

// Fold glyphs for a mirrored thread, pointing the way the row will move.
const (
	foldClosed = "▶"
	foldOpen   = "▼"
)

// threadCollapsed reports whether a mirrored thread renders as one summary line.
//
// Resolved threads start folded, unresolved ones open: a settled conversation is
// reference material until you go looking at it, while an open one is the reason
// you are reading. On a real PR that is the difference between 529 rows of
// conversation and sixteen — 43% of that diff was prose you had already dealt
// with, and holding j through it is what "slow scrolling" turned out to be.
//
// An explicit toggle wins over the default, and only for as long as the view is
// open: how you left a fold is a reading position, not a property of the review,
// and persisting it would mean a thread you opened once stays open forever.
func (m Model) threadFolded(t review.Thread) bool {
	if expanded, ok := m.threadFold[t.ID]; ok {
		return !expanded
	}
	return t.Resolved
}

// threadCollapsed answers the same question about an adapted comment, which is
// the form the row builders see.
func (m Model) threadCollapsed(c review.Comment) bool {
	t, ok := m.threadFor(c.ID)
	if !ok {
		// A local comment is the reviewer's own working set, always open. Folding
		// what you are in the middle of writing about would be perverse.
		return false
	}
	return m.threadFolded(t)
}

// threadHeaderLabel is a mirrored thread's header: which way it folds, where it
// came from, and what GitHub says about it — plus, when folded, how much is
// inside, since that is the one thing you lose by closing it.
//
// The glyph is on both states, not only the closed one: it is the affordance
// that says enter does something here.
//
// The count is messages, not replies, so a thread of one reads "1 msg" rather
// than claiming a reply it does not have.
func threadHeaderLabel(t review.Thread, folded bool) string {
	// The author leads, the way a local comment's header reads "you · published".
	// Who wrote a remark is header material: it was inside the body — the first
	// body line read "alice: this leaks" under a header that said only "github" —
	// which put the one thing you scan for in the one place you do not scan.
	label := remoteThreadLabel(t)
	if a := threadAuthor(t); a != "" {
		label = a + " · " + label
	}
	if !folded {
		return foldOpen + " " + label
	}
	return fmt.Sprintf("%s %s · %d msg%s",
		foldClosed, label, len(t.Comments), plural(len(t.Comments)))
}

// threadAuthor is who opened the thread, empty when GitHub reported no author
// (a deleted account).
func threadAuthor(t review.Thread) string {
	if len(t.Comments) == 0 {
		return ""
	}
	return strings.TrimSpace(t.Comments[0].Author)
}

// toggleThreadFold opens or closes the mirrored thread under the cursor.
func (m Model) toggleThreadFold() (tea.Model, tea.Cmd) {
	t, ok := m.threadAtCursor()
	if !ok {
		// Silent: enter on a line of code is not a mistake, and the diff has no
		// other meaning for it.
		return m, nil
	}
	if m.threadFold == nil {
		m.threadFold = map[string]bool{}
	}
	// Store what it is moving *to* as an explicit override, so the fold survives
	// the thread's resolved state changing under it — resolving a thread you
	// deliberately opened should not close it.
	m.threadFold[t.ID] = m.threadFolded(t)
	m.rebuildStream()
	return m, nil
}

// threadAsComments adapts a remote thread into the display shape local comments use
// — one comment per message — so one renderer covers both.
//
// Per message, not one comment holding the whole conversation. That was the shape
// here, and it flattened a thread into a single card headed by whoever spoke first,
// with every later author named inline on the first line of their own remark:
//
//	▼ CoWorker · github · published
//	[NOTE] On the naming question: keep `includesBotTraffic`.
//	- It reads correctly at the declaration and describes the population…
//	andrewcohen: Keeping `includesBotTraffic`. Agreed on the reasoning…
//	population rather than the mechanism is what makes it survive…
//	andrewcohen: ok
//
// Which reads as one person saying all of it. The prefix marks only the first line,
// so a multi-line reply's remaining lines run on under the previous speaker, and a
// reply you just posted arrives crammed onto the end of somebody else's remark. The
// old note here said the header could carry only one author — true of one comment,
// which is why there is now one per message.
//
// Replies carry ReplyTo, so everything downstream already handles them: placement
// keeps a conversation together under one row, the comment index folds them into
// their parent as a count, and the block renders as one card with a bar-only row
// between messages.
func (m Model) threadAsComments(t review.Thread) []review.Comment {
	folded := m.threadFolded(t)
	parent := review.Comment{
		ID:     review.RemoteThreadID(t.ID),
		Author: threadHeaderLabel(t, folded),
		State:  review.Published,
		Anchor: threadAnchor(t),
	}
	if len(t.Comments) > 0 {
		parent.Body = t.Comments[0].Body
	}
	// Folded is one row for the whole thread, and its label already carries the
	// message count — so the later messages are not adapted at all rather than being
	// adapted and then hidden.
	if folded || len(t.Comments) <= 1 {
		return []review.Comment{parent}
	}
	out := make([]review.Comment, 0, len(t.Comments))
	out = append(out, parent)
	for i, c := range t.Comments[1:] {
		out = append(out, review.Comment{
			// Indexed off the thread, so every message has a stable id that still names
			// the thread it belongs to — which is what threadFor reads back to answer
			// "resolve what?" from any row of the conversation.
			ID:      review.RemoteMessageID(t.ID, i+1),
			Author:  strings.TrimSpace(c.Author) + " · " + threadSource,
			Body:    c.Body,
			State:   review.Published,
			Anchor:  parent.Anchor,
			ReplyTo: parent.ID,
		})
	}
	return out
}

// threadAnchor is where a thread sits, in our own vocabulary.
//
// One spelling, because two things need it and they must agree: the adapted
// comment a thread is drawn as, and a reply filed against it. A reply anchored
// anywhere else would be relocated to a different line than the conversation it
// answers the moment it had to be drawn from our own record.
func threadAnchor(t review.Thread) review.Anchor {
	return review.Anchor{Path: t.Path, Side: t.Side, LineHint: t.Line}
}

// threadCarriesOurReply reports whether the mirror already holds this reply of
// ours.
//
// Matched on the id GitHub gave the reply when it was posted, which is the same id
// the mirror reports for it — the same match echoedByThread makes for a published
// comment, and for the same reason: after a successful post both records describe
// one message, and drawing both shows the reply twice.
//
// GitHub's copy is the one to keep. It sits inside the conversation, in order,
// where a reader expects to find an answer.
func (m Model) threadCarriesOurReply(c review.Comment) bool {
	id, ok := c.PublishedThreadID()
	if !ok {
		return false
	}
	for _, t := range m.threads {
		if t.ID != c.ReplyToThread {
			continue
		}
		for _, tc := range t.Comments {
			if tc.ID == id {
				return true
			}
		}
	}
	return false
}

// echoedByThread maps a local comment's id to the display id of the mirrored
// GitHub thread that is the same conversation.
//
// A comment published from here comes back through the mirror as a thread, so
// after a publish both records describe one conversation — and rendering both
// showed every published comment twice, once as the local record and once as
// `▶ github · 1 msg · you: 🤖 Suggestion: …` directly beneath it.
//
// The mirrored copy is the one to keep. It is GitHub's record of the same words,
// and it carries what the local one cannot know: whether the thread has been
// resolved, whether the code moved out from under it, and any reply that arrived
// after we posted. The local record stays in the store either way — it is what
// makes a re-publish skip rather than duplicate — it just stops being drawn.
//
// Matched on GitHub's comment node id, which both sides hold: the id
// addPullRequestReview returns for a comment it creates is the id the mirror
// reports for that same comment. Body and line were the alternatives and both
// drift — GitHub recomputes a thread's line as the PR moves (a comment filed
// against line 47 came back reported at 53), and editing a published comment
// locally changes its body.
func echoedByThread(comments []review.Comment, threads []review.Thread) map[string]string {
	// Parents only: a reply is never published, so it has no id to match on and
	// belongs to whichever copy of the conversation ends up being drawn.
	published := make(map[string]string, len(comments))
	for _, c := range comments {
		if c.ReplyTo != "" {
			continue
		}
		if id, ok := c.PublishedThreadID(); ok {
			published[id] = c.ID
		}
	}
	if len(published) == 0 {
		return nil
	}
	out := make(map[string]string)
	for _, t := range threads {
		for _, tc := range t.Comments {
			// An empty id is a mirror written before the ids were carried: it says
			// nothing, so it is not treated as a match. The next mirror refresh fills
			// it in.
			if tc.ID == "" {
				continue
			}
			if localID, ok := published[tc.ID]; ok {
				out[localID] = review.RemoteThreadID(t.ID)
				break
			}
		}
	}
	return out
}

// SetComments replaces the comment set and rebuilds the stream so they appear
// in place.
func (m *Model) SetComments(cs []review.Comment) {
	m.comments = cs
	m.rebuildStream()
}

// Comments returns the current comment set.
func (m Model) Comments() []review.Comment { return m.comments }

// placeComments resolves each comment to the stream row it belongs under, and
// sorts the rest into the two sections that stand apart from the diff: remarks
// about the change as a whole, and remarks whose anchor is gone.
//
// Called during the geometry pass, so it must not render anything.
func (m Model) placeComments(rows []rowRef) commentPlacement {
	// Remote threads render through the same path as local comments; they differ
	// in their label, not in how they are placed or anchored. Their line numbers
	// are GitHub's, against a particular commit, so they drift exactly the way
	// ours do and want the same relocation ladder.
	// Group into conversations first, then place each parent followed by its
	// replies. Placing replies independently would scatter an exchange across the
	// diff wherever each message's anchor happened to resolve.
	all := make([]review.Comment, 0, len(m.comments))
	threads := m.visibleThreads()
	// A comment published from here is drawn as the mirrored thread it produced,
	// not twice (see echoedByThread). Reconciled against the threads actually being
	// shown rather than every mirrored one, so cycling `T` to none does not hide a
	// remark of your own along with GitHub's conversation.
	echoed := echoedByThread(m.comments, threads)
	// deferred holds the replies of a local comment whose conversation is being
	// drawn from the mirror, keyed by that thread's display id. They are ours alone
	// — a reply is never published — so they move onto the thread rather than being
	// dropped with the parent, which would strand an exchange with the agent.
	var deferred map[string][]review.Comment
	// onto moves a comment of ours into a mirrored conversation, re-parented onto
	// that thread's display id so placement puts it under the thread's row rather
	// than wherever its own anchor happens to resolve.
	onto := func(displayID string, c review.Comment) {
		if deferred == nil {
			deferred = make(map[string][]review.Comment, len(threads))
		}
		c.ReplyTo = displayID
		deferred[displayID] = append(deferred[displayID], c)
	}
	// shown is the conversations on screen, so a reply can ask whether the thread it
	// answers is one of them.
	shown := make(map[string]bool, len(threads))
	for _, t := range threads {
		shown[t.ID] = true
	}
	for _, th := range review.Threads(m.comments) {
		if th.Parent.ThreadReply() {
			// Posted, and the mirror has it: GitHub's copy is drawn, ours is not. That
			// includes not drawing it when `T` has hidden the conversation — once these
			// words are on the PR they are part of it, and "hide GitHub's threads" has to
			// mean this one too.
			if m.threadCarriesOurReply(th.Parent) {
				continue
			}
			if shown[th.Parent.ReplyToThread] {
				onto(review.RemoteThreadID(th.Parent.ReplyToThread), th.Parent)
				for _, reply := range th.Replies {
					onto(review.RemoteThreadID(th.Parent.ReplyToThread), reply)
				}
				continue
			}
			// The thread it answers is not on screen — hidden, or gone from the mirror —
			// and this reply has not reached GitHub. Fall through, so it is drawn against
			// the line it is about: a reply of ours that nobody has received is the last
			// thing that should quietly disappear.
		}
		if displayID, ok := echoed[th.Parent.ID]; ok {
			for _, reply := range th.Replies {
				onto(displayID, reply)
			}
			continue
		}
		all = append(all, th.Parent)
		for _, reply := range th.Replies {
			// A reply displays in the thread's kind, not its own. The whole
			// conversation renders as one card sharing one left bar, and a reply
			// with a different kind would break that edge into two colours
			// mid-block. The kind describes what the exchange is about, which is
			// set by the remark that opened it — the same reason a published reply
			// omits the "(kind) - " prefix its parent carries.
			reply.Kind = th.Parent.Kind
			all = append(all, reply)
		}
	}
	for _, t := range threads {
		messages := m.threadAsComments(t)
		all = append(all, messages...)
		// Ours go last, after everything GitHub has: a reply is the newest thing in the
		// conversation, and the block closes on whichever message comes last.
		for _, reply := range deferred[messages[0].ID] {
			reply.Kind = messages[0].Kind
			all = append(all, reply)
		}
	}
	if len(all) == 0 {
		return commentPlacement{}
	}
	out := commentPlacement{byRow: make(map[int][]review.Comment, len(all))}
	// A reply goes wherever its parent went, so a thread stays intact even if the
	// reply's own anchor would resolve elsewhere (or nowhere). Review-level parents
	// need their own set: they have no row for parentRow to record.
	parentRow := make(map[string]int, len(all))
	reviewParent := make(map[string]bool)
	for _, c := range all {
		if c.ReplyTo != "" {
			if row, ok := parentRow[c.ReplyTo]; ok {
				out.byRow[row] = append(out.byRow[row], c)
				continue
			}
			if reviewParent[c.ReplyTo] {
				out.review = append(out.review, c)
				continue
			}
			out.orphans = append(out.orphans, c)
			continue
		}
		if reviewLevel(c) {
			reviewParent[c.ID] = true
			out.review = append(out.review, c)
			continue
		}
		if row, ok := m.locateComment(rows, c); ok {
			out.byRow[row] = append(out.byRow[row], c)
			parentRow[c.ID] = row
			continue
		}
		out.orphans = append(out.orphans, c)
	}
	return out
}

// reviewLevel reports whether a comment is about the change as a whole rather
// than about a line: it names no file at all.
//
// Distinct from an unplaceable comment, which names a file that no longer holds
// it. Conflating the two would file a deliberate summary remark under "their
// anchor could not be found", which reads as a failure rather than as the thing
// the author meant.
func reviewLevel(c review.Comment) bool {
	return c.Anchor.Scope() == review.ChangeScope
}

// collapsedFileRow is the divider row of a folded file at the given path, if the
// change still holds that file and it is folded. The handle a comment attaches to
// while the lines it was written against are hidden.
func collapsedFileRow(rows []rowRef, files []diff.FileDiff, path string) (int, bool) {
	row, ok := fileHeaderRow(rows, files, path)
	if !ok || !rows[row].collapsed {
		return 0, false
	}
	return row, true
}

// fileHeaderRow is the divider row for a path, folded or not. The row a remark
// about the file as a whole hangs under, and the one a folded file's line
// comments fall back to.
func fileHeaderRow(rows []rowRef, files []diff.FileDiff, path string) (int, bool) {
	for i, r := range rows {
		if r.kind != rowFileHeader {
			continue
		}
		if r.file >= 0 && r.file < len(files) && pathOf(files[r.file]) == path {
			return i, true
		}
	}
	return 0, false
}

// aboutTheWholeFile reports whether a comment is a remark about a file rather
// than about a place in it — the one that belongs on the file's divider.
//
// A path with no line is not enough to tell, which is the trap here. An outdated
// GitHub thread has exactly that shape for the opposite reason: it is a remark
// about a specific line that the change has since removed, and GitHub reports its
// line as null. Placing one on the divider would present a settled conversation
// about vanished code as a standing comment about the whole file, which is a claim
// nobody made — so those keep going to the detached section, labelled outdated
// (see the thread-visibility rules).
//
// A mirrored thread that is *not* outdated and still has no line is genuinely
// file-level on GitHub's side (subject_type=file), so it does belong on the
// divider. Outdated is the discriminator, not local-versus-remote.
func (m Model) aboutTheWholeFile(c review.Comment) bool {
	if c.Anchor.Scope() != review.FileScope {
		return false
	}
	// Our own reply into a GitHub thread inherits that thread's anchor rather than
	// choosing a scope, so a reply to a thread GitHub reports no line for looks
	// file-scoped and is not: it is an answer inside a conversation, and it belongs
	// with the conversation. Its own case because the outdated check below cannot see
	// it — threadFor keys off the comment's id, and this comment's id is ours.
	if c.ThreadReply() {
		return false
	}
	if t, ok := m.threadFor(c.ID); ok && t.Outdated {
		return false
	}
	return true
}

// locateComment finds the row a comment attaches to.
//
// A range comment hangs under the last line it covers rather than the first,
// which is where GitHub puts one and how it reads: everything above the remark is
// what the remark is about. Its start is what gets located — that is the end with
// recorded context — and the end is then found forward from there.
func (m Model) locateComment(rows []rowRef, c review.Comment) (int, bool) {
	start, ok := m.locateAnchorStart(rows, c)
	if !ok || !c.Anchor.Multiline() || rows[start].kind != rowLine {
		return start, ok
	}
	return m.rangeEndRow(rows, c.Anchor, start), true
}

// rangeEndRow is the row showing a range anchor's last line, searching forward
// from its located first line. Falls back to the start: a range whose end cannot
// be found at all is better shown one line high than pushed somewhere the code
// does not support.
func (m Model) rangeEndRow(rows []rowRef, a review.Anchor, start int) int {
	file := rows[start].file
	lineNo := func(r rowRef) int {
		if a.Side == review.SideOld {
			return r.oldNo
		}
		return r.newNo
	}
	byText, byLine := -1, -1
	for i := start + 1; i < len(rows); i++ {
		r := rows[i]
		if r.file != file && r.kind == rowLine {
			// Out of the file the range lives in; its rows are contiguous.
			break
		}
		if r.kind != rowLine || r.seg != 0 {
			continue
		}
		if n := lineNo(r); n > 0 && n == a.EndLineHint {
			// The number and the text agreeing is as certain as it gets.
			if a.EndText == "" || m.lineTextIn(rows, i) == a.EndText {
				return i
			}
			if byLine < 0 {
				byLine = i
			}
		}
		if byText < 0 && a.EndText != "" && m.lineTextIn(rows, i) == a.EndText {
			byText = i
		}
	}
	switch {
	// Text over number, for the same reason the start prefers it: an edit above
	// renumbers every line below it without changing what any of them say.
	case byText >= 0:
		return byText
	case byLine >= 0:
		return byLine
	}
	return start
}

// locateAnchorStart finds the row an anchor's first line is on, weakening the
// match the same way findAnchor does: exact line, then same text elsewhere in the
// file, then the same text with matching context.
func (m Model) locateAnchorStart(rows []rowRef, c review.Comment) (int, bool) {
	// A remark about the file as a whole has no line to look for, and that is not
	// the same as having a line nobody can find. It hangs under the file's divider,
	// above the first hunk, which is where a reader looks for something said about
	// the file rather than about a place in it.
	//
	// Checked before anything else: the searches below all key off a line, so a
	// file-scope anchor fell through every one of them and landed in the detached
	// section — under a heading that says the anchor could not be located, about the
	// one anchor that cannot fail to be.
	if m.aboutTheWholeFile(c) {
		return fileHeaderRow(rows, m.filtered, c.Anchor.Path)
	}
	var inFile []int
	for i, r := range rows {
		if r.kind != rowLine || r.file < 0 || r.file >= len(m.filtered) {
			continue
		}
		if pathOf(m.filtered[r.file]) != c.Anchor.Path {
			continue
		}
		inFile = append(inFile, i)
	}
	if len(inFile) == 0 {
		// No line rows for this file. That means one of two very different
		// things, and treating them alike is what made a comment on a *folded*
		// file read as detached: either the file is gone from the change, or it
		// is simply collapsed and its lines are not being emitted right now.
		//
		// Folding a file you have reviewed must not relabel its conversations as
		// remarks whose anchor could not be found — the anchor is fine, the code
		// is just hidden — so they attach to the divider, which is the one row
		// the file still has. Unfolding re-places them on their lines, since
		// every rebuild resolves placement from scratch.
		if row, ok := collapsedFileRow(rows, m.filtered, c.Anchor.Path); ok {
			return row, true
		}
		return 0, false
	}
	lineNo := func(r rowRef) int {
		if c.Anchor.Side == review.SideOld {
			return r.oldNo
		}
		return r.newNo
	}

	// An anchor with no recorded text can only be placed by line number. Remote
	// GitHub threads arrive this way — GitHub gives a line, not the line's
	// content — so without this they would all land in the detached section
	// despite pointing at code that is right there.
	if c.Anchor.Text == "" {
		// No line either, and there is nothing left to match on. Lines are
		// 1-based, so zero means "unknown", which is what GitHub reports (as
		// null) for a thread whose line no longer exists in the diff.
		//
		// This has to be checked rather than left to the comparison below: a
		// deleted row carries no new-side number, so its lineNo is also zero —
		// matching 0 against 0 pinned every outdated thread to the first removed
		// line in its file, presenting it as a remark about code it was never
		// written against.
		if c.Anchor.LineHint <= 0 {
			return 0, false
		}
		for _, i := range inFile {
			if r := rows[i]; r.seg == 0 && lineNo(r) == c.Anchor.LineHint {
				return i, true
			}
		}
		return 0, false
	}

	// The line is where it was, with the text it had.
	for _, i := range inFile {
		r := rows[i]
		if r.seg == 0 && lineNo(r) == c.Anchor.LineHint && m.lineTextIn(rows, i) == c.Anchor.Text {
			return i, true
		}
	}
	// The text moved: prefer a match whose surrounding context also agrees, so a
	// duplicate line elsewhere doesn't capture the comment.
	var textMatches []int
	for _, i := range inFile {
		if rows[i].seg == 0 && m.lineTextIn(rows, i) == c.Anchor.Text {
			textMatches = append(textMatches, i)
		}
	}
	if len(textMatches) == 1 {
		return textMatches[0], true
	}
	if best, ok := m.bestByContext(rows, textMatches, c.Anchor); ok {
		return best, true
	}
	// Ambiguous but present: nearest to where it used to be.
	if len(textMatches) > 0 {
		best, bestDist := textMatches[0], 0
		for n, i := range textMatches {
			d := lineNo(rows[i]) - c.Anchor.LineHint
			if d < 0 {
				d = -d
			}
			if n == 0 || d < bestDist {
				best, bestDist = i, d
			}
		}
		return best, true
	}
	return 0, false
}

// bestByContext picks the candidate whose neighbouring lines agree most with the
// anchor's recorded context. Requires a strict winner: a tie means we genuinely
// cannot tell, and guessing would attach the comment to the wrong code.
func (m Model) bestByContext(rows []rowRef, candidates []int, a review.Anchor) (int, bool) {
	if len(candidates) == 0 || (len(a.ContextBefore) == 0 && len(a.ContextAfter) == 0) {
		return 0, false
	}
	best, bestScore, tied := 0, -1, false
	for _, i := range candidates {
		score := 0
		for n, want := range a.ContextBefore {
			if got, ok := m.rowTextAt(rows, i-len(a.ContextBefore)+n); ok && got == want {
				score++
			}
		}
		for n, want := range a.ContextAfter {
			if got, ok := m.rowTextAt(rows, i+1+n); ok && got == want {
				score++
			}
		}
		switch {
		case score > bestScore:
			best, bestScore, tied = i, score, false
		case score == bestScore:
			tied = true
		}
	}
	if tied || bestScore <= 0 {
		return 0, false
	}
	return best, true
}

func (m Model) rowTextAt(rows []rowRef, i int) (string, bool) {
	if i < 0 || i >= len(rows) || rows[i].kind != rowLine {
		return "", false
	}
	return m.lineTextIn(rows, i), true
}

// lineTextIn reads a line row's content from an arbitrary row slice, so it works
// during the geometry pass before m.stream has been replaced.
func (m Model) lineTextIn(rows []rowRef, i int) string {
	r := rows[i]
	if r.file < 0 || r.file >= len(m.filtered) {
		return ""
	}
	f := m.filtered[r.file]
	if r.hunk < 0 || r.hunk >= len(f.Hunks) {
		return ""
	}
	h := f.Hunks[r.hunk]
	if r.line < 0 || r.line >= len(h.Lines) {
		return ""
	}
	return h.Lines[r.line].Content
}

// AnchorAtCursor describes the line under the cursor, for attaching a new
// comment. Reports false when the cursor isn't on a diff line.
func (m Model) AnchorAtCursor() (review.Anchor, bool) {
	line, ok := m.hunkLineAt(m.stream.rows, m.cursorRow)
	if !ok {
		return review.Anchor{}, false
	}
	// New side for added and context lines; old side only for a removed line,
	// which exists nowhere else. Keeping to the new side means relocation reads
	// current file content, the same source live refresh uses.
	side := review.SideNew
	if line.Type == '-' {
		side = review.SideOld
	}
	return m.anchorSpan(m.stream.rows, m.cursorRow, m.cursorRow, side)
}

// hunkLineAt is the diff line a row shows, false for a row that isn't one (a
// header, a comment, a spacer) or an index outside the slice.
func (m Model) hunkLineAt(rows []rowRef, i int) (diff.HunkLine, bool) {
	if i < 0 || i >= len(rows) {
		return diff.HunkLine{}, false
	}
	r := rows[i]
	if r.kind != rowLine || r.file < 0 || r.file >= len(m.filtered) {
		return diff.HunkLine{}, false
	}
	f := m.filtered[r.file]
	if r.hunk < 0 || r.hunk >= len(f.Hunks) {
		return diff.HunkLine{}, false
	}
	h := f.Hunks[r.hunk]
	if r.line < 0 || r.line >= len(h.Lines) {
		return diff.HunkLine{}, false
	}
	return h.Lines[r.line], true
}

// anchorSpan builds an anchor covering the diff lines shown by rows start
// through end, on the given side. start == end is the ordinary single-line case.
//
// Both endpoints have to be line rows in the same hunk; the caller is what
// establishes that (see rangeAnchor). Context is recorded before the start and
// after the end, which is what makes relocation of a range work the same way a
// single line's does — each end is found by its own text and its own
// surroundings.
func (m Model) anchorSpan(rows []rowRef, start, end int, side review.Side) (review.Anchor, bool) {
	if start < 0 || end < start || end >= len(rows) {
		return review.Anchor{}, false
	}
	first, last := rows[start], rows[end]
	if first.kind != rowLine || last.kind != rowLine || first.file != last.file || first.hunk != last.hunk {
		return review.Anchor{}, false
	}
	if first.file < 0 || first.file >= len(m.filtered) {
		return review.Anchor{}, false
	}
	f := m.filtered[first.file]
	if first.hunk < 0 || first.hunk >= len(f.Hunks) {
		return review.Anchor{}, false
	}
	h := f.Hunks[first.hunk]
	if first.line < 0 || last.line >= len(h.Lines) {
		return review.Anchor{}, false
	}
	lineNo := func(r rowRef) int {
		if side == review.SideOld {
			return r.oldNo
		}
		return r.newNo
	}
	a := review.Anchor{
		Path:     pathOf(f),
		Side:     side,
		LineHint: lineNo(first),
		Text:     h.Lines[first.line].Content,
	}
	if end != start {
		a.EndLineHint = lineNo(last)
		a.EndText = h.Lines[last.line].Content
	}
	for i := first.line - anchorContextLines; i < first.line; i++ {
		if i >= 0 {
			a.ContextBefore = append(a.ContextBefore, h.Lines[i].Content)
		}
	}
	for i := last.line + 1; i <= last.line+anchorContextLines && i < len(h.Lines); i++ {
		a.ContextAfter = append(a.ContextAfter, h.Lines[i].Content)
	}
	return a, true
}

// anchorContextLines is how much surrounding text an anchor records. Enough to
// disambiguate a repeated line, few enough that an edit nearby doesn't
// invalidate the anchor.
const anchorContextLines = 3

// commentGutter is the left bar every message in a conversation shares.
//
// One bar at one indent for the whole thread, rather than a deeper indent per
// reply level. Stepping right per level stair-stepped a long exchange across the
// pane and left a ragged left edge; a shared bar plus a blank row between
// messages reads as one block, and the author label on each message already says
// where one ends and the next begins.
const commentGutter = "  ▌ "

// commentRow is one display line of a comment: its text, and whether it is the
// header line. The header carries the kind's hue; the body stays readable.
type commentRow struct {
	text   string
	header bool
}

// commentRows is every row a comment occupies, gutter included and wrapped to
// width.
//
// Geometry and rendering both go through this, which is what keeps them in
// agreement: a comment's row count depends on the width it wraps at, and if the
// counter and the renderer disagreed the stream's row indices would stop matching
// what is drawn — the same desync the diff's own wrap accounting avoids.
//
// Comments wrap rather than truncate, and always — independent of the `w` wrap
// mode, which governs code. A review remark is prose written to be read; clipping
// it at the pane edge hides the half that explains the point, and there is no
// reason to make the reader ask for that.
//
// Word wrap, not the hard character wrap code uses. Breaking mid-word is right for
// code — reflowing at spaces misrepresents where a token ends — and wrong for
// prose, where it just makes sentences hard to read. ansi.Wrap still hard-breaks a
// word longer than the line, so a URL or a long identifier cannot overflow.
func commentRows(c review.Comment, width int, last, collapsed bool) []commentRow {
	label := displayText(c.Author)
	if label == review.AuthorHuman {
		label = "you"
	}
	// Sanitised once, here, because every row below is derived from it — the folded
	// summary, the header's first line, and every wrapped body row.
	cleanBody := displayText(c.Body)
	if collapsed {
		// Exactly one row — no pads. The pad rows below give a multi-message card
		// air at both ends; a one-line marker needs neither, and they tripled the
		// height of the thing whose whole point is being one line. Folded threads
		// now read as a compact list of markers, each sitting against the code it
		// annotates.
		//
		// The label already carries the fold glyph, where it came from, its state
		// and its message count (see threadHeaderLabel), so all that is left is the
		// opening remark's first line. No state suffix — the chips said it, and
		// appending after a truncated summary would put it where nobody reads.
		title := commentGutter + label
		if s := firstLine(cleanBody); s != "" {
			title += " · " + s
		}
		return []commentRow{{text: truncate(title, max(1, width)), header: true}}
	}

	// Every message opens with a bar-only row: top padding for the first one,
	// and the separator between messages after that. Uniform, so a thread reads
	// as one card with air around its content — the same Padding(1, ...) breathing
	// room every other panel in the app gets.
	out := []commentRow{{text: commentGutter}}
	title := commentGutter + label
	// The kind is named once per conversation, on the remark that opened it. A
	// reply already renders in the thread's hue, so repeating the word on every
	// message is noise — the same reason a published reply omits it. A plain
	// comment is the default and claims nothing, so it goes unlabelled too.
	if k := c.Kind.OrDefault(); k != review.KindComment && c.ReplyTo == "" {
		title += " · " + string(k)
	}
	// A reply GitHub has not got says so. Once it lands, the mirror's copy is what
	// gets drawn and this record is not — so a reply of ours still showing here is
	// one that did not go out, and it looks exactly like one that did. A reply
	// nobody received, reading as received, is the failure this surface exists to
	// prevent.
	if c.Origin() == review.OriginReply {
		title += " · unsent"
	}
	// Before the state chip, because it is the more urgent fact: a reply that has
	// stopped an agent is the one message in the change asking you for something,
	// and "open" is not competing with that for the reader's attention.
	if chip := proposalChip(c); chip != "" {
		title += " · " + chip
	}
	// Not on a mirrored message. "published" is our word for one of *our* comments
	// having reached GitHub, and every message of a GitHub thread is there by
	// definition — the conversation's own header already says `github`. Stamping it on
	// each one repeated the least interesting fact about the card on every row of it
	// (see review.Thread on keeping the two vocabularies apart).
	if c.State != review.Open && !c.Mirrored() {
		title += " · " + string(c.State)
	}
	out = append(out, commentRow{text: truncate(title, max(1, width)), header: true})

	// A robot's words are marked wherever they appear, so an agent's finding is
	// never mistaken for something the reviewer wrote. Prefixed at render time
	// rather than stored, so the marker cannot end up doubled or edited away.
	body := strings.TrimRight(cleanBody, "\n")
	if robotAuthored(c) {
		body = review.RobotMarker + " " + body
	}
	avail := width - len([]rune(commentGutter))
	for _, line := range strings.Split(body, "\n") {
		if avail < 1 {
			out = append(out, commentRow{text: truncate(commentGutter+line, max(1, width))})
			continue
		}
		if strings.TrimSpace(line) == "" {
			// A deliberate blank line in a comment is a paragraph break; wrapping
			// would swallow it.
			out = append(out, commentRow{text: commentGutter})
			continue
		}
		for _, wrapped := range strings.Split(ansi.Wrap(line, avail, ""), "\n") {
			out = append(out, commentRow{text: commentGutter + wrapped})
		}
	}
	// The final message of a conversation closes the block with a matching pad
	// row, so the card has air at both ends rather than butting straight into the
	// next line of code. Only the last one: giving every message a trailing pad
	// would put two blank rows between each pair.
	if last {
		out = append(out, commentRow{text: commentGutter})
	}
	return out
}

// displayTabWidth is what a tab becomes. Four rather than eight because these are
// comment bodies — prose and the occasional code fence — and eight columns of
// indent inside a pane that also holds a gutter and a diff is most of the width.
const displayTabWidth = 4

// displayText makes text from anywhere safe to draw.
//
// Control characters in a body are not characters: they are instructions to the
// terminal, and they arrive through a surface whose whole job is showing text other
// people wrote. GitHub returns comment bodies with **CRLF** line endings, and
// splitting those on "\n" leaves a bare carriage return at the end of every row —
// which moves the cursor back to the start of the line, so the row overwrites
// itself and the frame no longer matches what was measured. One mirrored thread
// with Windows line endings was enough to mangle the whole pane, fragments of the
// diff and of the left column included.
//
// Tabs go for the neighbouring reason: lipgloss measures one as a single cell and a
// terminal draws it as up to eight, so a body containing one is a row whose width
// we are simply wrong about — and every width here matters, since the geometry pass
// counts rows before anything is rendered.
//
// Everything else below 0x20, and DEL, is dropped. ESC above all: without this, a
// comment body could carry an escape sequence and recolour or reposition the pane
// it is drawn in — a review surface renders text written by agents and by strangers
// on the internet, so that has to be impossible rather than unlikely.
func displayText(s string) string {
	if !strings.ContainsFunc(s, needsSanitising) {
		// The common case, and worth not allocating for: a body whose only control
		// character is the newline comes back unchanged. This runs per comment per
		// frame, so an ordinary body must not pay for the pathological one.
		return s
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n':
			b.WriteRune(r)
		case r == '\r':
			// A lone CR is a line ending too — old Mac files, and what is left when a
			// CRLF pair was split apart somewhere upstream.
			b.WriteRune('\n')
		case r == '\t':
			b.WriteString(strings.Repeat(" ", displayTabWidth))
		case isControl(r):
			// Dropped rather than replaced with a glyph: a marker would still be a
			// column to account for, and the character carried no meaning worth keeping.
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// isControl reports whether a rune is a C0 control character or DEL — the ones that
// do something to a terminal rather than appearing in it.
func isControl(r rune) bool {
	return r < 0x20 || r == 0x7f
}

// needsSanitising is isControl minus the newline, which is the one control character
// a body is allowed to carry: the stream splits on it deliberately.
func needsSanitising(r rune) bool { return isControl(r) && r != '\n' }

// robotAuthored reports whether the marker belongs on a comment: a robot wrote
// it, and it is ours rather than a mirrored GitHub thread. A thread's synthetic
// author ("github") is not AuthorHuman, so ByRobot alone would mark other
// people's comments as an agent's.
func robotAuthored(c review.Comment) bool {
	return c.ByRobot() && !c.Mirrored()
}

// commentStyles picks the styling for a comment row.
//
// Keyed off the *kind* — what the remark is asking for — rather than off its
// author or whether it is a reply. Authorship is carried by the 🤖 marker on the
// body instead, which leaves the hue free to say the thing you cannot get from
// reading a label at a glance: whether this wants a change, an answer, or
// nothing.
//
// Factored out so the choice is assertable; lipgloss strips colour with no TTY,
// so it cannot be observed in rendered output.
// The kind's hue lands on the left bar and the header, not on the prose. Tinting
// the body blue made it noticeably harder to read against the block's fill, and a
// whole paragraph of colour is not more informative than a coloured edge — the
// signal is fully carried by the bar and the label.
func commentStyles(kind review.Kind, cursor bool) (bar, head, body, fill lipgloss.Style) {
	bar = kindStyles(kind)
	head = bar
	body = styleCommentText
	if cursor {
		// The cursorline has to be carried by every style on the row — an
		// enclosing style cannot supply it, since each inner style ends with a
		// reset that would clear it mid-row.
		return bar.Background(cursorlineBg), head.Background(cursorlineBg),
			body.Background(cursorlineBg), styleCursorFill
	}
	return bar.Background(commentBg), head.Background(commentBg),
		body.Background(commentBg), styleCommentFill
}

// kindColor is the palette token for a kind, for surfaces that need the colour
// rather than a style — the compose box's border, which is how tab's effect is
// visible before there is any saved comment to look at.
func kindColor(kind review.Kind) string {
	switch kind.OrDefault() {
	case review.KindSuggestion:
		return charm.Danger
	case review.KindQuestion:
		return charm.Warning
	default:
		return charm.Info
	}
}

// kindStyles is the unfilled hue for a kind — the left bar and the header. The
// index reuses it so a conversation looks the same in both places.
func kindStyles(kind review.Kind) lipgloss.Style {
	switch kind.OrDefault() {
	case review.KindSuggestion:
		return styleSuggestionHead
	case review.KindQuestion:
		return styleQuestionHead
	default:
		return styleCommentHead
	}
}

// commentLines renders a comment into styled display rows, painted across the
// full width so it reads as a block set into the diff. Each style carries the
// background itself — an enclosing style cannot supply it, since every inner
// style ends with a reset that would clear it mid-row.
//
// The gutter is styled separately from the text so the bar can carry the kind's
// hue while the prose stays readable.
func commentLines(c review.Comment, width int, cursor, last, collapsed bool) []string {
	bar, head, body, fill := commentStyles(c.Kind, cursor)
	rows := commentRows(c, width, last, collapsed)
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		style := body
		if row.header {
			style = head
		}
		gutter, rest := splitGutter(row.text)
		rendered := bar.Render(gutter) + style.Render(rest)
		if n := width - lipgloss.Width(row.text); n > 0 {
			rendered += fill.Render(strings.Repeat(" ", n))
		}
		out = append(out, rendered)
	}
	return out
}

// splitGutter separates a comment row's left bar from its text, so the two can
// take different styles. A bar-only row (padding, or a paragraph break) has no
// text after it.
func splitGutter(row string) (gutter, rest string) {
	if strings.HasPrefix(row, commentGutter) {
		return commentGutter, row[len(commentGutter):]
	}
	// A row narrower than the gutter itself — only reachable at absurd widths.
	return row, ""
}

// Reviewed files collapse out of the way.
//
// The flag is keyed to the file's *content*, not just its path: an edit after
// you marked it reviewed must bring it back. That matters far more here than in
// a conventional review tool, because the agent is editing while you read — a
// change hidden behind a stale reviewed flag is the one outcome this surface
// must never produce.

// fileContentHash fingerprints a file's diff body, so a reviewed mark can tell
// "unchanged since I looked" from "edited since I looked".
func fileContentHash(f diff.FileDiff) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(f.Status))
	for _, hunk := range f.Hunks {
		_, _ = fmt.Fprintf(h, "@%d,%d,%d,%d;", hunk.OldStart, hunk.OldCount, hunk.NewStart, hunk.NewCount)
		for _, l := range hunk.Lines {
			_, _ = h.Write([]byte{l.Type})
			_, _ = h.Write([]byte(l.Content))
			_, _ = h.Write([]byte{'\n'})
		}
	}
	return strconv.FormatUint(h.Sum64(), 16)
}

// isCollapsed reports whether a file is currently hidden: reviewed, and
// unchanged since it was reviewed.
func (m Model) isCollapsed(path string) bool {
	want, ok := m.ReviewedFiles[path]
	if !ok {
		return false
	}
	for _, f := range m.filtered {
		if pathOf(f) == path {
			return fileContentHash(f) == want
		}
	}
	return false
}

// toggleReviewed marks the file at the cursor reviewed, or un-marks it.
func (m Model) toggleReviewed() (tea.Model, tea.Cmd) {
	f, ok := m.cursorFile()
	if !ok {
		m.status = "no file at the cursor"
		return m, nil
	}
	path := pathOf(f)
	if m.ReviewedFiles == nil {
		m.ReviewedFiles = map[string]string{}
	}
	hash := ""
	if !m.isCollapsed(path) {
		hash = fileContentHash(f)
	}
	if hash == "" {
		delete(m.ReviewedFiles, path)
		m.status = path + ": unreviewed"
	} else {
		m.ReviewedFiles[path] = hash
		m.status = path + ": reviewed"
	}
	if m.MarkReviewed != nil {
		if err := m.MarkReviewed(path, hash); err != nil {
			m.fail("reviewed: %v", err)
			return m, nil
		}
	}
	// Collapsing changes the row count, so the geometry has to be rebuilt.
	at := m.fileIndexOf(path)
	m.rebuildStream()
	// Land on a diff line rather than wherever the clamp happened to leave the
	// cursor. Marking a file reviewed collapses it to one row, so a plain clamp
	// parks you on a divider and the next thing you do is always press `j` a
	// couple of times to get into the file you were sent to.
	m.cursorToFileFirstLine(at)
	return m, nil
}

// fileIndexOf is a path's position in the filtered set, or -1.
func (m Model) fileIndexOf(path string) int {
	for i, f := range m.filtered {
		if pathOf(f) == path {
			return i
		}
	}
	return -1
}

// cursorFile is the file the cursor is in.
func (m Model) cursorFile() (diff.FileDiff, bool) {
	if len(m.stream.rows) == 0 || m.cursorRow >= len(m.stream.rows) {
		return diff.FileDiff{}, false
	}
	fi := m.stream.rows[m.cursorRow].file
	if fi < 0 || fi >= len(m.filtered) {
		return diff.FileDiff{}, false
	}
	return m.filtered[fi], true
}

// SetReviewed installs the reviewed-file marks loaded from the store.
func (m *Model) SetReviewed(marks map[string]string) {
	m.ReviewedFiles = marks
	m.rebuildStream()
}

// reloadComments re-reads the store and rebuilds only when something actually
// changed. Called on every refresh tick, so the unchanged case has to be cheap
// and has to leave the cursor exactly where it was.
func (m *Model) reloadComments() {
	if m.LoadComments == nil {
		return
	}
	fresh, err := m.LoadComments()
	if err != nil {
		// A store read failure is not worth interrupting a review over; the next
		// tick tries again.
		return
	}
	if sameComments(m.comments, fresh) {
		return
	}
	// Rebuilding changes the row count, so the cursor is re-anchored by content
	// the same way a diff reload does rather than by index.
	anchor, hadAnchor := m.captureAnchor()
	offset := m.cursorRow - m.streamScroll
	m.comments = fresh
	if hadAnchor {
		m.restoreAnchor(anchor, offset)
		return
	}
	m.rebuildStream()
	m.clampCursor()
	m.followCursor()
}

// reloadThreads re-reads the mirrored remote threads, on the same tick and with
// the same discipline as reloadComments: rebuild only on a real change, and
// re-anchor the cursor by content when there is one, since a thread's rows shift
// every row below it.
//
// What makes this cheap enough for a 2-second tick is that the mirror is a local
// file the pr-status job maintains — the viewer never talks to GitHub itself.
func (m *Model) reloadThreads() {
	if m.LoadThreads == nil {
		return
	}
	fresh, err := m.LoadThreads()
	if err != nil {
		// Same as a failed comment read: not worth interrupting a review over,
		// and the mirror we already have stays on screen.
		return
	}
	if sameThreads(m.threads, fresh) {
		return
	}
	anchor, hadAnchor := m.captureAnchor()
	offset := m.cursorRow - m.streamScroll
	m.threads = fresh
	if hadAnchor {
		m.restoreAnchor(anchor, offset)
		return
	}
	m.rebuildStream()
	m.clampCursor()
	m.followCursor()
}

// sameThreads reports whether two mirrored thread sets are equivalent for
// display — the counterpart of sameComments, and for the same reason: the
// unchanged case is the common one on a tick and has to cost nothing.
func sameThreads(a, b []review.Thread) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID ||
			a[i].Path != b[i].Path ||
			a[i].Side != b[i].Side ||
			a[i].Line != b[i].Line ||
			a[i].StartLine != b[i].StartLine ||
			a[i].Resolved != b[i].Resolved ||
			a[i].Outdated != b[i].Outdated ||
			len(a[i].Comments) != len(b[i].Comments) {
			return false
		}
		for j := range a[i].Comments {
			if a[i].Comments[j].Author != b[i].Comments[j].Author ||
				a[i].Comments[j].Body != b[i].Comments[j].Body {
				return false
			}
		}
	}
	return true
}

// sameComments reports whether two comment sets are equivalent for display.
// Compares the fields that affect rendering or placement — a timestamp bump on
// its own must not cost a rebuild.
func sameComments(a, b []review.Comment) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID ||
			a[i].Body != b[i].Body ||
			a[i].Kind != b[i].Kind ||
			a[i].State != b[i].State ||
			a[i].Author != b[i].Author ||
			a[i].ReplyTo != b[i].ReplyTo ||
			a[i].Anchor.Path != b[i].Anchor.Path ||
			a[i].Anchor.LineHint != b[i].Anchor.LineHint ||
			a[i].Anchor.Side != b[i].Anchor.Side {
			return false
		}
	}
	return true
}
