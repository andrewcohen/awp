# The shell (apps/amoeba/src/electron) — evidence

The measurements, probe output and wrong turns behind the rules in
`apps/amoeba/src/electron/AGENTS.md`. Nothing here loads into a session; it is read when a
rule is being questioned.

### A dropped file's path is the preload's to answer

Dragging a file onto a text box writes its absolute path in. Two things about
that are not obvious, and both are the reason it needed a wire rather than a
handler.

**`File.path` is gone.** Electron removed it in 32; a `File` in the renderer is
a handle to bytes and the path is a privilege the page does not have.
`webUtils.getPathForFile` is the replacement and it is reachable only from a
preload — so `pathForFile` is on the host bridge, and in a plain browser it is
simply absent. `droppedPaths` answers `[]` there, which every caller reads as
"nothing to insert".

**A drop nobody handles navigates the window.** Chromium's default for a file
dropped on a page is to open it, which replaces the renderer with a picture of
somebody's screenshot and leaves no way back but a reload. A drop target is one
that _cancels_ `dragover`, so the cancel is what makes the feature safe as much
as what makes it work — and `main.tsx` cancels both events at `window` for
every pixel that is not a box, because the failure of forgetting one is not a
drop that does nothing.

```
  a composer, the brief   acceptsFiles(value, onValue)   spliced in at the caret
  the pane                the bytes go down the pty       as though typed
  anywhere else           cancelled at `window`           and nothing happens
```

**The path goes in raw, spaces and all.** Quoting it when it contained
whitespace was the first answer — the `sh` spelling, on the argument that the
text is going to an agent that will `cat` it — and it was asked to come out
again. That is the right call: what reads this is a person or a model, and
`'/Users/…/Screen Shots/b.png'` in the middle of a sentence is punctuation
from a language nobody here is writing.

`spliced` is a function rather than a template literal at each site for the
four ways a separator can be wrong: both ends of the text, and both sides
already spaced.

Driven in a real browser with the bridge stubbed, which is the only half a
browser cannot supply:

```
  caret in the middle   "look at |this"
  drop                  "look at /Users/…/shots/a.png this"   caret at 34
  a name with spaces    "… /Users/…/Screen Shots/b b.png this"
  a stray drop          prevented true · navigated false
```

### A native webview does not stack

The web panel is a real browser view — a `WebContentsView` the main process
creates and positions over the renderer at a rectangle it is told to occupy. An
`<iframe>` was the shorter answer and the wrong one — most of what a person
wants beside an agent sends `X-Frame-Options` or a `frame-ancestors` policy and
renders as a blank rectangle with a console error nobody sees.

**And it is not Electron's `<webview>` tag**, which its own docs discourage and
which is a `WebContentsView` underneath anyway. What the tag adds over calling
it directly is a custom element — and a custom element is exactly what the port
could not keep, because a preload runs in an isolated world and an element
defined there is invisible to the page's own scripts. So the element lives in
the renderer (`host.ts`) over a bridge that carries ids and rectangles, and the
main process holds the views (`src/electron/webviews.ts`).

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

Electrobun's tag offered two repairs and they were not interchangeable:

```
  toggleHidden(true)   the whole webview stops being drawn
  addMaskSelector(s)   holes cut where `s` matches, recomputed every 10ms
```

A mask suits something small overlapping a corner. A modal is not that — it
makes the rest of the window inert, so there is nothing left for the page
underneath to be useful for, and the mask would end up the size of the panel.
So `overlays.ts` holds a **count** of open modals and the panel hides on it.

**The port simplified that by having only the first.** Electron has no mask; the
repair is `view.setVisible(false)`, which is what `toggleHidden` now calls. The
decision above made the second one unused before it was unavailable, so nothing
was lost — which is the useful reading: a feature declined for a reason survives
a port, and one kept because it was there does not.

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

Unhiding forces a resync. While hidden the box's rectangle is pushed as zero
and the sync loop polls at 100ms, so the page otherwise returns a tenth of a
second late, which reads as the panel being slow to wake up.

**Position is not size, and only one of them has an observer.** A divider drag
moves this box without resizing it and a folding column resizes an ancestor, so
`host.ts` runs a `ResizeObserver` _and_ the poll. Electrobun's own tag polled at
the same interval, for the same reason.

**The native half can now be driven, and is.** Under electrobun it could not be
— Playwright has no Electrobun, so the check was a stubbed custom element and
counted calls, which proved the renderer half and left "does the native side
stop drawing" unwatched. `probe:shell` runs the real binary: it asks the page's
own `awpHost` for a view, tells it to run a script, and waits for the script's
answer to arrive back on `host-message`. That is three processes and the guest
preload, and no test reaches any of it.

Outside the app window the bridge is simply absent, and the panel says so in
words rather than showing an empty box — because an empty box is also what a
page that failed to load looks like.

### A second app instance is a client too, and its flags go to the wrong place

The rule above is about a _headless browser_ opening a route. There is a third
door and it is worse, because it looks like the safest possible test: launch the
**real application** with the debugger attached and drive it.

```
  electron . --remote-debugging-port=9333 --user-data-dir=<scratch>
                                          └─ goes to the APP, not to Chromium
```

Flags after the app path are the app's argv. Electron picks up
`--remote-debugging-port` anyway, so the probe _looks_ like it worked — and the
window came up on the **default profile**, read `amoeba.place` out of somebody's
real localStorage, and opened the workspace they had been looking at. Which
means it attached to that session and sized it to the probe window:

```
  [amoeba] window 1692x1370 …
  [amoeba] window 1275x1370 …    ← a real terminal reflowed three times
  [amoeba] window 1123x1370 …
```

So, for a debuggable instance: put every Chromium switch **before** the app
path, and drive the window to `#/` as the first thing after connecting, before
touching anything else. `#/` attaches to nothing and still has the accessory
column, so the web panel, the tabs and every layout question can be answered
there.

**What the debugger is worth, once it is safe.** It is the only way to exercise
a native webview at all: CDP screenshots do not include layers the compositor
draws over the page, `screencapture` needs a permission this machine has not
granted, and Playwright has no Electron driver here. What it _can_ do is press a
real tab and read the renderer's side of the contract:

```
  before   diff selected · web panel mounted · display:none, hidden
  on web   web selected  · display:block
  on diff  web unselected · display:none, hidden
```

**A synthetic `.click()` does not move a Base UI tab.** It listens for pointer
events, so the first attempt reported `web:false` after clicking web and read as
a broken control. `Input.dispatchMouseEvent` — mouseMoved, mousePressed,
mouseReleased, at the tab's own rectangle — is what works.

**And `contextBridge` objects cannot be wrapped.** Recording what the renderer
told the main process by patching `window.awpHost` fails with `Cannot redefine
property`, which is the bridge working as intended. Assert on the state the
renderer reaches instead, and on what the other process _does_ only where it
has a channel to say so.

### An orphaned webview cannot be closed by anything

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

`patches/electrobun@1.18.1.patch` fixed it at the source, because nothing
outside the element could: a `_detached` flag set in `disconnectedCallback`, and
checked twice — after the rAF, and after the request returns, where an arriving
id is removed rather than adopted.

**It was never an argument for Electron, and the port proves that both ways.**
Electron's `<webview>` is discouraged in its own docs, and `WebContentsView` is
also a native view positioned over the page by hand — same "does not stack"
property, same detach-during-init shape. `create` is still a round trip and
StrictMode still rehearses a mount inside one frame. The bug was a missing
guard, not an architecture, and the guard had to be written again.

What _did_ change is where it can live, and that is the whole benefit. The
element is the renderer's own now, so the flag is a field on an object this repo
owns — `gone` in `host.ts`, checked after the await, with the arriving view
destroyed rather than adopted. The main process holds the matching half: a
`destroy` for an id still in flight is remembered in `cancelled`, and `create`
throws its own view away when it finds it there. **Two halves, because either
side can be the one that is late.** The patch is gone with the dependency.

**It was not fixable from the consuming side under electrobun**, and that is
what forced the patch: removing the element fired a callback that did nothing;
re-appending it created a _second_ native view; a module-level singleton
re-parented fired both. Moving a node between parents is a disconnect and a
connect, and that element could survive neither. An element that is a plain
`<div>` this repo positions has no such lifecycle to lose.

The general shape, which has come up here before: **a cleanup that guards on a
field set by an async step does not run during that step.** The guard reads as
"nothing to do yet" and means "do nothing, ever".

### Hiding a native overlay must not depend on unmounting

The web panel was hidden by being **torn down**: Base UI unmounts a hidden tab,
the panel's cleanup ran, and the cleanup was the only thing that took the
`WebContentsView` down. That is electrobun's vocabulary — its tag offered
teardown and a mask and nothing else — and under Electron it is the wrong shape,
because it makes a native overlay's visibility depend on React choosing to
unmount.

Reported as "i cant switch off the web pane on the right it doesnt hide when i
switch to diff", and the cause was not in the panel at all:

```
  9:44:30  [renderer] [vite] SyntaxError: The requested module
                       '/src/renderer/refresh.ts' does not provide an export
                       named 'finishedKey'
  9:44:30  [renderer] [vite] Failed to reload /src/renderer/App.tsx
```

A hot reload that could not be applied left the tree stale, so the panel never
unmounted, so the page sat over the accessory column through every tab switch —
with no `z-index` and no gesture able to reach it. **Any tree that fails to
unmount produces this**, which is one reason too many for a thing this visible.

So the panel is `keepMounted` and the view is hidden by `setVisible`:

```
  before   tab switch → unmount → destroy → create again on the way back
           (a reload per switch, a round trip per switch, and the orphan
            hazard living in that round trip)
  after    the view lives as long as the column, and `shown` hides it
```

Three things follow, and the second is the one to copy:

- **The panel is told, not left to infer.** `shown` comes from the selected
  tab. A `ResizeObserver` reading 0x0 nearly does it, and "nearly" is the
  problem — it measures a _consequence_ where the selected tab is the cause.
  The box is still watched, because a folded column has no other tell.
- **`display: none` is written here rather than inherited from `[hidden]`.**
  Base UI marks the hidden panel `hidden` and the UA rule would do the job,
  until some class sets `display` and silently outranks it. Same rule as
  everything else in this file: do not depend on a mechanism read out of
  somebody else's source.
- **Nothing is built until the tab is first opened.** `keepMounted` puts the
  component in the tree from the first render of the column, and a native view
  is a process. The flag only goes false → true, so the creation effect still
  runs exactly once and the back button keeps its history.

Two improvements fall out rather than being arranged: switching tabs no longer
reloads the page — a login, a scroll position and a half-filled form survive —
and the create/destroy round trip per switch is gone, which is where the orphan
hazard lived.

**And a rule about working here at all: renaming an exported symbol breaks the
hot reload of every module that imports it.** This repo is edited from inside
the application it builds, so the cost is not a stale console message — it is a
window that keeps running until somebody notices it is lying. The failure is in
the app's own log, which `main.ts` forwards from the renderer, and the app runs
in a zmx session:

```
  zmx history awp-dev-app | grep -i 'failed to reload'
```

### On macOS a closed window is not a closed application

`window-all-closed` called `app.quit()` unconditionally, which is the Windows
and Linux convention. On this platform it means **a stray cmd+W ends amoeba** —
and the `activate` handler right above it, which exists to build a window
again, could never run: the app was gone before anything could activate it.

Reported as "where did the app go i think it died". It had not died. It had done
exactly what it was told, cleanly, and left this:

```
  [amoeba] renderer: http://127.0.0.1:5273
  ZMX_TASK_COMPLETED:0        ← seven minutes later, nothing in between
```

Which is the worst shape a shutdown can have: **indistinguishable from a
crash**, because an absence of complaint is all that either one leaves behind.
The exit code was 0 and the only way to tell was to notice that nothing had
asked it to stop.

So on darwin the app stays alive with no window, and cmd+tab or the dock icon
brings one back. Quitting is Quit — the menu item, cmd+Q, which `menu.ts`
already carries.

**And the menu outlives the window it was built for.** `installMenu(window)`
closes over one, the menu bar is application-wide, and it is still there while
no window is. So cmd+R after closing the last window called `webContents` on a
destroyed object and threw in the main process, where nothing renders an error.
`acting()` answers the focused window first — with several open, the menu means
the one in front, not the one the template was built for — and nothing at all
when there is none.

The View menu is also the answer to something this file got wrong out loud:
there **is** a reload accelerator, cmd+R, and a Fit to Window on cmd+alt+R. An
earlier session told somebody to restart the app because the window "has no
reload accelerator", having read `menu.ts` for the paste note and not for this.

### On macOS a paste is a menu item before it is a key

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

`apps/amoeba/src/electron/menu.ts` installs the menu, and the roles map to
NSResponder selectors — `undo:`, `paste:`, `selectAll:` — so AppKit performs a
real paste and the page gets a `paste` event carrying the text. No permission,
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

**The accelerators are spelled out.** Nothing in electrobun's JS layer assigned
a default one, and a Paste item with no key equivalent looks completely correct
in the menu bar while fixing nothing. Electron _does_ supply defaults for its
roles, and they are still written out — the claim this menu makes on the
keyboard should be readable in the file that makes it.

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

### One native view per slot, because a duplicate arrives by many routes

Reported as "a slim styleguide web view hanging out over the left pane" and,
separately, the styleguide "replicating" when devtools opened. Both are the
orphan shape recorded above — a `WebContentsView` the compositor still draws and
nothing in the renderer holds a handle to — reached by two routes no guard on
the renderer's side covers.

The first one's cause is in the app's own log, and it is this repository's
occupational hazard:

```
  [renderer] [vite] SyntaxError: The requested module '/src/renderer/Switcher.ts'
             does not provide an export named 'Switcher'
  [renderer] [vite] Failed to reload /src/renderer/App.tsx
```

A `Switcher.tsx` beside a `switcher.ts` **is one module on a case-insensitive
filesystem**, so the import resolved to the wrong file, the hot reload could not
be applied, and the tree went stale — which is exactly the state that leaves the
web panel mounted over everything. (`tsc` says so plainly: _"differs from
already included file name … only in casing"_. The pure module is `switching.ts`
now.)

Two repairs, and the second is the one to copy:

- **A window's views are dropped when its renderer navigates.** A reload
  destroys the element that owned the view without React cleanup ever running,
  so the renderer comes back with no reference to something still being drawn.
  Only the main process still has a handle, so it is the process that has to
  notice — `did-start-navigation` on the main frame, guarded on
  `isSameDocument` because the window is on a **hash history** and every route
  change is a same-document navigation.
- **`create` takes a `key`, and there is one view per (window, key).** A rule
  about how many there may be covers every route at once; a guard per route
  covers one. Every duplicate this panel has produced — StrictMode's mount
  rehearsal, a stale tree, devtools opening — was a view nothing in the
  renderer could reach, and the slot rule takes it down on the next create
  without anything having to ask.

### Pointing at something in a page you do not own

The web panel's annotator — point at an element, say what is wrong with it,
send it to the agent — is the first feature that has to reach _into_ the
webview rather than position it. The whole design is the shape of the two wires
that exist, and there are only two.

```
  this window  ──  view.executeJavascript(js)  ──►  the page
  this window  ◄──  window.__awpSendToHost     ──   the page
                     arriving as a "host-message" event on the view
```

**`executeJavascript` returns nothing.** Under electrobun it could not — the
native call was `evaluateJavaScriptWithNoCompletion` — and under Electron it is
kept that way deliberately, because a returned promise would be a second channel
beside the one below and the answer would then have two implementations. So the
picker is not asked a question; it volunteers one. That is why it is a script
that installs listeners and reports, rather than a function that is called.

**`__awpSendToHost` is in every page, and one line puts it there.**
`src/electron/preload/guest.ts` runs in every page the panel visits — sandboxed,
context isolated, exposing exactly that function and nothing else. Electrobun
gave every view its whole preload and `__electrobunSendToHost` came free; the
port had to choose what a stranger's page gets, and the answer is: this.

**The old name is still accepted.** The injected picker is a _string_ and tries
both, which costs one line and means the script is not the thing that has to
change if it is ever run under a different host.

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

**A disabled control dispatches no click, so the picker takes `pointerdown`.**
Measured over one, in the shipping engine:

```
  disabled   pointerdown · pointerup
  enabled    pointerdown · mousedown · pointerup · mouseup · click
```

The highlight drew — `elementFromPoint` does not care about `disabled` — and
the click that would have picked it was never dispatched. Reported as "i cant
select things like the send button or the tool call lines", and both are
disabled: the send while the box is empty, and a tool row's title when the call
has no output to disclose. An overlay inviting a gesture the browser then
swallows is worse than one that is simply absent.

Cancelling `pointerdown` also suppresses the compatibility mouse events, which
is how picking a link still does not navigate. And **settling installs a
one-shot click swallower**, because the picker's own listeners come off at that
moment: without it the click that follows reaches the page and activates
whatever was just picked. Verified on five targets — two disabled buttons, an
enabled button, a link and a tool row: all picked, none activated, no
navigation.

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

### The note box opens beside a keyboard aimed at another process

`autoFocus` on the composer was correct and did nothing, and the reason is one
process boundary over: **the click that picked was in the page.**

```
  the page      a WebContentsView — its own webContents, and it now has the
                keyboard because that is where the pointer went
  the renderer  draws the note box, focuses it, becomes document.activeElement
                — and receives nothing at all
```

Nothing about it reads as a focus bug from this side: the caret is in the box,
the element is `:focus`, and typing goes to the website. Only the main process
can move focus between two webContents, so `CH.focus` exists for exactly one
line — `windowOf(event)?.webContents.focus()` — and the renderer asks for it
when a pick arrives.

The placeholder is `leave a comment` rather than `what is wrong with it`. The
picker is used to point at things that are fine and ask for a change, and a
box that presumes a fault is a box that mislabels most of what goes in it.

### The shell is Electron, and it owns four seams

The window was electrobun's and is now Electron's. Almost nothing moved: the
renderer is the same Vite build, the daemon is the same separate Bun process on
the same socket, and the pane's byte stream still goes window → daemon with one
hop and one schema. What a shell is for is the handful of things only a native
process can do, and there are exactly four of them.

```
  apps/amoeba/src/electron/
    main.ts        the window, the app lifecycle, the geometry watch
    menu.ts        the Edit menu — see the paste note, it is not furniture
    protocol.ts    app:// , which serves the built renderer
    webviews.ts    the web panel's native view
    preload/host   the window's bridge: ids and rectangles
    preload/guest  one function, in a stranger's page
```

**The daemon did not move and must not.** It runs under Bun, which has
`bun:sqlite` and a pty; Electron's main process is Node. But that is not the
reason it stays out — a daemon that is a child of the window cannot outlive it,
and the whole point of zmx owning the sessions is that closing a window is not
the same as ending the work.

**`app://`, not `file://`.** The scheme replaces electrobun's `views://` and the
substitution is not cosmetic: `@pierre/diffs` tokenizes in module workers, and a
module worker refuses to load from a `file://` origin. AGENTS.md already records
what a missing worker pool looks like — the same pixels, later — so this would
have shipped as "the built app feels slow" and nothing else. A registered
standard scheme has a real origin, so workers, `fetch` and the module graph all
behave as they do against the dev server. The privileges have to be declared
before `app.ready`, which is why `declareScheme()` is called at module scope.

**`net.fetch` reads inside `app.asar`**, which was checked rather than assumed —
the packaged renderer lives in the archive, and a protocol handler that could
not read it would present as a white window in the packaged build only:

```
  renderer/index.html   200   393 bytes
  electron/main.js      200   63321 bytes
```

**Two preloads, and the guest one is the interesting half.** The window's
preload is sandboxed and exposes a bridge of five functions. The guest preload
runs inside whatever site the web panel is pointed at, and exposes _one_ — the
way back to the window. Electrobun handed every view its entire preload and the
annotator was built on what came free; here it is a deliberate list of one.

**The renderer never navigates, and nothing opens a second window.** A link in a
diff or a PR body goes to the person's browser through `shell.openExternal`; a
window opened by the _guest_ page becomes a navigation in it, because a popup
would be a second native view with no rectangle to live in.

**`trafficLightPosition` is not `trafficLightOffset`.** Electrobun's was a delta
from where AppKit would put the lights; Electron's is the position itself. The
window's top bar is 40px and centres its content at 20, the buttons are 16
across, so the group starts at 12. A knob that reads the same and means
something else is the kind of thing a port carries over unnoticed.

**The renderer's console reaches this process now.** Under electrobun it did not
— `console.log` was the first channel reached for out of the renderer and it
printed nothing, which is why the geometry watch used to make the _page_ `fetch`
a URL. Electron gives the window's console back, so `main.ts` forwards the error
level and the env var that named a log endpoint is gone. Only errors: a
main-process log that echoes every render is one nobody reads.

**`bun run probe:shell` is how any of this is known to work.** See _Seeing the
renderer_ — the harness is the application binary now, not a similar browser.
