package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/andrewcohen/awp/internal/agenthooks"
	"github.com/andrewcohen/awp/internal/charm"
	"github.com/andrewcohen/awp/internal/config"
	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/github"
	"github.com/andrewcohen/awp/internal/jj"
	"github.com/andrewcohen/awp/internal/jobs"
	"github.com/andrewcohen/awp/internal/portcapture"
	"github.com/andrewcohen/awp/internal/state"
	"github.com/andrewcohen/awp/internal/tmux"
	"github.com/andrewcohen/awp/internal/ui"
	"github.com/andrewcohen/awp/internal/watch"
	"github.com/andrewcohen/awp/internal/workspace"
)

// timeNowForJobs is a small indirection so a future test can stub
// the clock used by orphan detection / GC. Production callers always
// receive time.Now().
var timeNowForJobs = time.Now

func itoa(i int) string { return strconv.Itoa(i) }

// DeckSessionName returns the tmux session name for a workspace: "[awp]<repo>__<workspace>".
func DeckSessionName(repo, workspace string) string {
	return "[awp]" + repo + "__" + workspace
}

const deckSessionPrefix = "[awp]"

// deckEnrichTimeout bounds the per-refresh jj HEAD enrichment fan-out in
// loadDeckItems. jj log takes the repo operation-log lock, so a refresh
// that overlaps a workspace-create/delete subprocess can block until that
// op releases the lock; without a ceiling a stuck jj would wedge the
// refresh (and thus the deck's whole background poll) forever. Rows still
// render from state when enrichment times out; the next refresh fills in
// the HEAD descriptions once the lock clears.
const deckEnrichTimeout = 4 * time.Second

type noopReporter struct{}

func (noopReporter) Step(string) {}
func (noopReporter) Log(string)  {}

// recoverRepoRootFromSession returns the repo root for the workspace whose
// awp tmux session the deck was launched from. Used when the popup's CWD
// doesn't resolve to a jj repo (e.g. it landed in $HOME because the parent
// pane was running `less` and tmux couldn't read its cwd). Returns false
// if we're not in an [awp]... session or no matching workspace is known.
func recoverRepoRootFromSession(tmuxClient *tmux.Client, svc workspace.Service) (string, bool) {
	sessionName, err := tmuxClient.CurrentSessionName()
	if err != nil {
		return "", false
	}
	repo, ws, ok := parseAwpSession(strings.TrimSpace(sessionName))
	if !ok {
		return "", false
	}
	all, err := svc.ListAll()
	if err != nil {
		return "", false
	}
	for _, e := range all {
		if e.ProjectName == repo && e.Name == ws && strings.TrimSpace(e.RepoRoot) != "" {
			return e.RepoRoot, true
		}
	}
	return "", false
}

// parseAwpSession parses "[awp]<repo>__<workspace>" into (repo, workspace, true).
func parseAwpSession(name string) (string, string, bool) {
	if !strings.HasPrefix(name, deckSessionPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(name, deckSessionPrefix)
	idx := strings.Index(rest, "__")
	if idx < 0 {
		return "", "", false
	}
	return rest[:idx], rest[idx+2:], true
}

type fixedDirRunner struct {
	base Runner
	dir  string
}

func (r fixedDirRunner) Run(ctx context.Context, dir string, name string, args ...string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		dir = r.dir
	}
	return r.base.Run(ctx, dir, name, args...)
}

// deckDebugLogPath is the always-on diagnostic log for deck-side async work
// (currently: the PR-status fetcher). Best-effort writes — log failures never
// surface to the user. Tail with `tail -f /tmp/awp-deck.log` while running
// `awp deck` to see what gh saw on each repo without crowding the TUI.
const deckDebugLogPath = "/tmp/awp-deck.log"

func deckDebugLogf(format string, args ...any) {
	f, err := os.OpenFile(deckDebugLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, "%s "+format+"\n", append([]any{time.Now().Format("15:04:05.000")}, args...)...)
}

// persistPRStatusMerge merges fresh per-repo results into the on-disk
// cache, per-PR-number. New entries overwrite (so transitions like
// open→merged land); existing entries that aren't in the fresh fetch
// are kept untouched — used by the review write-through path, which
// only knows about the one PR it just fetched.
//
// For the periodic bulk-fetch path use persistPRStatusBulkMerge: that
// variant also prunes terminal cached PRs that have drifted out of
// `gh pr list --limit 100` so the cache doesn't grow monotonically.
func persistPRStatusMerge(byRepo map[string]map[string]deckui.PRStatus, fetchedAt time.Time) {
	if len(byRepo) == 0 {
		return
	}
	// Serialize against persistPRStatusBulkMerge: both do a
	// load→merge→save read-modify-write on the same on-disk cache file.
	// The single-PR write-through path (review) can fire while a bulk
	// fetch is mid-flight; without sharing the lock the two would clobber
	// each other's merged entries.
	prStatusCacheMu.Lock()
	defer prStatusCacheMu.Unlock()
	persistByRepo, persistFetchedAt, loadErr := loadPRStatusCache()
	if loadErr != nil {
		deckDebugLogf("prStatus cache reload err=%v", loadErr)
		persistByRepo = map[string]map[string]deckui.PRStatus{}
		persistFetchedAt = map[string]time.Time{}
	}
	for repo, fresh := range byRepo {
		existing := persistByRepo[repo]
		if existing == nil {
			existing = map[string]deckui.PRStatus{}
		}
		for head, status := range fresh {
			existing[head] = status
		}
		persistByRepo[repo] = existing
		persistFetchedAt[repo] = fetchedAt
	}
	if saveErr := savePRStatusCache(persistByRepo, persistFetchedAt); saveErr != nil {
		deckDebugLogf("prStatus cache save err=%v", saveErr)
	}
}

// prStatusCacheMu serializes every load→merge→save read-modify-write on
// the on-disk PR-status cache: the concurrent per-repo fetches in the
// pr-status job (persistPRStatusBulkMerge) and the single-PR
// write-through from `awp review` (persistPRStatusMerge). All writers
// share this one lock so none clobbers another's cache entries.
var prStatusCacheMu sync.Mutex

// persistPRStatusBulkMerge is the bulk-fetch persistence path. It
// merges fresh per-repo PRs into the cache like persistPRStatusMerge,
// then prunes cached entries that:
//   - have a terminal State (MERGED or CLOSED) — once terminal, the
//     PR can't transition further, so a cached "last-known state" is
//     just historical noise; AND
//   - whose headRefName is NOT in this repo's fresh set; AND
//   - whose PR number is NOT in pinnedByRepo[repo] (workspaces with
//     an explicit PRNumber pin keep their PR in the cache regardless
//     of bulk-window drift so the next refresh doesn't have to
//     topUpMissingOverrides them back in).
//
// Non-terminal cached entries missing from the fresh set are left
// alone: an OPEN PR temporarily absent from gh's response (race,
// label filter glitch, etc.) is more useful kept than dropped, and a
// future fetch will eventually surface its terminal state if it ever
// closes.
//
// Defensive: a repo whose fresh set is empty is treated as "nothing
// to merge, definitely don't prune" — that's the upstream-error shape
// (gh returned [] when we know there are PRs). The throttle stamp
// still updates so the next refresh respects the cooldown.
// completeByRepo[repo] == true means the fresh set for that repo is the
// COMPLETE list of its open PRs (the `gh pr list --state open` fetch was
// not truncated at the limit). When complete, a cached entry absent from
// the fresh set is definitively no longer open — it merged or closed —
// so it's pruned regardless of its (stale) cached state, not just when
// the cached state is already terminal. This is what clears a PR that
// merged out of the open list: under `--state open` it never comes back
// as MERGED to overwrite the stale OPEN entry, so without this it would
// linger forever (and, if it had ReviewRequested/IsInMergeQueue set,
// show as a phantom inbox row). When the fetch WAS truncated (== limit),
// absence is ambiguous (could be an open PR beyond the cap), so we fall
// back to the conservative terminal-only prune. Pinned PRs are always
// kept either way — their real terminal state arrives via the top-up.
func persistPRStatusBulkMerge(byRepo map[string]map[string]deckui.PRStatus, pinnedByRepo map[string]map[int]bool, completeByRepo map[string]bool, fetchedAt time.Time) {
	if len(byRepo) == 0 {
		return
	}
	// The pr-status job now fetches repos concurrently, so multiple
	// goroutines call this with one repo each. The load→merge→save below
	// is a read-modify-write on a single shared file; without this lock
	// two concurrent writers would each load, merge their own repo, and
	// save — the last one clobbering the other's entry. Serialize it.
	prStatusCacheMu.Lock()
	defer prStatusCacheMu.Unlock()
	persistByRepo, persistFetchedAt, loadErr := loadPRStatusCache()
	if loadErr != nil {
		deckDebugLogf("prStatus cache reload err=%v", loadErr)
		persistByRepo = map[string]map[string]deckui.PRStatus{}
		persistFetchedAt = map[string]time.Time{}
	}
	for repo, fresh := range byRepo {
		if len(fresh) == 0 {
			persistFetchedAt[repo] = fetchedAt
			continue
		}
		existing := persistByRepo[repo]
		if existing == nil {
			existing = map[string]deckui.PRStatus{}
		}
		pinned := pinnedByRepo[repo]
		complete := completeByRepo[repo]
		pruned := 0
		for head, cached := range existing {
			if _, inFresh := fresh[head]; inFresh {
				continue
			}
			if pinned[cached.Number] {
				continue
			}
			// Complete fetch → absence means "no longer open", so drop it
			// whatever its stale state says. Truncated fetch → only drop
			// entries already known terminal (an absent OPEN entry might
			// just be beyond the open-list cap).
			if complete || isTerminalPRState(cached.State) {
				delete(existing, head)
				pruned++
			}
		}
		for head, status := range fresh {
			existing[head] = status
		}
		persistByRepo[repo] = existing
		persistFetchedAt[repo] = fetchedAt
		if pruned > 0 {
			deckDebugLogf("prStatus prune repo=%s dropped=%d remaining=%d", repo, pruned, len(existing))
		}
	}
	if saveErr := savePRStatusCache(persistByRepo, persistFetchedAt); saveErr != nil {
		deckDebugLogf("prStatus cache save err=%v", saveErr)
	}
}

// isTerminalPRState reports whether a PR's state is one we can safely
// drop from the cache when the bulk fetch no longer returns it.
// MERGED and CLOSED are terminal — once a PR is in either state it
// doesn't transition further.
func isTerminalPRState(s deckui.PRState) bool {
	return s == deckui.PRStateMerged || s == deckui.PRStateClosed
}

// migrateBookmarkPRNumbersIfNeeded resolves Bookmark → PR number using
// the on-disk pr-status cache and persists the result on workspace
// entries that pre-date the PROverride→PRNumber collapse (i.e. have a
// bookmark but no PRNumber). Each successfully resolved entry is
// rewritten via store.Update; entries we couldn't match are left
// alone — they'll be retried next load if the cache picks the PR up.
//
// Mutates repoMap in place so the caller's downstream Item construction
// reflects the freshly populated PRNumber field without re-reading the
// store.
func migrateBookmarkPRNumbersIfNeeded(store *state.JSONStore, repoMap map[string]map[string]workspace.Entry) {
	cache, _, err := loadPRStatusCache()
	if err != nil || len(cache) == 0 {
		return
	}
	for repo, entries := range repoMap {
		byHead, ok := cache[repo]
		if !ok || len(byHead) == 0 {
			continue
		}
		for name, e := range entries {
			if e.PRNumber > 0 {
				continue
			}
			bm := strings.TrimSpace(e.Bookmark)
			if bm == "" {
				continue
			}
			status, found := byHead[bm]
			if !found || status.Number <= 0 {
				continue
			}
			e.PRNumber = status.Number
			entries[name] = e
			capturedRepo := repo
			capturedName := name
			capturedPR := status.Number
			if uerr := store.Update(repo, func(persisted map[string]workspace.Entry) map[string]workspace.Entry {
				if cur, ok := persisted[capturedName]; ok && cur.PRNumber == 0 {
					cur.PRNumber = capturedPR
					persisted[capturedName] = cur
				}
				return persisted
			}); uerr != nil {
				deckDebugLogf("pr-number migrate err repo=%s ws=%s pr=%d err=%v", capturedRepo, capturedName, capturedPR, uerr)
			} else {
				deckDebugLogf("pr-number migrate ok repo=%s ws=%s bookmark=%s pr=%d", capturedRepo, capturedName, bm, capturedPR)
			}
		}
	}
}

// prHasHead reports whether the byHead map (repo's PR cache) contains the
// given bookmark/headRefName. Used by the diagnostic log line in the link
// handler so the user can see at a glance whether the chosen bookmark
// matched any fetched PR's headRefName.
func prHasHead(byHead map[string]deckui.PRStatus, head string) bool {
	if byHead == nil {
		return false
	}
	_, ok := byHead[head]
	return ok
}

// sortedHeads returns the deduplicated, sorted set of headRefName keys from a
// byHead map. Used only by deckDebugLogf so the log line is stable.
func sortedHeads(byHead map[string]deckui.PRStatus) []string {
	heads := make([]string, 0, len(byHead))
	for h := range byHead {
		heads = append(heads, h)
	}
	sort.Strings(heads)
	return heads
}

// sortedPRNumbers returns the sorted PR numbers from a byHead map. Used
// by deckDebugLogf so a "pr-override" line can show the user whether
// the PR # they typed is in the cache at all.
func sortedPRNumbers(byHead map[string]deckui.PRStatus) []int {
	nums := make([]int, 0, len(byHead))
	for _, s := range byHead {
		if s.Number > 0 {
			nums = append(nums, s.Number)
		}
	}
	sort.Ints(nums)
	return nums
}

// prCacheHasNumber reports whether the byHead map contains a PR with
// the given number. Mirrors prHasHead for the `p s` chord's diagnostic.
func prCacheHasNumber(byHead map[string]deckui.PRStatus, n int) bool {
	if byHead == nil || n <= 0 {
		return false
	}
	for _, s := range byHead {
		if s.Number == n {
			return true
		}
	}
	return false
}

func newDeckActionServiceWithIO(runner Runner, repoRoot string, in io.Reader, out io.Writer) workspace.Service {
	fr := fixedDirRunner{base: runner, dir: repoRoot}
	return workspace.NewService(workspace.Dependencies{
		JJ:            jj.New(fr),
		Tmux:          tmux.New(runner),
		Store:         state.NewJSONStore(),
		Hooks:         config.NewFileHookProvider(),
		Runner:        fr,
		InvocationDir: repoRoot,
		Input:         in,
		Out:           out,
	})
}

func newDeckActionService(runner Runner, repoRoot string, in io.Reader) workspace.Service {
	return newDeckActionServiceWithIO(runner, repoRoot, in, io.Discard)
}

// paneHost is where a workspace's processes live when awp owns them instead of
// handing them to tmux.
//
// It is one interface rather than deckui.PaneBackend plus an optional extra,
// because every action that addresses "the workspace's agent" has to agree on
// which agent that is. A backend that can show you an agent but cannot be sent
// a prompt would leave the deck talking to two different processes — which is
// exactly the bug this type exists to make unspellable.
type paneHost interface {
	deckui.PaneBackend
	// SendPrompt delivers text to the agent the backend hosts.
	SendPrompt(item deckui.Item, text string, reporter deckui.Reporter) error
}

// paneHostAction routes the actions that address a workspace's agent, or a
// tmux client, to the pane host. handled is false when there is no host, or
// when the action is not one of those, and the caller should fall through to
// the ordinary tmux path.
//
// This is one switch rather than a check at each call site because the failure
// it prevents is silent: every action below it assumes tmux, so a missed case
// does not error — it talks to an agent the user cannot see, or asks a client
// that is not there to switch, and returns success.
func paneHostAction(panes paneHost, req deckui.ActionRequest, reporter deckui.Reporter) (err error, handled bool) {
	if panes == nil {
		return nil, false
	}
	switch req.Action {
	case deckui.ActionSendPrompt:
		return panes.SendPrompt(req.Item, req.Arg, reporter), true
	case deckui.ActionLastSession:
		// `tmux switch-client -l` from outside tmux exits 0 having done
		// nothing, so without this the key is a silent no-op.
		return errors.New("last session: nothing to switch to — this deck hosts its own panes"), true
	}
	return nil, false
}

// hostFirst wraps the deck's tmux action handler so the pane host is offered
// every action before the tmux handler sees any of them.
//
// The order is the whole point, and it used to live *inside* the handler, one
// early return away from being wrong — which it was: the ActionCustom branch
// returned above the interception, so `x` opened a tmux window on a deck that
// hosts its own panes and the host was never asked. Nothing errored; the
// action just happened somewhere the user could not see.
//
// Wrapping puts the interception outside the handler, where no branch of it
// can jump the queue. Declining is paneHostAction's job and its alone, which
// is the contract it already had.
func hostFirst(panes paneHost, tmuxHandler func(deckui.ActionRequest) error) func(deckui.ActionRequest) error {
	return func(req deckui.ActionRequest) error {
		reporter := req.Reporter
		if reporter == nil {
			reporter = noopReporter{}
		}
		if err, handled := paneHostAction(panes, req, reporter); handled {
			return err
		}
		return tmuxHandler(req)
	}
}

func runDeckWithCharm(runner Runner, svc workspace.Service, in io.Reader, out io.Writer, initialScope deckui.Scope, panes paneHost) error {
	// The deck needs tmux because every window key hands off to a tmux client.
	// A deck with a pane backend hosts those itself, so it is the one thing
	// that can be the outermost program — which is the whole point of it, and
	// requiring a multiplexer above it would defeat that.
	if panes == nil && os.Getenv("TMUX") == "" {
		return fmt.Errorf("awp deck must run inside tmux (hint: bind a display-popup -E awp deck)")
	}
	if charm.IsDumbTerminal() {
		return fmt.Errorf("awp deck not available in dumb terminal")
	}
	if runner == nil {
		runner = NewExecRunner()
	}
	// Drain the pending-kills queue on exit so deletes always tear down
	// their tmux sessions, regardless of whether the user wired
	// `run-shell "awp deck-cleanup"` into their popup binding. The
	// tmux-binding path remains a redundant safety net; drainPendingActions
	// is idempotent on an empty/missing queue file.
	defer func() { _ = runDeckCleanup(runner, out) }()
	j := jj.New(runner)
	tmuxClient := tmux.New(runner)

	repoRoot, err := j.RepoRoot()
	rootBad := err != nil || workspace.IsHomeDir(repoRoot)
	if rootBad {
		// The popup spawned with a CWD that isn't a project (e.g. tmux's
		// `pane_current_path` resolved to $HOME because the parent pane
		// was running `less` and proc lookup couldn't read its cwd).
		// If we're inside an [awp]<repo>__<workspace> session, recover
		// the repo root from the cross-repo state instead of giving up.
		if recovered, ok := recoverRepoRootFromSession(tmuxClient, svc); ok {
			repoRoot = recovered
			runner = fixedDirRunner{base: runner, dir: repoRoot}
			j = jj.New(runner)
			svc = newDeckActionServiceWithIO(runner, repoRoot, in, out)
		} else if err != nil {
			return fmt.Errorf("not a jj repository: %w", err)
		} else {
			return fmt.Errorf("refusing to open deck at $HOME — cd into a project first")
		}
	}
	projectName := filepath.Base(repoRoot)
	// First paint is JSON-only — no jj, no tmux. Rows render with their
	// last-known status from workspace-state.json. The Stale/Active "caution"
	// decorations require live tmux state, which we deliberately don't have
	// yet, so loadDeckItems suppresses them on the snap.known=false path.
	// The Init-driven enrichment refresh fills in real decorations within
	// a few tens of ms.
	// Fast first paint: skip jj entirely and skip the heavy tmux probes
	// (ListSessions/ListPanes), but still ask tmux for the current
	// session name so the initial cursor lands on the workspace the
	// user launched from. The full enrichment refresh follows ~50 ms
	// later and fills in caution glyphs / stale decorations.
	// awp deck's processes live in tmux, so tmux is what it asks. zdeck swaps
	// this for the zmx source; nothing below here knows which it got.
	sessionSource := deckSessions(tmuxSessions{client: tmuxClient})
	items, err := loadDeckItems(nil, sessionSource, true, svc, repoRoot, projectName, nil, nil)
	if err != nil {
		return err
	}

	cfg, _ := config.Load(repoRoot)
	var userActions []deckui.UserAction
	for name, act := range cfg.Actions {
		focus := true
		if act.Focus != nil {
			focus = *act.Focus
		}
		userActions = append(userActions, deckui.UserAction{
			Name:       name,
			Command:    act.Command,
			Alias:      act.Alias,
			Background: act.Background,
			Focus:      focus,
		})
	}
	// userActionsForRepo loads the merged global+per-repo actions for
	// the workspace at repoRoot. Returning a fresh list lets the deck
	// resolve actions against the SELECTED workspace's repo each time
	// the action menu opens — cross-project decks would otherwise show
	// the deck-startup repo's actions for every selection.
	userActionsForRepo := func(root string) []deckui.UserAction {
		c, err := config.Load(root)
		if err != nil {
			return nil
		}
		out := make([]deckui.UserAction, 0, len(c.Actions))
		for name, act := range c.Actions {
			focus := true
			if act.Focus != nil {
				focus = *act.Focus
			}
			out = append(out, deckui.UserAction{
				Name:       name,
				Command:    act.Command,
				Alias:      act.Alias,
				Background: act.Background,
				Focus:      focus,
			})
		}
		return out
	}
	resolveUserAction := func(root, name string) (deckui.UserAction, bool) {
		for _, a := range userActionsForRepo(root) {
			if a.Name == name {
				return a, true
			}
		}
		return deckui.UserAction{}, false
	}
	tmuxHandler := func(req deckui.ActionRequest) error {
		if req.Action == deckui.ActionCreateWorkspace {
			dir := strings.TrimSpace(req.Item.RepoRoot)
			if dir == "" {
				dir = repoRoot
			}
			fr := fixedDirRunner{base: runner, dir: dir}
			actionSvc := newDeckActionService(runner, dir, in)
			reporter := req.Reporter
			if reporter == nil {
				reporter = noopReporter{}
			}
			return openWorkspaceWithReporter(fr, actionSvc, openRequest{
				Name:             req.Workspace.Name,
				Bookmark:         req.Workspace.Bookmark,
				BookmarkToCreate: req.Workspace.BookmarkToCreate,
				Prompt:           req.Workspace.Prompt,
				PRNumber:         req.Workspace.PRNumber,
				Yes:              true,
				PaneHosted:       req.PaneHosted,
			}, reporter)
		}
		if req.Action == deckui.ActionReview {
			n, err := strconv.Atoi(req.Arg)
			if err != nil {
				return fmt.Errorf("review: invalid PR number %q", req.Arg)
			}
			dir := strings.TrimSpace(req.Item.RepoRoot)
			if dir == "" {
				dir = repoRoot
			}
			fr := fixedDirRunner{base: runner, dir: dir}
			reviewSvc := newDeckActionServiceWithIO(runner, dir, nil, io.Discard)
			reporter := req.Reporter
			if reporter == nil {
				reporter = noopReporter{}
			}
			return runReviewWithReporter(fr, reviewSvc, n, nil, reporter, reviewOpts{paneHosted: req.PaneHosted})
		}
		reporter := req.Reporter
		if reporter == nil {
			reporter = noopReporter{}
		}
		if req.Action == deckui.ActionCustom {
			lookupRoot := strings.TrimSpace(req.Item.RepoRoot)
			if lookupRoot == "" {
				lookupRoot = repoRoot
			}
			ua, ok := resolveUserAction(lookupRoot, req.Arg)
			if !ok {
				return fmt.Errorf("unknown user action %q in %s", req.Arg, lookupRoot)
			}
			actionSvc := svc
			if strings.TrimSpace(req.Item.RepoRoot) != "" {
				actionSvc = newDeckActionService(runner, req.Item.RepoRoot, in)
			}
			return openCustomActionWindow(tmuxClient, actionSvc, req.Item, ua, reporter)
		}
		actionSvc := svc
		if strings.TrimSpace(req.Item.RepoRoot) != "" {
			actionSvc = newDeckActionService(runner, req.Item.RepoRoot, in)
		}
		return handleDeckAction(tmuxClient, actionSvc, runner, req, reporter)
	}
	handler := hostFirst(panes, tmuxHandler)
	bookmarkFetcher := func(itemRepoRoot string) tea.Cmd {
		return func() tea.Msg {
			dir := strings.TrimSpace(itemRepoRoot)
			if dir == "" {
				dir = repoRoot
			}
			fr := fixedDirRunner{base: runner, dir: dir}
			if out, err := fr.Run(context.Background(), dir, "jj", "git", "fetch"); err != nil {
				return deckui.BookmarksDoneMsg{Err: fmt.Errorf("jj git fetch: %w: %s", err, out)}
			}
			names, err := jj.New(fr).AllBookmarksByRecency()
			if err != nil {
				return deckui.BookmarksDoneMsg{Err: err}
			}
			return deckui.BookmarksDoneMsg{Bookmarks: names}
		}
	}
	refresher := func() tea.Cmd {
		return func() tea.Msg {
			items, err := loadDeckItems(j, sessionSource, false, svc, repoRoot, projectName, in, out)
			return deckui.RefreshDoneMsg(items, err)
		}
	}
	prFetcher := func(itemRepoRoot string) tea.Cmd {
		return func() tea.Msg {
			dir := strings.TrimSpace(itemRepoRoot)
			if dir == "" {
				dir = repoRoot
			}
			gh := github.New(runner, dir)
			prs, err := gh.ListPRs()
			if err != nil {
				return deckui.PRFetchDoneMsg{Err: err}
			}
			items := make([]deckui.PRItem, len(prs))
			for i, pr := range prs {
				author := pr.Author.Login
				if author == "" {
					author = "?"
				}
				items[i] = deckui.PRItem{
					Number:  pr.Number,
					Title:   pr.Title,
					HeadRef: pr.HeadRef,
					Author:  author,
					IsDraft: pr.IsDraft,
					Labels:  pr.LabelNames(),
				}
			}
			return deckui.PRFetchDoneMsg{PRs: items}
		}
	}
	// PR-status cache survives deck restarts. Loading it before the model
	// is built means the 60s refresh throttle has a meaningful cooldown
	// across opens; a deck closed and reopened within a minute reuses the
	// cached glyphs without re-running gh.
	cachedByRepo, cachedFetchedAt, cacheErr := loadPRStatusCache()
	if cacheErr != nil {
		deckDebugLogf("prStatus cache load err=%v", cacheErr)
	}

	// prStatusFetcher dispatches PR-status work to a detached subprocess
	// (awp internal pr-status-job) so killing the deck mid-fetch doesn't
	// drop in-flight gh calls. Pipeline:
	//
	//   - If no live job exists, spawn one for the supplied repos.
	//   - If a live job is already running (from a previous deck open
	//     that was closed mid-fetch, or a sibling deck), reuse it.
	//   - Either way, poll the run file + cache every 250ms and emit
	//     one PRStatusRepoDoneMsg per repo as its entry appears in the
	//     cache. When the run file disappears (subprocess finished),
	//     emit the closing PRStatusDoneMsg.
	prStatusFetcher := func(repos []string) tea.Cmd {
		return func() tea.Msg {
			pollStartedAt := time.Now()
			deckDebugLogf("prStatus fetch start repos=%d", len(repos))
			if len(repos) == 0 {
				return deckui.PRStatusDoneMsg{FetchedAt: pollStartedAt}
			}
			if existing, ok := findActivePRStatusJob(); ok {
				deckDebugLogf("prStatus reusing job id=%s pid=%d", existing.ID, existing.PID)
			} else {
				job, err := spawnPRStatusJob(repos)
				if err != nil {
					deckDebugLogf("prStatus spawn err=%v", err)
					return deckui.PRStatusDoneMsg{FetchedAt: pollStartedAt}
				}
				deckDebugLogf("prStatus spawned detached job id=%s pid=%d repos=%d", job.ID, job.PID, len(repos))
			}
			return pollPRStatusJob(repos, map[string]bool{}, pollStartedAt)
		}
	}
	// bookmarkLinkHandler persists a chosen bookmark onto the workspace's
	// stored Entry so the PR glyph can resolve via Entry.Bookmark → PR
	// headRefName on the next refresh. Operates on the workspace's own
	// repoRoot (which may differ from the deck's current repoRoot when
	// scope=all surfaces a row from another project).
	linkStore := state.NewJSONStore()
	bookmarkLinkHandler := func(item deckui.Item, bookmark string) error {
		repo := strings.TrimSpace(item.RepoRoot)
		if repo == "" {
			return fmt.Errorf("workspace %q has no repo root", item.WorkspaceName)
		}
		bm := strings.TrimSpace(bookmark)
		if bm == "" {
			return fmt.Errorf("empty bookmark")
		}
		name := item.WorkspaceName
		updated := false
		if err := linkStore.Update(repo, func(entries map[string]workspace.Entry) map[string]workspace.Entry {
			if cur, ok := entries[name]; ok {
				cur.Bookmark = bm
				entries[name] = cur
				updated = true
			}
			return entries
		}); err != nil {
			return err
		}
		if !updated {
			return fmt.Errorf("workspace %q not found in store for repo %s", name, repo)
		}
		// Diagnostic: log what the PR-status cache currently knows about
		// this repo so a "I linked X but no glyph appeared" investigation
		// can compare the chosen bookmark name against the headRefName
		// keys the cache holds. The mismatch is the common cause when the
		// PR actually exists.
		cached, _, _ := loadPRStatusCache()
		heads := sortedHeads(cached[repo])
		deckDebugLogf("link ws=%s repo=%s bookmark=%s cache_heads=%v match=%t",
			name, repo, bm, heads, prHasHead(cached[repo], bm))
		return nil
	}
	// prNumberLinkHandler persists a PR-number override onto the
	// workspace's stored Entry. Drives the deck `p s` chord.
	// prNumber == 0 clears the override.
	prNumberLinkHandler := func(item deckui.Item, prNumber int) error {
		repo := strings.TrimSpace(item.RepoRoot)
		if repo == "" {
			return fmt.Errorf("workspace %q has no repo root", item.WorkspaceName)
		}
		name := item.WorkspaceName
		updated := false
		if err := linkStore.Update(repo, func(entries map[string]workspace.Entry) map[string]workspace.Entry {
			if cur, ok := entries[name]; ok {
				cur.PRNumber = prNumber
				entries[name] = cur
				updated = true
			}
			return entries
		}); err != nil {
			return err
		}
		if !updated {
			return fmt.Errorf("workspace %q not found in store for repo %s", name, repo)
		}
		// Diagnostic: log whether the PR # the user typed is actually in
		// the current PR-status cache. A `match=false` here with a
		// non-empty cache_numbers list is the most common failure mode —
		// either the PR is older than the gh `--limit 100` window, or
		// the deck hasn't fetched this repo yet.
		cached, fetchedAt, _ := loadPRStatusCache()
		numbers := sortedPRNumbers(cached[repo])
		fetched := "never"
		if t, ok := fetchedAt[repo]; ok {
			fetched = t.Format("15:04:05")
		}
		deckDebugLogf("pr-override ws=%s repo=%s pr=%d cache_count=%d cache_numbers=%v fetched_at=%s match=%t",
			name, repo, prNumber, len(numbers), numbers, fetched, prCacheHasNumber(cached[repo], prNumber))
		return nil
	}
	// pinGroupHandler persists the pin register onto the workspace's
	// stored Entry.PinGroup. Drives the deck `g` chord. group == ""
	// unpins.
	pinGroupHandler := func(item deckui.Item, group string) error {
		repo := strings.TrimSpace(item.RepoRoot)
		if repo == "" {
			return fmt.Errorf("workspace %q has no repo root", item.WorkspaceName)
		}
		name := item.WorkspaceName
		updated := false
		if err := linkStore.Update(repo, func(entries map[string]workspace.Entry) map[string]workspace.Entry {
			if cur, ok := entries[name]; ok {
				cur.PinGroup = strings.TrimSpace(group)
				entries[name] = cur
				updated = true
			}
			return entries
		}); err != nil {
			return err
		}
		if !updated {
			return fmt.Errorf("workspace %q not found in store for repo %s", name, repo)
		}
		return nil
	}
	// pinGroupAliasHandler persists a register's display alias to the
	// global pin-groups file. Drives the deck `gR` chord.
	pinGroupAliasHandler := state.SavePinGroupAlias
	pinGroupAliases, err := state.LoadPinGroupAliases()
	if err != nil {
		pinGroupAliases = map[string]string{}
	}
	stateEditor := func() tea.Cmd {
		path, err := state.GlobalStorePath()
		if err != nil {
			return func() tea.Msg { return deckui.StateEditDoneMsg{Err: err} }
		}
		editor := strings.TrimSpace(os.Getenv("EDITOR"))
		if editor == "" {
			return func() tea.Msg { return deckui.StateEditDoneMsg{Err: fmt.Errorf("$EDITOR is not set")} }
		}
		c := exec.Command("sh", "-c", `exec "$EDITOR" "$1"`, "sh", path)
		return tea.ExecProcess(c, func(err error) tea.Msg {
			return deckui.StateEditDoneMsg{Err: err}
		})
	}
	// hookInstaller re-syncs the global agent integrations (Claude hooks,
	// pi.dev extension) on deck open. Both installers are idempotent and
	// only write when the on-disk config has drifted, so this is a no-op
	// on most opens — it exists to self-heal after an awp upgrade bumps
	// HookMarkerVersion or after settings.json is hand-edited, without the
	// user having to remember to run `awp init hooks`.
	hookInstaller := func() tea.Cmd {
		return func() tea.Msg {
			claudeChanged, cerr := agenthooks.InstallClaude()
			piChanged, perr := agenthooks.InstallPi()
			return deckui.HookInstallDoneMsg{
				ClaudeChanged: claudeChanged,
				PiChanged:     piChanged,
				Err:           errors.Join(cerr, perr),
			}
		}
	}

	asyncLauncher, asyncList, asyncCancel, asyncDismiss, asyncLog, asyncRetry, asyncDeleteRetry := buildAsyncJobs(repoRoot, runner)
	if asyncLauncher != nil {
		go runJobsStartupCleanup()
	}

	// devURLDiscoverer fans out one tmux pane-PID enumeration + one
	// platform listener call per tick and returns a DevURLsMsg keyed by
	// session name. Silent — no activity-bar entry.
	devURLDiscoverer := func() tea.Cmd {
		return func() tea.Msg {
			panePIDs, err := tmuxClient.PanePIDsBySession()
			if err != nil || len(panePIDs) == 0 {
				return deckui.DevURLsMsg{URLs: nil}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			urls, _ := portcapture.Discover(ctx, panePIDs)
			return deckui.DevURLsMsg{URLs: urls}
		}
	}

	model := deckui.New(items, handler).
		WithInitialScope(initialScope).
		// nil for `awp deck`; zdeck supplies one so the window keys render a
		// pane in place instead of handing off to tmux.
		WithPaneBackend(panes).
		WithRefresher(refresher).
		WithDevURLDiscoverer(devURLDiscoverer).
		WithPRFetcher(prFetcher).WithPRStatusFetcher(prStatusFetcher).
		WithPRDescriptionLoader(prDescriptionLoader(runner)).
		WithPRStatusSeed(cachedByRepo, cachedFetchedAt).
		WithBookmarkFetcher(bookmarkFetcher).
		WithDiffViewer(diffLoaderFor(runner), openDiffFileInEditor).
		WithDiffBaseResolver(diffBaseResolverFor(runner)).
		// The same scope list standalone `awp diff` gets, so `c` and `awp diff`
		// open on the same range and answer `-` the same way.
		WithDiffScopes(func(item deckui.Item) []ui.ScopeOption {
			return scopeOptionsFor(runner, item, item.Path)
		}).
		WithReviewStore(reviewStoreWithSend(runner, tmuxClient, svc)).
		WithTrunkResolver(func(repo string) string {
			fr := fixedDirRunner{base: runner, dir: repo}
			name, _ := jj.New(fr).Trunk()
			return name
		}).
		WithBookmarkLinkHandler(bookmarkLinkHandler).
		WithPRNumberLinkHandler(prNumberLinkHandler).
		WithPinGroupHandler(pinGroupHandler).
		WithPinGroupAliasHandler(pinGroupAliasHandler).
		WithPinGroupAliases(pinGroupAliases).
		WithBookmarkPrefix(cfg.Deck.BookmarkPrefix).
		WithStateEditor(stateEditor).WithUserActions(userActions).
		WithUserActionsResolver(userActionsForRepo).
		WithStateChangeWatcher(newDeckStateChangeWatcher()).
		WithHookInstaller(hookInstaller).
		WithProjectFinder(projectFinderFromRoots(cfg.Deck.ProjectRoots, 4)).
		WithProjectOpener(openProjectViaTmux(runner)).
		WithAsyncJobLauncher(asyncLauncher).
		WithJobsListRefresher(asyncList).
		WithJobCancelHandler(asyncCancel).
		WithJobDismissHandler(asyncDismiss).
		WithJobLogOpener(asyncLog).
		WithJobRetryHandler(asyncRetry).
		WithJobDeleteWorkspaceRetryHandler(asyncDeleteRetry)
	if stop, perr := startDeckProfile(); perr != nil {
		// Diagnostics must never stop the deck opening.
		deckDebugLogf("pprof: %v", perr)
	} else if stop != nil {
		defer stop()
	}
	if os.Getenv("AWP_TRACE") != "" {
		// One line per frame into /tmp/awp-deck.log: what our code cost, and the
		// gap since the previous frame. See internal/deckui/trace.go.
		deckui.Trace = deckDebugLogf
		deckDebugLogf("trace on — frame timings follow")
		defer func() { deckui.Trace = nil }()
	}
	program := tea.NewProgram(model, tea.WithInput(in), tea.WithOutput(out))
	_, err = program.Run()
	return err
}

// startDeckProfile writes a CPU profile of the whole deck session when
// AWP_PPROF names a path, and returns the func that closes it.
//
//	AWP_PPROF=/tmp/deck.out awp deck    # do the slow thing, then quit
//	go tool pprof -top -nodecount=30 /tmp/deck.out
//
// It exists because measuring a component in isolation kept saying the frame was
// cheap while the running deck was not. A profile of the real session answers
// that without a theory — including the answer "the process was barely busy",
// which would move the search out of Go and into the terminal.
func startDeckProfile() (func(), error) {
	path := strings.TrimSpace(os.Getenv("AWP_PPROF"))
	if path == "" {
		return nil, nil
	}
	f, err := os.Create(path) //nolint:gosec // the path is the operator's own choice
	if err != nil {
		return nil, err
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		pprof.StopCPUProfile()
		_ = f.Close()
	}, nil
}

// buildAsyncJobs returns the deck-side glue to the jobs subsystem:
// a launcher that translates a deckui.AsyncJobSpec into an
// internal/jobs.Spec + spawns a detached subprocess, a list
// refresher that powers the tray and the J overlay, and three
// per-action handlers (cancel via SIGTERM, dismiss = delete record,
// open log in $PAGER). Returns nil-valued functions if the jobs
// store can't be initialized; the deck silently falls back to the
// synchronous path.
func buildAsyncJobs(repoRoot string, runner Runner) (deckui.AsyncJobLauncher, deckui.JobsListRefresher, deckui.JobCancelHandler, deckui.JobDismissHandler, deckui.JobLogOpener, deckui.JobRetryHandler, deckui.JobDeleteWorkspaceRetryHandler) {
	store, err := jobs.NewStore()
	if err != nil {
		return nil, nil, nil, nil, nil, nil, nil
	}
	launcher := func(spec deckui.AsyncJobSpec) error {
		root := strings.TrimSpace(spec.RepoRoot)
		if root == "" {
			root = repoRoot
		}
		jspec := jobs.Spec{
			Action:           jobs.JobAction(spec.Action),
			RepoRoot:         root,
			Name:             spec.Name,
			Bookmark:         spec.Bookmark,
			BookmarkToCreate: spec.BookmarkToCreate,
			Prompt:           spec.Prompt,
			PRNumber:         spec.PRNumber,
			Arg:              spec.Arg,
			WorkspaceName:    spec.WorkspaceName,
			WorkspacePath:    spec.WorkspacePath,
			PaneHosted:       spec.PaneHosted,
		}
		_, err := store.Spawn(jspec, spec.Title, jobs.SpawnOptions{})
		return err
	}
	listRefresher := func() []deckui.Job {
		return projectJobs(store)
	}
	cancel := func(id string) error {
		return store.SignalCancel(jobs.JobID(id))
	}
	dismiss := func(id string) error {
		return store.Delete(jobs.JobID(id))
	}
	logOpen := func(id string) tea.Cmd {
		path := store.LogPath(jobs.JobID(id))
		// For jobs still in flight, open with `less +F` (follow mode) so
		// new output streams in like `tail -f`. Ctrl-C inside less drops
		// into normal navigation. Terminal jobs use $PAGER (defaulting to
		// less) — no need to follow a file that won't grow.
		job, _ := store.Get(jobs.JobID(id))
		var c *exec.Cmd
		if job.IsActive() {
			c = exec.Command("sh", "-c", `exec less +F "$1"`, "sh", path)
		} else {
			pager := strings.TrimSpace(os.Getenv("PAGER"))
			if pager == "" {
				pager = "less"
			}
			c = exec.Command("sh", "-c", `exec "$PAGER" "$1"`, "sh", path)
			c.Env = append(os.Environ(), "PAGER="+pager)
		}
		return tea.ExecProcess(c, func(err error) tea.Msg {
			return deckui.JobActionDoneMsg{JobID: id, Kind: "log", Err: err}
		})
	}
	retry := func(id string) error {
		orig, err := store.Get(jobs.JobID(id))
		if err != nil {
			return err
		}
		_, err = store.Spawn(orig.Spec, orig.Title, jobs.SpawnOptions{})
		return err
	}
	// deleteAndRetry is the typed recovery for ErrorKindStaleWorkspace
	// failures: nuke the workspace the failure attached to, then
	// re-spawn the original job. CRITICAL: target ErrorWorkspace, not
	// Spec.WorkspaceName. For review jobs Spec.WorkspaceName is the
	// deck's *predicted* review workspace (`pr-N-<branch>`), which is
	// blank when the head ref wasn't known at dispatch and could be
	// wrong if the prediction drifted — ErrorWorkspace is the name the
	// subprocess actually failed against, so it's the only safe delete
	// target. (Historically the spec carried the user's home row, e.g.
	// `default`; falling back to it silently deleted that row, which we
	// did once and shouldn't do again.)
	deleteAndRetry := func(id string) error {
		orig, err := store.Get(jobs.JobID(id))
		if err != nil {
			return err
		}
		name := strings.TrimSpace(orig.ErrorWorkspace)
		if name == "" {
			return fmt.Errorf("delete+retry: job %q has no error workspace recorded — refusing to guess", id)
		}
		root := strings.TrimSpace(orig.Spec.RepoRoot)
		if root == "" {
			root = repoRoot
		}
		svc := newDeckActionServiceWithIO(runner, root, nil, io.Discard)
		if err := svc.Delete(name, true); err != nil {
			return fmt.Errorf("delete+retry: delete %q: %w", name, err)
		}
		if _, err := store.Spawn(orig.Spec, orig.Title, jobs.SpawnOptions{}); err != nil {
			return fmt.Errorf("delete+retry: re-spawn: %w", err)
		}
		return nil
	}
	return launcher, listRefresher, cancel, dismiss, logOpen, retry, deleteAndRetry
}

// projectJobs builds the deckui-side projection of the jobs
// directory: runs orphan detection in-line, then converts each
// internal/jobs.Job into a deckui.Job record. Sorted newest-first.
func projectJobs(store *jobs.Store) []deckui.Job {
	all, err := store.List()
	if err != nil {
		return nil
	}
	now := timeNowForJobs()
	out := make([]deckui.Job, 0, len(all))
	for _, j := range all {
		if !j.Status.IsTerminal() && jobs.IsOrphan(j, now) {
			_ = store.Update(j.ID, func(rec *jobs.Job) error {
				if rec.Status.IsTerminal() {
					return nil
				}
				rec.Status = jobs.StatusOrphaned
				rec.ErrMsg = "subprocess died (pid " + itoa(rec.PID) + ")"
				ended := now
				rec.EndedAt = &ended
				return nil
			})
			j.Status = jobs.StatusOrphaned
		}
		out = append(out, toDeckJob(j, store))
	}
	// Newest first — most recently-started jobs at the top of the
	// overlay so users see what they just dispatched.
	sort.Slice(out, func(i, k int) bool {
		return out[i].StartedAt.After(out[k].StartedAt)
	})
	return out
}

func toDeckJob(j jobs.Job, store *jobs.Store) deckui.Job {
	steps := make([]deckui.JobStep, 0, len(j.Steps))
	for _, st := range j.Steps {
		steps = append(steps, deckui.JobStep{
			Label: st.Label,
			Done:  st.State == jobs.StepDone,
			Error: st.State == jobs.StepError,
		})
	}
	ended := time.Time{}
	if j.EndedAt != nil {
		ended = *j.EndedAt
	}
	return deckui.Job{
		ID:             string(j.ID),
		Title:          j.Title,
		Action:         string(j.Spec.Action),
		Status:         deckui.JobStatus(j.Status),
		StartedAt:      j.StartedAt,
		EndedAt:        ended,
		Steps:          steps,
		LogsTail:       j.LogsInline,
		ErrMsg:         j.ErrMsg,
		ErrorKind:      j.ErrorKind,
		ErrorWorkspace: j.ErrorWorkspace,
		LogPath:        store.LogPath(j.ID),
		PID:            j.PID,
		WorkspaceName:  j.Spec.WorkspaceName,
		WorkspacePath:  j.Spec.WorkspacePath,
		RepoRoot:       j.Spec.RepoRoot,
	}
}

// runJobsStartupCleanup sweeps terminal records older than their
// retention threshold. Runs in a goroutine on deck startup; failures
// are silent (the next deck launch will retry). Orphan detection is
// handled by countActiveJobs on every refresh tick.
func runJobsStartupCleanup() {
	store, err := jobs.NewStore()
	if err != nil {
		return
	}
	all, err := store.List()
	if err != nil {
		return
	}
	now := timeNowForJobs()
	for _, j := range all {
		if !j.Status.IsTerminal() {
			continue
		}
		if j.EndedAt == nil {
			continue
		}
		retention := jobs.RetentionDone
		if j.Status == jobs.StatusOrphaned {
			retention = jobs.RetentionOrphaned
		}
		if now.Sub(*j.EndedAt) > retention {
			_ = store.Delete(j.ID)
		}
	}
}

// workspaceRef identifies the workspace a session belongs to, without saying
// what is holding it.
//
// The row model used to key on a tmux session name, so every consumer spelled
// DeckSessionName and only tmux could ever answer. A workspace is what all of
// them actually meant.
type workspaceRef struct {
	project   string
	workspace string
}

// sessionFacts is what one refresh knows about one workspace's session.
//
// present and agentGone are separate questions and both matter, because both
// substrates keep a session after its agent exits — tmux's window falls back to
// a bare shell, and zmx keeps a session listed so its output can still be read.
// A session whose agent is gone reads "exited", which is a different row from
// one with no session at all.
type sessionFacts struct {
	// name is the substrate's own name for the session, for a row that has to
	// display or address it.
	name string
	// present says a session exists for this workspace.
	present bool
	// agentGone says the session is there but its agent process is not.
	agentGone bool
}

// deckSessionSnapshot is everything one refresh knows about the processes
// behind the rows, captured up front so per-row enrichment is a map lookup.
//
// known is false on the JSON-only fast path (first paint), in which case row
// decorations fall back to optimistic defaults — Active reflects the stored
// SessionID, Stale stays off — so we don't flash a caution badge for the ~50 ms
// until the next refresh fills in real state.
type deckSessionSnapshot struct {
	known       bool
	byWorkspace map[workspaceRef]sessionFacts
	// current is the workspace the user is looking at, and only tmux can
	// answer it: a deck that hosts its own panes is the outermost program, so
	// there is no session to be "in". Under such a deck the honest source is
	// cwd, which is a separate question from which sessions exist.
	current    workspaceRef
	hasCurrent bool
}

// facts answers both session questions for one workspace.
func (s deckSessionSnapshot) facts(project, workspace string) sessionFacts {
	return s.byWorkspace[workspaceRef{project: project, workspace: workspace}]
}

// agentRunning says this workspace has a session and its agent is alive in it —
// the condition for trusting a stored "working" status.
func (s deckSessionSnapshot) agentRunning(project, workspace string) bool {
	f := s.facts(project, workspace)
	return f.present && !f.agentGone
}

// isCurrent says the user is looking at this workspace.
func (s deckSessionSnapshot) isCurrent(project, workspace string) bool {
	return s.hasCurrent && s.current == workspaceRef{project: project, workspace: workspace}
}

// deckSessions reads the substrate a deck's long-lived processes live on.
//
// An interface because there are two substrates and each deck has exactly one:
// awp deck's sessions are tmux's, zdeck's are zmx's. Deliberately not a merge
// of the two — asking both would let a workspace read live on the strength of a
// leftover session on the substrate this deck does not use, which is the
// staleness the seam exists to remove.
type deckSessions interface {
	// sessions reads the substrate. fast trades completeness for a single
	// cheap call, for the first paint; the snapshot says which it got via
	// known.
	sessions(fast bool) deckSessionSnapshot
}

// tmuxSessions answers from tmux, which is what awp deck's processes live on.
type tmuxSessions struct{ client *tmux.Client }

// sessions reads the two shell-outs (list-sessions + list-panes -a) the row
// decorations need. When fast is true only the currently-attached session is
// read — a single cheap display-message — and known stays false so caution
// glyphs and stale decorations remain suppressed until the next full pass. The
// current session still flows through so the first cursor lands on the
// workspace the user launched from.
func (t tmuxSessions) sessions(fast bool) deckSessionSnapshot {
	snap := deckSessionSnapshot{byWorkspace: map[workspaceRef]sessionFacts{}}
	if t.client == nil {
		return snap
	}
	name, _ := t.client.CurrentSessionName()
	snap.current, snap.hasCurrent = tmuxWorkspaceRef(name)
	if fast {
		return snap
	}
	snap.known = true
	sessions, _ := t.client.ListSessions()
	panes, _ := t.client.ListPanes()
	agentShell := agentShellSessions(panes)
	for _, s := range sessions {
		ref, ok := tmuxWorkspaceRef(s.Name)
		if !ok {
			// Not a session awp made. The deck has nothing to say about the
			// rest of the user's tmux.
			continue
		}
		snap.byWorkspace[ref] = sessionFacts{
			name:      s.Name,
			present:   true,
			agentGone: agentShell[s.Name],
		}
	}
	return snap
}

// tmuxWorkspaceRef reads the workspace out of an awp tmux session name.
//
// Inverse of DeckSessionName, and inherits parseAwpSession's ambiguity: a
// project whose directory name contains "__" splits in the wrong place. That
// predates this seam — adoption has always parsed these names — and the
// round-trip is pinned by session_source_test.go for names that do not.
func tmuxWorkspaceRef(name string) (workspaceRef, bool) {
	project, ws, ok := parseAwpSession(name)
	if !ok {
		return workspaceRef{}, false
	}
	return workspaceRef{project: project, workspace: ws}, true
}

// agentShellSessions reports, per session, whether its "agent" window has
// fallen back to a bare shell — i.e. the agent process is gone. A window
// counts as shell-fallback only when it has panes and NONE of them run a
// non-shell process. Keying on "any pane is a shell" instead wrongly flagged
// sessions where the agent is alive but the user has split an extra shell (or
// an `awp watch`) pane into the same window, which then dropped the row to
// "exited", hid it from the attention scope, and cleared its unread dot.
func agentShellSessions(panes []tmux.Pane) map[string]bool {
	hasAgentWindow := map[string]bool{}
	hasLiveProcess := map[string]bool{}
	for _, p := range panes {
		if p.Window != "agent" {
			continue
		}
		hasAgentWindow[p.Session] = true
		if !isShellCommand(p.Command) {
			hasLiveProcess[p.Session] = true
		}
	}
	out := map[string]bool{}
	for sess := range hasAgentWindow {
		if !hasLiveProcess[sess] {
			out[sess] = true
		}
	}
	return out
}

// isShellCommand reports whether a pane's current command is an interactive
// shell (the mark of an agent window that has dropped back to a prompt).
func isShellCommand(cmd string) bool {
	switch strings.TrimSpace(cmd) {
	case "bash", "zsh", "fish", "sh", "dash":
		return true
	}
	return false
}

// loadDeckItems builds the deck's row data from workspace-state.json
// directly, using a single batched tmux probe for live decorations.
// Reading is JSON-only on the hot path: no `jj` invocations, no
// per-row tmux calls. Workspaces created externally via `jj workspace
// add` won't appear until the deck reconciles via a write path
// (deck-driven create/delete already does this).
func loadDeckItems(j *jj.Client, sessions deckSessions, fastSessions bool, svc workspace.Service, repoRoot, projectName string, in io.Reader, out io.Writer) ([]deckui.Item, error) {
	_ = j
	_ = in
	_ = out

	store := state.NewJSONStore()
	repoMap, err := store.LoadAll()
	if err != nil {
		return nil, err
	}

	// One-time lazy migration: for entries that pre-date the
	// Bookmark+PROverride → PRNumber collapse, resolve Bookmark →
	// headRefName via the current pr-status cache. If a match exists,
	// persist Entry.PRNumber so future loads (and the new direct-lookup
	// path) don't depend on the cache to identify the PR. Idempotent:
	// entries already migrated (PRNumber > 0) and entries with no
	// bookmark match (cache cold for that repo, or bookmark drifted)
	// are no-ops.
	migrateBookmarkPRNumbersIfNeeded(store, repoMap)

	snap := sessions.sessions(fastSessions)

	// adoptable: live sessions for this project that no store entry claims.
	// Entries delete themselves from it as they are visited below, so what is
	// left is a session with no workspace behind it.
	adoptable := map[workspaceRef]sessionFacts{}
	for ref, facts := range snap.byWorkspace {
		if ref.project == projectName {
			adoptable[ref] = facts
		}
	}

	type repoRow struct {
		repo, project string
	}
	repos := make([]repoRow, 0, len(repoMap))
	for r := range repoMap {
		repos = append(repos, repoRow{repo: r, project: strings.TrimSpace(filepath.Base(filepath.Clean(r)))})
	}
	sort.Slice(repos, func(i, k int) bool {
		if repos[i].project != repos[k].project {
			return repos[i].project < repos[k].project
		}
		return repos[i].repo < repos[k].repo
	})

	// Fan out jj HeadDescription + BookmarkCommitID per workspace path
	// in parallel. Each goroutine spawns one or two `jj log` subprocesses
	// (~10–30ms with --ignore-working-copy); serialized across N
	// workspaces this used to dominate the enrichment pass. Running
	// concurrently drops wall time to roughly the slowest single
	// workspace. Skipped on the fast first paint where j == nil.
	// The bookmark commit-id powers the "behind remote" / stale signal:
	// comparing the local bookmark tip against the PR head SHA tells us
	// whether what we have locally still matches what's on the PR.
	type headInfo struct{ changeID, bookmarkCommitID, desc string }
	type pathSpec struct{ path, bookmark string }
	var headByPath map[string]headInfo
	if j != nil {
		var specs []pathSpec
		seen := map[string]bool{}
		for _, r := range repos {
			for _, e := range repoMap[r.repo] {
				p := strings.TrimSpace(e.Path)
				if p == "" || seen[p] {
					continue
				}
				seen[p] = true
				specs = append(specs, pathSpec{path: p, bookmark: strings.TrimSpace(e.Bookmark)})
			}
		}
		// Enrichment is best-effort: the authoritative row list comes from
		// the state file (repoMap) above, and headByPath only decorates rows
		// with their jj HEAD description / bookmark tip. jj log commands take
		// the repo's operation-log lock, so during heavy activity (a create
		// subprocess running `jj workspace add`, etc.) a concurrent log can
		// block until that op finishes — and a truly stuck jj would block
		// forever. Since this runs inside the deck's refresher cmd, a blocked
		// wg.Wait() would wedge m.refreshing=true permanently and kill the
		// deck's background poll. So bound the wait: take whatever enrichment
		// completed in time and proceed; stragglers keep writing to `live`
		// under the lock (harmless — we read a snapshot), and the next refresh
		// re-enriches the rows that timed out.
		live := make(map[string]headInfo, len(specs))
		var mu sync.Mutex
		var wg sync.WaitGroup
		for _, s := range specs {
			wg.Add(1)
			go func(s pathSpec) {
				defer wg.Done()
				id, desc, _ := j.HeadDescription(s.path)
				var bookmarkCommit string
				if s.bookmark != "" {
					bookmarkCommit, _ = j.BookmarkCommitID(s.path, s.bookmark)
				}
				mu.Lock()
				live[s.path] = headInfo{changeID: id, bookmarkCommitID: bookmarkCommit, desc: desc}
				mu.Unlock()
			}(s)
		}
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(deckEnrichTimeout):
		}
		mu.Lock()
		headByPath = make(map[string]headInfo, len(live))
		for k, v := range live {
			headByPath[k] = v
		}
		mu.Unlock()
	}

	// Dev-loop progress summaries (rendered on the row meta line, see
	// deckui.Model.metaLine). For each workspace whose agent is actively
	// working, replay its Claude Code transcript into a compact
	// done/total + phase + current-unit snapshot. This mirrors the `w`
	// watch overlay's data source (watch.Resolve → BuildState) so the row
	// and the overlay agree. The transcript scan can be large, so it is
	// gated to active + working rows (usually a handful) and only runs
	// once real tmux state is known (snap.known) — the fast first paint
	// stays cheap and the next full refresh fills these in ~50 ms later.
	// Best-effort and bounded by deckEnrichTimeout, like the head
	// enrichment above.
	devLoopByPath := map[string]*deckui.DevLoopSummary{}
	if snap.known {
		loopByRepo := map[string]watch.Loop{}
		type dlSpec struct {
			path, status string
			loop         watch.Loop
		}
		var specs []dlSpec
		seen := map[string]bool{}
		for _, r := range repos {
			for _, e := range repoMap[r.repo] {
				if !workspace.IsWorking(e.Status) {
					continue
				}
				if !snap.agentRunning(r.project, e.Name) {
					// No session, or one whose agent is gone — the stored
					// "working" is stale.
					continue
				}
				p := strings.TrimSpace(e.Path)
				if p == "" || seen[p] {
					continue
				}
				seen[p] = true
				loop, ok := loopByRepo[r.repo]
				if !ok {
					cfg, _ := config.Load(r.repo)
					if watch.IsConfigured(cfg) {
						loop = watch.Resolve(cfg)
					}
					// Unconfigured repos cache a zero Loop (empty Phases) so we
					// skip them below without reloading config per row.
					loopByRepo[r.repo] = loop
				}
				// Only watch repos that opted into a dev_loop — no guessed
				// default-loop phase/gate meta line for the rest.
				if len(loop.Phases) == 0 {
					continue
				}
				specs = append(specs, dlSpec{path: p, status: e.Status, loop: loop})
			}
		}
		if len(specs) > 0 {
			live := make(map[string]*deckui.DevLoopSummary, len(specs))
			var mu sync.Mutex
			var wg sync.WaitGroup
			for _, s := range specs {
				wg.Add(1)
				go func(s dlSpec) {
					defer wg.Done()
					summary := buildDevLoopSummary(s.loop, s.path, s.status)
					if summary == nil {
						return
					}
					mu.Lock()
					live[s.path] = summary
					mu.Unlock()
				}(s)
			}
			done := make(chan struct{})
			go func() { wg.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(deckEnrichTimeout):
			}
			mu.Lock()
			for k, v := range live {
				devLoopByPath[k] = v
			}
			mu.Unlock()
		}
		// Persist the fresh summaries so the next deck open's fast first
		// paint can render progress from the cache without a transcript
		// scan. Write per repo, and only when a workspace's snapshot
		// actually changed — a no-op Update still rewrites the state file
		// and would bounce back through the deck's state-file watcher, so
		// compare-and-skip keeps writes at the agent's pace (a snapshot
		// changes only when the agent does something) rather than once per
		// refresh.
		for _, r := range repos {
			changed := map[string]*workspace.DevLoopSnapshot{}
			for name, e := range repoMap[r.repo] {
				fresh := devLoopByPath[strings.TrimSpace(e.Path)]
				if fresh == nil {
					// No fresh scan (not working, or timed out): leave the
					// cached snapshot untouched rather than wiping it on a
					// transient miss.
					continue
				}
				snapshot := &workspace.DevLoopSnapshot{Done: fresh.Done, Total: fresh.Total, Phase: fresh.Phase, Task: fresh.Task}
				// HasTasks is scan-authoritative (it reflects the real task list
				// in the transcript), so take it from the fresh scan.
				snapshot.HasTasks = fresh.HasTasks
				// These fields are hook-managed and can't be derived by the scan,
				// so carry them forward untouched: UnitKey (set by `awp gate
				// check` on a unit's TaskUpdate→in_progress) and GatesSealed (set
				// on a green completion).
				if e.DevLoop != nil {
					snapshot.UnitKey = e.DevLoop.UnitKey
					snapshot.GatesSealed = e.DevLoop.GatesSealed
				}
				// Reconcile gate results from the transcript scan when it
				// observed any; otherwise keep the event-driven map so a scan
				// miss doesn't wipe hook-recorded results.
				switch {
				case len(fresh.Gates) > 0:
					snapshot.Gates = fresh.Gates
				case e.DevLoop != nil:
					snapshot.Gates = e.DevLoop.Gates
				}
				if !devLoopSnapshotEqual(e.DevLoop, snapshot) {
					changed[name] = snapshot
				}
			}
			if len(changed) == 0 {
				continue
			}
			_ = store.Update(r.repo, func(entries map[string]workspace.Entry) map[string]workspace.Entry {
				for name, snapshot := range changed {
					if cur, ok := entries[name]; ok {
						cur.DevLoop = snapshot
						entries[name] = cur
					}
				}
				return entries
			})
		}
	}

	var items []deckui.Item
	for _, r := range repos {
		entries := repoMap[r.repo]
		names := make([]string, 0, len(entries))
		for n := range entries {
			names = append(names, n)
		}
		sort.Strings(names)
		isCurrentRepo := r.repo == repoRoot
		for _, n := range names {
			e := entries[n]
			sessionName := DeckSessionName(r.project, e.Name)
			facts := snap.facts(r.project, e.Name)
			delete(adoptable, workspaceRef{project: r.project, workspace: e.Name})

			unread := e.Unread
			// If the user is currently focused on this workspace's session,
			// clear the unread badge. Persist via `svc.MarkRead` for the
			// deck's own repo; for other repos, write through the JSON
			// store directly so the tmux unread summary (which reads the
			// store) doesn't show a gray dot for a workspace the user is
			// actively viewing.
			if snap.known && unread && snap.isCurrent(r.project, e.Name) {
				unread = false
				if isCurrentRepo {
					_ = svc.MarkRead(e.Name)
				} else {
					name := e.Name
					_ = store.Update(r.repo, func(entries map[string]workspace.Entry) map[string]workspace.Entry {
						if cur, ok := entries[name]; ok && cur.Unread {
							cur.Unread = false
							cur.Touch(time.Now())
							entries[name] = cur
						}
						return entries
					})
				}
			}

			status := e.Status
			if strings.TrimSpace(status) == "" {
				status = "idle"
			}
			// Without session info (fast first paint), trust the stored
			// SessionID: if there is one, assume the session is still
			// alive. Real state arrives ~50 ms later from the
			// Init-driven refresh and overwrites this.
			active := facts.present
			current := snap.isCurrent(r.project, e.Name)
			if !snap.known {
				active = e.SessionID != ""
			}
			if snap.known && facts.present && facts.agentGone {
				status = "exited"
				// An exited agent has nothing for the user to act on, so the
				// transition drops any stale unread badge instead of setting
				// one.
				unread = false
				// Persist the override (only on the deck's own repo) so
				// downstream consumers — doctor, the cross-repo pane on
				// another deck — see the same thing. Claude has no exit
				// hook of its own. UpdateStatus clears the stored Unread.
				if isCurrentRepo && e.Status != "exited" {
					_ = svc.UpdateStatus(e.Name, "exited")
				}
			}

			// Dev-loop progress: prefer the freshly-computed summary; when
			// there isn't one yet (fast first paint skips the scan, or the
			// scan timed out) fall back to the cached snapshot in the entry
			// for an actively-working row, so the row shows its last-known
			// progress immediately instead of flashing the branch/port meta.
			devLoop := devLoopByPath[strings.TrimSpace(e.Path)]
			if devLoop == nil && active && workspace.IsWorking(status) && e.DevLoop != nil {
				devLoop = &deckui.DevLoopSummary{
					Done:  e.DevLoop.Done,
					Total: e.DevLoop.Total,
					Phase: e.DevLoop.Phase,
					Task:  e.DevLoop.Task,
					Gates: e.DevLoop.Gates,
				}
			}

			// Head info comes from the parallel pre-fetch above; missing
			// entry (e.g. j == nil on fast first paint, or empty path)
			// leaves both fields blank — the enrichment pass ~50 ms
			// later fills them in.
			head := headByPath[strings.TrimSpace(e.Path)]
			item := deckui.Item{
				ProjectName:      r.project,
				WorkspaceName:    e.Name,
				Path:             e.Path,
				RepoRoot:         r.repo,
				Bookmark:         strings.TrimSpace(e.Bookmark),
				PRNumber:         e.PRNumber,
				PinGroup:         strings.TrimSpace(e.PinGroup),
				Status:           status,
				Unread:           unread,
				PromptPreview:    e.ActivePrompt,
				HeadDesc:         head.desc,
				HeadChangeID:     head.changeID,
				BookmarkCommitID: head.bookmarkCommitID,
				TmuxWindow:       sessionName,
				SessionName:      sessionName,
				Active:           active,
				Current:          current,
				LastActiveAt:     e.LastActiveAt,
				DevLoop:          devLoop,
			}
			items = append(items, item)
		}
	}

	// Live sessions for the current project that aren't in state — show them as
	// "unmanaged" rows so the user can adopt or kill them.
	for ref, facts := range adoptable {
		items = append(items, deckui.Item{
			ProjectName:   ref.project,
			WorkspaceName: ref.workspace,
			Path:          "",
			Status:        "unmanaged",
			PromptPreview: "(live tmux session, not in store)",
			TmuxWindow:    facts.name,
			SessionName:   facts.name,
			Active:        true,
			Current:       snap.isCurrent(ref.project, ref.workspace),
		})
	}

	return items, nil
}

// devLoopSnapshotEqual reports whether two cached snapshots carry the same
// progress, treating nil as "no snapshot". Used to skip a state-file write
// when the deck recomputes an unchanged summary.
func devLoopSnapshotEqual(a, b *workspace.DevLoopSnapshot) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Done != b.Done || a.Total != b.Total || a.Phase != b.Phase ||
		a.Task != b.Task || a.UnitKey != b.UnitKey {
		return false
	}
	if len(a.Gates) != len(b.Gates) {
		return false
	}
	for k, v := range a.Gates {
		if b.Gates[k] != v {
			return false
		}
	}
	return true
}

// buildDevLoopSummary replays the workspace's newest agent transcript into
// the row-sized dev-loop projection rendered on the meta line. Returns nil
// when there is no transcript, the scan fails, or the derived state has
// nothing worth showing (no todos, no phase, no in-progress unit) — the
// meta line then falls through to its normal port/branch content.
func buildDevLoopSummary(loop watch.Loop, path, status string) *deckui.DevLoopSummary {
	transcript, err := watch.Locate(path)
	if err != nil || transcript == "" {
		return nil
	}
	st, err := watch.BuildState(loop, transcript, status, time.Now())
	if err != nil {
		return nil
	}
	summary := &deckui.DevLoopSummary{
		Done:     st.DoneCount(),
		Total:    len(st.Todos),
		Phase:    st.CurrentPhase,
		HasTasks: len(st.Todos) > 0,
	}
	if cur := st.CurrentUnit(); cur >= 0 {
		summary.Task = st.Todos[cur].Content
	}
	// Reconcile the current unit's gate results from the transcript (the
	// authoritative is_error signal), so the event-driven `awp gate record`
	// snapshot self-heals against a dropped hook. Only observed results are
	// recorded — an unrun gate stays absent (treated as pending downstream),
	// matching the map the hook writes so equality checks don't churn.
	if gm := reconcileGates(st); len(gm) > 0 {
		summary.Gates = gm
	}
	if summary.Total == 0 && summary.Phase == "" && summary.Task == "" && len(summary.Gates) == 0 {
		return nil
	}
	return summary
}

// reconcileGates projects a watch.State's per-gate results into the snapshot
// map shape (name -> pass/fail), skipping gates that haven't run.
func reconcileGates(st watch.State) map[string]string {
	gm := map[string]string{}
	for _, g := range st.Gates {
		if g.Result == "" {
			continue
		}
		gm[g.Name] = g.Result
	}
	return gm
}

func handleDeckAction(tmuxClient *tmux.Client, svc workspace.Service, runner Runner, req deckui.ActionRequest, reporter deckui.Reporter) error {
	if reporter == nil {
		reporter = noopReporter{}
	}
	item := req.Item
	sessionName := DeckSessionName(item.ProjectName, item.WorkspaceName)
	switch req.Action {
	case deckui.ActionSummon:
		return summonWorkspaceSession(tmuxClient, svc, item, reporter)
	case deckui.ActionOpenWindow:
		return openNamedWindow(tmuxClient, svc, item, expandWindowArg(runner, item, req.Arg), reporter)
	case deckui.ActionCI:
		return openCIWindow(tmuxClient, svc, runner, item, reporter)
	case deckui.ActionLastSession:
		reporter.Step("Switch to last tmux session")
		return tmuxClient.SwitchClientLast()
	case deckui.ActionDelete:
		reporter.Step(fmt.Sprintf("Delete workspace %s", item.WorkspaceName))
		opts := workspace.DeleteOptions{Force: true}
		var queuePath string
		if sessionID, err := tmuxClient.CurrentSessionID(); err == nil {
			if path, ok := pendingKillsPath(sessionID); ok {
				queuePath = path
				if item.Current {
					_ = appendPendingAction(path, "switch", DeckSessionName(item.ProjectName, "default"))
				}
				opts.DeferTmuxKill = func(window string) {
					_ = appendPendingKill(path, window)
				}
			}
		}
		if err := svc.DeleteWithOptions(item.WorkspaceName, opts); err != nil {
			return err
		}
		id, err := tmuxClient.SessionIDByName(sessionName)
		if err != nil {
			return err
		}
		if id != "" {
			if queuePath != "" {
				reporter.Step(fmt.Sprintf("Queue tmux session removal %s", sessionName))
				_ = appendPendingAction(queuePath, "session", sessionName)
			} else {
				reporter.Step(fmt.Sprintf("Kill tmux session %s", sessionName))
				if err := tmuxClient.KillSession(sessionName); err != nil {
					return err
				}
			}
		}
		return nil
	case deckui.ActionDeleteProject:
		return handleDeleteProjectAction(tmuxClient, svc, item, reporter)
	case deckui.ActionRename:
		newName := strings.TrimSpace(req.Arg)
		oldSessionName := DeckSessionName(item.ProjectName, item.WorkspaceName)
		newSessionName := DeckSessionName(item.ProjectName, newName)
		// Snapshot the live tmux session id (if any) before doing
		// anything. We use it both to refuse mid-rename when an agent
		// is running and to rebind the renamed session into state.
		sessionID, _ := tmuxClient.SessionIDByName(oldSessionName)
		// Refuse if the agent pane is running a non-shell process. The
		// running agent has AWP_WORKSPACE=<old> frozen in its environ,
		// and so does every hook subprocess it spawns. A rename would
		// silently break status reporting until the agent restarts.
		if sessionID != "" {
			if cmd, err := tmuxClient.PaneCurrentCommand(oldSessionName + ":agent"); err == nil {
				running := strings.TrimSpace(cmd)
				if running != "" && !isShellName(running) {
					return fmt.Errorf("workspace %q has a live agent (%s) — stop it first or pick a different time to rename", item.WorkspaceName, running)
				}
			}
		}
		reporter.Step(fmt.Sprintf("Rename workspace %s → %s", item.WorkspaceName, newName))
		if err := svc.Rename(item.WorkspaceName, newName); err != nil {
			return err
		}
		if sessionID != "" {
			reporter.Step(fmt.Sprintf("Rename tmux session %s → %s", oldSessionName, newSessionName))
			if err := tmuxClient.RenameSession(oldSessionName, newSessionName); err != nil {
				return err
			}
			if err := svc.RecordSession(newName, sessionID, newSessionName); err != nil {
				return err
			}
			// Update session env so fresh shells / hooks that fall back to
			// `tmux show-environment` pick up the new workspace name.
			// Existing processes keep their stale environ, but we refused
			// above when an agent was running so that's no longer a
			// concern.
			if _, err := ensureWorkspaceSessionEnv(tmuxClient, newSessionName, item.ProjectName, newName, item.RepoRoot, ""); err != nil {
				return err
			}
		}
		return nil
	case deckui.ActionSendPrompt:
		return sendPromptToAgent(tmuxClient, svc, item, req.Arg, reporter)
	case deckui.ActionMergePR:
		n, err := strconv.Atoi(strings.TrimSpace(req.Arg))
		if err != nil || n <= 0 {
			return fmt.Errorf("merge PR: invalid PR number %q", req.Arg)
		}
		repoDir := strings.TrimSpace(item.RepoRoot)
		if repoDir == "" {
			repoDir = item.Path
		}
		gh := github.New(runner, repoDir)
		// MergePR narrates its own steps (squash by default; "merge queue
		// detected" → enqueue) so the progress modal reflects the path it
		// actually took. We just log its final summary line.
		out, err := gh.MergePR(n, reporter)
		if s := strings.TrimSpace(out); s != "" {
			for _, line := range strings.Split(s, "\n") {
				reporter.Log(line)
			}
		}
		return err
	}
	return fmt.Errorf("unknown action: %q session=%q", req.Action, sessionName)
}

// sendPromptToAgent dispatches `prompt` to the workspace's agent. If
// the workspace tmux session doesn't exist yet, it's created with the
// agent CLI being invoked with the prompt as its first argument
// (mirrors `awp w open --prompt`). If the session exists but the agent
// window is sitting at a shell prompt, the agent is launched there.
// Otherwise the prompt is bracket-pasted into the running agent as a
// user message. Never switches the tmux client — the deck stays in
// focus by design.
func sendPromptToAgent(tmuxClient *tmux.Client, svc workspace.Service, item deckui.Item, prompt string, reporter deckui.Reporter) error {
	// Callers that have no progress UI pass nil; every Step below would then
	// panic on a nil interface, taking the deck down instead of sending. Cheaper
	// to absorb it here than to rely on every call site remembering.
	if reporter == nil {
		reporter = noopReporter{}
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return errors.New("prompt is empty")
	}
	if strings.TrimSpace(item.WorkspaceName) == "" {
		return errors.New("send-prompt: workspace name required")
	}
	sessionName := DeckSessionName(item.ProjectName, item.WorkspaceName)
	env := workspaceEnvPairs(item.ProjectName, item.WorkspaceName, item.RepoRoot)

	id, err := tmuxClient.SessionIDByName(sessionName)
	if err != nil {
		return err
	}
	sessionWasNew := id == ""
	if sessionWasNew {
		reporter.Step(fmt.Sprintf("Create tmux session %s", sessionName))
		path := resolvePath(svc, item)
		// NewSession rather than createWorkspaceSession: we want full
		// control over the agent window's initial command so we can
		// pass the prompt as argv[1] to the CLI in one go.
		if err := tmuxClient.NewSession(sessionName, path, "agent", env); err != nil {
			return err
		}
		id, _ = tmuxClient.SessionIDByName(sessionName)
		_ = svc.RecordSession(item.WorkspaceName, id, sessionName)
	}
	_ = ensureWorkspaceSessionEnvForItem(tmuxClient, sessionName, item.ProjectName, item.WorkspaceName, item.RepoRoot)

	// Make sure an agent window exists. If the session was created
	// above this is the window NewSession just opened; otherwise we
	// create it on-demand so users can dispatch a prompt without
	// having pressed `a` first.
	agentTarget := sessionName + ":agent"
	windows, _ := tmuxClient.ListWindowsInSession(sessionName)
	haveAgent := false
	for _, w := range windows {
		if w.Name == "agent" {
			haveAgent = true
			break
		}
	}
	if !haveAgent {
		reporter.Step("Open agent window")
		path := resolvePath(svc, item)
		if err := tmuxClient.NewWindowInSession(sessionName, "agent", path, env); err != nil {
			return err
		}
	}

	// If the agent pane is sitting at a shell (no agent running yet),
	// start the agent with the prompt as argv[1] — the same trick
	// `awp w open --prompt` uses on a brand-new session.
	if sessionWasNew || !haveAgent || paneIsShell(tmuxClient, agentTarget) {
		invocation := strings.TrimSpace(codingAgentInvocation(item.RepoRoot))
		if invocation == "" {
			return errors.New("send-prompt: no agent invocation configured")
		}
		reporter.Step("Launch agent with prompt")
		cmd := invocation + " " + shellSingleQuote(prompt)
		return tmuxClient.SendCommand(agentTarget, cmd)
	}

	// Agent is already running — paste the prompt as a user message
	// via bracketed paste so multi-line prompts don't fire as
	// separate submits.
	reporter.Step("Send prompt to agent")
	return tmuxClient.PasteText(agentTarget, prompt)
}

// handleDeleteProjectAction removes every non-default workspace under
// item.RepoRoot, kills their tmux sessions, and drops the repo entry
// from workspace state. The default jj workspace itself is left
// intact — "deleting the project" is a deck concept (the project
// disappears from the row list); the source repo and its default
// workspace stay on disk.
func handleDeleteProjectAction(tmuxClient *tmux.Client, svc workspace.Service, item deckui.Item, reporter deckui.Reporter) error {
	repoRoot := strings.TrimSpace(item.RepoRoot)
	if repoRoot == "" {
		return errors.New("delete-project: missing repo root")
	}
	store := state.NewJSONStore()
	entries, err := store.Load(repoRoot)
	if err != nil {
		return fmt.Errorf("load workspace state: %w", err)
	}
	names := make([]string, 0, len(entries))
	for n := range entries {
		if n == "default" {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)

	var queuePath string
	if sessionID, err := tmuxClient.CurrentSessionID(); err == nil {
		if path, ok := pendingKillsPath(sessionID); ok {
			queuePath = path
		}
	}

	reporter.Step(fmt.Sprintf("Delete project %s (%d workspace(s))", item.ProjectName, len(names)))
	for _, name := range names {
		reporter.Step(fmt.Sprintf("Delete workspace %s", name))
		opts := workspace.DeleteOptions{Force: true}
		if queuePath != "" {
			opts.DeferTmuxKill = func(window string) {
				_ = appendPendingKill(queuePath, window)
			}
		}
		if err := svc.DeleteWithOptions(name, opts); err != nil {
			return fmt.Errorf("delete %s: %w", name, err)
		}
		sessionName := DeckSessionName(item.ProjectName, name)
		id, err := tmuxClient.SessionIDByName(sessionName)
		if err != nil {
			return err
		}
		if id != "" {
			if queuePath != "" {
				_ = appendPendingAction(queuePath, "session", sessionName)
			} else {
				if err := tmuxClient.KillSession(sessionName); err != nil {
					return err
				}
			}
		}
	}

	reporter.Step("Drop project from deck state")
	if err := store.DeleteRepo(repoRoot); err != nil {
		return fmt.Errorf("drop project from state: %w", err)
	}
	return nil
}

// createWorkspaceSession runs tmux new-session for the workspace and
// launches the configured agent in the freshly-created "agent" window so
// the user doesn't land in a bare shell. Returns the new session id.
//
// Every deck-side handler that creates a workspace session must go
// through this helper (not raw tmux.NewSession), or the async
// create-workspace flow's agent-auto-launch silently no-ops when the
// session was created out-of-band: app.go's `sessionWasNew` check sees
// the pre-existing session and skips the SendCommand for the agent
// invocation.
func createWorkspaceSession(tmuxClient *tmux.Client, sessionName, path, repoRoot string, env []string) (string, error) {
	if err := tmuxClient.NewSession(sessionName, path, "agent", env); err != nil {
		return "", err
	}
	id, _ := tmuxClient.SessionIDByName(sessionName)
	if invocation := strings.TrimSpace(codingAgentInvocation(repoRoot)); invocation != "" {
		_ = tmuxClient.SendCommand(sessionName+":agent", invocation)
	}
	return id, nil
}

func summonWorkspaceSession(tmuxClient *tmux.Client, svc workspace.Service, item deckui.Item, reporter deckui.Reporter) error {
	sessionName := DeckSessionName(item.ProjectName, item.WorkspaceName)
	id, err := tmuxClient.SessionIDByName(sessionName)
	if err != nil {
		return err
	}
	env := workspaceEnvPairs(item.ProjectName, item.WorkspaceName, item.RepoRoot)
	if id == "" {
		reporter.Step(fmt.Sprintf("Create tmux session %s", sessionName))
		path := resolvePath(svc, item)
		newID, err := createWorkspaceSession(tmuxClient, sessionName, path, item.RepoRoot, env)
		if err != nil {
			return err
		}
		id = newID
	}
	if stale, envErr := ensureWorkspaceSessionEnv(tmuxClient, sessionName, item.ProjectName, item.WorkspaceName, item.RepoRoot, sessionName+":agent"); envErr != nil {
		reporter.Log(fmt.Sprintf("warning: failed to set session env: %v", envErr))
	} else if stale {
		reporter.Log("agent missing AWP_WORKSPACE — restart agent to enable status reporting")
	}
	_ = svc.RecordSession(item.WorkspaceName, id, sessionName)
	// Land on the agent window when the row had unread agent output —
	// the badge is the signal the user is reacting to, so complete the
	// gesture by putting them on what changed. Done before MarkRead so
	// the Unread flag still reflects the state the user clicked on.
	if item.Unread {
		_ = tmuxClient.SwitchToWindow(sessionName + ":agent")
	}
	_ = svc.MarkRead(item.WorkspaceName)
	reporter.Step(fmt.Sprintf("Switch to %s", sessionName))
	return tmuxClient.SwitchClient(sessionName)
}

// openNamedWindow ensures the workspace session exists, then switches to the
// named window, creating it (with an optional default command) if missing.
// Finally, it switches the tmux client to the session so the user lands there.
// resolveReviewStackBase returns the base revset the C (stack review) action
// diffs @ against. It prefers the workspace's nearest stacked-parent bookmark
// — the closest bookmarked ancestor of @ that is none of @ itself, trunk, or
// the workspace's own bookmark — so a stacked change is reviewed against its
// parent rather than the whole stack. When nothing is stacked it falls back to
// trunk(), which auto-resolves the repo's real default branch (so it's already
// better than a hardcoded "main"). Runs jj read-only in the workspace dir;
// any error degrades to trunk().
func resolveReviewStackBase(runner Runner, dir, ownBookmark string) string {
	revset, _ := resolveReviewStackBaseNamed(runner, dir, ownBookmark)
	return revset
}

// resolveReviewStackBaseNamed is resolveReviewStackBase plus the base's display
// name, for chrome that has to say what you are being diffed against.
//
// The two differ only for the trunk fallback: the revset is the literal
// "trunk()", which jj resolves but which means nothing to a reader, so the label
// is the branch trunk() actually names. The label is empty only when there is
// nothing to say — no directory to ask in.
func resolveReviewStackBaseNamed(runner Runner, dir, ownBookmark string) (revset, label string) {
	if strings.TrimSpace(dir) == "" {
		return "trunk()", ""
	}
	j := jj.New(fixedDirRunner{base: runner, dir: dir})
	trunk, _ := j.Trunk()
	if strings.TrimSpace(trunk) == "" {
		trunk = "main"
	}
	// Nearest bookmarked ancestor of @ (heads = closest to @), excluding the
	// trunk bookmark by name — bookmarks(exact:) matches both the local and
	// @remote forms, so a locally-ahead trunk can't masquerade as a parent —
	// and the workspace's own bookmark.
	// query finds the parent; it is not the base being returned.
	//
	// `~ @` is what stops a change being its own base. trunk()..@ includes @, so a
	// bookmarked @ answers this query with its own bookmark and the diff comes back
	// empty. The own-bookmark exclusion below used to hide that everywhere it was
	// recorded — but the default workspace never gets a bookmark, so there the guard
	// was simply absent and `awp diff` in a repo whose @ carried a bookmark showed
	// nothing at all. Excluding @ structurally does not depend on a name having been
	// written down somewhere.
	query := fmt.Sprintf(`heads((trunk()..@) & bookmarks() ~ @ ~ bookmarks(exact:%q)`, trunk)
	if b := strings.TrimSpace(ownBookmark); b != "" {
		query += fmt.Sprintf(` ~ bookmarks(exact:%q)`, b)
	}
	query += `)`
	if parent, err := j.BookmarkNameAt(query); err == nil && strings.TrimSpace(parent) != "" {
		return parent, parent
	}
	return "trunk()", trunk
}

// expandWindowArg resolves the review-window sentinel `C` emits into the command
// the window runs (see deckui.ReviewStackArg). Every other arg passes through
// untouched.
//
// Plain `awp diff`, with no revset spliced in: the standalone viewer resolves the
// same scope `c` opens on by itself now, so naming the range here would be a
// second copy of that decision — and the copy would be the one that goes stale.
// It also retires having to quote `trunk()..@` on its way through a shell.
func expandWindowArg(_ Runner, _ deckui.Item, arg string) string {
	if arg != deckui.ReviewStackArg {
		return arg
	}
	return "review:awp diff"
}

func openNamedWindow(tmuxClient *tmux.Client, svc workspace.Service, item deckui.Item, arg string, reporter deckui.Reporter) error {
	windowName, cmdOverride := arg, ""
	if idx := strings.IndexByte(arg, ':'); idx >= 0 {
		windowName = arg[:idx]
		cmdOverride = arg[idx+1:]
	}

	sessionName := DeckSessionName(item.ProjectName, item.WorkspaceName)
	id, err := tmuxClient.SessionIDByName(sessionName)
	if err != nil {
		return err
	}
	path := resolvePath(svc, item)
	env := workspaceEnvPairs(item.ProjectName, item.WorkspaceName, item.RepoRoot)
	if id == "" {
		reporter.Step(fmt.Sprintf("Create tmux session %s", sessionName))
		newID, err := createWorkspaceSession(tmuxClient, sessionName, path, item.RepoRoot, env)
		if err != nil {
			return err
		}
		id = newID
		_ = svc.RecordSession(item.WorkspaceName, id, sessionName)
	}
	_ = ensureWorkspaceSessionEnvForItem(tmuxClient, sessionName, item.ProjectName, item.WorkspaceName, item.RepoRoot)

	// Empty windowName = fresh shell window, no dedupe, tmux picks title.
	if strings.TrimSpace(windowName) == "" {
		reporter.Step("Open shell window")
		target, err := tmuxClient.NewShellWindowInSession(sessionName, path, env)
		if err != nil {
			return err
		}
		if err := tmuxClient.SwitchToWindow(target); err != nil {
			return err
		}
		return tmuxClient.SwitchClient(sessionName)
	}

	target := sessionName + ":" + windowName
	exists := false
	justCreated := false
	windows, _ := tmuxClient.ListWindowsInSession(sessionName)
	for _, w := range windows {
		if w.Name == windowName {
			exists = true
			break
		}
	}
	if !exists {
		reporter.Step(fmt.Sprintf("Open %s window", windowName))
		if err := tmuxClient.NewWindowInSession(sessionName, windowName, path, env); err != nil {
			return err
		}
		justCreated = true
	}
	winCmd := cmdOverride
	if winCmd == "" {
		winCmd = defaultWindowCommandWithRepo(windowName, item.RepoRoot)
	}
	if winCmd != "" && (justCreated || paneIsShell(tmuxClient, target)) {
		reporter.Step(fmt.Sprintf("Run %s", winCmd))
		if err := tmuxClient.SendCommand(target, winCmd); err != nil {
			return err
		}
	}
	reporter.Step(fmt.Sprintf("Switch to %s", target))
	if err := tmuxClient.SwitchToWindow(target); err != nil {
		return err
	}
	_ = svc.MarkRead(item.WorkspaceName)
	return tmuxClient.SwitchClient(sessionName)
}

func paneIsShell(tmuxClient *tmux.Client, target string) bool {
	cmd, err := tmuxClient.PaneCurrentCommand(target)
	if err != nil {
		return false
	}
	switch strings.TrimSpace(cmd) {
	case "bash", "zsh", "fish", "sh", "dash":
		return true
	default:
		return false
	}
}

func defaultWindowCommand(windowName string) string {
	return defaultWindowCommandWithRepo(windowName, "")
}

// defaultWindowCommandWithRepo returns the default command to run in a freshly
// created (or shell-reset) named window. Pulls the agent command from
// per-repo + global config; defaults to "pi" when nothing is configured.
func defaultWindowCommandWithRepo(windowName, repoRoot string) string {
	switch windowName {
	case "editor":
		return "$EDITOR"
	case "review":
		return "awp diff"
	case "vcs":
		return "jjui"
	case "agent":
		return codingAgentInvocation(repoRoot)
	}
	return ""
}

// ciWatchScript is what `i` runs, wherever it runs: resolve the workspace's
// nearest bookmark, find that branch's most recent GitHub run, and watch it.
//
// One string because the tmux window and the zdeck pane have to watch the same
// run, and the resolution happens in the shell rather than in Go — so a second
// copy would be a second answer to "which run", drifting the first time either
// is edited. It carries no single quote, which is what lets the tmux path wrap
// it in one to type it at a shell; ci_pane_test.go pins that.
const ciWatchScript = `b=$(jj log --no-graph -r "latest(::@ & bookmarks())" -T "local_bookmarks.map(|b| b.name()).join(\"\n\") ++ \"\n\"" | head -n1); id=$(gh run list --branch "$b" --limit 1 --json databaseId -q ".[0].databaseId"); gh run watch "$id" --compact --exit-status || gh run view "$id"`

// openCIWindow opens (or reuses) a `ci` tmux window in the workspace and runs
// ciWatchScript in it. gh resolves the repo from the workspace's cwd.
//
// The zdeck pane for the same key runs the same script directly on a pty — see
// the `ci` entry in zdeck.go's panes map.
func openCIWindow(tmuxClient *tmux.Client, svc workspace.Service, _ Runner, item deckui.Item, reporter deckui.Reporter) error {
	reporter.Step("Open ci window")
	path := resolvePath(svc, item)
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("ci: no path for workspace %q", item.WorkspaceName)
	}

	sessionName := DeckSessionName(item.ProjectName, item.WorkspaceName)
	id, err := tmuxClient.SessionIDByName(sessionName)
	if err != nil {
		return err
	}
	env := workspaceEnvPairs(item.ProjectName, item.WorkspaceName, item.RepoRoot)
	if id == "" {
		newID, err := createWorkspaceSession(tmuxClient, sessionName, path, item.RepoRoot, env)
		if err != nil {
			return err
		}
		id = newID
		_ = svc.RecordSession(item.WorkspaceName, id, sessionName)
	}
	_ = ensureWorkspaceSessionEnvForItem(tmuxClient, sessionName, item.ProjectName, item.WorkspaceName, item.RepoRoot)

	target := sessionName + ":ci"
	exists := false
	windows, _ := tmuxClient.ListWindowsInSession(sessionName)
	for _, w := range windows {
		if w.Name == "ci" {
			exists = true
			break
		}
	}
	if !exists {
		if err := tmuxClient.NewWindowInSession(sessionName, "ci", path, env); err != nil {
			return err
		}
	}
	cmd := "bash -c '" + ciWatchScript + "'"
	if !exists || paneIsShell(tmuxClient, target) {
		if err := tmuxClient.SendCommand(target, cmd); err != nil {
			return err
		}
	}
	if err := tmuxClient.SwitchToWindow(target); err != nil {
		return err
	}
	_ = svc.MarkRead(item.WorkspaceName)
	return tmuxClient.SwitchClient(sessionName)
}

func openCustomActionWindow(tmuxClient *tmux.Client, svc workspace.Service, item deckui.Item, ua deckui.UserAction, reporter deckui.Reporter) error {
	reporter.Step(fmt.Sprintf("Run user action %s", ua.Name))
	sessionName := DeckSessionName(item.ProjectName, item.WorkspaceName)
	id, err := tmuxClient.SessionIDByName(sessionName)
	if err != nil {
		return err
	}
	path := resolvePath(svc, item)
	env := workspaceEnvPairs(item.ProjectName, item.WorkspaceName, item.RepoRoot)
	if id == "" {
		newID, err := createWorkspaceSession(tmuxClient, sessionName, path, item.RepoRoot, env)
		if err != nil {
			return err
		}
		id = newID
		_ = svc.RecordSession(item.WorkspaceName, id, sessionName)
	}
	_ = ensureWorkspaceSessionEnvForItem(tmuxClient, sessionName, item.ProjectName, item.WorkspaceName, item.RepoRoot)

	windowName := ua.Name
	target := sessionName + ":" + windowName
	exists := false
	windows, _ := tmuxClient.ListWindowsInSession(sessionName)
	for _, w := range windows {
		if w.Name == windowName {
			exists = true
			break
		}
	}
	if !exists {
		if err := tmuxClient.NewWindowInSession(sessionName, windowName, path, env); err != nil {
			return err
		}
	}
	if !exists || paneIsShell(tmuxClient, target) {
		if err := tmuxClient.SendCommand(target, ua.Command); err != nil {
			return err
		}
	}
	if !ua.Focus {
		// Window is created and the command is running; deliberately
		// don't pull the user away from the deck.
		return nil
	}
	if err := tmuxClient.SwitchToWindow(target); err != nil {
		return err
	}
	_ = svc.MarkRead(item.WorkspaceName)
	return tmuxClient.SwitchClient(sessionName)
}

func resolvePath(svc workspace.Service, item deckui.Item) string {
	if strings.TrimSpace(item.Path) != "" {
		return item.Path
	}
	info, err := svc.Info(item.WorkspaceName)
	if err != nil {
		return ""
	}
	return info.Path
}

func maybeUpdateStaleWorkingCopy(j *jj.Client, in io.Reader, out io.Writer, cause error) (bool, error) {
	if !isInteractiveInput(in) {
		return false, cause
	}
	if out != nil {
		_, _ = fmt.Fprintf(out, "Detected stale jj working copy:\n\n%s\n\nRun `jj workspace update-stale` now? [y/N]: ", cause)
	}
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	answer := strings.TrimSpace(strings.ToLower(line))
	if answer != "y" && answer != "yes" {
		return false, cause
	}
	if out != nil {
		_, _ = fmt.Fprintln(out, "Updating stale working copy...")
	}
	if err := j.UpdateStale(); err != nil {
		return false, err
	}
	if out != nil {
		_, _ = fmt.Fprintln(out, "Working copy updated. Reloading deck...")
	}
	return true, nil
}
