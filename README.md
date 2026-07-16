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

  A `PreToolUse(Edit|Write|NotebookEdit)` → `awp internal require-task --hook` entry enforces **task discipline**: it blocks editing a non-markdown file (exit code 2 + reason on stderr, which Claude feeds back to the agent) unless a task is currently `in_progress` in the session's task list (`~/.claude/tasks/<session>/`). Markdown (`.md` / `.markdown` / `.mdx`) is always exempt, so specs, READMEs, and notes never trip it. Unlike the status and gate hooks, this one is **not** tmux-gated and does **not** depend on a `dev_loop` — it applies in every session so agents create and track a task before touching code. It fails open (allows the edit) if `awp` isn't on `PATH`, the payload is unreadable, or the task state can't be found, so a hook error never wedges editing.

  These hooks re-sync automatically: opening the deck fires an idempotent install in the background, so after an awp upgrade (which may add events or bump the hook schema version) the global hooks self-heal on the next `awp deck` without you re-running `awp init hooks`. It only writes when something has actually drifted.
- `~/.pi/agent/extensions/awp-status.ts` — a pi.dev extension that reports state on `session_start` / `before_agent_start` / `agent_end` / `tool_execution_start` / quit-time `session_shutdown`. `before_agent_start` forwards the user's prompt text so the deck stays in sync with Claude. If statuses aren't landing, set `AWP_DEBUG=1` in the pi pane to write diagnostics to `~/.awp/pi-extension.log`.

The status/gate integrations are no-ops outside awp-managed sessions (they only run in tmux and `awp internal report-status` ignores sessions without awp workspace metadata), so they never affect your standalone Claude or pi usage. The one exception is the `require-task` hook, which is deliberately session-wide (see above) — it gates code edits in *every* Claude session, not just awp-managed ones. All honor `$AWP_BIN` if you need the hook to invoke a non-PATH `awp`.

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

The status is fetched once when the deck opens, with a single `gh pr list --state open` call per distinct repo that has at least one non-default workspace. Only open PRs are listed — the deck only ever displays bulk-list PRs that are open, and listing every recently-closed PR forced GitHub to compute the expensive per-PR CI rollup for ~100 PRs that nothing rendered. Terminal (merged / closed) status for a workspace's PR is filled in the cheap way: a per-PR lookup of the workspace's pinned PR number, plus a write-through right after you merge from the deck. The repos are fetched **concurrently** (bounded so we stay clear of GitHub's rate limits), and within each repo the PR list, the merge-queue lookup, and the per-PR top-ups all run in parallel. The fetch is throttled so the same repo is never re-queried within a minute. The throttle is bypassed for actions that materially change the PR↔workspace mapping: linking a bookmark to an existing workspace, creating a new workspace from a bookmark, and opening a PR review — those refresh the affected repo immediately.

The fan-out runs as a **detached job** in the same jobs subsystem that powers workspace create / delete / review. It's spawned via `Setsid`, so closing the deck (or its tmux popup) mid-fetch no longer drops in-flight work. Per-repo PRs are persisted to `~/.awp/pr-status-cache.json` atomically as each repo finishes; the job record itself lives at `~/.awp/jobs/<id>.json` and shows up in the deck's `J` overlay (you can dismiss / open the log there). The next deck open reuses an existing active pr-status job instead of spawning a duplicate.

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
| `c` / `C` | Review window: `tuicr -r @` / `tuicr -r main..@` |
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
| `p r` | Repair the selected workspace's PR. Detects actionable conditions (merge conflicts, failing CI, branch behind base, changes requested by a reviewer, a pending request for **your** review on someone else's PR) and opens the `A` send-prompt form prepopulated with a fix prompt, so you can review and edit it before sending to the workspace's agent. Reports "nothing to repair" if the PR is healthy. **Re-reviewing someone else's PR:** when you `p r` a PR that isn't yours and the deck flags it **stale** (the `· stale` chip — the workspace's local bookmark is behind the PR head, i.e. the author pushed since you opened the review), submitting the prompt first reloads the tuicr `review` window onto the PR's current head — in place, by sending tuicr's `:e` command to the review pane (tuicr re-fetches, re-anchors onto the current head, and auto-migrates existing draft comments forward, without tearing down the split) — and splices the resolved session path (plus any prior-head sessions still holding draft comments) into the prompt, so the agent posts its re-review into the session the window is actually showing. The status line reads `repair: reloaded review on <shortSHA> · sent`. The reload is keyed off the same stale signal the row already shows — not a second staleness check — so a non-stale reviewer repair (addressing comments on the current head), a workspace with no live review window, or your own PR sends the prompt unchanged. |
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
| `awp review [pr#]` | Pick or open a PR for review in a fresh workspace. Opens a `review` window running `tuicr pr <n>`, resolves the persisted session JSON path from tuicr's `active_sessions.json` / `index.json`, and primes the agent with that absolute path plus a precise commit-SHA diff range so it can `tuicr review add` findings without falling back to the (broken-for-PR-mode) `--repo .` lookup. The PR's existing comments (inline review comments, review summaries, and conversation comments) are fetched and embedded in the prompt so the agent doesn't re-raise points already made — it's told to stay non-redundant but may agree or disagree with them. The full review instructions (the lengthy reviewing guide plus PR context) are written to `~/.awp/review-prompts/<repo>/<workspace>.md`; the agent receives only a short pointer prompt that names the PR and tells it to read that file, so the terminal isn't flooded with the whole guide (falls back to the inline prompt if the file can't be written). The file lives outside the workspace tree on purpose — a review workspace's own `.awp/` is symlinked to the shared source-repo `.awp/` during prep, so a prompt written there would be shared across every review and clobbered by the next one. Keying by repo + workspace name keeps each review's prompt private (even when workspace names collide across repos), and deleting or pruning the workspace removes the matching prompt file. The fetched PR is also written through to `~/.awp/pr-status-cache.json` and pinned to the new workspace as `PRNumber`, so `p o` / row glyphs resolve the instant `awp review` returns — no waiting for the next periodic fetch. Agent makes no file edits, commits, or GitHub comments. **Re-reviewing a force-pushed PR:** because a tuicr session's identity includes the PR head SHA, a force-push/rebase leaves the review pane on an old head (stale diff) and strands the prior pass's draft comments in the old-head session — which `tuicr review list` no longer surfaces. On re-run, `awp review` compares the live session's head to the freshly-fetched PR head; if they differ it resets the `review` window so tuicr reopens on the current head, and — when the current-head session has no comments of its own — it locates the prior-head session(s) that still hold drafts (by scanning tuicr's `sessions/`) and instructs the agent to re-anchor and carry those comments forward into the current session. The old session JSON is left untouched as the source of record. |
| `awp watch [name]` | Read-only live view of an agent's progress on the current task, built from its Claude Code transcript. Shows the **units of work** (from the agent's task list / todos, falling back to a markdown checklist or `Unit N:` prose) coupled with the current unit's position in the project's **dev loop** (`explore → implement → verify → commit`), plus per-unit gate pass/fail and a churn/stall signal. With no name, it resolves the workspace from the session's `AWP_WORKSPACE` env when set (so it "just works" inside a workspace session), otherwise shows a picker. Observe-only — it never runs gates or steers the agent. Flags: `--once` (print one frame and exit), `--transcript <path>` (replay a specific transcript), `--suggest` (print a prompt to configure `dev_loop`), `--preamble` (print the loop instruction to give an agent, generated from `dev_loop`). |
| `awp diff` | Charm-styled diff viewer |
| `awp doctor [--global] [--fix]` | Health checks; `--fix` repairs missing hooks/env |
| `awp init hooks` | Install/update global Claude + pi integrations (idempotent) |
| `awp config init` | Bootstrap `<repo>/.awp/config.json` (must run from repo root) |
| `awp config edit [--global]` | Open the project (or `--global`) config in `$EDITOR` |
| `awp internal report-status --state <…> [--prompt <text>\|--prompt-stdin] [--waiting-when-tool <list>]` | Hidden — used by hooks to write status. `--prompt` stores the active prompt text on the workspace; `--prompt-stdin` reads it from a Claude-style hook JSON payload on stdin. `--waiting-when-tool` takes a comma-separated list of tool names; when a `PreToolUse` payload's `tool_name` matches, the recorded state is overridden to `waiting` so blocking tools (e.g. `AskUserQuestion`) badge the row instead of staying in `working`. |
| `awp internal gate record --result <pass\|fail> [--json]` | Hidden — the `PostToolUse(Bash)` / `PostToolUseFailure(Bash)` enforcement hook. Records the run command's gate pass/fail (verdict from which event fired) into the workspace snapshot and emits a transition nudge. `--json` prints the recorded result for debugging. See [`dev_loop` → Enforcement](#dev_loop). |
| `awp internal gate check [--hook] [--workspace <ws>]` | Hidden — the `PreToolUse(TaskUpdate)` enforcement hook (`--hook`): resets a unit's gates on `in_progress`, blocks `completed` (exit 2 + reason on stderr) until the unit's gates are green, and seals a green completion so the next unit starts fresh. Without `--hook`, a self-check the agent can run: exit 0 when ready, else non-zero + reason. See [`dev_loop` → Enforcement](#dev_loop). |
| `awp internal require-task --hook` | Hidden — the `PreToolUse(Edit\|Write\|NotebookEdit)` task-discipline hook. Blocks editing a non-markdown file (exit 2 + reason on stderr) unless a task is `in_progress` in the session's task list (`~/.claude/tasks/<session>/`). Markdown is exempt; fails open on any error. Not tmux- or `dev_loop`-gated — it enforces in every session. |
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
- **`awp internal gate check --hook`** — a `PreToolUse(TaskUpdate)` hook. When the agent marks a unit `in_progress` it resets that unit's recorded gates; when it tries to mark a unit `completed` it is **blocked** (the hook exits with code 2 and writes the reason to stderr, which Claude feeds back to the agent) unless every configured (non-marker) gate is green. The reason names the unit, the blocking gate, and the command to re-run. A green completion also **seals** the unit: its results are kept (so re-marking the same unit `completed` stays allowed) but the next recorded gate starts a fresh set — so gates reset across a unit boundary even when the agent never marks the next unit `in_progress` (a common lapse). Run `awp internal gate check` yourself (no `--hook`) as a self-check: exit 0 when the current unit is ready, else a non-zero exit with the same reason.
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
    { "name": "commit", "phase": "commit", "match": "jj (commit|describe|squash)|jj git push", "not_match": "wip:", "marker": true }
  ]
}
```

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
