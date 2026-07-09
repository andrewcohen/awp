//! Unit tests for the reducer — the acceptance surface for the core.

use crate::{reduce, AppState, Effect, Event, Project, Scope, Status, Workspace, WorkspaceId};

fn ws(repo: &str, name: &str, status: Status) -> Workspace {
    Workspace {
        repo_root: repo.into(),
        name: name.into(),
        path: format!("{repo}/{name}"),
        status,
        ..Default::default()
    }
}

fn roster() -> Vec<Project> {
    vec![
        Project {
            repo_root: "/r/alpha".into(),
            name: "alpha".into(),
            workspaces: vec![
                ws("/r/alpha", "main", Status::Idle),
                ws("/r/alpha", "feature", Status::Working),
            ],
        },
        Project {
            repo_root: "/r/beta".into(),
            name: "beta".into(),
            workspaces: vec![ws("/r/beta", "wip", Status::Waiting)],
        },
    ]
}

fn loaded() -> AppState {
    let mut s = AppState::default();
    let effects = reduce(&mut s, Event::RosterLoaded(roster()));
    assert!(effects.is_empty());
    s
}

#[test]
fn roster_loads_sorted_and_visible() {
    let s = loaded();
    let vis = s.visible();
    assert_eq!(vis.len(), 3);
    // Projects sort by name (alpha before beta); workspaces by name.
    assert_eq!(vis[0], WorkspaceId::new("/r/alpha", "feature"));
    assert_eq!(vis[1], WorkspaceId::new("/r/alpha", "main"));
    assert_eq!(vis[2], WorkspaceId::new("/r/beta", "wip"));
}

#[test]
fn selection_moves_and_clamps() {
    let mut s = loaded();
    assert_eq!(s.selected, 0);
    reduce(&mut s, Event::SelectNext);
    reduce(&mut s, Event::SelectNext);
    reduce(&mut s, Event::SelectNext); // clamps at last
    assert_eq!(s.selected, 2);
    reduce(&mut s, Event::SelectPrev);
    assert_eq!(s.selected, 1);
    reduce(&mut s, Event::SelectFirst);
    assert_eq!(s.selected, 0);
    reduce(&mut s, Event::SelectLast);
    assert_eq!(s.selected, 2);
}

#[test]
fn cycle_scope_filters_to_attention_and_flashes() {
    let mut s = loaded();
    reduce(&mut s, Event::CycleScope);
    assert_eq!(s.scope, Scope::Attention);
    assert_eq!(s.status_flash.as_deref(), Some("scope: attention"));
    // Only working (feature) and waiting (wip) survive; idle main drops.
    let vis = s.visible();
    assert_eq!(vis.len(), 2);
    assert!(vis.contains(&WorkspaceId::new("/r/alpha", "feature")));
    assert!(vis.contains(&WorkspaceId::new("/r/beta", "wip")));
    reduce(&mut s, Event::CycleScope);
    assert_eq!(s.scope, Scope::All);
}

#[test]
fn filter_narrows_and_clear_is_silent() {
    let mut s = loaded();
    reduce(&mut s, Event::SelectLast);
    reduce(&mut s, Event::SetFilter("feat".into()));
    assert_eq!(s.visible(), vec![WorkspaceId::new("/r/alpha", "feature")]);
    // Selection clamped back into range.
    assert_eq!(s.selected, 0);
    reduce(&mut s, Event::ClearFilter);
    assert_eq!(s.filter, None);
    assert_eq!(s.status_flash, None);
    assert_eq!(s.visible().len(), 3);
}

#[test]
fn open_selected_clears_unread_focuses_panel_and_emits_effect() {
    let mut s = loaded();
    // Give the first row an unread badge.
    let id = WorkspaceId::new("/r/alpha", "feature");
    s.workspace_mut(&id).unwrap().unread = true;
    let effects = reduce(&mut s, Event::OpenSelected);
    assert_eq!(effects, vec![Effect::OpenWorkspace(id.clone())]);
    assert!(!s.workspace(&id).unwrap().unread);
    assert_eq!(s.focus, crate::Focus::Panel);
}

#[test]
fn report_status_sets_unread_unless_viewing() {
    let mut s = loaded();
    let id = WorkspaceId::new("/r/alpha", "main");
    let effects = reduce(
        &mut s,
        Event::ReportStatus {
            id: id.clone(),
            status: Status::Waiting,
            prompt: Some("please confirm".into()),
            viewing: false,
        },
    );
    let w = s.workspace(&id).unwrap();
    assert_eq!(w.status, Status::Waiting);
    assert!(w.unread);
    assert_eq!(w.active_prompt.as_deref(), Some("please confirm"));
    assert_eq!(
        effects,
        vec![Effect::PersistStatus {
            id,
            status: Status::Waiting,
            prompt: Some("please confirm".into()),
            unread: true,
        }]
    );
}

#[test]
fn report_status_viewing_suppresses_unread() {
    let mut s = loaded();
    let id = WorkspaceId::new("/r/alpha", "main");
    reduce(
        &mut s,
        Event::ReportStatus {
            id: id.clone(),
            status: Status::Waiting,
            prompt: None,
            viewing: true,
        },
    );
    assert!(!s.workspace(&id).unwrap().unread);
}

#[test]
fn report_status_exited_drops_unread_and_clears_nothing_extra() {
    let mut s = loaded();
    let id = WorkspaceId::new("/r/beta", "wip");
    s.workspace_mut(&id).unwrap().unread = true;
    s.workspace_mut(&id).unwrap().active_prompt = Some("busy".into());
    reduce(
        &mut s,
        Event::ReportStatus {
            id: id.clone(),
            status: Status::Exited,
            prompt: None,
            viewing: false,
        },
    );
    let w = s.workspace(&id).unwrap();
    assert!(!w.unread);
    // Exited is not idle, so the prompt is not cleared by the idle/exited rule…
    // wait: exited DOES clear per Go (idle||exited). Assert cleared.
    assert_eq!(w.active_prompt, None);
}

#[test]
fn idle_clears_active_prompt() {
    let mut s = loaded();
    let id = WorkspaceId::new("/r/alpha", "feature");
    s.workspace_mut(&id).unwrap().active_prompt = Some("mid task".into());
    reduce(
        &mut s,
        Event::ReportStatus {
            id: id.clone(),
            status: Status::Idle,
            prompt: None,
            viewing: false,
        },
    );
    assert_eq!(s.workspace(&id).unwrap().active_prompt, None);
}

#[test]
fn working_keeps_active_prompt() {
    let mut s = loaded();
    let id = WorkspaceId::new("/r/alpha", "feature");
    s.workspace_mut(&id).unwrap().active_prompt = Some("keep me".into());
    reduce(
        &mut s,
        Event::ReportStatus {
            id: id.clone(),
            status: Status::Working,
            prompt: None,
            viewing: false,
        },
    );
    assert_eq!(
        s.workspace(&id).unwrap().active_prompt.as_deref(),
        Some("keep me")
    );
}

#[test]
fn pin_floats_row_to_top_and_persists() {
    let mut s = loaded();
    let id = WorkspaceId::new("/r/beta", "wip");
    let effects = reduce(
        &mut s,
        Event::SetPin {
            id: id.clone(),
            group: "default".into(),
        },
    );
    assert_eq!(
        effects,
        vec![Effect::PersistPin {
            id: id.clone(),
            group: "default".into()
        }]
    );
    // Pinned row now sorts first.
    assert_eq!(s.visible()[0], id);
    // Unpin.
    reduce(
        &mut s,
        Event::SetPin {
            id: id.clone(),
            group: String::new(),
        },
    );
    assert!(!s.workspace(&id).unwrap().is_pinned());
}

#[test]
fn upsert_adds_new_workspace_and_project() {
    let mut s = loaded();
    reduce(
        &mut s,
        Event::UpsertWorkspace(ws("/r/gamma", "new", Status::Starting)),
    );
    assert!(s.workspace(&WorkspaceId::new("/r/gamma", "new")).is_some());
    assert_eq!(s.projects.len(), 3);
}

#[test]
fn quit_sets_flag() {
    let mut s = loaded();
    reduce(&mut s, Event::Quit);
    assert!(s.should_quit);
}

#[test]
fn report_status_unknown_workspace_is_noop() {
    let mut s = loaded();
    let effects = reduce(
        &mut s,
        Event::ReportStatus {
            id: WorkspaceId::new("/nope", "x"),
            status: Status::Working,
            prompt: None,
            viewing: false,
        },
    );
    assert!(effects.is_empty());
}
