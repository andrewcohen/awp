//! Side-effect descriptions produced by the reducer.
//!
//! An `Effect` is a *pure value* describing something to do; it performs no I/O
//! itself. An executor (in `awp-tui`) runs each effect off the reducer and
//! feeds results back in as [`crate::Event`]s. This keeps the core
//! deterministic and unit-testable — the reducer never touches a socket, a
//! subprocess, or the store directly.

use crate::model::WorkspaceId;
use crate::status::Status;

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Effect {
    /// Summon or attach the workspace's session and bring its live pane into
    /// focus. The executor owns the SessionBackend/VtEngine wiring.
    OpenWorkspace(WorkspaceId),

    /// Persist a status/prompt change as a partial, row-level store write —
    /// the SQLite `UPDATE workspaces SET status=?, active_prompt=?` that
    /// replaces the whole-file JSON rewrite.
    PersistStatus {
        id: WorkspaceId,
        status: Status,
        /// `Some` overwrites the prompt column; `None` leaves it unchanged.
        prompt: Option<String>,
        unread: bool,
    },

    /// Persist a pin-group change for a workspace row.
    PersistPin { id: WorkspaceId, group: String },

    /// Fetch fresh PR/CI state for a repo's PR (background enrichment; never on
    /// the switch/first-paint fast path).
    FetchPr { repo: String, number: u64 },

    /// Reload the in-RAM roster from the store (e.g. after a data_version bump).
    ReloadRoster,
}
