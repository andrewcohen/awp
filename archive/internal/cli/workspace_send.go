package cli

import (
	"fmt"
	"strings"

	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/tmux"
	"github.com/andrewcohen/awp/internal/workspace"
)

// `awp workspace send` — say something to a workspace's agent from outside it.
//
// The delivery itself is not new: agentPromptSender already decides which
// substrate holds the agent, and every surface that talks to one goes through it
// (the deck's `A`, the diff's ctrl+s, the review flow's opening prompt). What was
// missing is a way in from a shell. Until now instructing an agent meant being in
// the deck with the right row selected, which the captain — with no deck and no
// cursor — cannot be.
//
// So this is a fourth caller of one existing decision rather than a fourth way to
// send a prompt. The thing that must not happen here is a second path that talks
// to tmux directly: #201 and #219 were each one call site doing that, and the
// symptom both times was an invisible second agent holding the same workspace.

const workspaceSendUsage = `Usage: awp w send [--project <name|path>] <workspace> <text...>

Sends text to the workspace's agent, as if you had typed it at the agent.

  --project <name|path>   which project the workspace is in. A project name as the
                          deck's ` + "`o`" + ` picker lists it, or a path to a repo. Omit it
                          to use the repo you are standing in.

The agent has to be running: this will not start one, because starting one is how
you end up with two. Reports the project and workspace it sent to, so a wrong
target is visible rather than silent.`

// runWorkspaceSend implements `awp workspace send`.
func (a *App) runWorkspaceSend(args []string) error {
	if isHelpArgSlice(args) {
		_, _ = fmt.Fprintln(a.out, workspaceSendUsage)
		return nil
	}

	project, rest, err := takeProjectFlag(args)
	if err != nil {
		return err
	}
	if len(rest) < 2 {
		return fmt.Errorf("workspace send: give a workspace and something to say (try: awp w send --project <name> <workspace> 'text')")
	}
	name := strings.TrimSpace(rest[0])
	text := strings.TrimSpace(strings.Join(rest[1:], " "))
	if text == "" {
		return fmt.Errorf("workspace send: nothing to say to %q", name)
	}

	svc, projectName, repoRoot, err := a.sendTarget(project)
	if err != nil {
		return err
	}
	item, err := resolveWorkspaceItem(svc, projectName, repoRoot, name)
	if err != nil {
		return err
	}

	// panes nil: this is a shell, not a deck, so there is no host object to ask.
	// agentPromptSender's second branch is written for exactly this caller — it
	// asks zmx whether an agent is live and only reaches tmux when nothing there
	// claims the workspace.
	send := agentPromptSender(nil, a.runner, tmux.New(a.runner), svc)
	if err := send(item, text, nil); err != nil {
		return err
	}

	// Naming the target is the safety property, not decoration. `--project` is
	// optional so a person standing in a repo need not repeat themselves, which
	// means a wrong answer is possible; saying which project and workspace it went
	// to makes that wrong answer visible at once instead of after the agent acts
	// on it.
	_, _ = fmt.Fprintf(a.out, "sent to %s/%s\n", item.ProjectName, item.WorkspaceName)
	return nil
}

// sendTarget resolves the service, project name and repo root to act in.
//
// With --project it goes through resolveProjectRoot, which consults no cwd at all.
// Without it, the ambient service — the same resolution `awp w list`, `info` and
// `rename` have always used, so a person in a repo is not made to say where they
// are standing for one verb out of four.
//
// That asymmetry is deliberate and is the reason the caller prints what it acted
// on. The failure this is guarding against is addressing the wrong repository
// silently; refusing to resolve is one way to prevent it, and saying out loud
// which one you resolved to is the other. An agent is told, by its preamble, to
// always pass --project — for it there is no cwd to fall back to and the resolver
// refuses an empty project outright.
func (a *App) sendTarget(project string) (workspace.Service, string, string, error) {
	if strings.TrimSpace(project) == "" {
		root, err := a.ambientRepoRoot()
		if err != nil {
			return nil, "", "", err
		}
		return a.svc, projectNameFor(root), root, nil
	}
	svc, root, err := a.serviceForProject(project)
	if err != nil {
		return nil, "", "", err
	}
	return svc, projectNameFor(root), root, nil
}

// resolveWorkspaceItem turns a workspace name into the row-shaped value the
// prompt sender wants, or says which names exist.
//
// It reads the service's own list rather than trusting the name: the sender needs
// the working copy's path, and a name that is not there at all is worth catching
// here — where the list is in hand and can be printed — rather than as a failure
// to find a session later.
func resolveWorkspaceItem(svc workspace.Service, projectName, repoRoot, name string) (deckui.Item, error) {
	entries, err := svc.List()
	if err != nil {
		return deckui.Item{}, fmt.Errorf("list workspaces in %s: %w", projectName, err)
	}
	var known []string
	for _, e := range entries {
		if e.Name == name {
			return deckui.Item{
				ProjectName:   projectName,
				WorkspaceName: e.Name,
				Path:          e.Path,
				RepoRoot:      repoRoot,
			}, nil
		}
		known = append(known, e.Name)
	}
	if len(known) == 0 {
		return deckui.Item{}, fmt.Errorf("no workspace %q in %s, which has none yet", name, projectName)
	}
	return deckui.Item{}, fmt.Errorf("no workspace %q in %s; known: %s", name, projectName, strings.Join(known, ", "))
}
