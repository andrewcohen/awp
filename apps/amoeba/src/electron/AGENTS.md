# apps/amoeba/src/electron

The shell: the window, the menu, `app://`, and the native webviews. Loaded when
working under this directory. Renderer rules are one level up in
`apps/amoeba/AGENTS.md`; repo-wide rules are in the root file.

Evidence for everything here is in `docs/shell.md`, which no session loads.

## Four seams, and that is all a shell is for

```
  main.ts        the window, the app lifecycle, the geometry watch
  menu.ts        the Edit menu — not furniture, see the paste rule
  protocol.ts    app:// , which serves the built renderer
  webviews.ts    the web panel's native views
  preload/host   the window's bridge: ids and rectangles
  preload/guest  one function, in a stranger's page
```

- **The daemon did not move and must not.** It runs under Bun (`bun:sqlite`, a
  pty) where the main process is Node — but the real reason is that a daemon
  which is a child of the window cannot outlive it, and closing a window is not
  the same as ending the work.
- **`app://`, not `file://`.** `@pierre/diffs` tokenizes in module workers, and
  a module worker refuses to load from a `file://` origin — so this would have
  shipped as "the built app feels slow" and nothing else. A registered standard
  scheme has a real origin. Privileges must be declared before `app.ready`,
  which is why `declareScheme()` is called at module scope.
- **`net.fetch` reads inside `app.asar`**, checked rather than assumed.
- **Two preloads.** The host preload is sandboxed and exposes five functions.
  The guest preload runs inside whatever site the panel visits and exposes
  **one** — the way back to the window.
- **The preloads are CommonJS**, built by `scripts/build-electron.ts` through
  `Bun.build` rather than Vite: an ESM preload requires `sandbox: false`, and
  the guest one runs inside an arbitrary website. Bun writes `.js` whatever the
  format, so the build renames to `.cjs` — the app is `"type": "module"`, and a
  CommonJS preload under a `.js` name fails at its first `require`, in a process
  with nowhere to print it.
- **The renderer never navigates.** A link goes to the person's browser through
  `shell.openExternal`; a window opened by the _guest_ page becomes a navigation
  in it, because a popup would be a native view with no rectangle to live in.
- **`trafficLightPosition` is not `trafficLightOffset`.** Electron's is the
  position itself, not a delta. A knob that reads the same and means something
  else is what a port carries over unnoticed.
- **The renderer's console reaches this process**, so `main.ts` forwards the
  error level only. A main-process log echoing every render is one nobody reads.

`bun run probe:shell` is how any of this is known to work — the harness is the
application binary, not a similar browser.

## A native webview does not stack

The web panel is a `WebContentsView` the main process positions over the
renderer at a rectangle it is told to occupy. An `<iframe>` was the shorter
answer and the wrong one: most of what a person wants beside an agent sends
`X-Frame-Options` and renders as a blank rectangle.

It is **not** Electron's `<webview>` tag either — its own docs discourage it,
it is a `WebContentsView` underneath, and what it adds is a custom element,
which a preload cannot define where the page's scripts can see it. So the
element lives in the renderer (`host.ts`) over a bridge carrying ids and
rectangles, and this process holds the views.

**Nothing rendered in the renderer can be in front of it.** There is no
`z-index` that wins; the layers are in different processes.

```
  ┌──────────────────┐  the page          ← another process, always on top
  │ ┌──────────────┐ │
  │ │ the dialog   │ │  the renderer      ← under it, whatever it says
  │ └──────────────┘ │
  └──────────────────┘
```

Nothing about it reads as a stacking problem from the dialog's side: the
backdrop dims, focus moves in, Escape closes — every part works except showing
it to a person. So `overlays.ts` holds a **count** of open modals and the panel
hides on it.

- **A count, not a flag** — a select inside a dialog portals out of it, so two
  are open at once and the inner one closes first.
- **Releasing is guarded against running twice.** StrictMode rehearses mount and
  unmount; a count that goes negative never reaches zero again.
- **The dialog announces itself; nothing detects it.** A row's `⋯` menu is
  deliberately not registered — blanking the browser for it reads as a bug in
  the browser.

Unhiding forces a resync: while hidden the rectangle is pushed as zero and the
sync loop polls at 100ms. **Position is not size**, and only one has an
observer — a divider drag moves the box without resizing it — so `host.ts` runs
a `ResizeObserver` _and_ the poll.

## Hiding must not depend on unmounting

The panel was hidden by being torn down, so its visibility depended on React
choosing to unmount. A hot reload that could not be applied left the tree stale,
the panel never unmounted, and the page sat over the accessory column through
every tab switch with no gesture able to reach it. **Any tree that fails to
unmount produces this.**

So the panel is `keepMounted` and the view is hidden by `setVisible`:

- **The panel is told, not left to infer.** `shown` comes from the selected tab.
  A `ResizeObserver` reading 0x0 nearly does it, and "nearly" is the problem: it
  measures a consequence where the tab is the cause. The box is still watched,
  because a folded column has no other tell.
- **`display: none` is written here rather than inherited from `[hidden]`**, or
  some class setting `display` silently outranks the UA rule.
- **Nothing is built until the tab is first opened** — the flag only goes false
  → true, so the creation effect runs once and the back button keeps its history.

Switching tabs no longer reloads the page, and the create/destroy round trip per
switch — where the orphan hazard lived — is gone.

## An orphaned webview cannot be closed by anything

A view stuck over everything, unmovable, surviving every tab switch and reload,
going only when the process does. The lifecycle has an await in it and cleanup
guards on a field the await sets:

```
  create()          await the main process        ← one round trip
                    this.id = id                  ← only set here
  cleanup()         if (this.id !== null) destroy ← null for that whole window
```

An element removed inside that window has already run its cleanup, with nothing
to remove; the view then arrives and attaches to something detached, which no
later cleanup will ever reach. **StrictMode walks into this on every mount.**

**Two halves, because either side can be late.** `gone` in `host.ts` is checked
after the await, and an arriving view is destroyed rather than adopted; the main
process remembers a `destroy` for an id still in flight in `cancelled`, and
`create` throws its own view away when it finds it there.

**And one view per (window, key).** A rule about how many there may be covers
every route at once, where a guard per route covers one. **A window's views are
also dropped when its renderer navigates** — a reload destroys the element
without React cleanup ever running, so only this process can notice:
`did-start-navigation` on the main frame, guarded on `isSameDocument` because
the window is on a hash history.

The general shape: **a cleanup that guards on a field set by an async step does
not run during that step.** It reads as "nothing to do yet" and means "do
nothing, ever".

## On macOS a closed window is not a closed application

`window-all-closed` calling `app.quit()` unconditionally means **a stray cmd+W
ends amoeba** — and the `activate` handler that would rebuild a window can never
run. It exits 0 with nothing in the log, which is indistinguishable from a crash.

On darwin the app stays alive with no window. Quitting is cmd+Q.

**The menu outlives the window it was built for.** `installMenu(window)` closes
over one, the menu bar is application-wide, so cmd+R after closing the last
window called `webContents` on a destroyed object and threw where nothing
renders an error. `acting()` answers the focused window first, and nothing when
there is none.

## On macOS a paste is a menu item before it is a key

cmd+V is the **key equivalent of a menu item**: AppKit turns it into the
`paste:` action only if some menu item claims it. With no menu bar, cmd+V
arrived as an ordinary keydown and nothing pasted — so `clipboard.ts` read the
clipboard itself, which WebKit gates behind a prompt. A person can click that
prompt; **dictation cannot**, which is why speaking produced a small native
paste menu and nothing else.

`menu.ts` maps roles to NSResponder selectors, so AppKit performs a real paste
and the page gets a `paste` event carrying the text.

- **It was never only the pane.** Every text field had the same hole. A macOS app
  with no Edit menu is broken for text everywhere in it.
- **A menu is a set of claims on the keyboard.** No File menu and nothing on
  cmd+N or cmd+P: those belong to the renderer, and a menu item would take them
  before the page saw them.
- **The accelerators are spelled out** even where Electron supplies defaults —
  the claim this menu makes on the keyboard should be readable in the file that
  makes it.
- **The macOS chord is deliberately not cancelled**; cancelling the keydown is
  what stops the system pasting. The keystroke route defers 120ms and asks for
  the clipboard only if no `paste` event arrived.

## Pointing at something in a page you do not own

The annotator has exactly two wires, and its whole design is their shape:

```
  this window  ──  view.executeJavascript(js)  ──►  the page
  this window  ◄──  window.__awpSendToHost     ──   arriving as "host-message"
```

**`executeJavascript` returns nothing**, deliberately — a returned promise would
be a second channel and the answer would have two implementations. So the picker
is not asked a question; it volunteers one.

- **`host-message` is the page's channel, not this feature's.** Any script on any
  site can put any object down it, so every message carries a marker and
  `messageFrom` guards on it before anything else.
- **A stringified function is not the function you wrote** — renderer files go
  through Vite, the React Compiler and StyleX's Babel pass. The injected script
  is a template literal. Regex literals _can_ cross the boundary, because
  `RegExp.toString` returns its own source.
- The script must be **idempotent** (parks itself on `window[KEY]`),
  **removable** (a highlight left on somebody's page is indistinguishable from a
  broken site) and **non-destructive** (one absolutely-positioned div, and the
  clicks it consumes are cancelled).
- **`settle` and `disarm` are different.** Clicking takes the listeners off and
  leaves the highlight; only dismissing the note takes the paint off.
- **Re-inject on `dom-ready`, not `did-navigate`** — the second fires before
  there is a `document.body` — and only while armed.
- **A disabled control dispatches no click, so the picker takes `pointerdown`.**
  The highlight drew and the pick never fired, on exactly the elements people
  wanted (a send button while empty, a tool row with no output). Cancelling
  `pointerdown` also suppresses the compatibility mouse events, which is how
  picking a link does not navigate — and **settling installs a one-shot click
  swallower**, or the click that follows activates whatever was just picked.
- **The overlay is `pointer-events: none`**, or `elementFromPoint` returns it
  over itself forever.
- **An id nobody wrote is not an address.** `#base-ui-_r_0_` is unique today and
  different on the next build, because React's `useId` is a render-order
  counter. An id is preferred over a path only when it looks like something a
  person chose; the reject list may hold only prefixes nobody would choose.

**The note box opens beside a keyboard aimed at another process.** The click
that picked was in the page, so the page has the keyboard; `autoFocus` in the
renderer is correct and receives nothing. Only this process can move focus
between two webContents — `CH.focus` exists for that one line.

## A second app instance is a client too, and its flags go to the wrong place

```
  electron . --remote-debugging-port=9333 --user-data-dir=<scratch>
                                          └─ goes to the APP, not to Chromium
```

Flags after the app path are the app's argv. Electron picks the debugging port
up anyway, so the probe _looks_ like it worked — while the window comes up on
the **default profile**, reads `amoeba.place` out of real localStorage, and
attaches to whatever workspace somebody was looking at, resizing their terminal.

Every Chromium switch goes **before** the app path. And `#/` is not enough:
`main.tsx` restores the remembered place when the hash is `""` **or** `"#/"`, and
a fresh `--user-data-dir` is empty only on its first run. Clear `amoeba.place`
before the first load (`Page.addScriptToEvaluateOnNewDocument`, then reload), or
use `#/styleguide`, which cannot attach to anything.

- **A synthetic `.click()` does not move a Base UI tab** — it listens for pointer
  events. Use `Input.dispatchMouseEvent`.
- **`contextBridge` objects cannot be wrapped** (`Cannot redefine property`).
  Assert on the state the renderer reaches instead.

## A dropped file's path is the preload's to answer

**`File.path` is gone** — Electron removed it in 32. `webUtils.getPathForFile`
is reachable only from a preload, so `pathForFile` is on the host bridge and is
simply absent in a plain browser, where `droppedPaths` answers `[]`.

**A drop nobody handles navigates the window**, replacing the renderer with a
picture of somebody's screenshot. A drop target is one that _cancels_
`dragover`, so `main.tsx` cancels both events at `window` for every pixel that
is not a box — the failure of forgetting one is not a drop that does nothing.

The path goes in **raw, spaces and all**: what reads it is a person or a model,
and a shell-quoted path mid-sentence is punctuation from a language nobody here
is writing.
