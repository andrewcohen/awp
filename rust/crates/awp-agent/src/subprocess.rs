//! Thin subprocess orchestration for `jj` and `gh`.
//!
//! Language-agnostic, same shape as the Go version: shell out, capture stdout,
//! surface errors. The deck's fast path never calls these — they run in the
//! background to enrich the in-RAM roster.

use anyhow::{Context, Result};
use std::process::Command;

/// Run a command in `dir` and return trimmed stdout. A non-zero exit is an
/// error carrying stderr.
pub fn run(dir: &str, program: &str, args: &[&str]) -> Result<String> {
    let mut cmd = Command::new(program);
    cmd.args(args);
    if !dir.is_empty() {
        cmd.current_dir(dir);
    }
    let out = cmd.output().with_context(|| format!("spawn {program}"))?;
    if !out.status.success() {
        let stderr = String::from_utf8_lossy(&out.stderr);
        anyhow::bail!("{program} exited {}: {}", out.status, stderr.trim());
    }
    Ok(String::from_utf8_lossy(&out.stdout).trim().to_string())
}

/// Whether the process is running inside an awp-managed session. Integrations
/// are **no-ops outside awp sessions** (must-preserve behavior). We treat "in a
/// tmux/zmx session AND `AWP_WORKSPACE` is set" as the signal, matching the
/// Go hook's `$TMUX` gate plus env identity.
pub fn in_awp_session() -> bool {
    let has_mux = std::env::var_os("TMUX").is_some() || std::env::var_os("ZMX").is_some();
    let has_ident = std::env::var("AWP_WORKSPACE")
        .map(|v| !v.trim().is_empty())
        .unwrap_or(false);
    has_mux && has_ident
}

/// The awp binary to invoke from hooks — honors `$AWP_BIN`, falling back to
/// `awp` on `$PATH`. Mirrors the Go `${AWP_BIN:-awp}` convention.
pub fn awp_bin() -> String {
    std::env::var("AWP_BIN")
        .ok()
        .filter(|v| !v.trim().is_empty())
        .unwrap_or_else(|| "awp".to_string())
}

/// List `jj` bookmarks (local) in `dir`. Best-effort background enrichment.
pub fn jj_bookmarks(dir: &str) -> Result<Vec<String>> {
    let out = run(dir, "jj", &["bookmark", "list", "-T", "name ++ \"\\n\""])?;
    Ok(out
        .lines()
        .map(|l| l.trim().to_string())
        .filter(|l| !l.is_empty())
        .collect())
}

/// Whether the `gh` CLI is available on PATH.
pub fn gh_available() -> bool {
    Command::new("gh")
        .arg("--version")
        .output()
        .map(|o| o.status.success())
        .unwrap_or(false)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn run_captures_stdout() {
        let out = run("", "printf", &["hello"]).unwrap();
        assert_eq!(out, "hello");
    }

    #[test]
    fn run_surfaces_nonzero_exit() {
        assert!(run("", "sh", &["-c", "exit 3"]).is_err());
    }

    #[test]
    fn awp_bin_defaults_to_awp() {
        // Note: relies on AWP_BIN being unset in the test env.
        if std::env::var_os("AWP_BIN").is_none() {
            assert_eq!(awp_bin(), "awp");
        }
    }
}
