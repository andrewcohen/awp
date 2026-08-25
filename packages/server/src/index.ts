// The daemon. Owns ptys, hosts zmx sessions, serves the protocol.
//
// zmx stays the session owner, and that is not free: `zmx attach` is a CLI
// hosted in a pty, with no socket protocol to speak instead — so this package
// needs a real pty and cannot delegate its way out.
//
// Two rules travel with it, both learned the expensive way:
//
//   - `zmx attach` branches on ZMX_SESSION. From inside a session it switches
//     the *calling* client rather than making a new one, which steals the
//     terminal awp was launched from. The child's environment must not carry
//     that marker.
//   - A 64KB read can split a UTF-8 sequence across two messages, and something
//     has to hold half of one. bun-pty does: one TextDecoder across reads, in
//     streaming mode. That is why this path can be string-native, and why
//     gdeck's base64 transport does not carry over.

export const serverVersion = 0;

export * as attachment from "./attachment";
export * as handlers from "./handlers";
export * as multiplexer from "./multiplexer";
export * as pty from "./pty";
export * as sessions from "./sessions";
export * as zmx from "./zmx";
