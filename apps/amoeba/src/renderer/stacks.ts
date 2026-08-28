import type { InboxItem } from "@awp-kit/protocol";

// Drawing a stack of pull requests as a tree.
//
// Its own file, and not a helper inside the panel, for the reason `columns.ts`
// and `workspaces.ts` are: it is a pure function over a list, every interesting
// case is a shape of list, and a test can pin all of them without rendering
// anything. A guide drawn wrongly is also invisible in a screenshot of a stack
// of two, which is most stacks.

/**
 * The tree guide for one row of a stack — `├─`, `└─`, and the `│` above them.
 *
 * ── why this is drawn from the list and not from the row ──────────────────
 *
 * A guide character is a statement about what comes *after* a row: `└─` means
 * "nothing else hangs off my parent below me", and it can only be known by
 * looking down the list. So this takes the ordered rows and an index, which is
 * also why it lives here rather than on the wire — the daemon would have to send
 * a picture it cannot see the end of.
 *
 * The rows arrive in pre-order within a stack: a root, then its descendants,
 * depth increasing. That is the daemon's ordering guarantee — see `inboxItems` —
 * and this is the standard reading of such a list:
 *
 *   for each level above mine   `│ ` if the level continues below me, else two
 *                               spaces. That is what makes a deep branch's
 *                               siblings line up under the right ancestor
 *   at my own level             `└─` if no later row in this stack sits at my
 *                               depth or shallower, `├─` otherwise
 *
 *   #10 base
 *    ├─ #20
 *    │  └─ #25
 *    └─ #30
 *
 * A root draws nothing: it is the trunk, and a guide in front of it would point
 * at a parent that is not on screen. A lone pull request has no `stack` at all —
 * see the field — so it never reaches this.
 */
export const guide = (rows: ReadonlyArray<InboxItem>, index: number): string => {
  const row = rows[index];
  if (row?.stack === undefined || row.depth === 0) {
    return "";
  }
  /** Whether any later row of this same stack sits at exactly `depth`. */
  const continues = (depth: number): boolean => {
    for (let at = index + 1; at < rows.length; at += 1) {
      const later = rows[at];
      // The stack's rows are contiguous, so the first row belonging to another
      // stack ends the search — anything past it is a different tree.
      if (later === undefined || later.stack !== row.stack) {
        return false;
      }
      if (later.depth < depth) {
        return false;
      }
      if (later.depth === depth) {
        return true;
      }
    }
    return false;
  };

  let drawn = "";
  for (let level = 1; level < row.depth; level += 1) {
    drawn += continues(level) ? "│  " : "   ";
  }
  return `${drawn}${continues(row.depth) ? "├─ " : "└─ "}`;
};
