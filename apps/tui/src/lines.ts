// Items to lines, because a column is a fixed number of them.
//
// The transcript is scrolled by line and not by item: an agent's answer is
// frequently taller than the window, and a scroll that moved a whole message
// at a time would jump over the part somebody was reading.

import type { Item } from "./conversation";

export type Line = {
  readonly text: string;
  readonly role: "user" | "agent" | "thought" | "tool" | "ask" | "gutter";
};

/** Wrap on spaces, and hard-break a word that is longer than the column. */
export const wrap = (text: string, width: number): ReadonlyArray<string> => {
  if (width <= 0) return [text];
  const out: string[] = [];
  for (const paragraph of text.split("\n")) {
    let line = "";
    for (const word of paragraph.split(" ")) {
      let rest = word;
      while (rest.length > width) {
        if (line !== "") {
          out.push(line);
          line = "";
        }
        out.push(rest.slice(0, width));
        rest = rest.slice(width);
      }
      const candidate = line === "" ? rest : `${line} ${rest}`;
      if (candidate.length > width) {
        out.push(line);
        line = rest;
      } else {
        line = candidate;
      }
    }
    out.push(line);
  }
  return out;
};

const MARK = { user: "you", agent: "···", thought: "  ~" } as const;

const status = (state: string): string =>
  state === "completed" ? "✓" : state === "failed" ? "✗" : state === "asking" ? "?" : "…";

export const linesOf = (items: ReadonlyArray<Item>, width: number): ReadonlyArray<Line> => {
  const out: Line[] = [];
  const body = Math.max(8, width - 6);

  for (const item of items) {
    if (out.length > 0) out.push({ text: "", role: "gutter" });

    if (item.kind === "said") {
      const label = MARK[item.role];
      for (const [at, text] of wrap(item.text.trimEnd(), body).entries()) {
        out.push({
          // The speaker is named once and the rest is indented under it, so a
          // long answer reads as one block rather than as a column of marks.
          text: `${at === 0 ? label.padEnd(4) : "    "}${text}`,
          role: item.role,
        });
      }
      continue;
    }

    const title = item.subagent === undefined ? item.title : `${item.title} · ${item.subagent}`;
    out.push({ text: `  ${status(item.status)} ${title}`, role: "tool" });
    // One line of output, because a tool row is a receipt: what ran and
    // whether it worked. The whole of a 4000-line grep is not a receipt.
    const first = item.output.split("\n").find((one) => one.trim() !== "");
    if (first !== undefined) {
      out.push({ text: `      ${first.slice(0, Math.max(0, body))}`, role: "tool" });
    }
    if (item.ask !== undefined) {
      // Answered, by whoever answered it — see the daemon's `answer`. The
      // options go, because offering them is offering a refusal.
      const options =
        item.ask.answered === undefined
          ? item.ask.options.map((option, at) => `${at + 1} ${option.label}`).join("   ")
          : item.ask.answered;
      out.push({
        text: item.ask.answered === undefined ? `    asks: ${options}` : `    ${options}`,
        role: "ask",
      });
    }
  }

  return out;
};

/**
 * DEAD END, kept for the note. Hard-wrapping markdown does not work.
 *
 * ── and the premise under it is no longer true, so re-read this ─────────
 *
 * The note below says `MarkdownRenderable` clips rather than wraps. Measured
 * again against the installed opentui 0.5.11, in a box the shape `Message`
 * draws — a 4-cell gutter and `width={inner - 4}` — and it **wraps**:
 *
 * ```
 *   ··· A heading            #eed49f bold
 *       A paragraph long enough to need breaking at this width,
 *       with inline code and bold in it.      ← wrapped, tail present
 *       - a list item that is itself long enough to want
 *       wrapping somewhere around here
 *       ┌───────┬─────────┐                   ← a table, drawn
 * ```
 *
 * So the reason prose is drawn as `text` is a finding that has expired, and
 * what it costs is every inline mark an agent writes: a heading, a list, a
 * table, `code`, **bold**. What `<markdown>` still does that this column
 * does not want is wrap a **fence** — the breaks in one are the content —
 * so a move to it is a decision about fences and not about wrapping.
 *
 * Nothing is deleted yet, because the change is a rendering somebody has to
 * look at rather than a bug.
 *
 * Wrapping the text before it gets there is safe for prose: a newline inside a
 * paragraph is a soft break and markdown joins it back up. It is *not* safe
 * for the three kinds of line whose breaks are the content, so those are left
 * exactly as they are:
 *
 *   ``` fences   a wrapped line of code is a different line of code
 *   | tables |   the renderer measures the columns itself
 *   headings     short by nature, and a wrapped one loses its level
 *
 * And then markdown joins them straight back up: a newline inside a paragraph
 * is a *soft* break and the parser's whole job is to undo it. Measured — the
 * tail of a wrapped sentence never appeared on screen. Prose is segmented out
 * and drawn as text instead; see `segments`.
 */
export const wrapMarkdown = (text: string, width: number): string => {
  if (width < 12) return text;
  const out: string[] = [];
  let fenced = false;
  for (const line of text.split("\n")) {
    if (/^\s*(?:```|~~~)/u.test(line)) {
      fenced = !fenced;
      out.push(line);
      continue;
    }
    if (fenced || line.length <= width || /^\s*[|#]/u.test(line)) {
      out.push(line);
      continue;
    }
    const marker = /^\s*(?:[*+-]\s+|\d+[).]\s+|>\s+)?/u.exec(line)?.[0] ?? "";
    const body = line.slice(marker.length);
    const room = Math.max(8, width - marker.length);
    const [first, ...rest] = wrap(body, room);
    out.push(marker + (first ?? ""), ...rest.map((one) => " ".repeat(marker.length) + one));
  }
  return out.join("\n");
};

export type Segment =
  | { readonly kind: "prose"; readonly text: string }
  | { readonly kind: "code"; readonly text: string; readonly language: string };

/**
 * An agent's message, split into the two things it is made of.
 *
 * The markdown renderable would be the obvious way to draw a message and it
 * cannot: it does not wrap, and it undoes any wrapping done for it. So prose
 * is drawn as text — which wraps, being text — and a fenced block is drawn as
 * code, highlighted by the same tree-sitter, and left unwrapped because the
 * line breaks in a fence *are* the content.
 *
 * The same split amoeba's chat panel makes, for a different reason: there it
 * is because a fence is three different things and one of them is a patch.
 */
export const segments = (text: string): ReadonlyArray<Segment> => {
  const out: Segment[] = [];
  let prose: string[] = [];
  let code: string[] = [];
  let language = "";
  let fenced = false;

  const flushProse = () => {
    const joined = prose.join("\n").trim();
    if (joined !== "") out.push({ kind: "prose", text: joined });
    prose = [];
  };

  for (const line of text.split("\n")) {
    const fence = /^\s*(?:```|~~~)\s*(\S*)/u.exec(line);
    if (fence !== null) {
      if (fenced) {
        out.push({ kind: "code", text: code.join("\n"), language });
        code = [];
        language = "";
        fenced = false;
      } else {
        flushProse();
        language = fence[1] ?? "";
        fenced = true;
      }
      continue;
    }
    if (fenced) code.push(line);
    else prose.push(line);
  }
  // An unclosed fence is what a message still being streamed looks like, so it
  // is a code block rather than a mistake.
  if (fenced && code.length > 0) out.push({ kind: "code", text: code.join("\n"), language });
  flushProse();
  return out;
};
