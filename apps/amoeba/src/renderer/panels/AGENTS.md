# panels

The accessory and agent panels, the transcript, the composer and the dock,
and the renderers behind a fence. Loaded when working under
`src/renderer/panels/`; the window's own rules are in `apps/amoeba/AGENTS.md`,
and the evidence is in `docs/window.md`, which no session loads.

**A negative flag defaulting to false says nothing about whether the thing it
names exists — grep for the provider, not for the flag.** `@pierre/diffs` takes
`disableWorkerPool`, defaulting to `false`, which reads as "the pool is on"; no
provider means no context, an absent pool and a silent fall back to tokenizing
every file on the main thread. **Count the workers**: a working pool and none
deliver the same pixels, later, and neither a screenshot nor a stopwatch on a
small patch can tell them apart.

- **The pool is a module, not a component** — the library's provider builds it in
  `useState` and terminates it on unmount, and StrictMode walks into the gap.
- **It wraps the window, not the panel**, because Base UI unmounts a hidden tab.
- **A worker is addressed by URL, and a bare specifier is not one.**

**A render during a gesture ends the gesture.** An item's `version` keys the
renderer's cache, so changing it rebuilds that item's DOM and a rebuild under a
moving pointer ends the drag. Render at the end of a pointer gesture, not during
it — but only where the render changes an item's identity.

**Passing `selectedLines` at all makes the selection controlled**, and `null` is
not `undefined`: controlled means the renderer stops painting its own highlight,
so dropping the intermediate ranges left the drag working and _invisible_. A flag
raised in `onLineSelectionStart` is one call too late, and that fix changing
nothing at all is the useful half of the finding.

### Fences

`Fence.tsx` routes `diff`/`patch` to the diff panel's components, `mermaid` to a
dynamic import, and everything else to `CodeView`.

- **One highlighter, already behind three workers** — reaching for shiki directly
  would be the same library twice.
- **`PatchDiff` is the obvious component and the wrong one**: it refuses anything
  that is not exactly one file, which an agent's own one-line example is not, and
  it throws into the column's error boundary. Parse first; nothing parsed means
  it is still a patch to _read_.
- **`File` renders nothing** — it mounts, builds its shadow root and draws no
  rows, so a fenced code block had been invisible since it was written. Use the
  renderer known to work here, not the one that reads best in the README.
- **Count tokens inside the shadow root** — `el.querySelectorAll("pre span")` is
  0 in a window where highlighting works perfectly.
- **Read the failure, do not infer it.** Two hypotheses were coded against on the
  strength of a bare "did not draw"; rendering mermaid's own sentence beside the
  source answered it next run — six backticks, so one block had swallowed the
  next.
- **A plain fence must scroll, not wrap**: its line breaks **are** the content, so
  its language was deciding whether its alignment survived.

### Tool rows

**A tool row is an index, not a transcript**, and the information is not evenly
spread: for a path it is the basename, for a command the first line. **A path is
recognised narrowly — a slash and no whitespace** — because `cat src/x.ts` is a
command that mentions a path, and a naive split reports `read x.ts`.

- **Openable for anything held back, not only for output.** Gated on
  `output !== ""`, a twelve-line command drawn as one line had eleven lines
  nothing could reach.
- **A run of them is one block, keyed by its first call** — keying by the last
  remounts every row as the run grows, throwing away the disclosure state of the
  one just opened. Consecutive only. A block holding a **question** never folds,
  and neither does a call carrying a **patch**.
- **`flexShrink: 0` on the subject was wrong for a command**: a long one is
  entirely that span, and it ran 1560px past an 820px panel with no ellipsis.
  Shrink is shared in proportion to base size.
- **A completed call's output is collapsed and a failed one's is open.**
- **A question belongs on the call it is about** — the adapter emits the tool call
  _before_ it asks. A standalone row survives only for a question about a call
  this window was never told about.

### The composer and the dock

**A constant floor is the wrong way to measure one line.** One line of the brief
is 32px where one line of the body is 23, so a constant clipped it while
reporting `offsetHeight 24` against `scrollHeight 32`. `useGrow` releases the
height and measures on _every_ value, so one line is a property of the element
rather than a number in another file.

- **The textarea is a flex child, so `width: 100%` had to go.**
- **The send button is a stop while the agent works** — an empty draft's disabled
  send is exactly when a stop is wanted. **The glyph tells them apart, not the
  colour**: `warn` reads as _something has gone wrong_, and an interruption
  somebody asked for is not that.
- **A draft is kept per workspace**, because two checkouts of one piece of work
  have two conversations. Escape throws it away, silently, and only while there
  is something to throw away.

**A backdrop filter over the page colour is the page colour.** The dock had
nothing painted behind it, so the glass and the layout were one change: it is
absolute against the stage, and the scroller runs on behind it.

- **`inline-flex` does not make a flex item hug** — its display is blockified.
  `align-self: flex-start` does.
- **The activity is debounced, trailing**, so a call that finishes inside the
  window is never drawn. `layout="size"` rather than full `layout`, because the
  dock is already anchored.
- **The scroller's bottom padding is the dock's measured height**, through a
  `ResizeObserver` rather than a constant — the dock is one to four rows tall —
  and padding that grows must take a reader at the tail with it.
- **Clearance is deliberate**: exact measurement stops the last line on the
  dock's top edge, which reads as text behind the composer.
- **`colors.glass` is not opaque and the themes want different alpha.** The dark
  value is _deeper_ than the page, or the transcript reads through as a bright
  smear; `saturate` beside the blur is what separates glass from fog. Verified in
  the served sheet, because StyleX drops what it does not understand.

**Two scroll thresholds, and they must not be one.** `LEASH` is still-being-
followed, `AWAY` is far enough to offer a way back. They were briefly equal, and
because the button lives on the ledge, `away` turning over changed the dock's
height, which changed the padding, which re-followed every frame — so scrolling
back down pinned the reader to the bottom with no turn running. **`AWAY` must
exceed `LEASH` by more than the ledge is tall.** `away` is state where `stuck` is
a ref: one is drawn, the other only consulted.

### The transcript

**A `<p>` carries a 1em margin from the UA**, which put the prompt mark a whole
line above its own sentence.

**The activity line is a ledge on the composer, not the tail of the transcript**
— inside, every new activity re-laid the tail out and the follow effect chased
it. It reads the live turn's last unfinished call by its `purpose`.

**`going(status)` is not the same question as "is this happening now"**: a call
whose terminal status never arrived sits at `pending` forever, so a row from this
morning turned under a row from now. A call turns while **its own turn** is in
flight.

**One clock, and it stops.** A single 100ms interval for the whole panel — an
interval per row is a dozen timers and a dozen renders — and none at all under
reduced motion.

**The chat is read, not scanned**, so it is larger and looser than `Markdown`'s
root — a prop rather than a change to `root`, since the same component draws a PR
body in a 280px column. The user's message carries the same pair: two sizes in
one transcript read as two documents.

**The context figure has no floor**, unlike the footer's: the decision it informs
(`/new`) happens early. Raw tokens go on the hover — `18,606 of 200,000` is four
times the width of the answer to "how full is it".

### Quoting, empty states, slash commands, rename

**Quoting is selecting.** The affordance appears because something is
highlighted, not on hover: a highlight already answers "which part", a hover does
not. Settle on `pointerup`, never `selectionchange`, which fires all through a
drag; `position: fixed` at the _range's_ rectangle, so it cannot reflow the
paragraph under the pointer; and spend the highlight — quoting clears it.

**`body { user-select: none }` is right for chrome and wrong for prose.** A real
drag across a message left `getSelection().toString()` empty, so the selection
half was unreachable and **copying what an agent said did not work either**.
Check it by selecting: a synthetic `Range` lied twice, and both times the control
fell back to quoting the whole message and looked like it worked.

**An empty panel says so like it meant to** — one line of muted text at the top
left of several hundred pixels reads as a panel that failed to load, and the
accessory column is empty whenever nothing is selected. `Nothing.tsx` offers
nothing to do: where there is something, the panel says so itself.

**`/new` and `/mcp` are the window's**, and neither is expressible as a prompt —
sent as text they reach the agent as a sentence _about_ a command. **Intercepted
on an exact match of the whole draft**: `/tmp/build.log is missing` is a message
about a path, and a prefix match eats it. **`/new` is not a fork and not a
reload** — invalidate, forget, then acquire eagerly so a refusal lands on the
keypress; nothing is deleted. **`/mcp` says which half it knows**: whether the
agent's client accepted the handshake is not something ACP reports, so it says so
in a sentence rather than drawing a tick that would be a guess. `url` is on it
because a branch instance's agents reaching the daemon somebody is working in is
a real failure with nothing else on screen.

**A rename's field replaces the control rather than sitting inside it** — both
sites draw their title inside something interactive, and an input nested in
either has clicks belonging to its parent. On a row, only where the row stands in
for its whole thread.

**Two frozen copies of the title exist and neither is it.** `identity.label` is
the zmx label frozen at creation; `facts.displayName`, commented as "the one name
a person chose by hand", is the Go implementation's `workspace-state.json`,
written by hooks and frozen the same way. The thread title is the live one.

**Nothing is re-read after a write**: every thread write announces itself on the
store's feed. "The reply is the update" is the rule for calls with **no** feed.

### The shell panel

**The window has two terminals and they are addressed by slot.** `@awp-kit/pane`
kept one for the whole window and re-parented it on mount, so two panes on
screen at once took the canvas from each other — the loser goes blank, with no
error anywhere. `PaneSlot` is `stage` or `accessory`; a slot is a place in the
layout, never a session, because each one costs a 10,000-line scrollback that is
never given back. Switching shells re-attaches the accessory terminal exactly as
switching workspaces re-attaches the stage's, so **one shell is on screen at a
time** whatever the strip says.

**`+` is not `SessionStart`.** That call is idempotent because a workspace has
one agent; a second press here means a second shell, so **the daemon picks the
number** — two windows on one workspace would otherwise name the same session and
`Multiplexer.start` would hand them both the same terminal. It answers with the
name, and `shell_<n>` is an address rather than a position: closing the second of
three leaves the third where it is.

**An ended shell is not drawn**, and the daemon kills one before taking its
number back. zmx keeps a session listed after its command exits, `start` leaves
an existing name exactly as it was, and both halves of that fail silently — a
tab that draws a dead screen and takes no keys, and a `+` that reports success
and produces nothing.

**The close is one control on the strip, not a cross per tab.** A Base UI tab
_is_ a `<button>`, so a cross inside one is invalid markup with two owners for
every click.

### Gadgets

An agent's documents, drawn in this process, in a panel of their own after the
web one. **A thread accumulates them**, which is why they are not the web
panel's business: that feed holds one address per thread, so every gadget
written destroyed the last, and the one somebody asks about is rarely the newest.

**The subscription is `Accessory`'s, not the panel's** — a hidden panel is
unmounted, and a gadget written while somebody reads a diff must still produce a
tab. It is also what makes the tab exist only when the thread has one.

**Which tab is open is derived, and the pick records the newest `at` it was made
against.** A gadget written since moves that number, the pick stops matching, and
the strip is on the new one — the rule "newest unless you chose otherwise since",
with no effect correcting a stored value and no frame showing the old thread's.

**The scope is the parameter list of the function the window builds, and the
runtime has to go first.** MDX's compiled body reads its jsx runtime out of
`arguments[0]`, so `_runtime` is the first parameter and `gadgetScope` follows in
the contract's order; a scope name placed first is a document whose every element
fails to build with no sentence naming the cause. A name the contract lists and
the window does not hand over is refused rather than bound to `undefined` —
otherwise the document dies on `colors.accent` and the boundary blames the gadget
for the window's omission.

**Mounted under `address#at`, not under the address.** A revision keeps the name,
so the address is the same string: without the stamp the document is never
re-read and an error boundary that has caught stays caught, which means the
_fixed_ gadget never renders.

**A gadget needs no app window.** It is React rather than a `WebContentsView`, so
it draws in a browser tab and under a probe — check it there, and check the
throwing one too: `bun run probe:gadget` writes both.
