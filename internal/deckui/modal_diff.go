package deckui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/andrewcohen/awp/internal/review"
	"github.com/andrewcohen/awp/internal/ui"
)

// DiffScope is what a review is of. The surface is the same either way; only the
// revision range the diff comes from differs.
type DiffScope int

const (
	// ScopeWorking is the workspace's uncommitted change — what `c` shows.
	ScopeWorking DiffScope = iota
	// ScopeStackBase is the whole change against its stack base — what `C`
	// shows.
	ScopeStackBase
)

func (s DiffScope) String() string {
	if s == ScopeStackBase {
		return "vs stack base"
	}
	return "working copy"
}

// DiffLoader returns git-format diff text for a workspace at the given scope
// (`jj diff --git` with the matching revision). Installed by the CLI layer via
// WithDiffViewer so the deck package doesn't shell out itself.
type DiffLoader func(item Item, scope DiffScope) (string, error)

// DiffOpener returns the command that opens filePath at line for a
// workspace — an external $EDITOR process, which tea.ExecProcess handles.
type DiffOpener func(item Item, filePath string, line int) tea.Cmd

// DiffBaseResolver names what a scope's diff is against, for the modal's footer:
// the trunk branch, or a stacked parent's bookmark. Empty when there is nothing
// better to say than the scope's own wording.
//
// Separate from DiffLoader because it shells out and the answer is chrome. The
// viewer runs it as its own command, so a slow resolve delays the label rather
// than the diff.
type DiffBaseResolver func(item Item, scope DiffScope) string

// CommentStore is the review store seam. The deck package neither knows nor
// cares where findings live; the CLI layer supplies these.
type CommentStore struct {
	// Load returns the findings already anchored in this workspace's review.
	Load func(item Item) ([]review.Comment, error)
	// Save persists a newly written comment.
	Save func(item Item, c review.Comment) error
	// LoadReviewed returns the reviewed-file marks (path → content hash).
	LoadReviewed func(item Item) (map[string]string, error)
	// SaveReviewed persists one mark; an empty hash clears it.
	SaveReviewed func(item Item, path, hash string) error
	// Update revises an existing comment; Delete removes one.
	Update func(item Item, c review.Comment) error
	Delete func(item Item, id string) error
	// Reply files a reply against a parent comment.
	Reply func(item Item, parentID string, c review.Comment) error
	// LastSaved returns the record Save just wrote, id included.
	LastSaved func() (review.Comment, bool)
	// Resolve toggles a GitHub review thread's resolved state.
	Resolve func(item Item, threadID string, resolve bool) error
	// LoadThreads returns the mirrored PR threads for this workspace's review.
	LoadThreads func(item Item) ([]review.Thread, error)
	// Send hands a saved comment to the workspace's agent. Nil leaves the
	// send-to-agent exit unavailable, which the editor reports rather than
	// silently doing nothing.
	Send CommentSender
	// Publish sends the review to its PR with a verdict — "approve",
	// "request-changes", "comment", or empty for the comments alone — and returns
	// what happened. Nil leaves the viewer's `P` unavailable.
	Publish func(item Item, verdict string) (string, error)
}

// CommentSender delivers a comment to a workspace's agent.
type CommentSender func(item Item, c review.Comment) error

// diffModalChrome is the rows the deck's chrome takes around the viewer's
// body: 1 for the panel's top padding, 2 for the pane borders, 3 for the
// footer block (its own Padding(1, 1, 1, 1) around a one-line status bar).
//
// It has to be exact. Over-reserving does not shrink the frame — the deck pads
// whatever is left over to pin the footer to the bottom — it just converts the
// rows into a blank band above the footer. This was 8, two rows too many, which
// is what put a visible gap under the diff.
const diffModalChrome = 6

// diffModal is the `c` overlay: awp's own diff viewer (internal/ui, the
// same one `awp diff` runs) rendered in place of the row list, scoped to
// the selected workspace's working change.
//
// It wraps ui.Model rather than reimplementing it, and renders ui.Body so
// the deck keeps ownership of the header and footer. Close keys are
// intercepted here before forwarding, so the inner model never reaches its
// standalone `q` → tea.Quit path and take the whole deck down with it.
type diffModal struct {
	inner ui.Model
	label string
	// pr is the workspace's pinned PR as `repo#number` — "awp#1234" — or empty
	// when no PR was detected. Reading a change and knowing which PR it is are
	// the same question often enough that the footer should answer both.
	pr    string
	scope DiffScope
	// Styles are cached here rather than built per frame — view and
	// footerHelp are render paths.
	muted  lipgloss.Style
	danger lipgloss.Style
	panel  lipgloss.Style
}

// newDiffModal builds the modal and returns the command that loads the
// first diff.
func newDiffModal(item Item, scope DiffScope, load DiffLoader, open DiffOpener, base DiffBaseResolver, comments CommentStore) (*diffModal, tea.Cmd) {
	inner := ui.New(item.Path,
		func() (string, error) { return load(item, scope) },
		func(filePath string, line int) tea.Cmd {
			if open == nil {
				return nil
			}
			return open(item, filePath, line)
		},
	)
	if base != nil {
		inner.ResolveBase = func() string { return base(item, scope) }
	}
	ApplyCommentStore(&inner, item, comments)
	dm := &diffModal{
		inner:  inner,
		label:  item.ProjectName + "/" + item.WorkspaceName,
		pr:     prLabel(item),
		scope:  scope,
		muted:  lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)),
		danger: lipgloss.NewStyle().Foreground(lipgloss.Color(colDanger)),
		// Padding(1, 1, 0, 1) rather than the body panel's usual 1 on every
		// side: the footer block already contributes a row of top padding, and
		// this is the one body panel that fills its whole height budget, so the
		// two paddings stack into a 2-row gap instead of being absorbed by the
		// pad block. Dropping ours leaves exactly the 1 row of breathing room
		// the convention is after.
		panel: lipgloss.NewStyle().Padding(1, 1, 0, 1),
	}
	return dm, dm.inner.Init()
}

// prLabel is the item's PR in `repo#number` form, or empty when the workspace
// isn't pinned to one. The repo half is the project name — the same name the
// deck groups rows under, so "awp#1234" reads the way you would say it.
//
// Rendered muted with the rest of the footer rather than in the palette's PR
// blue: the footer is styled as one line (the whole thing turns red on an error
// status), so a per-segment hue would have to fight that.
func prLabel(item Item) string {
	if item.PRNumber <= 0 {
		return ""
	}
	project := strings.TrimSpace(item.ProjectName)
	if project == "" {
		return fmt.Sprintf("#%d", item.PRNumber)
	}
	return fmt.Sprintf("%s#%d", project, item.PRNumber)
}

func (dm *diffModal) footerHelp() string {
	status, isErr := dm.inner.Status()
	style := dm.muted
	if isErr {
		style = dm.danger
	}
	// Name the base when the resolver has answered — "vs main" says what you are
	// reading against, where "vs stack base" only says how it was picked. Falls
	// back to the scope's own wording for the frame or two before the answer
	// lands, and permanently when no resolver is installed.
	against := dm.scope.String()
	if base := dm.inner.Base(); base != "" && dm.scope == ScopeStackBase {
		against = "vs " + base
	}
	// `? help` rather than a legend: the viewer owns the full keymap behind `?`,
	// and listing a chosen dozen bindings here spent the whole footer on an
	// answer to a question asked once.
	segs := []string{dm.label}
	if dm.pr != "" {
		// Beside the workspace rather than at the end: it names the same thing.
		segs = append(segs, dm.pr)
	}
	segs = append(segs, against)
	if status != "" {
		segs = append(segs, status)
	}
	segs = append(segs, "? help")
	return style.Render(strings.Join(segs, " · "))
}

func (dm *diffModal) update(m *Model, msg tea.Msg) tea.Cmd {
	if Trace != nil {
		defer traceSince(time.Now(), "diff.update %T", msg)
	}
	// While the viewer's filter has focus, or its `?` overlay is up, every key
	// belongs to it — including the ones that would otherwise close the modal.
	// Grabbing esc/q with the help open would close the diff instead of the help,
	// so the only way out of the reference would be out of the view.
	if key, ok := msg.(tea.KeyMsg); ok && !dm.inner.Filtering() && !dm.inner.HelpVisible() {
		switch key.String() {
		// Deliberately not `c`: that opens the comment box inside the viewer.
		// `c` opened this modal from the row list, but once it is up the key
		// belongs to the surface, not to closing it.
		case "esc", "q", "ctrl+c":
			m.active = nil
			return tea.ClearScreen
		}
	}
	updated, cmd := dm.inner.Update(msg)
	if inner, ok := updated.(ui.Model); ok {
		dm.inner = inner
	}
	return cmd
}

func (dm *diffModal) view(m *Model) (string, string) {
	// Panel padding matches every other deck body panel; the inner width
	// accounts for the 1 col of padding on each side.
	innerWidth := m.width - 2
	if innerWidth < 1 {
		return "", ""
	}
	bodyHeight := m.height - diffModalChrome
	dm.inner.SetSize(innerWidth, bodyHeight)
	if Trace == nil {
		return dm.panel.Render(dm.inner.Body(innerWidth, bodyHeight)), ""
	}
	// Timed in two halves: the viewer building its body, and the deck's panel
	// Render over the result. The second is a lipgloss pass over the whole frame,
	// so it is worth knowing separately from the first.
	bodyStart := time.Now()
	body := dm.inner.Body(innerWidth, bodyHeight)
	bodyMS := sinceMS(bodyStart)
	padStart := time.Now()
	out := dm.panel.Render(body)
	Trace("diff.body %.1fms pad %.1fms bytes %d→%d", bodyMS, sinceMS(padStart), len(body), len(out))
	return out, ""
}

// ApplyCommentStore installs a review store's seams on a viewer: commenting,
// replying, editing, deleting, sending to the agent, publishing, reviewed marks,
// and the mirrored GitHub threads — plus the re-read closures the refresh tick
// calls.
//
// Exported because the deck's modal is not the only host any more. `awp diff`
// runs the same viewer standalone, and a second copy of this list would drift:
// the failure mode is a seam wired in one surface and silently missing in the
// other, which reads to the user as a key that does nothing.
//
// Every seam is optional. A nil one leaves the viewer's corresponding action
// unavailable, which it reports rather than silently doing nothing.
func ApplyCommentStore(inner *ui.Model, item Item, comments CommentStore) {
	if comments.Save != nil {
		inner.SaveComment = func(c review.Comment) error { return comments.Save(item, c) }
	}
	if comments.Reply != nil {
		inner.ReplyComment = func(parentID string, c review.Comment) error {
			return comments.Reply(item, parentID, c)
		}
	}
	if comments.LastSaved != nil {
		inner.LastSavedComment = comments.LastSaved
	}
	if comments.Update != nil {
		inner.UpdateComment = func(c review.Comment) error { return comments.Update(item, c) }
	}
	if comments.Delete != nil {
		inner.DeleteComment = func(id string) error { return comments.Delete(item, id) }
	}
	if comments.Send != nil {
		inner.SendComment = func(c review.Comment) error { return comments.Send(item, c) }
	}
	if comments.Publish != nil {
		inner.PublishReview = func(verdict string) (string, error) {
			return comments.Publish(item, verdict)
		}
	}
	if comments.SaveReviewed != nil {
		inner.MarkReviewed = func(path, hash string) error { return comments.SaveReviewed(item, path, hash) }
	}
	if comments.LoadReviewed != nil {
		if marks, err := comments.LoadReviewed(item); err == nil {
			inner.SetReviewed(marks)
		}
	}
	if comments.Resolve != nil {
		inner.ResolveThread = func(id string, resolve bool) error { return comments.Resolve(item, id, resolve) }
	}
	if comments.LoadThreads != nil {
		if threads, err := comments.LoadThreads(item); err == nil && len(threads) > 0 {
			inner.SetThreads(threads)
		}
		// And on every refresh tick, the same as comments: the mirror is
		// maintained by the pr-status job, so a reviewer's comment arrives while
		// the diff is open rather than only on the next open.
		inner.LoadThreads = func() ([]review.Thread, error) { return comments.LoadThreads(item) }
	}
	if comments.Load != nil {
		// Best-effort: a review that cannot be read should still open as a
		// readable diff rather than refusing to open at all.
		if existing, err := comments.Load(item); err == nil {
			inner.SetComments(existing)
		}
		// And re-read on every refresh tick, so a finding filed while the view is
		// open — by an agent replying, most importantly — shows up without
		// closing and reopening.
		inner.LoadComments = func() ([]review.Comment, error) { return comments.Load(item) }
	}
}
