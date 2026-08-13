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

  A `PreToolUse(Edit|Write|NotebookEdit)` → `awp internal require-task --hook` entry enforces **task discipline**: it blocks editing a non-markdown file (exit code 2 + reason on stderr, which Claude feeds back to the agent) unless a task is currently `in_progress` in the session's task list (`~/.claude/tasks/<session>/`). Markdown (`.md` / `.markdown` / `.mdx`) is always exempt, so specs, READMEs, and notes never trip it. So is **anything outside the session's tree**, taken from the payload's `cwd` rather than from wherever the hook process happens to be running: the gate exists to keep changes to the code attached to a task, and a file that isn't in that tree isn't that code. Editing a review record under `~/.awp` was being blocked as if it were — and repairing one by hand is often exactly what a debugging session has to do. A relative path is inside by construction (it resolves against the same `cwd`), but only after cleaning, so one that climbs out with `../` is not. When the check can't tell — no path, no `cwd` and no working directory — the gate stays on rather than handing out an exemption for an unparsed payload. Like the gate hooks, it only enforces on repos with a [`dev_loop`](#dev_loop) configured — the command self-gates on the same `watch.IsConfigured` predicate, so a repo that hasn't opted in (or a session outside an awp-managed workspace) is never blocked. It fails open (allows the edit) if `awp` isn't on `PATH`, the payload is unreadable, or the task state can't be found, so a hook error never wedges editing.

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

### The top row

The deck's first row says what wants you on the left, in dots and numbers
ordered by how much it is your problem, and which scope you're looking through
on the right:

```
   ● 2  ● 1  ● 3                                            scope: all

   frontend
 ┃ ● checkout-fix
```

Yellow is waiting on you, green is working, grey is notified — the same coloured
dot the matching rows wear a couple of lines below, on the same column, and the
same badge [`awp internal unread-summary`](#tmux-status-bar-badge) puts in the
tmux status bar. `?` has the legend. When nothing wants you the badge disappears
and the row is just the scope label.

The counts cover every workspace, not just the ones the current scope shows, so
pressing `P` changes which rows you see without changing what the badge says is
waiting.

Under `awp zdeck` the badge **stays on screen inside a pane**, at the left of the
deck's top row, ahead of the label of what is on screen and the
`ctrl+| menu · ctrl+\ deck` hint. A pane is where most of the time goes, and the counts are the reason to go
back to the row list — so they follow you into it, and they keep counting while
you are there: an agent that finishes its turn behind the pane shows up without
your leaving. The row is one row for three things, so a narrow terminal drops the
label first and the badge second; the leave key never goes. The scope label stays
behind on the deck's own title row — which slice of the list you were looking
through is not a question a pane raises.

**That row is in the same cells on every screen that is about your workspaces** —
the row list, one pane, or a split of two. It is the deck's row, not the pane's or
the list panel's: it spans the terminal on row 0, indented to the same column the
rows' status dots sit in, and a pane below it renders no header of its own.

It used to be three separate things — a header the pane drew inside its own
border, a copy the split drew above both halves, and a title row the list drew
inside its panel — so the badge, which is the thing you are actually glancing at,
sat in a different cell on each of the three screens you move between constantly.
One row, one address. A pane also gets back the row its header was spending.

What changes between the screens is only what the row has to say. Over a pane or a
split it names what is on screen, that workspace's own state, and the two keys that
act on it — `ctrl+| menu · ctrl+\ deck`, dropping the menu on a terminal that
cannot send it. Over the row list it names the scope — there is nothing to leave, and no
one workspace to report on. The badge is at the left on all three.

Between the badge and the label the row reports **the hosted workspace's own
state** — the row you came from, so you can tell from inside a pane whether the
thing on screen has fallen over:

| on the row | means |
|---|---|
| `#412` | the workspace's PR, tinted the way its row tints it — grey merged or closed, red CI failing, yellow CI pending or draft, green approved |
| the glyphs after it | the same PR glyph cluster the row list shows, in the same order: state, blocked-on-base, behind, locally stale, review conversation |
| `3/7` | dev-loop units done over units total |
| `✗2` / `○2` / `✔5` | the dev loop's gates: how many are failing, else how many have not run, else how many pass |

The gate mark is a digest rather than one glyph per gate. With no room for names,
a bare run of ticks and crosses cannot say *which* gate failed — and the order
that would carry that is the loop's, which is stored as a map by the time the deck
sees it. "Two failing" is what a glance is asking anyway; `w` names them.

Everything there is a glyph and a number, never a word — the same rule the badge
already followed. The only prose on the row is the name of what is on screen and
the key that leaves. The segments are separated by **space, not bullets**: each one
is internally punctuated already, so gluing them with ` · ` meant most of the ink
on the row was separator. A split does not list its two halves there — the
accent-vs-muted border already says which half has the keys, so naming both of
them spent the row's best columns on the one thing that cannot tell them apart.
Resizing the divider likewise reports nothing: you pressed the key, and the
divider moved.

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

Under `awp zdeck` only the **agent** pane clears it, and it clears both on the way in and on the way out. The badge means the agent produced output you have not seen, and its pane is the only surface that shows it; a tmux window switch cleared the badge because it put you in the session with the agent one key away, which is not true of a pane.

Closing the pane clears it too because entering cannot cover an agent that finishes *while* you are watching. That case is what `report-status`'s "don't badge a workspace the user is looking at" check exists for, and it answers by asking tmux whether a client is attached — under a deck that hosts its own panes there is no tmux and the answer is always nobody, so the badge lights while you are reading the very output it points at. Leaving the pane is the moment the reading finished.

**Workspaces still being created** show up in the deck the instant you submit the new-workspace form — an **optimistic row** appears immediately (meta line `creating…`) rather than waiting for the detached create subprocess to write state and a refresh to surface it. Once `jj workspace add` registers the workspace the optimistic row is reconciled into the real one, which keeps the spinner and switches its meta line to `setting up · <current step>` (e.g. `setting up · pnpm i`) while the bootstrap hooks (`pnpm i` and friends) run and the agent/tmux session launch. In both phases the row is badged with the animated **spinner** in place of the status dot. Workspace actions on it (`enter`/summon, window opens, send-prompt `A`, delete `d`, rename `R`, link `l`) are held with a `… is still being created` / `… is still setting up` toast until the create finishes — attaching before the session exists, or deleting mid-create, would race the create subprocess. The badge and guard clear automatically the moment the create job finishes (and the optimistic row is dropped if the create fails).

**Opening a PR review** (`r`, or `enter` on an *awaiting your review* inbox row) gets the same treatment: the review checks out the PR into a `pr-<n>-<branch>` workspace, and that row appears immediately as an optimistic `setting up · <current step>` row (the step tracks the review job — `jj git fetch`, `Prepare jj workspace`, opening the tmux windows, …) instead of waiting for the detached review subprocess to write state. Since the row carries the PR number, it supersedes the read-only inbox placeholder for the same PR rather than rendering next to it. As with create, workspace actions are held until the review finishes. (When the PR head ref isn't known at dispatch the name can't be predicted, so the row simply appears after the next refresh — the pre-existing behavior.)

**Workspaces being deleted** get the same spinner treatment: while the delete job runs, the row stays visible with the spinner and a `deleting…` meta line, then disappears the moment the delete finishes (rather than lingering until the next periodic refresh).

**Dev-loop progress on the meta line.** While a workspace's agent is **actively working**, its row's meta line switches from the usual branch/port to a live snapshot of the agent's dev loop, progress-first: `<done>/<total> · <phase> · ▶ <current unit>` (e.g. `3/7 · implement · ▶ wire up the meta line`). The `<done>/<total>` count is the agent's todo/unit list, `<phase>` is the current dev-loop phase (`explore → implement → verify → commit`), and `▶ <current unit>` is the in-progress task. `explore` is the pre-task-list stretch — investigating or writing the spec, before the work is broken into a task list; once a task list exists, each unit cycles `implement → verify → commit`. It's the same data the [`w` watch overlay](#key-bindings) shows, condensed to one line — read from the agent's Claude Code transcript by the deck's background refresh (so it lags live activity by up to the refresh interval; open the `w` overlay for a second-by-second view including gate pass/fail and churn). Each fresh snapshot is cached in `workspace-state.json` (the `DevLoop` field on the entry), so the next deck open renders progress on the very first paint instead of flashing the branch/port meta while the transcript is (re)scanned; the cache is rewritten only when the snapshot actually changes. For repos with a [`dev_loop`](#dev_loop), the phase and gate pass/fail are additionally kept live by event-driven hooks (`awp internal loop track` / `awp internal gate record`), so the cached snapshot reflects the *current* phase on open — even right after a phase switch — rather than the last scan's (`done`/`total` still refresh via the scan). Any missing slot drops out, and the row falls back to its normal branch/port meta the moment the agent stops working, once **all units are done** (a finished `12/12` loop has nothing in progress to surface), or if there's no transcript / no progress to show yet. Uses the project's [`dev_loop`](#dev_loop) config, or the inferred default loop when none is set.

The transcript scan behind that line is **incremental**. A refresh folds only the
bytes the agent has appended since the last one, keeping the accumulated state and
the offset it stopped at; a row whose agent has written nothing costs a `stat` and
no read at all. It used to replay the whole file every refresh, which is fine for
a fresh session and ruinous for a long one — a profile of a real `zdeck` session
(`AWP_PPROF`) spent **25% of the process** in that fold, ~50 MB/s of JSON, against
a transcript that had reached **251 MB**, plus most of the scheduler churn around
it. `BenchmarkRefresh` in `internal/watch` measures both ways: on a 20k-line
transcript, 118 µs resumed against 337 ms full. A transcript that has shrunk is
taken to be a different file and folded again from the top, and a line without its
newline yet is left for the next pass rather than folded half-written.

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
| 󰻞 | Your review is requested on someone else's PR — blue for a first request, yellow when it's a re-request (you reviewed, the author pushed and asked again). **A request from a team you're in counts as a request from you** — see below. |
| 󰭹 | Review feedback on **your** PR (yellow) — a reviewer requested changes *or* left review comments (pairs with `p r`, which preloads a fix prompt for it). Fires on any `COMMENTED` / `CHANGES_REQUESTED` review, not just a formal "request changes": GitHub's review *decision* stays `REVIEW_REQUIRED` when someone only comments, so the glyph reads the review states directly. Suppressed once the PR is approved — `p r` still offers the feedback there, deliberately: a glyph sits on the row every frame and would read as "act on this" for a PR that is ready to merge, while `p r` is a key you pressed. |
|  | Blocked on base (red) — this PR is stacked on another open PR that isn't ready to merge yet, so it can't land until the base does. Derived from the stack graph (see the inbox scope); pairs with the `└─` tree connector that nests the PR under its base. |

**A review requested from your team is requested from you.** GitHub lets a request name a *team* instead of a person, and on a PR assigned that way nobody is named individually — so awp read it as a PR that wanted nothing from anybody, and the 󰻞 glyph, the attention scope's `your review` reason and `p r`'s pending-request repair were all silent on every repo that reviews by team. A request now also matches against **your own team membership**, read once per fetch from `gh api user/teams` and compared org-qualified (`acme-corp/consumer-team`), so one org's `platform-team` never stands in for another's.

That call needs the **`read:org`** scope, which `gh auth login` does not grant by default. Without it awp cannot tell "in no teams" from "not allowed to look", so it assumes the former and team-assigned reviews stay quiet exactly as they did before — nothing on screen is wrong, so nothing is reported. `gh auth refresh -s read:org` grants it, and `AWP_TRACE` records the refusal if you want to confirm that is what is happening.

When the workspace's local bookmark tip doesn't match the PR head commit on GitHub, the row gains a  glyph (yellow) and its meta line a `· stale` chip — the signal that what you have locally is behind (or otherwise diverged from) what's actually on the PR, so any previous review pass or in-progress work is out of date and a fresh re-review is warranted. Most useful for PRs on a collaborator's branch: the PR head on GitHub is the truth, and a difference means the author has pushed since you last fetched. Independent of `behind base` — that signals the PR is behind its target branch, while `stale` signals your local bookmark is behind (or diverged from) the PR's remote head. Only renders on open PRs.

**PR labels.** When the matched PR carries GitHub labels, the meta line carries a tag segment right after the author (before the branch) — a tag glyph (Octicon) followed by the comma-joined label names (e.g. `bug, enhancement`). Like the rest of the meta line it stays muted: label colors are per-repo and don't route through the deck's semantic palette, so only the names render, not GitHub's per-label color. The same tag chip trails each PR in the [`r` review picker](#key-bindings) (capped so a heavily labeled PR can't crowd out the title), and the labels are listed in the merge-confirmation modal (`p m`). Labels are read from the same `gh pr list` / `gh pr view` calls that drive the row glyphs — no extra request.

The status is fetched once when the deck opens, with a single `gh pr list --state open` call per distinct repo that has at least one non-default workspace. Only open PRs are listed — the deck only ever displays bulk-list PRs that are open, and listing every recently-closed PR forced GitHub to compute the expensive per-PR CI rollup for ~100 PRs that nothing rendered. Terminal (merged / closed) status for a workspace's PR is filled in the cheap way: a per-PR lookup of the workspace's pinned PR number, plus a write-through right after you merge from the deck. The repos are fetched **concurrently** (bounded so we stay clear of GitHub's rate limits), and within each repo the PR list, the merge-queue lookup, and the per-PR top-ups all run in parallel. The fetch is throttled so the same repo is never re-queried within a minute. The throttle is bypassed for actions that materially change the PR↔workspace mapping: linking a bookmark to an existing workspace, creating a new workspace from a bookmark, and opening a PR review — those refresh the affected repo immediately.

The fan-out runs as a **detached job** in the same jobs subsystem that powers workspace create / delete / review. It's spawned via `Setsid`, so closing the deck (or its tmux popup) mid-fetch no longer drops in-flight work. Per-repo PRs are persisted to `~/.awp/pr-status-cache.json` atomically as each repo finishes; the job record itself lives at `~/.awp/jobs/<id>.json` and shows up in the deck's `J` overlay (you can dismiss / open the log there). The next deck open reuses an existing active pr-status job instead of spawning a duplicate.

The same pass also **mirrors each pinned PR's GitHub review threads** into that workspace's review store (`~/.awp/reviews/<repo>/work-<workspace>/remote/threads.json`), which is what the diff surface reads for `T` / `R`. Doing it here rather than from the diff itself keeps the reviewers' conversation current — within the same per-repo minute as the glyphs — while leaving `c` as instant as the rest of the deck: the viewer only ever reads a local file, never the network. One fetch covers every workspace pinned to the same PR (a review workspace beside the author's own). A fetch failure leaves the previous mirror exactly as it was rather than blanking it, and a PR with no threads doesn't get a review store conjured for it. The per-repo Step in the `J` overlay reports how many threads landed.

The mirror records **each message's GitHub node id**, which is how the diff recognises **its own echo**. A comment published from here comes back as a thread on the next pass, so both records then describe one conversation — and the diff used to show every published comment twice, the local copy with `▶ github · 1 msg · you: 🤖 Suggestion: …` immediately beneath it (on a real PR, seven of eight mirrored threads were awp's own comments coming home). The mirrored copy is the one kept: it's GitHub's record of the same words, and only it knows whether the thread has been resolved, whether the code moved out from under it, and what anyone replied. The local record stays in the store — it's what makes a re-publish skip rather than duplicate — it just stops being drawn. Local **replies** move onto the mirrored thread rather than going with the parent, since a local reply is never published and the exchange with the agent is the reason the local record still matters. A reply you wrote *into* a mirrored thread reconciles the same way once it's posted, against the id GitHub gave it: GitHub's copy sits inside the conversation in order, where a reader looks for an answer, so that's the one drawn. Before it's posted — and if the post fails — yours is drawn instead, labelled `unsent`, and it's drawn even when `T` has hidden GitHub's side: a reply of yours that nobody has received is the last thing that should quietly disappear. Matched on the node id and nothing else: the id GitHub returns when it creates a comment is the id the mirror reports for that same comment, where body and line both drift (a comment filed against line 47 came back reported at 53, and editing a published comment locally changes its text). Cycling `T` to none hides GitHub's conversation *and* brings your own copies back: "don't show me GitHub's side" isn't a statement about your own remarks, and with no mirror on screen there's nothing for them to defer to. **Resolving** one goes the other way — a settled conversation takes the local copy of the same words down with it, and any local replies on it, because "this is settled" *is* a statement about the whole exchange. Getting those two backwards is what made resolving a thread put your published comment back on screen as a fresh-looking remark on code that had just been agreed. `T` brings a resolved conversation back as one conversation, not two. A mirror written before the ids were carried matches nothing and shows both copies, which is the right way to be wrong: the next refresh fills the ids in.

**Requires a patched (Nerd Font) terminal font.** Anyone running awp without a Nerd Font will see empty rectangles where the PR glyphs would render.

### Attention scope (`P`)

The second `P` scope is **one flat list — the rows with an agent on them, then everything that wants you, most-your-problem first**. Unlike the inbox it has no section headers and unlike the all scope it is not grouped by project — grouping would cut a single priority-ordered list into as many lists as you have repos, and put a different kind of claim at the top of each. So reading down it is reading down the priority (in bands — see below), and **every row says why it is there** on its meta line, since nothing else on screen does.

**Agent rows lead**, which is not where urgency would put them: there is nothing to do about an agent that is working. But the deck is watched as much as it is acted on, and the agent rows are the ones that are changing — scattered below the rows that want you they were the hardest thing in the list to keep an eye on.

**The order is by band, not by reason.** The list sorts into five groups —

1. the **agents**: `working`, `waiting on you`, `finished a turn`
2. `re-review requested`
3. `your review`
4. **your own PRs**: `PR needs action`, `approved, green`
5. `2h ago` — you were just here

— and *inside* a band by project, then label. So an agent working, stopping to ask, and finishing a turn does not move its row at all: only its dot and its reason text change. A red PR going green does not move either.

This is the second pass at the scope, and it exists because the first one was unusable. Ranking by the reason itself meant the sort key was a signal that changes every few seconds, so an agent's ordinary lifecycle walked its row several places up and down and displaced everything in between — with a few agents running, the list was never still. The bands are the divisions you would name looking at the screen, and the transitions inside one are exactly the frequent ones. (The other half of that fix: **the cursor follows its row** when the list does re-order, so a refresh landing never leaves your next keypress aimed at whatever slid into the slot.)

The reason a row *reports* is still the precise one, and the precedence there is the table's own order: a workspace whose agent is working and whose PR has gone red reads as `working`, because something is already on it. Position tells you the band; the row tells you which reason within it.

A row qualifies for any of these, and shows the first one it matches:

| Reason | What it means |
|---|---|
| `working` | An agent running right now. Replaced by the dev-loop progress when there is any — `3/7 · implement · ▶ <unit>` says "working" with more in it than the word does. |
| `waiting on you` | The agent asked something and stopped. The first of the ones that want you, and the only one where the work has actually halted. |
| `re-review requested` | You reviewed this PR, the author pushed and asked again. |
| `your review` | A PR you have **checked out** whose review is still wanted from you. |
| `finished a turn` | The agent finished since you last looked. |
| `PR needs action` | Your own PR: changes requested, CI red, or a branch that won't merge as it stands. |
| `approved, green` | Your own PR, one keypress from done. |
| `2h ago` | You were in this workspace recently and nothing else is true of it. |

**Review means still-wanted, not still-open.** GitHub clears the review request the moment you submit a review and re-sets it only if the author asks again, so a PR you have already reviewed drops out on its own — there is no rule saying so, and none needed. "Checked out" is also the whole difference from the inbox's *Needs your review* bucket, which deliberately includes PRs you have **not** pulled down; a PR with no local workspace is the opposite of one you are working on.

**PR state is read off the inbox's own classification**, not re-derived. The two scopes answer the same question about the same PR, and a second copy of "is CI red" is how they would come to disagree. Deliberately not *every* open PR, though: one that is merely open and waiting on somebody else is the inbox's business, and pulling those in would make this a second copy of a scope that already sections them properly.

**Recency** keeps a workspace listed for **4 hours** after it was last active, so looking at a row no longer deletes the only evidence you were in the middle of it — before this the scope was binary on the unread flag. The clock is `LastActiveAt` in `workspace-state.json`, written when the agent reports a status and when you open the workspace. A workspace with no timestamp — anything that existed before the field did — is neither recent nor stale: unknown is no opinion, not "last active in 1970".

**Every row carries a muted `[project]` chip** before its name — the mini-deck's pattern for the same situation. With no project headers nothing else says which repo a row is from, and a `default` workspace in each of six projects otherwise renders as six rows called `default`.

**Pinned rows still float to the top**, in register order, above everything: a pin is somewhere you asked for, and when the deck's guess disagrees with what you said the deck is not the one to trust. **The workspace you opened the deck from is always kept** even when it wants nothing, so the cursor has somewhere to land — it is the one row with no reason, and it sorts last. Since there are no project headers, `f` skips its project stage and hints every row directly, the way it already does in the inbox.

### Inbox scope (`P`)

The third `P` scope sections open-PR workspaces by *what your next move is*, like GitHub's pull request inbox, instead of by project. Buckets render as headers with counts, most urgent first; empty buckets are hidden:

| Bucket | Header color | Membership |
|---|---|---|
| Needs your review | teal | Someone else's PR with your review requested (or re-requested) — by name, or from a team you belong to (see **PR status** above). Re-reviews — ones you already reviewed that the author pushed to and re-requested — sort to the top of the bucket. |
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
| `c` | Review the change on awp's **own diff surface, in the deck** — no external reviewer. **One entry key.** It opens on the whole change **against its stack base** (awp resolves the base to the nearest stacked-parent bookmark — the closest bookmarked ancestor of `@` that is none of `@` itself, trunk, or the workspace's own bookmark — falling back to `trunk()` when nothing is stacked). **`@` is excluded structurally, not by name.** `trunk()..@` includes `@`, so a change whose own commit carries a bookmark used to answer that query with itself and open on an empty diff against a base the footer named confidently. It only ever showed in the **default** workspace, because everywhere else the workspace's recorded bookmark was excluded by name and happened to remove `@` along with it — and the default workspace never gets a recorded bookmark, so the guard doing the real work simply wasn't there, because that is what a review is normally of; the narrower readings are a keypress away rather than a key of their own. **`-` inside the view switches scope**: `c` the change vs its stack base, `w` the working copy alone, `t` the whole stack vs trunk. The footer becomes the menu for the one keypress it lives, listing all three with the current one marked, and anything else — `esc` included — cancels rather than falling through to the viewer. A scope is a property of the open view, so it is chosen from inside it: which range you want to read is a question you answer after you have started reading, and giving each range its own entry key spent the deck's scarcest namespace on it up front. Switching rebuilds the view, since a different range is a different diff and there is no reading position worth carrying across it. Every scope opens the same surface; only the revision range differs. The footer **names the base it resolved** — `vs main`, `vs andrew/parent-change` — rather than saying "vs stack base", which only described how the base was picked. That name is resolved in the background, so it appears a moment after the diff rather than delaying it, and `ctrl+r` re-resolves it (the one thing that moves a base is a rebase).<br><br>The right pane is **one continuous scroll over the whole change** — every file's hunks in sequence, each opened by a full-width `══ path ══` divider — so reading never means stepping in and out of a per-file view. The left column is a **jump index**: the file list on top — shown as a **tree**, so each directory is named once and its files are listed under it as basenames. A flat list spends its whole width on prefixes that mostly repeat, and the part that distinguishes the rows is what gets truncated away; naming `app/lib/navigation/` once gives that width back to the filenames. Directory rows are structure, not destinations: `j`/`k` still move over files only, because the cursor is an index into the file set that every seek, the reviewed marker and the stream's own file cursor all speak. On a narrow pane the indent is capped rather than allowed to push names off the right edge — nesting is the first thing to give up. Moving the selection seeks the stream, and scrolling highlights whichever file you're in — and, once the change has comments, a **comment index** beneath it listing every conversation (one row per thread, replies folded in as a `·n` count, a `⚠` on any whose anchor no longer resolves). `tab` cycles files → comments → diff; moving the comment selection seeks the diff to that conversation and **centres it in the pane** — a minimal scroll would put its first line on the bottom row with the rest of the thread below the fold, so the selected conversation gets the middle, with the code it is about still in view above it (near the top of the change there is nothing to scroll away, so it simply sits where it falls) — and `enter` just hands the keyboard over with the cursor already there and `D` deletes the selected conversation without leaving the list. The index takes at most half the column and never shortens the file list past usability. The view **refreshes itself** every couple of seconds and keeps your cursor on the line it was on, so you can watch an agent work.<br><br>A **line cursor** marks where you are, highlighted vim-style across the full width. The file list and comment index carry **the same band on their selected row**, so the selection reads identically wherever the keyboard is — and, as in the diff, a band is painted only while its own pane holds the keyboard. Never two at once: the band is what says which selection the keys are actually driving, and a second one would leave that ambiguous. The `┃` bar stays either way, marking the row you'll come back to — but **muted**, and so is the full-width divider of the file the cursor is in. Both used to keep the selection hue no matter where the keyboard was, which made the diff pane the brightest thing on screen while its keys were dead, and both *moved*: seeking from the file list or the comment index slid the bar down the pane and jumped the yellow divider from file to file, drawing the eye to a place nothing was being driven. Muted, they read as a bookmark instead of as a cursor. Nothing is removed — where you'll come back to is still marked, and it's still marked on the row and on the file. `j`/`k` move it a row, `ctrl+u`/`ctrl+d` a half page, `{`/`}` jump to the previous/next hunk *anywhere in the change*, `g`/`G` to the ends, and **`zz` centres the diff on the cursor** — the vim gesture, for when you have read down to something and now want to see what is around it rather than have it pinned to the bottom margin. The cursor doesn't move; only the scroll does. `z` on its own arms the chord and any second key other than `z` cancels it, `esc` included, so a mistyped `zc` does nothing rather than falling through and opening the compose box. `tab`/`shift+tab` switches pane (`enter` on a file drills into the diff), `h`/`l` pans horizontally with `0`/`$` for line start and end (gutter stays pinned; no-ops under wrap), `w` toggles line wrap, `e` opens the file in `$EDITOR` at the cursor's line. **`|` switches between the unified layout and a side-by-side one** — the key draws the split it produces. Unified stacks a rewritten line's old and new text and leaves you to diff them in your head, which is fine for a pure addition and bad for a *modification*, which is most of a review; side-by-side puts the two versions on one row so the change is a difference in position rather than something to reconstruct. A run of removals is zipped against the additions that follow it, so a rewrite sits opposite the thing it was rewritten into; surplus rows on either side leave the other cell **blank rather than echoing its neighbour**, because “nothing was here” and “this is unchanged” are different facts and the whole reason to split is that unified makes them look alike. Both cells get equal width whatever their line numbers measure — an off-centre divider reads as a rendering fault rather than as information. Everything else is unchanged: comments, GitHub threads and the compose box render **full width beneath the pair** (a conversation is about the change, and the change is the pair), one row is still one selection, and `c`/`v`/`r`/`R`/`A`/`/`/`{`/`}`/`zz` all behave identically. A pair **anchors to its new side** when it has one and its old side otherwise — not a new rule, it is exactly what a mixed `v` range already does. Wrap and the split are mutually exclusive: `|` turns wrap off, and `w` while split says `wrap is off in side-by-side — h/l pans` rather than doing nothing. Below 100 columns `|` **refuses and names the numbers**, suggesting `\` — two columns of thirty is not a diff, it is two truncated diffs, and silently falling back to unified would leave the key looking broken at the one width where you most need telling what to do instead. Toggling keeps you on the same source line, not the same row number (pairing changes how many rows a hunk has), and the layout is a reading preference for the change in front of you rather than something persisted. **`\` hides the left column**, giving the diff the full terminal width for reading wide code; your place in the change is kept, so toggling back returns you to the row you were on, and while it's hidden `tab` stays on the diff rather than cycling into panes that aren't drawn.<br><br>**`?` opens the key reference** — the whole keymap, grouped, scrollable with `j`/`k` and `ctrl+d`/`ctrl+u`, and `?`/`esc`/`q` closes it. The footer carries state (which workspace, **which PR** when the workspace is pinned to one — `awp#1234`, project name then number — which range, the last thing that happened) and a `? help` pointer, not a legend: there are more bindings than fit on a row, so any list short enough to display was a list that hid most of them. The reference is rendered from one declared group slice (`internal/ui/help.go::viewerKeyGroups`), the same arrangement the deck's `?` uses, so the keymap and its documentation can't drift apart. A **host** appends its own group after the view's — in the deck, the `esc`/`q` that go back to the row list, which it intercepts before the viewer ever sees them. They have to be documented here because the deck's own `?` is unreachable while the diff is open, and a host with no keys of its own adds nothing, so the reference never advertises a key that does nothing. The `-` chord is listed the same way, spelled out with whatever scopes the host installed, and omitted entirely when it installed none.<br><br>`c` leaves a **comment** on the cursor's line, and **`v` starts a range** when the remark is about a block rather than a line — the vim gesture, so the movement keys are the extension keys: `j`/`k`, `{`/`}`, `g`/`G`, all of them, upward from the anchor as readily as down. The whole selection carries the cursorline band, so what is selected is what is highlighted; `c` then comments on the block, and `esc` — or a second `v` — cancels. A ranged comment records **both** ends by content, the same way a single-line anchor records one, so it keeps covering the same block as the code moves, and it sits under the range's **last** line: that is where GitHub puts one, and it reads correctly — everything above the remark is what the remark is about. A range stays inside one hunk, because the lines between two hunks aren't in the diff at all and a selection spanning them would silently cover code you never saw (GitHub refuses the same shape on publish). A mixed selection anchors to the new side, dropping the removals from its ends — a removed line has no new-side number to be one — while a selection of nothing but removals anchors to the old side, since those lines exist nowhere else. Every surface then names the location the same way: `a.go:12-18` in the compose box's header, in the comment index, in the prompt sent to the agent, and in the publish log. The lines a range covers keep a **left bar in the comment's kind colour** for as long as the comment is there — while you are composing (recoloured live as `tab` cycles the kind) and afterwards on the saved comment — so the block a remark is about is visible while you read rather than only stated in its header. The bar shares the two columns the selection marker uses; the cursor still wins its own row, and the rows above and below keep the mark, so the range still reads as continuous. `awp review publish` sends it to GitHub as a real multi-line review comment (`start_line` + `line`). The compose box opens **inline in the stream**, directly beneath the line it is about (or at the foot of the thread, when replying), so the code under discussion stays on screen while you write about it. `tab` inside the box cycles the comment's **kind** — *comment* (blue, the default, no action implied), *suggestion* (red, proposes a change), *question* (yellow, wants an answer), *praise* (green, says something is good and asks for nothing — the only kind left out of the open-findings count, so a compliment never reads as work) — recolouring the box as you go, and the kind drives the hue of the saved block and its row in the comment index. Hue says what the remark is asking for rather than who wrote it; authorship is carried by a 🤖 marker, added automatically to anything filed under an author other than you — in the diff, in the comment index, and in the body posted to GitHub. The marker and the kind are composed at display/publish time rather than stored, so they can't be doubled by a re-publish or edited away. `enter` saves it, `ctrl+s` also sends it to the workspace's agent as an approval-gated prompt naming where the remark is rather than pasting the code around it (the agent is sitting in the workspace and can read the file; a remark beside a long table row once turned a one-line note into a multi-kilobyte message), **`ctrl+g` writes it in `$EDITOR`** instead — the same binding the new-workspace and send-prompt forms use, since a remark worth sending to an agent is often longer than four rows of textarea; the box stays open, the kind survives the round trip, and a failed editor leaves your draft alone — and `esc` discards. Comments anchor to the line's **content**, not its number, so they follow the code as the agent edits around them; one that can no longer be located appears in a detached section at the foot of the stream rather than vanishing (each detached conversation is closed off like a placed one, so two of them read as two threads instead of one wall of text, and an orphaned reply sits under the parent it answers). **`C` comments on the whole file** the cursor is in — that it is in the wrong package, that it should not exist, that its whole approach is wrong. Its own key rather than a mode on `c`, because it is a different scope rather than a different way of saying the same thing, and because the thing reviewers reach for otherwise is picking whichever line is nearest the point and writing "this file", which buries a remark about the file inside a hunk. The remark hangs under the file's `══ path ══` divider, above the first hunk, where a reader looks for something said about the file rather than about a place in it — and it stays there when the file is folded, since the divider is the one row a folded file still has. The compose box heads itself `comment on all of a.go`, spelled out because `on a.go` and `on a.go:12` differ by a detail the eye skips and this is the moment the scope is being decided. A range under selection is dropped rather than refused: asking for the file's scope says what the remark covers. An **outdated GitHub thread has the same shape** — a path and no line, because GitHub reports no line for a remark whose line the change removed — and is deliberately *not* treated as file-level: it stays in the detached section labelled `outdated`, since presenting a settled conversation about vanished code as a standing comment on the file is a claim nobody made. A mirrored thread with no line that is *not* outdated is genuinely file-level on GitHub's side (`subject_type=file`) and does sit on the divider. Filing one is `awp review add --file <path>` with no `--line`, and it **publishes in the same staged review as everything else** — a `DraftPullRequestReviewThread` with the line left out, which GitHub records as `subject_type=file`. The preview names it `thread  all of internal/cli/deck.go` so it reads as covering the file rather than as a line comment that lost its number. Whether GraphQL would take a line-less thread was settled by **staging one PENDING against a real PR** rather than off the schema: both fields are nullable there, but nullable in the schema is not the same as accepted by the resolver, and GraphQL has no `subject_type` field at all. Had it refused, the fallback was `POST pulls/{n}/comments`, which creates a review entry of its own per call and would have undone what the single-mutation publish was built for. `side` is sent too, and is also accepted without a line, which is what lets a remark about a deleted file say which side it means.<br><br>The **review summary** — filed with no `--file` — gets its own section at the very top of the stream, headed `review summary` in teal, above the first file rather than after everything. Deliberately separate from the detached section: those are remarks whose anchor was *lost*, and filing an intentional summary under "anchor could not be found" would read as a failure. **The section is there before there is anything in it**, showing `nothing yet — c to say something about the whole change`, and **`c` on its header writes one**. That is the whole gesture: no new key, because `c` already means "comment on what the cursor is on", and the cursor being in the review section is what says *the whole change* — the same way the cursor being on a line says that line. A header with nothing under it would read as a rendering fault, so the empty section says what it is waiting for instead. It costs two rows at the top of every diff, which is what an always-reachable gesture is worth here: the alternative was a section that only appears once you already have a summary, i.e. a way to add to one but no way to start one. A viewer with no store behind it — a plain read of a diff — doesn't draw the invitation at all, since `c` there has nothing to save to. `c` on an existing review-level remark still **replies** to it; only the section's own header carries the new-remark meaning. They behave like any other conversation — `c` replies, `i` edits, `D` deletes, and they show in the comment index labelled `review` instead of a filename. They become the **review's body** on publish, since a GitHub review comment needs a line to attach to and a remark about the change as a whole has none. (`awp review publish` with no `--verdict` posts them as comments on the PR instead, after the inline ones, so a closing summary lands under the specifics it refers to.) They record what they posted the same way inline comments do, so a re-publish skips them instead of double-posting. A conversation renders as one block sharing a single left bar in the kind's hue, with a blank bar row between messages instead of a deeper indent per reply — stair-stepping a long exchange left a ragged left edge, and each message's author label already says where one ends and the next begins. The kind is named once, on the message that opened the thread. The hue lands on the bar and the header only; the prose itself stays a readable white, since a whole tinted paragraph is harder to read and says nothing a coloured edge doesn't. `c` on a comment **replies** to it (replying to a reply threads under the conversation's top, not under the reply), `i` **edits** your own comment in place — the box takes the comment's place in the stream for the duration, so you see one copy of the text you're changing rather than the saved version stacked above an editable one, and `esc` puts it back — and `D` deletes it along with every reply beneath it — an orphaned reply would otherwise be promoted to a conversation of its own, scattering the answers to a deleted remark through the diff as if each were an independent finding. Deleting a reply takes only that reply. Remote GitHub threads can't be edited or deleted from here, since they're GitHub's records — but **`c` on one replies to it**. Same key, because from the reader's side it's the same act: answer the thing under the cursor. What differs is where the answer goes, and the box says so — it heads itself `reply on github to a.go:12`, and its `enter` **posts** rather than filing a draft that waits for `P`. Answering a question is a message, not a review: a reply that needs a verdict attached, or that nobody sees until you publish, isn't a reply. (`c` on a reply of your own answers the same thread, not the message — one exchange, one thread, and here the exchange is the one on the PR. `tab` cycles no kind, since a reply's published body drops it.) The draft is **written to the store before it's sent**, so a post that fails leaves the words on disk rather than nowhere: it stays under the thread labelled `unsent`, and `P` lists it as a call it would make (`addPullRequestReviewThreadReply`) so it can be sent again. On success it's recorded against the id GitHub gave the new comment and appended to the mirrored conversation, so it reads as part of the thread immediately instead of after whatever refreshes the mirror next — and it's drawn **once**, not once as your record and once as GitHub's copy of it. Once it's up there, `i` and `D` refuse and say where the reply lives: editing the local copy would leave it disagreeing with what everyone else reads, and deleting it would look like a delete that did nothing while quietly losing your own record of having said it. **`A` approves an agent's proposal.** When a finding goes to the agent, the prompt tells it to reply before changing anything and then stop — and until now that gate had nowhere to be answered: the agent's reply was prose in a chat log, saying yes meant leaving the diff to find its tmux window and type it, and nothing recorded that it had happened. An agent files its offer with `awp review reply --proposal`, the conversation is chipped `awaiting approval` in the stream *and* on its row in the comment index, and `A` records the yes and sends the agent a go-ahead in one keystroke. It works from anywhere in the conversation — the finding, the proposal, any reply beneath it — because a proposal is always a reply and making you find which row of a two-message exchange the key belongs to is not a distinction worth teaching; and from the comment index, which is where a pending proposal announces itself. Approving also moves the finding it answers back to `sent`, the mirror of a reply reopening it: `open` means the exchange is waiting on you, `sent` means it's waiting on the agent, and the deck's finding count reads exactly that. **Both halves are reported** — `approved and sent to the agent`, or `approved (sending unavailable here)` — because the approval is on disk either way and a nudge that didn't go is your cue to go and poke it; a status line naming only the half that worked is how a send that never happened once went unnoticed. **Declining is a reply**: `A` is the only verdict key, because a bare no tells an agent nothing it can act on and the text of your reply is the reason. A proposal you haven't approved stays pending, which is the honest reading of it. The gate is about *changing code*, not about replying — an agent answering a question or explaining why the code is the way it is replies normally and carries on. It remains an honour system: nothing stops an agent editing without a yes, the prompt asks, and what's built here is a record of approval rather than an enforcement of it.<br><br>`r` marks the file **reviewed** and collapses it to its divider (keyed to content, so a later edit resurfaces it), leaving the cursor on the **next file's first changed line** rather than on a divider. Folding hides the file's *lines*, not its conversations: any comments on it move to sit under the divider and are unfolded back onto their lines when you un-review it — they are not relabelled as detached, since nothing about their anchor changed — `r` `r` `r` walks you through a change a file at a time with nothing in between. Un-reviewing expands the file and lands on its own first line. `T` cycles which of the PR's existing GitHub threads are shown (unresolved → all → none) — from **any pane**, since it changes what the view holds rather than what a list has selected, and the comment index is where the change is most visible; cycling to *none* empties that index, so the keyboard is handed back to the diff rather than left on a pane that is no longer drawn. `R` **settles the conversation under the cursor, whichever kind it is.** On a mirrored GitHub thread it resolves or reopens it on the PR. On one of *ours* — your finding and the agent's replies, which never leave the machine — it records that you are done with it: the root moves to `settled`, the row says so, and the deck's finding count stops counting it. That was the gap: the one kind of thread awp fully owns was the one you could not close from the keyboard. One key, because from the keyboard it is one gesture ("I have dealt with this") and you should not need to know which store a conversation lives in to close it; two words, because they are not the same act — GitHub records `resolved` about a thread, and `settled` is a position in one of our comments' lives. It is deliberately not called "resolved" for the same reason `addressed` is a different word again: `addressed` is *inferred* from the anchored code changing after a send, a claim about the code, where this is a claim about the reviewer. Settling acts on the conversation's **root** whichever of its messages the cursor sits on — the same property resolving a mirrored thread has — and only on the root: the count counts roots, so writing over a reply would erase what that reply's own state recorded and buy nothing. `R` again reopens it. The GitHub half works on the thread under the cursor in the diff, or **the one selected in the comment index**, since the list you scan conversations in is the list you decide they're settled from, and seeking into the diff and back for each one is not a gesture anyone repeats. The selection **holds its position** rather than following the thread: a resolved thread leaves the list under the default visibility, so staying put is what brings the next unresolved one under the cursor, and `R` `R` `R` walks the list the way `r` `r` `r` walks the files. Following it instead would scroll to wherever it went — or nowhere, since it is no longer listed. It acts on the *selection*, not on wherever the diff cursor happens to be; every path into that pane already seeks the two together, but resolving the wrong conversation is not a mistake you can see, so it doesn't rest on all of them remembering. On a local comment it says `only a GitHub thread can be resolved` — resolving is a state GitHub records, and a remark of your own has none. It **checks GitHub's answer** rather than the call's exit status. The mutation reports refusals in its response body, which `gh` doesn't reliably exit non-zero for, so a resolve GitHub declined used to come back as success; awp then wrote `resolved: true` into the mirror, and since resolved threads are hidden by default the conversation vanished while the mirror insisted GitHub had settled it. On a real PR that turned an open three-message thread into a comment that simply wasn't there. "GitHub accepted the call" and "the thread is now resolved" are different claims, and only the second is safe to mirror, so the returned `isResolved` is compared against what was asked for.<br><br>**The comment index says what it isn't showing** — `Comments (4) · 2 hidden · T` — because a list that merely lacks rows can't tell you a keystroke would bring them back. Hiding settled conversation is the right default; hiding it silently means any wrong flag or stale mirror reads as a missing comment. The notice earns the pane even with nothing to list, since a change whose conversation is *entirely* hidden is otherwise indistinguishable from one nobody has commented on — but it stays out of the `tab` rotation, since a notice has no rows to select.<br><br>A mirrored thread **folds to one line** — `▶ andrewcohen · github · resolved · outdated · 3 msgs · Suggestion: Drop the…` — and **`enter` opens or closes the one under the cursor**. **The author leads the header**, the way a local comment's reads `you · published`. It used to be the first thing in the *body* instead — the opening line read `alice: this leaks` under a header saying only `github` — which put the one thing you scan a conversation for in the one place you don't scan, and pushed the remark itself off the start of the line. **Every message gets its own header**, naming who wrote *that* message. The whole conversation used to be one card headed by whoever spoke first, with later authors marked inline at the start of their own remark — `andrewcohen: Keeping includesBotTraffic…` — which attributed the card to the wrong person, ran a multi-line reply's remaining lines on underneath the previous speaker, and delivered a reply you'd just posted crammed onto the end of somebody else's. Now a thread is adapted into a parent plus one comment per message, so the existing thread machinery does the rest: they stay together when placed, they fold into one row of the comment index as a `·n` count, and the block renders as one card with a bar-only row between messages. Every message carries its own `· github` chip, not just the opener: a card can run several screens, and "whose words are these, and where do they live" is asked at the message you're reading rather than at the one that happened to start the thread. Two conversations anchored to the same line get a **genuinely blank row** between them — no bar, no painted columns — because each card's own top/bottom padding carries the kind-coloured bar, so adjacent threads ran together as one block with a continuous left edge. Mirrored messages carry no `published` chip — that's our word for one of *our* comments having reached GitHub, and every message of a GitHub thread is there by definition, with the card's own `github` marker already saying so. Exactly one line, with no padding above or below: the blank bar rows that give an expanded conversation air at both ends would triple the height of the thing whose whole point is being short, so folded threads read as a compact list of markers, each sitting directly against the code it annotates. Resolved threads start folded and open ones start expanded: a settled conversation is reference material until you go looking at it, while an open one is why you're reading. That default is doing real work — on a PR with sixteen threads, showing them all used to add 529 rows of prose to a 697-row diff, with one unbroken run of 205 rows (four and a half screens of conversation with no code in it); folded, the same view adds 48 rows and the longest run is the single unresolved thread. A fold you set by hand outlasts the thread's state changing, so resolving a conversation you deliberately opened doesn't close it under you, and it lasts only as long as the view is open — how you left a fold is a reading position, not a property of the review. Your own local comments never fold; they're the working set. A thread carries whatever GitHub says about it — `github · resolved`, `github · outdated`, or **both**, since resolving a point is often what precedes the code moving out from under it. An **outdated** thread is one whose line the change itself removed: GitHub reports no line for it at all, so it sits in the detached section, and its index row says `outdated` in place of the generic `⚠` — GitHub's own word for the situation says more than the glyph. It is never shown against a line, because there is no longer a line it was written against; presenting one anyway made a settled remark read as a comment about whatever code happened to be there. Those threads come from the mirror the [pr-status pass](#pr-status-the-glyphs-leading-each-rows-meta-line) maintains, and the view re-reads it on the same tick it re-reads comments — so a reviewer's remark, or a thread someone resolved elsewhere, lands while you're reading rather than only on the next open. It's a local file read, so the tick never waits on the network. **`/` searches** — the diff's own content when the diff holds the keyboard, the file list when one of the lists does. `/` means search to anyone who has used vim or `less`, and it used to mean "filter the file list" everywhere including inside the diff, where nearly all the time goes; now it does what the pane you're in makes it mean. The search is **incremental** — each keystroke jumps to the first match of what you've typed so far, measured from where the search started rather than from the last match, so narrowing a query doesn't walk the cursor down the file. `enter` keeps the query (so `n`/`N` step matches afterwards, wrapping at both ends), `esc` abandons it and puts the cursor back where it was. The match is centred and the footer counts it (`/needle · 2 of 7`). Code lines only, and a wrapped line counts once: a conversation is reachable through the comment index, which beats stepping past it with `n`, and matching prose would make `n` walk through remarks while you're looking for an identifier. A folded file contributes no lines to search, so a fruitless search says how many files are folded rather than letting a hit inside one read as an absence. **`ctrl+s` outside the compose box sends every remark you have written and not sent yet**, and says how many went. Reviewing produces a handful of remarks before any one of them is worth interrupting an agent for, and until this the only way to hand one over was `ctrl+s` *while writing it* — so reading the change first and commenting as you went left you with no way to say "here is all of it". Same key as the box's, because it is the same verb: in the box "send this one", outside it "send what is waiting". They cannot collide, since the box has the keyboard whenever it is open. **One message for the set, not one per remark** — a prompt reaches an agent as a paste and a return, so five sends would be five turns started milliseconds apart, each reading files the others are rewriting; batched, the agent sees the review as what it is and picks an order. What counts as waiting is your own words, still yours to change, still awaiting triage (`review.Comment.Unsent`) — which pointedly excludes an agent's *own* findings, Open and awaiting **your** triage, since sending those back would answer a question with itself. Nothing waiting is an answer rather than an error, so the key says so instead of logging a failure.<br><br>**`P` publishes the review** to the PR — from inside the diff view (in the row list `P` is the scope cycle). **Two screens.** The first carries the **verdict and the review body together**, because choosing one and writing the other are a single thought: the verdict is one row you cycle with `tab` — **comment**, **request changes**, **approve**, GitHub's three, escalating in that order — and under it a textarea for the summary that `request changes` and `comment` require and an approval may want. **The box opens on the review summary the review already has** — they are one thing, so an empty box beside a summary sitting at the top of the diff would have invited a second one, and both would have gone up. It is prefilled with the text that will actually be sent, marker included, so an agent's 🤖 is visible before you publish its words under your account. The box keeps the keyboard the whole time, so `j`/`k` and the arrows type rather than moving a selection, and it is the same compose box the diff uses (`alt+enter` newline, `ctrl+g` out to `$EDITOR` — a summary is the longest thing anyone writes in a review). **`comment` is the default, not `approve`**: the default on an irreversible outward action should claim the least, and approving first meant a stray `enter` `enter` could put an approval on someone else's PR — the cost of not defaulting to it is two taps of `tab`. Leaving the box empty is a skip for a verdict that needs no body, so approving stays `tab` `tab` `enter` `enter`; choosing a verdict that needs a body with an empty box is refused **on that screen**, next to the box that fixes it, rather than one screen later by the plan or two later by GitHub. A review-level remark already on record counts as the body, so an empty box is not the same as having nothing to say. What you type is **not filed until the publish succeeds** — it used to be saved on the way out of the box, so backing out left the remark behind, and four abandoned attempts became four review-level comments on a real PR. The second screen shows **exactly what will be sent**, one line per API call — `POST pulls/54/comments  a.go:12-18  commit=1a2b3c4d5e6f  Suggestion: …`, `POST pulls/54/reviews  event=APPROVE` — and only a second, explicitly-labelled `enter` sends them; `esc` there goes back to the compose screen — where both the verdict and the summary are — since the usual reason to reject a plan is that one of them is wrong, and the text you wrote survives the round trip. Publishing is irreversible and outward-facing, so a menu choice is not the last thing between reading a diff and posting on someone's PR. The preview is the same code path as the real run (and as `--dry-run`), so it cannot describe a different run than the one it is previewing — which also makes it the diagnostic when a publish looks like it did nothing: an endpoint and a target either look right or they don't.<br><br>**`M` merges the PR**, the deck's `p m` reachable from the surface where the decision actually gets made — going back out to the row list to press it meant leaving the diff and finding the row you came from. Two screens again, one shorter: the **exact call** (`gh pr merge <n> --squash`, falling back to the merge queue if the repo requires it), then `y`, then what gh said. The report stays on screen rather than collapsing into the footer, because a refusal names the condition — not up to date with base, a required check still running — over several lines that one footer segment can't carry, and the same is true of a squash that landed in the queue instead. Unlike `p m` it does **not** refuse a PR that is already closed before offering: that check reads the deck's cached PR status, which the review surface doesn't have, so gh refuses it and the refusal is what you read. `M` says so and does nothing when the review has no PR.<br><br>Every anchor is **checked against the PR's own diff before anything is sent**, and each thread in the preview carries the verdict next to the target it's about — `⚠ line 47 is not in the diff`, `⚠ file is not in the PR's diff`, `⚠ nothing on the LEFT side of this file`. GitHub accepts a review comment only on a line that's part of the diff, and used to say so as a 422 *after* the attempt; now that the whole review is one atomic mutation, one bad anchor fails every comment with it and the error names only the first problem. So a run with a definitely-bad anchor **refuses and lists them all**, in your terms, before trying — which costs nothing, since GitHub would have refused the same batch. **The findings it refused survive the refusal**, each marked with the reason it was given: `awp review list` shows `refused: line 688 is not in the diff` in a column of its own, and `--json` carries the same under `rejected`. Until that existed, the refusal was a message on the way past and the only way to clear a blocked anchor was to delete the finding — which is what happened on a real review: the one GitHub wouldn't take was deleted to unblock the publish, its body went with it, and nobody noticed for two days. Repair the anchor and publish again and the mark clears itself; so does publishing the finding. A run that couldn't read the PR's diff at all retires nothing, since a refusal decided with the diff in hand outranks a run that had none. The source is `pulls/{n}/files`, because the question is what diff *GitHub* computed: a local read can disagree with it over a merge base that moved. The patch body is walked rather than just its hunk headers, since the two sides advance at different rates (a run of additions moves the new line number and not the old one) — and that's also what makes **context** lines commentable, which they are: the rule is "in the diff", not "changed". A range is checked at every line between its ends, not only at the ends, because GitHub requires the whole range to sit in one hunk and a range straddling two covers lines in no diff at all.<br><br>**A stale line is relocated rather than refused**, and the preview says so on the thread it happened to: `· relocated: 47 → 53`. The store keeps the number a finding was filed against, but an anchor is located by its **text** — the viewer already relocates one at render time, so you see it in the right place locally, and the publish disagreeing with that is a surprise nobody asked for. Meanwhile awp's own agent goes on editing the file while you read it, so a hint going stale is the normal case rather than a mistake. Two kinds of stale get caught. One is a line that has fallen **out of the diff** — the refusal above. The other is worse and used to be invisible: a line that's *still* in the diff but no longer says what the comment was about, which GitHub accepts without complaint and publishes silently onto whatever moved into that position. Both are answered the same way — the anchor's text is looked up in the same parsed patch, and if exactly **one** line carries it, that's where the comment goes. A range relocates both ends independently, since an edit inside a block moves its end and not its start. Text that matches **nothing, or more than one line**, is not guessed at: picking the nearest of three identical lines would be right often enough to be trusted and wrong silently. Such an anchor keeps its original verdict — refused if it was out of the diff, sent as filed if it wasn't, since refusing a comment GitHub accepts over a suspicion the check can't resolve would be a new way to lose a finished review. Matching ignores leading whitespace, so a line the agent reindented is still found; that can only ever turn one match into several, which is the refusing direction. A finding with no `--text` recorded is trusted as filed — there's nothing to check it against. The mark distinguishes the two: `·` is a detail worth reading, `⚠` is why the run will be refused. It refuses **only when confident**: a failed fetch, a file whose patch GitHub omitted (binary, too large), or an answer with no readable filename all mean *cannot tell*, and a check that can't tell must never block a publish that would have worked — GitHub arbitrates there, exactly as before. Afterwards the report stays on screen until dismissed, failures included, because a run that posted six of eight has to say which two. That is the decision a reviewer is actually making when they finish reading, and the moment you are most certain of it is the moment you just finished — so it happens here rather than after leaving the view to find a shell. Every screen is titled just `Publish review`; which one you are on is said by the keys underneath it, and the counts sit next to the calls they describe. It works from any pane since it acts on the review rather than on a pane's selection, and `esc` declines silently. The review-level remarks become the review's **summary**, joined under whatever you typed on the first screen. `request changes` and `comment` need a summary — GitHub's rule, and its own UI's — and an approval needs none, so approving a PR whose comments went up earlier is `P` `enter` `enter`. The submission runs off the update loop (it is one API call per comment) and the footer reports what landed, failures included; a second `P` while one is in flight is refused, since a comment is only marked published once GitHub has answered for it. It is the same code path as `awp review publish`, so a publish from here and one from a shell cannot drift. `ctrl+r` forces a refresh, `esc`/`q` closes. (`c` opens the view from the row list, but inside it `c` means comment — it does not close.) |
| `C` | The same review, **in a `review` tmux window** in the workspace's session, beside the agent — the arrangement that predates the in-deck viewer, kept because the deck is a popup and a popup is the wrong container for reading a change over half an hour: it closes the moment you switch away to look at something, and you cannot leave it open next to the agent working through your findings. `c` is for a look, `C` is for a sitting. It shows the **same scope `c` does** — the whole change against its stack base — so the two entry points can't disagree about what "review this change" means; the window runs a bare `awp diff`, which now resolves that scope for itself, so the range isn't named twice — and a range named twice is one that eventually disagrees with itself. It **summons** rather than relaunches — a window already running the viewer is focused as-is, and only a window that doesn't exist or has dropped back to a shell gets the command sent — so `C` `C` is idempotent and won't restart a review you were in the middle of. Same session/window plumbing as `a` / `e` / `v`, so it behaves like the rest of them — and `-` works in the window exactly as it does in the deck, since the chord belongs to the viewer rather than to either host. |
| `v` | VCS window (`jjui`) |
| `s` | Shell window |
| `i` | CI window (`gh run watch`) |
| `r` | Pick a PR to review |
| `x` | User actions menu (configurable via `actions` in config) |
| `n` | New workspace (inline form: workspace name / start-from / agent prompt). `start-from` is a select with `main` (default) and `pick a bookmark…` (opens the bookmark picker). The form also surfaces a `Will create bookmark:` hint when `deck.bookmark_prefix` is configured. |
| `o` | Open: fuzzy-pick a project from configured roots (tmux-sessionizer style) |
| `f` | Find: easymotion-style section → workspace jump. Stage 1 collapses the list to just section headers — both pinned register sections (see the `m` chord) and unpinned project headers — and hints each one, so a long list fits on one screen; picking one expands only that section (the rest stay as one-line headers for context) and scopes stage 2 to its rows. `backspace` re-collapses to the header list. (In the inbox scope there are no headers, so `f` hints every row directly.) |
| `/` | Filter rows · `esc` clears |
| `P` | Cycle scope: all → attention (mini-deck criteria: active agent or unread notification) → inbox (open-PR workspaces sectioned by next move — see below). **Remembered**: the scope you leave the deck in is the one it opens in next time, saved to `~/.awp/deck-prefs.json` as you press the key. An explicit `awp deck --scope=<scope>` overrides it for that run without overwriting it — a flag is an instruction about one launch, not a change to how your deck opens. |
| `g g` / `G` | Jump the cursor to the top (`gg` chord — press `g`, then `g`) / bottom (`G`) of the list, vim-style |
| `ctrl+u` / `ctrl+d` | Jump the cursor half a page up / down (vim-style), then scroll the list to follow |
| `ctrl+\` | Back into the pane you just left — the same key that leaves one, so the pair is a single gesture: out to check a row, back to carry on. The row is resolved when you press the key rather than remembered from when the pane opened, so a renamed workspace is followed and a deleted one is a refusal instead of a program started in a directory that is gone; the cursor moves to that row too, so leaving again lands where the pane was. A row the current scope or filter isn't showing is still reachable — an agent that exited drops out of `attention`, and the key is about where you have been, not about which list you are looking at. Not bound under `awp deck`, which hosts no panes: there `ctrl+\` belongs to tmux. |
| `\|` then a window key | **Split**: the workspace's agent on the left, the key's window on the right — `\|c` diff, `\|v` vcs, `\|e` editor, `\|s` shell, `\|i` ci, `\|W` watch. `\|a` is refused (the left half is already the agent), and anything else cancels the chord. Focus starts on the right, because the right is what you asked to look at. Refused below 120 columns, naming the width it wants: two halves of a narrow terminal are two panes you cannot read. From inside a pane the same gesture is `ctrl+\` (out to the deck) then `\|c`, which re-attaches the agent's session rather than starting a second one. See **A split, and the keys inside one** below. |
| `L` | Switch to the **previous** pane — the one before the pane you were last in. `tmux switch-client -l` one substrate over, and the point is the same: the two most recent things you were in are one keypress apart, so pressing it twice puts you back. Resuming (`ctrl+\`) does not disturb the alternate, so holding one pane open all afternoon doesn't lose the other. One pane deep it says so and names `ctrl+\` instead. Under `awp deck` it is still `tmux switch-client -l`. |
| `R` | Rename workspace (inline form: edit name, `enter` to rename, `esc` to cancel). Updates jj workspace, tmux session + window, and state — the on-disk directory keeps its original path. Not allowed on `default`. |
| `B` | Link a jj bookmark to the selected workspace (drives the per-row PR glyph) |
| `d` | Open the selected workspace's auto-discovered dev URL in your default browser |
| `p o` | Open the selected workspace's PR in your default browser (chord — press `p`, then `o`). `esc` cancels the chord. |
| `p m` | Merge the selected workspace's PR. Opens a confirmation modal showing the PR number, title, and the exact command (`gh pr merge <n> --squash`); `y`/`enter` confirms, `n`/`esc` cancels. The merge runs immediately and the progress modal stays open until gh reports success or failure — gh's own output (including why a merge was rejected, e.g. failing checks or pending review) streams into the log. Dismissing the modal refreshes PR status so the row glyph updates. Squash is used because `gh pr merge` has no non-interactive "repo default" mode. On branches that require a **merge queue**, `gh pr merge` is broken when the repo's queue is configured without `allow_auto_merge` ([cli/cli#13398](https://github.com/cli/cli/issues/13398) — gh only ever calls `enablePullRequestAutoMerge`, which that setting gates, and never `enqueuePullRequest`). awp detects the merge-queue / auto-merge-blocked signature in gh's output and works around the bug by calling the `enqueuePullRequest` GraphQL mutation directly (`gh api graphql`), so the PR is added to the queue and the log reports its queue position/state. |
| `p d` | Read the selected workspace's PR description **in the deck** — a scrollable popover with the PR's number, title, author and state pinned above the body. `↑`/`↓`/`j`/`k` scroll, `pgup`/`pgdn` page, `ctrl+u`/`ctrl+d` half-page, and `esc` / `q` / `d` close it. The fetch runs in the background, so the box opens immediately and fills a moment later; a PR with no description says so rather than showing an empty pane, and a failed fetch shows gh's reason. The body is the markdown as written, wrapped — not rendered. |
| `p D` | Open the same description in a **`pr description` tmux window** of the workspace's session (the way `r` opens a `review` window), running `gh pr view <n> \| less -R` with TTY formatting forced. `q` in less drops back to a shell in the window; re-running `p D` reuses the window. This is the one to reach for when you want the description open *beside* something else rather than glanced at — `p d` is the glance. |
| `p r` | Repair the selected workspace's PR. Detects actionable conditions (merge conflicts, failing CI, branch behind base, changes requested by a reviewer, a pending request for **your** review on someone else's PR — by name or via one of your teams) and opens the `A` send-prompt form prepopulated with a fix prompt, so you can review and edit it before sending to the workspace's agent. Reports "nothing to repair" if the PR is healthy. **Your own PR with review feedback:** when the repair covers review comments / changes requested on your PR, the prepopulated prompt is approval-gated — it asks the agent to read and understand each point and report the problem plus a proposed solution for your approval *first*, then (once you approve) address the points, push, reply to the threads, and re-request review if needed. If other actionable issues (failing CI, conflicts, behind base) share that prompt, the whole prompt is gated the same way so it reads consistently. Repairs that don't involve review feedback keep the immediate "fix it and push" prompt. **An approved PR still offers it**: approving and still wanting something aren't exclusive, and answering `p r` with "nothing to repair" there was the deck deciding on your behalf that a reviewer's remarks were settled. The prompt says the PR is approved and asks the agent to report which points are still open at the current head — the signal behind it is "a `COMMENTED` review exists", which never says whether it was addressed, so on an approved PR some or all of it may be done. This is the one place the repair prompt and the 󰭹 glyph deliberately disagree. **Someone else's PR:** the prompt is review-mode — investigate and report in chat, never modify files, mutate the branch, or push — and it lists **only what a reviewer can act on**. An out-of-date base branch is the author's rebase, and another reviewer's comments on someone else's PR aren't yours to answer, so neither appears; conflicts and failing CI do, since both are worth a sentence in a review. When those are the *only* conditions, `p r` says "nothing to repair" rather than opening a prompt with the author's chores in it. A **re-request** for your review still asks you to check whether each point was addressed — there the earlier feedback is your own. **Re-reviewing someone else's PR:** nothing extra happens on submit, even when the deck flags the row **stale** (the `· stale` chip — the author pushed since you opened the review). Findings are anchored to line content rather than to a head SHA, so the ones you already filed follow the code across the author's push instead of being stranded against an old diff; re-open `c` to read the current state. |
| `p s` | Set (or clear, via blank/0) the PR # for the selected workspace. Pins the workspace to a specific PR so the deck resolves status by number rather than guessing from the bookmark. Persisted to `~/.awp/...` workspace state. |
| `D` | Delete workspace · on a `default` row, deletes the **project**: removes every other workspace under that repo and drops the project from the deck (the default workspace itself is left intact). Requires typing the project name to confirm. Also ends the workspace's sessions on **both** substrates — its tmux session and every `awp.<project>.<workspace>.*` zmx session — because an agent that outlives its working copy keeps running in a directory that is no longer there, and comes back on the next refresh as an "unmanaged" row. Sessions that don't exist are a no-op, so this costs a tmux-only user nothing. |
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

### Syntax-highlighted diff bodies

Every diff surface — `c` in the deck and standalone `awp diff` — syntax-colours
the code, with the change type carried by a background tint. On by default;
`AWP_DIFF_SYNTAX` is the escape hatch rather than the switch.

| Value | Treatment |
|-------|-----------|
| unset (default) | Every code line is coloured, unchanged lines included. |
| `changed` | Only added and removed lines are coloured; unchanged code stays muted the way an unhighlighted diff has it. |
| `off` / `0` / `false` / `none` | No highlighting — the change type is the body's foreground colour, as it was before. |

Anything unrecognised gets the default, so a typo lands on the ordinary
rendering. Turning it off has to be spelled correctly, which is the useful way
round: a misspelled `off` that silently kept highlighting on would be a puzzle,
where a misspelled `changed` is just the default.

Colours are **Catppuccin** — Latte against a light terminal, Macchiato against a
dark one — in Catppuccin's own conventional syntax assignment, so a diff matches
an editor wearing the theme rather than approximating it:

| Class | Colour |
|-------|--------|
| keyword, builtin, constant | mauve |
| type, class, tag, YAML key, markdown heading | yellow |
| function name | blue |
| attribute, property, variable, decorator | teal |
| string and other non-numeric literals | green |
| number | peach |
| comment | overlay1 |
| operator | sky |
| punctuation | overlay2 |

A bare identifier keeps your terminal's default foreground, which under this
theme is already Catppuccin's Text.

This is the one place awp does **not** use the ANSI 16 slots the rest of its UI
routes through. Those exist so awp's own chrome — status dots, headers, the
selection bar — follows whatever theme your terminal is set to. Code in a diff
isn't chrome, it's content, and sixteen slots can't carry a syntax palette
anyway: six are already spoken for by status roles, so a token would either share
its hue with "CI failing" or get none at all.

The mapping is semantic, not per-language, because every lexer spells the same
idea differently: Go says `NameFunction` where TypeScript says `NameOther`, and
YAML says `NameTag` for a key where JSON says `LiteralStringDouble`. So a class
is chosen by what the token *means* — a name that describes a shape (type, class,
tag, YAML key, markdown heading) is one colour, a name that gets called is
another, a named slot (attribute, property, variable, decorator) a third. Getting
this wrong is silent, since "no colour" is a legitimate answer and a grey diff
looks like a plain one, so `internal/highlight/coverage_test.go` asserts a
representative line per language actually comes back coloured.

Added and removed lines get a **background tint** across the whole row — line
numbers and `+`/`-` gutter included — because highlighting spends the foreground
on the lexer and the change type has to live somewhere; without it a `+` line and
a `-` line differ only in the gutter glyph. The row under the cursor uses a
brighter variant of the same tint rather than the ordinary cursorline, so the
change is still readable on the line you are on; letting the cursorline win
outright makes the tint blink off and on down the file as you scroll. The tint
appears only when highlighting is on — unpainted, the change type is already the
colour of every character on the line.

Two limitations worth knowing. Each line is lexed on its own, because a hunk
interleaves the old and new sides and joining them would be lexing text that was
never source — so a line in the middle of a block comment or a multi-line string
is coloured as if it were code. And a file whose extension no lexer claims
renders exactly as it does today rather than being guessed at.

### More or less code around a hunk (`+` / `_`)

A hunk on its own says what changed, not what it does. `+` widens the unchanged
code that comes with each hunk and `_` narrows it, on both diff surfaces — `c` in
the deck and standalone `awp diff`.

The rungs are **0 → 3 → 6 → 12 → 24 → 48** lines. A ladder rather than a fixed
step, because the useful sizes are not evenly spaced: three lines is "which line
is it", a dozen is "what is this function doing", fifty is effectively the whole
file — and stepping by three would put seven presses between the first two
answers. Zero is on it, which is a real way to re-read a diff you have read once
and want only the changes from. The view opens on 3, which is `jj diff`'s own
default, so what you see before pressing anything is the diff jj would have
printed. The ends of the ladder refuse out loud rather than doing nothing.

`+` and `_`, not `+` and `-`: `-` is already the scope menu, and a diff has two
widening axes — which revisions it covers, and how much of each file comes with
them. These are the shifted halves of the two keys next to each other, which is
as close to the obvious pair as the taken key leaves.

Widening re-asks jj with a bigger `--context` rather than splicing file content
into the parsed hunks. jj already knows how to widen a hunk, merge two that grow
into each other, and stop at the start and end of a file, and it answers for the
revision being read — where the working copy on disk is a different question for
any scope that does not end at `@`. Your cursor stays on the line it was on
across the reload, and the header names the width whenever it is not the default,
since a widened diff looks like a different change once you have forgotten you
widened it.

## CLI reference (highlights)

| Command | Purpose |
|---|---|
| `awp deck [--scope=all\|attention\|inbox]` | Open the workspace dashboard. `--scope` sets the initial filter for this run, overriding the remembered scope (`~/.awp/deck-prefs.json`, default `all` when nothing has been remembered yet) without replacing it; `P` still cycles through every scope inside the deck, and records where you leave it. `pr` and the legacy `open-pr` are accepted as aliases for `inbox`. |
| `awp mini-deck` | Quick-jump list of workspaces with an active agent or unread notification |
| `awp zdeck` | **Proof of concept.** The same deck with a different backend: `a`, `e`, `v` and `s` render the process as a live pane inside the deck rather than opening a tmux window. See *zdeck* below. Requires [zmx](https://github.com/neurosnap/zmx) on PATH. |
| `awp w open [name]` | Create or attach to a workspace. Run with no name to drop into the same unified form the deck's `n` key shows: workspace name, `Start from` (`main` by default, or `pick a bookmark…`), and an optional agent prompt. To review a PR instead, use `awp review`. |
| `awp w list` | List workspaces in the current repo |
| `awp w info <name>` | Show details for a workspace |
| `awp w rename <old> <new>` | Rename |
| `awp w delete <name>` | Delete (use `--force` to skip prompts) |
| `awp w prune [--dry-run] [--force]` | Remove orphan workspace dirs under `~/.awp/workspaces` not tracked in state |
| `awp w bootstrap [name]` | Re-run bootstrap hooks for a workspace |
| `awp w bootstrap --all` | Re-run bootstrap hooks for every tracked workspace in the current repo (continues on failure) |
| `awp review add` | File a review finding from a script or agent: `awp review add --file <path> --line <n> [--end-line <n>] [--side new\|old] [--type comment\|suggestion\|question\|praise] [--text <line>] [--end-text <line>] (--body <text> \| --body-file <path>)`. **`--body-file` is the one to use for anything with markdown in it** (`-` reads stdin), and `awp review reply` takes it too. A finding is markdown and markdown is full of backticks; putting one through a shell argument means escaping it for a quoting context the caller has to guess, and guessing wrong is silent. That is not hypothetical — seven agent findings reached a real PR reading ``Pin the \`graphql_client\` git dep``, backslashes and all, because a backslash-backtick inside single quotes is two literal characters rather than an escaped backtick. A file has no quoting, so nothing can be mis-escaped into it. As a backstop, a `--body` in which *every* backtick is escaped and none is plain is un-escaped on the way in: no author escapes uniformly, so that pattern is a quoting accident rather than intent — while a body that mixes escaped and plain backticks is saying something deliberate and is stored exactly as written. A body from a file is never touched, since it went through no shell. `--end-line` files a finding about a **block** — the same thing `v` selects in the diff — with `--end-text` anchoring its last line the way `--text` anchors the first; an end equal to `--line` is simply one line, and an end *before* it is rejected rather than quietly dropped — the difference between "line 12" and "lines 12-18" is the whole content of the flag. **Three scopes, spelled by what you pass**: `--file` and `--line` is a comment on a line, `--file` alone is a comment on the **file as a whole** (which the diff shows on the file's divider, and GitHub publishes as `subject_type=file`), and neither is the **review summary**, which the diff shows in its own section above the first file. A `--line` without a `--file` is refused: there is nothing for the number to be a line of, either flag could be the mistake, and guessing which one files the remark somewhere nobody asked for. `--end-line` needs `--line` for the same reason — a range of nothing is not a range. `--type` says what the finding is asking for — it drives the colour the comment renders in, so a triager can tell a proposed change from a question without reading every body; an unrecognised value falls back to `comment` rather than dropping the finding. **Four types**: `suggestion` (blue-red — proposes a change), `question` (yellow — wants an answer), `comment` (blue — observation, the default, and what claims the least), and **`praise` (green — says something is good and asks for nothing)**. Praise is the only type that does **not** count towards the review's open-findings badge: a change with nine compliments and one bug owes its author one thing, and a badge reading 10 would turn every compliment into a complaint. It is also the only one the palette gives a hue that does not mean "deal with me", which is the distinction it exists to draw. The review is resolved from the workspace you are in — there is no session path to discover — and **the command says which review it wrote to**: `added suggestion c7 to review work-pr-54-coworker (workspace pr-54-coworker) on x.go:12`. That line is the whole guard against the one way this goes silently wrong. An agent run from the *source repo* rather than from the workspace resolves to that repo's own review, which is not the review the reviewer has open; both sides report success and the findings are nowhere. Seven findings on a real PR were lost exactly that way, and nothing detected it because the command never named its destination. **`--workspace <name>`** targets another workspace's review without moving there, on `add`, `reply`, `list`, and `publish` alike — running an agent from the source repo is a normal thing to do, and until now the only way to reach a workspace's review was to be standing in it. A name matching no workspace is an error listing the ones that exist, rather than a new empty review under the misspelling, since that would be the same silent loss the flag exists to prevent. A directory in no workspace at all says so where the name would be. As a second line of defence, an anchor naming a file **the review's own diff does not touch** prints `warning: cmd/other/main.go is not in this review's diff (main..@) — check --workspace, or the path`. Naming the destination only helps a reader who reads it, and an agent filing a dozen findings in a row is not reading twelve confirmation lines — so the odd one out says so itself. It is a **warning, never a refusal**, and it prints *after* the finding is on disk: the words are the valuable part and an anchor can be repaired, so a slow or broken check must never cost you the finding. It checks **file membership only** — the line-level check needs the whole patch parsed and runs once per publish, whereas this runs on every `add` — and it stays **silent when it cannot tell**, which includes an empty diff (a badly resolved range would otherwise report every finding as misfiled, and a warning on every call is one nobody reads). Findings appear inline in the deck's diff view (`c`). Comments are anchored to the line's **content**, not its number, so they follow the code as an agent edits around them; one that can no longer be located is shown in a detached section rather than dropped. |
| `awp review reply` | Reply on an existing finding's thread: `awp review reply --to <comment-id> [--type comment\|suggestion\|question\|praise] [--proposal] --body <text>`. The reply threads under its parent and flips that parent back to needing your attention, so an exchange stays one item rather than becoming two. The comment id is included in the prompt the agent receives. Names the review it wrote to, and takes `--workspace <name>`, the same way `add` does.<br><br>**`--proposal` marks the reply as a change the agent intends to make, and stops it there.** The prompt an agent gets with a finding tells it to reply before changing anything and then wait for approval — and until now approval had no home: the agent's answer was prose in a chat log, saying yes meant leaving the diff to find the agent's tmux window and type it, and nothing recorded that it had happened. A proposal is that exchange written down. The offer is a reply, the approval is a fact in the store, and `A` in the diff view is how you give it. The gate is about *changing code*, not about replying: an agent answering a question, or explaining why the code is the way it is, replies normally and carries on. Only "here is what I would do" needs a yes. A proposal says so on the way out — `proposed to <id> (<id>) in <review> — awaiting approval` — because the two replies mean different things to the caller that just ran the command. |
| `awp review list` | List the current workspace's findings — id, kind, state, **proposal state**, **refusal**, **thread state**, location, and a one-line body (`--json` for machine output). The body column is a preview — the first line with anything on it, cut to 72 columns, with a trailing `…` whenever something was left out. It used to join every line with ` / `, which read fine while findings were one sentence and became a ribbon wrapping the terminal several times over as soon as multi-paragraph proposals arrived. `--json` is unaffected and carries the whole body. The proposal column is `-` on everything that isn't one, `pending` on an offer awaiting a yes, and `approved` once it has one; it's on every row rather than only the proposals so the fields line up and a reader can index them, and it's a column of its own rather than folded into `state` — one column holding two vocabularies is how you end up matching `approved` against the wrong field. This listing is the whole answer to "was I approved": there's no dedicated query command, because the approval sends the agent a prompt of its own and this is where it confirms. The **refusal column** works the same way — `-` on everything a publish hasn't objected to, and `refused: <reason>` where one has, carrying the reason rather than only the fact. A bare `refused` marker would say a reason exists somewhere else, and somewhere else is `awp.log`, which is the arrangement that let a real refusal go unnoticed for two days.<br><br>**The listing joins awp's mirror of the PR's conversations**, so a published finding reports what GitHub knows about it rather than what the last publish knew. Without the join it read the local record and nothing else: on a real review four resolved-and-replied threads all listed as `published`, indistinguishable from the one still genuinely open. The **thread column** is `-` where the mirror has nothing, and otherwise `open` / `resolved`, plus `outdated` (GitHub's separate fact — the code moved out from under the thread; a conversation is usually both, since settling a point is what precedes the code changing) and a reply count. The **location column** shows drift: `spec.md:346-350 → 438-442`. The stored number is what the finding was filed against and is frozen on purpose — an anchor is located by its text — but GitHub recomputes a thread's line as the PR moves, and printing only ours is what had the listing name 346-350 for a thread sitting at 438-442. Two sets of numbers relative to two commits, so the header says which is which once: `anchored to 6fe2f75dabc1; thread lines are GitHub's, at the PR head`.<br><br>`--json` is where the **replies** go, and they're the reason the join is worth making: what the author already answered is what stops a re-reviewing agent raising a closed point a second time, and reading it used to mean shelling out to `gh api`. Each row gains a `thread` object — id, `resolved`, `outdated`, line, and the messages in the conversation other than the finding itself. Additive: every key that was at the top level still is, and it's still a bare array. The pairing is the same rule the diff viewer reconciles with (`review.MirrorOf`, matched on GitHub's comment node id — body and line both drift), because a second rule would be a second answer to "are these one conversation" and the two would disagree on exactly the rows that matter. **Resolving a thread from the CLI is deliberately not here**: that's a reviewer's judgement and a poor fit for an agent; if it ever lands it goes behind the same approval gate `--proposal` uses. **Names the review first**, and does so even when it is empty: "no findings" from the wrong review is the reading that sends you looking for a bug in the store. `--workspace <name>` lists another workspace's, which makes this the cheapest way to check where an agent is about to file. `--json` stays a bare array — it's the machine channel, so a caller parsing it doesn't have to cope with a header line. |
| `awp review publish` | Post the review's unpublished findings to its PR — anchored ones as inline comments, **review-level ones as comments on the PR itself** (a GitHub review comment needs a line to attach to, so a remark about the change as a whole has nowhere inline to go). The PR-level ones go up after the inline ones, so a closing summary lands under the specifics it refers to. A `suggestion` or `question` body is prefixed with its kind, capitalised (`Suggestion: …`) — it opens the comment, and a lowercase word starting a sentence reads as a typo rather than a label — GitHub has no notion of awp's palette, so the kind has to be spelled out there — while a plain `comment` gets no prefix at all, since it is the default and labelling it would label every remark that had nothing special to say. Anything a robot filed carries 🤖, **in front of the kind** (`🤖 Suggestion: …`) — an agent's comment posts under your account with nothing else to distinguish it, and who wrote a remark frames everything after it, including what the remark is asking for. Replies omit the kind, since the thread's first comment already carries it. It **names the review and the PR before sending anything** (`publishing review work-pr-54-coworker (workspace pr-54-coworker) to PR #54`): a publish that resolved the wrong review reports "0 comments" and reads like a store bug, so saying which one it read makes that a line of output instead of an investigation. The PR defaults to the one the workspace is pinned to — the number `awp review <n>` recorded when it created the workspace, or the one you set with `p #` — so you don't retype it; `--pr <n>` overrides, and `--dry-run` shows what would go up. **`--workspace <name>`** publishes another workspace's review, and the pinned PR and the commit the comments were read against then both come from *that* workspace rather than from the directory you happen to be in. **`--verdict approve\|comment\|request-changes` submits the comments as a review**, which is the decision a reviewer is actually making when they finish. **`--summary <text>` / `--summary-file <path>`** writes the review's body: without it, `--verdict comment` dead-ended on its own requirement, telling you to go and file a review-level remark without saying how. With a verdict the review-level remarks become the review's **summary** — what GitHub's review body is for — instead of separate comments on the PR; without one they keep going up as PR comments. `comment` and `request-changes` need a summary, the same rule GitHub's own UI applies (a verdict that asks for something has to say what), and that is checked **before anything is posted** — a run that published eight comments and then refused the verdict would leave you working out what landed. An approval needs no summary and can be submitted on its own, so approving a PR whose comments went up on an earlier run is one command. Inline comments are anchored to **the commit you read**, not to whatever GitHub says the head is now: a comment carries line numbers, and line numbers only mean anything against the commit they were read from, so a newer head would attach the remark to a diff nobody looked at. The order is what the review recorded the last time this resolved, then a commit the caller already knows (the deck reads every workspace's bookmark commit anyway, to spot one that has fallen behind its PR), then `@-` **in the workspace under review** — not in the source repo it belongs to, since a jj workspace has its own working copy and the repo root's answer describes a different change entirely; then the PR's current head as a last resort, so a review with no workspace can still publish. The working-copy commit itself (`@`) is never used: it has never been pushed, so GitHub would refuse it. Whatever comes out is checked against **the PR's own commit list** before anything is sent, because GitHub rejects a commit that isn't part of the pull request and there is no shortage of local commits that look plausible and aren't. A candidate that isn't on the PR falls back to its head and **says so** — the comments may land on lines that have moved, which is worth a sentence rather than a silent substitution. The resolved commit is then recorded on the review, so a retry anchors to the same one instead of re-deriving it from a workspace that has moved on. GitHub marks a comment against an older commit as outdated, which is the honest outcome. Idempotent: each comment records the thread it created the moment it succeeds, so a run that fails halfway can be retried without double-posting, and comments already on GitHub are skipped.

**Every comment goes up in one review.** The REST endpoint for a review comment creates a *single-comment review* per call, so eight comments appeared on the PR as eight review entries with empty bodies plus a ninth for the verdict — and GitHub does not allow deleting a submitted review, so that is permanent. Instead awp makes two GraphQL calls: `addPullRequestReview` with every thread and **no event**, which leaves the review **pending** (staged, visible to nobody but you), then `submitPullRequestReview` with the verdict and body. That inverts the reason the per-comment path existed: posting one at a time was meant to make partial failure recoverable, but a GraphQL mutation is atomic — a line that isn't in the diff fails the whole thing and creates nothing — and a pending review *is* the staging area. If the submit fails, the staged comments are invisible and get discarded (`deletePullRequestReview`) so a retry starts clean; nothing is ever half-published where someone else can see it. Because comments ride in a review and a review has to be submitted with a verdict, **`--verdict` is required when there are comments to post** — leaving the review pending instead would strand them where only you can see them, and a retry would stage a second copy of every one.

**A local reply is not published.** Replies are the conversation you and the agent have about a finding; a batched review creates new threads only, so sending one would post it as a fresh top-level comment divorced from what it answers. They're counted as skipped.<br><br>**A reply into a GitHub thread is, and by its own call.** `addPullRequestReviewThreadReply`, one per reply, listed in the plan like everything else — and **no verdict required**, since answering a question isn't submitting a review. It's sorted out of the inline bucket before the anchor rules are applied, because a reply carries the thread's own path and line: left inline it would have gone up as a second top-level thread on that line, right next to the question it was answering. Normally there's nothing here to send — the viewer posts a reply as you write it — so what publish finds is one whose post failed at the time. |
| `awp review [pr#]` | Pick or open a PR for review in a fresh workspace. Opens a `pr description` window and an `agent` window — no separate review window, because the deck's own diff view is the review surface (`c`, with `-` inside it to change scope). The agent is primed with a precise commit-SHA diff range and files its findings with `awp review add`, which resolves the review from the workspace it's running in; there is no session path to discover. The PR's existing comments (inline review comments, review summaries, and conversation comments) are fetched and embedded in the prompt so the agent doesn't re-raise points already made — it's told to stay non-redundant but may agree or disagree with them. The full review instructions (the lengthy reviewing guide plus PR context) are written to `~/.awp/review-prompts/<repo>/<workspace>.md`; the agent receives only a short pointer prompt that names the PR and tells it to read that file, so the terminal isn't flooded with the whole guide (falls back to the inline prompt if the file can't be written). The file lives outside the workspace tree on purpose — a review workspace's own `.awp/` is symlinked to the shared source-repo `.awp/` during prep, so a prompt written there would be shared across every review and clobbered by the next one. Keying by repo + workspace name keeps each review's prompt private (even when workspace names collide across repos), and deleting or pruning the workspace removes the matching prompt file. The fetched PR is also written through to `~/.awp/pr-status-cache.json` and pinned to the new workspace as `PRNumber`, so `p o` / row glyphs resolve the instant `awp review` returns — no waiting for the next periodic fetch. Agent makes no file edits, commits, or GitHub comments. **Re-reviewing a force-pushed PR:** nothing to migrate. A review's identity is the workspace, not `(repo, PR, head SHA)`, and its findings anchor to line content — so a force-push or rebase relocates them the same way an agent's own edits do, and a re-run reads the same review it did before. |
| `awp watch [name]` | Read-only live view of an agent's progress on the current task, built from its Claude Code transcript. Shows the **units of work** (from the agent's task list / todos, falling back to a markdown checklist or `Unit N:` prose) coupled with the current unit's position in the project's **dev loop** (`explore → implement → verify → commit`), plus per-unit gate pass/fail and a churn/stall signal. With no name, it resolves the workspace from the session's `AWP_WORKSPACE` env when set (so it "just works" inside a workspace session), otherwise shows a picker. Observe-only — it never runs gates or steers the agent. Flags: `--once` (print one frame and exit), `--transcript <path>` (replay a specific transcript), `--suggest` (print a prompt to configure `dev_loop`), `--preamble` (print the loop instruction to give an agent, generated from `dev_loop`). |
| `awp diff [-r <revset>]` | awp's **review surface**, standalone — the same viewer the deck's `c` opens, with the same keys: comment (`c`), reply, edit, `v` ranges, mirrored GitHub threads, `r` reviewed marks, `ctrl+s` send to the agent — the remark you are writing from inside the compose box, everything written and not yet sent from outside it — `P` publish. The review it reads is resolved from the working directory, the same lookup `awp review add` uses, so a finding you file here and one an agent files from the same directory land in the same place. In a directory that is not a tracked workspace it still opens as a review surface rather than refusing to comment. **With no arguments it resolves itself**, the same way `c` does: the whole change against its stack base, the review keyed to the workspace you're standing in, and the PR that workspace is pinned to — and **`-` switches scope here too**, off the same three-entry list `c` gets. Nothing needs passing, and no key means one thing in the deck and nothing in a window: `awp diff` in a workspace and `c` on its deck row are one surface with two doors, not two tools. The chord itself lives in the viewer (`internal/ui/scope.go`) rather than in either host, because two copies is exactly how the deck ended up with a `-` that standalone didn't have. It defaulted to the *working copy*, which meant that in any workspace whose work was committed — every PR workspace — `awp diff` opened on an **empty diff**, and the only cure was knowing to pass `-r`. **Two roots, and neither substitutes for the other.** The review store is keyed by `SourceRepoRoot`, not `jj root`: inside a secondary workspace `jj root` answers with the *workspace* path, and reading the wrong root there opened a different review than the deck did — so an agent's findings were simply absent, reported as nothing at all (same trap as the `default`-entry corruption above). The *viewer* is rooted at `jj root`, because `jj diff --git` prints paths relative to the workspace it ran in: that is what a file's path is joined onto, and what `e` hands `$EDITOR` as its directory. Rooting the viewer at the source repo meant that in any secondary workspace — every PR workspace — `e` opened the source repo's copy of the file: same relative path, different working copy, so an edit landed somewhere the review was not and nothing said so. Since the viewer's root is now a workspace directory, the header takes the project name as a given (`ui.Subject.Project`) rather than deriving it from that path, or it would read `pr-2336-dev` twice and never name the repo. The header names what it's a review of — `awp review · alpha · pr-2336-dev · alpha#2336 · vs main` — since standalone there's no deck footer to say it and "which PR is this" shouldn't need leaving the view to answer.<br><br>`-r` remains as an override — one range, named explicitly, so there is nothing for `-` to offer and the chord is inert — and takes any jj revset — `awp diff -r @-` for the change before this one, `awp diff -r 'main..@'` for the whole stack against main, `awp diff -r andrew/thing` for a bookmark. The revset is re-resolved on every refresh tick rather than pinned to a commit id, so `-r @-` keeps meaning "the change before this one" as the stack moves under it, and the footer names the revset you typed rather than a resolved hash. `-r` is hand-parsed rather than read by a flag library, because a revset routinely starts with a character a flag parser would claim for itself (`-r @-`, `-r -3`). |
| `awp doctor [--global] [--fix]` | Health checks; `--fix` repairs missing hooks/env |
| `awp init hooks` | Install/update global Claude + pi integrations (idempotent) |
| `awp logs [-n 50] [--path]` | Print the tail of awp's diagnostic log, `~/.awp/awp.log`, with its path on the first line. **This is where a failure goes after the status line that reported it is gone.** Almost everything awp does happens in a TUI, and a TUI's error channel is one row that can't be copied, can't be scrolled back to, and is replaced by the next keystroke — so when GitHub refuses a reply, its own sentence explaining why survives here rather than only on screen for as long as it takes to read. Always on (a log you have to enable isn't there for the failure you've *already had*), never fatal (a diagnostic that can break the thing it's diagnosing is worse than none), and rotated at 2 MiB keeping one `.1` generation so it can't quietly eat a disk. Entries are one line each with newlines folded out, since the messages most worth keeping — `gh`'s stderr, GraphQL error payloads — arrive multi-line and would otherwise make the log ungreppable. `--path` prints just the path, for `tail -f $(awp logs --path)` while reproducing something. Test binaries never write to it, so a suite can't fill the file you read when something real breaks. |
| `awp config init` | Bootstrap `<repo>/.awp/config.json` (must run from repo root) |
| `awp config edit [--global]` | Open the project (or `--global`) config in `$EDITOR` |
| `awp internal report-status --state <…> [--prompt <text>\|--prompt-stdin] [--waiting-when-tool <list>]` | Hidden — used by hooks to write status. `--prompt` stores the active prompt text on the workspace; `--prompt-stdin` reads it from a Claude-style hook JSON payload on stdin. `--waiting-when-tool` takes a comma-separated list of tool names; when a `PreToolUse` payload's `tool_name` matches, the recorded state is overridden to `waiting` so blocking tools (e.g. `AskUserQuestion`) badge the row instead of staying in `working`. |
| `awp internal gate record --result <pass\|fail> [--json]` | Hidden — the `PostToolUse(Bash)` / `PostToolUseFailure(Bash)` enforcement hook. Records the run command's gate pass/fail (verdict from which event fired) into the workspace snapshot and emits a transition nudge. `--json` prints the recorded result for debugging. See [`dev_loop` → Enforcement](#dev_loop). |
| `awp internal gate check [--hook] [--workspace <ws>]` | Hidden — the `PreToolUse(TaskUpdate)` enforcement hook (`--hook`): resets a unit's gates on `in_progress`, blocks `completed` (exit 2 + reason on stderr) until the unit's gates are green, and seals a green completion so the next unit starts fresh. Without `--hook`, a self-check the agent can run: exit 0 when ready, else non-zero + reason. See [`dev_loop` → Enforcement](#dev_loop). |
| `awp internal require-task --hook` | Hidden — the `PreToolUse(Edit\|Write\|NotebookEdit)` task-discipline hook. Blocks editing a non-markdown file (exit 2 + reason on stderr) unless a task is `in_progress` in the session's task list (`~/.claude/tasks/<session>/`). Markdown is exempt, and so is any path outside the session's tree (from the payload's `cwd`) — state under `~/.awp` is not the code a task is about. Fails open on any error. Like the gate hooks, self-gates on a configured `dev_loop` — no-ops in repos that haven't opted in. |
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

### `agent_options`

Flags inserted between the agent command and the prompt, e.g.
`"--permission-mode auto --model opus"`. Passed through verbatim, so quoting is
yours to get right.

**Continuity across a lost session.** A workspace's agent normally survives
because its session does, but a machine restart or a kill takes it with them,
and the next `a` starts a blank conversation. Adding `--continue` (Claude Code)
makes the agent resume the workspace's previous conversation instead:

```json
{ "agent": "claude", "agent_options": "--continue" }
```

Safe on a brand-new workspace — in a directory with no prior conversation
`--continue` simply starts one. The one surprise: conversation history is keyed
to the directory and outlives the workspace, so deleting a workspace and
recreating it under the same name resumes the old conversation rather than
starting clean.

Note that any conversation resumed this way gets a **new session id**, and
Claude Code's task list is stored per session id — so a resumed agent starts
with an empty task list even though its conversation is intact.

### `actions`

Custom commands surfaced by the deck's `x` action menu. By default each action runs in a new tmux window in the workspace.

Set `"background": true` to run the action detached via the jobs subsystem instead. The deck dispatches it without opening a tmux window; output is captured to `~/.awp/jobs/<id>.log` and the run shows up in the right panel's **Recent activity** list for that workspace. Failures appear in the bottom status bar's `⚠` count and stay until dismissed in the `J` overlay. Best for installs, lints, builds, or anything you'd rather not babysit.

Set `"focus": false` to keep the action foregrounded (it gets a real tmux window, runs interactively, scrollback intact) but **don't** switch the tmux client to it on launch. Useful for spawning a long-running watcher you'll check on later without losing your place in the deck. Ignored when `background` is true.

The action menu lists actions alphabetically, so an alias stays where you learned it.

Under `awp zdeck` a foreground action gets a **pane** instead of a tmux window, and a long-lived zmx session behind it — see [zdeck](#zdeck-proof-of-concept) for what that changes, including the fact that `focus` has no meaning there.

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
- **`awp internal loop track`** — a matcher-less `PostToolUse` / `PostToolUseFailure` hook (fires for every tool). It derives the current loop **phase** from the tool that just ran (edits → `implement`, reads → `explore`, a gate command → that gate's phase, etc. — the same mapping `awp watch` uses) and writes it into the workspace snapshot, resetting it when a `TaskUpdate` goes `in_progress`. Edits are scoped the same way the `require-task` gate above is: one to a file **outside the session's tree** doesn't move the phase, because it isn't work on the current unit — repairing a review record under `~/.awp` while verifying used to pull the unit back to `implement`, which read as the code having been rewritten. This keeps the deck's cached phase current on the fast first paint instead of lagging to the next transcript scan. It writes only when the phase actually changes, so a per-tool-call hook doesn't churn the state file.

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

This badge and [the deck's top row](#the-top-row) count the same buckets and are
decided in one place, so they can't disagree about the same workspaces.

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

## zdeck (proof of concept)

`awp zdeck` is **the same deck with a different backend**. Same list, same
keys, same everything — the only difference is that `a`, `e`, `v` and `s`
render the process as a live pane *inside* the deck, on a pty awp owns,
instead of handing off to a tmux window.

`awp deck` is unchanged and remains the one to use for work.

**Each deck reads its own substrate.** A row's status — is there a session,
did the agent exit, which workspace are you looking at — comes from wherever
that deck runs its processes: tmux for `awp deck`, zmx for zdeck. They are
deliberately not merged. A workspace whose agent is in a tmux session started
earlier by `awp deck` reads under zdeck as having no session at all, which is
the honest answer: zdeck cannot show it to you and `a` will not take you to
it. The flip side is that the same workspace can look idle in one deck and
busy in the other; the two are different views of different terminals, not
two views of one truth. `z` lists zmx's sessions directly when you want to
see what is actually there.

The point it tests is that awp can own the pty, and therefore the layout,
without negotiating with a multiplexer for it. Attaching to a tmux session
hands you a *client* — a status bar, a current window, its own key routing —
because that is the only pane-shaped thing tmux offers. [zmx](https://github.com/neurosnap/zmx)
has no windows and no status bar, so a client is simply the program.

`c` needs no backend: the deck already shows the diff in place. `enter`
brings the workspace's agent into the deck rather than switching tmux
clients — there is no other client to switch to, and outside tmux that
handoff silently does nothing.

Because it hosts its panes, zdeck is the one deck that can be the outermost
program: it does not require running inside tmux.

**Window kinds it does not handle are refused, not handed to tmux.** `C` (review
window) and `p D` (PR description) reached code that opens with *"no tmux
session for this workspace? make one"* — which starts a tmux server from nothing
and launches the coding agent in it. On a deck that hosts the agent there is
never a tmux session, so that fired every time: a second agent, invisible to the
deck, with the same `AWP_WORKSPACE`, reporting status and recording gates, while
`switch-client` no-opped so nothing appeared to happen. Refusing loses very
little, because both windows have in-deck equivalents already and the error names
them: `c` reviews the change in the deck, `p d` reads the PR description in the
deck.

| Key | Pane | Behind it |
|---|---|---|
| `a` | agent | **zmx session.** Survives closing the pane, and awp. |
| `e` | editor | **zmx session.** Keeps its buffers between glances. |
| `x` | a user action | **zmx session.** Survives closing the pane, and awp. |
| `s` | shell | **Spawned by awp.** Dies with the pane. |
| `v` | vcs (jjui) | **Spawned by awp.** Dies with the pane. |
| `i` | ci | **Spawned by awp.** Dies with the pane. |
| `W` | watch | **Spawned by awp.** Dies with the pane. |

Whether the process outlives the view is the only thing the two groups differ
by; the difference in code is which command awp runs. Everything downstream —
the emulator, the keys, the rendering — is identical.

**A user action gets a pane of its own.** `x` picks one from the `actions`
config, and under zdeck it opens as a pane running `sh -c <command>` in the
workspace — the same shell the tmux window types it at, so one config field means
the same thing on both decks. Its session is `awp.<project>.<workspace>.action_<name>`,
long-lived, which is what the case this exists for needs: a dev server you start
once and leave up while you work in the agent's pane. It survives closing the
pane and closing the deck, `z` lists it and reattaches, and deleting the
workspace reaps it along with the rest.

Because the command is the session's own process rather than a line typed at a
shell, *listed* and *running* stay different questions — a live session is a
running server, and `z` marks it `✗` once it exits. The trade is that `focus`
means nothing under zdeck: zmx cannot create a session without attaching to it,
so `x` always opens the pane. `esc` returns to the deck and leaves the command
running. An action marked `"background": true` is unaffected — it is not a window
at all, it runs as a detached job and appears in the activity bar and `J`.

`i` and `W` are the clearest ephemerals of the set: each runs one foreground
program you watch until it says something, and a stale one is worse than none.
`i` runs the same run-resolution script the tmux `ci` window runs, so both
watch the same GitHub run. `W` runs the awp binary the deck itself is running,
so a build in a temp path opens that build's watch view rather than an older
install's.

`ctrl+\` leaves a pane, on one press, whether one pane is up or a split of two. It
has to be a key nothing inside one wants, because every other key belongs to the
program: `esc`, `q` and `ctrl+c` all mean something to an agent. **`ctrl+|` — the
same key shifted — is the menu**: split what is on screen, move the keyboard
between halves, resize, zoom, close a half. Two gestures one key apart rather than
one key meaning two things depending on how many panes are up. See [a split, and
the keys inside one](#a-split-and-the-keys-inside-one) for the verbs.

Any window kind zdeck does not handle — the review
window, the PR-description window — falls through to tmux exactly as before.

Emulated panes all answer that key, because the deck reads it before forwarding
anything to the program. Under `AWP_PANE_EXEC=1` the deck is suspended and not
reading keys at all, so the key reaches the child, and what happens is the
child's business:

| pane | what ctrl+\ does under `AWP_PANE_EXEC=1` |
|------|------------------------------------------|
| agent, editor, user actions | Detaches. These run under `zmx attach`, and zmx's own detach key is also `ctrl+\` — the session keeps running, exactly as when the pane closes |
| `W` watch | Leaves. It is awp's own program, so it binds the key itself |
| the diff viewer (`awp diff`, the review window) | Leaves, same reason — see **the diff viewer** below |
| `i` CI | Exits, by signal: a cooked-mode `bash -c`, so the line discipline turns the key into SIGQUIT. The deck reads that as leaving rather than as a failure, so no error lands in the status bar |
| `v` vcs | Nothing — jjui is in raw mode with nobody in front of it. Use jjui's own `q` |
| shell | Nothing: interactive shells ignore SIGQUIT. Use `exit` / `ctrl+d` |

### A split, and the keys inside one

`|` then a window key puts two things on screen at once: the workspace's agent on
the left, and whatever the second key named on the right. `|c` is the one to
reach for — the agent's own account of what it did, beside the diff of what it
actually changed, with no keystroke between them. `|v` is jjui beside the agent,
`|s` a shell, and so on down the window keys.

`|c` opens the diff with its **left column already collapsed** — the file tree
and comment index would spend a third of half a terminal on navigation, and the
diff is what you opened the half to read. `\` brings them back for the file you
need to jump around in, and the cursors they hold were never lost.

A split wears **the same top row a single pane and the row list do** — see [the
top row](#the-top-row), which is where it is described: the badge, the name of the
half the keys are in, and how to leave, spanning the terminal above both halves. It is the deck's row rather than either half's,
because it answers for the screen; the halves render no header of their own.
While the `ctrl+\` prefix is armed, that same row becomes the verb menu.

It does **not** list both halves. That was the first cut, with the focused one
wearing the usual `┃` selection bar — but the halves are the same workspace, so
naming both spent the row's best columns on the one thing that cannot distinguish
them, and the border already says which has the keys. Resizing the divider used
to print `split: 84 / 42 columns` there too, and no longer says anything: you
pressed the key, and the divider moved.

Both halves are live. The one without the keyboard keeps painting: an agent's
output, a `watch` view, a diff refreshing itself on its own tick. Only the keys
are somewhere specific, and the half that has them is the one with the teal
border — the other drops to grey, the same tier the diff viewer's unfocused panes
drop to.

**`ctrl+|` is the menu**, in a single pane and in a split alike: a prefix, because
the programs on screen keep their own complete keymaps and there is no room to
spend a second key on each verb. It is the shifted leave key, so the pair you reach
for from inside a pane is one key apart. `ctrl+\` is not part of it — that is the
door, and it leaves on one press from either arrangement.

| after `ctrl+\|` | does |
|---|---|
| a window key — `c` diff, `v` vcs, `e` editor, `s` shell, `i` ci, `W` watch | **split**: that kind beside the pane you are in. Already split, there is nowhere to put a third, so it **replaces the focused half** |
| `h` / `l` / `tab` | move the keyboard to the left half / the right half / the other one |
| `<` / `>` | move the divider left / right by 5% of the width; `=` puts it back in the middle |
| `o` | zoom the focused half to the whole screen, and again to go back — both halves stay open, so nothing is re-opened |
| `x` | close the focused half; the other becomes an ordinary whole-screen pane |
| `ctrl+\|` | nothing — it re-arms, so holding the key down cannot do anything |
| anything else | cancels, and is swallowed rather than typed at the program |

A single pane's menu carries the window keys and nothing else: focus, size, zoom
and close-a-half have nothing to act on until there are two halves, so they are
absent rather than listed and inert.

Splitting from a pane keeps that pane as the left half rather than opening a fresh
one beside its replacement. The agent you were watching is the reason you wanted
something next to it, and re-opening it would repaint the program you were reading
mid-thought. The pane needs no resize of its own either — a pane asks its terminal
for whatever box it is handed, so the next frame is what moves the pty to half the
width.

The menu has no timeout, for the same reason it has no ambiguity: it is a state
resolved by the next key, not by a clock.

**`ctrl+|` needs the Kitty keyboard protocol.** A plain terminal sends `0x1c` for
`ctrl+shift+\` exactly as it does for `ctrl+\`, so there is nothing to tell apart —
reading one as the menu there would swallow the key that leaves. Where the flag is
not granted there is simply no menu: the top row stops offering one, `ctrl+\` still
leaves, and `|` from the row list is still the way to open a split. Terminals that
do grant it disagree about how they spell it — some resolve the shift and send `|`
with ctrl, others send `\` with ctrl and shift — and both are read as the same
keypress.

**A held `ctrl+\` does nothing.** The key spells two decisions — leave a pane, and
from the row list go back into one — so a repeat read as a press opened what the
next repeat closed for as long as the key was down. The deck asks its terminal for
the Kitty keyboard protocol's event-types flag, which is what makes a repeat
reportable as something other than a press, and drops it in both places. A terminal
that does not grant the flag never reports a repeat at all, which is exactly the
behaviour awp had before asking; the flap comes back there, and there is nothing to
be done about it from this side. A held `ctrl+|` is harmless on every terminal,
since re-arming an armed menu is idempotent.

The divider's position is a fraction of the width rather than a column count, so
resizing the terminal keeps it where you put it instead of leaving it wherever it
happened to fall in the old width. `<` and `>` move it 5% at a time — a fraction
so one tap feels the same on a 120-column terminal and a 400-column one — and stop
at the point where a half would be narrower than a pane's minimum, rather than
collapsing a half you would then have to re-open. Both halves resize themselves
from there: a pty is told its new size through the same path a window resize uses.
The status line reports the two widths as it moves.

The divider can also be **dragged**: press on it — its two border columns, plus a
column of slack either side, since a two-column target is one you miss — and it
follows the pointer until you let go. A press there is consumed by the divider
rather than reaching a half, so grabbing it does not move the keyboard, and the
motion in between belongs to the divider even when your hand runs ahead of where
it can go.

This is the one place awp asks the terminal for mouse reporting on its own
behalf. Everywhere else it asks only when a hosted pane's own program has enabled
it, because asking on behalf of a program that never wanted the mouse costs the
terminal's own drag-to-select for nothing. A split does want it, so while one is
up that selection is gone — `ctrl+\ < >` is there for when selecting text matters
more than dragging the divider.

**`ctrl+\` leaves the diff viewer too**, in both hosts: standalone `awp diff` quits on it the way it quits on `q`, and the deck's `c` modal closes on it the way it closes on `esc`. The key means "give the keyboard back to whatever put me here" everywhere else in awp, and the review surface is the one you spend the longest in — it had no reason to be the exception. It matters most in a handed-over pane, where the deck is suspended and reading nothing, so a program that does not bind the key itself cannot be left at all. The spelling lives in `internal/charm` for that reason: `internal/ui` cannot import `internal/deckui`, and a second copy of the string is how the hint a pane prints stops matching the key that works.

The two that don't answer are third-party programs reading the real terminal,
which is what handover means. Giving them the key needs something in front of
them that reads the terminal first — either the emulator, or a `zmx` session of
their own.

A pane costs two columns and two rows of border, plus the deck's own top row
above it — which is the deck's rather than the pane's, and is described in [the
top row](#the-top-row). There is no padding and no header of its own — unlike the deck's other overlays, which frame a fixed amount of awp's own text,
a pane is showing someone else's full-screen program, and every cell of chrome
is one that program does not get.

Long-lived sessions are named `awp.<project>.<workspace>.<kind>` and show up
in `zmx ls`, so they can be inspected and killed from outside awp. Requires
zmx on PATH; zdeck refuses to start without it rather than failing on the
first pane.

**A session name is bounded, so a long one is shortened.** zmx turns a name into
a socket path, and a unix socket address holds 104 bytes — with the daemon's
socket directory under a macOS per-user TMPDIR that leaves **46** for the name.
`awp.<project>.<workspace>.<kind>` passes that for ordinary input: a workspace
named after a PR's head branch spends 24 bytes on its own, so
`awp.alpha.pr-2336-dev-mlwzqyrmxslo.action_dev` is 47 and the pane could not
open at all. When a name would not fit, the workspace part is cut short and a
four-character fingerprint of the full name is appended —
`awp.alpha.pr-2336-dev-mlwzqy-5118.action_dev`. The fingerprint is what keeps
two workspaces named after the same PR from addressing one session; the kind is
never touched, because it is what reopens the pane and what finds a user action's
command. **A name that already fits is never rewritten**, so nothing running is
renamed.

Since a shortened name no longer contains the workspace's name, awp also writes
the identity as zmx **labels** — `awp_project`, `awp_workspace`, `awp_kind` —
which `zmx ls` prints inline. The deck matches a session to a row by generating
the name that row would have rather than by reading the name it got, so row
state is right whether or not a name was shortened; the labels are what still
identify a session whose workspace has since been renamed or deleted.

**`z` lists what is running.** A hosted session outlives the deck that opened
it, so the set of live agents is real state — and until now the only way to
see it was `zmx ls`, which prints dotted names, a unix timestamp and a full
argv on one line. `z` shows the same sessions as deck rows: the workspace they
belong to, their kind, how long they have been up, and a glyph for their state
(teal `○` running, green `●` running with a client attached, red `✗` exited —
zmx keeps a session listed after its command exits, so *listed* and *running*
are different questions). `/` filters, and `enter` attaches the selected
session in a pane, which is the thing the raw command cannot do.

`x` ends the selected session. It asks first — `end proj/ws agent? the agent's
context is lost [y/N]`, or `it has already exited` when there is nothing to lose
— and the question names one row, so anything other than `y` is a no, including
the keys that would otherwise move the cursor. The list stays up rather than
being replaced by a popover, because ending sessions is something you do to
several in a row. Deleting a workspace already reaps its own sessions; this is
for the ones no delete will ever reach — a session whose workspace is gone, or
an agent you just want stopped.

A session whose workspace has since been deleted is marked `no workspace` and
will not attach: a pane is opened for a workspace row, and there is nowhere to
put one without it. It can still be ended with `x`, which is the only thing left
to do with it. `z` is bound only under zdeck — `awp deck` hosts no sessions of
its own, so there the key stays free.

**One agent per workspace.** Sending a prompt goes to the zmx agent — the same
process `a` shows. In `awp deck` a prompt is typed into the workspace's tmux
session, and doing that under zdeck would give a workspace two agents: the one
on screen and an invisible tmux one receiving everything you send it. zdeck
will not *start* an agent that isn't running from the send-prompt key; it says
`no agent running for <workspace> — press a to start one`. (A create with a
prompt does start one — see below — but `A` is for talking to an agent that is
already there, and starting one to receive a message you could have opened the
pane to type would be a surprise.)

That is not just `A`'s rule. Every surface that talks to an agent — `A`, the diff
viewer's `ctrl+s`, the review flow's opening brief — resolves the destination
through **one** function (`agentPromptSender` in `internal/cli/prompt_sender.go`),
because picking the wrong substrate does not fail: tmux is asked for a session,
does not find one, makes it, and starts a coding agent in it. The review store
used to wire tmux in directly, so under zdeck every remark a reviewer sent from
`c` went to an agent created for the occasion, with the same `AWP_WORKSPACE`,
reporting status and recording gates — while the agent on screen never heard it.
Standalone `awp diff` has no host to ask and can perfectly well be running inside
a zdeck pane, so it looks for a live zmx agent session by name rather than
assuming tmux; tmux is the answer only when nothing on zmx claims the workspace,
which is where it stays the whole answer for `awp deck`.

**Two keys get you back into a pane, and they are not the same question.**
`ctrl+\` **resumes** — back into the pane you just left, which is the common case
and belongs on the key that already means "hop between the pane and the deck".
`L` **alternates** — the *previous* pane, the one before that, which is what
`tmux switch-client -l` is for: press it twice and you are back where you started.
A single slot can only ever offer the pane you just had, so one key could not do
both.

The deck remembers the workspace and kind of the two most recent panes, resolving
the row at press time — see the key table above. Re-entering the pane you are
already alternating from does not push it down, so `ctrl+\` never erases what `L`
exists to reach. Before any pane has been opened both keys say so rather than
opening the selected row's shell.

**Creating a workspace with a prompt starts its agent.** The create runs as a
detached subprocess, so there is no terminal to hand a hosted agent — but it does
not need one: the agent's session is not where you are looking. awp allocates a
pty, attaches on it, waits for the session to exist and throws the client away,
which leaves the agent running as the session's own process with nobody watching.
So `n` with a prompt means the work is under way before you open anything, the
same as under tmux, and `zmx ls` shows the session immediately.

The prompt arrives as the agent's own argument rather than as a paste, so there
is no waiting for it to boot and nothing races its input box. A create with **no**
prompt starts nothing — there is nothing for an agent to do yet, and an idle one
per workspace would spend a process and a row that reads as running.

If the start fails — no zmx, no daemon, a working copy that is not there yet —
the prompt is **parked** on the workspace instead and the job log says why. A
parked prompt is delivered by the first agent pane you open, which is what always
happened before anything started an agent here, so nothing is lost either way.

**`r` works the same way**, and more so: a review workspace exists to be reviewed
now. The review does the whole setup — fetches the PR, prepares the
`pr-<n>-<branch>` workspace, pins it, mirrors the existing review threads, writes
the review brief — and then starts the reviewer with it. The reviewer launches
*without* the dev-loop preamble, whether it starts here or from a parked brief: a
reviewer told to work in units, run gates and commit starts doing the author's job
on someone else's PR. No `pr description` tmux window is opened either — `p d`
renders it in the deck.

A detached start allocates a 120x40 pty, because a session takes its size from
the single client looking at it and this one exists for about a tenth of a second.
That is the shape the agent's first output is laid out at, until the first real
pane resizes it; there is no size that avoids a reflow, and a common terminal
shape makes it the smallest.

**shift+enter reaches the program.** A legacy terminal cannot say it — enter is
CR and CR carries no modifiers, so shift+enter, ctrl+enter and enter are the
same three bytes — which is why agents that bind it to "newline, don't submit"
first ask the terminal for an encoding that can express it. A pane reads those
requests out of its program's own output (`CSI > <flags> u` for the Kitty
keyboard protocol, `CSI > 4 ; <n> m` for xterm's modifyOtherKeys; Claude Code
sends both and pops them on exit) and answers in whichever it got, preferring
Kitty. A program that asked for neither gets a plain CR, which is what a real
terminal would send — a pane does not invent an escape sequence nobody is
listening for.

**A pane tells the truth about its colours.** A program that asks its terminal
what colour its foreground, background or cursor is (`OSC 10` / `11` / `12`) is
asking the pane, and the pane used to answer out of the emulator's defaults —
white on black, whatever was actually on your screen. So a program picking a dim
grey by blending toward the background was blending toward a background nobody
was looking at. The deck now asks its own terminal at startup and hands the
answers to every pane it opens; a pane opened before the terminal has replied
falls back to the emulator's defaults rather than guessing.

`TERM` is a different question and stays `xterm-256color`. It is stated rather
than inherited because a hosted program is talking to awp's emulator, not to
whatever awp is running under — inheriting `tmux-256color` describes tmux — and
`xterm-256color` is the closest true statement about that emulator. Claiming
`xterm-ghostty` would advertise capabilities it does not have.

The mouse and the cursor follow the pane's program rather than the pane. A
program that enables mouse reporting — an agent, jjui — gets the wheel
forwarded to it; one that doesn't, like a shell, leaves the mouse to your
terminal so its own drag-to-select keeps working. Likewise a program that
hides its cursor doesn't get one drawn.

**The cursor's shape follows the program too**, which is how an editor tells you
which mode it is in: nvim's insert-mode bar and replace-mode underline, and back
to a block on escape. Blink comes with it, because `DECSCUSR` encodes the two in
one parameter — 5 is a blinking bar and 6 a steady one — so reading the shape
without the blink is reading half of what the program said. A program that asks
for nothing gets the block, which is the terminal default rather than a fallback.

Only the libghostty-vt build reports it. libghostty keeps the shape on a render
state rather than on the terminal, so a pane holds one and refreshes it when the
pty has delivered something — a shape cannot change without bytes arriving, so
the frames where nothing happened cost nothing. On the x/vt build the cursor
stays a block; that emulator is being retired rather than taught.

### Recording what a pane and the deck actually wrote

Rendering questions in a pane come down to which bytes flowed, and two
environment variables answer that. Both are off unless set, both append, and
both write `0600` — a capture holds whatever an agent typed, including anything
pasted into it.

| Variable | Records |
|----------|---------|
| `AWP_PANE_LOG=<path>` | Every byte between the deck and each hosted process, both directions (`out` = process to us, `in` = us to the process). |
| `AWP_FRAME_LOG=<path>` | Every byte the deck writes to its own terminal (`tty`). |

Point them at the same path to read a pane's output and the frame it produced in
order — that pairing is the whole reason they share a format:

```sh
AWP_PANE_LOG=/tmp/awp.log AWP_FRAME_LOG=/tmp/awp.log awp zdeck
```

Escape sequences are written quoted (`"\x1b[2mdim"`), so the file is safe to
`cat` — a log of raw escapes would reprogram whichever terminal read it.

Two more diagnostics, both off unless set:

| Variable | Records |
|----------|---------|
| `AWP_TRACE=1` | One line per frame into `/tmp/awp-deck.log`: what awp's own render cost, and the gap since the previous frame. Also where best-effort failures the deck deliberately doesn't surface get written. |

The trace log also **says when the frame rate is absurd**: over 60 frames a second
it prints `frame rate N/s over budget` once a second. Nothing the deck draws
changes faster than the spinner (10/s), so a rate in the hundreds means something
is emitting messages in a loop — and that defect is otherwise invisible, since an
idle deck rendering 430 frames a second looks exactly like an idle deck. Finding
the first one took a CPU profile: `spinner.Tick` returns its message *immediately*
(only the spinner's `Update` schedules a delayed one), and the idle branch of the
tick handler returned it directly to keep the loop alive, so message and command
chased each other as fast as the renderer could go — 40% of the process, on a deck
doing nothing. The renderer itself is capped at 30 fps (`tea.WithFPS`), which
bounds the terminal writes but not how often `View` is called; Bubble Tea renders
after every message, so a message loop still costs a full render even when the
screen is not repainted. The cap is the backstop; the source is the fix.
| `AWP_PPROF=<path>` | A CPU profile of the whole deck session, written to that path on exit. Read it with `go tool pprof -top -nodecount=30 <path>`. |

`AWP_PPROF` is **a path, not a switch** — and it is refused if you give it one, because every spelling of "on" is also a valid filename. `AWP_PPROF=true awp deck` wrote a 26 KB profile to a file called `true` in whatever directory you launched from, which is how one ended up committed to this repo. An off-ish value (`0`, `false`, `no`, `off`) is honoured as "no profile"; an on-ish one (`1`, `true`, `yes`, `on`) prints what it wanted instead and opens the deck without profiling.

### Which emulator interprets a pane

A pane's output is interpreted by a terminal emulator inside awp, and `AWP_PANE_VT` picks which one:

| Value | Emulator |
|-------|----------|
| unset, or `x-vt` | `github.com/charmbracelet/x/vt` — the default, pure Go, what every ordinary build has. |
| `ghostty` | libghostty-vt, Ghostty's own VT library. Only present in a binary built with `-tags ghosttyvt`. |

It exists to compare the two on the same session with the same program, which is
the only way to tell a fidelity defect in the emulator from one in the layout
around it. Note it is **not** `AWP_PANE_EXEC`, which answers a different question
— that one hands the real terminal to the child and runs no emulator at all.

Asking for an emulator this binary does not have is an error naming the build
tag, not a quiet fall back to the default: the point of choosing is to compare,
and a comparison that silently ran the default twice would report that the two
agree.

The top row **no longer names the emulator**. It did while this was an open
question — a comparison you cannot confirm you are inside is not a comparison —
and the answer is settled, so the columns went to the hosted workspace's own
state instead. The byte log (`AWP_PANE_LOG`) still says which one ran.

The ghostty build needs an archive built from Ghostty's source, and `make` does
the whole thing:

```sh
make ghostty     # fetch libghostty-vt, build it, install awp with -tags ghosttyvt
make install     # back to the ordinary build
```

Both install the same way this repo always has (`go install`), so they are one
command apart in both directions — the ghostty build is the experiment and the
default is what it is being compared against, so returning must not be a research
task. The archive is cached under `~/.cache/awp/libghostty-vt`; `make
clean-ghostty` re-fetches it.

Under the hood it is Zig (pinned to 0.16.0, which `build.zig.zon` requires)
followed by a tagged `go install`:

```sh
zig build -Demit-lib-vt=true -Demit-xcframework=false -Dsimd=false \
  -Doptimize=ReleaseFast --prefix $DIR

CGO_CFLAGS=-I$DIR/include CGO_LDFLAGS=$DIR/lib/libghostty-vt.a \
  go install -tags ghosttyvt ./...
```

`-Demit-xcframework=false` is required on macOS or the install step fails in
`xcodebuild` after the library itself has already built — which reads as a failed
build when it is not one. `-Dsimd=false` keeps the C++ SIMD dependency out, which
is why the archive links against nothing but libc. Zig is invoked through `mise
exec zig@0.16.0` rather than listed in `mise.toml`, so nobody who will never make
this build has a toolchain fetched on their behalf. Nothing about the default
build changes: `go build ./...` stays cgo-free.
