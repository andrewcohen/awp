# awp

**agent work platform.** Composable `@awp-kit/*` packages, plus `apps/amoeba` —
a reference implementation that is, to start, roughly zdeck in a webview over a
client-server architecture.

Full reasoning lives in `specs/20260825-52cw-amoeba-rewrite-spec.md`. This file
is the rules. Every rule was learned from a measurement or a failure; those are
in `docs/repo.md`, which no session loads — read it when a rule is questioned,
not to learn the rule.

## Layout

```
apps/amoeba/       electron main process + vite renderer
apps/tui/          the terminal face
packages/protocol/ the RPC contract
packages/store/    one sqlite file, and the migrations for it
packages/jobs/     work that outlives whoever asked for it
packages/server/   the daemon: multiplexer, pty, attachment
packages/pane/     the terminal, ghostty-web
archive/           the Go implementation — reference only
```

`pane` importing from `server` is a compile error, not a convention: the
tsconfig project references are the import graph.

Rules are filed where they apply and load when work is happening there:

```
  apps/amoeba/AGENTS.md                      the window — stack, state, layout
  apps/amoeba/src/electron/AGENTS.md         the shell — menu, app://, webviews
  apps/amoeba/src/renderer/panels/AGENTS.md  chat, diff, the dock, fences
  apps/amoeba/src/renderer/design/AGENTS.md  palette, type, the style guide
  packages/jobs/AGENTS.md                    resume, compensation, the store
  packages/server/AGENTS.md                  tasks, review queue, chat, mcp
  docs/{repo,window,shell,jobs,daemon}.md    the evidence behind each of them
```

A `CLAUDE.md` beside each is a symlink to it.

## Every rules file has a budget, and it is the thing that keeps it a rules file

These files are read into the context of every session working in their
directory, before anything is asked. One grew to 351KB and cost 14% of a context
window to open an unrelated file.

```
  root AGENTS.md          20KB      loaded by everything
  a filed AGENTS.md       34KB      loaded by work in that directory
  docs/*.md               no limit  loaded by nobody
```

**`bun run test` fails over it** — `test/rules-budget.test.ts`, which walks for
`AGENTS.md` rather than listing them, so a rules file in a new directory is
bounded by the gate that exists to bound it. The budget is not advice.

Three tests before a paragraph goes in, and it must pass all three:

```
  could a session learn this by opening the file it is about?
                            → do not write it. One sentence naming the path,
                              at most, and only if the path is hard to find
  did it cost a measurement or a failure to find out?
                            → write the RULE. The measurement goes in docs/
  is it a narrative of how the current design was arrived at?
                            → docs/. A rules file says what to do, not what
                              was tried
```

**Adding is editing.** A file at its budget takes a new rule by cutting an old
one or moving it to `docs/`, in the same change — never by appending and fixing
it later, which is what produced the 351KB file. Prefer rewriting the paragraph
that is nearly the same rule over adding one beside it.

**A rule earns its length from what is surprising in it.** Names, tables and
lists of what the code already says are the part to cut first.

## zmx: two rules, and they are not the same rule

This repo is developed from **inside** a zmx session. `zmx attach` branches on
`ZMX_SESSION` — from inside a session it switches the _calling_ client instead
of making a new one, stealing the terminal the caller was launched from.

- **Spawning zmx as a child:** strip `ZMX_SESSION`. Always. `zmxChildEnv()`.
- **Probing or testing against a real zmx:** _refuse_ to run inside a session.
  Not strip — refuse. `requireOutsideZmxSession()`.

No environment edit makes the second safe: a session takes its size from the
client looking at it, so any new client reflows it.

- `packages/server/src/probe/` is run by hand from a plain terminal and refuses
  to run anywhere else. Read-only commands (`list`, `lookup`, `history`) are
  safe inside a session and are tested normally.
- `Attachment` refuses to attach to the session the process is running in.
- **Never** run `zmx kill`, `zmx attach`, or anything that changes a session,
  against a session this repo did not create.

A third route reaches a session and runs no zmx at all: **driving the renderer**
at a route that names one. Probes open `#/`, which attaches to nothing. When a
real session is needed, name one this repo created — the `ours()` shape
`probe:workspace` uses.

## Omitting an environment variable does not remove it

`zmxChildEnv()` **sets** `ZMX_SESSION` to the empty string. bun-pty hands its
pairs to a Rust `Command`, which inherits the parent environment with no
`env_clear()` — so a key left out is a key left alone, not a key removed.

The rule is the pair, and only the pair is coherent: **set on the way out, and
treat empty as absent on the way in.** `currentZmxSession()` is the one reader;
`insideZmxSession()` answers false for the empty string.

**A test of the function cannot catch this** — the bug is in the spawner. When a
guard's effect happens in another process, assert on what that process sees.
`bun run probe:child-env` spawns `/bin/sh` and prints what arrived; it never
invokes zmx, so it is safe anywhere. Run it after touching process environments
or the pty layer.

## archive/ is evidence, not truth

Read like vendored upstream source: consulted, never called, never ported line
for line, excluded from every gate. Its comments record what was once measured
and at least one was wrong. **Re-prove anything inherited from it** —
`bun run probe:claims`. Where a claim held, test the property, not the anecdote.

## An empty string is not an absent value

Recorded three times in this repo and each time it cost weeks: `ZMX_SESSION`
above, `NEVER` in the diff's fold state, and `startDir`, where `zmx ls` renamed
`start_dir` to `cwd`, no parse ever failed, and the field read `""` for every
session — so no project was ever derived from a running session.

```
  `??` does not catch ""          a guard written against `undefined` on a
                                  field typed `string` guards a case the type
                                  cannot produce
  a fixture agrees with itself    zmx-parse.test.ts carried the old spelling,
                                  so parser and fixture were both wrong
```

**Ask the real tool, and ask for the property rather than the spelling.**
`zmx.test.ts` asserts every session has an absolute directory, whatever the
field is called that day — a test a rename breaks instead of the project list.
Both spellings are accepted.

## A name cannot group a workspace

The sidebar lists **workspaces**; zmx lists **sessions**. `sessionName` gives
the stem whatever budget the kind does not need, so one workspace's sessions
shorten to **different stems** — splitting `awp.<project>.<workspace>.<kind>`
on dots recovers three workspaces where there is one. Project names may also
contain a dot, lost to `sanitize`.

The truth is the labels awp writes (`awp_project`, `awp_workspace`, `awp_kind`),
which are unshortened. Older sessions are repaired in `identities()` by asking
`stemMatches` per known workspace — only the workspace can reproduce the
shortening at the length a given stem has. One labelled session recovers every
sibling; a workspace with none stays split, which is honest rather than guessed.

`SessionIdentity` is on the wire for the same reason the refusal sentence is: a
client re-deriving a daemon's rule is a second implementation, and the copy that
drifts is the one nobody tests.

## Effect v4 is a release candidate, and its names moved

Most Effect material online is v3 and will mislead. **Read the installed
source** under `node_modules/.bun/effect@*/node_modules/effect/src/`.

| v3                                | v4                                                            |
| --------------------------------- | ------------------------------------------------------------- |
| `Effect.Service`                  | `Context.Service<Self, Shape>()("Key")`                       |
| `Effect.async`                    | `Effect.callback`                                             |
| `Effect.either`                   | `Effect.result` → `Result`, with `isSuccess`/`isFailure`      |
| `@effect/rpc`, `@effect/platform` | folded into core: `effect/unstable/{rpc,http,socket,workers}` |

There is **no v4 line of `@effect/rpc`** — it peers on `effect ^3.22.1`, and two
Effect runtimes in one workspace means two sets of Context tags, so a service
provided through one is not found by the other. `test/deps.test.ts` guards it.

Use `@effect/platform-node-shared`, not `-bun`: the Bun barrel imports `bun`
through `BunRedis`, so vitest cannot load anything touching it.
`BunChildProcessSpawner` is `export * from` the Node one.

## Services and their fakes

A tag exists so callers can be tested against a fake, not on any expectation of
swapping the real thing out.

```
Multiplexer   list · lookup · kill · labels · history   ← questions, no cost
Attachment    attach                                     ← an act, with consequences
PtySpawner    spawn a pty                                ← Scope in the type
```

`Attachment` is separate because that line is also the line between "testable
from inside a session" and "must run outside zmx". `Scope` in `spawn`'s return
type is the promise the process gets killed.

## A command's exit code is not in its output

`ChildProcessSpawner.string` collects stdout and **discards the exit code** —
and every command here reports a refusal by writing to stderr and exiting
non-zero.

```
  sh -c 'echo out; exit 3'   through `string`  →  succeeds with "out\n"
                             through `capture` →  { stdout: "out\n", exitCode: 3 }
```

So everything goes through `run.ts`. Two properties worth keeping: stdout,
stderr and the exit code are **awaited concurrently** (reading one to the end
first deadlocks once a command outgrows a pipe buffer — `run.test.ts` pushes
256KiB down each), and the failure carries **the CLI's own sentence**.

Found by a mutation check, not a test: **a guard whose removal changes nothing
is not doing what it claims.**

## jj: name the repository, and do not snapshot to answer a question

```
  -R <repo>                jj finds a repo by walking up from cwd, and the
                           daemon's cwd is a real repository — this one
  --ignore-working-copy    on reads. Without it a QUESTION writes to the
                           repository it is asking about
```

`-R` is a required argument on every method of `Jj`, so no call can reach the
wrong repository by accident. Writes deliberately omit `--ignore-working-copy`.

**`-R` does not walk up**, so a directory inside a repository is not a
repository to it. The one place a _person_ names a directory — importing a
project — climbs first: `nearestRepo` finds the first ancestor holding `.jj`,
then `sourceRoot` stops a secondary workspace being recorded as its own project.

- **A project marker is `.jj`, not `.git`** — every operation awp performs is a
  jj one, so a git-only repo is a row that fails on import.
- **Reads ask for `-T 'json(self)'`.** Human output puts a description on the
  same line as the name, and breaks on the first colon.
- **A bookmark name appears more than once** — one row per local bookmark and
  one per disagreeing remote. "Does this bookmark exist" means `localBookmarks`.
- **Everything is safe to run twice**, because the jobs runner re-enters the
  step it failed on.
- **`jj workspace forget` with no argument forgets the workspace it is standing
  in** — for the daemon, this repository. Empty is refused, not defaulted. And
  forgetting **does not remove the directory**; the undo of a creation does both.

## Never write a real name down

No real project, repository, branch, customer, product or person's name goes
into this repo — not in code, not in a fixture, not in a comment, not in a
commit message. `awp`, `amoeba` and `andrew` are the exceptions; the repository
is already public under them.

This is a repo about working on _other_ repositories, so real names arrive
constantly and by accident: a session read off `zmx ls`, a path in an error, a
workspace in a screenshot. Invent instead — the corpus in `naming.test.ts` is
`thicket`, `orchard`, `harbor-works`, `typed-router`, `lantern`, shaped like the
real ones and naming nothing.

Two costs of a careless replacement: the sidebar sorts alphabetically, so a name
that sorts differently silently reorders every fixture built on it; and
`naming.test.ts` pins shortened names carrying a fingerprint, so a changed stem
must be recomputed.

When a name has already been written down, **rewrite the history** — check
`git log --oneline -S <name> origin/main` first. Use `jj fix` over a revset, not
`jj edit` on an ancestor (47 conflicts across 140 commits) and not
`filter-branch`. `jj fix` touches **file content only**; commit messages are a
separate pass with `jj describe -r <id> --stdin`.

## A second instance, beside the one you are working in

This repo is developed from inside the application it builds, so restarting the
app stops the window the change is being made in. Run a second daemon and
renderer on their own ports.

```
  in use     5273 renderer · 5274 daemon        do not touch
  the branch 5283 renderer · 5284 daemon        the one under test
```

```
AWP_DAEMON_PORT=5284 bun run daemon

cd apps/amoeba && VITE_AWP_DAEMON_URL=ws://127.0.0.1:5284 \
  bunx vite --port 5283 --strictPort --clearScreen false
```

Then open `http://127.0.0.1:5283/#/`. Vite rather than `bun run amoeba`, which
would start a second Electron and a second menu bar. `--strictPort` always, or
a moved port leaves you looking at the instance you meant not to disturb.

Three things before clicking anything:

- **The database is shared.** Check `select count(*) from jobs where status in
('queued','running')` is zero first — two runners over one store both resume
  non-terminal jobs, and a job resumed by a daemon of a different build runs a
  different step list against the same `done`. **A chat opened in both is
  refused in the second**, by the claim in `chat_claims`: two daemons on one
  session id is two agents on one transcript, which has happened.
- **Anything you do there is real** — real workspaces, bookmarks, sessions.
- **Opening a workspace route attaches to that session and resizes it.** `#/`
  attaches to nothing and answers every question about layout or the daemon.

`VITE_AWP_DAEMON_URL` is substituted at build time, so a missing variable is not
an error — it is a window quietly talking to 5274. Verify:
`curl -s http://127.0.0.1:5283/src/renderer/daemon.ts | head -1`.

"The reply is the update" stops holding with two instances: a thread changed in
one window is not seen by the other until it reloads. `bun run probe:ask <url>`
asks a specific daemon; both its calls are questions.

## The daemon cannot restart itself, and neither can anything it spawned

The daemon is the parent of the ACP adapter and of every `zmx attach` it made,
so a restart typed in either agent runs its first half — the kill — and never
reaches the second. The failure has no error in it: the command that would have
complained is the thing that was killed.

Hand it to a process the daemon is not the parent of:

```
  bun run dev restart daemon
    └─ zmx run awp-dev-ops -d bash scripts/dev/restart.sh daemon
         └─ interrupt awp-dev-daemon · wait for the task · run it again
```

An interrupt, not `zmx kill` — kill takes the scrollback of what just happened
with it. `zmx wait` rather than a sleep, or the next daemon finds :5274 taken.

**`ended=`/`exit_code=` in `zmx ls` are about the last _task_, not about what is
running.** A session keeps them after a new task starts, so no field there
answers "is this running" — a child of the session's shell does.

```
  bun run dev up|down|restart|status|logs   scripts/dev/, one per session
```

## Running the app under zmx: two sessions

`bun run amoeba` forks Vite with `&` and returns, so under `zmx run` the **task
completes** and the session's shell reaps the dev server: Electron survives with
nothing to load, and the window is black. A zmx task must block for as long as
the thing is meant to run.

```
  awp-dev-daemon   env -u ZMX_SESSION bun run daemon
  awp-dev-vite     cd apps/amoeba && exec bunx vite --port 5273 --strictPort
  awp-dev-app      cd apps/amoeba && bun run build:electron &&
                   AMOEBA_DEV_SERVER=http://127.0.0.1:5273 exec electron .
```

`exec` so the session's process _is_ the server. `env -u ZMX_SESSION` on the
daemon only — it is the one that spawns `zmx attach`.

## Working here

- **Run each gate as its own command.** The dev-loop hook records one gate per
  Bash invocation, so `lint && test` registers only one of them.

  ```
  bun run fmt   ·  lint  ·  typecheck  ·  test  ·  doctor
  ```

- **Judge a gate by its exit code, never by grepping its output.** `tsc` colours
  its output, so escape codes sit between the words and `grep "error TS"` finds
  nothing on a run with eight errors. Beware `cmd | tail` — `$?` is tail's.
- **`typecheck` is `tsc --build --force`.** Incremental builds passed five times
  running on a file they had not checked.
- **The renderer may not import a node builtin** — `import/no-nodejs-modules` is
  on for `apps/amoeba/src/renderer/**` and `packages/pane/src/**`. A barrel can
  drag `node:fs` into the browser: the jobs index reaching `sqlite.ts` broke the
  dev server, and a production build would have tree-shaken it in silence. The
  sqlite store lives at `@awp-kit/jobs/sqlite`.
- `import/no-cycle` is on repo-wide.
- Dependency versions live in **bun workspace catalogs** in the root
  `package.json`. The effect family must move together.
- Relative imports carry **no extension** (`moduleResolution: "bundler"`).
- `tsc` writes to `.tsbuild/` and nothing consumes it.
- Commit messages go through `jj describe --stdin < file`. The shell is fish,
  and a long `-m` with apostrophes or backticks will be mangled.
- **Renaming an exported symbol breaks the hot reload of every module importing
  it**, and this repo is edited from inside the app it builds — so the window
  keeps running a stale tree until somebody notices it is lying.
  `zmx history awp-dev-app | grep -i 'failed to reload'`.
- **A `Foo.tsx` beside a `foo.ts` is one module** on this filesystem, and the
  import resolves to the wrong one. It has caused an unclosable webview once.

## Do not reach for `_tag`

It is Effect's discriminant, and there is an API over it for every case:
`Result.isSuccess`/`isFailure`, `Effect.catchTag`/`catchTags`,
`Match.tag`/`tags`/`tagsExhaustive`. These narrow; `result._tag === "Failure"`
does not. `no-underscore-dangle` is left on deliberately — if it fires, the
combinator is the answer.

`no-redeclare` **is** off: a schema and its inferred type sharing a name is the
idiom, and `tsc` catches a real redeclaration.
