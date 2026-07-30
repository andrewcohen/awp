# Feature Spec: awp-native review surface

## Metadata
- **Spec ID**: `20260730-4o94`
- **Feature name**: awp-native review surface
- **Owner**: Andrew (with AI coding agent)
- **Status**: Discovery
- **Last updated**: 2026-07-30

## Goal

One surface inside the deck where you can watch an agent implement a change and
review it — reading the diff, leaving findings, and sending those findings back
to the agent — without leaving awp or copying text between tools. Retires the
`tuicr` dependency.

## User Problem

Reviewing an agent's work today spans three tools that don't share state.

**1. Findings live somewhere awp can't see.** `awp review` primes an agent to
review a PR and push findings into tuicr via `tuicr review add`. Because tuicr
owns that store privately, awp has to reverse-engineer it:
`internal/cli/tuicr_session.go` is 372 lines of reading `active_sessions.json` /
`index.json` and globbing `reviews/sessions/*.json`, with a 5s discovery
timeout and a documented window-launch-order hack (the review window must open
first so `tuicr pr` has a head start writing its state). `tuicr review list
--repo .` returns `[]` for PR-mode sessions because `repo_path` is stored as
`forge:github.com/...`, which no local checkout matches. Two specs exist purely
to work around one consequence of this (`20260713-9p1y`, `20260714-064t`), both
pointing at upstream `agavra/tuicr#368` — which has no movement, so the
workarounds are permanent.

**2. Reading a change and acting on it are separate.** A finding gets from
"noticed while reading" to "the agent is fixing it" via a human copy-pasting
between a review TUI and an agent pane. This is the round trip tuicr
structurally cannot close, because it has no idea an agent exists.

**3. Watching an agent implement means reading tool calls.** The agent pane
shows edits as fragments with no surrounding code. `awp watch` shows unit and
gate progress but no content. Neither answers "what is it writing right now, in
context."

awp already has the pieces that would close all three: the in-deck diff stream
(`c`), dev-loop tracking in `internal/watch`, a `PostToolUse` hook that fires on
every tool call, `internal/github`, and the `ActionSendPrompt` / `p r` prompt
machinery. What's missing is a store it owns and a comment gesture.

## Scope

### In scope
- **Line cursor** in the diff stream — the anchor for commenting and for
  contextual expansion.
- **Content-anchored comments**, relocated against current file content on
  every load.
- **Findings store** owned by awp, written by both humans and agents.
- **`awp review add`** so an agent can file findings without touching a foreign
  tool's private state.
- **Comment gestures**: `c` to comment, `enter` to save; a second action sends
  the comment to the workspace's agent with diff context and instructions for
  how to reply.
- **Live refresh** — the diff follows the working copy while the agent edits.
- **`r` marks the current file reviewed and collapses it** (once live refresh
  makes manual refresh redundant).
- **Contextual expansion** — pull more context around a hunk from file content.
- **Review scope** — working copy, change-vs-stack-base (retiring `C`'s tuicr
  window), and PR mode (retiring `awp review`'s tuicr window).
- **Remote PR threads** — existing review threads pulled and rendered inline
  next to local drafts, so a reviewer sees the conversation already on the PR.
- **GitHub publish** — local findings become a PR review; replies to existing
  threads; threads can be resolved and unresolved.
- **Gate lights** in the diff header, from data `internal/watch` already
  computes.
- **Symmetric directions**: human→agent comments and agent→human findings are
  the same record type, distinguished by an author field.

### Deferred — designed for, built after the loop works
Both are wanted, neither is needed to retire tuicr. They come after publish so
the core loop isn't held up by them, and so their design can be informed by
using the surface daily.

- **Follow mode** (phase 8) — the agent's current edit position sets where the
  diff opens, so opening it mid-run lands on what's being written now. A good
  idea rather than a current need; the mechanism is cheap because
  `awp internal loop track` already fires on every tool call.
- **Hunks grouped by unit of work** (phase 9) rather than by file. The design
  must not preclude it (see *Unit grouping*), but whether it earns its place
  depends on what real unit boundaries look like — to be checked against actual
  transcripts before building.

### Out of scope
- **GitLab.** tuicr supports it; we don't use it.
- **tuicr's `--file` / `-A` annotate modes** and commit selector.
- **Diff-renderer polish.** Syntax highlighting and intra-line highlighting are
  explicitly closed — see the 2026-07-30 decision in
  `specs/20260410-1l07-diff-ui-spec.md`. The rendered diff is what `jj diff
  --git` produced. Effort belongs in the review loop, not in competing with
  industry-standard diff rendering.
- **Merging with the `awp watch` overlay.** They stay separate surfaces for
  now; revisit only if using both makes the split feel wrong.
- **SQLite.** Deferred, with triggers recorded in D2.
- **Migrating already-published GitHub comments.** Only awp-local findings are
  managed.

## Decisions

Recorded because each closes off an alternative that would otherwise look
reasonable later.

### D1 — Comments are anchored to content, not to a diff position

A comment's identity is `(path, anchor)`. The anchor is the line's text plus a
few lines of surrounding context, plus a line number as a *hint*. There is no
per-head session concept anywhere in the design.

**Why not tuicr's `(repo, PR, head_sha)` session key:** in awp the agent edits
files *while you are reading them*. Re-anchoring is therefore not an edge case
triggered by a force-push — it is the normal operating path, and live refresh
requires it regardless. Once re-anchoring works continuously, a force-push stops
being a special case: it is just "the file content changed," the same code path.
tuicr needed explicit carry-forward migration because its diffs are essentially
static; ours never are.

`head_sha` is *observed metadata* — what you last looked at, used to show a
stale indicator — never part of identity. Nothing needs migrating because
nothing is keyed on it.

### D2 — Storage is JSON files, not SQLite

Considered and deferred. The forcing functions would be aggregate queries and
publish bookkeeping; neither survives inspection:

- **Deck fast paint** needs per-row finding counts across every repo, and
  near-instant deck open is a hard constraint. Solved the way
  `~/.awp/pr-status-cache.json` already solves it — a precomputed index the deck
  reads once — rather than with a new engine.
- **Concurrent writes** are where files are *better*, not merely adequate. One
  file per comment means two agents calling `awp review add` never touch the
  same bytes: conflict-free by construction, no locking, no `SQLITE_BUSY` retry.
  The contended path is append-only, the one shape files do perfectly.
- **Publish state** (`pending → posted(thread_id) → failed(reason)`) is
  per-comment and independent, so it lives in that comment's file. There is no
  cross-comment invariant requiring a transaction.

Against: every part of awp's state today is JSON (`internal/state`, workspace
snapshots, the pr-status cache), so SQLite would mean two persistence models in
a small app. Pure-Go SQLite (`modernc.org/sqlite`) is ~7–10 MB of generated code
with a real build-time cost; the cgo driver breaks the single-static-binary
story. CLAUDE.md is explicit about minimal dependencies. And while building
this, being able to `cat` a comment to see whether anchoring did the right thing
is worth something.

**Revisit when any of these becomes true:**
- Search over comment bodies, or cross-repo review history, as a user feature.
- An invariant spanning multiple comments that must hold atomically.
- Measurable deck startup cost from the store even with the index.
- A third subsystem wanting the same treatment — at that point it is a
  persistence layer, not a special case.

### D3 — Scope runs through GitHub publish

Full tuicr retirement rather than stopping at local findings. Publish is phased
last, and **tuicr stays installed and reachable until publish has been driven in
anger** — it is the escape hatch, not a fallback we design around.

### D4 — Both directions are the same record

Human-authored comments and agent-authored findings differ by an `author` field,
not by type or location. Cheaper under D1 than it would be otherwise: with no
per-direction identity, symmetry costs nothing structurally.

### D5 — Unit grouping is designed for, built last

Mechanism settled (see *Unit grouping*) so the data model doesn't preclude it.
Build order puts it after the loop works, because its *value* — unlike its cost
— is unverified.

### D6 — The store lives under `~/.awp/`, with event-driven cleanup

**Settled 2026-07-30.**

Workspace-local `.awp/` sounds right and isn't, for three reasons:

1. **It isn't workspace-local.** Bootstrap *symlinks* `<repo>/.awp/` into every
   workspace rather than copying it (`internal/workspace/service.go:1084`,
   README "Built-in bootstrap **symlinks** `<repo>/.awp/` into each workspace").
   So a store written there is repo-shared, not per-workspace — the intuition
   buys no isolation. This is the exact trap that made `awp review` write its
   prompts to `~/.awp/review-prompts/<repo>/<workspace>.md` instead: "a review
   workspace's own `.awp/` is symlinked to the shared source-repo `.awp/` during
   prep, so a prompt written there would be shared across every review and
   clobbered by the next one."
2. **Anything in the tree appears in the diff being reviewed.** `.awp/` is
   inside the working copy, so store writes show up in `jj st` / `jj diff` —
   i.e. inside the very stream the review surface renders. Reviewing your own
   review state is a self-referential mess, and `.awp/` is a *tracked* directory
   (it holds the committed `dev_loop` config), so mutable state there also risks
   being committed.
3. **Findings should outlive the workspace.** Review workspaces get pruned
   routinely. A prompt file is regenerable, so deleting it on prune is correct;
   an unpublished draft comment is not.

So: `~/.awp/reviews/<repo-slug>/<review-id>/`, matching where every other
mutable awp artifact already lives (`pr-status-cache.json`,
`review-prompts/`, `dev-loop/`).

**Cleanup.** Global state needs explicit cleanup, and the alternative's
"implicit" cleanup is the same operation as "unpublished drafts silently
destroyed" — so it trades a tidiness problem for a data-loss one. That matters
most in the daily flow: `c` on a working change has no publish target at all, so
human→agent comments live only locally.

One rule makes cleanup safe to be aggressive: **the only thing worth preserving
is an unpublished draft.** The diff regenerates; published comments live on
GitHub. So: delete freely unless a review has unpublished comments; when it
does, keep it and say so.

Five triggers, all events awp already observes — and all reusing machinery that
already does exactly this for `~/.awp/review-prompts/`:

1. **Workspace deleted** — the hook that removes the prompt file
   (`internal/workspace/service.go:1602`) also removes that workspace's
   working-copy review. `pruneEmptyParents` (`:1838`) already tidies empty
   parents.
2. **`PruneOrphans`** (`:1762`) — same place it already resolves review-prompts
   paths for orphaned workspaces (`:1824`).
3. **PR merged or closed** — the pr-status refresh already learns this for
   `pr-status-cache.json`; archive, then drop.
4. **Successful publish** — bodies become a cache of GitHub. Keep only a
   tombstone (comment id → thread id) for publish idempotency.
5. **Manual `awp review prune`**, plus an `awp doctor` check for repo-slug
   directories whose repo no longer exists.

### D7 — "diff" is the surface, "review" is the activity

They converge but are not synonyms, and keeping the distinction sharp avoids a
rename that would buy nothing:

- **diff** is the *surface* — the stream of changed lines. `awp diff` standalone
  and `c` in the deck both open it, and it keeps that name.
- **review** is the *activity* performed in it, and the state that activity
  produces (comments, reviewed flags, threads).
- **`awp review <pr>`** stays the *setup workflow*: create a workspace, fetch
  the PR, prime an agent, open the surface scoped to that PR. What changes is
  only where it points — the in-deck diff instead of a tuicr window. The command
  name still describes what it does.

So `awp diff` does not get renamed, and the review store is not "the diff
store". A review can exist without a PR, and the surface can be opened without
starting a review — the concepts stay independent even though you usually meet
them together.

**Wrinkle to settle in phase 3:** `awp review 123` (numeric = start a review)
and `awp review add|list|publish` (subcommands) would share a namespace. It
parses unambiguously, but it reads oddly. Options: keep it, or move the setup
form to `awp review start <pr>`. Leaning toward keeping the numeric form since
it already exists and is the common case.

### D8 — No "unread": the badge counts open findings

**Settled 2026-07-30.** There is no seen flag and no last-viewed timestamp.

A finding starts `open` and leaves that state when it is acted on — dismissed,
sent to the agent, or published. The deck badge counts `open` comments, so it
reads as "3 findings awaiting triage" rather than as a claim about reading
history.

**Why this rather than tracking attention:** a per-review timestamp is destroyed
by any incidental open (glance at the diff for an unrelated reason and every
pending finding silently becomes read), and a per-comment `seen` flag needs a
definition of "seen" that is inherently fuzzy — does rendering it count, or
scrolling past it? State is well-defined, survives incidental opens, and is
actionable. It also composes with D4: a human-authored comment starts `open` too,
and becomes `sent` when pushed to the agent.

## Data model

```
~/.awp/reviews/<repo-slug>/<review-id>/
    review.json          # per-review state; only the deck writes this
    comments/<id>.json   # one file per comment; agents append here
~/.awp/reviews/<repo-slug>/index.json   # counts for the deck's fast paint
```

**review** — `{ id, repo, target, observed_head, created_at, updated_at,
reviewed_files: { path: content_hash } }` where `target` is
`{ kind: "working" | "revset" | "pr", value, workspace }`. A review with no PR
is first-class: `c` on a pre-PR change creates one, and its findings survive
into the PR because nothing is keyed on the PR number (D1).

**comment** — `{ id, author, body, state, anchor, created_at, updated_at,
published: { thread_id, at } | null }` where:

- `author` is `"human"` or an agent identifier (D4).
- `state` is `open | sent | addressed | orphaned | published`.
- `anchor` is `{ path, side, line_hint, text, context_before[],
  context_after[] }`.

**remote threads** are cached read-only under
`~/.awp/reviews/<repo-slug>/<review-id>/remote/threads.json`, refreshed on open
and on demand. They are GitHub's records, not ours, so nothing is authored
there — we store `{ thread_id, path, side, line, resolved, comments[] }` plus
our own local reply drafts, which live as normal comment records carrying a
`reply_to` thread ID. Cached rather than fetched-on-render so deck open and
first paint stay off the network.

Vocabulary is kept deliberately distinct: **local comments have a `state`**
(`open | sent | addressed | orphaned | published`), **remote threads have
`resolved`**. A local draft cannot be "resolved" and a remote thread cannot be
"addressed"; conflating them would make the UI lie about what a keystroke did.

**reviewed_files** maps path → content hash *as reviewed*, so an edit
invalidates the flag. This matters more here than in tuicr: marking a file
reviewed and having the agent silently change it afterwards is the worst failure
mode a review tool has.

Writes: comment files are created once and replaced atomically (temp + rename);
`review.json` and `index.json` are deck-owned and replaced atomically.

### D9 — Comments anchor to the new side, with no user choice

**Settled 2026-07-30.** Added and context lines anchor to the new side; removed
lines anchor to the old side, since they exist nowhere else. There is no
user-facing side selector.

**Why:** re-anchoring then always reads *current* file content, which is exactly
what live refresh already needs — one content source, one code path. Old-side
anchors would require fetching pre-change content to relocate against. It also
means a comment tracks the file as it will be, which is the version the agent
acts on.

The cost, accepted: commenting about a deletion's surroundings ("you removed
this, but the line above still assumes it") anchors to the surviving context
rather than to the removed line. Revisit only if that reads wrong in practice.

### D10 — Follow mode: seek on open always, continuous tracking behind a toggle

**Settled 2026-07-30, for phase 8 — not to be built yet.**

Opening the diff always lands on the agent's current edit position. Continuous
re-seeking is behind `F`, off by default, and any navigation keypress drops out
of tracking so it can never fight you.

**Why not continuous by default:** it is precisely the cursor-jump problem that
got auto-refresh disabled in April 2026 — the view moving while you are reading.
Seek-on-open delivers the useful part unconditionally; live tracking is opt-in
for when you actually want to watch.

## Anchoring algorithm

On load, for each comment, against current file content:

0. **Rename remap** — if the diff reports the anchor's path as renamed
   (`Status: "R"`, with old and new paths already parsed by
   `internal/diff/parser.go:91`), rewrite the anchor's path to the new one
   before anchoring. The rename is handed to us by `jj diff --git`, so this
   costs nothing to honour and rescues a whole file's comments.
1. **Hint hit** — anchor text matches at `line_hint`. Overwhelmingly the common
   case; must be the fast path.
2. **Unique text match** elsewhere in the file → move, update hint.
3. **Context match** — anchor text plus surrounding context matches at exactly
   one place → move, update hint, mark moved.
4. **No match** → `orphaned`. Kept and surfaced in a detached-comments area,
   never silently dropped.

Deliberately no diff library: hash-and-search is language-agnostic and doesn't
reopen the tokenizer question that killed intra-line highlighting.

**Not done: cross-file rescue.** Searching *other* files for an orphaned
anchor's text would also catch code moved between files, but it costs a scan per
orphan and can relocate a comment somewhere it doesn't belong on a coincidental
match. Explicit renames are honoured because the diff reports them; anything
else orphans visibly rather than being guessed at. Revisit only if moved code
turns out to strand comments often in practice.

## Unit grouping (phase 9 mechanism)

Units are already commits. The dev loop is `explore → implement → verify →
commit`, one unit per pass, and `internal/watch/loop.go:81` already defines a
`commit` **marker** gate matching `jj (commit|describe|squash)|jj git push|git
commit` with `NotMatch: wip:`. The loop already *detects* the commit — it just
doesn't record which revision resulted.

The missing link is one field: when the marker fires, `awp internal loop track`
records the resulting **jj change ID** against the current unit, giving
`units: [{ n, subject, change_ids: [...] }]` in the workspace snapshot. Grouping
is then `jj diff --git -r <change_id>` per unit, labelled with the unit's
subject; uncommitted work is "current unit, in progress" — the working-copy diff
`c` already shows.

**Change IDs, not commit hashes:** the agent amends and squashes constantly, and
jj change IDs survive rewrites. That is what makes the cheap version viable at
all.

Rendering is small because the stream's geometry is a flat row index
(`internal/ui/stream.go`): add a `rowUnitHeader` kind and let the builder take
grouped input.

**Verify before building:** only works where a `dev_loop` is configured and the
agent actually commits per unit; squashing two units loses the boundary; and if
units are small and single-file, unit grouping and file grouping look nearly
identical — the case where this is a novelty rather than a feature.

## UX

### TUI — diff stream additions

| Key | Action |
|-----|--------|
| `j`/`k`, `ctrl+u`/`ctrl+d` | move the line cursor (scroll follows it) |
| `c` | comment on the cursor's line; `enter` saves |
| `C` (in comment editor) | save **and** send to the agent with context + reply instructions |
| `r` | mark current file reviewed and collapse it |
| `]`/`[` | next / previous comment or thread |
| `+`/`-` | expand / collapse context around the hunk |
| `R` | resolve / unresolve the thread at the cursor (phase 7) |
| `T` | cycle remote-thread visibility: unresolved only → all → none |
| `F` | toggle follow mode (phase 8) |
| `p` | publish review to GitHub (phase 7) |

Existing bindings (`{`/`}`, `g`/`G`, `h`/`l`/`0`/`$`, `w`, `tab`, `e`, `/`) keep
their meanings from `specs/20260410-1l07-diff-ui-spec.md`. `r` only changes
meaning once live refresh lands (phase 2) — taking it earlier would leave no way
to see new work.

Comments render inline beneath their anchored line, with a gutter marker.
Remote threads render the same way but visually distinct from local drafts —
you must be able to tell at a glance what is already on GitHub from what only
exists on your machine. Resolved threads are hidden by default (`T` reveals
them), matching the `remote_comments_visibility: "unresolved"` default tuicr
settled on. Orphaned comments collect in a detached section at the end of the
stream.

### CLI

- `awp review add --file <path> --line <n> [--side new|old] --body <text>` —
  file a finding. Resolves the review from the workspace; no session path to
  discover.
- `awp review list [--json]` — findings for the current workspace's review.
- `awp review publish [--dry-run]` — phase 8.

The agent-facing prompt (`internal/cli/review_prompt.md`) drops the tuicr
session-path plumbing and names `awp review add` instead.

### Deck

- Row shows `N findings` from the counts index, counting `open` comments only
  (D8) — i.e. what still needs triage, not what exists.
- Inbox can bucket reviews with open findings.

## Implementation Plan

Each phase is independently landable and independently useful.

1. **Line cursor.** A cursor row in the stream, scroll following it. No new
   behavior beyond a visible cursor — deliberately isolated, because it is the
   design risk for both commenting and expansion. Reconsider `bubbles/viewport`
   here per the component conventions.
2. **Live refresh.** Refresh in place, re-anchoring the cursor by path and
   content rather than by index. This is what made auto-refresh unusable in
   April; fixing it properly is a prerequisite, not a nicety. Frees `r`.
   **Implemented 2026-07-30 as a 2s poll, not fsnotify** — `jj diff` already
   costs a subprocess and snapshots the working copy, an unchanged diff is
   dropped by fingerprint before touching view state, and watching a tree on
   macOS means per-directory kqueue watches with no recursive option. The
   anchoring ladder built here (`internal/ui/anchor.go`) is the one phase 3
   reuses for comments.
3. **Findings store + `awp review add` + inline display.** The store, the
   anchoring ladder, the `c` gesture, inline rendering, deck counts, and the
   cleanup hooks from D6 wired alongside the existing review-prompt deletion
   (cheaper to do here than to retrofit). At the end of this phase agents stop
   writing to tuicr.
4. **Comment → agent.** Second gesture, reusing `ActionSendPrompt` and the
   established propose-then-approve gating; comment state moves
   `open → sent → addressed`, with *addressed* inferred from the anchor moving
   or vanishing.
5. **Reviewed + collapse.** `r`; collapse as a geometry input to `buildStream`,
   joining `(files, width, wrap)` as a rebuild trigger — not a render-time skip,
   which would desync row counts from what is drawn.
6. **Scope + remote threads.** Working copy / stack base / PR in-deck. `C` and
   `awp review` stop opening tuicr windows. `internal/cli/tuicr_session.go` and
   the two workaround specs are deleted here.
   Also **pull existing PR review threads** and render them inline alongside
   local drafts. This is a read, so it belongs here rather than in publish: once
   a PR diff renders in-deck, seeing the conversation already on it is table
   stakes — it is the whole reason `awp review` embeds existing comments in the
   agent prompt today ("so the agent doesn't re-raise points already made").
   `internal/github.PRComment` gains a thread node ID, `resolved`, side, and
   multi-line range; remote threads anchor through the same ladder as local
   comments (their line numbers are against a specific commit and drift the same
   way — which is a useful confirmation of D1).
7. **Publish + resolve.** The write direction: local findings become a GitHub
   review, replies post to existing threads, and threads can be
   **resolved / unresolved**. Idempotent retry keyed per comment. Resolving is
   GraphQL-only (`resolveReviewThread` / `unresolveReviewThread`) — REST cannot
   do it — which fits the existing `gh api graphql` usage in
   `internal/github`. tuicr remains installed until this has been driven in
   anger. **tuicr is retired at the end of this phase** — the spec's finish
   line.

Deferred, after the finish line:

8. **Follow mode.** Extend `awp internal loop track` to record the current edit
   file/line; the stream seeks there on open. Continuous tracking sits behind
   `F` and is **off by default** (D10). Preserve the hook's
   write-only-on-change property. *Not to be built yet — confirmed deferred
   2026-07-30.*
9. **Unit grouping.** Per the mechanism above, after verifying against real
   transcripts.

**Contextual expansion** has no phase of its own: it is a small addition once
the line cursor exists, so it lands with whichever phase first wants it
(probably 3, while reading real diffs to test anchoring).

## Acceptance Criteria

- [ ] A finding filed by an agent via `awp review add` appears inline in the
      deck's diff view without awp reading any other tool's state.
- [ ] A comment survives the agent editing the file around it: it relocates to
      the moved line, or is marked orphaned and still shown — never silently
      dropped.
- [ ] A force-pushed PR requires no migration step and strands no comments.
- [ ] Renaming a commented file carries its comments to the new path.
- [ ] Two agents filing findings concurrently both succeed, with no lost writes.
- [ ] Marking a file reviewed collapses it; a subsequent edit resurfaces it.
- [ ] Sending a comment to the agent produces an approval-gated prompt and moves
      the comment to `sent`.
- [ ] The deck badge counts findings awaiting triage, and opening the diff does
      not change that count.
- [ ] Deck open stays near-instant with a populated store.
- [ ] Nothing the review surface writes appears in the diff it renders.
- [ ] Deleting or pruning a workspace removes its working-copy review, unless
      that review has unpublished comments — in which case it survives and says
      so.
- [ ] `C` and `awp review` open in-deck; `tuicr_session.go` is deleted.
- [ ] Existing PR threads render inline, visually distinct from local drafts,
      with resolved ones hidden by default.
- [ ] A thread can be resolved and unresolved from the diff, and the change is
      reflected on GitHub.
- [ ] Publishing twice does not duplicate comments on GitHub.
- [ ] `grep -r tuicr internal/` returns nothing.

## QA / Human Review Test Plan

### Setup
- [ ] `jj`, `tmux`, `gh` authed; a repo with a configured `dev_loop`.
- [ ] A workspace with an agent mid-run, and a second workspace with an open PR.

### Core Happy Path
- [ ] `c` on a working change, comment on a line, save; reopen and confirm it
      persisted and anchored correctly.
- [ ] Send a comment to the agent; confirm the prompt is approval-gated and the
      comment shows `sent`.
- [ ] Let the agent edit the commented region; confirm relocation or an explicit
      orphaned marker.
- [ ] Mark files reviewed through a multi-file change; confirm collapse and that
      the stream shrinks to what is left.

### Edge Cases & Failure Modes
- [ ] Comment on a deleted line (old side only).
- [ ] Comment on a file the agent then deletes.
- [ ] Comment on a file the agent then renames — comments follow the new path.
- [ ] Comment on code the agent then moves to a different file — orphans
      visibly, is not silently relocated.
- [ ] Force-push the PR; confirm no migration prompt and no lost comments.
- [ ] Two `awp review add` calls racing.
- [ ] Corrupt single comment file — degrades to skipping that comment, not
      failing the review.
- [ ] Publish with one comment failing mid-run; retry is idempotent.
- [ ] Resolve a thread, confirm on GitHub, unresolve it, confirm again.
- [ ] A PR whose threads anchor to lines that have since moved — confirm they
      relocate or show as detached rather than rendering at wrong lines.
- [ ] Offline / `gh` unauthenticated: cached threads still render, write actions
      fail with an actionable error rather than hanging.
- [ ] Prune a workspace with unpublished comments — confirm they are not lost
      silently.

### Regression Checks
- [ ] `awp diff` standalone still works.
- [ ] Deck open time unchanged with a populated store.
- [ ] `p r` repair flow still functions.
- [ ] Repos with no `dev_loop` degrade cleanly (no follow mode, no unit
      grouping).

### Reviewer Notes
- Capture store contents (`~/.awp/reviews/...`) alongside observed UI for any
  anchoring surprise.

## Discovery Questions
1. **Who is the first user?** Andrew, reviewing agent-written changes in jj
   repos, many workspaces at once.
2. **When do they use it?** Continuously during an agent run (progress) and
   after (review) — the same surface at different moments.
3. **What exact result do they need?** A finding that goes from noticed to
   being-acted-on without leaving the deck, and a diff that can be opened
   mid-run to see what is happening in context.
4. **What data sources?** `jj diff --git`, file contents for expansion, the
   agent transcript and `loop track` hook for units/gates/edit position,
   `internal/github` for PR threads.
5. **Smallest useful slice?** Phases 1–3: cursor, live refresh, and a store with
   `awp review add`. At that point agents stop writing to tuicr.
6. **Explicit non-goals?** GitLab, annotate modes, diff-renderer polish, merging
   with `awp watch`, SQLite.
7. **What does done look like?** `grep -r tuicr internal/` returns nothing and
   nobody misses it.

## Open Questions

Needs a call before the phase that depends on it; none block phase 1.
Resolved questions move into *Decisions*.

1. **Commit-per-unit** — verify against real transcripts before committing to
   unit grouping. *Travels with phase 9.*

## Spec Change Log
- 2026-07-30: Initial draft. Decisions recorded: content-anchored comments with
  no per-head sessions (D1); JSON-file storage with SQLite deferred behind
  explicit triggers (D2); scope through GitHub publish with tuicr retained as
  escape hatch until publish is proven (D3); symmetric human/agent comment
  records (D4); unit grouping designed-for but built last (D5); store under
  `~/.awp/` rather than the workspace tree, because bootstrap symlinks `.awp/`
  so "workspace-local" is actually repo-shared and in-tree writes land in the
  diff being reviewed (D6, open for override).
- 2026-07-30: Follow mode moved out of the main sequence to phase 8, after
  publish — wanted, but a nice idea rather than a current need, and nothing
  about retiring tuicr depends on it. The finish line is now phase 7.
- 2026-07-30: Phase 7 landed — the spec's finish line. `awp review publish` posts
  unpublished findings to a PR as inline comments, and `R` resolves/unresolves the
  thread under the cursor. Two decisions worth recording: comments are posted
  **individually rather than as one batched review submission**, because a partial
  failure inside a batch is unrecoverable — you cannot tell which comments landed,
  so a retry either duplicates everything or drops everything; and each comment's
  publish record is written **at the moment it succeeds**, not batched to the end,
  so a crash mid-run cannot leave posted comments looking unpublished. An
  unparseable post response counts as success with an unknown id, since reporting
  an error there would invite a duplicate. `--dry-run` reports what would go up.
- 2026-07-30: Phase 6 landed, and **tuicr is no longer invoked anywhere in
  `internal/`**. `C` opens the stack-base diff in-deck via a `DiffScope` on the
  loader rather than a tuicr window; `awp review` no longer opens a review window
  at all, and its agent prompt files findings with `awp review add` instead of
  `tuicr review add --session <abs-path>`. Deleted: `internal/cli/tuicr_session.go`
  (372 lines of reading another tool's private state), `runRepairReviewReload`,
  `renderTuicrSessionBlock`, `formatPriorSessions`, the session-discovery timeout,
  and the stale-head reset. The prior-head draft migration is gone as a *concept*,
  not just as code — content anchoring relocates findings across a force-push, so
  there is nothing to carry forward. Remote PR threads are fetched via GraphQL
  (`FetchReviewThreads`, plus `Resolve`/`UnresolveReviewThread` for phase 7 — REST
  cannot express either), mirrored read-only into the review, and rendered inline
  alongside local comments with `T` cycling visibility.
- 2026-07-30: Phases 4 and 5 landed. Phase 4: `ctrl+s` in the compose box saves
  and hands the comment to the agent, with an approval-gated prompt carrying the
  anchor's file/line/side/text/context and explicit reply instructions
  (`awp review add --author agent`), moving the comment to `sent` — not
  `addressed`, which stays inferred. Phase 5: `r` marks the file at the cursor
  reviewed and collapses it to its divider, keyed to a content hash so a later
  edit resurfaces it; collapse is a `buildStream` input rather than a render-time
  skip. `r` was freed by phase 2; explicit refresh moved to `ctrl+r`.
- 2026-07-30: Phase 3 landed. `internal/review` owns the store (one file per
  comment, atomic writes, per-repo counts index); `internal/ui/comments.go` places
  comments into the stream geometry and renders them inline, with unplaceable
  ones in a detached section; `c` opens a compose box; `awp review add` / `awp
  review list` are the agent-facing CLI; workspace delete and PruneOrphans now
  remove a workspace's review unless it holds unpublished comments.
  Deviation: reviews are keyed by **workspace** rather than by PR for now.
  `PRNumber` lives on the state-file entry, which `workspace.Service` does not
  expose for reading, and `awp review <pr>` already creates a dedicated workspace
  per PR — so a workspace-keyed review is effectively the PR's review until PR
  mode lands in phase 6. Safe to re-key later precisely because comments anchor
  to content and nothing depends on the review's identity (D1).
- 2026-07-30: Phase 2 landed. Trigger deviates from the fsnotify line above: a
  2s poll with a raw-diff fingerprint to drop no-op reloads. Anchoring by
  content (with occurrence-ordinal disambiguation for duplicate lines) is shared
  with phase 3's comment anchoring rather than implemented twice.
- 2026-07-30: Q1 settled — store under `~/.awp/reviews/` with event-driven
  cleanup on workspace delete, PruneOrphans, PR merge/close and successful
  publish, reusing the hooks that already delete `~/.awp/review-prompts/`
  entries. Guiding rule: the only thing worth preserving is an unpublished
  draft. D6 moved from open to settled.
- 2026-07-30: Q2 settled as D8 — no unread tracking at all. The badge counts
  `open` findings (awaiting triage) rather than unseen ones, which removes both
  the seen-flag and last-viewed-timestamp mechanisms and the fuzzy definition of
  "seen" they required.
- 2026-07-30: Q3 settled — the anchoring ladder gains a step 0 that remaps
  anchors across explicit renames, since `jj diff --git` already reports them.
  Cross-file rescue for moved code is explicitly not done: unbounded cost and it
  can relocate a comment on a coincidental match, so moved code orphans visibly
  instead.
- 2026-07-30: Q4 settled as D9 — comments anchor to the new side (old side only
  for removed lines), with no user-facing choice, so re-anchoring always reads
  current content and shares one code path with live refresh.
- 2026-07-30: Q5 settled as D10 — follow mode seeks on open always, continuous
  tracking behind `F` and off by default, with navigation keys dropping out of
  tracking. Confirmed as deferred: not to be built yet.
- 2026-07-30: Remote PR threads added. Pulling them lands in phase 6 rather than
  publish, since it is a read and a PR diff without its existing conversation is
  incomplete. Resolving/unresolving threads added to phase 7; it is GraphQL-only,
  which fits the `gh api graphql` calls `internal/github` already makes.
  `PRComment` needs a thread node ID, resolved state, side and multi-line range.
  Local `state` and remote `resolved` are kept as separate vocabularies (D-note
  in Data model). Naming settled as D7: diff is the surface, review is the
  activity, no rename.

## Validation
- [ ] `mise exec -- gofmt -l .`
- [ ] `mise exec -- golangci-lint run ./...`
- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `go build ./...`
