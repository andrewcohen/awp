import { describe, expect, test } from "vitest";
import { localBookmarks, parseBookmarks, parseWorkspaces } from "./jj-parse";

// Reading jj's answers, with no jj in sight.
//
// The fixtures below are real output, copied off `jj --ignore-working-copy
// workspace list -T 'json(self)'` in this repository on 2026-08-26. Copied
// rather than invented, because the point of asking jj for JSON is that the
// shape is jj's rather than one imagined here — a fixture written from memory
// would test the imagination.

const WORKSPACES = `
{"name":"awp-kit-amoeba","target":{"commit_id":"cc2b6e43bff84f356f59330368ae998cb88aad64","parents":["08650b24c0fbd44016de02a883c6ff74b4f0d786"],"change_id":"sompnokulttmtwtvuoqmtxkvsovuxmxz","description":"wip: next\\n"}}
{"name":"default","target":{"commit_id":"67b594a8857e716f3e2f4ca06aaee833bcbe233f","parents":["7b7150e917f9e53306f9ec619f6e84c57fbed004"],"change_id":"rlwqpwwwxkykspsxoszxzlnuuuwqqrqs","description":"wip: awp\\n"}}
`;

const BOOKMARKS = `
{"name":"andrew/awp-kit-amoeba","target":["36ede90af155f232f85b1e1deec399e447761e12"]}
{"name":"andrew/awp-kit-amoeba","remote":"git","target":["b112fe5d4e5c6314a990c9ef850ad2c3d43c7a84"],"tracking_target":["36ede90af155f232f85b1e1deec399e447761e12"]}
{"name":"main","target":["7b7150e917f9e53306f9ec619f6e84c57fbed004"]}
{"name":"main","remote":"origin","target":["7b7150e917f9e53306f9ec619f6e84c57fbed004"]}
`;

describe("workspaces", () => {
  test("a name and the commit it is sitting on", () => {
    const found = parseWorkspaces(WORKSPACES);

    expect(found.map((entry) => entry.name)).toEqual(["awp-kit-amoeba", "default"]);
    expect(found[0]?.commitId).toBe("cc2b6e43bff84f356f59330368ae998cb88aad64");
    expect(found[0]?.changeId).toBe("sompnokulttmtwtvuoqmtxkvsovuxmxz");
  });

  test("a description with a newline in it does not become two workspaces", () => {
    // The reason the read asks for JSON at all. jj's human output puts the
    // name, the change id and the description on one line, and a description
    // is arbitrary text a person wrote.
    expect(parseWorkspaces(WORKSPACES)).toHaveLength(2);
  });

  test("nothing at all is an empty list, not a failure", () => {
    expect(parseWorkspaces("")).toEqual([]);
    expect(parseWorkspaces("\n\n")).toEqual([]);
  });

  test("a line that will not parse is skipped, and the rest still read", () => {
    // jj adds keys to these objects between releases. A daemon that refused to
    // list workspaces because one line surprised it would be worse than one
    // that ignored the line.
    const found = parseWorkspaces(`not json\n${WORKSPACES.trim().split("\n")[0]}`);
    expect(found.map((entry) => entry.name)).toEqual(["awp-kit-amoeba"]);
  });

  test("a key jj adds later is ignored rather than fatal", () => {
    const found = parseWorkspaces('{"name":"a","target":{"commit_id":"x"},"something_new":42}');
    expect(found).toEqual([{ name: "a", commitId: "x", changeId: "" }]);
  });
});

describe("bookmarks", () => {
  test("a name can appear twice, once per remote", () => {
    const found = parseBookmarks(BOOKMARKS);

    // Four rows, two names. A caller asking "does this bookmark exist" against
    // the raw list would count `main` twice and, worse, would find a name that
    // only exists on a remote.
    expect(found).toHaveLength(4);
    expect(found.filter((entry) => entry.name === "main")).toHaveLength(2);
  });

  test("the local rows are the ones without a remote", () => {
    const local = localBookmarks(parseBookmarks(BOOKMARKS));

    expect(local.map((entry) => entry.name)).toEqual(["andrew/awp-kit-amoeba", "main"]);
    expect(local.every((entry) => entry.remote === undefined)).toBe(true);
  });

  test("target is the first commit, because a conflicted bookmark has several", () => {
    const found = parseBookmarks('{"name":"a","target":["one","two"]}');
    expect(found[0]?.target).toBe("one");
  });

  test("a deleted bookmark still listed has no target", () => {
    const found = parseBookmarks('{"name":"a","target":[]}');
    expect(found[0]?.target).toBeUndefined();
  });
});
