//! Core domain types: workspaces, projects, PR refs, pin groups, scope.
//!
//! These are the UI-agnostic source of truth. The store hydrates them from
//! SQLite; the reducer mutates them; the TUI projects them. None of them carry
//! rendering concerns.

use crate::status::Status;
use serde::{Deserialize, Serialize};

/// Stable identity of a workspace: the (canonical repo root, name) pair. This
/// is the primary key in both the SQLite store and the in-RAM roster, matching
/// the Go `workspace-state.json` shape of `repo_root -> name -> Entry`.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub struct WorkspaceId {
    pub repo_root: String,
    pub name: String,
}

impl WorkspaceId {
    pub fn new(repo_root: impl Into<String>, name: impl Into<String>) -> Self {
        Self {
            repo_root: repo_root.into(),
            name: name.into(),
        }
    }
}

/// One managed agent workspace. Fields mirror the Go `workspace.Entry` so the
/// JSON migration is a straight field map.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, Default)]
pub struct Workspace {
    pub repo_root: String,
    pub name: String,
    pub path: String,
    #[serde(default)]
    pub bookmark: Option<String>,
    /// Pins the workspace to a specific PR. `None`/0 means resolve via the bulk
    /// PR-status cache. Honors the legacy `PROverride` JSON alias on import.
    #[serde(default)]
    pub pr_number: Option<u64>,
    #[serde(default)]
    pub session_id: Option<String>,
    #[serde(default)]
    pub session_name: Option<String>,
    #[serde(default)]
    pub agent_window: Option<String>,
    #[serde(default)]
    pub agent_pane: Option<String>,
    #[serde(default)]
    pub active_prompt: Option<String>,
    #[serde(default)]
    pub status: Status,
    /// Set when the agent transitions to an attention state and the user isn't
    /// looking; cleared when summoned or the agent exits.
    #[serde(default)]
    pub unread: bool,
    /// Register key that floats this workspace to a pinned section at the top of
    /// the deck. `None` = unpinned; `"default"` is the `gg`-bound register;
    /// otherwise a single lowercase letter `a`–`z`.
    #[serde(default)]
    pub pin_group: Option<String>,
}

impl Workspace {
    pub fn id(&self) -> WorkspaceId {
        WorkspaceId::new(self.repo_root.clone(), self.name.clone())
    }

    /// Whether this workspace is pinned to any register.
    pub fn is_pinned(&self) -> bool {
        self.pin_group.as_deref().is_some_and(|g| !g.is_empty())
    }

    /// Whether this row should surface under the `attention` scope: it wants
    /// the user (unread) or is actively working.
    pub fn wants_attention(&self) -> bool {
        self.unread || self.status.always_shown() || self.status == Status::Waiting
    }
}

/// A project groups the workspaces of one repository. `name` is the repo-root
/// basename shown as the teal deck header.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct Project {
    pub repo_root: String,
    pub name: String,
    pub workspaces: Vec<Workspace>,
}

/// Cached PR association + CI state for a workspace's PR. Mirrors the Go
/// pr-status cache row.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, Default)]
pub struct PrRef {
    pub repo: String,
    pub number: u64,
    /// PR state: open / closed / merged / draft.
    pub state: String,
    /// CI rollup: passing / failing / pending / none.
    pub ci: String,
    /// Epoch-ms the row was last fetched.
    pub fetched_at: i64,
}

/// Global display alias for a pin register (register key -> human label).
/// Stored separately because a register spans repos in the merged deck view,
/// matching the Go `pin-groups.json`.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct PinGroup {
    pub name: String,
    pub label: String,
    pub sort_order: i64,
}

/// Which slice of the roster the deck is showing. Cycled with `P`.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Default)]
pub enum Scope {
    /// Every workspace across every project.
    #[default]
    All,
    /// Only workspaces that want attention (working / waiting / unread).
    Attention,
}

impl Scope {
    /// Cycle to the next scope. Matches the Go deck's `P` key.
    pub fn next(self) -> Self {
        match self {
            Scope::All => Scope::Attention,
            Scope::Attention => Scope::All,
        }
    }

    /// The label flashed in the status bar when the scope changes.
    pub fn label(self) -> &'static str {
        match self {
            Scope::All => "all",
            Scope::Attention => "attention",
        }
    }
}
