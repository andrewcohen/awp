// Reading `zmx ls`.
//
// Pure, and separated from the subprocess that produces the text, because this
// is where the bugs are: the format is tab-separated `key=value` pairs with no
// escaping, and several values contain spaces, equals signs, and truncation
// markers that zmx added.

import type { Session } from "./multiplexer.js";

/**
 * What `zmx ls` puts in front of the session the caller is running inside.
 *
 * Stripping it is not cosmetic. Left in place, the first field of that line is
 * `→ name=…` rather than `name=…`, the line fails to parse, and the session
 * that vanishes is always the caller's own — so awp would list every session
 * except the one it is running in, and the workspace a developer looks at first
 * would read as empty.
 *
 * Not directly verifiable from outside a session, which is where the probe has
 * to run. Stripping regardless is the safe direction: a leading arrow that
 * never appears costs nothing.
 */
const CURRENT_MARKER = "→";

/** Fields `zmx ls` emits that mean something specific. Anything else is a label. */
const KNOWN_FIELDS = new Set([
  "name",
  "pid",
  "clients",
  "start_dir",
  "ended",
  "exit_code",
  "created",
  "cmd",
]);

/**
 * A numeric field, or 0 if it is not one.
 *
 * `Number` rather than `parseInt`, deliberately: they disagree on `"12abc"` —
 * parseInt takes the 12 and Number gives NaN. A field that is not a number is
 * better read as absent than as its own prefix, and it matches what Go's
 * strconv.Atoi did.
 */
const toInt = (value: string): number => {
  const parsed = Math.trunc(Number(value));
  return Number.isNaN(parsed) ? 0 : parsed;
};

/**
 * Parse one line, or `undefined` if it is not a session.
 *
 * A line is dropped rather than raising: `zmx ls` output is a list, and one
 * unreadable row should not lose the other twenty.
 */
export const parseSessionLine = (line: string): Session | undefined => {
  const trimmed = line.trim().startsWith(CURRENT_MARKER)
    ? line.trim().slice(CURRENT_MARKER.length).trim()
    : line.trim();

  if (trimmed === "") {
    return undefined;
  }

  let name = "";
  let pid = 0;
  let clients = 0;
  let startDir = "";
  let ended = false;
  let exitCode = 0;
  let created: Date | undefined;
  let cmd = "";
  const labels: Record<string, string> = {};

  for (const field of trimmed.split("\t")) {
    const text = field.trim();
    const at = text.indexOf("=");
    if (at < 0) {
      continue;
    }
    // First `=` only. A value can contain one — `cmd=FOO=bar prog` is a
    // perfectly ordinary command line.
    const key = text.slice(0, at);
    const value = text.slice(at + 1);

    switch (key) {
      case "name":
        name = value;
        break;
      case "pid":
        pid = toInt(value);
        break;
      case "clients":
        clients = toInt(value);
        break;
      case "start_dir":
        startDir = value;
        break;
      case "ended":
        // Presence is the signal, whatever the value.
        ended = true;
        break;
      case "exit_code":
        exitCode = toInt(value);
        break;
      case "created": {
        // A unix stamp. Parsed here so nothing downstream has to know that, and
        // so a display can show an age rather than a number.
        const seconds = toInt(value);
        created = seconds > 0 ? new Date(seconds * 1000) : undefined;
        break;
      }
      case "cmd":
        cmd = value;
        break;
      default:
        labels[key] = value;
        break;
    }
  }

  // A row with no name is not addressable, so it is not a session.
  return name === ""
    ? undefined
    : { name, pid, clients, startDir, ended, exitCode, created, cmd, labels };
};

/** Parse the whole of `zmx ls`, skipping anything unreadable. */
export const parseSessionList = (output: string): ReadonlyArray<Session> => {
  const sessions: Session[] = [];
  for (const line of output.split("\n")) {
    const session = parseSessionLine(line);
    if (session !== undefined) {
      sessions.push(session);
    }
  }
  return sessions;
};

/** Guard against handing a process manager an empty argument to interpret. */
export const requireName = (op: string, name: string): string | undefined =>
  name.trim() === "" ? `${op} a session: no name given` : undefined;

export { CURRENT_MARKER, KNOWN_FIELDS };
