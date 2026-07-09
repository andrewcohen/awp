//! `LocalBackend` — portable-pty sessions with no persistence.
//!
//! The dev/parity default: builds and runs with zero Zig toolchain. A session
//! is a registered spec; `attach` spawns the PTY child and streams its output
//! over a channel, so the deck's live-pane mirroring works end-to-end without
//! zmx. (Persistence across deck exit is the `ZmxBackend`'s job.)

use crate::types::{SessionId, SessionInfo, SessionSpec, Window, WindowId};
use crate::{SessionBackend, SessionError};
use awp_core::WorkspaceId;
use portable_pty::{native_pty_system, CommandBuilder, PtySize};
use std::collections::HashMap;
use std::io::{Read, Write};
use std::sync::mpsc::{channel, Receiver};
use std::sync::Mutex;
use std::thread;

/// The single shell tab id every local session exposes. Multi-window is a
/// zmx/tmux capability; local sessions have one shell.
const DEFAULT_WINDOW: &str = "shell";

/// A live, attached window: a byte stream to read, an input sink, resize + kill.
pub struct Attached {
    output: Receiver<Vec<u8>>,
    writer: Box<dyn Write + Send>,
    master: Box<dyn portable_pty::MasterPty + Send>,
    child: Box<dyn portable_pty::Child + Send + Sync>,
}

impl Attached {
    /// The receiver carrying PTY output chunks. The render loop drains this
    /// off-thread and feeds a `VtEngine`.
    pub fn output(&self) -> &Receiver<Vec<u8>> {
        &self.output
    }

    /// Send input bytes to the shell.
    pub fn write_input(&mut self, data: &[u8]) -> crate::Result<()> {
        self.writer.write_all(data)?;
        self.writer.flush()?;
        Ok(())
    }

    /// Resize the PTY (cols, then rows — matching `VtEngine::resize`).
    pub fn resize(&mut self, cols: u16, rows: u16) -> crate::Result<()> {
        self.master
            .resize(PtySize {
                rows: rows.max(1),
                cols: cols.max(1),
                pixel_width: 0,
                pixel_height: 0,
            })
            .map_err(|e| SessionError::Spawn(e.to_string()))
    }

    /// Terminate the shell.
    pub fn kill(&mut self) -> crate::Result<()> {
        self.child.kill()?;
        Ok(())
    }
}

impl Drop for Attached {
    fn drop(&mut self) {
        // Best-effort teardown; a gone child is not an error worth surfacing.
        let _ = self.child.kill();
    }
}

/// A non-persistent, in-process session registry.
#[derive(Default)]
pub struct LocalBackend {
    sessions: Mutex<HashMap<String, SessionSpec>>,
}

impl LocalBackend {
    pub fn new() -> Self {
        Self::default()
    }

    fn info_for(name: &str) -> SessionInfo {
        SessionInfo {
            id: SessionId(name.to_string()),
            name: name.to_string(),
            windows: vec![Window {
                id: WindowId(DEFAULT_WINDOW.to_string()),
                title: DEFAULT_WINDOW.to_string(),
            }],
        }
    }
}

impl SessionBackend for LocalBackend {
    fn ensure(&self, _id: &WorkspaceId, spec: &SessionSpec) -> crate::Result<SessionInfo> {
        let mut sessions = self.sessions.lock().expect("session registry poisoned");
        sessions
            .entry(spec.name.clone())
            .or_insert_with(|| spec.clone());
        Ok(Self::info_for(&spec.name))
    }

    fn windows(&self, id: &SessionId) -> crate::Result<Vec<Window>> {
        let sessions = self.sessions.lock().expect("session registry poisoned");
        if !sessions.contains_key(&id.0) {
            return Err(SessionError::NoSession(id.0.clone()));
        }
        Ok(Self::info_for(&id.0).windows)
    }

    fn attach(&self, id: &SessionId, win: &WindowId) -> crate::Result<Attached> {
        let spec = {
            let sessions = self.sessions.lock().expect("session registry poisoned");
            sessions
                .get(&id.0)
                .cloned()
                .ok_or_else(|| SessionError::NoSession(id.0.clone()))?
        };
        if win.0 != DEFAULT_WINDOW {
            return Err(SessionError::NoWindow(win.0.clone()));
        }
        spawn(&spec)
    }

    fn list(&self) -> crate::Result<Vec<SessionInfo>> {
        let sessions = self.sessions.lock().expect("session registry poisoned");
        Ok(sessions.keys().map(|n| Self::info_for(n)).collect())
    }

    fn kill(&self, id: &SessionId) -> crate::Result<()> {
        self.sessions
            .lock()
            .expect("session registry poisoned")
            .remove(&id.0);
        Ok(())
    }
}

/// Spawn the PTY child for a spec and wire up the reader thread. Shared with
/// the tmux backend, which runs a `tmux attach` client through the same PTY
/// machinery.
pub(crate) fn spawn(spec: &SessionSpec) -> crate::Result<Attached> {
    let pty = native_pty_system();
    let pair = pty
        .openpty(PtySize {
            rows: spec.rows.max(1),
            cols: spec.cols.max(1),
            pixel_width: 0,
            pixel_height: 0,
        })
        .map_err(|e| SessionError::Spawn(e.to_string()))?;

    let mut cmd = build_command(spec);
    cmd.cwd(&spec.cwd);
    for (k, v) in &spec.env {
        cmd.env(k, v);
    }

    let child = pair
        .slave
        .spawn_command(cmd)
        .map_err(|e| SessionError::Spawn(e.to_string()))?;
    // Drop the slave handle so the child owns the only slave fd; otherwise the
    // reader never sees EOF when the shell exits.
    drop(pair.slave);

    let mut reader = pair
        .master
        .try_clone_reader()
        .map_err(|e| SessionError::Spawn(e.to_string()))?;
    let writer = pair
        .master
        .take_writer()
        .map_err(|e| SessionError::Spawn(e.to_string()))?;

    let (tx, rx) = channel::<Vec<u8>>();
    thread::spawn(move || {
        let mut buf = [0u8; 8192];
        loop {
            match reader.read(&mut buf) {
                Ok(0) => break,
                Ok(n) => {
                    if tx.send(buf[..n].to_vec()).is_err() {
                        break; // receiver dropped (pane closed)
                    }
                }
                Err(_) => break,
            }
        }
    });

    Ok(Attached {
        output: rx,
        writer,
        master: pair.master,
        child,
    })
}

fn build_command(spec: &SessionSpec) -> CommandBuilder {
    if let Some(cmd) = &spec.command {
        if let Some((prog, args)) = cmd.split_first() {
            let mut builder = CommandBuilder::new(prog);
            builder.args(args);
            return builder;
        }
    }
    let shell = spec
        .shell
        .clone()
        .or_else(|| std::env::var("SHELL").ok())
        .unwrap_or_else(|| "/bin/sh".to_string());
    CommandBuilder::new(shell)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::time::{Duration, Instant};

    fn drain_until(att: &Attached, needle: &str, timeout: Duration) -> String {
        let start = Instant::now();
        let mut acc = String::new();
        while start.elapsed() < timeout {
            if let Ok(chunk) = att.output().recv_timeout(Duration::from_millis(100)) {
                acc.push_str(&String::from_utf8_lossy(&chunk));
                if acc.contains(needle) {
                    break;
                }
            }
        }
        acc
    }

    #[test]
    fn ensure_windows_attach_streams_output() {
        let backend = LocalBackend::new();
        let id = WorkspaceId::new("/repo", "ws");
        let mut spec = SessionSpec::new(crate::session_name("repo", "ws"), "/");
        spec.command = Some(vec![
            "/bin/sh".into(),
            "-c".into(),
            "printf AWP_READY".into(),
        ]);
        let info = backend.ensure(&id, &spec).unwrap();
        assert_eq!(info.name, "[awp]repo__ws");
        assert_eq!(info.windows.len(), 1);

        let sid = SessionId(spec.name.clone());
        let windows = backend.windows(&sid).unwrap();
        assert_eq!(windows[0].id.0, "shell");

        let att = backend.attach(&sid, &windows[0].id).unwrap();
        let out = drain_until(&att, "AWP_READY", Duration::from_secs(5));
        assert!(out.contains("AWP_READY"), "got: {out:?}");
    }

    #[test]
    fn attach_unknown_session_errors() {
        let backend = LocalBackend::new();
        match backend.attach(&SessionId("nope".into()), &WindowId("shell".into())) {
            Err(SessionError::NoSession(_)) => {}
            other => panic!("expected NoSession, got {:?}", other.err()),
        }
    }

    #[test]
    fn write_input_reaches_shell() {
        let backend = LocalBackend::new();
        let id = WorkspaceId::new("/repo", "ws");
        let mut spec = SessionSpec::new(crate::session_name("repo", "ws"), "/");
        // `cat` echoes its stdin back to stdout.
        spec.command = Some(vec!["/bin/cat".into()]);
        backend.ensure(&id, &spec).unwrap();
        let sid = SessionId(spec.name.clone());
        let mut att = backend.attach(&sid, &WindowId("shell".into())).unwrap();
        att.write_input(b"ping-me\n").unwrap();
        let out = drain_until(&att, "ping-me", Duration::from_secs(5));
        assert!(out.contains("ping-me"), "got: {out:?}");
    }
}
