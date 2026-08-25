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
  `@rolldown/plugin-babel`, and StyleX rides the same pass. The tell that it was
  not running was a bundle byte-identical to one built without it.
- Vite owns the renderer and Electrobun copies `dist/renderer` in. Electrobun
  never compiles it.

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

Verify by grepping the built CSS for the property, not by reading the source:

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

## Debug tools live in the accessory column

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
