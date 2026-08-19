# tdeck

A desktop surface for awp's agents, in TypeScript on Bun, driving Claude through
the Agent Client Protocol.

This is the successor to the `gdeck` experiment. It exists because a day of
building gdeck answered a question nobody had asked at the start: **what is the
right seam between a GUI and a coding agent?** The answer turned out not to be a
terminal.

## Decisions

Each of these was measured rather than reasoned to, and the measurement is worth
keeping because most of them contradict the position taken an hour earlier.

### The chat drives the agent; it does not read its logs

gdeck's chat was built on the Claude Code transcript —
`~/.claude/projects/<slug>/<session>.jsonl` — because that file already existed
and `internal/watch` already knew how to find it. Against a real 7MB transcript
it yielded 300 turns, 190 tool calls and 17 patches, so the projection worked.

It is still the wrong source:

- **No live state.** A transcript gains a line *after* something completes, so
  it is a record and not a monitor. There is no "thinking", no in-progress tool.
- **Thinking is empty.** All 205 thinking blocks in that transcript carried a
  signature and no text — the content is stripped before it is written.
- **`translateToString` drops ZWJ continuations**, so scraped text is subtly
  lossy in the same way a screen scrape always is.
- **It was expensive.** Re-reading and shipping the whole conversation on every
  change — the change signal fires ~1/s while an agent works — was megabytes of
  JSON per second to deliver one new turn.

The agent's own event stream carries all of it: streamed text, thinking *with
content*, tool calls as they start, status, usage, cost.

### ACP over raw stream-json, but the margin is thin

Both reach the same harness. Measured differences:

| | ACP | `claude -p --output-format stream-json` |
|---|---|---|
| processes managed | adapter, then `claude` | `claude` |
| permissions | `session/request_permission` in the protocol | `can_use_tool` on a control channel |
| modes | `session/set_mode`, six advertised with names and descriptions | `set_permission_mode` |
| resume / fork / list | `loadSession`, `sessionCapabilities` | `--resume`, `--session-id` |
| other agents later | any ACP agent | Claude only |

The CLI's control channel already carries `can_use_tool`, `set_permission_mode`
and `interrupt` — the Agent SDK talks to the CLI over exactly that. So ACP is
**vocabulary, not capability**. It wins on having names for things, a spec
someone else maintains, and a path to non-Claude agents; it costs a process
unless the host is TypeScript.

Which is the deciding link: on a Bun host the adapter is `main: dist/lib.js` — an
**import, not a subprocess**. That removes ACP's only real cost, so ACP wins.

### Bun + Electrobun, not Wails, Tauri or Electron

| | backend | languages | webview | extra managed process |
|---|---|---|---|---|
| Wails | Go | Go + TS | system | adapter (Node) |
| Tauri | Rust | Rust + TS | system | sidecar (Node) |
| Electron | Node | TS | **Chromium** | none |
| **Electrobun** | **Bun** | **TS** | system | **none** |

Tauri's backend is Rust and cannot be Node; Node there is a bundled sidecar,
which is the layer being removed. Electron ships Chromium, and the resource
budget belongs to agent processes rather than to the window. Electrobun is
v1.18.1 with ~12.7k stars and commits the same day it was evaluated.

**Verified:** the entire ACP stack runs unmodified under Bun — `bun server.mjs`
in place of `node`, same adapter, same session, `apiType=native`.

### No terminal, no zmx, in tdeck

Dropping libghostty is not a retreat from the gdeck work, which proved a pane in
a webview is viable (p50 3ms keystroke latency, correct graphemes, wheel reaching
the agent as SGR mouse reports). It is that a structured agent stream makes the
terminal unnecessary *for the agent*, and everything the terminal is still good
for — editors, shells, agents started elsewhere — is what `awp deck` and zdeck
already do well.

zmx goes with it, for the chat path only — and this one was **measured**, because
zmx is the obvious answer to "how does an agent survive a restart" and it
deserved a real test rather than an assumption.

`zmx tail` + `zmx send` look exactly like a durable bidirectional pipe: a process
that outlives its clients, no attach, no reflow. The test was a scratch session
running `sh -c 'stty raw -echo; exec cat'` — raw mode, echo off, so nothing in
the line discipline could be blamed — with one 245-byte JSON-RPC line sent
through it.

**249 bytes came back, and the JSON no longer parsed.** Four carriage returns had
been inserted at ~80-column boundaries:

```
"sessionId":"abc-123", \r"prompt":[{"type":"text"…
```

The corruption is not echo and not `onlcr`. It is the **screen model**: zmx keeps
a terminal grid of a given width and `tail` emits what that grid shows, so
wrapping becomes real bytes in the stream. The same reason `history --vt` returns
the screen with SGR rather than the bytes the program wrote.

So zmx gives persistence for **a rendered screen**, not for a byte stream, and a
protocol needs every byte once and in order. Durability has to come from
somewhere else.

### Sessions belong to the agent

Measured against real sessions:

- **An idle session loads and replays.** `loadSession` on a conversation started
  by the terminal replayed it as `user_message_chunk` / `agent_message_chunk`.
  Confirmed again in the real client: resuming a past session in this repo
  replayed 107 events.
- `session/list` takes a **`cwd` filter**, which is the join with a workspace: a
  workspace is a directory, and its history is the sessions that ran there. The
  awp repo has 19.
- `sessionCapabilities` advertises `list`, `resume`, `fork`, `close`, `delete`.

### Correction: a live session does *not* refuse

The original probe reported that loading the currently-running conversation
failed, and this document concluded that "the one-writer rule enforces itself;
tdeck does not have to police it". **That is wrong**, and it was wrong in the
direction that matters.

Resuming the live session that was building tdeck — from inside tdeck — returned
`200` and attached cleanly. Nothing refused. Two clients were briefly holding one
conversation, and the only reason nothing was corrupted is that neither was
prompted while it lasted.

So the earlier probe failed for some other reason, and the conclusion drawn from
it was a guess dressed as a measurement. The rule has to be tdeck's after all:

- the client can attach to a session someone else is driving,
- so it needs a way to **let go** — `/close`, added with resume rather than after
  it, because resume without it is a trap,
- and the UI has to say which sessions are live elsewhere rather than assuming
  the agent will decline.

The general lesson is worth more than the specific one: a negative result from a
single probe is weak evidence. "It failed once" became "the agent enforces this",
and a safety property got built on it.

### One adapter hosts many sessions at once

The sidebar assumes it, so it was worth checking before building on it: if
`prompt` on session A blocked until A finished, the design would need an adapter
per chat — N node processes — and ACP's "an import, not a subprocess" advantage
would mostly evaporate.

`probe-concurrent.mjs` opens two sessions on one connection and prompts both
within a few milliseconds. The measurement is whether their update streams
interleave, since serialised execution produces exactly one handover point:

```
updates: 24   interleaves: 14
A: first 698ms  last 7700ms
B: first 1176ms  last 6949ms
overlap: 5773ms  ->  CONCURRENT
arrival order: ABBABAABABABABABBBBBAAAA
```

Both ran to `end_turn` on their own schedule, B finishing first despite starting
second. Both logged `apiType=native` — the subscription, two conversations at
once, one adapter process.

### Auto mode is the agent's, not the client's

The agent advertises six modes with names and descriptions — `auto` ("use a model
classifier to approve/deny permission prompts", and the default), `default`,
`acceptEdits`, `plan`, `dontAsk`, `bypassPermissions`. A client that
auto-approves by clicking its own buttons would only see the prompts the agent
bothered to send, and would diverge from what the same setting means everywhere
else in awp.

## What carries over from gdeck

- `ChatView`, `Markdown`, `Boundary`, `Sidebar`, panels, the shadcn setup and
  theme — React, so they port directly once the Wails bindings are cut.
- `@pierre/diffs` for rendering edits, already wired: unified layout, wrapped,
  framed to a fixed height because a diff is a citation inside a conversation
  rather than the document.
- The lessons that were expensive: prose capped to a reading measure while diffs
  stay wide; the composer owning its own draft so typing does not re-render the
  conversation; `content-visibility` and lazy diff mounting for scroll.

## What does not carry over

`transcript.go`, `Panes`, the libghostty patch set, the pty transport, the
resize/reflow plumbing. All of it stays in gdeck, which stays working.

## POC phases

Each phase answers one question and is judged by using it, not by whether it
compiles.

### Phase 1 — the chat, as a page

Grow `experiments/acp-chat` into the real UI. No desktop shell: it is a Bun
server and a page, which keeps the shell swappable and defers a decision that
does not need making yet.

- port the gdeck components
- adapter behind a unix socket rather than stdio, so an agent survives the window
  closing (see phase 5: cheap now, a rewrite later)
- one ACP connection hosting many sessions, not one adapter per chat (measured
  above: they genuinely run at the same time)
- sessions in a sidebar from `session/list`; `resume` on click; `fork` on a
  finished one
- modes in the UI, from what the agent advertises
- permission prompts rendered with the agent's own options

**Answers:** is a structured chat actually better to work in than the terminal?

### Phase 2 — live in it for a day

No new code. The signal is whether the terminal tab goes unopened. If it does,
the terminal was the wrong bet and tdeck is the product; if it does not, find out
what pulled you back before building further.

### Phase 3 — the shell

Electrobun, once phase 2 says the thing is worth a window. Buys a dock icon, no
browser chrome, file drop, no port. Keep the UI a page served by the backend so
the shell stays replaceable if Electrobun disappoints.

### Phase 4 — the parts of awp tdeck still needs

Workspaces, projects, PR status, review state. Read `~/.awp` state directly or
shell out to `awp`; do not reimplement any of it in TypeScript. awp's brain stays
Go — `zmx`, workspaces, `jj`, review, deckdata, and the hooks that produce live
status are the product, and tdeck is a client of them.

### Phase 5 — durability

An agent owned by tdeck dies with tdeck. `loadSession` makes a *restart* cheap —
the conversation is on disk — but work in flight is lost: kill the window while
an agent is three tool calls into something and that work is gone. That is
exactly what zmx protects for terminal programs, and the test above says zmx
cannot protect it here.

The answer is zmx's architecture with a different payload: run the adapter
**detached, listening on a unix socket** instead of on stdio. ACP is JSON-RPC over
a stream and the transport is swappable, so this is a small change — but only if
it is made early. Starting on stdio bakes in the assumption that agents die with
the window, and retrofitting a transport is the expensive kind of change.

So this is not "phase 5 if it matters". **Phase 1 should put the adapter behind a
socket**, because the cost now is a few lines and the cost later is a rewrite of
the seam. What waits for evidence is the rest of an agent host — supervision,
restart policy, multi-client fan-out — none of which is needed to keep a session
alive across a window closing.

**Done, and it works.** `src/adapterd.ts` owns the adapter and lends it out over
a unix socket; the server dials in. Tested by killing the server four seconds
into a 25-item generation: the daemon logged `client detached; the agent keeps
working`, buffered 13 chunks while nobody was listening, and flushed them when a
new server attached — which picked the count up at 6 and carried it to 25.

Two things that fell out of building it:

- **A reconnecting client must not send `initialize`.** ACP sends it once per
  connection and an adapter that already has one errors. The daemon writes a
  one-line handshake (`{"fresh":false}`) ahead of the protocol stream, so the
  protocol knowledge stays on the protocol side and the daemon remains a byte
  pipe. The client reuses the session id it recorded rather than opening a new
  one, which would abandon the very turn the daemon exists to protect.
- **A reattached client never sees `done`.** The reply to a `prompt` is
  addressed to the client that asked, so it arrives at a dead socket and the
  agent logs `Got response to unknown request N`. All the *output* is delivered;
  only the completion is lost, so the UI streams to the end of a turn and then
  sits there looking busy. Fixing it properly means the daemon tracking
  outstanding requests, which is the first protocol awareness it does not have.
  Deferred until there is a UI to be annoyed by it.

## The hard part, which none of this touches

Everything above settles the transport, and the transport was the easy question.
It makes tdeck a good **Claude client**. None of it yet makes it **awp**.

**One chat is a downgrade from the deck.** awp's value is showing eighteen
workspaces and which three need you. A chat window shows one conversation
beautifully. "Sessions in a sidebar" is not the same thing as "these two are
waiting on you, this one failed its gates" — the deck's job is attention across a
fleet, and nothing in the ACP work addresses it.

**Permissions get worse as they get rarer.** `auto` handles the routine ones, so
the prompts that do surface are the interesting ones. If you are reading a
different session when one arrives, a modal inside a conversation you are not
looking at is exactly the failure the deck's status dots exist to prevent. A
fleet-wide notion of "something is waiting" has to exist before per-session
permission UI is worth polishing.

**Attention has no home in tdeck yet.** Today it works: hooks →
`awp internal report-status` → the workspace store → status dots, which is
event-driven and already correct. That machinery is Go and should stay Go. So
tdeck consumes it — tdeck is a client of awp's brain, not a replacement for the
deck.

**And the taste question is unanswered.** Whether a structured chat is better to
work in than a TUI cannot be reasoned about, which is what phase 2 is for.

The trap to avoid: the terminal-versus-chat question is settled enough to build
on, and it is *not* the interesting one. The interesting one is what a fleet of
structured agents looks like when you are not reading any single conversation.
A chat client that handles one session well and eighteen sessions badly is a
worse tool than the deck it replaces, however much nicer the one session looks.

## Open questions

- **Billing is provisional.** Agent SDK usage currently draws from the Claude
  subscription; the change that would have moved it to a monthly credit is paused
  and Anthropic has said they will signal before anything takes effect. Fine for a
  personal tool, and the reason anything distributed would use an API key.
- **Distribution.** Offering claude.ai login in a product needs prior approval.
  tdeck as a personal client is not that; tdeck shipped to other people is.
- **Terminals in ACP.** The protocol has `createTerminal` / `terminalOutput`, so
  an agent can ask for one. Declining is fine for phase 1; if agents lean on it,
  that is the argument for keeping a real terminal in the client after all —
  and gdeck already proved that part works.
