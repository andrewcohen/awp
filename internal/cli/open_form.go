package cli

import (
	"io"
	"strings"

	"github.com/andrewcohen/awp/internal/config"
	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/jj"
)

// openRequest carries the CLI-facing fields collected via the unified
// workspace form, plus a couple of caller flags (Yes, NoSwitch) that the
// form itself doesn't surface. The form returns deckui.NewWorkspaceRequest;
// runOpenWithCharm maps that into openRequest while preserving the inbound
// flags.
type openRequest struct {
	Name             string
	Bookmark         string // anchor revision (jj new <bookmark>) for the new workspace's @
	BookmarkToCreate string // new bookmark to create on @ (blank = skip)
	Prompt           string
	PRNumber         int // pin the created workspace to this PR (0 = none)
	Yes              bool
	// NoSwitch suppresses the final tmux switch-client step. Used by the
	// async create-workspace job so the subprocess prepares the workspace
	// without yanking the user's tmux focus away from the deck.
	NoSwitch bool
}

// runOpenWithCharm runs the unified workspace form (a deckui tea.Program)
// against the given runner and io streams, returning the user's request.
//
// The form's "Start from: pick a bookmark…" branch surfaces an inline
// picker populated from `jj.AllBookmarks()`. The "Will create" hint uses
// the repo's configured deck.bookmark_prefix.
func runOpenWithCharm(initial openRequest, runner Runner, in io.Reader, out io.Writer) (openRequest, error) {
	j := jj.New(runner)
	prefix := loadBookmarkPrefix(j)
	trunk, _ := j.Trunk()
	listBookmarks := func() ([]string, error) { return j.AllBookmarks() }

	req, err := deckui.RunWorkspaceForm(in, out, deckui.NewWorkspaceInitial{
		Name:     strings.TrimSpace(initial.Name),
		Bookmark: strings.TrimSpace(initial.Bookmark),
	}, prefix, trunk, listBookmarks)
	if err != nil {
		return openRequest{}, err
	}
	return openRequest{
		Name:             req.Name,
		Bookmark:         req.Bookmark,
		BookmarkToCreate: req.BookmarkToCreate,
		Prompt:           req.Prompt,
		PRNumber:         initial.PRNumber,
		Yes:              initial.Yes,
		NoSwitch:         initial.NoSwitch,
	}, nil
}

// loadBookmarkPrefix returns the configured deck.bookmark_prefix for the
// current repo, or "" if either no repo root is discoverable or no prefix
// is set. Best-effort: any failure suppresses the hint, never aborts.
func loadBookmarkPrefix(j *jj.Client) string {
	root, err := j.RepoRoot()
	if err != nil || strings.TrimSpace(root) == "" {
		return ""
	}
	cfg, err := config.Load(root)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cfg.Deck.BookmarkPrefix)
}
