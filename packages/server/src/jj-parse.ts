import type { JjBookmark, JjRevision, JjWorkspace } from "./jj";

// Reading jj's answers.
//
// Nearly every read asks for `-T 'json(self) ++ "\n"'`, so what comes back is
// one JSON object per line rather than the columns jj prints for a person. The
// exception is the revision listing at the bottom, which needs three things
// `json(self)` does not carry — see the note there. That is the
// whole reason this file is small: the human output embeds a change id, a
// bookmark list and a description on the same line as the name, and splitting
// it is guesswork that breaks the first time someone's description contains a
// colon.
//
// The templates name the fields explicitly, so what is read here is a contract
// this codebase asked for rather than a format that happens to be current.
//
// Unknown fields are ignored and a line that will not parse is skipped. jj adds
// to these objects between releases, and a daemon that refused to list
// workspaces because a new key appeared would be worse than one that ignored
// it.

interface WorkspaceJson {
  readonly name?: unknown;
  readonly target?: { readonly commit_id?: unknown; readonly change_id?: unknown };
}

interface BookmarkJson {
  readonly name?: unknown;
  readonly remote?: unknown;
  readonly target?: unknown;
}

const text = (value: unknown): string | undefined =>
  typeof value === "string" && value !== "" ? value : undefined;

/** Each non-empty line as JSON, skipping any that will not parse. */
const objects = (output: string): ReadonlyArray<unknown> => {
  const found: unknown[] = [];
  for (const line of output.split("\n")) {
    const trimmed = line.trim();
    if (trimmed === "") {
      continue;
    }
    try {
      found.push(JSON.parse(trimmed));
    } catch {
      // Skipped rather than fatal. See the note above.
    }
  }
  return found;
};

export const parseWorkspaces = (output: string): ReadonlyArray<JjWorkspace> => {
  const found: JjWorkspace[] = [];
  for (const raw of objects(output)) {
    const entry = raw as WorkspaceJson;
    const name = text(entry.name);
    if (name === undefined) {
      continue;
    }
    found.push({
      name,
      commitId: text(entry.target?.commit_id) ?? "",
      changeId: text(entry.target?.change_id) ?? "",
    });
  }
  return found;
};

export const parseBookmarks = (output: string): ReadonlyArray<JjBookmark> => {
  const found: JjBookmark[] = [];
  for (const raw of objects(output)) {
    const entry = raw as BookmarkJson;
    const name = text(entry.name);
    if (name === undefined) {
      continue;
    }
    // `target` is an array, because a conflicted bookmark points at several
    // commits at once. The first is enough for every question asked here, and
    // an empty array is a bookmark that has been deleted but is still listed.
    const target = Array.isArray(entry.target) ? text(entry.target[0]) : text(entry.target);
    found.push({ name, remote: text(entry.remote), target });
  }
  return found;
};

/** The local bookmarks, which is what "does this bookmark exist" means. */
export const localBookmarks = (all: ReadonlyArray<JjBookmark>): ReadonlyArray<JjBookmark> =>
  all.filter((entry) => entry.remote === undefined);

// ── revisions ──────────────────────────────────────────────────────────────
//
// The one answer here that is **not** one JSON object per line, and the reason
// is that the object `json(self)` produces for a commit is fixed: it carries
// the ids, the description and the two signatures, and nothing else. Three
// things a list of commits wants are not in it — whether the commit is empty,
// whether it is the working copy, and which bookmarks point at it.
//
// So the template asks for those separately and the line is tab-separated JSON
// values in a fixed order:
//
//   json(self) \t json(empty) \t json(current_working_copy) \t json(bookmarks)
//
// Tabs are safe as the separator precisely because every field is JSON: a tab
// inside a description comes back as the two characters `\t` inside a string,
// so a raw tab only ever appears where the template put one.
//
// The alternative was building the object by string concatenation in the
// template, which is JSON written by hand in a language with no way to escape
// a quote. This is the smaller lie.

interface RevisionJson {
  readonly commit_id?: unknown;
  readonly change_id?: unknown;
  readonly description?: unknown;
  readonly author?: { readonly name?: unknown; readonly timestamp?: unknown };
}

interface BookmarkRefJson {
  readonly name?: unknown;
}

/** The template every revision listing asks for. See the note above. */
export const REVISION_TEMPLATE =
  'json(self) ++ "\\t" ++ json(empty) ++ "\\t" ++ json(current_working_copy) ++ "\\t" ++ json(bookmarks) ++ "\\n"';

const parsed = (field: string): unknown => {
  try {
    return JSON.parse(field);
  } catch {
    return undefined;
  }
};

/**
 * A timestamp jj wrote, or nothing.
 *
 * Nothing rather than the epoch, because a row that says 1970 is a row that
 * looks like data. An absent date is something a renderer can leave out.
 */
const when = (value: unknown): Date | undefined => {
  const raw = text(value);
  if (raw === undefined) {
    return undefined;
  }
  const at = new Date(raw);
  return Number.isNaN(at.getTime()) ? undefined : at;
};

export const parseRevisions = (output: string): ReadonlyArray<JjRevision> => {
  const found: JjRevision[] = [];
  for (const line of output.split("\n")) {
    if (line.trim() === "") {
      continue;
    }
    const [self, empty, workingCopy, bookmarks] = line.split("\t");
    const entry = parsed(self ?? "") as RevisionJson | undefined;
    const changeId = text(entry?.change_id);
    if (changeId === undefined) {
      // Skipped rather than fatal, the same as everywhere else in this file. A
      // row with no change id is a row nothing could be asked about.
      continue;
    }
    const refs = parsed(bookmarks ?? "");
    found.push({
      changeId,
      commitId: text(entry?.commit_id) ?? "",
      description: typeof entry?.description === "string" ? entry.description : "",
      author: text(entry?.author?.name) ?? "",
      authored: when(entry?.author?.timestamp),
      // `=== true` and not a cast: a field the template stopped emitting reads
      // as undefined, and "not stated" has to mean false rather than truthy.
      empty: parsed(empty ?? "") === true,
      workingCopy: parsed(workingCopy ?? "") === true,
      bookmarks: Array.isArray(refs)
        ? refs.flatMap((ref) => {
            const name = text((ref as BookmarkRefJson).name);
            return name === undefined ? [] : [name];
          })
        : [],
    });
  }
  return found;
};
