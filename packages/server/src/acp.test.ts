import { describe, expect, it } from "vitest";
import { adapterPath, chunkText, parseMessage } from "./acp";

describe("parseMessage", () => {
  it("reads one line of the protocol", () => {
    expect(parseMessage('{"jsonrpc":"2.0","id":1,"result":{"ok":true}}')).toEqual({
      jsonrpc: "2.0",
      id: 1,
      result: { ok: true },
    });
  });

  it("ignores a blank line", () => {
    expect(parseMessage("   ")).toBeUndefined();
  });

  // The adapter writes its own diagnostics to stderr, but a stray line on
  // stdout would otherwise take the whole conversation down with a parse
  // error. Skipping it costs nothing: a message that matters is answered by
  // its id, and an unrecognised one was never going to be.
  it("ignores a line that is not JSON", () => {
    expect(parseMessage("Debug: connected to the gateway")).toBeUndefined();
  });

  it("ignores JSON that is not an object", () => {
    expect(parseMessage("42")).toBeUndefined();
    expect(parseMessage("null")).toBeUndefined();
  });
});

const update = (payload: Record<string, unknown>) => ({
  method: "session/update",
  params: { update: payload },
});

describe("chunkText", () => {
  it("takes the text of an agent message", () => {
    expect(
      chunkText(
        update({ sessionUpdate: "agent_message_chunk", content: { type: "text", text: "hi" } }),
      ),
    ).toBe("hi");
  });

  // The whole reason the answer is not "every text on the update channel".
  // A thought is the model reasoning aloud, and appending it to the answer
  // would put reasoning inside the JSON object being fished out of it.
  it("leaves a thought alone", () => {
    expect(
      chunkText(
        update({ sessionUpdate: "agent_thought_chunk", content: { type: "text", text: "hmm" } }),
      ),
    ).toBeUndefined();
  });

  it("leaves a tool call alone", () => {
    expect(chunkText(update({ sessionUpdate: "tool_call", title: "Read" }))).toBeUndefined();
  });

  it("ignores content that is not text", () => {
    expect(
      chunkText(update({ sessionUpdate: "agent_message_chunk", content: { type: "image" } })),
    ).toBeUndefined();
  });

  it("ignores a message that is not an update", () => {
    expect(chunkText({ id: 1, result: {} })).toBeUndefined();
  });
});

describe("adapterPath", () => {
  // A path into node_modules is right until bun hoists it somewhere else, so
  // it is resolved through the package. This asserts the resolution works at
  // all — a throw here is the dependency having moved or gone.
  it("resolves the adapter's executable", () => {
    expect(adapterPath()).toMatch(/claude-agent-acp\/dist\/index\.js$/u);
  });
});
