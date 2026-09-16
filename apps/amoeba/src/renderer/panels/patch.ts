// What a patch amounts to, in the two numbers a header can hold.
//
// A second reading of a format that is already being parsed downstream, which
// is a thing worth justifying. The diff renderer parses the patch properly —
// files, hunks, tokens, highlighting — and it does that work asynchronously,
// inside a shadow root, and hands nothing back. So there is no arrangement in
// which the panel's own header could ask it "how many files is this".
//
// What this does is deliberately not parsing. It counts lines by their first
// character, which is the one thing about a unified diff that needs no state:
//
//   diff --git a/x b/x     a file             ← counted
//   --- a/x                the old path       ← not a deletion
//   +++ b/x                the new path       ← not an addition
//   @@ -1,2 +1,3 @@        a hunk header
//   -gone                  a deletion         ← counted
//   +new                   an addition        ← counted
//
// The three-character prefixes are the whole subtlety, and they are why this
// cannot be `lines.filter((line) => line.startsWith("+"))`. Everything else a
// real parser is for — renames, modes, binary files, hunk arithmetic — changes
// none of these three numbers.

export interface Stat {
  readonly files: number;
  readonly added: number;
  readonly removed: number;
}

export const statOf = (patch: string): Stat => {
  let files = 0;
  let added = 0;
  let removed = 0;

  for (const line of patch.split("\n")) {
    if (line.startsWith("diff --git ")) {
      files += 1;
    } else if (line.startsWith("+++ ") || line.startsWith("--- ")) {
      // The file headers, which look exactly like the lines below and are not
      // them. Tested before the single-character cases, not after.
      continue;
    } else if (line.startsWith("+")) {
      added += 1;
    } else if (line.startsWith("-")) {
      removed += 1;
    }
  }

  return { files, added, removed };
};

/**
 * The stat as a person reads it, or nothing when there is nothing to say.
 *
 * Nothing rather than "0 files · +0 −0": a header that renders a row of zeroes
 * is a header that has to be read before it can be dismissed, and the panel
 * already says so in words when a patch is empty.
 *
 * A real minus sign, U+2212, beside the plus. A hyphen next to a `+` at this
 * size is a dash of a different weight and reads as a typo.
 */
export const summarise = (stat: Stat): string | undefined => {
  if (stat.files === 0) {
    return undefined;
  }
  const files = stat.files === 1 ? "1 file" : `${stat.files} files`;
  return `${files} · +${stat.added} −${stat.removed}`;
};

/**
 * The first line of a commit message, or jj's own words for having none.
 *
 * jj's wording rather than one invented here, because it is the wording a
 * person is looking at in `jj log` in the next column over, and two names for
 * the same state is one more than anybody needs.
 */
export const subjectOf = (description: string): string => {
  const first = description.split("\n")[0]?.trim() ?? "";
  return first === "" ? "(no description set)" : first;
};

/**
 * A number that changes when the thing it names does.
 *
 * FNV-1a. Collisions are possible in principle and cost a redraw that did not
 * happen; the alternative — a version that fails to change — costs a stale
 * cache, and in the diff renderer that is not a cosmetic loss but a throw. See
 * `contentOf`.
 */
export const versionOf = (state: string): number => {
  let hash = 2166136261;
  for (let index = 0; index < state.length; index += 1) {
    hash ^= state.codePointAt(index) ?? 0;
    hash = Math.imul(hash, 16777619);
  }
  return hash >>> 0;
};

/** The parts of a parsed file that make it a different file to render. */
export interface FileContent {
  readonly type: string;
  readonly isPartial: boolean;
  readonly hunks: ReadonlyArray<unknown>;
  readonly unifiedLineCount: number;
  readonly splitLineCount: number;
  readonly deletionLines: ReadonlyArray<string>;
  readonly additionLines: ReadonlyArray<string>;
}

/**
 * A file's content, as a number to fold into a render version.
 *
 * ── why a renderer needs this and an id will not do ────────────────────────
 *
 * The diff renderer caches per item, keyed on the item's id and its `version`,
 * and its own type says to bump the version whenever the value changes. The id
 * is the path and its position, so it is deliberately the *same* id when the
 * same file changes on disk — that is what makes the cache worth having.
 *
 * So the content has to be in the version, and it was not: only the fold, the
 * viewed mark and the annotations were. A file that changed while none of those
 * did kept its version, the renderer reused the AST highlighted for the
 * previous content, and indexed it with hunks parsed from the new one:
 *
 *   deletionLines[deletionLine.lineIndex]   undefined
 *   additionLines[additionLine.lineIndex]   undefined
 *   → "deletionLine and additionLine are null, something is wrong"   thrown
 *
 * It read as intermittent because it needs the content to change with the fold
 * state standing still, which is exactly what an agent writing to a file does
 * now that the daemon pushes a new patch instead of waiting for a button.
 *
 * The lines are the content. The four counts beside them catch a reshape that
 * leaves the lines alone, which is what hydrating collapsed context does.
 */
export const contentOf = (file: FileContent): number =>
  versionOf(
    [
      file.type,
      file.isPartial ? "part" : "full",
      file.hunks.length,
      file.unifiedLineCount,
      file.splitLineCount,
      file.deletionLines.join("\n"),
      file.additionLines.join("\n"),
    ].join("|"),
  );
