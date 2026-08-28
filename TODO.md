# TODO

Every task that is not finished, written out so that losing the task list
does not lose the reasoning in it. The list itself lives in the session; this
file is the copy that survives.

**A task here is an argument, not a ticket.** Most of the value is in the
paragraphs — what was measured, what was tried and did not work, and which
choice is the one that matters. Read the body before starting, and update it
rather than the summary when what you learn changes the shape of the work.

Regenerated wholesale. Do not hand-edit a single entry expecting it to
survive — change the task, then write this out again.

47 open, 71 finished, as of 2026-08-28.

In progress: #91.

---

## 18. Delete the probes once the skeleton measures itself

Deferred deliberately, twice now. The probes still here are the ones AGENTS.md tells a future session to run — child-env, claims, jobs-store, workspace, thread-parent — and the three that look spent (zmx-pty, reflow, wheel) are the only way back to a question that is hard to ask any other way. Deleting working diagnostic tooling needs a reason better than tidiness; wants Andrew's call.

## 21. Design one theme for the app and the terminal together

The pane currently borrows Catppuccin Macchiato/Latte and the chrome derives a few roles from it, which is two palettes pretending to be one. Build a single theme: chrome roles and the terminal's sixteen ANSI slots designed against each other, in both light and dark.

The constraint learned the hard way on 2026-08-25: a terminal theme is not decoration, because programs choose their own colours against the background they assume. Our base was Catppuccin's crust rather than base — two steps too dark — and Claude Code's truecolor #373737 message block went from a subtle lift to a stark slab. Whatever we design has to state a background that programs can reason about, and the ANSI slots have to stay recognisable as themselves: a pane and a sidebar row showing the same status must not be two different greens.

## 41. Keep a thread's bookmark at its tip

Nothing moves andrew/<name> forward as commits land, so a bookmark sits at the FIRST commit of its branch. Measured: andrew/awp-kit-amoeba is at lmpznzxr "chore: move existing tree into archive/", 51 commits behind the workspace's working copy. test1234 branched from exactly that and landed 51 commits behind; diff-view did too and was rebased by hand afterwards (op log shows two "point bookmark andrew/diff-view" entries). So "base this thread on that bookmark" gives the start of the work rather than the current tip. Decide between moving the bookmark on commit and resolving the base to the workspace's tip.

## 42. Show what the agent is doing on a sidebar row

The dot only says "the session exists". The strip has no signal of activity, so at 26 rows the eye has nothing to land on — the one remaining item from the sidebar review. The Go implementation solved this with agent status reporting (archive/specs/20260429-i021-agent-status-reporting-spec.md): claude hooks write status, awp reads it. Needs a decision on the channel — hooks writing to a file, or ACP once #37 lands, which would carry it natively. Blocked on that choice rather than on the rendering.

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

## 57. cmd+P: pick a thread

A palette that jumps straight to a thread, and to a workspace within it. The sidebar already nests workspaces under threads, so the list exists; what is missing is a way to reach one without a pointer and without scrolling. Base UI's dialog and the existing address (/w/$project/$workspace/$kind) are both already there, so this is mostly a filter and a keyboard contract. Note the ctrl vs cmd split: cmd is free because the pane wants ctrl.

**The first row is the thread you were last on, so cmd+P then Return is a
toggle back.** That is the behaviour every editor's file picker has and the
reason people press it without reading: the most likely destination is the one
you just came from, not the one nearest the top of an alphabetical list.

Which means the picker's default order is by recency of visit, not by name, and
the window has to remember the previous address as well as the current one —
one more value beside the route, and it has to survive a reload the way the
current address already does.

## 58. cmd+shift+P: run an action

The command half of the palette — everything the window can do, by name, with the keyboard: fold a column, start a thread, send a review, clear finished jobs, switch appearance, open a panel. Shares the dialog and the filtering with the thread picker (#57) and differs in what it lists and what choosing one does. Worth deciding early whether actions are a registry each feature adds to, or a list assembled in one place; the first is the only one that stays correct as features land.

## 59. cmd+comma: a configurator for settings that exist

Settings are read from a config file by the daemon (Settings in packages/server) and there is no way to see or change one from the window. bookmark_prefix is the worked example: it decides what a thread branches from, it is invisible, and its absence silently changes behaviour (baseOfThread falls back to <name>@). A settings surface needs the contract to carry the settings both ways, which does not exist yet — so the first question is which settings are genuinely a person's to set, rather than building a form over whatever the file happens to hold.

## 60. Give the diff's splitter the pane's icon, and let it animate

The revision-list boundary in Diff.tsx uses CaretUp/CaretDown on its peg, where the column folds use SidebarSimpleIcon (mirrored for the other edge). Two controls that do the same job — fold the thing next to me — should read the same, so this wants the sidebar glyph turned ninety degrees. And it should move: folding a column eases over FOLD_MS with a reduced-motion opt-out (styles.eased in App.tsx), and the revision list snaps. Reuse the same duration and curve rather than picking new ones.

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

That is zmx's own record of what happened to a session — starts, attaches, exits — which is a different thing from `Multiplexer.history`, which is the *scrollback* the program wrote. When a session dies for no visible reason, the scrollback is empty and the log is the only place the reason is.

So: a panel in the accessory column that reads the log for the selected session. Needs

- `Multiplexer.logDir` over `zmx version` (parsed the same way the rest of zmx's tab-separated output is), rather than hardcoding a path that is XDG-dependent
- a `SessionLog` call, tailing rather than reading whole — one of those files is already tens of megabytes
- the reading to be safe: a log path is composed from a session name, so it must resolve inside the log directory and nowhere else

Wanted alongside the services work — a dev server that exited on its own is exactly the case where the scrollback says nothing and the log says why.

## 68. Dropping a file on the pane types its path

Drag an image (or any file) onto the terminal and its path should arrive at the program, the way it does in every other terminal. Today the drop does nothing — or worse, the webview navigates to the file, which replaces the window with the image.

Screenshots are the case this is actually wanted for, and they have a wrinkle of their own — see the last point.

Three things this needs, and the third is the one that bites:

- **`dragover` must be cancelled**, on the pane's host and probably on the window. A webview's default action for a dropped file is to navigate to it. Without `preventDefault` on `dragover` the drop event never fires at all, and without it on the window a miss outside the pane throws the whole renderer away.
- **The path, not the contents.** `DataTransfer.files` gives a `File` with no path — the browser deliberately hides it. Electrobun's preload may expose one (`webkitRelativePath` is not it); if not, this needs a small addition on the native side, which is exactly what `apps/amoeba/src/bun` is for. Worth checking `event.dataTransfer.items` for a `text/uri-list` entry first, which carries a `file://` URL that resolves to a real path with no native help at all.
- **Quote it.** A dropped path routinely has spaces — `~/Desktop/Screenshot 2026-08-27 at 16.04.11.png` — and typing that unquoted into a shell is two arguments. Shell-quote before sending, the same way a terminal that supports this does.

**A macOS screenshot may not be a file yet.** Dragged from the floating thumbnail that appears in the corner after cmd+shift+4, it is a *promised* file: the drag carries a promise the receiver has to accept before anything exists on disk, and there is no path to read. Dragged from Finder or the desktop after it has saved, it is an ordinary file and the routes above work.

That is worth knowing before the feature is called broken. Two honest options: handle only real files and say nothing when there is no path — which quietly fails exactly the gesture somebody most wants — or accept the promise, write the bytes somewhere, and type *that* path. The second means the window is now creating files on a person's disk, which needs a decided location and a decided lifetime, so it is a bigger question than it looks. Find out which kind of drag arrives before designing for either.

Delivered through `Terminal.paste`, not `write`: paste wraps the text in bracketed-paste markers when the program has turned them on, which is what stops a path with a newline in it being run. Same reason `clipboard.ts` uses it. (Claude Code does enable bracketed paste — measured, `ESC[?2004h` is in the first line it writes.)

Several paths at once is a real case (drag three screenshots) — join with spaces, each quoted.

`packages/pane/src/` is where it goes, beside `clipboard.ts` and `dictation`, and for the same reason those are separate files: it is its own subject and does not belong in `terminal.ts`.

Measured the way dictation was, because both failure modes are invisible from outside: a probe that dispatches a real `DragEvent` with a `DataTransfer` and counts what the emulator received. A drop that does nothing and a drop that navigated away look identical in a screenshot taken afterwards.

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

## 74. The window collapses when Safari's Web Inspector docks

Opening the Web Inspector against the app makes the whole window shrink and go very short. Devtools-only, so it blocks nothing that ships — but it blocks *inspecting*, which is the tool for everything else, so it is worth understanding rather than working around by leaving the inspector undocked.

Candidates, in the order worth checking:

- **The inspector docks inside the window and the layout is correct.** `html`, `body` and `#root` are `height: 100%`, so a shorter viewport gives a shorter app — which is right, and would look like this. Rule it out first by measuring `document.documentElement.clientHeight` with the inspector open and closed: if it dropped by roughly the inspector's height, nothing is wrong except the expectation.
- **`100%` resolving against something that collapsed.** `100%` needs a definite height on every ancestor. `global.css` pins all three, so this should hold — but the moment one of them is `auto`, the chain resolves to content height and the window "gets really short" exactly as described. Check the computed height of each of the three, not just the root.
- **Electrobun positioning the WKWebView by frame.** The window is `hiddenInset` and the native view is placed by the host. If the inspector changes the content bounds and nothing re-lays-out, the view keeps its old frame — which is the same class of bug as `OverlaySyncController.sync()` returning early on a zero rect, already documented for the web panel.

Related and worth doing alongside: nothing exposes `isInspectable` today, so check how the inspector is being attached at all — on macOS 13.3+ a WKWebView is not inspectable unless the flag is set, and if electrobun sets it only in dev that is the switch to find. See #73.

Measure with the probe rather than by eye: `page.evaluate` the computed heights of `html`, `body`, `#root` and the window's own `innerHeight`, since three of those can disagree and only one of them is what a screenshot shows.

## 75. Give every button a hover tip

Reported against the diff panel and generalised: all buttons. Audited across the renderer, seventeen have no `title`, and five of those have no `aria-label` either — so they are unexplained to a pointer *and* unnamed to a screen reader:

    Boundary  2      Jobs      4      Meter    2
    Diff      6      Sidebar   1      Web      1
    ImportProject 1

Several are icon-only, which is the case where a tip is not a nicety: an icon with no text and no tip is a control whose meaning has to be discovered by pressing it, and some of these are not things to press speculatively.

Rules for what a tip says, so they are worth having:

- **Name the effect, not the widget.** "hide the panels", not "toggle". The existing ones in `Bars.tsx` and `Sidebar.tsx` already do this and are the model.
- **State-dependent where the control is.** A toggle's tip says what pressing it will do *now* — "show the sidebar" versus "hide the sidebar" — which is the only way one glyph can carry two meanings.
- **Say the cost where there is one.** A destructive or outward-facing action's tip is the place to say so, in the way `MoveToThread`'s forget tip says "nothing else is removed".
- **A keyboard shortcut belongs in it**, where one exists — `new thread (⌘N)` is already the pattern.

`title` rather than a tooltip component: it is what the window already uses, it needs no library, and the alternative is a fourth thing in a stack that AGENTS.md says not to add to. Where a button is icon-only it also needs `aria-label`, which is a different job — `title` is a hint and `aria-label` is the name.

## 79. Let a diff hunk expand its collapsed context

@pierre/diffs supports this natively and we pass none of it: `collapsedContextThreshold`, `expansionLineCount`, `expandUnchanged`, and `FileDiff.expandHunk(hunkIndex, 'up'|'down'|'both', count?)`. `HunkData.expandable` carries `{chunked, up, down}` and the icon sprite already ships `diffs-icon-expand` and `diffs-icon-expand-all`, so the affordance is drawn for us.

The catch is the patch, not the library. `jj diff --git` emits three lines of context, so expansion has nothing beyond that to reveal unless `loadDiffFiles: FileDiffContentsLoader` is supplied — a callback that fetches both whole sides of a changed file. That is a new RPC (file contents at a revision, both sides) plus the loader wiring, and it is the actual work here. Without it, expansion only reaches the ends of what the patch already carries.

Also check whether the default `collapsedContextThreshold` already draws separators we are simply not noticing in a 200px column.

## 81. Let the new-thread modal grow with the prompt

The cmd+N dialog's textarea is a fixed height, so a long prompt scrolls inside a small box while most of the modal is empty. It should grow with the content, to a point.

Bounded, not unbounded — a dialog taller than the window has nowhere to go, and Base UI's dialog does not scroll the viewport (see the no-top-level-scrollbar rule). So: `min-height` for the empty state, grow with content, `max-height` as a fraction of the window, and the textarea scrolls only past that.

Autosizing a textarea has one honest implementation and several that look right and are not: measure `scrollHeight` after resetting `height` to `auto`, on input. A CSS-only approximation (a grid with a mirrored `::after` holding the same text) avoids the layout thrash and is worth trying first since the composer is already a controlled component with the text in hand.

Was raised alongside "the modal a little small btw" — that one was fixed by widening to 46rem, and this is the other half.

## 84. A style-guide panel, roughly a small Storybook

A tab in the accessory column showing this window's own components in isolation: the sidebar row in each of its states, the buttons, the tab strip, the dialog, the error fallback, the tokens themselves.

Why it fits here better than a real Storybook: the whole argument of `tokens.stylex.ts` is that a colour is only meaningful against the ground it is painted on, and this window paints two grounds (light and dark) and a third the pane draws for itself. A style guide rendered *in* the window sees the real ones. A separate Storybook process would need the theme rebuilt beside it, which is a second copy of the thing being documented.

It also gives the contrast measurements a home. `bun run` has no gate for them and the probe that found "our white theme is soo white" lives in a scratch directory — a panel that lists each token against its ground with the computed ratio would put that on screen instead, and would have shown the failure without anyone thinking to look.

Notes before starting:
- A hidden Base UI tab unmounts, so anything expensive must not live in the panel's own tree (the worker pool learned this).
- Every state worth showing needs a fixture, and `fixture.ts` is already the model for that — it is built so each block fails visibly when a specific renderer fix is missing, which is the property to copy.
- The pane cannot be shown here: one Terminal per window, and a second writes into freed memory (see the note at the top of `terminal.ts`).
- Related: #75 wants a lint rule for button tooltips, and a panel that lists every button is where a person would notice one missing.

## 85. A second button on a task: start it in its own thread

The tasks panel's row has one button, Send, which briefs the agent already open in this thread. The second thing a person wants is the opposite: leave this thread alone and start a *new* one for the task — the task's subject becomes the thread name and its description becomes the prompt, so it is the new-thread flow with both fields already filled in.

That makes the panel a queue rather than a list: read the pending work, and either hand one to the agent in front of you or fan one out beside it.

Open questions, none of them decided:
  - the new thread's base. Probably the current thread's bookmark, since a task read out of this workspace usually follows on from it — which is what `baseOfThread` already resolves.
  - what happens to the task afterwards. A task started in another thread is no longer pending, but nothing here writes to the task store, and it should stay that way until there is a reason.
  - the label. "Send" and a fan-out glyph beside it, or two named buttons; the row is narrow and the second control is the rarer one.

## 87. A show-completed section on the tasks panel

The panel hides completed tasks and says how many there are — "24 to do · 62 done". The count is right for scanning, but it makes the finished half unreachable, and there are two reasons to want it: checking whether something was already done before asking for it again, and reading back what an agent got through while you were away.

So the count becomes a control. Clicking "62 done" reveals them, below the outstanding ones and visually quieter — dimmed, and probably in the order they were completed rather than by id, since the useful reading of a done list is most-recent-first.

Whether the disclosure is remembered per thread is an open question. `rememberedPanels` is the worked example of a per-thread preference, but this one is a glance rather than a mode, so it may be right for it to close again every time the panel is opened.

Related: [[a second button on a task: start it in its own thread]] (#85).

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

Hovering a revision that is *ahead* of where its bookmark currently sits reveals a control on the right; pressing it moves the bookmark to that revision. `Jj.setBookmark` already exists and its doc says "Create a bookmark, or move an existing one. Already idempotent in jj." So the operation is there; what is missing is an RPC and the decision about when to offer it.

**"Ahead" needs care.** The obvious rule is "above the bookmark's row in the list", and the list is newest-first over `@ | trunk()..@`, so for a linear stack that is right. It is a claim about list position and not about ancestry, and it is wrong the moment the stack forks. Either ask jj (`jj log -r '<bookmark>::<rev>'` is non-empty when one descends from the other) or state the limit in the tooltip. Do not silently offer a move that would rewrite history sideways.

Refuse rather than guess when a row carries no bookmark and the workspace has none — there is nothing to tug. A row that shows the control and then explains why it did nothing is worse than a row without one.

Which bookmark gets tugged is its own question. A workspace usually has exactly one, `<prefix>/<workspace>`, which is what `baseOfThread` already composes — so the control can name it rather than offering a picker. Two bookmarks in one stack is the case that needs a decision.

Related to #41, the automatic version: nothing currently moves `andrew/<name>` forward as commits land, so a bookmark sits at the *first* commit of its branch — measured at 51 commits behind on this workspace. A manual tug is the smaller answer and may be the better one: moving a bookmark is a decision, and a button says "now" without having to pick a policy.

One thing already established while doing #70, which matters here: a remote bookmark appears in a commit's `json(bookmarks)` when it disagrees with local. Measured — `andrew/awp-kit-amoeba@git` sitting one commit behind shows up on its own commit, carrying `remote: "git"`. The revision list now filters those out, so anything this reads is local; a tug must not offer to move a name that only exists on a remote.

## 91. Run the agent under ACP, not only in a terminal · in progress

Today amoeba could reach an agent exactly one way: bytes down a pty. That is why the tasks panel reads files off disk rather than asking, why "what is the agent doing" (#42) is inferred from the process table, and why a workspace whose window is closed loses everything but a scrollback. A terminal is a picture of a conversation, not the conversation.

## The spike, 2026-08-28

Four throwaway sessions in temp directories. Both questions carried over from #37 are answered, and two more were found.

**A session is a file, not a process.** SIGKILL twelve seconds into a running Bash loop; a fresh process, given the same id, replayed the history and answered from it.

    process A   session/new → "remember chartreuse"   SIGKILL
    process B   session/load  → replays 2 updates → "chartreuse"
    process C   session/fork  → a NEW id, also "chartreuse"
    process D   session/resume → replays 0 updates → "chartreuse"

    descendants of the dead pid   0        nothing orphaned
    the loop                      killed with it
    the transcript                tool call started, never completed
    asked afterwards              "it was never actually executed"

So a restart costs the turn in flight, not the thread; there is nothing to daemonize under zmx, and a pty is the wrong pipe for line-delimited JSON-RPC anyway.

**load replays, resume does not.** `load` sends the conversation back in the same update shape a live turn uses, so one renderer draws history and present alike.

**Do not guess the transcript path — ask.** `agent-tasks.ts` composes `~/.claude/projects/<slug>/` from the path handed in; the real slug is built from the **resolved** path, so on macOS the guess missed `/private` and found nothing while the session sat plainly in `session/list`.

**Tools are approved by a model unless the mode says otherwise.** `session/new` opens in `auto` — a classifier approving on the client's behalf. Six modes; in `default` (Manual) a read was not referred to the client and `rm` was, with reject_once / allow_once / allow_always.

**A tool call is five updates sharing one id**, not one — pending with a generic title, then the command, then the output, then completed.

## Landed

`packages/server/src/chat.ts` — one adapter per workspace through an `RcMap`, released when the last window goes; `session/list` to find the session for a directory, `session/load` to open it, Manual mode set explicitly; permission requests carried out to a person and answered by id. `ChatOpen` / `ChatSend` / `ChatAnswer` on the wire, the first a stream. `bun run probe:chat` proves it against a real adapter: the word came back, five tool updates merged to one id, and a second process replayed what the first had said.

`Chat.tsx` and `conversation.ts` — the panel and the fold, split because a file importing StyleX cannot be loaded by vitest. A toggle in the new-thread modal sets what a workspace opens as; one in the agent bar switches the workspace on screen. Both halves are always made, so the toggle chooses a view and never a capability.

## Left

  - **no turn boundary on the wire.** `stopReason` is dropped, so the panel
    cannot say "the agent is thinking" or draw the end of a turn. That is
    also what #42 wants — the sidebar's real state, asked rather than guessed.
  - **the session id is not recorded** against the workspace. Asking
    `session/list` per open is correct and cheap today; it is a linear scan of
    a directory with 146 entries on this machine and nobody has measured it.
  - **markdown** — #102. A model answers in it, and the panel draws it as text.
  - **fork, for a session somebody is sitting in.** `load` would make the ACP
    side a second writer on a transcript an interactive `claude` is still
    appending to. Offering "open this in the chat" on a running terminal
    session is the feature people want and it is a `fork` underneath.
  - **the adapter is a per-machine install** at `~/.awp/tools`, with a sentence
    naming the command when it is missing. Nothing installs it for anybody.

## 92. Add a task from the tasks panel

The panel shows the agent's list and can hand one back. What it cannot do is put something *on* it — so noticing a thing that needs doing while reading a diff means typing it at the agent in prose and hoping it lands as a task rather than as work started immediately.

The design question is the whole task, because the panel is deliberately read-only and this is the first thing that wants to write. Two routes, and they are not close:

    write the file    a new `<n>.json` in ~/.claude/tasks/<session>/
                      immediate, exact, and makes amoeba a second writer of
                      somebody else's store — which is the thing agent-tasks.ts
                      says it will not be, and what claude-trust.ts needed a
                      lock for. Also has to pick an id without colliding with
                      one the agent is about to use.

    ask the agent     send a prompt: "add this to your task list: …"
                      no second writer, no id to invent, and it goes down the
                      wire that already exists. Costs a round trip through a
                      model, may reword what was typed, and does nothing at all
                      if the agent is busy or gone.

The second is the honest one today and the first becomes reasonable the moment #91 lands, because ACP would give a real channel instead of a file. Worth deciding rather than drifting: a composer that sometimes writes a file and sometimes types a sentence would be two features wearing one button.

Either way the panel needs a composer — a field at the head, or an "+" that opens one — and it should take a subject and an optional description, since a subject alone is what makes a task list unreadable a week later.

Related: [[fuzzy search over the tasks panel]] (#89) and [[a show-completed section on the tasks panel]] (#87) are the other two things the head of this panel has to hold, and there is not room for three separate controls up there. Worth designing the head once.

## 93. An MCP server so the agent can drive the window

Every wire between amoeba and its agent currently points one way. The window can type at the agent — a review, a page note, a task — and the agent cannot say anything back except by printing into a terminal that amoeba only draws.

An MCP server the agent connects to turns that round. The daemon already holds everything worth exposing and already serves it over a schema-checked RPC; MCP would be a second face on the same handlers, aimed at the agent instead of at the window.

The example that makes it concrete: **the agent posting review comments.** Today a review flows window → agent. An agent that has just finished a change should be able to annotate its own diff — "this bit is the risky one", "this file is generated, skip it" — and have those appear in the diff panel as comments beside the lines. `ReviewAdd` already takes exactly that shape (revision, path, side, two line numbers, body), so the tool is nearly the RPC.

Others worth having, roughly in order of how obviously they are wanted:

    open a diff at a revision       "look at what I just did"
    open a page in the web panel    a preview, a failing CI run, a dashboard
    put a task on the list          the honest version of #92, without a
                                    second writer of ~/.claude/tasks
    read the review comments        so an agent can pick up remarks left for
                                    it without being told them again
    say what it is doing            the real answer to #42, volunteered

Two things to decide early, because both are hard to change later:

**Scope.** An MCP server the agent can reach is an agent that can act on the window, including a window showing a different thread. Every tool should be bound to the workspace the agent is in — the same shape as `-R` on every jj call, and for the same reason: there should be no call that reaches the wrong place by accident.

**Transport.** stdio per agent is the simple answer and means the daemon spawns a server per session. A single HTTP/SSE server on a known port, with the workspace as an argument, is one process but needs the binding above to be real rather than conventional.

Related: [[run the agent under ACP, not only in a terminal]] (#91) — that is the same gap approached from the other side. ACP gives amoeba a channel *to* the agent's conversation; MCP gives the agent a channel *to* amoeba. They are complementary rather than alternatives, and doing both is what makes the window and the agent one system rather than two looking at each other.

## Tasks are the first thing the MCP server should offer

Anthropic's position is that the task tool has stopped earning its place. That
is not the experience here: the tasks panel is one of the most-used things in
this window, and the Send button that briefs an agent from a task is the
feature that made it so.

So task management is the first set of actions on the awp MCP server, and the
point of putting it there rather than leaving it in Claude Code's own store is
scope. Today a task list belongs to a *session*, found by its transcript
directory — see `agent-tasks.ts` — which means a task cannot outlive the
session that wrote it and cannot be seen from anywhere else.

    now       one list per Claude Code session, on disk, found by mtime
    wanted    tasks awp owns, with a scope, visible across the whole window

**Scope is the design question, and tagging is probably the answer.** A task is
not always about one thread:

    thread     "make the discount tiers apply at checkout"
    project    "this repo still has no integration tests"
    global     "learn what jj fix actually rewrites"

A field with three values forces every task to pick one and makes the third
awkward. Tags do not, and they give the cross-cutting view for free: one panel
listing everything, filtered by whatever tag is interesting — a project, a
thread, or nothing at all for the whole board.

**What the agent should be able to do**, which is the actual MCP surface: list
with a filter, add, update status, and link a task to the thread it is being
worked in. Reading is the half that matters first — an agent that can see the
project's open tasks before it starts is the thing that stops it inventing
work already written down.

Related: the pending task about adding a task from the panel, which is the same
store from the other side.

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

**A warning about measuring this.** The first harness reported 4 of 8 stuck, in a perfect STUCK/submitted alternation — and that number is an artefact, not a rate. It cleared the input box only after a failure, so every trial after a success began in a different state from every trial after a failure. Any future attempt has to clear before *every* trial and wait for the previous answer to finish, or it will measure its own asymmetry.

Still unexplained, and the next things to try:

  - whether the agent being *busy* is the variable. Sending while a response
    is streaming is the obvious candidate now that the paste window is out,
    and it fits "sometimes" exactly: a review is sent at the agent that is
    still working.
  - what the box actually contains when it sticks. Reading the raw screen
    with `zmx history --vt` at the moment of failure would say whether the CR
    arrived and was swallowed, or never arrived at all — those are different
    bugs and nothing measured so far separates them.
  - whether a longer gap helps at all. Cheap to test now that there is a
    harness that does not lie: 0, 50, 150ms, twenty trials each.

## 96. The diff panel should remember what has been viewed

Reviewing fifteen files means losing your place fifteen times. The panel already knows which files are folded and which revision that was true of; what it does not know is which ones have been *read*, so coming back to a patch after an agent has pushed a change means starting from the top with no way to tell what is new.

The mark itself: a per-file "viewed" state, shown on the file header, with a way to clear it. GitHub's checkbox is the shape everybody already knows, and the useful half is not the tick — it is that a viewed file auto-folds, so the list collapses down to what is left.

Two decisions with real consequences:

**What invalidates it.** A file marked viewed and then *changed by the agent* is not viewed any more, and this is the whole value of the feature — it is what turns a diff panel into a review queue that drains. So the mark has to be keyed by content, not by path: the blob id from the patch's `index` line, or a hash of the file's hunks. Keyed by path alone it goes stale silently, which is worse than not having it, because it hides exactly the change a person needed to see.

**Where it lives.** The fold state is already remembered per revision in the renderer (`remembered.ts`), and this could ride along — but a review survives a restart and a fold does not need to, and it is a claim about work rather than a UI preference. The threads store is where a durable one would go, alongside `ReviewComment`, which is already keyed by revision, path and side.

Related: the existing `foldsFor(revision)` machinery is the thing to extend rather than duplicate, and #69 (diff the whole stack) changes what "a revision" means here, so the key should be able to survive that.

## 98. An open-or-create PR button in the diff head

A diff is read to decide whether the work is ready, and the next thing after deciding it is is opening a pull request. Today that means leaving the window.

So a button in the diff head row, in one of two states:

    facts.pr is set        "open PR #412"   — opens it in the web panel
    facts.pr is undefined  "create PR"      — briefs the agent to make one

**The signal is already there.** `WorkspaceFacts.pr` is on the wire — `Schema.UndefinedOr(Schema.Int)`, described as "the pull request this workspace's branch is on, if it is on one" — and the sidebar row already renders it as `#412`. So the button reads a value the panel can already see; nothing new crosses the wire for the first state.

**Open goes to the web panel, not to a browser.** The panel exists precisely so a thing being read against a diff stays beside it, and handing the URL to the system browser throws away the window this was built for. That needs the PR's URL rather than its number, which `facts` does not currently carry — either add it beside `pr`, or compose it from the remote, which is a guess about a forge and should not be made in the renderer.

**Create is a prompt, not a call.** Deliberately: opening a pull request means a title and a description written from the change, which is exactly what the agent is for and exactly what a hardcoded `gh pr create --fill` gets wrong. Same wire as a review or a task — `TaskSend` and `NoteSend` are the worked shapes — and it should ask for *next* rather than *now*, for the reason in `taskPrompt`.

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

Related: the head row is now wanted by [[a side-by-side toggle on the diff panel]] (#95), [[the diff panel should remember what has been viewed]] (#96) and [[a pop-out file tree beside the patch]] (#97). This is the fourth, and it is the widest of them — a button with words in it rather than an icon. The row needs designing once, for all four.

## 99. Separate with space and fill, not with rules

Reported: "we have too many heavy borders generally, like too many dividing lines instead of just using space", and then the sharper version — "it has borders and then spacing outside the borders, which is weird".

The second sentence is the diagnosis and it generalises. A rule *and* a gap are two separators doing one job, and the eye reads the pair as a mistake even when it cannot say which half is wrong. The diff head row had exactly that shape twice in one afternoon and is now fixed by being filled instead of ruled — one step off the window's base says "band" on all four sides at once, and it keeps saying it while the patch scrolls underneath, which is the job the rule was there for.

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
  - a rule is for a boundary something *scrolls under* where a fill will not
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

Related: [[an open-or-create PR button in the diff head]] (#98) and [[keep a thread's bookmark at its tip]] (#41) — shipping a bookmark that sits at the first commit of a branch ships one commit, which was measured at 51 behind on this workspace. Worth deciding whether ship-it simply *is* #98's button after a hold, rather than a second control.

## 102. Render markdown in the chat, mermaid and diffs included

Once the agent's conversation is addressable rather than only drawn in a terminal — #91, ACP — the window has messages to render, and a message from an agent is markdown. That means a renderer, and two of its blocks are already solved here.

**Diffs.** A fenced ```diff block goes to `@pierre/diffs`, which is already a dependency and already the thing that renders every patch in this window. That matters beyond convenience: a diff in a message and a diff in the diff panel should look identical, or the same change reads as two different things depending on where it is seen. `parsePatchFiles` and `CodeView` take a string, which is exactly what a fenced block is.

**Mermaid.** ```mermaid blocks render as diagrams. Worth knowing before choosing how: mermaid is large — the full bundle is several hundred KB and pulls in its own parser per diagram type — so it wants a dynamic import at the point a block appears rather than a top-level one, the same shape the highlighting worker uses. It also renders to SVG asynchronously and can throw on a malformed graph, which an agent will produce: a failed diagram must fall back to showing the source text, not to an empty box or a boundary.

**Everything else is the ordinary list**, and the ordinary list is where the work actually is: headings, lists, tables, links, inline and fenced code. Fenced code that is *not* diff or mermaid should go through the same shiki highlighter the diff panel already loads, so there is one highlighter and one theme rather than two.

Three things to settle:

  - **which markdown parser.** Nothing here has one yet. Whatever it is has
    to be safe against a message containing raw HTML — an agent quoting a
    page will produce some — so either a parser that does not emit HTML, or a
    sanitiser, decided rather than assumed.
  - **streaming.** ACP delivers a message as it is written, so the renderer
    has to cope with a half-finished fence. The naive answer re-parses the
    whole message on every chunk, which for a long message with three diagrams
    in it is a lot of work per token. `shiki-stream` ships inside
    `@pierre/diffs` and is worth reading before inventing something.
  - **where it lives.** Probably its own package — `@awp-kit/markdown` — since
    the pane, the diff panel and a future chat all have a claim on it, and the
    tsconfig project references are the import graph.

Related: [[run the agent under ACP, not only in a terminal]] (#91), which is the thing that makes this worth having at all. Nothing to render until there is a conversation to render.

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

## 105. The degraded-inbox sentence names a number nobody asked for

Reported from a real window:

    GitHub would not compute mergeStateStatus for 100 pull requests here,
    so conflicts and behind-base are unknown

**100 is `LIMIT`, not a count.** It is the ceiling the query asks for, so the
sentence says "100 pull requests" for a repository with twelve open ones. The
number is the only concrete thing in the sentence and it is the one part that
is not a fact about this repository.

The first guess was that closed pull requests were being included and inflating
the query. They are not — `github-cli.ts` already passes `--state open`, with a
comment explaining why. So the mechanism is right and only the sentence is
wrong.

    now     "…for 100 pull requests here"      a constant, read as a count
    after   "…for this repository's open pull requests"

The real count is not available at the point the message is composed: the whole
query failed, so nothing came back to count. The cheap repair is to stop naming
a number. The fuller one is to compose the sentence after the cheap listing has
succeeded, where `raws.length` is in hand and can be stated truthfully.

Also worth reconsidering while in there: `mergeStateStatus` is the field that
fails, and it fails as a function of repository size rather than of anything a
person did — so a repository that degrades once will degrade every time. Asking
for the full field set on every refresh spends a failed multi-second query to
learn something already known. Remembering the degradation per repository, and
retrying it occasionally rather than always, is the same shape as the pr cache.

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

## 110. A re-render reopens a file marked viewed

Reported from a real window: marking a file viewed collapses it, and then a re-render expands it again.

The collapse is a property of the rendered item, so anything that rebuilds the item takes it back to the renderer's default — which is the same shape as the finding already written down about a gesture:

    a re-render is cheap
    a re-render that changes an item's `version` is a REBUILD
    a rebuild is what a collapsed item cannot survive

So the fix is the same as the drag's: viewed-ness has to be state the panel holds and re-applies, not a side effect of one render. What makes this one worse than the drag is that the diff is pushed — a change in the workspace re-reads the patch — so the reopening happens on its own, without anybody touching the panel.

To settle: whether "viewed" survives a change to the file itself. Marking a file viewed and then the agent editing it should probably un-mark it, since what was read is no longer what is there. That is the same question GitHub answers by dropping the viewed mark on a new commit.

Related to the pending task about the diff panel remembering what has been viewed — this is the bug in what exists, that one is the persistence.

## 111. A workspace made by a job does not appear until the window is reloaded

Reported: created a thread, the thread appeared, and its workspace never did — the sidebar read "nothing yet" under the heading, forever.

That is exactly the failure `App.tsx` already has a fix for, and its own comment says why it is so hard to see: "nothing yet" is precisely what a thread whose creation *failed* looks like, while the workspace, bookmark and session are all on disk. They were, in this case — checked in the store and on disk afterwards.

The existing fix re-reads the sessions and the threads when the count of terminal jobs changes:

    const finished = jobs.filter((job) => isTerminal(job.status)).length;
    useEffect(() => { reloadSessions(); reloadThreads(); }, [finished]);

So the suspect is the jobs feed, not the sidebar: `finished` only changes if the window is being told about the job. The daemon had been restarted a few minutes earlier, which is the one condition that takes every feed out at once — an rpc stream is a request, so its fiber dies with the connection.

Measured, and it did not reproduce the way expected. From a browser against an isolated second instance, with the daemon down at page load and brought up afterwards:

    before          rows=1    "no daemon — start it with bun run daemon"
    while down      rows=1
    after restart   rows=11   the real sidebar, with rows

So the resubscribe recovers from never-having-connected. What that run did NOT test is the case that actually happened — a window connected to a live daemon, that daemon dying, and a job completing in its replacement. That is the next measurement, and it needs the daemon to be up before the page loads.

Worth considering regardless: keying on a *count of terminal jobs* makes the refresh depend on the window having witnessed the transition. A window that reconnects after the job finished sees the job already terminal, so the count is whatever it is and never changes again. That is a real hole independent of whether it is this one.

## 113. Dragging a divider near the top moves the window

Grabbing the accessory column's divider at the top of the window moves the *window* instead of resizing the column.

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
  - **What it says.** The noun and its name — "archived tiered discounts" — and
    an undo where one exists. Archive is the obvious first: it is the only
    gesture in the window that removes rows.
  - **Where it goes.** Above the footer, right, over the accessory column; it
    must not cover the native webview's rectangle, which does not stack (see
    CLAUDE.md) — or if it does, the same overlay count the modals use.
  - **How it leaves.** Animated, per the window's mandate: nothing pops.
    Reduced motion means none.

Base UI ships a Toast, which is the answer to whether to hand-roll one.

## 115. The chat says what it is running as, and how full it is

Asked for by name: "the config controls for model and effort and mode too please. also would be great to show context usage %".

A chat session has three settings a person needs to see and change without leaving the panel, and one number they need to watch:

    model    `session/new` takes it in `_meta.claudeCode.options`. Changing it
             mid-session is untested — it may need a new session, which is a
             different gesture and should say so.
    effort   the same place. Not currently passed at all by the chat.
    mode     `session/set_mode`, and this one matters most. The chat sets
             `default` (Manual) at open; the other five are auto, acceptEdits,
             plan, dontAsk and bypassPermissions. A person needs to see which
             one they are in, because it decides whether they are asked before
             a tool runs.

**The context number is already arriving and is being thrown away.** `updateOf`
in chat.ts drops `usage_update` as an update "nobody reads" — measured six of
them in a single turn. That is exactly where the percentage is. The fix is to
stop dropping it, put the figures on `ChatUpdate`, and show them; nothing new
has to be asked for.

Two things to get right when it lands:

  - **A number that is always on screen is furniture.** The window's rule
    about the status bar applies: say nothing until it is worth saying.
    Somewhere past half, and louder near the end.
  - **Where the controls go.** A row under the composer is the obvious place
    and competes with the send. The agent bar already holds the face toggle
    and is where "how this session runs" belongs — but it is per-window
    chrome and these are per-session facts. Decide once.

Blocked on nothing. #91 is what made it possible.

## 116. Rename a thread from the header or the sidebar

A thread's title is written once, by a model, from the sentence somebody typed into the new-thread modal. It is frequently almost right and there is no way to fix it.

Two ways in, and they are the two places the title is already on screen:

    the agent header    click the title — `<project>/<title>`, the title half
                        only, since the project is not a thing to edit here
    the sidebar         the row's ⋯ menu, beside `archive…`, and right-click
                        on the heading as the same menu

`ThreadRename` exists on the wire already — the new-thread flow writes a title
and `threads.ts` has `setTitle` — so this is a call and an input, not a schema
change. Check before building.

Three things to decide when it is picked up:

  - **Edit in place, or a dialog.** In place is the better gesture and is more
    work: the header is a flex row that truncates, and a text input in it has
    to not resize the bar. The sidebar row is narrow enough that in-place
    editing would be typing into a 200px box.
  - **What a workspace shows.** A row's caption falls back through display
    name, the model's label, then the slug. Renaming a *thread* must not look
    like it renamed the workspace, and the sidebar draws both.
  - **Escape and blur.** Escape reverts, Return commits, and clicking away
    should commit rather than discard — the opposite reads as losing work.

The keyboard mandate applies: the control has to be reachable without a
pointer, so the sidebar's ⋯ menu is the one that has to work, and right-click
is the shortcut rather than the feature.

Depends on nothing. Related: #114 (say so when something changed) — a rename is
the least ambiguous case for a toast, since the row simply reads differently
afterwards.

## 117. Quote a piece of the chat and reply to it

Asked for: "hover or highlight anything in agent chat and be able to reply to it".

A conversation with an agent goes wrong in a specific place — one claim in a paragraph, one command in a tool call, one file it named. Today the only way to say so is to describe where you mean in prose, at the bottom, after it has scrolled away. The panel already holds the structure that would let you point instead.

**This window has done it twice already**, and both are the pattern to follow rather than the thing to reinvent:

    the diff       drag across line numbers → a comment on that range
    the web panel  point at an element → a note carrying its selector

Both anchor to something addressable and both send a *record*, not typed text.

What that means here:

  - **A selection is the anchor.** Hovering a message shows the affordance;
    selecting text inside it makes the quote. A whole-message reply is the
    degenerate case of selecting all of it, so build the selection one.
  - **What crosses the wire.** The quoted text and enough to find it again:
    which item, and the offsets within it. A message is chunks appended in
    `conversation.ts`, so an offset is stable only once the turn has ended —
    quoting a message still streaming needs deciding.
  - **A tool call is quotable too**, and is the more useful half: pointing at
    the command it ran, or at one line of its output, is exactly the "no, not
    that" a person wants to say.
  - **It goes in the composer, not straight out.** The diff batches comments
    because six remarks are one prompt; here the reply is the next turn, so
    the quote should land in the box with the cursor after it and let somebody
    type. That also makes it undoable by deleting it.

Two things to check before starting, both learned nearby:

  - **A render during a gesture ends the gesture.** The diff's line selection
    was broken for exactly this reason — opening the composer at pointerdown
    rebuilt the rows the pointer was still moving across. Settle on pointerup.
  - **Markdown output is React elements, not text**, so a selection inside an
    agent message spans nodes the panel did not create by hand. Read the
    Selection API rather than assuming a single text node.

Depends on nothing in flight. Related: #44 (comment on a diff and send it),
#55 (annotate an element and send it), #102 (patches inside tool output — a
quoted diff line would want the same renderer).

## 118. A subagent is a tool call with a name, and the chat should say so

An agent that spawns subagents currently reads, in the chat, as a single tool call that sits at `in_progress` for minutes and then produces a wall of text. Nothing says work was delegated, how many ways, or which one is stuck.

**The information is already arriving.** Measured in the adapter's own source, 2026-08-28: a Task call is an ordinary `tool_call` / `tool_call_update`, and the subagent facts ride in its `_meta`:

    _meta.claudeCode.toolResponse.subagentType    which kind was spawned
    _meta.claudeCode.toolResponse.subagentRetry   attempt, max_retries,
                                                  retry_delay_ms
    _meta.claudeCode.toolResponse.elapsedTimeSeconds

`updateOf` in chat.ts reads `title`, `kind`, `status` and the output and throws the rest away, so nothing new has to be asked for — this is a matter of keeping three more fields.

The retry counters are the ones worth having and are the least obvious. The adapter's own comment says why they are forwarded verbatim: *"when the subagent is waiting out an API rate-limit retry … so clients can show why a spawn looks stalled."* A subagent stuck behind a rate limit and a subagent doing slow work are the same picture today, and only one of them is worth waiting for.

**There is no subagent update kind in ACP**, and it is worth writing that down so nobody goes looking: no `subagent` anywhere in the schema, no nesting, no separate stream. A subagent's own messages do not arrive. What arrives is one tool call that takes a while. So this task is about labelling that call honestly, not about drawing a tree.

What to decide:

  - **How a delegated call reads.** `ran  Task` is what it says now. `spawned
    a code-reviewer · 2m14s` is the shape wanted, and the elapsed figure is
    already on the wire.
  - **Whether a retrying subagent is a state or a sentence.** The tool row has
    a status already; `attempt 2 of 5, retrying in 30s` is a sentence and
    probably belongs on the row rather than in a new state.
  - **What the sidebar does with it.** #42 wants a row to say what the agent is
    doing. "Delegating" is a different answer from "working" and may be worth
    the distinction — or may be a distinction only this panel cares about.

Related: #42 (sidebar status), #115 (which landed the config strip and is where the daemon's update parsing now lives).

## 119. Own the agent's terminals, so a long command is watchable and killable

A command the agent runs for two minutes is, in the chat, a row that says `…` and then eventually says something. It cannot be watched while it runs and cannot be stopped.

**ACP hands this to the client, and this window is unusually well placed to take it.** Five methods, all client-side:

    terminal/create          the agent asks THIS process to run a command
    terminal/output          what it has written so far
    terminal/wait_for_exit   how it ended
    terminal/kill            stop it
    terminal/release         let it go

And a tool call's content can be `{ type: "terminal", terminalId }` — a live handle rather than a string of output, so the row in the chat *is* the running command rather than a record of one that finished.

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
    terminal means the command arrives here to be *run*, so the refusal has to
    happen before we spawn anything rather than after.
  - **Scope and leaks.** A terminal is created by one call and released by
    another, and an agent that dies between them leaves a process. The
    conversation's own Scope is the natural owner, the same way the adapter
    process is.

Do not start this before the permission path has been exercised by hand: this
moves execution into the daemon, and the daemon is the process holding a
person's repositories.

Related: #91 (the chat), #115 (config strip), #118 (subagents), #63 (running a
workspace's services — a different long-running-process problem with some of
the same answers).
