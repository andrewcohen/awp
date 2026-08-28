import * as stylex from "@stylexjs/stylex";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import { colors, text } from "./tokens.stylex";

// Markdown, for text this window did not write.
//
// ── why a library and not a hundred lines ─────────────────────────────────
//
// Because markdown is a specification, and the failure mode of a hand-rolled
// subset is not "some things do not render" — it is *plausible wrong output*: a
// nested list flattened, a table shown as pipes, a fenced block whose closing
// fence is treated as content. A pull request description is the one text in
// this window nobody here wrote, so it is exactly where guessing is worst.
//
// **`react-markdown` and not `marked`.** The alternative renders a string of
// HTML, which then has to be handed to `dangerouslySetInnerHTML` — and this
// content is written by whoever opened the pull request. That is an injection
// straight into the window, so it would need a sanitiser beside it: two
// dependencies and a rule to get right, against one that builds React elements
// and never produces HTML at all.
//
// `remark-gfm` because GitHub's own dialect is what a PR body is written in:
// task lists, tables, strikethrough and bare autolinks. Without it a checklist
// — which is what half of all PR descriptions are — renders as literal `[ ]`.
//
// ── the components are overridden, not styled by cascade ─────────────────
//
// StyleX generates atomic classes and has no descendant selectors by design, so
// there is no way to say "paragraphs inside this block". Each element is
// therefore mapped to a styled component here, which is more code and is also
// the only shape that works: it puts every rule where the element it belongs to
// is named.

const styles = stylex.create({
  // The block itself sets the reading measure and nothing else.
  root: { fontSize: text.small, lineHeight: 1.55, overflowWrap: "break-word" },
  p: { marginBlock: "0.5rem" },
  // Headings step down by weight rather than by size: the type floor is 14px
  // and a PR body's `####` would otherwise land under it. See AGENTS.md.
  h1: { marginBlock: "0.8rem 0.35rem", fontSize: text.lead, fontWeight: text.strong },
  h2: { marginBlock: "0.7rem 0.3rem", fontSize: text.body, fontWeight: text.strong },
  h3: { marginBlock: "0.6rem 0.25rem", fontSize: text.small, fontWeight: text.strong },
  ul: { marginBlock: "0.4rem", paddingInlineStart: "1.1rem" },
  li: { marginBlock: "0.15rem" },
  a: { color: colors.ready, textDecorationLine: "underline" },
  code: {
    padding: "0.05rem 0.2rem",
    backgroundColor: colors.raised,
    borderRadius: "0.15rem",
    fontFamily: text.mono,
    fontSize: text.small,
  },
  // A fenced block scrolls inside its own box rather than widening the panel —
  // the rule from AGENTS.md: a column must never grow a horizontal scrollbar,
  // so the wide thing carries one itself.
  pre: {
    margin: "0.5rem 0",
    padding: "0.4rem 0.5rem",
    maxHeight: "18rem",
    overflow: "auto",
    backgroundColor: colors.surface,
    borderRadius: "0.25rem",
    fontFamily: text.mono,
    fontSize: text.small,
    lineHeight: 1.45,
  },
  // Inside a `pre`, the code element must not paint its own chip.
  bare: { padding: 0, backgroundColor: "transparent" },
  quote: {
    margin: "0.5rem 0",
    paddingInlineStart: "0.6rem",
    borderInlineStartWidth: 2,
    borderInlineStartStyle: "solid",
    borderInlineStartColor: colors.border,
    color: colors.muted,
  },
  hr: {
    marginBlock: "0.8rem",
    borderStyle: "none",
    borderBlockStartWidth: 1,
    borderBlockStartStyle: "solid",
    borderBlockStartColor: colors.border,
  },
  // Its own scroll box, for the same reason `pre` has one.
  tableBox: { margin: "0.5rem 0", overflowX: "auto" },
  table: { borderCollapse: "collapse", fontSize: text.small },
  cell: {
    padding: "0.15rem 0.4rem",
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    textAlign: "start",
  },
  head: { fontWeight: text.strong },
  // An image is not fetched: the panel is offline as far as a PR body's
  // screenshots go — they are on GitHub behind a session this window does not
  // have, so the alt text is what there is to show. See the note in `img`.
  image: { color: colors.muted, fontStyle: "italic" },
  strong: { fontWeight: text.strong },
});

/**
 * Every element, mapped to a styled one — at module scope, and that matters.
 *
 * Defined inside the component this map is rebuilt on every render, which makes
 * each element type a *new component identity* and remounts the whole rendered
 * body every time the panel re-renders. The lint says so
 * (`no-unstable-nested-components`) and it is right: what it costs is not a
 * frame of work, it is losing whatever state the subtree held — a scrolled code
 * block jumping back to the top while somebody reads it.
 *
 * It closes over nothing but the styles above, which are module scope too, so
 * there is nothing to capture and no reason it was ever inside.
 */
const components: Components = {
  p: (props) => <p {...stylex.props(styles.p)}>{props.children}</p>,
  h1: (props) => <div {...stylex.props(styles.h1)}>{props.children}</div>,
  h2: (props) => <div {...stylex.props(styles.h2)}>{props.children}</div>,
  h3: (props) => <div {...stylex.props(styles.h3)}>{props.children}</div>,
  // Deeper headings are the same as `h3`: the type floor is 14px, so
  // there is nowhere smaller for them to go, and a `######` in a PR body
  // is a heading in name rather than a sixth level of hierarchy.
  h4: (props) => <div {...stylex.props(styles.h3)}>{props.children}</div>,
  h5: (props) => <div {...stylex.props(styles.h3)}>{props.children}</div>,
  h6: (props) => <div {...stylex.props(styles.h3)}>{props.children}</div>,
  ul: (props) => <ul {...stylex.props(styles.ul)}>{props.children}</ul>,
  ol: (props) => <ol {...stylex.props(styles.ul)}>{props.children}</ol>,
  li: (props) => <li {...stylex.props(styles.li)}>{props.children}</li>,
  strong: (props) => <strong {...stylex.props(styles.strong)}>{props.children}</strong>,
  blockquote: (props) => <blockquote {...stylex.props(styles.quote)}>{props.children}</blockquote>,
  hr: () => <hr {...stylex.props(styles.hr)} />,
  // A new tab, not this window: following a link inside the panel would
  // navigate the renderer away from the application.
  a: (props) => (
    <a href={props.href} target="_blank" rel="noreferrer" {...stylex.props(styles.a)}>
      {props.children}
    </a>
  ),
  pre: (props) => <pre {...stylex.props(styles.pre)}>{props.children}</pre>,
  code: (props) => (
    // `className` is how react-markdown marks a fenced block's language,
    // and its absence is what distinguishes inline code from a block —
    // the block's own `pre` already carries the padding and the ground.
    <code {...stylex.props(styles.code, props.className !== undefined && styles.bare)}>
      {props.children}
    </code>
  ),
  table: (props) => (
    <div {...stylex.props(styles.tableBox)}>
      <table {...stylex.props(styles.table)}>{props.children}</table>
    </div>
  ),
  th: (props) => <th {...stylex.props(styles.cell, styles.head)}>{props.children}</th>,
  td: (props) => <td {...stylex.props(styles.cell)}>{props.children}</td>,
  // Not rendered as an image, deliberately. A screenshot in a PR body
  // lives on GitHub's user-content host, which this window has no
  // session for, so an `<img>` would be a broken icon where a caption
  // would do. The alt text is the part that was written by a person.
  img: (props) => (
    <span {...stylex.props(styles.image)}>[image: {props.alt ?? "no description"}]</span>
  ),
};

export function Markdown({ children }: { readonly children: string }) {
  return (
    <div {...stylex.props(styles.root)}>
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>
        {children}
      </ReactMarkdown>
    </div>
  );
}
