import { describe, expect, test } from "vitest";
import { type Session, identity, isLive } from "./multiplexer";
import {
  parseProcessTable,
  parseSessionLine,
  parseSessionList,
  requireName,
  withProcesses,
} from "./zmx-parse";

// Fixtures are real `zmx ls` output captured on 2026-08-25, not invented. The
// awkward cases are already in it: a start_dir containing a space, a cmd
// containing spaces and quotes, a cmd zmx truncated with a literal "...", the
// arrow marking the caller's own session, and rows both with and without awp's
// identity labels.
const t = (...fields: string[]) => fields.join("\t");

const OBSIDIAN = t(
  "  name=awp.Obsidian_Vault.default.agent",
  "pid=83768",
  "clients=0",
  "created=1787254760",
  "start_dir=/Users/acohen/Documents/Obsidian Vault",
  "cmd=claude --permission-mode auto --model opus",
);

const CALLERS_OWN = t(
  "→ name=awp.awp.awp-kit-amoeba.agent",
  "pid=28887",
  "clients=0",
  "created=1787662356",
  "start_dir=/Users/acohen/.awp/workspaces/awp/awp-kit-amoeba",
  "cmd=claude --permission-mode auto --model opus",
);

const LABELLED = t(
  "  name=awp.typed-router.effect-ts-v4-endpo-afe7.agent",
  "pid=30156",
  "clients=0",
  "created=1787575619",
  "start_dir=/Users/acohen/.awp/workspaces/typed-router/effect-ts-v4-endpoint-poc",
  "cmd=claude --permission-mode auto --model opus...",
  "awp_kind=agent",
  "awp_project=typed-router",
  "awp_workspace=effect-ts-v4-endpoint-poc",
);

const QUOTED_CMD = t(
  "  name=awp.thicket.effect-ts-tiered-d-f500.action_dev",
  "pid=65040",
  "clients=0",
  "created=1787590617",
  "start_dir=/Users/acohen/.awp/workspaces/thicket/effect-ts-tiered-discount-timemachine",
  "cmd=sh -c 'pnpm dev'",
);

describe("parseSessionLine", () => {
  test("reads the ordinary fields", () => {
    const session = parseSessionLine(OBSIDIAN);
    expect(session?.name).toBe("awp.Obsidian_Vault.default.agent");
    expect(session?.pid).toBe(83768);
    expect(session?.clients).toBe(0);
  });

  test("keeps a start_dir containing a space — fields are tab-separated", () => {
    expect(parseSessionLine(OBSIDIAN)?.startDir).toBe("/Users/acohen/Documents/Obsidian Vault");
  });

  test("keeps a cmd containing spaces and quotes", () => {
    expect(parseSessionLine(QUOTED_CMD)?.cmd).toBe("sh -c 'pnpm dev'");
  });

  test("keeps zmx's own truncation marker rather than pretending it is the command", () => {
    expect(parseSessionLine(LABELLED)?.cmd).toBe("claude --permission-mode auto --model opus...");
  });

  test("splits on the first = only, so a value may contain one", () => {
    const line = t("name=x", "cmd=FOO=bar prog --flag=1");
    expect(parseSessionLine(line)?.cmd).toBe("FOO=bar prog --flag=1");
  });

  test("turns created into a Date, not a unix stamp", () => {
    expect(parseSessionLine(OBSIDIAN)?.created).toEqual(new Date(1787254760 * 1000));
  });

  test("collects unknown fields as labels", () => {
    expect(parseSessionLine(LABELLED)?.labels).toEqual({
      awp_kind: "agent",
      awp_project: "typed-router",
      awp_workspace: "effect-ts-v4-endpoint-poc",
    });
  });

  test("a session with no labels has none, rather than undefined", () => {
    expect(parseSessionLine(OBSIDIAN)?.labels).toEqual({});
  });

  test("is not fooled by a row with no name", () => {
    expect(parseSessionLine(t("pid=1", "clients=0"))).toBeUndefined();
  });

  test("reads a non-numeric pid as absent rather than as its own prefix", () => {
    // "12abc" is 0, not 12. A partially-parsed pid is a pid that addresses the
    // wrong process.
    expect(parseSessionLine(t("name=x", "pid=12abc"))?.pid).toBe(0);
    expect(parseSessionLine(t("name=x", "pid="))?.pid).toBe(0);
  });

  test("ignores blank lines and text that is not key=value", () => {
    expect(parseSessionLine("")).toBeUndefined();
    expect(parseSessionLine("   ")).toBeUndefined();
    expect(parseSessionLine("no pairs here")).toBeUndefined();
  });
});

describe("the caller's own session", () => {
  // The one that would go missing. Left unstripped, the arrow makes the first
  // field `→ name=…`, the row fails to parse, and the session that disappears
  // is always the caller's own — the workspace a developer looks at first.
  test("is parsed despite the arrow marking it", () => {
    const session = parseSessionLine(CALLERS_OWN);
    expect(session?.name).toBe("awp.awp.awp-kit-amoeba.agent");
    expect(session?.pid).toBe(28887);
  });

  test("survives a full listing", () => {
    const all = parseSessionList([OBSIDIAN, CALLERS_OWN, LABELLED].join("\n"));
    expect(all.map((s) => s.name)).toContain("awp.awp.awp-kit-amoeba.agent");
    expect(all).toHaveLength(3);
  });
});

describe("ended", () => {
  test("zmx's `ended` is about the last task, and is read as such", () => {
    // zmx keeps a session listed after its command exits so the output can
    // still be read, and reports `ended` for that command — the thing
    // `zmx run` typed in, tracked by the `ZMX_TASK_COMPLETED:$?` marker.
    //
    // **It is not a statement about the session**, and reading it as one drew
    // a working agent in the sidebar as exited. So it lands on `taskEnded`,
    // and `ended` is answered by `withProcesses` from the process table.
    const done = parseSessionLine(t("name=x", "pid=1", "ended=true", "exit_code=2"));
    expect(done?.taskEnded).toBe(true);
    expect(done?.exitCode).toBe(2);
    expect(done?.ended).toBe(false);
  });

  test("liveness is unknown until the process table has been asked", () => {
    // The defaults are the safe way round: a session assumed live is one the
    // sidebar shows and `start` leaves alone. `ps` failing should hide nothing
    // and interrupt nothing.
    const seen = parseSessionLine(OBSIDIAN);
    expect(seen?.ended).toBe(false);
    expect(seen?.busy).toBe(true);
    expect(isLive(seen as never)).toBe(true);
  });
});

describe("identity", () => {
  test("prefers the labels, which carry the unshortened name", () => {
    const session = parseSessionLine(LABELLED);
    expect(identity(session as never)).toEqual({
      project: "typed-router",
      workspace: "effect-ts-v4-endpoint-poc",
      kind: "agent",
    });
  });

  test("falls back to the name when a session has no labels yet", () => {
    // There is a window between a session existing and its labels being set,
    // during which the name is all there is.
    expect(identity(parseSessionLine(OBSIDIAN) as never)).toEqual({
      project: "Obsidian_Vault",
      workspace: "default",
      kind: "agent",
    });
  });

  test("the fallback is lossy exactly where the labels are not", () => {
    const session = parseSessionLine(LABELLED);
    expect(session).toBeDefined();
    const fromName = identity({ ...(session as Session), labels: {} });
    // The name was shortened to fit, so reading it back gives the shortened
    // spelling — which matches no workspace.
    expect(fromName?.workspace).toBe("effect-ts-v4-endpo-afe7");
    expect(fromName?.workspace).not.toBe("effect-ts-v4-endpoint-poc");
  });

  test("reports a session awp did not create as not ours", () => {
    expect(identity(parseSessionLine(t("name=mysession", "pid=1")) as never)).toBeUndefined();
  });
});

describe("requireName", () => {
  test("refuses an empty name rather than letting zmx decide", () => {
    expect(requireName("kill", "")).toContain("no name given");
    expect(requireName("kill", "   ")).toContain("no name given");
  });

  test("allows a real one", () => {
    expect(requireName("kill", "awp.a.b.agent")).toBeUndefined();
  });
});

/** A session at a given pid, with everything else at the parser's defaults. */
const at = (pid: number): Session => ({
  name: "s",
  pid,
  clients: 0,
  startDir: "",
  ended: false,
  busy: true,
  taskEnded: false,
  exitCode: 0,
  created: undefined,
  cmd: "",
  labels: {},
});

describe("withProcesses", () => {
  // Real `ps -eo pid=,ppid=,comm=` rows, captured 2026-08-28 while one agent
  // was mid-task and another idle. The two session shapes are both here and
  // they are the whole reason the rule has two clauses:
  //
  //   357    -bash    ← `zmx run -d claude`: a shell, told to run something
  //   18057  claude   ← `zmx attach <name> claude`: no shell at all
  const TABLE = parseProcessTable(
    [
      "95732 18057 /Users/acohen/.local/bin/claude",
      "18057 18056 claude",
      "  357   356 -bash",
      "34392   357 claude",
      "  900   899 -bash",
      "  700   699 vim",
    ].join("\n"),
  );

  test("a session whose process is gone has ended", () => {
    const [seen] = withProcesses([at(999_999)], TABLE);
    expect(seen?.ended).toBe(true);
    expect(seen?.busy).toBe(false);
  });

  test("a shell with something running in it is alive and busy", () => {
    // The session that was reported as dead. It is a `-bash` — so the shell
    // clause alone would call it idle — with a claude child, which is the
    // clause that answers correctly.
    const [seen] = withProcesses([at(357)], TABLE);
    expect(seen?.ended).toBe(false);
    expect(seen?.busy).toBe(true);
  });

  test("a shell at a prompt is alive and not busy", () => {
    // The one state `start` is allowed to run a command in, and the only
    // reason `busy` is a separate question from `ended`.
    const [seen] = withProcesses([at(900)], TABLE);
    expect(seen?.ended).toBe(false);
    expect(seen?.busy).toBe(false);
  });

  test("a session that is the program, with no shell, is busy", () => {
    // `zmx attach <name> vim` leaves no shell — the session's own process is
    // the program, and it has no children of its own. A child-only rule calls
    // this idle, and `start` would then type a command into a running editor,
    // which is precisely what the guard exists to prevent.
    const [seen] = withProcesses([at(700)], TABLE);
    expect(seen?.busy).toBe(true);
  });

  test("an agent attached directly, with a helper of its own, is busy too", () => {
    // The other shape of the same thing: claude as the session's own process,
    // which does have children. Both clauses agree here, and that is fine —
    // the two tests above are the ones that separate them.
    const [seen] = withProcesses([at(18_057)], TABLE);
    expect(seen?.busy).toBe(true);
  });

  test("zmx's own report about the last task is left alone", () => {
    // Kept, and kept separate. It is a true statement about something else —
    // whether the last thing `zmx run` typed in has finished — and the bug was
    // reading it as a statement about the session.
    const line =
      "  name=awp.awp.review-inbox.agent\tpid=357\tclients=1\tstart_dir=/w\t" +
      "ended=2\texit_code=1\tawp_kind=agent";
    const parsed = parseSessionLine(line);
    const [seen] = withProcesses([parsed as Session], TABLE);
    expect(seen?.taskEnded).toBe(true);
    expect(seen?.exitCode).toBe(1);
    // And the session is not over, which is the whole finding.
    expect(seen?.ended).toBe(false);
  });
});

describe("parseProcessTable", () => {
  test("keeps a command containing a space", () => {
    // macOS reports a path here, and a path can contain a space. Splitting
    // into exactly three fields would drop the tail and misread the name.
    const [row] = parseProcessTable("42 1 /Applications/My App/bin/thing");
    expect(row).toEqual({ pid: 42, ppid: 1, comm: "/Applications/My App/bin/thing" });
  });

  test("skips a header or anything unreadable", () => {
    expect(parseProcessTable("  PID  PPID COMM\n\n7 1 bash")).toEqual([
      { pid: 7, ppid: 1, comm: "bash" },
    ]);
  });
});
