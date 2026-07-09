//! `TmuxBackend` — persistent sessions on a **headless, invisible** tmux server.
//!
//! tmux is used purely as a session/PTY holder, never as a UI:
//!
//! - A dedicated server socket (`-L awp`) keeps it isolated from the user's own
//!   tmux and lets the sessions survive deck exit / SSH disconnect — the
//!   persistence the local backend lacks.
//! - The server is configured invisible: status bar off, no prefix key, so it
//!   never intercepts input or paints chrome.
//! - The deck renders everything itself: `attach` runs a `tmux attach-session`
//!   client **inside our own PTY** (reusing the local backend's PTY machinery)
//!   and feeds that byte stream to libghostty. The user only ever sees the
//!   libghostty-rendered pane; tmux is invisible plumbing.
//!
//! Dropping the attached client (closing the pane) leaves the tmux session
//! running, so reattaching later re-renders the live shell.

use crate::local::{spawn, Attached};
use crate::types::{SessionId, SessionInfo, SessionSpec, Window, WindowId};
use crate::{SessionBackend, SessionError};
use awp_core::WorkspaceId;
use std::process::Command;

/// Dedicated tmux server socket name, isolating awp's sessions from the user's.
const SOCKET: &str = "awp";

/// Persistent, headless tmux-backed sessions.
#[derive(Default)]
pub struct TmuxBackend {
    configured: std::sync::atomic::AtomicBool,
}

impl TmuxBackend {
    pub fn new() -> Self {
        Self::default()
    }

    /// Run a `tmux -L awp …` command, returning trimmed stdout.
    fn tmux(args: &[&str]) -> crate::Result<String> {
        let out = Command::new("tmux")
            .arg("-L")
            .arg(SOCKET)
            .args(args)
            .output()
            .map_err(SessionError::Io)?;
        if !out.status.success() {
            return Err(SessionError::Spawn(format!(
                "tmux {}: {}",
                args.join(" "),
                String::from_utf8_lossy(&out.stderr).trim()
            )));
        }
        Ok(String::from_utf8_lossy(&out.stdout).trim().to_string())
    }

    /// Whether a session exists (headless check).
    fn has_session(name: &str) -> bool {
        Command::new("tmux")
            .args(["-L", SOCKET, "has-session", "-t", &exact(name)])
            .output()
            .map(|o| o.status.success())
            .unwrap_or(false)
    }

    /// Apply the invisible-server configuration once per process.
    fn ensure_configured(&self) {
        use std::sync::atomic::Ordering;
        if self.configured.swap(true, Ordering::SeqCst) {
            return;
        }
        // Best-effort: make the server invisible — no status bar, no prefix key
        // to intercept input, no unattached-session teardown. Failures are
        // ignored (the server may still be starting from the first new-session).
        let _ = Self::tmux(&["set-option", "-g", "status", "off"]);
        let _ = Self::tmux(&["set-option", "-g", "prefix", "None"]);
        let _ = Self::tmux(&["set-option", "-g", "prefix2", "None"]);
        let _ = Self::tmux(&["set-option", "-g", "escape-time", "0"]);
        let _ = Self::tmux(&["set-option", "-g", "destroy-unattached", "off"]);
    }
}

impl SessionBackend for TmuxBackend {
    fn ensure(&self, _id: &WorkspaceId, spec: &SessionSpec) -> crate::Result<SessionInfo> {
        if !Self::has_session(&spec.name) {
            let cols = spec.cols.max(1).to_string();
            let rows = spec.rows.max(1).to_string();
            let mut args: Vec<String> = vec![
                "new-session".into(),
                "-d".into(),
                "-s".into(),
                spec.name.clone(),
                "-x".into(),
                cols,
                "-y".into(),
                rows,
            ];
            if !spec.cwd.is_empty() {
                args.push("-c".into());
                args.push(spec.cwd.clone());
            }
            // Inject workspace env into the session's shell (tmux 3.2+ `-e`).
            for (k, v) in &spec.env {
                args.push("-e".into());
                args.push(format!("{k}={v}"));
            }
            let arg_refs: Vec<&str> = args.iter().map(String::as_str).collect();
            Self::tmux(&arg_refs)?;
        }
        // Configure the (now-running) server to be invisible.
        self.ensure_configured();
        Ok(SessionInfo {
            id: SessionId(spec.name.clone()),
            name: spec.name.clone(),
            windows: self.windows(&SessionId(spec.name.clone()))?,
        })
    }

    fn windows(&self, id: &SessionId) -> crate::Result<Vec<Window>> {
        if !Self::has_session(&id.0) {
            return Err(SessionError::NoSession(id.0.clone()));
        }
        // tmux sanitizes control chars (incl. tab) in -F output, so the
        // delimiter is a space. window_index is numeric, so the first space
        // unambiguously separates it from the (possibly space-containing) name.
        let out = Self::tmux(&[
            "list-windows",
            "-t",
            &exact(&id.0),
            "-F",
            "#{window_index} #{window_name}",
        ])?;
        let windows: Vec<Window> = out
            .lines()
            .filter_map(|line| {
                let line = line.trim();
                let (idx, name) = line.split_once(' ').unwrap_or((line, ""));
                if idx.is_empty() {
                    return None;
                }
                Some(Window {
                    id: WindowId(idx.to_string()),
                    title: name.trim().to_string(),
                })
            })
            .collect();
        Ok(windows)
    }

    fn attach(&self, id: &SessionId, win: &WindowId) -> crate::Result<Attached> {
        if !Self::has_session(&id.0) {
            return Err(SessionError::NoSession(id.0.clone()));
        }
        // Run a tmux attach client inside our own PTY. Selecting the target
        // window first ensures the requested shell tab is active. Keys typed in
        // the deck's pane are forwarded raw into this PTY → tmux → the shell.
        let target = format!("{}:{}", exact(&id.0), win.0);
        let mut spec = SessionSpec::new(id.0.clone(), String::new());
        spec.command = Some(vec![
            "tmux".into(),
            "-L".into(),
            SOCKET.into(),
            "attach-session".into(),
            "-t".into(),
            target,
        ]);
        // A capable TERM so tmux emits rich sequences for libghostty to parse.
        spec.env.insert("TERM".into(), "xterm-256color".into());
        spawn(&spec)
    }

    fn list(&self) -> crate::Result<Vec<SessionInfo>> {
        // No server yet → no sessions (not an error).
        let out = match Self::tmux(&["list-sessions", "-F", "#{session_name}"]) {
            Ok(o) => o,
            Err(_) => return Ok(Vec::new()),
        };
        let mut infos = Vec::new();
        for name in out.lines().map(str::trim).filter(|s| !s.is_empty()) {
            let windows = self
                .windows(&SessionId(name.to_string()))
                .unwrap_or_default();
            infos.push(SessionInfo {
                id: SessionId(name.to_string()),
                name: name.to_string(),
                windows,
            });
        }
        Ok(infos)
    }

    fn kill(&self, id: &SessionId) -> crate::Result<()> {
        if !Self::has_session(&id.0) {
            return Ok(());
        }
        Self::tmux(&["kill-session", "-t", &exact(&id.0)]).map(|_| ())
    }
}

/// Force exact session-name matching (`=name`), so the `[awp]…` bracket prefix
/// is never treated as a tmux target pattern.
fn exact(name: &str) -> String {
    format!("={name}")
}

#[cfg(test)]
mod tests {
    use super::*;

    // These tests need a real tmux binary. They use a throwaway session on the
    // dedicated `awp` socket and clean up after themselves. Skipped gracefully
    // when tmux is unavailable.
    fn tmux_available() -> bool {
        Command::new("tmux")
            .arg("-V")
            .output()
            .map(|o| o.status.success())
            .unwrap_or(false)
    }

    #[test]
    fn ensure_creates_persistent_session_and_kill_removes_it() {
        if !tmux_available() {
            eprintln!("skipping: tmux not available");
            return;
        }
        let backend = TmuxBackend::new();
        let id = WorkspaceId::new("/tmp/repo", "tmuxtest");
        let name = crate::session_name("repo", "tmuxtest-awp-unit");
        let mut spec = SessionSpec::new(name.clone(), "/tmp");
        spec.cols = 80;
        spec.rows = 24;
        let info = backend.ensure(&id, &spec).unwrap();
        assert_eq!(info.name, name);
        assert!(!info.windows.is_empty(), "session should have a window");
        // Persists independently of any attached client.
        assert!(TmuxBackend::has_session(&name));
        // Idempotent.
        backend.ensure(&id, &spec).unwrap();
        // Cleanup.
        backend.kill(&SessionId(name.clone())).unwrap();
        assert!(!TmuxBackend::has_session(&name));
    }
}
