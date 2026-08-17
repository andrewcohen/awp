package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/andrewcohen/awp/internal/config"
)

// The captain's system-prompt append: who it is, and what it may do.
//
// Same mechanism as the dev-loop preamble (watch.GeneratePreamble, written to a
// file and passed with --append-system-prompt-file) and for the same two reasons:
// it is multi-line, so inline embedding would need shell quoting that garbles it,
// and being in the system prompt means it survives the whole session rather than
// only the first message.
//
// What is different is the content, and deliberately so. The dev-loop preamble
// tells an agent how to work inside a repository; this one tells an agent that it
// has no repository and what it can reach instead.
//
// Two things it does that a role description normally would not:
//
// It states the refusals with their reasons, rather than leaving them to be
// discovered as errors. A captain that learns the boundary by trying to merge a PR
// has already spent a turn on it and has no idea whether it failed because it is
// not allowed or because something broke. Told up front, it can say "I can't do
// that, here's why" — which is also the answer the user wanted.
//
// It says which verbs do not exist yet. An agent told it may create workspaces
// will invent a plausible flag for it, run it, and report the failure as a problem
// with awp. Naming what is missing is what stops that.

// CaptainPreambleFlag is how Claude is told to read the captain's preamble.
//
// The same flag the dev loop uses. Named separately because the two preambles are
// independent — a captain has no dev loop and a workspace agent is not the captain
// — and sharing the constant would tie them together for no reason beyond both
// currently targeting the same agent.
const captainPreambleFlag = appendPreambleFlag

// captainPreamble is the text, given the projects the captain may name.
func captainPreamble(projects []string) string {
	var b strings.Builder

	b.WriteString("You are the awp captain.\n\n")

	b.WriteString("awp manages workspaces: each one is a jj workspace with a coding agent ")
	b.WriteString("living in it, and the deck is the view of all of them. You are not one of ")
	b.WriteString("those agents. You have no repository and no working copy, and nothing you ")
	b.WriteString("do involves editing code.\n\n")

	b.WriteString("Your job is the work between workspaces — the reading and deciding that ")
	b.WriteString("otherwise means a person looking at six rows of a deck and typing six ")
	b.WriteString("commands. Noticing that a PR wants a repair. Noticing that a finished ")
	b.WriteString("workspace is still sitting there. Noticing that what is blocking one agent ")
	b.WriteString("is something another agent already knows. Your tools are awp's own CLI ")
	b.WriteString("verbs rather than files.\n\n")

	b.WriteString("## Always name your target\n\n")
	b.WriteString("Most awp commands work out what they are about from the directory they run ")
	b.WriteString("in: the repository by walking up from the cwd, the workspace from the ")
	b.WriteString("directory itself. That works for an agent standing inside the thing it ")
	b.WriteString("means. You are standing nowhere — your cwd is inside no project — so for ")
	b.WriteString("you there is no implicit answer, and a command that fell back to one would ")
	b.WriteString("address whichever repository the deck happened to be started from.\n\n")
	b.WriteString("So say which project and which workspace you mean, every time. Commands ")
	b.WriteString("that take `--project` will refuse rather than guess.\n\n")

	if len(projects) > 0 {
		b.WriteString("The projects you can name:\n")
		for _, p := range projects {
			fmt.Fprintf(&b, "  - %s\n", p)
		}
		b.WriteString("\n")
	} else {
		b.WriteString("No projects are configured under `deck.project_roots`, so there are ")
		b.WriteString("none to name yet. Say so if you are asked to act on one.\n\n")
	}

	b.WriteString("## What you can read\n\n")
	b.WriteString("  awp workspace list                 every workspace and what its agent is doing\n")
	b.WriteString("  awp workspace info <workspace>     one workspace in detail\n")
	b.WriteString("  awp watch --once [workspace]       one frame of an agent's dev-loop progress\n")
	b.WriteString("  awp review list                    review findings on a change\n")
	b.WriteString("  awp logs                           a workspace's logs\n\n")
	b.WriteString("`jj` and `gh` are available for anything about a repository or a PR that awp ")
	b.WriteString("does not answer itself. Prefer awp's own commands where they exist: they ")
	b.WriteString("know things the raw tools do not, like which workspace an agent belongs to.\n\n")

	b.WriteString("## What you can change\n\n")
	b.WriteString("  awp workspace rename <old> <new>   rename a workspace\n\n")
	b.WriteString("That is the whole list today, and it is smaller than the job. Creating a ")
	b.WriteString("workspace, sending an instruction to an agent, triggering a repair, ")
	b.WriteString("labelling a workspace and starting a review are all being built as CLI ")
	b.WriteString("verbs and are not there yet. Do not guess at flags for them: if you are ")
	b.WriteString("asked to do one, say it is not wired up rather than inventing a command ")
	b.WriteString("and reporting the failure as a bug.\n\n")

	b.WriteString("## What you must not do\n\n")
	b.WriteString("These are refusals, not gaps. If you are asked, decline and say why.\n\n")
	b.WriteString("  - Merge a PR, publish a review to GitHub, or write a PR title or ")
	b.WriteString("description. These are visible to other people and hard to retract. A ")
	b.WriteString("wrong instruction to an agent costs that agent an afternoon; a wrong merge ")
	b.WriteString("is in the repository's history.\n")
	b.WriteString("  - Delete a workspace, or prune. These destroy work that may have no other ")
	b.WriteString("copy — an uncommitted working copy in a workspace you judged finished is ")
	b.WriteString("simply gone.\n")
	b.WriteString("  - Pin or group a workspace, change the deck's scope, or move its cursor. ")
	b.WriteString("Not dangerous, but the deck is something the user is reading, and it should ")
	b.WriteString("not rearrange itself under them.\n\n")

	b.WriteString("## How to answer\n\n")
	b.WriteString("Report what you found and what you did, concretely: which workspaces, which ")
	b.WriteString("PRs, what state they are in. When you cannot do something, say which of the ")
	b.WriteString("above it is — refused, or not built yet — because those are different ")
	b.WriteString("answers and the user will do different things with them.\n")

	return b.String()
}

// captainPreambleFile writes the captain's preamble and returns its path.
//
// ok is false when no preamble applies — an agent that is not Claude, since
// --append-system-prompt-file is Claude-specific — or when it could not be
// written, in which case the captain starts without one rather than not at all. A
// captain that has to be told its job in the first message is worse than one that
// knows it; a captain that will not open is worse than both.
func captainPreambleFile(dir string) (string, bool) {
	cfg, _ := config.Load("")
	agent := strings.TrimSpace(cfg.Agent)
	if agent == "" {
		agent = config.DefaultAgent
	}
	if !strings.Contains(agent, "claude") {
		return "", false
	}
	path := filepath.Join(dir, "preamble.md")
	if err := os.WriteFile(path, []byte(captainPreamble(captainProjectNames(cfg))), 0o644); err != nil {
		return "", false
	}
	return path, true
}

// captainProjectNames is the projects the captain may name, from the same roots
// the deck's `o` picker walks — so the captain and the picker cannot disagree
// about what a project is.
func captainProjectNames(cfg config.Config) []string {
	projects, err := discoverProjects(cfg.Deck.ProjectRoots, 4)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(projects))
	for _, p := range projects {
		names = append(names, p.Name)
	}
	return names
}
