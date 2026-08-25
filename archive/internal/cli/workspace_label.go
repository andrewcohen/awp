package cli

import (
	"fmt"
	"strings"
)

// `awp workspace label` — what a row should be called, when its name is wrong for
// reading.
//
// A workspace's name is three things at once: the directory on disk, the zmx session
// name, and usually the bookmark. So whoever creates one has to pick a single slug
// that satisfies a filesystem and a human at the same time, and those constraints
// disagree — `fix-badge-refresh-2` is a fine directory and a poor sentence. A person
// splits the difference by habit. An agent asked to spawn a workspace has no habit,
// and will produce either a slug nobody can read or a label no filesystem wants.
//
// Hence a second field, and hence the discipline around it: the label is presentation
// only. Nothing resolves anything from it — see workspace.Entry.DisplayName, and
// display_name_test.go, which enumerates the files allowed to mention it so that the
// rule survives the next person who needs a name from somewhere.
//
// Distinct from rename, which moves a directory and a session and should stay hard.
// Labelling is free and reversible, and renaming a labelled workspace keeps its
// label: a rename changes what the work is *called*, not what it *is*.

const workspaceLabelUsage = `Usage: awp w label [--project <name|path>] <workspace> [text...]

Sets what the deck shows for this workspace instead of its name. With no text, clears
the label and the row goes back to its name.

  --project <name|path>   which project the workspace is in. Omit it to use the repo
                          you are standing in.

The label changes nothing but what you read: the directory, the session and the
bookmark keep the workspace's name. Renaming keeps the label.`

// runWorkspaceLabel implements `awp workspace label`.
func (a *App) runWorkspaceLabel(args []string) error {
	if isHelpArgSlice(args) {
		_, _ = fmt.Fprintln(a.out, workspaceLabelUsage)
		return nil
	}

	project, rest, err := takeProjectFlag(args)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		return fmt.Errorf("workspace label: give a workspace (try: awp w label --project <name> <workspace> 'what it is')")
	}
	name := strings.TrimSpace(rest[0])
	label := strings.TrimSpace(strings.Join(rest[1:], " "))

	svc, projectName, _, err := a.sendTarget(project)
	if err != nil {
		return err
	}
	// Resolved first so a typo names the workspaces that exist rather than writing a
	// label into a state file for a workspace nobody has.
	entry, err := workspaceEntry(svc, projectName, name)
	if err != nil {
		return err
	}
	if err := svc.SetDisplayName(entry.Name, label); err != nil {
		return fmt.Errorf("label %s in %s: %w", entry.Name, projectName, err)
	}
	if label == "" {
		_, _ = fmt.Fprintf(a.out, "cleared the label on %s/%s — it reads as %q again\n", projectName, entry.Name, entry.Name)
		return nil
	}
	_, _ = fmt.Fprintf(a.out, "%s/%s reads as %q\n", projectName, entry.Name, label)
	return nil
}
