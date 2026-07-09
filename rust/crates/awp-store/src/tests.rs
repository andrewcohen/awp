use super::*;
use awp_core::WorkspaceId;
use std::io::Write;

fn write_json(dir: &Path, name: &str, body: &str) {
    let awp = dir.join(".awp");
    std::fs::create_dir_all(&awp).unwrap();
    let mut f = std::fs::File::create(awp.join(name)).unwrap();
    f.write_all(body.as_bytes()).unwrap();
}

const WORKSPACE_STATE: &str = r#"{
  "/repos/alpha": {
    "main": {
      "Name": "main",
      "Path": "/repos/alpha/main",
      "Bookmark": "andrew/main",
      "PROverride": 42,
      "SessionID": "$1",
      "SessionName": "[awp]alpha__main",
      "ActivePrompt": "fix the bug",
      "Status": "working",
      "Unread": true,
      "PinGroup": "default"
    },
    "spike": {
      "Name": "spike",
      "Path": "/repos/alpha/spike",
      "PRNumber": 7,
      "Status": "in_progress"
    }
  },
  "/repos/beta": {
    "wip": { "Name": "wip", "Path": "/repos/beta/wip", "Status": "waiting" }
  }
}"#;

const PIN_GROUPS: &str = r#"{ "default": "Now", "a": "Later" }"#;

const PR_CACHE: &str = r#"{
  "version": 1,
  "repos": {
    "alpha": {
      "fetched_at": "2026-07-01T12:00:00Z",
      "prs": {
        "42": { "Number": 42, "State": "OPEN", "CIState": "PASSING" }
      }
    }
  }
}"#;

#[test]
fn migration_maps_every_field_nondestructively() {
    let tmp = tempfile::tempdir().unwrap();
    write_json(tmp.path(), "workspace-state.json", WORKSPACE_STATE);
    write_json(tmp.path(), "pin-groups.json", PIN_GROUPS);
    write_json(tmp.path(), "pr-status-cache.json", PR_CACHE);

    let store = Store::open_in_memory().unwrap();
    let report = store.migrate_from_home(tmp.path(), false, false).unwrap();
    assert_eq!(report.workspaces, 3);
    assert_eq!(report.pin_groups, 2);
    assert_eq!(report.pr_rows, 1);
    assert!(report.applied);

    // JSON left intact.
    assert!(tmp.path().join(".awp/workspace-state.json").exists());

    let roster = store.load_roster().unwrap();
    assert_eq!(roster.len(), 2);
    let alpha = roster.iter().find(|p| p.name == "alpha").unwrap();
    let main = alpha.workspaces.iter().find(|w| w.name == "main").unwrap();
    // Legacy PROverride alias honored.
    assert_eq!(main.pr_number, Some(42));
    assert_eq!(main.bookmark.as_deref(), Some("andrew/main"));
    assert_eq!(main.status, Status::Working);
    assert!(main.unread);
    assert_eq!(main.pin_group.as_deref(), Some("default"));
    // "in_progress" folds to Working.
    let spike = alpha.workspaces.iter().find(|w| w.name == "spike").unwrap();
    assert_eq!(spike.status, Status::Working);
    assert_eq!(spike.pr_number, Some(7));

    let pr = store.load_pr("alpha", 42).unwrap().unwrap();
    assert_eq!(pr.state, "OPEN");
    assert_eq!(pr.ci, "PASSING");
    assert!(pr.fetched_at > 0);
}

#[test]
fn migration_is_idempotent() {
    let tmp = tempfile::tempdir().unwrap();
    write_json(tmp.path(), "workspace-state.json", WORKSPACE_STATE);
    let store = Store::open_in_memory().unwrap();
    let first = store.migrate_from_home(tmp.path(), false, false).unwrap();
    assert_eq!(first.workspaces, 3);
    assert!(store.is_migrated().unwrap());
    // Second run is a no-op (already migrated).
    let second = store.migrate_from_home(tmp.path(), false, false).unwrap();
    assert_eq!(second.workspaces, 0);
    assert!(!second.applied);
}

#[test]
fn dry_run_writes_nothing() {
    let tmp = tempfile::tempdir().unwrap();
    write_json(tmp.path(), "workspace-state.json", WORKSPACE_STATE);
    let store = Store::open_in_memory().unwrap();
    let report = store.migrate_from_home(tmp.path(), true, false).unwrap();
    assert_eq!(report.workspaces, 3);
    assert!(!report.applied);
    assert!(!store.is_migrated().unwrap());
    assert!(store.load_roster().unwrap().is_empty());
}

#[test]
fn corrupt_json_is_a_clear_error() {
    let tmp = tempfile::tempdir().unwrap();
    write_json(tmp.path(), "workspace-state.json", "{ not valid json ");
    let store = Store::open_in_memory().unwrap();
    let err = store
        .migrate_from_home(tmp.path(), false, false)
        .unwrap_err();
    let msg = err.to_string();
    assert!(msg.contains("workspace-state.json"), "got: {msg}");
    // Not marked migrated on failure.
    assert!(!store.is_migrated().unwrap());
}

#[test]
fn missing_json_migrates_empty() {
    let tmp = tempfile::tempdir().unwrap();
    let store = Store::open_in_memory().unwrap();
    let report = store.migrate_from_home(tmp.path(), false, false).unwrap();
    assert_eq!(report.workspaces, 0);
    assert!(report.applied);
}

#[test]
fn partial_status_write_does_not_clobber_other_fields() {
    let store = Store::open_in_memory().unwrap();
    store
        .upsert_workspace(&Workspace {
            repo_root: "/r".into(),
            name: "w".into(),
            path: "/r/w".into(),
            bookmark: Some("bm".into()),
            pr_number: Some(9),
            active_prompt: Some("orig".into()),
            status: Status::Idle,
            ..Default::default()
        })
        .unwrap();
    let id = WorkspaceId::new("/r", "w");
    // Update status only (prompt=None): bookmark and pr_number must survive.
    let changed = store
        .update_status(&id, Status::Working, None, false)
        .unwrap();
    assert!(changed);
    let roster = store.load_roster().unwrap();
    let w = &roster[0].workspaces[0];
    assert_eq!(w.status, Status::Working);
    assert_eq!(w.bookmark.as_deref(), Some("bm"));
    assert_eq!(w.pr_number, Some(9));
    assert_eq!(w.active_prompt.as_deref(), Some("orig"));
}

#[test]
fn concurrent_writers_never_lose_updates() {
    // Two connections to the same on-disk WAL db, each updating a different
    // field of different rows. The multi-writer correctness the store exists
    // for: no whole-record clobber.
    let tmp = tempfile::tempdir().unwrap();
    let db = tmp.path().join("state.db");
    let a = Store::open(&db).unwrap();
    a.upsert_workspace(&Workspace {
        repo_root: "/r".into(),
        name: "one".into(),
        path: "/r/one".into(),
        status: Status::Idle,
        ..Default::default()
    })
    .unwrap();
    a.upsert_workspace(&Workspace {
        repo_root: "/r".into(),
        name: "two".into(),
        path: "/r/two".into(),
        status: Status::Idle,
        ..Default::default()
    })
    .unwrap();

    let b = Store::open(&db).unwrap();
    // Writer A flips row "one"; writer B flips row "two", concurrently.
    a.update_status(&WorkspaceId::new("/r", "one"), Status::Working, None, false)
        .unwrap();
    b.update_status(&WorkspaceId::new("/r", "two"), Status::Waiting, None, true)
        .unwrap();

    let roster = a.load_roster().unwrap();
    let ws = &roster[0].workspaces;
    let one = ws.iter().find(|w| w.name == "one").unwrap();
    let two = ws.iter().find(|w| w.name == "two").unwrap();
    assert_eq!(one.status, Status::Working);
    assert_eq!(two.status, Status::Waiting);
    assert!(two.unread);
}

#[test]
fn data_version_bumps_on_external_write() {
    let tmp = tempfile::tempdir().unwrap();
    let db = tmp.path().join("state.db");
    let reader = Store::open(&db).unwrap();
    let writer = Store::open(&db).unwrap();
    let before = reader.data_version().unwrap();
    writer
        .upsert_workspace(&Workspace {
            repo_root: "/r".into(),
            name: "x".into(),
            path: "/r/x".into(),
            ..Default::default()
        })
        .unwrap();
    // The reader must observe a changed data_version after the other
    // connection commits.
    let after = reader.data_version().unwrap();
    assert_ne!(before, after);
}

#[test]
fn dirty_since_returns_only_recent_rows() {
    let store = Store::open_in_memory().unwrap();
    store
        .upsert_workspace(&Workspace {
            repo_root: "/r".into(),
            name: "old".into(),
            path: "/r/old".into(),
            ..Default::default()
        })
        .unwrap();
    let mark = now_ms();
    // Ensure a strictly greater timestamp for the second write.
    std::thread::sleep(std::time::Duration::from_millis(2));
    store
        .upsert_workspace(&Workspace {
            repo_root: "/r".into(),
            name: "new".into(),
            path: "/r/new".into(),
            ..Default::default()
        })
        .unwrap();
    let dirty = store.dirty_since(mark).unwrap();
    assert_eq!(dirty.len(), 1);
    assert_eq!(dirty[0].name, "new");
}
