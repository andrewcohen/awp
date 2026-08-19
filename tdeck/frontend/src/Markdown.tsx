import { memo } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

// Agent prose is markdown, and reading it raw means reading the punctuation.
//
// A coding agent writes in lists, inline code, tables and fenced blocks —
// rendered as plain text those become asterisks, backticks and pipes, which is
// the transcription problem this whole surface exists to avoid. The terminal tab
// shows what the agent drew; this one should show what it meant.
//
// Styled element by element rather than with a typography plugin. The defaults
// there are sized for an article — large headings, generous vertical rhythm —
// and these are chat messages, where a level-two heading is a sentence with
// emphasis rather than a new section of a document.
//
// Every size is a Tailwind step. This file used to be full of arbitrary values
// — a 13px body, an 11.5px code span — carried over from a layout that was
// sharing a window with a terminal and losing: sizes picked to squeeze prose
// into the space left over beside a pty. They read as cramped anywhere else,
// and half a pixel is not a decision anyone can hold in their head. The scale
// is text-base for prose and text-sm for code and secondary rows, stepping the
// same way as every other surface.
const components = {
  p: (props: { children?: React.ReactNode }) => (
    <p className="mb-2 text-base leading-relaxed last:mb-0" {...props} />
  ),
  h1: (props: { children?: React.ReactNode }) => (
    <h1 className="mt-4 mb-2 text-lg font-semibold first:mt-0" {...props} />
  ),
  h2: (props: { children?: React.ReactNode }) => (
    <h2 className="mt-4 mb-2 text-base font-semibold first:mt-0" {...props} />
  ),
  h3: (props: { children?: React.ReactNode }) => (
    <h3
      className="text-muted-foreground mt-4 mb-2 text-sm font-semibold uppercase tracking-wide first:mt-0"
      {...props}
    />
  ),
  ul: (props: { children?: React.ReactNode }) => (
    <ul
      className="mb-2 list-disc space-y-1 pl-5 text-base last:mb-0"
      {...props}
    />
  ),
  ol: (props: { children?: React.ReactNode }) => (
    <ol
      className="mb-2 list-decimal space-y-1 pl-5 text-base last:mb-0"
      {...props}
    />
  ),
  li: (props: { children?: React.ReactNode }) => (
    <li className="leading-relaxed" {...props} />
  ),
  strong: (props: { children?: React.ReactNode }) => (
    <strong className="font-semibold" {...props} />
  ),
  a: (props: { children?: React.ReactNode; href?: string }) => (
    // No navigation: this is a window, not a browser, and following a link
    // inside it would replace the app with a web page and no way back.
    <span
      className="text-primary underline underline-offset-2"
      title={props.href}
    >
      {props.children}
    </span>
  ),
  code: ({
    children,
    className,
  }: {
    children?: React.ReactNode;
    className?: string;
  }) => {
    const fenced = (className ?? "").startsWith("language-");
    if (fenced) {
      return (
        <code className="font-mono text-sm leading-relaxed">{children}</code>
      );
    }
    return (
      <code className="bg-muted rounded px-1 py-0.5 font-mono text-sm">
        {children}
      </code>
    );
  },
  pre: (props: { children?: React.ReactNode }) => (
    <pre
      className="bg-muted/60 border-border mb-2 max-h-72 overflow-auto rounded-md border p-2.5 last:mb-0"
      {...props}
    />
  ),
  blockquote: (props: { children?: React.ReactNode }) => (
    <blockquote
      className="border-border text-muted-foreground mb-2 border-l-2 pl-3"
      {...props}
    />
  ),
  table: (props: { children?: React.ReactNode }) => (
    <div className="mb-2 overflow-x-auto last:mb-0">
      <table className="w-full text-sm" {...props} />
    </div>
  ),
  th: (props: { children?: React.ReactNode }) => (
    <th
      className="border-border text-muted-foreground border-b px-2 py-1 text-left font-medium"
      {...props}
    />
  ),
  td: (props: { children?: React.ReactNode }) => (
    <td className="border-border/50 border-b px-2 py-1 align-top" {...props} />
  ),
  // An agent citing a screenshot writes ![](/abs/path), and a browser cannot
  // open a file:// URL from an http page — it fails silently, leaving a broken
  // image and no reason why. Absolute paths are rewritten through the server,
  // which is the only party that can read the disk.
  //
  // Markdown has one image syntax and no video syntax, so a recording written to
  // disk and linked the only way markdown allows would render as an <img> that
  // cannot decode — a broken-image icon where a video should be. The extension
  // decides the element instead. That matters more than it looks: a tool result
  // can only carry still images, so writing a file and linking it is the *only*
  // route an agent has to show a video at all.
  img: (props: { src?: string; alt?: string }) => {
    const src = props.src ?? "";
    const resolved =
      src.startsWith("/") && !src.startsWith("//")
        ? `/file?path=${encodeURIComponent(src)}`
        : src;
    const path = src.split("?")[0] ?? "";

    if (/\.(mp4|webm|mov|m4v)$/i.test(path)) {
      return (
        <video
          controls
          preload="metadata"
          src={resolved}
          className="border-border my-2 max-h-80 w-fit max-w-full rounded-lg border"
        />
      );
    }
    if (/\.(mp3|wav|ogg|m4a|flac)$/i.test(path)) {
      return <audio controls src={resolved} className="my-2 w-full max-w-md" />;
    }
    return (
      <a
        href={resolved}
        target="_blank"
        rel="noreferrer"
        className="block w-fit"
      >
        <img
          src={resolved}
          alt={props.alt ?? ""}
          loading="lazy"
          className="border-border my-2 max-h-80 w-fit max-w-full rounded-lg border object-contain"
        />
      </a>
    );
  },
  hr: () => <hr className="border-border my-3" />,
};

export const Markdown = memo(function Markdown({ text }: { text: string }) {
  return (
    <div className="min-w-0">
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>
        {text}
      </ReactMarkdown>
    </div>
  );
});
