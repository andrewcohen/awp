import { homedir } from "node:os";
import { basename, join } from "node:path";
import { Context, Duration, Effect, FileSystem, Layer, Option, Schema, Stream } from "effect";
import type { WorkspaceFacts } from "@awp-kit/protocol";

// What is known about a workspace beyond the fact that it exists.
//
// ── where this comes from, and why it is somebody else's file ──────────────
//
// `~/.awp/workspace-state.json` is written by the Go implementation, and it is
// the only place several of these facts have ever been recorded. Reading it is
// not a nicety: the sidebar showed slugs for every row because amoeba looked
// for a session label it invented — `awp_label` — which nothing on this machine
// carries, while nineteen display names sat in this file unread.
//
// ── two kinds of field, and only one of them is temporary ──────────────────
//
//   displayName · bookmark · prNumber      durable facts about the work
//   status · unread · prompt · devLoop     written by Claude Code hooks
//                                          shelling out to `awp internal
//                                          report-status`
//
// The second group is what ACP replaces, and replaces with something better: a
// live notification instead of a hook writing a file. That is why this is a
// *source* behind a field on the wire rather than a shape the wire exposes —
// when the better source arrives, nothing above this changes. See the note on
// `WorkspaceFacts` in the contract.
//
// ── read on change, not on a timer ─────────────────────────────────────────
//
// Status changes on its own, which makes it like a job and unlike a thread: a
// client that only asks misses the transition it was waiting for. The file is a
// few kilobytes, so the whole table is re-read and pushed rather than diffed —
// a diff here would be machinery in service of an economy nobody can measure.
//
// ── it never fails ─────────────────────────────────────────────────────────
//
// A missing file is what a machine that has only ever run amoeba looks like,
// and a half-written one is what it looks like during someone else's write.
// Both give the previous answer, or none. A sidebar that emptied itself because
// a JSON parse landed mid-write would be worse than a sidebar one tick stale.

/** Go's `~/.awp/workspace-state.json`. */
export const STATE_FILE = join(homedir(), ".awp", "workspace-state.json");

/**
 * The states the Go implementation writes, and the one it does not.
 *
 * `error` is in the union because `sidebarSectionOf` in the archive branches on
 * it, so something once wrote it; nothing in the current file does. Kept so a
 * client's rendering of it is decided now rather than the first time one turns
 * up, which is exactly when nobody is looking.
 */
const Status = Schema.Literals(["working", "waiting", "idle", "exited", "error"]);

/**
 * One entry, as much of it as is read.
 *
 * Every field optional and unknown keys ignored, because this file belongs to
 * another program: it gains fields between releases, and a daemon that refused
 * to show a sidebar over a key it did not recognise would be worse than one
 * that ignored it.
 */
const Entry = Schema.Struct({
  Name: Schema.optional(Schema.String),
  DisplayName: Schema.optional(Schema.String),
  Status: Schema.optional(Schema.String),
  Unread: Schema.optional(Schema.Boolean),
  PRNumber: Schema.optional(Schema.Number),
  Bookmark: Schema.optional(Schema.String),
  ActivePrompt: Schema.optional(Schema.String),
  LastActiveAt: Schema.optional(Schema.String),
  DevLoop: Schema.optional(
    Schema.Struct({
      Phase: Schema.optional(Schema.String),
      Task: Schema.optional(Schema.String),
      Done: Schema.optional(Schema.Number),
      Total: Schema.optional(Schema.Number),
    }),
  ),
});

/** `{ "<repo root>": { "<workspace>": Entry } }`. */
const File = Schema.Record(Schema.String, Schema.Record(Schema.String, Entry));

const decode = Schema.decodeUnknownSync(File);
const statusOf = Schema.decodeUnknownOption(Status);

const text = (value: string | undefined): string | undefined => {
  const trimmed = value?.trim();
  return trimmed === undefined || trimmed === "" ? undefined : trimmed;
};

/**
 * A date, or nothing — an unparseable one is nothing.
 *
 * Go writes RFC 3339 with a nanosecond fraction, which `Date` reads correctly.
 * A malformed one gives `Invalid Date`, which serialises to null and would
 * arrive at a client as a date it cannot render.
 */
const when = (value: string | undefined): Date | undefined => {
  if (value === undefined || value === "") {
    return undefined;
  }
  const at = new Date(value);
  return Number.isNaN(at.getTime()) ? undefined : at;
};

/**
 * The file's contents as one flat list, keyed the way the wire is.
 *
 * **The project is the repo directory's basename**, which is the same rule
 * `identityLabels` and `Project` already follow — so this joins to a session's
 * identity without a lookup table, and a project imported under that name is
 * the same project.
 *
 * Exported so it can be tested against real file contents without a file
 * system, which is where every interesting decision here is.
 */
export const factsIn = (contents: string): ReadonlyArray<WorkspaceFacts> => {
  const decoded = decode(JSON.parse(contents) as unknown);
  const out: WorkspaceFacts[] = [];
  for (const [root, workspaces] of Object.entries(decoded)) {
    const project = basename(root);
    for (const [workspace, entry] of Object.entries(workspaces)) {
      const loop = entry.DevLoop;
      out.push({
        project,
        workspace,
        displayName: text(entry.DisplayName),
        // Ignored rather than passed through when it is a word this does not
        // know. A client switching on a closed union is what makes a hue per
        // state expressible at all, and an unknown string would arrive as a row
        // with no dot and no explanation.
        status: Option.getOrUndefined(statusOf(entry.Status)),
        unread: entry.Unread === true,
        pr: entry.PRNumber !== undefined && entry.PRNumber > 0 ? entry.PRNumber : undefined,
        bookmark: text(entry.Bookmark),
        prompt: text(entry.ActivePrompt),
        phase: text(loop?.Phase),
        task: text(loop?.Task),
        done: loop?.Done,
        total: loop?.Total,
        lastActiveAt: when(entry.LastActiveAt),
      });
    }
  }
  return out;
};

export class WorkspaceState extends Context.Service<
  WorkspaceState,
  {
    /** Everything the file says, now. Empty when there is nothing to say. */
    readonly read: () => Effect.Effect<ReadonlyArray<WorkspaceFacts>>;
    /** The same, once immediately and again whenever the file changes. */
    readonly changes: () => Stream.Stream<ReadonlyArray<WorkspaceFacts>>;
  }
>()("awp/WorkspaceState") {}

/**
 * How long to let a burst settle.
 *
 * A hook writes the whole file, so a turn that reports `working` and then a
 * tool result lands as two writes a few milliseconds apart. Shorter than the
 * diff watcher's 300ms because this is one small read rather than a whole
 * patch, and because a status light that lags a third of a second behind the
 * thing it describes is the one lag a person notices.
 */
const SETTLE = Duration.millis(120);

export const make = (path: string = STATE_FILE) =>
  Effect.gen(function* () {
    const files = yield* FileSystem.FileSystem;

    const read = (): Effect.Effect<ReadonlyArray<WorkspaceFacts>> =>
      files.readFileString(path).pipe(
        Effect.map(factsIn),
        // One catch for both the missing file and the malformed one, because
        // the answer is the same and the difference is not this file's to
        // report: another program is writing it, and a read that landed halfway
        // through a write is a normal event rather than a fault.
        Effect.catchCause(() => Effect.succeed([] as ReadonlyArray<WorkspaceFacts>)),
      );

    return {
      read,
      changes: () =>
        Stream.concat(
          Stream.fromEffect(read()),
          // The directory, not the file. An atomic write is a write to a
          // temporary name followed by a rename, so the inode a file watch is
          // holding is not the one that ends up at the path — and the watch
          // goes quiet without failing, which is the worst way for this to
          // break. Watching the directory sees the rename.
          files.watch(join(path, "..")).pipe(
            Stream.filter((event) => event.path.includes(basename(path))),
            Stream.debounce(SETTLE),
            Stream.mapEffect(() => read()),
            Stream.catchCause(() => Stream.empty),
          ),
        ),
    };
  });

export const layer = (
  path: string = STATE_FILE,
): Layer.Layer<WorkspaceState, never, FileSystem.FileSystem> =>
  Layer.effect(WorkspaceState)(make(path));
