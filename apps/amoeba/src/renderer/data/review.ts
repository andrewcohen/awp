import { NoAgent, type CommentSide, type ReviewComment } from "@awp-kit/protocol";
import { useCallback, useEffect, useRef, useState } from "react";
import { addComment, listComments, removeComment, sendReview } from "./daemon";

// The comments on a workspace, and the three things that can be done to them.
//
// Kept apart from the panel that draws them, for one reason: a comment is about
// a *workspace* and the diff panel is about a *revision*. Those change at
// different moments — clicking a commit in the list changes the second and not
// the first — so a list reloaded per revision would be a round trip per click
// for an answer that did not change.
//
// ── what "unsent" counts ───────────────────────────────────────────────────
//
// Every draft on the workspace, not the ones visible in the patch on screen.
// The send button delivers exactly what the daemon finds unsent, so a count of
// what happens to be rendered would say "2" and deliver five. The button and
// the number beside it have to agree, and the daemon is the one that decides.

export interface Review {
  /** Every comment, draft and sent, oldest first. */
  readonly comments: ReadonlyArray<ReviewComment>;
  /** How many the send button would deliver. */
  readonly unsent: number;
  /** In flight, so the button can refuse a second press. */
  readonly sending: boolean;
  /** A sentence for a person, from the last thing that went wrong. */
  readonly failure: string | undefined;
  readonly add: (draft: {
    readonly revision: string;
    readonly path: string;
    readonly side: CommentSide;
    readonly line: number;
    readonly endLine: number;
    readonly body: string;
  }) => void;
  readonly remove: (id: string) => void;
  readonly send: () => void;
}

/** Nothing to say, and shared — a fresh `[]` per render is a memo that misses. */
const NONE: ReadonlyArray<ReviewComment> = [];

const said = (error: unknown): string => (error instanceof Error ? error.message : String(error));

export function useReview(project: string | undefined, workspace: string | undefined): Review {
  const [comments, setComments] = useState<ReadonlyArray<ReviewComment>>(NONE);
  const [sending, setSending] = useState(false);
  const [failure, setFailure] = useState<string | undefined>();

  // The workspace changed under the panel. Adjusted during render rather than
  // in an effect, the same way the diff resets its selected revision: the
  // previous workspace's comments drawn for one frame is the previous
  // workspace's comments on screen, anchored to lines that may not exist here.
  const [shownFor, setShownFor] = useState<string>();
  const key =
    project === undefined || workspace === undefined ? undefined : `${project} ${workspace}`;
  if (shownFor !== key) {
    setShownFor(key);
    setComments(NONE);
    setFailure(undefined);
  }

  // Which request the list on screen belongs to. The same reasoning as the
  // diff's: reloading happens from an effect and from all three actions, and a
  // reply arriving after a newer request must lose in every one of those alike.
  const newest = useRef(0);

  const load = useCallback((where: { readonly project: string; readonly workspace: string }) => {
    newest.current += 1;
    const mine = newest.current;
    listComments(where.project, where.workspace)
      .then((found) => {
        if (mine === newest.current) {
          setComments(found);
        }
      })
      .catch((error: unknown) => {
        if (mine === newest.current) {
          setFailure(said(error));
        }
      });
  }, []);

  useEffect(() => {
    if (project !== undefined && workspace !== undefined) {
      load({ project, workspace });
    }
  }, [project, workspace, load]);

  const at = project === undefined || workspace === undefined ? undefined : { project, workspace };

  return {
    comments,
    unsent: comments.filter((one) => one.sentAt === undefined).length,
    sending,
    failure,

    add: (draft) => {
      if (at === undefined) {
        return;
      }
      setFailure(undefined);
      // Reloaded rather than appended to what is held. The daemon assigns the
      // id and the timestamp and the panel's order is by timestamp, so a row
      // appended here would sit in the right place only until a second window
      // wrote one in the same second.
      addComment({ ...at, ...draft })
        .then(() => load(at))
        .catch((error: unknown) => setFailure(said(error)));
    },

    remove: (id) => {
      if (at === undefined) {
        return;
      }
      setFailure(undefined);
      removeComment(id)
        .then(() => load(at))
        .catch((error: unknown) => setFailure(said(error)));
    },

    send: () => {
      if (at === undefined || sending) {
        return;
      }
      setSending(true);
      setFailure(undefined);
      sendReview(at.project, at.workspace)
        .then(() => load(at))
        .catch((error: unknown) => {
          // `NoAgent` is the one worth its own sentence, because it is the only
          // failure here a person can do something about: there is no agent
          // running in this workspace, so start one. `instanceof` and not the
          // tag — the schema error is a class, and the class is the API.
          setFailure(
            error instanceof NoAgent ? "no agent session in this workspace to tell" : said(error),
          );
        })
        .finally(() => setSending(false));
    },
  };
}
