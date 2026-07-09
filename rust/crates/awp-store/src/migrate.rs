//! Non-destructive first-run migration from the Go JSON files.
//!
//! Reads `~/.awp/workspace-state.json`, `~/.awp/pin-groups.json`, and
//! `~/.awp/pr-status-cache.json`, maps every field into the SQLite tables, and
//! **leaves the JSON intact** so Go `awp` and the Rust build can run against the
//! same repos during the transition. Idempotent and re-runnable; `--dry-run`
//! reports the diff without writing.

use crate::error::{Result, StoreError};
use awp_core::{PinGroup, PrRef, Status, Workspace};
use serde::Deserialize;
use std::collections::BTreeMap;
use std::path::{Path, PathBuf};

/// What a migration did (or would do, for `--dry-run`).
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct MigrationReport {
    pub workspaces: usize,
    pub pin_groups: usize,
    pub pr_rows: usize,
    /// Absolute paths of the JSON files that were found and read.
    pub sources: Vec<PathBuf>,
    /// True when the rows were actually written (false for `--dry-run` or a
    /// no-op re-run).
    pub applied: bool,
}

/// The three JSON payloads, parsed into core types, ready to write.
pub(crate) struct Parsed {
    pub workspaces: Vec<Workspace>,
    pub pin_groups: Vec<PinGroup>,
    pub pr_rows: Vec<PrRef>,
    pub sources: Vec<PathBuf>,
}

// --- workspace-state.json --------------------------------------------------

/// Go serialized `Entry` (capitalized field names, `omitempty`). Accepts the
/// legacy `PROverride` alias for `PRNumber`.
#[derive(Debug, Deserialize, Default)]
struct JsonEntry {
    #[serde(rename = "Name", default)]
    name: String,
    #[serde(rename = "Path", default)]
    path: String,
    #[serde(rename = "Bookmark", default)]
    bookmark: String,
    #[serde(rename = "PRNumber", default)]
    pr_number: u64,
    #[serde(rename = "PROverride", default)]
    pr_override: u64,
    #[serde(rename = "SessionID", default)]
    session_id: String,
    #[serde(rename = "SessionName", default)]
    session_name: String,
    #[serde(rename = "AgentWindowID", default)]
    agent_window: String,
    #[serde(rename = "AgentPaneID", default)]
    agent_pane: String,
    #[serde(rename = "ActivePrompt", default)]
    active_prompt: String,
    #[serde(rename = "Status", default)]
    status: String,
    #[serde(rename = "Unread", default)]
    unread: bool,
    #[serde(rename = "PinGroup", default)]
    pin_group: String,
}

fn opt(s: String) -> Option<String> {
    if s.is_empty() {
        None
    } else {
        Some(s)
    }
}

impl JsonEntry {
    fn into_workspace(self, repo_root: &str, key_name: &str) -> Workspace {
        // Honor the legacy PROverride alias exactly like the Go UnmarshalJSON:
        // PRNumber wins, PROverride fills in when PRNumber is zero.
        let pr = if self.pr_number != 0 {
            self.pr_number
        } else {
            self.pr_override
        };
        let name = if self.name.is_empty() {
            key_name.to_string()
        } else {
            self.name
        };
        Workspace {
            repo_root: repo_root.to_string(),
            name,
            path: self.path,
            bookmark: opt(self.bookmark),
            pr_number: (pr != 0).then_some(pr),
            session_id: opt(self.session_id),
            session_name: opt(self.session_name),
            agent_window: opt(self.agent_window),
            agent_pane: opt(self.agent_pane),
            active_prompt: opt(self.active_prompt),
            status: Status::parse(&self.status),
            unread: self.unread,
            pin_group: opt(self.pin_group),
        }
    }
}

// --- pr-status-cache.json --------------------------------------------------

#[derive(Debug, Deserialize)]
struct PrCacheFile {
    #[serde(default)]
    repos: BTreeMap<String, PrCacheRepo>,
}

#[derive(Debug, Deserialize)]
struct PrCacheRepo {
    #[serde(default)]
    fetched_at: String,
    #[serde(default)]
    prs: BTreeMap<String, PrCacheEntry>,
}

#[derive(Debug, Deserialize)]
struct PrCacheEntry {
    #[serde(rename = "Number", default)]
    number: u64,
    #[serde(rename = "State", default)]
    state: String,
    #[serde(rename = "CIState", default)]
    ci_state: String,
}

/// Read + parse all three JSON files under `home/.awp`. Missing files are not
/// errors — they contribute nothing. Malformed JSON *is* an error (the caller
/// surfaces it and leaves the JSON intact), matching the QA "corrupt/partial
/// JSON → clear error" requirement.
pub(crate) fn parse_json_dir(home: &Path) -> Result<Parsed> {
    let awp = home.join(".awp");
    let mut sources = Vec::new();

    let mut workspaces = Vec::new();
    let ws_path = awp.join("workspace-state.json");
    if let Some(data) = read_optional(&ws_path)? {
        let by_repo: BTreeMap<String, BTreeMap<String, JsonEntry>> = serde_json::from_slice(&data)
            .map_err(|source| StoreError::ParseJson {
                path: ws_path.clone(),
                source,
            })?;
        for (repo_root, entries) in by_repo {
            for (name, entry) in entries {
                workspaces.push(entry.into_workspace(&repo_root, &name));
            }
        }
        sources.push(ws_path);
    }

    let mut pin_groups = Vec::new();
    let pin_path = awp.join("pin-groups.json");
    if let Some(data) = read_optional(&pin_path)? {
        let aliases: BTreeMap<String, String> =
            serde_json::from_slice(&data).map_err(|source| StoreError::ParseJson {
                path: pin_path.clone(),
                source,
            })?;
        for (order, (name, label)) in aliases.into_iter().enumerate() {
            pin_groups.push(PinGroup {
                name,
                label,
                sort_order: order as i64,
            });
        }
        sources.push(pin_path);
    }

    let mut pr_rows = Vec::new();
    let pr_path = awp.join("pr-status-cache.json");
    if let Some(data) = read_optional(&pr_path)? {
        let cache: PrCacheFile =
            serde_json::from_slice(&data).map_err(|source| StoreError::ParseJson {
                path: pr_path.clone(),
                source,
            })?;
        for (repo, entry) in cache.repos {
            let fetched_at = rfc3339_to_epoch_ms(&entry.fetched_at);
            for pr in entry.prs.into_values() {
                if pr.number == 0 {
                    continue;
                }
                pr_rows.push(PrRef {
                    repo: repo.clone(),
                    number: pr.number,
                    state: pr.state,
                    ci: pr.ci_state,
                    fetched_at,
                });
            }
        }
        sources.push(pr_path);
    }

    Ok(Parsed {
        workspaces,
        pin_groups,
        pr_rows,
        sources,
    })
}

fn read_optional(path: &Path) -> Result<Option<Vec<u8>>> {
    match std::fs::read(path) {
        Ok(data) => Ok(Some(data)),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(None),
        Err(source) => Err(StoreError::ReadFile {
            path: path.to_path_buf(),
            source,
        }),
    }
}

/// Best-effort RFC3339 → epoch-ms. The cache's `fetched_at` is only used for
/// throttle cooldowns, so an unparseable timestamp degrades to 0 rather than
/// failing the whole migration.
fn rfc3339_to_epoch_ms(s: &str) -> i64 {
    // Minimal parser for `YYYY-MM-DDTHH:MM:SS(.fff)?(Z|±HH:MM)`. Avoids pulling
    // in a date crate for a value that only gates a 60s refresh window.
    let s = s.trim();
    if s.is_empty() {
        return 0;
    }
    let bytes = s.as_bytes();
    let num = |a: usize, b: usize| -> i64 {
        std::str::from_utf8(&bytes[a..b])
            .ok()
            .and_then(|t| t.parse::<i64>().ok())
            .unwrap_or(0)
    };
    if s.len() < 19 || bytes.get(10) != Some(&b'T') {
        return 0;
    }
    let (year, month, day) = (num(0, 4), num(5, 7), num(8, 10));
    let (hour, min, sec) = (num(11, 13), num(14, 16), num(17, 19));
    days_from_civil(year, month, day) * 86_400_000 + (hour * 3600 + min * 60 + sec) * 1000
}

/// Days since the Unix epoch for a civil (proleptic Gregorian) date. Howard
/// Hinnant's algorithm — exact, no leap-second nonsense, no external crate.
fn days_from_civil(y: i64, m: i64, d: i64) -> i64 {
    let y = if m <= 2 { y - 1 } else { y };
    let era = if y >= 0 { y } else { y - 399 } / 400;
    let yoe = y - era * 400;
    let doy = (153 * (if m > 2 { m - 3 } else { m + 9 }) + 2) / 5 + d - 1;
    let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    era * 146_097 + doe - 719_468
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn epoch_parses_known_timestamp() {
        // 2021-01-01T00:00:00Z == 1609459200 s.
        assert_eq!(
            rfc3339_to_epoch_ms("2021-01-01T00:00:00Z"),
            1_609_459_200_000
        );
    }

    #[test]
    fn epoch_bad_input_is_zero() {
        assert_eq!(rfc3339_to_epoch_ms(""), 0);
        assert_eq!(rfc3339_to_epoch_ms("not a date"), 0);
    }
}
