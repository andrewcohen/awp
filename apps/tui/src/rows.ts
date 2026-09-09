// `zmx ls` as rows a picker can draw.
//
// This is the `_za_rows` fish function's job, minus the two things it does with
// a subprocess each: `ps -eo pid=,tty=` and `stat -f %m /dev/tty*`, which it
// joins to order sessions by when their tty was last written to. That is a nice
// ordering and it is not what a POC is answering, so the order here is
// clients-first then newest, and the README says so rather than pretending.
//
// The parse is deliberately the same shape as the repo's own zmx-parse: split
// on tabs, read `k=v`, ignore anything unknown. zmx adds fields between
// releases and a picker that refused to list a session over a new key would be
// worse than one that ignores it.

export type Row = {
  name: string;
  pid: number;
  clients: number;
  created: number;
  cmd: string;
  project: string;
  workspace: string;
  kind: string;
};

/** Strip the arrow zmx puts in front of the caller's own session. */
const unmark = (line: string) => line.replace(/^[^A-Z_a-z]*/u, "");

export const parseRows = (stdout: string): Row[] => {
  const rows: Row[] = [];
  for (const raw of stdout.split("\n")) {
    const line = raw.trim();
    if (line === "") continue;
    const fields = new Map<string, string>();
    for (const field of unmark(line).split("\t")) {
      const eq = field.indexOf("=");
      if (eq <= 0) continue;
      fields.set(field.slice(0, eq).replace(/^[^A-Z_a-z]*/u, ""), field.slice(eq + 1));
    }
    const name = fields.get("name");
    if (name === undefined || name === "") continue;

    // The labels are the truth; the name is an address that has been shortened
    // and cannot be split back into its parts. Splitting on dots is what this
    // repo's AGENTS.md spends a section on being wrong — so it is the fallback,
    // and it is only used for a display column, never to group anything.
    const parts = name.split(".");
    const derived =
      parts.length === 4 && parts[0] === "awp"
        ? { project: parts[1]!, workspace: parts[2]!, kind: parts[3]! }
        : { project: name, workspace: "", kind: "" };

    rows.push({
      name,
      pid: Number(fields.get("pid") ?? 0),
      clients: Number(fields.get("clients") ?? 0),
      created: Number(fields.get("created") ?? 0),
      cmd: fields.get("cmd") ?? "",
      project: fields.get("awp_project") ?? derived.project,
      workspace: fields.get("awp_workspace") ?? derived.workspace,
      kind: fields.get("awp_kind") ?? derived.kind,
    });
  }
  rows.sort((a, b) => b.clients - a.clients || b.created - a.created);
  return rows;
};

export const listRows = async (only?: string): Promise<Row[]> => {
  const proc = Bun.spawn(["zmx", "ls"], {
    // Present and empty, never absent: a key left out is a key left alone, and
    // with the marker set `zmx ls` also stops arrowing the caller's own row.
    env: { ...process.env, ZMX_SESSION: "" },
    stdout: "pipe",
    stderr: "pipe",
  });
  const [out, , code] = await Promise.all([
    new Response(proc.stdout).text(),
    new Response(proc.stderr).text(),
    proc.exited,
  ]);
  // A failing `zmx ls` parses to an empty list and reads as "no sessions",
  // which is exactly what having no sessions looks like. Refuse instead.
  if (code !== 0) throw new Error(`zmx ls exited ${code}`);
  const rows = parseRows(out);
  return only === undefined || only === "" ? rows : rows.filter((r) => r.name.startsWith(only));
};

export const age = (created: number, now = Date.now() / 1000): string => {
  const d = Math.max(0, Math.floor(now - created));
  if (d < 60) return `${d}s`;
  if (d < 3600) return `${Math.floor(d / 60)}m`;
  if (d < 86_400) return `${Math.floor(d / 3600)}h`;
  return `${Math.floor(d / 86_400)}d`;
};

/** The fish function's `shorten`: drop the flags nobody reads in a picker. */
export const shortCmd = (cmd: string, max = 40): string => {
  let t = cmd
    // The argument is a path and paths have spaces: the fish original matched
    // \S+ and left `Vault.md'` behind on one of this machine's own sessions.
    .replaceAll(/--append-system-prompt-file +('[^']*'|"[^"]*"|\S+)/gu, "")
    .replaceAll(/--permission-mode +\S+/gu, "")
    .replaceAll(/--model +\S+/gu, "")
    .replaceAll(/ {2,}/gu, " ")
    .trim()
    .replace(/^\S*\//u, "");
  if (t.length > max) t = `${t.slice(0, max - 1)}…`;
  return t;
};

export const matches = (row: Row, query: string): boolean => {
  const q = query.trim().toLowerCase();
  if (q === "") return true;
  const hay = `${row.name} ${row.project} ${row.workspace} ${row.kind} ${row.cmd}`.toLowerCase();
  return q.split(/\s+/u).every((term) => hay.includes(term));
};
