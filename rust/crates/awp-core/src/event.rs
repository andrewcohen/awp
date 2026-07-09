//! Events fed into the single reducer.
//!
//! Every mutation of `AppState` — user intent, store change notification,
//! backend/job completion — is expressed as an `Event` and applied by
//! [`crate::reduce`]. Nothing mutates state outside the reducer; that is the
//! fix for the Go version's scattered-goroutine + file-IPC race class.
//!
//! Events are UI-agnostic *intents*, not raw key codes. The TUI translates
//! keystrokes into these so the core never learns about crossterm/ratatui.

use crate::model::{Project, Workspace, WorkspaceId};
use crate::status::Status;

/// Which surface currently has keyboard focus.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum Focus {
    /// The sidebar roster (j/k select, Enter open, / filter).
    #[default]
    Deck,
    /// The live agent pane (keys forwarded raw to the PTY).
    Panel,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Event {
    /// The store finished (re)loading the roster into RAM.
    RosterLoaded(Vec<Project>),

    /// Move the deck selection.
    SelectNext,
    SelectPrev,
    SelectFirst,
    SelectLast,

    /// Cycle the visible scope (`P`).
    CycleScope,

    /// Filter interactions (`/`).
    SetFilter(String),
    ClearFilter,

    /// Toggle focus between the deck and the live panel (`Ctrl-a`).
    ToggleFocus,

    /// Open (summon/attach) the currently-selected workspace (`Enter`).
    OpenSelected,

    /// A status report for a workspace. `viewing` is true when the user is
    /// currently attached to that session (suppresses the unread badge, exactly
    /// like the Go `writeWorkspaceStatus` "attached client" check). Emitted both
    /// by the local `report-status` path and by store-change notifications.
    ReportStatus {
        id: WorkspaceId,
        status: Status,
        /// `Some("")` clears the prompt; `None` leaves it unchanged.
        prompt: Option<String>,
        viewing: bool,
    },

    /// Insert or replace a whole workspace row (e.g. after create/rename).
    UpsertWorkspace(Workspace),

    /// Pin or unpin the selected workspace to a register (`m` chord). Empty
    /// group unpins.
    SetPin {
        id: WorkspaceId,
        group: String,
    },

    /// Create a new workspace (`n` form). The row appears once the executor
    /// reports it back via `UpsertWorkspace`.
    CreateWorkspace {
        repo_root: String,
        name: String,
        bookmark: String,
        prompt: String,
    },

    /// Rename the selected workspace (`R` form).
    RenameWorkspace {
        id: WorkspaceId,
        new_name: String,
    },

    /// Delete the selected workspace (`D`, after confirmation).
    DeleteWorkspace {
        id: WorkspaceId,
    },

    /// Set a workspace's PR number (`p s`). Zero clears it.
    SetPr {
        id: WorkspaceId,
        number: u64,
    },

    /// Link a bookmark to a workspace (`B`).
    LinkBookmark {
        id: WorkspaceId,
        bookmark: String,
    },

    /// Send a typed prompt to a workspace's agent (`A`).
    SendPrompt {
        id: WorkspaceId,
        text: String,
    },

    /// Open a workspace's PR in the browser (`p o`).
    OpenPr {
        id: WorkspaceId,
    },

    /// Squash-merge a workspace's PR (`p m`).
    MergePr {
        id: WorkspaceId,
    },

    /// Periodic tick (spinner, poll cadence).
    Tick,

    /// Request to quit the deck (`q`).
    Quit,
}
