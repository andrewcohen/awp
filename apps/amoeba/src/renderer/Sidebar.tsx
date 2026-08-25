import type { SessionInfo } from "@awp-kit/protocol";
import * as stylex from "@stylexjs/stylex";
import { AppearanceToggle } from "./Appearance";
import { colors, space, text } from "./tokens.stylex";

// The list of sessions, and which of them can be opened.
//
// A row that cannot be attached to says why, and the sentence comes from the
// daemon rather than from here. Only the daemon can know that one of these is
// the session awp itself is running in, and re-deriving the rest would be a
// second copy of a rule — the copy that drifts is always the one nobody tests.
//
// The column is a header-less list with a footer: the list scrolls, the footer
// does not. Anything that is about the window rather than about the work lives
// down there, out of the way of the thing being scrolled through.

const shortName = (name: string): string => {
  // `awp.project.workspace.kind` is an address, not a label, and the project
  // repeats on every row. What distinguishes them is the middle.
  const parts = name.split(".");
  return parts.length >= 4 ? parts.slice(1, -1).join(".") : name;
};

const styles = stylex.create({
  column: { display: "flex", flexDirection: "column", height: "100%" },
  list: { flex: 1, minHeight: 0, overflowY: "auto", paddingTop: space.titlebar },
  empty: { padding: `0.5rem ${space.gutter}`, color: colors.muted },
  failure: {
    padding: `${space.titlebar} ${space.gutter} ${space.gutter}`,
    color: colors.muted,
    lineHeight: 1.6,
  },
  quiet: { fontSize: text.small, opacity: 0.8 },
  head: { marginBottom: "0.75rem" },
  gap: { marginTop: "0.75rem" },
  row: {
    display: "block",
    width: "100%",
    textAlign: "left",
    padding: `${space.row} ${space.gutter}`,
    borderStyle: "none",
    backgroundColor: "transparent",
    color: colors.text,
    font: "inherit",
    cursor: "pointer",
  },
  rowOn: { backgroundColor: colors.border },
  rowShut: { color: colors.muted, cursor: "default", opacity: 0.55 },
  name: { display: "flex", gap: "0.5rem", alignItems: "baseline" },
  label: { overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" },
  dot: { fontSize: 9, color: colors.live },
  dotOff: { color: colors.muted },
  reason: {
    fontSize: text.tiny,
    color: colors.muted,
    paddingLeft: space.gutter,
    whiteSpace: "normal",
    lineHeight: 1.4,
  },
  // A rule above the footer and nothing else: the row of controls needs to read
  // as belonging to the window rather than as the last session in the list.
  footer: {
    display: "flex",
    alignItems: "center",
    gap: "0.25rem",
    padding: "0.4rem 0.5rem",
    borderTopWidth: 1,
    borderTopStyle: "solid",
    borderTopColor: colors.border,
  },
});

export function Sidebar({
  sessions,
  selected,
  onSelect,
  failure,
}: {
  readonly sessions: ReadonlyArray<SessionInfo>;
  readonly selected: string | undefined;
  readonly onSelect: (session: SessionInfo) => void;
  readonly failure: string | undefined;
}) {
  // The two states of the column, chosen before the markup rather than inside
  // it. A daemon that is not running is the ordinary case during development,
  // so it gets a sentence and the command, not an empty list.
  const body =
    failure === undefined ? (
      <>
        {sessions.length === 0 && <div {...stylex.props(styles.empty)}>no sessions</div>}
        {sessions.map((session) => {
          const open = session.refusal === undefined;
          const active = session.name === selected;
          return (
            <button
              key={session.name}
              type="button"
              disabled={!open}
              // The reason is the tooltip as well as the subtitle. A row that
              // will not say why it is disabled is worse than no row at all.
              title={session.refusal ?? session.cmd}
              onClick={() => onSelect(session)}
              {...stylex.props(styles.row, active && styles.rowOn, !open && styles.rowShut)}
            >
              <div {...stylex.props(styles.name)}>
                <span aria-hidden {...stylex.props(styles.dot, session.ended && styles.dotOff)}>
                  ●
                </span>
                <span {...stylex.props(styles.label)}>{shortName(session.name)}</span>
              </div>
              {session.refusal !== undefined && (
                <div {...stylex.props(styles.reason)}>{session.refusal}</div>
              )}
            </button>
          );
        })}
      </>
    ) : (
      <div {...stylex.props(styles.failure)}>
        <div {...stylex.props(styles.head)}>no daemon</div>
        <div {...stylex.props(styles.quiet)}>{failure}</div>
        <div {...stylex.props(styles.quiet, styles.gap)}>
          start it with <code>bun run daemon</code>
        </div>
      </div>
    );

  return (
    <div {...stylex.props(styles.column)}>
      <div {...stylex.props(styles.list)}>{body}</div>

      <div {...stylex.props(styles.footer)}>
        <AppearanceToggle />
      </div>
    </div>
  );
}
