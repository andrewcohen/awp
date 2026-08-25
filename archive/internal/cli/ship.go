package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/andrewcohen/awp/internal/config"
	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/ship"
	"github.com/andrewcohen/awp/internal/tmux"
	"github.com/andrewcohen/awp/internal/watch"
	"github.com/andrewcohen/awp/internal/workspace"
)

// `awp ship` — this change is done; do what this repo does with done changes.
//
// One verb because the agent that finished the work should not have to know
// which convention the repository follows. It knows the change is finished; the
// repo's config says what finished means there (internal/ship holds the styles).
//
// No --project and no workspace argument, unlike `awp workspace repair`. Repair
// is something you do *to* another workspace, so it has to be able to name one;
// ship is something a workspace does to its own change, and the thing running it
// is the agent standing in that workspace. A flag naming a different workspace
// would only be a way to ship someone else's work by accident.
//
// That is also why the captain does not run this. It has no workspace, so there
// is nothing for it to ship, and its route to a shipped change is to tell the
// agent that wrote it: `awp workspace send <ws> "ship it"`. The outward action
// then belongs to the agent that did the work, and the captain's refusal list
// stays a fixed set of effects rather than gaining an entry that depends on how
// some repo is configured.
//
// Gates are re-read here rather than assumed. Ship is a precondition, not an
// assertion: the interesting failures live in the window between the last green
// check and the ship, so the check is part of shipping.

const shipUsage = `Usage: awp ship [--dry-run]

Ships the current workspace's change the way this repo ships changes. Run it in
the workspace whose change is done.

  --dry-run   print what would happen and change nothing.

What shipping means is the repo's "ship" setting in .awp/config.json:

  "ship": "main"           rebase the change onto the trunk bookmark, move the
                           bookmark onto it, and move the default workspace on.
                           Local only — nothing is pushed.
  "ship": "pull_request"   push the bookmark and open or update the PR.
                           Not implemented yet.

Unset means ship refuses: a repo that has not said which convention it follows
should not have one guessed for it.

The dev-loop gates are checked as part of shipping rather than taken on trust.
Under the main style a red gate is a stop. If the rebase onto trunk conflicts,
trunk is left where it was and the workspace's agent is sent a prompt asking it
to resolve the conflicts — the same turn-into-repair that awp workspace repair
makes for a PR.`

// runShip implements `awp ship`.
func (a *App) runShip(args []string) error {
	if isHelpArgSlice(args) {
		_, _ = fmt.Fprintln(a.out, shipUsage)
		return nil
	}
	dryRun := false
	for _, arg := range args {
		switch arg {
		case "--dry-run", "-n":
			dryRun = true
		default:
			return fmt.Errorf("ship: unexpected argument %q (try: awp ship [--dry-run])", arg)
		}
	}

	wsName, repoName, repoRoot := resolveWorkspaceIdent()
	if strings.TrimSpace(wsName) == "" {
		return errors.New("ship: this is not an awp workspace, so there is no change to ship — run it in the workspace whose change is done")
	}
	if root, ok := resolveGateRepoRoot(repoName, repoRoot, wsName); ok {
		repoRoot = root
	}
	if strings.TrimSpace(repoRoot) == "" {
		return fmt.Errorf("ship: could not work out which repo %q belongs to, so there is no trunk to ship onto", wsName)
	}

	res, err := a.shipWorkspace(repoRoot, wsName, dryRun, nil)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprint(a.out, res)
	return nil
}

// shipWorkspace is the ship itself, for both callers that have one.
//
// `awp ship` is one; the deck's `C S` is the other, arriving through
// deckui.ActionShip with a progress reporter attached. They share this rather
// than each assembling the style, the gates and the target, because a deck key
// that shipped by slightly different rules than the verb would be the worst kind
// of difference: invisible until the day one of them ships something the other
// would have stopped.
//
// Returns the report text rather than printing it — the CLI prints it, and the
// deck puts the first line in its status bar.
func (a *App) shipWorkspace(repoRoot, wsName string, dryRun bool, rep ship.Reporter) (string, error) {
	cfg, _ := config.Load(repoRoot)
	style, err := ship.StyleFor(cfg.Ship)
	if err != nil {
		return "", err
	}
	// Said before any work is done. A repo configured for a style that is not
	// built should hear that instead of watching its gates get checked first.
	if !style.Implemented() {
		if _, err := style.Run(a.runner, ship.Target{}, rep); err != nil {
			return "", err
		}
	}

	projectName := projectNameFor(repoRoot)
	// A service built against the resolved *source* repo root, not the ambient
	// one. Ship runs inside a workspace, where `jj root` answers with the
	// workspace's own directory — and a service built on that derives the
	// workspace base from it, resolving paths like `…/ship-verb/ship-verb`,
	// which is nowhere. The repo root is the one resolveWorkspaceIdent agreed on.
	svc := a.shipService(repoRoot)
	entry, err := workspaceEntry(svc, projectName, wsName)
	if err != nil {
		return "", err
	}
	target, err := a.shipTarget(repoRoot, entry.Path)
	if err != nil {
		return "", err
	}

	var report strings.Builder
	cond := ship.GateCondition(redRequiredGates(shipLoop(cfg), currentGates(repoRoot, wsName)), target.empty, target.description)
	if !cond.Shippable() {
		switch style.GatePolicy {
		case ship.PolicyStop:
			// Carries its own "ship:" like every other message from this path, so a
			// caller can surface it verbatim — the deck puts it in the status bar,
			// where a wrapped-at-the-call-site prefix would read as "ship: ship:".
			return "", fmt.Errorf("ship: not shipping %s — %s. Fix that and try again", target.Revision, cond.Summary())
		case ship.PolicyReportAndAllow:
			fmt.Fprintf(&report, "ship: %s, and the %s style ships anyway\n", cond.Summary(), style.Name)
		}
	}

	if dryRun {
		fmt.Fprintf(&report, "ship: would ship %s (%s) onto %s in %s, style %s:\n  jj rebase -s %s -d %s\n  jj bookmark set %s -r %s\n  jj new %s  (in the default workspace)\n",
			target.Revision, target.description, target.Trunk, projectName, style.Name,
			target.Revision, target.Trunk, target.Trunk, target.Revision, target.Trunk)
		return report.String(), nil
	}

	res, err := style.Run(a.runner, target.Target, rep)
	if errors.Is(err, ship.ErrConflicts) {
		return "", a.shipConflictRepair(projectName, repoRoot, entry.Name, entry.Path, target.Target)
	}
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&report, "shipped %s (%s) onto %s in %s\n", target.Revision, target.description, target.Trunk, projectName)
	for _, s := range res.Steps {
		fmt.Fprintf(&report, "  %s\n", s)
	}
	return report.String(), nil
}

// shipTarget is a ship.Target plus what the gate check needs to know about the
// revision. The two travel together because they come from one jj call.
type shipTarget struct {
	ship.Target
	description string
	empty       bool
}

// shipTarget resolves what to ship and where trunk is.
//
// The revision is the workspace's working copy, or the commit below it when the
// working copy is an empty undescribed commit — which is the ordinary state of a
// workspace whose change has been described and then left alone. Shipping the
// empty one would put a no-op on trunk and leave the actual change behind.
func (a *App) shipTarget(repoRoot, wsPath string) (shipTarget, error) {
	head, err := a.shipRevision(wsPath, "@")
	if err != nil {
		return shipTarget{}, err
	}
	if head.empty && strings.TrimSpace(head.description) == "" {
		below, err := a.shipRevision(wsPath, "@-")
		if err != nil {
			return shipTarget{}, err
		}
		head = below
	}
	trunk, err := a.shipTrunk(wsPath)
	if err != nil {
		return shipTarget{}, err
	}
	head.WorkspacePath = wsPath
	head.DefaultWorkspacePath = repoRoot
	head.Trunk = trunk
	return head, nil
}

// shipRevision reads one revision's change-id, description and emptiness.
//
// One jj call for all three so they cannot describe different revisions — the
// working copy moves under a sequence of calls, and "is it empty" answered about
// a different commit than the one being shipped is worse than not asking.
func (a *App) shipRevision(dir, revset string) (shipTarget, error) {
	const tmpl = `change_id.shortest(8) ++ "\t" ++ if(empty, "empty", "nonempty") ++ "\t" ++ description.first_line()`
	out, err := a.runner.Run(context.Background(), dir, "jj", "--ignore-working-copy", "log", "--no-graph", "-r", revset, "-T", tmpl)
	if err != nil {
		return shipTarget{}, fmt.Errorf("ship: read %s in %s: %w: %s", revset, dir, err, strings.TrimSpace(out))
	}
	fields := strings.SplitN(strings.TrimRight(out, "\n"), "\t", 3)
	if len(fields) < 2 || strings.TrimSpace(fields[0]) == "" {
		return shipTarget{}, fmt.Errorf("ship: %s in %s resolved to nothing to ship", revset, dir)
	}
	t := shipTarget{empty: strings.TrimSpace(fields[1]) == "empty"}
	t.Revision = strings.TrimSpace(fields[0])
	if len(fields) == 3 {
		t.description = strings.TrimSpace(fields[2])
	}
	return t, nil
}

// shipTrunk is the bookmark the main style moves, from jj's own trunk() revset
// so a repo whose integration branch is not called main is not assumed to be.
func (a *App) shipTrunk(dir string) (string, error) {
	out, err := a.runner.Run(context.Background(), dir, "jj", "--ignore-working-copy", "log", "--no-graph", "-r", "trunk()", "-T", `bookmarks.map(|b| b.name()).join("\n")`)
	if err != nil {
		return "", fmt.Errorf("ship: resolve this repo's trunk bookmark: %w: %s", err, strings.TrimSpace(out))
	}
	for _, line := range strings.Split(out, "\n") {
		name := strings.TrimSpace(line)
		if name == "" || strings.Contains(name, "@") {
			continue
		}
		return name, nil
	}
	return "", errors.New("ship: no bookmark at trunk(), so there is nothing to ship onto — create one, or point jj's trunk() alias at the branch you integrate on")
}

// shipConflictRepair turns a conflicted ship into the repair path.
//
// The useful output of a blocked ship is not an error message to whoever typed
// it — it is a job for the agent standing in the workspace, which is the same
// conclusion `awp workspace repair` reaches about a PR. Prompt text comes from
// internal/ship, beside the steps that produce the conflict.
//
// Still an error afterwards. The ship did not happen, and a command that
// arranged for someone else to finish the job has not done the job.
func (a *App) shipConflictRepair(projectName, repoRoot, wsName, wsPath string, target ship.Target) error {
	prompt := ship.ConflictPrompt(target)
	item := deckui.Item{ProjectName: projectName, WorkspaceName: wsName, Path: wsPath, RepoRoot: repoRoot}
	send := agentPromptSender(nil, a.runner, tmux.New(a.runner), a.shipService(repoRoot))
	if err := send(item, prompt, nil); err != nil {
		// The conflicts matter more than the delivery failure, so say both
		// rather than reporting only that a message could not be sent.
		return fmt.Errorf("ship: rebasing %s onto %s left conflicts, %s was not moved, and the prompt asking the agent to resolve them could not be sent (%v). Resolve them by hand, then run `awp ship` again", target.Revision, target.Trunk, target.Trunk, err)
	}
	return fmt.Errorf("ship: rebasing %s onto %s left conflicts, so %s was not moved and nothing was shipped — sent %s/%s's agent a prompt to resolve them", target.Revision, target.Trunk, target.Trunk, projectName, wsName)
}

// shipService is the workspace service for a named repo root.
//
// a.svc is deliberately not used. It is the ambient service, built from `jj
// root`, and inside a jj workspace that is the workspace's own directory rather
// than the repo it belongs to — so every path it derives is one level too deep.
// A test injects a service to keep this substitutable.
func (a *App) shipService(repoRoot string) workspace.Service {
	if a.shipSvc != nil {
		return a.shipSvc
	}
	return newDeckActionServiceWithIO(a.runner, repoRoot, a.in, a.out)
}

// shipLoop is the repo's dev loop, or an empty one when the repo has no
// dev_loop configured.
//
// An unconfigured repo has no gates, so it has no red ones and ship's gate
// check passes vacuously. That is the honest answer: awp cannot check what the
// repo never described, and refusing to ship on the grounds that a repo has no
// gate config would make the verb unavailable to every repo that has not opted
// into the dev loop.
func shipLoop(cfg config.Config) watch.Loop {
	if !watch.IsConfigured(cfg) {
		return watch.Loop{}
	}
	return watch.Resolve(cfg)
}
