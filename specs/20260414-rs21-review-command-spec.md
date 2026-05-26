# Review Command Spec

## Metadata
- **Spec ID**: `20260414-rs21`
- **Feature name**: `awp review <pr#>`
- **Owner**: Andrew Cohen
- **Status**: Planned
- **Last updated**: 2026-05-26

## Goal
One command spins up a dedicated workspace to review a GitHub PR: branch checked out, agent primed with the PR title/body, and a `tuicr` window scoped to the PR's diff.

## User Problem
Reviewing a PR today = fetch branch, create workspace, start agent, paste description, open diff tool. Repetitive. `awp review 123` collapses that to a single step.

## Scope
### In scope (v1)
- `awp review <pr#>` command.
- Fetch PR metadata (head branch, base branch, title, body) via `gh`.
- Create a workspace from the PR's head branch, using the **same bookmark flow** as existing `awp w` bookmark-based creation.
- New tmux session with two windows:
  - `agent` — runs `pi` with a review prompt that injects PR title + body and instructs the agent to push findings into the tuicr session via `tuicr review add`.
  - `review` — runs `tuicr pr <n>`, letting tuicr own PR resolution (base/head, metadata, persisted session).
- Workspace name = branch name (mirrors current bookmark-based creation).

### Out of scope (v1)
- Configurable agent (hardcoded `pi`; configurability later).
- Custom review prompt templates.
- Agent posting comments/approvals back to GitHub.
- Draft PR handling, batch review, auto-approve.
- Cleanup / auto-destroy of review workspace.
- Fork PR support beyond whatever the bookmark flow already handles.
- Idempotency logic — if the workspace already exists, just open it; no refresh/reuse semantics.

## UX
### CLI
```
awp review 1234
```
- Same env constraints as other workspace commands.
- Errors surface actionable messages (`gh` missing, PR not found, fetch failed, base ref unknown).

### TUI
- None new. Session attaches like existing workspace creation.

## Discovery Questions (resolved)
1. **First user**: Andrew, reviewing inbound PRs.
2. **When**: replaces manual checkout-and-review flow.
3. **Output**: attached tmux session with agent + tuicr windows ready.
4. **Data sources**: `gh pr view <n> --json headRefName,baseRefName,title,body,url`; jj for bookmark/fetch.
5. **Smallest slice**: hardcoded `pi` agent, minimal prompt (title + body), base from PR metadata.
6. **Non-goals**: see Out of scope.
7. **Done**: `awp review <n>` lands in `agent` window with prompt injected; `tuicr` window shows PR diff.

## Spec Change Log
- 2026-04-14: Initial draft.
- 2026-05-26: Switch review window from `tuicr -r <base>..@` to `tuicr pr <n>` (tuicr ≥0.16.1). Agent prompt now embeds a curated subset of tuicr's SKILL.md so the agent pushes findings via `tuicr review add` with `--username "awp-agent"` instead of editing files or commenting on GitHub directly. Prompt template lives at `internal/cli/review_prompt.md` (embedded via `//go:embed`).
- 2026-05-26: Field feedback from first agent run forced two material changes: (1) `tuicr review list --repo .` returns `[]` for PR-mode sessions (tuicr stores `repo_path` as `forge:github.com/...`, no local checkout matches), so `awp review` now reads tuicr's `active_sessions.json` / `index.json` to resolve the absolute session JSON path and injects it directly into the prompt — agent passes it via `--session <abs-path>`, bypassing the broken `--repo` lookup. This is a known hack; the upstream maintainer is formalizing an agent-discovery protocol in [agavra/tuicr#368](https://github.com/agavra/tuicr/issues/368) and the workaround sites are tagged `TODO(tuicr#368)` for replacement when it ships. (2) `<baseRef>..@` was wrong on jj workspaces where HEAD doesn't track the PR head; now we pass `headRefOid` from `gh pr view` and compute `git merge-base origin/<base> <headSHA>` to bake a precise SHA range into the prompt. Window-launch order also changed: `review` window opens first so `tuicr pr <n>` has a head start writing `active_sessions.json` before we build the agent prompt.

## Implementation Plan
1. Add `internal/github` helper: `FetchPR(num int) (PRInfo, error)` via `gh pr view --json …`. Struct: `Number, HeadRef, BaseRef, Title, Body, URL`.
2. Ensure base + head refs fetched locally (`jj git fetch`); create/update bookmark for head ref by reusing the current bookmark-creation path.
3. Wire `awp review <n>` in `internal/cli`:
   - Validate tmux/jj env like other commands.
   - Fetch PR info.
   - Call existing workspace-from-bookmark creation with the head branch name.
   - Create tmux session named per `DeckSessionName(project, branch)`.
4. Window setup:
   - Window `agent`: `cd <wsPath>; pi` with prompt pre-seeded. Prompt format:
     ```
     Please review PR #<n>: <title>

     <body>

     Diff range: <base>..@
     ```
     Inject via `tmux send-keys` after the agent starts (or an agent-specific seed mechanism if one exists).
   - Window `review`: `cd <wsPath>; tuicr pr <n>`.
5. Attach / switch-client to the new session (same as `summonWorkspaceSession`).
6. Tests: unit-test PR info parsing; integration-test command wiring via Runner stubs.

## Acceptance Criteria
- [ ] `awp review <n>` with a valid PR creates a workspace named after the PR head branch.
- [ ] Agent window starts `pi` with the injected prompt containing title + body.
- [ ] `review` window runs `tuicr pr <n>`; agent prompt instructs feedback to flow through `tuicr review add`.
- [ ] Missing `gh`, unreachable PR, or failed fetch returns an actionable error.
- [ ] Behaves the same as existing bookmark workspace creation for already-fetched branches.

## QA / Human Review Test Plan
### Setup
- [ ] `gh` auth'd; `jj`, `tmux`, `pi`, `tuicr` in PATH.
- [ ] A real open PR available in the current repo.

### Core Happy Path
- [ ] `awp review <n>` against a fresh PR: workspace created, session attached, both windows present, agent shows injected prompt, `tuicr` shows diff.

### Edge Cases & Failure Modes
- [ ] Invalid PR number → clear error.
- [ ] `gh` not installed / not authed → clear error.
- [ ] PR base ref not yet fetched locally → fetched automatically (or clear error).
- [ ] Running `awp review <n>` a second time → reopens existing workspace, no crash.
- [ ] PR with empty body → prompt renders without blank-body artifacts.

### Regression Checks
- [ ] `awp w open`, `awp deck`, bookmark-based workspace creation unchanged.

### Reviewer Notes
- Capture the injected prompt verbatim and the `tuicr` command line observed.

## Validation
- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `go build ./...`
