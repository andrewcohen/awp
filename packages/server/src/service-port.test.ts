import { describe, expect, it } from "vitest";
import { lowestPort } from "./service-port";

// Captured from a real `lsof -nP -a -p <tree> -iTCP -sTCP:LISTEN` on macOS,
// against a server started the way a service is — a shell running `bun`, so the
// listener is a grandchild of the pid the session reports. A fixture written
// from the man page would agree with the man page.
const real = `COMMAND   PID   USER   FD   TYPE             DEVICE SIZE/OFF NODE NAME
bun     96830 acohen    5u  IPv6 0x54780948898327a4      0t0  TCP *:51999 (LISTEN)`;

describe("the port a service bound", () => {
  it("is read off lsof's last column", () => {
    expect(lowestPort(real)).toBe(51999);
  });

  it("is nothing when nothing is listening", () => {
    expect(lowestPort("")).toBeUndefined();
    // The header alone, which is what lsof prints when the selection matched
    // processes but none of them hold a listening socket.
    expect(lowestPort(real.split("\n")[0] ?? "")).toBeUndefined();
  });

  // Three spellings of the same thing, and a parser that knew about only one of
  // them would be right on whichever machine it was written on. Taking the tail
  // after the final colon does not need to know which it is looking at.
  it.each([
    ["a wildcard bind", "bun 1 me 5u IPv6 0x0 0t0 TCP *:5273 (LISTEN)", 5273],
    ["v4 loopback", "bun 1 me 5u IPv4 0x0 0t0 TCP 127.0.0.1:5273 (LISTEN)", 5273],
    ["v6 loopback", "bun 1 me 5u IPv6 0x0 0t0 TCP [::1]:5273 (LISTEN)", 5273],
  ])("reads %s", (_what, line, want) => {
    expect(lowestPort(line)).toBe(want);
  });

  /**
   * A dev server binds its HTTP port and an ephemeral one beside it for hot
   * reload, and the low number is the one somebody wants to click. Ordered
   * with the ephemeral one first, because that is the order lsof gave them —
   * taking the first row would have looked right against a one-port fixture.
   */
  it("is the lowest of several, not the first", () => {
    const two = [
      "bun 1 me 14u IPv4 0x0 0t0 TCP 127.0.0.1:55728 (LISTEN)",
      "bun 1 me  5u IPv6 0x0 0t0 TCP *:5273 (LISTEN)",
    ].join("\n");
    expect(lowestPort(two)).toBe(5273);
  });

  /**
   * The failure this parse has to survive is not a malformed line — it is
   * `lsof` being asked wrongly.
   *
   * Without `-a`, lsof ORs its selectors, so `-p <pids> -iTCP` answers with
   * every listening socket on the machine. That output parses perfectly and
   * reports another application's port as this service's. Nothing in a parser
   * can catch it, which is why the flag is in `portOf` with the reason beside
   * it rather than being something a reader is expected to know.
   *
   * What is asserted here is only that a row belonging to another process is
   * not distinguishable — the parse cannot save us, and pretending otherwise
   * with a test that filtered by COMMAND would be worse than none.
   */
  it("cannot tell somebody else's socket from ours", () => {
    expect(lowestPort("workerd 98026 me 14u IPv4 0x0 0t0 TCP 127.0.0.1:55728 (LISTEN)")).toBe(
      55728,
    );
  });
});
