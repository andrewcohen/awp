import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Effect } from "effect";
import { afterAll, describe, expect, test } from "vitest";
import {
  type AwpSettings,
  DEFAULTS,
  SETTINGS_FILE,
  Settings,
  agentWith,
  layer,
  withFlag,
} from "./settings";

// What a person configured, read back.
//
// The fixture in "the real file" has the *shape* of a real config — the same
// keys, the same nesting — with the values changed. Shape copied rather than
// invented, because the point of reading an existing file is that its layout
// is the Go implementation's rather than one imagined here; values invented,
// because a test fixture is not the place for somebody's actual settings.

const scratch = mkdtempSync(join(tmpdir(), "awp-settings-"));
afterAll(() => rmSync(scratch, { recursive: true, force: true }));

let files = 0;
const withFile = (contents: string): string => {
  const path = join(scratch, `config-${(files += 1)}.json`);
  writeFileSync(path, contents);
  return path;
};

const read = (path: string) =>
  Effect.runPromise(
    Effect.gen(function* () {
      const settings = yield* Settings;
      return yield* settings.read();
    }).pipe(Effect.provide(layer(path))),
  );

describe("settings", () => {
  test("the real file, as it is on this machine", async () => {
    const found = await read(
      withFile(`{
        "agent": "claude --permission-mode auto --model opus",
        "actions": { "k9s": { "command": "k9s", "alias": "k" } },
        "deck": {
          "project_roots": ["~/p", "~/Documents"],
          "bookmark_prefix": "andrew"
        }
      }`),
    );

    expect(found.agent).toEqual(["claude", "--permission-mode", "auto", "--model", "opus"]);
    expect(found.bookmarkPrefix).toBe("andrew");
    expect(found.problem).toBeUndefined();
  });

  test("keys it does not read are ignored, not rejected", async () => {
    // `actions` and `project_roots` are in the file and have no consumer yet. A
    // reader that refused an unknown key would make the file unusable by the
    // implementation that wrote it.
    const found = await read(withFile('{"agent":"claude","future_key":42,"deck":{"x":1}}'));
    expect(found.agent).toEqual(["claude"]);
    expect(found.problem).toBeUndefined();
  });

  test("no file at all is the defaults, silently", async () => {
    // What a machine that has never run awp looks like. Not worth a sentence.
    const found = await read(join(scratch, "nothing-here.json"));
    expect(found).toEqual(DEFAULTS);
    expect(found.problem).toBeUndefined();
  });

  test("a malformed file is the defaults, and says so", async () => {
    // A typo must not stop a daemon holding a dozen ptys from starting, and
    // must not be silent either — `problem` is a field because the only useful
    // place for it is in front of the person who made the typo.
    const found = await read(withFile("{ this is not json"));

    expect(found.agent).toEqual(DEFAULTS.agent);
    expect(found.problem).toBeDefined();
  });

  test("no bookmark prefix means no bookmark, not an unprefixed one", async () => {
    // A bare workspace name in a shared repository's bookmark list is a name
    // nobody can attribute. Creating none is better than creating that.
    const found = await read(withFile('{"agent":"claude"}'));
    expect(found.bookmarkPrefix).toBeUndefined();
  });

  test("an empty prefix is the same as none", async () => {
    const found = await read(withFile('{"deck":{"bookmark_prefix":"   "}}'));
    expect(found.bookmarkPrefix).toBeUndefined();
  });

  test("the file lives where the Go implementation put it", () => {
    // Read rather than replaced. A rewrite that ignored the existing config
    // would silently change which agent launches, and that would not look like
    // a settings problem.
    expect(SETTINGS_FILE).toMatch(/\/\.config\/awp\/config\.json$/u);
  });
});

// ── the modal's overrides ──────────────────────────────────────────────────
//
// The property under test is *replacement*, not appending. Every case below is
// a configured agent that already says something the modal also says.

const config = (agent: ReadonlyArray<string>): AwpSettings => ({ ...DEFAULTS, agent });

describe("agentWith", () => {
  test("chooses nothing, and the config is untouched", () => {
    const argv = ["claude", "--permission-mode", "auto", "--model", "opus"];
    expect(agentWith(config(argv), {})).toEqual(argv);
    expect(agentWith(config(argv), { model: undefined, effort: undefined })).toEqual(argv);
  });

  test("replaces a model the config already chose, rather than following it", () => {
    expect(
      agentWith(config(["claude", "--permission-mode", "auto", "--model", "opus"]), {
        model: "sonnet",
      }),
    ).toEqual(["claude", "--permission-mode", "auto", "--model", "sonnet"]);
  });

  test("replaces the joined spelling in place", () => {
    expect(agentWith(config(["claude", "--model=opus"]), { model: "haiku" })).toEqual([
      "claude",
      "--model=haiku",
    ]);
  });

  test("appends a flag the config does not have", () => {
    expect(agentWith(config(["claude", "--model", "opus"]), { effort: "high" })).toEqual([
      "claude",
      "--model",
      "opus",
      "--effort",
      "high",
    ]);
  });

  test("both at once", () => {
    expect(
      agentWith(config(["claude", "--model", "opus", "--effort", "low"]), {
        model: "sonnet",
        effort: "max",
      }),
    ).toEqual(["claude", "--model", "sonnet", "--effort", "max"]);
  });

  // A config that names a flag and leaves its value out. Overwriting the next
  // element would silently drop `--verbose`, which is a different command than
  // the one anybody asked for.
  test("inserts rather than overwriting when the flag has no value", () => {
    expect(withFlag(["claude", "--model", "--verbose"], "--model", "opus")).toEqual([
      "claude",
      "--model",
      "opus",
      "--verbose",
    ]);
    expect(withFlag(["claude", "--model"], "--model", "opus")).toEqual([
      "claude",
      "--model",
      "opus",
    ]);
  });

  // Not reachable through `parse`, which always yields at least `claude`. Here
  // because the alternative is producing an argv whose first element is a flag
  // — a command that cannot run, from a function that cannot say so.
  test("an empty argv gains nothing", () => {
    expect(withFlag([], "--model", "opus")).toEqual([]);
  });
});
