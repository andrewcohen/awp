package deckui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/andrewcohen/awp/internal/charm"
	"github.com/andrewcohen/awp/internal/review"
	"github.com/andrewcohen/awp/internal/ui"
)

// DiffScope is what a review is of. The surface is the same whichever it is;
// only the revision range the diff comes from differs.
//
// ScopeStackBase is the default because it is what a review is normally of — the
// whole change, against whatever it is stacked on. The other two are narrower
// readings of the same workspace, reached with `-` once the view is open rather
// than each owning an entry key of its own.
type DiffScope int

const (
	// ScopeStackBase is the whole change against its stack base — the nearest
	// stacked-parent bookmark, or trunk when nothing is stacked. What `c` opens.
	ScopeStackBase DiffScope = iota
	// ScopeWorking is the workspace's uncommitted change alone.
	ScopeWorking
	// ScopeTrunk is the whole stack against trunk, for reading a stacked change
	// together with everything below it.
	ScopeTrunk
)

func (s DiffScope) String() string {
	switch s {
	case ScopeWorking:
		return "working copy"
	case ScopeTrunk:
		return "vs trunk"
	default:
		return "vs stack base"
	}
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

// DiffScopeProvider is the `-` menu for a workspace: the ranges the view offers,
// the first being the one it opens on.
//
// The chord itself lives in the viewer (internal/ui/scope.go) rather than here.
// The deck had its own copy and standalone `awp diff` had none, so the same
// surface answered the same key two different ways depending on which door you
// came through. One implementation; hosts only supply the list.
type DiffScopeProvider func(item Item) []ui.ScopeOption

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
	//
	// Update carries a *revision*, which is a change to the body and nothing else:
	// the implementation keeps the stored state, timestamps and publish record, on the
	// grounds that an editor does not own them. Anything that needs to change a
	// comment's state has to say so through its own seam — see MarkPublished.
	Update func(item Item, c review.Comment) error
	Delete func(item Item, id string) error
	// MarkPublished records that a comment reached GitHub, against the id GitHub gave
	// it.
	//
	// Its own seam because Update cannot carry it and silently did not: a reply posted
	// from the viewer went out, came back through Update with State=Published, and had
	// that state dropped on the way to disk — so a reply sitting on the PR read as
	// `unsent` in the diff forever, and a later publish would have offered to send it
	// again. Two different intentions had one spelling; now they have two.
	MarkPublished func(item Item, id, remoteID string) error
	// Reply files a reply against a parent comment.
	Reply func(item Item, parentID string, c review.Comment) error
	// Approve says yes to an agent's proposal, returning the record as written.
	//
	// It returns the comment rather than an error alone because approving is half a
	// gesture: the viewer hands what comes back to Send, and the prompt the agent
	// receives is rendered from the record the store actually wrote.
	Approve func(item Item, id string) (review.Comment, error)
	// LastSaved returns the record Save just wrote, id included.
	LastSaved func() (review.Comment, bool)
	// Resolve toggles a GitHub review thread's resolved state.
	Resolve func(item Item, threadID string, resolve bool) error
	// ReplyToThread posts a reply into a GitHub review thread and returns the id of
	// the comment it created. That id is not a courtesy: it is what the local record
	// is marked with, and what stops the same reply being drawn twice or sent twice.
	ReplyToThread func(item Item, threadID, body string) (string, error)
	// LoadThreads returns the mirrored PR threads for this workspace's review.
	LoadThreads func(item Item) ([]review.Thread, error)
	// Send hands a saved comment to the workspace's agent. Nil leaves the
	// send-to-agent exit unavailable, which the editor reports rather than
	// silently doing nothing.
	Send CommentSender
	// Publish sends the review to its PR with a verdict — "approve",
	// "request-changes", "comment", or empty for the comments alone — and returns
	// what happened. Nil leaves the viewer's `P` unavailable.
	// dryRun asks for the plan — the calls it would make — without making any of
	// them, which is what the viewer shows before it will post anything.
	Publish func(item Item, verdict, summary string, dryRun bool) (string, error)
	// MergePR merges the item's PR and returns what gh reported. Nil leaves the
	// viewer's `M` unavailable.
	//
	// dryRun asks for the call it would make without making it — the same
	// contract Publish has, and what the viewer's confirm screen shows.
	//
	// Not a review-store operation, and here anyway: this struct is in practice
	// the whole set of seams a host lends the review surface, and the alternative
	// was a second bundle threaded through the same two call sites for one field.
	// The deck's own `p m` does not go through it — that path has the row's
	// cached PR status and the jobs subsystem, neither of which the viewer has.
	MergePR func(item Item, dryRun bool) (string, error)
}

// CommentSender delivers a comment to a workspace's agent.
type CommentSender func(item Item, c review.Comment) error

// diffModalChrome is the rows the deck's chrome takes around the viewer's
// body: the panel's own vertical padding, 2 for the viewer's pane borders, and
// the footer block.
//
// It has to be exact. Over-reserving does not shrink the frame — the deck pads
// whatever is left over to pin the footer to the bottom — it just converts the
// rows into a blank band above the footer. It was written as a literal 8 once,
// two rows too many, which is what put a visible gap under the diff; derived
// from layout.go it cannot drift from the padding it is describing.
const diffModalChrome = panelRows + 2 + footerRows

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
	pr string
	// item is the row this is a review of, kept so `-` can rebuild the modal at
	// another scope without going back through the cursor.
	item  Item
	scope DiffScope
	// Styles are cached here rather than built per frame — view and
	// footerHelp are render paths.
	muted  lipgloss.Style
	danger lipgloss.Style
	panel  lipgloss.Style
}

// newDiffModal builds the modal and returns the command that loads the
// first diff.
func newDiffModal(item Item, scope DiffScope, load DiffLoader, open DiffOpener, base DiffBaseResolver, scopes DiffScopeProvider, comments CommentStore) (*diffModal, tea.Cmd) {
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
	if scopes != nil {
		// The `-` menu. The viewer owns the chord (internal/ui/scope.go) so the deck
		// and standalone `awp diff` cannot answer the same key differently.
		inner.WithScopes(scopes(item))
	}
	// The keys the deck takes before the viewer sees them. Documented here rather
	// than in the deck's own `?` overlay because that one is unreachable while this
	// is open — `?` opens the viewer's. `-` is not among them: the viewer owns it
	// now, and documents it itself.
	inner.HostKeys = []charm.KeyGroup{{
		Title: "In the deck",
		Keys:  [][2]string{{"esc / q / " + PaneLeaveKey, "back to the deck"}},
	}}
	ApplyCommentStore(&inner, item, comments)
	dm := &diffModal{
		inner:  inner,
		label:  item.ProjectName + "/" + item.WorkspaceName,
		pr:     PRLabel(item),
		item:   item,
		scope:  scope,
		muted:  lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)),
		danger: lipgloss.NewStyle().Foreground(lipgloss.Color(colDanger)),
		// The shared body-panel inset. This used to be Padding(1, 1, 0, 1) —
		// asymmetric, because back when the footer carried a row of top padding
		// the two stacked into a 2-row gap under the diff. The panels have no
		// vertical padding now, so there is nothing left to compensate for and
		// the one panel that fills its whole height budget can wear what the
		// others wear.
		panel: lipgloss.NewStyle().Padding(panelPadY, panelPadX),
	}
	return dm, dm.inner.Init()
}

// PRLabel is the item's PR in `repo#number` form, or empty when the workspace
// isn't pinned to one. The repo half is the project name — the same name the
// deck groups rows under, so "awp#1234" reads the way you would say it.
//
// Exported because standalone `awp diff` names its PR the same way, and two
// spellings of the same label is how the two surfaces start looking like
// different programs.
//
// Rendered muted with the rest of the footer rather than in the palette's PR
// blue: the footer is styled as one line (the whole thing turns red on an error
// status), so a per-segment hue would have to fight that.
func PRLabel(item Item) string {
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
	if label := dm.inner.ScopeLabel(); label != "" {
		// Whatever `-` last switched to, which the viewer owns.
		against = label
	}
	if base := dm.inner.Base(); base != "" {
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
	if key, ok := msg.(tea.KeyPressMsg); ok && !dm.inner.Filtering() && !dm.inner.HelpVisible() {
		switch key.String() {
		// Deliberately not `c`: that opens the comment box inside the viewer.
		// `c` opened this modal from the row list, but once it is up the key
		// belongs to the surface, not to closing it.
		case "esc", "q", "ctrl+c", PaneLeaveKey:
			// ctrl+\ means "give the keyboard back to whatever put me here"
			// everywhere else in awp, and the deck is what put this here. The
			// viewer binds it too, for when it is the whole program in a pane; this
			// arm is what makes the same key do the same thing when it is a modal.
			m.active = nil
			return nil
		}
	}
	return dm.forward(msg)
}

// forward hands a message to the wrapped viewer and keeps the updated model.
func (dm *diffModal) forward(msg tea.Msg) tea.Cmd {
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
	if comments.MarkPublished != nil {
		inner.RecordPublished = func(id, remoteID string) error {
			return comments.MarkPublished(item, id, remoteID)
		}
	}
	if comments.Send != nil {
		inner.SendComment = func(c review.Comment) error { return comments.Send(item, c) }
	}
	if comments.Approve != nil {
		inner.ApproveProposal = func(id string) (review.Comment, error) { return comments.Approve(item, id) }
	}
	if comments.Publish != nil {
		inner.PublishReview = func(verdict, summary string, dryRun bool) (string, error) {
			return comments.Publish(item, verdict, summary, dryRun)
		}
	}
	if comments.MergePR != nil && item.PRNumber > 0 {
		// Gated on the PR here rather than refused later, because "this review has
		// no PR" and "this host offers no merging" are one fact from the keyboard —
		// the key does nothing — and the viewer says exactly that for a nil seam.
		inner.MergePR = func(dryRun bool) (string, error) { return comments.MergePR(item, dryRun) }
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
	if comments.ReplyToThread != nil {
		inner.ReplyToThread = func(id, body string) (string, error) {
			return comments.ReplyToThread(item, id, body)
		}
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
