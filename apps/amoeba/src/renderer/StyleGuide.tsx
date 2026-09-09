import { paletteFor } from "@awp-kit/pane";
import { CheckCircleIcon } from "@phosphor-icons/react/CheckCircle";
import { ChatCircleIcon } from "@phosphor-icons/react/ChatCircle";
import { CircleDashedIcon } from "@phosphor-icons/react/CircleDashed";
import { ClockIcon } from "@phosphor-icons/react/Clock";
import { FileTextIcon } from "@phosphor-icons/react/FileText";
import { XCircleIcon } from "@phosphor-icons/react/XCircle";
import * as stylex from "@stylexjs/stylex";
import { type ReactNode, useEffect, useRef, useState } from "react";
import { AppearanceToggle } from "./Appearance";
import type { ChatConfigOption } from "@awp-kit/protocol";
import { Transcript } from "./Chat";
import { Chip } from "./Chip";
import { type Command, agentCommands } from "./commands";
import { Composer } from "./Composer";
import type { Item } from "./conversation";
import { AA_TEXT, channels, hexOf, ratio, verdict } from "./contrast";
import { Markdown } from "./Markdown";
import { themeFor, useAppearance, useColorScheme } from "./theme";
import { typeset } from "./typeset";
import { colors, space, text } from "./tokens.stylex";

// Colors, typography, components. `#/styleguide`.
//
// ── no subtext ────────────────────────────────────────────────────────────
//
// Stated as a rule and it is the right one: *if a section needs a caption to
// explain it, the title is wrong.* An earlier version of this page carried 678
// words of argument, then 179, and both were words a person comparing hues has
// to read past. There is now no prose on the page at all — a section title, a
// specimen's own name, and the numbers. The reasoning lives here and in
// AGENTS.md.
//
// The three sections are the ones every style guide has, and in that order:
// colour is judged first because everything else is drawn in it.
//
// ── measured, not restated ───────────────────────────────────────────────
//
// Every hex and ratio comes from `getComputedStyle` on the element that was
// painted. A token can be right and the rule applying it wrong, and only
// sampling tells those apart — the lesson this repo learned from a worker pool
// with no workers and a React Compiler that was not running. Literal hexes
// (the terminal's palette, the candidates) are measured against the *ground as
// painted*, so a candidate is judged where it would live rather than on a
// swatch site's white card.
//
// ── it owns no components ────────────────────────────────────────────────
//
// Nothing in the window imports this file. It draws `Chip`, `Markdown` and the
// icon set as they are, plus the recurring local shapes the panels write for
// themselves. A style guide that owned the buttons would be a fourth thing in
// a stack that names three.

/** One colour worth a row: a token, or a literal being considered. */
interface Hue {
  readonly name: string;
  /** As a foreground, when it is a token. Literals use {@link hex}. */
  readonly ink?: stylex.StyleXStyles;
  /** As a fill, when it is a token. */
  readonly fill?: stylex.StyleXStyles;
  /**
   * The literal, for a colour that is not a variable.
   *
   * Written out rather than derived from `ink`: `stylex.create` compiles to
   * class names, so a style object has no `color` to read at runtime. The
   * first draft of this page read one, which typechecks and paints nothing.
   */
  readonly hex?: string;
}

const inks = stylex.create({
  base: { color: colors.base },
  surface: { color: colors.surface },
  raised: { color: colors.raised },
  page: { color: colors.page },
  text: { color: colors.text },
  muted: { color: colors.muted },
  border: { color: colors.border },
  live: { color: colors.live },
  warn: { color: colors.warn },
  accent: { color: colors.accent },
  waiting: { color: colors.waiting },
  ready: { color: colors.ready },
  asked: { color: colors.asked },
});

const fills = stylex.create({
  base: { backgroundColor: colors.base },
  surface: { backgroundColor: colors.surface },
  raised: { backgroundColor: colors.raised },
  page: { backgroundColor: colors.page },
  text: { backgroundColor: colors.text },
  muted: { backgroundColor: colors.muted },
  border: { backgroundColor: colors.border },
  live: { backgroundColor: colors.live },
  warn: { backgroundColor: colors.warn },
  accent: { backgroundColor: colors.accent },
  waiting: { backgroundColor: colors.waiting },
  ready: { backgroundColor: colors.ready },
  asked: { backgroundColor: colors.asked },
});

/** A literal hex, which cannot be a static style. */
const literal = stylex.create({
  ink: (hex: string) => ({ color: hex }),
  fill: (hex: string) => ({ backgroundColor: hex }),
});

const token = (name: keyof typeof inks & keyof typeof fills): Hue => ({
  name,
  ink: inks[name],
  fill: fills[name],
});

const grounds = [token("base"), token("surface"), token("raised"), token("page"), token("border")];
const ink = [token("text"), token("muted"), token("accent")];
const agent = [token("live"), token("waiting"), token("ready")];
const review = [token("asked"), token("warn")];

// ── under consideration ───────────────────────────────────────────────────
//
// https://coolors.co/palette/3d348b-7678ed-f7b801-f18701-f35b04, put here
// rather than argued about in a message: three of the five sit inside a 23°
// arc that `accent` (21°) and `waiting` (40°) already occupy, and the only way
// to see what that costs is to draw them beside the tokens they would displace.
//
// Measured against the painted ground, so the answer changes with the theme —
// which is the whole point: this is a light-ground palette, and its warms are
// fills rather than ink.
const candidates: ReadonlyArray<Hue> = [
  { name: "violet", hex: "#3d348b" },
  { name: "periwinkle", hex: "#7678ed" },
  { name: "amber", hex: "#f7b801" },
  { name: "orange", hex: "#f18701" },
  { name: "vermilion", hex: "#f35b04" },
];

const styles = stylex.create({
  screen: {
    display: "flex",
    flexDirection: "column",
    height: "100%",
    minHeight: 0,
    backgroundColor: colors.base,
    color: colors.text,
  },
  bar: {
    display: "flex",
    alignItems: "center",
    gap: "0.75rem",
    flexShrink: 0,
    height: space.titlebar,
    // Clear of the traffic lights, which float over the start of any
    // `hiddenInset` window.
    paddingInline: `${space.lights} 0.75rem`,
    borderBottomWidth: 1,
    borderBottomStyle: "solid",
    borderBottomColor: colors.border,
  },
  title: { margin: 0, fontSize: text.lead, fontWeight: text.strong },
  spacer: { flex: 1, minWidth: 0 },
  body: { display: "flex", flex: 1, minHeight: 0 },
  // A rail of section links. Not `<a href="#colors">` — the window is on a
  // hash history, so an anchor would replace the route and leave the app at an
  // address it cannot parse. Buttons that scroll, which is also what makes
  // them reachable by keyboard without inventing a link that goes nowhere.
  rail: {
    display: {
      default: "flex",
      // The accessory column is a few hundred pixels wide, and a rail there
      // costs more than it gives.
      "@media (max-width: 760px)": "none",
    },
    flexDirection: "column",
    gap: "0.1rem",
    flexShrink: 0,
    width: "9rem",
    padding: "1rem 0.5rem",
    borderRightWidth: 1,
    borderRightStyle: "solid",
    borderRightColor: colors.border,
  },
  railLink: {
    padding: "0.25rem 0.5rem",
    textAlign: "start",
    backgroundColor: { default: "transparent", ":hover": colors.surface },
    borderStyle: "none",
    borderRadius: "0.25rem",
    color: colors.muted,
    font: "inherit",
    fontSize: text.small,
    cursor: "pointer",
  },
  scroll: { flex: 1, minHeight: 0, overflowY: "auto", overflowX: "hidden" },
  sections: {
    display: "flex",
    flexDirection: "column",
    gap: "2.5rem",
    padding: "1.25rem 1.5rem 4rem",
    maxWidth: "64rem",
  },
  section: { display: "flex", flexDirection: "column", gap: "1rem" },
  h2: {
    margin: 0,
    paddingBottom: "0.35rem",
    borderBottomWidth: 1,
    borderBottomStyle: "solid",
    borderBottomColor: colors.border,
    fontSize: text.title,
    fontWeight: text.strong,
  },
  group: { display: "flex", flexDirection: "column", gap: "0.4rem" },
  h3: { margin: 0, fontSize: text.small, fontWeight: text.strong, color: colors.muted },
  // A row per hue: the fill, the name in the hue, the hex, the ratio. The
  // table shape every style guide uses, because a hue has to be seen as a
  // block *and* as a word and the two are different questions.
  rows: {
    display: "flex",
    flexDirection: "column",
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.35rem",
    overflow: "hidden",
  },
  row: {
    display: "flex",
    alignItems: "center",
    gap: "0.75rem",
    padding: "0.35rem 0.5rem",
    backgroundColor: colors.surface,
    borderTopWidth: 1,
    borderTopStyle: "solid",
    borderTopColor: { default: colors.border, ":first-child": "transparent" },
  },
  block: {
    flexShrink: 0,
    width: "2.5rem",
    height: "1.5rem",
    borderRadius: "0.2rem",
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
  },
  name: { flexShrink: 0, width: "7rem" },
  sample: { flex: 1, minWidth: 0, overflow: "hidden", whiteSpace: "nowrap", fontSize: text.small },
  hex: {
    flexShrink: 0,
    width: "5rem",
    color: colors.muted,
  },
  ratio: {
    flexShrink: 0,
    width: "5.5rem",
    textAlign: "end",
    color: colors.muted,
  },
  bad: { color: colors.warn },
  /** The swatch slot in ground mode: width kept, nothing drawn. */
  bare: { borderColor: "transparent" },
  // The terminal's slots are a scale, so they are drawn as one: flush cells,
  // no gaps, the way every palette page draws a ramp.
  band: {
    display: "flex",
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.35rem",
    overflow: "hidden",
  },
  cell: {
    display: "flex",
    flexDirection: "column",
    justifyContent: "flex-end",
    flex: 1,
    minWidth: 0,
    height: "3.5rem",
    padding: "0.25rem 0.3rem",
  },
  cellName: {
    overflow: "hidden",
    textOverflow: "ellipsis",
    whiteSpace: "nowrap",
  },
  specimens: { display: "flex", flexDirection: "column", gap: "0.45rem" },
  specimen: { display: "flex", alignItems: "baseline", gap: "1rem" },
  gutter: {
    flexShrink: 0,
    width: "5.5rem",
    color: colors.muted,
  },
  t18: { fontSize: text.title, fontWeight: text.strong },
  t16: { fontSize: text.lead, fontWeight: text.medium },
  t15: { fontSize: text.body },
  t14: { fontSize: text.small },
  mono: { fontFamily: text.mono },
  ui: { fontFamily: text.ui },
  muted: { color: colors.muted },
  // A gallery: one bordered cell per specimen, its name in the corner. The
  // component-library shape, and it needs no caption to say what it is.
  gallery: {
    display: "grid",
    gridTemplateColumns: "repeat(auto-fill, minmax(15rem, 1fr))",
    gap: "0.6rem",
  },
  case: {
    display: "flex",
    flexDirection: "column",
    gap: "0.5rem",
    padding: "0.6rem",
    backgroundColor: colors.surface,
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.35rem",
  },
  caseName: { color: colors.muted },
  caseBody: { display: "flex", flexWrap: "wrap", alignItems: "center", gap: "0.4rem" },
  wide: { gridColumn: "1 / -1" },
  button: {
    padding: "0.25rem 0.7rem",
    backgroundColor: colors.accent,
    borderStyle: "none",
    borderRadius: "0.3rem",
    color: colors.base,
    font: "inherit",
    cursor: "pointer",
  },
  shut: { backgroundColor: colors.border, color: colors.muted, cursor: "default" },
  quiet: {
    padding: "0.25rem 0.7rem",
    backgroundColor: { default: "transparent", ":hover": colors.raised },
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.25rem",
    color: colors.muted,
    font: "inherit",
    fontSize: text.small,
    cursor: "pointer",
  },
  field: {
    flex: 1,
    minWidth: "8rem",
    padding: "0.2rem 0.45rem",
    backgroundColor: colors.base,
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: { default: colors.border, ":focus": colors.accent },
    borderRadius: "0.25rem",
    color: colors.text,
    font: "inherit",
    fontSize: text.small,
  },
  strip: { display: "flex", flexDirection: "column", width: "100%" },
  listRow: {
    display: "flex",
    alignItems: "center",
    gap: "0.5rem",
    padding: "0.3rem 0.4rem",
    borderLeftWidth: 2,
    borderLeftStyle: "solid",
    borderLeftColor: "transparent",
  },
  picked: { borderLeftColor: colors.accent, backgroundColor: colors.border },
  dot: { flexShrink: 0, width: "0.5rem", height: "0.5rem", borderRadius: "50%" },
  onLive: { backgroundColor: colors.live },
  onWaiting: { backgroundColor: colors.waiting },
  onReady: { backgroundColor: colors.ready },
  slot: { display: "flex", width: "1rem", justifyContent: "center", flexShrink: 0 },
  said: {
    flex: 1,
    minWidth: 0,
    overflow: "hidden",
    textOverflow: "ellipsis",
    whiteSpace: "nowrap",
    color: colors.muted,
    fontSize: text.small,
  },
  // The transcript's own ground and rhythm, as the panel draws them: `page`
  // rather than `surface`, because a conversation is read rather than glanced
  // at, and the gap is the panel's.
  transcript: {
    display: "flex",
    flexDirection: "column",
    gap: "0.9rem",
    padding: "1rem 1.1rem",
    backgroundColor: colors.page,
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.35rem",
  },
  working: { margin: 0, color: colors.muted, fontSize: text.small },
  document: {
    padding: "0.6rem 0.8rem",
    backgroundColor: colors.page,
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.3rem",
  },
});

/**
 * The ground a section is drawn on, as the browser painted it.
 *
 * A ref and a read rather than the token, because a literal candidate has to
 * be judged against the same ground the tokens are — and because the theme can
 * be forced from the bar above, which no hex in a file knows about.
 */
/**
 * The first real ground above an element, as painted.
 *
 * ── transparent is not a colour, and reading it as one was silent ─────────
 *
 * `getComputedStyle(el).backgroundColor` answers `rgba(0, 0, 0, 0)` for
 * anything with no background of its own, and this page used to measure every
 * ink against the rows *container*, which has none. Read as black, that made
 * the whole page report `FAIL` — `text 2.63` for a token that clears AA — and
 * a page whose entire job is measuring was confidently wrong in both themes.
 *
 * `channels` refuses alpha 0 now, so this walk is what supplies the answer it
 * refuses to invent: up the tree until something is actually painted, and
 * `undefined` if nothing is, which draws as `…` rather than as a number.
 */
const groundAbove = (from: HTMLElement | null): string | undefined => {
  let node: HTMLElement | null = from;
  while (node !== null) {
    const painted = getComputedStyle(node).backgroundColor;
    if (channels(painted) !== undefined) {
      return painted;
    }
    node = node.parentElement;
  }
  return undefined;
};

/** `7.62 AAA`, right-aligned, and red when it does not clear the floor. */
const Ratio = ({ found }: { readonly found: number | undefined }) => (
  <span
    {...stylex.props(
      typeset.address,
      styles.ratio,
      found !== undefined && found < AA_TEXT && styles.bad,
    )}
  >
    {found === undefined ? "…" : verdict(found, AA_TEXT)}
  </span>
);

/**
 * One hue, as a block, as a word, and as two numbers.
 *
 * A block alone says nothing about legibility and a word alone says nothing
 * about the fill, so both — which is why every palette page is a table.
 */
const HueRow = ({ hue, as }: { readonly hue: Hue; readonly as: "ink" | "ground" }) => {
  const word = useRef<HTMLSpanElement>(null);
  const plate = useRef<HTMLDivElement>(null);
  const [measured, setMeasured] = useState<
    | {
        readonly under: string;
        readonly hex: string | undefined;
        readonly found: number | undefined;
      }
    | undefined
  >(undefined);
  const appearance = useAppearance();

  useEffect(() => {
    // ── ink and ground are opposite readouts of one measurement ───────────
    //
    // A hue is either written in or written on:
    //
    //   ink      the word in the hue, on the ground the row is painted
    //   ground   the row filled with the hue, the word in `text`
    //
    // Either way what is measured is *the word against what is behind it* —
    // which is the only pair a person's eye is judging. What differs is which
    // half of it is the hue, and therefore which hex the row reports.
    //
    // The appearance is stored with the reading, and that is not bookkeeping:
    // the effect's input is what the browser painted, which React cannot
    // observe, so recording the theme is what makes the dependency real. It
    // also stops one theme's reading being shown under the other.
    const said = word.current === null ? undefined : getComputedStyle(word.current).color;
    const behind = groundAbove(plate.current);
    if (said === undefined || behind === undefined) {
      return;
    }
    // Which half of the pair is the hue is the only difference between the
    // two modes, and it is what the row reports as its hex.
    const mine = hue.hex ?? (as === "ink" ? said : behind);
    setMeasured({ under: appearance, hex: hexOf(mine), found: ratio(said, behind) });
  }, [appearance, as, hue.hex]);

  const reading = measured?.under === appearance ? measured : undefined;
  const hex = reading?.hex ?? hue.hex;
  const found = reading?.found;

  const asInk = as === "ink";
  return (
    <div
      ref={plate}
      {...stylex.props(
        styles.row,
        // In ground mode the row *is* the swatch, so there is no separate
        // block: a chip of the same colour on top of it is a rectangle nobody
        // can see. The slot keeps its width either way, so the names line up.
        !asInk && hue.fill,
        !asInk && hue.hex !== undefined && literal.fill(hue.hex),
      )}
    >
      <span
        {...stylex.props(
          styles.block,
          asInk && hue.fill,
          asInk && hue.hex !== undefined && literal.fill(hue.hex),
          !asInk && styles.bare,
        )}
        aria-hidden
      />
      <span
        ref={word}
        {...stylex.props(
          typeset.address,
          styles.name,
          asInk && hue.ink,
          asInk && hue.hex !== undefined && literal.ink(hue.hex),
        )}
      >
        {hue.name}
      </span>
      <span
        {...stylex.props(
          styles.sample,
          asInk && hue.ink,
          asInk && hue.hex !== undefined && literal.ink(hue.hex),
        )}
      >
        The quick brown fox jumps over the lazy dog
      </span>
      <span {...stylex.props(typeset.address, styles.hex)}>{hex ?? "…"}</span>
      <Ratio found={found} />
    </div>
  );
};

const Group = ({
  name,
  hues,
  as = "ink",
}: {
  readonly name: string;
  readonly hues: ReadonlyArray<Hue>;
  readonly as?: "ink" | "ground";
}) => (
  <div {...stylex.props(styles.group)}>
    <h3 {...stylex.props(styles.h3)}>{name}</h3>
    <div {...stylex.props(styles.rows)}>
      {hues.map((hue) => (
        <HueRow key={hue.name} hue={hue} as={as} />
      ))}
    </div>
  </div>
);

/**
 * Whichever end of the palette can be read on this cell.
 *
 * Measured, not inferred from lightness: the two candidates are the palette's
 * own `text` and `base`, which is exactly the pair a program drawing in this
 * terminal has to choose between.
 */
const legible = (fill: string, palette: Record<string, string>): string => {
  const light = palette["text"] ?? "#000000";
  const dark = palette["base"] ?? "#ffffff";
  return (ratio(light, fill) ?? 0) >= (ratio(dark, fill) ?? 0) ? light : dark;
};

/** The terminal's slots, as a ramp — because that is what they are. */
const Band = ({
  name,
  palette,
}: {
  readonly name: string;
  readonly palette: Record<string, string>;
}) => (
  <div {...stylex.props(styles.group)}>
    <h3 {...stylex.props(styles.h3)}>{name}</h3>
    <div {...stylex.props(styles.band)}>
      {Object.entries(palette).map(([slot, hex]) => (
        <div key={slot} title={`${slot} ${hex}`} {...stylex.props(styles.cell, literal.fill(hex))}>
          {/* Whichever of the palette's own two ends is legible on this cell,
              chosen by measuring rather than by guessing at lightness. Using
              `text` for every cell put unreadable labels on `white`, `yellow`
              and `brightWhite` — a palette page cannot be the thing that
              demonstrates the failure it is meant to catch. */}
          <span
            {...stylex.props(typeset.address, styles.cellName, literal.ink(legible(hex, palette)))}
          >
            {slot}
          </span>
        </div>
      ))}
    </div>
  </div>
);

const Case = ({
  name,
  wide,
  children,
}: {
  readonly name: string;
  readonly wide?: boolean;
  readonly children: ReactNode;
}) => (
  <div {...stylex.props(styles.case, wide === true && styles.wide)}>
    <span {...stylex.props(typeset.address, styles.caseName)}>{name}</span>
    <div {...stylex.props(styles.caseBody)}>{children}</div>
  </div>
);

const LEADS = [
  { icon: <XCircleIcon size={13} weight="bold" aria-hidden />, said: "ci red" },
  { icon: <ClockIcon size={13} weight="bold" aria-hidden />, said: "ci running" },
  { icon: <ChatCircleIcon size={13} weight="bold" aria-hidden />, said: "changes requested" },
  { icon: <CircleDashedIcon size={13} weight="bold" aria-hidden />, said: "asked again" },
  { icon: <CheckCircleIcon size={13} weight="bold" aria-hidden />, said: "approved" },
  { icon: <FileTextIcon size={13} weight="bold" aria-hidden />, said: "draft" },
  { icon: undefined, said: "open, green, nobody waiting" },
] as const;

const SAMPLE = `**Emphasis**, \`inline code\` and a [link](https://example.invalid).

\`\`\`
  cold   0 task(s)
  warm   46 task(s)
\`\`\`

- a list item
- and another`;

const SECTIONS = ["colors", "typography", "chat", "components"] as const;

const go = (id: string): void => {
  document.getElementById(id)?.scrollIntoView({ block: "start", behavior: "smooth" });
};

// ── a transcript with no agent behind it ──────────────────────────────────
//
// The chat is the newest rendering in the window and nothing on this page
// showed it, so the only way to criticise a message, a tool call or a
// permission request was to have a live agent produce one — which means
// waiting for the state you wanted to look at.
//
// `Row` is the panel's own component, imported rather than reimplemented: a
// second copy of a message bubble is a copy that drifts, and this page would
// then be a picture of a chat rather than the chat.
//
// The pair is a workspace that does not exist, deliberately. Pressing `Allow
// Once` here refuses, which is the honest answer and is better than wiring the
// buttons to nothing.
const FIXTURE: ReadonlyArray<Item> = [
  {
    kind: "said",
    key: "f-1",
    role: "user",
    text: "the diff panel feels chunky when i scroll it. can you find out why",
    queued: false,
  },
  {
    kind: "said",
    key: "f-2",
    role: "thought",
    text: "Worth checking whether the worker pool is actually in the tree before measuring anything.",
    queued: false,
  },
  {
    kind: "said",
    key: "f-3",
    role: "agent",
    text: `Every file was being tokenized **on the main thread** — the same thread the
terminal's render loop is on. \`disableWorkerPool\` defaults to \`false\`, which
reads as "the pool is on", and what it means is:

\`\`\`ts
const poolManager = useContext(WorkerPoolContext);
new CodeView(options, !disableWorkerPool ? poolManager : undefined, true);
\`\`\`

No provider means no context, an absent pool, and a silent fall back to
highlighting where you stand.`,
    queued: false,
  },
  {
    kind: "ran",
    key: "f-4",
    title: "rg -n 'WorkerPoolContextProvider' node_modules/@pierre/diffs",
    toolKind: "execute",
    status: "completed",
    output: "src/context.tsx:14:export const WorkerPoolContextProvider = ({ children }) => {",
    subagent: undefined,
    elapsed: undefined,
    ask: undefined,
    retry: undefined,
  },
  {
    kind: "ran",
    key: "f-5",
    title: "Task",
    toolKind: "execute",
    status: "in_progress",
    output: "",
    subagent: "code-reviewer",
    elapsed: 134,
    ask: undefined,
    retry: { attempt: 2, of: 5, inMs: 30_000 },
  },
  {
    kind: "ran",
    key: "f-6",
    title: "rm -rf node_modules/.cache/highlight",
    toolKind: "execute",
    status: "pending",
    output: "",
    subagent: undefined,
    elapsed: undefined,
    ask: {
      kind: "asked",
      key: "f-6-ask",
      title: "rm -rf node_modules/.cache/highlight",
      options: [
        { id: "reject", name: "Deny", kind: "reject_once" },
        { id: "allow", name: "Allow Once", kind: "allow_once" },
        { id: "always", name: "Always Allow", kind: "allow_always" },
      ],
    },
    retry: undefined,
  },
  {
    kind: "said",
    key: "f-7",
    role: "user",
    text: "no, dont delete the cache — just count the workers",
    queued: true,
  },
];

/**
 * Every other shape a tool call takes, which is where the variety is.
 *
 * `verb` turns `toolKind` into a word — ran · read · edited · searched — and
 * `status` into a mark, and the two are the whole of what a row says at a
 * glance. Drawn as its own list rather than mixed into the transcript above,
 * because a transcript is read in order and this is a set to compare.
 */
const CALLS: ReadonlyArray<Item> = [
  {
    kind: "ran",
    key: "c-read",
    title: "apps/amoeba/src/renderer/highlighting.tsx",
    toolKind: "read",
    status: "completed",
    output: "",
    subagent: undefined,
    elapsed: undefined,
    ask: undefined,
    retry: undefined,
  },
  {
    kind: "ran",
    key: "c-edit",
    title: "apps/amoeba/src/renderer/Fence.tsx",
    toolKind: "edit",
    status: "completed",
    output: "",
    subagent: undefined,
    elapsed: undefined,
    ask: undefined,
    retry: undefined,
  },
  {
    kind: "ran",
    key: "c-deep",
    // A path longer than the column, which is the ordinary case in this
    // repository and the one that used to wrap mid-word across three lines.
    // What must survive the clip is the basename.
    title: "packages/server/src/probe/thread-parent-and-a-long-tail/create-workspace.test.ts",
    toolKind: "read",
    status: "completed",
    output: "",
    subagent: undefined,
    elapsed: undefined,
    ask: undefined,
    retry: undefined,
  },
  {
    kind: "ran",
    key: "c-heredoc",
    // A multi-line command: one line on the row, the count beside it, and the
    // whole thing behind the disclosure.
    title:
      "jj describe --stdin <<'EOF'\nwip: the tool row draws one line\n\nthe rest is behind the disclosure\nEOF",
    toolKind: "execute",
    status: "completed",
    output: "Working copy  (@) now at: svkwrnpm a91def11",
    subagent: undefined,
    elapsed: undefined,
    ask: undefined,
    retry: undefined,
  },
  {
    kind: "ran",
    key: "c-search",
    title: "WorkerPoolContext",
    toolKind: "search",
    status: "completed",
    output: "3 files",
    subagent: undefined,
    elapsed: undefined,
    ask: undefined,
    retry: undefined,
  },
  {
    kind: "ran",
    key: "c-failed",
    title: "bun run typecheck",
    toolKind: "execute",
    status: "failed",
    output:
      "src/renderer/Fence.tsx(163,9): error TS2322: Type '{ file: { name: string; }; }' is not\nassignable to type 'IntrinsicAttributes & CodeViewProps'.",
    subagent: undefined,
    elapsed: 12,
    ask: undefined,
    retry: undefined,
  },
  {
    kind: "ran",
    key: "c-long",
    title: "bun run test",
    toolKind: "execute",
    status: "completed",
    // Long enough to prove the output box scrolls rather than growing the
    // column, which is the window's rule and the thing a two-line fixture
    // cannot show.
    output: Array.from(
      { length: 14 },
      (_unused, line) => `  ✓ packages/server/src/case-${String(line + 1)}.test.ts (7 tests)`,
    ).join("\n"),
    subagent: undefined,
    elapsed: 5,
    ask: undefined,
    retry: undefined,
  },
  {
    kind: "ran",
    key: "c-slow",
    title: "bun install",
    toolKind: "execute",
    status: "in_progress",
    output: "",
    subagent: undefined,
    // Past ten seconds, which is where an elapsed time starts being worth
    // saying — under that it is furniture on every row.
    elapsed: 96,
    ask: undefined,
    retry: undefined,
  },
  {
    kind: "ran",
    key: "c-plain",
    title: "an unnamed tool with no kind",
    toolKind: "",
    status: "completed",
    output: "",
    subagent: undefined,
    elapsed: undefined,
    ask: undefined,
    retry: undefined,
  },
  {
    kind: "asked",
    key: "c-orphan",
    title: "Write apps/amoeba/src/renderer/Composer.tsx",
    options: [
      { id: "reject", name: "Deny", kind: "reject_once" },
      { id: "allow", name: "Allow Once", kind: "allow_once" },
      { id: "always", name: "Always Allow", kind: "allow_always" },
    ],
  },
];

/**
 * What the adapter answers `configOptions` with, near enough.
 *
 * Four selects with one shape, which is why the composer has nothing bespoke
 * per setting — a fifth appearing upstream is a row that shows up rather than
 * a thing to add. The context figure beside them is set past its threshold
 * here on purpose: below 50% the panel deliberately says nothing.
 */
/**
 * A few of the agent's own commands, so the slash menu can be looked at.
 *
 * The menu lists two sets — the window's, which it intercepts, and the
 * agent's, which it sends — and the difference is one small `awp` mark. There
 * is no way to see that without a conversation behind the box, which is the
 * same argument the fake transcript makes.
 */
const THEIRS: ReadonlyArray<Command> = agentCommands([
  { name: "/bro", description: "Restate the last message in plain human language" },
  { name: "/commit", description: "Write a Conventional Commit message", hint: "[scope]" },
  { name: "/review", description: "Review the current change" },
  { name: "/usage", description: "Show session cost, plan usage, and what's contributing" },
]);

const OPTIONS: ReadonlyArray<ChatConfigOption> = [
  {
    id: "mode",
    name: "Mode",
    description: "who approves a tool call",
    currentValue: "default",
    values: [
      { value: "default", name: "Manual" },
      { value: "auto", name: "Auto" },
    ],
  },
  {
    id: "model",
    name: "Model",
    description: undefined,
    currentValue: "opus",
    values: [
      { value: "opus", name: "Opus (1M context)" },
      { value: "sonnet", name: "Sonnet" },
    ],
  },
  {
    id: "effort",
    name: "Effort",
    description: undefined,
    currentValue: "medium",
    values: [
      { value: "medium", name: "Medium" },
      { value: "high", name: "High" },
    ],
  },
  {
    id: "fast",
    name: "Fast",
    description: undefined,
    currentValue: "off",
    values: [
      { value: "off", name: "Off" },
      { value: "on", name: "On" },
    ],
  },
];

export function StyleGuide() {
  const appearance = useAppearance();
  const [draft, setDraft] = useState("");
  const system = useColorScheme();
  const scheme = appearance === "system" ? system : appearance;
  const [chosen, setChosen] = useState<"opus" | "sonnet">("opus");

  return (
    <div {...stylex.props(themeFor(appearance), typeset.prose, styles.screen)}>
      <div {...stylex.props(styles.bar)}>
        <h1 {...stylex.props(styles.title)}>style guide</h1>
        <span {...stylex.props(styles.spacer)} />
        <AppearanceToggle />
      </div>

      <div {...stylex.props(styles.body)}>
        <nav {...stylex.props(styles.rail)} aria-label="sections">
          {SECTIONS.map((id) => (
            <button
              key={id}
              type="button"
              data-nav-item
              {...stylex.props(styles.railLink)}
              onClick={() => go(id)}
            >
              {id}
            </button>
          ))}
        </nav>

        <div {...stylex.props(styles.scroll)}>
          <div {...stylex.props(styles.sections)}>
            <section id="colors" {...stylex.props(styles.section)}>
              <h2 {...stylex.props(styles.h2)}>colors</h2>
              <Group name="grounds" hues={grounds} as="ground" />
              <Group name="ink" hues={ink} />
              <Group name="agent states" hues={agent} />
              <Group name="review states" hues={review} />
              <Band name="terminal" palette={paletteFor(scheme)} />
              <Group name="candidates" hues={candidates} />
            </section>

            <section id="typography" {...stylex.props(styles.section)}>
              <h2 {...stylex.props(styles.h2)}>typography</h2>

              <div {...stylex.props(styles.group)}>
                <h3 {...stylex.props(styles.h3)}>families</h3>
                <div {...stylex.props(styles.specimens)}>
                  <div {...stylex.props(styles.specimen)}>
                    <span {...stylex.props(typeset.address, styles.gutter)}>ui</span>
                    <span {...stylex.props(styles.ui, styles.t15)}>
                      Inter Variable — a title, a label, a sentence somebody reads
                    </span>
                  </div>
                  <div {...stylex.props(styles.specimen)}>
                    <span {...stylex.props(typeset.address, styles.gutter)}>mono</span>
                    <span {...stylex.props(styles.mono, styles.t15)}>
                      JetBrains Mono — andrew/tabular-exports · @ · jj fix
                    </span>
                  </div>
                </div>
              </div>

              <div {...stylex.props(styles.group)}>
                <h3 {...stylex.props(styles.h3)}>scale</h3>
                <div {...stylex.props(styles.specimens)}>
                  <div {...stylex.props(styles.specimen)}>
                    <span {...stylex.props(typeset.address, styles.gutter)}>18 · 600</span>
                    <span {...stylex.props(styles.t18)}>The quick brown fox</span>
                  </div>
                  <div {...stylex.props(styles.specimen)}>
                    <span {...stylex.props(typeset.address, styles.gutter)}>16 · 500</span>
                    <span {...stylex.props(styles.t16)}>The quick brown fox jumps</span>
                  </div>
                  <div {...stylex.props(styles.specimen)}>
                    <span {...stylex.props(typeset.address, styles.gutter)}>15 · 400</span>
                    <span {...stylex.props(styles.t15)}>
                      The quick brown fox jumps over the lazy dog
                    </span>
                  </div>
                  <div {...stylex.props(styles.specimen)}>
                    <span {...stylex.props(typeset.address, styles.gutter)}>14 · 400</span>
                    <span {...stylex.props(styles.t14, styles.muted)}>
                      The quick brown fox jumps over the lazy dog — and no smaller
                    </span>
                  </div>
                </div>
              </div>
            </section>

            <section id="chat" {...stylex.props(styles.section)}>
              <h2 {...stylex.props(styles.h2)}>chat</h2>

              <div {...stylex.props(styles.group)}>
                <h3 {...stylex.props(styles.h3)}>transcript</h3>
                <div {...stylex.props(styles.transcript)}>
                  {/* The panel's own renderer, grouping and all — see
                      `Transcript`. Drawing the rows one at a time here showed
                      a run of tool calls as a run of paragraphs while the
                      panel drew it as one block. */}
                  <Transcript items={FIXTURE} project="thicket" workspace="no-such-workspace" />
                  <p {...stylex.props(styles.working)}>working…</p>
                </div>
              </div>

              <div {...stylex.props(styles.group)}>
                <h3 {...stylex.props(styles.h3)}>tool calls</h3>
                <div {...stylex.props(styles.transcript)}>
                  {/* Ten of them, which is what makes the fold visible: a run
                      draws its last four and offers the rest by count. */}
                  <Transcript items={CALLS} project="thicket" workspace="no-such-workspace" />
                </div>
              </div>

              <div {...stylex.props(styles.group)}>
                <h3 {...stylex.props(styles.h3)}>composer</h3>
                {/* The real one, with its own state. Typing `/` opens the
                    command menu, and running a command here says so in the box
                    rather than reaching a daemon: a fixture must not be able to
                    start a conversation. */}
                <div {...stylex.props(styles.transcript)}>
                  <Composer
                    draft={draft}
                    onDraft={setDraft}
                    onSend={() => setDraft("")}
                    onCommand={(command) => setDraft(`${command.name} would run here`)}
                    theirs={THEIRS}
                    config={OPTIONS}
                    onSetOption={(option, value) => setDraft(`${option} → ${value}`)}
                    usage={{ full: 0.62, used: 124_000, size: 200_000 }}
                  />
                </div>
              </div>
            </section>

            <section id="components" {...stylex.props(styles.section)}>
              <h2 {...stylex.props(styles.h2)}>components</h2>
              <div {...stylex.props(styles.gallery)}>
                <Case name="button">
                  <button type="button" {...stylex.props(typeset.control, styles.button)}>
                    send
                  </button>
                  <button
                    type="button"
                    disabled
                    {...stylex.props(typeset.control, styles.button, styles.shut)}
                  >
                    send
                  </button>
                  <button type="button" {...stylex.props(styles.quiet)}>
                    dismiss
                  </button>
                </Case>

                <Case name="chip">
                  <Chip
                    id="styleguide-model"
                    label={chosen === "opus" ? "Opus" : "Sonnet"}
                    title="Base UI: arrow keys, typeahead, portal"
                    value={chosen}
                    onChange={setChosen}
                    options={[
                      { value: "opus", label: "Opus" },
                      { value: "sonnet", label: "Sonnet" },
                    ]}
                  />
                  <Chip
                    id="styleguide-effort"
                    label="effort: medium"
                    title="quiet"
                    value="medium"
                    onChange={() => undefined}
                    options={[{ value: "medium", label: "Medium" }]}
                    quiet
                  />
                </Case>

                <Case name="field">
                  <input
                    aria-label="address"
                    placeholder="address"
                    {...stylex.props(styles.field)}
                  />
                </Case>

                <Case name="status dot">
                  <span {...stylex.props(styles.dot, styles.onLive)} aria-hidden />
                  <span {...stylex.props(styles.t14)}>live</span>
                  <span {...stylex.props(styles.dot, styles.onWaiting)} aria-hidden />
                  <span {...stylex.props(styles.t14)}>waiting</span>
                  <span {...stylex.props(styles.dot, styles.onReady)} aria-hidden />
                  <span {...stylex.props(styles.t14)}>ready</span>
                </Case>

                <Case name="sidebar row" wide>
                  <div {...stylex.props(styles.strip)}>
                    <div {...stylex.props(styles.listRow, styles.picked)}>
                      <span {...stylex.props(styles.dot, styles.onLive)} aria-hidden />
                      <span {...stylex.props(typeset.address)}>tabular-exports</span>
                      <span {...stylex.props(styles.said)}>selected</span>
                    </div>
                    <div {...stylex.props(styles.listRow)}>
                      <span {...stylex.props(styles.dot, styles.onWaiting)} aria-hidden />
                      <span {...stylex.props(typeset.address)}>lantern</span>
                      <span {...stylex.props(styles.said)}>#2418</span>
                    </div>
                    <div {...stylex.props(styles.listRow)}>
                      <span {...stylex.props(styles.dot, styles.onReady)} aria-hidden />
                      <span {...stylex.props(typeset.address)}>typed-router</span>
                      <span {...stylex.props(styles.said)} />
                    </div>
                  </div>
                </Case>

                <Case name="leading icon" wide>
                  <div {...stylex.props(styles.strip)}>
                    {LEADS.map((one) => (
                      <div key={one.said} {...stylex.props(styles.listRow)}>
                        <span {...stylex.props(styles.slot)}>{one.icon}</span>
                        <span {...stylex.props(styles.said)}>{one.said}</span>
                      </div>
                    ))}
                  </div>
                </Case>

                <Case name="markdown" wide>
                  <div {...stylex.props(styles.document)}>
                    <Markdown>{SAMPLE}</Markdown>
                  </div>
                </Case>
              </div>
            </section>
          </div>
        </div>
      </div>
    </div>
  );
}
