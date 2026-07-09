//! gh PR orchestration: open / merge / view.
//!
//! Pure `gh` argument builders (unit-tested) plus thin execute wrappers used by
//! the deck's `p …` PR menu. Run in the workspace directory so `gh` resolves
//! the repo from the checkout.

use crate::subprocess::run;
use anyhow::Result;

/// `gh pr view <number> --web` — open the PR in a browser.
pub fn open_web_args(number: u64) -> Vec<String> {
    vec![
        "pr".into(),
        "view".into(),
        number.to_string(),
        "--web".into(),
    ]
}

/// `gh pr merge <number> --squash` — squash-merge the PR.
pub fn merge_squash_args(number: u64) -> Vec<String> {
    vec![
        "pr".into(),
        "merge".into(),
        number.to_string(),
        "--squash".into(),
    ]
}

/// `gh pr view <number>` — print the PR description (piped to a pager in a
/// window by the caller).
pub fn view_args(number: u64) -> Vec<String> {
    vec!["pr".into(), "view".into(), number.to_string()]
}

/// Open the PR in a browser (best-effort; needs `gh` auth).
pub fn open_web(dir: &str, number: u64) -> Result<()> {
    let args = open_web_args(number);
    let arg_refs: Vec<&str> = args.iter().map(String::as_str).collect();
    run(dir, "gh", &arg_refs).map(|_| ())
}

/// Squash-merge the PR.
pub fn merge_squash(dir: &str, number: u64) -> Result<String> {
    let args = merge_squash_args(number);
    let arg_refs: Vec<&str> = args.iter().map(String::as_str).collect();
    run(dir, "gh", &arg_refs)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn gh_shapes() {
        assert_eq!(open_web_args(42), vec!["pr", "view", "42", "--web"]);
        assert_eq!(merge_squash_args(42), vec!["pr", "merge", "42", "--squash"]);
        assert_eq!(view_args(42), vec!["pr", "view", "42"]);
    }
}
