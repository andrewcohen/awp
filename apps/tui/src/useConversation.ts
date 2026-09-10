// The conversation as React state.
//
// The fold is in `conversation.ts` and stays there: it is a reducer over
// updates and has no idea a screen exists. This is the seam — a subscription
// that outlives a render, and a state update that does not re-subscribe.

import { useEffect, useRef, useState } from "react";
import type { ChatUpdate } from "@awp-kit/protocol";
import { type Conversation, empty, fold, mine } from "./conversation";
import { watchChat } from "./daemon";

export type Chat = {
  readonly state: Conversation;
  /**
   * Add what this client just typed, before anything has echoed it back.
   *
   * The key is the sender's own name for the message — see `ChatSend.key`. It
   * is what makes the daemon's echo, which exists so a *second* client sees
   * what was typed here, a no-op in the client that drew it already.
   */
  readonly saidLocally: (text: string, key: string) => void;
};

/**
 * The conversation for one checkout.
 *
 * There is no reset in here for a change of workspace, deliberately: `App`
 * gives `Chat` a key, so opening another checkout mounts another component and
 * the state starts empty because it is new state. Clearing it in an effect
 * instead is a render, then a second render — and the lint says so.
 */
export const useConversation = (project: string, workspace: string): Chat => {
  const [state, setState] = useState<Conversation>(empty);
  // Batched by frame rather than applied per update: a turn arrives as hundreds
  // of chunks, and a setState per chunk is a render per chunk.
  const pending = useRef<ChatUpdate[]>([]);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);

  useEffect(() => {
    const flush = () => {
      timer.current = undefined;
      const batch = pending.current;
      pending.current = [];
      if (batch.length > 0) setState((was) => batch.reduce((all, one) => fold(all, one), was));
    };
    const stop = watchChat(
      project,
      workspace,
      (update) => {
        pending.current.push(update);
        timer.current ??= setTimeout(flush, 50);
      },
      // A resubscription replays from the start, so what is held has to go
      // first — and so does anything batched but not yet applied, which
      // belongs to the conversation being replaced.
      () => {
        pending.current = [];
        setState(empty);
      },
    );
    return () => {
      stop();
      if (timer.current !== undefined) clearTimeout(timer.current);
      pending.current = [];
    };
  }, [project, workspace]);

  return {
    state,
    saidLocally: (text: string, key: string) => setState((was) => mine(was, text, key)),
  };
};

/** A turning mark, and only while there is something to say. */
export const useSpinner = (spinning: boolean): number => {
  const [tick, setTick] = useState(0);
  useEffect(() => {
    if (!spinning) return;
    const timer = setInterval(() => setTick((was) => was + 1), 100);
    return () => clearInterval(timer);
  }, [spinning]);
  return tick;
};
