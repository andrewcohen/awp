//! The single application state and the visible-row projection.

use crate::event::Focus;
use crate::model::{Project, Scope, Workspace, WorkspaceId};
use std::collections::BTreeMap;

/// The whole deck state. Owned by the reducer; projected (read-only) by the
/// TUI. There is exactly one of these.
#[derive(Debug, Clone, Default)]
pub struct AppState {
    /// The roster: projects in stable (sorted-by-name) order, each with its
    /// workspaces. Loaded from the store into RAM so switching/first-paint
    /// never touches jj/gh/session queries (the Go "fast path").
    pub projects: Vec<Project>,
    pub scope: Scope,
    pub focus: Focus,
    /// Active `/` filter text. `None` = no filter.
    pub filter: Option<String>,
    /// Selection index into the current visible-row list.
    pub selected: usize,
    /// Transient status-bar message (e.g. the scope flash). Cleared silently on
    /// cancel — never echoes "…: cancelled".
    pub status_flash: Option<String>,
    /// Register key -> human label (from pin-groups.json).
    pub pin_aliases: BTreeMap<String, String>,
    pub should_quit: bool,
}

impl AppState {
    /// The flattened, ordered list of workspace ids currently visible given the
    /// scope + filter. Pinned rows float to the top (grouped by register),
    /// then projects in name order.
    pub fn visible(&self) -> Vec<WorkspaceId> {
        let mut pinned: Vec<&Workspace> = Vec::new();
        let mut rest: Vec<&Workspace> = Vec::new();
        for project in &self.projects {
            for ws in &project.workspaces {
                if !self.passes(ws) {
                    continue;
                }
                if ws.is_pinned() {
                    pinned.push(ws);
                } else {
                    rest.push(ws);
                }
            }
        }
        // Pinned rows sort by register key then name for a stable section.
        pinned.sort_by(|a, b| {
            a.pin_group
                .cmp(&b.pin_group)
                .then_with(|| a.name.cmp(&b.name))
        });
        pinned.into_iter().chain(rest).map(Workspace::id).collect()
    }

    /// Whether a workspace passes the active scope + filter.
    fn passes(&self, ws: &Workspace) -> bool {
        if self.scope == Scope::Attention && !ws.wants_attention() {
            return false;
        }
        if let Some(f) = &self.filter {
            if !f.is_empty() && !matches_filter(ws, f) {
                return false;
            }
        }
        true
    }

    /// The currently-selected workspace id, if any rows are visible.
    pub fn selected_id(&self) -> Option<WorkspaceId> {
        self.visible().into_iter().nth(self.selected)
    }

    /// Look up a workspace by id (immutable).
    pub fn workspace(&self, id: &WorkspaceId) -> Option<&Workspace> {
        self.projects
            .iter()
            .find(|p| p.repo_root == id.repo_root)
            .and_then(|p| p.workspaces.iter().find(|w| w.name == id.name))
    }

    /// Look up a workspace by id (mutable).
    pub fn workspace_mut(&mut self, id: &WorkspaceId) -> Option<&mut Workspace> {
        self.projects
            .iter_mut()
            .find(|p| p.repo_root == id.repo_root)
            .and_then(|p| p.workspaces.iter_mut().find(|w| w.name == id.name))
    }

    /// Clamp the selection index into the current visible range. Called after
    /// any change that can shrink the visible set (scope, filter, roster).
    pub(crate) fn clamp_selection(&mut self) {
        let len = self.visible().len();
        if len == 0 {
            self.selected = 0;
        } else if self.selected >= len {
            self.selected = len - 1;
        }
    }
}

/// Case-insensitive substring match against the workspace name, bookmark, and
/// active prompt — the fields a user filters by in the Go deck.
fn matches_filter(ws: &Workspace, filter: &str) -> bool {
    let needle = filter.to_ascii_lowercase();
    let hay = [
        Some(ws.name.as_str()),
        ws.bookmark.as_deref(),
        ws.active_prompt.as_deref(),
    ];
    hay.into_iter()
        .flatten()
        .any(|s| s.to_ascii_lowercase().contains(&needle))
}
