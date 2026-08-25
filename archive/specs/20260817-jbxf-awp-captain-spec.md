# AWP Captain

## Metadata
- **Spec ID**: `20260817-jbxf`
- **Feature name**: AWP Captain — an agent that drives awp, and a message log it talks through
- **Owner**: andrewcohen
- **Status**: Discovery
- **Last updated**: 2026-08-17

## Goal

One agent whose workspace is awp itself. It creates workspaces, triggers
repairs, asks the other agents questions and reads their answers — all through
awp's own CLI, and all of it written down in a message log you can read.

## User Problem

Every workspace has an agent doing the work inside it. Nobody is doing the work
*between* them. Deciding that a PR needs a repair, that a finished workspace
should be deleted, that the thing blocking one agent is a question another agent
can answer — that is a person reading six rows of a deck and typing six
commands.

The deck already shows the state. What is missing is something that can act on
it. The captain is that: an agent with no repository, whose tools are `awp`
subcommands rather than files, and whose output is instructions to the
workspaces that do have repositories.

The second half of the problem is that agent-to-agent messages currently do not
exist as a thing. `A` sends a prompt into a zmx session and it is gone —
delivered into a terminal, unrecorded, and with no way for the recipient to
answer. A captain that can only speak into the void cannot ask a question, so
the message log is not a nice-to-have beside the captain; it is what makes the
captain able to do its job.

## Scope

### In scope (v1)
- The captain: a singleton workspace with no repository, view-only, hosting one
  agent. Excluded from attention scopes, PR machinery and every flow that
  assumes a working copy.
- `a` summons it from anywhere in the deck, as a single full-screen agent pane.
  The agent-window binding `a` currently carries is deleted; `enter` already
  summons a workspace's agent.
- A captain system-prompt append: its role, that it has no repository, and the
  `awp` verbs it has.
- **A display name on a workspace**, separate from its name — so a workspace the
  captain spawned can read as "fix the stale PR badge" rather than as the slug
  that also had to be a directory name.

The captain talks to workspace agents through the prompt-send path that already
exists (`internal/cli/prompt_sender.go`, what `A` uses). Unrecorded, one-way, no
reply channel — which is the whole reason the message log is v2 rather than cut.

### Out of scope (v1)
- **The message log.** No `internal/messages`, no `awp message` verbs, no
  recorded exchange, no reply channel. See *Deferred: the message log* — it is
  the next thing, not a discarded one.
- **A row in the deck.** The captain is a place you go, not a row you select.
  `a` reaches it wherever the cursor is, so there is nothing to render in the
  list and nothing to exclude from it beyond the guards above.
- **A messages pane, and therefore a split.** With no log to tail, the captain
  is one pane.
- **Captain → user surfacing.** No status-bar message, no badge. v1 ships with
  the captain unable to interrupt you.
- Scheduling, autonomy loops, or the captain acting without being asked. v1 is a
  captain you talk to, not a daemon.

### Deferred: the message log

Kept here because v1 is deliberately missing it, and the shape is already
decided — this is the design, parked, not an open question.

- `internal/messages`: an append-only JSONL log under `~/.awp/messages.jsonl`,
  one record per communication (id, at, from, to, body, optional reply-to),
  appended under the advisory-lock discipline `claude_trust.go` uses — several
  agents will write concurrently.
- `awp message send` / `list` (with `--follow`). `--from` resolves from cwd, the
  way `awp report-status` and the hooks already do, so an agent replying types
  the line it was handed and nothing else.
- Delivery appends the reply line to the delivered text, generated from the
  record rather than composed by the sender:

  ```
  To reply: awp message send --to captain --reply-to m7k2 '<your answer>'
  ```

  An agent should never have to be told how to be reachable; that is the
  transport's job.
- A `messages` pane kind (ephemeral, `awp message list --follow`), which is what
  makes the captain a split rather than a single pane.
- `A` in the deck becomes a message send, so the deck's typed prompts are
  recorded too.

Not the review store. A review remark is anchored to a line of a diff and has a
publish lifecycle; a message has neither, and forcing one into the other would
make both worse.

## Control surfaces

What the captain is allowed to do, and what it is not. This section is also the
contents of its system-prompt preamble — the captain knows what it can do because
this list is handed to it.

### The rule about targets

**Every command the captain runs names its target explicitly.** It may never rely
on the implicit one.

Almost everything in awp resolves its subject from the process's cwd: the repo
via `j.RepoRoot()` walking upward, the workspace from the directory you are
standing in. `awp watch --repo PATH` exists because that assumption already
needed one escape hatch. The captain has no repository and its cwd is inside no
project, so for it the implicit answer is not merely unhelpful, it is absent —
and a verb that silently resolves to "wherever the process started" would
address whatever repo the deck was launched from.

So the captain's verbs take a project and a workspace as arguments, and the
projects it may name come from `deck.project_roots` in config — the same list the
deck's `o` picker offers.

### Allowed: read

| Command | Gives it |
|---|---|
| `awp workspace attention [--json]` | which rows want you, and why — ✅ built |
| `awp workspace list` | the roster: every workspace, per project, with agent state |
| `awp workspace info <ws>` | one workspace in detail |
| `awp watch --once [ws]` | one frame of an agent's dev-loop progress, printed, exits |
| `awp review list` | findings on a change |
| `awp logs` | a workspace's logs |
| *PR / CI state* | the pr-status cache — still not exposed on `info`, though `repair` reads it |

Attention is the read worth exposing properly. Every other read the captain
could approximate by shelling out to `jj` and `gh`; attention it cannot, because
that predicate is awp's own opinion about what matters, and it is the reason a
captain beats a shell script.

### Allowed: write, inward

Acting on awp's own workspaces and the agents inside them.

| Command | State |
|---|---|
| `awp workspace new --project <p> <name> [--prompt …] [--label …] [--bookmark …]` | ✅ built. Creates and returns; `openRequest.PaneHosted` is the half of `open` that does not attach. |
| `awp workspace send --project <p> <ws> <text>` | ✅ built. A fourth caller of `agentPromptSender`, not a fourth way to send. |
| `awp workspace repair --project <p> <ws> [--dry-run]` | ✅ built, on `deckui.PRRepairPrompt` — the deck's own prompt, so #171's author/reviewer split survives. |
| `awp workspace label --project <p> <ws> [text]` | ✅ built, with `Entry.DisplayName` and its allow-list guard. |
| `awp review <pr#> --project <p> --no-attach` | ✅ built. The picker is refused rather than opened for a caller that cannot answer it. |
| `awp workspace rename` | exists |
| `awp internal mark-read` | exists |

### Refused

Not "unimplemented" — refused, with the reason, because each is a different kind
of risk and they were each decided separately:

- **Merging a PR, publishing a review, writing a PR description.** The only
  category where a mistake is visible to other people and hard to retract. A
  wrong instruction to an agent costs one agent's afternoon; a wrong merge is in
  the repo's history.
- **Deleting a workspace, pruning.** Where a mistake destroys work that has no
  other copy — an uncommitted working copy in a workspace the captain judged
  finished is simply gone.
- **Pinning or grouping a workspace, changing the deck's scope, moving the
  cursor.** Not dangerous, wrong. If the captain can rearrange the deck, the deck
  stops being something you are reading and becomes something being edited under
  you.

The preamble states these as refusals with their reasons rather than omitting
them, so the captain does not discover the boundary by hitting an error.

## UX

### CLI

See *Control surfaces* for the whole set. v1 adds five verbs — `workspace new`,
`send`, `repair`, `label`, and a non-interactive `review` — all of which take
their target explicitly.

### TUI

- `a` from the deck's row mode summons the captain, wherever the cursor is. The
  captain is a place, not a row you navigate to.
- **The pane menu (`ctrl+space`) has a verb for it too**, and this is not a
  convenience duplicate. Inside a pane the keyboard belongs to the hosted
  program, so `a` is that program's `a` — the menu is the *only* door to the
  captain from where you spend most of your time. A hub you can only reach by
  first leaving what you were doing is a hub you will not use.
- It opens as one full-screen agent pane, through the existing pane host.
- `ctrl+\` leaves it, the way it leaves any pane.
- The `?` overlay loses the `a` agent-window row and gains the captain row.

## Discovery Questions

1. **Who is the first user?** Andrew, with ~6 live workspaces across 2 projects.
2. **When?** When the deck says several things need doing and none of them needs
   his judgement — a PR wanting a repair, a finished workspace wanting deletion,
   an agent blocked on something another agent knows.
3. **What exact result?** Workspaces created, labelled, repaired and instructed,
   without him typing the six commands that do it.
4. **Data sources?** The deck's own state, read through the CLI the captain
   calls, and the dev-loop transcript via `awp watch`.
5. **Smallest useful slice?** This whole spec, which is why the message log was
   cut out of it. `a` opening an agent that can drive awp is the slice; a
   recorded, answerable exchange is an improvement to it.
6. **Non-goals?** See *Out of scope*. The one that matters most is captain → user
   surfacing; v1 deliberately ships with the captain unable to interrupt you.
7. **Done?** `a` opens an agent that, asked "what needs doing", reads the deck's
   state and acts on it — spawns a workspace with a readable label, sends a
   repair instruction to another agent — without being told how.

## Status: what shipped

Phases 0–12 are done, and the preamble no longer disclaims any of them. The captain
can read the deck's attention scope, create labelled workspaces with a starting
prompt, instruct an agent, trigger a repair, and start a review — all naming their
target explicitly.

The one gap left is the one the message log was going to close: `send` is one-way, so
no agent can answer the captain. The preamble says so, and tells it to prefer reading
progress with `awp watch --once` over asking a question nobody will answer. That is
the case for building *Deferred: the message log* next, now that the captain exists to
say what a message needs to carry.

Two things raised while building, both parked as their own tasks rather than folded in
here: whether the captain belongs in a ~70% modal over the live deck rather than a
full-screen pane (the pane hides exactly the rows its answers are about), and `A`
becoming "say something to the captain" with the cursor's project prefilled.

## Spec Change Log
- 2026-08-17: Initial draft. Decisions taken at spec time: captain gets `a` and
  the agent-window binding is deleted (`enter` covers it); messages are a new
  store, not the review store; store + captain pane only, no deck surface; no
  captain → user surfacing in v1.
- 2026-08-17: Added a workspace display name separate from its name, so an
  agent-spawned workspace can read as what it is rather than as the slug that had
  to double as a directory name.
- 2026-08-17: **Scope cut — the message log leaves v1**, and the captain no
  longer gets a deck row. The captain instructs other agents through the
  prompt-send path that already exists, so it needs nothing new to be useful, and
  it is one full-screen pane rather than a split. The log's design is parked in
  *Deferred: the message log* rather than deleted: building it first would mean
  designing a record shape against a guessed consumer, where building it second
  means the captain has already shown what a message needs to carry.
- 2026-08-17: Control surfaces decided. Reads and inward writes are allowed,
  including starting a review; merging, publishing, PR descriptions, delete,
  prune, pinning and anything that moves the user's view are refused. Added the
  explicit-target rule as phase 0 — the captain has no repo cwd, so no verb of
  its may resolve its subject implicitly — and five new CLI verbs plus attention
  on the CLI, since most of what the captain needs is deck-only today.

## Implementation Plan

Each phase is independently committable and gate-green on its own.

0. **Explicit targets.** Before any new verb: a shared way for a command to be
   told its project and workspace instead of resolving them from cwd, and an
   actionable error when neither is given. Everything in phases 5–11 depends on
   it, and getting it wrong means a captain command addressing whatever repo the
   deck was launched from. Projects resolve against `deck.project_roots`.
1. **Free `a`.** Delete `AgentWindow` from `deckKeyMap`, its handler, its
   `deckKeyGroups` row and its README row. `enter` is the one way to reach a
   workspace's agent.
2. **The captain.** A singleton with no `RepoRoot` and no `Path`, invisible to
   everything that assumes a working copy: attention banding, PR status, the
   stack/diff scopes, rename, delete. The guard pattern is `deckdata`'s existing
   `Virtual` — a thing that exists without a working copy — but the captain is
   not an inbox row, so it gets its own predicate rather than borrowing that one
   and inheriting inbox behaviour.
3. **The captain's pane.** `a` opens it as one full-screen pane through the
   existing host, and `panePrefixMenu` gains a verb for it — from inside a pane,
   `a` belongs to the hosted program, so the menu is the only way in. Its agent
   argv is neither `codingAgentArgv` (no dev loop — it has no repo to run gates
   in) nor `reviewAgentArgv`; it is a third.
4. **The captain preamble.** `watch.GeneratePreamble`'s shape for a different
   role: who it is, that it has no repository, and the control surfaces it has
   (see *Control surfaces* — that section is the preamble's contents, refusals
   and their reasons included, so the captain does not find the boundary by
   hitting an error). Same `--append-system-prompt-file` mechanism and the same
   reason (multi-line, so a path not an inline string).

The verbs. Each is one unit, and each is useful to a human at a shell before any
captain exists — which is the test of whether it belongs in the CLI at all.

5. **`awp workspace new`.** Creation without attaching. `Service.Open` creates
   *and* focuses, and the deck's form is the only caller; a captain needs the
   first half without the second. Takes `--project`, `--prompt`, `--label`.
6. **`awp workspace send`.** The prompt-send path (`prompt_sender.go`) behind a
   verb, so instructing an agent stops being reachable only from `A`.
7. **`awp workspace repair`.** `deckui.prRepairPrompt` moves somewhere callable
   and the verb sends it. Note it already knows to hand a reviewer a different
   prompt than an author (#171); that distinction has to survive the move.
8. **`awp workspace label`.** The display name (see below).
9. **A non-interactive `awp review`.** `awp review <pr#>` exists and spawns a
   reviewer agent, which the captain is allowed to do — but the flow prompts.
   Needs a form that takes every answer as an argument and refuses rather than
   asking.
10. **Attention on the CLI.** The deck's own attention predicate, printed. This
    is the read the captain cannot approximate with `jj` and `gh`, so it is the
    one that has to be exposed rather than left to it. Reuses
    `internal/deckdata` — the predicate is not re-derived.
11. **A display name, separate from the name.** `Entry.Name` is identity: it is
   the directory on disk, the zmx session name, and usually the bookmark. A
   captain that spawns workspaces therefore has to pick a slug that is
   simultaneously a filesystem name and the label a human reads on a row, and it
   will get one of those two jobs wrong.

   So `Entry.DisplayName` (`omitempty`, falling back to `Name` when unset), and
   it is **presentation only** — the deck row, the sidebar row, the host bar.
   Nothing resolves a path, a session, a bookmark or a PR from it. That
   invariant is only as strong as every call site remembering it, so it gets a
   guard in the shape `internal/github/dir_test.go` uses: a test that fails if
   `DisplayName` is read anywhere outside the render paths.

   Set at create time (`--label` on the create path, so the captain names its
   own work) and afterwards. Distinct from `R` rename, which moves the
   directory and the session and is the operation that *should* stay hard.
   Renaming a workspace does not clear its display name; changing the label is
   free and reversible, which is the point.

12. **README and this spec.** The key table (`a`, `enter`), CLI reference entries
    for the new verbs, and a prose paragraph on what the captain is for and what
    it is not allowed to do.

## Acceptance Criteria
- [ ] Every new verb refuses, with an actionable error, when its project or
      workspace is neither given nor resolvable — and never silently falls back
      to the process's cwd.
- [ ] `awp workspace new --project <p> <name> --prompt …` creates the workspace
      and starts its agent on the prompt, without switching the caller into it.
- [ ] `awp workspace send --project <p> <ws> 'text'` reaches that workspace's
      agent, and errors naming the workspace when it has no agent.
- [ ] `awp workspace repair` sends the author's prompt for your own PR and the
      reviewer's for someone else's (#171 survives the move).
- [ ] A non-interactive `awp review` spawns a reviewer without prompting, and
      refuses rather than asking when an answer is missing.
- [ ] Attention on the CLI agrees with the deck's attention scope, row for row.
- [ ] `a` in the deck opens the captain as one full-screen pane. `ctrl+\` leaves
      it.
- [ ] The pane menu reaches the captain from inside a pane, where `a` cannot.
- [ ] `a` no longer opens a workspace agent window, and the `?` overlay and
      README both say so.
- [ ] The captain row/entry never appears in an attention scope, never gets a PR
      badge, and cannot be renamed or deleted.
- [ ] The captain's agent starts with the captain preamble and without the
      dev-loop preamble.
- [ ] A workspace created with a `--label` shows the label on its deck row, and
      its directory and zmx session still carry its name.
- [ ] A workspace with no label reads exactly as it does today.
- [ ] Renaming a labelled workspace keeps the label.
- [ ] Nothing resolves a path, session, bookmark or PR from a display name, and
      a test fails if a new call site tries.

## QA / Human Review Test Plan

### Setup
- [ ] `jj`, `zmx`, `gh` on PATH; at least two live workspaces in two projects.
- [ ] Build to a temp path (not `go install`) and run that binary.

### Core Happy Path
- [ ] From the deck, `a`. Confirm one full-screen captain pane, and that its
      agent knows what it is without being told.
- [ ] Ask it what needs doing. Confirm it reads awp's own state through the CLI
      rather than guessing from `jj` and `gh`.
- [ ] Have it spawn a labelled workspace in a *named* project, with a prompt.
      Confirm the agent starts on the prompt, the row reads as the label, and
      your own view did not move.
- [ ] Have it instruct that agent, and start a review of a real PR.

### Edge Cases & Failure Modes
- [ ] Every new verb with its project omitted: refuses, naming what it needed.
      Run this from inside a repo, where a cwd fallback would have silently
      worked — that is the case that must still fail.
- [ ] A project not under `deck.project_roots`: refused, saying so.
- [ ] `send` to a workspace with no running agent: actionable error naming the
      workspace.
- [ ] Ask the captain to merge, delete and pin. Confirm it declines and says
      why, rather than trying and hitting an error.

### Regression Checks
- [ ] `enter` still summons a workspace agent; the arrangement you left is still
      remembered (#329, #327).
- [ ] `A` still sends a typed prompt from the deck.
- [ ] The attention scope's counts are unchanged with the captain present, and
      the captain is in no scope.
- [ ] The deck's own new-workspace form still works, now that creation is shared
      with a CLI verb.

### Reviewer Notes
- The captain's pane is visual — confirm it on a real deck before it is called
  done.

## Validation
- [ ] `mise exec -- gofmt -l .`
- [ ] `mise exec -- golangci-lint run ./...`
- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `go build ./...`
