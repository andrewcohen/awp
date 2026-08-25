# Deck Dev-Server URL Capture

## Metadata
- **Spec ID**: `20260514-ih0x`
- **Feature name**: Deck dev-server URL capture
- **Owner**: Andrew Cohen
- **Status**: In Progress
- **Last updated**: 2026-05-14

## Goal
Surface a clickable `http://localhost:<port>` URL for each workspace in the
deck's right-side details panel, populated automatically by detecting which
TCP port the workspace's dev server bound to, with a single keypress to open
it in the system browser.

## User Problem
When I run `pnpm dev` (or any framework dev server) in a workspace, the tool
picks an open port at runtime — Vite hops 5173 → 5174 → 5175 when the lower
ports are taken, Next picks 3000 → 3001 → 3002, etc. Today I have to focus
the tmux pane and read the banner to learn which port it picked, then type
the URL into a browser. The deck already knows the workspace, the project,
the tmux session, and the PR — it should just *show* me the URL and let me
open it.

## Scope
### In scope (v1)
- Discover one TCP listener owned by any descendant process of any pane in
  the workspace's tmux window. The shape of the URL is always
  `http://localhost:<port>`.
- Display `Dev: http://localhost:<port>` as a new line in the deck's right
  details panel (`renderDetails` in `internal/deckui/model.go`), between
  `Head:` and `Bookmark:`. The line is absent when no URL has been
  discovered for the selected workspace.
- A single keybinding (`u`, for "URL") that, when the selected workspace
  has a discovered URL, opens it via `open` (macOS) / `xdg-open` (Linux).
- Automatic clear: if the underlying listener disappears, the URL is
  removed from the next render within one discovery tick.
- macOS and Linux. Discovery uses the same OS-detection pattern as
  `internal/jobs/orphan_darwin.go` / `orphan_linux.go`.
- Silent discovery — no activity-bar entry while polling, no notification
  when found. Matches the PR-status pattern (Decision 4 in
  `[[20260513-lbkj-deck-global-activity-bar-spec]]`).

### Out of scope (v1)
- Multiple simultaneous URLs per workspace (e.g. `pnpm dev` *and*
  `pnpm api`). v1 surfaces one URL chosen by heuristic; defer multi-URL UX.
- Capturing the exact advertised URL — Vite `base: '/admin/'` or non-`/`
  paths are not parsed. We only know `localhost:<port>`.
- Stdout log scraping. Socket-scan is the sole signal in v1.
- Non-localhost URLs (Docker containers, remote dev hosts, IPv6-only
  binds). These almost never matter for `pnpm dev` on the user's box and
  add real complexity (port-mapping inspection, DNS).
- Persistence across deck restarts. A captured URL is in-memory state on
  the `Item`; restart of the deck re-discovers from scratch.
- Configurable port allow/deny lists. The heuristic is hard-coded in v1.
- Custom protocols (e.g. https on a local dev cert, ws://). Default to
  `http`. If the dev server is HTTPS-only, clicking the URL will fail
  visibly and the user can adjust.

## UX
### CLI
No new flags or commands. Discovery is implicit on the deck.

### TUI
**Right details panel** (existing layout, new line bolded):

```
details

Project:   awp
Workspace: port-capture
Status:    in-progress
Session:   awp/port-capture
Live:      yes
Path:      /Users/acohen/.awp/workspaces/awp/port-capture
Head:      feat(deck): R-key rename workspace with inline form
Dev:       http://localhost:5173
Bookmark:  andrew/port-capture
PR:        #427  ready

Prompt:
…
```

When no URL has been discovered for the selected workspace, the `Dev:` line
is omitted entirely — no "(none)" placeholder, since the absence of a dev
server is the common case for most workspaces.

The URL is styled with `lipgloss` `Underline(true)` and a colored
foreground (proposed color `39`, same as the running-job glyph) so it
reads as a link.

**Key binding** (top-level, row-mode only — not active inside modes):

| Key | Action                                                |
| --- | ----------------------------------------------------- |
| `u` | open the selected workspace's dev URL in the browser |

Behavior:
- If the selected workspace has `Item.DevURL != ""`, shell out to `open`
  (macOS) or `xdg-open` (Linux) with the URL. Asynchronous fire-and-forget
  — do not block the UI.
- If no URL: no-op. (Optionally a brief footer status like
  `no dev url — start a server`, but lean toward silent to match the rest
  of the deck.)

**Help overlay**: `deckKeyGroups` in `internal/deckui/model.go` gets the
new binding. The `?` overlay updates automatically because it renders from
that slice.

## Discovery Questions
1. **Who is the first user?** Andrew, running multi-repo workspaces with
   `pnpm dev` / Vite in a tmux pane per workspace.
2. **When do they use this feature?** Every time they spin up a workspace
   for frontend work and want to click through to the running app.
3. **What exact output/result do they need?** A clickable URL in the
   details panel, plus a one-key shortcut to open it.
4. **What data sources are required?**
   - `tmux list-panes -t <window> -F '#{pane_pid}'` to get pane shell PIDs.
   - PID descendant walk: a single `ps -ax -o pid=,ppid=` on macOS parsed
     in Go; `/proc/<pid>/task/<tid>/children` on Linux.
   - Listening-socket enumeration: `lsof -nP -iTCP -sTCP:LISTEN -a -p
     <comma-pids>` on macOS; `ss -tlnpH` (or `/proc/net/tcp` correlated
     to `/proc/<pid>/fd/*` as fallback) on Linux.
5. **What is the smallest useful slice?** Detect a listener on any
   descendant of any tmux pane in the workspace window, pick the lowest
   port, display + open it. No multi-URL, no path capture, no persistence.
6. **What are explicit non-goals?** See "Out of scope" above.
7. **What does "done" look like?** I can `pnpm dev`, wait ~2s, see
   `Dev: http://localhost:5173` in the details panel, hit `g`, the app
   opens in my browser. I `Ctrl+C` the dev server; within ~2s the line
   disappears.

## Decisions (locked 2026-05-14)
1. **Key**: `u` (mnemonic: "URL"). Verified unused at top level and in
   every modal. `g`/`G` are used inside the jobs overlay and were ruled
   out to keep mode-key parity.
2. **Discovery scope**: all panes in the workspace's tmux **session**
   (not just the agent window), so a dev server in a separate window or
   split is picked up without bookkeeping. The lowest-port heuristic
   filters out HMR and agent-side high-port sockets in practice.
   **Escape hatch deferred to v2**: a "tag this custom command as a
   dev-server" flag, if accidental catches (MCP server, debug HTTP) prove
   to be a real annoyance. v1 just scans.
3. **Multi-listener heuristic**: pick the numerically lowest port. If
   two are within 100 of each other, prefer the one bound to `127.0.0.1`
   over `0.0.0.0` — weak signal, cheap.
4. **Polling cadence**: 2-second tick while at least one workspace has a
   tmux session, gated off otherwise so idle decks burn no CPU.
5. **Cross-workspace concurrency**: one shell-out per tick across all
   active workspaces (one `lsof`/`ss` call, demuxed by PID). Drop down
   to per-workspace calls only if the global call proves brittle in
   practice.

## Spec Change Log
- 2026-05-14: Initial draft.
- 2026-05-14: Locked decisions — key `u`, scope = whole session, lowest-
  port heuristic, 2s tick, one shell-out per tick. Opt-in tagging
  deferred to v2.
- 2026-05-14: Implementation deviations:
  - **No `Item.DevURL` field**: Items are reconstructed on every refresh
    tick, so the discovered URLs live on the Model as `devURLs
    map[string]string` keyed by `SessionName`. `renderDetails` consults
    the map directly. Net effect for the user is identical.
  - **Linux `/proc` fallback dropped from v1**: spec proposed `ss` with
    `/proc/net/tcp` fallback; v1 only uses `ss` (iproute2 ships by
    default on modern distros). If we hit a real system without `ss`,
    add the fallback then.
  - **Cross-platform PID walker**: spec proposed darwin- and linux-
    specific PID-descendant walks. The implementation uses a single
    cross-platform `ps -A -o pid=,ppid=` shell-out plus an in-memory
    BFS, so the OS-specific code is just `lsof` (darwin) vs `ss`
    (linux) for the listener enumeration.
  - **Status**: Done (pending manual TUI verification per QA plan).
- 2026-05-14: First-run feedback — Claude Code's own local IPC socket
  (PID-tree descendant of the agent pane, bound to `127.0.0.1:59128`)
  was getting surfaced as the workspace's dev URL because it was the
  only listener and the lowest-port heuristic had nothing to filter
  against. Added a port-range filter to `pickURL`: only ports in
  `[1024, 9999]` are eligible. This drops ephemeral-range noise (MCP
  servers, Claude Code, language servers) while preserving every
  common dev-server default (Vite 5173, Next 3000, Phoenix 4000,
  Storybook 6006, Webpack 8080, Django 8000, etc.). Tradeoff: legacy
  Expo dev tools on 19000+ are no longer caught; modern Expo uses
  8081 and is unaffected.

## Implementation Plan
1. **Add the `DevURL` field.** Extend `Item` in `internal/deckui/model.go`
   with `DevURL string`. Wire it into the existing item-construction site
   in `internal/cli/deck.go` (initially always empty).
2. **Define the discovery interface.** New file
   `internal/deckui/portcapture.go`:
   ```go
   type DevPortDiscoverer interface {
       Discover(ctx context.Context, panePIDs []int) (port int, ok bool)
   }
   ```
   Constructor picks darwin/linux impl at process start. The deck wiring
   passes a single discoverer instance through to the refresh loop.
3. **Implement platform discoverers.**
   - `portcapture_darwin.go`: `lsof -nP -iTCP -sTCP:LISTEN -a -p
     <pid1,pid2,…>`. Parse the name column for `:<port>`. Apply heuristic
     (lowest port; tiebreak on bind address).
   - `portcapture_linux.go`: prefer `ss -tlnpH`; fall back to
     `/proc/net/tcp` + `/proc/<pid>/fd` correlation.
   - `portcapture_other.go` with `//go:build !darwin && !linux`: returns
     `(0, false)` — feature is a no-op on unsupported OS.
4. **PID descendant walker.** Small helper, also OS-specific:
   - macOS: single `ps -ax -o pid=,ppid=` shell-out, parsed in Go, walked
     in-memory (one shell-out per tick).
   - Linux: walk `/proc/<pid>/task/*/children`.
5. **Pane PID enumeration.** New helper (probably in `internal/tmux/`):
   `func SessionPanePIDs(session string) ([]int, error)` that shells out
   to `tmux list-panes -s -t <session> -F '#{pane_pid}'` (the `-s` flag
   widens the listing from a single window to the whole session).
6. **Refresh loop integration.** In `internal/cli/deck.go`, add a
   `devURLTickMsg` on a 2-second cadence (only scheduled when there's at
   least one active workspace). On each tick:
   - Collect `(workspaceName, panePIDs)` pairs across all active
     workspaces.
   - One discovery call per workspace (or one global call demuxed by
     PID — see open question 5).
   - Emit a `devURLsMsg{ map[workspaceName]string }` and merge into the
     `Items` slice in `Model.Update`.
7. **Render the URL line.** Update `renderDetails` in
   `internal/deckui/model.go` to insert the `Dev:` line between `Head:`
   and `Bookmark:`, with `Underline(true)` + `Foreground("39")` styling.
8. **Bind the key.** Add a `case "u"` in `Model.Update` handling row mode.
   The handler shells out to `open` / `xdg-open` with the URL,
   fire-and-forget via `exec.Cmd.Start()` (no `Wait`).
9. **Register the key in `deckKeyGroups`.** Add the entry so the `?`
   overlay picks it up automatically.
10. **README.** Add the key to the key-bindings table; one-paragraph
    section explaining dev-URL capture (what it does, that it's
    automatic, that the URL is `localhost` regardless of bind address).
11. **Tests.**
    - `portcapture_test.go`: parse golden `lsof` and `ss` outputs into
      `(port int, ok bool)`. Cover empty output, single listener, multi
      listener with heuristic tiebreak.
    - `model_test.go`: extend with a case showing `Dev:` line appears
      when `Item.DevURL != ""` and is absent otherwise.

## Acceptance Criteria
- [ ] In a workspace with `pnpm dev` running on Vite's default port, the
      deck's details panel shows `Dev: http://localhost:5173` within 4
      seconds of the server binding.
- [ ] Pressing the bound key opens that URL in the system browser.
- [ ] Stopping the dev server clears the line within 4 seconds.
- [ ] If Vite picks 5174 because 5173 was taken, the displayed URL
      reflects 5174.
- [ ] If both Vite (5173 HTTP) and its HMR socket (e.g. 24678) are
      listening, the displayed URL is `:5173`.
- [ ] Workspaces with no dev server show no `Dev:` line.
- [ ] No new activity-bar entries appear during normal discovery.
- [ ] `go test ./...`, `go vet ./...`, `go build ./...` all pass.

## QA / Human Review Test Plan
### Setup
- [ ] Build the binary: `go build -o /tmp/awp ./cmd/awp`.
- [ ] Open the deck against a project with a Vite-based frontend
      workspace.
- [ ] Have a second terminal handy to start/stop the dev server.

### Core Happy Path
- [ ] Run `pnpm dev` in the workspace's tmux window. Within ~4s, observe
      `Dev: http://localhost:5173` (or whatever port Vite picked) in the
      right details panel.
- [ ] Press the bound key. Confirm the URL opens in the default browser
      and renders the running app.
- [ ] Kill the dev server (`Ctrl+C` in the pane). Within ~4s the `Dev:`
      line disappears.

### Edge Cases & Failure Modes
- [ ] Start `pnpm dev` with port 5173 already in use elsewhere. Observe
      Vite picks 5174; deck reflects 5174.
- [ ] Confirm the HMR websocket (if Vite opens one on a higher port) is
      *not* displayed — only the HTTP port.
- [ ] Run `pnpm dev` in two workspaces simultaneously (different ports).
      Each workspace's details panel shows the correct port for that
      workspace.
- [ ] On a workspace with no dev server, verify the `Dev:` line is absent
      and pressing the bound key is a no-op (or quiet hint, per Open
      Question 1 resolution).
- [ ] On Linux without `ss` installed (only `/proc`), confirm fallback
      still produces a port.

### Regression Checks
- [ ] PR-status glyph still renders correctly alongside the new line.
- [ ] Activity bar does *not* gain an entry for discovery polling.
- [ ] `?` help overlay shows the new key under its group.
- [ ] Existing top-level keys (`a`, `B`, `c`, `C`, `D`, `e`, `f`, `i`,
      `J`, `k`, `L`, `n`, `o`, `P`, `r`, `R`, `s`, `v`, `x`, `/`) still
      do what they did before — the new binding doesn't collide.

### Reviewer Notes
- Capture commands run, observed output, and any follow-up issues.

## Validation
- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `go build ./...`
