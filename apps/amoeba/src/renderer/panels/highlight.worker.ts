// The syntax highlighter, off the main thread.
//
// One import and nothing else, and the indirection is Vite's requirement rather
// than a preference. A worker is addressed by URL — `new URL(…, import.meta.url)`
// — and a bare package specifier is not one. The library's own entry
// (`@pierre/diffs/worker/worker.js`) is published with bare imports of shiki and
// friends for a bundler to resolve, so it cannot be handed to `new Worker` as it
// stands. A one-line local module can: Vite sees a relative URL, follows it,
// resolves the imports inside and emits a real worker bundle.
//
// `worker.js` and not `worker-portable.js`. The portable build is the same code
// with shiki pre-bundled into it — 452KB against 60KB — for hosts with no
// bundler. Vite is a bundler, so taking the portable one would be shipping a
// second copy of shiki that no chunk is shared with.
//
// The lint rule below wants every import assigned to something. A worker entry
// is the one module shape where that is meaningless: the file is not imported
// by the page at all, it is *the program a Worker runs*, and its whole job is
// the side effect of installing an `onmessage` handler. There is nothing to
// assign.
// oxlint-disable-next-line import/no-unassigned-import
import "@pierre/diffs/worker/worker.js";
