# Backlog

Everything the task list is holding that is not being worked on right now, with
enough context to pick one up cold. It lives in `specs/` because that is where this
repo keeps the reasoning behind decisions, and most of these entries are a decision
someone has to make before there is code to write.

**Not in here: `awp ship` and the repo-level ship style (#376).** That is next, so it
stays a live item rather than a filed one.

An entry is a *problem*, not a design. Where the design is already settled the entry
says so; where it is not, the entry says what the open question is, because that is
the part that is expensive to reconstruct.

---

## The sidebar

Three of the four sidebar items landed (#365 sections, #367 spacing, #388 labels,
#389 drag-resize, #384 click). What is left is the keyboard and the grouping.

### #350 — the sidebar is somewhere you can go, not just something you can see

The strip has no cursor. #384 gave it a mouse target, and that was the cheap half on
purpose: a click carries its own target, so it needs no answer to the focus question.
The keyboard does.

**The design is settled:**

- `ctrl+\` from a pane goes to **the sidebar**, not the deck.
- `ctrl+\` again goes on to **the deck**.
- **Every key that works on a deck row works on a sidebar item.**

That last point is most of the work and the best part of it. The strip stops being a
second, weaker list with a subset of verbs and becomes the deck's row list in a narrow
column: whatever dispatches a key against "the row under the cursor" stops caring which
surface the cursor is on.

It also dissolves the focus question that blocked this, by making the door a cycle
rather than a mode — pane → sidebar → deck, on the key that already means "somewhere
else". Nothing new to learn and no second binding.

Still to settle:

- Where the cycle goes from the deck: back to the pane, making it a true cycle, or is
  the deck the end of the line?
- Whether the strip is reachable with no pane open. It only renders over a hosted
  program today (`showsSidebar`), so from the row list there is nothing to go to.
- One cursor shared between the strip and the row list, or one each. Shared is what
  "the same keys act on the same row" implies, and it is what makes leaving a pane land
  you on the row you were in.
- The cursor needs the design system's selection treatment (`┃ ` + `Warning` bold),
  which costs the two columns `sidebarRow` spends on nothing — the note at the end of
  that function reserved them for exactly this.

### #391 — the pinned section collapses every register into one heading

The row list sections pins **by register**, with `state.PinGroupAliases` naming them.
The strip has one flat `pinned` heading holding all of them, so the grouping that
registers exist to create is lost exactly where you are most likely to be looking for
it.

Open question, and the reason this is not just a loop change: the strip is 20–60
columns and a heading costs a row. Several one-member registers would spend most of
the section's height on headings. A single-member unnamed register may be better folded
into its row than given a heading — but that is a judgement to make looking at a real
deck, not in the abstract.

---

## The captain

The captain shipped end to end (#372–#383, #386, #387, #390). Two follow-ons.

### #385 — the captain belongs in a ~70% modal, not a full-screen pane

Everything else full-screen in awp is a workspace's program. The captain is not a
workspace — it has no repository — so wearing the same chrome says it is one. A modal
would say "this is awp talking to you" instead.

**Blocked on two answers:**

1. **Does the sidebar stay visible beside it?** Lean yes — the sidebar is the thing
   you would most want to check the captain's claims against.
2. **70% of both dimensions, or wider than tall?** Lean 80% wide × 60% tall, with a
   floor, since the captain's output is prose and prose wants width more than height.

### #377 — `A` sends a message to the captain, carrying the current project

Today reaching the captain means going to it. `A` from a row would send a message with
the row's project already filled in, which is the common case: you are looking at a
project and want the captain to do something about it.

Depends on nothing, but reads better after #385 — a message sent to a modal has a
place to land.

### #370 — the captain umbrella

Kept open as the umbrella. The deferred piece inside it is **the message log**: every
communication persisted and browsable. Cut from v1 deliberately ("i think the captain
doesnt need the message yet?"), and the spec records why. Reopen when one-way `send`
starts hurting — the captain can talk to an agent but an agent cannot answer, which is
the one real gap in the control surface.

---

## The zdeck cutover

### #245 — `awp deck` becomes zdeck

The big one. zdeck hosts its own panes and needs no multiplexer above it; `awp deck`
still hands off to tmux. Everything below is a thing the cutover either has to answer
or can drop.

### #395 — open on the workspace you were last in, not the row list

Launching the deck drops you on the row list every time, though the deck already knows
which arrangement you were last in: `paneArrangement` is persisted for `ctrl+\` and `L`
(#327, #359, #360). The information is there and startup does not use it. Open straight
into it, and let `ctrl+\` be the way *out* to the list rather than the way in.

Three things to settle first:

- **The escape hatch.** A workspace whose agent has since died, or one that was
  deleted, must not make the deck unopenable. Falling back to the row list covers both,
  but deliberately rather than by accident.
- **Does `--scope=…` mean "show me the list"?** A flag about which slice of the list to
  show reads like an instruction to show the list.
- **Preference or unconditional?** The sidebar and the split fraction both got a saved
  flag. The counter-argument is that opening where you left off is simply what resume
  means, and does not need a switch.

### #206 — decide what `C`, `p D` and CI do with no tmux

These three open tmux windows. Under a pane host there is no other client to hand off
to. Each needs to become a pane kind, become a modal, or go. Not a code task until
that call is made.

### #226 — the `?` help and README describe tmux semantics zdeck does not have

Documentation debt that becomes user-facing wrongness at cutover.

### #266 — rename orphans every session but the agent's

`rename` moves the agent's zmx session and leaves every other kind (shell, dev server,
vcs) under the old name, where nothing will ever reap them. #249 fixed the agent's
case; this is the same bug for the rest.

### #267 — start a long-lived action without opening its pane

A dev server you want running but not looking at. Today starting one means opening its
pane and leaving.

### #364 — a pane nudges its size once, so a re-attached program reflows

A program that was laid out for a different width does not redraw until something makes
it. One synthetic resize on attach fixes it. Small, self-contained, no open questions.

### #341 — the diff half persists instead of being torn down

Rebuilding a diff on every split re-reads and re-highlights the whole change, which is
the expensive path. Keeping it costs memory and a staleness question.

### #237 — typing latency in a pane, worst in nvim

Measured, not diagnosed. nvim is the worst case because it redraws most per keystroke.
Needs a profile before a fix.

---

## The review and diff surfaces

### #57 — suggest mode: send the agent your edit, not prose about it

The peer of commenting. Instead of describing a change, you edit the text and the agent
gets the diff. Comment mode says what is wrong; suggest mode says what it should be,
and a diff is unambiguous where prose is not.

### #107 — review tour: a guided walkthrough of a change

The author says "read these files in this order, and here is why" and the reviewer
follows it. A stack of commits already implies an order; this makes it explicit and
navigable.

### #150 — draft and edit the PR title and description from the diff view

You have just read the change. That is the moment you know what the description should
say, and the moment you are furthest from a browser.

### #68 — render GitHub-flavored markdown in comments, starting with details/summary

Mirrored threads arrive as GFM. `<details>` is the case that matters, because a
collapsed block rendered as literal HTML is worse than either rendering or hiding it.

### #295 — the approval prompt should point at the proposal, not echo it

Deliberately parked until there is something to point at: it needs the proposal to have
a stable on-screen location first.

---

## Bigger ideas, no design yet

### #356 — a workspace is in builder mode or reviewer mode, and the mode is what surfaces ask

Today "is this a review workspace" is inferred in several places from several signals
(a `pr-N-` name, a parked reviewer brief, a pinned PR). One stored mode, asked rather
than inferred, is the same shape as the explicit-target rule the captain's CLI follows:
one spelling, in the place it cannot be forgotten.

### #358 — a shell pane can be kept, as a named long-lived task

Shell panes are ephemeral. Sometimes one is a job you want to come back to. Overlaps
#267 — both want "a long-lived thing that is not the agent" to be first-class.

### #238 — `awp verify`: an agent's work comes with proof it works

An agent says it is done. The evidence is scattered across a transcript. `verify` would
make the proof an artifact of the work rather than something you reconstruct.

### #239 — awp task manager

Unspecified. Note the finding from #242's research: across 151 transcripts agents did
essentially no structured planning (0 TodoWrite, ~0 ExitPlanMode) — planning is an
organic read-only phase, which is evidence against a task artifact agents are expected
to maintain.

### #242 — a meta agent that can drive awp

Largely delivered by the captain (#370). Keep as the place the next round of "what
should an agent driving awp be able to do" goes — the current answer is five write
verbs plus attention, and the boundary is written down in the captain's preamble.

### #154 — `v` opens jjui in-deck, `V` opens it as a window

jjui in a pane works today. This is about which key means which, and it is the kind of
thing to decide alongside the cutover (#245) rather than on its own.
