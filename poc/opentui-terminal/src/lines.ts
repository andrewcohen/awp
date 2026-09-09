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
      const options = item.ask.options.map((option, at) => `${at + 1} ${option.label}`).join("   ");
      out.push({ text: `    asks: ${options}`, role: "ask" });
    }
  }

  return out;
};
