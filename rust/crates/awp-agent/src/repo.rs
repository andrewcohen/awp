//! Repo-root resolution.
//!
//! Ports the Go `json_store.go::canonicalizeRepoRoot`: a jj *secondary*
//! workspace directory points at its source repo via a `.jj/repo` pointer file;
//! awp keys workspaces by the canonical source root so a workspace and the repo
//! it was cut from share one project. Must-preserve behavior.

use std::path::{Path, PathBuf};

/// Resolve a possibly-secondary jj workspace dir to its canonical repo root by
/// following the `<path>/.jj/repo` pointer. Returns the input (absolutized +
/// cleaned) unchanged when there is no pointer.
pub fn canonicalize_repo_root(path: &str) -> String {
    let abs = absolutize(path);
    let pointer_file = abs.join(".jj").join("repo");
    let Ok(raw) = std::fs::read_to_string(&pointer_file) else {
        return abs.to_string_lossy().into_owned();
    };
    let pointer = raw.trim();
    if pointer.is_empty() {
        return abs.to_string_lossy().into_owned();
    }
    let mut resolved = if Path::new(pointer).is_absolute() {
        PathBuf::from(pointer)
    } else {
        abs.join(".jj").join(pointer)
    };
    resolved = normalize(&resolved);
    // `<root>/.jj/repo` → `<root>`.
    if resolved.file_name().and_then(|s| s.to_str()) == Some("repo")
        && resolved
            .parent()
            .and_then(|p| p.file_name())
            .and_then(|s| s.to_str())
            == Some(".jj")
    {
        if let Some(root) = resolved.parent().and_then(|p| p.parent()) {
            resolved = root.to_path_buf();
        }
    }
    if resolved.as_os_str().is_empty() {
        return abs.to_string_lossy().into_owned();
    }
    resolved.to_string_lossy().into_owned()
}

fn absolutize(path: &str) -> PathBuf {
    let p = PathBuf::from(path);
    let abs = if p.is_absolute() {
        p
    } else {
        std::env::current_dir().map(|cwd| cwd.join(&p)).unwrap_or(p)
    };
    normalize(&abs)
}

/// Lexical path cleaning (no filesystem touch), like Go's `filepath.Clean`.
fn normalize(path: &Path) -> PathBuf {
    let mut out: Vec<std::ffi::OsString> = Vec::new();
    for comp in path.components() {
        use std::path::Component::*;
        match comp {
            CurDir => {}
            ParentDir => {
                if matches!(out.last().map(|s| s.as_os_str()), Some(s) if s != "/") {
                    out.pop();
                }
            }
            RootDir => out.push("/".into()),
            Prefix(p) => out.push(p.as_os_str().to_os_string()),
            Normal(s) => out.push(s.to_os_string()),
        }
    }
    let mut buf = PathBuf::new();
    for c in out {
        buf.push(c);
    }
    buf
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs;

    #[test]
    fn no_pointer_returns_cleaned_input() {
        let tmp = tempfile::tempdir().unwrap();
        let root = tmp.path().join("repo");
        fs::create_dir_all(&root).unwrap();
        let got = canonicalize_repo_root(root.to_str().unwrap());
        assert_eq!(got, root.to_string_lossy());
    }

    #[test]
    fn follows_absolute_repo_pointer() {
        let tmp = tempfile::tempdir().unwrap();
        let source = tmp.path().join("source");
        let secondary = tmp.path().join("secondary");
        fs::create_dir_all(source.join(".jj")).unwrap();
        fs::create_dir_all(secondary.join(".jj")).unwrap();
        // Pointer names the source's `.jj/repo`.
        let pointer_target = source.join(".jj").join("repo");
        fs::write(
            secondary.join(".jj").join("repo"),
            pointer_target.to_string_lossy().as_bytes(),
        )
        .unwrap();
        let got = canonicalize_repo_root(secondary.to_str().unwrap());
        assert_eq!(got, source.to_string_lossy());
    }
}
