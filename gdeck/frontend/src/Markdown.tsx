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
const components = {
  p: (props: { children?: React.ReactNode }) => (
    <p className="mb-2 text-[13px] leading-relaxed last:mb-0" {...props} />
  ),
  h1: (props: { children?: React.ReactNode }) => (
    <h1 className="mt-3 mb-1.5 text-[14px] font-semibold first:mt-0" {...props} />
  ),
  h2: (props: { children?: React.ReactNode }) => (
    <h2 className="mt-3 mb-1.5 text-[13px] font-semibold first:mt-0" {...props} />
  ),
  h3: (props: { children?: React.ReactNode }) => (
    <h3 className="text-muted-foreground mt-3 mb-1.5 text-[13px] font-semibold first:mt-0" {...props} />
  ),
  ul: (props: { children?: React.ReactNode }) => (
    <ul className="mb-2 list-disc space-y-1 pl-5 text-[13px] last:mb-0" {...props} />
  ),
  ol: (props: { children?: React.ReactNode }) => (
    <ol className="mb-2 list-decimal space-y-1 pl-5 text-[13px] last:mb-0" {...props} />
  ),
  li: (props: { children?: React.ReactNode }) => <li className="leading-relaxed" {...props} />,
  strong: (props: { children?: React.ReactNode }) => <strong className="font-semibold" {...props} />,
  a: (props: { children?: React.ReactNode; href?: string }) => (
    // No navigation: this is a window, not a browser, and following a link
    // inside it would replace the app with a web page and no way back.
    <span className="text-primary underline underline-offset-2" title={props.href}>
      {props.children}
    </span>
  ),
  code: ({ children, className }: { children?: React.ReactNode; className?: string }) => {
    const fenced = (className ?? "").startsWith("language-");
    if (fenced) {
      return <code className="font-mono text-[11.5px] leading-relaxed">{children}</code>;
    }
    return (
      <code className="bg-muted rounded px-1 py-0.5 font-mono text-[11.5px]">{children}</code>
    );
  },
  pre: (props: { children?: React.ReactNode }) => (
    <pre
      className="bg-muted/60 border-border mb-2 max-h-72 overflow-auto rounded-md border p-2.5 last:mb-0"
      {...props}
    />
  ),
  blockquote: (props: { children?: React.ReactNode }) => (
    <blockquote className="border-border text-muted-foreground mb-2 border-l-2 pl-3" {...props} />
  ),
  table: (props: { children?: React.ReactNode }) => (
    <div className="mb-2 overflow-x-auto last:mb-0">
      <table className="w-full text-[12px]" {...props} />
    </div>
  ),
  th: (props: { children?: React.ReactNode }) => (
    <th className="border-border text-muted-foreground border-b px-2 py-1 text-left font-medium" {...props} />
  ),
  td: (props: { children?: React.ReactNode }) => (
    <td className="border-border/50 border-b px-2 py-1 align-top" {...props} />
  ),
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
