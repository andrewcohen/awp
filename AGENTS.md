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

## Frontend

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
  `@rolldown/plugin-babel`; StyleX will ride the same pass. The tell that it was
  not running was a bundle byte-identical to one built without it.
- Vite owns the renderer and Electrobun copies `dist/renderer` in. Electrobun
  never compiles it.

## Working here

- **Run each gate as its own command.** The dev-loop hook records one gate per
  Bash invocation, so `bun run lint && bun run test` registers only one of them.

  ```
  bun run fmt   ·  lint  ·  typecheck  ·  test  ·  doctor
  ```

- Relative imports carry **no extension**. `moduleResolution: "bundler"` resolves
  them; `.js` names a file that does not exist, and
  `allowImportingTsExtensions` conflicts with declaration emit under `composite`.
- `tsc` writes to a top-level `.tsbuild/` and nothing consumes it — exports point
  at `src/*.ts` and both Vite and Bun read TypeScript directly. It is a
  typechecker and nothing else.
- Commit messages go through `jj describe --stdin < file`. The shell here is
  fish, and a long `-m` with apostrophes or backticks will be mangled.
