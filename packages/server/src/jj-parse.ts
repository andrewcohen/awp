import type { JjBookmark, JjWorkspace } from "./jj";

// Reading jj's answers.
//
// Every read asks for `-T 'json(self) ++ "\n"'`, so what comes back is one JSON
// object per line rather than the columns jj prints for a person. That is the
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
