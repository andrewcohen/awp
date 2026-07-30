// Package review is awp's own store of review findings: the comments a human
// leaves while reading a diff, and the findings an agent files while reviewing
// one.
//
// It exists because the previous arrangement had those records living inside
// tuicr, a store awp could only read by reverse-engineering private state files
// (see the deleted internal/cli/tuicr_session.go and the user-problem section of
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
type Anchor struct {
	Path          string   `json:"path"`
	Side          Side     `json:"side"`
	LineHint      int      `json:"line_hint"`
	Text          string   `json:"text"`
	ContextBefore []string `json:"context_before,omitempty"`
	ContextAfter  []string `json:"context_after,omitempty"`
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

// Comment is one finding. Author distinguishes a human's note from an agent's
// finding; nothing else about the record differs between the two directions.
type Comment struct {
	ID        string         `json:"id"`
	Author    string         `json:"author"`
	Body      string         `json:"body"`
	State     State          `json:"state"`
	Anchor    Anchor         `json:"anchor"`
	ReplyTo   string         `json:"reply_to,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	Publish   *PublishRecord `json:"published,omitempty"`
}

// AuthorHuman marks a comment written by the person at the keyboard.
const AuthorHuman = "human"

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

// ThreadComment is one message in a remote thread.
type ThreadComment struct {
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
// head SHA: identity that includes the head is what stranded draft comments in
// tuicr on every force-push, and requires migration code to undo. Here the head
// is observed metadata on the record instead.
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
	dir := s.dir(r.Repo, r.ID)
	if dir == "" || c.ID == "" {
		return errors.New("review: cannot resolve comment path")
	}
	c.UpdatedAt = s.now()
	return writeJSON(filepath.Join(dir, "comments", c.ID+".json"), c)
}

// DeleteComment removes a comment.
func (s Store) DeleteComment(r Review, id string) error {
	dir := s.dir(r.Repo, r.ID)
	if dir == "" || id == "" {
		return errors.New("review: cannot resolve comment path")
	}
	err := os.Remove(filepath.Join(dir, "comments", id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
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
func OpenCount(comments []Comment) int {
	n := 0
	for _, c := range comments {
		if c.ReplyTo != "" {
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
