# TODO

Every task that is not finished, written out so that losing the task list
does not lose the reasoning in it. The list itself lives in the session; this
file is the copy that survives.

**A task here is an argument, not a ticket.** Most of the value is in the
paragraphs — what was measured, what was tried and did not work, and which
choice is the one that matters. Read the body before starting, and update it
rather than the summary when what you learn changes the shape of the work.

`bun run fmt` reflows this file, so the sequence is edit, then format, then commit; skipping the format leaves a diff that turns up under somebody else's change.

40 open, as of 2026-09-16. The count is taken from the entries rather than
carried forward — it read "46 open" over 51 of them once.

Closed later the same day, each against the code: #87 (the tasks panel's `N
done` is a control now), #92 (the `+` box writes to awp's own source, which is
what #124 made possible) and #116 (double-click a thread's title in the sidebar
or the bar, plus `rename…` in its menu).

Added the same day, out of what was asked while those landed: #132, #133 and
#134.

Closed on 2026-09-16: #85 (the tasks row's second control, which opens the
new-thread form with the task already in the brief). Both of its open questions
were decided by building it — the base is the workspace on screen, which is
what cmd+shift+N means, and the label is a glyph rather than a second word,
because the row is 280px wide and two labelled buttons make the rarer one look
like half of a pair of equals. The third, what happens to the task afterwards,
is still nothing: this panel does not write to the store for a fan-out, and
should not until there is a reason.

Hand-edited rather than regenerated, and that is now the normal way this file
changes: the session the list was regenerated from has ended.

Closed on 2026-09-15 as already finished, against the code rather than against
memory: #57 (cmd+P, `Switcher.tsx`), #60 (the diff splitter's icon and its
ease), #68 (a dropped file's path, `dropped.ts`), #74 (the Web Inspector
collapse — obsolete, the shell is Electron), #81 (the new-thread brief grows,
`grow.ts`), #84 (the style guide, at `#/styleguide`), #91 (the agent under ACP,
whose own Left section already said "nothing of its own"), #96 (the diff's
viewed marks) and #110 (the bug in them, fixed by the same state).

Added on 2026-09-15, out of what closing the nine turned up: #130 (the panel
has no way to learn a task changed) and #131 (a project off the list strands
its tasks). All three of those are done and their entries have left, which is
how a task finishes here — the measurements live in AGENTS.md, under "A field
nobody checks", "Forgetting a project lets go of its tasks" and "Tasks awp
owns".

#124 went with them. Its three remaining halves landed together, because each
made the next one worth having: Claude Code's own lists as a second source, a
source awp owns so there is something to write, and `thread:` tags — which
needed the sweep to stop deleting what a person applied.

---

## 18. Delete the probes once the skeleton measures itself

Deferred deliberately, twice now. The probes still here are the ones AGENTS.md tells a future session to run — child-env, claims, jobs-store, workspace, thread-parent — and the three that look spent (zmx-pty, reflow, wheel) are the only way back to a question that is hard to ask any other way. Deleting working diagnostic tooling needs a reason better than tidiness; wants Andrew's call.

## 21. Design one theme for the app and the terminal together

The pane currently borrows Catppuccin Macchiato/Latte and the chrome derives a few roles from it, which is two palettes pretending to be one. Build a single theme: chrome roles and the terminal's sixteen ANSI slots designed against each other, in both light and dark.

The constraint learned the hard way on 2026-08-25: a terminal theme is not decoration, because programs choose their own colours against the background they assume. Our base was Catppuccin's crust rather than base — two steps too dark — and Claude Code's truecolor #373737 message block went from a subtle lift to a stark slab. Whatever we design has to state a background that programs can reason about, and the ANSI slots have to stay recognisable as themselves: a pane and a sidebar row showing the same status must not be two different greens.

## 41. Keep a thread's bookmark at its tip

Nothing moves andrew/<name> forward as commits land, so a bookmark sits at the FIRST commit of its branch. Measured: andrew/awp-kit-amoeba is at lmpznzxr "chore: move existing tree into archive/", 51 commits behind the workspace's working copy. test1234 branched from exactly that and landed 51 commits behind; diff-view did too and was rebased by hand afterwards (op log shows two "point bookmark andrew/diff-view" entries). So "base this thread on that bookmark" gives the start of the work rather than the current tip. Decide between moving the bookmark on commit and resolving the base to the workspace's tip.

## 42. Show what a TERMINAL agent is doing on a sidebar row

Half of this landed with ACP, and the half that is left is the harder one — so
what remains is written out plainly rather than left as the original sentence.

What a row can now say, live, with no hook and no file:

    a turn in flight                 working
    a permission nobody has answered waiting — the one state that is about
                                     the person rather than the machine

That comes from the daemon's own ACP conversations, merged into
`WorkspaceFacts.status` by `factsWith` in handlers.ts, and it is a **precedence
rather than an override**: the chat reports only those two states and never
`idle`, because a chat nobody is using is no evidence at all about the agent
somebody has running in the workspace's terminal.

Which is exactly what is left. Most rows on a real machine are a `claude` in a
pty, and for those the only source is still `~/.awp/workspace-state.json` —
written by the Go implementation's Claude Code hooks, which nothing in this
repo installs. Three ways out, and the choice has not been made:

    our own hooks      write the same file, or a better one, from a
                       Claude Code hook awp installs into a workspace
    the agent under    the terminal session runs the ACP adapter rather than
    ACP                `claude` directly, and the chat is one view of it
    accept the split   a chat-backed row is live, a terminal-backed row is as
                       fresh as the last hook wrote, and the strip says
                       nothing about which

The second is the one that makes every other unanswered question here go away,
and it is also the largest.

## 47. Give a thread a drawing the agent can read

A tldraw canvas in the accessory column, one per thread, that an agent can read.

Read first, write later — the user's own sequencing, and the right one: reading is a projection, writing is a parser.

Shape:

- the snapshot is a thread's, not a workspace's — every workspace in the thread shares one canvas
- sqlite is the truth (one store rule), the daemon writes derived files
- the derived files are what the agent actually reads

The hard part is NOT the canvas. It is that a tldraw snapshot is shape records with coordinates, which is not something an agent can act on. Two projections, both derived from the same snapshot:

1. PNG — a multimodal agent reads the picture directly. Export happens in the RENDERER (tldraw needs a DOM), bytes go to the daemon.
2. a text outline — text shapes, and arrows as "A -> B" relations. Cheap, greppable, diffable.

Open questions recorded in the conversation: where the file lives, and how the agent is told the path.

## 58. cmd+shift+P: run an action

The command half of the palette — everything the window can do, by name, with the keyboard: fold a column, start a thread, send a review, clear finished jobs, switch appearance, open a panel. Shares the dialog and the filtering with the thread picker (`Switcher.tsx`, #57, done) and differs in what it lists and what choosing one does. Worth deciding early whether actions are a registry each feature adds to, or a list assembled in one place; the first is the only one that stays correct as features land.

## 59. cmd+comma: a configurator for settings that exist

Settings are read from a config file by the daemon (Settings in packages/server) and there is no way to see or change one from the window. bookmark_prefix is the worked example: it decides what a thread branches from, it is invisible, and its absence silently changes behaviour (baseOfThread falls back to <name>@). A settings surface needs the contract to carry the settings both ways, which does not exist yet — so the first question is which settings are genuinely a person's to set, rather than building a form over whatever the file happens to hold.

## 63. Run a workspace's services, and know which port each one took

A **service** is a long-running process a workspace needs while it is being worked on — a dev server, a queue worker, a database. Declared in config alongside `hooks.bootstrap` and `actions`, per project:

    "services": {
      "dev": { "command": "bun run dev" }
    }

It is a third thing, and the distinction is the whole design:

    hooks.bootstrap   runs once, must finish, blocks the brief
    actions           a person runs it, it ends, they read what it said
    services          started once, expected never to end, and its
                      interesting output is a URL somebody clicks

**One zmx session per service**, kind `service_<name>` — the same shape as `action_<name>`, and it fits: `MAX_KIND` is 16, so `service_` plus an eight-character name. That gives restart, scrollback and attachment for nothing, and `Multiplexer.start` is already idempotent, so "start the dev server" on a running one does nothing rather than starting a second.

**The port is the feature.** A service nobody can reach is a process. Two ways to learn it and they fail differently:

    scrape the log     "Local: http://localhost:5273/" — reads the number the
                       program itself printed, so it is right by construction,
                       and it is a regex over somebody else's output format
    ask the kernel     lsof over the session's process tree — true regardless of
                       what was printed, and answers with every port including
                       ones the program never mentioned

Probably both: lsof for the truth, the log line for the label. Worth measuring which one is actually available, since the dev server is a grandchild of the pty and `lsof -p` on the session pid alone will not see it.

**A service belongs to a workspace, not to a thread**, because it is bound to a checkout: two workspaces on the same repo each need their own dev server on their own port, which is exactly the case a single shared one gets wrong.

UI is undecided and does not have to be settled to build the daemon half. The cheapest honest surface is a row per service in the sidebar under its workspace, showing running/stopped and the port as a link — the port being the one thing a person actually wants from it.

**Started by a person, for now.** Whether a service comes up on its own when
a workspace is created is a separate decision with its own failure mode — a
workspace that builds four processes nobody asked for — so it is a follow-up
task rather than part of this one. Build the manual start/stop first and see
what it feels like.

**The first consumer is this repository.** awp itself needs three long-running
processes to be worked on — the daemon, Vite, and the app — in that order, with
the second-instance ports as overrides:

    daemon   AWP_DAEMON_PORT=5284 bun run daemon
    vite     VITE_AWP_DAEMON_URL=ws://127.0.0.1:5284 vite --port 5283 --strictPort
    app      bun run --filter amoeba dev:all

**One window, many renderers.** Only ever one Electrobun instance — the one
somebody is working in. A workspace on a branch runs its renderer and nothing
native, and it is read **in the web panel** of the window already open:

    ~/.awp/workspaces/awp/main      daemon + vite + electrobun   the window
    ~/.awp/workspaces/awp/<branch>  vite (+ daemon, see below)   a web panel tab

That is what makes the port worth knowing rather than merely tidy: the panel
needs a URL, and the URL is the service's own answer. It also removes the
second-instance ritual in AGENTS.md, where the ports are typed by hand and a
missing `VITE_AWP_DAEMON_URL` is a window silently talking to the wrong daemon.

**A branch needs its own daemon, so it is two services and not one.** Sharing
the window's daemon is cheaper and was the first answer; it only holds for a
branch that changes the renderer alone, and most branches worth looking at move
the contract as well. So:

    ~/.awp/workspaces/awp/<branch>   daemon on its own port
                                     vite, pointed at that daemon
                                     no electrobun

Two consequences, both already documented elsewhere in AGENTS.md and both
reasons this is not a small task:

    one database        ~/.awp/awp.sqlite is a single file and both daemons
                        open it. Two jobs runners each resume non-terminal
                        jobs on start, and the deduplication that stops a job
                        running twice is per process. Harmless when nothing is
                        in flight, which is the ordinary case — but it is a
                        check before starting, not a thing to discover after

    the port is an INPUT  the task above assumes a service prints its port and
                        something reads it. Here it is the reverse: the port is
                        chosen first, and VITE_AWP_DAEMON_URL is substituted at
                        BUILD time, so a renderer given the wrong one is a
                        window quietly talking to the daemon you were trying
                        not to disturb, with nothing on screen saying which

So a service's port has two directions — discovered, for somebody else's dev
server, and assigned, for one service that has to be told about another. A
design that only has the first cannot express this repository.

Every incident so far has been a mistake in running them by hand: three of them
started out of order (a white window loading a Vite that was not up yet), and
fourteen daemons left behind by restarts that did not kill anything. Both are
what a service list is for. It also forces two questions this task can otherwise
avoid — one service depending on another, and a service whose port is an input
rather than something to discover — and answering them here is cheaper than
answering them later against somebody else's project.

Depends on `deck.project_roots`-era config reading; see also the zmx log viewer task.

## 64. Read a session's zmx log in a panel

`zmx version` reports its own log directory, and there is one file per session:

    log_dir   ~/.local/state/zmx/logs
              awp_probe_1.log, acptest.log, …

That is zmx's own record of what happened to a session — starts, attaches, exits — which is a different thing from `Multiplexer.history`, which is the _scrollback_ the program wrote. When a session dies for no visible reason, the scrollback is empty and the log is the only place the reason is.

So: a panel in the accessory column that reads the log for the selected session. Needs

- `Multiplexer.logDir` over `zmx version` (parsed the same way the rest of zmx's tab-separated output is), rather than hardcoding a path that is XDG-dependent
- a `SessionLog` call, tailing rather than reading whole — one of those files is already tens of megabytes
- the reading to be safe: a log path is composed from a session name, so it must resolve inside the log directory and nowhere else

Wanted alongside the services work — a dev server that exited on its own is exactly the case where the scrollback says nothing and the log says why.

## 73. Get React DevTools attached to the window

Yes, and the route is the one React Native uses rather than the browser extension — there is no extension mechanism in a WKWebView.

`react-devtools` (the npm package) is a standalone app that listens on a socket, and a page connects to it by loading one script **before React initialises**:

    <script src="http://localhost:8097"></script>

That is the whole integration. It goes in `apps/amoeba/index.html` ahead of the module entry, and only in development — a production build must not try to reach localhost:8097, and Vite's `index.html` transform or a plain `import.meta.env.DEV` guard around an injected tag is enough to keep it out.

Two things to get right:

- **Before React.** The hook installs itself on `window.__REACT_DEVTOOLS_GLOBAL_HOOK__`, and React reads that once when it initialises. A script that loads after it connects to nothing and shows an empty tree, which reads as "devtools do not work here".
- **The React Compiler changes what you see.** Components are memoised and some hooks are rewritten, so the tree and the hook list will not match the source one-for-one. That is worth knowing before it is reported as a devtools bug.

Also worth having alongside, and cheaper: **Safari's Web Inspector can attach to the WKWebView** if the view is created inspectable. That gives DOM, console, network and the profiler — everything except the component tree — and needs no script and no dependency. Check whether electrobun sets `isInspectable` on the view and expose it in dev if it does not; on macOS 13.3+ it is off by default and nothing works without it.

The two are complementary: Web Inspector for what the page is doing, React DevTools for what the tree is.

## 75. Give every button a hover tip

Reported against the diff panel and generalised: all buttons. Audited across the renderer, seventeen have no `title`, and five of those have no `aria-label` either — so they are unexplained to a pointer _and_ unnamed to a screen reader:

    Boundary  2      Jobs      4      Meter    2
    Diff      6      Sidebar   1      Web      1
    ImportProject 1

Several are icon-only, which is the case where a tip is not a nicety: an icon with no text and no tip is a control whose meaning has to be discovered by pressing it, and some of these are not things to press speculatively.

Rules for what a tip says, so they are worth having:

- **Name the effect, not the widget.** "hide the panels", not "toggle". The existing ones in `Bars.tsx` and `Sidebar.tsx` already do this and are the model.
- **State-dependent where the control is.** A toggle's tip says what pressing it will do _now_ — "show the sidebar" versus "hide the sidebar" — which is the only way one glyph can carry two meanings.
- **Say the cost where there is one.** A destructive or outward-facing action's tip is the place to say so, in the way `MoveToThread`'s forget tip says "nothing else is removed".
- **A keyboard shortcut belongs in it**, where one exists — `new thread (⌘N)` is already the pattern.

`title` rather than a tooltip component: it is what the window already uses, it needs no library, and the alternative is a fourth thing in a stack that AGENTS.md says not to add to. Where a button is icon-only it also needs `aria-label`, which is a different job — `title` is a hint and `aria-label` is the name.

## 79. Let a diff hunk expand its collapsed context

@pierre/diffs supports this natively and we pass none of it: `collapsedContextThreshold`, `expansionLineCount`, `expandUnchanged`, and `FileDiff.expandHunk(hunkIndex, 'up'|'down'|'both', count?)`. `HunkData.expandable` carries `{chunked, up, down}` and the icon sprite already ships `diffs-icon-expand` and `diffs-icon-expand-all`, so the affordance is drawn for us.

The catch is the patch, not the library. `jj diff --git` emits three lines of context, so expansion has nothing beyond that to reveal unless `loadDiffFiles: FileDiffContentsLoader` is supplied — a callback that fetches both whole sides of a changed file. That is a new RPC (file contents at a revision, both sides) plus the loader wiring, and it is the actual work here. Without it, expansion only reaches the ends of what the patch already carries.

Also check whether the default `collapsedContextThreshold` already draws separators we are simply not noticing in a 200px column.

## 88. Find the daemon finaliser that never completes

The shutdown deadline in `main.ts` makes a stuck shutdown harmless, and while proving it a second hang turned up that it also covers.

Measured after the fix:

    isolated socket server, no client   scope closed after 310ms
    the daemon, with a client           2s, "leaving anyway"
    the daemon, with NO client          2s, "leaving anyway"   ← this one

The socket server's own hang is understood — `ws`'s `WebSocketServer.close` waits for every client connection and the ones handed to `run` are never terminated. That accounts for the first daemon line and not the second: with nothing connected it should close in a few hundred milliseconds and it does not.

So at least one more finaliser in the daemon's layer stack does not complete. Unruled-out candidates: the workspace watcher (`watch.ts`), the jobs runner's fiber, the pty layer, and the sqlite connection. Bisecting is straightforward — build the layer stack in a probe with one layer removed at a time and time the scope close, the same shape as the probe that isolated the socket server.

Not urgent. The deadline means the process always goes, and the daemon holds nothing whose loss a longer wait would prevent — sessions are zmx's and outlive it by design. What it costs today is that every stop takes the full grace period, and that a genuinely clean shutdown is indistinguishable from a stuck one in the log.

## 89. Fuzzy search over the tasks panel

The tasks panel is a list of titles and it is already long — twenty-four outstanding in this workspace, and that is before the completed ones become reachable. Scrolling to find one is the wrong gesture when the thing being looked for is a word somebody remembers.

So: a filter field at the top of the panel, matching fuzzily over the subject and the description, narrowing as it is typed. Matching the description matters — half of what a person remembers about a task is a phrase from its body, not its title — even though the description is collapsed by default, which means a hit needs to say where it was found.

Shape questions, none settled:

- the field's place. The panel's head already holds the count; a filter could
  replace it while typing, or sit under it as its own row.
- the algorithm. Subsequence matching with a score is the usual answer and
  needs no dependency; `browse.ts` may already hold something close enough to
  reuse rather than a second implementation.
- highlighting the matched characters, which is what makes a fuzzy match
  legible rather than mysterious. Without it a low-scoring hit reads as a bug.
- whether a filtered row should open its description automatically when the
  match was found there. Probably yes, or the row is a title that does not
  contain what was typed.
- the keyboard. Focus should reach the field first when the panel opens, and
  ctrl+j/k should step the filtered rows — see the navigation mandate.

Related: [[a show-completed section on the tasks panel]] (#87), which makes the list long enough that this stops being optional, and #58's command palette, which is the same matching problem in a different frame — worth one implementation rather than two.

## 90. Tug a bookmark forward to a revision

Split out of #70, which is now only about showing bookmarks on a revision row.

Hovering a revision that is _ahead_ of where its bookmark currently sits reveals a control on the right; pressing it moves the bookmark to that revision. `Jj.setBookmark` already exists and its doc says "Create a bookmark, or move an existing one. Already idempotent in jj." So the operation is there; what is missing is an RPC and the decision about when to offer it.

**"Ahead" needs care.** The obvious rule is "above the bookmark's row in the list", and the list is newest-first over `@ | trunk()..@`, so for a linear stack that is right. It is a claim about list position and not about ancestry, and it is wrong the moment the stack forks. Either ask jj (`jj log -r '<bookmark>::<rev>'` is non-empty when one descends from the other) or state the limit in the tooltip. Do not silently offer a move that would rewrite history sideways.

Refuse rather than guess when a row carries no bookmark and the workspace has none — there is nothing to tug. A row that shows the control and then explains why it did nothing is worse than a row without one.

Which bookmark gets tugged is its own question. A workspace usually has exactly one, `<prefix>/<workspace>`, which is what `baseOfThread` already composes — so the control can name it rather than offering a picker. Two bookmarks in one stack is the case that needs a decision.

Related to #41, the automatic version: nothing currently moves `andrew/<name>` forward as commits land, so a bookmark sits at the _first_ commit of its branch — measured at 51 commits behind on this workspace. A manual tug is the smaller answer and may be the better one: moving a bookmark is a decision, and a button says "now" without having to pick a policy.

One thing already established while doing #70, which matters here: a remote bookmark appears in a commit's `json(bookmarks)` when it disagrees with local. Measured — `andrew/awp-kit-amoeba@git` sitting one commit behind shows up on its own commit, carrying `remote: "git"`. The revision list now filters those out, so anything this reads is local; a tug must not offer to move a name that only exists on a remote.

## 93. The rest of the agent's face on the daemon

The server exists — `packages/server/src/mcp.ts`, stdio, one per agent, every
tool bound to the checkout it runs in. Three tools are in it: `awp_thread`,
`awp_review_comments` and `awp_file_finding`. The transport and scope decisions
are made and are structural, so everything below is additive.

What is left, roughly in order of how obviously it is wanted:

    open a diff at a revision       "look at what I just did". The window
                                    would have to be told, which means a
                                    change stream or a nudge — the first tool
                                    here that acts on the WINDOW rather than
                                    on the store
    open a page in the web panel    a preview, a failing CI run, a dashboard.
                                    Same problem, same answer
    put a task on the list          the honest version of #92, without a
                                    second writer of ~/.claude/tasks
    say what it is doing            the volunteered half of #42. The chat
                                    already reports a turn; a terminal agent
                                    has no way to say anything

**The first two need something that does not exist yet.** Every tool today
answers a question or writes to the store, and the window reads the store. A
tool that opens a panel has to reach a _window_, and there may be none, or two.
Decide whether that is a stream the window subscribes to — the shape
`JobChanges` already has — or a stored "what the agent last asked to be shown"
that the window picks up. The second survives a reload and the first does not.

**The terminal agent has no MCP server.** The ACP conversation gets one handed
to it in `session/new`; a `claude` started by the create job in a pty does not,
because nothing writes a `.mcp.json` into the workspace and nothing adds
`--mcp-config` to the agent command. That is most agents on a real machine. The
bootstrap step is the obvious place, and `.awp/` is already untracked — but a
file written into the workspace is a file that goes stale when the entry point
moves, where the flag is resolved fresh each start.

Related: #91 is the same gap from the other side and is done. ACP gives amoeba
a channel _to_ the agent's conversation; MCP gives the agent a channel _to_
amoeba.

## 94. A sent message sometimes lands without its Return

Reported: "sometimes when we send a message into the agent pane claude code term it doesnt hit enter and send". So the text arrives in the agent's box and sits there, which is exactly the failure #72 was supposed to have ended.

**Reproduced, against a real Claude Code in a throwaway session.** Roughly 1 in 6 with a clean input box and time to settle between trials. It is real and it is intermittent.

What is now measured, and what each measurement rules out:

    the gap between the two chunks     8.4 – 16.4ms over ten sends
    at the pty                         (two zmx send processes, back to back)

The existing comment on `send` says two writes produce two chunks and "a sleep here would be superstition with a cost". The first half is confirmed; the second is what is in doubt, because a gap that wanders between 8 and 16ms is exactly the shape of something straddling a threshold.

    Claude Code enables bracketed      ESC[?2004h, in the first line it writes
    paste

    wrapping the text in              made no measurable difference
    ESC[200~ … ESC[201~

That last one is the useful negative: if the failure were the TUI mistaking the CR for the tail of a paste, telling it explicitly where the paste ends would have fixed it. It did not, so **the paste-window theory is wrong** and the cause is somewhere else.

**A warning about measuring this.** The first harness reported 4 of 8 stuck, in a perfect STUCK/submitted alternation — and that number is an artefact, not a rate. It cleared the input box only after a failure, so every trial after a success began in a different state from every trial after a failure. Any future attempt has to clear before _every_ trial and wait for the previous answer to finish, or it will measure its own asymmetry.

Still unexplained, and the next things to try:

- whether the agent being _busy_ is the variable. Sending while a response
  is streaming is the obvious candidate now that the paste window is out,
  and it fits "sometimes" exactly: a review is sent at the agent that is
  still working.
- what the box actually contains when it sticks. Reading the raw screen
  with `zmx history --vt` at the moment of failure would say whether the CR
  arrived and was swallowed, or never arrived at all — those are different
  bugs and nothing measured so far separates them.
- whether a longer gap helps at all. Cheap to test now that there is a
  harness that does not lie: 0, 50, 150ms, twenty trials each.

## 98. An open-or-create PR button in the diff head

A diff is read to decide whether the work is ready, and the next thing after deciding it is is opening a pull request. Today that means leaving the window.

So a button in the diff head row, in one of two states:

    facts.pr is set        "open PR #412"   — opens it in the web panel
    facts.pr is undefined  "create PR"      — briefs the agent to make one

**The signal is already there.** `WorkspaceFacts.pr` is on the wire — `Schema.UndefinedOr(Schema.Int)`, described as "the pull request this workspace's branch is on, if it is on one" — and the sidebar row already renders it as `#412`. So the button reads a value the panel can already see; nothing new crosses the wire for the first state.

**Open goes to the web panel, not to a browser.** The panel exists precisely so a thing being read against a diff stays beside it, and handing the URL to the system browser throws away the window this was built for. That needs the PR's URL rather than its number, which `facts` does not currently carry — either add it beside `pr`, or compose it from the remote, which is a guess about a forge and should not be made in the renderer.

**Create is a prompt, not a call.** Deliberately: opening a pull request means a title and a description written from the change, which is exactly what the agent is for and exactly what a hardcoded `gh pr create --fill` gets wrong. Same wire as a review or a task — `TaskSend` and `NoteSend` are the worked shapes — and it should ask for _next_ rather than _now_, for the reason in `taskPrompt`.

Open questions:

- what the prompt says. "open a pull request for this work" is the whole of
  it, and the agent already has the diff and the bookmark; over-specifying
  it is how a prompt stops working when the repo's conventions differ.
- whether the button should wait. `facts` is pushed, so the number arriving
  is the confirmation — the button can go from "create PR" to "open PR #N"
  on its own with nothing to poll. That is the nicest version and needs
  nothing but for the facts watcher to notice.
- a workspace with no bookmark and nothing pushed. The refusal should name
  what is missing rather than sending a prompt that cannot succeed.
- #41 (keep a thread's bookmark at its tip) and #90 (tug a bookmark) both
  matter here: a PR opened against a bookmark sitting at the first commit of
  a branch is a PR with one commit in it, which is the state this workspace
  was measured in at 51 commits behind.

Related: the head row is now wanted by [[a side-by-side toggle on the diff panel]] (#95) and [[a pop-out file tree beside the patch]] (#97) — the viewed marks (#96) already landed in it. This is the fourth, and it is the widest of them — a button with words in it rather than an icon. The row needs designing once, for all four.

## 99. Separate with space and fill, not with rules

Reported: "we have too many heavy borders generally, like too many dividing lines instead of just using space", and then the sharper version — "it has borders and then spacing outside the borders, which is weird".

The second sentence is the diagnosis and it generalises. A rule _and_ a gap are two separators doing one job, and the eye reads the pair as a mistake even when it cannot say which half is wrong. The diff head row had exactly that shape twice in one afternoon and is now fixed by being filled instead of ruled — one step off the window's base says "band" on all four sides at once, and it keeps saying it while the patch scrolls underneath, which is the job the rule was there for.

Counted across the renderer: **29 border declarations**, and they are two different things that want different answers.

    structural rules — the ones this is about
      Accessory.tsx   the tab strip's underline
      Bars.tsx        3: the corner strip, the agent header, the footer
      App.tsx         a column edge
      Sidebar.tsx     a section rule
      Tasks.tsx       the panel head
      Jobs.tsx        the controls row
      Web.tsx         2: the address bar, and one more

    control outlines — mostly legitimate
      buttons and inputs in NewThread (5), Diff (2), Boundary (2),
      ImportProject, MoveToThread, Sidebar, Tasks (2), Jobs, Web (2), Meter

The structural rules are the pass. Every one of them is a band between two
things, and every one could be a fill instead — `colors.surface` over the
window's `colors.base`, which is exactly the third level that was added to
the palette for this. The rule to apply, stated once so it is not re-argued
per component:

- a band gets a fill, not a rule. Panel heads, tab strips, toolbars, the
  top and bottom bars.
- a rule is for a boundary something _scrolls under_ where a fill will not
  do — and after the fill there is usually nothing left in that category.
- never a rule with a gap outside it. If there is room around the line, the
  room was already the separator.

The control outlines are a second, smaller question and probably a different answer: a window with fifteen outlined buttons reads as busy for the same reason. Worth looking at whether the quiet ones (fold all, unfold all, the carets) need an outline at rest or only on hover — the tab strip already works that way and does not look unfinished.

Two cautions:

- **StyleX drops the `border` and `background` shorthands in silence** — see
  AGENTS.md. Every removal has to use the longhands, and the built CSS is
  what proves it, not the source.
- **contrast.** A fill one step off the base is a mark, not text, so the
  threshold is 3.0 rather than 4.5 — but Latte's surfaces were already the
  thing that failed once and were retuned by measurement. Any new pairing
  gets computed off the rendered element, not off the source hex.

## 100. A ship-it button, held down, with a countdown

A button at the bottom of the diff pane that ships the work. Two halves: what shipping means, and how it is pressed. The second half is the fun one and is the part that is decided.

**Held, not clicked.** Press and hold arms it: three, two, one, and the rocket goes. That is not decoration — it is the confirmation dialog, done as a gesture rather than as a modal, and it is better than a dialog for exactly this: the countdown is cancellable by letting go, which is what somebody who has changed their mind actually does with their hand.

Asked for with smoke, particles, maybe WebGL — "whatever, whatever, fun". So the launch is allowed to be a real animation rather than a spinner. Things worth knowing before picking a technique:

- the window already has a canvas renderer in it (the pane) and a worker pool
  for highlighting, so a second canvas is not a new kind of thing. WebGL is
  available; a 2D canvas with a few hundred particles is far less code and at
  this size probably indistinguishable.
- it must not fight the pane for frames. The meter in the debug panel exists
  to answer "is something dropping frames", so measure with it rather than
  guessing — a launch that stutters the terminal beside it is worse than no
  launch.
- `prefers-reduced-motion` shortens or stills the animation but does **not**
  skip the hold: the delay is the safety and the motion is only what says so.

Notes on the gesture:

- `pointerdown` → `pointerup` / `pointercancel` / leave, with pointer
  capture, cancelling on all three. A countdown still running after the
  pointer left the button is a launch nobody asked for.
- the keyboard needs an equivalent, per the mandate. Space held is the
  natural one, and it has to arm and cancel the same way.

**What shipping means is per project**, and is deliberately left open until the button exists — it may turn out to be a prompt to the agent like everything else here, which would be the smallest answer and the most consistent one. Some repositories ship by opening a pull request, some by pushing straight to the main line, some by pushing a bookmark and letting CI take it, so whatever it becomes belongs in `.awp/config.json` beside `hooks.bootstrap` and `actions`, merged per the replace-if-empty rule already in `merge`:

    "ship": { "mode": "pr" }        open a pull request
    "ship": { "mode": "trunk" }     land it on the main line
    "ship": { "command": "…" }      whatever this repo does, through sh -c
    "ship": { "prompt": "…" }       or just ask the agent

A `command` form is worth having for the same reason `hooks.bootstrap` has one: it goes to `sh -c` whole, and `&&` and a quoted path are its ordinary furniture.

Unconfigured should probably be "open a PR" rather than a refusal — it is the reversible one, and a button that lands on main by default is a button nobody presses twice.

Related: [[an open-or-create PR button in the diff head]] (#98) and [[keep a thread's bookmark at its tip]] (#41) — shipping a bookmark that sits at the first commit of a branch ships one commit, which was measured at 51 behind on this workspace. Worth deciding whether ship-it simply _is_ #98's button after a hold, rather than a second control.

## 102. Re-parse a streaming message without re-parsing all of it

Markdown, diffs and mermaid all landed — see the fence note in AGENTS.md. What
is left is the one bullet that was always the hard part, and it is now the
whole of this task.

ACP delivers a message as it is written, and the panel folds each chunk onto
the text and re-renders. So a long reply with three diagrams in it re-parses
the entire message on every token, and each `Fence` decides again what it is:

    chunk arrives    fold → whole message re-parsed by react-markdown
                     → every fence re-mounted → mermaid asked to draw again

Nothing about that is visible yet at the lengths a chat produces, which is why
it is a task rather than a fix. What to measure before changing anything: the
meter panel already answers "is something dropping frames", and a reply with a
diagram in it is the case to watch.

`shiki-stream` ships inside `@pierre/diffs` and is worth reading first — the
diff panel's own tokenizer already has a streaming form, so the answer may be
to render a fence from a stream rather than to memoise the parse.

A half-finished fence is the other half of the same problem: three backticks
have arrived and the language has not, so the block is briefly a `pre` and then
becomes a diagram. Deciding to hold a fence until it closes is cheap and may be
all this needs.

## 103. Decide whether a service starts on its own

Split out of the service layer task, which now builds manual start and stop only.

Autostart is a real question and a separate one: `"autostart": true` in a project's service config means creating a workspace also creates N processes, and the failure mode is a person with four workspaces open and twelve servers running, most of which they will never look at.

Things worth knowing before deciding, and none of them are knowable until manual start exists:

    how often is it started by hand    if it is every single time, autostart is
                                       just removing a chore
    what does it cost when idle        a dev server on a checkout nobody is
                                       reading is memory and a port
    what happens on a cold cache       `bun install` in a fresh workspace makes
                                       the first start minutes, not seconds

Middle options that may beat a boolean: start on first attach to the workspace, or start when a job's bootstrap finishes rather than at creation.

## 104. A linked pull request names the sidebar row

Once a thread holds a pull request link, the sidebar row should say what the PR is called rather than what the workspace is called.

    now     pr-2418
    after   #2418 Header allowlist for the proxy

The number alone is an address and a person still has to open it to find out what it is — which is exactly the cost the thread title already avoids elsewhere. A review thread's title is composed at creation from `gh`, so the text is already fetched; what is missing is the sidebar reading it for a workspace row instead of showing the directory name.

Two things to settle:

    truncation   a PR title is a sentence and the column is 260px, so the
                 number has to survive and the words are what get clipped
    a workspace  with no link, and one linked after the fact — the row has to
                 fall back to the workspace name rather than showing nothing

## 107. Reorder threads in the sidebar

Threads sort by whatever the daemon returns. A person should be able to put them in the order they think about them, and have it stay that way.

Three things to settle:

    where the order lives     a column on the thread record, not client state —
                              it has to survive a restart and be the same in a
                              second window
    the gesture               drag, and per the keyboard mandate also a chord,
                              since a drag-only feature does not exist without
                              a pointer
    what happens to new ones  a thread created while an order exists has to
                              land somewhere deterministic

Sparse integer or fractional ranks rather than a dense index, so moving one row rewrites one row instead of all of them.

## 108. Group threads in the sidebar

Separate from reordering, and the harder half: a person should be able to put threads into named groups in the sidebar.

The sidebar already nests two deep — thread, then its workspaces — so a group is a third level, and that is the design question rather than the storage. Whether a group is a folder a thread lives in (one group per thread, like the workspace claim) or a label a thread can wear several of changes what the tree can even draw.

Depends on reordering existing, since a group without an order inside it has the same problem one level down.

## 109. cmd+F searches the diff

cmd+F should find text in the patch on screen.

The browser's own find is not available — this is an app window, and the pane and the diff both draw into things a native find would not reach anyway. So it is a control the panel owns: a field, a count, next and previous, and Escape to dismiss.

Two things particular to this panel:

    the renderer virtualises      a match in a file that is not currently
                                  rendered has to be found in the model and
                                  scrolled to, not found in the DOM
    collapsed context hides text  a match inside a hunk nobody has expanded
                                  either does not exist or expands it, and
                                  those are different features

Per the keyboard mandate: cmd+F has to be claimed in the menu bar as well, or it is a key equivalent nothing owns — the same shape as the paste finding.

## 113. Dragging a divider near the top moves the window

Grabbing the accessory column's divider at the top of the window moves the _window_ instead of resizing the column.

The cause is almost certainly the drag region rather than the divider. The top bar wears `electrobun-webkit-app-region-drag`, and Electrobun's preload decides by walking up the DOM:

    target.closest('[style*="app-region"][style*="drag"]')
    target.closest(".electrobun-webkit-app-region-drag")

Anything interactive inside that bar has to wear the `-no-drag` counterpart or the native side claims the pointer before the renderer sees it — that is already why every button in the bar is marked. A divider that reaches up into the bar's band, or sits under it, has not been marked.

Two candidate fixes, and they are not the same:

    mark the divider no-drag       it stays resizable all the way up, and the
                                   window loses a strip of its drag handle
    stop the divider at the bar    the top few pixels are simply not a grab
                                   target, which is what the report suggests

The second is probably right: a divider that overlaps the title bar is a target a person hits by accident when reaching for the window. Worth confirming which is happening first — read the divider's rect against the bar's, rather than guessing at the z-order.

## 114. Say so when something is created or archived

Reported from a real window: "we need toasts especially when things are created or archived idk all the other times but its hard to know when things happen rn".

The window is quiet about its own work. Archiving a thread makes four rows disappear and says nothing; starting one puts a job in a panel that may be folded away; importing a project changes a list somebody is not looking at. Each of those is a gesture whose only feedback is the absence of what was there.

The jobs panel is not the answer to this and never was. It answers "what is running", which is a question somebody asks on purpose — a toast answers "something just happened", which nobody asks and everybody needs. The status bar is closer, and it is deliberately silent when there is nothing to say, so it cannot be the place a one-off announcement goes either.

What to work out when this is picked up:

- **What earns one.** A thing a person did that changed something they can no
  longer see: a thread archived, a project imported or forgotten, a review
  started, comments sent. Not a job step — that has a panel.
- **What it says.** The noun and its name — "archived tabular exports" — and
  an undo where one exists. Archive is the obvious first: it is the only
  gesture in the window that removes rows.
- **Where it goes.** Above the footer, right, over the accessory column; it
  must not cover the native webview's rectangle, which does not stack (see
  CLAUDE.md) — or if it does, the same overlay count the modals use.
- **How it leaves.** Animated, per the window's mandate: nothing pops.
  Reduced motion means none.

Base UI ships a Toast, which is the answer to whether to hand-roll one.

## 123. One thread, many repos, one agent each

Asked directly: is a thread to many agents and many repos viable, one agent per
repo? **It is the model already** — what is missing is a way in, a way for the
agents to see each other, and a way to say one waits on another.

What exists today, none of it new:

    thread_members    (project, workspace) pairs, UNIQUE on the pair — so a
                      thread already spans repositories and a workspace
                      belongs to exactly one thread
    sessions          one per kind per workspace: agent · editor · action
    thread_prs        several pull requests per thread, one thread per PR
    chat              one ACP conversation per workspace, keyed by directory
    create-workspace  takes `input.thread` and claims the workspace for it

    thread  "tabular exports"
      ├── rowan/tabular-exports   agent · editor · action   → PR #412
      └── beta/tabular-exports    agent · editor · action   → PR #98

So one agent per repo is not a decision to make; it is what a workspace _is_.
Three things are actually missing, and they are worth doing in this order.

### 1. Adding a repo to a thread — landed

`ThreadStart` takes an optional `thread`, and the cmd+N modal picks several
projects: the first call makes the thread, each one after it names the id the
first returned. Four things branch, each argued in the handler — no thread is
created, the base is this project's own trunk, the workspace takes its
sibling's name and prompt so a thread reads as one piece of work, and the
existing thread's lineage is passed through rather than re-read.

Two things left as they are, deliberately:

    a stack in one repo   refused by name. Two workspaces in one repository
                          for one thread is a real thing to want and is not
                          what "add this project" means — the create would
                          land on the directory the sibling occupies
    the loop is in the    each call after the first needs the id the first
    window                returned, and each workspace is its own job, so a
                          partial failure is one row failing rather than an
                          all-or-nothing create

### 2. The agents cannot see each other

One conversation per workspace, each with its own context. Nothing tells the
api agent what the frontend agent decided, so two agents on one piece of work
are two pieces of work that happen to share a title.

    a shared brief   put the thread's description and its sibling workspaces
                     into each opening brief. One line of work, one-way, and
                     stale from the moment it is written
    MCP (#93)        each agent ASKS what its thread holds — sibling
                     workspaces, their PRs, their diffs, the review comments
                     left on them. Read-only first

The second is the answer and it is already a task. This is the strongest
argument for #93 there is: without it, "many agents, one thread" is a claim the
sidebar makes and nothing else honours.

### There is no captain, and there is no one agent over many repos

Both were asked directly and they are the same argument, which is why they are
written down together: **each would be a thing with no workspace.** Every
per-workspace thing in this window is keyed by one — `WorkspaceFacts`, the diff
panel, the PR panel, the bookmark, `sessionName`, the address in the URL — so
an agent that spans repositories has no row to be drawn in, and gets drawn as
belonging to one of them while invisibly touching the rest. That is a lie the
window would be telling.

**A captain would also be a second copy of what the store already holds.**

    what the work is        the thread record: title, members, PRs, lineage
    what each agent is on   WorkspaceFacts, and the chat's turn state
    what must happen next   the jobs runner: ordered steps, resume,
                            compensation
    what a person wants     the person, sitting in the window

A captain's understanding lives in a context window that dies with its
session; the thread's lives in sqlite. This file's own rule covers the rest: a
client re-deriving the rule is a second implementation, and the copy that
drifts is the one nobody tests. A captain summarising the api agent's work for
the frontend agent is that copy, and it has to summarise work it has no context
for.

**What the agents actually lack is sight, not a chairman.**

    captain   api agent → captain → summary → frontend agent   three hops,
                                                               one of them lossy
    MCP       the frontend agent asks the daemon what its thread holds

Which is (2) above, and it closes the only thing one-agent-many-repos was
winning on — a single context that knows both sides.

**Real ordering is a rule, not a judgement.** "The api PR lands first" belongs
in the store with a step that waits, in the runner that already resumes and
compensates. Anything a captain would do that must survive a restart has to be
there anyway: a step resumed by a restarted daemon has only its record.

### When one agent over several repositories is right anyway

It is not never, and the case is specific: **when it is honestly one edit that
touches several repositories.** Rename a field in the shared contract and fix
the two consumers — one intention, done in order, nothing to coordinate. That
is not two conversations.

So the question is per thread rather than global:

    one change across repos     one agent. It is sequential anyway
    two changes, one outcome    an agent each. "Add the exports to the api"
                                and "show them in the ui" are two
                                conversations with two branches and two PRs

Left unbuilt until somebody hits the coupled case and wants it. When they do,
the session side is small — `session/new` takes `additionalDirectories`, which
the adapter advertises in `sessionCapabilities` — and the work is the
**sidebar's**: deciding how to draw one agent sitting across two rows. Which is
the same unanswered question as the captain's, and the reason neither is built.

The conditions that would change this: more members than a person can hold in
their head, work running unattended overnight, or a decision that genuinely
needs a model rather than a rule. None of them hold yet.

### 3. No member waits on another

"The frontend change lands after the api PR" has nowhere to live. `parentId`
records lineage _between_ threads, not order _inside_ one. A field on
`thread_members` is the cheap version; what makes it worth having is something
that reads it — a row that says "waiting on beta/tabular-exports" rather than
looking idle.

## 119. Own the agent's terminals, so a long command is watchable and killable

A command the agent runs for two minutes is, in the chat, a row that says `…` and then eventually says something. It cannot be watched while it runs and cannot be stopped.

**ACP hands this to the client, and this window is unusually well placed to take it.** Five methods, all client-side:

    terminal/create          the agent asks THIS process to run a command
    terminal/output          what it has written so far
    terminal/wait_for_exit   how it ended
    terminal/kill            stop it
    terminal/release         let it go

And a tool call's content can be `{ type: "terminal", terminalId }` — a live handle rather than a string of output, so the row in the chat _is_ the running command rather than a record of one that finished.

**It only happens if we ask for it.** The client declares capabilities at `initialize`, and chat.ts currently declares `fs` only:

    clientCapabilities: { fs: { readTextFile: false, writeTextFile: false } }

With no `terminal`, the agent runs the command itself and we see the output when it is done. That is today's behaviour and it is a choice made by omission, which is exactly the shape this repo has been caught by before — see the note in AGENTS.md about a default that reads as "on".

**The reason to take it is that the machinery exists.** `PtySpawner`, `Sessions` and the pane are already here, and `Scope` in `spawn`'s return type already means "the process gets killed". A terminal the agent asked for is a pty with a different caller.

What has to be decided, and none of it is obvious:

- **Where it is drawn.** Inline in the chat row is the honest place — the
  command is part of the turn — but a pane inside a scrolling transcript is
  a layout problem, and the accessory column already holds panes.
- **What killing it means to the agent.** `terminal/kill` ends the command;
  the agent then reads the output and decides what to do. A person killing a
  command mid-turn is a thing the agent has to be told about, and the only
  channel is the tool result.
- **Permission.** Today a destructive command is refused through
  `session/request_permission`, which the chat already draws. Owning the
  terminal means the command arrives here to be _run_, so the refusal has to
  happen before we spawn anything rather than after.
- **Scope and leaks.** A terminal is created by one call and released by
  another, and an agent that dies between them leaves a process. The
  conversation's own Scope is the natural owner, the same way the adapter
  process is.

Do not start this before the permission path has been exercised by hand: this
moves execution into the daemon, and the daemon is the process holding a
person's repositories.

Related: #91 (the chat, done), #115 (config strip), #63 (running a
workspace's services — a different long-running-process problem with some of
the same answers).

## 120. Electron's 317MB runtime must not land in every workspace

The same shape as #112, which removed a 306MB ACP adapter from every checkout — and this one is bigger and was two lines from being shipped.

**What is true today.** Electron installs, its binary does not. Bun refuses to run a package's postinstall unless it is named in `trustedDependencies`, and electron's postinstall is what downloads the runtime. So `bun run dev` builds the main process and then has nothing to launch, which reads as the app simply not appearing rather than as a missing step.

    node_modules/.bun/electron@44.0.0/…/electron/   install.js, cli.js, index.js
                                          /dist     absent until install.js runs
                                                    317MB extracted, 128MB cached zip

Adding `trustedDependencies: ["electron"]` fixes the launch and creates the real problem: the create-workspace job's bootstrap hook runs `bun install` in every workspace it makes, and the field is repo-wide, so **every workspace would extract its own 317MB**. It was added, measured, and taken back out for exactly that reason.

**Electron v44's installer has no skip flag.** Checked rather than assumed — the whole set of switches it honours is:

    electron_config_cache · force_no_cache · ELECTRON_INSTALL_ARCH
    ELECTRON_INSTALL_PLATFORM · ELECTRON_OVERRIDE_DIST_PATH
    electron_use_remote_checksums · npm_config_{arch,platform}

No `ELECTRON_SKIP_BINARY_DOWNLOAD`. So the per-install opt-out that older versions had is not available, and `trustedDependencies` is all-or-nothing.

**The shape that fits, and it is the user's own:** a workspace is web-only, and only the main line runs the native shell. That is already how this repo says to look at a branch — the second-instance section in AGENTS.md points at Vite and a browser tab, because a tab answers the layout, the theme, the panels, the pane's own rendering and every call over the socket. What a tab cannot answer is the native webview, the menu, and the window's own chrome, and those are exactly the things somebody opens the main line for.

So:

- **Leave `trustedDependencies` out**, which is where it is now. Nothing
  downloads a runtime as a side effect of making a workspace.
- **One command on the main line**, run once per machine, that resolves
  electron's package directory and runs its `install.js`.
  `ELECTRON_OVERRIDE_DIST_PATH` is the other half worth looking at: one
  extracted copy pointed at from everywhere beats one per checkout even on
  the main line.
- **`dev` should refuse with that sentence** when the binary is missing.
  The current failure is electron-not-found, three lines into a build, and
  it took a measurement to work out what it meant.

Related: #112 (the same 306MB lesson, one package earlier), #40 (the bootstrap
hook that runs `bun install`), #103 (what a workspace starts on its own).

## 125. cmd+click a thread to open it in a second window

Asked for directly: cmd+click on a row in the sidebar should open that thread
in a new window rather than moving the selection in this one.

cmd+click is the platform's own "open elsewhere", so nothing has to be
explained — and the shape it wants already exists. Selection is an **address**
(`/w/$project/$workspace/$kind`, see `address.ts`), the renderer is served over
`app://` in a build and from the dev server in development, and a window is a
`BrowserWindow` the main process makes. So a second window is the same renderer
opened at a different hash, and the daemon is already a separate process that
outlives any of them — which is the reason this is cheap here and would not
have been in a design where the window owned the sessions.

Three things to decide, and two of them are traps:

the pane a session takes its size from whoever is looking at it, and two
windows attached to one session is two clients with two
viewports. AGENTS.md records what that costs — a terminal
reflowing under somebody as a probe ran. Whether the second
window attaches, or opens on the chat face, is the first
question and it is not a detail
localStorage every window preference is keyed globally today: the columns,
the appearance, the panel per thread. Two windows sharing one
origin share all of it, and the mirror of the address
(`amoeba.place`, read once at launch) is written by whichever
window moved last
the count nothing tracks windows. `overlays.ts` counts modals per window
and the web panel's native view belongs to a window id, so a
second window is a second set of both

Smallest honest first step: cmd+click sends the address to the main process,
which opens a `BrowserWindow` at that hash and does not attach the pane —
opening on the chat face, which needs no pty and no size. The terminal in a
second window is a separate decision with a measurement attached to it.

## 126. The chat has to show a running shell

Asked for directly: "acp chat needs to show running shells". A long command in
the chat face is a tool call that sits at `in_progress` with its output arriving
only when it ends — so a `bun install`, a test run or a dev server started from
the chat is a row that says nothing for minutes and then says everything at
once. The terminal beside it has the opposite problem and none of this one: it
shows every line as it lands and cannot be asked what it is doing.

What is known, before deciding anything:

- **A tool call is already a patch stream.** `tool_call` then several
  `tool_call_update` sharing one id, merged by `fold` — so partial output has a
  place to go without a new update kind, if the adapter sends it.
- **Whether it does is the question to measure first.** `probe:chat` runs a
  real adapter against a temp directory and prints every update for a turn;
  the check is a command that writes slowly (`for i in 1 2 3; do echo $i;
sleep 2; done`) and whether `output` grows across updates or arrives once at
  `completed`. Do not design against a guess here — the last two things
  believed about this wire were both wrong, in opposite directions.
- **#119 is the neighbouring task, not the same one.** That one is about awp
  owning the terminals a _terminal_ agent spawns, so a long command is
  watchable and killable. This is the chat's own rendering, and the killing
  half may well end up shared with it.

The shape that follows if output does stream: the row keeps its disclosure and
opens itself while `in_progress` — a live command is the one output somebody
wants without asking, which is the same argument that opens a failed call's
output today. If it does not stream, this becomes a question about the adapter
rather than about the panel, and the honest interim is to say `running` with
the elapsed time, which the row already does past ten seconds.

## 127. A window chord pressed inside the web panel reaches nothing

The web panel is a real `WebContentsView` — a separate `webContents` — so while
somebody is reading a page in it, the keyboard belongs to _that_ process. Every
window-level chord is therefore inert there: cmd+P, cmd+N, cmd+shift+N, and the
column chords. Nothing about it reads as a shortcut problem from the panel's
side; the keys simply do nothing, which is what a broken binding looks like.

Measured, and worth knowing before designing: cmd+P from inside the **pane**
does work — the emulator installs its keydown on its own container in the
bubble phase, and the window's listener is capture-at-window, so it is decided
first. The panel is a different failure with the same symptom, one process over.

The shape that fits what is already here: `before-input-event` on each guest
view in the main process, and for a chord the window owns, focus the window's
own webContents and replay the key into it with `sendInputEvent`. That needs no
new renderer code at all — the existing capture listener would see it — and
`sendInputEvent`'s synthesised `code` is the thing to verify first, because
this window's shortcuts are matched on `event.code` and not `event.key`.

The alternative is a dedicated channel and an action the renderer performs, and
it is worse for the reason `awp_browse` is worse than a second RPC would be:
every chord would then have to be listed twice, once as a key and once as a
message.

Related: the annotator's note box needed `CH.focus` for the mirror image of
this — a control the _renderer_ draws because of a click that happened in the
page.

## 128. Panels as rearrangeable tabs, with a layout per thread — and named modalities

Andrew's, 2026-09-10, written down before it is designed. Today the window's
shape is fixed: an agent column with the chat or the pane in it, and an
accessory column whose panels are a tab strip in an order this repo chose.
The proposal is two steps, and the second is the interesting one.

**Rearrangeable, and remembered per thread.** Every panel — chat, pane, diff,
PR, jobs, tasks, web, style guide — becomes a tab that can be moved between
the two columns and reordered, and the arrangement is a property of the
thread rather than of the window. A review thread wants the PR beside the
diff; an implementation thread wants the pane beside the chat, and neither
wants the other's furniture.

**Then: saved configurations, one per kind of work.** The stronger half of
the idea, because it says the layout is not a preference but a claim about
what the session is _for_:

```
  research        chat · web · tasks            reading, and writing down
  architecture    chat · diff · tasks           shape, not lines
  implementation  chat · pane · diff            write, run, look
  code review     PR · diff · chat              somebody else's argument
```

A thread would open in the configuration its work implies — a review thread
already knows it is a review — with the arrangement editable from there and
kept if it is changed.

Three things that already exist and point at this, and one that fights it:

- The PR tab is **already conditional** on the thread naming a pull request,
  and the argument for it is exactly this one: "a permanent empty room in the
  column somebody switches most costs a keystroke every time". That is this
  idea applied once, by hand, to one panel.
- The chat/pane split is **already a per-thread choice** — `Face` is on the
  wire and on the create job, because the face somebody picked in the form
  decided which agent got briefed. So "what is in the agent column" is
  already thread state; this generalises it to the accessory column and to
  order.
- `ThreadStart` already carries an intent — the description, the base, the
  face — so the moment a configuration would be chosen is a moment the window
  is already asking about the work.
- Against: **Base UI unmounts a hidden tab**, which is why the inbox needed
  an atom and why the web panel is `keepMounted`. A layout with more panels
  visible at once is more mounted at once, and two of them are native views
  and a wasm terminal. Whatever this becomes has to say which panels may be
  live simultaneously, or it is a design that gets slower as it gets better.

Open questions worth settling before any of it is built: whether a
configuration is a **preset** (a starting point, then the thread owns its
own) or a **binding** (the thread points at a named configuration and edits
change every thread using it); whether the layout is per thread or per
(thread, workspace), since a thread holds several checkouts; and where it is
stored — a thread record on the daemon makes it follow the work to another
machine, `localStorage` makes it this window's, and the existing split says
per-window for `amoeba.place` and per-thread-on-the-daemon for everything
that is a claim about the work.

## 129. Two faces, two jobs: the TUI for popping in and out, the window as the IDE

Andrew's, 2026-09-10, written down beside #128 because they are halves of
one question — what each face is _for_ — and answering one without the
other will produce a window and a terminal that disagree about it.

The claim: the TUI is not a small version of the window. It is the face
somebody uses from a terminal they are already in, to see what an agent is
doing, say one thing to it, and leave. The window is where the work is
actually done — the diff, the pull request, the browser, the tasks, the
layout of #128.

```
  TUI      list · open · read the last answer · say one thing · leave
           seconds. No layout to arrange, because there is nothing to
           arrange: one column, one conversation
  window   the IDE. Panels, a terminal, a diff, a page, a review, and a
           shape per kind of work
```

What follows if it is taken seriously, and each of these is a decision
this repo has already half-made in one direction or the other:

- **The TUI stops growing panels.** No diff panel, no PR panel, no browser.
  What it grows instead is speed to a conversation — the thread list
  ordered by activity landed for exactly that reason, and a fuzzy jump
  straight to a thread would be the next.
- **A vocabulary is shared; a surface is not.** The tool-name rule, the
  slash commands and the spinner frames are in the contract package on the
  argument that two faces must not disagree about what a thing _is_. That
  argument does not extend to what each face draws — the TUI already drops
  the label column the window keeps, and that is right rather than a drift.
- **The window may assume it is on a desktop**, and the TUI may assume it
  is over ssh. Which is the honest reading of why the copy path has two
  routes: OSC 52 exists for the second case.
- Against, and worth arguing before committing: the TUI is currently the
  only face that works from a machine somebody has ssh'd into, and
  "everything real happens in the window" makes that a second-class way to
  work rather than a deliberate one.

Open: whether the TUI keeps a diff at all — reading a patch is arguably a
pop-in-and-out act — and whether "leave" should mean detach rather than
quit, which is a zmx question and not a UI one.

## 132. Take a queued message back — and what a real adapter says about that

**Measured before building, and the premise did not survive it.** `bun run
probe:steer` now drives two ordinary sends behind one slow turn, and the first
line is the whole finding:

```
  send answered   prompt in 1ms · prompt in 0ms
  turns           started → started → started → ended → ended → ended
```

A message typed mid-turn is **not** held by this window or by the daemon. It
goes as a plain `session/prompt` the moment Return is pressed, and the
_adapter_ queues it at priority `next` — which is exactly the design
`ChatSend.interrupt` argues for, because the adapter's own comment says the
check and the push "stay in one synchronous section so the turn cannot settle
in the gap". `queued` is a mark on a message somebody else already has.

So "the draft returns to the box, the queued item leaves the transcript" cannot
be honoured. There is no selective retract:

```
  session/cancel        settles EVERY queued turn as cancelled — and the
                        running one with them. Read in the adapter's source:
                        `for (const queued of session.turnQueue)`
  a per-prompt cancel    does not exist in ACP
```

Popping the row back would empty the box, remove the row, and leave the agent
answering it a minute later — a gesture that reports success and is undone by
the thing it was meant to undo. That is worse than not having it.

What is still worth having, and none of these is this task:

- **Up recalls the last message you sent, for editing.** The shell idiom, and
  it is true whatever the adapter is doing: it does not claim to un-send, it
  offers the text again. Costs one keypress and a `lastSent` on the panel.
- **Cancel and re-send.** `session/cancel` settles everything, so the queued
  one can be put back and the rest re-sent. Honest, and it throws away the
  answer in flight — which is the thing somebody typing a correction is
  usually reading.
- **Hold it here instead**, so it really is retractable — and that is a
  deliberate _reversal_ of the current design, not an addition to it. It puts
  the settle-in-the-gap race back on this side, which is the one thing
  `idleBehavior: "promptRequired"` exists to avoid. It needs an argument this
  task does not make.

Related: #133, which is what came out of the same measurement and is done.

## 134. A thread on a project's default checkout

Wanted for the wiki, and not in general: `~/wiki` is a project with one checkout — its own root — and no reason to ever have a second. Everything in this window is addressed by `(project, workspace)`, and a workspace is by construction `~/.awp/workspaces/<project>/<name>`, so the project's own root cannot be named at all.

```
  workspacePath(project, workspace)   ~/.awp/workspaces/<project>/<workspace>
  the wiki's actual checkout          ~/wiki                 ← nothing composes this
```

Fifteen callers compose that path, and they do not all want the same answer, which is the whole of the work:

    read      WorkspaceDir · the chat's cwd · a thread's checkout dir ·
              starting a session again        → must answer the project's root
    write     create-workspace                → must refuse the name outright
    destroy   archive-thread                  → must refuse it twice over.
                                                `jj workspace forget` on a
                                                default workspace and an `rm`
                                                of somebody's repository are
                                                the two worst outcomes here

So it is a reserved workspace name that resolves through the project record — the root is on it already — rather than a pure function gaining a branch. The refusals are the feature, not the resolution.

Then a thread claiming that pair gives the wiki a sidebar row, a chat, a diff and a tasks panel with nothing else built: `ThreadAttach` takes a member that already exists, and the default checkout always does.
