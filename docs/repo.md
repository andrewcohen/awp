# Repo-wide — evidence

The measurements, probe output and wrong turns behind the rules in
`AGENTS.md`. Nothing here loads into a session; it is read when a
rule is being questioned.

### A browser probe attaches to a session, and resizes it

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

### A command's exit code is not in its output

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

### A field nobody checks is a field that can be renamed out from under you

`zmx ls` reports where a session was started. The parser switched on
`start_dir`; the zmx on this machine says `cwd`. An unknown key falls through
to `labels`, so **no parse ever failed** — `startDir` simply read as the empty
string for every session, for weeks.

Nothing downstream fails loudly on an empty directory either. It is a
`Schema.String`, so it crosses the wire as `""` rather than as absent, and
every reader of it has a guard shaped for the wrong value:

```
  allProjects()   skips a session whose directory is empty, so NOT ONE
                  project was ever derived from a running session
  suggestedBy()   recovers an unlabelled session's identity from its
                  workspace path — inert, on most of the sessions here
  App.tsx         `open?.startDir ?? elsewhere` — `??` does not catch "",
                  and the fallback was gated on a session EXISTING, so the
                  daemon was never asked for the workspace's directory
```

What that cost is two features that read the project list and quietly narrowed
to whatever had been imported by hand. The tasks panel answered 51 against a
`TODO.md` holding 42, for nine days, because `ingest` is scoped by key prefix
and the prefixes come from that list:

```
  before   startDir   0 of 16 sessions      ProjectList  2, imported only
  after    startDir  16 of 16               ProjectList  5, two derived
```

Three things worth keeping, and the second is the one that generalises.

**The fixture suite could not have caught it.** `zmx-parse.test.ts`'s fixtures
carry `start_dir` and are commented as real output captured on a particular
day. So the fixture and the parser agreed with each other, and both were wrong
about the tool. A fixture is a record of what a program said once; it is not
evidence of what it says now.

**So the guard asks the real tool, and asks for the property rather than the
spelling.** `zmx.test.ts` already runs against a live zmx for names, labels and
liveness — `startDir` was the one field that came from zmx and was never put
back to it. It now asserts that every session has an absolute directory,
whatever the field is called on the day, which is a test a rename breaks
instead of the project list. Checked by removing the fix, which fails it.

**An empty string is not an absent value, and `??` says otherwise.** The same
shape this file already records for `ZMX_SESSION` — set on the way out, treat
empty as absent on the way in — and for `NEVER` in the diff's fold state, where
`undefined` was a real revision. A guard written against `undefined` on a field
typed `string` is a guard for a case the type cannot produce.

Both spellings are accepted now, because a parser that knows one has been
silently wrong once and the second case is a line.

### A name cannot group a workspace

The sidebar lists **workspaces**; zmx lists **sessions**. A workspace has one
session per kind — an agent, an editor, a user action — and the temptation is to
recover the workspace by splitting `awp.<project>.<workspace>.<kind>` on dots.
It does not work, and the reason is not obvious.

`sessionName` gives the stem whatever budget the kind does not need, so one
workspace's sessions are shortened to **different stems**:

```
  awp.thicket.effect-ts-tabular-ca90.action_dev
  awp.thicket.effect-ts-tabular-expo-ca90.editor
  awp.thicket.effect-ts-tabular-expor-ca90.agent
        └─ three stems, one workspace: effect-ts-tabular-export-timemachine
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

### A second instance, beside the one you are working in

This repository is developed from inside the application it builds, so the
ordinary way to look at a change — restart the app — stops the window the change
is being made in. The answer is a **second daemon and a second renderer**, on
their own ports, with the running pair untouched.

```
  in use     5273 renderer · 5274 daemon        do not touch
  the branch 5283 renderer · 5284 daemon        the one under test
```

Two overrides, and no more. Both are development handles rather than settings —
neither is in the config file, because a port a person could set permanently is
a port every other client would then have to be told about.

```
  AWP_DAEMON_PORT       which port the daemon binds. The HOST is not
                        overridable: the daemon hands out ptys onto the user's
                        own agent sessions, so binding off the loopback
                        interface would put a shell on the network
  VITE_AWP_DAEMON_URL   which daemon the renderer talks to. Substituted by
                        Vite at BUILD time — the renderer has no process
                        environment to read at runtime
```

The two commands, from the repository root:

```
AWP_DAEMON_PORT=5284 bun run daemon

cd apps/amoeba && VITE_AWP_DAEMON_URL=ws://127.0.0.1:5284 \
  bunx vite --port 5283 --strictPort --clearScreen false
```

Then open **`http://127.0.0.1:5283/#/`** in a browser.

**Vite and not `bun run amoeba`.** That script starts Electron as well, which
means a second native window, a second renderer process and a second menu bar
claiming cmd+V — and Electron is told its dev-server port by an env var baked
into the script. A browser tab is the cheaper half and answers nearly every
question: the layout, the theme, the panels, the pane's own rendering, and every
call over the socket. What a browser cannot answer is anything about the native
webview — see the web panel, which says so in words rather than rendering an
empty box.

**`--strictPort`, always.** A Vite that quietly moved to the next free port
would leave you looking at the instance you were trying not to disturb, and
nothing on screen would say which one it was.

Three things to check before clicking anything, in this order.

**The database is shared.** `~/.awp/awp.sqlite` is one file and both daemons
open it. That is usually what you want — the real threads are there, so a
feature that joins against them can be exercised for real — but two jobs
runners over one store both resume non-terminal jobs on start, and the
deduplication that stops a job running twice is per process. So look first:

```
sqlite3 ~/.awp/awp.sqlite \
  "select count(*) from jobs where status in ('queued','running')"
```

Zero is the safe state and the ordinary one. If it is not zero, wait for them,
or point the second daemon at its own file and accept that its threads, jobs
and projects will be empty.

**Anything you do there is real.** A review started in the second window makes
a real jj workspace, a real bookmark and a real zmx session, in the real store.
It is a second instance, not a sandbox.

**Opening a workspace route attaches to that session, and resizes it.** This is
the rule stated at length in _A browser probe attaches to a session, and resizes
it_, and a hand-driven browser is the same hazard as a headless one: a session
takes its size from whoever is looking at it, so clicking a row in the second
window reflows a terminal somebody is working in — to whatever the agent column
computes to in that browser tab.

```
  #/                          the fixture. Attaches to nothing        ← start here
  #/w/<project>/<ws>/agent    a real session, sized to this tab
```

`#/` is enough for anything about layout, theme, scrollbars, a panel's own
behaviour, or a call to the daemon. When a session genuinely is the thing under
test, name a workspace this repo created for the purpose — the `ours()` shape
`probe:workspace` uses — and never one a person is working in.

**Two daemons on one store means "the reply is the update" stops holding.**
Threads deliberately have no change stream — a thread changes when a person
changes it, in this window, so the reply to the change _is_ the update — and the
one thing that nudges a window to re-read is a job of its own finishing. With a
second instance, the person changing a thread is in the other window, and the
job ran in the other daemon's runner, whose change feed is per process.

Measured, after starting a review in the branch window:

```
  ws://127.0.0.1:5284   threads 26   review work thicket/pr-2418
  ws://127.0.0.1:5274   threads 26   review work thicket/pr-2418   ← the daemon knows
                        the base-branch WINDOW did not, until it was reloaded
```

So a record missing from the other window is not evidence it is missing.
`bun run probe:ask <url>` asks a specific daemon what it reports, which is the
only way to tell a stale window from an absent row — and both its calls are
questions, so it is safe against a daemon somebody is working in.

The corollary is a real hazard rather than a curiosity: a **non-terminal** job
written by one daemon can be resumed by the other, whose registry is a different
build. A `create-workspace` record whose `steps` list includes `fetch` resumed by
a daemon whose kind has no such step runs a different list against the same
`done`. Terminal jobs are inert and are the ordinary case; before starting a
second daemon, the check for in-flight jobs above is what keeps this theoretical.

Stopping it: the daemon and Vite are ordinary processes on those ports.

```
lsof -nP -iTCP:5283 -sTCP:LISTEN -iTCP:5284 -sTCP:LISTEN
```

**Verify the renderer is pointed where you think.** The substitution happens at
build time, so a missing variable is not an error — it is a window quietly
talking to the daemon on 5274, which looks exactly like a working second
instance until a branch-only call fails. Ask the dev server what it served:

```
curl -s http://127.0.0.1:5283/src/renderer/daemon.ts | head -1
#  import.meta.env = {… "VITE_AWP_DAEMON_URL": "ws://127.0.0.1:5284"};
```

That line is the whole check, and it is the same shape as every other silent
failure in this file: read what the other process received, not what was handed
to it.

### archive/ is evidence, not truth

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

### Do not reach for `_tag`

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

### Effect v4 is a release candidate, and its names moved

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

### jj: name the repository, and do not snapshot to answer a question

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

### Layout

```
apps/amoeba/       electron main process + vite renderer
packages/protocol/ the RPC contract
packages/store/    one sqlite file, and the migrations for it
packages/jobs/     work that outlives whoever asked for it
packages/server/   the daemon: multiplexer, pty, attachment
packages/pane/     the terminal, ghostty-web
archive/           the Go implementation — reference only
```

`pane` importing from `server` is a compile error, not a convention: the
tsconfig project references are the import graph.

The rest is filed where it applies, and loads when work is happening there:

```
  apps/amoeba/AGENTS.md                  the window — layout, motion, panels
  apps/amoeba/src/electron/AGENTS.md     the shell — menu, app://, webviews
  packages/jobs/AGENTS.md                resume, compensation, the store
  packages/server/AGENTS.md              tasks, review queue, chat, mcp, pages
```

A `CLAUDE.md` beside each is a symlink to it.

### Never write a real name down

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

### Omitting an environment variable does not remove it

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

**And the reading half was missing.** `currentZmxSession()` returned
`process.env.ZMX_SESSION` as it stood, so the empty string this function sets
read back as _a session named nothing_ — in exactly the children that had been
neutralised. `insideZmxSession()` answered true there, which is the refusal
the probes are built on, aimed at the one case that is safe.

Seen as three failures in `zmx.test.ts`, each asking a real zmx about a
session named `""` and being refused by name. Empty is absent now, in the one
function that reads it; the rule is not "set on the way out" but **set on the
way out, and treat empty as absent on the way in**, and only the pair is
coherent. `attachment.test.ts` was reading the variable directly and now asks
through the same function — a second implementation of a rule is the copy that
drifts.

### Running the app under zmx: two sessions, because one of them does not block

`bun run amoeba` is `dev:all`, which is `vite --clearScreen false & bun run
dev`. The `&` is the problem: the script returns as soon as it has forked Vite,
so under `zmx run` the **task completes** — and the session's shell reaps what
the task left behind. Vite dies, Electron survives with nothing to load, and
the window is black.

```
  zmx history awp-dev-app
    VITE v8.2.2  ready in 187 ms
    ➜  Local:   http://127.0.0.1:5273/
    Done in 361 ms
    ZMX_TASK_COMPLETED:0        ← the task is over; the dev server goes with it
```

It cost a black window twice before the log was read. A zmx task has to be
something that blocks for as long as the thing is meant to run, so the two
halves are two sessions:

```
  awp-dev-daemon   env -u ZMX_SESSION bun run daemon
  awp-dev-vite     cd apps/amoeba && exec bunx vite --port 5273 --strictPort
  awp-dev-app      cd apps/amoeba && bun run build:electron &&
                   AMOEBA_DEV_SERVER=http://127.0.0.1:5273 exec electron .
```

`--strictPort` for the reason the second-instance note gives: a Vite that
quietly moved to the next free port leaves the window loading whatever is on
5273, which may be a Vite nobody is watching. `exec` so the session's process
_is_ the server rather than a shell holding one — otherwise the same reaping
happens one level down.

And `env -u ZMX_SESSION` on the daemon only. The daemon spawns `zmx attach`; a
Vite and an Electron do not.

### Services and their fakes

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

### The daemon cannot restart itself, and neither can anything it spawned

Reported as "i tried restarting the daemon from within the acp and failed
pretty hard", and what was left behind was a session with `exit_code=130`, a
free port, and a window talking to nothing. The reason is a process tree, not
a command:

```
  the daemon ── spawns ──▶ the ACP adapter ── is ──▶ the agent in the chat
             ── spawns ──▶ zmx attach ──▶ the agent in a terminal it created
```

Both of those agents die with the daemon. So a restart typed in either one
runs its first half — the kill — and never reaches the second. The failure has
no error in it: the command that would have said something is the thing that
was killed.

**The repair is to hand the restart to a process the daemon is not the parent
of.** zmx has one — the session server — so the work goes into a session of
its own:

```
  bun run dev restart daemon
    └─ zmx run awp-dev-ops -d bash scripts/dev/restart.sh daemon
         └─ interrupt awp-dev-daemon · wait for the task · run it again
```

The caller can then die immediately, which is exactly what it is about to do.

**An interrupt, not `zmx kill`.** `kill` takes the session with it, so the next
`run` makes a new one and the scrollback of what just happened is gone — which
is the one thing a person wants after a restart that did not work. `zmx wait`
rather than a sleep, because a daemon still holding :5274 when the next one
starts fails with `address already in use` in a log nobody is reading.

### `ended=` is about the last task, not about what is running

The status command got this wrong first, and the reading was flatly
contradicted by the port:

```
  daemon   stopped   pid=60689        ← from `ended=… exit_code=130`
  :5274    bun 54407                  ← answering requests the whole time
```

A session keeps the `ended`/`exit_code` of its **previous** task after a new
one starts. It is the same distinction this file already records for
`SessionInfo.ended` — zmx's is about the task, the daemon's is about the
process — and it means no field in `zmx ls` answers "is this running".

What answers it is a **child of the session's shell**: a task is a process
under it, and a session sitting at a prompt has none. `probe:session-start`
reads a child of the session pid for exactly this reason.

### The dev processes, in one place

`scripts/dev/` holds one script per session, and they are what the sessions
run — not a line typed into a terminal once and lost. The commands:

```
  bun run dev up      [daemon|vite|app|all]   idempotent per session
  bun run dev down    [what]                  interrupt, keep the scrollback
  bun run dev restart [what]                  through awp-dev-ops
  bun run dev status                          sessions, and the ports
  bun run dev logs daemon [lines]
```

`up` leaves a running session alone, because `up` is what somebody types when
they are not sure. Each script `exec`s its process so the session's process
_is_ the thing, rather than a shell holding it — see the note below on why a
`&` produced a black window twice.

### Working here

- **Run each gate as its own command.** The dev-loop hook records one gate per
  Bash invocation, so `bun run lint && bun run test` registers only one of them.

  ```
  bun run fmt   ·  lint  ·  typecheck  ·  test  ·  doctor
  ```

- **`tsc --build` is incremental, and an incremental gate can pass on a file
  it did not check.** A prop changed from `focus?: string` to something passed
  `string | undefined` is an `exactOptionalPropertyTypes` error, and five
  consecutive `bun run typecheck` runs reported zero errors — then a clean
  `.tsbuild` failed immediately, and so did somebody else's machine. The
  script is `tsc --build --force` now: 1.4s for the whole workspace, against a
  gate that can be wrong.

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

### zmx: two rules, and they are not the same rule

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
