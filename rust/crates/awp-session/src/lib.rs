//! `awp-session` — the session backend boundary.
//!
//! A `SessionBackend` owns the *live* shells behind each workspace: the deck's
//! flattened middle level (shell tabs) and its body (the live pane). The trait
//! lets the persistence strategy swap without touching the deck — a
//! `LocalBackend` (portable-pty, no persistence, the dev/parity default) and a
//! feature-gated `ZmxBackend` (persistent, multi-client) implement the same
//! contract.

mod types;

pub use types::{SessionId, SessionInfo, SessionSpec, Window, WindowId};

use awp_core::WorkspaceId;

/// Errors from a session backend.
#[derive(Debug, thiserror::Error)]
pub enum SessionError {
    #[error("session backend io: {0}")]
    Io(#[from] std::io::Error),
    #[error("no such session: {0}")]
    NoSession(String),
    #[error("no such window: {0}")]
    NoWindow(String),
    #[error("spawn session: {0}")]
    Spawn(String),
}

pub type Result<T> = std::result::Result<T, SessionError>;

/// The session naming convention, preserved from the Go deck:
/// `[awp]<repo>__<workspace>`.
pub fn session_name(repo: &str, workspace: &str) -> String {
    format!("[awp]{repo}__{workspace}")
}

/// Backend contract. `ensure` is idempotent (create-or-find); `attach` returns
/// a live byte stream from the *existing* PTY plus input + resize handles.
pub trait SessionBackend {
    /// Create the session for a workspace if absent; return its info either way.
    fn ensure(&self, id: &WorkspaceId, spec: &SessionSpec) -> Result<SessionInfo>;
    /// The shell tabs (windows) of a session.
    fn windows(&self, id: &SessionId) -> Result<Vec<Window>>;
    /// Attach to a window: a stream to read, a handle to write, a way to resize.
    fn attach(&self, id: &SessionId, win: &WindowId) -> Result<Attached>;
    /// All known sessions.
    fn list(&self) -> Result<Vec<SessionInfo>>;
    /// Kill a session and its shells.
    fn kill(&self, id: &SessionId) -> Result<()>;
}

mod local;
pub use local::{Attached, LocalBackend};

mod tmux;
pub use tmux::TmuxBackend;
