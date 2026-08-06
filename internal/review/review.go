// Package review is awp's own store of review findings: the comments a human
// leaves while reading a diff, and the findings an agent files while reviewing
// one.
//
// It exists because the previous arrangement kept those records inside an
// external review TUI, whose store awp could only read by reverse-engineering
// private state files (see the user-problem section of
// specs/20260730-4o94-awp-native-review-surface-spec.md). Owning the store is
// what lets a finding travel from "noticed while reading" to "the agent is
// fixing it" without a human copying text between tools.
//
// Two shape decisions carry most of the weight:
//
//   - **One file per comment.** Agents append findings concurrently, and
//     separate files make that conflict-free by construction — no locking, no
//     lost updates, no retry. Per-review state that only the deck writes lives
//     in review.json instead.
//   - **Comments anchor to content, not to a position in a particular diff.**
//     There is no per-head session concept. awp's agent edits files while you
//     read them, so relocating a comment is the normal path rather than a
//     force-push special case; once that works continuously, a force-push is
//     just "the content changed".
package review

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// TargetKind is what a review is of.
type TargetKind string

const (
	// TargetWorking is a review of a workspace's uncommitted change. It has no
	// PR and never needs one — findings here are consumed by the agent.
	TargetWorking TargetKind = "working"
	// TargetRevset is a review of a commit range, e.g. a change against its
	// stack base.
	TargetRevset TargetKind = "revset"
	// TargetPR is a review of a GitHub pull request.
	TargetPR TargetKind = "pr"
)

// Target identifies what is under review. Workspace is recorded even for PR
// targets so cleanup can find a review when its workspace is deleted.
type Target struct {
	Kind      TargetKind `json:"kind"`
	Value     string     `json:"value,omitempty"`
	Workspace string     `json:"workspace,omitempty"`
}

// State is where a comment is in its life.
type State string

const (
	// Open is a comment awaiting triage. The deck's finding count counts these
	// and only these: it reports what still needs attention rather than making
	// a claim about what you have read.
	Open State = "open"
	// Sent means it has been handed to the agent and is awaiting a response.
	Sent State = "sent"
	// Addressed means the anchored code changed after the comment was sent,
	// inferred rather than asserted by the agent.
	Addressed State = "addressed"
	// Orphaned means the anchor could no longer be located. Kept and shown, never
	// silently dropped.
	Orphaned State = "orphaned"
	// Published means it lives on GitHub now.
	Published State = "published"
)

// Side is which side of the diff an anchor refers to. Added and context lines
// anchor to the new side; only removed lines use the old one, since they exist
// nowhere else. Keeping to the new side means relocation always reads current
// file content, sharing one code path with live refresh.
type Side string

const (
	SideNew Side = "new"
	SideOld Side = "old"
)

// Anchor is where a comment is attached, described so it can be found again
// after the code moves. LineHint is a hint, not an identity: the text and its
// surrounding context are what actually locate the line.
//
// A comment can cover a range of lines, in which case EndLineHint and EndText
// describe its last line the same way LineHint and Text describe its first. The
// two ends are located independently for the same reason a single anchor records
// text at all — the numbers move, the content is what identifies the code.
type Anchor struct {
	Path     string `json:"path"`
	Side     Side   `json:"side"`
	LineHint int    `json:"line_hint"`
	Text     string `json:"text"`
	// EndLineHint and EndText are the last line of a multi-line anchor, both zero
	// / empty for the ordinary single-line case. Kept optional rather than always
	// written so a single-line record reads the same as it always has.
	EndLineHint   int      `json:"end_line_hint,omitempty"`
	EndText       string   `json:"end_text,omitempty"`
	ContextBefore []string `json:"context_before,omitempty"`
	ContextAfter  []string `json:"context_after,omitempty"`
}

// Multiline reports whether the anchor covers more than one line.
//
// An end at or before the start is not a range: that is either a single-line
// anchor or a record written by something that filled the field in wrongly, and
// both are best read as "one line".
func (a Anchor) Multiline() bool { return a.EndLineHint > a.LineHint }

// Scope is how much of the change a remark is about: a line, a file, or the
// whole thing. Three scopes, borrowed from what GitHub's own review model
// supports, because they are three genuinely different acts — "this line is
// wrong", "this file is the wrong shape", "this change needs another pass".
//
// Not a stored field. It is implied by what the anchor already carries, and a
// second spelling of the same fact is a second thing to keep in step: a record
// whose Scope said FileScope while its LineHint said 12 would have to be
// adjudicated by every reader.
type Scope int

const (
	// LineScope is a remark about a line or a run of lines: path and line.
	LineScope Scope = iota
	// FileScope is a remark about a file as a whole: path, no line. GitHub
	// spells this subject_type=file.
	FileScope
	// ChangeScope is a remark about the change as a whole: no path at all. It
	// becomes the review's body when publishing with a verdict.
	ChangeScope
)

// Scope reads the anchor's scope off what it carries.
//
// A line without a path is the one incoherent combination — there is nothing for
// the number to mean — and it reads as ChangeScope here rather than getting a
// fourth case. The CLI refuses to write one, so a record like that came from a
// hand-edited file, and treating it as "about the change" loses the line hint but
// keeps the body, which is the part somebody wrote.
func (a Anchor) Scope() Scope {
	switch {
	case strings.TrimSpace(a.Path) == "":
		return ChangeScope
	case a.LineHint <= 0:
		return FileScope
	default:
		return LineScope
	}
}

// Where is the anchor's scope as a reader should see it: "a.go:12", "a.go:14-20",
// "a.go" for the file as a whole, "the whole change" for no path.
//
// One spelling for every surface that names what a comment is about — the compose
// box's header, the comment index, the agent prompt, the publish preview and the
// publish log — so they cannot disagree. Each of those used to derive it from
// Path and LineHint itself, which is how a scope gets added in one place and read
// as something else in four others.
func (a Anchor) Where() string {
	switch a.Scope() {
	case ChangeScope:
		return "the whole change"
	case FileScope:
		return a.Path
	default:
		return a.Path + ":" + a.LineRange()
	}
}

// LineRange is the anchor's lines as a reader should see them: "12" or "12-18",
// and empty when there is no line at all (a comment about the change as a
// whole). One spelling for every surface that names a location — the compose
// box's header, the comment index, the agent prompt and the publish log all use
// this so they cannot disagree about what a comment is attached to.
func (a Anchor) LineRange() string {
	if a.LineHint <= 0 {
		return ""
	}
	if !a.Multiline() {
		return strconv.Itoa(a.LineHint)
	}
	return strconv.Itoa(a.LineHint) + "-" + strconv.Itoa(a.EndLineHint)
}

// Anchor's text as it should be shown to a reader. A blank line has no text to
// quote, so it is labelled rather than rendered as nothing — otherwise a comment
// on a blank line looks like a comment on whatever happens to be above it.
func (a Anchor) Anchor() string {
	if strings.TrimSpace(a.Text) == "" {
		return "(blank line)"
	}
	return a.Text
}

// PublishRecord is what GitHub gave back. Retained after the body is dropped so
// a re-publish is idempotent rather than duplicating the comment.
type PublishRecord struct {
	ThreadID string    `json:"thread_id"`
	At       time.Time `json:"at"`
}

// Kind is what a comment is asking for. It changes what the reader is expected
// to do about it, which is worth distinguishing: a suggestion wants a change, a
// question wants an answer, and a plain comment wants neither.
//
// It is a property of the remark rather than of its author, so an agent's
// findings carry it too — that is how a reviewer can tell "this is broken" from
// "why is this here" without reading every body.
type Kind string

const (
	// KindComment is a remark with no implied action — the default, and what an
	// empty Kind means on a record written before kinds existed.
	KindComment Kind = "comment"
	// KindSuggestion proposes a change.
	KindSuggestion Kind = "suggestion"
	// KindQuestion asks for an answer.
	KindQuestion Kind = "question"
)

// Kinds is every kind, in the order the compose box cycles them.
func Kinds() []Kind { return []Kind{KindComment, KindSuggestion, KindQuestion} }

// Proposal is an agent's offer to make a change, and where that offer stands.
//
// The prompt an agent gets when a finding is handed to it says to reply before
// changing anything and then stop. That gate had no home: the agent's answer was
// prose in a chat log, and approving it meant leaving the diff, finding the
// agent's tmux window and typing "yes" — so the thing the prompt asked for was
// enforced nowhere and answered somewhere else entirely. A proposal is that
// exchange written down: the offer is a reply, and the approval is a fact in the
// store the agent can read back.
//
// One field rather than two, holding both "is this a proposal" and "where does it
// stand". The empty value is not a proposal at all, which is what every record
// written before this is — and it means the two facts cannot come apart, the same
// reasoning as Anchor.Scope reading a scope off what the anchor carries rather
// than storing it beside it.
//
// Not a Kind. Kind is what a remark asks for, and a reply's kind is already
// dropped everywhere it would be shown (see PublishBody). Approval is a property
// of the exchange, not of the text.
//
// There is no "declined". Approving is one key; declining is a reply, whose text
// is the reason — and a bare no tells an agent nothing it can act on. A proposal
// you have not approved stays pending, which is the honest reading of it.
type Proposal string

const (
	// ProposalPending is an offer awaiting a yes. The agent is expected to have
	// stopped.
	ProposalPending Proposal = "pending"
	// ProposalApproved means the reviewer said go ahead.
	ProposalApproved Proposal = "approved"
)

// IsProposal reports whether this comment is an offer to change something, as
// opposed to an ordinary remark or an answer to a question.
//
// The gate is about changing code, not about replying: an agent explaining why
// the code is the way it is replies normally and carries on. Only a reply that
// says "here is what I would do" needs a yes.
func (c Comment) IsProposal() bool { return c.Proposal != "" }

// Approved reports whether the reviewer said go ahead.
func (c Comment) Approved() bool { return c.Proposal == ProposalApproved }

// AwaitingApproval is a proposal nobody has answered yet — the state that stops
// an agent, and the only one `A` acts on.
func (c Comment) AwaitingApproval() bool { return c.Proposal == ProposalPending }

// ParseKind reads a kind from user or agent input, falling back to KindComment.
// An unrecognised value is not an error: a comment is worth keeping even when the
// label on it is wrong, and the default is the one that claims the least.
func ParseKind(s string) Kind {
	switch Kind(strings.ToLower(strings.TrimSpace(s))) {
	case KindSuggestion:
		return KindSuggestion
	case KindQuestion:
		return KindQuestion
	default:
		return KindComment
	}
}

// Next is the kind after this one, wrapping — what tab does in the compose box.
func (k Kind) Next() Kind {
	all := Kinds()
	for i, c := range all {
		if c == k.OrDefault() {
			return all[(i+1)%len(all)]
		}
	}
	return KindComment
}

// OrDefault resolves an unset kind. Records written before kinds existed have an
// empty one, and every reader has to see the same thing for them.
func (k Kind) OrDefault() Kind {
	if k == "" {
		return KindComment
	}
	return k
}

// Label is the kind as it reads at the head of a sentence: "Suggestion", "Question".
//
// Capitalised because that is where it is used — a published body opens with it, and
// a lowercase word starting a comment reads as a typo rather than as a label. The
// constant itself stays lowercase: it is what the CLI accepts and what the store
// holds, and neither should change because a display string did.
func (k Kind) Label() string {
	s := string(k.OrDefault())
	return strings.ToUpper(s[:1]) + s[1:]
}

// Comment is one finding. Author distinguishes a human's note from an agent's
// finding; nothing else about the record differs between the two directions.
type Comment struct {
	ID     string `json:"id"`
	Author string `json:"author"`
	Body   string `json:"body"`
	// Kind is what the comment is asking for. Empty means KindComment, so records
	// written before kinds existed read correctly.
	Kind   Kind   `json:"kind,omitempty"`
	State  State  `json:"state"`
	Anchor Anchor `json:"anchor"`
	// ReplyTo is the id of one of our own comments this answers.
	ReplyTo string `json:"reply_to,omitempty"`
	// ReplyToThread is the GitHub node id of a mirrored review thread this answers,
	// for a reply the reviewer wrote into someone else's conversation.
	//
	// A separate field from ReplyTo rather than the same one holding a foreign id.
	// The two mean different things and have different futures: a local reply is an
	// exchange with the agent that stays here, while this one is destined for
	// GitHub and is only a record once it gets there. Overloading ReplyTo would
	// also make every reader of it — the open count, the deletion cascade, the
	// grouping in Threads — have to know which namespace an id came from before it
	// could act on it.
	ReplyToThread string `json:"reply_to_thread,omitempty"`
	// Proposal marks a reply that offers to make a change, and says whether the
	// reviewer has agreed to it. Empty on everything that is not one.
	Proposal  Proposal       `json:"proposal,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	Publish   *PublishRecord `json:"published,omitempty"`
}

// ThreadReply reports whether this comment answers a mirrored GitHub thread.
//
// One spelling for the check, because it is asked in three packages — the store
// counts findings, the publish path decides which call sends a comment, and the
// viewer decides where to draw it — and a comment answering someone else's thread
// is none of the things a top-level remark is.
func (c Comment) ThreadReply() bool { return strings.TrimSpace(c.ReplyToThread) != "" }

// AuthorHuman marks a comment written by the person at the keyboard.
const AuthorHuman = "human"

// ByRobot reports whether a comment was written by something other than the
// person at the keyboard — an agent, or anything else filing findings through
// the CLI. Everything that renders or publishes a comment marks these, so an
// agent's words are never mistaken for a reviewer's.
func (c Comment) ByRobot() bool { return c.Author != AuthorHuman }

// RobotMarker prefixes anything a robot wrote. On GitHub especially, an agent's
// comment is otherwise indistinguishable from a person's — it posts under the
// authenticated user's account.
const RobotMarker = "🤖"

// PublishBody is the body as it should appear on GitHub: the kind, then the
// robot marker if a robot wrote it, then the text.
//
// Composed here rather than at each call site, and applied at publish time
// rather than baked into the stored body, because the stored body is what the
// author typed. Baking the prefixes in would double them on a re-publish, and
// would put them in front of the reviewer while they are still editing.
//
// The kind is spelled out rather than left to colour: GitHub has no notion of
// our palette, and "suggestion: " is the whole signal a reader gets there.
func (c Comment) PublishBody() string {
	body := strings.TrimSpace(c.Body)
	// The kind first, so the marker can be put in front of the whole thing below.
	//
	// A plain comment says nothing about what it is asking for — that is what makes
	// it the default — so labelling it labels every remark that had nothing special
	// to say. The other two are worth announcing, and read as a sentence: "question:
	// why is this here" rather than "(question) - why is this here". A reply joins a
	// thread whose first comment already carries the kind, so repeating it on every
	// message would be noise — and that holds whichever conversation it is joining,
	// ours or a mirrored GitHub one.
	if kind := c.Kind.OrDefault(); kind != KindComment && c.ReplyTo == "" && !c.ThreadReply() {
		body = kind.Label() + ": " + body
	}
	// The marker leads. Who wrote a remark frames everything after it — including
	// what the remark is asking for — so "🤖 suggestion: …" reads in the order a
	// reader needs, where "suggestion: 🤖 …" buries the authorship mid-sentence.
	if c.ByRobot() {
		body = RobotMarker + " " + body
	}
	return body
}

// Review is the container: what is under review, and the per-review state the
// deck owns.
type Review struct {
	ID           string            `json:"id"`
	Repo         string            `json:"repo"`
	Target       Target            `json:"target"`
	ObservedHead string            `json:"observed_head,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
	ReviewedFile map[string]string `json:"reviewed_files,omitempty"`
}

// Thread is a GitHub review thread, mirrored locally so the diff can show a
// PR's existing conversation without a network round trip per frame.
//
// Read-only: these are GitHub's records, not ours. A reply the reviewer writes is
// a normal Comment carrying ReplyTo, so authored content stays in one place with
// one lifecycle.
//
// Note the vocabulary split, kept deliberately: our comments have a State, a
// remote thread has Resolved. A local draft cannot be "resolved" and a remote
// thread cannot be "addressed"; blurring them would make the UI lie about what a
// keystroke just did.
type Thread struct {
	ID        string          `json:"id"`
	Path      string          `json:"path"`
	Side      Side            `json:"side"`
	Line      int             `json:"line"`
	StartLine int             `json:"start_line,omitempty"`
	Resolved  bool            `json:"resolved"`
	Outdated  bool            `json:"outdated"`
	Comments  []ThreadComment `json:"comments"`
}

// A mirrored thread is shown as ordinary comments, so one renderer covers both
// GitHub's conversations and ours. The ids those borrowed comments carry are the
// contract between the two types: they have to name the thread they came from, so
// that resolving, folding and replying can act on the conversation from any of its
// rows, and they have to be recognisable as *not ours* so that editing, deleting
// and the robot marker leave other people's words alone.
//
// The scheme lives here rather than in the viewer that renders it. It is a
// translation between two types this package owns, and while it lived upstairs
// review.Comment could not answer "are you GitHub's record or mine" — so every
// caller answered by prefix-matching the id itself, which is exactly the kind of
// invariant that holds only as long as every call site remembers it.
const (
	// remoteThreadPrefix marks a comment adapted from a mirrored GitHub thread.
	remoteThreadPrefix = "thread-"
	// threadMessageSep separates a thread's id from a message's index within it. A
	// character GitHub's node ids do not contain, so splitting on it cannot cut an
	// id in half.
	threadMessageSep = "#"
)

// RemoteThreadID is the id the thread's opening message is shown under.
func RemoteThreadID(threadID string) string { return remoteThreadPrefix + threadID }

// RemoteMessageID is the id of the nth message of a mirrored thread. Indexed off
// the thread so every message has a stable id that still names its conversation.
func RemoteMessageID(threadID string, n int) string {
	return RemoteThreadID(threadID) + threadMessageSep + strconv.Itoa(n)
}

// Mirrored reports whether this is GitHub's record rather than one of ours.
//
// One spelling, because the answer decides whether four different things are
// allowed — editing, deleting, the robot marker, and the published chip — and a
// comment that is other people's words is none of the things one of ours is.
func (c Comment) Mirrored() bool { return strings.HasPrefix(c.ID, remoteThreadPrefix) }

// ThreadIDOf recovers the thread a mirrored comment belongs to. False for one of
// ours.
//
// Any message of the conversation answers for the whole of it: resolving, folding
// and replying all act on the thread, so the cursor may sit on any of its rows.
func ThreadIDOf(commentID string) (string, bool) {
	if !strings.HasPrefix(commentID, remoteThreadPrefix) {
		return "", false
	}
	id := strings.TrimPrefix(commentID, remoteThreadPrefix)
	if cut := strings.Index(id, threadMessageSep); cut >= 0 {
		id = id[:cut]
	}
	return id, true
}

// ThreadComment is one message in a remote thread.
type ThreadComment struct {
	// ID is GitHub's node id for the message. Kept because it is how a mirrored
	// thread is recognised as the echo of a comment published from here: the id
	// GitHub hands back when it creates a comment is the same id it reports when
	// the mirror reads that comment again. Empty on a mirror written before the
	// ids were carried, which reconciliation treats as "cannot tell" rather than
	// as a match.
	ID     string `json:"id,omitempty"`
	Author string `json:"author"`
	Body   string `json:"body"`
}

// SaveThreads mirrors a PR's threads into the review. Cached rather than fetched
// per render so opening the diff — and the deck's first paint — stay off the
// network.
func (s Store) SaveThreads(r Review, threads []Thread) error {
	dir := s.dir(r.Repo, r.ID)
	if dir == "" {
		return errors.New("review: cannot resolve store path")
	}
	return writeJSON(filepath.Join(dir, "remote", "threads.json"), threads)
}

// Threads reads the mirrored threads. A missing or unreadable mirror yields none
// rather than an error: remote conversation is context, and its absence must not
// stop a diff from opening.
func (s Store) Threads(r Review) []Thread {
	b, err := os.ReadFile(filepath.Join(s.dir(r.Repo, r.ID), "remote", "threads.json"))
	if err != nil {
		return nil
	}
	var out []Thread
	if err := json.Unmarshal(b, &out); err != nil {
		return nil
	}
	return out
}

// Counts is the per-repo summary the deck reads on its fast first paint, so
// rendering a row never means walking a comments directory.
type Counts struct {
	// ByWorkspace maps a workspace name to its open-comment count.
	ByWorkspace map[string]int `json:"by_workspace"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// Store is a review store rooted at a directory.
type Store struct {
	// Root is the reviews directory. Empty means the default under ~/.awp.
	Root string
	// Now is the clock, injectable for tests.
	Now func() time.Time
}

func (s Store) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}

// ID derives a review's identity from its target. Deliberately not keyed on a
// head SHA: identity that includes the head is what stranded draft comments on
// every force-push in the external tool this replaced, and requires migration
// code to undo. Here the head is observed metadata on the record instead.
func ID(t Target) string {
	switch t.Kind {
	case TargetPR:
		return "pr-" + t.Value
	case TargetRevset:
		return "rev-" + slug(t.Value)
	default:
		return "work-" + slug(t.Workspace)
	}
}

func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	if out == "" {
		return "unnamed"
	}
	return out
}

func (s Store) dir(repoRoot, id string) string {
	if s.Root != "" {
		return filepath.Join(s.Root, slug(filepath.Base(repoRoot)), slug(id))
	}
	return configReviewStorePath(repoRoot, id)
}

func (s Store) repoDir(repoRoot string) string {
	if s.Root != "" {
		return filepath.Join(s.Root, slug(filepath.Base(repoRoot)))
	}
	return configReviewStoreRepoDir(repoRoot)
}

// Open loads a review, creating it if this is the first time that target has
// been reviewed.
func (s Store) Open(repoRoot string, t Target) (Review, error) {
	id := ID(t)
	dir := s.dir(repoRoot, id)
	if dir == "" {
		return Review{}, errors.New("review: cannot resolve store path")
	}
	r, err := s.load(repoRoot, id)
	if err == nil {
		return r, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Review{}, err
	}
	now := s.now()
	r = Review{ID: id, Repo: repoRoot, Target: t, CreatedAt: now, UpdatedAt: now}
	if err := s.Save(r); err != nil {
		return Review{}, err
	}
	return r, nil
}

func (s Store) load(repoRoot, id string) (Review, error) {
	b, err := os.ReadFile(filepath.Join(s.dir(repoRoot, id), "review.json"))
	if err != nil {
		return Review{}, err
	}
	var r Review
	if err := json.Unmarshal(b, &r); err != nil {
		return Review{}, fmt.Errorf("review: parse review.json: %w", err)
	}
	return r, nil
}

// Save writes the review's own state. Only the deck should call this; agents
// only ever add comment files.
func (s Store) Save(r Review) error {
	dir := s.dir(r.Repo, r.ID)
	if dir == "" {
		return errors.New("review: cannot resolve store path")
	}
	if err := os.MkdirAll(filepath.Join(dir, "comments"), 0o755); err != nil {
		return err
	}
	r.UpdatedAt = s.now()
	return writeJSON(filepath.Join(dir, "review.json"), r)
}

// AddComment files a comment. Each lands in its own file, so concurrent writers
// never contend.
func (s Store) AddComment(r Review, c Comment) (Comment, error) {
	dir := s.dir(r.Repo, r.ID)
	if dir == "" {
		return Comment{}, errors.New("review: cannot resolve store path")
	}
	if strings.TrimSpace(c.Body) == "" {
		return Comment{}, errors.New("review: comment body is empty")
	}
	now := s.now()
	if c.ID == "" {
		c.ID = fmt.Sprintf("%d-%s", now.UnixNano(), slug(c.Author))
	}
	if c.Author == "" {
		c.Author = AuthorHuman
	}
	if c.State == "" {
		c.State = Open
	}
	if c.Anchor.Side == "" {
		c.Anchor.Side = SideNew
	}
	c.CreatedAt, c.UpdatedAt = now, now
	if err := os.MkdirAll(filepath.Join(dir, "comments"), 0o755); err != nil {
		return Comment{}, err
	}
	if err := writeJSON(filepath.Join(dir, "comments", c.ID+".json"), c); err != nil {
		return Comment{}, err
	}
	return c, nil
}

// UpdateComment replaces a comment in place.
func (s Store) UpdateComment(r Review, c Comment) error {
	_, err := s.writeComment(r, c)
	return err
}

// writeComment is UpdateComment for a caller that needs the record as written.
//
// UpdateComment takes its comment by value and stamps UpdatedAt on that copy, so
// the caller's own copy comes back a version behind — harmless when the return
// value is discarded, and a lie when it is handed on. Approve hands it on: what
// it returns is what the agent's nudge is rendered from.
func (s Store) writeComment(r Review, c Comment) (Comment, error) {
	dir := s.dir(r.Repo, r.ID)
	if dir == "" || c.ID == "" {
		return Comment{}, errors.New("review: cannot resolve comment path")
	}
	c.UpdatedAt = s.now()
	if err := writeJSON(filepath.Join(dir, "comments", c.ID+".json"), c); err != nil {
		return Comment{}, err
	}
	return c, nil
}

// DeleteComment removes a comment and every reply beneath it.
//
// Cascading rather than leaving the replies behind. Threads promotes a reply
// whose parent is missing to a conversation of its own, so deleting a remark you
// had already discussed used to scatter the answers through the diff as if each
// were an independent finding — the record would still hold them, but nothing
// would say what they were answering.
//
// Transitive, because `awp review reply --to` accepts any comment's id: the deck
// normalises a reply-to-a-reply onto the conversation's top, but an agent going
// through the CLI can build a deeper chain.
func (s Store) DeleteComment(r Review, id string) error {
	dir := s.dir(r.Repo, r.ID)
	if dir == "" || id == "" {
		return errors.New("review: cannot resolve comment path")
	}
	doomed := []string{id}
	// Best-effort: if the listing fails we still delete the comment that was
	// asked for. Refusing would leave the reviewer unable to remove anything.
	if existing, err := s.Comments(r); err == nil {
		doomed = CommentAndReplies(existing, id)
	}
	var errs []error
	for _, victim := range doomed {
		err := os.Remove(filepath.Join(dir, "comments", victim+".json"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// CommentAndReplies is id plus every comment replying to it, transitively, sorted
// so callers get a stable order.
//
// Exported because deletion happens in two places that must agree: the store
// removes the records, and a viewer holding the set in memory has to prune the
// same ones rather than waiting for a reload to notice.
func CommentAndReplies(comments []Comment, id string) []string {
	doomed := map[string]bool{id: true}
	// Repeated passes rather than one: a reply may appear before the reply it
	// answers, so a single pass could miss the tail of a chain.
	for grew := true; grew; {
		grew = false
		for _, c := range comments {
			if c.ReplyTo != "" && doomed[c.ReplyTo] && !doomed[c.ID] {
				doomed[c.ID] = true
				grew = true
			}
		}
	}
	out := make([]string, 0, len(doomed))
	for cid := range doomed {
		out = append(out, cid)
	}
	sort.Strings(out)
	return out
}

// Comments lists a review's comments, oldest first.
//
// A comment file that cannot be read or parsed is skipped rather than failing
// the whole listing: one corrupt record must not make a review unopenable.
func (s Store) Comments(r Review) ([]Comment, error) {
	dir := filepath.Join(s.dir(r.Repo, r.ID), "comments")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]Comment, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var c Comment
		if err := json.Unmarshal(b, &c); err != nil || c.ID == "" {
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

// OpenCount is how many findings still need triage — what the deck badge shows.
//
// Replies are excluded: one exchange is one thing awaiting your attention, not
// one per message. A reply's significance is carried by reopening its parent (see
// Reply), so a conversation that needs you still counts exactly once.
//
// A reply into a GitHub thread is excluded for a different reason: it is not a
// finding at all. It is something you said to someone else, and counting it would
// have the badge ask you to triage your own answer.
func OpenCount(comments []Comment) int {
	n := 0
	for _, c := range comments {
		if c.ReplyTo != "" || c.ThreadReply() {
			continue
		}
		if c.State == Open {
			n++
		}
	}
	return n
}

// Reply files a reply against a parent comment and reopens that parent.
//
// Reopening is the point: an exchange the agent has responded to needs the
// reviewer again, and the badge counts parents. Without it, a reply would land
// silently on a comment still marked `sent`.
func (s Store) Reply(r Review, parentID string, c Comment) (Comment, error) {
	if strings.TrimSpace(parentID) == "" {
		return Comment{}, errors.New("review: reply needs a parent comment id")
	}
	existing, err := s.Comments(r)
	if err != nil {
		return Comment{}, err
	}
	var parent *Comment
	for i := range existing {
		if existing[i].ID == parentID {
			parent = &existing[i]
			break
		}
	}
	if parent == nil {
		return Comment{}, fmt.Errorf("review: no comment %q to reply to", parentID)
	}
	// A reply inherits the parent's anchor, so the thread stays together even if
	// the caller knows only the id.
	c.ReplyTo = parentID
	if c.Anchor.Path == "" {
		c.Anchor = parent.Anchor
	}
	saved, err := s.AddComment(r, c)
	if err != nil {
		return Comment{}, err
	}
	if parent.State != Open {
		parent.State = Open
		if err := s.UpdateComment(r, *parent); err != nil {
			return saved, err
		}
	}
	return saved, nil
}

// Approve says yes to a proposal and hands the exchange back to the agent.
//
// Two records change, and both have to: the proposal itself becomes approved, and
// the finding it answers moves to Sent. That is the mirror of Reply, which
// reopens the parent when the agent answers — Open means the exchange is waiting
// on you, Sent means it is waiting on the agent, and the deck's finding count
// reads exactly that. Leaving the parent Open would keep asking you to triage a
// question you have already answered.
//
// Approving something that is already approved is not an error and writes
// nothing: the reviewer pressing the key twice means "get on with it", which is
// the caller's business (it can send the agent another nudge) and not a reason to
// rewrite the record with a new timestamp.
func (s Store) Approve(r Review, id string) (Comment, error) {
	if strings.TrimSpace(id) == "" {
		return Comment{}, errors.New("review: approve needs a comment id")
	}
	existing, err := s.Comments(r)
	if err != nil {
		return Comment{}, err
	}
	var c *Comment
	for i := range existing {
		if existing[i].ID == id {
			c = &existing[i]
			break
		}
	}
	if c == nil {
		return Comment{}, fmt.Errorf("review: no comment %q to approve", id)
	}
	// Refused rather than treated as an approval of something. Writing the field
	// onto an ordinary remark would invent a proposal nobody made, and the record
	// would then claim an agent had offered to do something it never offered.
	if !c.IsProposal() {
		return Comment{}, fmt.Errorf("review: comment %q is not a proposal", id)
	}
	if c.Approved() {
		return *c, nil
	}
	c.Proposal = ProposalApproved
	written, err := s.writeComment(r, *c)
	if err != nil {
		return Comment{}, err
	}
	*c = written
	if c.ReplyTo != "" {
		for i := range existing {
			if existing[i].ID != c.ReplyTo || existing[i].State == Sent {
				continue
			}
			parent := existing[i]
			parent.State = Sent
			// The approval is already on disk by here, so the error has to say that
			// rather than read as "approving failed" — the caller's next move is to
			// tell the agent to go ahead, which is still the right move.
			if err := s.UpdateComment(r, parent); err != nil {
				return *c, fmt.Errorf("review: approved, but the finding it answers is still marked open: %w", err)
			}
			break
		}
	}
	return *c, nil
}

// Thread groups a top-level comment with its replies, oldest first.
type CommentThread struct {
	Parent  Comment
	Replies []Comment
}

// Threads groups comments into conversations, preserving order. A reply whose
// parent is missing is promoted to a top-level entry rather than dropped —
// showing it out of place beats losing it.
func Threads(comments []Comment) []CommentThread {
	byID := make(map[string]int, len(comments))
	out := make([]CommentThread, 0, len(comments))
	for _, c := range comments {
		if c.ReplyTo == "" {
			byID[c.ID] = len(out)
			out = append(out, CommentThread{Parent: c})
		}
	}
	for _, c := range comments {
		if c.ReplyTo == "" {
			continue
		}
		if i, ok := byID[c.ReplyTo]; ok {
			out[i].Replies = append(out[i].Replies, c)
			continue
		}
		out = append(out, CommentThread{Parent: c})
	}
	return out
}

// WriteCounts refreshes the per-repo index the deck reads on first paint.
func (s Store) WriteCounts(repoRoot string, counts Counts) error {
	dir := s.repoDir(repoRoot)
	if dir == "" {
		return errors.New("review: cannot resolve store path")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	counts.UpdatedAt = s.now()
	return writeJSON(filepath.Join(dir, "index.json"), counts)
}

// ReadCounts loads the per-repo index. A missing or unreadable index yields zero
// counts rather than an error: the badge is a nicety, not a correctness concern.
func (s Store) ReadCounts(repoRoot string) Counts {
	dir := s.repoDir(repoRoot)
	if dir == "" {
		return Counts{}
	}
	b, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		return Counts{}
	}
	var c Counts
	if err := json.Unmarshal(b, &c); err != nil {
		return Counts{}
	}
	return c
}

// HasUnpublished reports whether a review holds work that only exists locally.
// This is the one thing cleanup must never destroy: a diff regenerates and a
// published comment lives on GitHub, but an unpublished draft is gone for good.
func HasUnpublished(comments []Comment) bool {
	for _, c := range comments {
		if c.State != Published {
			return true
		}
	}
	return false
}

// DeleteWorkspaceReview removes the working-copy review for a workspace, unless
// it still holds unpublished comments. Reports whether it was removed.
func (s Store) DeleteWorkspaceReview(repoRoot, workspace string) (bool, error) {
	if strings.TrimSpace(workspace) == "" {
		return false, nil
	}
	t := Target{Kind: TargetWorking, Workspace: workspace}
	r, err := s.load(repoRoot, ID(t))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	comments, err := s.Comments(r)
	if err != nil {
		return false, err
	}
	if HasUnpublished(comments) {
		return false, nil
	}
	dir := s.dir(repoRoot, r.ID)
	if dir == "" {
		return false, nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return false, err
	}
	return true, nil
}

// writeJSON writes atomically: a partially written record is worse than a
// missing one, and the deck may be reading while an agent writes.
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Rename(name, path)
}
