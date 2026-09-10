// A transcript's rows: what was said, and what was run.
//
// Split out of `Chat.tsx` because the screen is the part with the keyboard,
// the composer and the socket on it, and these are pure — an item in, rows
// out. It is also what the style guide's fixture would draw, the day this
// column has one.

import { useState } from "react";
import { heldBack } from "@awp-kit/protocol/tools";
import { type Item, titleOf, verbOf } from "./conversation";
import { segments } from "./lines";
import { CHROME, SPIN, SYNTAX } from "./theme";

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

/**
 * The mark on a tool row, and the running one turns.
 *
 * ── a still row and a working row read the same, and one of them is wrong ──
 *
 * `…` is what a call in flight had, and three dots are also what a truncated
 * anything looks like — so the one row somebody is waiting on was the
 * quietest thing in the column. The frame comes from the same `SPIN` the
 * header and the thinking line turn, on the same tick, so the three move
 * together rather than as three independent clocks.
 *
 * `tick` is undefined wherever nothing is turning — a finished run, the
 * render probe — and the mark falls back to the dots it had.
 */
const mark = (status: string, tick?: number) =>
  status === "completed"
    ? "✓"
    : status === "failed"
      ? "✗"
      : // The turn ended with this call still in flight — see
        // `settleHangingCalls`. Not a cross: nothing failed, and nothing is
        // known about what the call did.
        status === "cancelled"
        ? "⊘"
        : status === "asking"
          ? "?"
          : tick === undefined
            ? "…"
            : (SPIN[tick % SPIN.length] ?? "…");

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
export const Message = ({
  item,
  inner,
  streaming = false,
}: {
  item: Item;
  inner: number;
  /** Whether the turn that is writing this is still going. */
  streaming?: boolean;
}) => {
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
              /* ── a fence stays a fence ────────────────────────────────
                 `<markdown>` draws one itself and *wraps* it, and the line
                 breaks in a fence are the content: a wrapped line of code is
                 a different line of code. So prose is handed to markdown and
                 a fence is handed to the same tree-sitter directly, with no
                 wrapping. That split is the whole reason `segments` survives
                 the move. */
              <code
                key={index}
                width={inner - 4}
                content={part.text}
                filetype={part.language === "" ? "text" : part.language}
                syntaxStyle={SYNTAX}
              />
            ) : (
              /* ── prose is markdown, and it was text for a stale reason ──
                 `lines.ts` records that `MarkdownRenderable` clips instead
                 of wrapping. Re-measured against the installed 0.5.11 in
                 exactly this shape and it wraps — and draws the headings,
                 lists, tables, bold and inline code that being text threw
                 away. An agent writes all five in every other answer.

                 `streaming` while the turn is in flight, which is the
                 renderable's own instruction: the trailing block stays
                 unstable so a half-written table or a fence still being
                 typed is re-parsed rather than frozen wrong. */
              <markdown
                key={index}
                width={inner - 4}
                content={part.text}
                syntaxStyle={SYNTAX}
                streaming={streaming}
                fg={CHROME.text}
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

/**
 * One tool row: the mark, what it was where that is not obvious, and as much
 * of the title as fits.
 *
 * ── no column, because there is nothing to line up ──────────────────────
 *
 * The label used to be padded to nine cells so the titles shared an edge,
 * which is the window's rule and is right there — its panel is one of three
 * and the rows are short. Here the label is empty on most rows (see
 * `toolLabel`), so the column was nine spaces of nothing in front of every
 * command, and the edge it aligned was an edge nothing sat on.
 */
const Line = ({ item, inner, tick }: { item: Item; inner: number; tick?: number | undefined }) => {
  if (item.kind !== "tool") return undefined;
  const label = verbOf(item);
  return (
    <text
      width={inner}
      wrapMode="none"
      fg={CHROME.muted}
      content={`  ${mark(item.status, tick)} ${label === "" ? "" : `${label} `}${oneLine(
        titleOf(item),
        Math.max(12, inner - label.length - 5),
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
export const Call = ({
  item,
  inner,
  tick,
}: {
  item: Item;
  inner: number;
  /** The turning frame, while a turn is in flight. See `mark`. */
  tick?: number | undefined;
}) => {
  if (item.kind !== "tool") return undefined;
  const asking = item.ask !== undefined && item.ask.answered === undefined;
  return (
    <box width={inner} flexDirection="column" paddingBottom={1}>
      <Line item={item} inner={inner} tick={tick} />
      {/* ── the command, under what it was for ──────────────────────────
          A row drawn as its purpose has the command behind it, and this
          column has no tooltip to hide it in. So a call that stands alone —
          one that changed a file, or is asking something — shows both: the
          intent on the row and the mechanism dim beneath it.

          A rolled-up receipt gets one line and no more; there is nothing
          worth two lines in a `Read` that worked. */}
      {heldBack(item) ? (
        <text
          width={inner}
          wrapMode="none"
          fg={CHROME.muted}
          content={`      ${oneLine(item.title, Math.max(12, inner - 8))}`}
        />
      ) : undefined}
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
 * ── whole while the turn is running, one line once it is over ────────────
 *
 * The window keeps the last four of every run whatever is happening. Here
 * the column is the whole screen and the two states are worth telling apart:
 * while the agent is working these rows *are* the progress, and hiding all
 * but three hides the thing being waited for. When the turn ends the same
 * rows are a receipt — `ran 7 tools`, one line, and the mark says whether
 * any of them failed, which is the only part of a receipt anybody re-reads.
 */
export const Calls = ({
  items,
  inner,
  live,
  tick,
}: {
  items: ReadonlyArray<Item>;
  inner: number;
  /** Whether the turn that made these is still going. */
  live: boolean;
  /** The turning frame, while a turn is in flight. See `mark`. */
  tick?: number | undefined;
}) => {
  // ── and it opens again ────────────────────────────────────────────────
  //
  // A summary is only ever an offer to look, so the row is a control: click
  // it and the run comes back. There is no chord for it, because there is
  // no focus model in a transcript — every row would have to be reachable
  // before one row could be — and the mouse is already opentui's, which is
  // what makes a click something this can hear at all.
  const [open, setOpen] = useState(false);
  const failed = items.filter((item) => item.kind === "tool" && item.status === "failed").length;
  const count = items.length;

  // A live run is drawn whole and has no summary to click: the rows *are*
  // the progress, and a control that folded them would be offering to hide
  // the thing being waited for.
  if (live) {
    return (
      <box width={inner} flexDirection="column" paddingBottom={1}>
        {items.map((item, at) => (
          <Line key={at} item={item} inner={inner} tick={tick} />
        ))}
      </box>
    );
  }

  return (
    <box width={inner} flexDirection="column" paddingBottom={1}>
      {/* The whole row is the hit area, not the words: a two-cell target in
          a terminal is a control nobody hits. `onMouseDown` rather than a
          click, because a press is what a terminal reports first and the
          release may land somewhere else. */}
      <box
        width={inner}
        height={1}
        onMouseDown={() => {
          setOpen((was) => !was);
        }}
      >
        <text
          width={inner}
          wrapMode="none"
          fg={CHROME.muted}
          // A run is a failure if any of it was: a rolled-up `✓` over a call
          // that did not work is the one reading somebody would act on and
          // be wrong.
          content={`  ${failed > 0 ? "✗" : "✓"} ${open ? "▾" : "▸"} ran ${String(count)} tool${
            count === 1 ? "" : "s"
          }${failed > 0 ? ` · ${String(failed)} failed` : ""}`}
        />
      </box>
      {open ? items.map((item, at) => <Line key={at} item={item} inner={inner} />) : undefined}
    </box>
  );
};

/**
 * A compaction, as a rule across the column.
 *
 * ── the one row that is about the transcript rather than in it ───────────
 *
 * The adapter reports `/compact` as three ordinary agent sentences — see
 * `compactionOf` in the daemon, which is the one place they are recognised —
 * so left alone a compaction reads as the agent saying "compacting" twice in
 * the middle of its own answer, and nothing says that the conversation above
 * is no longer what it can see.
 *
 * A rule is the shape that says "everything above this is different from
 * everything below it", and it is the same shape the window draws. It turns
 * while it runs: a compaction of a long conversation is half a minute of
 * nothing else happening.
 */
export const Boundary = ({
  item,
  inner,
  tick,
}: {
  item: Item;
  inner: number;
  /** The turning frame, while it is running. See `mark`. */
  tick?: number | undefined;
}) => {
  if (item.kind !== "compacted") return undefined;
  const running = item.status === "running";
  const failed = item.status === "failed";
  const word = running
    ? `${SPIN[(tick ?? 0) % SPIN.length]} compacting`
    : failed
      ? "compacting failed"
      : "compacted";
  // Half the width either side, so the word sits in the middle whatever the
  // column is doing. Two cells is the floor: a rule that vanishes at a narrow
  // width is worse than a short one.
  const rule = "─".repeat(Math.max(2, Math.floor((inner - word.length - 4) / 2)));
  return (
    <box width={inner} flexDirection="column" paddingBottom={1}>
      <text
        width={inner}
        wrapMode="none"
        fg={failed ? CHROME.warn : CHROME.muted}
        content={`  ${rule} ${word} ${rule}`}
      />
      {failed && item.reason !== undefined && item.reason !== "" ? (
        <text width={inner} wrapMode="none" fg={CHROME.muted} content={`    ${item.reason}`} />
      ) : undefined}
    </box>
  );
};
