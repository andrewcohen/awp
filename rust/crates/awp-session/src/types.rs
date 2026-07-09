//! Value types shared across session backends.

use std::collections::BTreeMap;

/// A backend-specific session handle (e.g. the `[awp]repo__ws` name).
#[derive(Debug, Clone, PartialEq, Eq, Hash)]
pub struct SessionId(pub String);

impl std::fmt::Display for SessionId {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(&self.0)
    }
}

/// A window (shell tab) handle within a session.
#[derive(Debug, Clone, PartialEq, Eq, Hash)]
pub struct WindowId(pub String);

impl std::fmt::Display for WindowId {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(&self.0)
    }
}

/// How to spawn a session's shells.
#[derive(Debug, Clone)]
pub struct SessionSpec {
    /// The session name (`[awp]repo__ws`).
    pub name: String,
    /// Working directory for the shells.
    pub cwd: String,
    /// Shell program; falls back to `$SHELL` then `/bin/sh` when empty.
    pub shell: Option<String>,
    /// Explicit command + args to run instead of an interactive shell. `None`
    /// spawns the login shell (the normal case); `Some` is used for tests and
    /// one-shot windows.
    pub command: Option<Vec<String>>,
    /// Extra environment (e.g. `AWP_WORKSPACE`, `AWP_REPO`, `AWP_REPO_ROOT`).
    pub env: BTreeMap<String, String>,
    pub cols: u16,
    pub rows: u16,
}

impl SessionSpec {
    pub fn new(name: impl Into<String>, cwd: impl Into<String>) -> Self {
        Self {
            name: name.into(),
            cwd: cwd.into(),
            shell: None,
            command: None,
            env: BTreeMap::new(),
            cols: 80,
            rows: 24,
        }
    }
}

/// Metadata for a live session.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SessionInfo {
    pub id: SessionId,
    pub name: String,
    pub windows: Vec<Window>,
}

/// One shell tab.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Window {
    pub id: WindowId,
    /// Display label for the tab strip (e.g. "shell", "agent").
    pub title: String,
}
