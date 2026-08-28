import type { InboxItem } from "@awp-kit/protocol";
import { describe, expect, it } from "vitest";
import { guide } from "./stacks";

// Every case is a shape of list, which is why this is a pure function: a guide
// drawn wrongly is invisible in a screenshot of a stack of two, and a stack of
// two is most stacks.

/** Only the four fields the guides read. */
const row = (number: number, depth: number, stack: string | undefined): InboxItem =>
  ({ number, depth, stack, project: "thicket" }) as InboxItem;

/** The whole list, drawn — which is the only way to read a tree. */
const drawn = (rows: ReadonlyArray<InboxItem>): ReadonlyArray<string> =>
  rows.map((one, at) => `${guide(rows, at)}#${one.number}`);

describe("drawing a stack as a tree", () => {
  it("a pull request that stands alone gets no guide", () => {
    // `stack` is absent for one, which is what decides that no tree is drawn —
    // a `└─` in front of every unstacked row is noise.
    expect(drawn([row(1, 0, undefined)])).toEqual(["#1"]);
  });

  it("a root draws nothing, and its one child closes the tree", () => {
    // The root is the trunk: a guide in front of it would point at a parent
    // that is not on screen.
    expect(drawn([row(10, 0, "base"), row(20, 1, "base")])).toEqual(["#10", "└─ #20"]);
  });

  it("siblings continue with ├─ and the last one closes with └─", () => {
    expect(drawn([row(10, 0, "base"), row(20, 1, "base"), row(30, 1, "base")])).toEqual([
      "#10",
      "├─ #20",
      "└─ #30",
    ]);
  });

  it("a level that continues below draws │ through the rows between", () => {
    // The case the `│` exists for: without it #25 reads as a child of nothing
    // and #30 looks like it belongs to #25.
    expect(
      drawn([row(10, 0, "base"), row(20, 1, "base"), row(25, 2, "base"), row(30, 1, "base")]),
    ).toEqual(["#10", "├─ #20", "│  └─ #25", "└─ #30"]);
  });

  it("the last branch's descendants draw spaces, not a trailing │", () => {
    expect(
      drawn([row(10, 0, "base"), row(20, 1, "base"), row(30, 1, "base"), row(35, 2, "base")]),
    ).toEqual(["#10", "├─ #20", "└─ #30", "   └─ #35"]);
  });

  it("the next stack ends the search rather than joining it", () => {
    // Rows of one stack are contiguous, and the first row of another is the
    // boundary. Without that check #20 would look for siblings in the stack
    // below it and draw ├─ for a tree it is not in.
    expect(
      drawn([row(10, 0, "base"), row(20, 1, "base"), row(40, 0, "other"), row(50, 1, "other")]),
    ).toEqual(["#10", "└─ #20", "#40", "└─ #50"]);
  });
});
