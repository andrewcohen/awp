// Finding a row by a word somebody remembers.
//
// Its own directory and not `panels/`, because #58's command palette is this
// same question asked of actions rather than tasks, and two implementations of
// a matcher disagree about what matches the week after the second one is
// written.
//
// Nothing here imports a component or a token — see `routing/browse.ts` for
// why that matters: `stylex.defineConsts` cannot be evaluated outside the
// Babel pass, so a test importing a module that reaches one dies before it
// gets to the function.
//
// ── two matchers, because one of them is a lie at length ───────────────────
//
// Subsequence matching is the right answer for a title and the wrong one for a
// body, and the reason is arithmetic rather than taste:
//
//   subject      ~50 characters    "fzp" finds "FuZzy search over the Panel",
//                                  and a miss is a real miss
//   description  up to 6722 here   over that much prose almost any short query
//                                  is a subsequence of almost every task, so a
//                                  subsequence filter is a filter that keeps
//                                  everything
//
// So a description is matched by substring — which is also what a person
// typing a remembered phrase is actually doing — and a subject fuzzily.

/** Half-open, into the string it came from. */
export type Span = readonly [number, number];

/** Matched offsets, collapsed into runs. */
const spansOf = (at: ReadonlyArray<number>): ReadonlyArray<Span> => {
  const spans: Array<Span> = [];
  for (const one of at) {
    const last = spans.at(-1);
    if (last !== undefined && last[1] === one) {
      spans[spans.length - 1] = [last[0], one + 1];
      continue;
    }
    spans.push([one, one + 1]);
  }
  return spans;
};

/**
 * Every character of the query, in order, somewhere in the text.
 *
 * Two passes, and the second is what makes the highlight legible. A forward
 * greedy scan finds *a* match and habitually finds a scattered one — `tsk`
 * against "the tasks panel" takes the `t` of "the". The backward pass then
 * pulls each matched character as late as it can go, which gathers the run
 * onto "**t**a**sk**s" where a reader would have put it.
 *
 * Case-insensitive, and whitespace in the query is dropped rather than matched:
 * a space typed between two words is a pause, not a character somebody is
 * claiming appears in the subject.
 */
export const fuzzy = (query: string, text: string): ReadonlyArray<Span> | undefined => {
  const want = query.toLowerCase().replaceAll(/\s+/gu, "");
  if (want === "") {
    return [];
  }
  const said = text.toLowerCase();
  const at: Array<number> = [];
  let from = 0;
  for (const letter of want) {
    const found = said.indexOf(letter, from);
    if (found === -1) {
      return undefined;
    }
    at.push(found);
    from = found + 1;
  }
  // Latest-possible, from the back. Each character may move right up to the
  // one after it; the last may move to the end of the text.
  for (let i = at.length - 1; i >= 0; i -= 1) {
    const ceiling = i === at.length - 1 ? said.length - 1 : (at[i + 1] as number) - 1;
    const later = said.lastIndexOf(want[i] as string, ceiling);
    if (later > (at[i] as number)) {
      at[i] = later;
    }
  }
  return spansOf(at);
};

/**
 * The query as it was typed, somewhere in the text.
 *
 * Every occurrence and not only the first: an excerpt is cut around one of
 * them, and a body that says the word four times should light all four up
 * inside whatever window is shown.
 */
export const contains = (query: string, text: string): ReadonlyArray<Span> | undefined => {
  const want = query.toLowerCase().trim();
  if (want === "") {
    return [];
  }
  const said = text.toLowerCase();
  const spans: Array<Span> = [];
  let from = 0;
  for (;;) {
    const found = said.indexOf(want, from);
    if (found === -1) {
      break;
    }
    spans.push([found, found + want.length]);
    from = found + want.length;
  }
  return spans.length === 0 ? undefined : spans;
};

export interface Piece {
  /** Where it starts in the original, which is what a react key is made of. */
  readonly at: number;
  readonly text: string;
  readonly hit: boolean;
}

/** The text split into what matched and what did not, in order. */
export const pieces = (text: string, spans: ReadonlyArray<Span>): ReadonlyArray<Piece> => {
  const out: Array<Piece> = [];
  let at = 0;
  for (const [from, to] of spans) {
    if (from > at) {
      out.push({ at, text: text.slice(at, from), hit: false });
    }
    out.push({ at: from, text: text.slice(from, to), hit: true });
    at = to;
  }
  if (at < text.length) {
    out.push({ at, text: text.slice(at), hit: false });
  }
  return out;
};

/**
 * One line of the body, around the first thing that matched.
 *
 * A description hit cannot be shown by opening the description: the bodies here
 * run to thousands of characters of markdown, and a filter that answers with a
 * wall of prose per row has replaced the list it was meant to narrow. So the
 * answer is grep's — the window the match is in, and the rest said by an
 * ellipsis.
 *
 * **The text handed in is already one line**, flattened by `flatten` before it
 * was matched, and that is the whole reason this function only slices. Cutting
 * a window and *then* collapsing its whitespace moves every offset after the
 * first run of spaces by an amount nothing here knows — the spans would light
 * up the wrong characters, and only in bodies with a blank line before the
 * match, which is most of them.
 */
export const excerpt = (
  text: string,
  spans: ReadonlyArray<Span>,
  width = 80,
): { readonly text: string; readonly spans: ReadonlyArray<Span> } => {
  const first = spans[0];
  if (first === undefined) {
    return { text: "", spans: [] };
  }
  // A third of the window ahead of the match rather than half: what follows a
  // remembered phrase is what says whether this is the right task, and what
  // precedes it is only there so the fragment reads as a sentence.
  const from = Math.max(0, first[0] - Math.floor(width / 3));
  const to = Math.min(text.length, from + width);
  const head = from > 0 ? "…" : "";
  const shift = head.length - from;
  const inside: Array<Span> = [];
  for (const [a, b] of spans) {
    if (a >= from && b <= to) {
      inside.push([a + shift, b + shift]);
    }
  }
  return {
    text: `${head}${text.slice(from, to)}${to < text.length ? "…" : ""}`,
    spans: inside,
  };
};

/** A body as one line, so an excerpt of it can be cut by offset alone. */
export const flatten = (text: string): string => text.replaceAll(/\s+/gu, " ").trim();
