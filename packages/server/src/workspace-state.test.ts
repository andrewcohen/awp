import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { NodeFileSystem } from "@effect/platform-node-shared";
import { Effect, Option, Stream } from "effect";
import { afterAll, describe, expect, test } from "vitest";
import { WorkspaceState, factsIn, layer } from "./workspace-state";

// Reading somebody else's file.
//
// Almost every decision here is about what to do with a shape this code does
// not control, so the tests are mostly about malformed, partial and unexpected
// input rather than about the happy path — which is one line.

const scratch = mkdtempSync(join(tmpdir(), "awp-wsstate-"));
afterAll(() => rmSync(scratch, { recursive: true, force: true }));

let files = 0;
const write = (contents: string): string => {
  const path = join(scratch, `state-${(files += 1)}.json`);
  writeFileSync(path, contents);
  return path;
};

const read = (path: string) =>
  Effect.runPromise(
    Effect.gen(function* () {
      const state = yield* WorkspaceState;
      return yield* state.read();
    }).pipe(Effect.provide(layer(path)), Effect.provide(NodeFileSystem.layer), Effect.scoped),
  );

describe("factsIn", () => {
  test("names the project after the repository directory", () => {
    // The same rule `identityLabels` and `Project` already follow, which is
    // what lets this join to a session's identity with no lookup table. A
    // different rule here would be a table that has to be kept in step.
    const [one] = factsIn(
      JSON.stringify({ "/Users/someone/code/thicket": { lantern: { Name: "lantern" } } }),
    );
    expect(one).toMatchObject({ project: "thicket", workspace: "lantern" });
  });

  test("reads the fields the sidebar draws", () => {
    const [one] = factsIn(
      JSON.stringify({
        "/code/thicket": {
          lantern: {
            DisplayName: "the lantern rewrite",
            Status: "working",
            Unread: true,
            PRNumber: 412,
            Bookmark: "andrew/lantern",
            ActivePrompt: "make the lamp light",
            LastActiveAt: "2026-08-20T15:39:22.924871-04:00",
            DevLoop: { Phase: "implement", Task: "wire the switch", Done: 3, Total: 7 },
          },
        },
      }),
    );
    expect(one).toMatchObject({
      displayName: "the lantern rewrite",
      status: "working",
      unread: true,
      pr: 412,
      bookmark: "andrew/lantern",
      prompt: "make the lamp light",
      phase: "implement",
      task: "wire the switch",
      done: 3,
      total: 7,
    });
    expect(one?.lastActiveAt?.toISOString()).toBe("2026-08-20T19:39:22.924Z");
  });

  test("an unknown status is dropped rather than passed through", () => {
    // A client switching on a closed union is what makes a hue per state
    // expressible at all. An unrecognised word would arrive as a row with no
    // dot and no explanation — worse than a row that says nothing on purpose.
    const [one] = factsIn(
      JSON.stringify({ "/code/thicket": { lantern: { Status: "pondering" } } }),
    );
    expect(one?.status).toBeUndefined();
  });

  test("keys this file does not know about are ignored", () => {
    // It belongs to another program and gains fields between releases. A
    // daemon that refused to show a sidebar over an unrecognised key would be
    // worse than one that ignored it.
    const [one] = factsIn(
      JSON.stringify({
        "/code/thicket": { lantern: { Status: "idle", PinGroup: "a", SomethingNew: { x: 1 } } },
      }),
    );
    expect(one).toMatchObject({ project: "thicket", status: "idle" });
  });

  test("a blank string is absent, not empty", () => {
    // `DisplayName: ""` is what an entry written before the field existed looks
    // like once something touched it, and a row falling back to its slug is
    // right where a row showing nothing is not.
    const [one] = factsIn(
      JSON.stringify({ "/code/thicket": { lantern: { DisplayName: "  ", Bookmark: "" } } }),
    );
    expect(one?.displayName).toBeUndefined();
    expect(one?.bookmark).toBeUndefined();
  });

  test("a pull request number of zero is no pull request", () => {
    // Go writes the zero value for an int it never set, so 0 is how "none"
    // reaches this — and a row reading `#0` is a link to nowhere.
    const [one] = factsIn(JSON.stringify({ "/code/thicket": { lantern: { PRNumber: 0 } } }));
    expect(one?.pr).toBeUndefined();
  });

  test("an unparseable date is nothing rather than an invalid one", () => {
    // `new Date("soon")` is an Invalid Date, which serialises to null and would
    // arrive at a client as a date it cannot render.
    const [one] = factsIn(
      JSON.stringify({ "/code/thicket": { lantern: { LastActiveAt: "soon" } } }),
    );
    expect(one?.lastActiveAt).toBeUndefined();
  });

  test("unread is a boolean either way", () => {
    // Absent means false, because what it drives is a dot and a dot is drawn or
    // it is not. Optional here would push that decision onto every client.
    const [one] = factsIn(JSON.stringify({ "/code/thicket": { lantern: {} } }));
    expect(one?.unread).toBe(false);
  });
});

describe("WorkspaceState.read", () => {
  test("a file that is not there is an empty table, not a failure", async () => {
    // What a machine that has only ever run amoeba looks like.
    expect(await read(join(scratch, "no-such-file.json"))).toEqual([]);
  });

  test("a half-written file is an empty table, not a failure", async () => {
    // Another program writes this, so a read landing mid-write is an ordinary
    // event. A sidebar that emptied itself over a JSON parse would be worse
    // than one a tick stale — and this is the same answer as the missing file
    // deliberately, because the difference is not this file's to report.
    expect(await read(write('{"/code/thicket": {"lantern": {"Status": "wo'))).toEqual([]);
  });

  test("reads a real file", async () => {
    const path = write(JSON.stringify({ "/code/thicket": { lantern: { Status: "waiting" } } }));
    expect(await read(path)).toMatchObject([{ project: "thicket", status: "waiting" }]);
  });
});

describe("WorkspaceState.changes", () => {
  test("says what it knows before anything has changed", async () => {
    // A stream that only spoke on change would leave a window blank until
    // somebody happened to run an agent, which for an idle machine is never.
    const path = write(JSON.stringify({ "/code/thicket": { lantern: { Status: "idle" } } }));
    const first = await Effect.runPromise(
      Effect.gen(function* () {
        const state = yield* WorkspaceState;
        return yield* Stream.runHead(state.changes());
      }).pipe(Effect.provide(layer(path)), Effect.provide(NodeFileSystem.layer), Effect.scoped),
    );
    expect(Option.getOrElse(first, () => [])).toMatchObject([{ status: "idle" }]);
  });

  test("a directory that is not there does not stop the first answer", async () => {
    // The watch is on the directory, and a path under one that does not exist
    // cannot be watched at all. The read still has to happen.
    const missing = join(scratch, "gone", "state.json");
    const first = await Effect.runPromise(
      Effect.gen(function* () {
        const state = yield* WorkspaceState;
        return yield* Stream.runHead(state.changes());
      }).pipe(Effect.provide(layer(missing)), Effect.provide(NodeFileSystem.layer), Effect.scoped),
    );
    expect(Option.getOrUndefined(first)).toEqual([]);
  });
});
