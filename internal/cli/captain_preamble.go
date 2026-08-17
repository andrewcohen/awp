package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/andrewcohen/awp/internal/config"
	"github.com/andrewcohen/awp/internal/state"
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
		b.WriteString("The projects with work in them:\n")
		for _, p := range projects {
			fmt.Fprintf(&b, "  - %s\n", p)
		}
		b.WriteString("\nThat is where the workspaces are, not everything on the machine. If you ")
		b.WriteString("are asked about a project not on this list, you can still name it — pass ")
		b.WriteString("its name, or a path to it — but do not go looking for one.\n\n")
	} else {
		b.WriteString("There are no workspaces yet, in any project, so there is nothing to ")
		b.WriteString("report on. Creating the first one is still something you can do, given a ")
		b.WriteString("project name or a path.\n\n")
	}

	b.WriteString("## What you can read\n\n")
	b.WriteString("  awp workspace attention            what wants attention, most urgent first, and why\n")
	b.WriteString("  awp workspace list                 every workspace and what its agent is doing\n")
	b.WriteString("  awp workspace info <workspace>     one workspace in detail\n")
	b.WriteString("  awp watch --once [workspace]       one frame of an agent's dev-loop progress\n")
	b.WriteString("  awp review list                    review findings on a change\n")
	b.WriteString("  awp logs                           a workspace's logs\n\n")
	b.WriteString("Start with `awp workspace attention`. It is awp's own answer to \"what should I ")
	b.WriteString("look at\", assembled from things the raw tools cannot see — an agent's reported ")
	b.WriteString("state, its unread mark, whether its session is live — and it is the same list, in ")
	b.WriteString("the same order, that the user's own deck shows them. `--json` if you would rather ")
	b.WriteString("parse it.\n\n")
	b.WriteString("`jj` and `gh` are available for anything about a repository or a PR that awp ")
	b.WriteString("does not answer itself. Prefer awp's own commands where they exist: they ")
	b.WriteString("know things the raw tools do not, like which workspace an agent belongs to.\n\n")

	b.WriteString("## What you can change\n\n")
	b.WriteString("  awp workspace new --project <p> <name> [--prompt <text>] [--label <text>]\n")
	b.WriteString("      Creates a workspace and returns. With --prompt its agent starts on that\n")
	b.WriteString("      prompt; without one, nothing is running in it yet. --label is what the deck\n")
	b.WriteString("      shows for it: the name has to work as a directory, the label does not, so\n")
	b.WriteString("      give the name a slug and the label a sentence.\n")
	b.WriteString("  awp workspace send --project <p> <workspace> '<text>'\n")
	b.WriteString("      Says something to that workspace's agent, as if it had been typed at it.\n")
	b.WriteString("      The agent has to be running.\n")
	b.WriteString("  awp workspace repair --project <p> <workspace> [--dry-run]\n")
	b.WriteString("      Tells the agent what is wrong with its PR — conflicts, failing CI, an\n")
	b.WriteString("      out-of-date base, review feedback. Whose PR it is decides whether the agent\n")
	b.WriteString("      is asked to fix it or to investigate and report.\n")
	b.WriteString("  awp workspace label --project <p> <workspace> [text]\n")
	b.WriteString("      Sets what the deck shows for it. No text clears it.\n")
	b.WriteString("  awp review <pr#> --project <p> --no-attach\n")
	b.WriteString("      Starts a review of that PR: a workspace at its head ref with a reviewing\n")
	b.WriteString("      agent reading it. --no-attach is required for you — without it the command\n")
	b.WriteString("      wants a terminal to put someone in.\n")
	b.WriteString("  awp workspace rename <old> <new>\n")
	b.WriteString("      Renames a workspace, which moves its directory and its session. Prefer\n")
	b.WriteString("      `label` when what you want is for a row to read better.\n\n")
	b.WriteString("Those verbs are complete, so if one fails it is a real failure and worth ")
	b.WriteString("reporting. What is still missing is a way for another agent to answer you: ")
	b.WriteString("`send` is one-way, so ask a question only when the user is there to relay the ")
	b.WriteString("reply, and otherwise prefer reading an agent's progress with `awp watch --once`.\n\n")

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
	b.WriteString("PRs, what state they are in. When you cannot do something, say which kind of ")
	b.WriteString("cannot it is: refused, because it is on the list above; blocked, because ")
	b.WriteString("something about the workspace is in the way; or failed, because the command ")
	b.WriteString("errored. Those are three different answers and the user will do three ")
	b.WriteString("different things with them.\n")

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
	if err := os.WriteFile(path, []byte(captainPreamble(captainProjectNames())), 0o644); err != nil {
		return "", false
	}
	return path, true
}

// captainProjectNames is the projects awp actually has workspaces in.
//
// Deliberately not discoverProjects, which walks deck.project_roots and finds every
// repository under them. That is the right list for the deck's `o` picker, whose whole
// job is opening a project awp does not know about yet; it is the wrong list here. On
// a real machine it came to 56 entries, including a notes vault and some advent-of-code
// solutions — repos the captain will never be asked about, spending its system prompt
// and inviting it to reason about work that does not exist.
//
// What the captain is *told* about and what it can *address* are two questions, and
// only this one narrows. resolveProjectRoot still accepts any name under the roots, or
// a path, so the captain can be pointed at a project that is not on the deck — it just
// will not go looking for one.
//
// Reads the store the deck reads. A read that fails yields no names rather than an
// error: a captain that knows its verbs but not its projects can still be told one,
// where a captain that would not start is no use at all.
func captainProjectNames() []string {
	byRepo, err := state.NewJSONStore().LoadAll()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	names := make([]string, 0, len(byRepo))
	for root, entries := range byRepo {
		// A repo with no workspaces left is not somewhere work is happening. The
		// store keeps such a bucket around after the last delete, so counting it
		// would put an empty project on the list for as long as the file lives.
		if len(entries) == 0 {
			continue
		}
		name := projectNameFor(root)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
