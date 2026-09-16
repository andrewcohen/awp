# Jobs (packages/jobs) — evidence

The measurements, probe output and wrong turns behind the rules in
`packages/jobs/AGENTS.md`. Nothing here loads into a session; it is read when a
rule is being questioned.

#### A hook is a line, an agent is a program

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

#### A step may write down what it learned

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

#### A thread branches from a bookmark, not from a working copy

`cmd+shift+N` starts a thread from the one on screen, and the obvious reading of
that is wrong in a way worth stating.

```
  andrew/tabular-exports   the bookmark — where the work is named,
                            moved when a person decides it should be     ← this
  tabular-exports@         jj's revset for the workspace's working-copy
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

#### Clearing is not clearing

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

#### Jobs: resume and compensation are the same disagreement

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

#### Making a workspace: the first job that does anything

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

#### One database, two runtimes, and named migrations

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

#### `send` returns before the answer, and that kills a briefed agent

The part that made the fix not work, and it is a bug that was already there.

`RcMap` releases a conversation two minutes after its last reference goes, and
releasing it kills the adapter. `send` returns as soon as the adapter accepts
the prompt — which is right for a person typing, because their window is
subscribed and something is holding it. **The create job has no window.** So a
brief delivered by `send` alone reaches the agent and then has it shot two
minutes into its first answer, which from outside is a model that gave up
mid-thought.

It is not new. A person who sends a message and switches to the diff tab
unmounts the chat panel — Base UI unmounts a hidden tab — which drops the
subscription, and a long answer dies the same way. The job made it certain
rather than likely.

`Chat.brief` is `send` plus holding the reference until the turn has ended, and
the caller's own wait is what does the holding. It is the last step of a job
that already spends minutes in `bun install`, and a step that waits is a step
the jobs panel can show — better feedback than a job that says succeeded while
the agent is still reading.

**Polled, not driven off the change stream.** `statuses` has one and
`settledWhen` does not use it, because what is wanted is a _settled_ reading
and the stream is a stream of edges: the status is absent both before a turn
starts and after it ends. An edge-driven wait either returns instantly on the
reading it began with or has to reason about which absence it is looking at.
Two reads a second for a few minutes costs nothing measurable.

**Two bounds, and neither fails.**

```
  startsWithin  30s   an adapter that accepted the prompt and did nothing with
                      it is a real thing — the whole reason `send` reports how
                      it was delivered. Without this the step hangs forever
  holdsFor      20m   a turn running for an hour is the agent doing what it
                      was asked, and a job has no business holding a step open
                      that long
```

Giving up is not a failure: the transcript is on disk, so somebody opening the
chat re-acquires the adapter and replays. A timeout that _failed_ would fail a
job whose work is already done.

`settledWhen` takes a reading rather than the ref, which is the only thing that
makes those two bounds testable — the real one is a `SubscriptionRef` fed by an
adapter, and there is no adapter in a test. The script `idle, working, working,
idle` is the shape that catches the hazard: a wait that returned on the first
idle reading passes every other check.

#### The brief goes where the person was looking

Reported as "i started it in chat mode yet it is running in terminal mode",
and the diagnosis is one line: the face was a **renderer preference**.

```
  the form       you pick "chat"  →  rememberFaceDefault("chat")  →  localStorage
  ThreadStart    description · project · thread · from · parent · base ·
                 model · effort            ← no face. The daemon was never told
  the brief step zmx send <the prompt>      ← the pty, always
```

So the choice decided which panel the window _drew_ and nothing else. The
other order is worse and is what makes this a wire field rather than a wider
default: had the window opened on the chat face, it would have shown an empty
conversation saying `nothing said yet` beside work happening in a terminal
nobody was looking at. Two agents, one briefed, one visible.

`Face` is on the contract now, `ThreadStart` carries it, and it is on the job
record — as `Schema.optional`, which is the rule for every field on that
record: the input is stored as JSON, JSON has no `undefined`, and
`UndefinedOr` requires the key. Absent means the terminal, which is what every
job enqueued before the field existed asked for by saying nothing.

**One step, two deliveries — not two steps.** The step list is fixed per kind
because the runner reads `done` back from the store and resumes against it, so
a list that varied by payload is a list a restarted daemon could not
reproduce. Same reason the bookmark is an optional _bookmark_ and never an
optional step.

**The session is still started for the chat face**, and that is a decision
rather than an oversight. The chat is a separate process from the pty, so a
workspace worked in the chat still gets a terminal — idle, at a prompt, for
whoever wants one. Skipping it would leave nothing to attach to from another
window and `zmx history` with nothing in it. Only one of them is briefed,
though: two agents told the same thing in one checkout is two agents editing
the same files.

**A closure, not the `Chat` service.** `chat.ts` imports `workspacePath` from
`create-workspace.ts` — it is the one place the workspaces convention lives —
so the job importing `Chat` would be a cycle, and `import/no-cycle` is on
repo-wide. `daemon.ts` wires one call, which is the one place both are in hand.

And it is resolved **there**, not reached for inside the step:
`Effect.flatMap(Chat, …)` would put `Chat` in the step's requirement channel,
and **a step's `run` has no requirements** — a step resumed by a restarted
daemon has no caller whose context it could inherit.

**One Chat, shared.** `Layer.provideMerge`, not `provide`: `provide` keeps the
dependency private to what it provided to, so a second `Layer.provide(chatLayer)`
under `jobs` would build a second `Chat`. Two would mean the job briefing a
conversation nobody is watching, which from outside is a chat that came up
empty next to work that had already been asked for.

#### The store is JSON, and that changes what a schema may say

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

#### Threads: the work, not the checkout

A thread is a piece of work; a workspace is a checkout, and one piece of work
often needs two of them.

```
  thread  "tabular exports"
    ├── rowan/tabular-exports   agent · editor · action
    └── beta/tabular-exports    agent · editor · action
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

#### Two config files, and the project wins outright

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

**The model, the effort and the mode are one block, and both faces read it.**
They were in two places and neither could be read: the model and the permission
mode were words inside the `agent` string — `claude --permission-mode auto
--model opus`, which is the _terminal's_ command line and says nothing to the
chat — and the effort was nowhere at all. So the chat ran on whatever the
adapter defaulted to while the terminal ran on opus, and no file said what the
machine's answer was.

```json
  "defaults": { "model": "opus", "effort": "medium", "mode": "auto" }
```

Named `defaults` rather than `chat` or `agent`, because it is one answer for
both faces and a block named after either is a second one waiting to disagree.
Three layers, and the order is the whole of it:

```
  the `agent` line     claude --permission-mode auto --model opus
  defaults             applied over it — a file that names a model in one place
                       and another on the command line contradicts itself, and
                       this is the clearer half
  the modal's choice    wins over both. "From settings" means choosing nothing,
                       which is why those are `undefined` and not a value
```

The terminal gets them as argv through `agentWith`; the chat gets them through
the adapter's own `session/set_config_option`, which is the only way that does
not lie — `configOptions` is what the open reply carries and what the panel
draws, so setting a model any other way leaves the chips reporting the opposite
of the truth. That already happened once, to the mode.

**`mode` absent leaves each face where it was**: the terminal keeps whatever the
`agent` line says and the chat stays in Manual, which is the decision `MODE`
argues for at length. Writing `auto` in the file is how somebody opts out of
being asked — a decision worth having to write down rather than inherit.

Read per conversation, not per daemon: `settings.ts` is deliberately read per
call, so an edit takes effect on the next chat opened without restarting a
daemon holding a dozen ptys.

**Read from the source repository, never from the new workspace.** `.awp/` is
untracked, so a fresh `jj workspace add` has no copy of it — the Go
implementation symlinked one in for exactly this reason. `input.repo` is the
repository the workspace was made _from_, and that is where a project's own
config actually is.
