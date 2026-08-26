import type { Revision } from "@awp-kit/protocol";
import { CodeView, type CodeViewItem } from "@pierre/diffs/react";
import * as stylex from "@stylexjs/stylex";
import { parsePatchFiles } from "@pierre/diffs";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { listRevisions, readDiff } from "./daemon";
import { statOf, subjectOf, summarise } from "./patch";
import type { ColorScheme } from "@awp-kit/pane";
import { colors, text } from "./tokens.stylex";

// What the workspace on screen has changed.
//
// The accessory column has always been described as the place for "a diff or a
// webview" — see the note in columns.ts about which column yields first when
// the window is too narrow. This is the diff.
//
// ── two questions, not one ─────────────────────────────────────────────────
//
//   revisions   the working copy, and every commit back to the main line
//   diff        the patch for whichever of those is selected
//
// The list is the commit-by-commit view. It is not a separate mode with a
// switch beside it, because a mode is a thing to be in and this is a thing to
// point at: the working copy is one row among the commits, sitting where it
// belongs — at the top, above the commit it is built on.
//
// ── the row at the top is not the same as the commit it names ──────────────
//
// This is the one thing in this file worth reading twice. jj's working copy is
// a real commit with a real change id, so the top row *could* ask for its diff
// by name — and doing so would be wrong. Only a request that names no revision
// snapshots the files on disk first, and without that snapshot a workspace
// where an agent has written six files and run no jj command diffs as empty.
// That is precisely the state this panel exists to show, so the top row asks
// for `undefined` and every other row asks by change id. See `Diff` in the
// contract, and `DiffOf.snapshot` in the daemon's jj service.
//
// ── nothing polls ──────────────────────────────────────────────────────────
//
// A timer here would snapshot someone's working copy every few seconds, which
// writes an operation to the repository they are standing in — for a panel
// that may be sitting behind another tab. Three things ask instead, and each
// of them is a moment when the answer plausibly changed:
//
//   opening the tab      Base UI unmounts a hidden panel, so showing it again
//                        remounts this and the effects below run afresh
//   the window regaining focus   you went to an editor, you came back
//   the refresh button   you did something the window could not see
//
// ── @pierre/diffs, and which of its components ─────────────────────────────
//
// `CodeView` rather than `PatchDiff`, and not as a preference: `PatchDiff`
// throws outright on a patch containing more than one file — `getSingularPatch`
// is exactly what its name says. A commit touches several files roughly always.
// CodeView takes a list of parsed diffs, virtualizes them and owns its own
// scrolling, which is also what a column this narrow needs.

const styles = stylex.create({
  panel: {
    display: "flex",
    flexDirection: "column",
    height: "100%",
    minHeight: 0,
    overflow: "hidden",
  },

  // ── the revisions ────────────────────────────────────────────────────────
  // Capped and scrollable rather than sized to its contents. A stack measured
  // against a trunk nobody has fetched in a month is fifty rows, and fifty
  // rows of commit subjects with the diff pushed off the bottom is a commit
  // list, not a diff panel.
  revisions: {
    flexShrink: 0,
    maxHeight: "35%",
    overflowY: "auto",
    borderBottomWidth: 1,
    borderBottomStyle: "solid",
    borderBottomColor: colors.border,
  },
  revision: {
    display: "flex",
    alignItems: "baseline",
    gap: "0.4rem",
    width: "100%",
    padding: "0.2rem 0.6rem",
    backgroundColor: {
      default: "transparent",
      ":hover": colors.border,
    },
    borderStyle: "none",
    color: colors.muted,
    font: "inherit",
    fontSize: text.tiny,
    textAlign: "start",
    cursor: "pointer",
  },
  on: { backgroundColor: colors.border, color: colors.text },
  // The change id is a fixed-width address and the subject is prose. Giving
  // the room to the subject is the same decision the jobs panel makes about a
  // job's title, and for the same reason: it is the field that cannot be
  // reconstructed from anything else on the row.
  id: { flexShrink: 0, opacity: 0.7 },
  subject: {
    flex: 1,
    minWidth: 0,
    overflow: "hidden",
    textOverflow: "ellipsis",
    whiteSpace: "nowrap",
  },
  mark: { flexShrink: 0, color: colors.live },

  // ── the header over the patch ────────────────────────────────────────────
  head: {
    display: "flex",
    alignItems: "baseline",
    gap: "0.4rem",
    flexShrink: 0,
    padding: "0.3rem 0.6rem",
    color: colors.muted,
    fontSize: text.tiny,
  },
  stat: { flex: 1, minWidth: 0 },
  button: {
    padding: "0.1rem 0.4rem",
    backgroundColor: "transparent",
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.2rem",
    color: colors.muted,
    font: "inherit",
    fontSize: text.tiny,
    cursor: "pointer",
  },

  // CodeView scrolls itself, so this only has to give it a bounded height to
  // scroll inside. `minHeight: 0` is what makes "bounded" true in a flex
  // column — without it the box grows to its content and the whole panel
  // scrolls instead, which throws away the virtualization.
  patch: { flex: 1, minHeight: 0 },
  view: { height: "100%" },

  said: { padding: "0.5rem 0.6rem", color: colors.muted, fontSize: text.small },
  warn: { color: colors.warn },
});

/** The row for the working copy, which is not addressed by change id. */
const WORKING_COPY = "@";

/**
 * Shiki's themes, one per scheme.
 *
 * Named rather than derived from the window's own palette. The colours in
 * `tokens.stylex` are six roles — text, muted, border and three signals —
 * which is the vocabulary a list of rows needs and nothing like the thirty a
 * syntax theme assigns. Building one out of six would be inventing the other
 * twenty-four.
 */
const THEME = { light: "github-light", dark: "github-dark" } as const;

export function Diff({
  dir,
  scheme,
}: {
  /** A directory in the workspace — a session's `startDir`. */
  readonly dir: string | undefined;
  readonly scheme: ColorScheme;
}) {
  const [revisions, setRevisions] = useState<ReadonlyArray<Revision>>([]);
  // The change id of the commit being shown, or absent for the working copy.
  // Absent is the initial state and the one the panel returns to whenever the
  // workspace changes, because the working copy is the row someone opened this
  // to look at.
  const [at, setAt] = useState<string | undefined>();
  const [patch, setPatch] = useState<string | undefined>();
  const [failure, setFailure] = useState<string | undefined>();

  // ── the workspace changed under the panel ────────────────────────────────
  // Derived state, and the reason it is not an effect that calls setAt: the
  // selection belongs to a directory, so it is reset by *rendering a different
  // one*, not by noticing afterwards that one arrived. React's own name for
  // this is adjusting state during render.
  const [shownFor, setShownFor] = useState(dir);
  if (shownFor !== dir) {
    setShownFor(dir);
    setAt(undefined);
    setPatch(undefined);
    setFailure(undefined);
  }

  // Which request the answers on screen belong to.
  //
  // A ref rather than a flag closed over by each effect, because asking is no
  // longer only something an effect does — the refresh button and the focus
  // listener call the same function, and a reply that arrives after a newer
  // request has gone out must lose in every one of those cases alike. One
  // counter, compared on the way in, covers all three.
  const newest = useRef(0);

  const ask = useCallback((where: string, revision: string | undefined) => {
    newest.current += 1;
    const mine = newest.current;

    listRevisions(where)
      .then((found) => {
        if (mine === newest.current) {
          setRevisions(found);
        }
      })
      .catch(() => {
        // Silent, deliberately. The diff below has its own failure and it is
        // the one worth showing; two messages about the same unreachable
        // daemon is one more than says anything.
        if (mine === newest.current) {
          setRevisions([]);
        }
      });

    readDiff(where, revision)
      .then((answer) => {
        // The revision is checked as well as the counter. The reply carries
        // what it was for precisely so a stale one can be told apart from a
        // current one, and the two guards answer different questions: the
        // counter is "is this request still wanted", the revision is "is this
        // the row on screen".
        if (mine === newest.current && answer.revision === (revision ?? WORKING_COPY)) {
          setPatch(answer.patch);
          // Cleared here rather than when the request went out, so a panel
          // showing an error keeps showing it until there is something to put
          // in its place.
          setFailure(undefined);
        }
      })
      .catch((error: unknown) => {
        if (mine === newest.current) {
          setPatch(undefined);
          setFailure(error instanceof Error ? error.message : String(error));
        }
      });
  }, []);

  useEffect(() => {
    if (dir !== undefined) {
      ask(dir, at);
    }
  }, [dir, at, ask]);

  // Coming back to the window is the one moment worth asking on that costs
  // nothing to detect. Everything else that would change this answer happens
  // somewhere else — an agent writing files, a commit in a terminal — and the
  // window finds out about all of it the same way: someone looks at it again.
  useEffect(() => {
    if (dir === undefined) {
      return;
    }
    const again = () => ask(dir, at);
    window.addEventListener("focus", again);
    return () => window.removeEventListener("focus", again);
  }, [dir, at, ask]);

  // Parsed here rather than inside the renderer, because the item list is what
  // CodeView takes and the cache key prefix has to be stable per revision: it
  // is what lets a re-render of the same patch reuse work instead of
  // re-highlighting every file.
  const items = useMemo<ReadonlyArray<CodeViewItem>>(() => {
    if (patch === undefined || patch === "") {
      return [];
    }
    return parsePatchFiles(patch, at ?? WORKING_COPY).flatMap((parsed) =>
      parsed.files.map((fileDiff, index) => ({
        // The path and its position, because CodeView keys its items by id
        // and a patch is allowed to carry the same path twice — a file split
        // across two diff entries by a mode change is the ordinary way that
        // happens. The path alone would silently drop the second.
        id: `${fileDiff.name}-${index}`,
        type: "diff" as const,
        fileDiff,
      })),
    );
  }, [patch, at]);

  const stat = patch === undefined ? undefined : summarise(statOf(patch));

  if (dir === undefined) {
    return <div {...stylex.props(styles.said)}>no workspace open</div>;
  }

  return (
    <div {...stylex.props(styles.panel)}>
      <div {...stylex.props(styles.revisions)}>
        {revisions.map((revision) => {
          // The working copy addresses itself as absent — see the note at the
          // top of this file. Every other row is its change id.
          const value = revision.workingCopy ? undefined : revision.changeId;
          const selected = value === at;
          return (
            <button
              key={revision.changeId}
              type="button"
              {...stylex.props(styles.revision, selected && styles.on)}
              onClick={() => setAt(value)}
            >
              {revision.workingCopy ? (
                <>
                  <span aria-hidden {...stylex.props(styles.mark)}>
                    ●
                  </span>
                  <span {...stylex.props(styles.subject)}>working copy</span>
                </>
              ) : (
                <>
                  <span {...stylex.props(styles.id)}>{revision.changeId.slice(0, 8)}</span>
                  <span {...stylex.props(styles.subject)}>{subjectOf(revision.description)}</span>
                </>
              )}
            </button>
          );
        })}
      </div>

      <div {...stylex.props(styles.head)}>
        <span {...stylex.props(styles.stat)}>{stat ?? ""}</span>
        <button type="button" {...stylex.props(styles.button)} onClick={() => ask(dir, at)}>
          refresh
        </button>
      </div>

      {failure !== undefined && <div {...stylex.props(styles.said, styles.warn)}>{failure}</div>}

      {failure === undefined &&
        patch === undefined && (
          // The first answer has not arrived. Said out loud for the same reason
          // "no changes" is: a blank panel is what every one of these states
          // looks like, and they are not the same state.
          <div {...stylex.props(styles.said)}>reading the diff…</div>
        )}

      {failure === undefined &&
        patch !== undefined &&
        items.length === 0 && (
          // A revision that changed nothing. Said in words rather than left
          // blank, because an empty panel is what a panel that failed to load
          // also looks like — and the top of a jj stack is empty most of the day.
          <div {...stylex.props(styles.said)}>no changes</div>
        )}

      <div {...stylex.props(styles.patch)}>
        {items.length > 0 && (
          <CodeView
            items={items}
            {...stylex.props(styles.view)}
            options={{
              // Unified, because this column is two hundred pixels wide at its
              // floor and split would give each side a hundred of them.
              diffStyle: "unified",
              // Wrapped for the same reason. A horizontal scrollbar per file
              // in a narrow column is a diff nobody reads the right-hand half
              // of.
              overflow: "wrap",
              theme: THEME,
              // The scheme the window resolved, not "system". The appearance
              // toggle is the window's own and the media query knows nothing
              // about it — see theme.ts.
              themeType: scheme,
              stickyHeaders: true,
            }}
          />
        )}
      </div>
    </div>
  );
}
