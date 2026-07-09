//! `awp` — the deck. Running the binary launches the flattened deck: there is
//! no user-facing CLI. The deck owns the sessions it renders, so what the Go
//! CLI exposed as separate commands now happens *inside* the deck lifecycle —
//! first-run JSON migration and idempotent hook self-heal run automatically on
//! open.
//!
//! The single exception is the invisible `report-status` hook callback: the
//! Claude hooks awp installs shell out to `awp report-status …`, so the binary
//! honors exactly that one machine-facing invocation and exits. It is plumbing,
//! not a CLI — there are no other subcommands, no `--help`, no `--version`.
//!
//! The VT engine is the real libghostty (Ghostty's terminal core, linked via
//! the `libghostty-vt` crate). Sessions persist on a headless, invisible tmux
//! server; the deck renders every pane itself, so tmux is never seen.

mod app;
mod pane;
mod theme;

use anyhow::{Context, Result};
use app::App;
use awp_session::SessionBackend;
use awp_store::Store;
use std::io::{IsTerminal, Write};
use std::process::ExitCode;

fn main() -> ExitCode {
    init_logging();
    let args: Vec<String> = std::env::args().skip(1).collect();
    // The one machine-facing hook callback (see module docs). Everything else
    // launches the deck.
    let result = match args.first().map(String::as_str) {
        Some("report-status") => run_report_status(&args[1..]),
        // Legacy Go invocation shape, kept so hooks written before the rewrite
        // still resolve.
        Some("internal") if args.get(1).map(String::as_str) == Some("report-status") => {
            run_report_status(&args[2..])
        }
        _ => run_deck(),
    };
    match result {
        Ok(()) => ExitCode::SUCCESS,
        Err(err) => {
            let _ = writeln!(std::io::stderr(), "awp: {err:#}");
            ExitCode::FAILURE
        }
    }
}

/// The `report-status` hook callback: write the agent's status to the store.
/// Silent no-op outside an awp session (missing identity), so a misfired hook
/// never disrupts an agent turn.
fn run_report_status(args: &[String]) -> Result<()> {
    let parsed = awp_agent::report_status::parse_args(args)?;
    let store = Store::open_default().context("open state.db")?;
    let ident = awp_agent::report_status::Ident::from_env();
    let mut stdin = std::io::stdin();
    // Badge-suppression (is the user viewing this session) is a follow-up;
    // default false so we badge rather than miss.
    awp_agent::report_status::run(&store, &parsed, &mut stdin, &ident, false)?;
    Ok(())
}

/// The session backend: a headless, invisible tmux server so agent sessions
/// persist across deck exit / SSH disconnect. tmux is never shown — the deck
/// renders every pane itself through libghostty.
fn make_backend() -> Box<dyn SessionBackend> {
    Box::new(awp_session::TmuxBackend::new())
}

fn run_deck() -> Result<()> {
    if !std::io::stdout().is_terminal() {
        anyhow::bail!("awp needs a terminal (stdout is not a tty)");
    }
    let store = Store::open_default().context("open state.db")?;
    // First-run migration is non-destructive and idempotent — the deck owns it,
    // there is no separate `migrate` command.
    if !store.is_migrated().unwrap_or(false) {
        if let Err(e) = store.migrate_default(false, false) {
            tracing::warn!(%e, "first-run migration failed; continuing with empty store");
        }
    }
    // Idempotent hook self-heal on deck open (best-effort).
    if let Err(e) = awp_agent::hooks::install_claude() {
        tracing::warn!(%e, "hook self-heal failed");
    }

    let mut app = App::new(store, make_backend())?;
    run_ui(&mut app)
}

fn run_ui(app: &mut App) -> Result<()> {
    use crossterm::event::{self, Event, KeyEventKind};
    use crossterm::terminal::{
        disable_raw_mode, enable_raw_mode, EnterAlternateScreen, LeaveAlternateScreen,
    };
    use ratatui::backend::CrosstermBackend;
    use ratatui::layout::Rect;
    use ratatui::Terminal;
    use std::time::Duration;

    enable_raw_mode().context("enable raw mode")?;
    let mut stdout = std::io::stdout();
    crossterm::execute!(stdout, EnterAlternateScreen).context("enter alt screen")?;
    let mut terminal = Terminal::new(CrosstermBackend::new(stdout)).context("init terminal")?;

    let result = (|| -> Result<()> {
        loop {
            if app.should_quit() {
                break;
            }
            let size = terminal.size()?;
            app.sync_size(Rect::new(0, 0, size.width, size.height));
            app.pump_pane();
            terminal.draw(|f| app.draw(f))?;
            app.poll_store();
            // ~60fps coalescing: block up to 16ms for input, else loop to pump
            // the pane and redraw. Never render per-byte.
            if event::poll(Duration::from_millis(16))? {
                match event::read()? {
                    Event::Key(key) if key.kind == KeyEventKind::Press => app.on_key(key),
                    _ => {}
                }
            }
        }
        Ok(())
    })();

    // Always restore the terminal, even on error.
    disable_raw_mode().ok();
    crossterm::execute!(terminal.backend_mut(), LeaveAlternateScreen).ok();
    terminal.show_cursor().ok();
    result
}

/// Structured logging to `~/.awp/awp-rs.log` (best-effort; failures are silent
/// so logging never breaks the deck).
fn init_logging() {
    let Some(home) = std::env::var_os("HOME") else {
        return;
    };
    let dir = std::path::PathBuf::from(home).join(".awp");
    if std::fs::create_dir_all(&dir).is_err() {
        return;
    }
    let Ok(file) = std::fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(dir.join("awp-rs.log"))
    else {
        return;
    };
    use tracing_subscriber::EnvFilter;
    let _ = tracing_subscriber::fmt()
        .with_env_filter(
            EnvFilter::try_from_env("AWP_LOG").unwrap_or_else(|_| EnvFilter::new("info")),
        )
        .with_writer(file)
        .with_ansi(false)
        .try_init();
}
