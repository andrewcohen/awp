import { describe, expect, it } from "vitest";
import { interesting } from "./watch";

// The ignore list, which is the whole of this file's behaviour that can be
// wrong. The stream around it is a debounce and a clock read; what decides
// whether the feature works at all is which paths are dropped.

describe("what is worth telling a client about", () => {
  it("reports an ordinary file", () => {
    expect(interesting("/repo/src/index.ts")).toBe(true);
  });

  it("drops anything inside .jj, which is the loop this exists to prevent", () => {
    // Asking for the working copy snapshots it, and a snapshot writes here. A
    // watcher that reported this would report the consequence of its own last
    // report, for ever — so this assertion is about a loop, not about noise.
    expect(interesting("/repo/.jj/repo/op_store/operations/abc")).toBe(false);
    expect(interesting("/repo/.jj/working_copy/tree_state")).toBe(false);
  });

  it("drops .git for the same reason", () => {
    expect(interesting("/repo/.git/index")).toBe(false);
  });

  it("drops the directories an install or a build churns", () => {
    expect(interesting("/repo/node_modules/left-pad/index.js")).toBe(false);
    expect(interesting("/repo/apps/amoeba/dist/renderer/main.js")).toBe(false);
    expect(interesting("/repo/.tsbuild/tsconfig.tsbuildinfo")).toBe(false);
  });

  it("matches a segment, not a prefix", () => {
    // A file whose name merely starts with an ignored one is an ordinary file.
    // Substring matching here would silently drop real work — a package called
    // `distances`, a file called `.jjconfig` a person is editing.
    expect(interesting("/repo/src/distances.ts")).toBe(true);
    expect(interesting("/repo/.jjconfig.toml")).toBe(true);
    expect(interesting("/repo/node_modules_notes.md")).toBe(true);
  });

  it("drops an ignored directory wherever it sits", () => {
    // Not only at the root. A workspace holds packages, and each of them has
    // its own build output.
    expect(interesting("/repo/packages/store/dist/index.js")).toBe(false);
  });
});
