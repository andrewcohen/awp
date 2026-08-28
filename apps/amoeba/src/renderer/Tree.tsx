import { FileTree, useFileTree, useFileTreeSelection } from "@pierre/trees/react";
import * as stylex from "@stylexjs/stylex";
import { useEffect, useRef } from "react";
import { FOLD_MS } from "./columns";
import { colors, space, text } from "./tokens.stylex";

// The patch's files as a tree, over the patch.
//
// Fifteen files in one scrolling column means finding a file by scrolling past
// the others. This is the index.
//
// ── why a dependency, and what it actually is ──────────────────────────────
//
// `@pierre/trees` rather than thirty lines of our own, and the deciding factor
// was not the rows — it was everything under them. Shared-prefix flattening,
// so `packages/server/src/…` is one row deep rather than three; a virtualiser;
// arrow-key navigation with the roving focus; search. Every one of those is a
// thing the hand-rolled version gets wrong first and fixes later.
//
// What it *is*, checked before adopting it rather than after:
//
//   ./react   a thin React wrapper — real React, real `react/jsx-runtime` —
//             that mounts a custom element and hands it a model
//   inside    Preact renders the rows, in the element's shadow root
//
// So the two renderers never interleave: the boundary is a custom element,
// exactly as `@pierre/diffs` already does with its own. That also means
// **StyleX cannot reach the rows** — a shadow root is what a stylesheet does
// not cross — and the door through is `FILE_TREE_UNSAFE_CSS_ATTRIBUTE`, the
// same shape as the diff renderer's `unsafeCSS`.
//
// It is a beta, and its own dependency is a Preact beta. Both are written down
// here rather than discovered later.
//
// ── the model is built once and mutated, not rebuilt ──────────────────────
//
// `useFileTree(options)` is `useState(() => new FileTree(options))` and nothing
// else: **the options are read on the first render and never again.** So the
// first shape here memoised the paths array to keep its identity stable, which
// was solving a problem the hook does not have — and the lint said so, which is
// how it was found.
//
// The library's own answer is `resetPaths`, and it is the better one anyway: it
// updates the tree in place, so which folders were open survives a patch
// arriving underneath it.

export interface TreeProps {
  /** Every path in the patch, in the order the patch carries them. */
  readonly paths: ReadonlyArray<string>;
  /**
   * Whether it should be on screen.
   *
   * A prop rather than the caller mounting and unmounting, because **a
   * component that is not in the tree has nothing to transition**. It has to
   * be here to slide out, so the caller keeps it mounted and this decides
   * which way it is going. See the animation mandate in AGENTS.md.
   */
  readonly open: boolean;
  /** Jump the patch to a file. Called only for leaves, never for a directory. */
  readonly onPick: (path: string) => void;
  /** Put the tree away. */
  readonly onClose: () => void;
}

/**
 * The rows live in a shadow root, so this is the only way to colour them.
 *
 * Kept to the window's own tokens rather than the library's defaults, and kept
 * short: this is not a place to restyle somebody else's component, only to stop
 * it being the one thing on screen wearing a different palette.
 */
const unsafeCss = `
  [data-file-tree-id] { font-family: inherit; font-size: ${"0.875rem"}; }
`;

const styles = stylex.create({
  // Over the patch, not beside it. A permanent tree would take width from the
  // code in a column that is a few hundred pixels wide to begin with — and the
  // tree is opened to find one file, which is a moment rather than a mode.
  //
  // **It closes on a pick**, for the same reason: leaving it up would leave the
  // file it was opened to find sitting underneath it. See `onPick` in `Diff`.
  //
  // Not a modal, deliberately: the point is clicking a file and watching the
  // patch move — and with the tree closing behind the click, that motion is
  // the whole of the feedback that the pick landed.
  over: {
    position: "absolute",
    insetBlock: 0,
    insetInlineStart: 0,
    zIndex: 3,
    display: "flex",
    flexDirection: "column",
    width: "min(20rem, 78%)",
    backgroundColor: colors.base,
    // The one place a shadow is the right answer: this floats over content, so
    // it needs to read as being in front rather than as being part of it.
    boxShadow: "0.5rem 0 1.5rem -0.5rem rgba(0, 0, 0, 0.45)",
  },
  // ── it slides, per the mandate in AGENTS.md ───────────────────────────────
  //
  // `transform` and `opacity`, which are the two properties a compositor can
  // animate without touching layout — this is drawn over a virtualised patch,
  // and animating a width would reflow the code underneath it every frame.
  //
  // A *dynamic* style. `${FOLD_MS}ms` inside a static `stylex.create` value is
  // a build error about theming rules, and no gate catches it because only
  // Vite runs the StyleX pass. See the note in AGENTS.md.
  moving: (ms: number) => ({
    transitionProperty: "transform, opacity",
    transitionTimingFunction: "cubic-bezier(0.32, 0.72, 0, 1)",
    transitionDuration: {
      default: `${ms}ms`,
      "@media (prefers-reduced-motion: reduce)": "0s",
    },
  }),
  /** Off to the left and invisible: where it comes from and where it goes. */
  away: { transform: "translateX(-102%)", opacity: 0 },
  shown: { transform: "translateX(0)", opacity: 1 },
  head: {
    display: "flex",
    alignItems: "center",
    gap: "0.4rem",
    flexShrink: 0,
    minHeight: space.titlebar,
    paddingInline: "0.6rem",
    color: colors.muted,
    fontSize: text.small,
  },
  count: { flex: 1, minWidth: 0 },
  close: {
    flexShrink: 0,
    padding: "0.1rem 0.4rem",
    backgroundColor: "transparent",
    borderStyle: "none",
    borderRadius: "0.2rem",
    color: colors.muted,
    font: "inherit",
    fontSize: text.small,
    cursor: "pointer",
    ":hover": { color: colors.text },
  },
  tree: { flex: 1, minHeight: 0 },
});

export function Tree({ paths, open, onPick, onClose }: TreeProps) {
  // `open` drives the class directly, with no "wait one frame" dance.
  //
  // That dance is only needed when an element mounts straight into its shown
  // state — the browser then has nothing to interpolate *from* and it simply
  // appears. Here the element is already mounted and already at `away`, so
  // flipping the class is a change on a live element and the transition runs.
  // Keeping it mounted is what buys that, and it is the same thing that lets
  // it slide *out*.

  const { model } = useFileTree({
    // Read once, on the first render. Every later change goes through the
    // effect below.
    paths,
    // `packages/server/src/x.ts` as one row rather than three. Without it a
    // repository laid out like this one is a tree of empty directories.
    flattenEmptyDirectories: true,
    // The patch's order is the diff's order, which is not the reading order of
    // a tree. Let it sort.
    presorted: false,
    // Open, because a patch is not a repository. A file tree of a whole
    // checkout has to start closed or it is thousands of rows; a tree of the
    // fifteen files in one diff has nothing to hide, and a person opening it
    // to find a file should see the files.
    initialExpansion: "open",
  });

  // A new patch while the tree is open. Joined rather than compared by
  // identity, because the array is rebuilt every render by the caller and its
  // *contents* are what decide whether the tree has changed.
  const key = paths.join("\n");
  const shown = useRef(key);
  useEffect(() => {
    if (shown.current === key) {
      return;
    }
    shown.current = key;
    model.resetPaths(key === "" ? [] : key.split("\n"));
  }, [key, model]);

  const selected = useFileTreeSelection(model);

  // Jump on a change of selection, not on a click handler, because selection
  // is what the tree exposes — and it is also what the keyboard changes, so
  // arrow-then-enter works without a second path through this.
  //
  // Directories are selectable too and must not jump: a directory is not a file
  // the patch has an item for, and asking to scroll to one would be asking for
  // an id that does not exist.
  const last = useRef<string | undefined>(undefined);
  useEffect(() => {
    const path = selected[0];
    if (path === undefined || path === last.current || !key.split("\n").includes(path)) {
      return;
    }
    last.current = path;
    onPick(path);
  }, [selected, key, onPick]);

  return (
    <div
      // Out of the way of the keyboard and the pointer while it is off screen.
      // It is still in the layout — that is what lets it animate — so without
      // this a tab from the head row would land in an invisible tree.
      inert={!open}
      aria-hidden={!open}
      {...stylex.props(styles.over, styles.moving(FOLD_MS), open ? styles.shown : styles.away)}
    >
      <div {...stylex.props(styles.head)}>
        <span {...stylex.props(styles.count)}>
          {paths.length === 1 ? "1 file" : `${paths.length} files`}
        </span>
        <button
          type="button"
          data-nav-item
          title="hide the file tree"
          aria-label="hide the file tree"
          onClick={onClose}
          {...stylex.props(styles.close)}
        >
          close
        </button>
      </div>

      <FileTree
        model={model}
        data-file-tree-unsafe-css={unsafeCss}
        {...stylex.props(styles.tree)}
      />
    </div>
  );
}
