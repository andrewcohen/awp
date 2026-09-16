// Contrast, measured off what was painted.
//
// ── why this is not a table of ratios ─────────────────────────────────────
//
// The Latte palette was fixed by computing every chrome hue against its own
// base and darkening the ones that failed, and the numbers went into a comment.
// A comment is the wrong place for them: a token can be right and the rule
// applying it wrong, and the only thing that tells those two apart is sampling
// the element that is actually on screen. AGENTS.md says so about the palette
// work and again about the fonts, and both times the finding was that a
// declaration being emitted is not evidence that anything consumes it.
//
// So the style guide measures. `getComputedStyle` on the rendered swatch, the
// ratio worked out here, and the verdict beside it — which means the page
// re-measures itself when somebody changes a token, a theme or a rule, and
// nobody has to remember to update a comment.

/** The thresholds WCAG names, and what each is for. */
export const AA_TEXT = 4.5;

/** A mark rather than a word: a status dot, a rule, an icon. */
export const AA_MARK = 3;

/**
 * `rgb(30, 32, 48)` and `rgba(…)` as three channels, or nothing.
 *
 * A computed colour comes back in one of those two spellings in every engine
 * this window runs in, and nothing else is accepted: a colour that cannot be
 * read must report as unmeasurable rather than as a plausible wrong number.
 * `color(srgb …)` and `lab(…)` are the shapes that would arrive if a token
 * were ever written in one, and this says so by refusing.
 */
export const channels = (colour: string): readonly [number, number, number] | undefined => {
  const said = colour.trim();
  // A hex, for a colour that is a literal rather than a computed value — a
  // candidate palette being judged, or a terminal slot, neither of which is a
  // variable this window can sample off an element. Both spellings, because
  // `#abc` is what a person writes and `#aabbcc` is what a tool emits.
  const hex = /^#([\da-f]{3}|[\da-f]{6})$/iu.exec(said);
  if (hex !== null) {
    const digits = hex[1] as string;
    const full =
      digits.length === 3
        ? digits
            .split("")
            .map((one) => one + one)
            .join("")
        : digits;
    return [
      Number.parseInt(full.slice(0, 2), 16),
      Number.parseInt(full.slice(2, 4), 16),
      Number.parseInt(full.slice(4, 6), 16),
    ] as const;
  }
  const found = /^rgba?\(\s*([\d.]+)[,\s]+([\d.]+)[,\s]+([\d.]+)(?:[,\s/]+([\d.]+))?/u.exec(said);
  if (found === null) {
    return undefined;
  }
  const [, r, g, b, a] = found;
  // ── `rgba(0, 0, 0, 0)` is not black ──────────────────────────────────────
  //
  // It is *nothing*, and it is what `getComputedStyle` answers for any element
  // with no background of its own. Read as three channels it becomes black,
  // and every ratio measured against it is a plausible number about a colour
  // nobody painted.
  //
  // That is not hypothetical: the style guide measured every ink against the
  // rows container, which has no background, so the whole page reported its
  // hues against **black** in both themes — `text 2.63 FAIL` for a token that
  // clears AA comfortably. The caller has to find a real ground (walk up the
  // tree) and this is what makes it notice rather than be told a wrong number.
  //
  // A *partly* transparent colour is still read by its own channels, which is
  // an approximation and is what the test above says. Alpha 0 is not an
  // approximation of anything.
  if (a !== undefined && Number(a) === 0) {
    return undefined;
  }
  const parts = [Number(r), Number(g), Number(b)] as const;
  return parts.every((one) => Number.isFinite(one)) ? parts : undefined;
};

/** One channel, linearised. WCAG's own curve, not a gamma of 2.2. */
const linear = (value: number): number => {
  const unit = value / 255;
  return unit <= 0.04045 ? unit / 12.92 : ((unit + 0.055) / 1.055) ** 2.4;
};

export const luminance = (colour: readonly [number, number, number]): number => {
  const [r, g, b] = colour;
  return 0.2126 * linear(r) + 0.7152 * linear(g) + 0.0722 * linear(b);
};

/**
 * The ratio between two painted colours, or nothing if either is unreadable.
 *
 * Order does not matter — the lighter of the two is the numerator either way,
 * which is why this takes a foreground and a background and does not care
 * which is which.
 */
export const ratio = (fg: string, bg: string): number | undefined => {
  const one = channels(fg);
  const two = channels(bg);
  if (one === undefined || two === undefined) {
    return undefined;
  }
  const a = luminance(one);
  const b = luminance(two);
  return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
};

/**
 * How a ratio reads: `7.4 AAA`, `4.6 AA`, `2.6 FAIL`.
 *
 * `AA` at 4.5 and `AAA` at 7 are the text thresholds. A swatch that is only
 * ever a mark — a status dot, a border — is held to 3 instead, and the caller
 * says which by passing the floor it means. Both are stated rather than
 * inferred, because the same token is a word in one place and a dot in
 * another: `live` is text on a session row and a bullet beside it.
 */
export const verdict = (found: number | undefined, floor: number = AA_TEXT): string => {
  if (found === undefined) {
    return "unmeasurable";
  }
  const said = found.toFixed(2);
  if (found >= 7) {
    return `${said} AAA`;
  }
  if (found >= AA_TEXT) {
    return `${said} AA`;
  }
  return found >= floor ? `${said} mark` : `${said} FAIL`;
};

/**
 * A painted colour as `#1e2030`, or nothing if it could not be read.
 *
 * Off the element rather than out of `tokens.stylex.ts`, for the same reason
 * the ratio is: what is wanted is the value that reached the screen. A hex
 * copied from the source would be right about the token and silent about the
 * rule that applied it.
 */
export const hexOf = (colour: string): string | undefined => {
  const found = channels(colour);
  if (found === undefined) {
    return undefined;
  }
  return `#${found.map((one) => Math.round(one).toString(16).padStart(2, "0")).join("")}`;
};
