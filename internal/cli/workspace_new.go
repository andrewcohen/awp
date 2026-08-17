package cli

import (
	"fmt"
	"strings"
)

// `awp workspace new` — create a workspace without being taken to it.
//
// `awp workspace open` has always been create-or-focus: it prepares the workspace
// and then ends by switching your tmux client into it. That is right for a person
// typing it, and wrong for anything acting on your behalf. The captain has no
// client to switch, and a create that tried to switch one would either fail or —
// worse — start a tmux session and an agent in it that nothing on your deck can
// see. #219 is that defect, from the deck's own create path.
//
// What makes this cheap is that the split already exists. openRequest.PaneHosted
// was added for #219: it means "the caller hosts this workspace's processes, so
// prepare it and stop". A create from a shell is the same situation as a create
// from a pane-hosting deck — nobody here is attaching to a tmux window — so this
// verb is that flag with a command-line in front of it rather than a second create
// path to keep in step with the first.
//
// With a prompt, the agent is started detached (see startHostedAgent, #269): a
// create carrying work is a request for the work to be under way, not for it to be
// waiting behind the next time somebody opens a pane. Parking is the fallback and
// is a complete one.

const workspaceNewUsage = `Usage: awp w new [--project <name|path>] [--prompt <text>] [--label <text>] [--bookmark <name>] <workspace>

Creates the workspace and returns. It does not switch you into it, and it starts no
tmux session — the agent runs where the deck hosts it.

  --project <name|path>   which project to create it in. A project name as the deck's
                          ` + "`o`" + ` picker lists it, or a path to a repo. Omit it to use the
                          repo you are standing in.
  --prompt <text>         start the agent on this. Without it the workspace is created
                          with no agent running, and the first pane starts one.
  --label <text>          what the deck should show for it instead of its name. The
                          name still has to be a directory; the label does not.
  --bookmark <name>       start the workspace at an existing bookmark, rather than at
                          the current working copy.

Reports the project and workspace it created, so a wrong target is visible rather
than silent.`

// runWorkspaceNew implements `awp workspace new`.
func (a *App) runWorkspaceNew(args []string) error {
	if isHelpArgSlice(args) {
		_, _ = fmt.Fprintln(a.out, workspaceNewUsage)
		return nil
	}

	project, rest, err := takeProjectFlag(args)
	if err != nil {
		return err
	}
	prompt, rest, err := takeValueFlag(rest, "--prompt")
	if err != nil {
		return err
	}
	bookmark, rest, err := takeValueFlag(rest, "--bookmark")
	if err != nil {
		return err
	}
	label, rest, err := takeValueFlag(rest, "--label")
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		return fmt.Errorf("workspace new: give the workspace a name (try: awp w new --project <name> <workspace>)")
	}
	if len(rest) > 1 {
		return fmt.Errorf("workspace new: one workspace name, got %d (%s) — quote a name with spaces in it", len(rest), strings.Join(rest, " "))
	}
	name := strings.TrimSpace(rest[0])
	if name == "" {
		return fmt.Errorf("workspace new: give the workspace a name")
	}

	svc, projectName, repoRoot, err := a.sendTarget(project)
	if err != nil {
		return err
	}

	// Pinned to the resolved root, like the service is. openWorkspaceWithReporter
	// builds its own jj client from this runner, so handing it the ambient one
	// would prepare the workspace in the project the caller named while running jj
	// against wherever the process started.
	runner := Runner(fixedDirRunner{base: a.runner, dir: repoRoot})

	if err := openWorkspaceInDeckMode(runner, svc, newWorkspaceRequest(name, bookmark, prompt)); err != nil {
		return fmt.Errorf("create %s in %s: %w", name, projectName, err)
	}

	// After the create, not before: there is no entry to label until the workspace
	// exists. Best-effort and reported rather than fatal — the workspace is made and
	// its agent may already be working, so failing the whole command over the text
	// on a row would be the tail wagging the dog.
	if label = strings.TrimSpace(label); label != "" {
		if err := svc.SetDisplayName(name, label); err != nil {
			_, _ = fmt.Fprintf(a.out, "created %s/%s, but could not label it (%v) — set it with `awp w label`\n", projectName, name, err)
		}
	}

	if strings.TrimSpace(prompt) != "" {
		_, _ = fmt.Fprintf(a.out, "created %s/%s, agent started on the prompt\n", projectName, name)
		return nil
	}
	_, _ = fmt.Fprintf(a.out, "created %s/%s, no agent running yet\n", projectName, name)
	return nil
}

// newWorkspaceRequest is what this verb asks the create path for.
//
// Its own function so the two flags that matter can be checked directly. Both are
// the sort that break silently:
//
// PaneHosted false would create the workspace and then start a tmux session with a
// coding agent in it — the #219 defect, where the workspace ends up with two agents
// and the invisible one is holding the prompt. Nothing about the output would say
// so.
//
// Yes false would ask "workspace does not exist, create it?" on a stdin that, for
// an agent, nobody is typing at. The caller hangs rather than fails, which is the
// worse of the two.
func newWorkspaceRequest(name, bookmark, prompt string) openRequest {
	return openRequest{
		Name:       strings.TrimSpace(name),
		Bookmark:   strings.TrimSpace(bookmark),
		Prompt:     strings.TrimSpace(prompt),
		Yes:        true,
		PaneHosted: true,
	}
}

// takeValueFlag pulls `--name <value>` (or `--name=<value>`) out of args.
//
// The same shape as takeProjectFlag, and the reason that one is not generalised
// into this: --project resolves against configured roots and has an opinion about
// being absent, where these are plain strings. Sharing the parse but not the policy
// would put the policy somewhere less obvious than the flag it belongs to.
func takeValueFlag(args []string, flag string) (value string, rest []string, err error) {
	rest = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == flag:
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("%s needs a value", flag)
			}
			value = args[i+1]
			i++
		case strings.HasPrefix(arg, flag+"="):
			value = strings.TrimPrefix(arg, flag+"=")
		default:
			rest = append(rest, arg)
		}
	}
	return value, rest, nil
}
