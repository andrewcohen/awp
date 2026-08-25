import type { SessionInfo } from "@awp-kit/protocol";
import * as stylex from "@stylexjs/stylex";
import { AppearanceToggle } from "./Appearance";
import { colors, space, text } from "./tokens.stylex";
import { PRIMARY, type Workspace, groupByWorkspace, openable } from "./workspaces";

// The list of workspaces, and which of them can be opened.
//
// Two lines per row, and the rules below are the Go deck's — see
// `archive/internal/deckui/sidebar.go`, which is around sixty percent prose
// about exactly this strip. They are worth taking rather than rediscovering,
// and each one is here because something was tried and read badly.
//
//   ● pr-2340-lantern-sentry-header-allowlist
//     thicket · agent
//
//   ● effect-ts-tiered-discount-timemachine
//     thicket · agent editor action_dev
//
// **Two lines, always.** A row has two unrelated facts to carry — which
// workspace, and what is in it — and on one line they compete: the kinds are
// short and go last, so the name is what truncates, and a truncated name is the
// one field you cannot work out from the others. Given a line to itself the
// name gets the whole column. The cadence has to be fixed to be a cadence, so
// the second line always says something; a name with nothing under it reads as
// a one-line row and the rhythm is gone.
//
// **Colour marks structure, not content.** One dot per row carries a hue and
// nothing else on the row does. The second line is the line there is one of per
// row, so a colour on it is a colour repeated down the whole column — and
// emphasis spent everywhere is emphasis nowhere.
//
// **A workspace called `default` is the repository's**, and the word says
// nothing: six projects with one workspace each would render as six rows
// reading `default`. So the project is the name and `default` goes below it,
// which is the same trade in both directions — line two is whichever half of
// project/workspace line one did not use.
//
// One rule of the Go strip is deliberately **not** taken: it drops a
// `pr-1234-` prefix from a name because the number is on the line below. Here
// there is no PR number on any line, so dropping it would lose the only place
// that information appears.
//
// Air between rows is a margin rather than a blank row. The Go strip paid a
// third of its height for that separation because a terminal has no smaller
// unit than a line; this one does not have to.

const styles = stylex.create({
  column: { display: "flex", flexDirection: "column", height: "100%" },
  list: {
    flex: 1,
    minHeight: 0,
    overflowY: "auto",
    padding: `${space.titlebar} 0 ${space.gutter}`,
  },
  empty: { padding: `0.5rem ${space.gutter}`, color: colors.muted },
  failure: {
    padding: `${space.titlebar} ${space.gutter} ${space.gutter}`,
    color: colors.muted,
    lineHeight: 1.6,
  },
  head: { marginBottom: "0.75rem" },
  quiet: { fontSize: text.small, opacity: 0.8 },
  gap: { marginTop: "0.75rem" },

  // The band is the row, both lines of it, edge to edge — the gutter is inside
  // the row rather than around it, so a selected workspace is a strip and not a
  // floating rectangle.
  row: {
    padding: `${space.row} ${space.gutter}`,
    marginBottom: "0.3rem",
  },
  rowOn: { backgroundColor: colors.border },

  // Line one is a button and line two is not, which is why the padding lives on
  // the row: a button carrying it would put the band on one line of two.
  title: {
    display: "flex",
    alignItems: "baseline",
    gap: "0.5rem",
    width: "100%",
    padding: 0,
    borderStyle: "none",
    backgroundColor: "transparent",
    color: colors.text,
    font: "inherit",
    textAlign: "left",
    cursor: "pointer",
  },
  titleShut: { color: colors.muted, cursor: "default", opacity: 0.55 },
  label: { flex: 1, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" },

  // A fixed width, so the second line starts under the first letter of the name
  // and not under the dot. One level of structure on this strip, one left edge
  // for everything that is not the dot.
  dot: { width: "0.75rem", flexShrink: 0, fontSize: 9, color: colors.live },
  dotOff: { color: colors.muted },

  meta: {
    display: "flex",
    alignItems: "baseline",
    gap: "0.35rem",
    paddingInlineStart: "1.25rem",
    fontSize: text.tiny,
    color: colors.muted,
    lineHeight: 1.5,
    overflow: "hidden",
  },
  ident: { flexShrink: 0 },
  // The separator, not a word. Present so the two halves of the line do not run
  // together, muted so it is not one of them.
  sep: { flexShrink: 0, opacity: 0.5 },
  kinds: { display: "flex", gap: "0.3rem", overflow: "hidden" },
  kind: {
    padding: 0,
    borderStyle: "none",
    backgroundColor: "transparent",
    color: colors.muted,
    font: "inherit",
    fontSize: text.tiny,
    cursor: "inherit",
    whiteSpace: "nowrap",
  },
  kindOn: { color: colors.text },
  kindPick: { cursor: "pointer" },
  reason: { overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" },

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
 * One workspace: its name, and whatever line one did not already say.
 *
 * Line one is always a button and opens the workspace's primary session, so
 * every row has a full-width target. Where a workspace has more than one
 * session the kinds on line two are buttons too — that is the only way to
 * reach the editor without reaching the agent first — and putting them on the
 * second line is what keeps them out of the name's way.
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
  const active = workspace.sessions.some((session) => session.name === selected);
  const live = workspace.sessions.some((session) => !session.ended);
  const primary = openable(workspace);
  const several = workspace.sessions.length > 1;

  // Whichever half of project/workspace the name did not use. A `default`
  // workspace is the repository's, so the project is the name and `default`
  // goes below; anything else names itself and the project goes below.
  const other = workspace.foreign ? "elsewhere" : (workspace.otherIdent ?? "");

  // Shown when worth showing, which is not whenever it exists. Eighteen of
  // twenty-one rows are one agent, and eighteen lines each ending in the word
  // "agent" is one word repeated down a column while the names it crowds out
  // are the part being read. A lone session names itself only when it is *not*
  // the agent — a captain, an editor on its own.
  const listed = several
    ? workspace.sessions
    : workspace.sessions.filter((session) => session.identity?.kind !== PRIMARY);

  // The reason takes the whole of line two when there is one. It is the most
  // important thing the row has to say, and giving it a third line would break
  // the cadence the two lines exist to keep.
  const refusal = several ? undefined : workspace.sessions[0]?.refusal;

  return (
    <div {...stylex.props(styles.row, active && styles.rowOn)}>
      <button
        type="button"
        disabled={primary === undefined}
        // The reason is the tooltip as well as line two. A row that will not
        // say why it is disabled is worse than no row at all.
        title={refusal ?? workspace.label}
        onClick={() => primary !== undefined && onSelect(primary)}
        {...stylex.props(styles.title, primary === undefined && styles.titleShut)}
      >
        <Dot live={live} />
        <span {...stylex.props(styles.label)}>{workspace.name}</span>
      </button>

      <div {...stylex.props(styles.meta)}>
        {refusal === undefined ? (
          <>
            {other !== "" && <span {...stylex.props(styles.ident)}>{other}</span>}
            {other !== "" && listed.length > 0 && (
              <span aria-hidden {...stylex.props(styles.sep)}>
                ·
              </span>
            )}
            <span {...stylex.props(styles.kinds)}>
              {listed.map((session) => {
                const kind = session.identity?.kind ?? "";
                if (kind === "") {
                  return null;
                }
                const chip = stylex.props(
                  styles.kind,
                  session.name === selected && styles.kindOn,
                  several && session.refusal === undefined && styles.kindPick,
                );
                return several ? (
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
          </>
        ) : (
          <span {...stylex.props(styles.reason)}>{refusal}</span>
        )}
      </div>
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
