import { type SessionInfo, shellNumber } from "@awp-kit/protocol";

// Which of a machine's sessions are one workspace's shells.
//
// Its own module because it is the panel's one piece of arithmetic and the
// panel is a terminal — nothing about `Shell.tsx` can be checked without a
// canvas, and this can be checked without anything. Named `shells.ts` and not
// `shell.ts`: this filesystem is case-insensitive, so a `shell.ts` beside a
// `Shell.tsx` is one module and the import resolves to whichever it feels like.

/**
 * The shells of one workspace, in number order.
 *
 * Read off `identity` rather than off the name: a name is shortened and a dot
 * in a real project name comes back as an underscore, so the labels are the
 * only unshortened truth about which workspace a session belongs to.
 *
 * **Ended shells are left out.** zmx keeps a session listed after its command
 * exits so the output can still be read, and a tab that attaches to a dead
 * shell draws its last screen and takes no keys — which reads as the pane being
 * broken. The number it had is not reused until `+` is pressed, which is the
 * daemon's business rather than this strip's.
 */
export const shellsOf = (
  sessions: ReadonlyArray<SessionInfo>,
  project: string | undefined,
  workspace: string | undefined,
): ReadonlyArray<{ readonly session: SessionInfo; readonly n: number }> => {
  if (project === undefined || workspace === undefined) {
    return [];
  }
  return sessions
    .flatMap((session) => {
      const identity = session.identity;
      if (
        identity === undefined ||
        identity.project !== project ||
        identity.workspace !== workspace ||
        session.ended
      ) {
        return [];
      }
      const n = shellNumber(identity.kind);
      return n === undefined ? [] : [{ session, n }];
    })
    .toSorted((a, b) => a.n - b.n);
};
