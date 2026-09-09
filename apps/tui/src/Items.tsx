// A transcript's rows: what was said, and what was run.
//
// Split out of `Chat.tsx` because the screen is the part with the keyboard,
// the composer and the socket on it, and these are pure — an item in, rows
// out. It is also what the style guide's fixture would draw, the day this
// column has one.

import type { Item } from "./conversation";
import { segments } from "./lines";
import { CHROME, SYNTAX } from "./theme";

/**
 * A tool row is a receipt: what ran, and whether it worked.
 *
 * Wrapped rather than clipped, but bounded — a title here is whatever command
 * was run, and some of them are a forty-line script. Wrapping one of those
 * unbounded gives a single tool call the whole transcript.
 */
const clip = (text: string, most: number) =>
  text.length > most ? `${text.slice(0, most - 1)}…` : text;

/**
 * One line, whatever it takes.
 *
 * A title is not a title: it is the command, and a `python3 - <<'PY'` carries
 * its whole script in it, newlines and all. Clipping counts characters and a
 * multi-line string was still multi-line after being clipped — which is why a
 * "one line" row was drawing nine. So the first line is taken *before* the
 * width is applied, and an ellipsis says the rest is there.
 */
const oneLine = (text: string, most: number): string => {
  const [first = "", ...rest] = text.split("\n");
  const trimmed = first.trimEnd();
  return clip(rest.length > 0 ? `${trimmed} …` : trimmed, most);
};

const mark = (status: string) =>
  status === "completed" ? "✓" : status === "failed" ? "✗" : status === "asking" ? "?" : "…";

/**
 * How many of a rolled-up run of calls are still drawn.
 *
 * The window keeps four. Three here, because this column is the whole screen
 * rather than one of three, and what a run of receipts is competing with is
 * the sentence somebody actually came to read.
 */
const SHOWN = 3;

/**
 * A patch, trimmed to whole hunks, and what it is a patch of.
 *
 * ── the trim is by hunk, not by line ──────────────────────────────────────
 *
 * An edit is worth drawing and a whole-file rewrite is not worth 900 rows of
 * transcript, so there is a bound. Cutting at a line would hand `<diff>` a
 * patch whose last hunk claims more lines than it carries — jsdiff parses the
 * counts — so what is dropped is whole hunks, and the header is kept because
 * that is what the parse needs.
 */
const PATCH_LINES = 32;

const HUNK = /^@@ /u;

interface Trimmed {
  readonly patch: string;
  readonly more: number;
}

const trim = (patch: string): Trimmed => {
  const lines = patch.split("\n");
  const head: string[] = [];
  const hunks: string[][] = [];
  for (const line of lines) {
    if (HUNK.test(line)) hunks.push([line]);
    else if (hunks.length === 0) head.push(line);
    else hunks.at(-1)?.push(line);
  }

  const kept: string[][] = [];
  let rows = 0;
  for (const hunk of hunks) {
    if (kept.length > 0 && rows + hunk.length > PATCH_LINES) break;
    kept.push(hunk);
    rows += hunk.length;
  }
  const dropped = hunks.slice(kept.length).reduce((all, hunk) => all + hunk.length, 0);
  return { patch: [...head, ...kept.flat()].join("\n"), more: dropped };
};

/** How tall the drawn patch is, so the box it sits in can be that tall. */
const rowsIn = (patch: string): number =>
  patch.split("\n").filter((line) => !/^(?:Index: |={3,}$|--- |\+\+\+ |$)/u.test(line)).length;

/**
 * What to highlight the code inside a patch as.
 *
 * The path's extension, because that is what a filetype is. opentui ships
 * four grammars — javascript, typescript, markdown, zig — and an unknown one
 * is not an error: the code draws unhighlighted, which is the right outcome
 * for a `.toml` and the reason nothing here keeps a list.
 */
const filetypeOf = (path: string): string => {
  const extension = path.split(".").at(-1)?.toLowerCase() ?? "";
  return extension === "ts" || extension === "tsx"
    ? "typescript"
    : extension === "js" || extension === "jsx"
      ? "javascript"
      : extension === "md"
        ? "markdown"
        : extension;
};

/**
 * What somebody said, or what the agent answered.
 *
 * The speaker is named in a four-cell gutter and the words are indented under
 * it, so a long answer reads as one block rather than as a column of marks.
 */
export const Message = ({ item, inner }: { item: Item; inner: number }) => {
  if (item.kind !== "said") return undefined;
  return (
    <box width={inner} flexDirection="row" paddingBottom={1}>
      <text
        width={4}
        fg={CHROME.muted}
        content={item.role === "user" ? "you " : item.role === "thought" ? "  ~ " : "··· "}
      />
      {item.role === "agent" ? (
        <box width={inner - 4} flexDirection="column">
          {segments(item.text.trimEnd()).map((part, index) =>
            part.kind === "code" ? (
              <code
                key={index}
                width={inner - 4}
                content={part.text}
                filetype={part.language === "" ? "text" : part.language}
                syntaxStyle={SYNTAX}
              />
            ) : (
              <text
                key={index}
                width={inner - 4}
                wrapMode="word"
                fg={CHROME.text}
                content={part.text}
              />
            ),
          )}
        </box>
      ) : (
        <text
          width={inner - 4}
          wrapMode="word"
          fg={item.role === "user" ? CHROME.said : CHROME.muted}
          content={item.text.trimEnd()}
        />
      )}
    </box>
  );
};

/** One tool row: the mark, the kind, and as much of the title as fits. */
const Line = ({ item, inner }: { item: Item; inner: number }) => {
  if (item.kind !== "tool") return undefined;
  return (
    <text
      width={inner}
      wrapMode="none"
      fg={CHROME.muted}
      content={`  ${mark(item.status)} ${(item.subagent ?? item.toolKind ?? "tool").padEnd(7)} ${oneLine(
        item.title,
        Math.max(12, inner - 13),
      )}`}
    />
  );
};

/**
 * A tool call that has something to show: a change, or a question.
 *
 * The patch arrives composed — see `ChatDiff`, and the daemon is where it is
 * made so that this column and the window are drawing the same thing rather
 * than each diffing two texts. What is decided here is only how it is
 * coloured; see `patchRows` for why that is not the highlighter's job.
 */
export const Call = ({ item, inner }: { item: Item; inner: number }) => {
  if (item.kind !== "tool") return undefined;
  const asking = item.ask !== undefined && item.ask.answered === undefined;
  return (
    <box width={inner} flexDirection="column" paddingBottom={1}>
      <Line item={item} inner={inner} />
      {(item.diffs ?? []).map((diff, at) => {
        const shown = trim(diff.patch);
        return (
          <box key={at} width={inner} flexDirection="column" paddingLeft={4}>
            {/* opentui's own `diff`, which takes a unified patch and does the
                line numbers, the signs and the highlighting — the same shape
                the window's half has, where `CodeView` takes the same string.
                It parses with jsdiff, which is what composed it in the daemon.

                A first version drew the lines by hand after `<code
                filetype="diff">` turned out to be flat: there is no diff
                grammar in opentui's four, and an unknown filetype is not an
                error. This renderable is the answer to that, and it was there
                the whole time. */}
            <diff
              width={inner - 4}
              height={rowsIn(shown.patch)}
              diff={shown.patch}
              view="unified"
              filetype={filetypeOf(diff.path)}
              syntaxStyle={SYNTAX}
              wrapMode="none"
              // The palette's own hues rather than the defaults, which are a
              // generic green and red and read as another application's.
              lineNumberFg={CHROME.muted}
              addedBg={CHROME.addedBg}
              removedBg={CHROME.removedBg}
              addedSignColor={CHROME.live}
              removedSignColor={CHROME.warn}
            />
            {shown.more > 0 ? (
              <text
                width={inner - 4}
                fg={CHROME.muted}
                content={`… ${String(shown.more)} more line${shown.more === 1 ? "" : "s"}`}
              />
            ) : undefined}
          </box>
        );
      })}
      {asking && item.ask !== undefined ? (
        <text
          width={inner}
          wrapMode="word"
          fg={CHROME.ask}
          content={`    asks: ${item.ask.options.map((one) => one.label).join("   ")}`}
        />
      ) : undefined}
    </box>
  );
};

/**
 * A run of calls that changed nothing, as one block.
 *
 * The tail is drawn and the rest is counted, which is the window's rule and
 * the same arithmetic: a turn is regularly a dozen calls between two
 * sentences, and a dozen equal rows are most of the transcript by height and
 * the least of it by interest.
 */
export const Calls = ({ items, inner }: { items: ReadonlyArray<Item>; inner: number }) => {
  const hidden = Math.max(0, items.length - SHOWN);
  return (
    <box width={inner} flexDirection="column" paddingBottom={1}>
      {hidden > 0 ? (
        <text
          width={inner}
          wrapMode="none"
          fg={CHROME.muted}
          content={`  · ${String(hidden)} earlier call${hidden === 1 ? "" : "s"}`}
        />
      ) : undefined}
      {items.slice(-SHOWN).map((item, at) => (
        <Line key={at} item={item} inner={inner} />
      ))}
    </box>
  );
};
