import { parsePatchFiles } from "@pierre/diffs";
import { CodeView, File } from "@pierre/diffs/react";
import * as stylex from "@stylexjs/stylex";
import { type ReactNode, isValidElement, useEffect, useId, useState } from "react";
import { THEME } from "./highlighting";
import { useColorScheme } from "./theme";
import { colors, text } from "./tokens.stylex";

// A fenced block in a message, drawn as the thing it is.
//
// An agent answers in markdown and three of its fences are not prose:
//
//   ```diff       a patch. Rendered by the same library that renders every
//                 patch in this window, because a change shown in a message
//                 and the same change shown in the diff panel reading as two
//                 different things is worse than either
//   ```mermaid    a diagram
//   ```<lang>     code, highlighted by the same shiki the diff panel already
//                 has three workers running for
//
// ── one highlighter, and it is already on the other side of a worker ──────
//
// `File` reads its worker pool out of the same context `CodeView` does — see
// highlighting.tsx, which puts one there for the window's life. So a fence
// costs a message to a worker rather than a tokenize on the thread the
// terminal's render loop is on, and it does it without a second highlighter,
// a second theme or a second set of grammars.
//
// The alternative was shiki directly, and it is the same library twice: the
// diff panel's copy resolved in three workers and a new one resolved here.

/** Where a fence's language lives: react-markdown writes `language-ts`. */
const LANGUAGE = /^language-(?<name>[\w+-]+)$/;

/** A patch, under either of the names people write. */
const PATCH = new Set(["diff", "patch"]);

/**
 * The text inside a fence, flattened.
 *
 * A fence is one string in the ordinary case and an array of strings when the
 * content held something react-markdown split — so this is a walk rather than
 * a cast, and a cast is what would silently render `[object Object]`.
 */
const textOf = (node: ReactNode): string => {
  if (typeof node === "string") {
    return node;
  }
  if (Array.isArray(node)) {
    return node.map((one) => textOf(one as ReactNode)).join("");
  }
  if (isValidElement(node)) {
    return textOf((node.props as { children?: ReactNode }).children);
  }
  return "";
};

/**
 * A fenced block, by what it holds.
 *
 * Given react-markdown's `pre`, whose only child is the `code` element that
 * carries the language. Reading it here rather than overriding `code` is
 * deliberate: what replaces a fence is a `<div>`, and a div inside a `<pre>`
 * is invalid markup that browsers repair by closing the `pre` early.
 */
export const Fence = ({ children }: { readonly children: ReactNode }) => {
  const only = Array.isArray(children) ? children[0] : children;
  const props = isValidElement(only) ? (only.props as { className?: string }) : {};
  const language = LANGUAGE.exec(props.className ?? "")?.groups?.["name"]?.toLowerCase();
  const source = textOf(children);

  // A fence with no language is not code this can say anything about, and
  // guessing one is how a shell transcript gets highlighted as JavaScript.
  if (language === undefined) {
    return <pre {...stylex.props(styles.plain)}>{source}</pre>;
  }

  if (PATCH.has(language)) {
    return <Patch source={source} />;
  }

  if (language === "mermaid") {
    return <Mermaid source={source} />;
  }

  return <Code source={source} language={language} />;
};

/**
 * A patch in a message, or the patch as highlighted text when it is not one.
 *
 * ── `PatchDiff` was the obvious component and it is the wrong one ─────────
 *
 * It refuses anything that is not exactly one file — measured in a real
 * window, by an agent replying with a one-line example:
 *
 *   Error: FileDiff: Provided patch must contain exactly 1 file diff
 *
 * which the agent column's error boundary caught, and which is what a fence
 * in a message looks like most of the time: a fragment, a two-file change, or
 * a few lines of `+` and `-` that were never a patch at all. So this parses
 * first and draws whatever came back, the same way the diff panel does — and
 * when nothing came back, the text is still a patch to *read*, so it goes
 * through shiki's own `diff` grammar rather than being thrown away.
 */
const Patch = ({ source }: { readonly source: string }) => {
  const scheme = useColorScheme();
  // Parsed in a try, because this string was written by a model. The library
  // throws on input it cannot make sense of, and a throw here is the whole
  // panel replaced by a stack trace.
  const items = (() => {
    try {
      return parsePatchFiles(source, "message").flatMap((one) =>
        one.files.map((fileDiff, index) => ({
          id: `patch-${String(index)}`,
          type: "diff" as const,
          fileDiff,
        })),
      );
    } catch {
      return [];
    }
  })();

  if (items.length === 0) {
    return <Code source={source} language="diff" />;
  }

  return (
    <div {...stylex.props(styles.block)}>
      <CodeView
        items={items}
        options={{
          theme: THEME,
          themeType: scheme,
          // Wrapped, never scrolled sideways — the window's rule, and this is
          // a column narrower than the diff panel's.
          overflow: "wrap",
          diffStyle: "unified",
          // Nothing here is selectable or commentable: a patch in a message is
          // something to read, and the anchors a comment needs (a revision, a
          // path, a side) do not exist for it.
          enableLineSelection: false,
        }}
      />
    </div>
  );
};

/** Code, highlighted by the pool the diff panel already keeps three workers in. */
const Code = ({ source, language }: { readonly source: string; readonly language: string }) => {
  const scheme = useColorScheme();
  return (
    <div {...stylex.props(styles.block)}>
      <File
        file={{
          // A name, because that is what the library infers a language from,
          // and the language as well: a fence says `ts`, and shiki knows that
          // name where a filename would have to be invented from it.
          name: `block.${language}`,
          contents: source,
          lang: language as never,
        }}
        options={{
          theme: THEME,
          themeType: scheme,
          overflow: "wrap",
          disableFileHeader: true,
          // Numbers on a four-line snippet in a message are furniture. The
          // diff panel keeps them because a line number there is an address
          // somebody comments on; here nothing points at one.
          disableLineNumbers: true,
        }}
      />
    </div>
  );
};

/**
 * Draw one mermaid diagram.
 *
 * Dynamic import, because mermaid is 83MB installed and several hundred
 * kilobytes in a bundle with its own parser per diagram type — a top-level
 * import puts all of it in the renderer's first load for a block most
 * conversations never contain. Same shape as the highlighting worker.
 *
 * `initialize` runs per call rather than once, because the theme it sets is
 * the window's appearance and that changes while the window is open.
 */
const draw = async (id: string, source: string, scheme: "light" | "dark"): Promise<string> => {
  const mermaid = (await import("mermaid")).default;
  mermaid.initialize({
    startOnLoad: false,
    // Its own strict mode, which is what makes the SVG safe to insert: the
    // source is written by an agent, and a diagram label is a place to put a
    // `<script>` otherwise.
    securityLevel: "strict",
    // Its own dark theme rather than one built from this window's six roles —
    // the same argument as shiki's themes in highlighting.tsx.
    theme: scheme === "dark" ? "dark" : "default",
  });
  return (await mermaid.render(id, source)).svg;
};

/**
 * A mermaid graph, or its source and mermaid's reason when it will not draw.
 *
 * ── the fallback is not hypothetical, and it says why ────────────────────
 *
 * It fired on the first real reply that contained a diagram, and it kept
 * firing — twice, on two different messages — while the diagrams themselves
 * were fine. What was wrong was the agent's fencing:
 *
 *   ```mermaid
 *   graph TD; A-->B;
 *   ``````mermaid            ← six backticks, so the fence never closed
 *   graph TD; A-->B; B-->C;
 *
 * so one block swallowed the next and mermaid threw on the result, correctly,
 * with "Parse error on line 3" — which is exactly right for the text it was
 * given and nonsense for the text anybody thought it had.
 *
 * **Showing the reason is what settled it.** Two hypotheses were built and
 * coded against on the strength of a bare "did not draw" — that two diagrams
 * were racing, and then that StrictMode's double effect invoke made one
 * component collide with itself — and both were wrong. Rendering mermaid's
 * own sentence, plus the source it was given, answered it on the next run.
 * The general shape is this file's neighbours': **read what the other side
 * said, rather than inferring it from the fact that it failed.**
 *
 * The other two options are both worse than the text: an empty box says the
 * window is broken, and letting it throw replaces the whole column with a
 * stack trace over a diagram somebody could have read as source.
 */
const Mermaid = ({ source }: { readonly source: string }) => {
  const scheme = useColorScheme();
  const [svg, setSvg] = useState<string | undefined>(undefined);
  // The reason, not a flag. A diagram that will not draw is nearly always
  // malformed, and mermaid says exactly where — "Parse error on line 2" is
  // the difference between a person fixing their fence and shrugging at it.
  const [failed, setFailed] = useState<string | undefined>(undefined);
  // Unique, because mermaid puts this id in the DOM it builds and two
  // diagrams sharing one produce the second drawn inside the first.
  //
  // `useId` rather than a random one: it is stable across renders, so a
  // re-render does not make mermaid build a second copy — and the colons it
  // contains are stripped, because mermaid puts the value in a selector and
  // `:r1:` is not one.
  const id = `mermaid-${useId().replaceAll(":", "")}`;

  useEffect(() => {
    let gone = false;
    draw(id, source, scheme)
      .then((drawn) => {
        if (!gone) {
          setSvg(drawn);
        }
      })
      .catch((wrong: unknown) => {
        if (!gone) {
          setFailed(wrong instanceof Error ? wrong.message : String(wrong));
        }
      });
    return () => {
      gone = true;
    };
  }, [source, scheme, id]);

  if (failed !== undefined) {
    return (
      <div {...stylex.props(styles.block)}>
        <p {...stylex.props(styles.broken)}>this diagram did not draw — {failed.split("\n")[0]}</p>
        <pre {...stylex.props(styles.plain)}>{source}</pre>
      </div>
    );
  }

  return svg === undefined ? (
    // Not a spinner: the jobs panel's rule, and a diagram arrives in a frame
    // or two. What this reserves is a line of text saying what is coming, so
    // the transcript does not jump by the diagram's height when it lands.
    <p {...stylex.props(styles.drawing)}>drawing…</p>
  ) : (
    <div
      {...stylex.props(styles.block, styles.diagram)}
      // mermaid's own output, from its own strict mode. There is no other way
      // to mount an SVG it built as a string.
      // eslint-disable-next-line react/no-danger
      dangerouslySetInnerHTML={{ __html: svg }}
    />
  );
};

const styles = stylex.create({
  block: { margin: "0.5rem 0", minWidth: 0, overflowX: "auto" },
  diagram: { display: "flex", justifyContent: "center" },
  plain: {
    fontFamily: text.mono,
    fontSize: text.small,
    color: colors.text,
    backgroundColor: colors.surface,
    borderRadius: "0.3rem",
    padding: "0.5rem 0.6rem",
    margin: "0.5rem 0",
    whiteSpace: "pre-wrap",
    overflowWrap: "anywhere",
  },
  drawing: { fontFamily: text.ui, fontSize: text.small, color: colors.muted },
  broken: { fontFamily: text.ui, fontSize: text.small, color: colors.muted },
});
