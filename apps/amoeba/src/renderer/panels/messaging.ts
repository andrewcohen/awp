import type { Message, ThreadMember } from "@awp-kit/protocol";

// How a message's two ends are named on screen.
//
// A thread's checkouts usually share a workspace name and differ only by
// project — adding a project to a thread takes the sibling's name on purpose,
// so the thread reads as one piece of work. Drawn as workspace names, every row
// of `Testing Multi` says `testing-multi → testing-multi`, which names nothing.
//
// The same shape of mistake the daemon's addressing made, one layer up, and the
// same answer: use whichever half tells the members apart.
//
//   workspaces all equal   the project      grove → redwood
//   projects all equal     the workspace    the-api → exports-ui
//   neither                the pair         grove/api → redwood/ui
//
// Decided per group rather than per row, so one thread's rows all read the same
// way. A rule applied per row would draw `grove → redwood` above
// `the-api → exports-ui` in one list and leave the reader working out which
// field each arrow is about.

/** Which half of a pair distinguishes the members of one group. */
export type Half = "project" | "workspace" | "pair";

/**
 * The half to draw, given every end that appears in a group.
 *
 * Takes the ends rather than the messages so the rule is stated over the thing
 * it is about, and so a caller with members in hand — rather than messages —
 * could use it too.
 */
export const halfFor = (ends: ReadonlyArray<ThreadMember>): Half => {
  if (ends.length === 0) {
    return "project";
  }
  const workspaces = new Set(ends.map((end) => end.workspace));
  const projects = new Set(ends.map((end) => end.project));
  if (workspaces.size === 1 && projects.size > 1) {
    return "project";
  }
  if (projects.size === 1 && workspaces.size > 1) {
    return "workspace";
  }
  // Both vary, or a group with one end in it. The pair is never ambiguous and
  // is what the inbox hands an agent back as the way to reply, so it is also
  // the form somebody reading this could paste.
  return workspaces.size === 1 && projects.size === 1 ? "project" : "pair";
};

/** One end, drawn as the chosen half. */
export const endLabel = (end: ThreadMember, half: Half): string => {
  if (half === "project") {
    return end.project;
  }
  return half === "workspace" ? end.workspace : `${end.project}/${end.workspace}`;
};

/** Every end a group's messages mention, senders and recipients alike. */
export const endsOf = (messages: ReadonlyArray<Message>): ReadonlyArray<ThreadMember> =>
  messages.flatMap((message) => [message.from, message.to]);
