# Feature Spec: stale re-review comment migration

## Metadata
- **Spec ID**: `20260713-9p1y`
- **Feature name**: stale re-review comment migration
- **Owner**: acohen
- **Status**: In Progress
- **Last updated**: 2026-07-13

## Goal
When a PR's head moves after it was reviewed (force-push / rebase) and the
user re-runs `awp review`, the tuicr review pane should show the current
head's diff *and* the draft comments from the prior review — not a stale
diff or an empty session.

## User Problem
A tuicr review session's identity is `(repo, PR number, head_sha)`. Draft
comments live inside the session JSON for one specific head; there is no
migration between heads. So after a force-push:

1. `awp review` re-run **reuses** the existing tuicr window
   (`have["review"]` guard in `runReviewOpts`), so the pane keeps showing
   the old head's diff.
2. When tuicr *does* re-resolve the current head (a `:e` reload, or a fresh
   `tuicr pr`), it opens a new, empty session for the new head — the prior
   review's draft comments are stranded in the old-head session file.
3. `tuicr review list` collapses to one entry per PR slug and hides the
   other on-disk sessions, so neither the user nor an agent can discover
   the stranded comments through the CLI.

Net: the user re-reviews a force-pushed PR and either sees a stale diff or
loses the earlier draft comments.

## Scope
### In scope (v1)
- Detect a **re-head**: the live tuicr session's `pr_session_key.head_sha`
  differs from the freshly-fetched `pr.HeadSHA`.
- On re-head, **reset** the `review` tmux window (kill + relaunch
  `tuicr pr <n>`) so tuicr opens the current-head session.
- **Discover** prior-head sessions for the PR that still hold comments, by
  scanning tuicr's `reviews/sessions/*.json` (the CLI can't surface them).
- **Delegate the comment move to the agent** via the review prompt: hand it
  the prior session path(s) and instruct it to re-anchor and re-post the
  drafts into the current-head session.

### Out of scope (v1)
- awp performing the re-anchoring itself in Go (kept in the agent; awp only
  locates the sessions).
- Any change to tuicr itself (the durable fix — tuicr carrying comments
  forward on head change — is tracked upstream, agavra/tuicr#368).
- Migrating *published* GitHub comments (only tuicr-local drafts are at
  risk; published ones are re-surfaced through the existing `{{comments}}`
  block).
- Preserving in-flight text a user typed in the TUI but that tuicr has not
  persisted to the session JSON.

## UX
### CLI
- `awp review <n>` (and the deck `ActionReview`) on a stale PR now logs the
  detected re-head, resets the review pane, and — when prior drafts exist —
  logs how many are being carried forward.

### TUI
- No new keys. The re-review action (already how the deck triggers a fresh
  pass) gains the reset + migration behavior transparently.

## Implementation Steps
1. **Helpers** (`internal/cli/tuicr_session.go`):
   - `readSessionHeadSHA(path) string` — read `pr_session_key.head_sha`.
   - `findPriorSessionsWithComments(dataDir, prNumber, currentHead)` —
     scan `reviews/sessions/*.json`, return other-head sessions for the PR
     that hold at least one comment (review-level, file, or line), newest
     first.
2. **Prompt plumbing** (`buildReviewPrompt` + `review_prompt.md`): add a
   `{{prior_sessions}}` token and a "Carrying forward comments from a prior
   head" section.
3. **Wiring** (`runReviewOpts`): before the window-setup block, read the
   live session head; if it differs from `pr.HeadSHA` and the review window
   exists, reset it. Feed discovered prior sessions into the prompt.
4. **Docs**: README review section.

## Acceptance Criteria
- Re-running review on a force-pushed PR relaunches tuicr on the new head.
- The generated prompt file names the prior session path(s) when drafts
  exist, and instructs the agent to migrate them.
- No re-head → no reset, no prior-session block (behavior unchanged).
- Helpers degrade to empty (never panic) on missing/malformed session files.

## QA Plan
- Unit tests for both helpers (head read; discovery incl. no-match,
  other-PR, same-head-excluded, malformed-skipped, comment-in-line-vs-review
  variants).
- `buildReviewPrompt` test asserting the prior-sessions block renders and
  the sentinel appears when empty.
- Manual: force-push a test PR, `awp review`, confirm pane resets and the
  prompt file lists the prior session.

## Spec Change Log
- 2026-07-13: Initial draft.
