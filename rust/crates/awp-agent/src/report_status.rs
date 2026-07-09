//! `awp report-status` — an agent hook writes a workspace's status to the store.
//!
//! Ports the Go `internal/cli/report_status.go`, with the whole-file JSON
//! rewrite replaced by a partial, row-level SQLite `UPDATE`. Must-preserve
//! behaviors:
//!
//! - Closed set of reportable states (working/idle/waiting/exited).
//! - `--prompt` / `--prompt-stdin` (Claude payload `prompt` field) /
//!   `--waiting-when-tool` (PreToolUse `tool_name` → override to waiting).
//! - Resolve the workspace via `$AWP_WORKSPACE` + (`$AWP_REPO_ROOT` |
//!   `$AWP_REPO`); no-op silently when identity is missing or the row is
//!   unknown, so a misconfigured hook never breaks an agent turn.
//! - ActivePrompt lifecycle: non-empty prompt overwrites; idle/exited clears;
//!   working/waiting leave it. Unread: exited clears; waiting/idle set (unless
//!   viewing); working preserves.

use anyhow::{bail, Result};
use awp_core::{Status, WorkspaceId};
use awp_store::Store;
use std::io::Read;

/// Parsed `report-status` arguments.
#[derive(Debug, Default, PartialEq, Eq)]
pub struct ReportArgs {
    pub state: String,
    pub prompt: Option<String>,
    pub prompt_stdin: bool,
    pub waiting_tools: Vec<String>,
}

/// Workspace identity from the environment (or tmux fallback, resolved by the
/// caller).
#[derive(Debug, Default, Clone)]
pub struct Ident {
    pub workspace: String,
    pub repo: String,
    pub repo_root: String,
}

impl Ident {
    /// Resolve from the process environment. Mirrors Go `resolveWorkspaceIdent`
    /// (minus the tmux `show-environment` fallback, which the deck's own env
    /// injection makes unnecessary in the common case).
    pub fn from_env() -> Self {
        let get = |k: &str| std::env::var(k).unwrap_or_default().trim().to_string();
        Ident {
            workspace: get("AWP_WORKSPACE"),
            repo: get("AWP_REPO"),
            repo_root: get("AWP_REPO_ROOT"),
        }
    }
}

/// Parse the CLI args. Unknown flags are an error (matching Go).
pub fn parse_args(args: &[String]) -> Result<ReportArgs> {
    let mut out = ReportArgs::default();
    let mut i = 0;
    while i < args.len() {
        let arg = args[i].as_str();
        match arg {
            "--state" => {
                i += 1;
                out.state = args
                    .get(i)
                    .cloned()
                    .ok_or_else(|| anyhow::anyhow!("--state requires a value"))?;
            }
            _ if arg.starts_with("--state=") => {
                out.state = arg.trim_start_matches("--state=").to_string();
            }
            "--prompt" => {
                i += 1;
                out.prompt = Some(
                    args.get(i)
                        .cloned()
                        .ok_or_else(|| anyhow::anyhow!("--prompt requires a value"))?,
                );
            }
            _ if arg.starts_with("--prompt=") => {
                out.prompt = Some(arg.trim_start_matches("--prompt=").to_string());
            }
            "--prompt-stdin" => out.prompt_stdin = true,
            "--waiting-when-tool" => {
                i += 1;
                let v = args
                    .get(i)
                    .ok_or_else(|| anyhow::anyhow!("--waiting-when-tool requires a value"))?;
                out.waiting_tools = parse_tool_list(v);
            }
            _ if arg.starts_with("--waiting-when-tool=") => {
                out.waiting_tools = parse_tool_list(arg.trim_start_matches("--waiting-when-tool="));
            }
            other => bail!("unknown argument {other:?}"),
        }
        i += 1;
    }
    Ok(out)
}

fn parse_tool_list(s: &str) -> Vec<String> {
    s.split(',')
        .map(str::trim)
        .filter(|s| !s.is_empty())
        .map(str::to_string)
        .collect()
}

/// Extract the `prompt` field from a Claude UserPromptSubmit payload.
pub fn read_prompt_from_bytes(data: &[u8]) -> Option<String> {
    if data.is_empty() {
        return None;
    }
    let v: serde_json::Value = serde_json::from_slice(data).ok()?;
    let p = v.get("prompt")?.as_str()?;
    (!p.is_empty()).then(|| p.to_string())
}

/// Extract `tool_name` from a Claude PreToolUse payload.
pub fn read_tool_name_from_bytes(data: &[u8]) -> Option<String> {
    if data.is_empty() {
        return None;
    }
    let v: serde_json::Value = serde_json::from_slice(data).ok()?;
    let t = v.get("tool_name")?.as_str()?.trim();
    (!t.is_empty()).then(|| t.to_string())
}

/// Run `report-status`: parse args (+stdin), resolve identity, and apply a
/// partial status write. Returns the workspace id that was written, or `None`
/// when it was a silent no-op (missing identity / unknown row / invalid state).
///
/// `viewing` is whether the user is currently attached to the session — passed
/// by the caller (false in v1; the badge-suppression query is a follow-up). A
/// bad state string is an error; everything else degrades to `Ok(None)`.
pub fn run(
    store: &Store,
    args: &ReportArgs,
    stdin: &mut dyn Read,
    ident: &Ident,
    viewing: bool,
) -> Result<Option<WorkspaceId>> {
    let mut state = args.state.trim().to_ascii_lowercase();
    if state.is_empty() {
        bail!("--state is required");
    }
    // Validate against the closed set of raw strings (not via `Status::parse`,
    // which folds unknown strings to Idle). Matches Go `validReportStates`.
    if !matches!(state.as_str(), "working" | "idle" | "waiting" | "exited") {
        bail!("invalid --state {state:?} (want working|idle|waiting|exited)");
    }

    let mut prompt = args.prompt.clone();
    // Read stdin once and route both extractions through it.
    if args.prompt_stdin || !args.waiting_tools.is_empty() {
        let mut buf = Vec::new();
        // Best-effort: a malformed payload never breaks the agent turn.
        let _ = stdin.read_to_end(&mut buf);
        if args.prompt_stdin {
            if let Some(p) = read_prompt_from_bytes(&buf) {
                prompt = Some(p);
            }
        }
        if !args.waiting_tools.is_empty() {
            if let Some(tool) = read_tool_name_from_bytes(&buf) {
                if args.waiting_tools.iter().any(|t| t == &tool) {
                    state = "waiting".to_string();
                }
            }
        }
    }
    let status = Status::parse(&state);
    let prompt = prompt.map(|p| p.trim().to_string());

    if ident.workspace.is_empty() {
        return Ok(None);
    }

    let Some((id, current_unread)) = resolve(store, ident)? else {
        return Ok(None);
    };

    // ActivePrompt lifecycle.
    let prompt_write: Option<String> = match prompt.as_deref() {
        Some(p) if !p.is_empty() => Some(p.to_string()),
        _ if status.is_exited() || status == Status::Idle => Some(String::new()),
        _ => None,
    };
    // Unread lifecycle.
    let unread = if status.is_exited() {
        false
    } else if status.wants_attention() {
        !viewing
    } else {
        current_unread
    };

    store.update_status(&id, status, prompt_write.as_deref(), unread)?;
    Ok(Some(id))
}

/// Resolve identity to a stored `(WorkspaceId, current_unread)`. Prefers an
/// exact repo-root match; falls back to a repo-basename lookup with the same
/// collision handling as Go (exactly-one match wins; ambiguity no-ops).
fn resolve(store: &Store, ident: &Ident) -> Result<Option<(WorkspaceId, bool)>> {
    if !ident.repo_root.is_empty() {
        let root = crate::repo::canonicalize_repo_root(&ident.repo_root);
        let id = WorkspaceId::new(root, ident.workspace.clone());
        return Ok(store.get_workspace(&id)?.map(|w| (id, w.unread)));
    }
    if ident.repo.is_empty() {
        return Ok(None);
    }
    // Basename fallback.
    let mut candidates: Vec<String> = store
        .repo_roots()?
        .into_iter()
        .filter(|root| basename(root) == ident.repo)
        .collect();
    candidates.sort();
    let mut matches: Vec<WorkspaceId> = Vec::new();
    for root in &candidates {
        let id = WorkspaceId::new(root.clone(), ident.workspace.clone());
        if store.get_workspace(&id)?.is_some() {
            matches.push(id);
        }
    }
    match matches.len() {
        0 => Ok(None), // Row not yet created — nothing to write.
        1 => {
            let id = matches.into_iter().next().unwrap();
            let unread = store.get_workspace(&id)?.map(|w| w.unread).unwrap_or(false);
            Ok(Some((id, unread)))
        }
        _ => Ok(None), // Ambiguous basename collision — drop rather than misroute.
    }
}

fn basename(path: &str) -> String {
    path.trim_end_matches('/')
        .rsplit('/')
        .next()
        .unwrap_or(path)
        .to_string()
}

#[cfg(test)]
mod tests {
    use super::*;
    use awp_core::Workspace;
    use std::io::Cursor;

    fn store_with(repo: &str, name: &str, status: Status, unread: bool, prompt: &str) -> Store {
        let store = Store::open_in_memory().unwrap();
        store
            .upsert_workspace(&Workspace {
                repo_root: repo.into(),
                name: name.into(),
                path: format!("{repo}/{name}"),
                status,
                unread,
                active_prompt: (!prompt.is_empty()).then(|| prompt.to_string()),
                ..Default::default()
            })
            .unwrap();
        store
    }

    fn ident(repo_root: &str, workspace: &str) -> Ident {
        Ident {
            workspace: workspace.into(),
            repo: String::new(),
            repo_root: repo_root.into(),
        }
    }

    #[test]
    fn waiting_sets_unread_and_writes_prompt() {
        let store = store_with("/r", "w", Status::Idle, false, "");
        let args = ReportArgs {
            state: "waiting".into(),
            prompt: Some("confirm?".into()),
            ..Default::default()
        };
        let id = run(
            &store,
            &args,
            &mut Cursor::new(b""),
            &ident("/r", "w"),
            false,
        )
        .unwrap()
        .unwrap();
        let w = store.get_workspace(&id).unwrap().unwrap();
        assert_eq!(w.status, Status::Waiting);
        assert!(w.unread);
        assert_eq!(w.active_prompt.as_deref(), Some("confirm?"));
    }

    #[test]
    fn working_preserves_existing_unread_and_prompt() {
        let store = store_with("/r", "w", Status::Waiting, true, "keep");
        let args = ReportArgs {
            state: "working".into(),
            ..Default::default()
        };
        let id = run(
            &store,
            &args,
            &mut Cursor::new(b""),
            &ident("/r", "w"),
            false,
        )
        .unwrap()
        .unwrap();
        let w = store.get_workspace(&id).unwrap().unwrap();
        assert_eq!(w.status, Status::Working);
        assert!(w.unread, "working preserves prior unread");
        assert_eq!(w.active_prompt.as_deref(), Some("keep"));
    }

    #[test]
    fn idle_clears_prompt() {
        let store = store_with("/r", "w", Status::Working, false, "midtask");
        let args = ReportArgs {
            state: "idle".into(),
            ..Default::default()
        };
        let id = run(
            &store,
            &args,
            &mut Cursor::new(b""),
            &ident("/r", "w"),
            false,
        )
        .unwrap()
        .unwrap();
        let w = store.get_workspace(&id).unwrap().unwrap();
        assert_eq!(w.active_prompt, None);
    }

    #[test]
    fn exited_clears_unread() {
        let store = store_with("/r", "w", Status::Waiting, true, "x");
        let args = ReportArgs {
            state: "exited".into(),
            ..Default::default()
        };
        let id = run(
            &store,
            &args,
            &mut Cursor::new(b""),
            &ident("/r", "w"),
            false,
        )
        .unwrap()
        .unwrap();
        let w = store.get_workspace(&id).unwrap().unwrap();
        assert!(!w.unread);
    }

    #[test]
    fn viewing_suppresses_unread() {
        let store = store_with("/r", "w", Status::Idle, false, "");
        let args = ReportArgs {
            state: "waiting".into(),
            ..Default::default()
        };
        let id = run(
            &store,
            &args,
            &mut Cursor::new(b""),
            &ident("/r", "w"),
            true,
        )
        .unwrap()
        .unwrap();
        assert!(!store.get_workspace(&id).unwrap().unwrap().unread);
    }

    #[test]
    fn prompt_stdin_extracts_from_payload() {
        let store = store_with("/r", "w", Status::Idle, false, "");
        let args = ReportArgs {
            state: "working".into(),
            prompt_stdin: true,
            ..Default::default()
        };
        let payload = br#"{"prompt":"do the thing"}"#;
        let id = run(
            &store,
            &args,
            &mut Cursor::new(payload),
            &ident("/r", "w"),
            false,
        )
        .unwrap()
        .unwrap();
        assert_eq!(
            store
                .get_workspace(&id)
                .unwrap()
                .unwrap()
                .active_prompt
                .as_deref(),
            Some("do the thing")
        );
    }

    #[test]
    fn waiting_when_tool_overrides_state() {
        let store = store_with("/r", "w", Status::Working, false, "");
        let args = ReportArgs {
            state: "working".into(),
            waiting_tools: vec!["AskUserQuestion".into()],
            ..Default::default()
        };
        let payload = br#"{"tool_name":"AskUserQuestion"}"#;
        let id = run(
            &store,
            &args,
            &mut Cursor::new(payload),
            &ident("/r", "w"),
            false,
        )
        .unwrap()
        .unwrap();
        assert_eq!(
            store.get_workspace(&id).unwrap().unwrap().status,
            Status::Waiting
        );
    }

    #[test]
    fn missing_identity_is_silent_noop() {
        let store = store_with("/r", "w", Status::Idle, false, "");
        let args = ReportArgs {
            state: "working".into(),
            ..Default::default()
        };
        let empty = Ident::default();
        assert!(run(&store, &args, &mut Cursor::new(b""), &empty, false)
            .unwrap()
            .is_none());
    }

    #[test]
    fn unknown_row_is_silent_noop() {
        let store = store_with("/r", "w", Status::Idle, false, "");
        let args = ReportArgs {
            state: "working".into(),
            ..Default::default()
        };
        assert!(run(
            &store,
            &args,
            &mut Cursor::new(b""),
            &ident("/r", "nope"),
            false
        )
        .unwrap()
        .is_none());
    }

    #[test]
    fn invalid_state_is_error() {
        let store = store_with("/r", "w", Status::Idle, false, "");
        let args = ReportArgs {
            state: "bananas".into(),
            ..Default::default()
        };
        assert!(run(
            &store,
            &args,
            &mut Cursor::new(b""),
            &ident("/r", "w"),
            false
        )
        .is_err());
    }

    #[test]
    fn basename_fallback_resolves_unique_match() {
        let store = Store::open_in_memory().unwrap();
        store
            .upsert_workspace(&Workspace {
                repo_root: "/home/me/proj".into(),
                name: "w".into(),
                path: "/home/me/proj/w".into(),
                ..Default::default()
            })
            .unwrap();
        let args = ReportArgs {
            state: "working".into(),
            ..Default::default()
        };
        let id_env = Ident {
            workspace: "w".into(),
            repo: "proj".into(),
            repo_root: String::new(),
        };
        let id = run(&store, &args, &mut Cursor::new(b""), &id_env, false)
            .unwrap()
            .unwrap();
        assert_eq!(id.repo_root, "/home/me/proj");
    }

    #[test]
    fn parse_args_rejects_unknown_flag() {
        assert!(parse_args(&["--bogus".to_string()]).is_err());
    }
}
