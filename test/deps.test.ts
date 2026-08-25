import { readdirSync } from "node:fs";
import { describe, expect, test } from "vitest";

// Effect v4 folds RPC into core as `effect/unstable/rpc`. There is no v4 line
// of `@effect/rpc` — it is the v3 series and peers on `effect ^3.22.1` — so a
// dependency that pulls it in also pulls a second Effect runtime into the
// workspace. Nothing about that failure looks like a version problem at the
// call site: two runtimes means two sets of Context tags, and a service
// provided through one is simply not found by the other.
//
// The invariant is only as strong as every future `bun add` remembering it,
// which is what this test is for.
//
// `node_modules/.bun` is bun's flat package store — every workspace's
// node_modules is symlinks into it — so one readdir sees the whole tree
// without walking it.

const store = readdirSync("node_modules/.bun");
const copiesOf = (name: string) => store.filter((entry) => entry.startsWith(`${name}@`));

describe("dependency tree", () => {
  test("exactly one copy of effect", () => {
    expect(copiesOf("effect")).toHaveLength(1);
  });

  test("effect is the v4 line", () => {
    expect(copiesOf("effect")[0]).toMatch(/^effect@4\./u);
  });

  test("no @effect/rpc — v4 serves rpc from core", () => {
    expect(copiesOf("@effect+rpc")).toHaveLength(0);
  });
});
