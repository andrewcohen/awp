import { describe, expect, test } from "vitest";
import { agentEnv } from "./agent-env";
import { adapterEnv } from "./chat";

describe("a chat's adapter environment", () => {
  // Measured: a daemon started from a shell inside one workspace's session
  // handed that workspace to every chat it opened, and their status hooks
  // reported as it.
  const daemon = { PATH: "/usr/bin", AWP_WORKSPACE: "lantern", AWP_REPO_ROOT: "/repos/lantern" };

  test("is the chat's workspace, not the one the daemon was launched from", () => {
    const env = adapterEnv("/bin/claude", agentEnv("thicket", "/repos/thicket"), daemon);
    expect(env["AWP_WORKSPACE"]).toBe("thicket");
    expect(env["AWP_REPO_ROOT"]).toBe("/repos/thicket");
    expect(env["PATH"]).toBe("/usr/bin");
  });

  test("an unresolved root is empty, which is present and not inherited", () => {
    const env = adapterEnv("/bin/claude", agentEnv("", ""), daemon);
    expect(env["AWP_WORKSPACE"]).toBe("");
    expect(env["AWP_REPO_ROOT"]).toBe("");
  });

  test("keeps the executable, set after the markers childEnv empties", () => {
    const env = adapterEnv("/bin/claude", {}, { CLAUDE_CODE_EXECUTABLE: "/elsewhere" });
    expect(env["CLAUDE_CODE_EXECUTABLE"]).toBe("/bin/claude");
  });
});
