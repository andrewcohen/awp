# Rust rewrite — ratatui / libghostty / zmx inverted deck

## Metadata
- **Spec ID**: `20260709-vn9m`
- **Feature name**: `awp Rust rewrite (ratatui + libghostty + zmx), inverted multi-panel deck`
- **Owner**: AI coding agent (spec) → implementer agent (build)
- **Status**: Planned
- **Last updated**: 2026-07-09

## Goal
Rebuild `awp` in Rust as a single, purpose-built agent workspace pilot whose
deck is the **primary surface** — a flattened multi-panel UI (project/workspace
sidebar → per-workspace shell tabs → live agent terminal) that composites in
one process, instead of a Bubble Tea popup layered over tmux. The rewrite fixes
the current architecture's structural weaknesses (a ~7k-line god-object model,
tmux fused across the codebase, racy whole-file JSON state) while preserving
every hard-won behavior the Go version encodes.

## User Problem
The Go `awp` works but has degraded in quality: subtle bugs cluster in a fused
UI/domain model, ~388 swallowed errors, and multi-process JSON state races
(the deck plus every `report-status` hook rewrite the same file). Meanwhile the
desired UX — a persistent deck with a live agent beside it, instant switching
across ~25 sessions — is structurally awkward on tmux popups. The maintainer
wants a clean, well-architected, lint-enforced foundation designed around the
now-known requirements.

### Validation already done (PoCs)
Two throwaway PoCs (in sibling dirs, not in this repo) proved the UX is real and
cheap in *both* candidate stacks, reading the real `~/.awp/workspace-state.json`:
- **Rust**: `ratatui` + `tui-term` (`vt100`) + `portable-pty` — the parity build.
- **Go**: `vaxis` + `widgets/term`.
Both render sidebar → shell tabs → live terminal, flat, no popup. This spec
takes the **Rust** stack to production, upgrading the VT engine to libghostty and
adding zmx-backed persistence.

## Decisions (locked)
1. **Language/stack**: Rust. UI `ratatui`. VT engine `libghostty-vt` (via C ABI /
   FFI). Session persistence `zmx`. PTY plumbing `portable-pty`.
2. **Location**: new `rust/` Cargo workspace on branch `andrew/rust-rewrite`.
   The Go tree stays intact on the branch; `rust/` graduates to root later.
3. **Coexistence**: side-by-side. New SQLite store at `~/.awp/state.db`.
   First run **imports** existing `workspace-state.json`, `pin-groups.json`, and
   the PR-status cache and **leaves them intact**, so Go `awp` and the Rust build
   can run against the same repos during transition.
4. **Quality bar**: pragmatic + enforced — `rustfmt`, `clippy::all -D warnings`
   (CI-gated), `cargo-deny`, `cargo-audit`, `cargo-nextest`, `rust-toolchain.toml`
   pin. Not pedantic/nursery (avoid `#[allow]` noise).

### Risks accepted (documented, not re-litigated)
- **Alpha Zig dependencies under the core**: `libghostty-vt` (new C API) and
  `zmx` (young, single-maintainer) are pre-1.0 Zig projects; the broader Zig
  ecosystem took a hit when Bun moved to Rust. Mitigation: both sit behind
  Rust trait boundaries (`VtEngine`, `SessionBackend`) with pure-Rust default
  impls (`vt100`, `portable-pty`), so the build is never blocked on the Zig
  toolchain and either can be swapped without touching call sites.
- **No inherited test suite**: the Go tests do not port. Mitigation: the
  verification harness (below) is built alongside, and the **must-preserve
  behaviors** checklist is the acceptance surface.
- **Encoded-knowledge loss**: the Go code holds many edge-case fixes.
  Mitigation: the must-preserve checklist mines them from README/CLAUDE.md/code.

## Scope
### In scope (v1)
- `rust/` Cargo workspace with enforced lint/format/CI tooling.
- UI-agnostic **domain core** (`awp-core`) as the single source of truth.
- **SQLite store** (`awp-store`) with WAL, replacing the three JSON files and
  the file-as-IPC status channel; first-run migration from existing JSON.
- **`SessionBackend`** trait with a `zmx` impl and a local `portable-pty` impl.
- **`VtEngine`** trait with a `libghostty` impl and a `vt100` impl.
- **`awp-tui`** ratatui binary: the flattened deck (sidebar → shell tabs → live
  pane), instant switching, one keymap, no popup layers.
- **Status reporting**: `awp report-status` writes to the store (row update),
  hooks install/self-heal, no-op outside awp sessions.
- Parity with the Go deck's core roster/status/PR/pin/scope features.

### Out of scope (v1)
- Removing the Go tree from the branch (graduation is a later change).
- Feature-for-feature parity with every deck modal on day one (phased).
- Windows support (macOS + Linux only, matching libghostty targets).
- Cross-host / remote orchestration beyond what zmx multi-client gives free.

## Target architecture

### Crate layout (`rust/`)
Dependency direction is strict and enforced: **UI depends on core; core never
depends on UI.** This is the fix for the god-object problem.

```
rust/
  Cargo.toml                 # [workspace], shared [workspace.lints], deny/fmt config
  rust-toolchain.toml
  deny.toml  rustfmt.toml
  .github/workflows/ci.yml   # (or mise task) fmt + clippy -D + nextest + deny + audit
  crates/
    awp-core/                # domain types, state, reducer. NO ratatui, NO sqlite driver.
    awp-store/               # SQLite persistence + JSON migration. depends on awp-core.
    awp-session/             # SessionBackend trait + zmx/local impls. depends on awp-core.
    awp-vt/                  # VtEngine trait + libghostty/vt100 impls + a Screen type.
    awp-agent/               # jj / gh / hooks orchestration (subprocess). depends on core.
    awp-tui/                 # ratatui binary. depends on ALL of the above.
```
Rationale: `awp-core` compiles with zero I/O and zero UI deps, so it is fully
unit-testable and the render layer is a thin projection — a renderer swap (or a
second frontend) touches only `awp-tui`.

### Domain core (`awp-core`)
- Types: `Project`, `Workspace`, `WorkspaceId {repo_root, name}`, `Status`
  (`Idle|Starting|Working|Waiting|Exited|Error|Done`), `PrRef`, `PinGroup`,
  `Scope`, `Shell/Window`.
- **Single reducer**: `fn reduce(state: &mut AppState, ev: Event) -> Vec<Effect>`.
  ALL mutations (user keys, store change notifications, backend events, job
  completions) flow through it. No mutation happens outside the reducer. This
  kills the race class that the Go version has from scattered goroutine writes +
  file IPC.
- `Effect` is a pure description of side effects (spawn shell, fetch PR, write
  store row); an executor performs them off the reducer and feeds results back
  as `Event`s. Keeps the core deterministic and testable.

### Store (`awp-store`) — SQLite, WAL
Chosen for **multi-writer correctness**, not query power or speed (switching
stays fast because the roster is loaded into RAM regardless of backend). Driver:
`rusqlite` (bundled SQLite) — acceptable C dep; keep it isolated in this crate.

Schema (initial):
```sql
PRAGMA journal_mode=WAL;              -- concurrent deck + hook writers
CREATE TABLE workspaces (
  repo_root     TEXT NOT NULL,
  name          TEXT NOT NULL,
  path          TEXT NOT NULL,
  bookmark      TEXT,
  pr_number     INTEGER,
  session_id    TEXT,
  session_name  TEXT,
  status        TEXT,
  active_prompt TEXT,
  unread        INTEGER DEFAULT 0,
  pin_group     TEXT,
  agent_window  TEXT,
  agent_pane    TEXT,
  updated_at    INTEGER NOT NULL,    -- epoch ms, for change detection
  PRIMARY KEY (repo_root, name)
);
CREATE TABLE pin_groups   (name TEXT PRIMARY KEY, label TEXT, sort_order INTEGER);
CREATE TABLE pr_status    (repo TEXT, pr_number INTEGER, state TEXT, ci TEXT,
                           fetched_at INTEGER, PRIMARY KEY (repo, pr_number));
CREATE TABLE schema_meta  (key TEXT PRIMARY KEY, value TEXT);  -- version, migrated_from
```
- A status hook is `UPDATE workspaces SET status=?, active_prompt=?, updated_at=?
  WHERE repo_root=? AND name=?` — a partial, row-level, transactional write.
  This is the single biggest correctness win over whole-file JSON rewrite.
- **Cross-process change detection**: the deck polls `PRAGMA data_version` on a
  cheap tick; when it changes, reload dirty rows (`updated_at > last_seen`). No
  file watching, no lost updates. (Optional later: a tiny unix-socket ping from
  writer→deck to avoid polling latency.)

### Migration / bootstrap (side-by-side, non-destructive)
On startup, if `state.db` is missing or `schema_meta.migrated_from` is unset:
1. Read `~/.awp/workspace-state.json` (`repo → workspace → Entry`), map every
   field into `workspaces` (honor legacy `PROverride` alias for `pr_number`).
2. Read `~/.awp/pin-groups.json` → `pin_groups`.
3. Read the PR-status cache → `pr_status`.
4. Write `schema_meta(migrated_from = <timestamp>)`. **Do not delete the JSON.**
Idempotent and re-runnable. A `awp migrate --dry-run` prints the diff.

### Session backend (`awp-session`)
```rust
pub trait SessionBackend {
    fn ensure(&self, id: &WorkspaceId, spec: &SessionSpec) -> Result<SessionInfo>;
    fn windows(&self, id: &SessionId) -> Result<Vec<Window>>;   // the shell tabs
    fn attach(&self, id: &SessionId, win: &WindowId) -> Result<Attached>; // stream+input+resize
    fn list(&self) -> Result<Vec<SessionInfo>>;
    fn kill(&self, id: &SessionId) -> Result<()>;
}
```
- `ZmxBackend` (feature `zmx`, default in release): sessions persist across deck
  exit / SSH disconnect; `attach` connects to zmx's unix socket and receives the
  same byte stream for rehydration; multi-client. This is the persistence layer.
- `LocalBackend` (feature `local`, default in dev): spawns via `portable-pty`,
  no persistence. Lets the whole app build/run with zero Zig toolchain.
- **Live-pane mirroring** (the true-mirror endgame): `attach` returns a byte
  stream from the *existing* PTY (not a fresh shell). For `LocalBackend` this is
  a spawned child; for `ZmxBackend` it is the running session's stream. Feed the
  stream into a `VtEngine`; send input back over the same channel.

### VT engine (`awp-vt`)
```rust
pub trait VtEngine {
    fn process(&mut self, bytes: &[u8]);
    fn resize(&mut self, cols: u16, rows: u16);
    fn screen(&self) -> Screen;      // cells: grapheme + fg/bg/attrs + cursor
}
```
- `Vt100Engine` (feature `vt100`, default): pure Rust, renders via `tui-term`.
- `LibghosttyEngine` (feature `libghostty`): FFI to `include/ghostty/vt.h`
  (write bytes, resize, read cells/cursor). Renders through a **custom ratatui
  widget** that maps `Screen` cells → ratatui `Buffer` cells (tui-term is
  vt100-specific, so libghostty needs its own thin render adapter — this is the
  main integration cost; budget for the C API's cell/attr readout maturity).
- Build: `libghostty` feature requires the Zig toolchain + a `build.rs` that
  links the prebuilt/`zig build`-produced lib; documented in `rust/README.md`.

### Render / TUI (`awp-tui`)
- ratatui immediate-mode; `Frame` split into sidebar (`Length(40)`) + panel.
- Panel = tab strip (`Length(1)`) over the active shell's `VtEngine` screen.
- **Flattened three-level model** (validated by the PoC):
  sidebar = project→workspace, tab strip = workspace→shells (from
  `SessionBackend::windows`), body = the live pane.
- **Concurrency**: PTY/stream ingestion runs off-thread, feeding `VtEngine`s
  behind locks; the render loop **coalesces** redraws to ≤60fps (never render
  per-byte) and only draws the active pane. Switching = draw a resident grid →
  sub-frame, no re-attach. This is the "max speed switching" requirement.
- **Focus/keymap** (from the PoC, Mac-safe — no Alt):
  - `Ctrl-a` toggle deck ↔ panel focus.
  - deck: `j/k` select, `Enter` open workspace, `/` filter, `q` quit.
  - panel: `Shift+←/→` switch shell tab; all other keys forwarded raw to the pane.
  - One keymap everywhere; no tmux prefix collisions.
- Design system: port awp's semantic palette (ANSI-16 tokens: Accent/Info/
  Success/Warning/Danger/Muted), the `┃ ` selection treatment, uniform panel
  padding. Keep it in one `theme` module (no raw color codes inline).

### Orchestration (`awp-agent`)
- `jj`, `gh`, hooks driven via subprocess (language-agnostic; same shape as Go).
- Hooks: install + **idempotent self-heal** on deck open; **no-op outside awp
  sessions**; `report-status` writes a store row. `--prompt-stdin` parses the
  Claude hook payload for the active prompt.

## Code quality & tooling (enforced)
- `rustfmt.toml` — standard, `imports_granularity = "Crate"`.
- `[workspace.lints.clippy] all = "deny"`, `[workspace.lints.rust] warnings =
  "deny"` — every crate inherits; CI runs `cargo clippy --all-targets
  --all-features -- -D warnings`.
- **Error policy**: no swallowed errors. `thiserror` for library error enums,
  `anyhow` at the binary edge. `let _ =` is a clippy-flagged exception requiring
  a justification comment. (Directly targets the Go version's 388 ignored errs.)
- `cargo-deny` (`deny.toml`): license allowlist, advisory DB, banned/duplicate
  deps. `cargo-audit` in CI.
- `cargo-nextest` for tests; `awp-core` reducer + `awp-store` migration are the
  unit-test priorities.
- `rust-toolchain.toml` pins the toolchain (reproducible builds).
- `tracing` for structured logs to `~/.awp/awp-rs.log` (replaces ad-hoc logging).
- CI (GitHub Actions or a `mise` task set): fmt-check → clippy -D → nextest →
  deny → audit. Green required before the branch graduates to root.

## Must-preserve behaviors (mined from README / CLAUDE.md / Go code)
The implementer MUST reproduce these; they are encoded edge-case knowledge.
- **Hooks**: report state on SessionStart(idle)/UserPromptSubmit+PreToolUse+
  PostToolUse(working)/Stop(idle)/PermissionRequest+Elicitation(waiting).
  `PreToolUse --waiting-when-tool AskUserQuestion` → waiting; PostToolUse → back
  to working. **Do NOT hook `Notification`** (fires on ~60s idle ping → false
  `waiting`); remove any stale awp-managed Notification hook on deck open.
- Hooks **self-heal**: idempotent install on deck open; only write on drift;
  schema-version bump triggers re-sync.
- Hooks/integrations are **no-ops outside awp tmux sessions**; honor `$AWP_BIN`.
- **Session naming**: `[awp]<repo>__<workspace>`.
- **Repo-root resolution**: follow `.jj/repo` pointer files (see Go
  `json_store.go`).
- **PR association**: `PRNumber` pins a workspace to a PR; accept legacy
  `PROverride` JSON field; zero = resolve via bulk PR-status cache.
- **Status states + colors**: idle/starting/working/waiting/exited/error/done
  mapped to the palette; virtual "to review"/"to check out" hint rows.
- **Scope cycling** (`P`): all / attention; flash new scope in status bar.
- **Pin groups** and **inbox buckets** (urgency-colored) carry over.
- **One renderer, no nested program**: never launch a second full-screen program
  inside the deck for UI; `Exec` only external commands ($EDITOR/git/pager).
  (Rust equiv: suspend ratatui for external commands; never nest a second TUI.)
- **Fast path**: no `jj`/`gh`/session queries on the switch/first-paint path;
  serve from the in-RAM roster (loaded from SQLite), enrich in the background.
- **tmux/zmx PATH**: document the non-interactive-shell PATH pitfall for popup
  launchers (the Go README's exit-127 note).
- **Cancellations** clear status silently (no "…: cancelled" noise).

## Implementation plan (for the next agent)
1. Scaffold `rust/` workspace: crates, `rust-toolchain.toml`, `rustfmt.toml`,
   `deny.toml`, workspace lints, CI. Empty crates compile clean.
2. `awp-core`: domain types + `AppState` + `reduce` + `Effect`/executor seam;
   unit tests for reducer transitions.
3. `awp-store`: schema + `rusqlite` access + **JSON migration** (+ `--dry-run`);
   tests against a copy of real `workspace-state.json` shapes.
4. `awp-vt`: `VtEngine` trait + `Vt100Engine` (tui-term render). Defer
   `LibghosttyEngine` behind its feature + `build.rs` (needs Zig).
5. `awp-session`: `SessionBackend` trait + `LocalBackend` (portable-pty).
   Defer `ZmxBackend` behind its feature (needs zmx running).
6. `awp-tui`: port the validated PoC UX onto the core/store/session/vt seams —
   sidebar → shell tabs → live pane, coalesced render, keymap, theme.
7. `awp-agent`: jj/gh/hooks + `report-status` → store; hook install/self-heal.
8. Enable `libghostty` + `zmx` features; wire `build.rs`/socket; validate on the
   maintainer's 25 live sessions.
9. Parity pass against the must-preserve checklist; fill remaining deck modals.

## Acceptance criteria
- [ ] `rust/` workspace builds; `cargo fmt --check`, `cargo clippy --all-targets
      --all-features -- -D warnings`, `cargo nextest run`, `cargo deny check`,
      `cargo audit` all pass in CI.
- [ ] `awp-core` has zero `ratatui`/`rusqlite` deps; reducer is unit-tested.
- [ ] First run migrates real `~/.awp/*.json` into `state.db` **non-destructively**;
      `awp migrate --dry-run` shows an accurate diff.
- [ ] Deck renders the real roster (projects → workspaces → shell tabs → live
      pane), flat, no popup; switching a workspace/tab is sub-frame with no
      re-attach.
- [ ] Concurrent `report-status` writes from multiple sessions never lose updates
      (multi-writer test against WAL).
- [ ] `libghostty` and `zmx` are feature-gated; default build needs no Zig.
- [ ] Every item in **must-preserve behaviors** is implemented or explicitly
      deferred with a tracked follow-up.

## QA / human review test plan
### Setup
- [ ] macOS/Linux with `cargo`, `jj`, `gh`, `tmux`, and (for full features) the
      Zig toolchain + `zmx` installed; real `~/.awp` present.
### Core happy path
- [ ] Launch deck; confirm real roster, statuses, PR/pin/scope.
- [ ] Open a workspace; confirm shell tabs match live `tmux`/`zmx` windows;
      `Shift+←/→` switches; input reaches the pane; `Ctrl-a` returns to deck.
- [ ] Stream a busy agent; confirm no jank and smooth switching to another.
### Edge cases & failure modes
- [ ] Dead/missing session → graceful tab fallback, no crash.
- [ ] Corrupt/partial JSON on migrate → clear error, JSON left intact.
- [ ] Two processes writing status concurrently → last-writer-per-field correct,
      no whole-record clobber.
- [ ] Zig toolchain absent → default (vt100/local) build still works.
### Regression / knowledge
- [ ] Walk the must-preserve checklist item by item.
- [ ] Confirm no `Notification`-hook false `waiting`; self-heal on deck open.

## Validation
- [ ] `cargo fmt --check`
- [ ] `cargo clippy --all-targets --all-features -- -D warnings`
- [ ] `cargo nextest run`
- [ ] `cargo deny check` / `cargo audit`
- [ ] `cargo build --release --features "zmx,libghostty"` (full stack, with Zig)

## Spec change log
- 2026-07-09: Initial draft. Stack, crate architecture, SQLite store + migration,
  session/VT trait boundaries, quality tooling, must-preserve checklist, phased
  plan. Decisions locked: rust/ subdir on branch; side-by-side migrate-on-first-
  run; pragmatic+enforced linting. Zig-dependency and no-inherited-tests risks
  accepted and mitigated via trait boundaries + pure-Rust default impls.
- 2026-07-09: **Implementation landed** (`rust/` workspace, all six crates).
  Three maintainer directions during the build refined the plan:
  1. **Deck-only binary — no CLI.** The `awp` binary launches the deck and
     nothing else; there is no `migrate` / `hooks` / `--help` command surface.
     First-run migration and idempotent hook self-heal run automatically on deck
     open. The single machine-facing exception is the invisible `report-status`
     hook callback (the installed Claude hooks shell out to it), which the binary
     honors and exits — plumbing, not a CLI. The migrate/hooks/report-status
     *logic* remains fully implemented + unit-tested in `awp-store` / `awp-agent`.
  2. **libghostty is the only VT engine.** There is no vt100-vs-libghostty
     choice: `awp-vt` exposes exactly one engine, `LibghosttyEngine`. It drives
     the native libghostty C ABI when linked (`AWP_LIBGHOSTTY_LIB` → build.rs
     sets `awp_libghostty_native`) for peak performance, and otherwise runs on an
     embedded VT core (the `vt100` crate, now an internal implementation detail —
     not a public engine). The default build still needs no Zig. This resolves
     the open question below by making native a *link-time* selection behind an
     identical surface rather than a shipped-on-vt100 fallback engine.
  3. Consequent to (2), the `awp-vt` `vt100-engine`/`libghostty` Cargo features
     were removed; `vt100` is a non-optional internal dep. `awp-tui` keeps only
     the `zmx` feature.
  Status of the first pass: 71 unit tests green; fmt/clippy/build clean.
- 2026-07-09: **Native libghostty + persistence landed** (second pass; maintainer
  directions "libghostty must be linked, only libghostty" and "don't lose on
  disconnect"):
  1. **Real libghostty, linked.** `awp-vt` now depends on the `libghostty-vt`
     crate (`uzaaft/libghostty-rs`), whose `libghostty-vt-sys` compiles the
     genuine Ghostty terminal core from source via Zig. Bytes flow into a real
     `ghostty::Terminal`; the screen is read back through Ghostty's render-state
     row/cell iterators (resolved RGB + styles) into the ratatui-free `Screen`.
     The hand-written FFI stub and the vt100 fallback are **deleted** —
     `LibghosttyEngine::is_native()` is `true`. Consequence: the build now
     **requires the Zig toolchain** (0.15.x); the "default build needs no Zig"
     goal is explicitly retired per the maintainer's "libghostty required"
     direction. CI installs Zig.
  2. **Persistence via headless tmux, not zmx.** libghostty is only the
     emulator; session/PTY management is ours. `TmuxBackend` holds sessions on a
     dedicated, **invisible** tmux server (`-L awp`, status off, prefix None) so
     they survive deck exit / disconnect, and `attach` runs `tmux attach` inside
     our own PTY streamed into libghostty — tmux's UI is never shown. zmx is
     dropped (not buildable here); the `SessionBackend` trait leaves the door
     open to it. `LocalBackend` (portable-pty) remains for tests.
  3. Binary is deck-only (plus the invisible `report-status` hook callback); no
     Cargo feature flags remain.
  Status: **75 tests green** including a tmux-persistence test and an end-to-end
  test (tmux → PTY → real libghostty → rendered `Screen`, live shell output
  verified); `cargo fmt --check`, `clippy --all-targets -D warnings`, and
  `cargo build --workspace` all pass. Deferred follow-ups: `gh` PR/CI background
  enrichment, report-status badge-suppression (is-viewing) query, shell-tab
  switching wired to tmux windows.

## Open questions / follow-ups
- ~~Confirm the `libghostty-vt` C API exposes full styled-cell readout~~ →
  **Resolved**: linked the real libghostty via the `libghostty-vt` crate and
  read styled cells through Ghostty's render-state row/cell iterators. Not a
  stub. The trade-off accepted per maintainer direction: the build now requires
  the Zig toolchain.
- ~~zmx session backend~~ → **Dropped**. libghostty is only the emulator;
  persistence is provided by a headless, invisible `TmuxBackend` behind the
  `SessionBackend` trait (survives disconnect). zmx could still slot in later.
- Decide when `rust/` graduates to repo root and the Go tree is retired.
- Optional: unix-socket writer→deck ping to eliminate `data_version` poll latency.
- Wire the remaining deferred pieces: `gh` PR/CI background enrichment, the
  report-status badge-suppression (is-viewing) query, and shell-tab switching
  bound to tmux windows.
