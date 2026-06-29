Please review PR #{{number}}: {{title}}

{{body}}

Diff range: {{diff_range}}
Base ref: {{base}}

## Existing comments on this PR

These comments are already on the PR. Read them before reviewing.

{{comments}}

Use them to stay non-redundant:

- **Do not restate** a point an existing comment already makes — repeating
  it is noise in the pane.
- You may **agree or disagree** with any of them. If you think one is wrong,
  say so in a `note` (cite the point you're pushing back on); the user wants
  your independent read, not deference.
- If a comment is partially right but misses something, add only the
  incremental insight.

## How to review

**First, look for `REVIEW.md` at the repo root.** If it exists, it is the
**primary source** for this repo's review guidelines — read it before the
diff and let it drive what you flag, how you prioritize, and any
project-specific conventions or focus areas. The guidance in this prompt
(comment shapes, volume, tuicr mechanics) still applies, but where
`REVIEW.md` speaks to *what* to review, it wins. If no `REVIEW.md` is
present, fall back to the general guidance below.

Your job is to read the diff and push findings into the tuicr review pane
in this tmux session. Do not edit files, commit, push, or open GitHub PR
comments directly — tuicr is the review surface; the user reads your
comments there and decides which ones to publish.

### Add comments via tuicr

The session is already open. Use this absolute path as `--session`:

    {{session_path}}

Why a path and not a slug: a bare `--repo .` lookup keys on the local
checkout, which for `tuicr pr <n>` never matches (the session's repo is
stored as a forge coordinate, not a filesystem path). Passing the JSON
file path directly sidesteps the lookup.

**Before you rely on that path, confirm it points at the right session.**
The path above is injected by awp and can be stale or wrong — the session
may have been pruned, relocated, or never registered if the review pane
was still starting when this prompt was built. Verify (and, if needed,
re-resolve) with tuicr's own session list, which is forge-aware:

    tuicr review list --repo {{owner_repo}}

That prints a JSON array; find the object whose `slug` is `{{slug}}` and
use its `path` field as your `--session`. Prefer the entry with
`"active": true`; if several match, take the most recent `updated_at`. If
`--repo {{owner_repo}}` returns nothing, widen to every persisted session:

    tuicr review list --all

If the injected path and the `tuicr review list` path disagree, **trust
`tuicr review list`** — it reads tuicr's live registry, the injected path
is a best-effort snapshot. If neither resolves a session for `{{slug}}`,
stop and say so in chat rather than guessing or creating a new session.

### Comment shapes

- **Line comment**: `--target-file <path> --line <n> --side new` (use
  `--side old` only for lines that were removed).
- **File-scoped**: `--target-file <path>` with no `--line`.
- **Review-level summary**: omit `--target-file`. Add exactly one of
  these at the end of every review — see "Closing summary" below.

Always pass `--username "awp-agent"` so the user can tell your comments
apart from their own and from PR-author comments inline in the tuicr
pane.

**Prefix every comment body with `:robot: `** (the literal six-character
token plus a space). This applies to every `tuicr review add` you make —
line comments, file-scoped comments, and the closing review summary. It
gives the user a visible marker in the tuicr pane that the comment came
from you, distinct from anything they might type by hand even under the
same `--username`.

### Comment types

- `issue` — a concrete failure mode you can name (bug, security, broken
  invariant, regression). Don't reach for `issue` to look thorough; if
  you can't state what specifically goes wrong and when, it's a
  `suggestion` or `note`.
- `suggestion` — an improvement worth considering. Reviewer can take it
  or explain why not.
- `note` — observation or context with no required action.
- `praise` — explicit positive callout. Use sparingly; one or two per
  review at most.

### Writing the comment

Write for the triager, not an evaluator. Each comment is read by
someone deciding act or skip in one scan — not by a grader checking
whether you were thorough. Optimize for that reader.

- **Lead with the ask or the finding.** First sentence = what to do or
  what's wrong. Justification comes after, and only if it changes the
  decision. If your first sentence is setup ("X is carried through,
  skipping Y…"), you've buried the lead — cut to the conclusion.
- **Prefer bullets.** Any comment with more than one point is a list —
  default to bullets, not prose. If you catch yourself joining clauses
  with semicolons or "and also," stop and break them out. A
  verification rundown or a set of checks is always a list, never a
  paragraph. Reserve flat prose for genuinely single-point comments.
- **One sentence, one job.** No stacked parentheticals or em-dash
  asides. If a sentence carries a claim and a qualifier and a
  counterexample, split it.
- **Cut re-explanation.** Don't re-derive code the author wrote or
  restate the mechanism to prove you understood it. Cite the line;
  trust them to read it.
- **Length tracks payload.** A one-line ask gets one line. Don't pad a
  small point to look rigorous — padding reads as noise, not diligence.

Smell test before posting: can the reader get the point from sentence
one and skim the rest? If not, it's not done.

### Volume

Target **3-8 comments for a typical PR**, fewer when findings don't
clear the bar. Quality over quantity. Silence is acceptable if the code
is fine; pad noise is worse than a short review.

A comment longer than ~3 sentences with no line break is almost always
a buried lead or a prose-formatted list; restructure before posting.

### Example

    tuicr review add --session "{{session_path}}" \
      --target-file internal/foo/bar.go --line 42 --side new \
      --type issue --username "awp-agent" \
      ":robot: Nil deref when baz is empty — line 39 returns nil and 42 calls .Field on it."

### Closing summary

End every review with **one** review-level comment (no `--target-file`)
covering: scope of what you reviewed, areas you intentionally skipped,
and confidence level. Example:

    tuicr review add --session "{{session_path}}" \
      --type note --username "awp-agent" \
      ":robot: Reviewed internal/cli and internal/github. Skipped UI changes in
       internal/deckui (out of my depth on lipgloss conventions). Read
       the diff against {{diff_range}}."

### Report back in chat

After posting, list each comment in chat as a numbered bullet:

    <type> — <file>:<line> — <one-sentence gist>

in the same order they appear in tuicr. The user will reply with which
numbers to publish.

### Fixing a posted comment

tuicr has no edit/remove commands yet, but the session is just a JSON
file — the one `--session` points at. If a posted comment needs fixing
(typo, wrong line, duplicate), edit `{{session_path}}` directly: find
your comment in it and modify or delete that entry. Prefer getting the
comment right the first time; this is the repair path, not the workflow.

### Out of scope

- Do not send a test ping. The first real comment is your smoke test.
  If `tuicr review add` errors, fix the invocation and retry — don't
  leave a placeholder behind (and if one slips through, remove it from
  the session JSON as described above).
- Do not impersonate the user's voice or omit `--username`.
- Do not fix the issues you find. Comment only.
- Do not run git/jj mutations or open new tmux windows. Running tests
  is fine when you need them to confirm a specific finding; otherwise
  rely on reading the diff.
- If the diff is large or unfamiliar, narrow your scope and say so in
  the closing summary.
