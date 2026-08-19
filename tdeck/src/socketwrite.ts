// Writing every byte to a socket, in order, including the ones it would not
// take the first time.
//
// `Socket.write` returns how much it accepted. When the kernel buffer is full
// that is less than the chunk — and a caller that ignores the return value has
// silently dropped the middle of its stream. For a protocol carried as one JSON
// object per line, the symptom is a parse error ("Expected '}'", "Unterminated
// string") on a line that was perfectly well-formed when it was written, at
// whatever point traffic first got heavy enough to fill a buffer. Nothing about
// the error names the socket.
//
// So the remainder is queued and re-offered on `drain`. Both directions need
// this: the daemon writing agent output to a client, and a client writing
// prompts to the daemon.

import type { Socket } from "bun";

export class SocketWriter {
  // Unwritten bytes, oldest first. Empty in the ordinary case — this only fills
  // when the socket has pushed back.
  private queue: Uint8Array[] = [];

  constructor(private readonly socket: Socket<undefined>) {}

  write(chunk: Uint8Array): void {
    // Anything already queued has to go first, or the stream is reordered.
    if (this.queue.length > 0) {
      this.queue.push(new Uint8Array(chunk));
      return;
    }
    const written = this.socket.write(chunk);
    if (written < chunk.length) {
      // Copied: the caller's buffer may be reused before drain arrives.
      this.queue.push(new Uint8Array(chunk.subarray(written)));
    }
  }

  // Call from the socket's `drain` handler.
  drain(): void {
    while (this.queue.length > 0) {
      const head = this.queue[0]!;
      const written = this.socket.write(head);
      if (written < head.length) {
        this.queue[0] = head.subarray(written);
        return;
      }
      this.queue.shift();
    }
  }
}
