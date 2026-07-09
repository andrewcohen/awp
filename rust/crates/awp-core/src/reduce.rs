//! The single reducer. `reduce(state, event) -> Vec<Effect>`.
//!
//! This is the only place `AppState` is mutated. Every event — user intent,
//! store notification, backend result — is applied here, and the reducer
//! returns pure [`Effect`]s for the executor to perform. Determinism makes the
//! whole domain unit-testable without a terminal, a database, or a PTY.

use crate::effect::Effect;
use crate::event::{Event, Focus};
use crate::model::{Project, WorkspaceId};
use crate::state::AppState;

/// Apply one event to the state, returning the side effects to perform.
pub fn reduce(state: &mut AppState, event: Event) -> Vec<Effect> {
    match event {
        Event::RosterLoaded(projects) => {
            set_roster(state, projects);
            Vec::new()
        }

        Event::SelectNext => {
            let len = state.visible().len();
            if len > 0 && state.selected + 1 < len {
                state.selected += 1;
            }
            Vec::new()
        }
        Event::SelectPrev => {
            state.selected = state.selected.saturating_sub(1);
            Vec::new()
        }
        Event::SelectFirst => {
            state.selected = 0;
            Vec::new()
        }
        Event::SelectLast => {
            let len = state.visible().len();
            state.selected = len.saturating_sub(1);
            Vec::new()
        }

        Event::CycleScope => {
            state.scope = state.scope.next();
            // Flash the new scope in the status bar, matching the Go deck's `P`.
            state.status_flash = Some(format!("scope: {}", state.scope.label()));
            state.clamp_selection();
            Vec::new()
        }

        Event::SetFilter(text) => {
            state.filter = Some(text);
            state.clamp_selection();
            Vec::new()
        }
        Event::ClearFilter => {
            // Cancellations clear silently — no "filter: cancelled" noise.
            state.filter = None;
            state.status_flash = None;
            state.clamp_selection();
            Vec::new()
        }

        Event::ToggleFocus => {
            state.focus = match state.focus {
                Focus::Deck => Focus::Panel,
                Focus::Panel => Focus::Deck,
            };
            Vec::new()
        }

        Event::OpenSelected => match state.selected_id() {
            Some(id) => {
                // Summoning clears the unread badge and moves focus to the pane.
                if let Some(ws) = state.workspace_mut(&id) {
                    ws.unread = false;
                }
                state.focus = Focus::Panel;
                vec![Effect::OpenWorkspace(id)]
            }
            None => Vec::new(),
        },

        Event::ReportStatus {
            id,
            status,
            prompt,
            viewing,
        } => {
            let Some(ws) = state.workspace_mut(&id) else {
                return Vec::new();
            };
            ws.status = status;
            // ActivePrompt lifecycle (Go writeWorkspaceStatus): a non-empty
            // prompt overwrites; idle/exited clears; working/waiting leave it.
            match prompt.as_deref() {
                Some(p) if !p.is_empty() => ws.active_prompt = Some(p.to_string()),
                _ if status.is_exited() || status == crate::Status::Idle => {
                    ws.active_prompt = None;
                }
                _ => {}
            }
            // Unread lifecycle: exited drops the badge; attention states set it
            // unless the user is viewing the session.
            if status.is_exited() {
                ws.unread = false;
            } else if status.wants_attention() {
                ws.unread = !viewing;
            }
            let unread = ws.unread;
            vec![Effect::PersistStatus {
                id,
                status,
                prompt,
                unread,
            }]
        }

        Event::UpsertWorkspace(ws) => {
            upsert(state, ws);
            state.clamp_selection();
            Vec::new()
        }

        Event::SetPin { id, group } => {
            if let Some(ws) = state.workspace_mut(&id) {
                ws.pin_group = if group.is_empty() {
                    None
                } else {
                    Some(group.clone())
                };
            }
            state.clamp_selection();
            vec![Effect::PersistPin { id, group }]
        }

        Event::CreateWorkspace {
            repo_root,
            name,
            bookmark,
            prompt,
        } => vec![Effect::CreateWorkspace {
            repo_root,
            name,
            bookmark,
            prompt,
        }],

        Event::RenameWorkspace { id, new_name } => {
            if new_name.trim().is_empty() || new_name == id.name {
                return Vec::new();
            }
            // Optimistically re-key the row so the deck updates immediately.
            if let Some(ws) = state.workspace_mut(&id) {
                ws.name = new_name.clone();
            }
            state.clamp_selection();
            vec![Effect::RenameWorkspace { id, new_name }]
        }

        Event::DeleteWorkspace { id } => {
            remove_workspace(state, &id);
            state.clamp_selection();
            vec![Effect::DeleteWorkspace { id }]
        }

        Event::SetPr { id, number } => {
            if let Some(ws) = state.workspace_mut(&id) {
                ws.pr_number = (number != 0).then_some(number);
            }
            state.clamp_selection();
            vec![Effect::PersistPr { id, number }]
        }

        Event::LinkBookmark { id, bookmark } => {
            if let Some(ws) = state.workspace_mut(&id) {
                ws.bookmark = (!bookmark.is_empty()).then(|| bookmark.clone());
            }
            vec![Effect::PersistBookmark { id, bookmark }]
        }

        Event::SendPrompt { id, text } => {
            if text.trim().is_empty() {
                return Vec::new();
            }
            if let Some(ws) = state.workspace_mut(&id) {
                ws.active_prompt = Some(text.clone());
            }
            vec![Effect::SendPrompt { id, text }]
        }

        Event::OpenPr { id } => match state.workspace(&id).and_then(|w| w.pr_number) {
            Some(number) => vec![Effect::OpenPrWeb { id, number }],
            None => {
                state.status_flash = Some("no PR for this workspace".into());
                Vec::new()
            }
        },

        Event::MergePr { id } => match state.workspace(&id).and_then(|w| w.pr_number) {
            Some(number) => vec![Effect::MergePr { id, number }],
            None => {
                state.status_flash = Some("no PR for this workspace".into());
                Vec::new()
            }
        },

        Event::Tick => Vec::new(),

        Event::Quit => {
            state.should_quit = true;
            Vec::new()
        }
    }
}

/// Remove a workspace row from the roster, dropping its project if it becomes
/// empty.
fn remove_workspace(state: &mut AppState, id: &WorkspaceId) {
    for project in &mut state.projects {
        if project.repo_root == id.repo_root {
            project.workspaces.retain(|w| w.name != id.name);
        }
    }
    state.projects.retain(|p| !p.workspaces.is_empty());
}

/// Install a fresh roster, keeping projects in stable name order and clamping
/// the selection.
fn set_roster(state: &mut AppState, mut projects: Vec<Project>) {
    projects.sort_by(|a, b| {
        a.name
            .cmp(&b.name)
            .then_with(|| a.repo_root.cmp(&b.repo_root))
    });
    for p in &mut projects {
        p.workspaces.sort_by(|a, b| a.name.cmp(&b.name));
    }
    state.projects = projects;
    state.clamp_selection();
}

/// Insert or replace a single workspace row within its project, creating the
/// project if it is new.
fn upsert(state: &mut AppState, ws: crate::Workspace) {
    if let Some(project) = state
        .projects
        .iter_mut()
        .find(|p| p.repo_root == ws.repo_root)
    {
        if let Some(existing) = project.workspaces.iter_mut().find(|w| w.name == ws.name) {
            *existing = ws;
        } else {
            project.workspaces.push(ws);
            project.workspaces.sort_by(|a, b| a.name.cmp(&b.name));
        }
        return;
    }
    let name = basename(&ws.repo_root);
    state.projects.push(Project {
        repo_root: ws.repo_root.clone(),
        name,
        workspaces: vec![ws],
    });
    state.projects.sort_by(|a, b| {
        a.name
            .cmp(&b.name)
            .then_with(|| a.repo_root.cmp(&b.repo_root))
    });
}

/// The final path component of a repo root, used as the project display name.
fn basename(path: &str) -> String {
    path.trim_end_matches('/')
        .rsplit('/')
        .next()
        .unwrap_or(path)
        .to_string()
}
