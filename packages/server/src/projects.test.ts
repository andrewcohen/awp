import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { NodeFileSystem, NodePath } from "@effect/platform-node-shared";
import { layer as dbLayer } from "@awp-kit/store";
import { Effect, Layer } from "effect";
import { afterAll, describe, expect, test } from "vitest";
import {
  ProjectNameTaken,
  Projects,
  discover,
  expand,
  layer,
  migrations,
  nearestRepo,
} from "./projects";

// A real file and a real directory tree, for the two things worth proving:
// that an import survives a restart, and that the walk stops where it should.
// Neither is a claim a fake filesystem could make.

const scratch = mkdtempSync(join(tmpdir(), "awp-projects-"));
afterAll(() => rmSync(scratch, { recursive: true, force: true }));

let files = 0;
const file = (): string => join(scratch, `projects-${(files += 1)}.sqlite`);

const at = (path: string) => layer.pipe(Layer.provide(dbLayer(path, migrations)));

const on = <A>(
  path: string,
  program: (projects: Projects["Service"]) => Effect.Effect<A, unknown>,
): Promise<A> =>
  Effect.gen(function* () {
    const projects = yield* Projects;
    return yield* program(projects);
  }).pipe(Effect.provide(at(path)), Effect.scoped, Effect.orDie, Effect.runPromise);

describe("expand", () => {
  test("replaces a leading tilde and nothing else", () => {
    expect(expand("~/code", "/Users/someone")).toBe("/Users/someone/code");
    expect(expand("/srv/code", "/Users/someone")).toBe("/srv/code");
    // Not a home reference: a tilde in the middle is part of the name.
    expect(expand("/srv/~backup", "/Users/someone")).toBe("/srv/~backup");
  });
});

describe("Projects", () => {
  test("records one and reads it back", async () => {
    const path = file();
    const made = await on(path, (projects) => projects.record("thicket", "/code/thicket"));
    expect(made.name).toBe("thicket");
    expect(made.root).toBe("/code/thicket");
    expect(made.importedAt).toBeInstanceOf(Date);

    // A second connection over the same file, which is the whole reason this
    // is a table rather than a value the daemon holds.
    const again = await on(path, (projects) => projects.list());
    expect(again.map((one) => one.name)).toEqual(["thicket"]);
  });

  test("importing the same repository twice changes nothing", async () => {
    const path = file();
    const first = await on(path, (projects) => projects.record("thicket", "/code/thicket"));
    const second = await on(path, (projects) => projects.record("thicket", "/code/thicket"));
    // The row that was already there, not a new one — an insert would have
    // failed on the primary key, and the point is that it does not get that
    // far. Clicking a name in a picker twice must not be an error.
    expect(second.importedAt?.getTime()).toBe(first.importedAt?.getTime());
    expect(await on(path, (projects) => projects.list())).toHaveLength(1);
  });

  test("a second repository with the same basename is refused, by name", async () => {
    const path = file();
    await on(path, (projects) => projects.record("thicket", "/work/thicket"));
    const failed = await Effect.gen(function* () {
      const projects = yield* Projects;
      return yield* projects.record("thicket", "/play/thicket").pipe(Effect.flip);
    }).pipe(Effect.provide(at(path)), Effect.scoped, Effect.orDie, Effect.runPromise);

    expect(failed).toBeInstanceOf(ProjectNameTaken);
    // The root already holding the name is on the error, because the sentence
    // a person needs is "which one" — being told the name is taken without
    // being told by what leaves them nothing to do.
    expect(failed).toMatchObject({ name: "thicket", held: "/work/thicket" });
    expect(await on(path, (projects) => projects.list())).toHaveLength(1);
  });

  test("forget says whether there was anything to forget", async () => {
    const path = file();
    await on(path, (projects) => projects.record("thicket", "/code/thicket"));
    expect(await on(path, (projects) => projects.forget("thicket"))).toBe(true);
    expect(await on(path, (projects) => projects.forget("thicket"))).toBe(false);
    expect(await on(path, (projects) => projects.list())).toEqual([]);
  });
});

// ── the walk ───────────────────────────────────────────────────────────────

/** A directory, and a `.jj` inside it if it is meant to be a repository. */
const tree = (spec: Record<string, boolean>): string => {
  const root = join(scratch, `tree-${(files += 1)}`);
  for (const [path, repo] of Object.entries(spec)) {
    const full = join(root, path);
    mkdirSync(full, { recursive: true });
    if (repo) {
      mkdirSync(join(full, ".jj"), { recursive: true });
    }
  }
  return root;
};

const found = (roots: ReadonlyArray<string>) =>
  Effect.runPromise(
    discover(roots).pipe(
      Effect.provide(Layer.mergeAll(NodeFileSystem.layer, NodePath.layer)),
      Effect.scoped,
    ),
  );

describe("discover", () => {
  test("finds repositories under a root and names them by basename", async () => {
    const root = tree({ thicket: true, orchard: true, notes: false });
    const under = await found([root]);
    expect(under.map((one) => one.name).toSorted()).toEqual(["orchard", "thicket"]);
    // Found, not claimed. The window needs to tell the two apart to know which
    // name an import button applies to.
    expect(under.every((one) => one.importedAt === undefined)).toBe(true);
  });

  test("stops at a repository rather than descending through it", async () => {
    // A repository with a checkout inside it is the ordinary shape here —
    // `~/.awp/workspaces/<project>/<workspace>` — and offering the inner one
    // as a project of its own would offer a checkout as if it were the work.
    const root = tree({ "thicket/vendor/orchard": true, thicket: true });
    expect((await found([root])).map((one) => one.name)).toEqual(["thicket"]);
  });

  test("gives up after four levels", async () => {
    const root = tree({ "a/b/c/d/deep": true, "a/b/shallow": true });
    const names = (await found([root])).map((one) => one.name);
    expect(names).toContain("shallow");
    expect(names).not.toContain("deep");
  });

  test("names a repository once when two roots overlap", async () => {
    // `~/p` and `~/p/work` is an ordinary way to write that config, and the
    // same repository arriving twice would be two rows for one directory.
    const root = tree({ "work/thicket": true });
    const under = await found([root, join(root, "work")]);
    expect(under).toHaveLength(1);
    expect(under[0]?.root).toBe(join(root, "work", "thicket"));
  });

  test("a root that is not there is skipped, not a failure", async () => {
    const root = tree({ thicket: true });
    // A picker that refused to open because one line of somebody's config
    // pointed at a disk they unplugged would be worse than a short list.
    const under = await found([join(scratch, "no-such-directory"), root]);
    expect(under.map((one) => one.name)).toEqual(["thicket"]);
  });

  test("does not walk into node_modules or a dot directory", async () => {
    const root = tree({ "node_modules/thicket": true, ".cache/orchard": true, lantern: true });
    expect((await found([root])).map((one) => one.name)).toEqual(["lantern"]);
  });

  test("a file where a directory was expected does not stop the walk", async () => {
    const root = tree({ lantern: true });
    writeFileSync(join(root, "README"), "not a directory");
    expect((await found([root])).map((one) => one.name)).toEqual(["lantern"]);
  });
});

const climb = (from: string) =>
  Effect.runPromise(
    nearestRepo(from).pipe(
      Effect.provide(Layer.mergeAll(NodeFileSystem.layer, NodePath.layer)),
      Effect.scoped,
    ),
  );

describe("nearestRepo", () => {
  test("finds the repository a subdirectory is inside", async () => {
    // The whole reason this exists. `jj -R <dir> root` names a repository
    // exactly and does not walk, so without this a person naming a directory
    // inside their project is told there is no repository in a directory that
    // is plainly inside one.
    const root = tree({ "thicket/src/deep": false, thicket: true });
    expect(await climb(join(root, "thicket", "src", "deep"))).toBe(join(root, "thicket"));
  });

  test("a directory that is itself a repository is its own answer", async () => {
    const root = tree({ thicket: true });
    expect(await climb(join(root, "thicket"))).toBe(join(root, "thicket"));
  });

  test("stops at the filesystem root rather than walking forever", async () => {
    // Nothing under the scratch directory is a repository, and neither is any
    // ancestor of it — a temp directory is not inside a checkout.
    const root = tree({ nothing: false });
    expect(await climb(join(root, "nothing"))).toBeUndefined();
  });

  test("the nearest one wins", async () => {
    // A workspace inside a repository is the ordinary shape here. The *nearest*
    // is the honest answer; `sourceRoot` is what then resolves a secondary
    // workspace back to the repository it belongs to, and that is a separate
    // step for a separate reason.
    const root = tree({ "outer/inner": true, outer: true });
    expect(await climb(join(root, "outer", "inner"))).toBe(join(root, "outer", "inner"));
  });
});
