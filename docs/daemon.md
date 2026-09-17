# The daemon (packages/server) — evidence

The measurements, probe output and wrong turns behind the rules in
`packages/server/AGENTS.md`. Nothing here loads into a session; it is read when a
rule is being questioned.

### A loaded conversation reports no usage at all

Reported as "im looking at a real chat i dont see it", after the floor came
off. Two separate reasons, and only one of them was the floor.

Measured against a real adapter:

```
  a turn, live           updates=9  usage=4   last used=28148 size=1000000
  loaded, nothing said   updates=3  usage=0
```

**A load sends none.** So the ordinary case — open a chat, read what the agent
said last night, say nothing — has no reading at all, and the figure was absent
exactly when somebody was deciding whether to carry on in that conversation or
start a fresh one. There is no call that asks: `fetchContextUsedTokens` is the
adapter's own, on its own schedule.

So the daemon remembers. `chat_usage` holds the last reading **per session id**,
written on every usage update — two integers against a cost measured in seconds
of model time — and handed back through `ChatOptions.usage`, which
`conversation` emits as its first update so every subscriber and every replay
sees it. Tokens do not change while nobody is talking, which is what makes a
stored reading still true.

**Keyed by the session, not the workspace**, and that is what makes `/new`
correct with no delete: a fresh conversation has a new id and therefore no
reading, so it cannot inherit the tokens of the one it replaced. The row for a
forgotten session stays, because it is still true of that transcript — which a
fork can load later.

`probe:chat` carries the check, and it is a `usage=0` away from being a test
that cannot fail: a fixture would agree with itself about an update the adapter
does not send.

### The agent's own face on the daemon

Every wire between the window and its agent pointed one way. The window could
type at an agent — a review, a page note, a task — and the agent could answer
only by printing into a terminal amoeba draws. `mcp.ts` is the other
direction: an MCP server the agent connects to, over the same handlers the
window uses.

Three decisions, and each is the sort that is hard to change later.

**The transport is stdio, one server per agent.** The alternative was one
HTTP/SSE server on a known port with the workspace as an argument — one
process instead of many, and it makes the binding below _conventional_ rather
than structural: anything that could reach the port could name any workspace.
stdio has no port and no argument, and the cost is a process that does nothing
but forward and dies with the agent.

**The scope is the working directory, and the binding is the absence of a
parameter.**

```
  ThreadAt   ReviewAt   ReviewFile      all take `from`, none takes a pair
  awp_thread · awp_review_comments · awp_file_finding
                                       none takes a project or a workspace
```

There is no call an agent could make that reaches another checkout. Same rule
as `-R` on every jj call, and `mcp.test.ts` asserts it on the tool schemas
rather than on the dispatch — a tool that grew a `project` argument would fail
that test before anything called it.

The Go implementation is the argument: an agent that ran the filing command in
the _source_ repository filed seven findings into that repository's own review,
and both sides reported success. `NotAWorkspace` exists so that arrives as a
sentence naming the directory, and the sentence _is_ the interface — what reads
it is a model.

**No MCP SDK.** MCP's stdio transport is line-delimited JSON-RPC 2.0, which is
byte for byte what `acp.ts` already speaks to the Claude Code adapter — and
that client is hand-rolled here for the same reason. Three methods are answered
and one notification ignored; a dependency for thirty lines of dispatch is a
dependency whose upgrades this repo would have to track.

### A refused tool is a result; an unknown method is an error

The two negatives go down different channels, and getting either wrong is
invisible until a real client is on the other end.

```
  tools/call, no such tool     { isError: true }   the MODEL chose the name
  tools/call, daemon refused   { isError: true }   the model has to read why
  an unimplemented METHOD      -32601              the CLIENT asked; clients
                                                   probe for optional methods
                                                   expecting exactly this
```

A refusal sent as a JSON-RPC error is hidden by most clients, which tell the
model only that the call failed — for `NotAWorkspace` that throws away the one
thing worth knowing.

**A notification is answered with nothing at all.** `notifications/initialized`
has no `id`, and a reply carrying a null id is a protocol error at the other
end. Every client sends it on every connection, so getting this wrong breaks
all of them. The probe sends it _between_ two requests, deliberately: a stray
reply would be read as the answer to the next one, which is how it would
actually break, and is invisible if it is the last thing sent.

### Prose, not JSON, because a model reads it

`awp_thread` answers sentences. A JSON blob makes every field equally
prominent, and here they are not — the other checkouts' **directories** are the
reason to call it at all, and a pair is not something an agent can act on. So
`ThreadCheckout` carries `dir`, which the caller could not compose: the
convention is the daemon's rule, the same argument as `SessionIdentity` being
on the wire.

`running` on each checkout is not "healthy" — it is `isLive`, not mere presence
in the listing, because zmx keeps an exited session listed and that would report
every abandoned checkout as occupied.

Two negatives again, and only one is a failure:

```
  not a workspace at all   NotAWorkspace — the agent is somewhere it did not
                           expect to be, and is told by name
  a workspace, no thread   `thread: undefined` — an answer. Most checkouts on
                           a real machine predate threads entirely
```

Refusing the second would make the tool useless on the ordinary case.

### The server is handed to the conversation, not written to a file

`chat.ts` puts it in `mcpServers` on every `session/new`, `session/load` **and**
`session/fork`. No `.mcp.json` in the workspace, no edit to anybody's config —
and on every open rather than only new ones, because a loaded conversation that
came back without its tools reads as an agent that has forgotten how to use
them.

`serverSpec` names `process.execPath` rather than a `bun` on the PATH, for the
same reason `adapterPath` does: the daemon runs under Bun and the agent's
environment is not the daemon's. And `AWP_DAEMON_URL` travels with it, so a
second instance's agents reach the second instance — otherwise every branch
daemon's conversations would file findings into the one somebody is working in,
which is the same class of mistake the directory binding prevents, one level up.

### `bun run probe:mcp`, and the two things it caught

```
  initialize    {"name":"awp","version":"0.0.0"} {"tools":{}}
  tools/list    awp_thread, awp_review_comments, awp_file_finding
  awp_thread    You are in awp/awp-kit-amoeba at …
  outside       refused: /Users/acohen is not inside an awp workspace
  file_finding  added a comment to awp/awp-kit-amoeba on AGENTS.md:1
  round trip    the finding came back
```

**`await` the flush.** Bun's writable end buffers, and a `flush()` whose promise
is dropped can leave the line unsent while the probe waits for an answer to it.
That presents as a server that never replies — and it was the server answering
perfectly the whole time, with nothing having reached it. Verified by running
the entry point with a here-doc on stdin, which answered instantly.

**A check that cannot fail reads as a pass.** The "started outside a workspace
must refuse" check used `process.cwd()` and reported NOT REFUSED — correctly,
because this repository is itself checked out at
`~/.awp/workspaces/awp/awp-kit-amoeba`, so the probe's own directory _is_ a
workspace. It uses `homedir()` now.

The probe also removes the finding it filed, over the rpc rather than through a
tool — because there deliberately is no removal tool. An agent that could delete
review comments could delete the ones somebody left for it. A probe that left its
own remarks in a person's diff panel is a probe nobody runs twice.

### `String(error)` is the tag, and only the tag

Five places in the renderer rendered a refusal as `String(error)`, three of them
under a comment saying it was "the daemon's own sentence, which names the
directory". It is not. Every refusal in the contract is a `Schema.TaggedError`
carrying one field, `reason`, and none of them sets `message`:

```
  String(error)   "SessionStartFailed"
  error.reason    'could not run zmx in …/awp/diff-view (does the directory
                   exist, and is zmx on PATH?)'
```

What that looked like: a button that appeared to do nothing at all. The click
ran, the daemon refused, the panel set its failure to one word and drew it in a
row nobody would read as an error. `said` in `daemon.ts` is the one reader, and
it falls back to `message` then `String` so a real defect still renders.

Found by instrumenting the click, having first read the button as broken — the
general shape being the one already recorded twice here: **a declaration being
emitted is not evidence that anything consumes it**, and a value being _set_ is
not evidence that what was set says anything.

The same run improved the sentence it was failing to show. `runIn` in `zmx.ts`
answered every spawn failure with "zmx failed (is it installed and on PATH?)",
which is right for `run` — nothing else can stop a spawn with no `cwd` — and
wrong for the one call that has a directory: **a directory that does not exist
is also a spawn failure.** A confident wrong cause is worse than an uncertain
right one, because it sends the reader to the wrong file.

- **The shell is Electron, and its three bundles are not Vite's.** `main`,
  `preload-host` and `preload-guest` come out of `scripts/build-electron.ts`
  through `Bun.build`, because what they need is two module formats and no Babel
  — the whole reason Vite is here is StyleX and the React compiler, and none of
  that applies to a hundred lines of glue. The preloads are **CommonJS**, and
  that is not a style: an ESM preload requires `sandbox: false`, and the guest
  one runs inside an arbitrary website.
- **`.cjs`, because the app is `"type": "module"`.** Bun writes `.js` whatever
  the format asked for, so the build renames — a CommonJS preload under a `.js`
  name inside a module package fails at its first `require`, in a process with
  nowhere to print it.
- **`@vitejs/plugin-react` v6 silently ignores a `babel` option.** It was removed
  and passing one is not an error. React Compiler comes through
  `@rolldown/plugin-babel`, and StyleX rides the same pass. The tell that it was
  not running was a bundle byte-identical to one built without it.
- Vite owns the renderer and the shell only serves it. Nothing compiles it
  twice.
- **A barrel export can drag `node:fs` into the browser.** The job record is a
  Schema, so the contract imports it, so the renderer imports it — and
  `@awp-kit/jobs`' index reaching `sqlite.ts` was enough to break the dev server
  outright. The sqlite store lives at `@awp-kit/jobs/sqlite`, which only the
  daemon asks for. A production build would have tree-shaken it and said
  nothing.
- **Base UI tabs are controlled here, deliberately.** StyleX resolves styles at
  render — `stylex.props(a, on && b)` — so which tab is selected has to be a
  value the component can read. Base UI still owns the arrow keys, the roving
  tab stop and the aria wiring, which is the whole reason it is there.

### The page is a place both sides can move

The web panel's address was the window's alone: typed into a box, kept per
thread in localStorage, reachable by nothing else. That rules out the ordinary
request — "open the failing build" — being answered by the half of the
conversation holding the URL.

```
  agent ──awp_browse{url}──▶ mcp-main ──PageOpen{from,url}──▶ daemon
                                                               │ dir → pair → thread
  window ◀──────────── PageChanges{thread,url,at} ─────────────┘
```

**The daemon resolves the thread; the caller has a directory.** Same binding as
every other agent-facing call — `from` and no thread parameter — so a
conversation cannot move a page beside somebody else's work. A workspace no
thread claims gets the absent-thread bucket the panel already keeps for one,
because most checkouts on this machine predate threads.

**A stream, and nothing is replayed.** A navigation is an _event_, so `Pages`
holds a `PubSub` rather than the `SubscriptionRef` `WorkspaceState` uses:
replaying the last one to a window that has just connected would move the page
somebody is reading, for a request answered before they opened the window.
`pages.test.ts` asserts the silence, and asserts it with a timeout rather than
an interrupt — a check that cannot fail reads as a pass.

**`at` is on the wire because asking for the page already showing means
reload.** Two value-equal urls are otherwise one event, and the window cannot
tell a second ask from no ask at all. `Web.tsx` acts on `at`, and the
last-acted value is **module scope**: the panel is unmounted on every tab
switch, so a ref would be reset and the newest request — still sitting in the
atom, unchanged — would be acted on again, which is the page reloading every
time somebody opens the tab.

**The subscription is above the panel, and that is the whole of `usePages.ts`.**
Base UI unmounts a hidden tab, so a subscription owned by the web panel is not
there precisely when it is needed: the moment an agent has something to show is
the moment somebody is reading the diff. `App` calls `usePageWatch()`; the
panel reads the atom. Measured, on `#/` — which mounts no web panel at all:

```
  PageOpen  →  {"thread":"20260826-ck22","url":"https://example.invalid/build/412", …}
  before       amoeba.page  null
  after        amoeba.page  {"20260826-ck22":"https://example.invalid/build/412"}
```

**The daemon refuses a url; the address bar guesses at one.** `addressFor` turns
`localhost:5173` into a URL and prose into a search, which is right for a person
watching the result and wrong for a call nobody is watching — a mistyped path
becoming a search reports success for a navigation to a search engine. Two
schemes only, and `file://` is the one worth naming as excluded: the panel is a
real browser view with a preload in it.

`probe:mcp` drives the _refusal_ rather than a navigation, deliberately: the
success path moves the panel of the thread this repository is in, which is a
panel somebody has open. Same shape as `probe:workspace` guarding on `ours()` —
a guard on the property that matters beats a blanket refusal, and it keeps the
check runnable.

### Two clients on one conversation, and what neither of them could see

`apps/tui` opens the same `ChatOpen` the window does, so a conversation can
have a terminal client and a window client at once. **No daemon change was
needed to read one** — the numbered subscriber queues were already a fan-out —
and the two things that were missing were both about _writing_.

```
  ChatOpen            already fans out: register, then snapshot          ✓
  what somebody typed no adapter echoes a user chunk on a live turn      ✗
  a question answered nothing in ACP says one was                        ✗
```

Left alone, that is a pair of clients each seeing half a conversation: the TUI
watched a turn start and an answer arrive with the question missing, and both
went on offering buttons for a permission the other had settled minutes ago —
where pressing one earns `that request has already been answered`, which is a
refusal about somebody else's click.

**The daemon says both, because it is the only process that knows.** `send`
emits the user's message before the turn edges; `answer` emits
`status: "answered"` with the option id after the reply.

**The key is the client's, and that is the load-bearing half.** The sender
paints its copy on the keypress — `mine` in conversation.ts, which exists
because nothing echoed anything — so the echo names a row that is already on
screen, and the two need one name to be one row. A key minted by the _reply_
would arrive after the row it names. So `ChatSend` carries it, both folds
ignore an echo whose key they already hold, and it is a **uuid**: two clients
with a counter each would both mint `mine-1`, and one window's second message
would silently swallow the other's.

**The option id and not its name.** Every client holds the options for the
request it is drawing, so it can say `Always Allow` in its own words — a name
on the wire would be a second copy of something already sent, and the one that
drifts is the copy nobody tests.

Measured against a real adapter, `probe:chat`:

```
  echoed      yes, under probe-1
```

which is a check that could not be a test: a fixture would agree with itself
about an update the adapter does not send.

### Two slash commands, in two faces, and one rule between them

The TUI reads a slash now, so `commands.ts` moved out of the renderer to
`@awp-kit/protocol/commands`. Which commands are the _client's own_ is a rule
rather than a rendering — the same argument that puts `SessionIdentity` on the
wire — and a second face deciding it for itself is the copy that drifts.

The move found the bug it exists to prevent, already shipped in the window:
the menu called `onCommand` for **every** row and `run` branched only on the
name, so a highlighted `/bro` ran `/new`. Picking a skill from the menu threw
the conversation away.

```
  mine        /new · /mcp        the client acts, nothing is sent
  not mine    /bro · /usage      a prompt. `run` sends it, or completes it
                                 when it takes arguments
```

`/compact` needs nothing, for the same reason `/usage` did not: the adapter
advertises it and passes it through.

### A bar is a box, and a menu that shrinks lands on top of the composer

Three findings from putting that menu in a terminal, none of which has a web
equivalent.

**`bg` on a `text` paints under its own characters and stops.** Width 100%
does not change it — measured, `["17:245,169,127","23:30,32,48"]` on a
40-cell row. What fills a row edge to edge is a **box's**
`backgroundColor`, with the text inside it. Every bar in the TUI was a `text`,
so every bar was a coloured phrase.

**Every child of a column shrinks by default, and one with no height of its
own overflows its parent rather than pushing its siblings.** The menu drew two
of its six rows, then the composer, then a third row _over_ the composer.
`flexShrink={0}` and a height is the pair; the scrollbox is what gives.

**A rolled-up run opens again on a click.** A summary is an offer to look, so
the row is a control — `onMouseDown` on the whole row, because a two-cell
target in a terminal is one nobody hits. No chord: a transcript has no focus
model, and every row would have to be reachable before one row could be.
Driven in `probe:transcript` with `createMockMouse`, which puts a real press
through the renderer's hit testing rather than calling the handler.

**A copy leaves the screen exactly as it was**, which is what a gesture that
did nothing also looks like — so `notices.ts` and `Toast.tsx` say `copied 12
characters`, bottom right, for 1.6 seconds. Module scope and not component
state, because the copy happens off a renderer event outside React: what is
wanted is a value _plus_ a subscription, the same argument the window's
`atoms.ts` makes.

`notices.ts` is named that way because `Toast.tsx` sits beside it and this
filesystem is case-insensitive — `toast.ts` and `Toast.tsx` are one module,
and the import resolves to the wrong one. Already recorded here as the cause
of a webview nothing could close.

**A run of tool calls folds when its turn ends, not while it is running.** The
window keeps the last four of every run whatever is happening, and in a column
that is the whole screen that is wrong for the one case somebody is watching:
while the agent works those rows are the progress. So `Item` carries the turn
it was made in — nothing on the wire does — and `grouped(items, live)` draws a
live run whole and a finished one as `ran 7 tools`, with the mark set by
whether any of them failed.

### `toolKind` is ACP's coarse enum, and the tool's own name is one field away

The verb on a tool row comes from ACP's `kind`, which the adapter maps from
the tool it actually ran. Most of what an agent does in a terminal is `Bash`,
so most rows read `execute`, and everything the adapter has no case for —
skills, MCP tools, `AskUserQuestion` — reads `other`.

```
  Bash                                    execute
  Read                                    read
  Edit · Write                            edit
  Grep · Glob                             search
  WebFetch · WebSearch                    fetch
  Task · TodoWrite · Task{Create,Get,…}   think
  Skill · AskUserQuestion · mcp__*        other
  ExitPlanMode                            switch_mode
```

`_meta.claudeCode.toolName` is the real name — `Bash`, `Grep`,
`mcp__awp__awp_thread` — and it was simply not being read. It is on the wire
now as `ChatUpdate.toolName`, and `toolVerb` in `@awp-kit/protocol/tools` is
the rule **both faces** read it by:

```
  Bash                  bash          the name, lowercased
  mcp__awp__awp_thread  awp_thread    the server is already in the title
  WebFetch              fetch         two words where one will do
  a subagent            code-reviewer `spawned` said neither what nor to what
  nothing               execute       an older daemon, or a replayed row
```

**And then most rows do not draw it.** `toolLabel` is what a row actually
puts in front of its title, and for a command that is nothing at all — the
same arithmetic as the accent and the review queue's leading icon: most of what an
agent does in a terminal is `Bash`, so a column saying `bash` on every other
row has spent its left edge on the thing nobody is scanning for.

```
  before   ✓ bash      Check the types        ← nine cells of padding, and a
           ✓ read      apps/tui/src/lines.ts    word true of half the column
           ✓ awp_tasks awp_tasks

  after    ✓ Check the types
           ✓ read apps/tui/src/lines.ts       ← the row that is NOT a command
           ✓ awp_tasks                          says so once
```

Three suppressions: the baseline (`bash`, and `execute` for an older
daemon), and a tool that names itself, where the label would repeat the
title. Reported as "i think you can remove bash and all the spaces".

**With the label gone, a title-less row would be a blank line**, so
`toolTitleOf` falls back to the name — which is the whole of what a pending
Bash call has until its command streams in. The TUI drops the padded column
with it; the window keeps its 4rem one, because that panel is one of three
and the empty slot is what keeps its short rows sharing an edge.

**Passed through, not translated.** A list of known tools here would report
every tool this repo has not heard of as `other`, which is the failure being
repaired. Anything unrecognised is drawn as itself.

**And the row says what the call was FOR, where the agent said.** Bash's own
schema requires a description — "Clear, concise description of what this
command does in active voice" — and the adapter forwards it as
`_meta.claudeCode.title`. It is the only field on a tool call that carries
intent rather than mechanism, and it was going nowhere:

```
  bash  python3 - <<'PY' … forty lines of heredoc …
  bash  Show where the adapter reads a tool's description field
```

`ChatUpdate.purpose` on the wire, `toolTitleOf` prefers it, and **the command
stays reachable** — the window on the tooltip and in the opened row, the TUI
as a dim line under any call that stands alone. That last one is not
symmetry: a call waiting on a permission is drawn as `Remove the build
output`, and approving `rm -rf` from a description alone is the decision
nobody should be asked to make.

`heldBack` is what each face asks — a row drawn as its purpose has something
to open, a row drawn as its command does not, and a pending `Terminal` has
nothing worth either.

Only Bash and `Task` carry one, which is why nothing tries to invent one:
`Task`'s is already its title.

**One rule, beside commands.ts and for the same reason.** The window said
`ran`, `read`, `edited`, `searched` off the kind while the terminal said
`bash` — two vocabularies for one conversation, and somebody moving between
the faces had to learn both. The window's `verb` and the TUI's `verbOf` are
shape adapters over `toolLabel` now, and the rule is tested beside itself.

Still unread, and both are worth having the day a row wants more than a
label: `rawInput` (the call's own arguments) and `locations` (the paths it
touches). Read out of the installed adapter's `tools.js` and `acp-agent.js`,
0.70.0.

#### A fork is not a load, and it has to happen where it will be used

"Open the conversation the terminal is having" is the feature people want, and
it is a `session/fork` underneath — never a `session/load`. Loading makes the
daemon a second writer on a transcript an interactive `claude` is still
appending to, with neither process aware of the other, which is why
`ChatOpen` refuses to join the newest session in a directory at all. A fork
reads it, copies it under a new id and leaves the original alone.

**The first shape was: fork here, write the id down, open it there.** It does
not work, and the way it fails is the shape this file keeps recording:

```
  forked to        2392409f-…
  in the listing   NO            a fresh fork is not in session/list
  opened the fork  NO — fell back to 715cd9c7-…
```

`session/load` on a fork that has said nothing yet fails, and the fallback is a
new empty session — which from outside is _exactly_ what a fork that carried no
memory looks like. So the fork is made inside the adapter that will hold the
conversation: `ChatOptions.fork` asks the open itself to do it. What the daemon
can do from outside is arrange for the next open to fork, which is what the
in-memory `forkNext` set is; a refusal clears it, or an ordinary open minutes
later would fork on somebody's behalf with nothing having asked.

**And once it has said something it is loadable.** That is the half the feature
rests on, because the adapter is released two minutes after the last window
closes and every later visit is a fresh process loading by id:

```
  fresh fork, then load      NO
  after one turn, then load  yes, replaying 6 updates
```

**A fork replays nothing.** It is `resume` + `forkSession` under the hood, and
resume "replays nothing, remembers everything" — so an empty stream proves
nothing either way and only a question does. `probe:chat` asks the fork what
word the original was told, which is the only check that separates a working
fork from a new session wearing the name.

That question was also reported as failing twice while the fork worked
perfectly, because the answer arrived as `"he"` then `"ron"` and the check
tested each update for the whole word. **Join the chunks before asserting on
them** — the same thing the panel's fold exists to do.

#### A question belongs on the call it is about

The adapter emits the tool call **before** it asks — `ensureToolCallEmitted` in
its own source — and the permission request carries that call's id. Drawn as
its own row the question was a second copy of the command already on screen
directly above it, with the buttons belonging to neither:

```
  before   …  ran  rm notes.txt          after   …  ran  rm notes.txt
           rm notes.txt                          Deny  Allow Once  Always Allow
           Deny  Allow Once  Always Allow
```

The standalone row is still there for a question about a call this window was
never told about — refusing to draw it would leave an agent waiting on
somebody who cannot see what it asked.

#### A steer is not a reply, and two turns overlap

Reported as "when you steer the message gets out of order", and every part of
the diagnosis is a fact about a real adapter that no fake produces.
`bun run probe:steer` sends a long prompt and interrupts it twelve seconds in:

```
  0s    turn started            the first turn
  2.7s  agent "…"
  12s   turn started            ← the steer. The first turn is still working
  20.7s turn ended              the FIRST one, while the second still runs
  23s   agent "heron"
  23.1s turn ended

  user chunks echoed back   0
```

Three things follow, and the panel had all three wrong.

**`running` is a count, not a flag.** The first `ended` arrives while the
steer's own turn is still working, so a boolean cleared there says the agent
has finished while it is still answering — the worst of the three states to be
wrong about. The same shape as the modal overlay count, and for the same
reason: two of a thing can be open at once and the inner one closes first.

**Nothing echoes a steer back.** Zero `user_message_chunk` on a live turn, so
the window's own copy is the only record of what a person typed until
`session/load` replays it. It cannot be dropped in favour of the wire.

**A steer is answered after the turn it interrupted** — when it is sent as a
prompt at all, which is the next section. So it is queued rather than said. Appending it to the end put it above the rest of a reply that was
still arriving, and two turns in the transcript read as though the agent had
answered a question before it was asked:

```
  agent  I'll look at
  you    no, not that file        ← typed here
  agent  src/foo.ts               ← the SAME sentence, below the interruption
```

So `mine` in `conversation.ts` is its own function rather than a `fold` over a
synthesized update, and that is as much of the fix as the queueing is: the
local copy is not something the daemon said, and dressing it up as an update is
what let it be placed by arrival order in a list arrival order does not
describe. A queued message floats at the tail, everything the agent is still
producing is inserted **above** it, and a turn ending un-queues it.

#### A subagent is a tool call, and `_meta` says which

There is **no subagent update kind in ACP** — no nesting, no separate stream,
and a subagent's own messages never arrive. Worth writing down so nobody goes
looking. What arrives is one tool call that sits at `in_progress` for minutes,
and the facts ride in `_meta.claudeCode.toolResponse` on its progress beats:

```
  subagentType         which kind was spawned    →  `spawned  a code-reviewer`
  elapsedTimeSeconds   how long                  →  `2m14s`, past ten seconds
  subagentRetry        attempt · max_retries ·   →  `attempt 2 of 5,
                       retry_delay_ms                retrying in 30s`
```

The retry counters are the least obvious and the ones worth having: the
adapter's own comment says it forwards them "so clients can show why a spawn
looks stalled". They are the SDK's fields in the SDK's spelling, so they are
read as `max_retries` first and camelCase second rather than assumed. A
subagent behind a rate limit and a subagent doing slow work are otherwise the
same picture, and only one of them is worth waiting for.

#### A turn edge says when the daemon SENT, not when the agent got there

`queued` is the mark on a message typed while the agent is working, and what
cleared it was "a turn ended, so clear every queued message" — under a comment
claiming the turn that ended is the one it was waiting behind. With one message
queued that is true by accident. `bun run probe:steer` drives two, and its
second scenario is the whole finding:

```
  send answered   prompt in 1ms · prompt in 0ms
  turns           started → started → started → ended → ended → ended
  a dequeue edge  NONE
```

Three things follow, and the first is the one that is easiest to get backwards.

**A message typed mid-turn is not held anywhere in awp.** `send` returns in a
millisecond: it goes as a plain `session/prompt` and the _adapter_ queues it at
priority `next`. So `queued` is a mark on a message somebody else already has —
which is also why "take it back" is not available, and why TODO #132 now says
so rather than asking for it. There is no per-prompt cancel in ACP, and
`session/cancel` settles the running turn and **every** queued one with it.

**All three `started` fire at send time**, so that edge says nothing about when
the agent reached a message. The adapter's own `activateTurn` promotes its
queue head and notifies nobody — read in its source, and confirmed by the probe
finding nothing at all between the second start and the first end. An end is
the only readable edge there is.

**And an end was unattributed, which is what made ends unusable.** Two of the
three landed in the same millisecond. So `ChatUpdate.id` is now on a `turn` as
well — the key of the message that prompted it, which only the daemon can say —
and `conversation.ts` keeps `inflight`, the keys of the turns in flight in the
order they started:

```
  head of inflight       the agent is working on this one
  anything behind it     still waiting        ← the mark, and now only here
  not in inflight        its turn is over, or a daemon too old to name them
```

Matched by name rather than by dropping the head, because nothing promises the
adapter settles them in the order they were sent. An empty `inflight` falls back
to the old rule, so an older daemon is no worse than it was.

The same probe with the keys on shows exactly what the old rule was getting
wrong, and it is three and a half seconds of a lie rather than a subtlety:

```
    21s  TURN  ended  probe-slow       end_turn
    21s  TURN  ended  probe-queued-0   end_turn   ← same second as the one
  24.4s  agent "orchard"                             above it
  24.5s  TURN  ended  probe-queued-1   end_turn
```

Two ends in one second, and the second message is not answered until 24.4s.
Clearing every mark on the first end reported it as sent while the agent had
not started it.

Checked by putting the old rule back, which fails
`releases only the message the agent has reached, when two are waiting` — a
test that needs two messages to fail at all, which is why one never caught it.

#### An edit answers with nothing, so the daemon has to say what it changed

A tool call's row is its title, its mark and its output — and the one kind of
call that changes anything has **no output at all**. So the row for an edit
said the least about it:

```
  ran     bun run test           ✓   + 40 lines of what happened
  edited  apps/.../Fence.tsx     ✓   ← and nothing else. The change is on
                                       disk and nowhere on screen
```

What the adapter does send is on the call's `content`, and it is not a patch:
`{type: "diff", path, oldText, newText}` — two whole texts — and **one block
per hunk**, so a `MultiEdit` of three places in one file is three of them.
Read out of the installed adapter's own `tools.js`, 0.70.0 on this machine.

**The daemon diffs, once.** Two clients would otherwise each need a differ,
and the terminal one has none. What crosses the wire is a unified patch, which
is the shape both faces already render — amoeba through the same
`parsePatchFiles` a fenced ` ```diff ` goes through, the TUI through the
colouring in `Items.tsx`. Same argument as `SessionIdentity`: a client
re-deriving a daemon's rule is a second implementation.

`diff` (jsdiff) is the differ, and it is not a new kind of dependency —
`@pierre/diffs` is built on it, and `createTwoFilesPatch` produces exactly
what `parsePatchFiles` reads back. Verified before anything was built on it,
because a patch format that nearly parses is the worst outcome.

Three rules follow, each one a way to get it wrong:

- **Replace, never merge.** The adapter sends its guess at the change when the
  call is made and the real one — out of the SDK's `structuredPatch` — when it
  has run, about the same file. Merged, the row draws the edit twice, older
  copy first.
- **An unchanged block is dropped.** A `Write` of content already on disk
  sends one, and an empty patch under a row is a row claiming an edit that did
  not happen.
- **`oldText: null` is a new file**, and jsdiff makes every line an addition
  from an empty left side with nothing special asked of it.
- **Each side gets a trailing newline, and that is not cosmetic.** An
  `Edit`'s two strings are a _fragment_, so they almost never end in one, and
  jsdiff says so with git's own marker — which `@pierre/diffs` throws on, from
  inside its renderer rather than its parser:

  ```
    \ No newline at end of file
    → DiffHunksRenderer.processDiffResult: deletionLine and additionLine are
      null, something is wrong
  ```

  The patch parses and then the panel dies, so the agent column is a stack
  trace for an edit that worked. Three of the five shapes an edit takes
  produce the marker, including every ordinary `Edit`, so it is the common
  case. A newline is added rather than the marker stripped — the marker is
  jsdiff telling the truth about what it was handed, and the wrong half is the
  question: a fragment has no end of file to be missing a newline at. An empty
  side stays empty, or a `Write` gains a line to delete that never existed.

**And a chat's patch needed the render version the diff panel already had.**
Reported as the same sentence — `DiffHunksRenderer.processDiffResult:
deletionLine and additionLine are null` — and the cause is the one `patch.ts`
records for the panel, in the one place that had not been fixed. A tool call's
patch is _replaced_: the adapter's guess when the call is made, the SDK's real
`structuredPatch` when it has run, on the same tool id. `Patch` in `Fence.tsx`
built its item as `patch-0` with no `version`, so the renderer reused the AST it
highlighted for the first patch and indexed it with hunks parsed from the
second.

It read as intermittent because it needs the patch to be replaced while the row
stays mounted, which is what an edit that _succeeds_ does — so the failure was
on the ordinary path rather than an exotic one. `contentOf(fileDiff)` on both
the `cacheKey` and the `version`, which is what `Diff.tsx` already does.

Two smaller things came with it. A file that parses and carries no line on
either side is dropped rather than drawn: there is no row the renderer can make
from one, and what it does about that is throw. And "did not parse" is now a
different answer from "parsed to nothing" — the first is still shown as the
text somebody wrote, because a model's fence often is not really a patch; the
second is shown as nothing at all.

Worth knowing while reading the log: the library `console.error`s **and then**
throws, so an error boundary catching it leaves the sentence in the console
anyway. A caught throw is not a quiet one.

**And the fold had to stop swallowing it.** `grouped` rolls a run of
consecutive calls into one block that draws its tail and counts the rest — so
an edit early in a long run sits behind `+7 earlier calls`, which is the
change itself folded away. A call carrying a patch now stands alone, and so
does a question, which was previously excepted one layer lower, where the
block is drawn. Both are decided in `grouped` now: a guard that can no longer
fire is one nothing tests.

The TUI's own fold is the same rule and a **different threshold** — it rolls
up _every_ receipt and keeps three, because that column is the whole screen
rather than one of three.

**opentui has a `<diff>`, and the wrong conclusion was drawn first.**
`<code filetype="diff">` was the first version of the TUI's half, on the
strength of `diff.plus` and `diff.minus` already being in `SYNTAX`. It draws a
patch in one flat colour: opentui 0.5.11 ships four grammars — javascript,
typescript, markdown, zig, read out of its own `default-parsers.ts` — and an
unknown filetype is not an error. That much was right, and the repair chosen
from it was wrong: colouring the lines by hand, off the first character.

`DiffRenderable` was there the whole time, and it is the same shape as the
window's half — it takes **the unified patch string**, parses it with the same
jsdiff that composed it, and does the line numbers, the signs, the row
backgrounds and the syntax highlighting of the code _inside_ the patch. So
both faces are now handed one string by the daemon and neither parses
anything:

```
  amoeba   <CodeView items=[{type:"diff", fileDiff}]>   parsePatchFiles
  tui      <diff diff={patch} filetype="typescript">    parsePatch
```

The lesson is the one about looking for the reader: a missing _grammar_ was
read as "this library cannot draw a diff", when what was missing was the
component that does. Grep the component list, not only the one you reached
for.

Three things it needed that are not obvious:

- **`addedBg` is the content background** when `addedContentBg` is unset, and
  the line-number gutter's is separately transparent. So sampling a row's
  colour at its first span reads the page and reports every row as unmarked.
- **Highlighting is asynchronous.** A frame captured immediately after the
  first render has white code on the right backgrounds; a second later it is
  the palette. Both are real frames, and only one is what a person sees.
- **A hand-written fixture patch is nearly always invalid**, and the
  renderable says so rather than drawing nonsense —
  `Error parsing diff: Added line count did not match for hunk at line 5`.
  The probe's patch comes out of `createTwoFilesPatch` for that reason.

Measured, which is the only way to tell a coloured patch from a flat one:

```
  the + row      bg #26382c  const #c6a0f6      ← palette, and highlighted
  the - row      bg #3b2733  const #c6a0f6
  a context row  bg #1e2030  if    #c6a0f6
```

`bun run probe:transcript` renders the TUI's own components through
`createTestRenderer` — no tty, no daemon, no socket — and prints the frame
plus those four readings. Every state in it is one a live agent happens not to
be in when somebody looks.

`probe:chat` carries the daemon's half, against a real adapter, and its first
run is the argument for the replace rule in one screen:

```
  blocks, all updates   2
  what a client draws   1
    notes.txt  -the word is: heron  +the word is: lantern
```

Two blocks for one `Edit`, on one tool id: the guess when the call was made
and the structured patch when it had run. A fold that merged would draw both,
and the stale one first.

**A patch survives being opened again, and it is not quite the same patch.**
Worth knowing because a chat is read far more often than it is had: the
adapter is released two minutes after the last client goes, so almost every
look at an edit made this morning is a `session/load` in a fresh process. If
the patch were live-only, the panel would show it for a few minutes and then
quietly stop — which is indistinguishable from a tool that never reported one.

It does come back. Replay walks the transcript through the same
`toAcpNotifications` the live path uses, and the diff block is built from the
tool's own **input**, which is in the transcript:

```
  live     -the word is: heron   +the word is: lantern   ← the SDK's
                                                            structuredPatch
  replayed -heron                +lantern                ← the Edit's own
                                                            old_string/new_string
```

So a reopened conversation shows the narrower hunk: the strings the agent
replaced, without the surrounding line. Both are true and neither is wrong to
draw; nothing tries to reconcile them, because the two are what the two
sources actually said.

#### And steering is an interrupt, so it is not what Return does

The section above is right about the mechanism and was wrong about the
default. Every message went through `_session/steering`, gated only on the
capability and on a compaction — so the ordinary act of typing while an agent
was working destroyed the answer in flight. Reported as steering too
aggressively, and as _"i thought steering was queueing"_, which is the whole
misreading in one sentence: the word sounds like a nudge.

The adapter says what it is, in its own comment, in capitals:

```
  priority "now"   pre-empts the current generation
  Pre-empting means ABORTING: the interrupted cycle emits a `result` of its
  own and the steered message runs as a second one
```

**The CLI settles what the default should be, and it is not ours.** Its input
queue is a rank, and a person's message is not at the top of it:

```
  pRe = { now: 0, next: 1, later: 2 }      lowest wins

  now     jump the queue AND abort the generation   ← the adapter hard-codes
                                                       this for steering
  next    head of the queue, taken at the next boundary
          └─ an ordinary user message is built at `next`, and an absent
             priority READS as `next`. This is the default everywhere else
  later   the model's own background traffic — task notifications, poll
          events. Nothing constructs a person's message at `later`
```

So waiting is not a fallback for adapters that cannot steer; it is what a
human message does, and awp was the exception. `interrupts()` is the rule now,
pure and tested, and `ChatSend.interrupt` carries the intent — **optional, so
that absent means the safe one**: an older client, a replayed call or a caller
that has not thought about it must not stop somebody's agent.

```
  return        session/prompt        the agent finishes, then reads this
  cmd+return    _session/steering     the agent stops where it is
```

Three things worth keeping.

**The window says which, and it did not.** A steer got no mark at all, so a
pre-empted answer and an ordinary one looked identical — the sentence simply
stopped and a different one began, which reads as a glitch rather than as a
consequence of typing. Being `prompt` now, a message typed mid-turn wears the
`queued` mark the panel already had.

**`onClick={onSend}` would have interrupted every time.** A click handler is
handed a MouseEvent, which is truthy, and `interrupt` is the first parameter.
`tsc` caught it — which is the argument for the flag being a named boolean
rather than an optional anything.

**Every daemon-side sender passes `false` explicitly.** A brief is the first
thing said to a new conversation, so there is nothing to cut short; an
`AgentSend` is awp itself talking, and a review to look at is not worth
throwing away an answer somebody is reading; and the compaction flush runs a
minute after the keypress, against whatever happens to be running by then.

#### Escape throws the draft away, in both faces

It did so in the TUI and, in the window, only while the slash menu was open —
so a key people press by reflex abandoned a half-typed message on one face and
did nothing at all on the other. Reported as exactly that.

Silently, in both. The TUI used to answer an empty composer with
`nothing to cancel · ctrl-\ goes back`, which put a sentence in the one slot a
refusal has to land in, for a key that did nothing.

**Only while there is something to throw away.** An empty composer lets Escape
past, because it is a window-level gesture elsewhere — with a dialog over this
panel, what should close is the dialog.

#### Steering is a request of its own, and a capability

All of the above is what a _second `session/prompt`_ does, and that was the
daemon asking the wrong question. The adapter has

```
  _session/steering    "injected into the in-flight turn rather than queued
                       as a separate session/prompt", at a priority that
                       pre-empts the current generation
```

advertised in the initialize reply as `_meta.steering.supported` — so it is
read rather than assumed, and an agent without it still works, one turn later.
The same probe run, before and after:

```
  session/prompt        started → started → ended → ended     two turns
  _session/steering     started → ended                       one, with the
                                                              steer inside it
```

**`idleBehavior: "promptRequired"` is the whole reason this is one call and not
two.** A steer sent when no turn is running would otherwise make the _adapter_
start one, detached — this process would emit no `turn started` and no `turn
ended`, and the window would watch a reply arrive with nothing saying a turn
was under way. With the opt-in the adapter refuses by name instead
(`{outcome: "promptRequired", reason: "noRunningTurn"}`) and the ordinary
prompt path runs and owns the lifecycle.

It also means **no "is a turn running" state is kept on this side**, which is
not a saving but a correctness argument: the adapter's own comment says its
check and its push "stay in one synchronous section so the turn cannot settle
in the gap between deciding to inject and enqueueing". Anything this process
believed about that could be stale by the time the request arrived.

So `ChatSend` answers `steer` or `prompt`, and the window marks a message as
waiting only for a `prompt` sent while the agent was working. The first version
set that from its own `running` count at send time and showed a `queued` label
for a few milliseconds on every ordinary steer.

#### The status row under the TUI's composer

The same read-only facts the window draws under its own composer — mode,
model, effort, fast mode, and how full the context is. It replaced a row of
chords, which were four things that are always true, and this file's own
argument about the status bar applies: a row that is never anything but
furniture is a row nobody reads.

Two things it needed that were not there:

```
  the settings   ChatConfig, asked once when the screen opens. A call and not
                 a field on the stream — the contract's own reasoning, and
                 nothing in the list changes unless somebody changes it
  the figure     `usage` was DROPPED by the TUI's fold, under a note saying it
                 said nothing a person reads. It is the only place the context
                 figure exists
```

A notice still takes the row while it has something to say, because a refusal
is exactly the thing that has to be read, and the answer keys are a notice of
that kind.

#### Two sources for one status, and neither can prove the other idle

A sidebar row's dot has always come from `~/.awp/workspace-state.json` — the Go
implementation's file, written by Claude Code hooks, and `workspace-state.ts`
says in its own note that ACP is what replaces that: "a live notification
instead of a hook writing a file".

It is a _second source_, though, not a replacement, and the reason is that the
two describe **different agents**:

```
  the file    the `claude` running in a workspace's TERMINAL
  the chat    the ACP conversation open in THIS WINDOW
```

A workspace can have both. So the merge is a precedence, in `factsWith`:

```
  waiting   a question nobody has answered. Wins outright — the one state
            that is about the person rather than the machine
  working   a turn in flight. Wins over the file, which is a hook's last
            write where this is live
  absent    the file's answer stands
```

**The chat never reports `idle`**, and that is the load-bearing half. A chat
sitting idle is no evidence at all about the agent somebody has running in the
terminal, and writing `idle` over the file's `working` would claim knowledge
this process does not have.

What the daemon tracks is folded from the conversation's own updates rather
than asked for, because there is nothing to ask — a turn is a state, and the
daemon is the thing that knows both its edges. One wrinkle worth knowing:
**there is no update for a permission being answered.** The adapter does not
report a reply, because the reply is the reply, so the only place that knows is
`answer` — which is why the watcher's counters are decremented from there
rather than from the stream.

#### A copied row's dot is a mark; an awp row's is a control

The store has a writer that is not ingest now, and everything about the panel's
one act follows from which rows it may touch.

```
  awp      written here. Swept by nothing, so there is no later reading of a
           file that could decide it had been finished    ← the dot moves it
  todo     ingest's upsert writes the source's status back over anything set
  claude   here, so a task marked done in this panel is pending again within
           ten seconds, with nothing on screen to say why  ← the dot is a mark
```

A control that lies is worse than no control, and the refusal already exists
one layer down — `TaskNotOurs`, republished as `TaskRefused`, whose sentence
names where that task is actually written.

**A tag is the exception, and it needed a column.** `task_tags.applied`
separates what a person applied from what a source implies, because ingest
replaces a task's tags wholesale — a source that stops implying
`project:thicket` must stop carrying it. Without the flag, a `thread:<id>` on a
`TODO.md` task is a write silently undone by the next reading of the file it
came from. Checked by removing the `applied = 0` from the sweep's delete, which
fails two tests.

An untag of a tag the source still implies is honest about being temporary: the
next sweep puts it back. That is the same shape as the status, one layer
smaller.

Measured in a browser at `#/`, which attaches to no session:

```
  47 to do        #91 and #124 first, both marked in progress
  markdown        P · EM · CODE · PRE · STRONG — and no literal `##`
  panel scroll    280 = 280, with 2327 characters of somebody's markdown in it
```

#### A plain fence must scroll, not wrap

Found by the above, and it had been wrong since `Fence.tsx` was written —
invisible for as long as the only fences on screen were short.

```
  block  a HIGHLIGHTED fence   overflow-x: auto      ✓ columns survive
  plain  no language on it     pre-wrap + anywhere   ✗ columns destroyed
```

A fence with no language is nearly always preformatted text whose line breaks
**are** the content — an ascii diagram, a column of measurements, a command.
What wrapping did to one of this file's own diagrams, in a 280px column:

```
  now       one list per Claude Code session, on disk, found by mtime

  now       one list          ← the same line, wrapped. Every column gone,
  per Claude Code               and the diagram now reads as prose
  session, on disk,
```

So a fence's _language_ was deciding whether its alignment survived, which is
not a distinction anybody wrote down on purpose. `plain` now matches `block`.

**And `Markdown.tsx` had a `styles.pre` that nothing used.** Its comment said a
fenced block scrolls inside its own box — the AGENTS.md rule, quoted correctly
— while `pre:` in the components map renders `<Fence>`, which has styles of its
own. The same shape recorded elsewhere in this file: **a declaration being
emitted is not evidence that anything consumes it.** Measure the computed style
on the element, which is what settled this one:

```
  whiteSpace  "pre-wrap"   overflowX  "visible"   scroll [186, 186]   before
  whiteSpace  "pre"        overflowX  "auto"      scroll [616, 186]   after
                                                          └─ real content
                                                             width, scrolling
```

#### A project's root is its default workspace, and that is the wrong file

The finding that `probe:tasks` exists for, and it could not have come from a
test — `tasks.test.ts` proves ingest over a list handed to it, and the sweep's
whole job is to _find_ that list on a real machine.

```
  project awp, root ~/go/src/…/awp   the DEFAULT jj workspace, on an old commit
  TODO.md there                      absent
  TODO.md in the workspace being      46 tasks
  worked in
```

`TODO.md` is a working-copy file, so reading a project's root reads whatever
revision that one checkout happens to be parked on. So every candidate is
offered — the root, and each `~/.awp/workspaces/<project>/*` — and the
**newest by modification time** wins.

Newest, and not all of them: taking all would put one project's list in the
store several times over, at several revisions, with nothing able to say which
row was true. It is also the rule `agent-tasks.ts` already applies to pick
among a directory's sessions, which is the argument for it being this one.

The candidates come from the directory convention rather than from
`jj workspace list`: `workspacePath`'s shape is already the thing this repo
relies on to recover a session's identity when it carries no labels, and a
subprocess per project per sweep is a cost paid for an answer `readdir` has.

#### A rename is a double click, in the two places the title is drawn

A thread's title is written once by a model, out of the sentence somebody typed
into the new-thread modal — frequently almost right, and until now unfixable.
`ThreadRename` had been on the wire since threads landed with **no caller at
all**.

```
  the sidebar heading   double-click the title · `rename…` in its ⋯ menu
  the sidebar row       same, but only where the row stands in for its whole
                        thread — under a heading the title belongs to the
                        heading, and two controls for one name is one of them
                        being wrong about what it owns
  the agent bar         double-click the title
```

**The field replaces the control rather than sitting inside it.** Both sites
draw their title inside something interactive — a fold button, the window's
drag region — and an input nested in either is an input whose clicks belong to
its parent. Escape is unambiguous for the same reason: there is nothing else on
the row to give the key to.

**And the bar was showing a copy.** Its title came from `identity.label`, the
zmx label written when the workspace was created, which is the thread's title
frozen at that moment — so a rename left the bar saying the old name with
nothing on screen to say which was true. The live title wins now, and only
where the thread holds one checkout: that is the sidebar row's own rule, which
this file already states one paragraph up.

```
  displayName   a name a person chose by hand            ← still wins
  thread title  where the thread holds one checkout      ← new, and live
  identity.label / workspace / session name              ← as before
```

Nothing is re-read after a write: every thread write announces itself on the
store's own feed, so this window and any other are told by the daemon. "The
reply is the update" is the rule for calls with **no** feed, and this one has
had one since `watchThreads`.

**And the bar was still showing a copy after that fix — a different one.** The
paragraph above replaced `identity.label` with the live title and left
`facts.displayName` in front of both, under a comment calling it "the one name
a person chose by hand". It is not: it is the Go implementation's
`~/.awp/workspace-state.json`, written by hooks, and frozen in exactly the way
the label is. Read off this machine:

```
  thread title   amoeba                                   ← renameable
  displayName    experimental rewrite from a clean slate  ← what was drawn
```

So renaming a thread still left the bar saying the old name. The sidebar has
always had the order right and says why — the title is the only one of these
that is neither shortened, sanitized nor second-hand — and the two strips agree
now, which matters more than either ranking alone: a window whose header and
whose selected row disagree gives nobody a reading that says which is true.

#### A tag, not a scope column

```
  thread     "paginate the tabular exports"
  project    "this repo still has no integration tests"
  global     "learn what jj fix actually rewrites"
```

A field with three values forces every task to pick one and makes the third
awkward. Tags do not, and they give the cross-cutting view for free: one query,
filtered by whatever tag is interesting, or nothing at all for everything.

**A tag is deliberately not a foreign key.** `thread:<id>` is a label somebody
applied, and it outlives the thread being archived — the same argument as
recording a thread's `parentId` rather than re-deriving it from jj. A tag
pointing at a thread that is gone is a claim about history, not a broken
reference.

#### cmd+B folds a column, cmd+shift+B the other one

The toggles in the top bar are the discoverable half; these are the half that
works from inside the pane, where the pointer is not. Both write through the
same `fold`, so the remembered state and the animation are the ones a press of
the button produces — and `menu.ts` claims nothing on B, which is what leaves
the key reachable at all.

`event.code` earns its keep twice here: the two chords are told apart by shift,
and `key` arrives upper-case whenever shift is down.

#### Forgetting a project lets go of its tasks, and that is not tidiness

`ProjectForget` is documented as taking nothing with it — no workspace
removed, no session killed, no thread touched — and that promise is about the
**world**. A task row is not the world: it is this daemon's copy of a `TODO.md`
still sitting on disk.

Leaving them is what cannot be survived. `ingest` is scoped by key prefix so
one project's read cannot delete another's, and the prefixes come from the
project list — so a project that is not on it is a prefix **nothing will ever
name again**:

```
  the sweep     one ingest per project on the list
  the prefix    `<project>#`
  off the list  frozen at whatever the last sweep said, answering every
                read, with nothing able to correct it
```

Measured: a board reporting 51 tasks against a file holding 42, for nine days,
with nothing in the window able to say so.

Deleting is safe for a reason the store already states about itself. `taskId`
is derived from the source and the key rather than minted — "stable across a
wipe of the table" — so re-importing rebuilds every row under the id it had,
and a project with a session still running in it reappears derived and is
swept again. Nothing is lost that a sweep does not put back.

**`ingest(source, prefix, [])` is the whole implementation**, because ingest
already takes the set a source _has_ and deletes the rest — which is the same
line that makes a finished task an absence.

**It runs even when the project was not on the list.** That reads as a no-op
and is the one repair available for rows already stranded: by the time anybody
wants them gone, the name is exactly what `ProjectForget` answers `false` for.
Gating the release on the reply would refuse to clean up precisely when there
is something to clean up.

#### Ingest takes the whole set, because a finished task is an absence

`ingest(source, keyPrefix, tasks)` and not an upsert per task. How a task
finishes in this repository is that its entry **leaves** `TODO.md`, and there is
no record whose absence a per-task write could notice — the same shape as
`useJobs`' refresh, and the same reason.

**Scoped by a key prefix**, which is not decoration: without it, reading one
project's file would delete every other project's rows, and the panel would
show whichever project was read last.

#### Subscribing is what makes the sweep run

The sources are files nothing here writes. There is no event to hang a refresh
on, so the old arrangement was a timer per open panel, each reading every
project's files for itself.

```
  nobody watching   no timer at all — a closed panel costs nothing
  one watching      a sweep every 10s, serving every client at once
  a turn ends       `nudge()` — the files an agent was editing have settled
```

`TaskChanges` carries **counts, not rows**, because the daemon does not know
which tags a given client is narrowing by; a push carrying rows is a push most
clients would have to correct. And the rule this file states twice already
applies: a stream carries changes from _now_, so the panel re-asks on
`onReconnect` as well.

**The turn edge is the one trigger that fires because of the thing that changed
the file.** `settled()` is that edge, and the case worth writing down is the
one an equality check would lose: a workspace that was `working` and is now
**absent** — a conversation released mid-turn — has settled too. `waiting`
counts as well; a turn that stopped to ask a question has stopped writing.

**And a write is not a sweep, so it has to say so itself.** Every trigger above
is about a _file_, and the store has a writer that touches none —
`awp_task_add` from the agent in the next column, a dot pressed in another
window, a project forgotten. The sweep counts what ingest moved, so a write
moves nothing it can count, and a panel that only re-reads on a push therefore
never learns. Measured in a browser against the daemon, on `#/`:

```
  written from outside, before   never arrived — the panel sat on 53 for 25s
  written from outside, after    11ms, unprompted
  removed from outside, before   still drawn, with the row already gone
```

`TaskFeed.wrote` is the announcement and the four handlers call it, after the
store has answered so a refusal publishes nothing. One row per call, in the
column that moved, because each of those calls is exactly one row.
`ProjectForget` says it too: releasing a project's rows empties them without
touching a file, which is the same shape one level up.

#### `TaskBoard`, not `TaskList` — the name was taken

`TaskList` is the reader for a _session's_ own list, keyed by a directory. The
two are deliberately different calls: that one asks what the agent in one
checkout is doing, this one asks what is written down anywhere.

#### Tasks awp owns, filled from files nothing here writes

A task list belonged to a **session**: `agent-tasks.ts` walks from a directory
to Claude Code's transcripts to the newest task directory under them. That is a
good reader and a bad home — a task cannot outlive the session that wrote it,
cannot be seen from any other checkout, and cannot be about anything larger
than the one it was written in.

```
  before   one list per Claude Code session, on disk, found by mtime
  after    one table in awp.sqlite, tagged, readable from anywhere
```

**Nothing here is the first writer, and the panel is read-only on purpose.** A
store nobody writes to is empty forever, so the writer is _ingest_: whatever a
source already wrote, copied in. That keeps the promise `agent-tasks.ts` makes
in its own comment — amoeba is not a second writer of somebody else's list —
while giving a task a home that survives.

#### The count of what is finished is the way to it

The panel hid completed tasks and said how many — `24 to do · 62 done`. That is
right for scanning and it made the finished half **unreachable**, which costs
the two readings people actually want: whether a thing was done already, and
what an agent got through while nobody was watching.

So the count is a button, and the section it opens is ordered by something the
list above it is not:

```
  outstanding   what is underway, then an agent's live queue above what is
                merely written down          ← a reading order
  done          newest first                 ← "what got done while I was
                                                away" is a question about when
```

`updatedAt` is on the wire for that one reader. Ingest only touches it when a
field actually changed — the upsert carries a `where` — so for a finished task
it is as close to "when it finished" as a file-backed source can say.

**The dot cycles back to `pending` from `completed`.** A finished row was
otherwise a row whose control did nothing, which is the lie this panel already
refuses to tell for a copied row. Putting it back is the one act a done list
is for.

**Not remembered.** `rememberedPanels` is the worked example of a per-thread
preference; this is a glance, and a section that came back open would make the
panel's first screen a list of work nobody has to think about.

#### The MCP surface is two readers and three writers

```
  awp_tasks        subjects, statuses and ids   scanned, and read to plan from
  awp_task         one entry in full            where the argument actually is
  awp_task_add     write one down               awp's own source, swept by none
  awp_task_status  move one awp owns            refused for a copy, by name
  awp_task_tag     label any task at all        it outlives every sweep
```

One tool answering both would put 46 tasks' worth of argument into a context
window to answer "what is already written down". A task here is an argument
rather than a ticket — `TODO.md` says so in its own preamble — so the body is
the valuable half and has to be asked for one at a time.

**An id is the one argument that could name another checkout, so it is
checked.** The three writing tools arrived after the two readers, and
`awp_task_status` / `awp_task_tag` take an id — which names a task in any
project. Every other tool here is bound by the _absence_ of an argument;
these are bound by `ownTask`, which reads the project's own board and refuses
anything not on it. The Go implementation filing seven findings into the wrong
repository is what that is for. `awp_task_add` takes no project either: it
tags with the checkout's own, and with its thread when one claims it, because
the agent has no way to know that id and an argument for it would be a second
thing to get wrong.

**`scope` is not a project name.** The binding rule holds — no tool here can
name another checkout — but the cross-cutting read is the reason the store
exists, so it is offered as `scope: project | all` with nothing to get wrong.
`project` is resolved from the server's own directory, through the same call
and the same refusal `awp_thread` uses.

**`includeDone` drops the status filter rather than inverting it.** The open
set is named — `pending`, `in_progress`, `blocked` — because a negative filter
would quietly include a status this window has never seen.

#### The panel draws every source as one list

```
  claude   what an agent in a checkout wrote for itself, copied in before the
           session that wrote it ends
  todo     a project's TODO.md, in whichever checkout wrote it last
  awp      written here, and swept by nothing
```

One list with the source as a mark, not two headed sections. Somebody scanning
this column is asking "what should happen next", and provenance is not the axis
they are scanning by — heading the sections makes the one thing nobody sorts by
the primary one.

**Nothing is deduplicated, and that is deliberate.** The same work being a
`TODO.md` entry _and_ a session task is common, and the two entries are not the
same object: different ids, different statuses, and the agent's copy is the one
it is actually working from. Merging would have to pick a status, and picking
wrong is worse than a row appearing twice with two honest states.

**The board needs no directory**, which changes what an empty panel means. It
used to be blank whenever no session was open; a workspace with nothing running
still has tasks written down about it.

**`#91`, not `todo:awp#91`.** The full id is what `awp_task` takes and what
nobody would read in a 280px column — the source is already said by the mark
and the project by the panel's scope.

**The scope control widens the question rather than being a fixed choice.** The
default is this project, because a column beside a checkout is usually asked
about that checkout; `everywhere` is the reason the store exists at all, so it
cannot be the thing nobody can reach. It only appears when a project is known.
A third width — this checkout — is what the second read used to be, and it had
no name while it was a separate call.

#### The read answers from the store and sweeps behind it

The pull request cache's shape, for the same two reasons: Base UI unmounts a
hidden tab, so the panel is mounted on every glance and must not cost a disk
sweep per glance — and a question that writes is what `--ignore-working-copy`
exists to prevent. `Effect.forkDetach` and not `fork`, because the fiber has to
outlive the request that started it.

**So a cold first read is legitimately empty**, and looks exactly like a
project with no `TODO.md`. `probe:tasks` reads twice for that reason alone:

```
  cold   0 task(s)
  warm   46 task(s)     ← the only line that separates "nothing to read" from
                          "the sweep never ran"
```

#### Two acts on a row, and they are opposites

The row had one button, `send`, which briefs the agent already open in this
thread. The second thing somebody wants is the reverse — leave this thread
alone and start a new one for the task — and having only the first is what
made this a list rather than a queue:

```
  send      hand it to the agent in front of you
  fan out   start a thread beside it, with the task as the brief
```

**The brief is the task, and the thread's name is not set here.** Subject, a
blank line, then the body — markdown, because a board task's body already is,
and a description running on from its own title is a paragraph nobody wrote. A
task with an empty body is its subject and no trailing gap. The _name_ comes
from the daemon's `name` step reading that sentence, which is the one place it
can come from: a client composing one would be guessing at a prompt it cannot
see.

**The base is the workspace on screen**, which is what cmd+shift+N already
means — a task read out of this checkout usually follows on from it, and
`baseOfThread` already resolves it. With no workspace the form stays on trunk,
which is cmd+N's answer. Measured, opening it from a row in a checkout:

```
  project   thicket
  base      andrew/tabular-exports              ← the workspace's own bookmark
  brief     "<subject>\n\n<the body>"            3 lines
```

**A glyph, not a second word.** The column is 280px and `send` is what is done
here most often; two labelled buttons make the rarer one read as half of a pair
of equals. Hidden by `opacity` like the send beside it — measured 0 at rest and
1 on hover — because `display: none` leaves the layout and takes the keyboard
with it.

**Nothing is written to the task.** A task started in another thread is
arguably no longer pending, and this panel still does not say so: the one act
it has on a row is the dot, for rows awp owns, and inventing a second status
writer for a fan-out would be a claim the source that owns the task would
overwrite on the next sweep anyway.

**And the modal is App's, so the request is an atom.** `newThreadAtom` replaced
App's `useState`, rather than joining it — a dialog whose openness is in two
places has two answers to "is it open", and the one that loses is whichever a
later reader believed. The tasks panel is the first opener that is not App's:
it is inside `Accessory`, behind a Base UI tab, and threading a callback down
to it would put a prop about a dialog through two components that have nothing
to do with either. The same argument the review queue made — the value _plus_ a
subscription, which is what a `let` is not.

#### Two sources go in the same door, and then there is one reader

The panel used to make two calls on a four second timer: a session's own list,
read off disk by directory, and the board. The second source closed that —
`task-feed.ts` ingests Claude Code's own per-workspace lists as the `claude`
source, so an agent's queue _is_ a board row, and reading it twice drew it
twice.

```
  before   listTasks(dir) + listBoard(tags)   one file read by two readers
  after    listBoard(tags)                    `source` says which file it was
```

**Sweeping every checkout is affordable, and that was measured rather than
assumed.** The candidates are the project root plus each directory under
`~/.awp/workspaces/<project>` — the same convention `todo-tasks.ts` reads by:

```
  59 workspaces   11ms serially   2ms concurrently   4 of them keeping a list
```

**The key carries the workspace**, because two checkouts of one project each
have an agent numbering its tasks from one. The prefix is still the project's,
which is what an ingest's sweep is scoped by — so `ProjectForget` has to
release **both** sources, or the one it left behind sits under a prefix nothing
will ever name again.

#### `unique (source, source_key)` buys two things

sqlite treats NULLs as **distinct** in a UNIQUE, so the one index that makes
ingest idempotent puts no constraint at all on a task with no source key — one
typed here, the day there is somewhere to type it. Both from one line.

`source_seq` is beside it because a source that counts its tasks writes `10`
after `2`, and text order puts them the other way round — which is a task list
in an order nobody wrote. `agent-tasks.ts` already sorts numerically for the
same reason.

`status` is text with **no `check`**. Claude Code's own set can grow — that file
says "or whatever else it gains" — and a constraint here turns an upstream
addition into a daemon that will not start.

#### A pull request moves, and the checkout does not

The signal a review cannot do without, and the one that is invisible without it.
A review workspace is a checkout of the head at the moment it was made; the
author then pushes a fix, or force-pushes a rewrite, and from that moment the
diff being read, the comments being written and the agent's findings are all
about code the pull request no longer has. Nothing on screen changes. That is
worse than being out of date, because a review delivered against an old head
reads as a review of the current one.

Measured on this machine the first time the check ran — two of two review
checkouts were stale, and one of them was made two hours earlier:

```
  checkouts   thicket/pr-2320 MOVED, thicket/pr-2418 MOVED
  by hand     present(<the PR's head>) & ::@   → empty
              present(<the PR's head>)         → empty
              the head is not in the repository at all
```

**Asked as "is the head an ancestor of `@`", not "is it equal to `@`".** Somebody
who has committed something of their own on top is still reviewing the right
code, and an equality check would call that stale every time.

**`present()` is what makes it one jj call rather than three.** Without it an
absent commit is an error — `Revision \`deadbeef…\` doesn't exist`, which is
exactly what a force-push leaves behind — and the caller has to tell that apart
from a broken directory by reading jj's prose. With it, an absent commit is an
empty answer, which is the same conclusion as having something older: this
checkout does not contain what the pull request is. A directory that is not a
workspace answers "nothing to repair" rather than claiming a stale checkout that
is not there.

**The daemon asks jj; the feed asks the daemon.** The head commit is in the
listing, which is `ReviewQueueFeed`'s, and answering the question means asking jj about
a workspace, which is the handler's — so `read` takes a `contains` callback. It
is asked only for rows that have a workspace, which on a real machine is a
handful of forty-eight, concurrently, and locally.

#### A review is the same job, with one step turned on

Reviewing a PR is `create-workspace` with `review` on its input. That switches
the `fetch` step from a no-op into a fetch and changes nothing else — and two
other differences fall out rather than being arranged:

```
  workspace  pre-set to `pr-<n>`   so the `name` step skips the model. Ten
                                   seconds spent inventing a name that must
                                   not vary is ten seconds wasted
  bookmark   none                  it is composed by the step that names, so a
                                   job that skips naming has none. `pr-123` is
                                   not a branch anybody should push
```

**The base is patched by the step, not decided by the handler.** A PR's head is
a branch name, which is not a revision until something has fetched it — and
which revset it becomes depends on what the fetch produced:

```
  from origin    feature@origin   jj does not track a fetched branch locally
  from a fork    feature          git wrote refs/heads, so jj imports it local
```

The remote one wins when both exist: a local bookmark of the same name is
somebody's own copy and may be behind the pull request, and reviewing a stale
branch is worse than not reviewing because nothing about it says so.

**`jj git import` after a fork fetch, or jj cannot see the ref.** jj caches its
view of the git refs per operation, and nothing about the symptom points at it:
the bookmark is simply not in `bookmark list` and the revision "does not exist".

**The name is `pr-<n>`, and the branch is deliberately not in it.** The archive
called it `pr-<n>-<branch>`, which reads better in a directory listing and is
the wrong identity — a branch can be renamed or force-pushed while the pull
request stays the same one, and this name is what every idempotence check is.

**Idempotent by two records, because one is not enough.**

```
  a thread holding `pr-<n>`   the review finished. Its job record may have been
                              cleared and the workspace is still there
  the job's idempotency key   the review is still being built. The claim is the
                              job's second-to-last step, so a running job holds
                              a thread no member lookup can find
```

`ReviewStart` answers with `created: false` in both cases, so the row can go to
the workspace rather than reporting a success that did not happen. The window
therefore does not track what it has started — a reload mid-create would forget,
and the daemon would not.

There is a third case, and it is a race rather than a state: two presses in one
second. `enqueue` answers the second with the first job, which leaves the thread
the handler had just made as litter — so the handler compares the thread on the
returned job's record with the one it created, and removes its own if it lost.

#### A stack, drawn as a tree

`├─`, `└─` and the `│` above them, monospace so consecutive rows line up as
columns — in a proportional face `│  ` is a different width from `└─ ` and the
tree bends.

```
  #10 base
   ├─ #20
   │  └─ #25
   └─ #30
```

Drawn from the _list_, not from the row, and that is why `ReviewQueueItem.stack` came
back after being removed as "an implementation of contiguity": a guide character
is a statement about what comes **after** a row — `└─` means nothing else hangs
off my parent below me — and only the client is holding the list. A client
inferring stack membership from runs of `depth` would be re-deriving the grouping
the daemon already did.

The root draws nothing: it is the trunk, and a guide in front of it points at a
parent that is not on screen. A lone pull request has no `stack` at all, which is
what stops a `└─` appearing in front of every unstacked row.

#### A thread says which pull request it is about

It was readable without saying it: a review workspace is `pr-<n>`, so the number
could be parsed back out of a thread member. That holds until any of the
ordinary things happen — a workspace renamed, a PR opened for work that already
had a thread, a review done in a checkout somebody made by hand — and each of
those is a thread whose pull request awp cannot name.

So it is recorded, for the same reason `parentId` is: a name is an address, and
this is a claim about the work. `thread_prs` with **UNIQUE (project, number)** —
one thread per pull request, the same rule a workspace's single claim has, and
for the same reason: two threads about one PR has no rendering, because the
review queue row would have to pick which to point at.

**Several per thread, though.** A thread already holds several workspaces in
several repositories, and each has its own pull request — a frontend change and
the api behind it is one piece of work and two PRs. A stack in one repository is
the same case.

Three writers, and each is a different moment:

```
  ReviewStart   links at creation, so the row and the sidebar can name the PR
                now rather than in the half minute the job takes
  restore()     puts the link back with the thread — the one place a
                rolled-back thread is rebuilt, so the link belongs in it
  ThreadLinkPr  a person saying so, for the cases above that no name encodes
```

The review queue join reads the link **after** the name-based recovery, so the link
wins. The name path stays because this machine is full of workspaces that
predate the field — including the Go implementation's `pr-<n>-<branch>` — and a
row that could not find its thread would offer to build a second one.

No chip for it in the sidebar, deliberately: a review thread's title already
begins `#2418`, and the workspace row already shows `facts.pr`. A third copy of
the same number is duplication, not information. The link exists to be the
record the review queue joins on.

#### `gh -R` is not `jj -R`

jj's takes a path. gh's takes `OWNER/REPO` and refuses a directory outright:

```
  gh pr list -R /Users/…/thicket
  expected the "[HOST/]OWNER/REPO" format, got "/Users/…/thicket"
```

So every call in `github-cli.ts` names its repository by **running in it** —
`ChildProcess.make(…, { cwd })` — and `gh` resolves owner and name off the
remote. The Go implementation did the same thing, its runner taking a directory.

Two consequences found by `bun run probe:review-queue`, which is what a fake could
never have said:

- **A secondary jj workspace is not a git repository.** `gh` needs one, and
  `~/.awp/workspaces/<project>/<workspace>` has no `.git` — so the repository
  handed to gh has to be the _source_ root, which is what `Jj.sourceRoot` and
  the `Project.root` record already hold. Pointing this at a workspace answers
  `fatal: not a git repository`.
- **`gh pr list` with `statusCheckRollup` is seconds, not milliseconds.**
  Measured 4.5s for eleven pull requests on a repository with real CI. That is
  the whole reason `ReviewQueueFeed` has a cache with a lifetime rather than a refresh
  button alone: the panel is mounted every time its tab is opened.

#### One field kills the query, and it is not the slow one

A repository with a hundred open pull requests could not be listed at all: six
seconds, then `GraphQL: Something went wrong while executing your query`. It
read as a slow cache; it was a failing project being retried on every read,
because a failure is deliberately never cached.

Bisected against the real repository:

```
  the whole field set (18)        GraphQL: Something went wrong    ✗
  without `reviews`              GraphQL: Something went wrong    ✗
  without `mergeStateStatus`     12 rows in 4.6s                  ✓
```

`mergeStateStatus` makes GitHub compute mergeability for **every** pull request
in the answer, and past some size that exceeds their own time limit. The field is
not slow, it is _fatal_ — which is the opposite of how one reasons about
expensive fields, and the reason to write the measurement down.

So the listing asks for everything and asks again without that field when
refused. What it costs is `conflicts` and `behind base` being unknown there, and
`ReviewQueueSource.degraded` says so in a sentence — muted rather than red, because
nothing is broken. **Silence was the alternative and is worse:** a clean-looking
clean-looking queue for the one repository where nothing is _able_ to report a conflict.

**The sentence named the ceiling, not a count.** It read "for 100 pull requests
here", where 100 is `LIMIT` — the number the query _asks_ for — so the one
concrete thing in it was the one part that was not a fact about the repository,
and it said 100 for a repository with twelve. It is composed after the cheap
listing now, which is the first point at which there is anything true to say:
the query that failed returned nothing to count. At the ceiling it says "100 or
more" rather than claiming a total it cannot know.

**And a repository that refuses once refuses every time.** The refusal is a
function of how many open pull requests there are, which nobody changes between
two refreshes — so asking anyway spends a multi-second _failing_ query per
refresh to learn what is already known. `refused` remembers it for six hours,
in memory rather than in the store: it is a reading about GitHub's patience
rather than a fact about the work, and a daemon restart asking once more is the
cheapest way to notice it has changed. Written on the way down and **cleared on
the way back up**, or the memory outlives the thing it remembers.

#### Pressing a row has to change the row

The click started a job and the row said nothing for half a minute, which is
indistinguishable from a button that does not work — and pressing it again is
the natural response. The state it was reading was "does a thread hold
`pr-<n>`", and the claim is the create job's **second-to-last** step.

```
  press ──▶ ReviewStart ──▶ fetch · workspace · bookmark · trust · session ·
            (a gh call)     bootstrap ──▶ claim ──▶ brief
            ↑ nothing                     ↑ the row's only signal, 30s later
```

Four states now, each a different thing to do next, and the sources are three
records the daemon already holds:

```
  starting…      local, between the press and the reply — a gh call, not instant
  <step> N/M     the job. WHICH step, because fetch and bootstrap wait on very
                 different things
  failed         the job stopped, with its sentence on the hover. Only when
                 there is no workspace: a hook that failed after building one
                 leaves something worth opening
  open           a workspace exists
```

**Openable when the SESSION exists, not when the claim lands.** The annotation
reads the session listing as well as the threads, which moves the row's "open"
a step earlier — into the window a person is watching. The thread is still
reported separately, because it is what says the job finished.

**The job's id crosses the wire, not the record.** A job changes on its own and
the window already has a live feed of every one; sending the record would put a
second, staler copy on a list that is a snapshot, and the two would disagree
exactly while somebody watched a row progress. The id is the join, `JobChanges`
is the truth — and the panel is handed the jobs the window already streams
rather than subscribing again, because an rpc stream is a request and a second
listener is a second feed.

**No spinner.** The jobs panel's rule, and it holds harder in a list somebody
leaves open all day: the word already says it is running.

**A key is composed in one place and parsed in the same file.** `reviewKey` and
`reviewOf` sit together for the reason `reviewWorkspace` and `reviewNumber` do —
a format written in one file and read in another drifts by a colon. Matching a
job by its key is one string comparison per job, where reading its stored input
would be a schema decode per job on every listing.

**Minting a name and recognising one are different rules.** `reviewWorkspace`
mints `pr-<number>` and nothing else, because a branch in the name is an
identity that goes stale on a force-push. `reviewNumber` has to be wider, and
this machine is the reason: every review workspace made before amoeba carries
the Go implementation's `pr-<number>-<branch>`.

```
  awp.thicket.pr-2340-header-allowlist-6fb6.agent   ← eight of these on this
  awp.orchard.pr-558-typed-router-ide-bfad.agent      machine, all reviews
```

A reader matching only the new shape reports every one of those pull requests as
unreviewed, and the row then offers to build a _second_ workspace beside the one
already there. Found by reading a real session list, not by a test.

#### Repair is a prompt, not an act

The first version of this moved the checkout: fetch, then `jj new <head>`. It
worked, and it was the wrong feature under the right name — the deck's `C r`
composes a **sentence** describing what is wrong with the pull request and hands
it to a form, and what a person expects from a button called repair is that.
The checkout-mover was removed rather than kept beside it: two things called
repair is how the confusion gets built in.

`repair.ts` is the deck's own logic, ported, and nearly every line of it is a
decision somebody got wrong first.

**Tone follows ownership.** On your own pull request the agent is asked to _fix_
— resolve the conflict, push the branch. On somebody else's it is asked to
_look_: investigate and report, change no files, push nothing. Reviewing a
stranger's PR should not have an agent start rebasing their branch.

**An issue with no reviewer's angle is dropped, not translated.** The archive
records the failure: a reviewer was asked to report how far behind its base
someone else's branch was, which is the author's rebase and nothing a reviewer
can act on. So a missing `look` _means_ "not a reviewer's problem", and a new
issue has to decide that on purpose rather than inherit a plausible-sounding
review action.

**Review feedback gates the whole prompt.** When one of the issues is a
reviewer's comments, the agent is told to propose the problem and its fix for
each point and wait — because an agent told to fix CI _and_ answer a reviewer in
one message should not do half of it unprompted.

**Approving and still wanting something are not exclusive.** An approved PR with
comments used to answer "nothing to repair", which is the tool deciding on the
user's behalf that a reviewer's remarks were settled. It now asks which points
are still open at the current head.

**A local read beats `gh pr diff`.** The review-tone prompt tells the agent to
fetch and park the working copy on the head, because that lets it open files at
the right revision, chase context and run tests — where a raw patch allows none
of it. `gh pr diff` stays as the fork fallback.

**And it is offered, not sent.** `PullRequestRepair` returns text; `AgentSend`
delivers whatever is in the box afterwards. On your own pull request that text
tells an agent to push, so the person whose branch it is reads it first and edits
it if they want something else. That is also why the send is a _general_ call
rather than one that re-composes: what should arrive is what was in the box.

Measured against real pull requests, which is the only way to see whether the
sentences read as English:

```
  #545  theirs → look   conflicts + a pending request for your review
                        "Do NOT modify files, run jj/git mutations, or push"
  #2364 theirs → look   one issue, one sentence, with the local-read recipe
```

#### The icons are Phosphor, and the baseline row has none

`@phosphor-icons/react`, deep-imported per icon — `@phosphor-icons/react/XCircle`
— which is what the rest of the window already does. Not a glyph font: the deck
used Nerd Font codepoints in the Private Use Area, which is right for a terminal
and is tofu here, because this window ships Inter and JetBrains Mono and nothing
else.

**One icon leads the row, chosen by priority, and the ordinary state has none.**
That is the deck's rule kept rather than a space saving: an open pull request
with green CI and nobody waiting is most rows, and painting it teaches the eye
to skim the icon column — which costs the one row that deviates. The slot keeps
its width regardless, so the titles still line up.

```
  ✗ ci red             go and look now
  ⧗ ci running         nothing to do yet
  ● changes requested  somebody wants work from you
  ◌ asked again        you reviewed it, and the author came back
  ○ review requested   a first request
  ✓ approved           one press from done
  ▤ draft              not submitted, so its CI is information
  (nothing)            open, green, nobody waiting
```

Two chat bubbles rather than one for the review states, which is also the deck's
choice: a conversation is what a review is, where a tick or a flag reads as a
verdict. Hollow is "somebody is asking", dotted is "asking again".

What the lead cannot also say goes on the second line, small and after the
branch — conflicts, behind, notes on your own PR, an ancestor that cannot merge.
A pull request is regularly two things at once and one icon cannot be both.

**`title` goes on the wrapping span, never on the icon.** Phosphor renders an
`<svg>`, and a `title` _attribute_ on an SVG element is not a tooltip — SVG
wants a `<title>` child, which the component does not take. Every icon is
`aria-hidden` and the words are said once in the row's own `title`, because an
icon that announces itself in the middle of a title makes the title unreadable.

#### The PR tab, and markdown

A workspace whose thread names a pull request gets one more panel, first in the
strip and labelled `PR #2418`. First because a review workspace exists _because_
of a pull request — while one is open the PR is the subject and the diff is a way
of reading it. Absent entirely otherwise, rather than present and empty: this is
the column somebody switches most, and a permanent empty room in it costs a
keystroke every time.

Which PR is derived in the window from the thread record it already holds — no
call, because a call would be a second copy of something on screen. The panel's
own content is cached like the listing, for a reason particular to the strip:
**Base UI unmounts a hidden tab**, so switching to the diff and back remounts
this panel, and without a cache every switch would be a `gh pr view`.

**Markdown is a library, and that is a deliberate exception to "do not add a
fourth thing".** The stack rule is about UI frameworks; this is a content
renderer, like `@pierre/diffs` and the icon set. It was preformatted text first,
which in practice showed `## Summary` and `- [ ] done` as literal characters —
most of a PR body. `react-markdown` rather than `marked`, and the reason is the
content's provenance: `marked` returns a string of HTML that has to go through
`dangerouslySetInnerHTML`, and this text was written by whoever opened the pull
request. That needs a sanitiser beside it — two dependencies and a rule to get
right — against one that builds React elements and never produces HTML.
`remark-gfm` because a task list is what half of all PR descriptions are.

Two details in `Markdown.tsx` worth not rediscovering: the components map is at
**module scope**, because rebuilding it per render makes every element type a new
component identity and remounts the whole body — losing the scroll position of a
code block somebody is reading; and an image is rendered as its alt text, because
a screenshot in a PR body lives on GitHub's user-content host, which this window
has no session for, so an `<img>` would be a broken icon where a caption will do.

#### The pull request cache, and the four things wrong with the first one

`gh pr list` with `statusCheckRollup` is seconds, and the review queue is asked every
time its tab is opened — so there is a cache. What that cache went through is
worth keeping, because three of the four faults were invisible and one killed
the daemon.

```
  cold, nothing anywhere                    11.5s
  warm disk, daemon just restarted           0.40s
  warm memory                                0.28s
```

**On disk, not only in memory.** It was a `Ref<Map>`, which a restart empties —
and this repository is worked on by restarting the daemon. `pr_lists`,
`pr_details` and `gh_viewer` in `awp.sqlite`, payloads as JSON in a text column
because what is stored is _this daemon's projection_ of gh's answer: a column
per field would make every change to `github-parse.ts` a migration. A row that
will not parse counts as a miss, which is the honest reading of "written by a
version that is no longer here".

**Two lifetimes that fought each other.** The first version used a two-minute
memory TTL to decide re-fetching and a one-hour disk TTL to decide whether a
stored row was worth loading. Those disagree by construction: a row read off
disk carries the moment it was _fetched_, so a twenty-minute-old row is loaded
and instantly judged stale, and the read pays the full `gh` call anyway.

```
  warm disk, first read     2.6s
  warm disk, second read    7.9s   ← re-fetched everything, every time
```

They now mean different things. `DISK_TTL_MS` answers "is there anything worth
saying" — an hour-old queue with `read at 09:14` under it beats a spinner —
and `TTL_MS` answers "is it worth re-reading", **behind** the answer rather than
in front of it: `Effect.forkDetach`, guarded by a set of in-flight repositories
so three tab switches are not three `gh` calls. `refresh` stays synchronous,
because somebody pressing a button is asking to wait.

**A cache that was never hit, and said nothing.** The viewer row was read
through the same `stored` helper the others use — which parses a column called
`payload`, where that table keeps its teams in a column called `teams`. Every
read threw on `JSON.parse("undefined")`, missed, and asked `gh` again. It cost
exactly the 1.7 seconds it had been added to remove, and the only tell was a
number that would not come down. **A cache with no hit counter is a cache you
cannot tell is broken** — the measurement above is the counter.

**And the daemon would not start.** The `gh_viewer` table was first added as a
third statement inside `inbox.001-cache`, which had already run. The name was
recorded, the statement never executed, and:

```
  ERROR: SQLiteError: no such table: gh_viewer
    at <anonymous> (packages/server/src/review-queue-feed.ts:217:25)
```

Which is this file's own rule — a migration's name is fixed the moment it has
run anywhere — and the loud failure is what `create table` rather than
`create table if not exists` buys.

#### The review queue is a list of pull requests, not of workspaces

The deck's inbox scope was built out of **workspace** rows, and a pull request
with no local checkout had to be invented as a "virtual" row. That took three
passes — review-requested, then your own, then a fourth to fill the holes a
partly-shown stack left — each with its own dedup table against the ones before
it.

```
  deck    workspaces, plus synthesized PRs      3 synthesis passes, 3 dedups
  here    pull requests, plus a workspace       0
          annotation on the ones that have one
```

Nothing here is cleverer; it starts from the set GitHub returns. A stack's
middle link is frequently somebody else's PR, which is _why_ the deck needed the
third pass: its rows could not represent one. With the PRs as the rows, the
base/head graph is already in hand.

**A row's section is the whole stack's, not its own.** The first version here
computed it from a row's own ancestor chain, which reads as correct and splits
every stack whose tip is what makes it your problem:

```
  #20 tip     needs your review    ← the request names you
  #10 base    other open PRs       ← somebody else's, so it sorted away
              and the chain drew broken, under two headings
```

`review-queue.test.ts`'s "a stack stays together" is the test that caught it.

**The daemon classifies, sections and orders.** Same argument as
`SessionIdentity` being on the wire: `bucketOf`'s precedence is subtle enough
that the archive locked it with tests, and a client re-deriving it is a second
implementation. The one clause worth knowing without reading it: a review
request wins over everything the PR itself says, including its CI being red.

**The merge queue is deliberately not read.** The archive treated "queued" as
ready-to-merge, and that signal exists only in GraphQL — `gh pr list --json`
does not expose it — so it cost a second query per repository per refresh for a
state that lasts minutes. A queued PR that is approved and green already reads
as ready; one that is neither lands in "Mine", which is a row under the wrong
heading rather than a row nobody can find.

**A repository with no GitHub remote is not a failure, and must not be asked.**
Reported from a real window, and it is the shape of complaint that trains a
person to stop reading warnings:

```
  orchard: no git remotes found
  Notes Vault: no git remotes found
  harbor-works: none of the git remotes configured for this repository point
                to a known GitHub host
```

Every sentence is true and none is actionable — a vault of notes and a scratch
repository are working exactly as intended and have no pull requests to have.
Worse, `gh` can only report the condition as an error, so the panel had a
permanent red row per repository, which costs the one project whose token really
has expired.

So it is decided **before** `gh` is asked, and locally: `git remote -v`, whose
hosts are matched against the ones `gh` itself knows — github.com plus the
top-level keys of `~/.config/gh/hosts.yml`, which is where `gh auth login`
records an enterprise host. A repository that matches nothing is left out of
`sources` entirely, so nothing is said about it at all.

Two details worth keeping. Hosts are compared **exactly**, never by suffix:
`github.com.evil.example` ends with the right string. And a directory that is
not a git repository counts as off GitHub rather than as an error, which is what
a jj workspace with no colocated git is.

**A failure is per project.** One repository's `gh` being unauthenticated, or
its remote not being GitHub at all, must not cost the others their rows — so
`ReviewQueueSource` carries a sentence per project and the call has no error channel
at all. The one global failure is the login, and it is not fatal either: what it
costs is every viewer-relative bucket, which is why `ReviewQueue.viewer` is on the
answer. A review queue that is empty because nobody is signed in looks exactly like an
review queue with nothing in it.

## Gadgets: what was measured before any of it was written

The rules are in `packages/server/AGENTS.md`. This is what they cost.

### MDX, and what its compiler actually emits

Compiled with `outputFormat: "function-body"`, a document comes back as a
fragment that begins:

```
"use strict";
const {Fragment: _Fragment, jsx: _jsx, jsxs: _jsxs} = arguments[0];
…
return { Counter, default: MDXContent };
```

Three facts fell out of reading that rather than the README:

- **`arguments[0]` is the runtime**, which is what makes the window's parameter
  list the scope and fixes the runtime as its first parameter. Named parameters
  and `arguments[0]` coexist — the first parameter _is_ `arguments[0]` — so a
  document can be handed `React` and the tokens by name without the compiler
  knowing anything about them.
- **`export function` at the top level of the document becomes a local
  declaration** and a key in the returned object. That is the whole of "the
  agent inlines its own components": a gadget defines what it uses, and awp
  publishes no registry.
- **`import` is not refused, it is deferred.** It compiles to
  `await import(_resolveDynamicMdxSpecifier('y'))` preceded by
  `if (!_importMetaUrl) throw new Error("Unexpected missing \`options.baseUrl\`…")`— a throw in the *renderer*, naming an option the document's author never
chose. Hence`noImports`, which reads the estree of each `mdxjsEsm` node and
  refuses at the top, where the agent is still listening.

### Why the address is a third scheme rather than `app://`

`pageAddress` admits two schemes because the panel is a real `WebContentsView`
with a preload in it. `app://` is a registered standard scheme with a real
origin — which is exactly the problem: `app://renderer/index.html` is this
application, so admitting the scheme admits a navigation call that points that
view at the window's own origin. A `gadget:` address cannot be that, because no
code path hands it to a webview at all; `Web.tsx` branches before the navigation
and draws it instead. The guard's meaning is unchanged: two schemes to browse,
and one that is not browsing.

### What was deliberately not built

- **No durability.** Gadgets are a `Ref<Map>`; a restart forgets them. The cost
  is a sentence in the panel rather than an empty column, and a table can follow
  the first gadget somebody misses.
- **No list, no strip, no titles.** A thread may hold many named gadgets and the
  column shows the one its page names. A strip of chips to switch between them
  is the obvious next thing and is the reason `GadgetHead` was considered and
  dropped: a title field nothing renders is a field that goes stale.
- **No app-native scope.** `gadgetScope` is `React` and the three token groups.
  Open this diff, this thread's status, a live value from the daemon — all of it
  is guesswork until a gadget reaches for something and cannot have it.

## Two agents on one conversation: what happened, and what now refuses it

The rule is in `packages/server/AGENTS.md`. This is the incident and the
measurements behind it.

### What was observed

On 2026-09-17, between 09:53:05 and 09:55:30, two `claude` processes wrote into
one session's transcript and one working copy. Both were implementing the same
task; they produced two contracts for it, in the same file, minutes apart. The
evidence was three-way and worth recording because none of it is obvious:

- **The transcript interleaves.** `tool_use` entries a second apart, in one
  `.jsonl`, doing different halves of one job. A transcript carries no process
  id, so the split between the two streams is by content — that is the limit of
  what can be reconstructed afterwards.
- **A jj snapshot bounds it.** At the `jj new` operation the contested file was
  3585 lines with none of the second stream's symbols in it; 97 seconds later
  it was 3918 lines and held both designs. `jj op log` plus
  `jj --at-op=<id> file show` dates a working-copy change to the second without
  anybody having committed anything.
- **No second session exists.** Grepping every transcript under
  `~/.claude/projects` for the second stream's symbols finds them in exactly
  one file — the same session — and as _tool calls_, not tool results.

### Why it was possible

```
  chat_sessions(project, workspace) → session_id     shared sqlite, every daemon
  RcMap key      project\nworkspace                  one process's memory
  claude --resume=<session_id>                       nothing claimed it
```

`chat.ts` already refuses to _adopt_ a session from `session/list`, with a
comment naming this exact failure — "a second writer on a transcript an
interactive agent is still appending to, and neither process knows about the
other". The door it left open is the stored pointer: when the remembered
session is one something else currently holds, `session/load` proceeded.

Three ways in, all ordinary here: the two-instance dev workflow (5274 and 5284
share the store), a session running as a harness background agent, and
`claude --resume` typed by hand.

### What the guard costs, and what it deliberately does not do

- **A `ps` scan, fail-open.** `ps -Ao pid=,command=` is parsed by a pure
  function, and every non-answer is an empty list: the table is the guard that
  has to be right, and a `ps` that is missing or shaped differently must not be
  able to stop a conversation opening. `--resume=<id>` and not the bare id,
  because the id appears in transcript paths and in the argv of anything
  grepping for it — including the diagnosis.
- **Liveness before staleness.** `process.kill(pid, 0)` answers immediately for
  a daemon that was killed, so a restart never waits out `STALE_AFTER`. The
  heartbeat covers only what liveness cannot: a pid that exists but is not that
  conversation any more.
- **Nothing here touches sockets.** The word heartbeat is doing different work
  in this file than in a feed that drops. A claim beat says "this process still
  holds this conversation"; it does not keep a connection alive, notice a
  client leaving, or bear on the window's feeds reconnecting.

### The refusal has to be on screen

Found while wiring it: `subscribe` in the renderer retries a feed forever and
then catches the cause, which is right for an outage and wrong for a refusal —
the sentence would have been retried every half second and then thrown away,
leaving a chat that spins with nothing saying why. `watchChat` now catches
`ChatUnavailable` inside the retry, as `Attach` already did for `AttachRefused`,
and `Chat.tsx` prints it in `colors.warn` beside the turn-ended line.

`bun run probe:claim` is the cross-process check: a second process against one
store, printing what it was told. Unit tests drive one connection, which proves
the logic and not the property.
