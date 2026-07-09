# awp — Rust rewrite

A ground-up Rust rebuild of `awp` (Agentic Workspace Pilot) whose **deck is the
primary surface**: a flattened multi-panel UI — project/workspace sidebar →
per-workspace shell tabs → live agent terminal — composited in one process,
instead of a Bubble Tea popup layered over tmux.

Spec: [`../specs/20260709-vn9m-rust-rewrite-ratatui-libghostty-zmx-spec.md`](../specs/20260709-vn9m-rust-rewrite-ratatui-libghostty-zmx-spec.md).

## Workspace layout

Dependency direction is strict and enforced: **UI depends on core; core never
depends on UI.**

```
rust/
  crates/
    awp-core/     domain types, AppState, the single reducer. No I/O, no UI deps.
    awp-store/    SQLite (WAL) persistence + non-destructive JSON migration.
    awp-session/  SessionBackend trait: portable-pty local impl + headless tmux.
    awp-vt/       the real libghostty VT engine + a ratatui-free Screen.
    awp-agent/    jj/gh subprocess glue, Claude hook install/self-heal, report-status.
    awp-tui/      the ratatui deck binary (`awp`).
```

`awp-core` compiles with zero I/O and zero UI deps, so the whole domain — the
reducer especially — is unit-testable without a terminal, a database, or a PTY.
The reducer is the *only* place `AppState` mutates; every user key, store
notification, and backend event flows through `reduce(&mut state, event) ->
Vec<Effect>`. That kills the race class the Go version had from scattered
goroutine writes + whole-file JSON IPC.

## Building

The VT engine links the **real** libghostty (Ghostty's terminal core) via the
`libghostty-vt` crate, whose `libghostty-vt-sys` builds the native library from
Ghostty source with Zig. So the build needs the **Zig toolchain** (0.15.x) on
`PATH`; the first build clones Ghostty and compiles the vt library (a few
minutes, cached afterwards). `tmux` is needed at runtime (and for the session
backend tests).

```
# one-time: install Zig 0.15.x (https://ziglang.org/download) and tmux
cargo build -p awp-tui         # builds the native libghostty-vt too
cargo run  -p awp-tui          # launch the deck (needs a terminal)
```

The binary is **deck-only** — running it launches the deck; there is no CLI
subcommand surface. What the Go CLI exposed as separate commands now happens
inside the deck lifecycle:

- **First-run migration** — on open, if `~/.awp/state.db` has no migration
  recorded, the deck imports `~/.awp/workspace-state.json`, `pin-groups.json`,
  and `pr-status-cache.json` into SQLite **non-destructively** (the JSON is left
  intact, so Go `awp` and the Rust build can run side by side during the
  transition).
- **Hook self-heal** — idempotent install of the Claude Code status hooks into
  `~/.claude/settings.json` on open; only writes on drift.

The one machine-facing exception is the invisible `report-status` hook callback:
the installed Claude hooks shell out to `awp report-status …`, so the binary
honors exactly that one invocation and exits. It is plumbing, not a CLI.

## VT engine: real libghostty

There is exactly one VT engine — **libghostty** — and it is the genuine Ghostty
terminal core, linked natively via the `libghostty-vt` crate. Bytes go into a
real `ghostty` terminal (`Terminal::vt_write`); the screen is read back through
Ghostty's render state (row/cell iterators, resolved fg/bg colors + styles) and
projected onto a ratatui-free `Screen`. A custom ratatui widget renders that
`Screen`, so the deck never couples to the engine internals.

`LibghosttyEngine::is_native()` returns `true`: this is not a shim or a
fallback. The native library is compiled from Ghostty source at build time (see
**Building**).

## Session persistence: headless tmux

libghostty is only the emulator — it does not manage sessions or PTYs. That is
the `SessionBackend`'s job:

- **`TmuxBackend`** (the deck's default) holds sessions on a **dedicated,
  headless, invisible** tmux server (`tmux -L awp`, status bar off, no prefix
  key). Sessions **survive deck exit / SSH disconnect** — the persistence
  requirement. `attach` runs `tmux attach-session` inside our *own* PTY and
  streams that into libghostty, so tmux's UI is never shown; the user only sees
  the libghostty-rendered pane.
- **`LocalBackend`** (portable-pty) is the non-persistent backend used by tests.

Both implement `SessionBackend`, so a different persistence layer (e.g. zmx)
could slot in without touching the deck.

## Quality gates (CI)

```
cargo fmt --all --check
cargo clippy --all-targets -- -D warnings
cargo nextest run          # or: cargo test --workspace
cargo deny check
cargo audit
```

(All need Zig on `PATH` and `tmux` installed, since a rebuild links libghostty
and the session-backend tests drive a real headless tmux server.)

Lints are workspace-wide and denied (`clippy::all`, `warnings`). Errors use
`thiserror` in libraries and `anyhow` at the binary edge — no swallowed errors.
Structured logs go to `~/.awp/awp-rs.log` (`AWP_LOG` controls the filter).

## Deck keys

| Key | Action |
|-----|--------|
| `j` / `k` or ↓ / ↑ | move selection |
| `gg` / `G` | jump to top / bottom |
| `Enter` / `a` | open (attach) the selected workspace's agent pane |
| `L` | jump to the last-opened session |
| `s` | shell window |
| `e` | editor window (`$EDITOR`) |
| `c` / `C` | review window (`tuicr -r @` / `tuicr -r main..@`) |
| `v` | vcs window (`jjui`) |
| `i` | ci window (`gh run watch`) |
| `n` | new workspace (form: name / bookmark / prompt) |
| `R` / `D` | rename / delete workspace |
| `p` | PR menu — `o` open · `m` merge · `d` description · `s` set number |
| `B` | link a bookmark to the workspace |
| `A` | send a typed prompt to the agent |
| `m` then `m` / `a`–`z` / `D` | pin to default / register / unpin |
| `f` | find (easymotion hint jump) |
| `/` | filter (name / bookmark / prompt) |
| `P` | cycle scope (all → attention → inbox) |
| `?` | help overlay |
| `Ctrl-a` | toggle focus between deck and live pane |
| `q` / `Ctrl-c` | quit |
| in pane | keys forwarded raw to the shell |

The window commands (`e/s/c/v/i/a`) open a named window in the workspace's
headless tmux session and switch the live pane to it. Workspace lifecycle
(`n/R/D`) drives `jj` (`workspace add` / `rename` / `forget`); PR actions drive
`gh`. Remaining Go-deck follow-ups: the `J` jobs overlay, the `d` dev-server URL
capture, the `x` user-actions menu, and `,` edit-state.
