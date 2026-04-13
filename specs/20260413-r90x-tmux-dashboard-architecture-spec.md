# Agent deck tmux dashboard architecture spec

## Metadata
- **Spec ID**: `20260413-r90x`
- **Feature name**: `awp agent deck tmux dashboard architecture`
- **Owner**: AI coding agent
- **Status**: In Progress
- **Last updated**: 2026-04-14

## Goal
Define a concrete, simple-first architecture for an `awp` dashboard that runs inside tmux, gives one-pane visibility into multiple agents/workspaces, and safely orchestrates tmux windows, panes, and popups without fighting tmux’s actual constraints.

## User Problem
Andrew wants a future `awp` dashboard for multi-agent work that lives comfortably inside tmux. The key design challenge is that tmux panes cannot literally contain other tmux windows, so the dashboard cannot be a magical nested tiling environment. The design needs to make navigation and orchestration feel coherent while staying honest about tmux’s model and keeping the first version easy to build and reliable.

## Scope
### In scope (v1)
- A dashboard-first architecture where one awp TUI pane controls other tmux targets.
- Clear guidance for when awp should create/use tmux windows vs panes vs popups.
- A robust state model keyed by stable tmux ids where possible.
- UX flows for focus switching, summons, logs/tests/shell helpers, return-to-dashboard, and background attention.
- Failure-mode handling for stale/deleted tmux targets and missing tmux context.
- A Go implementation outline covering package boundaries, abstractions, persistence, and tests.
- Explicit recommendation on tmux orchestration vs native-awp rendering.

### Out of scope (v1)
- A fully implemented multi-agent dashboard.
- Nesting tmux windows inside the dashboard pane.
- Cross-host orchestration.
- Rich live terminal mirroring of multiple active agent panes inside the dashboard.
- Replacing tmux as the primary terminal multiplexer in the first release.

## UX
### CLI
- The existing diff viewer command should be named `awp diff`.
- A future top-level dashboard command could be `awp deck` or `awp agents`; this spec focuses on architecture, not final naming.
- Primary invocation is via a tmux key binding (e.g. `prefix + a`) that runs `display-popup -E awp deck`, opening the dashboard as a floating overlay above the current session.
- The overlay is ephemeral: actions that target a workspace (open workspace, start prompt, review PR, jump to agent) dismiss the popup and `switch-client` to the target tmux session.
- Running `awp deck` inside tmux outside the popup binding still works and opens the same overlay UI in the current pane as a fallback.
- Running it outside tmux should fail clearly by default, with a future opt-in mode to spawn/attach a tmux session if desired.

### TUI
- The dashboard lives in a tmux popup overlay, not a persistent window or pane.
- The layout should be a two-column deck:
  - **Left column:** global awp project/workspace roster
  - **Right column:** details and actions for the selected workspace/agent
- The **left column is the primary navigation surface** and should show all known awp projects/workspaces, not just raw tmux targets.
- Each left-column row/card should include:
  - project name
  - workspace name/path
  - assigned agent, if any
  - active prompt preview
  - status/health (`idle`, `starting`, `in progress`, `waiting`, `error`, optionally `done`)
  - unread-attention indicator
  - tmux presence/target hint
- Selecting an item does **not** embed its terminal; instead the right side offers actions:
  - jump to agent window
  - summon/start agent
  - open logs/tests/shell
  - open a popup for quick actions
  - split a temporary helper pane in the current window
  - return to dashboard via keybinding/command
- Attention states should be visible from the dashboard even when agents are running elsewhere.

## Discovery Questions
1. Who is the first user? Andrew, working in tmux with multiple awp-managed workspaces and agents.
2. When do they use this feature? During active multi-agent sessions where they want fast dispatch, monitoring, and return paths.
3. What exact output/result do they need? A stable dashboard pane that can create, find, focus, and recover tmux targets for each agent/workspace.
4. What data sources are required? tmux introspection, awp workspace metadata, optional agent status/attention signals, and persisted deck state.
5. What is the smallest useful slice? A dashboard pane that lists agents/workspaces, can jump to their tmux window, create missing windows, and return to the dashboard reliably.
6. What are explicit non-goals? Embedded nested terminal layouts, replacing tmux, and sophisticated real-time multiplexed previews in v1.
7. What does “done” look like? The design makes tmux behavior unsurprising, ids are tracked safely, the MVP is small, and the architecture leaves room for a richer native dashboard later.

## Spec Change Log
- 2026-04-13: Initial draft covering tmux dashboard architecture, command rename context, and phased recommendation.
- 2026-04-13: Added left-column project/workspace roster as a first-class requirement, including active prompt preview and idle/in-progress/waiting/error visibility.
- 2026-04-13: Implementation sequencing clarified so coding can start with a narrow MVP slice before richer prompt/status plumbing and global recovery features.
- 2026-04-14: Reframed topology around popup-overlay deck and session-per-workspace model with `[awp]<repo>__<workspace>` naming. Deck has no persistent tmux identity; actions `switch-client` to workspace sessions. Updated state model, UX flows, recovery rules, and MVP accordingly.
- 2026-04-14: Phases 1–4 implemented. Deck now lists cross-repo workspaces, surfaces orphan `[awp]` sessions, supports summon / logs / tests / shell / relink / edit-prompt actions, persists session id + prompt + status metadata. Full phased delivery landed in one change.
- 2026-04-14: Popup-close-on-action — deck quits Bubble Tea after any action (summon/logs/tests/shell/relink/new) succeeds, so `display-popup -E` returns the user to the targeted tmux session. Prompt/status edits still keep the deck open.
- 2026-04-14: Default scope set to "all projects" when multi-repo data is available; `P` still toggles to current-repo scope.
- 2026-04-14: Added `n` action — records a `PendingNew{RepoRoot}` intent on the model and quits the deck; the caller re-execs `awp w open` with `cwd=<RepoRoot>`, reusing the existing interactive form (`internal/cli/open_form.go`) rather than a `-y` bypass. Works across projects regardless of the deck's invocation cwd.
- 2026-04-14: Canonicalized state-store repo keys via `.jj/repo` pointer resolution (`jj.Client.SourceRepoRoot`, `state.canonicalizeRepoRoot`) so secondary jj workspaces no longer fragment the top-level state map. One-time cleanup of `~/.awp/workspace-state.json` performed manually.

## Architecture
### 1. Core recommendation
Use awp as a **tmux-aware orchestrator** with a native Bubble Tea dashboard rendered in a tmux popup, not as a nested tmux replacement.

That means:
- The dashboard is a native awp TUI launched as an ephemeral tmux popup.
- Each awp workspace maps to its own tmux session.
- awp stores logical state and issues tmux commands to create/focus/update those sessions.
- awp treats tmux as the execution/display substrate for terminal workloads.
- awp gradually adds native dashboard panels for metadata, status, task queues, and logs summaries, while leaving full interactive shells/agents in tmux sessions.

### 2. Concrete topology
Recommended first topology:
- **One tmux session per awp workspace.**
- Session name convention: `[awp]<repo>__<workspace>` (e.g. `[awp]agent-deck__qa`). The `[awp]` prefix makes awp-managed sessions greppable and filterable in `tmux ls`.
- Inside each workspace session, reserved window names for known roles: `agent`, `logs`, `tests`, `shell`. Additional user-created windows allowed.
- The dashboard is **not** a persistent session or window. It is an overlay launched via `display-popup -E awp deck`, typically bound to `prefix + a`.
- Dashboard state persists to disk, so the popup can be closed and reopened freely without losing roster/prompt/status data.
- Optional temporary panes created only for short-lived helper tasks inside a workspace session's focused window.
- Secondary popups (from inside the deck popup or from a workspace session) used for transient actions and lightweight inspection.

Example:
- User is in session `main`, hits `prefix + a` → popup runs `awp deck`.
- Selects workspace `qa` → popup closes, `switch-client -t [awp]agent-deck__qa`.
- Inside that session: window `agent` runs the coding agent, window `logs` tails output, window `shell` is a scratch shell.
- `prefix + a` again from anywhere re-opens the overlay.

### 2a. Nested-tmux guard
If `awp deck` is invoked while `$TMUX` is set but **not** from the popup binding (e.g. user ran it by hand inside a workspace session), detect the nesting case and render in-pane rather than trying to launch a nested popup. Never spawn a child tmux server.

### 3. Process model
Separate the world into:
1. **Logical deck model**
   - projects
   - workspaces
   - agents
   - active/last prompt
   - execution status (`idle`, `starting`, `in progress`, `waiting`, `error`, `done`)
   - desired target kind
   - desired commands
   - attention state
2. **Observed tmux model**
   - current session id/name
   - known windows and panes
   - current focus
   - existence/liveness of targets
3. **Reconciliation layer**
   - compares desired and observed state
   - creates missing targets
   - repairs stale mappings
   - marks broken targets without crashing the UI

This keeps the UI simple and makes tmux failures recoverable.

## Recommended tmux interaction patterns
### Use sessions when
Use a tmux **session** for each awp workspace:
- one workspace = one session, named `[awp]<repo>__<workspace>`
- session is the durable isolation boundary: its own window list, no pollution of parent session
- `switch-client -t` jumps cleanly; detach returns user to their prior session
- easy to enumerate/recover by name prefix after server restart

### Use windows when
Inside a workspace session, use windows for the durable roles within that workspace:
- `agent` — the coding agent terminal
- `logs` — durable log tail
- `tests` — long-running watch/build loop
- `shell` — scratch shell
- additional user-created windows are fine; awp only reserves the role names above

Why: within a session, window ids are stable and easy to target; role names give awp predictable handles.

### Use panes when
Use panes sparingly and mostly for **local companion tasks** within an already-focused window:
- split current agent window to show a short-lived shell
- split current window for a temporary test runner
- split current window for side-by-side log tailing while actively debugging

Avoid using panes as the main multi-agent topology in v1 because:
- pane-heavy layouts get crowded quickly
- pane ids become easier to lose track of when users rearrange layouts
- switching between many agents is less legible than one-window-per-agent

### Use popups when
Use tmux popups for transient, interruptible, or modal tasks:
- quick command palette / summon flow
- recent logs preview
- test result summary
- confirmation dialogs
- one-off shell in current workspace
- “agent needs attention” detail view

Popups are especially good when the action should not permanently change layout.

### Simple rule of thumb
- **Session** = one workspace
- **Window** = role within a workspace (`agent`, `logs`, `tests`, `shell`)
- **Pane** = local sidecar within a window
- **Popup** = transient tool, including the deck overlay itself

## State model for safe tmux targeting
### 1. Stable identifiers
Prefer tmux ids over names for live targeting:
- session id: `#{session_id}` like `$1`
- window id: `#{window_id}` like `@7`
- pane id: `#{pane_id}` like `%12`

Names are useful for display and recovery, but not as the primary live key.

For dashboard UX, the primary visible identity should still be awp-native:
- project name
- workspace name
- agent id/name
- active prompt preview

tmux ids support actions behind the scenes; they should not dominate the left-column UX.

### 2. Persisted model
Suggested persisted entities:

```go
type DeckState struct {
    Version        int
    Sessions       map[string]SessionRecord      // keyed by session id
    Projects       map[string]ProjectRecord      // keyed by project id/name
    Items          map[string]DeckItemRecord     // keyed by deck item id = "<repo>__<workspace>"
    WorkspaceIndex map[string]string             // "<repo>__<workspace>" -> deck item id
}

// Note: no persistent DashboardTarget. The deck is a popup overlay; it has no
// durable tmux identity. State is restored from disk on each popup invocation.

type SessionRecord struct {
    SessionID     string
    SessionName   string
    ServerPIDHint string
    Windows       map[string]WindowRecord          // keyed by window id
    LastSeenAt    time.Time
}

type WindowRecord struct {
    WindowID      string
    WindowName    string
    PaneIDs       []string
    WorkspaceName string
    Role          string // deck|agent|logs|tests|shell
    LastSeenAt    time.Time
}

type ProjectRecord struct {
    ProjectID   string
    ProjectName string
    RootPath    string
    LastSeenAt  time.Time
}

type DeckItemRecord struct {
    ID              string // "<repo>__<workspace>"
    ProjectID       string
    ProjectName     string // repo
    WorkspaceName   string
    WorkspacePath   string
    AgentID         string
    ActivePrompt    string
    PromptUpdatedAt time.Time
    // Primary target is the workspace's tmux session.
    SessionID       string // authoritative live key
    SessionName     string // "[awp]<repo>__<workspace>" — recovery hint
    AgentWindowID   string // reserved "agent" window within the session, if present
    AgentPaneID     string // primary pane for command injection
    Command         []string
    Status          string // idle|starting|in_progress|waiting|done|error|unknown
    Attention       string // none|info|warning|urgent
    LastEvent       string
    LastEventAt     time.Time
    LastActivityAt  time.Time
    LastSeenAt      time.Time
}
```

### 3. Safety rules
- Treat pane/window/session ids as authoritative only after a fresh tmux query.
- Never assume a persisted id still exists.
- Before sending keys or focusing, revalidate the target.
- Store names as recovery hints, not guarantees.
- If id lookup fails but a unique matching recovery candidate exists by role/name/workspace, relink and persist the repaired mapping.
- If multiple candidates match, mark the record ambiguous and ask the user.

### 4. Association model
Recommended association rules:
- Each deck item corresponds to one awp workspace, optionally with an assigned agent.
- Each workspace belongs to one awp project.
- Each logical agent belongs to exactly one workspace at a time.
- Each logical agent/workspace has one primary tmux window target.
- That window may have multiple panes, but awp tracks one primary pane for command injection/focus.
- Auxiliary logs/tests/shell views should be represented as child actions or auxiliary targets, not separate top-level deck items.
- The left column should always be rendered from deck items, not from raw tmux windows.

## UX recommendations
### Left-column roster behavior
The left column should be the source of truth for all known awp work.

It should support:
- filter by project/workspace/prompt
- sort by attention, in-progress state, recent activity, or name
- compact status badges such as `IDLE`, `RUN`, `WAIT`, `ERR`
- a one-line truncated active prompt preview, typically 40-60 chars
- a clear empty value when no prompt is active
- quick layered keyboard navigation for dense decks

Quick layered keyboard navigation should be a deliberate future interaction pattern:
- assign home-row hint keys such as `a s d f j k l ;` to visible top-level items/groups
- when the user drills into a group, reuse the same compact hint-key set for the next visible layer
- continue repeating this pattern until the user reaches a jumpable workspace/agent target
- once a target is uniquely selected, execute a direct jump/focus action
- the interaction should feel closer to Vim EasyMotion than to linear cursoring when many items are visible

This should complement, not replace, normal cursor/filter navigation. MVP can ship with arrows/j/k first, but the architecture should reserve room for this faster layered mode.

Example row shape:
- `RUN  agent-deck/qa        design tmux dashboard...`
- `IDLE agent-deck/docs      —`
- `WAIT payments/bugfix-42   investigate CI failure...`

### Switching focus
- Dashboard selection + `Enter`: dismiss the popup and `switch-client -t [awp]<repo>__<workspace>` to the workspace session, selecting its `agent` window by default.
- Dashboard selection + `o`: open an action menu (secondary popup) without leaving the deck.
- The deck-opening binding (e.g. `prefix + a`) is the universal return path from any awp-managed session.

### Summoning an agent
“Summon” should mean: ensure target exists, then focus it.

Flow:
1. User selects a workspace row in the left column.
2. The right column shows the full active prompt, status details, and available actions.
3. awp checks for a tmux session named `[awp]<repo>__<workspace>` (and revalidates the stored session id).
4. If session exists, dismiss popup and `switch-client -t` to it (selecting the `agent` window).
5. If missing, create the session with `new-session -d -s [awp]<repo>__<workspace>`, create reserved windows (`agent` at minimum), start the command in `agent`, persist new ids, then switch-client.
6. If the workspace path is missing or invalid, show actionable repair UI in the popup.

### Opening logs/tests/shells
Recommended behavior:
- `l` logs: popup first, with option to promote to a durable window if needed.
- `t` tests: popup for quick run; durable window if the user chooses watch mode or repeated runs.
- `s` shell: popup or split pane from the current focused agent window; avoid spawning permanent new windows for one-off shells.

This keeps the layout clean while still allowing escalation to durable views.

### Returning to the dashboard
The deck is an overlay, not a place. The return path is simply the deck binding:
- recommended tmux binding: `bind a display-popup -E -w 90% -h 90% awp deck`
- works from any session, any window, including awp-managed workspace sessions
- because the deck has no persistent tmux identity, there is no duplicate-deck problem
- `awp deck` run in a plain pane (no popup binding) falls back to in-pane rendering

### Background agents needing attention
The dashboard should surface attention independent of focus:
- colored badge or icon in the left-column list
- sortable “needs attention” section
- optional tmux window rename prefix/suffix like `! qa`
- optional tmux activity/monitor bells as secondary signals, not the primary UX

Attention should come from awp logical events where possible, not only tmux activity flags.

A live tmux pane should not automatically imply `in progress`; awp should track logical prompt/task state separately from mere tmux existence.

## Failure modes and edge cases
### Pane/window deleted
Behavior:
- tmux lookup fails during refresh or action.
- Mark target stale.
- Show item as “missing target”.
- Offer repair actions: recreate, relink, clear association.
- Do not silently send commands to a recovered-but-unverified similarly named target.

### tmux session renamed/restarted
Important detail: session **name** may change, but session **id** is more reliable only for the life of that server. A full tmux server restart invalidates all ids.

Behavior:
- On each popup invocation, list sessions and match `[awp]<repo>__<workspace>` names to rebuild the session-id cache.
- If persisted ids are all missing, assume server restart and rebuild associations from name prefix `[awp]` unambiguously.
- A user-renamed session that drops the `[awp]` prefix is treated as detached from awp; the deck offers to recreate or relink.

### awp launched outside tmux
Recommended v1 behavior:
- Fail clearly: “awp deck must run inside tmux”.
- Offer an actionable hint: `tmux new-session -A -s awp` then run `awp deck`.

Possible v2 behavior:
- `awp deck --spawn-tmux` creates/attaches a session and opens the dashboard automatically.

### jj working copy is stale
Recommended v1 behavior:
- Detect the stale-working-copy error explicitly.
- Show the jj output and hint to the user.
- In interactive use, ask whether awp should run `jj workspace update-stale` now.
- Only run the command after explicit confirmation.
- In non-interactive use, never mutate automatically; return the actionable error.

### Stale pane ids
Behavior:
- Revalidate before every pane-targeted operation.
- If window still exists but pane does not, either:
  - select the window’s active pane as replacement if role rules allow, or
  - recreate the auxiliary pane if the action requested one.
- Persist repaired ids only after successful validation.

## Implementation plan for awp in Go
### Package/module boundaries
Recommended packages:
- `internal/deck`
  - domain types: agent, workspace card, attention, actions
  - orchestration/reconciliation service
- `internal/deckstate`
  - persisted JSON store for deck/session/agent mappings
- `internal/tmux`
  - tmux client abstraction and concrete implementation
  - query/create/focus/popup/split helpers
- `internal/deckui`
  - Bubble Tea dashboard model/view/update
- `internal/agent`
  - optional logical agent metadata/status model
- `internal/workspace`
  - existing workspace discovery and validation
- `internal/cli`
  - `awp diff`, future `awp deck`, command wiring

### tmux abstraction layer
Expand `internal/tmux` from simple name-based helpers into id-based operations and queries.

Suggested interface shape:

```go
type Client interface {
    InTmux() bool
    CurrentSession(ctx context.Context) (Session, error)
    ListSessions(ctx context.Context) ([]Session, error)
    ListWindows(ctx context.Context, sessionID string) ([]Window, error)
    ListPanes(ctx context.Context, windowID string) ([]Pane, error)
    NewWindow(ctx context.Context, req NewWindowRequest) (Window, error)
    SplitPane(ctx context.Context, req SplitPaneRequest) (Pane, error)
    DisplayPopup(ctx context.Context, req PopupRequest) error
    FocusWindow(ctx context.Context, windowID string) error
    FocusPane(ctx context.Context, paneID string) error
    SendKeys(ctx context.Context, paneID string, text string, enter bool) error
    RenameWindow(ctx context.Context, windowID string, name string) error
    KillWindow(ctx context.Context, windowID string) error
}
```

Prefer tmux format queries returning ids and paths in one pass, rather than many ad hoc shell calls.

### UI model responsibilities
The dashboard UI should:
- render deck items and status
- own selection, filtering, action menus, and lightweight notifications
- request actions from a controller/service
- never directly encode tmux command strings in view code

The controller/service should:
- translate UI actions into tmux operations
- validate/recover targets
- update persisted state
- surface friendly status back to the UI

### Persistence needs
Persist only what is useful for recovery and return paths:
- dashboard target ids
- project/workspace/agent inventory for the left column
- active prompt summary and prompt timestamps
- workspace -> session/window/pane mappings
- last-known workspace path and command
- attention/status metadata if it helps restore dashboard state

Do **not** persist ephemeral layout details that tmux already owns unless awp truly needs them.

### Implementation sequencing
#### Phase 1: bootstrap the deck command
Build the thinnest useful slice first:
1. Add `awp deck` CLI entrypoint.
2. Require tmux and fail clearly outside tmux.
3. Launch a two-column Bubble Tea dashboard. Intended invocation is `display-popup -E awp deck`; fall back to in-pane rendering when invoked directly.
4. Document the recommended tmux binding (`bind a display-popup -E -w 90% -h 90% awp deck`) in CLI help.
5. Populate the roster from the current repo's known workspaces first, with status defaults and empty prompt metadata where needed.
6. Selecting a workspace ensures a session `[awp]<repo>__<workspace>` exists (create with `agent` window if missing) and `switch-client -t` to it.

Notes:
- This phase intentionally limits the data source to the current repo so the command is usable quickly.
- The UI must explicitly label phase-1 placeholder metadata so users do not mistake repo-basename/grouping or idle prompt/status defaults for real modeled awp state.
- The left-column shape should still match the long-term design, even if prompt/status values are placeholders.
- This is a delivery slice, not the final architecture boundary.

#### Phase 2: persist deck metadata
1. Add deck state persistence for active prompt summary, timestamps, status, and attention.
2. Render real prompt/status values in the left column.
3. Keep tmux ids as the targeting source of truth as the tmux client grows.

#### Phase 3: broaden inventory and recovery
1. Expand the roster from current-repo-only to all known awp projects/workspaces.
2. Add stale-target detection and explicit repair actions.
3. Add dashboard window identity persistence and return-to-dashboard behavior.

#### Phase 4: richer helper surfaces
1. Add popups for logs/tests/quick actions.
2. Add helper panes for short-lived sidecars.
3. Add stronger attention and agent lifecycle signals.

### Testing strategy
1. **Unit tests: tmux parser/client**
   - parse `list-sessions`, `list-windows`, `list-panes` formatted output
   - verify id-based targeting commands
2. **Unit tests: reconciliation**
   - existing target valid
   - target missing and recreated
   - ids stale but recoverable by hints
   - ambiguous recovery path
3. **Unit tests: UI model**
   - selection/filter/action dispatch
   - attention badge rendering state
   - missing-target states
4. **Store tests**
   - state persistence and schema migration/versioning
5. **Integration-style tests with fake tmux runner**
   - summon agent
   - return to deck
   - popup/split escalation
6. **Manual validation in real tmux**
   - create/delete windows externally
   - rename sessions/windows
   - restart tmux server
   - launch outside tmux

## Recommendation: tmux-driven vs native-awp rendering
### Strong recommendation
For the foreseeable future, awp should **primarily orchestrate tmux as an external window manager**, while gradually adding native awp dashboard panels for non-terminal views.

### Why
This is the best fit for the stated goals:
- simplest first version
- robust targeting with tmux ids
- low-surprise UX for tmux users
- preserves full terminal fidelity for agents, shells, tests, and logs
- leaves room for richer native summaries without requiring awp to become a terminal multiplexer

### What to keep tmux-managed
Keep these tmux-native:
- interactive shells
- running agent terminals
- long-running test/watch loops
- durable logs terminals

### What to move native over time
Move these into native awp panels when valuable:
- dashboard list/grid
- task/attention summaries
- recent event feed
- compact logs snippets
- command palette / action menus
- searchable agent/workspace metadata

## MVP scope
A minimal MVP should do only this:
1. Add `awp deck` command that requires tmux.
2. Render as a tmux popup overlay via the recommended binding; fall back to in-pane when invoked directly.
3. Render a left-column roster of all known awp projects/workspaces.
4. For each row, show workspace name, active prompt summary, and status (`idle`, `in progress`, `waiting`, `error`).
5. Show details/actions for the selected row on the right.
6. Summon a workspace by ensuring one tmux session `[awp]<repo>__<workspace>` exists with an `agent` window, then `switch-client` to it.
7. Re-focus existing workspace sessions by stable session id when possible; fall back to name lookup.
8. Track session id, session name, agent window id, agent pane id, workspace association, and prompt/status metadata.
9. Detect and clearly report missing/stale targets.
10. Document the deck binding as the universal return path.

Not in MVP:
- pane orchestration beyond minimal helper splits
- advanced popup flows
- log/test management beyond opening a shell or command target
- auto-healing across complex ambiguities

## Good v2
A good v2 adds:
- project-grouped or expandable left-column navigation
- layered hint-key navigation using repeated home-row keys (for example `a s d f j k l ;`) to drill from groups to workspaces to jump targets
- prompt history in the right column
- popups for logs/tests/quick actions
- helper split panes from the current agent window
- attention/unread indicators
- better recovery and relinking after tmux churn
- command palette
- optional activity integration with tmux flags
- better native summary panels for recent output/status
- explicit agent lifecycle events to distinguish `idle`, `in progress`, `waiting`, and `done` more accurately

## Tradeoffs: tmux-driven vs native-awp rendering
### tmux-driven orchestration
Pros:
- simplest and most honest model
- full terminal compatibility
- leverages user’s existing tmux habits
- easier to target durable workspaces
- lower implementation risk

Cons:
- dashboard cannot truly “contain” live child terminals
- more context switching than a fully native dashboard
- state reconciliation required when users manipulate tmux outside awp

### native-awp rendering
Pros:
- more cohesive single-app feeling
- better room for custom summaries and dashboards
- less dependence on tmux layout for metadata views

Cons:
- hard to replicate real terminal interactivity well
- risks becoming a partial terminal multiplexer
- higher implementation cost and more surprising behavior
- likely worse first version

## Acceptance Criteria
- [x] The architecture clearly defines one dashboard pane controlling external tmux targets rather than implying nested tmux windows.
- [x] Window/pane/popup usage guidelines are explicit and simple enough to drive implementation decisions.
- [x] The state model prefers tmux ids and defines safe recovery behavior for stale or deleted targets.
- [x] UX guidance covers focus switching, summon flow, helper views, return path, and attention handling.
- [x] Failure modes and a phased Go implementation plan are documented.
- [x] The recommendation between tmux-driven orchestration and native-awp rendering is explicit, with MVP and v2 scope called out.
- [x] Phase 1 implementation lands: `awp deck` opens a two-column tmux-only dashboard for current-repo workspaces with selection and jump behavior.

## QA / Human Review Test Plan
### Setup
- [ ] Build `awp` from repo root.
- [ ] Prepare a tmux session with multiple windows and at least one awp workspace.
- [ ] Review current `internal/tmux` capabilities against the proposed interface.

### Core Happy Path
- [ ] Walk through the proposed MVP flow: open dashboard, summon agent, focus agent, return to dashboard.
- [ ] Verify the design does not require impossible nested tmux embedding.
- [ ] Verify the window/pane/popup guidance feels natural for daily tmux usage.

### Edge Cases & Failure Modes
- [ ] Review deleted-window, stale-pane-id, session-restart, and outside-tmux behavior against the proposed state machine.
- [ ] Confirm ambiguous recovery is surfaced to the user instead of guessed.

### Regression Checks
- [ ] Existing workspace management assumptions remain compatible with one-window-per-agent orchestration.
- [ ] Existing diff command naming is consistent with `awp diff`.

### Reviewer Notes
- Capture any disagreement about command naming, default topology, or how much pane usage is acceptable in v1.

## Validation
- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `go build ./...`
