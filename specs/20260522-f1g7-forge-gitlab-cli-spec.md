# Forge Abstraction + GitLab CLI Support

## Metadata
- **Spec ID**: `20260522-f1g7`
- **Feature name**: Forge abstraction with `gh` and `glab` backends
- **Owner**: SalTor
- **Status**: In Progress
- **Last updated**: 2026-05-22

## Goal
Make awp's PR/review/CI flows work on GitLab-hosted repos by abstracting the
host-CLI shellouts (`gh`, `glab`) behind a small interface and picking the
backend from the git remote URL (with a config override for self-hosted
GitLab on non-"gitlab" hostnames).

## User Problem
awp currently hardcodes `gh` for every host-CLI shellout. Engineers on
GitLab-hosted repos get errors from `awp review`, can't use the deck's `i`
(CI window) or `r` (PR picker) keys, and the deck PR fetcher errors out on
every refresh. The deck is otherwise host-agnostic, so the gh dependency is
a single sharp edge keeping GitLab users off the tool.

## Scope

### In scope (v1)
- A `Forge` interface (`ListPRs`, `FetchPR`, `PRDescriptionCommand`, `CIWatchCommand`).
- Exported `forge.GitHub` / `forge.GitLab` types with `New*` constructors.
- `forge.Detect(runner, override) (Forge, error)` that:
  - Honors a non-empty `override` ("github" | "gitlab") first.
  - Otherwise parses `git remote get-url origin` and routes by hostname.
  - Returns an explicit error for unsupported hosts (Bitbucket, Gitea, etc.)
    rather than falling back silently.
- `deck.forge` field in awp config (project overrides global) for the
  self-hosted GitLab escape hatch.
- Migration of every existing `gh` shellout in the user-facing flows
  (`awp review`, deck PR fetcher, deck `i` CI window, new-flow picker,
  `?` help overlay text, README key/CLI tables).
- GitLab MR JSON normalized into the unified `PRSummary`/`PRInfo` shape
  (`iid` → Number, `source_branch` → HeadRef, etc.).
- pr-status job: detect forge per repo; skip non-github with a Step log
  rather than failing per-repo with a gh error.

### Out of scope (v1)
- GitLab MR-status fetching for the deck glyph (review/CI/merge-status).
  The deck glyph mapping is built around gh's exact field shape; landing a
  partial GitLab path now would require redefining several glyph semantics
  to match GitLab's `detailed_merge_status` / `pipeline.status` and isn't
  worth bundling into this PR. Follow-up.
- Pipeline log streaming on GitLab. `gh run watch` streams + exits;
  `glab ci view` is a TUI you `Ctrl+Q` out of. Acceptable UX divergence
  for v1; documented in `?` help overlay.
- Caching the detected forge per-repo. Detection is one `git remote get-url`
  per call site; if profiles flag it later, add a cache then.

## UX

### CLI
- `awp review <n>` works against the detected forge — gh PR or glab MR.
- `awp review` (no arg) picks from open PRs/MRs via the detected forge.
- `awp review --help` mentions PR/MR and the auto-detection behavior.

### TUI (deck)
- `r` picker, `i` CI window, PR fetcher all use the detected forge.
- `?` help text reads "ci window (gh run watch / glab ci view)".
- No new keys, no new state — the swap is transparent to muscle memory.

### Config
```json
{
  "deck": {
    "forge": "gitlab"
  }
}
```
- Unset → auto-detect from remote.
- `"github"` / `"gitlab"` → force the backend (self-hosted GitLab escape).
- Anything else → `Detect` returns an actionable error.

## Discovery Questions
1. **First user**: SalTor and anyone whose primary workspace is a GitLab repo.
2. **When**: every `awp review`, every deck refresh, every `i` press.
3. **Output**: the same tmux windows and TUI experience as today on GitHub.
4. **Data**: `glab mr list/view --output=json`, `glab ci view -b "$b"`,
   `git remote get-url origin`.
5. **Smallest useful slice**: PR pick + PR review + CI window working on a
   GitLab repo. MR-status glyph is a separate slice.
6. **Non-goals**: see Out of scope.
7. **Done**: `awp review` works on both a GitHub and a GitLab repo without
   code changes; deck PR glyph degrades gracefully on GitLab; unsupported
   hosts get an actionable error.

## Spec Change Log
- 2026-05-22: Initial draft after PR feedback (forge config override,
  unsupported-host error, GLAB_FORCE_TTY, pr-status skip-non-github).

## Implementation Plan
1. `internal/forge/forge.go` — `Forge` interface, `PRSummary`/`PRInfo` types,
   `Detect(runner, override)`, `remoteHost` via `git remote get-url`,
   `parseHost` for ssh/https/scp URL forms.
2. `internal/forge/github.go` — gh-backed `*GitHub` with the existing
   `gh pr list/view` + ported `ListPRStatus`/`ListMergeQueuedHeads`/
   `rollupCIState` from the old `internal/github` package.
3. `internal/forge/gitlab.go` — glab-backed `*GitLab` (`mr list/view`,
   `ci view -b`).
4. `internal/config/config.go` — `Deck.Forge string` field, merged
   project-over-global.
5. `internal/cli/exec.go` — `detectForge(runner, repoRoot)` helper that
   reads the config override.
6. Migrate call sites: `review.go`, `deck.go` (PR fetcher + CI window),
   `new_flow.go`, `pr_status_job.go` (skip non-github), `app.go`
   (usage string), `deckui/model.go` (`?` help).
7. README: key table, CLI reference, architecture pointer, config block.
8. Tests: forge unit tests (URL parsing, override, both backends, gh-only
   methods), pr-status skip-non-github regression, review picker regression.

## Acceptance Criteria
- [x] `awp review <n>` against a GitHub repo opens the same tmux layout as
  before this change.
- [x] `awp review` (no arg) lists PRs on a GitHub repo and MRs on a GitLab
  repo via the detected forge.
- [x] Deck `i` opens a CI window that runs `gh run watch`-equivalent on
  GitHub and `glab ci view -b "$b"` on GitLab.
- [x] `deck.forge` config field forces the backend without consulting the
  git remote URL.
- [x] Unsupported hosts (Bitbucket, etc.) produce an actionable error
  mentioning the host and the config escape.
- [x] pr-status job logs `<repo> — skipped (gitlab)` for non-github repos
  and continues with the rest.
- [x] No reference to `internal/github` remains in the tree.

## QA / Human Review Test Plan

### Setup
- [ ] `jj`, `tmux`, `gh`, `glab` on PATH.
- [ ] `gh auth status` and `glab auth status` both succeed.
- [ ] A GitHub-hosted repo and a GitLab-hosted repo cloned locally.

### Core Happy Path
- [ ] `awp review <gh-pr#>` in the GitHub repo opens the standard 3-window
  layout (`pr description`, `agent`, `review`).
- [ ] `awp review <glab-mr-iid>` in the GitLab repo opens the same layout;
  `pr description` window shows `glab mr view <iid>` output.
- [ ] `awp deck` in each repo: `r` lists PRs/MRs respectively; `i` opens
  the CI window with the matching CLI.

### Edge Cases & Failure Modes
- [ ] `awp review` in a Bitbucket-hosted repo (or any non-gh/gl host)
  prints an actionable error mentioning the host and `deck.forge`.
- [ ] Setting `deck.forge` to a nonsense value (`"bitbucket"`) errors with
  the same actionable message.
- [ ] Self-hosted GitLab (`code.company.com`) with `deck.forge: "gitlab"`
  works end-to-end.
- [ ] pr-status job logs a skip for the GitLab repo and continues to the
  GitHub repo in the same job.

### Regression Checks
- [ ] All deck keys other than `r`/`i` behave identically to pre-change.
- [ ] Sessions created by an earlier flow (e.g. `enter` on a workspace
  row) still get the missing `pr description`/`review` windows added by
  `awp review`.
- [ ] `?` help overlay lists both CLIs and stays in sync with the row
  legend.

### Reviewer Notes
- Capture the actual `glab mr view`/`glab ci view` UX on a real GitLab
  repo — if either feels worse than the gh equivalent (e.g. `glab ci view`
  blocking the pane indefinitely after the pipeline finishes), file a
  follow-up to add a thin wrapper.

## Validation
- [x] `go test ./...`
- [x] `go vet ./...`
- [x] `go build ./...`
