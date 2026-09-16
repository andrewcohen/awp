# apps/amoeba

The window: the Vite renderer, its stack, its state, its layout and its motion.
Loaded when working under `apps/amoeba/`; repo-wide rules are in the root
`AGENTS.md`.

```
  src/electron/          the shell — menu, app://, webviews
  src/renderer/
    shell/               App, the bars, the columns, the boundaries
    panels/              the accessory strip, chat, diff, pr, the pane
    design/              palette, type, motion presets, the style guide
    data/                daemon.ts, the hooks, the atoms
    routing/             address, routes, navigation, switching
    dialogs/             every modal, and the menus that open them
    debug/               the meter
```

Three of those carry their own rules and load only when work is happening there:
`src/electron/AGENTS.md`, `src/renderer/panels/AGENTS.md`,
`src/renderer/design/AGENTS.md`.

**Only what the code cannot show is here** — silent failures, mandates, and
thresholds with a reason. Anything answerable by opening the file it is about was
cut, and so was every account of how a design was arrived at. Both are in
`docs/window.md`, which no session loads.

## The stack is chosen. Do not add a fourth thing to it

Base UI (behaviour) · StyleX (appearance) · TanStack Router · Effect Atom.
Content renderers are a deliberate exception and are listed in `package.json`.

**Base UI ships no styles and StyleX writes no behaviour.** Hand-rolling a
dropdown loses the arrow keys, the typeahead, the roving tab stop, the aria
wiring and — the one that shows up as a visual bug — the portal, without which a
popup inside a scrolling column is clipped by it.

`react-markdown`, not `marked`: the text was written by whoever opened the pull
request, and `marked` returns HTML needing `dangerouslySetInnerHTML`.

**`@vitejs/plugin-react` v6 silently ignores a `babel` option.** The React
Compiler comes through `@rolldown/plugin-babel` and StyleX rides the same pass;
the tell that it was not running was a bundle byte-identical to one built
without it.

## Base UI unmounts a hidden tab

Every panel's `useState` is destroyed by switching away from it — forty-five pull
requests were fetched over a socket and thrown away because somebody glanced at
the diff.

- **An atom rather than a module-level `let`**: a `let` holds the value and tells
  nobody, and what is wanted is the value _plus_ a subscription.
- **Plain state atoms, not `Atom.make(effect)`** — fetching stays in `data/daemon.ts`,
  the seam this window keeps between Effect and React. No provider, and no atom
  before there is a reason for one.
- **A modal belongs to the window, not to a column.** A folded sidebar is `inert`
  and zero pixels wide, so an overlay owned by it has an owner that is not on
  screen.

## A subscription answers what changes; a question answers what is

A stream carries changes from _now_, so **every list re-asks the daemon on
`onReconnect`**. A create job that finished during an outage otherwise leaves the
thread on screen, the workspace on disk, and the row saying `nothing yet` —
which is what a _failed_ creation looks like.

**Key on which jobs, not how many.** Clearing deletes terminal rows, so `.length`
returns to numbers it has already been. `finishedKey` joins the **sorted** ids;
the listing and the feed do not agree on order.

**A running job changes the sidebar, so waiting for it to stop is too late** —
`brief` can take 20 minutes. `progressKey` keys on `id:status:done.length`.

**A feed does not die the way a failure dies, and `Effect.retry` stepped over
both.** One outage, two feeds: threads arrived as `Die`, facts as `Interrupt`.
The client writes an `RpcClientError` into every request it holds, and a feed
declaring no error has nowhere to put one; whatever the socket's scope closes out
from under dies as an interrupt. The cost was every feed in both faces for the
life of the window — the calls came back after a restart and not one feed did,
with nothing on screen saying a sidebar dot had stopped moving.

**The loop stops on a flag, not on a shape of cause.** Unsubscribing is an
interruption too, so `stopped` — set by the returned closer — is the only witness
that knows _why_ the fiber was interrupted.

## Selection is an address, not a name

Routes are in `routing/routes.ts`; `routing/address.ts` is kept out of it so nothing pure imports
the router.

A session name is shortened and cannot be split back into its parts, so a session
restarted under a different shortening is a selection that silently stops
resolving. Hash history, because only one of Vite and `app://` rewrites a deep
path.

**The address is derived, never written back** — writing it would be a second
copy of something already known, and would race the first listing on launch.
`localStorage` keeps one mirror, read once in `main.tsx`.

**An address names a workspace; only the pane wants a session.** `sessionAt` was
once the only question asked, so a workspace whose agent had exited lost its
chat, diff and pull request while its directory, bookmark, thread and
conversation were all where they were left.

- `placeAt` is gated on the pair being **known** — a remembered address survives
  a quit, and the workspace it named may not.
- **`ended` is a third refusal**: zmx keeps an exited session in `zmx ls`, so the
  pane attached to a dead process and drew a blank terminal. Read the daemon's
  `ended`; zmx's is about the last _task_.
- **The sidebar draws a thread's members, not only its sessions** — a member with
  nothing running had no row, and its thread drew "nothing yet" over all of it.
  `address` is not a substitute for `Workspace.pair`: a project name may contain
  a dot.
- **`WorkspaceDir` is a call for a pure function and has to be** — a browser does
  not know the home directory.

## Everything is reachable from the keyboard, and the keys are vim's

**A mandate.** This is a terminal multiplexer with furniture around it; the
furniture answering to different keys than the thing inside it is the friction
the application exists to remove. The chords are in `routing/navigation.ts` and
`dialogs/menus.tsx`.

`ctrl+hjkl`, not bare `hjkl`: an unmodified `j` belongs to whatever is running in
the pane.

- **Capture phase, on `window`.** The emulator calls `stopPropagation`, so a
  bubble listener never hears a chord while a pane has focus.
- **`event.code`, not `event.key`** — `key` is layout-dependent and arrives
  upper-case whenever shift is down.
- **A control hidden on hover must still be focusable**: `opacity: 0`, never
  `display: none`.
- **The terminal claims to be a text field and must not be treated as one.** Its
  keyboard surface is a `contenteditable` with `role=textbox`, so an
  `isContentEditable` test reported "editing" for the whole agent column and
  every chord silently did nothing. Ask _where_ the element is, by `data-column`.
  The chords are still given up inside real `<input>`/`<textarea>`.
- `[data-nav-item]` is opt-in: every focusable element would step through
  hover-revealed row controls.
- **`⌘⇧P` and `⌘K` are left unclaimed** — the action-palette chords in every
  editor somebody here has open. `menu.ts` claims nothing on B, N or P, which is
  what leaves them reachable at all.
- Two places a chord cannot arrive: inside the web panel (a separate
  `webContents`) and on a menu item that claims it. Otherwise suspect the
  renderer — a casing collision once left a stale tree with no `cmd+P` in it.

**cmd+P defaults to the previous thread, and the current goes last of
everything.** "Last among the visited" reads as the same rule and is not: with a
single visit it puts the current thread under the cursor and makes Return a
no-op that looks broken. **Typing narrows without rescoring**, so the row under
the cursor does not move while somebody types towards it; the match is a
substring, because a thread title is a sentence somebody wrote. History is read
on every thread change, not at mount — another window may have written it.

**Arriving somewhere is not the same as being able to type there.** The pane
focused itself only when it _attached_, and the chat not at all. `App` derives a
focus key and deliberately does not change it when the session list refreshes:
**focus that moves on its own is worse than focus that has to be asked for.** The
nonce is there because closing the switcher is not a move; Base UI's restore is
turned off with `finalFocus={false}`.

## Anything that appears or disappears is animated

**A mandate.** A thing that vanishes between two frames leaves a person to work
out _what_ changed and _where it went_, every time. Duration and curve in `shell/columns.ts`, spring presets in
`design/springs.ts`.

- **One duration and one curve.** Two animations that disagree about how long a
  fold takes read as two applications.
- **Reduced motion means none, not less.** `useArriving`/`useSquish` answer
  _still_.
- **A gesture is not animated** — a transition on a dragged boundary makes the
  thing chase the pointer a frame behind. The eased style is held in state for
  the fold's duration rather than living on the element.
- **Animate a property that can be animated.** `display: none` cannot, and
  neither can a conditional render.
- **A dynamic style, not a static one** — `${MS}ms` inside `stylex.create` is a
  build error about theming rules.
- **`motion` earns its place on two things CSS cannot do**: a spring carries its
  velocity when interrupted, and `layoutId` moves one element to where another
  was.
- **Only new rows animate in** — a snapshot of the keys at mount enters still, or
  a glance at the diff springs forty.

**An animation outranks a transition in both directions, and removing an
animation does not hand the value back to a transition.** So the two cannot share
a property; put them on different elements, and delay the second by exactly the
first's duration so the handover is invisible.

**A shape at twelve pixels is its silhouette** — `border-radius` only rounds the
corners of its own box, so a squircle never leaves a circle by more than a pixel.

**`timing` and `lift` are token groups**, named `timing` because a file cannot
import both `motion`s under one name. Shadows are mostly black: a coloured shadow
reads as a glow, and a glow reads as a state.

## The window is an app, not a page

- **Nothing scrolls at the top level.** `html`, `body` and `#root` are pinned in
  `design/global.css`. `height: 100%`, never `100vh` — vh measures the visual viewport.
- **No horizontal scrollbar anywhere without being asked for by name.** A
  vertical one is the ordinary state of a list; a horizontal one means the layout
  is wrong. `overflowX: hidden`, so the fault shows as clipped text — findable —
  rather than as a scrollbar, which reads as deliberate. It is nearly always
  `width: 100%` on a flex child or a missing `minWidth: 0`: **`flex: 1` with
  `minWidth: 0` is the pair, and either alone is the bug.** Wide things scroll
  inside their own `overflow-x: auto` box, never the column.
- **Colour follows the system preference.** `useColorScheme` is
  `useSyncExternalStore` — `useState` + `useEffect` reads a frame late and
  flashes the wrong theme on launch.
- Both bars are `flex-shrink: 0` with `minHeight: 0` on the middle row, so a
  short window shrinks the columns rather than pushing the footer off.
- **The footer says nothing when there is nothing to say.** A status bar that
  always reads `0 running · 0 failed` teaches the eye to skip it.

**The traffic lights stay, and a tiling window manager is why.**
`titleBarStyle: "hidden"` works and was reverted in a minute: AeroSpace stops
managing anything that is not a _standard_ window, and an untitled window is not
one.

**The top bar is the drag handle, and the CSS property is not what makes it one.**
Electron reads the computed style, so `-webkit-app-region` is live — but the
classes stay in `design/global.css`, because StyleX drops what it does not understand
**in silence** and the failure is a window nobody can move. `probe:shell` reads
`webkitAppRegion` back out of the running window.

**The left column is a menu and a list, not two tabs**, and the review queue
opens over the window. **Nothing counts it** — a badge would be a `gh` call per
project whether or not anybody asked — and it is **not remembered across
launches**: a window that came back by itself answers a question nobody asked.

**No debug tool is behind a flag** — one nobody can find is one nobody uses — and
the meter **shows peaks beside live figures**, because by the time a hand leaves
the trackpad the live figure is zero.

## One boundary per column

A single boundary at the root is the same as none: the whole window is replaced
and whatever was being looked at goes with it. The granularity is _the part a
person can carry on without_.

**The report is selectable and there is a copy button**, and that is the feature
rather than a nicety: a stack trace that cannot be copied gets retyped from a
photograph. **`componentStack` arrives at `componentDidCatch` and nowhere else**
— it is not on the Error, and it names the component that threw rather than the
frame the throw happened in.

## StyleX fails quietly, twice

Both produce markup that is structurally right and visually wrong, with no error
anywhere, and no gate catches either.

**One set of options, two passes.** The Babel plugin turns `stylex.create` into
class names; the PostCSS plugin re-reads the same files. `dev` changes the class
names, so the two arms disagreeing yields class names no rule matches — which is
why `stylex.babel.mjs` exists and `postcss.config.mjs` imports from it. The
PostCSS pass also needs `parserOpts` naming `typescript` and `jsx`, or it dies on
the first `import type`. **A dev server started before `postcss.config.mjs`
existed keeps serving the old sheet**, which looks exactly like a StyleX bug.
Restart Vite.

**`border` and `background` shorthands are dropped in silence** — the declaration
is simply not in the output, so `border: "none"` on a `<button>` leaves the macOS
2px outset bevel and every session row becomes a little box.

```
  border: "none"          ✗ dropped     borderStyle: "none"   ✓
  background: colour      ✗ dropped     backgroundColor       ✓
  flex · font · padding · margin        ✓ these do survive
```

Verify by grepping the **built CSS** for the property, not by reading the source.

**An identifier in a static style is a build error, and it has happened three
times.** A constant from an ordinary module fails the Babel pass with a message
about _theming rules_, which is not what is wrong; the module then answers **500**
and the page renders nothing — while fmt, lint, typecheck, test and doctor are
all green, because only Vite runs that pass. A dynamic style asks no such
question. The check:

```
  curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:5273/src/renderer/panels/Composer.tsx
```

**A declaration being emitted is not evidence that anything consumes it.**
Measure the computed style on the element.

## Seeing the renderer

No gate can tell you the pane is right.

```
  bun run probe:shell    the application binary — same app://, same preloads
  Playwright chromium    for driving a GESTURE, which the probe cannot
```

The engine argument **inverted** when the shell changed: the old rule was
_WebKit, not Chromium_, because electrobun rendered in WKWebView. The window is
Chromium now, so the engine that ships is the engine to drive.

`probe:shell` answers four things a picture cannot: **errors** (a blank window
and a broken window look identical), **scroll** (`scrollWidth === clientWidth`,
both axes), **canvas** present (so "did not start" and "drew the wrong thing"
differ), and **the bridge** end to end — three processes, no test.

- **Errors first.** A screenshot of a black rectangle is not evidence of
  anything.
- **Then read the image and say what you see, check by check.** The fixture is
  built so each block fails visibly if one patch fix is not reached — descenders
  clipped, wide glyphs bleeding, box corners not meeting. A pane that merely
  "looks like a terminal" is not a pass. Under Latte the `░▒▓█` ramp runs
  light-to-dark, inverted from Macchiato, because the blocks are drawn in the
  foreground colour — stronger evidence the patched glyph path is live than the
  dark screenshot alone. **Prefer checks with that property.**
- **One appearance per process.** Both in one run had the second window answering
  `ERR_FAILED (-2)` on a URL the first had just loaded.
- **Open `#/`.** It attaches to nothing — see the root file's zmx rules.
- **One page per gesture.** A probe driving three had the second and third
  reading the composer the first left open.
- **Piercing the shadow root: Playwright's locators do, `page.evaluate` does
  not.** A check running `document.querySelectorAll` inside `evaluate` found
  nothing and would have read as "no highlight" in every state including the
  working one.
- **A single corner pixel is the wrong probe**, and cost an hour twice: the
  fixture painted it, so it reads "unchanged" for a swap that worked and one that
  did nothing. Count the canvas' most common colours instead.
- **Check selection by selecting.** `getComputedStyle(el).userSelect` came back
  empty under WebKit while `-webkit-user-select` was `text`.
- **Measure; do not assume the mechanism.** `canvas.width = 0` reads in the
  library's source like the one full redraw reachable from public API. It is not,
  and not one pixel changed. **A mechanism read out of someone else's source is a
  hypothesis.**
- Playwright installs **outside the repo** — a verification tool, not a
  dependency — and `bunx playwright install chromium` is not optional: each
  release pins an exact browser build. Point it at the dev server.
