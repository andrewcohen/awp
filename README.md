# awp — Agentic Workspace Pilot

> ⚠️ **Disclaimer**: This project is heavily vibe-coded. The maintainer doesn't necessarily know what's inside any given file at any given moment. Treat the source as a sketch, not a contract: read it before depending on it, and assume any behavior may change without warning. Bug reports and PRs welcome; "this code is weird" is fair feedback.

`awp` is a Go CLI and TUI for running multiple AI coding agents (Claude Code, pi.dev, …) in parallel across isolated [Jujutsu](https://github.com/martinvonz/jj) workspaces, each in its own [tmux](https://github.com/tmux/tmux) session. It gives you a single keyboard-driven dashboard ("the deck") to summon, switch between, and observe agents — including a live status indicator showing which agents are working, idle, or waiting on you.

## What problem it solves

You want to run 5+ agents at once on different branches without:

- Manually managing `jj` workspaces, branch checkouts, and tmux layout.
- Tab-switching to figure out which agent is blocked, which is generating, and which has finished.
- Re-typing the same `claude` / `pi` invocation in every pane.

`awp` automates the whole loop: create a workspace from a bookmark or PR, drop you into a tmux session running your default agent, wire up status reporting via hooks, and give you a one-screen overview of every running workspace.

## Installation

```sh
go install github.com/andrewcohen/awp/cmd/awp@latest
awp init hooks   # one-time: install Claude Code + pi.dev integrations globally
```

`awp init hooks` installs:

- `~/.claude/settings.json` — hooks that report state to awp on `SessionStart` (idle), `UserPromptSubmit` / `PreToolUse` / `PostToolUse` (working), `Stop` (idle), and `PermissionRequest` / `Elicitation` (waiting). The `UserPromptSubmit` hook also pipes the prompt JSON into `awp internal report-status --prompt-stdin` so the deck can show the active prompt under each workspace. The `PreToolUse` hook passes `--waiting-when-tool AskUserQuestion` so the row flips to `waiting` while the agent is paused on an `AskUserQuestion` call; `PostToolUse` flips it back to `working` once the answer is in. `PermissionRequest` (a permission dialog is up — approve/deny a Bash command or file write) and `Elicitation` (an MCP server is requesting form input) are the dedicated "blocked on you" events: they badge the row `waiting` even when desktop notifications aren't configured. awp deliberately does **not** hook `Notification`: Claude fires it for its ~60s idle ping as well as permission prompts, so mapping it to `waiting` lit up the unread summary with false `▲` triangles for agents that had simply finished their turn — the dedicated `PermissionRequest` event covers the real case. Any stale awp-managed `Notification` hook from an older awp version is removed automatically on the next deck open. Unknown events are ignored by older Claude Code builds, so these are safe to install regardless of client version.

  For repos with a [`dev_loop`](#dev_loop), extra entries enforce the loop and keep the deck's meta line live: `PostToolUse(Bash)` / `PostToolUseFailure(Bash)` → `awp internal gate record` (the success/failure event is the pass/fail verdict), `PreToolUse(TaskUpdate)` → `awp internal gate check --hook`, and a matcher-less `PostToolUse` / `PostToolUseFailure` → `awp internal loop track` (caches the current loop phase). They coexist with the matcher-less status entries above and no-op for repos without a `dev_loop`. See [`dev_loop` → Enforcement](#dev_loop).

  A `PreToolUse(Edit|Write|NotebookEdit)` → `awp internal require-task --hook` entry enforces **task discipline**: it blocks editing a non-markdown file (exit code 2 + reason on stderr, which Claude feeds back to the agent) unless a task is currently `in_progress` in the session's task list (`~/.claude/tasks/<session>/`). Markdown (`.md` / `.markdown` / `.mdx`) is always exempt, so specs, READMEs, and notes never trip it. Like the gate hooks, it only enforces on repos with a [`dev_loop`](#dev_loop) configured — the command self-gates on the same `watch.IsConfigured` predicate, so a repo that hasn't opted in (or a session outside an awp-managed workspace) is never blocked. It fails open (allows the edit) if `awp` isn't on `PATH`, the payload is unreadable, or the task state can't be found, so a hook error never wedges editing.

  These hooks re-sync automatically: opening the deck fires an idempotent install in the background, so after an awp upgrade (which may add events or bump the hook schema version) the global hooks self-heal on the next `awp deck` without you re-running `awp init hooks`. It only writes when something has actually drifted.
- `~/.pi/agent/extensions/awp-status.ts` — a pi.dev extension that reports state on `session_start` / `before_agent_start` / `agent_end` / `tool_execution_start` / quit-time `session_shutdown`. `before_agent_start` forwards the user's prompt text so the deck stays in sync with Claude. If statuses aren't landing, set `AWP_DEBUG=1` in the pi pane to write diagnostics to `~/.awp/pi-extension.log`.

The status/gate integrations are no-ops outside awp-managed sessions (they only run in tmux and `awp internal report-status` ignores sessions without awp workspace metadata), so they never affect your standalone Claude or pi usage. The `require-task` hook is likewise a no-op unless the session resolves to an awp workspace whose repo has a `dev_loop` configured (see above). All honor `$AWP_BIN` if you need the hook to invoke a non-PATH `awp`.

## The deck

```sh
awp deck
```

Recommended invocation as a tmux popup (in `~/.tmux.conf`):

```tmux
bind a display-popup -E -w 90% -h 90% awp deck \; run-shell "awp deck-cleanup"
```

> **Heads up — "exit code 127" from the popup or `deck-cleanup`?**
> tmux's popup/run-shell commands run under a non-interactive `/bin/sh` that does **not** read your `~/.zshrc` / `~/.config/fish/config.fish`. If `awp` lives in `~/go/bin` (the `go install` default) and your shell rc adds that to PATH, tmux won't see it and you'll get a bare exit 127. Two fixes:
>
> 1. **Inject PATH into the tmux server** (recommended — covers all popups):
>    ```tmux
>    set -g update-environment "PATH DISPLAY ..."   # if not already
>    set-environment -g PATH "$HOME/go/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin"
>    ```
> 2. **Use absolute paths** in the binding:
>    ```tmux
>    bind a display-popup -E -w 90% -h 90% "$HOME/go/bin/awp deck" \; run-shell "$HOME/go/bin/awp deck-cleanup"
>    ```

Press `?` inside the deck for the full key + status legend.

### Mini deck (quick-jump)

```sh
awp mini-deck
```

A short, filtered version of the deck that surfaces only workspaces that want your attention *right now*. Use it when you want to alt-tab between live agents without scrolling the full deck.

- `j` / `k` move the cursor
- `g` / `G` jump to top / bottom
- `f` easymotion-style hint jump — type the single (or two-char) hint that appears next to a row to land the cursor on it
- `enter` summons (creates or focuses) the workspace's tmux session and clears its unread badge
- `q` / `esc` quit without jumping

A row qualifies for the mini-deck when **all of** (the same rules the full deck's `P`-cycled "attention" scope uses, so the two stay in sync):

1. Its status is one of:
   - `working` — agent is generating output or running a tool. Always surfaced.
   - `waiting` **with the unread flag set** — Claude is blocked on you: a permission dialog (`PermissionRequest`), an `AskUserQuestion`, or an MCP form (`Elicitation`). The unread flag is only true if you weren't already attached to the session when the hook fired, so requiring it skips prompts you already saw and dealt with in-session. A `waiting` row without unread is just stale noise — the row self-heals to `working`/`idle` on the next tool use or turn end.
   - `idle` **with the unread flag set** — agent finished a turn since you last visited.

   `exited` workspaces never appear; nothing's listening on the other end. `idle` without unread is a quiet workspace and doesn't appear either.
2. Its tmux session is actually alive and the `:agent` pane is still running an agent, not a bare shell. This catches the common case where the agent process died without firing an exit hook (Claude has no exit hook), so a stale `working` from days ago doesn't keep cluttering the list.

The `default` workspace per project is no longer filtered out by name — if an agent really is running in it (or it has an unread turn waiting on you), it surfaces just like any other row.

When a project's **only** workspace is `default` **and its agent is quiet** (no status dot would render), the deck collapses it to a single row: the project name stands in for the (uninformative) `default` label, with the PR glyph and meta (branch · author · prompt) inline on one line, instead of the usual project header + workspace row + meta line. The moment the agent has something to show — working, or an unread waiting/idle turn — the project uncollapses into the full layout so the status dot sits in its usual column. A project that has `default` plus other workspaces always renders normally.

Suggested tmux binding under capital `A` (lowercase `a` already opens the full deck):

```tmux
bind A display-popup -E -w 50% -h 60% awp mini-deck
```

The same PATH caveats as `awp deck` apply (see above) — use absolute paths or `set-environment -g PATH ...` if the popup exits 127.

### Agent status (the colored dot at the start of each row)

| Color | State | Meaning |
|---|---|---|
| 🟢 Green | `working` | Agent is actively producing output or running a tool |
| 🟡 Yellow | `waiting` | Blocked on you — permission prompt, `AskUserQuestion`, or MCP form |
| ⚪ Grey | `notified` | Agent finished a turn and you haven't summoned the workspace since |
| _(blank)_ | `idle` / `starting` / `exited` | Quiet — no badge until the agent actually surfaces something. An exited agent never badges: the process is gone, so there's nothing to act on |

The grey "notified" dot is a per-workspace unread badge: it lights up when the agent transitions into `waiting` or `idle`, and clears the next time you summon that workspace (any of `enter`, `a`, `e`, `c`, `v`, `s`, `i`, `x`) — or when the agent exits.

**Workspaces still being created** show up in the deck the instant you submit the new-workspace form — an **optimistic row** appears immediately (meta line `creating…`) rather than waiting for the detached create subprocess to write state and a refresh to surface it. Once `jj workspace add` registers the workspace the optimistic row is reconciled into the real one, which keeps the spinner and switches its meta line to `setting up · <current step>` (e.g. `setting up · pnpm i`) while the bootstrap hooks (`pnpm i` and friends) run and the agent/tmux session launch. In both phases the row is badged with the animated **spinner** in place of the status dot. Workspace actions on it (`enter`/summon, window opens, send-prompt `A`, delete `d`, rename `R`, link `l`) are held with a `… is still being created` / `… is still setting up` toast until the create finishes — attaching before the session exists, or deleting mid-create, would race the create subprocess. The badge and guard clear automatically the moment the create job finishes (and the optimistic row is dropped if the create fails).

**Opening a PR review** (`r`, or `enter` on an *awaiting your review* inbox row) gets the same treatment: the review checks out the PR into a `pr-<n>-<branch>` workspace, and that row appears immediately as an optimistic `setting up · <current step>` row (the step tracks the review job — `jj git fetch`, `Prepare jj workspace`, opening the tmux windows, …) instead of waiting for the detached review subprocess to write state. Since the row carries the PR number, it supersedes the read-only inbox placeholder for the same PR rather than rendering next to it. As with create, workspace actions are held until the review finishes. (When the PR head ref isn't known at dispatch the name can't be predicted, so the row simply appears after the next refresh — the pre-existing behavior.)

**Workspaces being deleted** get the same spinner treatment: while the delete job runs, the row stays visible with the spinner and a `deleting…` meta line, then disappears the moment the delete finishes (rather than lingering until the next periodic refresh).

**Dev-loop progress on the meta line.** While a workspace's agent is **actively working**, its row's meta line switches from the usual branch/port to a live snapshot of the agent's dev loop, progress-first: `<done>/<total> · <phase> · ▶ <current unit>` (e.g. `3/7 · implement · ▶ wire up the meta line`). The `<done>/<total>` count is the agent's todo/unit list, `<phase>` is the current dev-loop phase (`explore → implement → verify → commit`), and `▶ <current unit>` is the in-progress task. `explore` is the pre-task-list stretch — investigating or writing the spec, before the work is broken into a task list; once a task list exists, each unit cycles `implement → verify → commit`. It's the same data the [`w` watch overlay](#key-bindings) shows, condensed to one line — read from the agent's Claude Code transcript by the deck's background refresh (so it lags live activity by up to the refresh interval; open the `w` overlay for a second-by-second view including gate pass/fail and churn). Each fresh snapshot is cached in `workspace-state.json` (the `DevLoop` field on the entry), so the next deck open renders progress on the very first paint instead of flashing the branch/port meta while the transcript is (re)scanned; the cache is rewritten only when the snapshot actually changes. For repos with a [`dev_loop`](#dev_loop), the phase and gate pass/fail are additionally kept live by event-driven hooks (`awp internal loop track` / `awp internal gate record`), so the cached snapshot reflects the *current* phase on open — even right after a phase switch — rather than the last scan's (`done`/`total` still refresh via the scan). Any missing slot drops out, and the row falls back to its normal branch/port meta the moment the agent stops working, once **all units are done** (a finished `12/12` loop has nothing in progress to surface), or if there's no transcript / no progress to show yet. Uses the project's [`dev_loop`](#dev_loop) config, or the inferred default loop when none is set.

### PR status (the glyphs leading each row's meta line)

Each workspace is matched to a PR by its jj bookmark (PR `headRefName`). If a match is found, a glyph cluster (Nerd Font Octicons + Material icons) leads the meta line under the workspace row — primary PR state first, then any condition glyphs from the tables below. The meta line itself is mostly muted; only the `:port` token is tinted (blue) for a touch of contrast — everything else, including the workspace-less inbox row's keyboard-return `to review` / `to check out` hint, stays muted. On a collapsed default-only project row the glyphs render inline after the project name instead (there's no second line). Workspaces with no bookmark on file, or no matching PR, show no glyphs.

| Glyph | Meaning |
|---|---|
| 󱍓 | PR draft — still being drawn up |
|  | PR approved — at least one approving review |
|  | PR in merge queue — GitHub has queued the PR to merge |
|  | CI pending — checks in flight |
|  | CI failed — at least one check failing |
|  | PR merged — safe to delete this workspace |
|  | PR closed without merging |

Priority (highest wins): merged → closed → CI failed → CI pending → in merge queue → approved → draft. A plain open PR with nothing notable shows no primary glyph — open is the baseline state, so only deviations from it earn ink. A merged PR always shows the merge icon (even if its last CI was failing); an open PR with failing CI shows the alert icon; once a PR enters GitHub's merge queue (and CI is still green) it reads as queued rather than approved.

When the PR needs attention beyond its primary state, a second glyph renders to the right of the primary PR glyph:

| Glyph | Meaning |
|---|---|
|  | Behind base — the base branch has moved past this PR. Only signaled when the repo's branch protection requires up-to-date branches before merging; otherwise GitHub reports the PR as clean even when behind. |
|  | Merge conflicts — the PR can't merge cleanly until the conflicts are resolved. |
|  | Stale — your local bookmark tip differs from the PR head on GitHub; what you have locally (or last reviewed) is out of date. |
| 󰻞 | Your review is requested on someone else's PR — blue for a first request, yellow when it's a re-request (you reviewed, the author pushed and asked again). |
| 󰭹 | Review feedback on **your** PR (yellow) — a reviewer requested changes *or* left review comments (pairs with `p r`, which preloads a fix prompt for it). Fires on any `COMMENTED` / `CHANGES_REQUESTED` review, not just a formal "request changes": GitHub's review *decision* stays `REVIEW_REQUIRED` when someone only comments, so the glyph reads the review states directly. Suppressed once the PR is approved. |
|  | Blocked on base (red) — this PR is stacked on another open PR that isn't ready to merge yet, so it can't land until the base does. Derived from the stack graph (see the inbox scope); pairs with the `└─` tree connector that nests the PR under its base. |

When the workspace's local bookmark tip doesn't match the PR head commit on GitHub, the row gains a  glyph (yellow) and its meta line a `· stale` chip — the signal that what you have locally is behind (or otherwise diverged from) what's actually on the PR, so any previous review pass or in-progress work is out of date and a fresh re-review is warranted. Most useful for PRs on a collaborator's branch: the PR head on GitHub is the truth, and a difference means the author has pushed since you last fetched. Independent of `behind base` — that signals the PR is behind its target branch, while `stale` signals your local bookmark is behind (or diverged from) the PR's remote head. Only renders on open PRs.

**PR labels.** When the matched PR carries GitHub labels, the meta line carries a tag segment right after the author (before the branch) — a tag glyph (Octicon) followed by the comma-joined label names (e.g. `bug, enhancement`). Like the rest of the meta line it stays muted: label colors are per-repo and don't route through the deck's semantic palette, so only the names render, not GitHub's per-label color. The same tag chip trails each PR in the [`r` review picker](#key-bindings) (capped so a heavily labeled PR can't crowd out the title), and the labels are listed in the merge-confirmation modal (`p m`). Labels are read from the same `gh pr list` / `gh pr view` calls that drive the row glyphs — no extra request.

The status is fetched once when the deck opens, with a single `gh pr list --state open` call per distinct repo that has at least one non-default workspace. Only open PRs are listed — the deck only ever displays bulk-list PRs that are open, and listing every recently-closed PR forced GitHub to compute the expensive per-PR CI rollup for ~100 PRs that nothing rendered. Terminal (merged / closed) status for a workspace's PR is filled in the cheap way: a per-PR lookup of the workspace's pinned PR number, plus a write-through right after you merge from the deck. The repos are fetched **concurrently** (bounded so we stay clear of GitHub's rate limits), and within each repo the PR list, the merge-queue lookup, and the per-PR top-ups all run in parallel. The fetch is throttled so the same repo is never re-queried within a minute. The throttle is bypassed for actions that materially change the PR↔workspace mapping: linking a bookmark to an existing workspace, creating a new workspace from a bookmark, and opening a PR review — those refresh the affected repo immediately.

The fan-out runs as a **detached job** in the same jobs subsystem that powers workspace create / delete / review. It's spawned via `Setsid`, so closing the deck (or its tmux popup) mid-fetch no longer drops in-flight work. Per-repo PRs are persisted to `~/.awp/pr-status-cache.json` atomically as each repo finishes; the job record itself lives at `~/.awp/jobs/<id>.json` and shows up in the deck's `J` overlay (you can dismiss / open the log there). The next deck open reuses an existing active pr-status job instead of spawning a duplicate.

The same pass also **mirrors each pinned PR's GitHub review threads** into that workspace's review store (`~/.awp/reviews/<repo>/work-<workspace>/remote/threads.json`), which is what the diff surface reads for `T` / `R`. Doing it here rather than from the diff itself keeps the reviewers' conversation current — within the same per-repo minute as the glyphs — while leaving `c` as instant as the rest of the deck: the viewer only ever reads a local file, never the network. One fetch covers every workspace pinned to the same PR (a review workspace beside the author's own). A fetch failure leaves the previous mirror exactly as it was rather than blanking it, and a PR with no threads doesn't get a review store conjured for it. The per-repo Step in the `J` overlay reports how many threads landed.

**Requires a patched (Nerd Font) terminal font.** Anyone running awp without a Nerd Font will see empty rectangles where the PR glyphs would render.

### Inbox scope (`P`)

The third `P` scope sections open-PR workspaces by *what your next move is*, like GitHub's pull request inbox, instead of by project. Buckets render as headers with counts, most urgent first; empty buckets are hidden:

| Bucket | Header color | Membership |
|---|---|---|
| Needs your review | teal | Someone else's PR with your review requested (or re-requested). Re-reviews — ones you already reviewed that the author pushed to and re-requested — sort to the top of the bucket. |
| Needs action | red | Your PR with changes requested, CI failing, merge conflicts, or behind base |
| Ready to merge | green | Your PR, approved + CI green + clean (or already in the merge queue) |
| Other open PRs | gray | Open PRs that are neither yours nor awaiting your review (e.g. a collaborator's branch you checked out) |
| Mine | gray | Your own in-flight PRs that aren't blocked on you — waiting for review, or still a draft. The bottom pile: nothing here needs your action right now. |

Bucket headers are colored by urgency (the table above) so the section you need to act on stands out. Within each bucket, rows are grouped by project under a teal project **subheader** (the bucket is the primary section; the project is nested beneath it), so no per-row project chip is needed. Buckets are classified from the same cached PR status that drives the row glyphs — no extra fetches. Merged and closed PRs stay out, as before.

**PR stacks** are surfaced as cohesive units. When one open PR's base branch is another open PR's head — a stacked PR — the deck draws the dependency: the stacked PR nests under the one it's based on with a teal `└─` tree connector, and a PR that can't merge until its base lands gets a red lock () in its glyph cluster. The connector is flat — every PR in a stack (however deep) sits at one indent under its root — so deep stacks don't drift off the right edge. In the inbox, buckets group by a muted-blue project subheader with a blank line between projects. A whole stack is treated atomically for bucketing: it sections under its *most-actionable* member (e.g. a stack whose tip is approved but whose base is still failing CI lands in **Needs action**, not **Ready to merge** — because you can't merge the tip until the base lands), and all its members stay together under that one header. Stack edges are derived from the PR base branches already in the status cache, so this needs no extra fetches; a PR based on `main` (or on a PR that isn't shown) renders as a normal top-level row. The `all` and `attention` scopes render the same nesting + lock within each project group (they just don't have the inbox's buckets). In those scopes, pinning any PR in a stack drags the **whole** stack into the pinned section (contiguous, root → tip), so a pin never splits a chain across the pinned region and its project group.

**Open PRs you haven't checked out** also show up, even without a local workspace — the status cache already knows about every open PR in the repos you work in, so the inbox isn't limited to PRs that happen to have a workspace. Three cases are surfaced: someone else's PR **awaiting your review** (lands in *Needs your review*, keyboard-return `to review` hint), **your own** open PRs (sorted into *Mine* / *Needs action* / *Ready to merge* by state, keyboard-return `to check out` hint), and **stack-completion links** — any open PR that connects a stack you already see (an ancestor or descendant), pulled in *regardless of ownership* so a stack never renders with a hole where a teammate's PR sits in the chain (keyboard-return `to check out` hint). So a PR you opened from another machine, whose workspace you deleted, or that's a teammate's link in your stack no longer silently disappears. These rows are read-only (no agent dot). Pressing `enter` depends on whose PR it is:

- **Awaiting your review** → starts the review flow (`awp review <n>`), which creates the workspace and primes the reviewer.
- **Your own, or a stack-completion link** → opens the new-workspace form prefilled with the PR branch (anchor + derived name), so you land in a normal working workspace rather than the review tooling. Confirm to create, or tweak the name / add an agent prompt first. The created workspace is pinned to the PR (same link the `B` key applies), so it shows up linked — PR glyph and status — as soon as the row list refreshes, without reopening the deck.

Whenever a workspace is created anchored on an existing bookmark (the inbox path above, the new-workspace form with a chosen bookmark), the create job runs `jj git fetch` first — so the working copy lands on the current origin tip, and a branch that lives only on origin (a PR you pushed from another machine, or a collaborator's branch) is present locally to track. It's best-effort: a fetch failure (offline, etc.) is logged and creation continues. A workspace created with no bookmark starts from the local working copy, so it skips the fetch.

Other workspace keys (delete, rename, send-prompt, link) are no-ops on a workspace-less row until it exists. Workspaces you *do* have are shown as normal rows and never duplicated.

Note: this still only covers repos where you have at least one workspace — the PR-status cache is fetched per repo and a repo only enters that set once it has a tracked workspace. A PR in a repo you've never opened a workspace in won't appear.

### Activity bar (bottom of the deck)

The bottom status line shows in-flight background work as a single segment between the row body and the right-aligned status / `? help`. It surfaces:

- `⠼ pr-status N/M` while gh PR-status is fanning out across repos; ticks down per repo as each one returns.
- `⠼ enrich` during the cold-start refresh, post-rename / post-delete / post-state-edit refreshes, and post-bookmark-link refreshes. The 5-second periodic refresh runs silently.
- `⠼ workspace:rename:<name>` / `workspace:link:<name>` for the deck-local lifecycle actions that don't go through the async-jobs subsystem.
- Each async deck job (workspace `create`, `delete`, `review`, custom `background: true` actions). Failed (`⚠`) and orphaned (`☠`) jobs stay visible in the bar until dismissed via the `J` overlay.

Finished entries flash `✓ <label>` for 500ms before disappearing. When no background work is running, the bar is empty.

### Dev URL capture

When a workspace's tmux session has a process listening on a TCP port (e.g. `pnpm dev` launching Vite on 5173), the deck auto-discovers it and shows a `Dev: http://localhost:<port>` line in the right details panel. Press `u` to open the URL in your default browser.

Detection works by enumerating listening sockets owned by descendants of any tmux pane in the workspace's session (no log scraping, no per-framework config), then picking the numerically lowest port in the range **1024–9999** — typically the HTTP server, since dev-tool sidecars like Vite's HMR socket sit on random high ports. The 1024–9999 cap also keeps ephemeral-range listeners (Claude Code's IPC socket, MCP servers, language servers) from being mistaken for dev URLs. The URL is always `http://localhost:<port>` regardless of whether the server binds to `127.0.0.1` or `0.0.0.0`; the bind address controls who can *reach* the server, not what URL works locally. The line disappears within ~2 seconds of the server stopping.

Backed by `lsof` on macOS and `ss` on Linux. On other OSes the feature is a silent no-op.

### Key bindings

| Key | Action |
|---|---|
| `enter` | Summon (create or focus) the workspace's tmux session. If the row has the unread badge, lands you on the `:agent` window so you see what changed; otherwise tmux's last-focused window wins |
| `a` | Open agent window — re-launches the agent if its pane is at a shell |
| `A` | Send a typed prompt to the workspace's agent (inline form). Header confirms the target project/workspace. If the agent is already running, the prompt is bracket-pasted as a user message; if it isn't, the agent launches with the prompt as its first message. Deck stays in focus — switch with `a` once you want to follow along. |
| `e` | Open editor window (`$EDITOR`) |
| `c` / `C` | Review the change on awp's **own diff surface, in the deck** — no external reviewer. `c` scopes it to the selected workspace's working copy; `C` scopes it to the whole change **against its stack base** (awp resolves the base to the nearest stacked-parent bookmark — the closest bookmarked ancestor of `@` that is neither trunk nor the workspace's own bookmark — falling back to `trunk()` when nothing is stacked). Both open the same surface; only the revision range differs. The footer **names the base it resolved** — `vs main`, `vs andrew/parent-change` — rather than saying "vs stack base", which only described how the base was picked. That name is resolved in the background, so it appears a moment after the diff rather than delaying it, and `ctrl+r` re-resolves it (the one thing that moves a base is a rebase).<br><br>The right pane is **one continuous scroll over the whole change** — every file's hunks in sequence, each opened by a full-width `══ path ══` divider — so reading never means stepping in and out of a per-file view. The left column is a **jump index**: the file list on top — shown as a **tree**, so each directory is named once and its files are listed under it as basenames. A flat list spends its whole width on prefixes that mostly repeat, and the part that distinguishes the rows is what gets truncated away; naming `app/lib/navigation/` once gives that width back to the filenames. Directory rows are structure, not destinations: `j`/`k` still move over files only, because the cursor is an index into the file set that every seek, the reviewed marker and the stream's own file cursor all speak. On a narrow pane the indent is capped rather than allowed to push names off the right edge — nesting is the first thing to give up. Moving the selection seeks the stream, and scrolling highlights whichever file you're in — and, once the change has comments, a **comment index** beneath it listing every conversation (one row per thread, replies folded in as a `·n` count, a `⚠` on any whose anchor no longer resolves). `tab` cycles files → comments → diff; moving the comment selection seeks the diff to that conversation and **centres it in the pane** — a minimal scroll would put its first line on the bottom row with the rest of the thread below the fold, so the selected conversation gets the middle, with the code it is about still in view above it (near the top of the change there is nothing to scroll away, so it simply sits where it falls) — and `enter` just hands the keyboard over with the cursor already there and `D` deletes the selected conversation without leaving the list. The index takes at most half the column and never shortens the file list past usability. The view **refreshes itself** every couple of seconds and keeps your cursor on the line it was on, so you can watch an agent work.<br><br>A **line cursor** marks where you are, highlighted vim-style across the full width. The file list and comment index carry **the same band on their selected row**, so the selection reads identically wherever the keyboard is — and, as in the diff, a band is painted only while its own pane holds the keyboard. Never two at once: the band is what says which selection the keys are actually driving, and a second one would leave that ambiguous. The `┃` bar stays either way, marking the row you'll come back to. `j`/`k` move it a row, `ctrl+u`/`ctrl+d` a half page, `{`/`}` jump to the previous/next hunk *anywhere in the change*, `g`/`G` to the ends. `tab`/`shift+tab` switches pane (`enter` on a file drills into the diff), `h`/`l` pans horizontally with `0`/`$` for line start and end (gutter stays pinned; no-ops under wrap), `w` toggles line wrap, `e` opens the file in `$EDITOR` at the cursor's line. **`\` hides the left column**, giving the diff the full terminal width for reading wide code; your place in the change is kept, so toggling back returns you to the row you were on, and while it's hidden `tab` stays on the diff rather than cycling into panes that aren't drawn.<br><br>**`?` opens the key reference** — the whole keymap, grouped, scrollable with `j`/`k` and `ctrl+d`/`ctrl+u`, and `?`/`esc`/`q` closes it. The footer carries state (which workspace, **which PR** when the workspace is pinned to one — `awp#1234`, project name then number — which range, the last thing that happened) and a `? help` pointer, not a legend: there are more bindings than fit on a row, so any list short enough to display was a list that hid most of them. The reference is rendered from one declared group slice (`internal/ui/help.go::viewerKeyGroups`), the same arrangement the deck's `?` uses, so the keymap and its documentation can't drift apart.<br><br>`c` leaves a **comment** on the cursor's line, and **`v` starts a range** when the remark is about a block rather than a line — the vim gesture, so the movement keys are the extension keys: `j`/`k`, `{`/`}`, `g`/`G`, all of them, upward from the anchor as readily as down. The whole selection carries the cursorline band, so what is selected is what is highlighted; `c` then comments on the block, and `esc` — or a second `v` — cancels. A ranged comment records **both** ends by content, the same way a single-line anchor records one, so it keeps covering the same block as the code moves, and it sits under the range's **last** line: that is where GitHub puts one, and it reads correctly — everything above the remark is what the remark is about. A range stays inside one hunk, because the lines between two hunks aren't in the diff at all and a selection spanning them would silently cover code you never saw (GitHub refuses the same shape on publish). A mixed selection anchors to the new side, dropping the removals from its ends — a removed line has no new-side number to be one — while a selection of nothing but removals anchors to the old side, since those lines exist nowhere else. Every surface then names the location the same way: `a.go:12-18` in the compose box's header, in the comment index, in the prompt sent to the agent, and in the publish log. The lines a range covers keep a **left bar in the comment's kind colour** for as long as the comment is there — while you are composing (recoloured live as `tab` cycles the kind) and afterwards on the saved comment — so the block a remark is about is visible while you read rather than only stated in its header. The bar shares the two columns the selection marker uses; the cursor still wins its own row, and the rows above and below keep the mark, so the range still reads as continuous. `awp review publish` sends it to GitHub as a real multi-line review comment (`start_line` + `line`). The compose box opens **inline in the stream**, directly beneath the line it is about (or at the foot of the thread, when replying), so the code under discussion stays on screen while you write about it. `tab` inside the box cycles the comment's **kind** — *comment* (blue, the default, no action implied), *suggestion* (red, proposes a change), *question* (yellow, wants an answer) — recolouring the box as you go, and the kind drives the hue of the saved block and its row in the comment index. Hue says what the remark is asking for rather than who wrote it; authorship is carried by a 🤖 marker, added automatically to anything filed under an author other than you — in the diff, in the comment index, and in the body posted to GitHub. The marker and the kind are composed at display/publish time rather than stored, so they can't be doubled by a re-publish or edited away. `enter` saves it, `ctrl+s` also sends it to the workspace's agent as an approval-gated prompt carrying the surrounding code, **`ctrl+g` writes it in `$EDITOR`** instead — the same binding the new-workspace and send-prompt forms use, since a remark worth sending to an agent is often longer than four rows of textarea; the box stays open, the kind survives the round trip, and a failed editor leaves your draft alone — and `esc` discards. Comments anchor to the line's **content**, not its number, so they follow the code as the agent edits around them; one that can no longer be located appears in a detached section at the foot of the stream rather than vanishing (each detached conversation is closed off like a placed one, so two of them read as two threads instead of one wall of text, and an orphaned reply sits under the parent it answers). The **review summary** — filed with no `--file` — gets its own section at the very top of the stream, headed `review summary` in teal, above the first file rather than after everything. Deliberately separate from the detached section: those are remarks whose anchor was *lost*, and filing an intentional summary under "anchor could not be found" would read as a failure. They behave like any other conversation — `c` replies, `i` edits, `D` deletes, and they show in the comment index labelled `review` instead of a filename. They become the **review's body** on publish, since a GitHub review comment needs a line to attach to and a remark about the change as a whole has none. (`awp review publish` with no `--verdict` posts them as comments on the PR instead, after the inline ones, so a closing summary lands under the specifics it refers to.) They record what they posted the same way inline comments do, so a re-publish skips them instead of double-posting. A conversation renders as one block sharing a single left bar in the kind's hue, with a blank bar row between messages instead of a deeper indent per reply — stair-stepping a long exchange left a ragged left edge, and each message's author label already says where one ends and the next begins. The kind is named once, on the message that opened the thread. The hue lands on the bar and the header only; the prose itself stays a readable white, since a whole tinted paragraph is harder to read and says nothing a coloured edge doesn't. `c` on a comment **replies** to it (replying to a reply threads under the conversation's top, not under the reply), `i` **edits** your own comment in place — the box takes the comment's place in the stream for the duration, so you see one copy of the text you're changing rather than the saved version stacked above an editable one, and `esc` puts it back — and `D` deletes it along with every reply beneath it — an orphaned reply would otherwise be promoted to a conversation of its own, scattering the answers to a deleted remark through the diff as if each were an independent finding. Deleting a reply takes only that reply. Remote GitHub threads can't be edited or deleted from here, since they're GitHub's records. `r` marks the file **reviewed** and collapses it to its divider (keyed to content, so a later edit resurfaces it), leaving the cursor on the **next file's first changed line** rather than on a divider. Folding hides the file's *lines*, not its conversations: any comments on it move to sit under the divider and are unfolded back onto their lines when you un-review it — they are not relabelled as detached, since nothing about their anchor changed — `r` `r` `r` walks you through a change a file at a time with nothing in between. Un-reviewing expands the file and lands on its own first line. `T` cycles which of the PR's existing GitHub threads are shown (unresolved → all → none) — from **any pane**, since it changes what the view holds rather than what a list has selected, and the comment index is where the change is most visible; cycling to *none* empties that index, so the keyboard is handed back to the diff rather than left on a pane that is no longer drawn. `R` resolves or reopens the thread under the cursor on GitHub.<br><br>A mirrored thread **folds to one line** — `▶ github · resolved · outdated · 3 msgs · CoWorker: [SUGGESTION] Drop the…` — and **`enter` opens or closes the one under the cursor**. Exactly one line, with no padding above or below: the blank bar rows that give an expanded conversation air at both ends would triple the height of the thing whose whole point is being short, so folded threads read as a compact list of markers, each sitting directly against the code it annotates. Resolved threads start folded and open ones start expanded: a settled conversation is reference material until you go looking at it, while an open one is why you're reading. That default is doing real work — on a PR with sixteen threads, showing them all used to add 529 rows of prose to a 697-row diff, with one unbroken run of 205 rows (four and a half screens of conversation with no code in it); folded, the same view adds 48 rows and the longest run is the single unresolved thread. A fold you set by hand outlasts the thread's state changing, so resolving a conversation you deliberately opened doesn't close it under you, and it lasts only as long as the view is open — how you left a fold is a reading position, not a property of the review. Your own local comments never fold; they're the working set. A thread carries whatever GitHub says about it — `github · resolved`, `github · outdated`, or **both**, since resolving a point is often what precedes the code moving out from under it. An **outdated** thread is one whose line the change itself removed: GitHub reports no line for it at all, so it sits in the detached section, and its index row says `outdated` in place of the generic `⚠` — GitHub's own word for the situation says more than the glyph. It is never shown against a line, because there is no longer a line it was written against; presenting one anyway made a settled remark read as a comment about whatever code happened to be there. Those threads come from the mirror the [pr-status pass](#pr-status-the-glyphs-leading-each-rows-meta-line) maintains, and the view re-reads it on the same tick it re-reads comments — so a reviewer's remark, or a thread someone resolved elsewhere, lands while you're reading rather than only on the next open. It's a local file read, so the tick never waits on the network. **`/` searches** — the diff's own content when the diff holds the keyboard, the file list when one of the lists does. `/` means search to anyone who has used vim or `less`, and it used to mean "filter the file list" everywhere including inside the diff, where nearly all the time goes; now it does what the pane you're in makes it mean. The search is **incremental** — each keystroke jumps to the first match of what you've typed so far, measured from where the search started rather than from the last match, so narrowing a query doesn't walk the cursor down the file. `enter` keeps the query (so `n`/`N` step matches afterwards, wrapping at both ends), `esc` abandons it and puts the cursor back where it was. The match is centred and the footer counts it (`/needle · 2 of 7`). Code lines only, and a wrapped line counts once: a conversation is reachable through the comment index, which beats stepping past it with `n`, and matching prose would make `n` walk through remarks while you're looking for an identifier. A folded file contributes no lines to search, so a fruitless search says how many files are folded rather than letting a hit inside one read as an absence. **`P` publishes the review** to the PR — from inside the diff view (in the row list `P` is the scope cycle). **Two screens.** The first carries the **verdict and the review body together**, because choosing one and writing the other are a single thought: the verdict is one row you cycle with `tab` — **comment**, **request changes**, **approve**, GitHub's three, escalating in that order — and under it a textarea for the summary that `request changes` and `comment` require and an approval may want. **The box opens on the review summary the review already has** — they are one thing, so an empty box beside a summary sitting at the top of the diff would have invited a second one, and both would have gone up. It is prefilled with the text that will actually be sent, marker included, so an agent's 🤖 is visible before you publish its words under your account. The box keeps the keyboard the whole time, so `j`/`k` and the arrows type rather than moving a selection, and it is the same compose box the diff uses (`alt+enter` newline, `ctrl+g` out to `$EDITOR` — a summary is the longest thing anyone writes in a review). **`comment` is the default, not `approve`**: the default on an irreversible outward action should claim the least, and approving first meant a stray `enter` `enter` could put an approval on someone else's PR — the cost of not defaulting to it is two taps of `tab`. Leaving the box empty is a skip for a verdict that needs no body, so approving stays `tab` `tab` `enter` `enter`; choosing a verdict that needs a body with an empty box is refused **on that screen**, next to the box that fixes it, rather than one screen later by the plan or two later by GitHub. A review-level remark already on record counts as the body, so an empty box is not the same as having nothing to say. What you type is **not filed until the publish succeeds** — it used to be saved on the way out of the box, so backing out left the remark behind, and four abandoned attempts became four review-level comments on a real PR. The second screen shows **exactly what will be sent**, one line per API call — `POST pulls/54/comments  a.go:12-18  commit=1a2b3c4d5e6f  Suggestion: …`, `POST pulls/54/reviews  event=APPROVE` — and only a second, explicitly-labelled `enter` sends them; `esc` there goes back to the compose screen — where both the verdict and the summary are — since the usual reason to reject a plan is that one of them is wrong, and the text you wrote survives the round trip. Publishing is irreversible and outward-facing, so a menu choice is not the last thing between reading a diff and posting on someone's PR. The preview is the same code path as the real run (and as `--dry-run`), so it cannot describe a different run than the one it is previewing — which also makes it the diagnostic when a publish looks like it did nothing: an endpoint and a target either look right or they don't. Afterwards the report stays on screen until dismissed, failures included, because a run that posted six of eight has to say which two. That is the decision a reviewer is actually making when they finish reading, and the moment you are most certain of it is the moment you just finished — so it happens here rather than after leaving the view to find a shell. Every screen is titled just `Publish review`; which one you are on is said by the keys underneath it, and the counts sit next to the calls they describe. It works from any pane since it acts on the review rather than on a pane's selection, and `esc` declines silently. The review-level remarks become the review's **summary**, joined under whatever you typed on the first screen. `request changes` and `comment` need a summary — GitHub's rule, and its own UI's — and an approval needs none, so approving a PR whose comments went up earlier is `P` `enter` `enter`. The submission runs off the update loop (it is one API call per comment) and the footer reports what landed, failures included; a second `P` while one is in flight is refused, since a comment is only marked published once GitHub has answered for it. It is the same code path as `awp review publish`, so a publish from here and one from a shell cannot drift. `ctrl+r` forces a refresh, `esc`/`q` closes. (`c` opens the view from the row list, but inside it `c` means comment — it does not close.) |
| `v` | VCS window (`jjui`) |
| `s` | Shell window |
| `i` | CI window (`gh run watch`) |
| `r` | Pick a PR to review |
| `x` | User actions menu (configurable via `actions` in config) |
| `n` | New workspace (inline form: workspace name / start-from / agent prompt). `start-from` is a select with `main` (default) and `pick a bookmark…` (opens the bookmark picker). The form also surfaces a `Will create bookmark:` hint when `deck.bookmark_prefix` is configured. |
| `o` | Open: fuzzy-pick a project from configured roots (tmux-sessionizer style) |
| `f` | Find: easymotion-style section → workspace jump. Stage 1 collapses the list to just section headers — both pinned register sections (see the `m` chord) and unpinned project headers — and hints each one, so a long list fits on one screen; picking one expands only that section (the rest stay as one-line headers for context) and scopes stage 2 to its rows. `backspace` re-collapses to the header list. (In the inbox scope there are no headers, so `f` hints every row directly.) |
| `/` | Filter rows · `esc` clears |
| `P` | Cycle scope: all → attention (mini-deck criteria: active agent or unread notification) → inbox (open-PR workspaces sectioned by next move — see below). Starts at `all` unless `awp deck --scope=<scope>` is passed at launch — not persisted across opens. |
| `g g` / `G` | Jump the cursor to the top (`gg` chord — press `g`, then `g`) / bottom (`G`) of the list, vim-style |
| `ctrl+u` / `ctrl+d` | Jump the cursor half a page up / down (vim-style), then scroll the list to follow |
| `L` | Switch to last tmux session |
| `R` | Rename workspace (inline form: edit name, `enter` to rename, `esc` to cancel). Updates jj workspace, tmux session + window, and state — the on-disk directory keeps its original path. Not allowed on `default`. |
| `B` | Link a jj bookmark to the selected workspace (drives the per-row PR glyph) |
| `d` | Open the selected workspace's auto-discovered dev URL in your default browser |
| `p o` | Open the selected workspace's PR in your default browser (chord — press `p`, then `o`). `esc` cancels the chord. |
| `p m` | Merge the selected workspace's PR. Opens a confirmation modal showing the PR number, title, and the exact command (`gh pr merge <n> --squash`); `y`/`enter` confirms, `n`/`esc` cancels. The merge runs immediately and the progress modal stays open until gh reports success or failure — gh's own output (including why a merge was rejected, e.g. failing checks or pending review) streams into the log. Dismissing the modal refreshes PR status so the row glyph updates. Squash is used because `gh pr merge` has no non-interactive "repo default" mode. On branches that require a **merge queue**, `gh pr merge` is broken when the repo's queue is configured without `allow_auto_merge` ([cli/cli#13398](https://github.com/cli/cli/issues/13398) — gh only ever calls `enablePullRequestAutoMerge`, which that setting gates, and never `enqueuePullRequest`). awp detects the merge-queue / auto-merge-blocked signature in gh's output and works around the bug by calling the `enqueuePullRequest` GraphQL mutation directly (`gh api graphql`), so the PR is added to the queue and the log reports its queue position/state. |
| `p d` | Open the selected workspace's PR description in a `pr` window of its tmux session (the same way `r` opens a `review` window), running `gh pr view <n> \| less -R` with TTY formatting forced. `q` in less drops back to a shell in the window; re-running `p d` reuses the window. |
| `p r` | Repair the selected workspace's PR. Detects actionable conditions (merge conflicts, failing CI, branch behind base, changes requested by a reviewer, a pending request for **your** review on someone else's PR) and opens the `A` send-prompt form prepopulated with a fix prompt, so you can review and edit it before sending to the workspace's agent. Reports "nothing to repair" if the PR is healthy. **Your own PR with review feedback:** when the repair covers review comments / changes requested on your PR, the prepopulated prompt is approval-gated — it asks the agent to read and understand each point and report the problem plus a proposed solution for your approval *first*, then (once you approve) address the points, push, reply to the threads, and re-request review if needed. If other actionable issues (failing CI, conflicts, behind base) share that prompt, the whole prompt is gated the same way so it reads consistently. Repairs that don't involve review feedback keep the immediate "fix it and push" prompt. **Re-reviewing someone else's PR:** nothing extra happens on submit, even when the deck flags the row **stale** (the `· stale` chip — the author pushed since you opened the review). Findings are anchored to line content rather than to a head SHA, so the ones you already filed follow the code across the author's push instead of being stranded against an old diff; re-open `c` to read the current state. |
| `p s` | Set (or clear, via blank/0) the PR # for the selected workspace. Pins the workspace to a specific PR so the deck resolves status by number rather than guessing from the bookmark. Persisted to `~/.awp/...` workspace state. |
| `D` | Delete workspace · on a `default` row, deletes the **project**: removes every other workspace under that repo and drops the project from the deck (the default workspace itself is left intact). Requires typing the project name to confirm. |
| `,` | Edit global state file in `$EDITOR` |
| `m m` | Pin the selected workspace to the **default** group (chord — press `m`, then `m`). Pinned workspaces float to a ★-marked section at the top of the deck (all / attention scopes), above the project groups and out of their own project group. Pressing `m m` again on a default-pinned row unpins it. |
| `m` + `a`–`z` | Pin the selected workspace to the letter group (e.g. `m a`). Groups are single-letter registers, vim-mark style; sections order default-first then alphabetically by name-or-letter. Aiming at the group the row is already in unpins it; a different letter moves it. While the chord is pending, each pinned section header shows a highlighted `[x]` chip so you can see which registers are in use. |
| `m D` | Unpin / ungroup the selected workspace. |
| `m R` | Name the selected row's group — opens an input to set a display alias for that register (cosmetic; the register key stays the letter). Aliases persist globally in `~/.awp/pin-groups.json`. Blank clears the alias. |
| `J` | Jobs overlay (running async dispatches — cancel, retry, dismiss, open log, yank to clipboard) |
| `w` | Watch overlay — live dev-loop progress for the selected workspace (units + loop phase + gate pass/fail), read from the agent's transcript; scroll with `↑`/`↓` / `pgup`/`pgdn`, `esc`/`q`/`w` to close. See [`awp watch`](#cli-reference-highlights) and the `dev_loop` config. |
| `W` | Open the same watch view as a real tmux **window** in the workspace's session (running `awp watch`), rather than the in-deck overlay — useful for leaving it up alongside the agent/review windows. No-ops for repos without a `dev_loop`. |
| `?` | Help overlay (scrollable — `↑`/`↓` or `j`/`k` to scroll, `pgup`/`pgdn` / `ctrl+u`/`ctrl+d` to page; `?` / `esc` / `q` / `enter` to close) |
| `q` / `esc` | Quit |

## CLI reference (highlights)

| Command | Purpose |
|---|---|
| `awp deck [--scope=all\|attention\|inbox]` | Open the workspace dashboard. `--scope` sets the initial filter (default `all`); `P` still cycles through every scope inside the deck. `pr` and the legacy `open-pr` are accepted as aliases for `inbox`. |
| `awp mini-deck` | Quick-jump list of workspaces with an active agent or unread notification |
| `awp w open [name]` | Create or attach to a workspace. Run with no name to drop into the same unified form the deck's `n` key shows: workspace name, `Start from` (`main` by default, or `pick a bookmark…`), and an optional agent prompt. To review a PR instead, use `awp review`. |
| `awp w list` | List workspaces in the current repo |
| `awp w info <name>` | Show details for a workspace |
| `awp w rename <old> <new>` | Rename |
| `awp w delete <name>` | Delete (use `--force` to skip prompts) |
| `awp w prune [--dry-run] [--force]` | Remove orphan workspace dirs under `~/.awp/workspaces` not tracked in state |
| `awp w bootstrap [name]` | Re-run bootstrap hooks for a workspace |
| `awp w bootstrap --all` | Re-run bootstrap hooks for every tracked workspace in the current repo (continues on failure) |
| `awp review add` | File a review finding from a script or agent: `awp review add --file <path> --line <n> [--end-line <n>] [--side new\|old] [--type comment\|suggestion\|question] [--text <line>] [--end-text <line>] (--body <text> \| --body-file <path>)`. **`--body-file` is the one to use for anything with markdown in it** (`-` reads stdin), and `awp review reply` takes it too. A finding is markdown and markdown is full of backticks; putting one through a shell argument means escaping it for a quoting context the caller has to guess, and guessing wrong is silent. That is not hypothetical — seven agent findings reached a real PR reading ``Pin the \`graphql_client\` git dep``, backslashes and all, because a backslash-backtick inside single quotes is two literal characters rather than an escaped backtick. A file has no quoting, so nothing can be mis-escaped into it. As a backstop, a `--body` in which *every* backtick is escaped and none is plain is un-escaped on the way in: no author escapes uniformly, so that pattern is a quoting accident rather than intent — while a body that mixes escaped and plain backticks is saying something deliberate and is stored exactly as written. A body from a file is never touched, since it went through no shell. `--end-line` files a finding about a **block** — the same thing `v` selects in the diff — with `--end-text` anchoring its last line the way `--text` anchors the first; an end equal to `--line` is simply one line, and an end *before* it is rejected rather than quietly dropped — the difference between "line 12" and "lines 12-18" is the whole content of the flag. **Omit `--file` for the review summary**, which the diff shows in its own section above the first file. `--file` and `--line` go together: either both or neither. `--type` says what the finding is asking for — it drives the colour the comment renders in, so a triager can tell a proposed change from a question without reading every body; an unrecognised value falls back to `comment` rather than dropping the finding. The review is resolved from the workspace you are in — there is no session path to discover. Findings appear inline in the deck's diff view (`c`). Comments are anchored to the line's **content**, not its number, so they follow the code as an agent edits around them; one that can no longer be located is shown in a detached section rather than dropped. |
| `awp review reply` | Reply on an existing finding's thread: `awp review reply --to <comment-id> [--type comment\|suggestion\|question] --body <text>`. The reply threads under its parent and flips that parent back to needing your attention, so an exchange stays one item rather than becoming two. The comment id is included in the prompt the agent receives. |
| `awp review list` | List the current workspace's findings — id, kind, state, location, and a one-line body (`--json` for machine output). |
| `awp review publish` | Post the review's unpublished findings to its PR — anchored ones as inline comments, **review-level ones as comments on the PR itself** (a GitHub review comment needs a line to attach to, so a remark about the change as a whole has nowhere inline to go). The PR-level ones go up after the inline ones, so a closing summary lands under the specifics it refers to. A `suggestion` or `question` body is prefixed with its kind, capitalised (`Suggestion: …`) — it opens the comment, and a lowercase word starting a sentence reads as a typo rather than a label — GitHub has no notion of awp's palette, so the kind has to be spelled out there — while a plain `comment` gets no prefix at all, since it is the default and labelling it would label every remark that had nothing special to say. Anything a robot filed carries 🤖, **in front of the kind** (`🤖 Suggestion: …`) — an agent's comment posts under your account with nothing else to distinguish it, and who wrote a remark frames everything after it, including what the remark is asking for. Replies omit the kind, since the thread's first comment already carries it. The PR defaults to the one the workspace is pinned to — the number `awp review <n>` recorded when it created the workspace, or the one you set with `p #` — so you don't retype it; `--pr <n>` overrides, and `--dry-run` shows what would go up. **`--verdict approve\|comment\|request-changes` submits the comments as a review**, which is the decision a reviewer is actually making when they finish. **`--summary <text>` / `--summary-file <path>`** writes the review's body: without it, `--verdict comment` dead-ended on its own requirement, telling you to go and file a review-level remark without saying how. With a verdict the review-level remarks become the review's **summary** — what GitHub's review body is for — instead of separate comments on the PR; without one they keep going up as PR comments. `comment` and `request-changes` need a summary, the same rule GitHub's own UI applies (a verdict that asks for something has to say what), and that is checked **before anything is posted** — a run that published eight comments and then refused the verdict would leave you working out what landed. An approval needs no summary and can be submitted on its own, so approving a PR whose comments went up on an earlier run is one command. The verdict goes up as its own submission after the inline comments rather than as one batch carrying them, for the idempotency reason below. Inline comments are anchored to **the commit you read**, not to whatever GitHub says the head is now: a comment carries line numbers, and line numbers only mean anything against the commit they were read from, so a newer head would attach the remark to a diff nobody looked at. The order is what the review recorded the last time this resolved, then a commit the caller already knows (the deck reads every workspace's bookmark commit anyway, to spot one that has fallen behind its PR), then `@-` **in the workspace under review** — not in the source repo it belongs to, since a jj workspace has its own working copy and the repo root's answer describes a different change entirely; then the PR's current head as a last resort, so a review with no workspace can still publish. The working-copy commit itself (`@`) is never used: it has never been pushed, so GitHub would refuse it. Whatever comes out is checked against **the PR's own commit list** before anything is sent, because GitHub rejects a commit that isn't part of the pull request and there is no shortage of local commits that look plausible and aren't. A candidate that isn't on the PR falls back to its head and **says so** — the comments may land on lines that have moved, which is worth a sentence rather than a silent substitution. The resolved commit is then recorded on the review, so a retry anchors to the same one instead of re-deriving it from a workspace that has moved on. GitHub marks a comment against an older commit as outdated, which is the honest outcome. Idempotent: each comment records the thread it created the moment it succeeds, so a run that fails halfway can be retried without double-posting, and comments already on GitHub are skipped. Comments are posted individually rather than as one batched review submission — a partial failure inside a batch is unrecoverable, since you can't tell which comments landed. |
| `awp review [pr#]` | Pick or open a PR for review in a fresh workspace. Opens a `pr description` window and an `agent` window — no separate review window, because the deck's own diff view is the review surface (`c`, or `C` for the change against its stack base). The agent is primed with a precise commit-SHA diff range and files its findings with `awp review add`, which resolves the review from the workspace it's running in; there is no session path to discover. The PR's existing comments (inline review comments, review summaries, and conversation comments) are fetched and embedded in the prompt so the agent doesn't re-raise points already made — it's told to stay non-redundant but may agree or disagree with them. The full review instructions (the lengthy reviewing guide plus PR context) are written to `~/.awp/review-prompts/<repo>/<workspace>.md`; the agent receives only a short pointer prompt that names the PR and tells it to read that file, so the terminal isn't flooded with the whole guide (falls back to the inline prompt if the file can't be written). The file lives outside the workspace tree on purpose — a review workspace's own `.awp/` is symlinked to the shared source-repo `.awp/` during prep, so a prompt written there would be shared across every review and clobbered by the next one. Keying by repo + workspace name keeps each review's prompt private (even when workspace names collide across repos), and deleting or pruning the workspace removes the matching prompt file. The fetched PR is also written through to `~/.awp/pr-status-cache.json` and pinned to the new workspace as `PRNumber`, so `p o` / row glyphs resolve the instant `awp review` returns — no waiting for the next periodic fetch. Agent makes no file edits, commits, or GitHub comments. **Re-reviewing a force-pushed PR:** nothing to migrate. A review's identity is the workspace, not `(repo, PR, head SHA)`, and its findings anchor to line content — so a force-push or rebase relocates them the same way an agent's own edits do, and a re-run reads the same review it did before. |
| `awp watch [name]` | Read-only live view of an agent's progress on the current task, built from its Claude Code transcript. Shows the **units of work** (from the agent's task list / todos, falling back to a markdown checklist or `Unit N:` prose) coupled with the current unit's position in the project's **dev loop** (`explore → implement → verify → commit`), plus per-unit gate pass/fail and a churn/stall signal. With no name, it resolves the workspace from the session's `AWP_WORKSPACE` env when set (so it "just works" inside a workspace session), otherwise shows a picker. Observe-only — it never runs gates or steers the agent. Flags: `--once` (print one frame and exit), `--transcript <path>` (replay a specific transcript), `--suggest` (print a prompt to configure `dev_loop`), `--preamble` (print the loop instruction to give an agent, generated from `dev_loop`). |
| `awp diff [-r <revset>]` | awp's **review surface**, standalone — the same viewer the deck's `c` opens, with the same keys: comment (`c`), reply, edit, `v` ranges, mirrored GitHub threads, `r` reviewed marks, `ctrl+s` send to the agent, `P` publish. The review it reads is resolved from the working directory, the same lookup `awp review add` uses, so a finding you file here and one an agent files from the same directory land in the same place. In a directory that is not a tracked workspace it still opens as a review surface rather than refusing to comment. With no `-r` it shows the working copy; `-r` takes any jj revset — `awp diff -r @-` for the change before this one, `awp diff -r 'main..@'` for the whole stack against main, `awp diff -r andrew/thing` for a bookmark. The revset is re-resolved on every refresh tick rather than pinned to a commit id, so `-r @-` keeps meaning "the change before this one" as the stack moves under it, and the footer names the revset you typed rather than a resolved hash. `-r` is hand-parsed rather than read by a flag library, because a revset routinely starts with a character a flag parser would claim for itself (`-r @-`, `-r -3`). |
| `awp doctor [--global] [--fix]` | Health checks; `--fix` repairs missing hooks/env |
| `awp init hooks` | Install/update global Claude + pi integrations (idempotent) |
| `awp config init` | Bootstrap `<repo>/.awp/config.json` (must run from repo root) |
| `awp config edit [--global]` | Open the project (or `--global`) config in `$EDITOR` |
| `awp internal report-status --state <…> [--prompt <text>\|--prompt-stdin] [--waiting-when-tool <list>]` | Hidden — used by hooks to write status. `--prompt` stores the active prompt text on the workspace; `--prompt-stdin` reads it from a Claude-style hook JSON payload on stdin. `--waiting-when-tool` takes a comma-separated list of tool names; when a `PreToolUse` payload's `tool_name` matches, the recorded state is overridden to `waiting` so blocking tools (e.g. `AskUserQuestion`) badge the row instead of staying in `working`. |
| `awp internal gate record --result <pass\|fail> [--json]` | Hidden — the `PostToolUse(Bash)` / `PostToolUseFailure(Bash)` enforcement hook. Records the run command's gate pass/fail (verdict from which event fired) into the workspace snapshot and emits a transition nudge. `--json` prints the recorded result for debugging. See [`dev_loop` → Enforcement](#dev_loop). |
| `awp internal gate check [--hook] [--workspace <ws>]` | Hidden — the `PreToolUse(TaskUpdate)` enforcement hook (`--hook`): resets a unit's gates on `in_progress`, blocks `completed` (exit 2 + reason on stderr) until the unit's gates are green, and seals a green completion so the next unit starts fresh. Without `--hook`, a self-check the agent can run: exit 0 when ready, else non-zero + reason. See [`dev_loop` → Enforcement](#dev_loop). |
| `awp internal require-task --hook` | Hidden — the `PreToolUse(Edit\|Write\|NotebookEdit)` task-discipline hook. Blocks editing a non-markdown file (exit 2 + reason on stderr) unless a task is `in_progress` in the session's task list (`~/.claude/tasks/<session>/`). Markdown is exempt; fails open on any error. Like the gate hooks, self-gates on a configured `dev_loop` — no-ops in repos that haven't opted in. |
| `awp internal loop track` | Hidden — the matcher-less `PostToolUse` / `PostToolUseFailure` hook. Derives the current dev-loop phase from the tool that just ran and caches it on the workspace snapshot so the deck renders the current phase on the fast first paint. No-ops without a `dev_loop`. See [`dev_loop` → Enforcement](#dev_loop). |
| `awp internal unread-summary` | Print a tmux-status-bar badge of workspace activity (working + waiting + notified counts). Empty when nothing's working and nothing's unread. |
| `awp internal mark-read [--workspace <name>]` | Clear the unread badge for one workspace. Resolves from `$AWP_WORKSPACE` when no flag given. |

`awp doctor` checks environment tooling, the agent hook installs, and (when run inside a repo) per-repo configuration. `--global` skips repo-scoped checks and scans every live `[awp]*` tmux session across all projects. `--fix` reinstalls missing hooks and re-injects `AWP_WORKSPACE` / `AWP_REPO` into any session that's missing them.

## Configuration

awp reads JSON config from two locations and merges them (project wins):

- `~/.config/awp/config.json` — global
- `<repo>/.awp/config.json` — per-project

Example:

```json
{
  "agent": "claude",
  "actions": {
    "logs": { "command": "tail -f /tmp/app.log", "alias": "l" },
    "install": { "command": "pnpm install", "alias": "i", "background": true }
  },
  "hooks": {
    "bootstrap": ["pnpm install", "make migrate"]
  },
  "deck": {
    "project_roots": ["~/p", "~/go/src/github.com/andrewcohen"],
    "bookmark_prefix": "andrew"
  }
}
```

> **Note**: awp will refuse to operate on `$HOME` as a repo (deck open, workspace open/create/delete/rename, project picker selection) so workspace dirs and bookmarks don't end up scattered across your home.

### `agent`

Command used to launch the workspace agent. Invoked as `<agent> <prompt>` (with the prompt shell-quoted) when summoning with a prompt, or just `<agent>` when re-attaching via the `a` key. Defaults to `pi`. Common values: `pi`, `claude`, `aider`. Anything that accepts a prompt as its first positional argument works.

### `actions`

Custom commands surfaced by the deck's `x` action menu. By default each action runs in a new tmux window in the workspace.

Set `"background": true` to run the action detached via the jobs subsystem instead. The deck dispatches it without opening a tmux window; output is captured to `~/.awp/jobs/<id>.log` and the run shows up in the right panel's **Recent activity** list for that workspace. Failures appear in the bottom status bar's `⚠` count and stay until dismissed in the `J` overlay. Best for installs, lints, builds, or anything you'd rather not babysit.

Set `"focus": false` to keep the action foregrounded (it gets a real tmux window, runs interactively, scrollback intact) but **don't** switch the tmux client to it on launch. Useful for spawning a long-running watcher you'll check on later without losing your place in the deck. Ignored when `background` is true.

### `hooks.bootstrap`

Shell commands run after a workspace's jj layout exists but before the agent starts. Used for things like `pnpm install` or `make seed`.

> Built-in bootstrap **symlinks** `<repo>/.awp/` into each workspace rather than copying it, so config edits propagate across all workspaces immediately. Editing `<workspace>/.awp/config.json` writes through to the source repo.

### `deck.project_roots`

List of directories the deck's `o` (open) screen scans for projects. Tilde-expanded. The walker descends up to 4 levels and stops at any directory containing `.git` or `.jj`. Selecting a project summons (or creates) a tmux session named `[awp]<basename>__default` at that path and records a `default` workspace entry under that repo root in `~/.awp/workspace-state.json`, so the project appears in the deck on subsequent launches.

When the deck exits, `deck-cleanup` also kills any leftover `[awp]<repo>__<workspace>` tmux sessions that no longer have a matching entry in the workspace state file (the current session is always preserved). This keeps stray sessions from accumulating after a project is deleted from the deck.

### `deck.bookmark_prefix`

When set, a new workspace created with **no explicit bookmark** auto-creates a jj bookmark named `<prefix>/<workspace-name>` at the new workspace's revision and records it in `Entry.Bookmark`. The deck's per-row PR glyph matches `Entry.Bookmark` against PR `headRefName`, so the auto-bookmark lets a freshly-created workspace's PR (once pushed) light up in the deck without a manual `B`-link step.

Unset = no auto-create. The `B` key in the deck stays available for backfilling existing workspaces whose bookmark is empty.

### `dev_loop`

Defines the per-unit development loop that `awp watch` visualizes: the ordered `phases` a unit of work passes through, and the `gates` (named checks) awp recognizes in the agent's transcript. Each gate has a `name`, the `phase` it belongs to, and a `match` regex tested against the shell command the agent ran (a paired non-zero exit marks the gate red). Optional per-gate fields: `command` (the human-facing command shown in `awp watch --preamble`, distinct from the detection regex — use it to express intent like `"pnpm lint <files you changed>"`; falls back to the first alternative of `match` when unset), `not_match` (exclude commands that also match this regex — e.g. skip `wip:` commits), and `marker: true` (a phase transition like `commit` that advances the loop but isn't a pass/fail check, so it's kept out of the gate lights).

Unset = an inferred default (Go: `gofmt` / `go vet` / `golangci-lint` / `go test` / `go build`, then a `commit` marker). Run `awp watch --suggest` for a ready-to-paste prompt that has an agent inspect the repo and write this block; `awp watch --preamble` prints the matching loop instruction to give the agent, generated from the same config so the observed loop and the instructed loop can't drift.

**Auto-injection.** When `dev_loop` is configured (has phases/gates) **and** the agent is Claude, new coding workspaces launch the agent with the generated loop instruction appended to its **system prompt**: awp writes the preamble to `~/.awp/dev-loop/<repo>.md` and launches `claude --append-system-prompt-file <that path>`, so every new agent starts already following the loop `awp watch` observes — no manual paste. It's the system prompt (not a one-shot prompt prefix) so it persists across the session and applies even when the workspace is opened without an initial prompt. Note the instruction is **invisible inside Claude Code** — the system prompt is shown in neither the chat nor the transcript JSONL (that's the point: it keeps the prompt clean). The `awp review` flow is intentionally excluded (a reviewer shouldn't be told to work in units / run gates / commit), and non-Claude agents fall back to no injection (`--append-system-prompt-file` is Claude-specific).

**Enforcement (gate hooks).** The preamble is only a nudge — an agent can ignore it. When `dev_loop` is configured, awp also *enforces* the loop and keeps the deck's meta line live with three Claude hooks (installed by `awp init hooks`, see [How status reporting works](#how-status-reporting-works)):

- **`awp internal gate record`** — recording hooks on `PostToolUse(Bash)` **and** `PostToolUseFailure(Bash)`. Claude fires `PostToolUse` only after a command **succeeds** and `PostToolUseFailure` only after it **fails**, so the event itself is the pass/fail verdict (passed to `record` as `--result pass` / `--result fail`) — no exit-code parsing, no transcript scan. `record` matches the command against the `dev_loop` gates and writes the result into the workspace snapshot. A compound command (`gofmt && go test`) records only its **first** matching gate. It records only while a unit is in progress. On a gate transition — a gate goes red, or the unit's gates all turn green — it feeds a terse reminder back to the agent (rung 2); intermediate passes stay silent. The `nudge` field controls this: `"off"` (never), `"transitions"` (default), or `"verbose"` (also acknowledge each pass).
- **`awp internal gate check --hook`** — a `PreToolUse(TaskUpdate)` hook. When the agent marks a unit `in_progress` it resets that unit's recorded gates; when it tries to mark a unit `completed` it is **blocked** (the hook exits with code 2 and writes the reason to stderr, which Claude feeds back to the agent) unless every **required** (non-marker, non-[`optional`](#dev_loop)) gate is green. The reason names the unit, the blocking gate, and the command to re-run. [`optional`](#dev_loop) gates never block: when the required gates are green but an optional gate is still red, the completion is **allowed** and the hook exits 0 with a `PreToolUse` `additionalContext` reminder naming the still-red optional gate (advisory, not a block). A green completion also **seals** the unit: its results are kept (so re-marking the same unit `completed` stays allowed) but the next recorded gate starts a fresh set — so gates reset across a unit boundary even when the agent never marks the next unit `in_progress` (a common lapse). Run `awp internal gate check` yourself (no `--hook`) as a self-check: exit 0 when the current unit is ready, else a non-zero exit with the same reason.
- **`awp internal loop track`** — a matcher-less `PostToolUse` / `PostToolUseFailure` hook (fires for every tool). It derives the current loop **phase** from the tool that just ran (edits → `implement`, reads → `explore`, a gate command → that gate's phase, etc. — the same mapping `awp watch` uses) and writes it into the workspace snapshot, resetting it when a `TaskUpdate` goes `in_progress`. This keeps the deck's cached phase current on the fast first paint instead of lagging to the next transcript scan. It writes only when the phase actually changes, so a per-tool-call hook doesn't churn the state file.

The recorded gates and phase are the same data `awp watch` derives from the transcript; the deck's transcript scan on open reconciles the snapshot against ground truth (`done`/`total` still come from that scan), so a dropped hook self-heals. Repos with no `dev_loop` block are unaffected — the hooks no-op.

```json
"dev_loop": {
  "nudge": "transitions",
  "phases": ["explore", "implement", "verify", "commit"],
  "gates": [
    { "name": "fmt", "phase": "verify", "match": "gofmt|go fmt" },
    { "name": "vet", "phase": "verify", "match": "go vet" },
    { "name": "lint", "phase": "verify", "match": "golangci-lint" },
    { "name": "test", "phase": "verify", "match": "go test" },
    { "name": "build", "phase": "verify", "match": "go build" },
    { "name": "integration", "phase": "verify", "match": "go test -tags=integration", "optional": true },
    { "name": "commit", "phase": "commit", "match": "jj (commit|describe|squash)|jj git push", "not_match": "wip:", "marker": true }
  ]
}
```

**Gate flags.** `marker: true` marks a phase-transition detector (e.g. `commit`) that has no pass/fail outcome — it advances the loop's phase but never appears in the gate-lights row or blocks completion. `optional: true` marks an **advisory** gate: it's still recorded and shown in the gate-lights row, but a red (or not-yet-run) optional gate does **not** block marking a unit complete. When a unit completes with its required gates green but an optional gate still red, the completion is allowed and a reminder about the still-red optional gate is fed back to the agent. Use `optional` for checks you want tracked but not enforced per unit (a slow integration suite, a flaky external check). A gate with neither flag is required: it must be green before the unit can be completed.

## Tmux status bar badge

Add this to `~/.tmux.conf` to surface waiting / notified workspaces from any session:

```tmux
set -g status-interval 5
set -g status-right '#(awp internal unread-summary) | %H:%M'
```

`awp internal unread-summary` prints `● N` (working, green), `▲ N` (waiting, yellow), and/or `● N` (notified, grey) — empty output when nothing is working and nothing is pending, so the divider/clock collapses cleanly. Working is counted live regardless of the unread flag (mirroring the deck's always-on green dot), so the badge stays lit while agents are running, not just when something needs you.

## Async deck jobs

All "progress" actions in the deck — create workspace (`n`), review a PR (`r`),
CI watch (`i`), user actions (`x`), delete (`D`) — now dispatch a detached
subprocess (`awp run-job <id>`) instead of blocking the deck. The deck stays
fully interactive: navigate, dispatch more, `q` out — jobs keep running.

Each job lives at `~/.awp/jobs/<id>.json` (status record) and
`~/.awp/jobs/<id>.log` (full subprocess output). The deck's bottom status bar
inlines an active-set summary on the left:

```
▶ 2 ⚠ 1 ☠ 1                                  ready                       ? help
```

The selected workspace's right panel includes a **Recent activity** block
listing its most recent job runs (newest first, up to 5) — handy for seeing
what `install` or `lint` last did without opening the `J` overlay.

- `▶` Running / pending.
- `⚠` Failed — error details visible in the `J` overlay.
- `☠` Orphaned — subprocess died without flushing a final state (SIGKILL,
  OOM, crash). Detected via heartbeat staleness + `kill(pid, 0)` + a
  process-start-time check that also catches PID reuse. Terminal records
  — done, failed, or orphaned — are GC'd after 24 hours on next deck
  startup (cleanup runs in a background `tea.Cmd`, never blocks startup).
  Successful `pr-status` jobs are deleted as soon as the subprocess exits
  cleanly, since they're background polls the user never inspects;
  failures stick around so the `J` overlay can surface them.

Press `J` to open the jobs overlay:

| Key | Action in overlay |
|---|---|
| `↑` / `↓` (or `k` / `j`) | Move cursor |
| `g` / `G` | Jump to top / bottom |
| `c` | Cancel the selected running job (sends `SIGTERM`; subprocess flushes a `cancelled` record before exiting) |
| `r` | Retry a failed/cancelled/orphaned job (re-spawns from the original spec; useful after manually resolving a stale workspace and similar fixable conditions) |
| `D` | Delete the workspace named in the spec and re-spawn the job. Only enabled when the failed job's `ErrorKind` is `stale_workspace` — surfaced today by the workspace reconciler when an existing workspace can't be aligned to the requested bookmark (e.g. a half-finished prior review left it in a weird state). Press `D` to start clean. |
| `x` | Dismiss a finished/failed/orphaned record (deletes the JSON + log file) |
| `o` | Open the sidecar log file. Active jobs open with `less +F` (follow mode — new output streams in like `tail -f`; press Ctrl-C inside less to drop into normal navigation). Terminal jobs open with `$PAGER` (default `less`). |
| `y` | Yank current job details (id, status, error, steps, recent log) to the system clipboard via OSC 52 |
| `esc` / `q` / `J` | Close the overlay |

The `y` yank exists because tmux popups don't expose copy-mode, so dragging
to select text inside the deck doesn't work the way it does in a normal
tmux pane. If you want native mouse selection instead, hold your terminal's
"bypass tmux mouse" modifier while dragging — Option (⌥) in iTerm2 / Terminal.app
/ Ghostty, Shift in Alacritty — or turn off `set -g mouse` in tmux. On
terminals that honor OSC 52 (iTerm2, Ghostty, Kitty, WezTerm, modern xterm,
foot) the yank lands directly in your system clipboard; tmux must have
`set -g set-clipboard on` for the escape to pass through.

Completion is non-intrusive: created workspaces appear in the deck list via the
existing 2-second refresher, and you press `enter` on the new row to summon as
usual. No auto-quit, no auto-switch.

Inspecting a running job from a shell:

```sh
ls ~/.awp/jobs/                             # list records
jq . ~/.awp/jobs/20260502-or72.json         # status
tail -f ~/.awp/jobs/20260502-or72.log       # streaming subprocess output
```

`awp run-job <id>` is an internal subcommand spawned by the deck — you
shouldn't need to run it directly.

## Concurrent writes

Workspace state lives in a single `~/.awp/workspace-state.json` written from many places (every Claude/pi hook, the deck refresh tick, summon/delete/rename). Writes are guarded by an OS-level advisory lock (`flock`) on `~/.awp/workspace-state.json.lock` and committed via temp-file + atomic `rename`, so concurrent writers don't drop each other's changes or leave a torn file. The lock has a 2-second timeout — if a writer ever stalls, agent hooks fail loudly rather than blocking the agent's turn.

A workspace's pin group (the `m` chord, above) is stored as `PinGroup` on its entry in that same file. The per-register **display aliases** set by `m R` live separately in `~/.awp/pin-groups.json` (a small `register → name` map) because a pin register spans repos in the deck's merged view — the alias is a property of the register, not of any one workspace.

## How status reporting works

1. When awp creates or summons a tmux session, it sets `AWP_WORKSPACE`, `AWP_REPO`, and `AWP_REPO_ROOT` on the session env.
2. The globally-installed hooks (Claude) / extension (pi) run on every state transition. Each one calls `awp internal report-status --state <state>` from tmux; the CLI is a silent no-op when awp workspace metadata is missing.
3. The status writer mutates the workspace entry's `Status` field in `~/.awp/workspace-state.json`.
4. The deck watches the state file for changes and refreshes immediately when possible, while keeping a periodic poll as a fallback.
5. Crash fallback: if the agent pane has dropped back to a shell, the deck overrides the in-memory status to `exited` regardless of what's on disk.

For repos with a [`dev_loop`](#dev_loop), `awp init hooks` also installs Claude hooks that enforce the loop and keep the deck's meta line live (they no-op elsewhere): `PostToolUse(Bash)` / `PostToolUseFailure(Bash)` → `awp internal gate record` (records each gate's pass/fail — the success vs. failure event *is* the verdict, so no exit-code parsing), `PreToolUse(TaskUpdate)` → `awp internal gate check --hook` (resets a unit's gates when it goes `in_progress`, seals them on a green completion, and blocks marking a unit `completed` — via exit code 2 with the reason on stderr — until its gates are green), and a matcher-less `PostToolUse` / `PostToolUseFailure` → `awp internal loop track` (caches the current loop phase so the deck renders it on the first paint). The record hook preserves stdout so Claude reads its nudge; the check hook preserves stderr and its exit code so exit 2 blocks; the track hook swallows everything and always exits 0.

If the deck's status looks stuck, run `awp doctor --fix` to repair env injection and reinstall hooks.

## Repository layout

- `cmd/awp/` — main entry point
- `internal/cli/` — command dispatch, deck wiring, init/hooks installer
- `internal/deckui/` — Bubble Tea TUI model/view
- `internal/workspace/` — workspace lifecycle (jj + state + hooks)
- `internal/tmux/` — tmux client
- `internal/jj/` — jj client
- `internal/agenthooks/` — Claude Code + pi.dev integration installers
- `internal/config/` — project + global JSON config
- `internal/state/` — workspace state JSON store
- `internal/doctor/` — `awp doctor`
- `internal/diff/`, `internal/review/`, `internal/github/` — diff and PR review flows
- `specs/` — feature specs (start from `specs/spec-template.md`; use `scripts/new-spec`)

See `AGENTS.md` for contributor and AI-agent guidance.

## License

See repository.
