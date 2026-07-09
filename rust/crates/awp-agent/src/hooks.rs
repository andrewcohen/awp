//! Claude Code hook install + idempotent self-heal.
//!
//! Ports the Go `internal/agenthooks`. The awp deck installs global hooks into
//! `~/.claude/settings.json` that call `awp report-status` on agent lifecycle
//! events. Must-preserve behaviors baked in here:
//!
//! - Report on SessionStart(idle) / UserPromptSubmit+PreToolUse+PostToolUse
//!   (working) / Stop(idle) / PermissionRequest+Elicitation(waiting).
//! - `PreToolUse --waiting-when-tool AskUserQuestion` → waiting.
//! - **Do NOT** hook `Notification` (fires on the ~60s idle ping → false
//!   waiting); actively remove any stale awp-managed Notification hook.
//! - Idempotent self-heal: only write on drift; a marker-version bump forces a
//!   re-sync. Gate on `$TMUX` so global install never affects non-awp Claude
//!   usage; honor `$AWP_BIN`.

use anyhow::{Context, Result};
use serde_json::{json, Map, Value};
use std::path::{Path, PathBuf};

/// Bumps when the hook block schema changes; the installer rewrites entries
/// whose version differs. Mirrors Go `HookMarkerVersion`.
pub const HOOK_MARKER_VERSION: i64 = 6;

/// Tools that block on user input. A PreToolUse hook for one of these reports
/// `waiting` instead of `working`.
pub const BLOCKING_TOOLS: &[&str] = &["AskUserQuestion"];

/// event -> reported state. Mirrors Go `DesiredClaudeHooks`.
pub fn desired_hooks() -> Vec<(&'static str, &'static str)> {
    vec![
        ("SessionStart", "idle"),
        ("UserPromptSubmit", "working"),
        ("PreToolUse", "working"),
        ("PostToolUse", "working"),
        ("Stop", "idle"),
        ("PermissionRequest", "waiting"),
        ("Elicitation", "waiting"),
    ]
}

/// Events awp installed in the past but no longer wants. Their awp-managed
/// entries are stripped on the next sync. Mirrors Go `ObsoleteClaudeHooks`.
pub fn obsolete_hooks() -> Vec<&'static str> {
    vec!["Notification"]
}

/// The shell snippet each Claude hook runs. Gates on `$TMUX` so global install
/// never affects non-tmux Claude usage, and honors `$AWP_BIN`.
pub fn hook_command(event: &str, state: &str) -> String {
    let extra = match event {
        "UserPromptSubmit" => " --prompt-stdin".to_string(),
        "PreToolUse" if !BLOCKING_TOOLS.is_empty() => {
            format!(" --waiting-when-tool {}", BLOCKING_TOOLS.join(","))
        }
        _ => String::new(),
    };
    format!(
        "[ -n \"$TMUX\" ] && \"${{AWP_BIN:-awp}}\" report-status --state {state}{extra} \
         >/dev/null 2>&1 || true"
    )
}

/// The canonical awp-authored entry for an event.
fn desired_entry(event: &str, state: &str) -> Value {
    json!({
        "x-awp": { "version": HOOK_MARKER_VERSION, "state": state },
        "hooks": [ { "type": "command", "command": hook_command(event, state) } ]
    })
}

/// Whether an entry was authored by awp — tagged with `x-awp`, or (for
/// pre-marker installs) recognizable by the `report-status` command.
fn is_awp_entry(entry: &Value) -> bool {
    if entry.get("x-awp").is_some() {
        return true;
    }
    entry
        .get("hooks")
        .and_then(Value::as_array)
        .is_some_and(|hooks| {
            hooks.iter().any(|h| {
                h.get("command")
                    .and_then(Value::as_str)
                    .is_some_and(|c| c.contains("report-status"))
            })
        })
}

/// Collapse every awp entry for an event down to a single canonical one,
/// leaving user entries untouched and in order. Returns (entries, changed).
fn upsert_awp_entry(entries: &[Value], event: &str, state: &str) -> (Vec<Value>, bool) {
    let desired = desired_entry(event, state);
    let mut non_awp: Vec<Value> = Vec::new();
    let mut awp_count = 0;
    let mut sole: Option<&Value> = None;
    for e in entries {
        if is_awp_entry(e) {
            awp_count += 1;
            sole = Some(e);
        } else {
            non_awp.push(e.clone());
        }
    }
    if awp_count == 1 && sole == Some(&desired) {
        return (entries.to_vec(), false);
    }
    non_awp.push(desired);
    (non_awp, true)
}

/// Drop every awp-authored entry, preserving order + non-awp entries.
fn remove_awp_entry(entries: &[Value]) -> (Vec<Value>, bool) {
    let mut out = Vec::new();
    let mut removed = false;
    for e in entries {
        if is_awp_entry(e) {
            removed = true;
        } else {
            out.push(e.clone());
        }
    }
    (out, removed)
}

/// Install / refresh awp hooks into the given settings file. Returns whether it
/// wrote (i.e. whether there was drift to heal). Idempotent.
pub fn install_claude_at(path: &Path) -> Result<bool> {
    if let Some(dir) = path.parent() {
        std::fs::create_dir_all(dir).with_context(|| format!("create {}", dir.display()))?;
    }
    let mut settings: Map<String, Value> = match std::fs::read(path) {
        Ok(data) if !data.is_empty() => {
            serde_json::from_slice(&data).with_context(|| format!("parse {}", path.display()))?
        }
        Ok(_) => Map::new(),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Map::new(),
        Err(e) => return Err(e).with_context(|| format!("read {}", path.display())),
    };

    let mut hooks: Map<String, Value> = settings
        .get("hooks")
        .and_then(Value::as_object)
        .cloned()
        .unwrap_or_default();

    let mut changed = false;
    for (event, state) in desired_hooks() {
        let existing = hooks
            .get(event)
            .and_then(Value::as_array)
            .cloned()
            .unwrap_or_default();
        let (updated, evt_changed) = upsert_awp_entry(&existing, event, state);
        if evt_changed {
            changed = true;
        }
        hooks.insert(event.to_string(), Value::Array(updated));
    }
    for event in obsolete_hooks() {
        let Some(existing) = hooks.get(event).and_then(Value::as_array).cloned() else {
            continue;
        };
        let (updated, evt_changed) = remove_awp_entry(&existing);
        if !evt_changed {
            continue;
        }
        changed = true;
        if updated.is_empty() {
            hooks.remove(event);
        } else {
            hooks.insert(event.to_string(), Value::Array(updated));
        }
    }

    if !changed {
        return Ok(false);
    }
    settings.insert("hooks".to_string(), Value::Object(hooks));
    let mut encoded = serde_json::to_vec_pretty(&settings)?;
    encoded.push(b'\n');
    std::fs::write(path, &encoded).with_context(|| format!("write {}", path.display()))?;
    Ok(true)
}

/// Whether awp's hooks are fully installed (canonical shape) in the file.
pub fn is_installed_at(path: &Path) -> Result<bool> {
    let data = match std::fs::read(path) {
        Ok(d) if !d.is_empty() => d,
        Ok(_) => return Ok(false),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(false),
        Err(e) => return Err(e.into()),
    };
    let settings: Value = match serde_json::from_slice(&data) {
        Ok(v) => v,
        Err(_) => return Ok(false),
    };
    let Some(hooks) = settings.get("hooks").and_then(Value::as_object) else {
        return Ok(false);
    };
    for (event, state) in desired_hooks() {
        let entries = hooks
            .get(event)
            .and_then(Value::as_array)
            .cloned()
            .unwrap_or_default();
        let desired = desired_entry(event, state);
        let awp: Vec<&Value> = entries.iter().filter(|e| is_awp_entry(e)).collect();
        if awp.len() != 1 || awp[0] != &desired {
            return Ok(false);
        }
    }
    Ok(true)
}

/// Default Claude settings path (`~/.claude/settings.json`).
pub fn claude_settings_path() -> Result<PathBuf> {
    let home = std::env::var_os("HOME").context("resolve HOME")?;
    Ok(PathBuf::from(home).join(".claude").join("settings.json"))
}

/// Install into the default settings path.
pub fn install_claude() -> Result<bool> {
    install_claude_at(&claude_settings_path()?)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn install_is_idempotent_and_self_heals() {
        let tmp = tempfile::tempdir().unwrap();
        let path = tmp.path().join("settings.json");
        assert!(install_claude_at(&path).unwrap(), "first install writes");
        assert!(is_installed_at(&path).unwrap());
        // Second run: no drift, no write.
        assert!(!install_claude_at(&path).unwrap(), "idempotent");
    }

    #[test]
    fn does_not_install_notification_and_removes_stale_one() {
        let tmp = tempfile::tempdir().unwrap();
        let path = tmp.path().join("settings.json");
        // Seed a stale awp-managed Notification hook.
        let seed = json!({
            "hooks": {
                "Notification": [ {
                    "x-awp": { "version": 5, "state": "waiting" },
                    "hooks": [ { "type": "command", "command": "awp report-status --state waiting" } ]
                } ]
            }
        });
        std::fs::write(&path, serde_json::to_vec_pretty(&seed).unwrap()).unwrap();
        install_claude_at(&path).unwrap();
        let out: Value = serde_json::from_slice(&std::fs::read(&path).unwrap()).unwrap();
        let hooks = out.get("hooks").unwrap().as_object().unwrap();
        // Notification key removed entirely (only awp entry was there).
        assert!(
            !hooks.contains_key("Notification"),
            "stale Notification stripped"
        );
        // Never installs Notification as a desired hook.
        assert!(!desired_hooks().iter().any(|(e, _)| *e == "Notification"));
    }

    #[test]
    fn preserves_user_hooks_for_same_event() {
        let tmp = tempfile::tempdir().unwrap();
        let path = tmp.path().join("settings.json");
        let seed = json!({
            "hooks": {
                "Stop": [ {
                    "hooks": [ { "type": "command", "command": "my-own-thing" } ]
                } ]
            }
        });
        std::fs::write(&path, serde_json::to_vec_pretty(&seed).unwrap()).unwrap();
        install_claude_at(&path).unwrap();
        let out: Value = serde_json::from_slice(&std::fs::read(&path).unwrap()).unwrap();
        let stop = out["hooks"]["Stop"].as_array().unwrap();
        // User entry retained, awp entry appended.
        assert!(stop
            .iter()
            .any(|e| e["hooks"][0]["command"] == "my-own-thing"));
        assert!(stop.iter().any(is_awp_entry));
    }

    #[test]
    fn hook_command_gates_on_tmux_and_honors_awp_bin() {
        let cmd = hook_command("PreToolUse", "working");
        assert!(cmd.contains("$TMUX"));
        assert!(cmd.contains("${AWP_BIN:-awp}"));
        assert!(cmd.contains("--waiting-when-tool AskUserQuestion"));
        let ups = hook_command("UserPromptSubmit", "working");
        assert!(ups.contains("--prompt-stdin"));
    }
}
