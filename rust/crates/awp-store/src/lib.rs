//! `awp-store` — SQLite (WAL) persistence for awp.
//!
//! Chosen for **multi-writer correctness**: a status hook is a partial,
//! row-level `UPDATE`, so concurrent writers (the deck plus every
//! `report-status` hook) never clobber each other the way the Go whole-file
//! JSON rewrite did. Cross-process change detection is a cheap `PRAGMA
//! data_version` poll; dirty rows are reloaded by `updated_at`.
//!
//! The `rusqlite` C dependency is deliberately isolated to this crate.

mod error;
mod migrate;

pub use error::{Result, StoreError};
pub use migrate::MigrationReport;

use awp_core::{PinGroup, PrRef, Project, Status, Workspace, WorkspaceId};
use rusqlite::{params, Connection, OptionalExtension};
use std::collections::BTreeMap;
use std::path::{Path, PathBuf};
use std::time::{SystemTime, UNIX_EPOCH};

/// Current schema version. Bumped when the table shapes change.
const SCHEMA_VERSION: i64 = 1;

const SCHEMA: &str = r#"
CREATE TABLE IF NOT EXISTS workspaces (
  repo_root     TEXT NOT NULL,
  name          TEXT NOT NULL,
  path          TEXT NOT NULL,
  bookmark      TEXT,
  pr_number     INTEGER,
  session_id    TEXT,
  session_name  TEXT,
  status        TEXT,
  active_prompt TEXT,
  unread        INTEGER DEFAULT 0,
  pin_group     TEXT,
  agent_window  TEXT,
  agent_pane    TEXT,
  updated_at    INTEGER NOT NULL,
  PRIMARY KEY (repo_root, name)
);
CREATE TABLE IF NOT EXISTS pin_groups (
  name       TEXT PRIMARY KEY,
  label      TEXT,
  sort_order INTEGER
);
CREATE TABLE IF NOT EXISTS pr_status (
  repo       TEXT,
  pr_number  INTEGER,
  state      TEXT,
  ci         TEXT,
  fetched_at INTEGER,
  PRIMARY KEY (repo, pr_number)
);
CREATE TABLE IF NOT EXISTS schema_meta (
  key   TEXT PRIMARY KEY,
  value TEXT
);
"#;

/// A handle to the SQLite state database.
pub struct Store {
    conn: Connection,
    path: PathBuf,
}

impl Store {
    /// Open (creating if needed) the store at `path`, enabling WAL and applying
    /// the schema.
    pub fn open(path: impl AsRef<Path>) -> Result<Self> {
        let path = path.as_ref().to_path_buf();
        if let Some(dir) = path.parent() {
            std::fs::create_dir_all(dir).map_err(|source| StoreError::ReadFile {
                path: dir.to_path_buf(),
                source,
            })?;
        }
        let conn = Connection::open(&path).map_err(|source| StoreError::Open {
            path: path.clone(),
            source,
        })?;
        // WAL enables concurrent deck + hook writers.
        conn.pragma_update(None, "journal_mode", "WAL")?;
        conn.execute_batch(SCHEMA)?;
        let store = Self { conn, path };
        store.set_meta("schema_version", &SCHEMA_VERSION.to_string())?;
        Ok(store)
    }

    /// Open the default store at `~/.awp/state.db`.
    pub fn open_default() -> Result<Self> {
        let home = home_dir().ok_or(StoreError::NoHome)?;
        Self::open(home.join(".awp").join("state.db"))
    }

    /// In-memory store, for tests.
    pub fn open_in_memory() -> Result<Self> {
        let conn = Connection::open_in_memory()?;
        conn.execute_batch(SCHEMA)?;
        Ok(Self {
            conn,
            path: PathBuf::from(":memory:"),
        })
    }

    pub fn path(&self) -> &Path {
        &self.path
    }

    // --- roster ------------------------------------------------------------

    /// Load the full roster into RAM, grouped into projects. Serves the deck's
    /// fast path — no jj/gh/session queries here.
    pub fn load_roster(&self) -> Result<Vec<Project>> {
        let mut stmt = self.conn.prepare(
            "SELECT repo_root, name, path, bookmark, pr_number, session_id, session_name, \
             status, active_prompt, unread, pin_group, agent_window, agent_pane \
             FROM workspaces ORDER BY repo_root, name",
        )?;
        let rows = stmt.query_map([], row_to_workspace)?;

        let mut by_repo: BTreeMap<String, Vec<Workspace>> = BTreeMap::new();
        for ws in rows {
            let ws = ws?;
            by_repo.entry(ws.repo_root.clone()).or_default().push(ws);
        }
        Ok(by_repo
            .into_iter()
            .map(|(repo_root, workspaces)| Project {
                name: basename(&repo_root),
                repo_root,
                workspaces,
            })
            .collect())
    }

    /// Fetch a single workspace row by id.
    pub fn get_workspace(&self, id: &WorkspaceId) -> Result<Option<Workspace>> {
        let mut stmt = self.conn.prepare(
            "SELECT repo_root, name, path, bookmark, pr_number, session_id, session_name, \
             status, active_prompt, unread, pin_group, agent_window, agent_pane \
             FROM workspaces WHERE repo_root=?1 AND name=?2",
        )?;
        Ok(stmt
            .query_row(params![id.repo_root, id.name], row_to_workspace)
            .optional()?)
    }

    /// All distinct repo roots in the store, for basename-fallback resolution.
    pub fn repo_roots(&self) -> Result<Vec<String>> {
        let mut stmt = self
            .conn
            .prepare("SELECT DISTINCT repo_root FROM workspaces ORDER BY repo_root")?;
        let rows = stmt.query_map([], |r| r.get::<_, String>(0))?;
        rows.collect::<rusqlite::Result<Vec<_>>>()
            .map_err(Into::into)
    }

    /// Insert or replace a whole workspace row.
    pub fn upsert_workspace(&self, ws: &Workspace) -> Result<()> {
        self.conn.execute(
            "INSERT OR REPLACE INTO workspaces \
             (repo_root, name, path, bookmark, pr_number, session_id, session_name, \
              status, active_prompt, unread, pin_group, agent_window, agent_pane, updated_at) \
             VALUES (?1,?2,?3,?4,?5,?6,?7,?8,?9,?10,?11,?12,?13,?14)",
            params![
                ws.repo_root,
                ws.name,
                ws.path,
                ws.bookmark,
                ws.pr_number.map(|n| n as i64),
                ws.session_id,
                ws.session_name,
                ws.status.as_str(),
                ws.active_prompt,
                ws.unread as i64,
                ws.pin_group,
                ws.agent_window,
                ws.agent_pane,
                now_ms(),
            ],
        )?;
        Ok(())
    }

    /// The partial, row-level status write that replaces the whole-file JSON
    /// rewrite — the single biggest correctness win of the rewrite. Only the
    /// status/unread columns (and the prompt, when supplied) are touched, so
    /// concurrent writers never clobber each other's fields.
    pub fn update_status(
        &self,
        id: &WorkspaceId,
        status: Status,
        prompt: Option<&str>,
        unread: bool,
    ) -> Result<bool> {
        let changed = match prompt {
            Some(p) => self.conn.execute(
                "UPDATE workspaces SET status=?1, active_prompt=?2, unread=?3, updated_at=?4 \
                 WHERE repo_root=?5 AND name=?6",
                params![
                    status.as_str(),
                    p,
                    unread as i64,
                    now_ms(),
                    id.repo_root,
                    id.name
                ],
            )?,
            None => self.conn.execute(
                "UPDATE workspaces SET status=?1, unread=?2, updated_at=?3 \
                 WHERE repo_root=?4 AND name=?5",
                params![
                    status.as_str(),
                    unread as i64,
                    now_ms(),
                    id.repo_root,
                    id.name
                ],
            )?,
        };
        Ok(changed > 0)
    }

    /// Delete a workspace row.
    pub fn delete_workspace(&self, id: &WorkspaceId) -> Result<()> {
        self.conn.execute(
            "DELETE FROM workspaces WHERE repo_root=?1 AND name=?2",
            params![id.repo_root, id.name],
        )?;
        Ok(())
    }

    /// Update a workspace's pin register (`None` unpins).
    pub fn set_pin(&self, id: &WorkspaceId, group: Option<&str>) -> Result<()> {
        self.conn.execute(
            "UPDATE workspaces SET pin_group=?1, updated_at=?2 WHERE repo_root=?3 AND name=?4",
            params![group, now_ms(), id.repo_root, id.name],
        )?;
        Ok(())
    }

    /// Rows whose `updated_at` is strictly greater than `since` (epoch-ms).
    /// Drives cross-process reload: after a `data_version` bump, pull only the
    /// dirty rows.
    pub fn dirty_since(&self, since: i64) -> Result<Vec<Workspace>> {
        let mut stmt = self.conn.prepare(
            "SELECT repo_root, name, path, bookmark, pr_number, session_id, session_name, \
             status, active_prompt, unread, pin_group, agent_window, agent_pane \
             FROM workspaces WHERE updated_at > ?1 ORDER BY repo_root, name",
        )?;
        let rows = stmt.query_map(params![since], row_to_workspace)?;
        rows.collect::<rusqlite::Result<Vec<_>>>()
            .map_err(Into::into)
    }

    /// The SQLite change counter. Changes whenever another connection commits a
    /// write — the cheap cross-process poll.
    pub fn data_version(&self) -> Result<i64> {
        Ok(self
            .conn
            .pragma_query_value(None, "data_version", |r| r.get(0))?)
    }

    // --- pin aliases -------------------------------------------------------

    pub fn load_pin_groups(&self) -> Result<Vec<PinGroup>> {
        let mut stmt = self
            .conn
            .prepare("SELECT name, label, sort_order FROM pin_groups ORDER BY sort_order, name")?;
        let rows = stmt.query_map([], |r| {
            Ok(PinGroup {
                name: r.get(0)?,
                label: r.get::<_, Option<String>>(1)?.unwrap_or_default(),
                sort_order: r.get::<_, Option<i64>>(2)?.unwrap_or_default(),
            })
        })?;
        rows.collect::<rusqlite::Result<Vec<_>>>()
            .map_err(Into::into)
    }

    pub fn save_pin_group(&self, group: &PinGroup) -> Result<()> {
        if group.label.trim().is_empty() {
            self.conn
                .execute("DELETE FROM pin_groups WHERE name=?1", params![group.name])?;
            return Ok(());
        }
        self.conn.execute(
            "INSERT OR REPLACE INTO pin_groups (name, label, sort_order) VALUES (?1,?2,?3)",
            params![group.name, group.label, group.sort_order],
        )?;
        Ok(())
    }

    // --- pr status ---------------------------------------------------------

    pub fn upsert_pr(&self, pr: &PrRef) -> Result<()> {
        self.conn.execute(
            "INSERT OR REPLACE INTO pr_status (repo, pr_number, state, ci, fetched_at) \
             VALUES (?1,?2,?3,?4,?5)",
            params![pr.repo, pr.number as i64, pr.state, pr.ci, pr.fetched_at],
        )?;
        Ok(())
    }

    pub fn load_pr(&self, repo: &str, number: u64) -> Result<Option<PrRef>> {
        Ok(self
            .conn
            .query_row(
                "SELECT repo, pr_number, state, ci, fetched_at FROM pr_status \
                 WHERE repo=?1 AND pr_number=?2",
                params![repo, number as i64],
                |r| {
                    Ok(PrRef {
                        repo: r.get(0)?,
                        number: r.get::<_, i64>(1)? as u64,
                        state: r.get::<_, Option<String>>(2)?.unwrap_or_default(),
                        ci: r.get::<_, Option<String>>(3)?.unwrap_or_default(),
                        fetched_at: r.get::<_, Option<i64>>(4)?.unwrap_or_default(),
                    })
                },
            )
            .optional()?)
    }

    // --- migration ---------------------------------------------------------

    /// Whether a JSON migration has already been recorded.
    pub fn is_migrated(&self) -> Result<bool> {
        Ok(self.get_meta("migrated_from")?.is_some())
    }

    /// Migrate the Go JSON files under `home/.awp` into the store. Idempotent:
    /// once `migrated_from` is set a re-run is a no-op unless `force` is set.
    /// With `dry_run`, nothing is written and the report shows what *would*
    /// happen. Never deletes the JSON.
    pub fn migrate_from_home(
        &self,
        home: &Path,
        dry_run: bool,
        force: bool,
    ) -> Result<MigrationReport> {
        if !force && !dry_run && self.is_migrated()? {
            return Ok(MigrationReport::default());
        }
        let parsed = migrate::parse_json_dir(home)?;
        let report = MigrationReport {
            workspaces: parsed.workspaces.len(),
            pin_groups: parsed.pin_groups.len(),
            pr_rows: parsed.pr_rows.len(),
            sources: parsed.sources.clone(),
            applied: !dry_run,
        };
        if dry_run {
            return Ok(report);
        }
        for ws in &parsed.workspaces {
            self.upsert_workspace(ws)?;
        }
        for pg in &parsed.pin_groups {
            self.save_pin_group(pg)?;
        }
        for pr in &parsed.pr_rows {
            self.upsert_pr(pr)?;
        }
        self.set_meta("migrated_from", &now_ms().to_string())?;
        Ok(report)
    }

    /// Run the default first-run migration from `~/.awp`.
    pub fn migrate_default(&self, dry_run: bool, force: bool) -> Result<MigrationReport> {
        let home = home_dir().ok_or(StoreError::NoHome)?;
        self.migrate_from_home(&home, dry_run, force)
    }

    // --- meta --------------------------------------------------------------

    pub fn get_meta(&self, key: &str) -> Result<Option<String>> {
        Ok(self
            .conn
            .query_row(
                "SELECT value FROM schema_meta WHERE key=?1",
                params![key],
                |r| r.get(0),
            )
            .optional()?)
    }

    fn set_meta(&self, key: &str, value: &str) -> Result<()> {
        self.conn.execute(
            "INSERT OR REPLACE INTO schema_meta (key, value) VALUES (?1, ?2)",
            params![key, value],
        )?;
        Ok(())
    }
}

/// Shared row mapper for the workspace SELECTs. Empty text columns normalize to
/// `None` so a cleared field (Go's `omitempty` "") reads back as absent.
fn row_to_workspace(r: &rusqlite::Row<'_>) -> rusqlite::Result<Workspace> {
    let opt = |v: Option<String>| v.filter(|s| !s.is_empty());
    Ok(Workspace {
        repo_root: r.get(0)?,
        name: r.get(1)?,
        path: r.get(2)?,
        bookmark: opt(r.get(3)?),
        pr_number: r
            .get::<_, Option<i64>>(4)?
            .filter(|n| *n != 0)
            .map(|n| n as u64),
        session_id: opt(r.get(5)?),
        session_name: opt(r.get(6)?),
        status: Status::parse(&r.get::<_, Option<String>>(7)?.unwrap_or_default()),
        active_prompt: opt(r.get(8)?),
        unread: r.get::<_, i64>(9)? != 0,
        pin_group: opt(r.get(10)?),
        agent_window: opt(r.get(11)?),
        agent_pane: opt(r.get(12)?),
    })
}

fn home_dir() -> Option<PathBuf> {
    std::env::var_os("HOME")
        .map(PathBuf::from)
        .filter(|p| !p.as_os_str().is_empty())
}

fn now_ms() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_millis() as i64)
        .unwrap_or(0)
}

fn basename(path: &str) -> String {
    path.trim_end_matches('/')
        .rsplit('/')
        .next()
        .unwrap_or(path)
        .to_string()
}

#[cfg(test)]
mod tests;
