// What the backend sends the page.
//
// Deliberately not ACP's own vocabulary. The protocol's update union is a
// streaming wire format — chunks, ids, statuses — and a view that renders it
// directly ends up knowing about ACP, which is the thing that would have to
// change to host a second kind of agent. So the fold happens here and the page
// stays a renderer.
//
// This is the flat, chunk-shaped version. Folding these into whole turns (the
// shape ChatView already renders) is its own unit; keeping the two apart means
// the fold can be written and tested against a stream that already works.

export type UiEvent =
  | { kind: "user"; text: string }
  | { kind: "text"; text: string }
  | { kind: "thought"; text: string }
  | { kind: "tool"; id: string; title: string; status: string }
  | { kind: "tool_update"; id: string; status?: string; content?: unknown }
  | { kind: "plan"; entries: unknown[] }
  | { kind: "permission"; title: string; options: PermissionOption[] }
  | { kind: "permission_resolved"; optionId: string }
  | { kind: "mode"; modeId: string }
  | { kind: "usage"; used: number; size: number; cost?: number }
  | { kind: "title"; title: string }
  | { kind: "done"; stopReason: string }
  | { kind: "error"; message: string }
  | { kind: "other"; update: unknown };

export type PermissionOption = { id: string; name: string; kind?: string };

export type Command = { name: string; description: string };

// A log plus its readers.
//
// The log exists because a reload should show the conversation rather than a
// blank page, and because an agent works for minutes at a time — a viewer that
// only sees events arriving after it connected would miss most of them. It is
// not persistence: the adapter owns the real conversation, this is only what
// has been shown.
export class EventLog {
  private readonly shown: UiEvent[] = [];
  private readonly viewers = new Set<(line: string) => void>();

  emit(event: UiEvent): void {
    this.shown.push(event);
    const line = `data: ${JSON.stringify(event)}\n\n`;
    for (const write of this.viewers) write(line);
  }

  // Returns an unsubscribe, so the caller does not have to hold onto the
  // identity of the function it registered.
  subscribe(write: (line: string) => void): () => void {
    for (const event of this.shown) write(`data: ${JSON.stringify(event)}\n\n`);
    this.viewers.add(write);
    return () => void this.viewers.delete(write);
  }
}
