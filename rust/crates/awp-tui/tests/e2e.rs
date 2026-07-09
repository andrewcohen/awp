//! End-to-end integration: a persistent tmux session, attached through our own
//! PTY, streamed into the real libghostty engine, rendered to a `Screen`.
//!
//! This exercises the whole live-pane pipeline the deck depends on. It needs a
//! real `tmux` binary and the native libghostty build; it skips gracefully when
//! tmux is unavailable and cleans up the session it creates.

use awp_session::{session_name, SessionBackend, SessionId, SessionSpec, TmuxBackend, WindowId};
use awp_vt::{LibghosttyEngine, VtEngine};
use std::process::Command;
use std::time::{Duration, Instant};

fn tmux_available() -> bool {
    Command::new("tmux")
        .arg("-V")
        .output()
        .map(|o| o.status.success())
        .unwrap_or(false)
}

#[test]
fn tmux_session_streams_through_libghostty_to_a_screen() {
    if !tmux_available() {
        eprintln!("skipping: tmux not available");
        return;
    }

    let backend = TmuxBackend::new();
    let name = session_name("repo", "e2e-libghostty");
    let sid = SessionId(name.clone());
    // Start clean.
    let _ = backend.kill(&sid);

    let mut spec = SessionSpec::new(name.clone(), "/tmp");
    spec.cols = 80;
    spec.rows = 24;
    let info = backend
        .ensure(&awp_core::WorkspaceId::new("/tmp/repo", "e2e"), &spec)
        .expect("ensure tmux session");
    let win = info
        .windows
        .first()
        .map(|w| w.id.clone())
        .unwrap_or(WindowId("0".into()));

    // Attach through our own PTY and drive the real libghostty engine.
    let mut attached = backend.attach(&sid, &win).expect("attach");
    let mut engine = LibghosttyEngine::new(24, 80);

    // Type a command into the live shell.
    attached
        .write_input(b"printf 'AWP_E2E_OK\\n'\n")
        .expect("write input");

    // Pump the byte stream into the engine until the marker renders (or time
    // out). tmux redraws the pane; libghostty parses it into the Screen.
    let start = Instant::now();
    let mut found = false;
    while start.elapsed() < Duration::from_secs(10) {
        let mut got_bytes = false;
        while let Ok(chunk) = attached.output().recv_timeout(Duration::from_millis(200)) {
            engine.process(&chunk);
            got_bytes = true;
        }
        let screen = engine.screen();
        let rendered: String = (0..screen.rows)
            .map(|r| screen.row_text(r))
            .collect::<Vec<_>>()
            .join("\n");
        if rendered.contains("AWP_E2E_OK") {
            found = true;
            break;
        }
        if !got_bytes && start.elapsed() > Duration::from_secs(2) {
            // Nudge a redraw in case tmux coalesced output.
            let _ = attached.write_input(b"");
        }
    }

    // Cleanup regardless of outcome.
    drop(attached);
    let _ = backend.kill(&sid);

    assert!(
        found,
        "expected the shell's output to render through libghostty"
    );
}
