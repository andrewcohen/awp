import { describe, expect, test } from "vitest";
import { type Session, identity, isLive } from "./multiplexer.js";
import { parseSessionLine, parseSessionList, requireName } from "./zmx-parse.js";

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
  test("a listed session is not necessarily a running one", () => {
    // zmx keeps a session listed after its command exits so the output can
    // still be read. Attaching to one renders a dead program's last screen.
    const dead = parseSessionLine(t("name=x", "pid=1", "ended=true", "exit_code=2"));
    expect(dead?.ended).toBe(true);
    expect(dead?.exitCode).toBe(2);
    expect(isLive(dead as never)).toBe(false);
  });

  test("a session without the field is live", () => {
    expect(isLive(parseSessionLine(OBSIDIAN) as never)).toBe(true);
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
