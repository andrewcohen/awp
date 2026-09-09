# tui

Two questions, in one TUI.

1. **Is opentui's embedded terminal good enough to build on?** — a `za`-shaped
   session picker with a real pty inside a layout, measured under load.
2. **Can a TUI share an ACP conversation with the window?** — the same
   `ChatOpen` the renderer uses, from a Bun process.

Both answers are yes. The numbers and the findings are below.

```
bun run tui                 # from the repository root — runs bun directly,
                            # never through `--filter`; see below
bun src/main.tsx            # or from here
bun src/za-tui.ts           # the first half on its own: a zmx picker
bun src/probe/poc.ts        # drives all three screens and says what it saw
bun src/probe/render.tsx    # a message at a width this file chose
bun src/probe/transcript.tsx # an edit, a rolled-up run, and the status row
```

**Not `bun run --filter tui tui`.** Filtering runs the script in a subprocess
whose output bun collects to prefix it, so the child gets a pipe rather than a
terminal: opentui finds no tty, falls back to 80x24, never enters the
alternate screen and never puts stdin in raw mode. What that looks like is the
escape codes printed as text down the bottom of an ordinary scrollback, with
nothing responding to a key. The root script execs `bun apps/tui/src/main.tsx`
for that reason and must keep doing so.

## The POC

```
  ┌ awp · 9 threads ─────────────────────┐
  │ ▸ paginate the tabular exports       │   enter  the agent's conversation
  │   ❯ acorn/tabular-exports            │   s      a shell in that directory
  │ ▸ lantern rewrite                    │   j/k    move
  │     lantern/rewrite                  │   q      quit
  └ enter agent · s terminal · j/k move ─┘
```

`ctrl-\` comes back from either screen — zmx's own detach chord, so there is
one muscle memory rather than two. `ctrl-q` quits from anywhere.

The chat screen is a real client of the daemon: history, live updates, a
composer that steers a running turn, and `ctrl-y`/`ctrl-n` to answer a
permission request. Under the composer is the same read-only row the window
draws — mode, model, effort, fast mode and how full the context is — which
took the place of a list of chords, and which a notice borrows while it has
something to say.

A run of tool calls that changed nothing is rolled up to its last three with a
count for the rest. A call that **did** change something is not: its patch is
drawn under it by opentui's own `<diff>` — line numbers, signs, and the code
inside it highlighted — which is the row's whole content. The daemon composes
the patch; see AGENTS.md. The terminal screen is a plain `$SHELL` in the checkout's
directory — never `zmx attach`, because a session takes its size from whoever
is looking at it and a POC has no business reflowing somebody's work.

## What was measured

A 40MB drain through the embedded terminal, and the same drain through a pty
with no rendering at all, so there is something to compare against:

```
  repeating spew    38.9MB in 1.8s = 21MB/s     painted out:  18KB
  unrepeating       15.3MB in 3.2s = 4.8MB/s    painted out: 840KB
  no rendering      38.9MB in 0.7s = 50MB/s     ← the pipe's own ceiling
  frame             avg 0.29ms  max 2.55ms      keystroke → paint p50 11ms
```

The emulator costs about half the pipe and never spends more than 3ms on a
frame. `yes` is its best case — every line is the line above it, so almost
nothing is rewritten; base64 of `/dev/urandom` is the honest number.

## What sharing a conversation needs from the daemon

Nothing, to read it. `ChatOpen` is already a fan-out: `chat.ts` keeps a set of
subscriber queues and numbers what it sends them, registering before it
snapshots so a late joiner cannot miss or double an update. `client.ts` runs
under Bun. Two things are missing for two clients to be honest with each other:

- **A user's message never reaches the other client.** `send` emits the turn's
  edges and no adapter echoes a user chunk on a live turn, so each client's own
  copy is the only record until the conversation is opened again. One `emit` in
  `chat.ts` fixes the daemon half; the renderer's optimistic copy then needs a
  shared key, which is a `ChatDelivery` change.
- **Nothing says a permission was answered**, so the other client goes on
  showing a question that has been settled.

## Findings worth keeping

- **A renderer that is destroyed does not stop the process.** opentui's exit
  handlers call `renderer.destroy()` and nothing else, so a SIGTERM tore the
  screen down while timers painted into a dead renderer once a second forever —
  and the probe that killed it waited for an exit that never came. There is one
  `quit()` and the signals use it.
- **A TUI's output is a diff of cells.** `message cancelled` painted over
  `esc cancel · ctrl-q quit` arrives at a reader as `message canceed`. Three
  probe assertions were wrong before this was understood; anything finer than a
  short token is read from the process itself, not off the screen.
- **ctrl+j and Enter are the same byte.** Only the kitty protocol tells them
  apart, so the list moves on arrows and a bare `j`/`k`. Driving the probe with
  `0x0A` for "down" opened a conversation on the row above the intended one.
- **`Bun.write` truncates**, so a debug log written with it holds one line.
- A cumulative pty stream is not a screen: `slice(0, 200)` is the first frame
  ever painted, and reading `no daemon` off it cost two wrong diagnoses.

## Layout

```
  src/main.tsx         the renderer, the root, and the one way out
  src/App.tsx          three screens and the moves between them
  src/Threads.tsx      the list
  src/Chat.tsx         the conversation
  src/Term.tsx         a shell in the checkout
  src/daemon.ts        the client seam — a third the size of the renderer's
  src/conversation.ts  updates folded into items: chunks joined, tools merged
  src/lines.ts         items to wrapped lines
  src/za-tui.ts        the first half, imperative, kept for its measurements
  src/probe/           a pty to look at a TUI through, and what to ask it
```
