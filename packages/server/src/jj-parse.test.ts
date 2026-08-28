import { describe, expect, test } from "vitest";
import { localBookmarks, parseBookmarks, parseRevisions, parseWorkspaces } from "./jj-parse";

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

// ── revisions ──────────────────────────────────────────────────────────────
//
// Real output again, off `jj log --no-graph -T` with the revision template, in
// this repository on 2026-08-26. The second row is the one that matters: its
// description is nine paragraphs of prose containing quotes, blank lines and
// the two characters `\n` more times than anything else in this file — which
// is exactly what a tab-separated format has to survive.

const REVISIONS = [
  '{"commit_id":"34095b5fd649bbfa1069eeeceb38823b681465df","parents":["19f6c111e47ae12bfc67f4c05827bbe4e6b781ff"],"change_id":"wrvptklssostyswpqqzxxkmtpnmpptrv","description":"wip: a diff view in the accessory column\\n","author":{"name":"Andrew Cohen","email":"andrew@example.com","timestamp":"2026-08-26T16:06:44-04:00"},"committer":{"name":"Andrew Cohen","email":"andrew@example.com","timestamp":"2026-08-26T16:06:44-04:00"}}\ttrue\ttrue\t[{"name":"andrew/diff-view","target":["34095b5fd649bbfa1069eeeceb38823b681465df"]}]',
  '{"commit_id":"19f6c111e47ae12bfc67f4c05827bbe4e6b781ff","parents":["2479783ef2b364a6c3684c493ab71339b88e79e6"],"change_id":"mnlkwnzmqqnzmzyxmuxnllqomsnuktlo","description":"fix: a thread that is not filled says so\\n\\n\\"nothing yet\\" under a thread means it has claimed no workspace.\\n","author":{"name":"Andrew Cohen","email":"andrew@example.com","timestamp":"2026-08-26T13:40:01-04:00"},"committer":{"name":"Andrew Cohen","email":"andrew@example.com","timestamp":"2026-08-26T13:40:01-04:00"}}\tfalse\tfalse\t[]',
  "",
].join("\n");

describe("revisions", () => {
  test("the ids, the whole description, and the author", () => {
    const found = parseRevisions(REVISIONS);

    expect(found).toHaveLength(2);
    expect(found[0]?.changeId).toBe("wrvptklssostyswpqqzxxkmtpnmpptrv");
    expect(found[0]?.commitId).toBe("34095b5fd649bbfa1069eeeceb38823b681465df");
    expect(found[0]?.author).toBe("Andrew Cohen");
    // Whole, trailing newline included. Trimming belongs to whatever draws it,
    // because a row wants the first line and a header wants the rest.
    expect(found[0]?.description).toBe("wip: a diff view in the accessory column\n");
  });

  test("a description full of newlines and quotes stays one revision", () => {
    // The claim the tab separator rests on. A newline inside a description is
    // the two characters `\n` inside a JSON string, so it never reaches the
    // line splitting — and neither does a quote, or a tab.
    const found = parseRevisions(REVISIONS);

    expect(found[1]?.description).toContain('"nothing yet" under a thread');
    expect(found[1]?.description.split("\n")).toHaveLength(4);
  });

  test("the three things json(self) does not carry", () => {
    const found = parseRevisions(REVISIONS);

    // The entire reason this answer is not one JSON object per line.
    expect(found[0]?.empty).toBe(true);
    expect(found[0]?.workingCopy).toBe(true);
    expect(found[0]?.bookmarks).toEqual(["andrew/diff-view"]);
    expect(found[1]?.empty).toBe(false);
    expect(found[1]?.workingCopy).toBe(false);
    expect(found[1]?.bookmarks).toEqual([]);
  });

  test("a remote bookmark on the row is not the row's bookmark", () => {
    // A commit's `json(bookmarks)` is not only its local bookmarks, which is
    // easy to believe because most of the time it looks like it is. A remote
    // row appears exactly when the remote disagrees with local, on the commit
    // the *remote* points at, wearing the same name — read off this repository
    // where a bookmark had been exported and then moved:
    //
    //   local  andrew/lantern             → 7919e0e4
    //   git    andrew/lantern  remote:git → 8dc8f7ff   ← one commit behind
    //
    // Without the filter, that older commit claims the bookmark and two rows
    // in one list show the same name.
    const line = [
      JSON.stringify({ change_id: "abc", commit_id: "8dc8f7ff", description: "" }),
      "false",
      "false",
      JSON.stringify([
        { name: "andrew/lantern", remote: "git", target: ["8dc8f7ff"] },
        { name: "andrew/kept", target: ["8dc8f7ff"] },
      ]),
    ].join("\t");

    expect(parseRevisions(`${line}\n`)[0]?.bookmarks).toEqual(["andrew/kept"]);
  });

  test("a timestamp comes back as a Date", () => {
    const found = parseRevisions(REVISIONS);

    expect(found[0]?.authored).toBeInstanceOf(Date);
    expect(found[0]?.authored?.toISOString()).toBe("2026-08-26T20:06:44.000Z");
  });

  test("a row with fields missing is skipped, not fatal", () => {
    // jj adds to these objects between releases. A daemon that refused to list
    // anything because one row was unreadable would be worse than one that
    // dropped the row — and a row with no change id is a row nothing could be
    // asked about anyway.
    const found = parseRevisions(
      `not json at all\n{"commit_id":"abc"}\ttrue\ttrue\t[]\n${REVISIONS}`,
    );

    expect(found.map((entry) => entry.changeId)).toEqual([
      "wrvptklssostyswpqqzxxkmtpnmpptrv",
      "mnlkwnzmqqnzmzyxmuxnllqomsnuktlo",
    ]);
  });

  test("a row with no trailing fields reads as not empty and not the working copy", () => {
    // "Not stated" has to mean false. The alternative is a template that
    // stopped emitting a field quietly turning every commit into the working
    // copy, which is the kind of wrong that looks like a working list.
    const [only] = parseRevisions('{"change_id":"aaa","commit_id":"bbb"}');

    expect(only?.empty).toBe(false);
    expect(only?.workingCopy).toBe(false);
    expect(only?.authored).toBeUndefined();
    expect(only?.bookmarks).toEqual([]);
  });

  test("nothing at all is an empty list, not a failure", () => {
    expect(parseRevisions("")).toEqual([]);
  });
});
