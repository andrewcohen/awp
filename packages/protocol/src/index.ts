// The RPC contract between an awp client and the daemon.
//
// Effect v4 folds RPC into core, so the import is `effect/unstable/rpc` and
// there is no `@effect/rpc` in the tree — that package is the v3 line and peers
// on `effect ^3.22.1`. Depending on both would put two Effect runtimes in one
// workspace.
//
// `unstable/` is upstream's own label for the surface. Absorbing its churn is
// this package's job: a rename upstream touches this file rather than every
// call site in the daemon and the renderer.

export const protocolVersion = 0;
