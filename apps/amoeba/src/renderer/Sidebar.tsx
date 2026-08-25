import type { SessionInfo } from "@awp-kit/protocol";
import * as stylex from "@stylexjs/stylex";
import { AppearanceToggle } from "./Appearance";
import { colors, space, text } from "./tokens.stylex";
import { PRIMARY, type Workspace, groupByWorkspace, openable } from "./workspaces";

// The list of workspaces, and which of them can be opened.
//
// Workspaces, not sessions — see workspaces.ts for why that distinction is not
// pedantry. A row that cannot be attached to says why, and the sentence comes
// from the daemon rather than from here: only the daemon can know that one of
// these is the session awp itself is running in, and re-deriving the rest would
// be a second copy of a rule. The copy that drifts is always the one nobody
// tests.
//
// The column is a header-less list with a footer: the list scrolls, the footer
// does not. Anything about the window rather than about the work lives down
// there, out of the way of the thing being scrolled through.

const styles = stylex.create({
  column: { display: "flex", flexDirection: "column", height: "100%" },
  list: { flex: 1, minHeight: 0, overflowY: "auto", paddingTop: space.titlebar },
  empty: { padding: `0.5rem ${space.gutter}`, color: colors.muted },
  failure: {
    padding: `${space.titlebar} ${space.gutter} ${space.gutter}`,
    color: colors.muted,
    lineHeight: 1.6,
  },
  head: { marginBottom: "0.75rem" },
  quiet: { fontSize: text.small, opacity: 0.8 },
  gap: { marginTop: "0.75rem" },

  // A row is a strip whether or not the whole of it is one control, so the
  // padding and the selected fill live here and never on the button.
  row: {
    display: "flex",
    alignItems: "baseline",
    gap: "0.5rem",
    width: "100%",
    padding: `${space.row} ${space.gutter}`,
    borderStyle: "none",
    backgroundColor: "transparent",
    color: colors.text,
    font: "inherit",
    textAlign: "left",
    cursor: "pointer",
  },
  rowOn: { backgroundColor: colors.border },
  rowShut: { color: colors.muted, cursor: "default", opacity: 0.55 },
  rowPlain: { cursor: "default" },
  label: { flex: 1, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" },
  dot: { fontSize: 9, color: colors.live },
  dotOff: { color: colors.muted },

  kinds: { display: "flex", gap: "0.25rem", flexShrink: 0 },
  // The kind is a label on a one-session row and a control on a row with
  // several, and it is the same size and colour either way — a thing that
  // becomes clickable by growing a neighbour would be a strange thing to learn.
  kind: {
    padding: "0 0.25rem",
    borderStyle: "none",
    borderRadius: "0.2rem",
    backgroundColor: "transparent",
    color: colors.muted,
    font: "inherit",
    fontSize: text.tiny,
    cursor: "inherit",
  },
  kindOn: { backgroundColor: colors.base, color: colors.text },
  kindPick: { cursor: "pointer" },
  reason: {
    fontSize: text.tiny,
    color: colors.muted,
    padding: `0 ${space.gutter} ${space.row} 2rem`,
    lineHeight: 1.4,
  },
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

const Dot = ({ live }: { readonly live: boolean }) => (
  <span aria-hidden {...stylex.props(styles.dot, !live && styles.dotOff)}>
    ●
  </span>
);

/**
 * One workspace.
 *
 * The whole strip is the control when there is only one session in it, because
 * then there is nothing to disambiguate and a full-width target is simply
 * better. With several, the strip is inert and each kind is its own button —
 * the alternative is a button inside a button, which is not markup.
 */
function Row({
  workspace,
  selected,
  onSelect,
}: {
  readonly workspace: Workspace;
  readonly selected: string | undefined;
  readonly onSelect: (session: SessionInfo) => void;
}) {
  const single = workspace.sessions.length === 1 ? workspace.sessions[0] : undefined;
  const active = workspace.sessions.some((session) => session.name === selected);
  const live = workspace.sessions.some((session) => !session.ended);
  const shut = openable(workspace) === undefined;

  // Shown when it is worth showing, which is not the same as whenever it
  // exists. Eighteen of twenty-one rows are one agent, and eighteen rows each
  // ending in the word "agent" is the same word repeated down a column while
  // the names it is crowding out are the part being read. So a lone session
  // says nothing unless it is *not* the agent — `captain`, an editor on its
  // own — and a workspace with several always names them, because there the
  // kind is the thing being chosen between.
  const listed =
    single === undefined
      ? workspace.sessions
      : workspace.sessions.filter((session) => session.identity?.kind !== PRIMARY);

  const kinds = (
    <span {...stylex.props(styles.kinds)}>
      {listed.map((session) => {
        const kind = session.identity?.kind ?? "";
        if (kind === "") {
          return null;
        }
        const chip = stylex.props(
          styles.kind,
          session.name === selected && styles.kindOn,
          single === undefined && session.refusal === undefined && styles.kindPick,
        );
        return single === undefined ? (
          <button
            key={session.name}
            type="button"
            disabled={session.refusal !== undefined}
            title={session.refusal ?? session.cmd}
            onClick={() => onSelect(session)}
            {...chip}
          >
            {kind}
          </button>
        ) : (
          <span key={session.name} {...chip}>
            {kind}
          </span>
        );
      })}
    </span>
  );

  // The reason belongs to the workspace when it has one session and to nothing
  // in particular when it has several — there it is on each kind's tooltip
  // instead, because two sessions can be unattachable for different reasons.
  const refusal = single?.refusal;

  return (
    <div>
      {single === undefined ? (
        <div {...stylex.props(styles.row, styles.rowPlain, active && styles.rowOn)}>
          <Dot live={live} />
          <span title={workspace.label} {...stylex.props(styles.label)}>
            {workspace.label}
          </span>
          {kinds}
        </div>
      ) : (
        <button
          type="button"
          disabled={shut}
          // The reason is the tooltip as well as the subtitle. A row that will
          // not say why it is disabled is worse than no row at all.
          title={refusal ?? single.cmd}
          onClick={() => onSelect(single)}
          {...stylex.props(styles.row, active && styles.rowOn, shut && styles.rowShut)}
        >
          <Dot live={live} />
          <span title={workspace.label} {...stylex.props(styles.label)}>
            {workspace.label}
          </span>
          {kinds}
        </button>
      )}
      {refusal !== undefined && <div {...stylex.props(styles.reason)}>{refusal}</div>}
    </div>
  );
}

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
  const workspaces = groupByWorkspace(sessions);

  // The two states of the column, chosen before the markup rather than inside
  // it. A daemon that is not running is the ordinary case during development,
  // so it gets a sentence and the command, not an empty list.
  const body =
    failure === undefined ? (
      <>
        {workspaces.length === 0 && <div {...stylex.props(styles.empty)}>no workspaces</div>}
        {workspaces.map((workspace) => (
          <Row key={workspace.key} workspace={workspace} selected={selected} onSelect={onSelect} />
        ))}
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
