import { homedir } from "node:os";
import { basename, join } from "node:path";
import { Context, Duration, Effect, FileSystem, Layer, Schema, Stream } from "effect";
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
// ── two kinds of field, and only one of them is read ───────────────────────
//
//   displayName · bookmark · prNumber      durable facts about the work
//   status · unread · prompt · devLoop     written by Claude Code hooks
//                                          shelling out to `awp internal
//                                          report-status` — NOT READ
//
// The second group is replaced by the chat, which reports `working` and
// `waiting` live from ACP. The hooks are gone, so nothing writes it any more:
// what is in the file is the last hook's write, frozen. A turn whose `Stop` was
// never written said `working` for as long as anybody looked, because a chat
// with nothing to say let the file's answer stand. So none of it is read, and
// a workspace with no live chat has no status rather than a stale one.
//
// `LastActiveAt` is hook-written too and is still read: it only orders rows,
// and an old time orders them the way they last moved.
//
// ── read on change, not on a timer ─────────────────────────────────────────
//
// A display name or a pull request can change while a window is open, and a
// client that only asks misses it. The file is a
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
  PRNumber: Schema.optional(Schema.Number),
  Bookmark: Schema.optional(Schema.String),
  LastActiveAt: Schema.optional(Schema.String),
});

/** `{ "<repo root>": { "<workspace>": Entry } }`. */
const File = Schema.Record(Schema.String, Schema.Record(Schema.String, Entry));

const decode = Schema.decodeUnknownSync(File);

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
      out.push({
        project,
        workspace,
        displayName: text(entry.DisplayName),
        // The chat's to say — see the header. The daemon lays it over this.
        status: undefined,
        // The file cannot know; the daemon lays its own answer over this.
        chat: false,
        unread: false,
        pr: entry.PRNumber !== undefined && entry.PRNumber > 0 ? entry.PRNumber : undefined,
        bookmark: text(entry.Bookmark),
        prompt: undefined,
        phase: undefined,
        task: undefined,
        done: undefined,
        total: undefined,
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
 * A writer rewrites the whole file, often as a temporary name and a rename —
 * two events a few milliseconds apart. Shorter than the diff watcher's 300ms
 * because this is one small read rather than a whole patch.
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
