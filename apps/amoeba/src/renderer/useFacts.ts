import type { WorkspaceFacts } from "@awp-kit/protocol";
import { useEffect, useState } from "react";
import { watchFacts } from "./daemon";

// What is known about each workspace, keyed the way the sidebar looks it up.
//
// A stream rather than a list, which is the split jobs and threads sit either
// side of: a thread changes when a person changes it in this window, so the
// reply to the change is the update — an agent goes from working to waiting on
// its own, and a window that only asked would miss it.
//
// The whole table arrives each push, so this replaces rather than merges. That
// is also what makes a reconnect free: `watchFacts` resubscribes and the next
// push is the truth, with nothing here to reconcile.

/** `project/workspace` — the key a row already has both halves of. */
export const factsKey = (project: string, workspace: string): string => `${project}/${workspace}`;

export type Facts = ReadonlyMap<string, WorkspaceFacts>;

const EMPTY: Facts = new Map();

export function useFacts(): Facts {
  const [facts, setFacts] = useState<Facts>(EMPTY);

  useEffect(
    () =>
      watchFacts((all) => {
        setFacts(new Map(all.map((one) => [factsKey(one.project, one.workspace), one])));
      }),
    [],
  );

  return facts;
}
