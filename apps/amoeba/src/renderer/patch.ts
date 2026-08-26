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
