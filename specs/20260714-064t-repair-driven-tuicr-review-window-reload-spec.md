# Feature Spec: repair-driven tuicr review-window reload

## Metadata
- **Spec ID**: `20260714-064t`
- **Feature name**: repair-driven tuicr review-window reload
- **Owner**: acohen
- **Status**: Implemented
- **Last updated**: 2026-07-14

## Goal
When you re-review a stale PR via the deck's `p r` (PR repair) action, the
tuicr review window should reload onto the PR's current head *before* the
agent receives the repair prompt, and the prompt should name the new tuicr
session so the agent posts its re-review into the session the window is
actually showing — not a stale one.

## User Problem
The reviewer, mid-review of someone else's PR. The workflow today:

1. An earlier `awp review <n>` (`ActionReview` → `runReviewOpts`) sets up
   the tuicr review tmux window on head `H1` and launches the review agent.
2. The author pushes; origin head is now `H2`. The deck's PR-status refresh
   surfaces the PR as stale / re-review-requested.
3. The reviewer hits **`p r`** (PR menu → repair). `prRepairPrompt` builds
   the reviewer-tone prompt ("`jj git fetch` … `jj new <branch>@origin` …
   re-read your earlier feedback, report findings"), drops it in the prompt
   form, and on submit `dispatchPromptForm` fires **`ActionSendPrompt`** —
   pushing the text straight to the already-running agent.

The bug: that `p r` → `ActionSendPrompt` path **never touches tuicr**
(`model.go:2701`). So:

- The tuicr review window stays pinned to `H1` — the reviewer is looking at
  a stale diff.
- Even after a manual `:e`/relaunch, tuicr opens a *new, empty* session for
  `H2`; the agent's prompt says nothing about it, so the agent doesn't know
  which session to post comments into, and prior-head drafts sit stranded
  (the same stranding the `20260713-9p1y` spec fixed — but only on the
  `runReviewOpts` review path, not this repair path).

Net: re-reviewing via repair leaves a stale window and an agent talking to
the wrong (or no) tuicr session.

Note the timing distinction from an *owner* repair: for a reviewer repair
the origin head has **already** moved when `p r` is pressed (that stale
state is what surfaces the repair in the first place), so the reload is
synchronous at submit — no async push-completion detection needed. Owner
repair (agent pushes new commits *later*) is explicitly out of scope here.

## Scope
### In scope (v1)
- On submit of a **repair-originated** prompt (from `p r`), when the
  workspace has a live tuicr `review` window whose session head differs from
  the PR's current head:
  1. **Reload the tuicr review window** (kill + relaunch `tuicr pr <n>`) so
     it reopens on the current head — reusing the reset logic already in
     `runReviewOpts` (`review.go:226-237`).
  2. **Resolve the new session path** and discover prior-head sessions that
     still hold draft comments (`readSessionHeadSHA` /
     `findPriorSessionsWithComments` in `tuicr_session.go`).
  3. **Splice a tuicr block into the repair prompt** naming the new session
     path (and any prior-session paths to carry drafts forward), then
  4. dispatch `ActionSendPrompt` — the agent receives the message *after*
     the window has reloaded.
- Ordering guarantee: window reload completes before the agent message is
  sent.

### Out of scope (v1)
- **Owner repair** (`mine == true`): the agent pushes commits later, so
  there is no already-stale window at submit time. Covered by the deferred
  async-detection idea, not here.
- Changing `runReviewOpts`' own reset/migration behavior (already shipped in
  `20260713-9p1y`); we reuse its helpers, we don't alter them.
- Any change to tuicr itself (durable carry-forward tracked upstream at
  `agavra/tuicr#368`).
- Reloading when no tuicr `review` window exists (nothing to reload — the
  prompt sends unchanged).

## UX
### CLI
- No new top-level command. A new internal action reloads the review window;
  it logs "Reset review window (PR head moved since last open)" and, when
  prior drafts exist, how many are being carried forward — mirroring the
  existing `runReviewOpts` reporter output.

### TUI
- No new keys. `p r` on a stale reviewer PR transparently gains the reload:
  the status line reads e.g. `repair: reloaded review on <shortSHA> · sent`
  after submit. Cancel (`esc`) does nothing new.

## Discovery Questions
1. **First user?** The reviewer re-reviewing a teammate's stale PR from the deck.
2. **When?** On `p r` submit for a `!mine` PR that has a live-but-stale tuicr review window.
3. **Exact result?** tuicr window on current head + agent prompt naming the new session (and prior-draft sessions) + drafts carried forward.
4. **Data sources?** `pr.HeadSHA` (PR status), `readSessionHeadSHA` (session JSON), `findPriorSessionsWithComments` (session store), tmux window state.
5. **Smallest useful slice?** Reload the window + append the resolved session path to the repair prompt. Draft carry-forward reuses existing helpers, so it comes nearly free.
6. **Non-goals?** Owner-repair async case; tuicr-internal carry-forward; touching the shipped review-path reset.
7. **Done?** `p r` re-review reloads the window before the agent message, and the message names the live session.

## Implementation Plan
1. **Carry repair context on the prompt form.** `newPromptForm` (or a new
   `newRepairPromptForm`) records that the prompt is repair-originated plus
   the PR number, repo root, and `pr.HeadSHA`, so `dispatchPromptForm` can
   decide whether to reload. Set it in `modal_prmenu.go:92-94`.
2. **New action `ActionReloadReview`** (deck → CLI handler): given the
   workspace + PR, run the existing reset block (guarded on `have["review"]`
   and `liveHead != pr.HeadSHA`), relaunch `tuicr pr <n>`, then return the
   resolved current-head session path and prior-session paths. Factor the
   reset/resolve out of `runReviewOpts` into a shared helper both paths call.
3. **Two-step submit.** In `dispatchPromptForm`'s `promptFormActionSubmit`
   case, when the form is repair-originated and eligible: emit the
   `ActionReloadReview` command first; on its result, append a
   `renderTuicrSessionBlock(sessionPath, priorSessions)` to the prompt and
   *then* fire `ActionSendPrompt`. Non-eligible prompts keep the current
   single-step behavior.
4. **tuicr block text.** Small renderer describing the session path and (if
   any) prior-session paths to migrate drafts from — mirror the phrasing in
   `buildReviewPrompt` / `formatPriorSessions` so the agent gets consistent
   instructions across both entry points.
5. **Docs**: README review/repair section — note that `p r` re-review
   reloads the tuicr window.

## Implementation Notes (as built)

The plan above described a two-step submit inside `dispatchPromptForm` where
a new `ActionReloadReview` returns the resolved session path to deckui, which
then appends the block and fires `ActionSendPrompt`. As built, this collapsed
into **one injected closure** because the deck's generic `Handler` signature
returns only `error` — it can't hand structured data (session path, reloaded
SHA) back to deckui, and adding a data-returning handler was more machinery
than the feature warranted.

- **deckui side** (`feat(deck): route reviewer PR-repair prompts through a
  review reloader`): `promptForm` carries repair context (`repair`,
  `prNumber`, `prHeadSHA`, `prURL`), set in `modal_prmenu.go` for a `!mine`
  PR with a PR number **that the deck flags stale** (`prStaleSuffix != ""` —
  the same signal behind the `· stale` chip). On submit, a repair-eligible
  prompt routes through a new injected `ReviewReloader` dependency
  (`WithReviewReloader`) instead of firing `ActionSendPrompt`. The reloader
  emits a `ReviewReloadedMsg` whose handler sets the status
  (`repair: reloaded review on <sha> · sent`, or the plain send status when
  nothing reloaded) and, on error, a `repair: <err>` status that never claims
  the prompt was sent.
- **cli side** (`feat(cli): reload stale tuicr review window on reviewer
  PR-repair`): `runRepairReviewReload` does reload → resolve → build block →
  send in one call (reusing the leaf helpers `resolveTuicrSessionPath`,
  `readSessionHeadSHA`, `findPriorSessionsWithComments`,
  `sessionCommentsForHead` rather than refactoring `runReviewOpts`, so the
  shipped review path is untouched). The tuicr block is rendered by
  `renderTuicrSessionBlock`, mirroring `review_prompt.md`'s session /
  carry-forward phrasing. It reloads whenever a live `review` window exists —
  the deck already decided the PR is stale, so the CLI does **not** re-derive
  staleness (see the trigger-correction change-log entry). `wantHead` is the
  deck's cached PR head (`PRStatus.HeadRefOid`) — no extra `gh` fetch — used
  only to wait for tuicr to re-anchor before naming the session.
- **Reload mechanism (revised in testing):** the reload is done *in place* by
  sending tuicr's `:e` command to the review pane
  (`SendCommand(session+":review", ":e")`), **not** by killing and relaunching
  the window as the plan's "reuse the reset logic in `runReviewOpts`" step
  implied. `:e` makes tuicr re-fetch, re-anchor onto the current head, and
  auto-migrate the prior drafts forward, while preserving the split and scroll
  position. Because the `:e` re-fetch is async, the code then waits on a new
  head-aware poll — `awaitTuicrSessionPathForHead` — until the registry
  resolves to a session whose `head_sha` equals the target head, so the block
  never names the stale-head session. (tuicr's auto-migration also means the
  current-head session usually already holds the drafts, so the
  `sessionCommentsForHead(...) == 0` gate leaves `priorSessions` empty and no
  redundant carry-forward instructions are emitted.)

Consequence for the "no live window / head unchanged" case: the reviewer
repair still routes through the reloader, which detects nothing stale and
sends the prompt unchanged. The dispatched prompt is byte-identical to the
pre-reload path; the only difference from "today" is a transient
`repair: reloading…` status that resolves to the normal `sent prompt → …`.

## Acceptance Criteria
- [ ] `p r` re-review on a stale `!mine` PR with a live review window kills +
      relaunches the tuicr window on the current head before the agent
      receives the prompt.
- [ ] The dispatched prompt names the current-head session path; when prior
      drafts exist, it names the prior session path(s) and instructs
      migration.
- [ ] No live `review` window, or head unchanged → prompt sends unchanged,
      no reload (behavior identical to today).
- [ ] Owner repair (`mine == true`) is unaffected — no reload.
- [ ] Reload failure surfaces an actionable status and does not send a prompt
      pointing at a window that failed to reload.

## QA / Human Review Test Plan
### Setup
- [ ] `jj`, `tmux`, `tuicr`, and the awp binary available in PATH.
- [ ] A test PR authored by someone else (bookmark outside the configured
      prefix) with an existing awp review workspace + tuicr window.

### Core Happy Path
- [ ] `awp review <n>` to open the tuicr window on head `H1`.
- [ ] Push a new commit to the PR (head → `H2`); let the deck refresh PR status.
- [ ] `p r` → submit. Confirm: tuicr window reloads on `H2`, then the agent
      pane receives the prompt, and the prompt text names the `H2` session
      path.
- [ ] With a draft comment left in the `H1` session, confirm the prompt names
      the `H1` session for carry-forward.

### Edge Cases & Failure Modes
- [ ] No `review` window open → prompt sends unchanged, no kill.
- [ ] Head unchanged (`liveHead == pr.HeadSHA`) → no reload.
- [ ] `mine == true` repair → no reload.
- [ ] Malformed / missing session JSON → helpers degrade to empty, prompt
      still sends (never panics).
- [ ] `KillWindow` / relaunch error → status shows the error; `ActionSendPrompt`
      is not fired.

### Regression Checks
- [ ] `awp review` (initial + its own re-head reset) unchanged.
- [ ] `A` typed-prompt form unaffected (no reload path).
- [ ] Owner-tone `p r` repair unchanged.

### Reviewer Notes
- Capture the ordering (window reload log line precedes the send-prompt
  activity) and the resolved session path embedded in the prompt file.

## Spec Change Log
- 2026-07-14: Initial draft. Corrected earlier framing (repair is not
  author-only and needs no async head detection — reviewer repair is already
  stale at submit); scoped to the synchronous reviewer case reusing the
  `20260713-9p1y` reset/migration helpers.
- 2026-07-14: Implemented. Collapsed the planned two-step
  `ActionReloadReview` + deckui block-append into a single injected
  `ReviewReloader` closure (the deck `Handler` returns only `error`, so it
  can't return the session path to deckui). All tuicr/tmux logic stays in
  `internal/cli`; deckui gains only the repair-context form fields, the
  `ReviewReloader` dependency, and the `ReviewReloadedMsg` status handler.
  See *Implementation Notes (as built)*. Status → Implemented.
- 2026-07-14: Revised the reload mechanism after live testing. The original
  build killed + relaunched the `review` window (`tuicr pr <n>`); switched to
  an in-place `:e` reload sent to the review pane, which re-anchors tuicr onto
  the current head and auto-migrates drafts without tearing down the split.
  Added `awaitTuicrSessionPathForHead` to wait for the async `:e` re-fetch to
  land on the target head before naming the session in the prompt. Verified
  end-to-end against grove PR #503. (Also note: an already-running deck keeps
  its in-memory binary — the deck must be restarted after upgrading for the
  new path to take effect.)
- 2026-07-14: Corrected the reload trigger. The first build re-derived
  staleness in the CLI by comparing the live tuicr session head to the PR
  head; this wrongly suppressed the reload whenever the window had been
  reloaded out-of-band (so the tuicr head already matched) even though the
  deck's `· stale` chip still flagged the row. Fix: **the deck gates the
  reload on the same `prStaleSuffix` signal that renders the stale chip**
  (local bookmark behind the PR head) — only stale reviewer repairs carry the
  reload context — and the CLI reloads whenever a live `review` window exists,
  with no second staleness check (`:e` is idempotent, so reloading an
  already-current window is harmless). Non-stale reviewer repairs send
  unchanged. Added a `awaitTuicrSessionPathForHead` unit test; `runRepairReviewReload`
  logs its decision to `/tmp/awp-deck.log` (`REPAIR-RELOAD …`) for field
  diagnosis since the tmux orchestration isn't unit-tested.

## Validation
- [x] `go test ./...`
- [x] `go vet ./...`
- [x] `go build ./...`
- [x] `gofmt -l .` clean, `golangci-lint run ./...` → 0 issues
