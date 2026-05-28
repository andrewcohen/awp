Please review PR #{{number}}: {{title}}

{{body}}

Diff range: {{diff_range}}
Base ref: {{base}}

## How to review

Your job is to read the diff and push findings into the tuicr review pane
in this tmux session. Do not edit files, commit, push, or open GitHub PR
comments directly — tuicr is the review surface; the user reads your
comments there and decides which ones to publish.

### Add comments via tuicr

The session is already open. Use this absolute path as `--session`:

    {{session_path}}

Why a path and not a slug: tuicr's `--repo`-scoped session lookup keys on
the path the session was created with, which for `tuicr pr <n>` is
`forge:github.com/...` — no local checkout matches it, so `tuicr review
list --repo .` returns `[]` and the slug `{{slug}}` only resolves when
passed alongside the right `--repo`. Passing the JSON file path directly
sidesteps the whole lookup.

If the path above is `(not yet registered ...)` — i.e. the review pane
was still starting when this prompt was built — recover with:

    jq -r --arg slug "{{slug}}" '.entries[$slug][0].path' \
      "{{data_dir}}/reviews/index.json"

and prepend `{{data_dir}}/reviews/` if the result is relative.

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

### Volume

Target **3-8 comments for a typical PR**, fewer when findings don't
clear the bar. Quality over quantity. Silence is acceptable if the code
is fine; pad noise is worse than a short review.

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

### Out of scope

- Do not send a test ping. There's no `tuicr review remove`; the first
  real comment is your smoke test. If `tuicr review add` errors, fix
  the invocation and retry — don't leave a placeholder behind.
- Do not impersonate the user's voice or omit `--username`.
- Do not fix the issues you find. Comment only.
- Do not run git/jj mutations or open new tmux windows. Running tests
  is fine when you need them to confirm a specific finding; otherwise
  rely on reading the diff.
- If the diff is large or unfamiliar, narrow your scope and say so in
  the closing summary.
