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

**`-R` does not walk up.** The rule above says jj finds a repo by walking up
from cwd, and `-R` is how that is prevented — which also means a directory
_inside_ a repository is not a repository as far as `-R` is concerned:

```
  jj -R ~/code/thicket/src root   Error: There is no jj repo in ".../src"
  jj -R ~/code/thicket     root   /Users/…/code/thicket
```

Every call in this repo passes `-R`, so nothing here ever gets the walk. That
is right for the daemon and wrong for the one place a _person_ names a
directory: importing a project. `nearestRepo` in `projects.ts` climbs to the
first ancestor holding `.jj` before `sourceRoot` is asked anything, and the two
are not interchangeable — the climb is what makes a subdirectory work at all,
and `sourceRoot` is what stops a secondary workspace being recorded as though
it were the project it is a checkout of.

It was found by a probe against a real daemon and could not have been found by
a test: the fake `Jj` answers `/repos/<basename>` for any string, so a
subdirectory resolves there and the whole path passes.

**A project marker is `.jj`, not `.git`.** The walk that offers candidates
looks for one thing, and the reason is the same as the reason the thread-base
picker offers only local bookmarks: every operation awp performs on a project
is a jj one, so a git-only repository is a row that fails on import. Counted
on this machine, under the same roots:

```
  .jj or .git   56 candidates     most of which awp cannot act on
  .jj only      16
```

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

### A project is a claim, not a consequence of a session

The window used to derive its project list from the running sessions, which
made a project exist _because_ something was running in it. That is backwards:
the moment somebody wants to name a project is usually the moment nothing is
running in it yet.

```
  before   projectsOf(sessions)     the picker was empty exactly when it
                                    was opened — the first thread in any
                                    repository could not be started at all
  after    ProjectList              imported rows, plus what the sessions
                                    still imply, merged in the daemon
```

**Merged in the daemon, not the window**, because only the daemon holds both
halves and the two can name the same repository. The imported row wins: it is
the one that survives a restart and the one `forget` applies to.

**Forgetting takes nothing with it** — no workspace removed, no session killed,
no thread touched. That is what makes it safe to offer beside a name in a
picker. A project with sessions still running simply reappears, derived, which
reads correctly: awp does still know about it, it is just no longer claimed.

**The name is the basename, and that is the identity.** `sessionName` composes
`awp.<project>.<workspace>.<kind>`, the sidebar groups on it and the address
carries it, so two repositories with one basename are refused rather than
disambiguated — there is nowhere to put the second, and inventing `widgets-2`
would make an address nothing else in the system would ever produce.

**Two routes in, and the order they are drawn in is the order they are worth.**
A path works on any machine with no configuration; `deck.project_roots` is a
convenience over it and is empty for most people. Leading with the found list
would make the panel look broken for anybody importing their first project,
which is everybody the feature exists for.

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

### Two config files, and the project wins outright

```
  ~/.config/awp/config.json    global — the agent, the bookmark prefix
  <repo>/.awp/config.json      per project — how this repository is set up
```

Merged **per field, replace-if-empty** — not deep, not concatenated. That is
what the Go implementation does and both files on this machine were written
against it. A project that says nothing about hooks inherits the global ones; a
project that lists one inherits none of them, which is the only way a repository
can turn a global hook off. `[]` and an absent key mean the same thing, so "run
nothing" is not currently expressible; the day it needs to be, `merge` is the
line that changes.

**Read from the source repository, never from the new workspace.** `.awp/` is
untracked, so a fresh `jj workspace add` has no copy of it — the Go
implementation symlinked one in for exactly this reason. `input.repo` is the
repository the workspace was made _from_, and that is where a project's own
config actually is.

### A hook is a line, an agent is a program

`hooks.bootstrap` is whatever should run in a new workspace before its agent is
briefed. It goes to `sh -c` **whole**, and that is the opposite of what `agent`
does with the same file:

```
  agent            "claude --model opus"   split on whitespace → argv
  hooks.bootstrap  "mise trust"            handed to a shell, entire
```

Both are right for what they name. An agent is a program awp launches; a hook
is a line a person writes, and `&&`, a glob and a quoted path are its ordinary
furniture. Splitting one on whitespace produces nonsense.

**`zmxChildEnv()`, again.** A hook is free to run zmx — plenty of people's
bootstrap starts a server or opens a shell — and a child that inherits
`ZMX_SESSION` resolves it and switches the _calling_ client, which is whatever
session the daemon is running in. `bootstrap.test.ts` asserts on what the child
**prints**, not on what was handed to it, which is the only way to know:

```
  marker=[] set=yes
            └─ present and empty. Absent would print `set=` — and absence is a
               request a spawner is free to ignore, which is the bug that shipped
```

**The step sits after `session` and before `brief`**, and neither neighbour is
arbitrary. After the session, because `bun install` on a cold cache takes
minutes and there should be something on screen while it does. Before the
brief, because briefing an agent into a workspace with no dependencies asks it
to discover and fix that itself, which is the thing hooks exist to stop.

**A failing hook fails the job**, and the compensation takes the workspace back
to nothing. Logging it and carrying on was the alternative and is worse: it
produces a workspace that reports success and does not work, and the person
finds out from the agent some minutes later, in a message about something else.
Later hooks do not run once one has failed — each may depend on the one before
it.

**No undo, and it needs none:** everything a hook wrote is inside the workspace
directory, which the `workspace` step's undo removes. A hook that reached
outside it is beyond what this job can reason about, and an undo that pretended
otherwise would be worse than saying so.

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

### Anything that appears or disappears is animated

**A mandate, like the keyboard one.** Every show and hide in this window moves:
a column folding, a panel sliding in, a list collapsing, a tree opening over a
patch. Nothing pops.

The reason is not decoration. A thing that vanishes between two frames leaves a
person to work out _what_ just changed and _where the thing went_, and that
work happens every single time. A thing that moves has already answered both by
the time it has finished — which is why the columns were animated first and why
the same treatment kept getting asked for everywhere else, one control at a
time.

```
  FOLD_MS = 260                         columns.ts. One duration for the window.
  cubic-bezier(0.32, 0.72, 0, 1)        out fast, in gently
  @media (prefers-reduced-motion)       0s
```

Four rules that follow, each of which was learned by getting it wrong:

- **One duration and one curve, from `columns.ts`.** Two animations in one
  window that disagree about how long a fold takes read as two applications.
- **Reduced motion means none, not less.** Somebody who has asked their system
  for less motion is not asking for a faster version of it. Every eased style
  carries the media query; a transition without one is a bug.
- **A gesture is not animated.** A transition on a dragged boundary makes the
  thing chase the pointer a frame behind, which reads as lag rather than as
  motion. So the eased style goes _on for the toggle and off for the drag_ —
  held in state for `FOLD_MS` and removed — rather than living on the element.
- **Animate a property that can be animated.** `display: none` cannot, and
  neither can a conditional render — a component that is not in the tree has
  nothing to transition. Either keep it mounted and move `opacity` and a
  `transform`, or hold the unmount until the transition has finished.

**And a dynamic style, not a static one.** `${FOLD_MS}ms` inside
`stylex.create` is a build error about theming rules — an identifier in a
static style is resolved by StyleX and must come from a `.stylex.ts` file. A
dynamic style takes the value at runtime and asks no such question. This has
been walked into twice; see the note further down on StyleX failing quietly.
**No gate catches it** — fmt, lint, typecheck, test and doctor are all green on
the broken file, because only Vite runs the StyleX Babel pass. Fetch the module
from the dev server and grep it after touching styles.

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
- **The terminal claims to be a text field, and must not be treated as one.**
  These chords have to be given up inside `<input>` and `<textarea>`, because on
  macOS ctrl+h, ctrl+j and ctrl+k are the emacs bindings there — and the pane's
  keyboard surface is a `contenteditable` div with `role=textbox`, which is
  correct of it and is how an input method reaches the emulator. A plain
  `isContentEditable` test therefore reported "editing" for the whole agent
  column and every chord did nothing:

  ```
    keydown seen      KeyL, ctrlKey true
    defaultPrevented  false     ← the listener returned before acting
  ```

  No error, no visible failure, focus simply staying where it was. Ask _where_
  the element is rather than what it claims to be — `navigation.ts` answers the
  agent column by its `data-column`.

`ctrl+j`/`ctrl+k` step through `[data-nav-item]`, which is opt-in. The
alternative — every focusable element — steps through hover-revealed row
controls and toolbar buttons, and a list nobody can predict is not navigation.
A column that marks nothing still receives focus; it just has nothing to step.

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

## A default that reads like "on" and means "if you provided one"

The diff panel felt chunky to scroll and slow to change revision, and none of it
was the daemon: `jj diff` answers in 40ms against a real repository. Every file
was being tokenized **on the main thread** — the same thread the terminal's
render loop, React and the pointer handlers are on — because nothing had put a
worker pool in the tree.

Nothing says so. `@pierre/diffs` takes a prop called `disableWorkerPool` which
defaults to `false`, and that reads like the pool is on unless it is turned off.
What it means is in the library's own source:

```
  const poolManager = useContext(WorkerPoolContext);
  … new CodeView(options, !disableWorkerPool ? poolManager : undefined, true)
```

No provider means no context, an absent pool, and a silent fall back to
highlighting where you stand. The general shape: **a negative flag defaulting to
false says nothing about whether the thing it names exists.** Grep for the
provider, not for the flag.

Three things about the fix, each of which replaced something that did not work:

- **The pool is a module, not a component.** The library ships
  `WorkerPoolContextProvider`, and it builds the pool in `useState` and
  terminates it in an effect cleanup once the last one unmounts — while
  `terminateWorkerPoolSingleton` clears the singleton, so the manager still held
  in that state has no workers and no way to `initialize` again. StrictMode's
  mount/unmount/remount rehearsal walks straight into it. `highlighting.tsx`
  builds the pool at module scope and only _publishes_ it.
- **It wraps the window, not the panel.** Base UI unmounts a hidden tab, so a
  provider inside the diff panel would build and destroy the pool every time
  someone looked at jobs instead.
- **A worker is addressed by URL, and a bare specifier is not one.** The
  library's worker entry is published with bare imports for a bundler to
  resolve, so it cannot be handed to `new Worker` directly. A one-line local
  module that imports it can — Vite follows a relative URL and emits a real
  worker bundle.

Measured after, and the first line is the one that matters because it is the
only one that distinguishes a working pool from no pool at all:

```
  workers spawned          3     ← counted by patching `window.Worker`
                                   before any app code ran
  revision click → redraw  119ms
  errors                   []
```

**Count the workers.** There is no visible difference between a pool that is
working and no pool: the same pixels arrive, later. A screenshot cannot tell
them apart and neither can a stopwatch on a small patch.

## A render during a gesture ends the gesture

The diff's line selection supported dragging the whole time. What did not
survive was the element being dragged over.

An item's `version` keys the renderer's cache, so a changed version rebuilds
that item's DOM. The comment composer is an annotation on the item, so opening
it changes the version — and opening it at _pointerdown_ rebuilt the rows the
pointer was still moving across:

```
  pointerdown line 4   selection 4–4  →  composer  →  the item rebuilds
  pointermove line 9   nothing left to track
  pointerup            "line 4"
```

Nothing about that reads as a bug in the drag, and the one gesture that kept
working is why it stayed hidden:

```
  drag the numbers    line 4      ← settles mid-gesture
  click, shift-click  lines 4–9   ← two gestures, a settled render between
  drag the +          lines 4–5   ← the rebuild caught it two lines in
```

The rule: **render at the end of a pointer gesture, not during it — but only
where the render would change an item's identity.** A re-render is cheap. A
re-render that changes a `version` is a rebuild, and a rebuild is what a
gesture cannot survive. So the panel holds two selections:

```
  live       every pointermove   →  selectedLines  →  the blue band
  selection  pointerup only      →  the annotation →  a new item version
```

Two things about the shape of this, both learned by getting it wrong first.

**A flag raised in `onLineSelectionStart` is one call too late.** The library's
wrapper calls `onSelectedLinesChange` _first_ and the bracket callback after
it, so the composer was already open by the time the flag went up. That fix
changed nothing at all, which is the useful half of the finding.

**Passing `selectedLines` at all is what makes the selection controlled** —
`controlledSelection = selectedLines !== undefined`, and `null` is not
undefined. In controlled mode the renderer stops painting its own highlight and
waits to be told, so ignoring the intermediate ranges left the drag working and
_invisible_. It was reported as "it works but i cant see as im drgging", which
is a sentence about a feature that was measured as passing.

Both of those are the same shape as the worker pool two sections up: a default
that reads as "on" until the source says otherwise. Read the wrapper, not the
prop's type.

**Piercing the shadow root: Playwright's locators do, `page.evaluate` does
not.** A first attempt to check the highlight ran `document.querySelectorAll`
inside `evaluate`, found nothing, and would have read as "no highlight" in
every state including the working one. Walk `el.shadowRoot` explicitly:

```
  idle       marked 0
  dragging   marked 12, numbers 4–9, bg lab(39 -11 -5)   ← the band
  after      marked 14  (the composer's own rows join)
```

**The probe that first said dragging worked was wrong**, in the ordinary way:
it drove three gestures on one page, and the second and third read the composer
the first had left open. One page per gesture, or measure nothing. Playwright's
WebKit does deliver real `pointermove` with a stable `pointerId` — that was
checked by counting events from an init script before blaming the harness.

## One boundary per column, and the message has to be copyable

A single error boundary at the root is the same thing as no boundary: the whole
window is replaced by a message and whatever was being looked at is gone with
it. What made this worth building was the diff panel throwing on a bad
option — the sidebar was fine, the terminal was fine, and all three went white.

So the granularity is _the part a person can carry on without_: each column
wraps its own, and the newest code — a panel — is the one that fails.

```
  sidebar     fails → the other two columns still work
  agent       fails → the terminal is the point, but the diff can still be read
  accessory   fails → the common one. Panels are where the new code is.
```

**The report is selectable and there is a copy button**, and that is the whole
feature rather than a nicety. A stack trace that cannot be copied is one that
gets retyped from a photograph or described in prose. Two details:

- **`componentStack` arrives at `componentDidCatch` and nowhere else** — it is
  not on the Error — so it is kept in state rather than looked up later. It is
  the most useful line in the report, because it names the component that threw
  rather than the frame the throw happened in.
- **Check selection by selecting, not by reading the declaration.**
  `getComputedStyle(el).userSelect` came back as the empty string under WebKit
  while `-webkit-user-select` was `text`. Asserting on the unprefixed property
  would have reported the feature broken when it works:

  ```
    user-select           ""       ← would have read as "not applied"
    -webkit-user-select   "text"
    triple-click          selects  ← the only one that answers the question
  ```

`Boundary` is a class, and has to be: `getDerivedStateFromError` and
`componentDidCatch` have no hook equivalent. Everything it renders is a
function component.

Proved by forcing a throw and looking, which is the only way: the fallback
appeared, named `Diff`, centred **in the 280px column rather than the window**,
and the sidebar's four rows and the terminal's canvas were both still there.

## Latte's accents are not text colours

The window was reported as "very gray and boring and low contrast and hard to
see". It was not short of colour — it was full of colour nobody could see.
Every chrome hue measured against its own base:

```
  latte                          macchiato
    text     6.57  AA             text     10.85  AAA
    muted    2.63  FAIL           muted     2.60  FAIL
    live     2.75  FAIL           live     10.03  AAA
    accent   2.45  FAIL           accent    8.33  AAA
    waiting  2.15  FAIL           waiting  11.16  AAA
```

4.5 is the threshold for text and 3.0 for a mark, so Latte failed both on
everything but its body text. **Catppuccin's Latte palette is tuned to be an
accent on a light surface, not ink on one** — that is upstream working as
intended, and taking its hexes at face value for text is the mistake.

The fix is the smallest one: each Latte hue darkened **along its own hue and
saturation** until it clears 4.6, rather than a different palette. Macchiato
needed nothing, which is the asymmetry a dark-first palette has and nobody
notices until they measure.

This is allowed in `tokens.stylex.ts` and would not be in `palette.ts`. The
pane's sixteen slots have to be upstream's exact hexes or a program picking
colours against them looks wrong. The chrome answers to nothing but this app,
and that file already said so.

**Compute contrast off the rendered element, not off the source hex.** A token
can be right and the rule applying it wrong, and only sampling
`getComputedStyle(el).color` against the painted background tells the two
apart. The probe does it in the page.

### Two families, and the line is address versus prose

One monospace for everything is the terminal habit, and it is what made the
furniture look like output. The line is not "chrome versus pane":

```
  mono   a slug, a bookmark, a revision, a path, a command, the pane
  ui     a title, a label, a heading, a count, a sentence, a button
```

A slug is a thing somebody will type somewhere else, and the monospace says so.

**A font stack that misses fails in silence** — the same shape as the React
Compiler that was not running and the worker pool that had no workers. Three
candidates were tried before the two that shipped, and only measurement told
them apart:

```
  system-ui / -apple-system / 'SF Pro Text'   the same face, resolves
  'Helvetica Neue'                            resolves
  Inter                                       NOT INSTALLED
  'New York'                                  NEVER RESOLVES
```

New York is at `/System/Library/Fonts/NewYork.ttf` and the family name does not
resolve in the web view, so every rule naming it fell through to Georgia while
reading as applied.

**Ship the face, do not name it.** The only way a font is certainly the one on
screen is to bundle it. `apps/amoeba/src/renderer/fonts.css` declares the faces
by hand from `@fontsource-variable/*` rather than importing those packages'
`index.css`, which declares every subset published — 1.9MB for Inter alone
against 189KB for the four latin files actually wanted. `unicode-range` is what
makes latin-ext free until a character in it appears, and that was measured:
only the two latin subsets are ever requested.

The bundled family names carry `Variable` — `Inter Variable`, not `Inter` —
which is what Fontsource declares and is also what makes the check meaningful:
neither name is installed on any machine here, so a probe finding them proves
the bundle rather than the system happening to have the face.

**Check the build as well as dev.** Vite emits the woff2 into
`dist/renderer/assets` and the built app loads them over `views://`, which is a
different loader from the dev server. What matters is that the emitted CSS
references them relatively:

```
  url(./inter-latin-wght-normal-Dx4kXJAl.woff2)     ← relative, so views:// resolves it
```

**Measure by rendering, and use a real element.** `getComputedStyle().fontFamily`
echoes the declaration back whether or not anything in it exists, and canvas
`measureText` reported every family as the same width — including ones that
certainly exist — so it was measuring its own fallback. Render a string in the
candidate and in a family nobody has, and compare:

```
  'NoSuchFaceAnywhere'   371.05    the control
  'New York'             371.05    ← identical: never found
  Georgia                398.81
  'JetBrains Mono'       528.00
```

**Width is a real criterion, not a nicety.** The sidebar's caption line lives in
a 260px column, and a monospace spends about a third more of it on the same
words. That is what settled prose on SF Pro rather than on any of the eleven
monospaces installed on this machine.

**Changing the pane's face invalidates its size.** ghostty-web sizes a cell as
`ceil(measureText("M").width)`, so the padding left in each cell is a property
of the _font_, and `paneFontSize`'s note was written about Maple Mono. It was
re-measured rather than carried over — JetBrains Mono wastes 1.7% at 18px where
Maple Mono wasted 7%.

### The type floor is 14px, and it is about text

Stated as a requirement — "stop using such tiny fonts in headers my eyesight
isnt amazing nothing smaller than idk 14" — so the scale is built around it
rather than clamped afterwards. It cost a step: 15/14/13/12/11 put four of its
five sizes under the floor, and raising them collapses the bottom two. Four
readable steps beat five where two are not, and a caption is then separated by
**weight and colour** instead of by size — which is the better axis anyway,
since size is the one that trades legibility for hierarchy.

The floor is about text. A status bullet is sized against the name beside it,
and an icon's `font-size` is its em box; both are legitimately smaller. Checked
rather than assumed — everything in the window under 14px is one of those two,
and a _word_ appearing in that list is a bug.

### An accent is spent, not applied

The first pass put the orange on every thread heading, which is a second body
colour: with an accent on everything, nothing is left to mark the one row that
matters. It is now on exactly two things that answer "this, here" — the
selected row's edge and the selected tab — plus the pull request number, which
earns it by being the only thing on the strip pointing outside the window.

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

The pane recolours through `setPaneTheme`, and **it can only ever half work in
ghostty-web 0.4.0** — the library says so itself, in the option handler nobody
had read:

```
  case "theme":
    console.warn("ghostty-web: theme changes after open() are not yet fully supported");
```

The colours are compiled into the wasm terminal when it is built.
`buildWasmConfig` hands the emulator `fgColor`, `bgColor`, `cursorColor` and the
sixteen-colour palette, and the only thing that rebuilds that config is
`reset()` — which frees the wasm terminal and makes a new one, taking the
scrollback with it. For a pane watching an agent work that is a worse outcome
than the wrong colours.

So `setPaneTheme` repaints the _renderer's_ half: the ground, and any cell whose
colour is the default rather than one the program asked for. `clear()` fills the
ground and `render(buffer, forceAll)` redraws every line — both public, and
`render(…, true)` is exactly what the library calls on open. Counted on the
fixture, latte-base pixels after switching to light:

```
  canvas.width = 0            0     ← the nudge this replaced
  clear() + render(forceAll)  263
```

Three things worth keeping.

**The nudge never did anything.** Setting `canvas.width = 0` to put the canvas'
pixel size in disagreement with the renderer's metrics reads in the source like
the one full redraw reachable from public API. It is not one, the canvas returns
to its own size, and not one pixel changes. That was written into this file as a
finding without a pixel ever being sampled — the general shape being that a
mechanism read out of someone else's source is a hypothesis.

**A single corner pixel is the wrong probe, and it cost an hour twice.** The
fixture draws colour ramps and blocks, so the corner is whatever the fixture
painted there rather than the theme's ground — it reads "unchanged" for a swap
that worked and for one that did nothing, alike. Count the canvas' most common
colours instead:

```
  dark   [["36,39,58", 11052], …]
  light  [["36,39,58", 10793], ["239,241,245", 263], …]
                                └─ the ground that did recolour
```

**A reflow does repaint**, because `Terminal.resize` calls
`renderer.render(wasmTerm, true, …)` outright — but it early-returns on
unchanged dimensions, so it cannot be used as a repaint: resizing to `rows - 1`
and back fires `resizeEmitter` twice and reflows the real session.

What is left of task #23 is the patch. `patches/ghostty-web@0.4.0.patch` already
exists, and a `setTheme` that rebuilds the wasm config while keeping the buffer
is where it goes.

## A browser probe attaches to a session, and resizes it

The zmx rules above are stated in terms of `zmx` commands, and there is a third
way to reach a session that runs none of them: **drive the window**.

A Playwright probe that opens `#/w/<project>/<workspace>/agent` is a client
attaching to that session, through the daemon, with the probe's viewport as the
size. A session takes its size from whoever is looking at it — so every probe
reflowed a real terminal somebody was working in, to whatever the agent column
computed to at 1400x900.

```
  guarded    zmx attach · zmx kill        refuse, or strip the marker
  guarded    the daemon spawning zmx      zmxChildEnv()
  NOT        a headless browser opening
             a route that names a session ← this one, and it looks like nothing
```

Nothing about it reads as touching a session. There is no `zmx` in the script,
no `ZMX_SESSION` to strip, and the tell arrives somewhere else entirely: a
person's terminal reflowing while they type in it, minutes after a probe ran.
It was reported as "something you keep doing keeps reflowing this into a very
narrow window", which is not a sentence anything in the repo would have
produced.

So, for any probe that drives the renderer:

- **Open `#/`.** The fixture needs no session and attaches to nothing. It is
  enough for every question about layout, theme, scrollbars and the pane's own
  rendering.
- **When a route with a session is genuinely needed**, name a workspace this
  repo created for the purpose — never one a person is working in. The same
  `ours()` shape `probe:workspace` already uses: a guard on the property that
  matters is stronger than a blanket refusal.
- The general rule, again: **when a guard's effect happens in someone else's
  process, assert on what that process sees.** The daemon is the process that
  attaches, and no assertion about the probe's own environment could have said
  so.

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

- **The traffic lights stay, and a tiling window manager is why.** The window
  is `hiddenInset`, so the controls float over the first 5.25rem and the top
  bar's start padding clears them.

  `titleBarStyle: "hidden"` was tried. It works — the lights go, the Window
  menu covers close and minimise, the drag region moves it — and it was
  reverted within a minute, because **AeroSpace stopped managing the window**.
  That is not a bug in either program: a tiler picks windows out through the
  accessibility API and skips anything that is not a _standard_ window, and an
  untitled window is not one. On a 3440x1440 display the difference is the
  whole point of the display:

  ```
    hiddenInset   3424x1393    tiled to the screen, less the gaps
    hidden        whatever it was last dragged to
  ```

  Three circles are a small price for being a window somebody's tiler will
  manage, and the person running the tiler is the person using this.
  `trafficLightOffset: { x, y }` is the knob actually available if they are in
  the way — it moves them, it cannot remove them.

- **The top bar is the drag handle, and the CSS property is not what makes it
  one.** This is worth reading before trusting it, because an earlier version
  of this note was wrong in the way that is hardest to catch: it said
  `-webkit-app-region: drag` was the mechanism and told you to check the built
  CSS. The CSS _is_ emitted. Nothing reads it.

  Electrobun's preload matches on the DOM, and on exactly two things:

  ```
    target.closest('[style*="app-region"][style*="drag"]')   an INLINE style
    target.closest(".electrobun-webkit-app-region-drag")     its own class
  ```

  StyleX produces neither — it produces a class of its own plus a stylesheet
  rule. So the property had never moved this window. It moved because
  `hiddenInset` left a real title bar behind the strip and AppKit was doing the
  work, and the bug was invisible for exactly as long as that title bar existed.

  So the bar wears `electrobun-webkit-app-region-drag` as well, and everything
  interactive in it wears the `no-drag` counterpart. `withRegion` in `Bars.tsx`
  appends the class to whatever `stylex.props` returned.

  The general shape, which has come up here more than once: **a declaration
  being emitted is not evidence that anything consumes it.** The same mistake
  as the worker pool that had no workers and the React Compiler that was not
  running. Grep for the reader, not for the declaration.

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

## A native webview does not stack

The web panel is a real WKWebView, drawn by another process over the top of the
renderer at a rectangle it is told to occupy. An `<iframe>` was the shorter
answer and the wrong one — most of what a person wants beside an agent sends
`X-Frame-Options` or a `frame-ancestors` policy and renders as a blank
rectangle with a console error nobody sees.

What that costs is the thing every React instinct gets wrong: **it is not in
the stacking context, so nothing rendered here can be in front of it.** There
is no `z-index` that wins, because the layers are in different processes.

```
  ┌──────────────────┐  the page          ← another process, always on top
  │ ┌──────────────┐ │
  │ │ the dialog   │ │  the renderer      ← under it, whatever it says
  │ └──────────────┘ │
  └──────────────────┘
```

Nothing about it reads as a stacking problem from the dialog's side. The
backdrop dims, focus moves in, Escape closes it — every part works except the
one that shows it to a person.

The tag offers two repairs and they are not interchangeable:

```
  toggleHidden(true)   the whole webview stops being drawn
  addMaskSelector(s)   holes cut where `s` matches, recomputed every 10ms
```

A mask suits something small overlapping a corner. A modal is not that — it
makes the rest of the window inert, so there is nothing left for the page
underneath to be useful for, and the mask would end up the size of the panel.
So `overlays.ts` holds a **count** of open modals and the panel hides on it.

Three things about that count, each of which was a way to get it wrong:

- **A count, not a flag.** A select inside a dialog portals out of it, so two
  are open at once and the inner one closes first. A boolean lets that clear
  the outer one's claim.
- **Releasing is guarded against running twice.** StrictMode rehearses mount
  and unmount. A count that goes negative never reaches zero again for the
  overlay still open, and the page stays hidden for the life of the window.
- **The dialog announces itself; nothing detects it.** The panel cannot see a
  portal outside its own subtree. A row's `⋯` menu is deliberately _not_
  registered — it is in another column, and blanking the browser for it would
  read as a bug in the browser.

Unhiding forces a resync. While hidden the element's rectangle is zero and the
tag's own loop polls at 100ms, so the page otherwise returns a tenth of a
second late, which reads as the panel being slow to wake up.

**Verified by stubbing the tag, because the native half cannot be driven.**
Playwright has no Electrobun, so `customElements.define("electrobun-webview",
…)` runs in an init script before any app code and the calls are counted:

```
  on mount   no calls                              ← nothing spurious
  cmd+N      toggleHidden true
  escape     toggleHidden false, syncDimensions true
```

That proves the renderer half — that the panel learns a modal opened and says
so. Whether the native side then stops drawing is still unwatched, and the
panel says which of the two paths it is on the moment it is opened: outside
Electrobun `customElements.get` answers undefined and it says so in words,
because an empty box is also what a page that failed to load looks like.

## An orphaned webview cannot be closed by anything

The first real bug the web panel produced, and it is worth stating in full
because nothing about it is recoverable at runtime: a webview stuck in the
top-right corner of the window, over everything, unmovable, that survives every
tab switch and every reload and goes only when the process does.

The lifecycle has two awaits in it, and `disconnectedCallback` guards on a
field that neither has set yet:

```
  connectedCallback()      requestAnimationFrame(() => this.initWebview())
                                        ↑ one frame
  initWebview()            await request("webviewTagInit")
                                        ↑ a round trip to the native side
                           this.webviewId = id          ← only set here

  disconnectedCallback()   if (this.webviewId !== null) send remove
                                        ↑ null for the whole window above
```

An element removed inside that window has already run its
`disconnectedCallback` — with nothing to remove. The native webview then
arrives and attaches itself to a **detached** element, which is not in the
document, so no further `disconnectedCallback` will ever fire for it. There is
no reference left that anything can reach: not `toggleHidden`, not the sync
loop, not a re-render. It floats at the rectangle it was born with for the life
of the process.

**StrictMode walks into this on every mount** — create, clean up, create again,
all inside one frame — so the panel's first open orphaned one every time. The
corner it appears in is not a clue about the bug; it is just where the
accessory column was.

It cannot even be nudged back into place: `OverlaySyncController.sync()`
returns early when the rect is zero by zero, which is exactly what a detached
element reports. So it keeps its birth rectangle and no later layout reaches
it.

`patches/electrobun@1.18.1.patch` fixes it at the source, because nothing
outside the element can: a `_detached` flag set in `disconnectedCallback`, and
checked twice — after the rAF, and after the request returns, where an arriving
id is now removed rather than adopted.

Two things this is not.

**It is not an argument for Electron.** Electron's `<webview>` is discouraged
in its own docs, and the replacement — `WebContentsView` — is also a native
view positioned over the page by hand, with the same "does not stack" property
and the same detach-during-init shape. The bug is a missing guard, not an
architecture.

**It is not fixable from the consuming side.** Every repair available to a
React component makes it worse: removing the element fires the callback that
does nothing; re-appending it to rescue it fires `connectedCallback` and
creates a _second_ native view; keeping a module-level singleton and
re-parenting it fires both. Moving a node between parents is a disconnect and a
connect, and this element cannot survive either.

**The preload is a pre-compiled string, and that is what ships.** Patching
`dist/api/bun/preload/webviewTag.ts` alone changes nothing at all: the build
bundles `dist/api/bun/preload/.generated/compiled.ts`, which holds the whole
preload as one escaped JS string. The first patch here was written, committed,
built and launched, and the running app was byte-identical to one built without
it — the same tell as the React Compiler that was not running. Patch both, and
check the built bundle rather than the source:

```
  grep -c "_detached" apps/amoeba/build/dev-macos-arm64/\
      amoeba-dev.app/Contents/Resources/app/bun/index.js
```

Restarting is not enough either — `electrobun dev` rebuilds, so the app must be
rebuilt, not relaunched. And an orphan already on screen is not cleaned up by
any of this: there is no handle left to clean it up with, so the process has to
go.

The general shape, which has come up here before: **a cleanup that guards on a
field set by an async step does not run during that step.** The guard reads as
"nothing to do yet" and means "do nothing, ever".

## On macOS a paste is a menu item before it is a key

Dictation into the pane produced a small native paste menu beside the cursor
and nothing else. That prompt is the whole diagnosis, and it points at
something this window was missing entirely.

cmd+V is not a key the way ctrl+j is. It is the **key equivalent of a menu
item**, and AppKit turns it into the `paste:` action only if some menu item
claims it. This app had no menu bar at all, so cmd+V arrived at the web view as
an ordinary keydown and nothing pasted. `clipboard.ts` worked around that by
reading the clipboard itself:

```
  navigator.clipboard.readText()      ← WebKit gates this behind a prompt
```

A person can click that prompt. **Dictation cannot.** Handy transcribes speech,
puts the text on the clipboard and synthesises cmd+V; a permission prompt is a
wall it has no way through. So the symptom was a paste menu appearing when
somebody spoke.

`apps/amoeba/src/bun/menu.ts` installs the menu, and the roles map to
NSResponder selectors — `undo:`, `paste:`, `selectAll:` — so AppKit performs a
real paste and WebKit raises a `paste` event carrying the text. No permission,
no prompt, and route one in `clipboard.ts` already handled that event.

Three things about it worth keeping.

**It was never only the pane.** Every text field in the window had the same
hole — the address bar, the thread composer, the diff comment box. cut, copy,
paste, select-all and undo are supplied by the system to any focused field once
the items exist, and none of them worked. A macOS app with no Edit menu is
broken for text everywhere in it, not just where somebody noticed.

**A menu is a set of claims on the keyboard.** No File menu and nothing on
cmd+N: that chord opens the new-thread composer, and a menu item claiming it
would take the key before the renderer ever saw it.

**The accelerators are spelled out.** Nothing in electrobun's JS layer assigns
a default one, and a Paste item with no key equivalent looks completely correct
in the menu bar while fixing nothing.

The keystroke route is now a fallback rather than the plan, and it defers
instead of deciding, because there is no way to ask whether the chord is
claimed:

```
  a Paste item exists   AppKit runs paste: → a `paste` event → route one
  none                  nothing arrives; after 120ms, ask for the clipboard
```

The macOS chord is deliberately **not** cancelled — cancelling the keydown is
exactly what stops the system pasting. The two non-macOS chords still are,
because nothing turns those into a command and there is nothing to wait for.

## A terminal listens for keys, and not everything that types is a keyboard

Dictation into the pane did nothing. Not an error, not a dropped character —
someone spoke and the terminal did not move, which is the same thing a broken
microphone looks like.

ghostty-web listens on the host element for `keydown`, `keypress`, `paste` and
the three composition events. That covers a keyboard and an input method. What
it does not cover is text _inserted_ into the document by something else —
dictation, an assistive tool writing on someone's behalf, a snippet expander.
All of those reach a page the same way: `beforeinput`, with the text in `data`
and no key event at all.

The host is `contenteditable`, so `open()` does this and nothing reads it
first:

```
  A.addEventListener("beforeinput", (E) => E.preventDefault())
```

Cancelled every time. `installDictation` runs in the capture phase to get there
ahead of it. The `preventDefault` is right, incidentally — the host must not
accumulate real DOM text under the canvas — what was missing is reading the
event before cancelling it.

**The trap is that everything is a `beforeinput`.** Ordinary typing raises one
too, and sending on both routes doubles every character a person types, which
reads as a broken keyboard and is worse than the drop. Nothing on the event
says "a key did this"; what there is, is the ordering — a key raises `keydown`
then `beforeinput` in the same task, an insertion raises `beforeinput` alone. So
a keystroke is remembered for two frames and an insertion inside that window is
taken to be its echo.

Two frames rather than the two tidier alternatives, both of which were tried in
thought and are wrong: a microtask checkpoint can run _before_ the input event
is dispatched, so the flag would already be down; and comparing `event.timeStamp`
assumes the engine copies the key's timestamp onto the input event it derives,
which nothing requires it to.

Composition is left alone. ghostty-web sends the finished text on
`compositionend`, so reading it here as well doubles a whole word.

**Measured, and the measurement is the point**, because both failure modes are
invisible from outside — a drop does nothing, and a double looks like hardware.
`page.keyboard.insertText` is exactly the dictation path: `beforeinput` with no
key event. The counts come off the meter panel, which now splits them, because
`inserted` staying at 0 while someone is speaking is the whole diagnosis and
there was nowhere to read it:

```
  insertText "dictated"   typed 0  inserted 8
  type "abcde"            typed 5  inserted 8    ← not 10; no double
  insertText "more"       typed 5  inserted 12

  without installDictation
  insertText "dictated"   typed 0  inserted 0    ← the reported symptom
```

On this host a keystroke raises no `beforeinput` at all, because ghostty-web
cancels the keydown — so the anti-doubling guard never fires in ordinary use
and could not be measured by typing. It was reached by dispatching a `keydown`
and a `beforeinput` by hand, which is the only way to a case the emulator
currently prevents and would stop preventing for any key it does not consume.

## Pointing at something in a page you do not own

The web panel's annotator — point at an element, say what is wrong with it,
send it to the agent — is the first feature that has to reach _into_ the
webview rather than position it. The whole design is the shape of the two wires
that exist, and there are only two.

```
  this window  ──  element.executeJavascript(js)   ──►  the page
  this window  ◄──  window.__electrobunSendToHost  ──   the page
                     arriving as a "host-message" event on the tag
```

**`executeJavascript` returns nothing.** It is `send`, not `request`, and the
native call under it is `evaluateJavaScriptWithNoCompletion`. So the picker
cannot be asked a question — it has to volunteer. That is why it is a script
that installs listeners and reports, rather than a function that is called.

**`__electrobunSendToHost` is on every page, and that is not this repo's
doing.** Every `BrowserView` — including the child a tag creates for an
arbitrary site — is given Electrobun's full preload, which defines it. Worth
knowing before designing around a page that "might not have it": it does.

**`host-message` is the page's channel, not this feature's.** Any script on any
site can put any object down it, and the native side `JSON.parse`s it before it
arrives. So every message carries a marker and `messageFrom` guards on it before
anything else. A cast there would put a stranger's object into a prompt typed at
an agent. Removing the guard fails a test that exists to say so.

**A stringified function is not the function you wrote.** The first shape was a
real function put through `toString()`, which is a trap in this repo
specifically: renderer files go through Vite, the React Compiler and StyleX's
Babel pass, and what comes out has minified names, hoisted constants and
references to a module scope the page has never heard of. A template literal is
the same string wherever it is read.

Three properties the injected script has to have, each a way to get it wrong:

```
  idempotent      it parks itself on window[KEY]; a second injection re-arms
                  the first rather than adding a second set of listeners
  removable       a highlight left painted over somebody's page is
                  indistinguishable from the site being broken
  non-destructive one absolutely-positioned div, and the clicks it consumes
                  are cancelled — picking "delete" must not delete
```

**`settle` and `disarm` are different, and collapsing them loses the feature.**
Clicking takes the listeners off but leaves the highlight: somebody is about to
type a sentence about that element and has to be able to see which one it was.
Only dismissing the note takes the paint off.

**Re-inject on `dom-ready`, not `did-navigate`.** The second says a navigation
was committed, which is before there is a `document.body` to append to. And
only while armed — putting a highlight back on a page somebody turned the
picker off for reads as the site doing it.

**The overlay is `pointer-events: none`, or nothing is ever hovered but the
overlay** — `elementFromPoint` would return it, over itself, forever.

Measured in a real WebKit page, because none of it is reachable from a fake:

```
  hover a 120x40 button   the highlight is 124x44 — the border, drawn outside
  click a link            navigated false, and a note sent instead
  click after Escape      navigated true — the page works again
  inject twice, click     1 message, not 2
  four picks              #title · #four · em · section:nth-of-type(2) > button:nth-of-type(1)
                          every one resolving to exactly 1 element
```

`em` rather than `main > div > span > em` is the selector rule doing its job:
walk up appending `:nth-of-type()` and stop at the shortest suffix that is
unique. A full path from `<html>` is correct and unreadable, and it breaks the
first time an unrelated part of the page changes.

**An id nobody wrote is not an address**, and the annotator's _first real use_
found it. Pointing at a tab reported `#base-ui-_r_0_` — unique in the document,
perfect today, and a different string on the next build, because React's
`useId` is a render-order counter. So an id is only preferred over a path when
it looks like something a person chose.

```
  base-ui-_r_0_ · :r3: · radix-:r1:   minted  →  fall back to a path
  jobs-tab · save · aria-live-log     kept    →  the best selector there is
```

`aria` was on the reject list and had to come off: people write `aria-desc-2`
by hand all the time, and throwing that away costs the one good anchor the
element had. **The list may only hold prefixes nobody would choose on purpose.**

The patterns are shared with the injected script as **regex literals, not a
stringified function** — a `RegExp`'s `toString` is specified to return its own
source, so it crosses the compiler boundary as data. Stringifying the function
would be the trap two paragraphs up, and it was written that way first.

**The probe imports the module rather than rebuilding the script.** The first
version read `annotate.ts` as text and re-did the interpolation by hand, which
broke the moment a third `${…}` was added — and, worse, would have gone on
passing while testing its own reconstruction. `annotate.ts` has no imports of
its own precisely so Bun can load it directly.

**A page note is not a review comment, and forcing it to be one would lie.** A
`ReviewComment` is anchored by `revision`, `path`, `side` and two line numbers;
a page has a URL and a selector. `NoteSend` is therefore its own call, and it is
**unbatched** where `ReviewSend` is batched — a review is six remarks written
while reading a diff, and a page note is one whole gesture with no second one on
the way. A draft that waits for a batch is a draft nobody remembers to deliver.

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

- **The renderer may not import a node builtin, and the lint says so.**
  `import/no-nodejs-modules` is on for `apps/amoeba/src/renderer/**` and
  `packages/pane/src/**`. This is the barrel hazard above given a gate: the job
  record is a Schema, so the contract imports it, so the renderer does — and
  `@awp-kit/jobs`' index reaching `sqlite.ts` broke the dev server outright,
  while a production build would have tree-shaken it and said nothing.

  The tsconfig project references remain the import graph between packages —
  `pane` importing from `server` is a compile error. What the lint adds is the
  case references cannot see: a legal import whose _transitive_ reach is a
  builtin the browser has never heard of.

  Checked by breaking it deliberately, because a guard whose removal changes
  nothing is not doing what it claims:

  ```
    import { readFileSync } from "node:fs";   in review.ts
    → error import(no-nodejs-modules): Do not import Node.js builtin module
  ```

  `import/no-cycle` is on repo-wide for the same reason `address.ts` is kept
  out of `routes.ts`: `App → routes → App` was a real risk and the reason that
  file exists.

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
