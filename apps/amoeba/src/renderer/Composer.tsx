import type { ChatConfigOption } from "@awp-kit/protocol";
import { ArrowUpIcon } from "@phosphor-icons/react/ArrowUp";
import * as stylex from "@stylexjs/stylex";
import { useState } from "react";
import { FOLD_MS } from "./columns";
import { Chip } from "./Chip";
import { type Command, completed, matching } from "./commands";
import { growth, useGrow } from "./grow";
import { typeset } from "./typeset";
import { colors } from "./tokens.stylex";

// What you type at an agent, and everything under it.
//
// ── lifted out of Chat.tsx, and for one reason ───────────────────────────
//
// The style guide needs to draw it. A composer copied onto that page would be
// a copy that drifts, and the whole argument for that page's chat section is
// that it renders the panel's own components — so what somebody criticises
// there is what ships. `Row` went the same way.
//
// What stayed behind in Chat.tsx is everything with a consequence: the send,
// the command that runs, the option that reaches the daemon. This file is the
// box, the menu, the chips, and the arithmetic of when each is worth drawing —
// a component that is a function of its props, which is exactly what a fixture
// can render.

/**
 * How a setting reads on its chip.
 *
 * The value's own name, and the setting's name when the value would not say
 * which setting it belongs to. `Manual` and `Opus` name themselves; `On` and
 * `Off` do not, so fast mode says `fast: off`.
 */
const nameOf = (option: ChatConfigOption): string => {
  const current = option.values.find((value) => value.value === option.currentValue);
  const said = current?.name ?? option.currentValue;
  return said === "On" || said === "Off" || said === "Default"
    ? `${option.name.toLowerCase()}: ${said.toLowerCase()}`
    : said;
};

/**
 * Past this, the figure is worth minding rather than merely knowing.
 *
 * There used to be a floor under it as well — nothing on screen until half
 * full, on the status bar's argument that a figure which is always there is
 * furniture. Asked for the other way ("can the context usage show in the chat
 * bottom bar"), and the ask is right for this one: a person deciding whether
 * to start a fresh conversation wants the number *before* it is a problem, and
 * unlike the status bar this row is already full of session facts, so one more
 * is not what teaches the eye to skip it.
 */
const NEARLY_FULL = 0.85;

const styles = stylex.create({
  // ── the composer ────────────────────────────────────────────────────────
  //
  // One box with the button inside it rather than a field and a button side by
  // side. Two controls in a row makes the field look short and puts the send
  // where a person's eye is not — at the end of a line they are not looking at
  // — where inside the box it sits under the last word they typed.
  // ── the command menu ────────────────────────────────────────────────────
  //
  // A list, not a popup: it takes height above the box rather than floating,
  // for the reason every overlay in this window is examined for — it is inside
  // a scrolling column, and a floating element there is either clipped by the
  // column or portalled out of it, and neither is worth it for two rows.
  menu: {
    display: "flex",
    flexDirection: "column",
    marginBottom: "0.4rem",
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.4rem",
    backgroundColor: colors.surface,
    // ── the cap ───────────────────────────────────────────────────────────
    //
    // Unfiltered, this list is every command the agent advertises — 57 on
    // this machine — and an uncapped one takes the whole column and pushes
    // the conversation off the top to say what typing one more letter would
    // narrow to three rows. Eight rows is enough to show that there is more
    // and to scroll for it.
    //
    // `overscrollBehavior: contain`, or reaching the end of the menu carries
    // on scrolling the transcript behind it.
    maxHeight: "13rem",
    overflowX: "hidden",
    overflowY: "auto",
    overscrollBehavior: "contain",
  },
  slash: {
    display: "flex",
    // A flex child in a scrolling column shrinks rather than overflowing, so
    // without this every row is squeezed instead of the list scrolling.
    flexShrink: 0,
    alignItems: "baseline",
    gap: "0.5rem",
    // Left-aligned, because it is a row in a list rather than a button.
    textAlign: "start",
    padding: "0.3rem 0.6rem",
    backgroundColor: { default: "transparent", ":hover": colors.raised },
    borderStyle: "none",
    color: colors.text,
    font: "inherit",
    cursor: "pointer",
  },
  /** The highlighted row. The accent is not spent here — see AGENTS.md: it is
      on four things in the whole window, and a transient menu is not one. */
  slashOn: { backgroundColor: colors.border },
  slashName: { flexShrink: 0 },
  // `flex: 1` with `minWidth: 0`. The pair, and a sentence that must clip
  // rather than widen a column.
  /** What it takes: `[file]`. Beside the name, because it is part of it. */
  slashHint: { flexShrink: 0, color: colors.muted },
  /** `awp` — the window runs this one, and nothing is sent. */
  slashMine: { flexShrink: 0, color: colors.muted },
  slashSaid: {
    flex: 1,
    minWidth: 0,
    overflow: "hidden",
    textOverflow: "ellipsis",
    whiteSpace: "nowrap",
    color: colors.muted,
  },
  composer: {
    padding: "0.9rem 1.25rem 1.1rem",
    borderTopStyle: "solid",
    borderTopWidth: 1,
    borderTopColor: colors.border,
    backgroundColor: colors.page,
  },
  box: {
    display: "flex",
    flexDirection: "column",
    gap: "0.4rem",
    padding: "0.55rem 0.6rem 0.5rem",
    borderStyle: "solid",
    borderWidth: 1,
    // The whole box takes the focus ring, because the whole box is the
    // control. `:focus-within` and not `:focus` — the thing being focused is
    // the textarea inside it.
    borderColor: { default: colors.border, ":focus-within": colors.accent },
    borderRadius: "0.5rem",
    // One step off the page, so the box somebody types into is visibly a
    // control sitting on the document rather than part of it.
    backgroundColor: colors.surface,
    transitionProperty: "border-color",
    transitionDuration: { default: "160ms", "@media (prefers-reduced-motion: reduce)": "0s" },
  },
  // ── one line, growing with what is in it ────────────────────────────────
  //
  // It was `rows={2}`, which is two lines of nothing under every conversation
  // — and still two lines when somebody pastes twelve. The height is set from
  // `scrollHeight` on every change instead, between one line and a cap, and
  // the transition is the window's own fold duration and curve.
  //
  // `overflowY: hidden` while it fits, or the element scrolls itself at the
  // exact moment it should be growing. Past the cap it becomes a scroller,
  // which is the honest end of "expand as you fill it".
  input: {
    // `flex: 1` with `minWidth: 0` — the pair. `width: 100%` on a flex child
    // beside a button is a row wider than the box it is in, which is the
    // window's most common cause of a horizontal scrollbar.
    flex: 1,
    minWidth: 0,
    resize: "none",
    overflowY: "auto",
    lineHeight: 1.5,
    color: colors.text,
    backgroundColor: "transparent",
    borderStyle: "none",
    padding: 0,
    outline: "none",
  },
  /**
   * The text and the send, on one line.
   *
   * `flex-end`, so the button stays with the last line as the box grows
   * rather than floating beside the middle of a paragraph.
   */
  line: { display: "flex", alignItems: "flex-end", gap: "0.5rem" },
  /** What Return does, said quietly and only when there is something to say. */
  hint: { color: colors.muted },
  // Round, and the size of the modal's, because it is the same control: an
  // arrow in a circle, at the end of the line somebody just typed.
  send: {
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    flexShrink: 0,
    width: "1.6rem",
    height: "1.6rem",
    padding: 0,
    borderRadius: "0.8rem",
    borderStyle: "none",
    backgroundColor: colors.accent,
    color: colors.base,
    cursor: "pointer",
  },
  /** Present and plainly unavailable, rather than gone. */
  shut: { backgroundColor: colors.border, color: colors.muted, cursor: "default" },
  settings: {
    display: "flex",
    alignItems: "center",
    gap: "0.35rem",
    marginTop: "0.55rem",
    // Wrapping rather than scrolling: this is inside the composer, and the
    // window's rule is that nothing grows a sideways scrollbar. Four chips in
    // a narrow agent column become two rows.
    flexWrap: "wrap",
  },
  spacer: { flex: 1 },
  full: { color: colors.muted },
  warn: { color: colors.warn },
});

export const Composer = ({
  draft,
  onDraft,
  onSend,
  onStop,
  working,
  onCommand,
  theirs = [],
  config,
  onSetOption,
  usage,
  onBox,
}: {
  readonly draft: string;
  readonly onDraft: (draft: string) => void;
  /** Enter, or the button. What a message *does* is the caller's. */
  readonly onSend: () => void;
  /**
   * Stop the turn the agent is in.
   *
   * Separate from `onSend` because it is the opposite act, and the button is
   * one control that becomes the other — see the send below.
   */
  readonly onStop: () => void;
  /**
   * Whether the agent is working, which is what makes the send a stop.
   *
   * A boolean and not the turn count the panel holds: what the composer needs
   * is whether there is anything to stop, and two overlapping turns are still
   * one press.
   */
  readonly working: boolean;
  /**
   * One of the window's own commands was chosen from the menu.
   *
   * Separate from `onSend` because a command is not a message — see
   * commands.ts. The caller still intercepts an exactly-typed command on send,
   * for the case where the menu is not open: `/new ` with a trailing space is
   * a command and is not a bare `/word`.
   */
  readonly onCommand: (command: Command) => void;
  /**
   * The agent's own commands, skills included.
   *
   * Listed beside the window's two and *not* intercepted — a skill is a
   * prompt, and the adapter passes it through to the CLI verbatim. Empty by
   * default so the style guide can draw the composer with no conversation
   * behind it.
   */
  readonly theirs?: ReadonlyArray<Command>;
  readonly config: ReadonlyArray<ChatConfigOption>;
  readonly onSetOption: (option: string, value: string) => void;
  /**
   * How full the context is, and in what.
   *
   * Absent until the adapter has said — which is the one case that draws
   * nothing, because a window that has just opened genuinely does not know.
   */
  readonly usage?:
    | {
        readonly full: number;
        readonly used?: number | undefined;
        readonly size?: number | undefined;
      }
    | undefined;
  /**
   * Told what the textarea is, for a caller that has to put the caret in it.
   *
   * The quote control is the reason: quoting types into the box and then
   * focuses it, and focus is not a prop. A callback rather than a ref object
   * to write into — the holder belongs to whoever declared it, and a
   * component that reaches into a prop's `.current` is a component mutating
   * something it does not own.
   */
  readonly onBox?: ((node: HTMLTextAreaElement | null) => void) | undefined;
}) => {
  /**
   * Which command in the menu is highlighted.
   *
   * Kept rather than derived from the draft, because the arrow keys move it —
   * and reset whenever the list changes underneath it, since an index into a
   * list that has just been filtered is an index into the wrong list.
   */
  const [picked, setPicked] = useState(0);
  const commands = matching(draft, theirs);
  const at = Math.min(picked, Math.max(0, commands.length - 1));

  // ── the box grows with what is in it ────────────────────────────────────
  //
  // One line at rest and taller as it fills, up to a cap, animated with the
  // window's own duration and curve — everything that appears or disappears
  // here moves, and a box that jumps from one line to four is the same
  // startle as a panel that pops. `grow.ts` holds the arithmetic, because the
  // new-thread brief is the same box.
  const hold = useGrow(draft);

  /** What the next keypress does, when that is not obvious. */
  const said =
    commands.length > 0
      ? "tab to complete, return to run"
      : draft.includes("\n")
        ? "shift+return for a new line"
        : "";

  /**
   * One element, two holders.
   *
   * This component always keeps its own — the height is measured off it on
   * every keystroke — and the caller is *told* when there is one, rather than
   * handed a holder to write into. The effect above reads only the local ref,
   * so nothing here touches an object it does not own.
   */
  const attach = (node: HTMLTextAreaElement | null): void => {
    hold(node);
    onBox?.(node);
  };

  return (
    <div {...stylex.props(styles.composer)}>
      {/* ── the command menu ───────────────────────────────────────────────

        Above the box rather than below it, because it is a list of things
        the *box* can become and the eye is already at the box. Shown only
        while the draft is a bare `/word`: a message about a path must not
        put a menu over the conversation.

        Not a Base UI menu, deliberately. This is not a popup with its own
        focus — the caret stays in the textarea and the keys are read
        there — so a component that moved focus would take the typing with
        it. What Base UI gives is roving focus and a portal, and neither is
        wanted here. */}
      {commands.length > 0 && (
        <div {...stylex.props(styles.menu)} role="listbox" aria-label="commands">
          {commands.map((command, index) => (
            <button
              key={command.name}
              type="button"
              role="option"
              aria-selected={index === at}
              // The pointer is a second way in, and a press must not take
              // focus off the box — `onMouseDown` with `preventDefault` is
              // what keeps the caret where it was.
              onMouseDown={(event) => event.preventDefault()}
              onClick={() => onCommand(command)}
              // The list scrolls now, so the arrow keys can walk the
              // highlight out of sight. `block: "nearest"` moves the menu by
              // the least that brings the row back — anything more scrolls
              // the column behind it as well.
              ref={(node) => {
                if (node !== null && index === at) node.scrollIntoView({ block: "nearest" });
              }}
              {...stylex.props(styles.slash, index === at && styles.slashOn)}
            >
              <span {...stylex.props(typeset.address, styles.slashName)}>{command.name}</span>
              {command.hint !== undefined && (
                <span {...stylex.props(typeset.address, styles.slashHint)}>{command.hint}</span>
              )}
              <span {...stylex.props(typeset.label, styles.slashSaid)}>{command.said}</span>
              {/* ── the window's two are marked, not the agent's dozens ────
                  An agent on a real machine advertises twenty commands and
                  the window has two, so marking the majority is marking the
                  baseline — the same arithmetic as the inbox's leading icon
                  and the accent's four sites. What a person needs to know
                  here is which rows do NOT reach their agent. */}
              {command.mine && <span {...stylex.props(typeset.label, styles.slashMine)}>awp</span>}
            </button>
          ))}
        </div>
      )}
      <div {...stylex.props(styles.box)}>
        <div {...stylex.props(styles.line)}>
          <textarea
            ref={attach}
            {...stylex.props(typeset.prose, styles.input, growth.eased(FOLD_MS))}
            value={draft}
            rows={1}
            placeholder="say something, or / for a command"
            onChange={(event) => {
              onDraft(event.target.value);
              // The list is re-filtered on every keystroke, so an index into
              // the old one names the wrong row. Reset rather than clamped:
              // the first match is what somebody narrowing a list means.
              setPicked(0);
            }}
            onKeyDown={(event) => {
              // ── the menu's keys, and only while it is open ─────────────
              //
              // Up and down rather than ctrl+j/k: those two are the window's
              // column chords and are given up inside a textarea, which is
              // where this caret is. Inside a list of two, the arrows are
              // what a person will press.
              if (commands.length > 0) {
                if (event.key === "ArrowDown" || event.key === "ArrowUp") {
                  event.preventDefault();
                  const step = event.key === "ArrowDown" ? 1 : -1;
                  setPicked((was) => (was + step + commands.length) % commands.length);
                  return;
                }
                if (event.key === "Tab") {
                  // Completion, not selection. Tab fills the box in and leaves
                  // the next gesture — Return — to run it, so a mistyped
                  // completion can still be edited or abandoned.
                  event.preventDefault();
                  const one = commands[at];
                  if (one !== undefined) {
                    onDraft(completed(one));
                  }
                  return;
                }
                if (event.key === "Enter" && !event.shiftKey) {
                  event.preventDefault();
                  const one = commands[at];
                  if (one !== undefined) {
                    onCommand(one);
                  }
                  return;
                }
                if (event.key === "Escape") {
                  // The draft is what the menu is open on, so clearing it is
                  // what closes the menu. `stopPropagation` because Escape is
                  // a window-level gesture elsewhere and this one is answered.
                  event.stopPropagation();
                  onDraft("");
                  return;
                }
              }
              // ── escape throws the draft away ──────────────────────────
              //
              // The same gesture the TUI's composer has, and it was here only
              // while the slash menu was open — so the two faces disagreed
              // about a key somebody presses by reflex: one abandoned the
              // message, the other did nothing at all.
              //
              // Only while there is something to throw away. An empty
              // composer lets Escape past, because it is a window-level
              // gesture elsewhere — a dialog over this panel is what closes.
              if (event.key === "Escape" && draft !== "") {
                event.stopPropagation();
                onDraft("");
                return;
              }
              // The same rule the pane has: Return sends, shift+Return is a
              // newline. A composer where Return inserts a line is one where
              // every message needs a second gesture to leave.
              if (event.key === "Enter" && !event.shiftKey) {
                event.preventDefault();
                onSend();
              }
            }}
          />
          <button
            type="button"
            data-nav-item
            aria-label="send"
            title={draft.trim() === "" ? "say something first" : "send (return)"}
            {...stylex.props(styles.send, draft.trim() === "" && styles.shut)}
            onClick={onSend}
            disabled={draft.trim() === ""}
          >
            {/* The same control the new-thread window uses. An arrow because
                the box it sits in is already the message — the word `send`
                there is a label on the obvious — and because the two places a
                person types at an agent should not look like two different
                applications. */}
            <ArrowUpIcon size={13} weight="bold" aria-hidden />
          </button>
        </div>
        {/* ── the hint has to earn its line ────────────────────────────────
            Reported as "the composer is still 2 lines", and it was: the text
            was one line and a permanent hint row under it was the second. The
            box is now one line at rest with the send beside the text, and this
            appears only when there is something to say about the next
            keypress — which is exactly when somebody is about to press it. */}
        {said !== "" && <span {...stylex.props(typeset.label, styles.hint)}>{said}</span>}
      </div>

      {/* ── what this session is running as ──────────────────────────────

        Under the composer rather than in the agent bar, because these are
        facts about the *session* and the bar is the window's own chrome.
        They also read in the right order down here: what you are about to
        say, and then who is about to answer it.

        One shape for all of them. The adapter answers `mode`, `model`,
        `effort` and `fast` as four selects with the same fields, so there
        is nothing bespoke per setting — and a fifth appearing upstream is
        a row that shows up rather than a thing to add here. */}
      {(config.length > 0 || usage !== undefined) && (
        <div {...stylex.props(styles.settings)}>
          {config.map((option) => (
            <Chip
              key={option.id}
              id={`chat-${option.id}`}
              label={nameOf(option)}
              title={option.description ?? option.name}
              value={option.currentValue}
              onChange={(value) => onSetOption(option.id, value)}
              options={option.values.map((value) => ({ value: value.value, label: value.name }))}
              quiet
            />
          ))}

          <span {...stylex.props(styles.spacer)} />

          {/* Whenever there is a reading. The tokens go on the hover rather
            than in the row: `18,606 of 200,000` is the answer to "how much is
            that", and it is four times the width of the answer to "how full
            is it". */}
          {usage !== undefined && (
            <span
              title={
                usage.used === undefined || usage.size === undefined
                  ? "how much of the context window this conversation has spent"
                  : `${usage.used.toLocaleString()} of ${usage.size.toLocaleString()} tokens`
              }
              {...stylex.props(
                typeset.label,
                styles.full,
                usage.full >= NEARLY_FULL && styles.warn,
              )}
            >
              {Math.round(usage.full * 100)}% context
            </span>
          )}
        </div>
      )}
    </div>
  );
};
