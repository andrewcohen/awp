package cli

import (
	"fmt"
	"strings"

	"github.com/andrewcohen/awp/internal/config"
	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/tmux"
	"github.com/andrewcohen/awp/internal/workspace"
)

// `awp workspace repair` — tell a workspace's agent what is wrong with its PR.
//
// The prompt is the deck's (`C r`, via deckui.PRRepairPrompt) and that is the whole
// point: one sentence, built in one place. It knows things a hand-written
// instruction would not, most importantly whose PR it is. On your own PR it asks
// the agent to fix and push; on someone else's it asks the agent to investigate and
// report, changing no files and pushing nothing — #171, where a reviewer was handed
// the author's chores.
//
// Where this differs from `C r` is what happens next. The deck prepopulates the
// send-prompt form so you can read the prompt before it goes, because you are
// standing right there. There is nobody standing here, so it sends — and prints the
// prompt it sent, so the exchange is not invisible.

const workspaceRepairUsage = `Usage: awp w repair [--project <name|path>] [--dry-run] <workspace>

Tells the workspace's agent what is wrong with its PR: merge conflicts, failing CI,
an out-of-date base, review feedback.

  --project <name|path>   which project the workspace is in. Omit it to use the repo
                          you are standing in.
  --dry-run               print the prompt and send nothing.

Whose PR it is decides what the agent is asked to do. Your own (by the repo's
bookmark prefix): fix it and push. Someone else's: investigate and report back,
changing no files and pushing nothing.

Reads the PR status the deck caches. If nothing is cached for the workspace yet,
this says so rather than guessing — open the deck once to populate it.`

// runWorkspaceRepair implements `awp workspace repair`.
func (a *App) runWorkspaceRepair(args []string) error {
	if isHelpArgSlice(args) {
		_, _ = fmt.Fprintln(a.out, workspaceRepairUsage)
		return nil
	}

	project, rest, err := takeProjectFlag(args)
	if err != nil {
		return err
	}
	dryRun := false
	kept := rest[:0:0]
	for _, arg := range rest {
		if arg == "--dry-run" {
			dryRun = true
			continue
		}
		kept = append(kept, arg)
	}
	if len(kept) != 1 {
		return fmt.Errorf("workspace repair: give one workspace (try: awp w repair --project <name> <workspace>)")
	}
	name := strings.TrimSpace(kept[0])

	svc, projectName, repoRoot, err := a.sendTarget(project)
	if err != nil {
		return err
	}
	entry, err := workspaceEntry(svc, projectName, name)
	if err != nil {
		return err
	}

	status, err := cachedPRStatusFor(repoRoot, projectName, entry)
	if err != nil {
		return err
	}
	cfg, _ := config.Load(repoRoot)
	mine := deckui.PRIsMine(entry.Bookmark, cfg.Deck.BookmarkPrefix)
	prompt := deckui.PRRepairPrompt(status, "", mine)
	if strings.TrimSpace(prompt) == "" {
		// Not an error. "Nothing to repair" is a correct and common answer — an open
		// PR with green CI and no feedback — and a caller that treated it as a
		// failure would report a healthy PR as a problem.
		_, _ = fmt.Fprintf(a.out, "%s/%s: nothing to repair (PR #%d is %s)\n", projectName, name, status.Number, strings.ToLower(string(status.State)))
		return nil
	}

	if dryRun {
		_, _ = fmt.Fprintf(a.out, "%s/%s: would send this prompt (PR #%d, %s):\n\n%s\n", projectName, name, status.Number, repairTone(mine), prompt)
		return nil
	}

	item := deckui.Item{ProjectName: projectName, WorkspaceName: entry.Name, Path: entry.Path, RepoRoot: repoRoot}
	send := agentPromptSender(nil, a.runner, tmux.New(a.runner), svc)
	if err := send(item, prompt, nil); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(a.out, "sent a %s prompt to %s/%s for PR #%d\n", repairTone(mine), projectName, name, status.Number)
	return nil
}

// repairTone names which of the two prompts was built, so the output says what the
// agent was actually asked to do rather than only that something was sent.
func repairTone(mine bool) string {
	if mine {
		return "fix-and-push"
	}
	return "investigate-only"
}

// workspaceEntry is the workspace's own state entry, or an error naming what exists.
//
// Separate from resolveWorkspaceItem because this caller needs more than a path: the
// bookmark decides whose PR it is, and the PR number is how a pinned workspace finds
// its status when its bookmark does not match the PR's head ref.
func workspaceEntry(svc workspace.Service, projectName, name string) (workspace.ListEntry, error) {
	entries, err := svc.List()
	if err != nil {
		return workspace.ListEntry{}, fmt.Errorf("list workspaces in %s: %w", projectName, err)
	}
	var known []string
	for _, e := range entries {
		if e.Name == name {
			return e, nil
		}
		known = append(known, e.Name)
	}
	if len(known) == 0 {
		return workspace.ListEntry{}, fmt.Errorf("no workspace %q in %s, which has none yet", name, projectName)
	}
	return workspace.ListEntry{}, fmt.Errorf("no workspace %q in %s; known: %s", name, projectName, strings.Join(known, ", "))
}

// cachedPRStatusFor finds the workspace's PR in the status the deck caches.
//
// Two ways in, because a workspace can be tied to a PR two ways. Normally its
// bookmark is the PR's head ref and that is the cache's key. A workspace pinned to a
// PR number (`C s`, or a review) may have a bookmark that is not the head ref at
// all, so a miss falls back to scanning for the number — which is the same pairing
// the deck's own row does.
//
// Refusing rather than fetching. A gh call per repair would be slow, would need
// auth, and would put a second source of PR truth beside the cache the whole deck
// reads; saying "not cached yet" is honest and recoverable, where a second source
// that disagrees is neither.
func cachedPRStatusFor(repoRoot, projectName string, entry workspace.ListEntry) (deckui.PRStatus, error) {
	byRepo, _, err := loadPRStatusCache()
	if err != nil {
		return deckui.PRStatus{}, fmt.Errorf("read the PR status cache: %w", err)
	}
	byHead := byRepo[repoRoot]
	if len(byHead) == 0 {
		return deckui.PRStatus{}, fmt.Errorf("no PR status cached for %s yet — open the deck once to populate it, or wait for the pr-status job", projectName)
	}
	if bm := strings.TrimSpace(entry.Bookmark); bm != "" {
		if s, ok := byHead[bm]; ok {
			return s, nil
		}
	}
	if entry.PRNumber > 0 {
		for _, s := range byHead {
			if s.Number == entry.PRNumber {
				return s, nil
			}
		}
		return deckui.PRStatus{}, fmt.Errorf("%s is pinned to PR #%d but that PR is not in the cached status for %s — open the deck once to refresh it", entry.Name, entry.PRNumber, projectName)
	}
	if strings.TrimSpace(entry.Bookmark) == "" {
		return deckui.PRStatus{}, fmt.Errorf("%s has no bookmark and no pinned PR, so there is no PR to repair — link one with B in the deck, or set the number with C s", entry.Name)
	}
	return deckui.PRStatus{}, fmt.Errorf("no PR found for %s (bookmark %q) in %s — it may not be pushed yet", entry.Name, entry.Bookmark, projectName)
}
