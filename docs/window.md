# The window (apps/amoeba) — evidence

The measurements, probe output and wrong turns behind the rules in
`apps/amoeba/AGENTS.md`. Nothing here loads into a session; it is read when a
rule is being questioned.

### A default that reads like "on" and means "if you provided one"

The diff panel felt chunky to scroll and slow to change revision, and none of it
was the daemon: `jj diff` answers in 40ms against a real repository. Every file
was being tokenized **on the main thread** — the same thread the terminal's
render loop, React and the pointer handlers are on — because nothing had put a
worker pool in the tree.

Nothing says so. `@pierre/diffs` takes a prop called `disableWorkerPool` which
defaults to `false`, and that reads like the pool is on unless it is turned off.
What it means is in the library's own source:

```
  const poolManager = useContext(WorkerPoolContext);
  … new CodeView(options, !disableWorkerPool ? poolManager : undefined, true)
```

No provider means no context, an absent pool, and a silent fall back to
highlighting where you stand. The general shape: **a negative flag defaulting to
false says nothing about whether the thing it names exists.** Grep for the
provider, not for the flag.

Three things about the fix, each of which replaced something that did not work:

- **The pool is a module, not a component.** The library ships
  `WorkerPoolContextProvider`, and it builds the pool in `useState` and
  terminates it in an effect cleanup once the last one unmounts — while
  `terminateWorkerPoolSingleton` clears the singleton, so the manager still held
  in that state has no workers and no way to `initialize` again. StrictMode's
  mount/unmount/remount rehearsal walks straight into it. `highlighting.tsx`
  builds the pool at module scope and only _publishes_ it.
- **It wraps the window, not the panel.** Base UI unmounts a hidden tab, so a
  provider inside the diff panel would build and destroy the pool every time
  someone looked at jobs instead.
- **A worker is addressed by URL, and a bare specifier is not one.** The
  library's worker entry is published with bare imports for a bundler to
  resolve, so it cannot be handed to `new Worker` directly. A one-line local
  module that imports it can — Vite follows a relative URL and emits a real
  worker bundle.

Measured after, and the first line is the one that matters because it is the
only one that distinguishes a working pool from no pool at all:

```
  workers spawned          3     ← counted by patching `window.Worker`
                                   before any app code ran
  revision click → redraw  119ms
  errors                   []
```

**Count the workers.** There is no visible difference between a pool that is
working and no pool: the same pixels arrive, later. A screenshot cannot tell
them apart and neither can a stopwatch on a small patch.

### A fence in a message is three different things

An agent answers in markdown, and three of its fences are not prose. All three
go through machinery this window already has rather than anything new:

````
  ```diff · ```patch   parsePatchFiles → CodeView, the same components the
                       diff panel renders every patch with
  ```mermaid           a diagram, dynamically imported
  ```<lang>            @pierre/diffs' `File`, which reads the worker pool out
                       of the same context CodeView does
````

**One highlighter, and it is already behind three workers.** `File` reads
`WorkerPoolContext` exactly as `CodeView` does — see `highlighting.tsx`, which
puts a pool there for the window's life — so a fence costs a message to a
worker rather than a tokenize on the thread the terminal's render loop is on.
Reaching for shiki directly would have been the same library twice: the diff
panel's copy resolved in three workers, and a second one resolved here.

**`PatchDiff` is the obvious component and the wrong one.** It refuses anything
that is not exactly one file, which a fence in a message usually is not:

```
  Error: FileDiff: Provided patch must contain exactly 1 file diff
```

— thrown by an agent's own one-line example, and caught by the agent column's
error boundary, which is the boundary earning its keep. So the text is parsed
first and whatever came back is drawn; nothing parsed means it is still a patch
to _read_, so it goes through shiki's `diff` grammar instead of being dropped.

**Read the failure, do not infer it.** The mermaid fallback fired repeatedly on
diagrams that were fine. Two hypotheses were built and coded against on the
strength of a bare "did not draw" — two diagrams racing, then StrictMode's
double effect invoke making one component collide with itself — and both were
wrong. Rendering mermaid's own sentence beside the source answered it on the
next run:

```````
  Parse error on line 3        ← correct for the text it was given
  ```mermaid
  graph TD; A-->B;
  ``````mermaid                ← six backticks: the fence never closed, so one
  graph TD; A-->B; B-->C;        block swallowed the next
```````

**`File` renders nothing, and `CodeView` renders the same file.** The
highlighted case was `<File file={…}>`, which is the component the library
documents for exactly this — one file, no diff. It mounts, builds its shadow
root, and draws no rows at all:

```
  pre                939 x 0, and empty
  diffs-container    <svg data-icon-sprite> and nothing else
  console            []
```

So **a fenced code block in a message had been invisible since it was
written**, and nothing said so — the message around it rendered, so what a
person saw was an agent that mentioned code and showed none. `CodeView` with a
single `type: "file"` item is the same renderer with a coordinator in front of
it, and it is the path the diff panel drives all day: the one known to work in
this window rather than the one that reads best in the library's README.

It was found by the style guide's **fake transcript**, which is what that
fixture is for — the state was otherwise reachable only by waiting for a live
agent to answer with a fenced block, and then it looks like the agent's doing.

**Count tokens inside the shadow root.** `File` and `CodeView` render into one,
so `el.querySelectorAll("pre span")` is 0 in a window where highlighting is
working perfectly — the same trap already recorded for Playwright. Walk
`shadowRoot` explicitly:

```
  pre span, from the page          0
  spans inside 3 shadow roots      137
```

### A render during a gesture ends the gesture

The diff's line selection supported dragging the whole time. What did not
survive was the element being dragged over.

An item's `version` keys the renderer's cache, so a changed version rebuilds
that item's DOM. The comment composer is an annotation on the item, so opening
it changes the version — and opening it at _pointerdown_ rebuilt the rows the
pointer was still moving across:

```
  pointerdown line 4   selection 4–4  →  composer  →  the item rebuilds
  pointermove line 9   nothing left to track
  pointerup            "line 4"
```

Nothing about that reads as a bug in the drag, and the one gesture that kept
working is why it stayed hidden:

```
  drag the numbers    line 4      ← settles mid-gesture
  click, shift-click  lines 4–9   ← two gestures, a settled render between
  drag the +          lines 4–5   ← the rebuild caught it two lines in
```

The rule: **render at the end of a pointer gesture, not during it — but only
where the render would change an item's identity.** A re-render is cheap. A
re-render that changes a `version` is a rebuild, and a rebuild is what a
gesture cannot survive. So the panel holds two selections:

```
  live       every pointermove   →  selectedLines  →  the blue band
  selection  pointerup only      →  the annotation →  a new item version
```

Two things about the shape of this, both learned by getting it wrong first.

**A flag raised in `onLineSelectionStart` is one call too late.** The library's
wrapper calls `onSelectedLinesChange` _first_ and the bracket callback after
it, so the composer was already open by the time the flag went up. That fix
changed nothing at all, which is the useful half of the finding.

**Passing `selectedLines` at all is what makes the selection controlled** —
`controlledSelection = selectedLines !== undefined`, and `null` is not
undefined. In controlled mode the renderer stops painting its own highlight and
waits to be told, so ignoring the intermediate ranges left the drag working and
_invisible_. It was reported as "it works but i cant see as im drgging", which
is a sentence about a feature that was measured as passing.

Both of those are the same shape as the worker pool two sections up: a default
that reads as "on" until the source says otherwise. Read the wrapper, not the
prop's type.

**Piercing the shadow root: Playwright's locators do, `page.evaluate` does
not.** A first attempt to check the highlight ran `document.querySelectorAll`
inside `evaluate`, found nothing, and would have read as "no highlight" in
every state including the working one. Walk `el.shadowRoot` explicitly:

```
  idle       marked 0
  dragging   marked 12, numbers 4–9, bg lab(39 -11 -5)   ← the band
  after      marked 14  (the composer's own rows join)
```

**The probe that first said dragging worked was wrong**, in the ordinary way:
it drove three gestures on one page, and the second and third read the composer
the first had left open. One page per gesture, or measure nothing. Playwright's
WebKit does deliver real `pointermove` with a stable `pointerId` — that was
checked by counting events from an init script before blaming the harness.

### A terminal listens for keys, and not everything that types is a keyboard

Dictation into the pane did nothing. Not an error, not a dropped character —
someone spoke and the terminal did not move, which is the same thing a broken
microphone looks like.

ghostty-web listens on the host element for `keydown`, `keypress`, `paste` and
the three composition events. That covers a keyboard and an input method. What
it does not cover is text _inserted_ into the document by something else —
dictation, an assistive tool writing on someone's behalf, a snippet expander.
All of those reach a page the same way: `beforeinput`, with the text in `data`
and no key event at all.

The host is `contenteditable`, so `open()` does this and nothing reads it
first:

```
  A.addEventListener("beforeinput", (E) => E.preventDefault())
```

Cancelled every time. `installDictation` runs in the capture phase to get there
ahead of it. The `preventDefault` is right, incidentally — the host must not
accumulate real DOM text under the canvas — what was missing is reading the
event before cancelling it.

**The trap is that everything is a `beforeinput`.** Ordinary typing raises one
too, and sending on both routes doubles every character a person types, which
reads as a broken keyboard and is worse than the drop. Nothing on the event
says "a key did this"; what there is, is the ordering — a key raises `keydown`
then `beforeinput` in the same task, an insertion raises `beforeinput` alone. So
a keystroke is remembered for two frames and an insertion inside that window is
taken to be its echo.

Two frames rather than the two tidier alternatives, both of which were tried in
thought and are wrong: a microtask checkpoint can run _before_ the input event
is dispatched, so the flag would already be down; and comparing `event.timeStamp`
assumes the engine copies the key's timestamp onto the input event it derives,
which nothing requires it to.

Composition is left alone. ghostty-web sends the finished text on
`compositionend`, so reading it here as well doubles a whole word.

**Measured, and the measurement is the point**, because both failure modes are
invisible from outside — a drop does nothing, and a double looks like hardware.
`page.keyboard.insertText` is exactly the dictation path: `beforeinput` with no
key event. The counts come off the meter panel, which now splits them, because
`inserted` staying at 0 while someone is speaking is the whole diagnosis and
there was nowhere to read it:

```
  insertText "dictated"   typed 0  inserted 8
  type "abcde"            typed 5  inserted 8    ← not 10; no double
  insertText "more"       typed 5  inserted 12

  without installDictation
  insertText "dictated"   typed 0  inserted 0    ← the reported symptom
```

On this host a keystroke raises no `beforeinput` at all, because ghostty-web
cancels the keydown — so the anti-doubling guard never fires in ordinary use
and could not be measured by typing. It was reached by dispatching a `keydown`
and a `beforeinput` by hand, which is the only way to a case the emulator
currently prevents and would stop preventing for any key it does not consume.

### A tool row is an index, not a transcript

Reported as "tool lines are hard to read i cant parse the important info from
them". The title is whatever the agent named — a path, a command, a query —
and drawn whole with `overflow-wrap: anywhere` it wrapped mid-word:

```
  read  apps/amoeba/src/renderer/highlig
  hting.tsx
```

**The information is not evenly spread**, and that is the whole finding. For a
path it is the **basename**; the directories are where the file happens to
live. For a command it is the **first line**; a heredoc or an `&&` chain
continues below. So `toolTitle` splits it and the row spends its width
accordingly — `lead` muted and allowed to clip, `name` never shrinking:

```
  read  packages/server/src/probe/thread-…/  create-workspace.test.ts
        └─ 281 of 470px shown, measured        └─ 202px, all of it
  ran   jj describe --stdin <<'EOF'   +4 lines
```

**A path is recognised narrowly: a slash and no whitespace.** `cat src/x.ts` is
a command that mentions a path, and a naive split at the last slash reports it
as `ran  x.ts` — the verb thrown away is the one thing that row is about.

**Openable for anything held back, not only for output.** The disclosure used
to be gated on `output !== ""`, so a twelve-line command drawn as one line had
eleven lines nothing could reach. The full text is on the tooltip either way,
which costs no pixels until it is asked for.

### One line each was not enough: a run of them is one block

Reported again as "my tool calls are still hard to look at". Two things were
still wrong, and neither is about the individual row.

**The subjects did not line up.** `read`, `edited` and `searched` are three
different widths, so every subject started somewhere else and the eye had no
edge to run down. The verb is a fixed 4rem column, **right aligned** — that
puts a clean edge on both sides of it, where left-aligning leaves a ragged gap
after every short verb.

**And a dozen equal rows are most of the transcript by height and the least of
it by interest.** `grouped()` merges _consecutive_ `ran` items into one block
with a rule down its left side, and a long block draws its last four with the
rest behind a count:

```
  │ 5 earlier calls
  │ ✗      ran  bun run typecheck                    12s
  │ ✓      ran  bun run test
  │ …      ran  bun install                        1m36s
  │ ✓      did  an unnamed tool with no kind
```

Consecutive only: a call after a sentence is a new piece of work, and merging
across the sentence loses the order things happened in. **A block is keyed by
its first call**, because a run grows by one on every update and keying it by
the last would remount every row — throwing away the disclosure state of the
one somebody just opened. And a block holding a **question** never folds: an
agent waiting on somebody is not something to hide behind a count.

**`flexShrink: 0` on the subject was wrong for a command.** It is right for a
path's basename, and a long command is _entirely_ that span — so an
unshrinkable one ran past the right edge with no ellipsis and no scrollbar to
say so, measured at 1560px inside an 820px panel. The directories now carry a
large shrink factor and the name a factor of 1: shrink is shared in proportion
to base size, so two children that both merely "can shrink" produce a path
clipped at both ends.

**The style guide draws `Transcript`, not `Row`.** It exported only the row, so
the page showed a run of calls as a run of paragraphs while the panel drew one
block — a page that lies about the thing it exists to let somebody criticise.

### An animation outranks a transition, in both directions

The sidebar's status mark grew a shape for `working` — an amoeba, which is
what this window is called — and it flicked between states. Reported as
exactly that, and the fix is not where anybody would look for it.

Three separate causes, and each one is a rule worth keeping.

**A conditional render has nothing to transition.** The first build swapped a
text node `●` for an element. That is the mandate this file already states
about `display: none`, one level up: a component that is not in the tree
cannot move. The repair is that the _bullet is the amoeba_, at rest — a
border-radius of 50% and a scale down to the 6.05px the glyph's ink actually
measured, so the states are values on one element rather than two elements
taking turns.

**A CSS animation beats a transition on the same property**, so the keyframes
imposed their 0% values on the frame the class landed:

```
  idle → working    ms  0   sx 1.060     ← already there. No transition ran
                    ms 38   sx 1.060
```

**And removing an animation does not hand the value back to a transition.**
This is the half that is genuinely surprising — the spec reads as though the
computed value changing would start one, and it does not:

```
  working → idle    ms  6   sx 0.520     ← straight from 1.037, mid-wobble
```

So the two cannot share a property, and the answer is to put them on
different elements:

```
  the span     transform     the state morph. No keyframe writes it, so it
                             is free to spring in both directions
  ::before     fill · radii  the wobble. An animation may win here
  ::after      the bud       `opacity` carries it in and out — deliberately
                             absent from its own keyframes, for this reason
```

Measured after, and the overshoot is the evidence that it interpolated at all
rather than arriving early:

```
  idle → working   0.520 → 0.828 → 1.066 → 1.112 → 1.077 → 1.060
  working → idle   1.060 → 0.645 → 0.483 → 0.476 → 0.514 → 0.520
```

Two details that fall out:

- **The wobble is delayed by exactly the morph's duration.** The static values
  on the crawling style _are_ the 0% keyframe, so the transition's target and
  the animation's first frame are the same shape and the handover is
  invisible. Two movements at once on an eleven pixel mark is one movement
  nobody can read.
- **What is left snapping is left on purpose.** `border-radius` on the pseudo
  still cuts — while the whole organism is scaling between six and twelve
  pixels, which is the movement the eye is following. Buying it back would
  mean a second animation runtime on a mark this size.

**A shape at twelve pixels is its silhouette.** Two attempts were squircles:
`border-radius` only rounds the corners of the box it is given, so a square
one never leaves a circle by more than a pixel however far the percentages
are pushed. The bud is what makes the union lopsided, and it travels on its
own slower clock so the shape changes rather than the pair merely moving.

**One state, deliberately.** Not every running row — most workspaces on a real
machine have no reported status and fall back to "something is live", so
spending the shape there would put it on the baseline. The same arithmetic as
the accent and the review queue's leading icon.

### Arriving somewhere is not the same as being able to type there

Reported as two sentences and they are two different gaps: "cmd p into thread
with terminal doesnt get focus", and "even plain chat doesnt get focus when we
enter thread".

```
  the pane   focuses itself when it ATTACHES — so arriving at a workspace whose
             session was already mounted, or switching the face back to the
             terminal, focused nothing
  the chat   had no focus call at all. Every arrival needed a click in the box
             before a key did anything, on the one face that is nothing but
             typing
```

So `App` derives a **focus key** — `project/workspace/face/nonce` — and both
faces focus themselves when it changes. Derived rather than a counter, so there
is no state to keep in step, and it deliberately does not change when the
session list refreshes or a job progresses: _focus that moves on its own is
worse than focus that has to be asked for._

**The nonce is there because closing the switcher is not a move.** Escape
changes no address, and the keyboard still has to come back to the work. Base
UI's own restore does not do it — measured at `#/`:

```
  finalFocus default   after Escape   role null, editable false   ← nothing
  finalFocus={false}   after Escape   role textbox                ← the pane
  + the window asks
```

So the dialog is told never to restore, and the window says where focus goes by
either route. After a pick the default would have been actively wrong anyway:
it hands the keyboard back to the thread just left.

**`focus` is read in the effect, not merely watched.** An absent prop is nobody
having asked, which is what makes the same effect safe on a component a fixture
also renders — and it is what satisfies react-doctor, which is right to flag an
effect that ignores its own dependency.

### cmd+P, and the first row is where you just were

Asked for exactly: "cmd p, enter flips you back to the last thread". So the
order is not alphabetical and not newest — it is recency of _visiting_, and the
default row is the **previous** thread rather than the current one.

```
  visits   [ current, previous, … ]      what this window has been looking at
  rows     [ previous, …, everything never opened, current ]
             └─ cmd+P, Return. The whole gesture being paid for
```

**The current thread goes last of everything**, and the first attempt had it
"last among the visited" — which reads as the same rule and is not. With a
single visit, a freshly opened window, that put the current thread under the
cursor and made Return a no-op that looks like a broken shortcut. It is still
in the list, because going where you already are is a thing somebody may choose
on purpose and a missing row reads as a bug.

**Typing narrows without rescoring.** A query filters the same order rather than
ranking by match quality, so the row under the cursor does not move while
somebody is typing towards it. And the match is a substring rather than fuzzy: a
thread title is a sentence somebody wrote, so the letters they remember are in
it, in order.

**The history is this window's, in localStorage**, for the reason that file
states: two windows on one machine should be able to have been looking at
different work. Read on every thread change rather than only at mount — it is
the truth, another window may have written it, and a thread change is rare.

The switcher navigates to `/t/<id>` rather than to a workspace, so the
resolution rule stays in the one place that already has it: `App` swaps a thread
address for the thread's first checkout, with `replace`.

### cmd+P does work from inside the pane, and the tell was elsewhere

Reported as "the claude code traps focus and cant cmd p from in there". Measured
both in a browser and in the app binary, with focus in the pane's own
`contenteditable`:

```
  focus       {"role":"textbox","editable":true}
  cmd+P       {"dialog":true,"placeholder":"go to a thread"}
```

The emulator installs its keydown on its own container in the **bubble** phase,
so a capture listener at `window` is decided first — the same reason cmd+N
works, recorded above. What was actually wrong was the renderer: the app's own
log had `Failed to reload /src/renderer/App.tsx` from a casing collision, so the
window was running a tree with no cmd+P in it. **A shortcut that does nothing is
a renderer that did not reload, before it is a shortcut that was claimed.**

Two places a chord genuinely cannot arrive, both worth knowing:

- **Inside the web panel.** It is a separate `webContents`, so the keyboard is
  not this renderer's at all. TODO #127.
- **A menu item that claims it.** `menu.ts` claims cmd+R, cmd+Q, cmd+W and the
  Edit items; nothing claims cmd+P or cmd+N, and adding one would take the key
  before the page ever saw it.

### A debuggable instance restores the remembered place, so `#/` is not enough

The rule above — drive to `#/` before touching anything — is necessary and was
**not sufficient**, and this cost a real attach: a probe instance came up and
read `/#/w/thicket/caption-backfill/agent`, which is a session somebody
else is in.

```
  main.tsx   restores `amoeba.place` when the hash is "" OR "#/"
             └─ so setting "#/" from the outside is indistinguishable from
                the launch state, and the restore is free to run again
```

A fresh `--user-data-dir` is not the guard either: the profile is empty on the
first run and the window writes the place into it, so the _second_ run of the
same harness restores what the first one wandered onto.

What actually holds: clear `amoeba.place` **before the renderer's first load** —
`Page.addScriptToEvaluateOnNewDocument` and then reload — or point the instance
at `#/styleguide`, which renders a different screen entirely and cannot attach
to anything. `#/styleguide` has no window chords on it, so a keyboard test
needs the first of those two.

### Debug tools live in the accessory column

The accessory column is a set of panels behind Base UI tabs — jobs first,
because that is the one someone opens on purpose, then the debug tools, which
are the ones opened when something feels wrong.

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

### Latte's accents are not text colours

The window was reported as "very gray and boring and low contrast and hard to
see". It was not short of colour — it was full of colour nobody could see.
Every chrome hue measured against its own base:

```
  latte                          macchiato
    text     6.57  AA             text     10.85  AAA
    muted    2.63  FAIL           muted     2.60  FAIL
    live     2.75  FAIL           live     10.03  AAA
    accent   2.45  FAIL           accent    8.33  AAA
    waiting  2.15  FAIL           waiting  11.16  AAA
```

4.5 is the threshold for text and 3.0 for a mark, so Latte failed both on
everything but its body text. **Catppuccin's Latte palette is tuned to be an
accent on a light surface, not ink on one** — that is upstream working as
intended, and taking its hexes at face value for text is the mistake.

The fix is the smallest one: each Latte hue darkened **along its own hue and
saturation** until it clears 4.6, rather than a different palette. Macchiato
needed nothing, which is the asymmetry a dark-first palette has and nobody
notices until they measure.

This is allowed in `tokens.stylex.ts` and would not be in `palette.ts`. The
pane's sixteen slots have to be upstream's exact hexes or a program picking
colours against them looks wrong. The chrome answers to nothing but this app,
and that file already said so.

**Compute contrast off the rendered element, not off the source hex.** A token
can be right and the rule applying it wrong, and only sampling
`getComputedStyle(el).color` against the painted background tells the two
apart. The probe does it in the page.

### Two families, and the line is address versus prose

One monospace for everything is the terminal habit, and it is what made the
furniture look like output. The line is not "chrome versus pane":

```
  mono   a slug, a bookmark, a revision, a path, a command, the pane
  ui     a title, a label, a heading, a count, a sentence, a button
```

A slug is a thing somebody will type somewhere else, and the monospace says so.

**A font stack that misses fails in silence** — the same shape as the React
Compiler that was not running and the worker pool that had no workers. Three
candidates were tried before the two that shipped, and only measurement told
them apart:

```
  system-ui / -apple-system / 'SF Pro Text'   the same face, resolves
  'Helvetica Neue'                            resolves
  Inter                                       NOT INSTALLED
  'New York'                                  NEVER RESOLVES
```

New York is at `/System/Library/Fonts/NewYork.ttf` and the family name does not
resolve in the web view, so every rule naming it fell through to Georgia while
reading as applied.

**Ship the face, do not name it.** The only way a font is certainly the one on
screen is to bundle it. `apps/amoeba/src/renderer/fonts.css` declares the faces
by hand from `@fontsource-variable/*` rather than importing those packages'
`index.css`, which declares every subset published — 1.9MB for Inter alone
against 189KB for the four latin files actually wanted. `unicode-range` is what
makes latin-ext free until a character in it appears, and that was measured:
only the two latin subsets are ever requested.

The bundled family names carry `Variable` — `Inter Variable`, not `Inter` —
which is what Fontsource declares and is also what makes the check meaningful:
neither name is installed on any machine here, so a probe finding them proves
the bundle rather than the system happening to have the face.

**Check the build as well as dev.** Vite emits the woff2 into
`dist/renderer/assets` and the built app loads them over `views://`, which is a
different loader from the dev server. What matters is that the emitted CSS
references them relatively:

```
  url(./inter-latin-wght-normal-Dx4kXJAl.woff2)     ← relative, so views:// resolves it
```

**Measure by rendering, and use a real element.** `getComputedStyle().fontFamily`
echoes the declaration back whether or not anything in it exists, and canvas
`measureText` reported every family as the same width — including ones that
certainly exist — so it was measuring its own fallback. Render a string in the
candidate and in a family nobody has, and compare:

```
  'NoSuchFaceAnywhere'   371.05    the control
  'New York'             371.05    ← identical: never found
  Georgia                398.81
  'JetBrains Mono'       528.00
```

**Width is a real criterion, not a nicety.** The sidebar's caption line lives in
a 260px column, and a monospace spends about a third more of it on the same
words. That is what settled prose on SF Pro rather than on any of the eleven
monospaces installed on this machine.

**Changing the pane's face invalidates its size.** ghostty-web sizes a cell as
`ceil(measureText("M").width)`, so the padding left in each cell is a property
of the _font_, and `paneFontSize`'s note was written about Maple Mono. It was
re-measured rather than carried over — JetBrains Mono wastes 1.7% at 18px where
Maple Mono wasted 7%.

### The type roles, and why the scale was not the thing to name

Asked as "should our text scale have some semantic tokens?", and the answer
came out of counting rather than taste. Before `typeset.ts` existed, 168 style
entries in the renderer set a type property, in 21 combinations:

```
  text.small   135 of 156 sized entries   87%
  text.body     11
  text.lead      6
  text.title     2
  a literal 10   2   ← two status dots, deliberately under the floor
  a literal 600  1   ← a weight that was not from the scale. Fixed
```

So the **scale is not what was being used**: one size does 87% of the work and
the other three are headings. Naming sizes semantically would have renamed four
things, three of which appear twice. What repeats is a _pair_:

```
  small + mono   28   a slug, a path, a revision, a command, a hex
  small + ui     15   a hint, a state word, a caption
  body  + ui      7   a container children read in
  small + medium  6   a tab, a button, the send
  small + strong  3   a section heading inside a panel
  lead  + medium  5   a panel or dialog title
```

64 entries, six shapes, each of them the same decision restated by hand once
per panel. Those six are the roles: `prose · heading · subhead · control ·
label · address`.

**They compose at the call site, and that is StyleX's doing.** `create` cannot
include another entry — there is no `include` in 0.19 — so a role is applied
where a style is:

```
  {...stylex.props(typeset.address, styles.slug)}
                                    └─ still there: it holds the colour and
                                       the truncation. The role holds the face
                                       and the size
```

**One 16px weight, not two.** `lead + medium` and `lead + strong` were both in
use for the same thing — a title over a panel and a title over a dialog — so
`heading` is `medium`, the majority and what the style guide's own specimen
advertises. Exactly one thing changed appearance: the archive dialog's title
lost 100 of weight.

**Proved a no-op by measuring, not by reading the diff.** Every element's
painted `fontSize | fontWeight | fontFamily` was captured before and after, on
`#/styleguide` and on `#/`:

```
  styleguide   449 elements   0 differing triples
  window       186 elements   0 differing triples
```

The first run was _not_ zero: twelve cells of the terminal band had fallen back
to inherited prose. The rewriter had skipped one call site —
`stylex.props(styles.cellName, literal.ink(legible(hex, palette)))`, two levels
of nested parens — and the entry had already lost its own properties. Which is
the shape worth keeping: **a refactor that strips a declaration and misses its
call site is silent**, and the only thing that catches it is asking the browser
what it painted. An audit over every `stylex.props` call naming a converted key
is what found it in one line.

Four hand-written pairs are left on purpose: the top bar's address and title,
an import field, and the style guide's own h1. Each is one site, and a role for
one site is a name with nothing to hold together.

### The type floor is 14px, and it is about text

Stated as a requirement — "stop using such tiny fonts in headers my eyesight
isnt amazing nothing smaller than idk 14" — so the scale is built around it
rather than clamped afterwards. It cost a step: 15/14/13/12/11 put four of its
five sizes under the floor, and raising them collapses the bottom two. Four
readable steps beat five where two are not, and a caption is then separated by
**weight and colour** instead of by size — which is the better axis anyway,
since size is the one that trades legibility for hierarchy.

The floor is about text. A status bullet is sized against the name beside it,
and an icon's `font-size` is its em box; both are legitimately smaller. Checked
rather than assumed — everything in the window under 14px is one of those two,
and a _word_ appearing in that list is a bug.

### An accent is spent, not applied

The first pass put the orange on every thread heading, which is a second body
colour: with an accent on everything, nothing is left to mark the one row that
matters. It is now on exactly two things that answer "this, here" — the
selected row's edge and the selected tab — plus the sidebar's pull request
number, which earns it by being the only thing on that strip pointing outside
the window.

**An accent marks a deviation from the rows around it, so the same field earns
it in one list and not in another.** The review queue found this the second way round:
its rows drew the PR number in the accent, on exactly the argument above, and
the window came back as "too much orange". In the sidebar a PR number is an
exception — most rows have none. In the review queue _every_ row is a pull request, so
the number is the baseline, and an accent on the baseline is thirty accents in a
column.

It is the same arithmetic as the review queue's leading state icon having no icon for
the ordinary case, and the same as `waiting` and `live` in the sidebar: a colour
that appears on most rows is not emphasis, it is the body text of that column.
Counted after the fix, the whole window spends the accent in four places:

```
  Accessory  the selected panel tab
  LeftColumn the selected column tab
  Sidebar    the selected row's 2px edge
  Sidebar    a row's PR number — an exception on that strip
```

### Two vocabularies, and a colour belongs to one

The review queue also borrowed the _agent_ state colours for **review** states, which
put one green on two subjects: "a session is alive" in one column and "a pull
request is approved" in the next.

```
  chrome    base · surface · raised · text · muted · border
  accent    accent
  agent     live · waiting · ready
  review    asked · warn · live · muted
```

`warn`, `live` and `muted` appear twice on purpose — a red that means broken and
a grey that means secondary are the same claim whatever the subject, and minting
`failing` and `draft` as aliases would add a name without adding a distinction.

`asked` is the one that had to exist: "somebody is asking you to look at their
work" is a review state with no agent equivalent. It was the accent (thirty
rows) and then nearly became `ready`, which is the near miss worth naming —
`ready` is blue and does mean "waiting to be read", but it is an agent state,
so one token for both would make a row's colour ambiguous exactly while somebody
is scanning for what to do next.

Mauve, from Catppuccin like every other hue here, and the only one in that table
not already spoken for: red, yellow, green, blue and orange were all taken.
Measured against each flavour's own base, and Latte's needed the same treatment
every other Latte token got — darkened along its own hue and saturation until it
clears 4.6:

```
  mauve as published    latte 4.09  FAIL      macchiato 7.48  AAA
  latte darkened        #7e35dd → 4.61  AA
```

**The `Record<ChromeRole, string>` tables in theme.ts are what caught the
half-finished job.** Adding the token and forgetting the forced-light and
forced-dark themes is a window with one wrong colour in a state nobody looks at
— which is exactly what happened to `warn` once. It is a type error now, and it
fired within a minute of the token being added.

### One boundary per column, and the message has to be copyable

A single error boundary at the root is the same thing as no boundary: the whole
window is replaced by a message and whatever was being looked at is gone with
it. What made this worth building was the diff panel throwing on a bad
option — the sidebar was fine, the terminal was fine, and all three went white.

So the granularity is _the part a person can carry on without_: each column
wraps its own, and the newest code — a panel — is the one that fails.

```
  sidebar     fails → the other two columns still work
  agent       fails → the terminal is the point, but the diff can still be read
  accessory   fails → the common one. Panels are where the new code is.
```

**The report is selectable and there is a copy button**, and that is the whole
feature rather than a nicety. A stack trace that cannot be copied is one that
gets retyped from a photograph or described in prose. Two details:

- **`componentStack` arrives at `componentDidCatch` and nowhere else** — it is
  not on the Error — so it is kept in state rather than looked up later. It is
  the most useful line in the report, because it names the component that threw
  rather than the frame the throw happened in.
- **Check selection by selecting, not by reading the declaration.**
  `getComputedStyle(el).userSelect` came back as the empty string under WebKit
  while `-webkit-user-select` was `text`. Asserting on the unprefixed property
  would have reported the feature broken when it works:

  ```
    user-select           ""       ← would have read as "not applied"
    -webkit-user-select   "text"
    triple-click          selects  ← the only one that answers the question
  ```

`Boundary` is a class, and has to be: `getDerivedStateFromError` and
`componentDidCatch` have no hook equivalent. Everything it renders is a
function component.

Proved by forcing a throw and looking, which is the only way: the fallback
appeared, named `Diff`, centred **in the 280px column rather than the window**,
and the sidebar's four rows and the terminal's canvas were both still there.

### Quoting is selecting, and the window says text is not selectable

`body { user-select: none }` in `global.css`, and the comment says why: a drag
on empty chrome should move the window rather than select it. That is right for
chrome and wrong for the one surface in this window that is _prose_ — the chat
transcript, which somebody reads for minutes, copies from, and quotes.

It was measured while building the quote control, and the measurement is the
finding: a real drag across a message left `getSelection().toString()` empty,
and `user-select` computed to `none`. So the selection half of the feature was
unreachable, and — the larger of the two — **copying what an agent said did not
work either**.

```
  before   userSelect "none"   a drag selects nothing
  after    userSelect "text"   a drag selects, and a quote carries the phrase
                               rather than the whole message
```

`Boundary` already did this for the same reason, and its note is the one to
copy: a stack trace nobody can select is one that gets retyped from a
photograph.

**Check it by selecting, not by reading the declaration.** A real drag through
`agent-browser mouse down/move/up`, then `getSelection().toString()` — a
synthetic `Range` proves nothing here, and it lied twice: once because a
focused textarea owns the selection, and once because `selectAllChildren` on a
row returned an empty string. Both times the control fell back to quoting the
whole item and looked like it worked.

### Seeing the renderer

No gate can tell you the pane is right. A terminal emulator either lays the
glyphs down correctly or it does not, and every claim in
`patches/ghostty-web@0.4.0.patch` is a claim about pixels. Three routes were
tried; two of them are dead ends worth not rediscovering.

```
  Chrome extension    list_connected_browsers → []      not connected
  osascript           "Not authorized to send Apple events"
  screencapture       "could not create image from rect"  no Screen Recording
  Playwright WebKit   worked, and was the right engine — until the port ✓
  bun run probe:shell Electron itself, which is the same binary ✓✓
```

**The engine argument inverted with the port, and it is worth reading before
copying either harness.** The old rule was _WebKit, not Chromium_: electrobun
rendered in WKWebView, the pane draws every glyph with `fillText` onto a canvas,
and canvas text rasterisation is what differs most between engines — so a
Chromium screenshot was a picture of a different renderer and proved nothing
about `patches/ghostty-web@0.4.0.patch`.

The window is Chromium now. So the engine that can be driven is the engine that
ships, and the strongest harness is no longer a similar browser but **the
application binary**: `apps/amoeba/src/electron/probe.ts` loads the built
renderer over the same `app://` scheme, through the same preloads, and answers
four things a picture cannot before taking one.

```
  errors      a blank window and a broken window look identical
  scroll      scrollWidth === clientWidth, both axes
  canvas      present, so "did not start" and "drew the wrong thing" differ
  bridge      the native webview end to end — made, told to run a script, and
              the script's answer arriving back. Three processes, no test
```

Measured after the port, one process per appearance:

```
dark  {"scroll":[1200,1200,760,760],"canvas":[638,713],"rootBg":"rgb(30, 32, 48)",
       "bridge":true,"drag":["drag"]}  webview: {"from":"awp-annotate",…}  errors: []
light {…,"rootBg":"rgb(220, 224, 232)",…}                                  errors: []
```

**One appearance per process.** Both in one run was the first shape: the second
window answered `ERR_FAILED (-2)` on a URL the first had just loaded, and with a
retry in front of it the load never settled. The script takes the scheme as an
argument and is run twice — the same rule as one page per gesture.

Playwright is still the answer for **driving** a gesture, because the Electron
probe has no mouse. Use `chromium`, not `webkit`, for the same reason the rule
above inverted: the shipping engine is now the one Playwright can drive
identically.

**Install outside the repo.** Playwright is a verification tool, not a
dependency of anything that ships, and its browser is ~77MB. Put it in a scratch
directory and the repo never learns about it:

```
cd $CLAUDE_JOB_DIR/tmp
bun add playwright
bunx playwright install chromium    # required — see below
```

The install step is not optional, and having WebKit already downloaded is not
the same as having the right one. Each Playwright release pins an exact browser
build number: a cached build from some other project is invisible to a newer
client, and `ls ~/Library/Caches/ms-playwright` showing a browser is therefore
not evidence you can skip this. The failure reads `Executable doesn't exist at
...`.

**Point it at the Vite dev server**, not at a built app — `http://127.0.0.1:5273/`
with `bun run dev` already up. Building first adds a step that can fail on its
own and tests a different artefact than the one being edited.

The script does three things, and the screenshot is the last of them. Written
out in full because a future session will not have the scratch directory this
one used — copy it, do not reconstruct it:

```js
// $CLAUDE_JOB_DIR/tmp/shot.mjs  ·  run: SHOT_DIR=$PWD bun run shot.mjs
import { chromium } from "playwright";

const out = process.env.SHOT_DIR;
const browser = await chromium.launch();

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

### StyleX fails quietly, twice

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

**An identifier in a static style is a build error, and it has now happened
three times.** `${FOLD_MS}ms` inside `stylex.create` — a constant from
`columns.ts`, not from a `.stylex.ts` file — fails the Babel pass with a
message about _theming rules_, which is not what is wrong:

```
  [BabelError] Could not resolve the path to the imported file.
  Please ensure that the theme file has a .stylex.js or .stylex.ts extension
  > 5 | import { FOLD_MS } from "./columns";
```

The module then answers **500** and the page renders nothing — and fmt, lint,
typecheck, test and doctor are all green, because only Vite runs that pass. A
dynamic style takes the value at runtime and asks no such question. The check
is to fetch the module from the dev server:

```
  curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:5273/src/renderer/Composer.tsx
```

**`border` and `background` shorthands are dropped in silence.** No error, no
warning; the declaration is simply not in the output. `border: "none"` on a
`<button>` therefore leaves the UA default, which on macOS is a 2px outset
bevel — the tell is that every session row is suddenly a little box:

```
  border: "none"          ✗ dropped     borderStyle: "none"   ✓
  background: colour      ✗ dropped     backgroundColor       ✓
  flex · font · padding · margin        ✓ these do survive
```

Verify the two silent ones by grepping the built CSS for the property, not by
reading the source:

```
  grep -oE "[;{]border:[^;}]*" apps/amoeba/dist/renderer/assets/*.css
```

### The affordance appears because something is highlighted

The request was "hover or highlight anything in agent chat and be able to reply
to it", and the first build read that as _hover_: a control on every row,
revealed when the pointer was over it. Reported back as wrong, and the reason
generalises —

```
  hover      a control per row, present whether or not anybody wants one, and
             silent about WHICH part of the row it means
  highlight  one control, only while there is a selection, beside the words
             that were selected
```

A highlight is already an answer to "which part"; a hover is not. So the
control is `position: fixed` at the _range's_ rectangle — not the row's, since
what somebody highlighted is a phrase halfway down a paragraph and a button at
the top of the message is a button about something else.

Three things that follow:

- **Settle on `pointerup`, never `selectionchange`.** That event fires all
  through a drag, and reading it there puts a control under a pointer that is
  still selecting. `selectionchange` is used for one thing only: taking the
  control away once the selection has collapsed.
- **Fixed, so it cannot move the text it is about.** In the flow it would
  reflow the paragraph under the pointer, and a selection cannot survive its
  own words moving.
- **Spend the highlight.** Quoting clears the selection, or the control stays
  over a phrase already quoted and a second press quotes it twice.

What this cannot grant is a way to _make_ a selection without a pointer: the
transcript is not a focusable region and caret browsing is the browser's to
offer. The control itself is a real button while it is shown, so Tab reaches it
— and the gap is said out loud rather than papered over with a hover control
nobody asked for.

### The chat is read, not scanned, so it is not the panel's size

Asked as "the chat font is generally a little too small and thin and maybe the
line spacing could breathe slightly", then immediately narrowed: "dont actually
thicken just try making it a little larger to start". Both halves matter — a
heavier face at the same size reads as louder rather than clearer, on a surface
somebody has open for minutes.

```
  before   14px / 1.55      ← `Markdown`'s root, which the PR panel also uses
  after    16px / 1.7       ← `reading`, on the chat only
```

A prop rather than a change to `root`: the same component draws a pull request
body in a 280px column, where 14 is right. The user's own message carries the
same pair, because two sizes in one transcript read as two documents.

### The composer was one line of text and a second line of hint

Reported twice — "make composer default line height 1", then "the composer is
still 2 lines" — and the text was one line both times. The box held a hint row
under it, permanently:

```
  before  ┌──────────────────────────────┐    after  ┌───────────────────┬──┐
          │ say something…               │           │ say something…    │↑ │
          │ tab to complete…        (↑)  │           └───────────────────┴──┘
          └──────────────────────────────┘
```

The send moved onto the text's line — `align-items: flex-end`, so it stays with
the last line as the box grows rather than floating beside the middle of a
paragraph — and the hint appears only when it has something to say, which is
when somebody is about to press the key it describes.

**A constant floor was the wrong way to measure one line.** `LEAST = 23` is one
line of `text.body` with no padding, and the new-thread brief is `text.lead`
with 4px either side: it needs 32, so the box clipped the line it was holding
while reporting `offsetHeight 24` against `scrollHeight 32` — nothing on screen
says that. `useGrow` now releases the height and measures on _every_ value,
including the empty one, so one line is a property of the element rather than a
number written in a different file.

```
  brief before   offset 24   scrollHeight 32   ← clipped, silently
  brief after    offset 32   scrollHeight 32
  chat  after    offset 23   scrollHeight 23
```

**And the textarea is a flex child now, so `width: 100%` had to go.** A
full-width child beside a button is a row wider than its box, which is this
window's most common cause of a horizontal scrollbar — `flex: 1` with
`minWidth: 0` is the pair.

### The context figure has no floor any more

Asked as "can the context usage show in the chat bottom bar", and it was
already there — behind `full >= 0.5`, on the status bar's rule that a figure
which is always on screen is furniture. That rule is right for the window's
footer and wrong here, for two reasons worth keeping:

- **The decision it informs happens early.** Somebody weighing up `/new`
  wants the number _before_ it is a problem; a figure that appears at half full
  is one that arrives after the reading it exists to give.
- **This row is already session facts.** Mode, model, effort, fast — one more
  is not what teaches the eye to skip it. The footer's argument holds because
  that bar is otherwise empty.

The warn colour past 85% stays, which is the part that was actually doing the
work of "worth minding".

**The tokens are kept beside the fraction and go on the hover.** One update
carries both, so keeping them apart would be two states that can disagree —
and `18,606 of 200,000` is four times the width of the answer to "how full is
it", so it belongs on a tooltip rather than in the row:

```
  62% context     title="124,000 of 200,000 tokens"
```

### The left column is a menu and a list, not two tabs

`work` and `review queue` used to sit beside each other as a pair of tabs, which said
the column held two lists of the same kind. It does not: the threads **are**
this column — they fill it, they are what is selected, they are what the
address points at — and the review queue is a list of work happening elsewhere that
somebody opens on purpose. A tab strip made the second one cost the first its
whole column, and made the first one look like a mode.

```
  ⊕  new thread                    ⌘N
  ⑂  pull requests
  ─────────────────────────────────────
  ▸ tabular exports
      rowan · agent
```

**The `+ thread` button at the foot of the strip went with it**, along with the
sticky footing, its sentinel and the `IntersectionObserver` that told the two
apart. It was the only way to make a workspace from this window and it is now
the first line of the menu, which is where somebody looks for it — a second
copy at the other end of the same column is two controls for one act.

**The review queue opens over the window.** A pull request row carries a number, a
title, a project, an author, a branch, a stack guide and up to three chips, and
in 260px the title is what truncates — the one field that cannot be
reconstructed from the others. The accessory column was the other candidate and
is wrong for the reason the tabs' own comment gave: that column is about the
thing already on screen, and the review queue is about everywhere else. So it is
modal, bounded at 56rem rather than the whole window — a row's action sits at
its right edge, and every rem past what the titles need is distance between the
thing read and the thing pressed.

**Nothing counts it.** A badge reading "3 to review" is the obvious next thing
and is deliberately absent: the count is a `gh` call per project, seconds each,
and a row that is always on screen would pay for it whether or not anybody
asked. The rows are fetched when the dialog mounts, which is the promise
`useReviewQueue` was written around — and the atoms are what make a second open show
the last answer at once.

**Not remembered across launches**, unlike the tab it replaces. A tab is where
the column was left standing; this is a window somebody opened, and one that
came back by itself on launch would answer a question nobody had asked.

**The dialog is App's, and a folded sidebar is what said so.** It was held in
`LeftColumn` for a few minutes, which is wrong for a reason the fold makes
plain: that column is `inert` and zero pixels wide while it is closed, so an
overlay owned by it is an overlay whose owner is not on screen — and the one
way to reach it went with the column. `NewThread` has always been App's, and
that is the shape: **a modal belongs to the window, and the control that opens
it belongs to whichever column has room for it.**

`⌘⇧R` lives beside `⌘N` for the same reason — the menu item is the discoverable
half and the chord is the half that still works with the column folded away.
It toggles, where `⌘N` and `⌘P` only open: those two hold something somebody is
part way through typing, so pressing again means "make sure", and this holds a
list, so pressing again means "put it away".

**Neither initial was available, and the obvious spare is reserved.** It was
`⌘I` while the list was called the inbox; the letter named the list and names
nothing now. `P` is the switcher, and `⌘⇧P` is deliberately **left unclaimed**
— it is the action-palette chord in every editor somebody using this has open,
and spending it on a list of pull requests would spend it on the wrong thing.
`⌘K` is left for the same reason. That leaves `R` for review, on the shift the
menu does not claim: `menu.ts` names `CommandOrControl+R` and
`CommandOrControl+Alt+R` and nothing else, and an accelerator matches an exact
set of modifiers, so plain `⌘R` still reloads. The only habit it crosses is a
browser's hard reload, which this window has no equivalent of.

### The style guide measures rather than asserting

`#/styleguide` — every colour, every type step and every recurring control on
one page, with nothing else on it. Colour is judged against its neighbours, so a
palette drawn beside a terminal and somebody's diff is a palette judged against
those; and half the tokens have no state that reliably produces them (`asked`
needs a review request, `dirty` needs a rollback to fail), so the hues most in
need of looking at were the hardest to see.

**Three sections, and no subtext anywhere.** Colors, typography, components —
plus a chat, which is the fourth because it is the newest rendering in the
window. The rule that produced this shape is worth keeping: _if a section needs
a caption to explain it, the title is wrong._ Every caption is gone; what is
left is a heading, a specimen's own name, and the numbers.

```
  colors      a row per hue: the block, the name in it, a pangram, the hex,
              the ratio · grounds are filled rows, ink is drawn on surface
  typography  the two families, then the scale, in a mono gutter
  chat        a fixture transcript — every item shape, no agent behind it
  components  a gallery, one bordered cell per specimen
```

**The candidates group is how a palette gets decided.** A hue offered for the
window goes on the page beside the tokens it would displace, measured against
the ground it would live on — not on a swatch site's white card. `channels`
reads a hex as well as an `rgb()` for that reason: a candidate and a terminal
slot are literals, and they still have to be measured.

**The composer is a component because the style guide draws it.** It was 170
lines inside `Chat.tsx`'s panel; it is `Composer.tsx` now, and what stayed
behind is everything with a consequence — what a message does, what a command
does, what an option change tells the daemon. The same argument as `Row`: a
composer copied onto that page is a copy that drifts, and then the page is a
picture of the window rather than the window.

**The tool-call group is a set to compare, not a transcript.** `verb` turns a
`toolKind` into a word and `status` into a mark, and the fixture holds one of
each: read · edited · searched · a failure with its output open · a completed
call whose output is collapsed · an in-progress call past ten seconds · a call
with no kind at all (`did`) · and a permission with no call above it. Two of
those are states a live agent produces rarely and a fixture produces on demand.

Worth knowing while reading it: **a completed call's output is collapsed and a
failed one's is open** — `useState(item.status === "failed")`, and the title is
the disclosure. That is deliberate: a successful `cat` is noise, and a failure
is the one output somebody wants without asking.

**The chat fixture is not a picture of a chat.** It imports the panel's own
`Row`, so it cannot drift — and its pair is a workspace that does not exist,
so pressing `Allow Once` refuses, which is the honest answer. It found a real
bug on its first render: see the fence note above.

**It is a visual guide, so there is almost no text on it.** The first version
carried the argument for each decision onto the page, and was reported back as
"awful — why is there so many paragraphs of text". Measured:

```
  before   678 words · 9 paragraphs of 15–67 words
  after    179 words · one paragraph, which is the markdown specimen's own
```

Somebody opening this is comparing hues and spacing, and prose is the thing they
read past to do it. What is left is a heading per section, a caption of a few
words where a section would otherwise be ambiguous, and the specimens; anything
a person might want in words is a `title`, which costs no pixels until asked
for. The reasoning lives here and in that file's comments.

**A route with a hand-made branch in `App`, not a panel in the accessory
strip.** The panels are about the work; a page of swatches is about the
application, and in the strip it would be a permanent empty room in the column
somebody switches most. The route tree renders no `Outlet`, so a child route's
component would never draw — `App` reads the location and picks between
`StyleGuide` and `Window`, above every one of the window's hooks. `STYLE_GUIDE`
lives in `address.ts` because `routes.ts` names `App`, and `import/no-cycle` is
on repo-wide.

**Every ratio is computed off `getComputedStyle` on the swatch that was
painted.** A token can be right and the rule applying it wrong, and the numbers
in this file's Latte section went into a comment that nothing keeps true. The
page cannot go stale: change a token and the verdicts move with it.

**Ink and ground are different measurements, and the first version only had
one.** Each is a tile that reports the hex it painted and the ratio that
measured — and a hue is either written in or written on:

```
  ink      the word in the hue, on the panel's surface     live · muted · warn
  ground   the row in the hue, the word in `text`          raised · page · border
```

Drawing every role as ink measured `surface` against `surface` and reported
`1.00 FAIL` for five rows that are fine. The probe caught it, and the number was
real — it was the answer to a question nobody asks.

Two findings from the first honest run, both against `surface`:

```
  macchiato  muted    4.14  FAIL   ← under AA. This file's own table has it at
                                     2.60 against base, so it is worse there
  latte      border   4.39  FAIL   ← text on a SELECTED row just misses AA
```

Neither is a bug in the page. `muted` is a subtitle colour and `border` is the
fill behind the selected row, so both carry real words.

### The window is an app, not a page

Two rules that hold everywhere in the renderer:

- **Nothing scrolls at the top level.** `html`, `body` and `#root` are pinned in
  `global.css`. A column scrolls its own content; the document never does, and
  overflow that reaches the window is meant to be visible as a bug rather than
  absorbed by a scrollbar. `height: 100%`, never `100vh` — vh measures the
  visual viewport, which is a different number as soon as anything insets the
  window.
- **No horizontal scrollbar anywhere, without being asked for by name.** A
  vertical scrollbar means there is more content than height, which is the
  ordinary state of a list. A horizontal one means the layout is wrong: some
  child was allowed to be wider than the column holding it, and scrolling
  sideways to read a name is not the repair for a name that should have been
  truncated. Set `overflowX: hidden` so the fault shows up as clipped text,
  which is findable, rather than as a scrollbar, which reads as deliberate.

  It is nearly always one of two things, and both have bitten here:

  ```
    width: 100%  on a flex child   a full-width child plus a sibling is
                                   wider than the row — 236px of column
                                   against 240px of content
    no minWidth: 0                 a flex item will not shrink below its
                                   content, so a long name pushes the row
  ```

  `flex: 1` **with** `minWidth: 0` is the pair. Either alone is the bug. The
  wide things — a table, a diagram, a code block — scroll inside their own
  `overflow-x: auto` box, which is a deliberate container and not the column.

- **Colour follows the system preference.** `color-scheme: light dark` for the
  engine's own furniture, and `useColorScheme` — `useSyncExternalStore`, not
  `useState` + `useEffect`, which reads a frame late and flashes the wrong theme
  on launch.

Latte is not Macchiato with the ends swapped. Its ANSI black is subtext1 rather
than surface1, because the mirror of Macchiato's choice is `#bcc0cc`, which
against a near-white background is not ink. `palette.ts` says so at the table.

The pane recolours through `setPaneTheme`, and **it can only ever half work in
ghostty-web 0.4.0** — the library says so itself, in the option handler nobody
had read:

```
  case "theme":
    console.warn("ghostty-web: theme changes after open() are not yet fully supported");
```

The colours are compiled into the wasm terminal when it is built.
`buildWasmConfig` hands the emulator `fgColor`, `bgColor`, `cursorColor` and the
sixteen-colour palette, and the only thing that rebuilds that config is
`reset()` — which frees the wasm terminal and makes a new one, taking the
scrollback with it. For a pane watching an agent work that is a worse outcome
than the wrong colours.

So `setPaneTheme` repaints the _renderer's_ half: the ground, and any cell whose
colour is the default rather than one the program asked for. `clear()` fills the
ground and `render(buffer, forceAll)` redraws every line — both public, and
`render(…, true)` is exactly what the library calls on open. Counted on the
fixture, latte-base pixels after switching to light:

```
  canvas.width = 0            0     ← the nudge this replaced
  clear() + render(forceAll)  263
```

Three things worth keeping.

**The nudge never did anything.** Setting `canvas.width = 0` to put the canvas'
pixel size in disagreement with the renderer's metrics reads in the source like
the one full redraw reachable from public API. It is not one, the canvas returns
to its own size, and not one pixel changes. That was written into this file as a
finding without a pixel ever being sampled — the general shape being that a
mechanism read out of someone else's source is a hypothesis.

**A single corner pixel is the wrong probe, and it cost an hour twice.** The
fixture draws colour ramps and blocks, so the corner is whatever the fixture
painted there rather than the theme's ground — it reads "unchanged" for a swap
that worked and for one that did nothing, alike. Count the canvas' most common
colours instead:

```
  dark   [["36,39,58", 11052], …]
  light  [["36,39,58", 10793], ["239,241,245", 263], …]
                                └─ the ground that did recolour
```

**A reflow does repaint**, because `Terminal.resize` calls
`renderer.render(wasmTerm, true, …)` outright — but it early-returns on
unchanged dimensions, so it cannot be used as a repaint: resizing to `rows - 1`
and back fires `resizeEmitter` twice and reflows the real session.

What is left of task #23 is the patch. `patches/ghostty-web@0.4.0.patch` already
exists, and a `setTheme` that rebuilds the wasm config while keeping the buffer
is where it goes.

### The window is two bars with three columns between them

The columns used to run edge to edge, top to bottom, and everything the window
had to say about _itself_ had to borrow space from a column already spoken for —
the appearance toggle ended up in a sidebar footer for exactly that reason.

```
  ┌──────────────────────────────────────────────┐  header · drag region
  │ sidebar │ agent            │ accessory       │  the only row that flexes
  └──────────────────────────────────────────────┘  footer · appearance, jobs
```

Two consequences, both of which replaced something:

- **The traffic lights stay, and a tiling window manager is why.** The window
  is `hiddenInset`, so the controls float over the first 5.25rem and the top
  bar's start padding clears them.

  `titleBarStyle: "hidden"` was tried. It works — the lights go, the Window
  menu covers close and minimise, the drag region moves it — and it was
  reverted within a minute, because **AeroSpace stopped managing the window**.
  That is not a bug in either program: a tiler picks windows out through the
  accessibility API and skips anything that is not a _standard_ window, and an
  untitled window is not one. On a 3440x1440 display the difference is the
  whole point of the display:

  ```
    hiddenInset   3424x1393    tiled to the screen, less the gaps
    hidden        whatever it was last dragged to
  ```

  Three circles are a small price for being a window somebody's tiler will
  manage, and the person running the tiler is the person using this.
  `trafficLightOffset: { x, y }` is the knob actually available if they are in
  the way — it moves them, it cannot remove them.

- **The top bar is the drag handle, and the CSS property is not what makes it
  one.** This is worth reading before trusting it, because an earlier version
  of this note was wrong in the way that is hardest to catch: it said
  `-webkit-app-region: drag` was the mechanism and told you to check the built
  CSS. The CSS _is_ emitted. Nothing reads it.

  Electrobun's preload matched on the DOM, and on exactly two things:

  ```
    target.closest('[style*="app-region"][style*="drag"]')   an INLINE style
    target.closest(".electrobun-webkit-app-region-drag")     its own class
  ```

  StyleX produces neither — it produces a class of its own plus a stylesheet
  rule. So under electrobun the property had never moved this window. It moved
  because `hiddenInset` left a real title bar behind the strip and AppKit was
  doing the work, and the bug was invisible for exactly as long as that title
  bar existed.

  **Electron reads the computed style, so the declaration is live again** — and
  the classes stayed, pointed at two rules in `global.css`. That is not belt and
  braces: StyleX drops declarations it does not understand _in silence_, which
  is already recorded here for `border` and `background`, and the failure that
  produces is a window nobody can move. A property written in a hand-authored
  sheet cannot be lost by a compiler that never sees it. `withRegion` in
  `Bars.tsx` appends `awp-app-region-drag`, and everything interactive in the
  bar wears the `no-drag` counterpart.

  Verified rather than assumed: `probe:shell` reads
  `getComputedStyle(bar).webkitAppRegion` back out of the running window and it
  says `drag`.

  The general shape, which has come up here more than once: **a declaration
  being emitted is not evidence that anything consumes it.** The same mistake
  as the worker pool that had no workers and the React Compiler that was not
  running. Grep for the reader, not for the declaration.

Both bars are `flex-shrink: 0` in a column layout with `minHeight: 0` on the
middle row, so a short window shrinks the columns rather than pushing the footer
off the bottom — which is the usual way a flex column grows the scrollbar
`global.css` says it must not have.

The footer says nothing when there is nothing to say. A status bar that always
reads `0 running · 0 failed` teaches the eye to skip it, which costs exactly the
one moment it exists for.

### Transparent is not a colour, and reading it as one is silent

The style guide's entire job is measuring, and it was confidently wrong in both
themes for as long as it has existed. Every ink was measured against the _rows
container_, which has no background of its own:

```
  getComputedStyle(rows).backgroundColor   "rgba(0, 0, 0, 0)"
  channels(…)                              [0, 0, 0]      ← black
```

So the page reported every hue against black. What that looked like:

```
  before   text 2.63 FAIL   muted 3.43 FAIL   accent 3.44 FAIL   base 1.00 FAIL
  after    text 7.06 AAA    muted 5.41 AA     accent 5.39 AA     base 6.04 AA
```

Five `1.00 FAIL` rows and a page of red on a palette that is fine — and the
numbers were _plausible_, which is what made it survive: an earlier session
read them as a finding and wrote two of them into this file.

Two halves to the fix, and the first is the one that generalises. **`channels`
refuses alpha 0** rather than returning three channels for a colour nobody
painted — a wrong ratio is worse than none, because it sends the reader to
darken a token that was already right. A _partly_ transparent colour is still
read by its own channels, which is an approximation and is documented as one;
alpha 0 is not an approximation of anything.

Second, `groundAbove` walks up from the row to the first thing actually
painted, and both modes then measure **the word against what is behind it** —
which is the only pair an eye judges. What differs between ink and ground is
which half of that pair is the hue, and therefore which hex the row reports.

### Two sets of slash commands, and only one is intercepted

A skill is a slash command, so `/bro` working in the terminal and doing nothing
in the chat was one dropped update: `available_commands_update`, which this
daemon threw away under a comment saying it "says nothing a person reads". Both
updates dismissed that way turned out to matter — the other was the only place
the context figure exists.

```
  /new · /mcp   the WINDOW acts. Not expressible as a prompt: sent as text
                they reach the agent as a sentence about a command
  /bro · …      delivered as ordinary text, and that is all it takes — the
                adapter's `promptToClaude` passes `/bro` through to the CLI
```

So `commandOf` answers **only** the window's two, and `matching` lists both
sets. Getting that backwards is the failure worth naming: an agent command run
by the window is a keystroke that clears the box and sends nothing.

Measured against a real adapter — `probe:chat` in a temp directory with no
`.claude` of its own, so everything came from the machine's:

```
  commands    54
    /bro              Restate the last message in plain human language
    /commit           Create a Conventional Commit message …
  updates     2 command list(s) — pushed, never asked for
```

**Pushed, so it is an update and not a call.** The adapter's own comment says
skills are "discovered dynamically as the agent works in a subdirectory", so a
client that asked once would be right until the agent learned something. It
goes through the daemon's transcript like every other update, which is what
tells a window that opens later without a second call — and the list **replaces**
rather than merging, so an empty list is an answer.

**`/usage` needed nothing.** Asked as "can we support /usage in chat", and the
answer is what the two-sets rule buys: the adapter advertises it, so it is in
the menu, and it is a prompt, so sending it is the whole of supporting it.
Measured against a real adapter — sent as text, answered as an ordinary agent
message:

```
  /usage  →  You are currently using your subscription to power your Claude
             Code usage
             Current session: 79% used · resets Sep 9 at 1:40pm
             Current week (all models): 22% used · resets Sep 11 at 6am
```

57 commands are advertised on this machine. The one collision worth knowing is
`/mcp`: the agent has one and so does this window, and the window wins because
`commandOf` only ever answers its own two. That is the right way round here —
ours says which server the _daemon_ handed this conversation and where it is
bound, which is a question about awp rather than about the agent.

**A fresh session now emits an update of its own accord**, and that broke a
probe check rather than a test: "replayed nothing" counted every update, so a
working fork reported as having replayed something. `spoken()` counts the
transcript's own kinds. The general shape is the one worth keeping — _a new
event on a stream invalidates every assertion that counted the stream._

### Two slash commands, and they are the window's

`/new` and `/mcp` in the chat composer. Claude Code has its own slash commands
and the adapter advertises them — `available_commands_update`, which `chat.ts`
drops — and these are not those: neither is expressible as a prompt, and sent as
text they reach the agent as a sentence _about_ a command, which the agent then
answers.

**Intercepted on an exact match of the whole draft.** `/new` is a command and
`/tmp/build.log is missing` is a message about a path; a prefix match eats the
second. There is no escape syntax because there is nothing to escape. The menu
appears only while the draft is a bare `/word`, for the same reason.

**`/new` is not a fork and not a reload.** `ChatFork` copies the conversation
the _terminal_ is having; this forgets the one the chat is having — the stored
session id goes, the adapter holding it is invalidated, and the next open is a
`session/new`. Nothing is deleted: the transcript is on disk and still loadable,
it simply is not this workspace's any more. The three steps are the same shape
as `openTerminal`'s, and the order matters — invalidate, then forget, then
acquire eagerly so a refusal lands on the keypress.

**`/mcp` says which half it knows.** The daemon hands every conversation an MCP
server on every open, so what it knows for certain is _what it handed over_.
Whether the agent's own client accepted the handshake is not something ACP
reports and there is no call that asks — so the panel says so in a sentence
rather than drawing a tick that would be a guess. An agent that never connected
otherwise looks exactly like one that was never asked to use a tool.

The two fields worth being on screen, and `McpStatus` is composed from the same
functions `chat.ts` passes rather than from a description of them:

```
  cwd   the whole of the server's scope — no tool takes a workspace argument,
        so this path is WHY a conversation cannot reach another checkout
  url   which daemon the spawned server talks to. A second instance's agents
        reaching the instance somebody is working in is a real failure with
        nothing else on screen to show it
```

Read against a branch daemon, which is the case that field exists for:

```
  url    ws://127.0.0.1:5284
  cwd    /Users/…/.awp/workspaces/awp/awp-kit-amoeba
  tools  awp_thread · awp_review_comments · awp_file_finding · awp_browse ·
         awp_tasks · awp_task
```

Descriptions are cut to their first sentence. A tool description is written for
a model choosing between tools and is a paragraph; a person scanning a list
reads none of it.

#### A running job changes the sidebar, so waiting for it to stop is too late

The window re-read the sessions and the threads when the set of **finished**
jobs changed, on the premise that a finished job is when there is something new
to see. A chat-face thread proved the premise wrong:

```
  1 workspace   jj workspace add
  2 bookmark    jj bookmark set
  3 session     zmx run -d            ← the sidebar can draw a row from here
  4 claim       the thread takes it   ← and the row belongs under its thread
  5 brief       Chat.brief — sends, then WAITS for the turn to end, up to 20
                minutes. The job is `running` for the whole first answer
```

So on a chat-face create the two things the sidebar needs land at steps 3 and 4,
and the job does not go terminal until the agent has finished answering — or,
if it stopped to ask a permission nobody can see, not at all. Reported as "i
cant connect to the chat in the new opentui thread", and what was on screen was
a thread reading **`nothing yet`** over a workspace that was on disk, with a
session running in it and a briefed agent halfway through a turn. The row is
how a person gets into it, so "no row" and "no chat" are the same sentence from
outside.

Measured while it was happening, which is what separated the window from the
daemon: `ChatOpen` over the rpc replayed a live conversation — tool calls, a
`permission-0` with three options, `working…` — and the panel rendered it
perfectly when addressed by route. Nothing was broken except when the window
looked.

`progressKey` keys on `id:status:done.length` per job. A step boundary is a
record save and therefore a push down `JobChanges`, so the claim now reaches the
sidebar in the second it happens. The cost is a re-read per step of every job —
four for a create, two socket round trips each, against a daemon holding both
answers in memory.

**The terminal face hid it**, because `zmx send` returns immediately and the job
was terminal a second after the claim. The general shape is the one this file
keeps recording: a trigger derived from a _proxy_ for the event works until
something changes how long the proxy takes.

#### A stream carries changes from now, so it is not a substitute for asking

Every list in the window re-asks the daemon when the socket comes back —
`onReconnect`, in `useThreads`, `useProjects`, `useReviewQueue`, `usePullRequest`.
The jobs hook was the only one that did not, and its own stream is exactly
why it had to.

```
  listJobs()     everything, as of now      ← taken once, at mount
  JobChanges     every change FROM now      ← resubscribed on reconnect
                 └─ so a job that went terminal while the socket was down
                    arrives nowhere at all. The feed carries on from `now`,
                    and `now` is after the thing that happened
```

**What that cost was not the jobs panel.** It was the sidebar. `App.tsx`
re-reads the sessions and the threads when the jobs that have _stopped_
change, because a job is the only thing that creates a session — so a create
job that finished during an outage left the window with no reason to look
again. The thread was on screen, the workspace was on disk, and the row said
`nothing yet`, which is precisely what a thread whose creation _failed_ looks
like.

**And the key is which jobs, not how many.** It was `.length`, which only
moves when a job finishes _and_ nothing else has left the list — and clearing
the panel deletes terminal rows, so the count falls and the next completion
returns it to a number it has already been. No change, therefore no refresh,
for exactly the job somebody is waiting on. `finishedKey` in `refresh.ts`
joins the sorted ids instead; sorted, because the listing and the feed do not
agree on order and an order-dependent key would re-read on nothing.

The general shape, which this file records twice already in other words:
**a subscription answers what changes, and a question answers what is.**
Anything that resubscribes has to ask again as well, or it is up to date on
everything except what it missed.

#### An address names a workspace; only the pane wants a session

`sessionAt` was the only question asked of an address, and every panel beside
the terminal was answered by it. So a workspace whose agent had exited had no
chat, no diff and no pull request — while its directory, its bookmark, its
thread and its conversation on disk were all exactly where they were left.

```
  a session exists   →  the row resolves  →  everything opens
  nothing running    →  nothing resolves  →  the conversation is unreachable,
                                             and reads as lost work
```

Reported as "the test thread is orphaned i think? i cant get into the chat",
which is the sentence to keep: **nothing about the symptom points at the
address.** The chat needs no pty at all — `ChatOpen` takes a pair and derives
the directory — so the one part of the window that could not have cared was
the part that stopped working.

Two questions now, and each caller asks the one it means:

```
  sessionAt   the session, if it is here and can be attached to   the pane
  placeAt     the workspace, running or not                       everything else
```

**`placeAt` is gated on the pair being _known_, by two sources.** A session
carrying the identity is the ordinary case; a live thread holding the pair is
what covers the case this exists for. A remembered address survives a quit and
the workspace it named may not, so an unknown pair answers nothing rather than
opening panels onto a directory nothing has heard of.

**`ended` is a third refusal, and it was found in the wild.** The old test was
presence plus no refusal, and zmx keeps an exited session in `zmx ls`:

```
  name=awp.awp.test.agent  ended=1788891181  exit_code=127
```

So the pane attached to a process that was not running, which draws a blank
terminal — indistinguishable from a terminal that failed to start. Read the
daemon's `ended` and not zmx's, incidentally: zmx's is about the last **task**,
and `withProcesses` overwrites it from the process table. That distinction is
already recorded further up and it is what makes the field usable here.

**The sidebar draws a thread's members, not only its sessions.** Every row on
that strip came from `groupByWorkspace(sessions)`, so a member with nothing
running had no row and its thread drew "nothing yet" over all of it.
`unstarted` builds the row from the member; running rows come first, and a
member already covered by a session is not drawn twice.

`Workspace.pair` exists because of that. Every caller used to read
`sessions[0].identity`, which is nothing for a row with no sessions — and
`address` is not a substitute: it is `project.workspace` for a tooltip, and a
project name may contain a dot, so splitting it back is the same mistake as
splitting a session name.

**The one act is `SessionStart`, and it is on the pane's face only.** Everything
else about a dead workspace is a question; the terminal is the thing that is
actually gone. `NoSession` replaced the _fixture_, which is what `Pane` drew
with no session name — colour ramps and box drawing, which reads as a bug in
the terminal rather than as an answer.

**`WorkspaceDir` is a call for a pure function, and has to be.** The path is
`~/.awp/workspaces/<project>/<workspace>`, and the renderer cannot compose it:
a browser does not know the home directory, and `import/no-nodejs-modules` is
on for the renderer for exactly this reason. Same argument as `SessionIdentity`
being on the wire — a client re-deriving a daemon's rule is a second
implementation, and the copy that drifts is the one nobody tests.

`bun run probe:session-start` is what proves the act, and one line of its
output is the whole reason it exists:

```
  before        awp.awp.test.agent ended=false exit=127
  started       awp.awp.test.agent
  after         ended=false exit=127 pid=48016      ← byte for byte "before"
  running in it claude                              ← the only line that answers it
```

**Every field in the listing is about the session, and none of them says
whether the agent came up.** `exit_code` is the previous task's and stays the
newest one until the new task finishes; `ended` is about the process, which is
the shell either way. The first read of a start that worked perfectly is
identical to the read before it, and was taken as "nothing happened" once.
`busy` is the field that answers it, is deliberately not on the wire, and the
probe therefore reads a child of the session's pid — the first half of the same
rule `withProcesses` applies.

#### And a feed does not die the way a failure dies

The section above is right and was not enough. Both faces wrapped every feed in
`Effect.retry`, under a comment saying an rpc stream is a request and its fiber
dies with the connection — which is true. What neither of them survived is
_how_ it dies. Measured against a real daemon, killed with two feeds open on
it:

```
  threads   Die("Expected never at [\"cause\"][\"failures\"][1][\"error\"]")
  facts     Interrupt(7)
```

One outage, two feeds, and **neither arrives as a failure**. The client writes
an `RpcClientError` into every request it still holds, and a feed whose
contract declares no error at all has nowhere to put one — so it dies as a
defect; whatever the socket's scope closes out from under instead dies as an
interrupt. `Effect.retry` acts on failures, so it stepped over both, and the
`catchCause` behind it swallowed them.

What that cost is every feed in both faces, for the life of the window:

```
  after a restart, before   SessionList · JobList · ThreadList · ProjectList
                            └─ the calls came back. Not one feed did
  after a restart, after    + PageChanges · JobChanges · ThreadChanges ·
                              WorkspaceFactsChanges
```

Read off the frames the window actually sent, which is the only place the two
are distinguishable: the status bar said the daemon was fine and the lists were
correct **once**, so nothing on screen says a job has stopped reporting
progress, a sidebar dot has stopped moving, or an agent's `awp_browse` will
never arrive.

**The loop stops on one thing, and it is not a shape of cause.** Unsubscribing
is an interruption too, so no reading of the cause can tell it from a dropped
connection — an attempt to keep `Cause.hasInterrupts` as the test passed the
threads feed and left the facts feed exactly as dead as before. `stopped` is a
flag set by the returned closer, which is the only witness that knows _why_ the
fiber was interrupted, because it is the one function that decides.

**And the probe that existed for this could not fail.** `probe:reconnect` had
three faults, each of which alone makes every line it prints a report on the
first connection:

```
  bun run daemon is 3 processes   `.kill()` reached the script runner; the
                                  daemon went on listening and answering
  a squatter on 5284              a daemon left over from six days earlier
                                  held the port, so the probe's own never
                                  bound and it measured a stranger
  no feed was watched at all      only the edges and a call
```

It spawns the entry point rather than the script, refuses to start beside
anything already answering on the port, exits if the kill produced no edge, and
watches `watchFacts` across the restart. Facts rather than threads because its
first push is the whole table — a resubscribe produces one immediately, where a
feed of genuine edges can answer "no push" by having nothing to say.

#### Anything that appears or disappears is animated

**A mandate, like the keyboard one.** Every show and hide in this window moves:
a column folding, a panel sliding in, a list collapsing, a tree opening over a
patch. Nothing pops.

The reason is not decoration. A thing that vanishes between two frames leaves a
person to work out _what_ just changed and _where the thing went_, and that
work happens every single time. A thing that moves has already answered both by
the time it has finished — which is why the columns were animated first and why
the same treatment kept getting asked for everywhere else, one control at a
time.

```
  FOLD_MS = 260                         columns.ts. One duration for the window.
  cubic-bezier(0.32, 0.72, 0, 1)        out fast, in gently
  @media (prefers-reduced-motion)       0s
```

Four rules that follow, each of which was learned by getting it wrong:

- **One duration and one curve, from `columns.ts`.** Two animations in one
  window that disagree about how long a fold takes read as two applications.
- **Reduced motion means none, not less.** Somebody who has asked their system
  for less motion is not asking for a faster version of it. Every eased style
  carries the media query; a transition without one is a bug.
- **A gesture is not animated.** A transition on a dragged boundary makes the
  thing chase the pointer a frame behind, which reads as lag rather than as
  motion. So the eased style goes _on for the toggle and off for the drag_ —
  held in state for `FOLD_MS` and removed — rather than living on the element.
- **Animate a property that can be animated.** `display: none` cannot, and
  neither can a conditional render — a component that is not in the tree has
  nothing to transition. Either keep it mounted and move `opacity` and a
  `transform`, or hold the unmount until the transition has finished.

**And a dynamic style, not a static one.** `${FOLD_MS}ms` inside
`stylex.create` is a build error about theming rules — an identifier in a
static style is resolved by StyleX and must come from a `.stylex.ts` file. A
dynamic style takes the value at runtime and asks no such question. This has
been walked into twice; see the note further down on StyleX failing quietly.
**No gate catches it** — fmt, lint, typecheck, test and doctor are all green on
the broken file, because only Vite runs the StyleX Babel pass. Fetch the module
from the dev server and grep it after touching styles.

#### Everything is reachable from the keyboard, and the keys are vim's

**A mandate, not a preference.** Every control in this window has to be
operable without a pointer, and the movement keys are `h j k l` rather than the
arrows. This is a terminal multiplexer with furniture around it; the furniture
answering to a different set of keys than the thing inside it is the friction
the whole application exists to remove.

What follows from it, stated once so it is not re-argued per feature:

```
  ctrl+h / ctrl+l   move between columns — sidebar · agent · accessory
  ctrl+j / ctrl+k   move within one, down and up
```

`ctrl` and not a bare `hjkl`, because the pane is a terminal: an unmodified `j`
belongs to whatever is running in it, and stealing it would break vim inside
the very window whose keys are being copied from vim. The chord has to be one
the pane does not want.

Three consequences worth knowing before writing a control:

- **Capture phase, on `window`.** The emulator installs its own keydown handler
  and calls `stopPropagation` for every key it consumes, so a bubble-phase
  listener never hears a chord while a pane has focus. Measured — see the note
  on `cmd+N` in `App.tsx`. Capture is also the right meaning: an application
  shortcut is decided before the terminal claims the key.
- **`event.code`, not `event.key`.** With a non-US layout `key` is whatever the
  physical key maps to, and a shortcut is the physical key.
- **A control hidden on hover must still be focusable.** `opacity: 0`, never
  `display: none` — an element outside the layout cannot be tabbed to, and
  hover-only means the feature does not exist without a pointer. `MoveToThread`
  is the worked example.
- **The terminal claims to be a text field, and must not be treated as one.**
  These chords have to be given up inside `<input>` and `<textarea>`, because on
  macOS ctrl+h, ctrl+j and ctrl+k are the emacs bindings there — and the pane's
  keyboard surface is a `contenteditable` div with `role=textbox`, which is
  correct of it and is how an input method reaches the emulator. A plain
  `isContentEditable` test therefore reported "editing" for the whole agent
  column and every chord did nothing:

  ```
    keydown seen      KeyL, ctrlKey true
    defaultPrevented  false     ← the listener returned before acting
  ```

  No error, no visible failure, focus simply staying where it was. Ask _where_
  the element is rather than what it claims to be — `navigation.ts` answers the
  agent column by its `data-column`.

`ctrl+j`/`ctrl+k` step through `[data-nav-item]`, which is opt-in. The
alternative — every focusable element — steps through hover-revealed row
controls and toolbar buttons, and a list nobody can predict is not navigation.
A column that marks nothing still receives focus; it just has nothing to step.

This is also why Base UI is a dependency and not a nicety: its menus, tabs and
dialogs ship the roving tab stop, the typeahead, the focus return and the aria
wiring. Hand-rolling any of them starts this mandate over from nothing.

#### Frontend

**The stack is chosen. Do not add a fourth thing to it.**

```
  Base UI          behaviour — dialogs, selects, tabs, menus
  StyleX           appearance — every rule in the renderer
  TanStack Router  navigation, when there is any
  Effect Atom      renderer state that outlives a component
```

The division between the first two is the one that gets violated, so it is
worth stating flatly: **Base UI ships no styles and StyleX writes no
behaviour.** Reaching for a styled component library replaces both at once;
hand-rolling a dropdown replaces the first and loses the arrow keys, the
typeahead, the roving tab stop, the aria wiring and — the one that shows up as
a visual bug rather than an accessibility one — the portal, without which a
popup inside a scrolling column is clipped by it.

`@effect/atom-react` was a dependency imported by nothing, kept as the answer
for the day the window needed shared state. **That day arrived, and it was Base
UI's doing.** A hidden tab is unmounted, so every panel's `useState` is destroyed
by switching away from it — which the diff panel _wants_ (it re-reads the patch
on the way back) and the review queue does not: forty-five pull requests, fetched over a
socket, thrown away because somebody glanced at the diff. What that looked like
was an empty panel saying `reading…` every single time the tab was opened, for a
list the daemon already had in memory.

So `atoms.ts` holds the review queue, and `useReviewQueue` reads and writes it. Three things
about that shape:

- **An atom rather than a module-level `let`**, because a `let` holds the value
  and tells nobody. What is wanted is the value _plus_ a subscription, so a fetch
  that finishes after its component unmounted still reaches whichever component
  is mounted now. That is `useSyncExternalStore`'s shape, and an atom is it.
- **Plain state atoms, not `Atom.make(effect)`.** The fetching stays in
  `daemon.ts` behind promises — the seam this window keeps between Effect and
  React — and moving it into an atom would relocate that decision into a file
  about state.
- **No provider.** `RegistryContext` defaults to a standalone registry when none
  is present, so nothing in the tree changed.

The guard against a read per tab switch is module scope too, for the same reason
the atoms are: it has to outlive the component that set it.

It is still not an invitation to introduce an atom before there is one to have —
the PR panel is the obvious next one and is deliberately still using `useState`,
because the daemon caches its answer and a remount costs a round trip rather
than a `gh` call.

The router _is_ now used, and the reason is worth stating because the obvious
one is wrong. The window has one screen and no navigation to speak of, so
"needs routes" was never going to be what earned it.

#### Selection is an address, not a name

What earned it is that **a session name is shortened and cannot be split back
into its parts** — the rule this file already states at length above. Selection
used to be one string of React state holding exactly that shortened name, kept
across reloads by hand:

```
  before   selected = "awp.thicket.effect-ts-tabular-expor-ca90.agent"
  after    /w/thicket/effect-ts-tabular-export-timemachine/agent
```

The daemon sends the unshortened truth as `SessionIdentity` and the old
selection threw it away, storing the shortening and then searching the listing
for a name equal to it. A session restarted under a different shortening — a
sibling appearing and changing the stem's budget — is a selection that silently
stops resolving. The route holds the three fields the labels carry, so it
cannot.

Everything else follows from that and is not the argument for it: back and
forward now work, `remembered.ts` lost its hand-rolled session key, and the
address is one value rather than a name plus the rules for reading it.

Three shapes, in `address.ts` — kept separate from `routes.ts` so that nothing
pure imports the router, which is what stops `App → routes → App` being a cycle:

```
  /                              nothing open — the fixture
  /w/$project/$workspace/$kind   one of ours: the unshortened truth
  /s/$name                       someone else's: the name is all there is
```

**One route level, and no `Outlet`.** The layout does not change with the
address — the same two bars and three columns are on screen whatever is
selected — so a nested route rendering a different tree would model a screen
change that does not happen, and would then have to hand the session list back
down through it. The root renders the window and reads the address; the leaf
routes exist to type and parse it.

**Hash history**, because the renderer is served by Vite in development and by
the app's own `app://` scheme in a build, and only one of those is a server that
would rewrite a deep path back to `index.html`.

**The address is derived, never written back.** `sessionAt` answers undefined
for an address naming a session that has gone _or_ one the daemon refuses — the
session the daemon is itself running in is in the listing and must not be
opened. Correcting the address from the listing would be a second copy of
something already known, and would race the first listing on launch.

`localStorage` keeps one mirror of the path, read exactly once, in `main.tsx`,
and only when the hash is empty. A reload keeps the hash on its own; what a
history cannot survive is the application being quit and started again.

#### A call the turn ended underneath never resolves itself

Reported as bash calls "that just spin forever and dont resolve", and the
spinner was the smaller half: **every client read those rows as work still
happening**, hours later.

ACP has no update meaning "the turn took this call with it", and the adapter
sends no terminal status for a call in flight when a turn is cancelled,
refused, or dies. So the row keeps whatever it last had, which is `pending`.

The daemon settles them, because a client deriving the rule would be a second
implementation and the two faces would disagree about what a hanging call
means. `hanging()` folds the transcript to the last status per tool id — a
call is a patch keyed by id, so "did any update say completed" is the wrong
question — and every id that is not over gets one more ordinary `tool`
update, which every fold already merges.

```
  cancelled   the turn stopped; nothing is known about what the call did
  failed      the tool said so
```

`cancelled` and not `failed`, and the mark is `⊘` rather than `✗` in both
faces: a cross is a claim about the tool, and this is a claim about the
turn. Emitted _before_ the turn's own `ended`, so a client folding a batch
sees the rows resolve and then the turn stop, rather than a turn that ended
with work apparently still going on inside it.

#### A composer keeps what you were writing

Switching threads unmounts the panel, and the draft went with it. A
half-written sentence is not a preference — it is the only copy of something
somebody was in the middle of. `amoeba.draft` in localStorage, per
**workspace** rather than per thread, because two checkouts of one piece of
work have two conversations. Written on unmount rather than per keystroke.

#### A stream resubscribes; a call is asked once

The window re-asks six lists when the socket comes back — `onReconnect` in
its `daemon.ts`, one per list — and the TUI re-asked nothing. So a daemon
restart left the thread list showing what it had before, the status row
without its model or mode, and a screen that mounted _during_ the outage
empty for good.

The socket was never the problem: `makeProtocolSocket` retries its own loop,
and the TUI's `subscribe` retries every feed on top of that. What has no
retry is a question, because nothing knows it was asked.

```
  feed   ChatOpen · WorkspaceFactsChanges   subscribe retries      ✓
  call   ThreadList · ChatConfig            asked in a mount effect ✗
```

`onReconnect` is the transition and not the state, which is the distinction
the window's own note makes: `onConnection` reports where things stand the
moment it is called, and a list that has just asked would ask again for the
same answer.

#### Motion is the fifth thing, and the stack rule still holds

`motion` (motion.dev, 13.2.0) is in `apps/amoeba`. The rule in CLAUDE.md is
about UI frameworks — Base UI for behaviour, StyleX for appearance — and an
animation runtime sits beside `@pierre/diffs` and `react-markdown` as a
renderer of one thing this window cannot do itself. Two things earn it:

```
  a spring   a curve is a guess at how long something takes; a spring is a
             statement about weight. An interrupted spring carries its
             velocity, where a CSS transition restarts from wherever it got
             to — which is the stutter every re-toggled fold had
  layoutId   one element moves to where another one was. The selected tab's
             fill and the sidebar's accent edge were four elements blinking
             out and in; each is now one thing that travels
```

`springs.ts` holds the presets and nothing invents its own numbers:
`jelly` for a row arriving, `pill` for a selection travelling, `snap` for a
press, `heavy` for a panel. Everything goes through `useArriving` /
`useSquish`, which answer **still** under `prefers-reduced-motion` — the
mandate is unchanged and it means none, not slower.

#### The caret was on the wrong row, and it looked random

Reported as "a random blinking orange cursor when i send a message". The
transcript marks the last row of a live turn as streaming — and the moment
somebody sends, the last row is _theirs_. So the caret blinked after what you
had just typed, over nothing arriving. It is the agent's row only.

#### The dock is glass, and that is what made it a layout change

Asked for as "give our thinking line a blurred background instead of white,
give it a glassy. same with the composer maybe we can split it in half and
pin the bottom controls and make the composer feel more floaty".

**A backdrop filter over the page colour is the page colour.** The ledge and
the composer were the last two children of a flex column, so there was
nothing painted behind either of them to blur — the glass and the layout are
one change, not a style on top of an existing one.

```
  ┌──────────────────────────────┐
  │ transcript                   │   the scroller, full height
  │ ~~~ the tail, blurred ~~~~~~ │   ← runs UNDER the dock
  │ ⟨ ⠹ Check the types  2m14s ⟩ │   pill     ┐
  │ ┌──────────────────────────┐ │            │ the dock: absolute,
  │ │ say something…        ↑  │ │   card     │ inset-inline 0, bottom 0
  │ └──────────────────────────┘ │            │
  │  Manual  Opus  62% context   │   strip    ┘
  └──────────────────────────────┘
```

**The activity is a pill, not a band.** It was the full width of the column,
which drew a second horizontal register above the composer and made the dock
two stacked slabs — and what the line actually is is one short sentence about
what is happening right now. So the strip is only the clipping box the height
spring needs, and the glass is on the words.

`inline-flex` is **not** what makes it hug: a flex item's display is
blockified, so inside the strip — and inside the style guide's own specimen
cell — it becomes `flex` and stretches. Measured at 939px in a 976px cell,
which is the bar it was meant to stop being. `align-self: flex-start` is the
property that answers it.

**It carried a 420px ceiling for a while, and the ceiling came off.** Clipping
a long purpose to keep the pill short costs the half of the sentence that says
what the agent is doing, on the one line whose whole job is to say it. The cap
is the column — `max-width: 100%` — and a sentence that will not fit there
still clips.

The activity is the one part that gives — `flex-shrink` with `minWidth: 0`,
without which a flex item will not shrink below its content and the elapsed
count is what gets pushed out instead. The count itself never clips: it is
four characters, and half a duration is worse than none.

**The width is animated, and the activity is debounced.** Those are one
finding from two directions. `read a file` and `Find who provides the worker
pool` are a hundred pixels apart, so a snapped width is an edge jumping beside
the composer every time the agent moves on — `layout="size"` on a spring, and
**size** rather than a full `layout` because the pill sits in a dock anchored
to the bottom of the column, where a layout animation would also animate the
position it is already being held at.

And an agent reading six files answers six calls inside a second:

```
  before   read a ─ Find who ─ Check ─ grep ─ read b ─ Write    six springs
  after    ·······················  Write                       one, at the end
           └─ 220ms of stillness
```

Trailing, so a burst paints once with whatever is still going. The cost is
deliberate and is the other half of it: a call that finishes inside `SETTLING`
is never drawn at all, and a reading nobody could have read is not worth the
movement.

**The scroller's bottom padding is the dock's measured height**, through a
`ResizeObserver` rather than a constant. The dock is one to four rows tall
depending on the draft, whether a turn is running, and whether the settings
chips have wrapped at a narrow column — a number written down here is a
number that is wrong in three of those four states, and what that produces is
a transcript whose last message cannot be scrolled out from under the glass.
Padding that _grows_ has to take a reader at the tail with it, which is the
same rule as content arriving and reuses `followIfStuck`.

**Each pane carries its own glass; the dock carries none.** `Composer` is
drawn on its own in the style guide, and a dock that held the fill would make
that page a picture of something the window does not have.

**The blur is a token.** `glaze.pane` in `tokens.stylex.ts`, because two
surfaces wear it and two radii that disagree read as two materials rather
than one dock — and because a plain constant interpolated into
`stylex.create` is the build error about theming rules this file records
three times already.

`saturate` beside the blur is what separates glass from fog: blurring alone
averages what is behind it towards grey, and pushing the saturation back up
keeps a running row's accent recognisable as it passes underneath.

**`colors.glass` is the one token here deliberately not opaque, and the
themes want different amounts of alpha.** White over dark text hides more per
unit than near-black over light text does — and the dark value is _deeper_
than the page rather than lighter, which is not symmetry: a light blur
lightens what is behind it and a dark one has to darken, or the transcript
reads through as a bright smear.

```
  latte      rgba(255, 255, 255, 0.68)
  macchiato  rgba( 20,  21,  32, 0.62)   ← below #181926, not above
```

It is measured against nothing, which is the exception to the style guide's
rule. Every other token is judged by its ratio on a ground; this one _is_ a
ground, and the words on it are `text` and `muted`, already measured against
`page` — which is what the blur moves everything behind it towards.

**The split is a control and a readout, which is why one floats and one is
pinned.** What somebody types takes the keyboard, has a border that goes
accent and is the only part that acts; what is under it is four facts about
the session. So the card is inset from every edge and carries `lift.mid` at
rest, and the strip is flush, edge to edge, under a faint rule. Inset with
the card, the chips read as more of the composer rather than as a status
line.

Verified in the served stylesheet, because StyleX drops what it does not
understand in silence and three of these are properties it had never emitted
here before:

```
  backdrop-filter:var(--x1617d6r)              → blur(18px) saturate(1.7)
  border-top-color:color-mix(in oklab, …55%…)
  rgba(255, 255, 255, 0.68) · rgba(20, 21, 32, 0.62)
```

#### The dock stacks; only the composer floats

The dock began as one absolutely positioned block over the transcript holding
three things — the activity ledge, the composer card and the session's chips —
and the scroller was the full height of the column with all three standing on
its bottom padding. Two complaints came out of that, and they are one cause.

```
  before  ┌ chat ─────────────────┐   after  ┌ chat ─────────────┐
          │ scroller (full height)│         │ stage  flex:1     │
          │ ┌ dock ─ absolute ──┐ │         │   scroller        │
          │ │ ⠹ activity        │ │         │   ┌ dock ───────┐ │ ← floats
          │ │ say something…  ↑ │ │         │   │ ⠹ · box   ↑ │ │
          │ │ Manual · 62%      │ │         │   └─────────────┘ │
          │ └───────────────────┘ │         ├───────────────────┤
          └───────────────────────┘         │ Manual · Opus · % │ ← the bottom
                                            └───────────────────┘
```

**The scrollbar was the first tell** — "goes off screen and is a little stuck
at the bottom". These are classic always-present scrollbars (`global.css`
styles `::-webkit-scrollbar`, which is what turns off the overlay kind), so the
track is the scroller's own height: it ran on behind the composer _and_ the
chips, and the thumb could never reach a visible bottom. The fix is the
stacking rather than a rule about scrollbars — the scroller's box now ends
where the bar begins, so the track ends there too, and the floating card is
inset 1rem against an 11px scrollbar, so the thumb runs in the gutter beside it.

**The dock is `absolute` against a `stage`, not against the column.** That is
what stacks the two without either measuring the other: `bottom: 0` for the
dock _is_ the top of the bar. `SessionBar` is its own export for that — the
style guide draws both, so the page still shows what the window has.

**The clearance was an accident before, and had to be made deliberate.** The
scroller's bottom padding is the dock's measured height, which clears the card
_exactly_ — the last line stops on its top edge. That read as the message being
behind the composer, and it is; a card with a blur and a shadow needs text to
stop short of it. It used to get the slack from the chips, which were inside
the dock and had nothing drawn over them. `calc(<dock>px + 1.25rem)` now, the
transcript's own gutter, so the column has one margin rather than three numbers
that nearly agree.

**The glass ended up on the input, not around it.** Three asks in a row — the
outer card transparent, then its blur off too, then "the composer text input
can keep the blur and bg" — and together they say where a material belongs: on
the thing somebody reads and types into, not on the region around it. A
full-width pane of glass is a surface, and a surface with a control on it is a
footer.

That left the outer element with a `display` and nothing else, at exactly its
only child's width — an invisible rectangle anyone inspecting the composer had
to step past, and reported as one. Merging it into the card is what removed it.
**A `return` may hold one node, not a comment and a node:** the JSX comment
above that wrapper became a second root the moment its parent went, and `tsc`
reports that as a missing `)` on the line _after_ it.

#### The row that is running has to look like it

Three complaints in one breath: the mark was lame, the chat was boring, and
**an old tool call was still spinning**. The third is the one that mattered.

`going(status)` — anything but `completed` or `failed` — is not the same
question as "is this happening now". A call whose terminal status never
arrived sits at `pending` for the life of the conversation: a turn cancelled
under it, an adapter that stopped talking, a permission denied. So a row from
this morning turned forever, under a row from now.

**A call turns while its own turn is in flight**, which means the window's
fold needed the turn counter the TUI's already had — `Conversation.turn`, and
`turn` on every `Ran`. `held.running > 0 ? held.turn : undefined` is what the
transcript is handed, and a style guide that passes nothing sits perfectly
still, which is what a transcript of finished work should do.

What the window draws now, and each says something the other cannot:

```
  the mark    the same braille the TUI turns — TURNING, in the contract
              package, because a spinning notch in one face and a braille dot
              in the other is two vocabularies for one state
  the caret   a block at the tail of the answer arriving. A paragraph that
              has stopped mid-sentence and one still growing are otherwise
              the same picture
```

**One clock, and it stops.** `useTurning` runs a single 100ms interval for
the whole panel while a turn is in flight — an interval per row is a dozen
timers and a dozen renders — and it does not run at all under
`prefers-reduced-motion`, where the mark falls back to `…`. That is the
mandate read strictly: reduced motion means none, and a still mark is a state
rather than a slower animation.

The accent is spent here for the fifth time, and it earns it on the same
rule as the other four: at most one row in a transcript is running, so it
marks a deviation rather than a baseline.

**The row's band of light was removed, and it is worth saying why it was
wrong rather than merely disliked.** It was a gradient moving under text
somebody is trying to read, forever, in the one column they are reading —
and the argument for it (a mark is one cell and cannot catch an eye three
rows up) is an argument for interrupting a reader who is not looking for the
interruption.

It was also wrong about _which_ rows. The fold's `turning` is about the
**turn**, not about the call, so a run of finished calls under a live turn
swept too. Reported in two messages — "stop the background shimmer if the
tool is not running", then "actually just remove that" — and the second is
the better fix: the condition was never the whole of what was wrong.

What the removal is checked by is the served sheet, which is the rule for
anything StyleX. The keyframes are gone from it:

```
  before   @keyframes …{from{background-position:180% 0;}to{…-80% 0;}}
  after    0 matches
```

#### The send button is a stop while the agent works

`onStop` and `working` had been props on `Composer` that nothing used —
which was two of the repo's three red gates and, more to the point, a window
with no way to interrupt an agent short of the terminal. One button rather
than two: an empty draft's disabled send is exactly the moment a stop is
wanted, and the arrow and the square trade places on a spring.

**The glyph is what tells the two apart, not the colour.** It was `warn`
first, on the argument that stopping is not the ordinary act and the states
have to be distinguishable by somebody whose eyes are on the transcript. That
is the wrong sentence for the colour to be saying: a red circle appearing
where the send was reads as _something has gone wrong_, and an interruption
somebody asked for is not that. The button is the accent through both, and an
arrow against a square is already two silhouettes — which is what the eye
lands on at 1.6rem, before any hue.

#### The window moves now, and it moves on physics

Reported in three sentences over one evening: the spinner was "lame", the
thinking line "super weak", and "a lot of the gui is super flat and lame
JUICE IT UP". Taken together they are one finding — **every state in this
window was a still frame** — and the answer is a vocabulary rather than a
pile of animations.

#### Two more measurements in the TUI

**The composer is two lines at rest, not one.** A box the height of the text
in it has nowhere for the caret to go — the line above what somebody is
typing is the transcript. Six is still the ceiling.

**The thread list is ordered by activity, and the record has no such field.**
A `Thread` carries `createdAt`, which is right: a thread is a claim, and when
it was made does not change. What changes is the work, so the reading comes
from the workspaces it holds — `WorkspaceFacts.lastActiveAt`, written by the
agent's own hooks into `~/.awp/workspace-state.json` and already on the wire.
Counted on this machine: 57 entries, all 57 stamped. A thread is as recent as
its most recent checkout, and one whose workspaces were never stamped falls
back to `createdAt` rather than to the bottom.

**Copy is the end of a drag, and paste needed nothing.** Reported together
as "i cant copy paste", and they are two different things:

```
  paste   the renderer already enables bracketed paste (`?2004h`) and the
          textarea inserts the text whole — including a two-line paste,
          whose newline is a newline and not a send
  copy    opentui owns the mouse, so a drag is ITS selection and the
          terminal never sees one. Nothing wrote it anywhere: `copyOnSelect`
          in `clipboard.ts` is the missing last step of the gesture
```

Selecting copies, with no chord — there is none a terminal reliably delivers:
ctrl+shift+c needs the kitty protocol and cmd+C never reaches a program. Both
routes, every time: **OSC 52** for the terminal (the only one that survives
ssh or a multiplexer) and opentui's **host** backend for this machine.

`bun run probe:paste` is a pty driving a composer, and the OSC 52 coming back
is the only evidence a selection was copied rather than merely painted:

```
  asked for 2004h  yes
  a paste          arrived
  two lines        both arrived
  a drag copied    "a line worth"
```

**A missing space is not a dropped paste.** The renderer repaints only the
cells that changed, and a space drawn over a space has not changed — so
`pasted one line` arrives on the probe's side as `pastedoneline`, with the
gaps never sent. It read as a failure until the screen was printed.

**`<markdown>` wraps now, and the note saying it does not is expired.**
`lines.ts` records — from opentui's own source — that `MarkdownRenderable`
clips a long paragraph, which is why an agent's prose is drawn as `text` and
every inline mark is thrown away. Re-measured against the installed 0.5.11 in
the shape `Message` draws, it wraps, and draws headings, lists, tables, bold
and inline code. What it also wraps is a **fence**, whose breaks are the
content — so prose goes to `<markdown>` and a fence still goes to `<code>`,
which is why `segments` survives the move. `streaming` is on while the turn
is in flight, which is the renderable's own instruction.

Measured in `probe:transcript`, because characters alone cannot tell a
rendered heading from a paragraph that says the same words:

```
  no literal ##  parsed
  the heading    What #eed49f          ← markup.heading.2, and bold
  inline code    wrap #91d7e3
  a table drawn  yes
  the fence      unwrapped
```

**And `Terminal` is not a title.** The adapter titles a Bash call
`input?.command ? input.command : "Terminal"`, so a call whose input is still
streaming reads `…  bash  Terminal` — a word that names no command and
repeats the verb. Reported as "i dont know what that is and i do not like
it". `toolTitleOf` drops that one pair, from that one tool; the real command
lands on the same id a moment later.

#### Two thresholds, and they must not be one

`⌄` appears on the ledge's right when the reader is a long way from the tail.
The interesting part is the pair of numbers behind it.

```
  LEASH  120   still being followed — content arriving takes you with it
  AWAY   500   far enough to be offered a way back
```

They were briefly **one** number, deliberately: with `AWAY` below `LEASH` there
is a band where the reader is far enough to be offered the button and near
enough to still be followed, so an arriving message takes them to the bottom
and the button leaves on its own. That reasoning is right and the repair was
wrong, because the button lives on the ledge — so `away` turning over opens and
closes that row, which changes the dock's height, which changes the scroller's
padding, and the row's height spring calls `followIfStuck` on **every frame**.

With the two equal, scrolling back down crossed both at once: the row closed
while the reader was inside the leash, and the closing animation pinned them to
the bottom for its whole duration. Reported as "the scroll is getting stuck at
bottom briefly when there is no turn active" — no turn, because the ledge
moving under them was the button's and not an agent's.

So the rule is not the number: **`AWAY` must exceed `LEASH` by more than the
ledge is tall**, or the control's own arrival moves a reader who is still being
followed. Checked in both directions for chatter — crossing 500 upward opens
the row and pushes the distance to ~540, downward closes it and drops to ~460,
monotone away from the threshold either way.

`away` is state where `stuck` is a ref, and that asymmetry is the point: one is
drawn and the other is only consulted. They are written together on a scroll
and never derived from one another — `stuck` deliberately survives content
arriving, which is the whole of why it is not recomputed then.

#### Two token groups the window was missing

```
  timing   fold · quick · enter · ease · spring · even
  lift     low · mid · high — a hover, a surface, a dialog
```

Named `timing` and not `motion` because a file cannot import both under one
name, and a token group that will not sit beside the thing it describes is a
token group nobody uses. Every duration was previously written out by hand at
each site with a comment explaining that an identifier inside `stylex.create`
must come from a `.stylex.ts` file — this **is** that file, so they can stop.

`lift` is the answer to "flat": every surface was a fill against another
fill, so a panel, a row and a dialog were the same object at three
brightnesses. Three steps, soft and mostly black — a coloured shadow reads as
a glow, and a glow reads as a state rather than as height.

#### What actually moves, and what each movement says

```
  a row arriving      springs up 8px. Only rows that are NEW: a snapshot of
                      the keys at mount enters still, because Base UI
                      unmounts a hidden tab and a glance at the diff would
                      otherwise spring forty rows
  the running call    a turning braille mark, the same frames the TUI turns.
                      It had a band of light across the row too — removed,
                      see below
  the working line    what the agent is doing this second, rolling as it
                      changes, and an elapsed count past ten seconds. A
                      still word is the same picture as a dead adapter
  the answer          a block caret at the tail while it streams, and only
                      on the AGENT's row — see below
  a tab               the fill travels between tabs
  the sidebar         the accent edge slides down the strip; a working or
                      waiting dot breathes at 2.6s
  a running job       a progress bar under the row, `scaleX` on a spring
  every press         the send, the permission buttons: a lift on hover and
                      a squash on press
```

**An empty panel says so like it meant to.** Every panel's empty state was
one line of muted text at the top left of several hundred pixels of nothing
— which reads as a panel that failed to load. The accessory column is empty
whenever nothing is selected, so it is the first thing somebody sees, not an
edge case. `Nothing.tsx` centres a mark, a sentence and a line saying why.
It offers nothing: where there is something to do about the emptiness the
panel says so itself, which is what the chat's `continue the terminal's
conversation` already does.

**Your own messages are drawn as the prompt they were typed at.** A
transcript in one voice reads as an essay with a name in the margin, so the
two halves have to look different — and the first attempt at that was a
rounded fill, which came back as "what am i imessage 2007".

That is the right complaint, and the deeper one is that a chat bubble is a
**borrowed idiom**: it says "this is a messaging app" about a window whose
whole subject is terminals. A prompt says the same thing in this
application's own vocabulary, and it is the mark every person using this
reads a hundred times a day in the pane two columns over:

```
  ❯ the diff panel feels chunky when i scroll it. can you find out why

  agent
  Every file was being tokenized on the main thread — …
```

One character, no fill, no radius, no shadow. It replaces the `you` label
rather than joining it, since a chevron and the word `you` are two marks for
one fact, and the label row comes back only for a message that is queued.

**A `<p>` carries a 1em margin from the UA**, which put the mark a whole line
above its own sentence. Measured — the row began 16px above its text — and
the fix is to reset it, because the column already spaces its blocks with a
gap. Space goes _before_ a prompt instead, which is where an exchange
starts.

**A shimmer was the first answer and it was borrowed.** A gradient sweeping
through the word `working` is what every chat in the world does, and it was
reported back as exactly that — "the shimmer is lame… dont just copy codex".
The deeper fault is that it is **decoration**: it says something is happening
without saying what, on the one line that could say it.

So the line carries the work instead. It reads the live turn's last
unfinished call by its `purpose` — the field that says intent — and each new
activity **rolls** the last one up and out of a one-line window:

```
  ⠹  Check the types              2m14s
  ⠼  Find who provides the pool          ← rolls up; the new line rises
  ⠧  thinking                            ← between calls, and honest
```

The movement is a consequence of the information changing, which is the only
kind that stays worth looking at. `AnimatePresence` with `mode="popLayout"`,
so the outgoing line leaves the flow at once rather than pushing the
incoming one down, and `overflow: hidden` on a fixed one-line strip is the
whole mechanism.

Two things this gets for free: the mark and the words now say different
things — one that it is alive, one what it is doing — and a turn that has
stalled says `thinking` for two minutes, which is a reading rather than a
mood.

**And it is a ledge on the composer, not the tail of the transcript.** That
is where it started, which put a line changing every few seconds _inside_
the surface somebody is reading: every new activity re-laid the tail out,
and the follow-the-tail effect chased it — so a transcript being read three
screens up was not still either. Reported as "pin the thinking line above
our composer so its not causing so much shifting".

```
  ┌──────────────────────────────┐
  │ transcript · scrolls · still │   the document
  ├──────────────────────────────┤
  │ ⠹  Check the types      2m14s│   the ledge — height springs in once
  ├──────────────────────────────┤     per turn, and never again
  │ say something…            ↑  │
  └──────────────────────────────┘
```

The strip animates its **height**, so the composer is moved once when a turn
starts and once when it ends, rather than on every change of activity — and
the activity itself rolls inside a box that no longer changes size.
`overflow: hidden` is what makes a height spring possible at all, and the
padding is inline-only for the same reason: vertical padding on a box
animating to `height: 0` leaves a gap that never closes. `Working` lost its
own entrance with the move, because two animations on one thing is the fight
`springs.ts` exists to stop.
