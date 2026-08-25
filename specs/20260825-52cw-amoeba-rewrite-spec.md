# amoeba: the client-server rewrite

## Metadata

- **Spec ID**: `20260825-52cw`
- **Feature name**: amoeba — awp as a client-server platform
- **Owner**: Andrew Cohen
- **Status**: In Progress
- **Last updated**: 2026-08-25

## Goal

Rebuild awp as **agent work platform**: a set of composable `@awp-kit/*` packages
from which someone can assemble their own awp, plus `apps/amoeba` — a reference
implementation that is, to start, roughly zdeck in a webview over a
client-server architecture.

## User Problem

The Go deck is a single process that _is_ the thing owning agent sessions,
terminals, and the UI. That coupling is the source of most of its constraints:

- A session cannot outlive the UI that opened it, so the deck cannot be closed
  and reopened around long-running work without consequence.
- There is exactly one frontend, and adding a second means reimplementing
  everything behind it.
- An agent's rich output — markdown, diffs, images, artifacts — can only ever be
  _transcribed into a terminal_. gdeck was built to test whether a webview fixes
  that, and it does.

Separating the client from the server makes the CLI, the desktop window, and any
later remote or web client peers rather than rewrites.

## Scope

### In scope (v1)

- Bun monorepo: `packages/@awp-kit/*`, `apps/amoeba`.
- A Bun daemon that owns ptys and speaks a typed RPC contract over a localhost
  socket.
- An Electrobun desktop client rendering a React frontend.
- A live terminal pane: attach to an existing zmx session, stream bytes both
  ways, resize, and measure the latency a person actually feels.

### Out of scope (v1)

- Turborepo. Bun workspaces plus scripts cover a repo this size; Turbo earns its
  place when CI time or cross-package caching hurts.
- Packaging, self-update, code signing.
- Any port of the Go feature surface: new-workspace flow, review, GitHub
  integration, keybinding parity, the inbox. None of it is a requirement.
- Remote / multi-machine clients. The architecture must not _preclude_ them; v1
  does not build them.

## Decisions

### zmx stays the session owner

The daemon does not invent its own session model. zmx already owns sessions and
their lifetime, and `awp` has years of accumulated knowledge about its edges.

The consequence is concrete: `internal/zmx/zmx.go:544`'s `AttachCmd` returns an
`exec.Cmd`. zmx is driven as a **CLI hosted in a pty** — there is no socket
protocol to speak to it instead. So the Bun daemon needs a real pty; it cannot
delegate its way out of the problem. This is the largest unknown in the stack
and is spiked first.

The hijack rule comes along unchanged: `zmx attach` branches on `ZMX_SESSION`.
From inside a session it switches the _calling_ client rather than creating a
new one, which would steal the terminal the app was launched from. The child's
environment must have that marker stripped.

Attaching also reflows: a zmx session takes its size from the client looking at
it, so opening a pane resizes the agent and closing it resizes again. Nothing
avoids that — it is what attaching means. gdeck said so in the UI and disabled
the row for its own launching session. The same honesty is required here.

### Go is reference only

`archive/` is read-only history. Nothing in it ships, is called as a subprocess,
or is ported line-for-line. It is consulted the way vendored upstream source is
consulted — because a measured answer beats a guess.

### One RPC layer, not two

Electrobun ships typed webview↔main RPC; Effect ships its own. Stacking them
means every call has two hops and two schemas.

The webview talks to the **daemon directly** over a localhost socket using the
Effect RPC contract. Electrobun's RPC is reserved for what only the native
process can do: window and menu, file dialogs, keychain, deep links.

### Vite in the renderer, Bun in the main process

StyleX and react-compiler are both Babel plugins and ride the same
`@vitejs/plugin-react` pass. Bun's bundler hosting a Babel-based CSS compiler was
the one stack-level risk in the original sketch; Vite removes it. Electrobun is
pointed at Vite's `dist`, exactly as gdeck pointed Wails at it.

### Package decomposition is earned, not planned

`@awp-kit/*` composability is the destination. Boundaries drawn before a second
consumer exists are guesses that later have to be unpicked. v1 ships three
packages — `protocol`, `server`, `pane` — and splits further only when something
is genuinely imported twice.

## Stack

Versions confirmed against the npm registry on 2026-08-25.

| Layer            | Choice                       | Version             | Note                                               |
| ---------------- | ---------------------------- | ------------------- | -------------------------------------------------- |
| Runtime          | Bun                          | —                   | main process, daemon, workspaces                   |
| Desktop shell    | electrobun                   | 2.0.1               | pre-1.0 ecosystem, system webview (WKWebView)      |
| Renderer build   | vite                         | 8.2.2               | hosts the Babel pass StyleX + react-compiler share |
| UI               | react                        | 19.2.8              | 19 required by react-compiler and atom-react       |
| Compiler         | babel-plugin-react-compiler  | 1.0.0               | stable                                             |
| Routing          | @tanstack/react-router       | 1.170.x             |                                                    |
| State            | @effect/atom-react           | 4.0.0-beta.107      | peers `effect ^4.0.0-beta.107`, `react >=19.2.7`   |
| Styling          | @stylexjs/stylex             | 0.19.0              | compile-time; needs the Babel pass                 |
| Primitives       | @base-ui/react               | 1.7.0               | already proven in gdeck's webview                  |
| Diffs / trees    | @pierre/diffs, @pierre/trees | 1.3.6, 1.0.0-beta.6 | diffs already proven in gdeck                      |
| Terminal         | ghostty-web                  | 0.4.0               | libghostty→wasm, canvas renderer                   |
| Backend          | effect                       | 4.0.0-rc.112        | dist-tag `rc`                                      |
| Backend platform | @effect/platform-bun         | 4.0.0-rc.112        |                                                    |
| Lint             | oxlint                       | 1.80.0              | oxc; no formatter of its own                       |
| Format           | oxfmt                        | 0.65.0              | oxc; pre-1.0                                       |
| Test             | vitest                       | 4.1.11              | peers vite ^8                                      |
| Types            | typescript                   | 7.0.2               | the native compiler                                |
| React checks     | react-doctor                 | 0.9.12              | wired up with the rest of the React tooling        |

### Effect v4 folds RPC into core

`effect@4` exposes `effect/unstable/rpc`, `/http`, `/socket`, `/workers`. There
is **no v4 line of `@effect/rpc`** — that package is the v3 series and peers on
`effect ^3.22.1`. Depending on it alongside v4 puts two Effect runtimes in one
workspace, which is the failure mode worth naming rather than discovering.

`unstable/` is upstream's own label. The contract package is where that churn is
absorbed, so a breaking rename touches one file rather than every call site.

## The pty under Bun

Measured 2026-08-25 with a disposable probe, before any of it was wrapped in an
Effect service — a release-candidate API and an unproven native binding failing
at the same time gives a failure two suspects and rules out neither.

**node-pty does not work under Bun.** It loads, and it spawns: the native module
is reachable and returns a pid. Then no callback ever fires — not `onData`, not
`onExit`, not after two seconds for a process as short as `/bin/echo hi`. The
identical script under Node streams the output and exits in 27ms. Bun's node-api
does not drive node-pty's read loop.

Two things were ruled out on the way there, and both are worth knowing:

- `posix_spawnp failed` is not the node-api problem, it comes first. node-pty
  ships `prebuilds/darwin-arm64/spawn-helper` mode `-rw-r--r--`, and Bun's
  installer does not restore the executable bit that npm would. Anyone
  debugging node-pty under Bun hits this before reaching the real wall.
- The first probe "failed" its resize check because it fed `stty size` to a
  `cat`, which echoed the command back. An echo of a question is not an answer
  to it, and a probe that cannot tell the difference reports a working feature
  as broken.

**bun-pty 0.4.10 works.** Spawn, stream, write, and a resize the kernel actually
performed — `stty size` reported `30 100` before and `12 40` after. It is a Rust
FFI binding via `bun:ffi`, sources vendored at `~/.references/bun-pty@0.4.10`.

Its one limitation: `onData` is `string` and `write` takes `string`, with no
encoding switch in `IPtyForkOptions`. Bytes never surface. The decode is done
correctly for the case that matters — one `TextDecoder("utf-8")` held across
reads with `{ stream: true }`, so a character split across a 64KB boundary is
buffered and emitted whole, which is exactly what gdeck warned about. What it
cannot carry is a byte sequence that is not valid UTF-8: those become U+FFFD
before the emulator sees them, and the emulator is what should be deciding.

Nothing that actually flows through a pane needs those bytes. Agent output is
text. ANSI escapes are ASCII. Mouse SGR reports and key encodings are ASCII, and
even the inline-image protocols — Sixel, kitty graphics, iTerm2 — carry their
payloads base64-encoded inside ASCII escape sequences. What is left is `cat`-ing
a binary, which renders as garbage on a real terminal too. So bun-pty is
adopted, and `PtySpawner` is typed in `string`.

**This deletes base64 from the transport.** gdeck sent pty traffic as base64
because its pty was in Go: raw `[]byte` had to cross into JSON, and a 64KB read
could split a UTF-8 sequence across two events with only the emulator able to
hold half of one. Neither is true here. bun-pty holds the partial sequence
itself, and ghostty-web's `write` takes `string | Uint8Array` while its `onData`
emits `string` — so the path is string-native end to end. Along with the
encoding goes `LivePane`'s hand-rolled per-byte `atob`/`btoa` loop, which
existed only to serve it.

The knowledge survives the code: a split multi-byte sequence is still the hazard
on this path. It is now handled one layer down, inside the binding, rather than
by us.

### zmx through bun-pty

`bun run probe:zmx`, run from a plain terminal on 2026-08-25:

|                      | probe  | gdeck (Go) |
| -------------------- | ------ | ---------- |
| spawn                | 16.4ms | 3ms        |
| first byte           | 44.5ms | 9ms        |
| screen live          | 52.7ms | 11ms       |
| keystroke round trip | 9.1ms  | —          |

It works: session created, size reported as `30 100`, keystroke echoed, clean
exit, nothing left behind.

**The first three rows do not compare.** The probe _creates_ a session — zmx
forks a shell which then runs the command — from a cold Bun process whose first
`spawn` also pays bun-pty's one-time `dlopen` and FFI init. gdeck's 25ms was
attaching to a session that already existed, from a warm process. Reading these
as a 2× regression would be wrong, and so would reading them as a pass.

The row that does transfer is the last one. **9.1ms from keystroke to echo**,
through zmx, through a pty, under Bun — against gdeck's p50 of 8ms for a pane
that did not stutter. That is the latency a person feels, and it is at parity.

A warm attach to a pre-existing session, which is what the daemon actually does,
is still unmeasured.

### Running anything against a real zmx

`zmx attach` from inside a session steals the caller's terminal, and this repo
is developed from inside one. Stripping `ZMX_SESSION` from the child stops the
hijack — and that is **not** sufficient, which was learned by doing it wrong: a
probe that stripped the marker correctly still opened a new client, and a
session takes its size from the client looking at it, so it reflowed and
redrew the session it was being run from.

Two rules, and conflating them is the mistake:

- **Spawning zmx as a child:** strip `ZMX_SESSION`. Always.
- **Probing or testing against a real zmx:** refuse to run inside a session. Not
  strip — refuse. No environment edit makes it safe.

`packages/server/src/zmx-session.ts` holds both. The Go tree paired its
equivalent guard with a reflective test asserting every real-zmx test called it,
because a guard is only as good as nobody forgetting; that test belongs here as
soon as there is more than one caller.

## What the gdeck prototype already answered

gdeck and tdeck were deleted in `chore: delete the gdeck and tdeck surfaces`.
Everything below is recoverable at `jj file show -r xuwqyvqupvvu- <path>` and is
lifted rather than rediscovered.

**libghostty's wasm loads in WKWebView.** 35ms. WKWebView is particular about
WebAssembly served over a custom URL scheme — which is how both Wails and
Electrobun serve frontend assets — and ghostty-web sidesteps the scheme entirely
by carrying the wasm inline as a base64 `data:` URL and compiling it from an
ArrayBuffer. This risk is retired before the rewrite starts.

**One Terminal per window, never disposed.** `gdeck/frontend/src/paneTerminal.ts`
is the single highest-value file in the deleted tree. `dispose()` frees wasm
state the module-level Ghostty instance keeps handing out, so building a
Terminal per view caused four distinct bugs with one cause: a second Terminal
writing into freed memory ("Out of bounds memory access", which React
StrictMode's double-mount hit every time); a recycled handle receiving another
pane's bytes, which put a live agent's output into a _static_ pane's cells; and
a re-allocated 10,000-line scrollback plus font re-measure on every switch,
which is where a "slow attach" came from when Go's own timings had the whole
attach at 25ms. The canvas lives in a host element the module owns, so mounting
a view re-parents it. Keystroke and resize handlers are registered once and
dispatch through swappable sinks, because `onData` has no unsubscribe and a
stale handler writes to a pane the user has left.

**Three canvas renderer defects, each measured.**
`gdeck/patches/ghostty-web-renderer.patch`: row height must come from
`fontBoundingBoxAscent/Descent` rather than one glyph's ink (measuring `"M"` — a
letter with no descender — gave an 18px font a 17px row and sliced descenders
off); `fillText`'s `maxWidth` argument must confine a glyph to its cell, since
bold and fallback glyphs are routinely wider than a cell measured from regular
`M` and only dirty rows repaint, so the spill stays on screen; and glyphs need
thickening, because Core Text dilates stems and Canvas2D does not. The obvious
`strokeText` spelling of that last one cost **p50 734ms per keystroke** in
WKWebView against a 3ms baseline — stroking rasterises the outline as a path. A
second `fillText` offset by a third of a pixel looks the same and is the
cheapest operation the canvas has.

**Vite keys its dependency pre-bundle on the lockfile, not on file contents.**
Editing `node_modules` changes nothing until `node_modules/.vite` is deleted. Two
conclusions were drawn from screenshots of an unpatched build before this was
noticed. Confirm a marker reaches `.vite/deps/ghostty-web.js` before believing a
result.

**A 64KB read can split a UTF-8 sequence across two messages** — and something
has to be able to hold half of one. gdeck's answer was base64 in both
directions, because its pty was in Go and raw `[]byte` had to cross into JSON.
**That answer does not carry over**; see _The pty under Bun_. The hazard does.
It is now handled inside bun-pty rather than by the transport, which is the only
reason this path can be string-native.

**The pane measures itself.** Latency is reported as a number, not a verdict —
whether a pane feels right is exactly what a pass/fail line cannot carry.
Keystroke-to-echo as p50/p90/max rather than a mean, because a pane whose median
is 8ms and whose worst case is 300ms is a pane that stutters and the mean hides
that. Samples are queued rather than timed one at a time: typing outruns the
round trip, and matching the newest keystroke to the next arriving byte times
the wrong one.

**The wheel must reach the program.** An alternate-screen agent has no
scrollback, so a naive terminal synthesises arrow keys and the pane reads as
typing to itself. When the program has asked for mouse tracking, a notch goes
down the pty as an SGR report and the agent scrolls itself.

## Repository layout

```
apps/
  amoeba/            reference awp: electrobun main + vite renderer
packages/
  protocol/          @awp-kit/protocol — schema + rpc contract
  server/            @awp-kit/server   — bun daemon, pty, zmx
  pane/              @awp-kit/pane     — ghostty-web terminal component
archive/             the Go implementation, reference only
specs/
```

## Implementation Plan

Ordered so the two unproven links fail early and loudly. Everything else in the
stack is either already proven in gdeck or is ordinary.

1. **This spec.**
2. **Workspace scaffold.** Bun workspaces, base tsconfig, the pinned dependency
   set installing without peer conflict.
3. **pty under Bun.** Spawn `zmx attach` with `ZMX_SESSION` stripped, bytes both
   directions, resize. Answered — see _The pty under Bun_ below.
4. **Electrobun window over the Vite renderer.** Dev loop and production build.
5. **`@awp-kit/pane`.** Port `paneTerminal.ts` and regenerate the renderer patch
   against ghostty-web 0.4.0. Static pane from a byte fixture, no daemon.
   Re-test StrictMode against the singleton design — it is the fix for what
   StrictMode exposed, so it may now survive.
6. **`@awp-kit/protocol` + `@awp-kit/server`.** The contract, and the daemon
   serving it over a localhost socket.
7. **Close the skeleton.** Renderer pane bound to a live zmx session, with the
   echo timer reporting.
8. **StyleX, react-compiler, Base UI, router.** Deferred until the skeleton
   walks.

## Acceptance Criteria

- [ ] `bun install` resolves with one Effect major in the tree and no peer conflicts.
- [ ] A pty under Bun hosts `zmx attach`; the child's environment has no `ZMX_SESSION`.
- [ ] An Electrobun window renders the Vite-built React app in dev and from `dist`.
- [ ] A pane renders a byte fixture correctly, with the three renderer defects fixed
      and a patch marker confirmed present in `.vite/deps`.
- [ ] The renderer attaches to a live zmx session and a keystroke round-trips.
- [ ] Attach cost and keystroke latency are **reported as numbers**, compared against
      gdeck's Go baseline: 25ms attach total (spawn 3ms, first byte 9ms, screen 11ms).
- [ ] No `@effect/rpc` in the dependency tree.

## QA / Human Review Test Plan

### Setup

- [ ] `bun`, `zmx`, `jj` on PATH; macOS.
- [ ] At least one live zmx session that is **not** the one this work is being done in.

### Core Happy Path

- [ ] Launch amoeba, attach to that session, type, see the echo.
- [ ] Resize the window; the agent reflows and stays correct.
- [ ] Scroll with the wheel in an alternate-screen agent; the agent scrolls, the
      pane does not type to itself.

### Edge Cases & Failure Modes

- [ ] Attaching from inside a zmx session does not steal the launching terminal.
- [ ] The row for amoeba's own launching session is disabled or clearly marked.
- [ ] Daemon down: the client says so, and says what to do about it.
- [ ] A dead session reports actionably rather than hanging.
- [ ] Wide glyphs — bold, box drawing, Nerd Font icons, emoji, a ZWJ sequence —
      stay inside their cells and descenders are not sliced.

### Reviewer Notes

- Terminal fidelity and latency are **human-verified**, not self-certified.
  Record the numbers the pane reports alongside the observation.

## Validation

Per-unit gates, each run as its own command:

- [ ] `bun run fmt` — oxfmt
- [ ] `bun run lint` — oxlint
- [ ] `bun run typecheck` — `tsc --build`
- [ ] `bun run test` — vitest
- [ ] `bun run doctor` — react-doctor

`archive/` is excluded from every one of them. oxfmt reformatted 43 files in
there the first time it ran, which is the one way a reference tree stops being
a reference.

react-doctor is advisory rather than blocking: it reports and exits 0 whatever
it finds, so the gate records that it ran and the findings are for a person to
read. Two notes on it. Its supply-chain scan sends the dependency list to
Socket.dev and is **off** in the `doctor` script — `doctor:supply-chain` is
there for when that is wanted deliberately. And it reaches a score API even
without that flag, degrading to "Score unavailable" when the network is not
there, so a verify is never blocked by being offline.

The Go gates in `archive/` are not run; that tree is reference only.

## Spec Change Log

- 2026-08-25: Initial draft. Stack pinned against the registry; zmx confirmed as
  session owner; Go confirmed reference-only; gdeck findings recovered from
  history rather than restated from memory.
- 2026-08-25: Toolchain named rather than deferred — oxlint, oxfmt, vitest,
  react-doctor. The first scaffold picked Biome unprompted and was abandoned;
  leaving the choice open in the Validation section is what allowed that, so the
  stack table now carries the tooling alongside the runtime dependencies.
