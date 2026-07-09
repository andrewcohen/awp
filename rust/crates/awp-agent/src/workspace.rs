//! jj workspace orchestration: create / rename / delete.
//!
//! Ports the Go `internal/jj` + `internal/workspace` command shapes. The pure
//! argument builders and the managed-path scheme are unit-tested; the `execute`
//! wrappers run them via `jj` and are driven by the deck's effect executor
//! (never on the fast path).

use crate::subprocess::run;
use anyhow::{bail, Result};
use std::path::PathBuf;

/// Normalize a workspace/bookmark name into a filesystem-safe slug, mirroring
/// the Go `NormalizeName`: lowercase, spaces/slashes → `-`, collapse repeats,
/// trim leading/trailing `-`.
pub fn normalize_name(raw: &str) -> String {
    let mut out = String::with_capacity(raw.len());
    let mut prev_dash = false;
    for ch in raw.trim().chars() {
        let c = ch.to_ascii_lowercase();
        let mapped = if c.is_ascii_alphanumeric() || c == '.' || c == '_' {
            prev_dash = false;
            c
        } else {
            if prev_dash {
                continue;
            }
            prev_dash = true;
            '-'
        };
        out.push(mapped);
    }
    out.trim_matches('-').to_string()
}

/// The managed base for created workspaces: `~/.awp/workspaces`.
fn managed_base() -> PathBuf {
    match std::env::var_os("HOME").filter(|h| !h.is_empty()) {
        Some(home) => PathBuf::from(home).join(".awp").join("workspaces"),
        None => PathBuf::from(".awp").join("workspaces"),
    }
}

/// The default on-disk path for a workspace. `default` maps to the repo root
/// itself; everything else lives at `~/.awp/workspaces/<repo>/<name>`. Mirrors
/// Go `defaultWorkspacePath` + `repoWorkspaceBase`.
pub fn workspace_path(repo_root: &str, name: &str) -> PathBuf {
    if name.trim() == "default" {
        return PathBuf::from(repo_root);
    }
    let repo = repo_basename(repo_root);
    managed_base().join(repo).join(name)
}

fn repo_basename(repo_root: &str) -> String {
    let base = repo_root
        .trim_end_matches('/')
        .rsplit('/')
        .next()
        .unwrap_or(repo_root);
    let n = normalize_name(base);
    if n.is_empty() {
        "repo".to_string()
    } else {
        n
    }
}

// --- pure argument builders (unit-tested) ----------------------------------

/// `jj --ignore-working-copy workspace list -T 'name ++ "\n"'`
pub fn list_args() -> Vec<String> {
    vec![
        "--ignore-working-copy".into(),
        "workspace".into(),
        "list".into(),
        "-T".into(),
        "name ++ \"\\n\"".into(),
    ]
}

/// `jj workspace add --name <name> -r <revision> <path>`
pub fn add_args(name: &str, revision: &str, path: &str) -> Vec<String> {
    vec![
        "workspace".into(),
        "add".into(),
        "--name".into(),
        name.into(),
        "-r".into(),
        revision.into(),
        path.into(),
    ]
}

/// `jj new <revision>` (run inside the workspace dir).
pub fn new_on_revision_args(revision: &str) -> Vec<String> {
    vec!["new".into(), revision.into()]
}

/// `jj workspace rename <new_name>` (run inside the workspace dir).
pub fn rename_args(new_name: &str) -> Vec<String> {
    vec!["workspace".into(), "rename".into(), new_name.into()]
}

/// `jj --ignore-working-copy workspace forget <name>`
pub fn forget_args(name: &str) -> Vec<String> {
    vec![
        "--ignore-working-copy".into(),
        "workspace".into(),
        "forget".into(),
        name.into(),
    ]
}

// --- execute wrappers ------------------------------------------------------

/// Create a workspace named `name` from `bookmark` (a jj revision/bookmark),
/// returning the on-disk path. Runs `jj workspace add …` from the source repo
/// root; when a bookmark is given, moves the new working copy onto it.
pub fn create(repo_root: &str, name: &str, bookmark: &str) -> Result<PathBuf> {
    let name = normalize_name(name);
    if name.is_empty() {
        bail!("workspace name is empty after normalization");
    }
    let path = workspace_path(repo_root, &name);
    if let Some(parent) = path.parent() {
        std::fs::create_dir_all(parent)?;
    }
    let revision = if bookmark.trim().is_empty() {
        "@".to_string()
    } else {
        bookmark.trim().to_string()
    };
    let path_str = path.to_string_lossy().to_string();
    let args = add_args(&name, &revision, &path_str);
    let arg_refs: Vec<&str> = args.iter().map(String::as_str).collect();
    run(repo_root, "jj", &arg_refs)?;
    // Start a fresh change on top of the bookmark so edits don't move it.
    if !bookmark.trim().is_empty() {
        let na = new_on_revision_args(bookmark.trim());
        let na_refs: Vec<&str> = na.iter().map(String::as_str).collect();
        let _ = run(&path_str, "jj", &na_refs);
    }
    Ok(path)
}

/// Rename a workspace in place (`jj workspace rename` run from its dir).
pub fn rename(workspace_dir: &str, new_name: &str) -> Result<String> {
    let new_name = normalize_name(new_name);
    if new_name.is_empty() {
        bail!("new workspace name is empty after normalization");
    }
    let args = rename_args(&new_name);
    let arg_refs: Vec<&str> = args.iter().map(String::as_str).collect();
    run(workspace_dir, "jj", &arg_refs)?;
    Ok(new_name)
}

/// Forget a workspace (`jj workspace forget`) and remove its managed directory.
/// Never touches the source repo root (the `default` workspace).
pub fn delete(repo_root: &str, name: &str) -> Result<()> {
    let args = forget_args(name);
    let arg_refs: Vec<&str> = args.iter().map(String::as_str).collect();
    // Forget is best-effort: a workspace jj already dropped shouldn't block the
    // directory cleanup.
    let _ = run(repo_root, "jj", &arg_refs);
    let path = workspace_path(repo_root, name);
    if name.trim() != "default" && path.starts_with(managed_base()) && path.exists() {
        std::fs::remove_dir_all(&path)?;
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn normalize_slugifies() {
        assert_eq!(normalize_name("Feature/Foo Bar"), "feature-foo-bar");
        assert_eq!(normalize_name("  --Hello--  "), "hello");
        assert_eq!(normalize_name("keep.dots_and_1"), "keep.dots_and_1");
        assert_eq!(normalize_name("///"), "");
    }

    #[test]
    fn default_maps_to_repo_root() {
        assert_eq!(
            workspace_path("/repos/alpha", "default"),
            PathBuf::from("/repos/alpha")
        );
    }

    #[test]
    fn managed_path_uses_repo_basename() {
        let p = workspace_path("/repos/alpha", "feature");
        assert!(p.ends_with("workspaces/alpha/feature"), "got {p:?}");
    }

    #[test]
    fn add_args_match_go_shape() {
        assert_eq!(
            add_args("qa", "feature/bookmark", "/tmp/qa"),
            vec![
                "workspace",
                "add",
                "--name",
                "qa",
                "-r",
                "feature/bookmark",
                "/tmp/qa"
            ]
        );
    }

    #[test]
    fn list_and_forget_and_rename_shapes() {
        assert_eq!(
            list_args(),
            vec![
                "--ignore-working-copy",
                "workspace",
                "list",
                "-T",
                "name ++ \"\\n\""
            ]
        );
        assert_eq!(
            forget_args("qa"),
            vec!["--ignore-working-copy", "workspace", "forget", "qa"]
        );
        assert_eq!(rename_args("qa2"), vec!["workspace", "rename", "qa2"]);
    }
}
