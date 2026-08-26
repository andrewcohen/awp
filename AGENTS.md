# awp

**agent work platform.** Composable `@awp-kit/*` packages, plus `apps/amoeba` —
a reference implementation that is, to start, roughly zdeck in a webview over a
client-server architecture.

Full reasoning lives in `specs/20260825-52cw-amoeba-rewrite-spec.md`. This file
is only what would otherwise be learned the expensive way.

## Layout

```
apps/amoeba/       electrobun main process + vite renderer
packages/protocol/ the RPC contract
packages/store/    one sqlite file, and the migrations for it
packages/jobs/     work that outlives whoever asked for it
packages/server/   the daemon: multiplexer, pty, attachment
packages/pane/     the terminal, ghostty-web
archive/           the Go implementation — reference only
```

`pane` importing from `server` is a compile error, not a convention: the
tsconfig project references are the import graph.

## zmx: two rules, and they are not the same rule

This repo is developed from **inside** a zmx session. `zmx attach` branches on
`ZMX_SESSION` — from inside a session it switches the _calling_ client instead of
making a new one, which steals the terminal the caller was launched from.

- **Spawning zmx as a child:** strip `ZMX_SESSION`. Always. `zmxChildEnv()`.
- **Probing or testing against a real zmx:** _refuse_ to run inside a session.
  Not strip — refuse. `requireOutsideZmxSession()`.

Conflating them is a mistake already made here: a probe that stripped the marker
correctly still opened a new client, and a session takes its size from the client
looking at it, so it reflowed and redrew the session it was being run from. No
environment edit makes that safe.

Consequences that follow:

- `packages/server/src/probe/` holds things a human runs by hand from a plain
  terminal. They refuse to run anywhere else. Read-only commands (`list`,
  `lookup`, `history`) are safe from inside a session and are tested normally.
- `Attachment` refuses to attach to the session the process is running in.
- Never run `zmx kill`, `zmx attach`, or anything that changes a session, against
  a session this repo did not create.

## Omitting an environment variable does not remove it

`zmxChildEnv()` sets `ZMX_SESSION` to the empty string. It used to leave the key
out, a unit test asserted the returned object had no such property, and it
passed for weeks while doing nothing at all.

bun-pty hands its pairs to a Rust `Command`, which **inherits the parent
environment** and applies what it is given on top. With no `env_clear()` there
is no way to express a removal by omission — a key left out is a key left alone.
So every `zmx attach` the daemon spawned saw the marker, resolved it, and
switched the _calling_ client: the precise hijack the function exists to
prevent, aimed at whatever session the daemon was running in.

Two things follow, and the second matters more than the first.

- Neutralise by **setting**, never by omitting. An absent key is a request the
  spawner is free to ignore, and this one does.
- **A test of the function could not have caught it.** The bug was in the
  spawner. `probe/child-env.ts` spawns `/bin/sh` and prints what the child
  actually received, which is the only way to know. It never invokes zmx, never
  attaches and never names a session, so it is safe to run anywhere — run it
  after touching anything to do with process environments or the pty layer.

The general shape is worth keeping: when a guard's effect happens in someone
else's process, assert on what that process sees, not on what you handed it.

## archive/ is evidence, not truth

It is read like vendored upstream source: consulted, never called, never ported
line for line, excluded from every gate.

Its comments record what was once measured, and at least one was wrong — a
comment justifying the identity labels cited a session name as 47 characters and
over the limit; it is 45, and fits. **Re-prove anything inherited from it.**
`bun run probe:claims` exists for exactly this.

Where a claim _did_ hold, the test that proves it should test the property, not
reproduce the anecdote. `naming.test.ts` checks ten real shortened session names
read off a live `zmx ls`, because a name is an address and one character of
disagreement would leave every shortened session unfindable.

## A name cannot group a workspace

The sidebar lists **workspaces**; zmx lists **sessions**. A workspace has one
session per kind — an agent, an editor, a user action — and the temptation is to
recover the workspace by splitting `awp.<project>.<workspace>.<kind>` on dots.
It does not work, and the reason is not obvious.

`sessionName` gives the stem whatever budget the kind does not need, so one
workspace's sessions are shortened to **different stems**:

```
  awp.thicket.effect-ts-tiered-d-f500.action_dev
  awp.thicket.effect-ts-tiered-disco-f500.editor
  awp.thicket.effect-ts-tiered-discou-f500.agent
        └─ three stems, one workspace: effect-ts-tiered-discount-timemachine
```

Read one at a time those are three workspaces, and that is exactly what the
sidebar showed. Nothing there is a bug in the shortening — a name is an address,
and an address only has to resolve. Names also lose a dot inside a real project
name to `sanitize`.

The truth is in the labels awp writes (`awp_project`, `awp_workspace`,
`awp_kind`), which are unshortened. Sessions predating them — most of the ones
on this machine today — are repaired in `identities()` by asking
`stemMatches` per known workspace, which is what that function was written for:
only the workspace can reproduce the shortening at the length a given stem
actually has. One labelled session recovers every sibling. A workspace where
none is labelled stays split, which is honest rather than guessed.

The wire carries `SessionIdentity` for the same reason the refusal sentence is
on it: a client re-deriving the rule is a second implementation, and the copy
that drifts is the one nobody tests.

## Effect v4 is a release candidate, and its names moved

Most Effect material online is v3 and will mislead. **Read the installed source**
under `node_modules/.bun/effect@*/node_modules/effect/src/`.

| v3                                | v4                                                            |
| --------------------------------- | ------------------------------------------------------------- |
| `Effect.Service`                  | `Context.Service<Self, Shape>()("Key")`                       |
| `Effect.async`                    | `Effect.callback`                                             |
| `Effect.either`                   | `Effect.result` → `Result`, with `isSuccess`/`isFailure`      |
| `@effect/rpc`, `@effect/platform` | folded into core: `effect/unstable/{rpc,http,socket,workers}` |

There is **no v4 line of `@effect/rpc`** — that package is v3 and peers on
`effect ^3.22.1`. Depending on it puts two Effect runtimes in one workspace, and
the failure does not look like a version problem: two runtimes means two sets of
Context tags, so a service provided through one is simply not found by the other.
`test/deps.test.ts` guards this.

Use `@effect/platform-node-shared`, not `@effect/platform-bun`. The Bun barrel
imports `bun` (through `BunRedis`), so vitest — on Node — cannot load anything
touching it. `BunChildProcessSpawner` is `export * from` the Node one, so nothing
is lost.

## Services and their fakes

A tag exists so callers can be tested against a fake, not on any expectation of
swapping the real thing out.

```
Multiplexer   list · lookup · kill · labels · history   ← questions, no cost
Attachment    attach                                     ← an act, with consequences
PtySpawner    spawn a pty                                ← Scope in the type
```

`Attachment` is separate from `Multiplexer` because that line is also the line
between "testable from inside a session" and "must run outside zmx".

`Scope` in `spawn`'s return type is the promise the process gets killed. The
hand-rolled version this replaced had a path where cleanup ran twice and another
where it never ran.

## A command's exit code is not in its output

`ChildProcessSpawner.string` collects stdout and **discards the exit code**.
That is a fair contract for a function returning a string and the wrong one for
every command in this repo, all of which report a refusal by writing to stderr
and exiting non-zero.

```
  sh -c 'echo out; exit 3'   through `string`  →  succeeds with "out\n"
                             through `capture` →  { stdout: "out\n", exitCode: 3 }
```

What it looked like: `jj workspace add` on a workspace that already exists
prints `Error: Workspace named 'second' already exists` and exits 1, which
arrived as a successful empty answer — so the service reported creating a
workspace it had not. `zmx.ts` had the same hole, where a failing `zmx ls`
parses to an empty list and reaches the sidebar as **"no sessions"**, which is
exactly what having no sessions looks like.

So everything goes through `run.ts`. Two things about it are worth keeping:

- **stdout, stderr and the exit code are awaited concurrently.** Reading one
  stream to the end first deadlocks as soon as a command writes more to the
  unread one than its pipe buffer holds — rare enough to pass every small test
  and then hang on a long jj error. `run.test.ts` pushes 256KiB down each.
- **The failure carries the CLI's own sentence**, not one composed here. jj
  names the workspace, the bookmark and what was wrong with it.

It was found by a mutation check, not by a test: removing the idempotence guard
from `addWorkspace` should have failed the test that adds a workspace twice, and
did not. **A guard whose removal changes nothing is not doing what it claims** —
which is the general lesson, and the reason to keep running those.

## jj: name the repository, and do not snapshot to answer a question

Two flags, on every command, for two different reasons.

```
  -R <repo>                jj finds a repo by walking up from cwd. The
                           daemon's cwd is a real repository — this one.
  --ignore-working-copy    on reads. jj snapshots the working copy before
                           almost every command, `workspace list` included.
```

`-R` is why `repo` is a required argument on every method of `Jj` and not a
field somewhere: there is no call that could reach the wrong repository by
accident. It is the structural form of the zmx rule.

`--ignore-working-copy` is the quieter one — without it a _question_ writes to
the repository it is asking about. Reads take it; writes deliberately do not,
because suppressing the snapshot on a write makes a commit out of step with the
files beside it.

**`jj workspace forget` with no argument forgets the workspace it is standing
in.** For the daemon that is this repository. The name is refused when empty
rather than defaulted, and that refusal has its own test.

**Reads ask for `-T 'json(self)'`.** jj's human output puts the name, a change
id, a bookmark list and a description on one line, and taking that apart breaks
the first time a description contains a colon. Unknown keys are ignored and an
unparseable line is skipped — jj adds fields between releases, and a daemon that
refused to list workspaces over a new key would be worse.

**A bookmark name appears more than once.** `jj bookmark list` prints a row per
local bookmark _and_ per remote that disagrees, and `jj git init` gives the repo
a `git` remote that a set bookmark is immediately exported to:

```
  {"name":"andrew/x","target":[...]}                  ← local
  {"name":"andrew/x","remote":"git","target":[...]}   ← the same bookmark
```

So "does this bookmark exist" means the _local_ rows — `localBookmarks`. Asking
the raw list finds names that only exist on a remote. The first draft of
`jj.test.ts` got this wrong, which is why there is now a test whose entire job
is to state it.

**Everything is safe to run twice**, because the jobs runner re-enters the step
it failed on. `addWorkspace` on a workspace that exists succeeds; `forgetWorkspace`
on one that does not succeeds; `bookmark set` is already idempotent in jj, and
`bookmark delete` is not, so it asks first.

Forgetting a workspace **does not remove its directory**. jj says so in its own
help, and it matters: the undo of a workspace creation has to do both.

## Jobs: resume and compensation are the same disagreement

A job is a named kind with ordered steps. The design is entirely the
reconciliation of two things that want opposite behaviour on failure, and every
mistake available here is a mistake about which of them applies.

```
  attempt fails, attempts remain  →  queued, sleep the backoff, run again from
                                     the first step not in `done`. Nothing is
                                     undone.
  attempts exhausted, or cancel   →  walk `done` backwards, run each `undo`,
                                     emptying `done` as each one succeeds.
```

Because the first branch re-enters the step that failed, **`run` must be safe to
call twice** — `mkdir -p`, not `mkdir`. Because `done` is emptied by the second,
a retry after a rollback starts from nothing rather than resuming into a world
that no longer matches.

Compensation **stops at the first `undo` that fails** and marks the job
`cleanup: "dirty"`. It does not press on: each undo assumes the ones after it
already ran, so once one has not, the rest are undoing a state that never
existed. `dirty` is the only outcome the package cannot fix by itself, which is
why it is a field rather than a log line and why the status bar says it out loud
even when the jobs column is folded away.

**Interruption is two different events.** A cancelled job and a daemon shutting
down both arrive as an interrupted fiber, and nothing about the interrupt tells
them apart. `cancel` records the intent in a set before it interrupts; the exit
handler reads it. Shutdown therefore leaves the record `running` with its `done`
list intact, and the next start finds it non-terminal and resumes. Getting this
backwards silently undoes work that was meant to carry on, and the record looks
tidy either way — `runner.test.ts` asserts it on the trace and on a second
runner over the same store, because no assertion about the record could.

### The store is JSON, and that changes what a schema may say

A kind's input is encoded at enqueue, stored, and decoded again at every step.
JSON has no `undefined`, so a field written `Schema.UndefinedOr(…)` and left
unset is _absent_ when read back — and `UndefinedOr` requires the key. The kind
then dies on its first step with "stored input does not match", one backoff
after the mistake and in a message about the wrong thing. Use `Schema.optional`,
which accepts both.

Two things follow, and they were both added after watching it happen:

- `enqueue` puts the encoded input through JSON **and reads it straight back**,
  refusing with `InputNotPortable` if it does not survive. The refusal lands
  where the mistake is.
- That same JSON pass is what makes the memory store and the sqlite store hold
  the same thing. Without it every kind that loses something in JSON passes its
  tests and fails in the daemon.

### One database, two runtimes, and named migrations

Everything durable lives in `~/.awp/awp.sqlite`, opened once by
`@awp-kit/store` and shared. Threads were a JSON file for about an hour; what
moved them is the first real job — creating a workspace writes a job record
**and** claims the workspace for a thread, and two stores means it can do one
and not the other with nothing afterwards able to say which.

The daemon runs under Bun, which has `bun:sqlite` and not `node:sqlite`. vitest
runs on Node, which is the other way round. So the driver is a dynamic import
chosen at open time, and only the intersection of the two APIs is used —
positional `?` parameters, `exec`, `prepare().run()`, `prepare().all()`. Named
parameters are spelled differently by each and are avoided for that alone.

vitest can only ever exercise the Node arm. `bun run probe:jobs-store` runs the
store under Bun and asserts on what that process sees, which is the same shape
`probe:child-env` exists for. Run it after touching `store/src/index.ts`.

**Migrations are named, not numbered.** A `pragma user_version` counter cannot
survive two owners: jobs appending a migration would renumber threads'. So a
`schema_migrations` table records applied names, each package exports its own
list, and the daemon concatenates them. Appending to either list cannot disturb
the other. Each migration runs inside a transaction with the row that records
it — a name written for work that did not finish is the one state nothing
recovers from by running again.

A migration's name is fixed the moment it has run anywhere; renaming one makes
it run a second time. The DDL is deliberately `create table`, not
`create table if not exists`, so a migrator that failed to consult the record
fails loudly rather than quietly doing nothing.

This replaced a version number that **discarded the tables** when it
disagreed. That was a real loss of data, and the way it read from outside was a
daemon starting normally with nothing in it.

The connection settings, and what each is for:

```
  journal_mode = wal      a probe can read while the runner writes
  foreign_keys = on       off by default in sqlite — an unenforced
                          reference is a comment
  busy_timeout = 5000     wait for a writer instead of SQLITE_BUSY
  synchronous = normal    safe under WAL, much faster than full
```

`journal_mode` is stored in the file and persists; the other three are per
connection and are set on every open.

**Every table is `strict`** — but read the promise narrowly. `strict` rejects
what cannot be _losslessly_ converted, so `kind = 7` still becomes the text
`"7"` and raises nothing; `attempt = 'many'` is what it stops. It is the second
line of defence, not the first.

`store.test.ts` runs one suite against the memory and sqlite stores together.
It found an off-by-one in the sqlite log trim on its first run, which is the
entire argument for writing it that way: the implementation that drifts is
always the one written second.

### Threads: the work, not the checkout

A thread is a piece of work; a workspace is a checkout, and one piece of work
often needs two of them.

```
  thread  "tiered discounts"
    ├── rowan/tiered-discounts   agent · editor · action
    └── beta/tiered-discounts    agent · editor · action
```

**A thread holds `(project, workspace)` pairs, not sessions**, and that choice
removed a step that looked necessary. Sessions come and go; a workspace with
nothing running is still part of the work. A pair is also exactly what
`identity()` already recovers, so the sidebar nests by looking the pair up —
no `awp_thread` label, nothing new to shorten.

**A workspace belongs to at most one thread**, and that is a UNIQUE constraint
on `thread_members (project, workspace)` rather than a rule this code remembers
to apply. `attach` is one `on conflict do update`, so the release and the claim
cannot half happen. Resolving it on read instead has no rendering: the sidebar
would draw the workspace twice and a person would have to decide which claim
was lying.

Threads are on the wire **without a change stream**, unlike jobs, and the
asymmetry is the point. A job changes on its own — that is what a job is — so a
client that only asks misses everything interesting. A thread changes when a
person changes it, in this window, so the reply to the change is the update.

### A thread branches from a bookmark, not from a working copy

`cmd+shift+N` starts a thread from the one on screen, and the obvious reading of
that is wrong in a way worth stating.

```
  andrew/tiered-discounts   the bookmark — where the work is named,
                            moved when a person decides it should be     ← this
  tiered-discounts@         jj's revset for the workspace's working-copy
                            commit, carrying whatever is half-done in it
```

`<name>@` was the first answer, and a thread based on it inherits somebody's
uncommitted edits. That is not what "follow on from this work" means.

**The client names a thread; the daemon resolves a revision.** It has to be that
way round — the bookmark is `<prefix>/<name>` and the prefix is in the daemon's
config, so a client composing one would be guessing at a setting it cannot see.
`baseOfThread` in `handlers.ts` does the resolving, and it **asks jj** whether
the bookmark is really there rather than trusting the name it just composed. A
revision that does not exist fails inside the job, one step in, in a message
about bookmarks.

Three outcomes, and each one is deliberate:

```
  bookmark exists          andrew/lantern       the base
  no prefix configured     lantern@             fall back, do not refuse
  prefix set, no bookmark  lantern@             same
  parent in another repo   refused, by name     a revset means nothing there
  parent has no workspace  refused, by name     nothing to branch from
```

The two fallbacks are not failures. Someone with no `bookmark_prefix` has no
bookmarks at all, and refusing there would make the feature unavailable to them.

**The picker offers bookmarks, not threads.** Offering threads was the first
attempt and was wrong in a way only use showed: most workspaces on a real
machine predate threads and belong to none, so the list came up empty exactly
when someone stood in a branch they wanted to continue from. `ThreadBases`
returns `trunk()` plus every _local_ bookmark — local, because a name that only
exists on a remote cannot be branched from without fetching first, and offering
it would be offering a failure.

The daemon then recovers the parent thread _from the chosen base_, by taking the
prefix off the bookmark and asking which thread holds that workspace. So
branching off an unclaimed branch works and simply records no lineage.

**`parentId` is recorded, not re-derived.** It could be recovered later by
asking jj which revision a workspace descends from — but that answers a question
about commits, and this is a claim about work: someone said "this follows from
that" when they started it. jj's answer changes as branches are rebased and
deleted; the claim does not.

`bun run probe:thread-parent` is what proves the whole path, and it exists
because `handlers.test.ts` structurally cannot: a fake jj accepts any string, so
a test of the _decision_ passes on a revset the real jj would reject. The probe
builds a parent workspace in a throwaway repo, starts a child from it, and then
asks jj from outside whether the child really landed on the parent's tip.

Its own first run earned its keep, and not in the way expected — the branching
was right and the marker commit was empty:

```
  jj -R <repo>      describe   snapshots the DEFAULT workspace
  jj -R <parentDir> describe   snapshots the one you meant
```

Every other check passed. Only "the parent's file came with it" caught it.

### A step may write down what it learned

`JobStep.run` returns `Effect<void | Partial<Input>, JobError>`. Almost every
step answers `Effect.void`; one that _discovers_ something the later steps
depend on returns a patch, and the runner merges it into the stored input **with
the same write that marks the step done**.

This exists because a step cannot hand a value to the next one — there is
nowhere to put it. A job resumed by a restarted daemon has only its record, so
anything not on the record did not happen.

The first need for it was naming a workspace:

```
  before   ThreadStart ── 10s model call ── enqueue ── job appears
           the window waits here ↑          and the jobs panel is empty

  after    ThreadStart ── enqueue ── job appears ── step "name" ── 10s
                                     ↑ immediately, with somewhere to watch
```

Resolving before enqueue _worked_. What it cost was ten seconds spent in front
of a person watching a form that would not close, for work that has a progress
panel of its own.

Two things this is not. It is not a channel between steps — the patch goes into
the durable, schema-checked input, not into memory. And it is not an escape from
`run` being safe twice: a step whose patch is already there must notice and do
nothing, which is how a retry avoids a second, different answer from the model.

`CreateWorkspace.workspace` is therefore `Schema.optional`, and every step after
`name` goes through `named(input)` rather than `input.workspace!` — a missing
name asserted away becomes a directory called `undefined` four steps later.

**A step that throws now fails the job.** It used to hang it: a defect is not on
the error channel, so it sailed past the `Effect.result` wrapping an attempt,
killed the fiber, and left the record saying `running` with nothing behind it.
Found by a fake missing a method, which is exactly how a real service gains one.

### Clearing is not clearing

`JobStore.forgetFinished` deletes terminal jobs and **keeps** two kinds:

```
  queued · running    the runner still holds a fiber; the next save would
                      put the row back, minus its log
  cleanup: dirty      compensation stopped partway. The one outcome the
                      package cannot put right by itself, so the one a
                      person most needs to still be there tomorrow
```

The rule lives in the daemon and the reply is a count, so the button can say
what actually happened when rows stay put. `job_logs` has no foreign key back to
`jobs` — a constraint check per appended line is a cost paid on every line for a
guarantee only this one place needs — so **the delete order is the guarantee**:
logs first, then jobs.

### Making a workspace: the first job that does anything

```
  1  workspace   jj workspace add          undo: forget it, remove it
  2  bookmark    jj bookmark set           undo: delete it
  3  session     zmx run -d, then labels   undo: kill it
  4  claim       the thread takes it       undo: the thread lets it go
```

**The claim is last on purpose.** A workspace appears in the sidebar under its
thread once claimed, so claiming first would show a half-built workspace as a
finished one for as long as the rest took.

**A step's `run` has no requirements**, and cannot: a step resumed by a
restarted daemon has no caller whose context it could inherit. So a kind that
needs jj, zmx and the thread store is a _function of them_, built where the
layers exist — `Layer.unwrap` in `daemon.ts` is the one place all of them are in
hand at once.

**`enqueue` takes a `JobRef`, not a `JobKind`.** All it uses is the name, the
schema and the title; the steps come from the registry, looked up by name. That
matters here because a handler that had to pass a whole kind would have to build
the services those steps close over — which it briefly did, and which was a lie
about what the handler needs.

**A step that does nothing is still a step.** The bookmark is optional and the
_step_ is not: the runner reads `done` back from the store and resumes against
the kind's list, so a list that varied by payload is a list a restarted daemon
could not reproduce.

**One attempt.** Every failure this job has is a refusal — a name taken, a
directory occupied, zmx missing — and none pass on their own. Retrying only
delays the rollback, which is the thing a person is waiting for.

**`jj workspace forget` does not remove the directory** — jj says so in its own
help — so the undo does both, or the next attempt cannot create into what the
last one left. The directory is removed **only when it contains `.jj`**.
Deleting a person's files because a later step failed is far worse than leaving
a stray directory, and that guard is the only place this job could do it.
`create-workspace.test.ts` fails when it is removed; that was checked.

Workspaces go at `~/.awp/workspaces/<project>/<workspace>`, which is not a free
choice — `suggestedBy` in `multiplexer.ts` recovers a session's identity from
exactly that shape when it carries no labels.

**Two things only the first end-to-end run found**, and neither was reachable
by a test against fakes:

```
  jj workspace add  makes the workspace directory, and refuses when the
                    directory ABOVE it is missing. Every project's first
                    workspace would have failed.

  bookmark set -r   takes a revision. A workspace NAME is not one —
                    `<name>@` is jj's revset for its working-copy commit.
                    jj said so itself: Revision `probe-1` doesn't exist.
```

`bun run probe:workspace` is what found them: a throwaway jj repo, a real
workspace, a real session, checked from outside and then cleaned up. Run it
after touching the job — the unit tests prove the _order_ of the steps and the
order they are undone in, which is what fakes are good for, and nothing else.

That probe **does not refuse to run inside a zmx session**, unlike the others,
and the reason is worth reading before copying either pattern. The session is
created by the daemon, which is already outside one; what the probe itself runs
is `zmx ls` and `zmx get`, which are read-only, and one `zmx kill` that names
the session it made. So the guard is on the property that matters — `ours()`
rejects any name outside `awp.awp-probe.*` — which is stronger than a refusal,
not weaker. A blanket refusal would have been easier to write and would have
guarded the wrong thing.

**Sessions are started with `zmx run -d`, never `zmx attach`.** Attaching is how
an interactive caller makes a session, and a session takes its size from
whoever is looking at it — a daemon attaching to create one would size a
terminal to nothing. `Multiplexer.start` makes it and leaves it alone; a window
attaches later if a person opens it. It also does nothing when the name already
exists, which is both the idempotence and the guarantee that it never touches a
session it did not create.

### `demo` is scaffolding

`packages/server/src/jobs/demo.ts` and the `JobDemo` call in the contract exist
so the jobs panel can be looked at while nothing real enqueues anything. They go
together, and they go as soon as the first real kind lands.

## Frontend

**The stack is chosen. Do not add a fourth thing to it.**

```
  Base UI          behaviour — dialogs, selects, tabs, menus
  StyleX           appearance — every rule in the renderer
  TanStack Router  navigation, when there is any
  Effect Atom      renderer state that outlives a component
```

The division between the first two is the one that gets violated, so it is
worth stating flatly: **Base UI ships no styles and StyleX writes no
behaviour.** Reaching for a styled component library replaces both at once;
hand-rolling a dropdown replaces the first and loses the arrow keys, the
typeahead, the roving tab stop, the aria wiring and — the one that shows up as
a visual bug rather than an accessibility one — the portal, without which a
popup inside a scrolling column is clipped by it.

`@effect/atom-react` is a dependency already and is **not yet imported
anywhere**. That is deliberate: it is the answer when the window needs shared
state, and reaching for something else on the day it does is the mistake this
list exists to prevent. It is not an invitation to introduce an atom before
there is one to have.

The router _is_ now used, and the reason is worth stating because the obvious
one is wrong. The window has one screen and no navigation to speak of, so
"needs routes" was never going to be what earned it.

### Everything is reachable from the keyboard, and the keys are vim's

**A mandate, not a preference.** Every control in this window has to be
operable without a pointer, and the movement keys are `h j k l` rather than the
arrows. This is a terminal multiplexer with furniture around it; the furniture
answering to a different set of keys than the thing inside it is the friction
the whole application exists to remove.

What follows from it, stated once so it is not re-argued per feature:

```
  ctrl+h / ctrl+l   move between columns — sidebar · agent · accessory
  ctrl+j / ctrl+k   move within one, down and up
```

`ctrl` and not a bare `hjkl`, because the pane is a terminal: an unmodified `j`
belongs to whatever is running in it, and stealing it would break vim inside
the very window whose keys are being copied from vim. The chord has to be one
the pane does not want.

Three consequences worth knowing before writing a control:

- **Capture phase, on `window`.** The emulator installs its own keydown handler
  and calls `stopPropagation` for every key it consumes, so a bubble-phase
  listener never hears a chord while a pane has focus. Measured — see the note
  on `cmd+N` in `App.tsx`. Capture is also the right meaning: an application
  shortcut is decided before the terminal claims the key.
- **`event.code`, not `event.key`.** With a non-US layout `key` is whatever the
  physical key maps to, and a shortcut is the physical key.
- **A control hidden on hover must still be focusable.** `opacity: 0`, never
  `display: none` — an element outside the layout cannot be tabbed to, and
  hover-only means the feature does not exist without a pointer. `MoveToThread`
  is the worked example.

This is also why Base UI is a dependency and not a nicety: its menus, tabs and
dialogs ship the roving tab stop, the typeahead, the focus return and the aria
wiring. Hand-rolling any of them starts this mandate over from nothing.

### Selection is an address, not a name

What earned it is that **a session name is shortened and cannot be split back
into its parts** — the rule this file already states at length above. Selection
used to be one string of React state holding exactly that shortened name, kept
across reloads by hand:

```
  before   selected = "awp.thicket.effect-ts-tiered-discou-f500.agent"
  after    /w/thicket/effect-ts-tiered-discount-timemachine/agent
```

The daemon sends the unshortened truth as `SessionIdentity` and the old
selection threw it away, storing the shortening and then searching the listing
for a name equal to it. A session restarted under a different shortening — a
sibling appearing and changing the stem's budget — is a selection that silently
stops resolving. The route holds the three fields the labels carry, so it
cannot.

Everything else follows from that and is not the argument for it: back and
forward now work, `remembered.ts` lost its hand-rolled session key, and the
address is one value rather than a name plus the rules for reading it.

Three shapes, in `address.ts` — kept separate from `routes.ts` so that nothing
pure imports the router, which is what stops `App → routes → App` being a cycle:

```
  /                              nothing open — the fixture
  /w/$project/$workspace/$kind   one of ours: the unshortened truth
  /s/$name                       someone else's: the name is all there is
```

**One route level, and no `Outlet`.** The layout does not change with the
address — the same two bars and three columns are on screen whatever is
selected — so a nested route rendering a different tree would model a screen
change that does not happen, and would then have to hand the session list back
down through it. The root renders the window and reads the address; the leaf
routes exist to type and parse it.

**Hash history**, because the renderer is served by Vite in development and by
electrobun's own protocol in a build, and only one of those would rewrite a
deep path back to `index.html`.

**The address is derived, never written back.** `sessionAt` answers undefined
for an address naming a session that has gone _or_ one the daemon refuses — the
session the daemon is itself running in is in the listing and must not be
opened. Correcting the address from the listing would be a second copy of
something already known, and would race the first listing on launch.

`localStorage` keeps one mirror of the path, read exactly once, in `main.tsx`,
and only when the hash is empty. A reload keeps the hash on its own; what a
history cannot survive is the application being quit and started again.

- **electrobun is pinned to 1.18.1.** 2.x is a _bootstrap_: the npm package
  contains no runtime, importing it throws by design, and the real APIs come from
  a separate toolchain (Hutch) via `npx electrobun init`. Migrating is real work
  and is not on the path to anything.
- Electrobun publishes raw TypeScript as its entry, so `tsc` typechecks the
  library's own source and fails against a newer `@types/bun`. `apps/amoeba/types`
  stubs the surface actually used; `skipLibCheck` cannot help, because these are
  `.ts` files rather than declarations.
- **`@vitejs/plugin-react` v6 silently ignores a `babel` option.** It was removed
  and passing one is not an error. React Compiler comes through
  `@rolldown/plugin-babel`, and StyleX rides the same pass. The tell that it was
  not running was a bundle byte-identical to one built without it.
- Vite owns the renderer and Electrobun copies `dist/renderer` in. Electrobun
  never compiles it.
- **A barrel export can drag `node:fs` into the browser.** The job record is a
  Schema, so the contract imports it, so the renderer imports it — and
  `@awp-kit/jobs`' index reaching `sqlite.ts` was enough to break the dev server
  outright. The sqlite store lives at `@awp-kit/jobs/sqlite`, which only the
  daemon asks for. A production build would have tree-shaken it and said
  nothing.
- **Base UI tabs are controlled here, deliberately.** StyleX resolves styles at
  render — `stylex.props(a, on && b)` — so which tab is selected has to be a
  value the component can read. Base UI still owns the arrow keys, the roving
  tab stop and the aria wiring, which is the whole reason it is there.

## StyleX fails quietly, twice

Both of these produce markup that is structurally right and visually wrong, with
no error anywhere. Neither is caught by a gate, so both are listed here.

**One set of options, two passes.** The Babel plugin turns `stylex.create` into
class names and hands the rules out as metadata the bundler drops; the PostCSS
plugin re-reads the same files with its own Babel and keeps the metadata instead
of the code. `dev` changes the class names, so the two arms disagreeing about it
yields class names no rule matches. That is why `apps/amoeba/stylex.babel.mjs`
exists and why `postcss.config.mjs` imports from it rather than restating.
`include` there must likewise cover everything the bundler compiles.

The PostCSS pass also needs `parserOpts` naming `typescript` and `jsx` — it wants
metadata rather than output, so nothing needs stripping, but without a parser
that knows the language it dies on the first `import type`.

**A dev server started before `postcss.config.mjs` existed keeps serving the old
sheet.** It does not fail; it serves a handful of rules instead of ninety, which
looks exactly like a StyleX bug. Restart Vite after adding or moving a PostCSS
config.

**`border` and `background` shorthands are dropped in silence.** No error, no
warning; the declaration is simply not in the output. `border: "none"` on a
`<button>` therefore leaves the UA default, which on macOS is a 2px outset
bevel — the tell is that every session row is suddenly a little box:

```
  border: "none"          ✗ dropped     borderStyle: "none"   ✓
  background: colour      ✗ dropped     backgroundColor       ✓
  flex · font · padding · margin        ✓ these do survive
```

A third rule, which at least does fail loudly: **an identifier used inside a
`create` value is resolved by StyleX itself**, and must come from a `.stylex.ts`
file. Interpolating an ordinary constant — `DIVIDER` from `columns.ts` — into a
static style is a build error about theming rules, which is not what is wrong. A
dynamic style takes the value at runtime and asks no such question, so a
constant that belongs somewhere else can stay there.

Verify the two silent ones by grepping the built CSS for the property, not by
reading the source:

```
  grep -oE "[;{]border:[^;}]*" apps/amoeba/dist/renderer/assets/*.css
```

## The window is an app, not a page

Two rules that hold everywhere in the renderer:

- **Nothing scrolls at the top level.** `html`, `body` and `#root` are pinned in
  `global.css`. A column scrolls its own content; the document never does, and
  overflow that reaches the window is meant to be visible as a bug rather than
  absorbed by a scrollbar. `height: 100%`, never `100vh` — vh measures the
  visual viewport, which is a different number as soon as anything insets the
  window.
- **No horizontal scrollbar anywhere, without being asked for by name.** A
  vertical scrollbar means there is more content than height, which is the
  ordinary state of a list. A horizontal one means the layout is wrong: some
  child was allowed to be wider than the column holding it, and scrolling
  sideways to read a name is not the repair for a name that should have been
  truncated. Set `overflowX: hidden` so the fault shows up as clipped text,
  which is findable, rather than as a scrollbar, which reads as deliberate.

  It is nearly always one of two things, and both have bitten here:

  ```
    width: 100%  on a flex child   a full-width child plus a sibling is
                                   wider than the row — 236px of column
                                   against 240px of content
    no minWidth: 0                 a flex item will not shrink below its
                                   content, so a long name pushes the row
  ```

  `flex: 1` **with** `minWidth: 0` is the pair. Either alone is the bug. The
  wide things — a table, a diagram, a code block — scroll inside their own
  `overflow-x: auto` box, which is a deliberate container and not the column.

- **Colour follows the system preference.** `color-scheme: light dark` for the
  engine's own furniture, and `useColorScheme` — `useSyncExternalStore`, not
  `useState` + `useEffect`, which reads a frame late and flashes the wrong theme
  on launch.

Latte is not Macchiato with the ends swapped. Its ANSI black is subtext1 rather
than surface1, because the mirror of Macchiato's choice is `#bcc0cc`, which
against a near-white background is not ink. `palette.ts` says so at the table.

The pane recolours through `setPaneTheme`, and **that does not currently work** —
this paragraph used to claim it did, which is the reason it now says otherwise at
length.

`setTheme` updates the renderer's theme and palette and repaints nothing; the
render loop only redraws rows the buffer marks dirty, and recolouring marks
none. The nudge afterwards — putting the canvas' pixel size in disagreement with
the renderer's metrics — reads in the source like the one full redraw reachable
from public API, and it is not one. Measured:

```
  setPaneTheme reached        yes, theme.background = #eff1f5
  term.renderer               present; setTheme, getCanvas both functions
  canvas.width = 0 applied    yes — and the canvas returns to 336x588 on its own
  corner pixel afterwards     rgb(36,39,58)   ← macchiato base, the old colour
  the same nudge by hand      also nothing
  a real reflow, 900 → 1150   rgb(239,241,245) ← the new colour
```

Two things this is worth keeping for. The reflow works because
`Terminal.resize` calls `renderer.render(wasmTerm, true, …)` outright — and it
early-returns on unchanged dimensions, so it cannot simply be called: resizing
to `rows - 1` and back would fire `resizeEmitter` twice and reflow the real
session. And the last reading that fits all six rows is that `term.renderer` is
not the renderer the loop draws with, which is where task #23 starts.

The general shape, again: a mechanism read out of someone else's source is a
hypothesis. This one was written down as a finding without a pixel ever being
sampled to check it.

## Seeing the renderer

No gate can tell you the pane is right. A terminal emulator either lays the
glyphs down correctly or it does not, and every claim in
`patches/ghostty-web@0.4.0.patch` is a claim about pixels. Three routes were
tried; two of them are dead ends worth not rediscovering.

```
  Chrome extension    list_connected_browsers → []      not connected
  osascript           "Not authorized to send Apple events"
  screencapture       "could not create image from rect"  no Screen Recording
  Playwright WebKit   works, and is the right engine ✓
```

**WebKit, not Chromium.** Electrobun renders in WKWebView. The pane draws every
glyph with `fillText` onto a canvas, and canvas text rasterisation is the part
that differs most between engines — a Chromium screenshot would be a picture of
a different renderer and proves nothing about the patch. Playwright's WebKit is
the same engine family, so it answers the actual question.

**Install outside the repo.** Playwright is a verification tool, not a
dependency of anything that ships, and its browser is ~77MB. Put it in a scratch
directory and the repo never learns about it:

```
cd $CLAUDE_JOB_DIR/tmp
bun add playwright
bunx playwright install webkit      # required — see below
```

The install step is not optional, and having WebKit already downloaded is not
the same as having the right one. Each Playwright release pins an exact browser
build number: a cached `webkit-2287` from some other project is invisible to a
`webkit-2336` client, and `ls ~/Library/Caches/ms-playwright` showing a webkit
is therefore not evidence you can skip this. The failure reads `Executable
doesn't exist at .../webkit-2336`.

**Point it at the Vite dev server**, not at a built app — `http://127.0.0.1:5273/`
with `bun run dev` already up. Building first adds a step that can fail on its
own and tests a different artefact than the one being edited.

The script does three things, and the screenshot is the last of them. Written
out in full because a future session will not have the scratch directory this
one used — copy it, do not reconstruct it:

```js
// $CLAUDE_JOB_DIR/tmp/shot.mjs  ·  run: SHOT_DIR=$PWD bun run shot.mjs
import { webkit } from "playwright";

const out = process.env.SHOT_DIR;
const browser = await webkit.launch();

for (const scheme of ["dark", "light"]) {
  const page = await browser.newPage({
    viewport: { width: 1200, height: 760 },
    deviceScaleFactor: 2, // glyph detail; at 1x the renderer checks are unreadable
    colorScheme: scheme, // this is what drives prefers-color-scheme
  });

  // 1. Errors first. A blank pane and a broken pane look identical.
  const errors = [];
  page.on("pageerror", (e) => errors.push(String(e)));
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(m.text());
  });

  await page.goto("http://127.0.0.1:5273/", { waitUntil: "networkidle" });
  // The wasm compiles, then the fixture is written, then the render loop paints.
  // Wait for the canvas to exist rather than for a duration, then let it settle.
  await page.waitForSelector("canvas", { timeout: 15000 }).catch(() => {});
  await page.waitForTimeout(1500);

  // 2. Assert what a screenshot cannot show.
  const probe = await page.evaluate(() => {
    const el = document.documentElement;
    const canvas = document.querySelector("canvas");
    return {
      scroll: [el.scrollWidth, el.clientWidth, el.scrollHeight, el.clientHeight],
      canvas: canvas ? [canvas.width, canvas.height] : null,
      rootBg: getComputedStyle(document.querySelector("#root").firstElementChild).backgroundColor,
    };
  });
  console.log(scheme, JSON.stringify(probe), "errors:", JSON.stringify(errors));

  // 3. And only then look.
  await page.screenshot({ path: `${out}/pane-${scheme}.png` });
  await page.close();
}
await browser.close();
```

What a pass looks like, and what each field is for:

```
dark  {"scroll":[1200,1200,760,760],"canvas":[1344,1512],"rootBg":"rgb(30, 32, 48)"}  errors: []
light {"scroll":[1200,1200,760,760],"canvas":[1344,1512],"rootBg":"rgb(230, 233, 239)"} errors: []
        └─ scrollW===clientW, both axes         └─ present   └─ differs by scheme
```

- `errors: []` first. gdeck lost a whole debugging session to a missing binding
  presenting as a black rectangle, and a screenshot of a black rectangle is not
  evidence of anything.
- `scroll` proves the no-top-level-scrollbar rule. A scrollbar is a layout that
  has been mis-sized, and it is invisible in a screenshot of content that fits.
- `rootBg` differing between the runs proves the system preference is actually
  being read, rather than a palette that merely happens to be dark. A hardcoded
  theme passes the dark screenshot.
- `canvas` non-null separates "the emulator failed to start" from "the emulator
  started and drew the wrong thing", which are different bugs in different
  files.

The same harness drives gestures, and that is how the columns were checked:
`page.mouse.down()` and a run of `mouse.move()` for a divider drag,
`page.setViewportSize()` stepped down through the widths where `fitColumns` has
to give something up. Read `aria-valuenow` off the separators for the resulting
widths — a layout worth an assertion is usually one worth announcing to
assistive technology anyway, so the accessible name is already the probe.

Two things that measurement does **not** establish, in case a later session
reads it as more than it is: a run of mouse moves is one gesture, not one reflow
per move, because ResizeObserver coalesces; and a fixture is not a scrollback.
The cost of reflowing ten thousand lines is a question for a live session, not
for this harness.

**Then read the image and say what you see, check by check.** The fixture in
`apps/amoeba/src/renderer/fixture.ts` is built so each block fails visibly if
one specific patch fix is not reached — descenders clipped, wide glyphs
bleeding, stems washed out, shades hatched, box corners not meeting. A pane that
merely "looks like a terminal" is not a pass.

One result from this that is worth keeping: under Latte the `░▒▓█` ramp runs
light-to-dark, inverted from Macchiato, because the blocks are drawn in the
foreground colour. That is stronger evidence the patched glyph path is live than
the dark screenshot alone — it shows the patch reading the theme rather than
holding hexes. Prefer checks with that property.

## The window is two bars with three columns between them

The columns used to run edge to edge, top to bottom, and everything the window
had to say about _itself_ had to borrow space from a column already spoken for —
the appearance toggle ended up in a sidebar footer for exactly that reason.

```
  ┌──────────────────────────────────────────────┐  header · drag region
  │ sidebar │ agent            │ accessory       │  the only row that flexes
  └──────────────────────────────────────────────┘  footer · appearance, jobs
```

Two consequences, both of which replaced something:

- **The top bar clears the traffic lights on behalf of all three columns.** The
  window is `hiddenInset`, so the controls float over the content; each column
  used to carry `space.titlebar` of padding to stay clear of them, in three
  places, none of which said why. `space.titlebar` is now that bar's height.
- **The top bar is the drag handle** — `-webkit-app-region: drag`, which StyleX
  does emit; check the built CSS rather than believing this. With the title bar
  hidden there is nothing else to grab, which is also why nothing interactive
  goes in it.

Both bars are `flex-shrink: 0` in a column layout with `minHeight: 0` on the
middle row, so a short window shrinks the columns rather than pushing the footer
off the bottom — which is the usual way a flex column grows the scrollbar
`global.css` says it must not have.

The footer says nothing when there is nothing to say. A status bar that always
reads `0 running · 0 failed` teaches the eye to skip it, which costs exactly the
one moment it exists for.

## Debug tools live in the accessory column

The accessory column is a set of panels behind Base UI tabs — jobs first,
because that is the one someone opens on purpose, then the debug tools, which
are the ones opened when something feels wrong.

`apps/amoeba/src/renderer/debug/` is a collection, not a panel. The meter there
answers what "feels laggy" means — what the pointing device emitted, how many
reports the pane made of it, how much came back, and whether frames are being
dropped — and it exists because guessing at that question twice produced two
wrong answers.

Two things worth keeping about its shape. Nothing is behind a flag: a debug tool
nobody can find is a debug tool nobody uses, and a 4Hz timer is not a cost worth
hiding it for. And it shows peaks beside live figures, because by the time a
hand leaves the trackpad the live figure is zero — a reading only anyone fast
enough to catch is not a reading.

## Never write a real name down

No real project, repository, branch, customer, product or person's name goes
into this repo — not in code, not in a test fixture, not in a comment, not in a
commit message. `awp`, `amoeba` and `andrew` are the exceptions, because the
repository is already public under them.

This is a repo about a tool for working on _other_ repositories, so real names
arrive constantly and by accident: a session read off `zmx ls`, a path in an
error, a workspace in a screenshot, an example in a doc. Every one of them is a
thing that ends up on GitHub.

Invent instead. The corpus in `naming.test.ts` uses `thicket`, `orchard`,
`harbor-works`, `typed-router` and `lantern`, which are shaped like the real
ones — long enough to shorten, sharing prefixes where the real pair did — and
name nothing.

Two things learned doing the scrub, both worth not rediscovering:

- **A rename can move an assertion.** The sidebar orders workspaces
  alphabetically, so swapping a project name for one that sorts differently
  silently reorders every fixture built on it. Two tests failed on exactly
  that. Pick a replacement that sorts where the original did, or fix the
  expectation deliberately.
- **A rename can change a hash.** `naming.test.ts` pins ten shortened session
  names, and a stem that changes changes its fingerprint. The expectations were
  recomputed, and the test now says plainly what that cost: they were real
  names once, so they proved agreement with a hash written months ago by other
  code; recomputed, they only pin the current behaviour. That is still the
  property worth having — a name is an address — but it is a weaker claim, and
  the comment says so rather than pretending otherwise.

When something has already been written down, rewrite the history rather than
adding a commit on top. Check `git log --oneline -S <name> origin/main` first,
because a name that reached the remote is a different problem.

**Use `jj fix`.** Not `jj edit` on an ancestor, and not `git filter-branch`.
Editing an old commit by hand and letting descendants rebase produced 47
conflicts across a 140-commit branch, because every later commit that touched
the same files collides. `jj fix` runs a tool over the file content of a whole
revset and says so in its own docs: _"Descendants will also be updated by
passing their versions of the same files through the same tools. This will
never result in new conflicts."_ It rewrote 129 commits with none.

```
jj fix \
  --config 'fix.tools.scrub.command=["python3", "<filter>.py", "$path"]' \
  --config 'fix.tools.scrub.patterns=["glob:**/*.ts", "glob:**/*.go", …]' \
  -s '<base>..@'
```

The filter reads a file on stdin and writes it back on stdout, so it must be
deterministic — `jj fix` reuses one result for identical content across
commits.

It fixes **file content only**. Commit messages are separate, and the check
that catches them is `jj log -T description` piped through the same filter;
rewrite each with `jj describe -r <id> --stdin`. Five were missed on the first
pass because the trees came back clean and the messages were not looked at.

## Working here

- **Run each gate as its own command.** The dev-loop hook records one gate per
  Bash invocation, so `bun run lint && bun run test` registers only one of them.

  ```
  bun run fmt   ·  lint  ·  typecheck  ·  test  ·  doctor
  ```

- **Judge a gate by its exit code, never by grepping its output.** `tsc` colours
  its output, so there are escape codes _between_ the words:

  ```
    what it prints   - \e[91merror\e[0m\e[90m TS2741: …
    grep "error TS"  no match — on a run with eight errors
  ```

  A whole afternoon was reported as "typecheck: 0 errors" on a renderer whose
  entry point could not resolve an import, because the count came from a grep
  that never matched anything. The exit code was 2 the whole time.

  ```
  bun run typecheck > /tmp/tc.txt 2>&1; echo "exit=$?"
  ```

  Beware `cmd | tail` for the same reason: `$?` is then _tail's_ status.

- Dependency versions live in **bun workspace catalogs** in the root
  `package.json` — `"effect": "catalog:"` in a package, the number in one place.
  The effect family has to move together, and four packages naming their own
  version is four chances for two runtimes in one tree.

- Relative imports carry **no extension**. `moduleResolution: "bundler"` resolves
  them; `.js` names a file that does not exist, and
  `allowImportingTsExtensions` conflicts with declaration emit under `composite`.
- `tsc` writes to a top-level `.tsbuild/` and nothing consumes it — exports point
  at `src/*.ts` and both Vite and Bun read TypeScript directly. It is a
  typechecker and nothing else.
- Commit messages go through `jj describe --stdin < file`. The shell here is
  fish, and a long `-m` with apostrophes or backticks will be mangled.

## Do not reach for `_tag`

It is Effect's discriminant, and there is an API over it for every case worth
having: `Result.isSuccess` / `isFailure`, `Effect.catchTag` and `catchTags`,
`Match.tag` / `tags` / `tagsExhaustive`. These are type guards and narrowing
combinators, so they do something `result._tag === "Failure"` does not — the
value narrows and its payload is reachable without a cast.

`no-underscore-dangle` is therefore left on, deliberately. It was briefly given
an allowance for `_tag`, and that was the wrong fix: the rule firing was correct
and the code was reaching past an API that already existed. If it fires again,
the combinator is the answer.

`no-redeclare` **is** off, and that one is a genuine false positive:
`export const SessionInfo = Schema.Struct(…)` beside
`export type SessionInfo = typeof SessionInfo["Type"]` is the schema idiom, and
a value and a type sharing a name is legal TypeScript. `tsc` catches a real
redeclaration; the lint rule only sees the shape.
