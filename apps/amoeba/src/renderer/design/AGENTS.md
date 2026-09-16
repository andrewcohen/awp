# design

The palette, the type scale, the motion presets and the style guide. Loaded
when working under `src/renderer/design/`; the rules every component obeys —
the accent budget, the 14px floor, StyleX's silent failures — stay in
`apps/amoeba/AGENTS.md`. Evidence is in `docs/window.md`, which no session
loads.

## Colour

Tokens in `tokens.stylex.ts`; the `Record<ChromeRole, string>` tables in
`theme.ts` make a half-finished addition a type error.

**Latte's accents are not text colours** — muted, live and accent all measure
under 3.0 against a 4.5 threshold. Catppuccin's Latte is tuned to be an accent on
a light surface, not ink on one; that is upstream working as intended. Each hue
is darkened **along its own hue and saturation** until it clears 4.6, rather than
swapped for another palette.

**That is allowed in `tokens.stylex.ts` and would not be in `palette.ts`:** the
pane's sixteen slots must be upstream's exact hexes, or a program picking colours
against them looks wrong. Latte is not Macchiato with the ends swapped — its ANSI
black is subtext1, not surface1.

**Compute contrast off the rendered element, not the source hex.** A token can be
right and the rule applying it wrong.

**`setPaneTheme` can only half work in ghostty-web 0.4.0** — colours are compiled
into the wasm terminal and only `reset()` rebuilds it, taking the scrollback with
it, so it repaints the renderer's half only.

**An accent is spent, not applied**, and it is on exactly four things. **An
accent marks a deviation from the rows around it, so the same field earns it in
one list and not another**: a PR number is an exception in the sidebar and the
baseline in the review queue, where the same rule drew thirty accents in one
column.

**`live`, `warn` and `muted` appear in two vocabularies on purpose** — a red
meaning broken is the same claim whatever the subject. `asked` nearly became
`ready`, which is the near miss worth naming: `ready` is an _agent_ state, so one
token for both makes a row ambiguous exactly while somebody scans for what to do
next.

## Type

Roles in `typeset.ts`, faces in `fonts.css`. `mono` is for anything somebody will
type somewhere else — a slug, a bookmark, a revision, a path, a command; `ui` for
everything read. The line is not "chrome versus pane".

**Ship the face, do not name it.** A font stack that misses fails in silence —
`Inter` is not installed, and `'New York'` **never resolves** in the web view
while reading as applied. `fonts.css` declares the faces by hand rather than
importing each package's `index.css`, which declares every subset published
(1.9MB against 189KB).

**Measure by rendering, and use a real element.** `getComputedStyle().fontFamily`
echoes the declaration back whether or not anything in it exists, and canvas
`measureText` reported every family identically. Render a string in the candidate
and in a family nobody has: a missing face matches the control exactly.

**Width is a real criterion** — the sidebar's caption lives in a 260px column,
and a monospace spends a third more of it on the same words.

**Changing the pane's face invalidates its size** — ghostty-web sizes a cell as
`ceil(measureText("M").width)`. Re-measure; do not carry the old note over.

**The floor is 14px, and it is about text.** Stated as a requirement so the scale
is built around it rather than clamped; a caption is separated by weight and
colour instead. A status bullet and an icon's em box are legitimately smaller; a
_word_ under 14px is a bug.

**A refactor that strips a declaration and misses its call site is silent.** The
typeset rewrite was proved a no-op by painting every element's
`fontSize | fontWeight | fontFamily` before and after — and the first run was
**not** zero: a rewriter had skipped one call site two levels of nested parens
deep.

## The style guide measures rather than asserting

`#/styleguide`, a route with a hand-made branch in `App` rather than a panel in
the strip — a page of swatches in the strip is a permanent empty room in the
column somebody switches most.

**Every ratio is computed off `getComputedStyle` on the swatch that was painted**,
so the page cannot go stale. **Ink and ground are different measurements**: one
measurement drew every role as ink and reported `1.00 FAIL` for five rows that
are fine.

**Transparent is not a colour, and reading it as one is silent.** Inks were
measured against a container with no background, `rgba(0, 0, 0, 0)` read as
black, and the numbers were _plausible_ — which is what made it survive: an
earlier session read them as a finding and wrote two into a rules file. `channels`
refuses alpha 0, and `groundAbove` walks up to the first thing painted. **A wrong
ratio is worse than none**: it sends the reader to darken a token that was
already right.

**It is a visual guide, so there is almost no text on it.** If a section needs a
caption to explain it, the title is wrong; anything a person might want in words
is a `title`, which costs no pixels until asked for.

**The page draws the window's own components, not copies** — a copy drifts, and
then the page is a picture of the window rather than the window. The chat
fixture's pair is a workspace that does not exist, so `Allow Once` refuses, which
is the honest answer.
