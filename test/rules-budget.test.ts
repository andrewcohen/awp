import { readdirSync, statSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, test } from "vitest";

// Every AGENTS.md is read into the context of every session working in its
// directory, before anything is asked. One of them reached 351KB — 14% of a
// context window spent to open an unrelated file — because appending is easy
// and nothing ever failed.
//
// The budget is what makes adding an edit: a file at its limit takes a new
// rule by cutting an old one or moving it to docs/, in the same change.
// docs/ is deliberately unbounded — nothing loads it.
//
// Sizes are bytes, not tokens, because bytes are what a test can read. The
// ratio in these files is close to 2.75 chars per token.

const ROOT_BUDGET = 20_000;
const FILED_BUDGET = 34_000;

// Walked rather than listed, so a rules file added in a new directory is
// caught by the gate that exists to bound it.
const rulesFiles = (dir: string): string[] =>
  readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    if (entry.name === "node_modules" || entry.name === ".git" || entry.name === ".jj") return [];
    if (entry.name === "archive" || entry.name === "docs") return [];
    const path = join(dir, entry.name);
    if (entry.isDirectory()) return rulesFiles(path);
    // CLAUDE.md is a symlink to the AGENTS.md beside it — one file, counted once.
    return entry.isFile() && entry.name === "AGENTS.md" ? [path] : [];
  });

describe("rules files stay within budget", () => {
  const found = rulesFiles(".");

  test("the root file is the smallest thing everything loads", () => {
    expect(statSync("AGENTS.md").size).toBeLessThanOrEqual(ROOT_BUDGET);
  });

  test.each(found.filter((path) => path !== "AGENTS.md"))("%s", (path) => {
    expect(statSync(path).size).toBeLessThanOrEqual(FILED_BUDGET);
  });
});
