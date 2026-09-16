import { describe, expect, test } from "vitest";
import { type FileContent, contentOf, statOf, subjectOf, summarise } from "./patch";

// Counting a patch without parsing one.
//
// The fixture is real `jj diff --git` output, trimmed. It has to be real: the
// whole risk in this file is a prefix that looks like a changed line and is
// not, and those are exactly the lines an invented fixture leaves out.

const PATCH = `diff --git a/apps/amoeba/src/renderer/Accessory.tsx b/apps/amoeba/src/renderer/Accessory.tsx
index 5cc19176d3..63d402ef74 100644
--- a/apps/amoeba/src/renderer/Accessory.tsx
+++ b/apps/amoeba/src/renderer/Accessory.tsx
@@ -1,4 +1,4 @@
-import { Tabs } from "@base-ui/react/tabs";
+import { Tabs } from "@base-ui/react/tabs";
+import { Diff } from "./Diff";
 import { Jobs } from "./Jobs";
diff --git a/apps/amoeba/src/renderer/patch.ts b/apps/amoeba/src/renderer/patch.ts
new file mode 100644
index 0000000000..1111111111
--- /dev/null
+++ b/apps/amoeba/src/renderer/patch.ts
@@ -0,0 +1,2 @@
+export const statOf = () => 0;
+
`;

describe("counting a patch", () => {
  test("two files, and the changed lines within them", () => {
    expect(statOf(PATCH)).toEqual({ files: 2, added: 4, removed: 1 });
  });

  test("the file headers are not changed lines", () => {
    // The entire reason this is not a filter on startsWith("+"). `+++ b/x`
    // and `--- a/x` appear once per file and begin with the same characters as
    // the lines they introduce — four of them here, which is most of the
    // difference between the right answer and a plausible one.
    const headers = PATCH.split("\n").filter(
      (line) => line.startsWith("+++ ") || line.startsWith("--- "),
    );

    expect(headers).toHaveLength(4);
    expect(statOf(PATCH).added).toBe(PATCH.split("\n").filter((l) => l.startsWith("+")).length - 2);
  });

  test("`/dev/null` on the old side is still a file header", () => {
    // An added file writes `--- /dev/null`, which is not a deletion. A version
    // that matched on `--- a/` rather than on `--- ` would count it as one.
    expect(statOf("--- /dev/null\n+++ b/x\n+new\n")).toEqual({
      files: 0,
      added: 1,
      removed: 0,
    });
  });

  test("an empty patch counts nothing", () => {
    expect(statOf("")).toEqual({ files: 0, added: 0, removed: 0 });
  });
});

describe("saying it out loud", () => {
  test("the files, then the two signs", () => {
    expect(summarise(statOf(PATCH))).toBe("2 files · +4 −1");
  });

  test("one file is singular", () => {
    expect(summarise({ files: 1, added: 3, removed: 0 })).toBe("1 file · +3 −0");
  });

  test("nothing changed has nothing to say", () => {
    // Not "0 files · +0 −0". A header of zeroes has to be read before it can
    // be dismissed, and the panel already says so in words.
    expect(summarise({ files: 0, added: 0, removed: 0 })).toBeUndefined();
  });
});

describe("a commit's subject", () => {
  test("the first line, and only the first", () => {
    expect(subjectOf("feat: a diff view\n\nA body nobody asked for.\n")).toBe("feat: a diff view");
  });

  test("a commit with no message says so in jj's words", () => {
    expect(subjectOf("")).toBe("(no description set)");
    expect(subjectOf("\n\n")).toBe("(no description set)");
  });
});

// ── the version a file is rendered under ──────────────────────────────────
//
// The bug these are written against threw inside the diff renderer and took the
// panel out through its boundary:
//
//   DiffHunksRenderer.processDiffResult: deletionLine and additionLine are
//   null, something is wrong
//
// The cause was on this side. The renderer caches per item, keyed on the item's
// id and version; the id is the path, which is deliberately unchanged when the
// file changes; and the version carried the fold, the viewed mark and the
// annotations but nothing about the content. So a changed file reused an AST
// highlighted for the old content and was indexed with new hunks.

const file = (over: Partial<FileContent> = {}): FileContent => ({
  type: "modified",
  isPartial: true,
  hunks: [{}],
  unifiedLineCount: 3,
  splitLineCount: 3,
  deletionLines: ["one", "two"],
  additionLines: ["one", "three"],
  ...over,
});

describe("the version a file is rendered under", () => {
  test("moves when a line changes", () => {
    // The whole point. Everything else about the item can stand still — same
    // path, same fold, same comments — and this still has to differ, or the
    // renderer keeps the AST it highlighted last time.
    expect(contentOf(file())).not.toBe(contentOf(file({ additionLines: ["one", "four"] })));
  });

  test("moves when a line is added or removed", () => {
    expect(contentOf(file())).not.toBe(contentOf(file({ additionLines: ["one", "three", "x"] })));
    expect(contentOf(file())).not.toBe(contentOf(file({ deletionLines: ["one"] })));
  });

  test("moves when the shape changes but the lines do not", () => {
    // Hydrating collapsed context reshapes the hunks. The counts are in the
    // hash for this case alone, and it is the one the lines cannot catch.
    expect(contentOf(file())).not.toBe(contentOf(file({ hunks: [{}, {}] })));
    expect(contentOf(file())).not.toBe(contentOf(file({ isPartial: false })));
    expect(contentOf(file())).not.toBe(contentOf(file({ unifiedLineCount: 4 })));
  });

  test("does not move for the same file", () => {
    // The other half, and it is what the cache is for: a re-render of an
    // unchanged file must not re-highlight it. A version that changed every
    // time would hide the bug above and cost every file on every keystroke.
    expect(contentOf(file())).toBe(contentOf(file()));
  });

  test("does not confuse a line boundary with content", () => {
    // Joined with a newline rather than concatenated, so two files that differ
    // only in where the lines are cut apart do not hash alike.
    expect(contentOf(file({ additionLines: ["ab", "c"] }))).not.toBe(
      contentOf(file({ additionLines: ["a", "bc"] })),
    );
  });
});
