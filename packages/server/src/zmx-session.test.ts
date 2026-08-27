import { describe, expect, test } from "vitest";
import {
  InsideZmxSessionError,
  currentZmxSession,
  insideZmxSession,
  requireOutsideZmxSession,
  childEnv,
} from "./zmx-session";

// None of this touches zmx. That is the point of the split: the rules about
// when it is safe to talk to zmx are ordinary logic and get ordinary tests,
// and only fidelity against a real daemon needs the refusal.

describe("childEnv", () => {
  // Present and empty, not absent — and the difference is the whole point.
  //
  // This test asserted absence for weeks and passed, while the guard did
  // nothing: bun-pty hands its pairs to a Rust Command, which inherits the
  // parent environment and applies them on top, so a key left out is a key left
  // alone. Every `zmx attach` the daemon spawned saw the marker and switched
  // the calling client instead. See childEnv, and probe/child-env.ts, which
  // checks what a child actually receives rather than what this function
  // returns — the gap between those two is where it hid.
  test("empties ZMX_SESSION rather than omitting it", () => {
    const env = childEnv({ PATH: "/bin", ZMX_SESSION: "awp.some.session" });
    expect(env.ZMX_SESSION).toBe("");
    expect(env.PATH).toBe("/bin");
  });

  test("drops undefined values rather than passing them through", () => {
    // process.env is typed with undefined values; a child env must not carry
    // keys with no value, which spawn would stringify as "undefined".
    const env = childEnv({ PATH: "/bin", EMPTY: undefined });
    expect(env).not.toHaveProperty("EMPTY");
  });

  // The marker is set even when the parent had none, and that is deliberate.
  // The daemon may be started outside a session and re-exec itself, or inherit
  // one later; an env that always says "no session" cannot be wrong about it.
  test("says there is no session even when the parent had none", () => {
    expect(childEnv({ PATH: "/bin", TERM: "xterm" })).toEqual({
      PATH: "/bin",
      TERM: "xterm",
      ZMX_SESSION: "",
    });
  });
});

// These drive process.env directly rather than passing a session in, because
// reading the ambient environment is the guard's whole job — a version taking
// the session as an argument would be trivially callable with the wrong one,
// which is the mistake it exists to prevent.
const withSession = <A>(session: string | undefined, run: () => A): A => {
  const before = process.env.ZMX_SESSION;
  if (session === undefined) {
    delete process.env.ZMX_SESSION;
  } else {
    process.env.ZMX_SESSION = session;
  }
  try {
    return run();
  } finally {
    if (before === undefined) {
      delete process.env.ZMX_SESSION;
    } else {
      process.env.ZMX_SESSION = before;
    }
  }
};

describe("requireOutsideZmxSession", () => {
  test("throws inside a session, naming it", () => {
    withSession("awp.awp.awp-kit-amoeba.agent", () => {
      expect(() => requireOutsideZmxSession()).toThrow(InsideZmxSessionError);
      expect(() => requireOutsideZmxSession()).toThrow(/awp\.awp\.awp-kit-amoeba\.agent/u);
    });
  });

  test("the message says what to do, not just what went wrong", () => {
    withSession("some.session", () => {
      expect(() => requireOutsideZmxSession()).toThrow(/plain terminal outside zmx/u);
    });
  });

  test("passes outside a session", () => {
    withSession(undefined, () => {
      expect(() => requireOutsideZmxSession()).not.toThrow();
    });
  });

  test("reports the session it found", () => {
    withSession("named.session", () => {
      expect(currentZmxSession()).toBe("named.session");
      expect(insideZmxSession()).toBe(true);
    });
    withSession(undefined, () => {
      expect(currentZmxSession()).toBeUndefined();
      expect(insideZmxSession()).toBe(false);
    });
  });
});
