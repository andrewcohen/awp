# packages/jobs

Work that outlives whoever asked for it. Loaded when working under
`packages/jobs/`; repo-wide rules are in the root `AGENTS.md`.

The measurements and failures behind these rules are in `docs/jobs.md`, which no
session loads.

## Resume and compensation are the same disagreement

A job is a named kind with ordered steps. Every mistake available here is a
mistake about which of these two applies:

```
  attempt fails, attempts remain  →  queued, sleep the backoff, run again from
                                     the first step not in `done`. Nothing undone
  attempts exhausted, or cancel   →  walk `done` backwards running each `undo`,
                                     emptying `done` as each one succeeds
```

- **`run` must be safe to call twice** — `mkdir -p`, not `mkdir` — because the
  first branch re-enters the step that failed.
- **A retry after a rollback starts from nothing**, because `done` was emptied.
- **Compensation stops at the first `undo` that fails** and marks the job
  `cleanup: "dirty"`. Each undo assumes the ones after it ran, so once one has
  not, the rest are undoing a state that never existed. `dirty` is the only
  outcome the package cannot fix by itself — a field, not a log line.
- **A step that throws fails the job.** A defect is not on the error channel, so
  it used to sail past the `Effect.result` wrapping an attempt and leave the
  record `running` with nothing behind it.

**Interruption is two different events**, and nothing about the interrupt tells
them apart. `cancel` records the intent in a set before interrupting; the exit
handler reads it. Shutdown therefore leaves the record `running` with `done`
intact, and the next start resumes. Backwards, this silently undoes work meant
to carry on — and the record looks tidy either way, so `runner.test.ts` asserts
it on the trace and on a second runner over the same store.

## A step's `run` has no requirements, and may patch its own input

`JobStep.run` returns `Effect<void | Partial<Input>, JobError>`. A step that
_discovers_ something later steps need returns a patch, and the runner merges it
into the stored input **with the same write that marks the step done**. There is
nowhere else to put it: a job resumed by a restarted daemon has only its record.

It is not a channel between steps, and not an escape from `run` being safe
twice — a step whose patch is already there must notice and do nothing, or a
retry gets a second, different answer from the model. So
`CreateWorkspace.workspace` is `Schema.optional` and every later step goes
through `named(input)`; `input.workspace!` becomes a directory called
`undefined` four steps later.

**And `run` has no requirements at all** — a step resumed by a restarted daemon
has no caller whose context it could inherit. A kind needing jj, zmx and the
thread store is a _function of them_, built where the layers exist
(`Layer.unwrap` in `daemon.ts`). Reaching for a service inside a step puts it in
the requirement channel and will not compile.

## The store is JSON, and that changes what a schema may say

A kind's input is encoded at enqueue, stored, and decoded at every step. JSON has
no `undefined`, so a field written `Schema.UndefinedOr(…)` and left unset is
_absent_ when read back — and `UndefinedOr` requires the key. The kind then dies
on its first step with "stored input does not match", one backoff after the
mistake and in a message about the wrong thing.

**Use `Schema.optional` for every optional field on a job record.**

`enqueue` puts the encoded input through JSON and **reads it straight back**,
refusing with `InputNotPortable` if it does not survive — so the refusal lands
where the mistake is. That same pass is what makes the memory and sqlite stores
hold the same thing.

## One database, two runtimes, and named migrations

Everything durable is `~/.awp/awp.sqlite`, opened once by `@awp-kit/store`. The
daemon runs under Bun (`bun:sqlite`), vitest on Node (`node:sqlite`), so the
driver is a dynamic import chosen at open time and **only the intersection of
the two APIs is used** — positional `?` parameters, `exec`, `prepare().run()`,
`prepare().all()`. Named parameters are spelled differently by each.

vitest can only exercise the Node arm. `bun run probe:jobs-store` runs the store
under Bun; run it after touching `store/src/index.ts`.

**Migrations are named, not numbered.** A `pragma user_version` counter cannot
survive two owners. A `schema_migrations` table records applied names, each
package exports its own list, and the daemon concatenates them — so appending to
either list cannot disturb the other. Each migration runs inside a transaction
with the row that records it.

- **A migration's name is fixed the moment it has run anywhere.** Renaming one
  runs it a second time; appending a statement to one already applied never runs
  it at all, and the daemon then fails on a missing table.
- The DDL is `create table`, not `if not exists`, so a migrator that failed to
  consult the record fails loudly. This replaced a version number that
  **discarded the tables** when it disagreed.

```
  journal_mode = wal      a probe can read while the runner writes  (persisted)
  foreign_keys = on       off by default — an unenforced reference is a comment
  busy_timeout = 5000     wait for a writer instead of SQLITE_BUSY
  synchronous = normal    safe under WAL, much faster than full
```

**Every table is `strict`, read narrowly:** it rejects what cannot be losslessly
converted, so `kind = 7` still becomes `"7"`. It is the second line of defence.

`store.test.ts` runs one suite against both stores — the implementation that
drifts is always the one written second.

## Threads: the work, not the checkout

A thread is a piece of work; a workspace is a checkout, and one piece of work
often needs two.

```
  thread  "tabular exports"
    ├── rowan/tabular-exports   agent · editor · action
    └── beta/tabular-exports    agent · editor · action
```

A thread holds `(project, workspace)` pairs, **not sessions** — sessions come
and go, and a workspace with nothing running is still part of the work. A pair
is also what `identity()` already recovers, so the sidebar nests by lookup with
no `awp_thread` label to shorten.

**A workspace belongs to at most one thread**, as a UNIQUE constraint on
`thread_members (project, workspace)` rather than a rule this code remembers.
`attach` is one `on conflict do update`, so the release and the claim cannot
half happen. Resolved on read instead, the sidebar would draw the workspace
twice and a person would have to decide which claim was lying.

Threads are on the wire **without a change stream**: a job changes on its own, a
thread changes when a person changes it in this window, so the reply to the
change is the update.

**`parentId` is recorded, not re-derived.** jj could answer which revision a
workspace descends from, but that is a question about commits; this is a claim
about work, and jj's answer changes as branches are rebased.

## A thread branches from a bookmark, not a working copy

```
  andrew/tabular-exports   the bookmark — where the work is named          ← this
  tabular-exports@         the workspace's working-copy commit, carrying
                           whatever is half-done in it
```

A thread based on `<name>@` inherits somebody's uncommitted edits, which is not
what "follow on from this work" means.

**The client names a thread; the daemon resolves a revision** — the bookmark is
`<prefix>/<name>` and the prefix is in the daemon's config, so a client
composing one would guess at a setting it cannot see. `baseOfThread` **asks jj**
whether the bookmark is really there.

```
  bookmark exists          andrew/lantern       the base
  no prefix configured     lantern@             fall back, do not refuse
  prefix set, no bookmark   lantern@             same
  parent in another repo   refused, by name     a revset means nothing there
  parent has no workspace  refused, by name     nothing to branch from
```

The fallbacks are not failures: somebody with no `bookmark_prefix` has no
bookmarks, and refusing would make the feature unavailable to them.

## Making a workspace: the first job that does anything

```
  1  workspace   jj workspace add          undo: forget it, remove it
  2  bookmark    jj bookmark set           undo: delete it
  3  session     zmx run -d, then labels   undo: kill it
  4  claim       the thread takes it       undo: the thread lets it go
```

- **The claim is last**: a workspace appears in the sidebar under its thread once
  claimed, so claiming first shows a half-built workspace as a finished one.
- **A step that does nothing is still a step.** The bookmark is optional and the
  _step_ is not — the runner resumes against the kind's list, so a list that
  varied by payload is one a restarted daemon could not reproduce.
- **One attempt.** Every failure here is a refusal — a name taken, a directory
  occupied, zmx missing — and none pass on their own.
- **`jj workspace forget` does not remove the directory**, so the undo does
  both — but **only when the directory contains `.jj`**. Deleting somebody's
  files because a later step failed is far worse than a stray directory.
- **`jj workspace add` refuses when the directory _above_ it is missing.** Every
  project's first workspace would have failed.
- **`bookmark set -r` takes a revision, and a workspace name is not one** —
  `<name>@` is.
- **Sessions start with `zmx run -d`, never `zmx attach`.** A session takes its
  size from whoever is looking at it, so a daemon attaching to create one sizes
  a terminal to nothing. `start` does nothing when the name exists.

Workspaces go at `~/.awp/workspaces/<project>/<workspace>` — not a free choice:
`suggestedBy` recovers a session's identity from exactly that shape.

`bun run probe:workspace` is what proves it end to end, and found both jj
refusals above. Unit tests prove the _order_ of steps and undos; nothing else.
It does **not** refuse to run inside a zmx session, because `ours()` rejects any
name outside `awp.awp-probe.*` — a guard on the property that matters.

## Two config files, and the project wins outright

```
  ~/.config/awp/config.json    global — the agent, the bookmark prefix
  <repo>/.awp/config.json      per project — how this repository is set up
```

Merged **per field, replace-if-empty** — not deep, not concatenated. A project
that says nothing about hooks inherits the global ones; a project listing one
inherits none, which is the only way a repo can turn a global hook off. `[]` and
an absent key mean the same thing.

**Read from the source repository, never from the new workspace.** `.awp/` is
untracked, so a fresh `jj workspace add` has no copy. `input.repo` is where it is.

Read per call, not per daemon, so an edit takes effect on the next chat opened.

**The model, effort and mode are one block**, `defaults`, and both faces read it:

```json
  "defaults": { "model": "opus", "effort": "medium", "mode": "auto" }
```

```
  the `agent` line     claude --permission-mode auto --model opus
  defaults             applied over it
  the modal's choice   wins over both — "from settings" means choosing nothing,
                       which is why those are `undefined` and not a value
```

The terminal gets them as argv through `agentWith`; the chat through the
adapter's own `session/set_config_option`, which is the only way that does not
lie — `configOptions` is what the panel draws. `mode` absent leaves each face
where it was.

## A hook is a line, an agent is a program

```
  agent            "claude --model opus"   split on whitespace → argv
  hooks.bootstrap  "mise trust"            handed to `sh -c`, entire
```

A hook is a line a person writes, and `&&`, a glob and a quoted path are its
ordinary furniture. **`zmxChildEnv()` applies** — a bootstrap is free to run zmx.
`bootstrap.test.ts` asserts on what the child **prints**.

The step sits **after `session`** (so there is something on screen while
`bun install` runs) and **before `brief`** (so an agent is not asked to discover
its own missing dependencies). **A failing hook fails the job** and compensation
takes the workspace back to nothing; later hooks do not run. No undo is needed —
everything a hook wrote is inside the workspace directory.

## The brief goes where the person was looking

The face was a renderer preference, so picking "chat" decided which panel was
drawn and nothing else: the brief always went to the pty. `Face` is on the
contract and on the job record, `Schema.optional`, absent meaning the terminal.

**One step, two deliveries — not two steps**, because the step list is fixed per
kind. **The session is still started for the chat face**: the chat is a separate
process, so the workspace still gets an idle terminal for whoever wants one.
Only one of them is briefed.

**`Chat.brief` is `send` plus holding the reference until the turn has ended.**
`send` returns as soon as the adapter accepts the prompt, and `RcMap` releases a
conversation two minutes after its last reference — so a brief delivered by
`send` alone has its agent shot two minutes into the first answer. The bug was
not new: a person who sends a message and switches tabs drops the subscription
the same way.

```
  startsWithin  30s   an adapter that accepted the prompt and did nothing
  holdsFor      20m   a turn running for an hour is the agent doing its job
```

Neither bound fails the step — the transcript is on disk, and a timeout that
failed would fail a job whose work is already done. Polled rather than driven
off the change stream: what is wanted is a _settled_ reading, and a stream of
edges is absent both before a turn starts and after it ends.

`chat.ts` is wired as **a closure, not the `Chat` service** — it imports
`workspacePath` from `create-workspace.ts`, so importing `Chat` would be a cycle.
One `Chat`, shared: `Layer.provideMerge`, not `provide`, or a second one is built
and the job briefs a conversation nobody is watching.

## Clearing is not clearing

`forgetFinished` deletes terminal jobs and **keeps** two kinds:

```
  queued · running    the runner still holds a fiber; the next save would put
                      the row back, minus its log
  cleanup: dirty      the one outcome the package cannot put right by itself
```

The reply is a count, so the button can say what happened when rows stay put.
`job_logs` has no foreign key back to `jobs`, so **the delete order is the
guarantee**: logs first, then jobs.
