// Finding the urls in text nobody is allowed to reformat.
//
// ── why this exists beside a markdown renderer ─────────────────────────────
//
// An agent's message goes through `Markdown.tsx`, where `remark-gfm` turns a
// bare url into a link for free. Your own message deliberately does not: it is
// drawn as text exactly as you typed it, because rendering it would mean a
// message containing `# ` silently becoming a heading, which is the window
// editing what somebody said.
//
// That decision is right and it took the links with it — so a url pasted into
// your own message was dead, in the one place a person pastes urls most.
//
// Linkifying is the narrow version of the same want. It adds no styling, drops
// no characters and reorders nothing: every byte of the original is still on
// screen, in order, and the only difference is that some of it is now inside an
// anchor. There is no input for which this changes what the message *says*,
// which is exactly what could not be promised about rendering markdown.
//
// ── what counts as a url ───────────────────────────────────────────────────
//
// `https:` and `http:` and nothing else. The temptation is `www.`, bare hosts,
// `file:` — and each one costs more than it pays:
//
//   www.foo.com      a host with no scheme is a guess about what to prepend,
//                    and a guess in an anchor is a link that goes somewhere
//                    the text did not say
//   trees.com        indistinguishable from a sentence ending in a word and
//                    a two-letter word. "See index.ts" is not a link
//   file:///…        the window opens links in the person's browser; handing
//                    a local path to it is at best nothing and at worst a
//                    file manager opening somebody's home directory
//
// The same two schemes `addressFor` accepts for the web panel, and for the
// same reason: two schemes are what a person can predict.

/**
 * A run of text, and whether it is a link.
 *
 * Returned as a list rather than as html, because the caller builds React
 * elements — the same argument `Markdown.tsx` makes for `react-markdown` over
 * `marked`: nothing here ever produces a string of markup, so there is no
 * sanitiser to get right and no `dangerouslySetInnerHTML` to audit.
 */
export type Piece = { readonly text: string; readonly href: string | undefined };

/**
 * Where a url stops, which is the only hard part.
 *
 * A url at the end of a sentence is followed by punctuation that is not part of
 * it, and the punctuation people actually write is closing rather than opening:
 *
 *   look at https://example.invalid/build/412.        the full stop is prose
 *   (see https://example.invalid/build/412)           the bracket is prose
 *   https://example.invalid/wiki/Foo_(bar)            the bracket is the URL
 *
 * The last is why brackets are balanced rather than simply stripped: Wikipedia
 * and every wiki like it put parentheses inside urls, and a rule that dropped
 * the trailing one would break the link while leaving it looking fine.
 */
const TRAILING = /[.,;:!?'"]+$/;

const balanced = (url: string): string => {
  let out = url;
  // Repeated, because a url can end in more than one kind of trailing junk —
  // `…/Foo).` is a bracket that is prose and then a full stop that is prose.
  for (;;) {
    const trimmed = out.replace(TRAILING, "");
    const closing = trimmed.endsWith(")")
      ? // Only when it is unmatched. Counting is what separates a bracket the
        // url owns from one the sentence around it does.
        (trimmed.match(/\(/g) ?? []).length < (trimmed.match(/\)/g) ?? []).length
      : false;
    const next = closing ? trimmed.slice(0, -1) : trimmed;
    if (next === out) {
      return out;
    }
    out = next;
  }
};

const URLS = /https?:\/\/[^\s<>]+/g;

/**
 * Split text into runs, marking the urls.
 *
 * Always returns the whole of the input: joining every piece's `text` gives
 * back exactly what was passed in. `linkify.test.ts` asserts that on every
 * case, because it is the promise that makes this safe to apply to somebody's
 * own words.
 */
export const linkify = (text: string): ReadonlyArray<Piece> => {
  const pieces: Array<Piece> = [];
  let at = 0;
  for (const found of text.matchAll(URLS)) {
    const start = found.index;
    const whole = found[0];
    const url = balanced(whole);
    // A match that is entirely trailing punctuation once trimmed — `https://`
    // on its own — is not a link, and must still reach the output as text.
    if (url === "" || url === "https://" || url === "http://") {
      continue;
    }
    if (start > at) {
      pieces.push({ text: text.slice(at, start), href: undefined });
    }
    pieces.push({ text: url, href: url });
    at = start + url.length;
  }
  if (at < text.length) {
    pieces.push({ text: text.slice(at), href: undefined });
  }
  return pieces;
};
