import { useEffect, useState } from "react";
import { workspaceDir } from "./daemon";

/**
 * Where a workspace's checkout is, when no session is carrying the answer.
 *
 * A session already knows — `startDir` — so this is asked only for a workspace
 * with nothing running in it, which is the case the address used to refuse
 * outright. See `placeAt`.
 *
 * ── two things about the shape ──────────────────────────────────────────────
 *
 * **Cleared before the ask, not after.** Holding the previous workspace's
 * directory while the new one is in flight would point the diff panel at the
 * wrong checkout for a round trip — and a patch from somewhere else is worse
 * than no patch, because nothing about it says it is the wrong one.
 *
 * **Guarded by the pair it was asked for.** Two selections in quick succession
 * are two calls, and the replies may land in either order. The check is on the
 * key rather than a cancellation flag so a stale reply is dropped by the value
 * it is about rather than by when it arrived.
 */
export const useWorkspaceDir = (
  project: string | undefined,
  workspace: string | undefined,
  needed: boolean,
): string | undefined => {
  const [dirs, setDirs] = useState<Record<string, string>>({});
  const key =
    project === undefined || workspace === undefined ? undefined : `${project}/${workspace}`;

  useEffect(() => {
    if (!needed || key === undefined || project === undefined || workspace === undefined) {
      return;
    }
    // Answered already. The path is a pure function of the pair in the daemon,
    // so it cannot change under a window that is still open on it.
    if (dirs[key] !== undefined) {
      return;
    }
    let live = true;
    void workspaceDir(project, workspace)
      .then((found) => {
        if (live) {
          setDirs((prev) => ({ ...prev, [key]: found }));
        }
      })
      // Said nothing about, deliberately. The panels that use this each report
      // an unreadable directory in their own words, and a failure here is the
      // socket being down — which the window already says once, at the top.
      .catch(() => {});
    return () => {
      live = false;
    };
    // `dirs` is read to avoid re-asking and must not re-run this on its own
    // change, which would be a loop through the write above.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [needed, key, project, workspace]);

  return key === undefined ? undefined : dirs[key];
};
