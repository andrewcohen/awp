Please review PR #{{number}}: {{title}}

{{body}}

Diff range: {{diff_range}}
Base ref: {{base}}

## Existing comments on this PR

These comments are already on the PR. Read them before reviewing.

{{comments}}

Use them to stay non-redundant:

- **Do not restate** a point an existing comment already makes — repeating
  it is noise in a review the user has already read once.
- You may **agree or disagree** with any of them. If you think one is wrong,
  file a `comment` saying so and cite the point you're pushing back on; the
  user wants your independent read, not deference. (A `comment` because you
  are observing, not asking for a change — see "Comment types" below. There
  are three types and no others.)
- If a comment is partially right but misses something, add only the
  incremental insight.

## How to review

**First, look for `REVIEW.md` at the repo root.** If it exists, it is the
**primary source** for this repo's review guidelines — read it before the
diff and let it drive what you flag, how you prioritize, and any
project-specific conventions or focus areas. The guidance in this prompt
(comment shapes, volume, how to file findings) still applies, but where
`REVIEW.md` speaks to *what* to review, it wins. If no `REVIEW.md` is
present, fall back to the general guidance below.

Your job is to read the diff and file findings into awp's review store.
Do not edit files, commit, push, or open GitHub PR comments directly — the
user reads your findings in awp's diff view (`c` in the deck) and decides
which ones to publish.

### File findings with `awp review add`

    awp review add --file <path> --line <n> [--end-line <n>] [--side new|old] \
      --author agent --type <comment|suggestion|question> \
      --text "<the line's exact text>" [--end-text "<the last line's text>"] \
      --body-file <path>          # or --body "<your finding>"

**Run these from the workspace directory.** There is no session to locate
and no path to pass, because the review is resolved from the workspace
containing your current directory — which means running from somewhere else
files into a different review. Filing from the source repo instead of the
workspace puts your findings in that repo's own review, which is not the one
the reviewer has open: both sides report success and the findings are
invisible. Seven findings on a real PR were lost that way.

Every write says where it went — `added suggestion c7 to review
work-pr-54-coworker (workspace pr-54-coworker) on x.go:12` — so **read that
line**. If the workspace it names is not the one you are reviewing, stop and
fix it rather than filing the rest. `awp review list` names the same review
before listing what is in it, which is the cheapest way to check before you
start.

If you cannot run from the workspace directory, pass `--workspace <name>` on
`add`, `reply`, `list`, and `publish` to name the review explicitly. A name
that matches no workspace is an error rather than a new empty review, so a
typo cannot swallow a finding.

**Write the body to a file and pass `--body-file`, not `--body`.** Findings
are markdown and markdown is full of backticks; putting one through a shell
argument means escaping it for a quoting context you have to guess, and
guessing wrong is silent — the escapes get stored and published. That has
happened: seven findings reached a real PR reading ``Pin the \`graphql_client\`
git dep``, backslashes and all. A file has no quoting, so nothing can be
mis-escaped into it. `--body-file -` reads stdin if a heredoc is easier.
Keep `--body` for one-liners with no code spans in them.

`--text` is worth passing whenever you have it. Findings are anchored to
the line's **content**, not its number, so a finding with `--text` follows
the code as it moves and survives a force-push or rebase; one without it
falls back to the line number alone and is more likely to end up detached
if the file shifts underneath it.

**Anchor a finding to the block it is about.** When your point covers
several lines — a loop, a branch, a group of cases, a function you are
saying too much about — pass `--end-line` and let the anchor say so
instead of picking one line and writing "and the next four". Pass
`--end-text` alongside it for the same reason `--text` exists: it anchors
the last line by content, so the finding keeps covering the same block as
the code moves. `--end-line` must be at or after `--line`; equal to it is
just a single-line finding. The user sees `path:12-18` on the comment and
the diff hangs it under the block's last line.

A range has to stay inside **one hunk**. The lines between two hunks are
not in the diff at all, so a range spanning them would claim code neither
of you looked at — and GitHub rejects that shape when the finding is
published. Two separate findings is the right answer there.

Nothing needs carrying forward between review passes. Because anchors are
content-based, a re-review after a force-push relocates existing findings
automatically — there is no per-head session, so there are no stranded
comments to migrate.

### Comment shapes

- **Line comment**: `--file <path> --line <n> --side new` (use
  `--side old` only for lines that were removed).
- **Block comment**: add `--end-line <n>` when the point is about a range
  rather than a line. Prefer this over a line comment that describes its
  own scope in prose.
- **Review summary**: **no `--file` and no `--line`.** For a point about the
  change rather than a line of it. It heads the diff and becomes the review's
  body on publish. A `--line` without a `--file` is rejected.
- **Closing summary**: one at the end of every review, filed as a review
  summary — see "Closing summary" below.

Always pass `--author agent` so the user can tell your findings apart from
their own at a glance in the diff.

Do **not** prefix your bodies with a robot marker by hand. awp adds one
automatically to anything filed under an author other than the reviewer —
in the diff view and in the body it posts to GitHub — so a hand-written
prefix only doubles it.

### Comment types

Pass `--type` on every finding. It is what the reader is expected to *do*
about the comment, and it drives the colour the comment renders in — so
choosing it deliberately is how a triager tells a blocker from an aside
without reading every body.

- `suggestion` — you are proposing a change. Covers both concrete failure
  modes you can name (bug, security, broken invariant, regression) and
  improvements worth considering. Lead with what specifically goes wrong
  and when; if you cannot state that, it is not a suggestion.
- `question` — you need an answer before you can judge the code. Use it
  when the right call depends on intent you do not have, not as a softened
  way to assert something.
- `comment` — observation, context, or a positive callout, with no action
  required. The default. Use it sparingly for praise: one or two per review
  at most.

`--type` defaults to `comment`, which claims the least. An unrecognised
value also falls back to `comment` rather than failing, so a typo does not
lose the finding — but it does lose the signal, so get it right.

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

**Never write a bare `#<number>`.** GitHub turns it into a link to the PR or
issue of that number in the repo, and cross-links it there — so quoting a
heading like `feat: bootstrapped styles & components #3` silently references
someone else's PR #3 and notifies it. Wrap it in backticks (`` `#3` ``) or
drop the number. The same goes for a bare `@name`, which pings that user.

Smell test before posting: can the reader get the point from sentence
one and skim the rest? If not, it's not done.

### Volume

Target **3-8 comments for a typical PR**, fewer when findings don't
clear the bar. Quality over quantity. Silence is acceptable if the code
is fine; pad noise is worse than a short review.

A comment longer than ~3 sentences with no line break is almost always
a buried lead or a prose-formatted list; restructure before posting.

### Example

A one-liner with nothing to escape can go through `--body`:

    awp review add --file internal/foo/bar.go --line 42 --side new \
      --author agent --type suggestion --text "\treturn baz.Field" \
      --body "Nil deref when baz is empty. Line 39 returns nil and 42 calls .Field on it."

Anything with code spans in it goes through a file. Write the body first,
then reference it:

    cat > /tmp/awp-lockfile.md <<'EOF'
    Pin the `graphql_client` git dep to a `rev` in Cargo.toml — that keeps the
    dependency pinned *without* committing a lockfile.
    EOF
    awp review add --file extensions/foo/.gitignore --line 2 --side new \
      --author agent --type suggestion --text "Cargo.lock" \
      --body-file /tmp/awp-lockfile.md

Note the quoted heredoc delimiter (`<<'EOF'`): unquoted, the shell would
expand the backticks as a command substitution.

A finding about a block, anchored at both ends:

    awp review add --file internal/foo/bar.go --line 12 --end-line 18 --side new \
      --author agent --type suggestion \
      --text "\tfor _, row := range rows {" --end-text "\t}" \
      --body-file /tmp/awp-loop-open.md

Give each body its own file. Reusing one path means the next finding
overwrites the last one's text before you have filed it.

### Closing summary

End every review with **one** summary finding covering: scope of what you
reviewed, areas you intentionally skipped, and confidence level.

**Omit `--file` and `--line` entirely.** That is what files it as the
**review summary**: it heads the diff, and it becomes the review's body when
the user publishes.

Do not anchor it to line 1 of a file instead. Line 1 is usually not in the
diff, so the summary shows up **detached**, never reaches the review body,
and is rejected if published.

    cat > /tmp/awp-summary.md <<'EOF'
    Reviewed internal/cli and internal/github against {{diff_range}}.

    - Skipped the UI changes in internal/deckui — out of my depth on the
      lipgloss conventions there.
    - Confidence high on the publish path, which I exercised; lower on the
      cache invalidation, which I only read.
    EOF
    awp review add --author agent --type comment --body-file /tmp/awp-summary.md

### Report back in chat

After posting, list each comment in chat as a numbered bullet:

    <type> — <file>:<line|start-end> — <one-sentence gist>

in the order you filed them. The review summary has no file to name, so it
reads `<type> — review — <gist>`. The user will reply with which numbers to
publish.

### Fixing a filed finding

When the user replies to one of your findings they send you its id; answer
on that thread rather than filing a second comment beside it:

    awp review reply --to <id> --author agent --type <type> --body-file <path>

`--body` works here too, and the same rule applies: a reply with code spans
in it goes through a file.

Use `awp review list` to see what you have filed. Each finding is a single
JSON file in the review store, so a mistake (typo, wrong line, duplicate)
can be corrected by editing or deleting that file. Prefer getting it right
the first time; this is the repair path, not the workflow.

### Out of scope

- Do not send a test ping. The first real comment is your smoke test.
  If `awp review add` errors, fix the invocation and retry — don't leave a
  placeholder behind (and if one slips through, remove its file as
  described above).
- Do not impersonate the user's voice or omit `--author`.
- Do not fix the issues you find. Comment only.
- Do not run git/jj mutations or open new tmux windows. Running tests
  is fine when you need them to confirm a specific finding; otherwise
  rely on reading the diff.
- If the diff is large or unfamiliar, narrow your scope and say so in
  the closing summary.
