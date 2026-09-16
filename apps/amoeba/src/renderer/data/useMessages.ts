import type { Message } from "@awp-kit/protocol";
import { useEffect, useRef, useState } from "react";
import { listMessages, onReconnect, said, watchMessages } from "./daemon";

// The messages: asked for once, and then watched.
//
// `useThreads`' shape, and for a stronger version of its reason. A thread at
// least changes when somebody changes it in this window; a message never does.
// All three writes belong to somebody else — an agent's `awp_message`, the
// daemon's nudge, the recipient's fetch — so there is no reply here that could
// ever be the update, and a viewer without a feed would be a viewer that is
// right only about a list it did not cause.

export interface Messages {
  readonly messages: ReadonlyArray<Message>;
  /** Absent while it is working, which is not the same as "none". */
  readonly failure: string | undefined;
}

/**
 * Ask the daemon, and report back through the setters.
 *
 * Outside the component for the reason `useThreads` states: inside it is either
 * a fresh function each render, which `exhaustive-deps` refuses as a
 * dependency, or a `useCallback`, which react-doctor refuses as manual
 * memoization.
 */
const load = (
  alive: { readonly current: boolean },
  setMessages: (found: ReadonlyArray<Message>) => void,
  setFailure: (reason: string | undefined) => void,
): void => {
  listMessages()
    .then((found) => {
      if (alive.current) {
        setMessages(found);
        setFailure(undefined);
      }
    })
    .catch((error: unknown) => {
      if (alive.current) {
        setFailure(said(error));
      }
    });
};

export function useMessages(): Messages {
  const [messages, setMessages] = useState<ReadonlyArray<Message>>([]);
  const [failure, setFailure] = useState<string | undefined>();
  const alive = useRef(true);

  useEffect(() => {
    alive.current = true;
    load(alive, setMessages, setFailure);
    // A list is an answer and the stream carries changes from now, so both are
    // needed and the question is asked again on every reconnect.
    const stop = onReconnect(() => load(alive, setMessages, setFailure));
    const watching = watchMessages((found) => {
      if (alive.current) {
        setMessages(found);
        setFailure(undefined);
      }
    });
    return () => {
      alive.current = false;
      stop();
      watching();
    };
  }, []);

  return { messages, failure };
}
