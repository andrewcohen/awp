// Put the ACP adapter on this machine.
//
// The adapter is a per-machine install at `~/.awp/tools` — not a dependency of
// this repository, and deliberately: it is 306MB, and the create-workspace job
// runs `bun install` in every workspace it makes, so a dependency here is that
// much per checkout. The daemon's refusal already names the command; what was
// missing is anything that runs it, so somebody reading the refusal had to
// retype it.
//
// **The command is imported, not restated.** `INSTALL` is the same string the
// failure prints, so this cannot drift from the sentence a person was told to
// follow — which is the whole reason this is four lines around an import
// rather than a line in package.json.
//
// Safe to run twice: `mkdir -p`, and `bun add` on a package that is already
// there is a no-op.

import { spawnSync } from "node:child_process";
import { INSTALL, adapterPath } from "./acp";

if (adapterPath() !== undefined) {
  console.log("  the ACP adapter is already installed");
  process.exit(0);
}

console.log(`  ${INSTALL}\n`);
// Through a shell, whole, for the same reason `hooks.bootstrap` is: it is a
// line with `&&` and a `cd` in it, and splitting that on whitespace produces
// nonsense.
const done = spawnSync("sh", ["-c", INSTALL], { stdio: "inherit" });

if (done.status !== 0) {
  console.error("\n  the install did not finish");
  process.exit(done.status ?? 1);
}

// Checked rather than assumed: `bun add` can succeed while putting the files
// somewhere this does not look, and a script that reported success on that
// would send the next person back to the daemon's refusal with nothing to do
// about it.
console.log(
  adapterPath() === undefined
    ? "\n  installed, but the adapter is not where the daemon looks for it"
    : "\n  the ACP adapter is installed",
);
