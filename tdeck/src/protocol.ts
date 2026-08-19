// The line protocol between the UI server and the agent daemon.
//
// This exists because of a wart with a cause: a turn is a JSON-RPC *request*,
// and its reply is addressed to whoever asked. When the ACP client lived in the
// UI server, restarting that server — which `bun --watch` does on every saved
// file — orphaned the reply. All of the agent's output still arrived, because
// the daemon buffered it, but the `done` never did, so a finished turn sat in
// the UI spinning forever.
//
// Buffering harder cannot fix that. The request has to be owned by something
// that does not die, which means the ACP client belongs in the daemon and the
// server becomes a subscriber. That is what this protocol carries.
//
// Deliberately not ACP. The daemon speaks ACP to the agent; what it offers here
// is tdeck's own vocabulary — the same one the browser sees — so the UI server
// stays a translator of HTTP and nothing more.

import type { UiEvent } from "./events.ts";

export type Command =
  | { cmd: "sessions" }
  | { cmd: "open"; cwd: string }
  | { cmd: "resume"; sessionId: string; cwd: string; title: string }
  | { cmd: "close"; sessionId: string }
  | { cmd: "say"; session: string; text: string }
  | { cmd: "cancel"; session: string }
  | { cmd: "permit"; session: string; optionId: string }
  | { cmd: "mode"; session: string; modeId: string }
  | { cmd: "config"; session: string; configId: string; value: string | boolean }
  | { cmd: "history"; cwd: string }
  | { cmd: "commands" }
  // Replays what the conversation has already shown, then follows it. The
  // replay is why a subscription is a command rather than a filter applied to a
  // firehose: a page that has just loaded needs the conversation, not the next
  // token of it.
  | { cmd: "subscribe"; session: string }
  | { cmd: "unsubscribe"; session: string };

export type Request = Command & { id: number };

export type Reply =
  | { id: number; ok: true; data: unknown }
  | { id: number; ok: false; error: string };

export type Push =
  | { push: "event"; session: string; event: UiEvent }
  // Something about the set of conversations changed — one opened, closed, went
  // busy or idle. The server re-reads rather than the daemon describing the
  // delta, because the list is small and a delta is a second thing to get wrong.
  | { push: "sessions" };

export type Frame = Reply | Push;

export function isPush(frame: Frame): frame is Push {
  return "push" in frame;
}
