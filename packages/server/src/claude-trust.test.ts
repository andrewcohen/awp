import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Effect } from "effect";
import { afterAll, describe, expect, test } from "vitest";
import { trustWorkspace, untrustWorkspace } from "./claude-trust";

// Writing two booleans into somebody else's configuration file.
//
// Everything worth testing here is about *not* damaging that file, so the
// assertions are mostly about what survived rather than about what changed.

const scratch = mkdtempSync(join(tmpdir(), "awp-trust-"));
afterAll(() => rmSync(scratch, { recursive: true, force: true }));

let files = 0;
const configWith = (contents: unknown): string => {
  const path = join(scratch, `claude-${(files += 1)}.json`);
  writeFileSync(path, typeof contents === "string" ? contents : JSON.stringify(contents, null, 2));
  return path;
};

/**
 * The file as written, indexed freely.
 *
 * `any` because the whole point is that this round-trips a shape nothing here
 * declares — the file is Claude Code's and most of what a test looks at is a
 * key this code has never heard of. A type for it would be a second, wrong,
 * description of somebody else's format.
 */
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const read = (path: string): any => JSON.parse(readFileSync(path, "utf8"));

const trust = (dir: string, path: string) =>
  Effect.runPromise(trustWorkspace(dir, path).pipe(Effect.orDie));

const untrust = (dir: string, path: string) =>
  Effect.runPromise(untrustWorkspace(dir, path).pipe(Effect.orDie));

describe("trustWorkspace", () => {
  test("adds an entry that turns the prompt off", async () => {
    const path = configWith({ projects: {} });
    expect(await trust("/work/thicket", path)).toBe(true);

    const entry = read(path)["projects"]["/work/thicket"];
    expect(entry).toMatchObject({
      hasTrustDialogAccepted: true,
      hasCompletedProjectOnboarding: true,
    });
    // The empty collections come with it. An entry carrying only the two
    // booleans is a shape Claude Code did not write, and it reads the rest.
    expect(entry).toHaveProperty("mcpServers");
  });

  test("keeps every field it does not understand", async () => {
    // The whole reason this round-trips through a map rather than writing a
    // shape of its own: this file is a person's entire configuration, and most
    // of what is in it is nothing to do with trust.
    const path = configWith({
      numStartups: 412,
      oauthAccount: { emailAddress: "someone@example.com" },
      projects: { "/work/orchard": { history: ["a", "b"], allowedTools: ["Bash"] } },
    });
    await trust("/work/thicket", path);

    const after = read(path);
    expect(after["numStartups"]).toBe(412);
    expect(after["oauthAccount"]).toEqual({ emailAddress: "someone@example.com" });
    expect(after["projects"]["/work/orchard"]).toEqual({
      history: ["a", "b"],
      allowedTools: ["Bash"],
    });
  });

  test("leaves an existing entry's own settings alone", async () => {
    const path = configWith({
      projects: { "/work/thicket": { allowedTools: ["Bash", "Read"], history: ["x"] } },
    });
    await trust("/work/thicket", path);

    expect(read(path)["projects"]["/work/thicket"]).toMatchObject({
      allowedTools: ["Bash", "Read"],
      history: ["x"],
      hasTrustDialogAccepted: true,
    });
  });

  test("does nothing the second time, and says so", async () => {
    // The runner re-enters a step after a later one fails, so this has to be
    // safe twice — and answering `false` is what keeps the job log from saying
    // it trusted a workspace it had already trusted.
    const path = configWith({ projects: {} });
    expect(await trust("/work/thicket", path)).toBe(true);
    expect(await trust("/work/thicket", path)).toBe(false);
  });

  test("a relative path is resolved before it is written", async () => {
    // Claude Code keys on an absolute path. A relative one would be an entry
    // that never matches, which is a silent no-op rather than an error.
    const path = configWith({ projects: {} });
    await trust("./somewhere", path);
    expect(Object.keys(read(path)["projects"])[0]?.startsWith("/")).toBe(true);
  });

  test("no config file is not an error", async () => {
    // Absence means the person's agent is something else — codex, cursor,
    // aider — and littering their $HOME on their behalf is not this job's
    // business. It must also not fail the job that called it.
    expect(await trust("/work/thicket", join(scratch, "no-such-config.json"))).toBe(false);
  });

  test("a config with no projects map gains one", async () => {
    const path = configWith({ numStartups: 1 });
    expect(await trust("/work/thicket", path)).toBe(true);
    expect(read(path)["projects"]["/work/thicket"]).toHaveProperty("hasTrustDialogAccepted", true);
  });

  test("a malformed config is refused rather than overwritten", async () => {
    // The one case where doing nothing is much better than doing something: a
    // file that will not parse is still a file somebody may be able to
    // recover, and writing a fresh one over it is the only unrecoverable
    // outcome available here.
    const path = configWith("{ not json at all");
    const failed = await Effect.runPromise(trustWorkspace("/work/thicket", path).pipe(Effect.flip));
    expect(failed.reason).toContain("cannot update");
    expect(readFileSync(path, "utf8")).toBe("{ not json at all");
  });
});

describe("untrustWorkspace", () => {
  test("takes the entry away and leaves the rest", async () => {
    const path = configWith({ numStartups: 7, projects: { "/work/orchard": { history: [] } } });
    await trust("/work/thicket", path);
    expect(await untrust("/work/thicket", path)).toBe(true);

    const after = read(path);
    expect(after["projects"]).not.toHaveProperty("/work/thicket");
    expect(after["projects"]).toHaveProperty("/work/orchard");
    expect(after["numStartups"]).toBe(7);
  });

  test("an entry that is not there is already gone", async () => {
    // Undo is re-entered by the runner as well, so it is idempotent for the
    // same reason `run` is.
    const path = configWith({ projects: {} });
    expect(await untrust("/work/thicket", path)).toBe(false);
  });
});
