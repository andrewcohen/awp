// Reading `zmx ls`.
//
// Pure, and separated from the subprocess that produces the text, because this
// is where the bugs are: the format is tab-separated `key=value` pairs with no
// escaping, and several values contain spaces, equals signs, and truncation
// markers that zmx added.

import type { Session } from "./multiplexer";

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
  let taskEnded = false;
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
        taskEnded = true;
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
    : {
        name,
        pid,
        clients,
        startDir,
        taskEnded,
        // Both are answered from the process table, which this parser has no
        // access to — `withProcesses` fills them in. The defaults are what a
        // caller that never asks would see, and they are the safe way round:
        // a session assumed live is one the sidebar shows and `start` leaves
        // alone.
        ended: false,
        busy: true,
        exitCode,
        created,
        cmd,
        labels,
      };
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

// ── what "ended" means, and why zmx cannot answer it ───────────────────────
//
// `zmx ls` reports `ended` and `exit_code` for the session's most recent
// **task** — the thing `zmx run` typed in, tracked by the
// `ZMX_TASK_COMPLETED:$?` marker it appends to the line. It says nothing about
// whether the session is still there.
//
// Those are not the same question, and the sidebar was asking the first while
// meaning the second. Measured on a live, working agent:
//
//   zmx ls    ended=1787923966  exit_code=1     ← reads as dead
//   history   Claude Code drawing its UI        ← is alive
//   ps 357    -bash, with a claude child        ← and busy
//
// Reported as "it looks pretty dead to me still in this sidebar", by somebody
// who had just attached to it by hand and found it fine.
//
// So the process table answers it instead. Two different questions, and the
// two callers want different ones:
//
//   ended   the process is gone         the sidebar, attach, the wire
//   busy    something is running in it  `start`, deciding whether `zmx run`
//                                       would interrupt anything
//
// **Busy needs both halves of its rule**, because a session comes in two
// shapes and only one of them has a shell in it:
//
//   pid    comm     child            created by
//   357    -bash    claude           `zmx run -d claude` — a shell, told to run
//   18057  claude   claude helper    `zmx attach <name> claude` — no shell
//
// A child is the signal for the first; not being a shell is the signal for the
// second. An idle `-bash` with no children is the one case that is genuinely
// not busy, and it is exactly the case `start` has to be willing to run in.

/** A shell sitting at a prompt is a session with nothing running in it. */
const SHELLS = new Set(["sh", "bash", "zsh", "fish", "dash", "ksh", "tcsh", "csh", "nu"]);

/** One process, as the table reports it. */
export interface ProcessRow {
  readonly pid: number;
  readonly ppid: number;
  /** The executable's name. A login shell is reported with a leading `-`. */
  readonly comm: string;
}

/**
 * Parse `ps -eo pid=,ppid=,comm=`.
 *
 * Whitespace-separated, and `comm` may contain spaces on macOS when it is a
 * path — so the first two fields are taken and the rest is the command,
 * rather than splitting into exactly three.
 */
export const parseProcessTable = (output: string): ReadonlyArray<ProcessRow> => {
  const rows: ProcessRow[] = [];
  for (const line of output.split("\n")) {
    const parts = line.trim().split(/\s+/u);
    const pid = Number(parts[0]);
    const ppid = Number(parts[1]);
    if (!Number.isInteger(pid) || !Number.isInteger(ppid) || parts.length < 3) {
      continue;
    }
    rows.push({ pid, ppid, comm: parts.slice(2).join(" ") });
  }
  return rows;
};

/** The bare name of an executable, without a path or a login shell's dash. */
const basename = (comm: string): string => {
  const last = comm.slice(comm.lastIndexOf("/") + 1);
  return last.startsWith("-") ? last.slice(1) : last;
};

/**
 * Fill in `ended` and `busy` from a process table.
 *
 * Pure, and takes the rows rather than reading them, so the rule can be tested
 * against a table written by hand — which is the only way to state the
 * two-shapes case above without two live sessions to point at.
 */
export const withProcesses = (
  sessions: ReadonlyArray<Session>,
  processes: ReadonlyArray<ProcessRow>,
): ReadonlyArray<Session> => {
  const byPid = new Map(processes.map((row) => [row.pid, row]));
  const parents = new Set(processes.map((row) => row.ppid));
  return sessions.map((session) => {
    const own = byPid.get(session.pid);
    if (own === undefined) {
      return { ...session, ended: true, busy: false };
    }
    return {
      ...session,
      ended: false,
      busy: parents.has(session.pid) || !SHELLS.has(basename(own.comm)),
    };
  });
};
