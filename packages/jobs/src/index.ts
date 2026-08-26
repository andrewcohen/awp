// Jobs: work that outlives whoever asked for it.
//
// A job is a named kind with ordered steps. Two promises make the whole thing
// work, and both are the step author's to keep:
//
//   run   must be safe to call twice — a retry re-enters the step that failed,
//         which may have got halfway
//   undo  optional, and must be safe against a `run` that only got halfway,
//         because that is exactly when it is called
//
// Given those, the runner can resume a retry from the last completed step and
// still wind the whole thing back when the attempts run out. See `runner.ts`
// for how those two are kept from contradicting each other.
//
// The store is a tag with two implementations: an in-memory one, which every
// test in this package runs against, and a sqlite one for the daemon. The
// second is what makes a job survive a restart — and surviving a restart is
// only useful because the completed steps are on the record, so a job resumes
// where it stopped rather than starting again.

export const jobsVersion = 0;

export * from "./job";
export * from "./kind";
export * from "./runner";
export * from "./sqlite";
export * from "./store";
