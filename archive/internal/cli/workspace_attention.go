package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/andrewcohen/awp/internal/deckdata"
	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/prstatus"
	"github.com/andrewcohen/awp/internal/tmux"
)

// `awp workspace attention` — which workspaces want you, and why.
//
// This is the one read the captain cannot approximate. Everything else it needs
// about a repository or a PR it could get from `jj` and `gh`; attention is awp's own
// opinion about what matters, assembled from six sources — an agent's reported state,
// its unread mark, whether its session is live, its PR's review decision, its CI, how
// recently it changed hands — and ranked into bands so an agent's lifecycle does not
// keep moving its row (#284). Reproducing that outside awp would be inventing a
// second opinion, and the second one would be wrong in ways nobody noticed.
//
// So the predicate is not re-derived here. internal/deckdata is the read model the
// deck itself renders from: the same View, the same Scope, the same Wants. This
// command is a printer for it, which is what makes "agrees with the deck" a property
// rather than a hope.

const workspaceAttentionUsage = `Usage: awp w attention [--json]

Lists the workspaces that want your attention, most urgent first, each with the
reason — the same rows and the same order the deck's attention scope shows.

  --json   machine-readable, one object per row.

Reasons come from awp's own state, not from git: an agent waiting on an answer, an
errored agent, a PR whose review is requested from you, failing CI, a workspace that
changed hands recently.`

// attentionRow is one row of the answer, in the words the reason is written in.
//
// Its own type rather than printing deckui.Item because most of an Item is about
// rendering a row on a deck — glyph state, session names, dev-loop progress — and
// what a reader of this wants is which workspace, in which project, and why. The
// JSON shape is part of the interface once an agent parses it, so it names its fields
// rather than exposing the deck's internals to be renamed later.
type attentionRow struct {
	Project   string `json:"project"`
	Workspace string `json:"workspace"`
	Reason    string `json:"reason"`
	// PR is the PR number when this row has one, 0 otherwise. Included because the
	// most common next thing to do with an attention row is about its PR.
	PR int `json:"pr,omitempty"`
	// Virtual marks a row with no local workspace — a PR whose review is requested
	// from you that you have not checked out. It is in the list precisely because
	// it wants something, but there is nothing local to send a prompt to.
	Virtual bool `json:"virtual,omitempty"`
}

// runWorkspaceAttention implements `awp workspace attention`.
func (a *App) runWorkspaceAttention(args []string) error {
	if isHelpArgSlice(args) {
		_, _ = fmt.Fprintln(a.out, workspaceAttentionUsage)
		return nil
	}
	asJSON := false
	for _, arg := range args {
		switch arg {
		case "--json":
			asJSON = true
		default:
			return fmt.Errorf("workspace attention: unknown argument %q (try: awp w attention [--json])", arg)
		}
	}

	rows, err := a.attentionRows()
	if err != nil {
		return err
	}
	return printAttention(a.out, rows, asJSON)
}

// printAttention writes the rows, either for a person or for a parser.
//
// Separate from gathering them so the output can be checked without a repo, a state
// file or a session substrate — which is most of what gathering involves and none of
// what the shape of the answer depends on.
func printAttention(w io.Writer, rows []attentionRow, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	if len(rows) == 0 {
		// Said rather than left blank: no output is indistinguishable from the
		// command having failed quietly, and "nothing wants you" is a real answer
		// that a captain should be able to report.
		_, _ = fmt.Fprintln(w, "nothing wants your attention")
		return nil
	}
	for _, r := range rows {
		suffix := ""
		if r.Virtual {
			suffix = " (no local workspace)"
		}
		_, _ = fmt.Fprintf(w, "%s/%s — %s%s\n", r.Project, r.Workspace, r.Reason, suffix)
	}
	return nil
}

// attentionRows is the deck's attention scope, as data.
//
// No --project: attention is a question about everything at once. The deck's own
// scope spans every project in the store, and narrowing it here would answer a
// different question than the one the deck answers — which is the property this
// command exists to have.
func (a *App) attentionRows() ([]attentionRow, error) {
	items, err := a.attentionItems()
	if err != nil {
		return nil, err
	}
	byRepo, _, err := loadPRStatusCache()
	if err != nil {
		// Not fatal. A cold or unreadable cache costs the PR-derived reasons and
		// leaves the agent-derived ones, which is a shorter answer rather than a
		// wrong one — and a captain that got an error here would have nothing.
		byRepo = nil
	}
	return attentionRowsFor(items, prStatusForDeckdata(byRepo)), nil
}

// attentionRowsFor is the whole of the derivation: build the deck's read model over
// these rows, ask it for the attention scope, and ask it why each row is in it.
//
// Three lines that must not become four. The moment anything here decides for itself
// which rows want you, this command and the deck can disagree — and both answers
// would look reasonable, so nobody would notice which was wrong.
func attentionRowsFor(items []deckui.Item, prStatus map[string]map[string]prstatus.PRStatus) []attentionRow {
	view := deckdata.View{
		All:            items,
		Scope:          deckdata.ScopeAttention,
		PRStatusByRepo: prStatus,
	}
	scoped := view.Items()
	out := make([]attentionRow, 0, len(scoped))
	for _, it := range scoped {
		reason := strings.TrimSpace(view.WantsText(it))
		if reason == "" {
			// In the scope with nothing to say happens for a row whose reason is its
			// rank rather than a sentence. Naming it beats an empty column.
			reason = "wants a look"
		}
		out = append(out, attentionRow{
			Project:   it.ProjectName,
			Workspace: it.WorkspaceName,
			Reason:    reason,
			PR:        it.PRNumber,
			Virtual:   it.Virtual,
		})
	}
	return out
}

// attentionItems is every workspace row the deck would have, from the same builder.
//
// loadDeckItems rather than a walk of the state file: the rows carry the agent state,
// the unread mark and the session liveness that three of the attention reasons are
// about, and reading the store alone would produce rows that look idle whatever is
// running in them.
func (a *App) attentionItems() ([]deckui.Item, error) {
	root, err := a.ambientRepoRoot()
	if err != nil {
		// The repo only names which project is "current", for the adoptable-session
		// pass. Attention itself spans every project in the store, so standing
		// outside a repo is not a reason to refuse — the captain always is.
		root = ""
	}
	// Rooted here for the same reason it is in runDeckWithCharm: this is a
	// one-shot read with no caller that would cancel it, and the fan-out inside
	// bounds itself.
	return loadDeckItems(context.Background(), nil, a.attentionSessions(), false, a.svc, root, projectNameFor(root), nil, nil, nil)
}

// attentionSessions picks the substrate to ask about live agents.
//
// deckSessions is deliberately not a merge of tmux and zmx — a leftover session on
// the substrate a deck does not use would make a workspace read live when it is not.
// A CLI caller has no deck to inherit the answer from, so it has to choose, and it
// chooses the way agentPromptSender already does when it has no host object: ask zmx
// first, because that is where a hosted agent lives, and fall back to tmux when zmx
// has nothing to say. The same reasoning, for the same reason — a surface can be
// hosted by a deck it cannot see.
func (a *App) attentionSessions() deckSessions {
	client := zmxClientFor(a.runner)
	if list, err := client.List(context.Background()); err == nil && len(list) > 0 {
		return zmxSessions{client: client, rows: knownWorkspaceRefs}
	}
	return tmuxSessions{client: tmux.New(a.runner)}
}

// prStatusForDeckdata re-keys the PR cache into the read model's own type.
//
// The cache stores deckui.PRStatus and deckdata wants prstatus.PRStatus. They are
// the same fields — deckui.PRStatus is an alias of it — so this is a map copy rather
// than a conversion, and it exists to keep the seam honest if they ever diverge.
func prStatusForDeckdata(byRepo map[string]map[string]deckui.PRStatus) map[string]map[string]prstatus.PRStatus {
	if len(byRepo) == 0 {
		return nil
	}
	out := make(map[string]map[string]prstatus.PRStatus, len(byRepo))
	for repo, byHead := range byRepo {
		inner := make(map[string]prstatus.PRStatus, len(byHead))
		for head, s := range byHead {
			inner[head] = s
		}
		out[repo] = inner
	}
	return out
}
