# packages/server

The daemon: tasks, the review queue, chat and steering, the MCP surface, pages.
Loaded when working under `packages/server/`; repo-wide rules are in the root
`AGENTS.md`.

**Only what the code cannot show is here.** Schemas, tool lists and handler
tables were cut — read `tasks.ts`, `mcp.ts`, `review-queue.ts`, `chat.ts`,
`pages.ts`. The evidence and the long findings are in `docs/daemon.md`, which no
session loads.

## Tasks: a store awp owns, filled from files nothing here writes

One table in `awp.sqlite`, tagged, readable from anywhere — a task list used to
belong to a **session**, found on disk by mtime and unable to outlive it.

**Nothing here is the first writer, and the panel is read-only on purpose.** The
writer is _ingest_: whatever a source already wrote, copied in.

**A tag, not a scope column**, and deliberately **not a foreign key** —
`thread:<id>` outlives the thread being archived, the same argument as recording
`parentId` rather than deriving it. A tag pointing at a gone thread is a claim
about history.

**`unique (source, source_key)` buys two things:** sqlite treats NULLs as
distinct in a UNIQUE, so the index that makes ingest idempotent puts no
constraint on a task with no source key. `source_seq` is beside it because a
source that counts its tasks writes `10` after `2`, and text order reverses them.
`status` is text with **no `check`** — an upstream addition must not become a
daemon that will not start.

### Ingest takes the whole set, because a finished task is an absence

A task finishes by its entry **leaving** `TODO.md`, and there is no record whose
absence a per-task write could notice.

**Scoped by a key prefix**, or reading one project's file deletes every other
project's rows. The prefixes come from the project list, so a project not on that
list is a prefix **nothing will ever name again**, frozen at whatever the last
sweep said — measured as a board reporting 51 tasks against a file holding 42,
for nine days. That is why **`ProjectForget` releases its tasks**, and why
`ingest(source, prefix, [])` runs even when the project was not on the list: the
one repair available for rows already stranded. Safe, because `taskId` is derived
from source and key rather than minted.

**A project's root is its default workspace, and that is the wrong file.**
`TODO.md` is a working-copy file, so the root reads whatever revision that one
checkout is parked on — frequently a stale one with no file at all. Every
candidate is offered and the **newest by mtime** wins; taking all would store one
list at several revisions with nothing able to say which is true. Candidates come
from the directory convention rather than `jj workspace list` — a subprocess per
project per sweep for an answer `readdir` has. **The key carries the workspace**
for the `claude` source, because two checkouts each have an agent numbering its
tasks from one.

### The read answers from the store and sweeps behind it

The panel is mounted on every glance (Base UI unmounts a hidden tab) and must not
cost a disk sweep per glance — and a question that writes is what
`--ignore-working-copy` exists to prevent. `Effect.forkDetach`, not `fork`,
because the fiber must outlive the request. **So a cold first read is
legitimately empty**, and looks exactly like a project with no `TODO.md`;
`probe:tasks` reads twice for that reason alone.

### Subscribing is what makes the sweep run

The sources are files nothing here writes, so there is no event to hang a refresh
on: nobody watching costs no timer at all, one watcher starts a sweep every 10s
serving every client.

**The turn edge is the one trigger that fires because of the thing that changed
the file.** `settled()` is that edge, and the case an equality check would lose
is a workspace that was `working` and is now **absent** — a conversation released
mid-turn has settled too.

**A write is not a sweep, so it has to say so itself.** The sweep counts what
ingest moved, so `awp_task_add`, a dot pressed in another window, or a project
forgotten moves nothing it can count, and a panel that only re-reads on a push
never learns. The handlers call `TaskFeed.wrote` **after** the store has
answered, so a refusal publishes nothing.

`TaskChanges` carries **counts, not rows**: the daemon does not know which tags a
client is narrowing by.

### A copied row's dot is a mark; an awp row's is a control

```
  awp      swept by nothing, so no later reading can decide it finished  ← moves
  todo     ingest's upsert writes the source's status back over anything
  claude   set here, so a task marked done is pending again within 10s  ← a mark
```

A control that lies is worse than no control. The refusal exists one layer down
as `TaskNotOurs`, republished as `TaskRefused`, whose sentence names where the
task is actually written.

**A tag is the exception, and it needed a column.** `task_tags.applied` separates
what a person applied from what a source implies, because ingest replaces a
task's tags wholesale — without it, a `thread:<id>` on a `TODO.md` task is a
write silently undone by the next sweep.

**`TaskBoard`, not `TaskList` — the name was taken.** `TaskList` reads a
_session's_ own list, keyed by a directory.

## The MCP surface

Two readers and three writers, in `mcp.ts`. **One tool answering both readers
would put 46 tasks' worth of argument into a context window to answer "what is
already written down".**

**An id is the one argument that could name another checkout, so it is checked** —
`ownTask` refuses anything not on the project's own board. Every other tool here
is bound by the _absence_ of an argument. **`includeDone` drops the status filter
rather than inverting it**, because a negative filter would include a status
never seen here.

### The agent's own face on the daemon

`mcp.ts` is an MCP server the agent connects to, over the same handlers the
window uses. Three decisions that are hard to change later:

- **stdio, one server per agent.** The alternative — one HTTP server with the
  workspace as an argument — makes the binding below _conventional_ rather than
  structural: anything reaching the port could name any workspace.
- **The scope is the working directory, and the binding is the absence of a
  parameter.** `mcp.test.ts` asserts it on the tool schemas rather than the
  dispatch. The Go implementation is the argument: an agent that ran the filing
  command in the _source_ repository filed seven findings there, and both sides
  reported success. `NotAWorkspace` exists so that arrives as a sentence naming
  the directory — **the sentence is the interface**, because what reads it is a
  model.
- **No MCP SDK.** Line-delimited JSON-RPC 2.0, byte for byte what `acp.ts`
  already speaks.

```
  tools/call, no such tool     { isError: true }   the MODEL chose the name
  tools/call, daemon refused   { isError: true }   the model has to read why
  an unimplemented METHOD      -32601              the CLIENT asked
```

A refusal sent as a JSON-RPC error is hidden by most clients. **A notification is
answered with nothing at all** — a reply carrying a null id is a protocol error
at the other end, and every client sends `notifications/initialized`.

**Prose, not JSON, because a model reads it.** The other checkouts'
**directories** are the reason to call `awp_thread`, and a pair is not something
an agent can act on, so `ThreadCheckout` carries `dir`.

**The server is handed to the conversation, not written to a file** — in
`mcpServers` on every `session/new`, `load` **and** `fork`, because a loaded
conversation that came back without its tools reads as an agent that forgot how
to use them. `AWP_DAEMON_URL` travels with it, so a branch daemon's agents reach
the branch daemon.

## The review queue is a list of pull requests, not of workspaces

The deck built rows out of **workspaces** and had to synthesize rows for a PR with
no checkout — three synthesis passes, three dedup tables. Starting from the set
GitHub returns needs none of them: a stack's middle link is frequently somebody
else's PR, which is _why_ the deck needed a third pass.

**A row's section is the whole stack's, not its own.** Computed per row, a stack
whose tip is what makes it your problem splits across two headings and draws
broken.

**The daemon classifies, sections and orders.** The one clause worth knowing: a
review request wins over everything the PR itself says, including red CI. **The
merge queue is deliberately not read** — GraphQL-only, a second query per repo per
refresh, for a state that lasts minutes.

**A repository with no GitHub remote is not a failure, and must not be asked.**
`gh` can only report it as an error, so a vault of notes earned a permanent red
row. Decided **before** `gh` is asked, locally, against the hosts `gh` knows —
compared **exactly, never by suffix**, because `github.com.evil.example` ends with
the right string. A non-repository counts as off GitHub, not as an error.

**A failure is per project**, and the call has no error channel. The one global
failure is the login, which costs every viewer-relative bucket, so
`ReviewQueue.viewer` is on the answer: a queue empty because nobody is signed in
looks exactly like an empty queue.

**`gh -R` is not `jj -R`.** jj's takes a path; gh's takes `OWNER/REPO` and refuses
a directory, so every call names its repository by **running in it** — and a
secondary jj workspace is not a git repository, so the directory handed to gh must
be the _source_ root.

### One field kills the query, and it is not the slow one

A repo with a hundred open PRs could not be listed at all. `mergeStateStatus`
makes GitHub compute mergeability for **every** PR in the answer: the field is not
slow, it is _fatal_ — the opposite of how one reasons about expensive fields. So
the listing asks for everything and asks again without it when refused;
`conflicts` and `behind base` are then unknown, and `degraded` says so in a
sentence, muted rather than red. Silence is worse: a clean-looking queue for the
one repository unable to report a conflict. The sentence is composed **after** the
cheap listing — the first point at which there is anything true to say.

**A repository that refuses once refuses every time**, so `refused` remembers it
for six hours, in memory rather than the store — a reading about GitHub's
patience, not a fact about the work. Written on the way down and **cleared on the
way back up**.

### The cache

`gh pr list` with `statusCheckRollup` is **seconds, not milliseconds** (4.5s for
eleven PRs), which is why there is a cache with a lifetime rather than a refresh
button alone. Cold 11.5s, warm disk 0.40s, warm memory 0.28s.

- **On disk, not only in memory** — this repo is worked on by restarting the
  daemon. Payloads as JSON in a text column, because what is stored is _this
  daemon's projection_: a column per field makes every parser change a migration.
  A row that will not parse counts as a miss.
- **Two lifetimes that mean different things.** `DISK_TTL_MS` answers "is there
  anything worth saying" — an hour-old queue with `read at 09:14` beats a
  spinner. `TTL_MS` answers "is it worth re-reading", **behind** the answer.
  `refresh` stays synchronous: somebody pressing a button is asking to wait.
- **A cache with no hit counter is a cache you cannot tell is broken.** The viewer
  row was parsed from a column it does not have, missed every time, and the only
  tell was a number that would not come down.
- **A migration's name is fixed the moment it has run** — adding a table as a
  third statement inside an applied migration is a daemon that will not start.

### A pull request moves, and the checkout does not

A review workspace is a checkout of the head when it was made; the author pushes,
and from then the diff being read and the findings being written are about code
the PR no longer has. Nothing on screen changes — worse than out of date, because
the review reads as current.

**Asked as "is the head an ancestor of `@`", not "is it equal to `@`"** — somebody
with a commit of their own on top is still reviewing the right code. **`present()`
is what makes it one jj call rather than three**: without it an absent commit is
an error, which is exactly what a force-push leaves.

### Repair is a prompt, not an act

The first version moved the checkout. It worked, and was the wrong feature under
the right name: what a person expects from "repair" is a **sentence** describing
what is wrong, handed to a form.

- **Tone follows ownership.** Your own PR: _fix_. Somebody else's: _look_ —
  investigate and report, change no files, push nothing. Reviewing a stranger's PR
  should not start rebasing their branch.
- **An issue with no reviewer's angle is dropped, not translated.** The archive
  once asked a reviewer to report how far behind its base somebody else's branch
  was, which is the author's rebase.
- **Review feedback gates the whole prompt** — an agent told to fix CI _and_
  answer a reviewer should not do half of it unprompted.
- **A local read beats `gh pr diff`**: fetch and park the working copy on the
  head, so the agent can open files at the right revision and run tests.
- **It is offered, not sent.** `PullRequestRepair` returns text; `AgentSend`
  delivers whatever is in the box afterwards.

### A review is the same job, with one step turned on

`create-workspace` with `review` on its input, which switches `fetch` from a
no-op. The name is pre-set to `pr-<n>` so `name` skips the model, and there is no
bookmark — `pr-123` is not a branch anybody should push.

**The base is patched by the step, not decided by the handler** — a PR's head is a
branch name, not a revision until something has fetched it, and which revset it
becomes depends on what the fetch produced:

```
  from origin    feature@origin   jj does not track a fetched branch locally
  from a fork    feature          git wrote refs/heads, so jj imports it local
```

The remote one wins when both exist: a local bookmark of the same name may be
behind. **`jj git import` after a fork fetch**, or jj cannot see the ref — and
nothing about the symptom points at it.

**The name is `pr-<n>` and the branch is deliberately not in it** — a branch can
be renamed or force-pushed while the PR stays the same one, and this name is what
every idempotence check is. **Idempotent by two records, because one is not
enough:** a thread holding `pr-<n>` (the review finished, its job record may be
cleared) and the job's idempotency key (still being built). A third case is a
race: two presses in one second get the same job back, so the handler removes the
thread it just made if it lost.

**Minting a name and recognising one are different rules.** `reviewNumber` must
also read the Go implementation's `pr-<n>-<branch>`, or every older review reports
as unreviewed and the row offers to build a second workspace beside the one
already there.

**A thread says which pull request it is about**, recorded rather than parsed back
out of the name — a rename, a PR opened for work that already had a thread, or a
review done by hand all break the parse. **UNIQUE (project, number)**, because two
threads about one PR has no rendering; **several per thread** is allowed, because
a frontend change and the api behind it is one piece of work and two PRs. The join
reads the link **after** the name-based recovery, so the link wins.

## Chat: two clients, and what neither of them could see

`apps/tui` opens the same `ChatOpen` the window does — the numbered subscriber
queues were already a fan-out, so no daemon change was needed to read one. What
was missing was both about _writing_: no adapter echoes a user chunk on a live
turn, and nothing in ACP says a question was answered. The daemon says both,
because it is the only process that knows.

**The key is the client's, and that is the half that matters.** The sender paints
its copy on the keypress, so the echo names a row already on screen. It is a
**uuid** — two clients with a counter each would both mint `mine-1`. **The option
id, not its name**, so each client says it in its own words.

## A steer is not a reply, and two turns overlap

Measured against a real adapter, interrupting twelve seconds in: the first turn
ended at 20.7s while the second, started at 12s, was still running.

- **`running` is a count, not a flag.** A boolean cleared on the first `ended`
  says the agent has finished while it is still answering.
- **Nothing echoes a steer back**, so the window's own copy is the only record
  until `session/load` replays it.
- **A queued message floats at the tail**, and everything the agent is still
  producing is inserted above it — appending put a steer above the rest of a reply
  still arriving, so two turns read as an answer before its question.

### Steering is an interrupt, so it is not what Return does

Every message went through `_session/steering`, so typing while an agent worked
**destroyed the answer in flight**. The adapter says what it is, in capitals:
pre-empting means ABORTING. The CLI settles the default and it is not ours — `now`
jumps the queue _and_ aborts; an ordinary user message is built at `next`, and an
absent priority reads as `next`.

```
  return        session/prompt        the agent finishes, then reads this
  cmd+return    _session/steering     the agent stops where it is
```

Waiting is not a fallback for adapters that cannot steer; it is what a human
message does. `ChatSend.interrupt` is **optional, so absent means the safe one**,
and every daemon-side sender passes `false` explicitly. `_session/steering` is
advertised as `_meta.steering.supported` and is read rather than assumed.
**`idleBehavior: "promptRequired"` is why this is one call and not two** — a steer
with no turn running would otherwise make the adapter start one, detached, with no
turn edges from this process. It also means **no "is a turn running" state is kept
here**, which is correctness rather than saving: the adapter's check and push are
in one synchronous section.

### A turn edge says when the daemon SENT, not when the agent got there

All three `started` fire at send time; the adapter promotes its queue head and
notifies nobody. **An end is the only readable edge there is** — and ends were
unattributed, two landing in the same millisecond. So `ChatUpdate.id` is on a
`turn` as well: head of inflight is being worked on, anything behind it is
waiting, not in inflight means its turn is over.

Matched by name rather than by dropping the head, because nothing promises the
adapter settles them in order. Clearing every mark on the first end reported the
second message as reached three and a half seconds before it was.

**A message typed mid-turn is not held anywhere in awp** — `send` returns in a
millisecond and the _adapter_ queues it. So there is no "take it back", and
`session/cancel` settles the running turn and every queued one with it.

### A call the turn ended underneath never resolves itself

ACP has no update meaning "the turn took this call with it", so a call in flight
when a turn is cancelled keeps `pending` — and every client reads it as work still
happening, hours later. `hanging()` folds the transcript to the **last** status per
tool id (a call is a patch keyed by id, so "did any update say completed" is the
wrong question) and emits one more ordinary `tool` update for each, **before** the
turn's own `ended`.

`cancelled`, not `failed`, and the mark is `⊘` rather than `✗` in both faces: a
cross is a claim about the tool, this is a claim about the turn.

### A loaded conversation reports no usage at all

A load sends no usage updates, so the ordinary case — opening a chat and reading
what the agent said last night — had no figure at all, exactly when somebody is
deciding whether to carry on here or start fresh. There is no call that asks, so
the daemon remembers the last reading **per session id**. Tokens do not change
while nobody is talking. Keyed by session and not by workspace, which is what
makes `/new` correct with no delete.

### An edit answers with nothing, so the daemon says what it changed

A tool call's row is its title, mark and output — and the one kind of call that
changes anything has **no output at all**.

**The daemon diffs, once**, with `diff` (jsdiff, what `@pierre/diffs` is built
on): two clients would otherwise each need a differ, and the terminal has none.
`createTwoFilesPatch` produces exactly what `parsePatchFiles` reads back.

- **Replace, never merge** — the adapter sends its guess when the call is made and
  the SDK's real `structuredPatch` when it has run, about the same file.
- **An unchanged block is dropped**; an empty patch is a row claiming an edit that
  did not happen. **`oldText: null` is a new file.**
- **Each side gets a trailing newline.** An `Edit`'s strings are a _fragment_, so
  jsdiff emits `\ No newline at end of file`, which `@pierre/diffs` throws on from
  inside its renderer — the patch parses and then the panel dies. A newline is
  added rather than the marker stripped: the marker is jsdiff telling the truth.
- **A patch survives being reopened, and is not quite the same patch.** Replay
  builds the block from the tool's own **input**, so a reopened conversation shows
  the narrower hunk. Both are true; nothing tries to reconcile them.

### `toolKind` is coarse, and the tool's own name is one field away

Most of what an agent does in a terminal is `Bash`, so most rows read `execute`,
and skills, MCP tools and `AskUserQuestion` all read `other`.
`_meta.claudeCode.toolName` is the real name and was simply not being read.
`toolVerb` in `@awp-kit/protocol/tools` is the rule **both faces** read it by — the
window said `ran`/`read`/`edited` off the kind while the terminal said `bash`,
which is two vocabularies for one conversation.

**Passed through, not translated** — a list of known tools would report every
unheard-of tool as `other`, which is the failure being repaired.

**And the row says what the call was FOR.** Bash's schema requires a description
and the adapter forwards it as `_meta.claudeCode.title`; it is the only field
carrying intent rather than mechanism. **The command stays reachable**, because
approving `rm -rf` from a description alone is the decision nobody should be asked
to make.

**A subagent is a tool call, and `_meta` says which.** There is no subagent update
kind in ACP — no nesting, and a subagent's own messages never arrive; what arrives
is one tool call at `in_progress` for minutes. The retry counters are the ones
worth having: a subagent behind a rate limit and one doing slow work are otherwise
the same picture. Read as `max_retries` first and camelCase second — they are the
SDK's fields in the SDK's spelling.

### Two sets of slash commands, and only one is intercepted

`available_commands_update` was dropped under a comment saying it said nothing a
person reads. Both updates dismissed that way turned out to matter. `/new` and
`/mcp` are the **window's**; everything else is delivered as ordinary text, which
is all it takes. The list **replaces** rather than merging, so an empty list is an
answer. `commands.ts` lives in `@awp-kit/protocol/commands` because which commands
are the client's own is a rule, not a rendering — and the move found the bug it
exists to prevent, already shipped: the menu called `onCommand` for every row, so
picking `/bro` ran `/new` and threw the conversation away.

### A fork is not a load, and it happens where it will be used

Loading makes the daemon a second writer on a transcript an interactive `claude`
is still appending to, with neither process aware of the other. A fork reads it,
copies it under a new id and leaves the original alone.

**Fork here, write the id down, open it there does not work:** a fresh fork is not
in `session/list`, `session/load` on one that has said nothing fails, and the
fallback is a new empty session — which from outside is exactly what a fork
carrying no memory looks like. So `ChatOptions.fork` asks the open itself to do
it; `forkNext` is in memory, and a refusal clears it.

**A fork replays nothing**, so an empty stream proves nothing and only a question
does. **Join the chunks before asserting on them**: the answer arrives as `"he"`
then `"ron"`.

## Two sources for one status, and neither can prove the other idle

```
  ~/.awp/workspace-state.json   the `claude` running in a workspace's TERMINAL
  the chat                      the ACP conversation open in THIS WINDOW
```

A workspace can have both, so `factsWith` is a precedence: `waiting` (the one
state about the person) beats `working` (a turn in flight) beats the file, which
is a hook's last write.

**The chat never reports `idle`**, and that is the half that matters: an idle chat
is no evidence about the agent in the terminal.

Folded from the conversation's own updates rather than asked for. One wrinkle:
**there is no update for a permission being answered** — the reply is the reply —
so the counters are decremented from `answer`.

## The page is a place both sides can move

```
  agent ──awp_browse{url}──▶ mcp ──PageOpen{from,url}──▶ daemon
                                                          │ dir → pair → thread
  window ◀────────── PageChanges{thread,url,at} ──────────┘
```

**The daemon resolves the thread; the caller has a directory** — the same binding
as every other agent-facing call, so a conversation cannot move a page beside
somebody else's work.

**A stream, and nothing is replayed.** A navigation is an _event_, so `Pages` holds
a `PubSub` rather than a `SubscriptionRef`: replaying the last one to a window
that just connected would move the page somebody is reading. `pages.test.ts`
asserts the silence with a timeout rather than an interrupt — a check that cannot
fail reads as a pass.

**`at` is on the wire because asking for the page already showing means reload.**
Two value-equal urls are otherwise one event.

**The daemon refuses a url; the address bar guesses at one.** `addressFor` turns
prose into a search, which is right for a person watching and wrong for a call
nobody is watching. Two schemes only — `file://` is excluded by name, because the
panel is a real browser view with a preload in it.

`probe:mcp` drives the **refusal** rather than a navigation: the success path
moves the panel of the thread this repository is in.

### Gadgets

A gadget is the other thing that column can hold: MDX the agent writes, compiled
here, listed per thread at `gadget://<thread>/<name>`. It rode the page feed
first and that was the design error — **one address per thread against a set
that grows**, so the second gadget written erased the first. `GadgetChanges`
therefore says _one more exists_, and `pageAddress` is back to two schemes.

**The title is read out of the document's first heading**, in the walk that is
already happening, and is never asked for. A title passed as an argument is one
that can disagree with the document under it with nothing able to notice; this
one cannot, because it is the heading. No heading falls back to the name, which
is never empty.

**The compile is at the moment of writing, and that is the whole reason it is
here** — a document that does not compile is a refusal returned to the agent
still holding the source, in the compiler's own words. Compiled in the window it
would be a blank panel an hour later, in front of the one party that cannot fix
it.

**MDX does not refuse `import`, it defers it.** With `outputFormat:
"function-body"` an import becomes `await import(_resolveDynamicMdxSpecifier(…))`
guarded by a check for `options.baseUrl` that throws _in the renderer_, about an
option the author has never heard of. `noImports` refuses it at the top, reading
the **estree** and not the source text — `import` also begins `important`, and a
fenced block full of imports is prose about imports.

**The source is stored and the compile is not.** A restart used to lose every
gadget, which here is several times an hour. `list` answers from the table and
compiles nothing, so a thread with forty opens its strip without building one;
`read` compiles on a miss. Storing the output instead puts a compiler's artifact
in a table outliving the compiler — a source cannot go stale that way.

**`at` has millisecond resolution and an agent writes gadgets in a loop**, so a
tie is the ordinary case rather than the exotic one. `rowid desc` behind
`at desc` settles it, and a rewrite deletes before it inserts, so the one written
second sorts first.

`probe:gadget` writes two the way an agent does — one that draws and one that
throws — and defaults to **5284** rather than the instance in use: unlike
`probe:ask` it writes.

### One writer per conversation, and the guarantee is not in memory

`Chat`'s `RcMap` gives one adapter per workspace **inside one process**. What it
guards is a session id in a store every daemon on the machine shares, opened by
`claude --resume=<id>`, which nothing stops two processes running at once. That
is not a hazard in the abstract: two agents ran on one session for two and a
half minutes and wrote two implementations of one task into one working copy.

So the claim is on the **session id**, in `chat_claims`, taken before the
adapter is spawned — `session-claim.ts`. Two guards, in this order:

```
  the table    every awp daemon, exactly      one row, one writer
  the process  everything else, approximately  a `--resume` nobody told the
                                               store about — a background
                                               agent, a hand-typed resume
```

- **The process check runs only on a session `take` reports as free.** It
  answers three states, not a boolean: ours, a predecessor at this address
  taken over, or free. Only free knows nothing about who was in the session —
  under either other one a `ps` finds an awp adapter, this daemon's own or the
  outgoing one's, and refuses a conversation awp is handing over. Collapsing
  the first two is what left the restart lockout standing after the takeover
  rule that was written to end it.
- **A dead holder is taken over at once** — `process.kill(pid, 0)`, not the
  heartbeat. Waiting out `STALE_AFTER` after every `dev restart daemon` is how
  a refusal becomes a sentence everybody learns to ignore.
- **The heartbeat is what makes the row an assertion rather than a lock.** Stop
  beating and it decays; without it one `kill -9` makes a conversation
  unopenable forever. It has nothing to do with sockets.
- **Release is refcounted in-process.** `RcMap.invalidate` then `RcMap.get` —
  what `/new` and the terminal fork both do — runs the new lookup while the old
  scope is still closing, and both are this pid.
- **The refusal must reach the window.** `subscribe` retries a feed forever and
  then swallows the cause, so a refusal caught there is a chat that spins with
  nothing on screen. `watchChat` catches `ChatUnavailable` _inside_ the retry,
  the way `Attach` handles `AttachRefused`.

`bun run probe:claim` spawns a second process against one store and prints what
it was told. It runs no agent and writes nothing that outlives it.

### Nothing an agent starts outlives the conversation

`ChildProcessSpawner` makes each child the leader of its **own process group**,
and the kill goes to the group — so a dev server an agent backgrounds dies with
the adapter, half a second after the `RcMap` releases it. Measured, because
POSIX would have orphaned it and the opposite is the reasonable guess:
`bun run probe:child-tree`.

`mindUntilSettled` holds a conversation for the length of a **turn**, and a
backgrounded command ends its turn at once, so nothing is holding it. **Anything
meant to outlive a turn needs a session of its own** — holding the adapter open
longer only moves the deadline.

### An adapter that stopped is not a conversation

The reader ending is the only stop this side sees, and knowing was all it did:
the `RcMap` entry stayed live to the TTL, so the next message went to a corpse.
`Conversation.gone` is that edge, and the lookup invalidates its own entry on it.

**A finalizer may not reach for a key by name.** `invalidate` then `get` — `/new`,
the fork, and this — runs the new lookup while the old scope is still closing, so
a watcher naming the key would kill the conversation `/new` just opened.
`generations` hands out a token and the holder speaks only while it is still the
key's; registered **last**, so LIFO makes it the first finalizer to run and a
kill this daemon asked for has given the key up before `gone` completes.
