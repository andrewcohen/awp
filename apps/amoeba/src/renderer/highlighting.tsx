import { WorkerPoolContext } from "@pierre/diffs/react";
import { getOrCreateWorkerPoolSingleton } from "@pierre/diffs/worker";
import type { ReactNode } from "react";

// Where the diff's syntax highlighting happens, and why it is not the main
// thread.
//
// ── what this was before ───────────────────────────────────────────────────
//
// Nothing. `CodeView` reads its worker pool out of a React context and treats
// an absent one as "highlight here", so a tree with no provider highlights
// every file synchronously — on the same thread that runs the terminal's render
// loop, React, and the pointer handlers. It works, it is correct, and it is
// what "the diff feels chunky when I scroll it" was: the virtualizer bringing a
// new file into view has to tokenize it before it can draw a frame, and shiki
// tokenizing a thousand-line file is not a frame's worth of work.
//
// Nothing in the library's types says this. `disableWorkerPool` defaults to
// false, which reads like the pool is on unless it is turned off, and what it
// actually means is "do not use the pool *from the context*, if there is one".
// The line that settles it is in its own source:
//
//   const poolManager = useContext(WorkerPoolContext);
//   … new CodeView(options, !disableWorkerPool ? poolManager : undefined, true)
//
// ── the pool is a module, not a component ──────────────────────────────────
//
// The library ships `WorkerPoolContextProvider`, and it is deliberately not
// used here. It builds the pool in `useState` and terminates it in an effect
// cleanup once the last provider unmounts — and `terminateWorkerPoolSingleton`
// clears the singleton, so the manager an unmounted provider is still holding
// in state is one whose workers are gone and whose `initialize` will not run
// again. Under StrictMode the mount/unmount/remount rehearsal walks straight
// into exactly that.
//
// A pool for the window's life is not component state in the first place. It is
// built once, here, at module scope, and outlives every tree that reads it —
// which is also what makes the tab switching in the accessory column free.
// Base UI unmounts a hidden panel, so a provider inside the diff panel would
// build and destroy the pool every time someone looked at jobs instead.
//
// ── the numbers, and what each is for ─────────────────────────────────────
//
//   poolSize 3        the default is 8. Each worker is its own shiki, its own
//                     grammars and its own theme set, and this panel is one
//                     narrow column showing one patch — three files being
//                     tokenized at once is already more than the virtualizer
//                     can be looking at.
//   shiki-js          the library's own default, restated so it is not lost by
//                     accident: the JavaScript regex engine rather than the
//                     oniguruma wasm one. Nothing here waits on a wasm compile
//                     before the first file can be drawn.

/**
 * Shiki's themes, one per scheme.
 *
 * Named rather than derived from the window's own palette. The colours in
 * `tokens.stylex` are six roles — text, muted, border and three signals —
 * which is the vocabulary a list of rows needs and nothing like the thirty a
 * syntax theme assigns. Building one out of six would be inventing the other
 * twenty-four.
 *
 * **The pool and the view have to be given the same value.** The pool resolves
 * these into token colours up front; a `CodeView` asking for a theme the pool
 * was not told about makes the pool re-resolve and re-broadcast to every worker
 * before it can answer, which is the cost this file exists to avoid.
 */
export const THEME = { light: "github-light", dark: "github-dark" } as const;

const pool =
  typeof Worker === "undefined"
    ? undefined
    : getOrCreateWorkerPoolSingleton({
        poolOptions: {
          workerFactory: () =>
            new Worker(new URL("./highlight.worker.ts", import.meta.url), { type: "module" }),
          poolSize: 3,
        },
        highlighterOptions: { theme: THEME, preferredHighlighter: "shiki-js" },
      });

/**
 * Put the pool where `CodeView` looks for it.
 *
 * Wraps the whole window rather than the diff panel — see above. The workers
 * start when this module is first imported, which is at launch, so the first
 * patch someone opens is not also the moment three workers boot.
 */
export function Highlighting({ children }: { readonly children: ReactNode }) {
  return <WorkerPoolContext.Provider value={pool}>{children}</WorkerPoolContext.Provider>;
}
