import type { SessionInfo, Thread } from "@awp-kit/protocol";
import { PlusIcon } from "@phosphor-icons/react/Plus";
import { TrayIcon } from "@phosphor-icons/react/Tray";
import * as stylex from "@stylexjs/stylex";
import type { ComponentType } from "react";
import { Sidebar } from "./Sidebar";
import { typeset } from "./typeset";
import { colors, space, timing } from "./tokens.stylex";
import type { Facts } from "./useFacts";

// The left column: a short menu, a rule, and the threads.
//
//   ⊕  new thread                    ⌘N
//   ⊟  inbox
//   ─────────────────────────────────────
//   ▸ tabular exports
//       rowan · agent
//
// ── this used to be two tabs, and the tabs were the wrong shape ───────────
//
// `work` and `inbox` sat beside each other as a pair of peers, which said the
// column held two lists of the same kind. It does not: the threads *are* this
// column — they fill it, they are what is selected, they are what the address
// points at — and the inbox is a list of work happening elsewhere that somebody
// opens on purpose. A tab strip made the second one cost the first its whole
// column, and made the first one look like a mode.
//
// So the two acts are a menu and the threads are the column. The inbox opens
// over the window instead — see `InboxDialog` for why that, rather than a panel
// in the accessory strip.
//
// ── the dialog is App's, and the menu only asks for it ────────────────────
//
// It was held here, and a folded sidebar is what said that was wrong: this
// whole column is `inert` and zero pixels wide while it is closed, so an
// overlay owned by it is an overlay whose owner is not on screen — and the one
// way to reach it went with the column. `NewThread` has always been App's for
// the same reason, which is the shape to copy: a modal belongs to the window,
// and the control that opens it belongs to whichever column has room for it.
//
// That is also where `⌘I` lives, beside `⌘N` — see App.tsx. A menu item is the
// discoverable half and a chord is the half that still works with this column
// folded away.
//
// **The `+ thread` button at the foot of the strip went with it.** It was the
// only way to make a workspace from this window and it is now the first line of
// the menu, which is where somebody looks for it — a second copy at the other
// end of the same column is two controls for one act.
//
// ── nothing counts the inbox ──────────────────────────────────────────────
//
// A badge on `inbox` reading "3 to review" is the obvious next thing and is
// deliberately absent: the count is a `gh` call per project, seconds each, and
// putting it on a row that is always on screen means paying for it whether or
// not anybody asked. The rows are fetched when the dialog mounts, which is the
// promise `useInbox` was written around.

const styles = stylex.create({
  column: { display: "flex", flexDirection: "column", height: "100%", minHeight: 0 },

  // ── this strip is NOT part of the window's header band ──────────────────
  //
  // The other two columns each begin with one, all `space.titlebar` tall so
  // they line up: the corner strip over the sidebar, the agent's header, the
  // panels' tab strip. The sidebar's share of that band is the corner strip —
  // which holds the traffic lights and both fold controls, belongs to the
  // window rather than to this column, and never folds.
  //
  // So the menu sits *under* it and its rows are deliberately shorter than a
  // header: two full-height bands stacked in a 260px column would make the
  // strip that is chrome for the window and the menu that acts on it read as
  // the same thing.
  menu: {
    display: "flex",
    flexDirection: "column",
    flexShrink: 0,
    padding: `0.35rem 0.4rem`,
  },
  // The rule between the acts and the list. Not decoration: without it the
  // first thread heading is one more row in the menu, and the menu's items are
  // the two things in this column that do not open a workspace.
  rule: {
    flexShrink: 0,
    height: 1,
    marginInline: space.gutter,
    backgroundColor: colors.border,
  },

  item: {
    display: "flex",
    alignItems: "center",
    gap: "0.5rem",
    width: "100%",
    padding: "0.3rem 0.45rem",
    backgroundColor: "transparent",
    borderStyle: "none",
    borderRadius: "0.25rem",
    color: colors.muted,
    font: "inherit",
    textAlign: "start",
    cursor: "pointer",
    transitionProperty: "background-color, color",
    transitionDuration: { default: timing.quick, "@media (prefers-reduced-motion: reduce)": "0s" },
    ":hover": { color: colors.text, backgroundColor: colors.surface },
    // A menu row is a control, not prose: without this a double-click selects
    // the word instead of pressing twice. Both spellings, because WebKit
    // reports the unprefixed one as empty while honouring the prefixed one.
    userSelect: "none",
    WebkitUserSelect: "none",
  },
  // The icon keeps its box whether or not it is the same width as the other,
  // so the words start on one edge — the same argument the tool rows' verb
  // column makes.
  icon: { flexShrink: 0, display: "flex", width: "1rem", justifyContent: "center" },
  word: { flex: 1, minWidth: 0 },
  // The chord, said once where the act is rather than only in a tooltip. Muted
  // and small: it is a reminder for the second time somebody uses this, not a
  // field anybody scans.
  chord: { flexShrink: 0, color: colors.muted, opacity: 0.7 },
});

/** One row of the menu: an icon, a word, and — when there is one — its chord. */
function Item({
  icon: Icon,
  word,
  chord,
  onPress,
}: {
  readonly icon: ComponentType<{ readonly size?: number; readonly "aria-hidden"?: boolean }>;
  readonly word: string;
  readonly chord?: string;
  readonly onPress: () => void;
}) {
  return (
    <button
      type="button"
      // ctrl+j/ctrl+k step through these, which is the keyboard mandate: every
      // control in this window is reachable without a pointer, and a list that
      // marks nothing has nothing to step.
      data-nav-item
      title={chord === undefined ? word : `${word} (${chord})`}
      onClick={onPress}
      {...stylex.props(typeset.control, styles.item)}
    >
      <span {...stylex.props(styles.icon)}>
        <Icon size={15} aria-hidden />
      </span>
      <span {...stylex.props(styles.word)}>{word}</span>
      {chord !== undefined && <span {...stylex.props(typeset.label, styles.chord)}>{chord}</span>}
    </button>
  );
}

export function LeftColumn({
  sessions,
  facts,
  threads,
  selected,
  at,
  onSelect,
  onNew,
  onInbox,
  onThreadsChanged,
  onOpenWorkspace,
  failure,
}: {
  readonly sessions: ReadonlyArray<SessionInfo>;
  readonly facts: Facts;
  readonly threads: ReadonlyArray<Thread>;
  readonly selected: string | undefined;
  /** The workspace the window is looking at, session or no session. */
  readonly at: { readonly project: string; readonly workspace: string } | undefined;
  readonly onSelect: (session: SessionInfo) => void;
  readonly onNew: () => void;
  /** Open the inbox. The dialog itself is App's — see the note above. */
  readonly onInbox: () => void;
  readonly onThreadsChanged: () => void;
  /** Go to a workspace's agent, named rather than handed as a session: an
   * inbox row knows the pair and not which session is running. */
  readonly onOpenWorkspace: (project: string, workspace: string) => void;
  readonly failure: string | undefined;
}) {
  return (
    <div {...stylex.props(styles.column)}>
      <nav aria-label="actions" {...stylex.props(styles.menu)}>
        <Item icon={PlusIcon} word="new thread" chord="⌘N" onPress={onNew} />
        <Item icon={TrayIcon} word="inbox" chord="⌘I" onPress={onInbox} />
      </nav>

      <div {...stylex.props(styles.rule)} />

      <Sidebar
        sessions={sessions}
        facts={facts}
        threads={threads}
        selected={selected}
        at={at}
        onSelect={onSelect}
        // The same callback the inbox opens a row with: both of them name a
        // pair rather than a session, because neither knows — or needs to know
        // — which of a workspace's sessions happens to be running.
        onOpen={onOpenWorkspace}
        onThreadsChanged={onThreadsChanged}
        failure={failure}
      />
    </div>
  );
}
