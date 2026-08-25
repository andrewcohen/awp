import { describe, expect, test } from "vitest";
import {
  InsideZmxSessionError,
  currentZmxSession,
  insideZmxSession,
  requireOutsideZmxSession,
  zmxChildEnv,
} from "./zmx-session.js";

// None of this touches zmx. That is the point of the split: the rules about
// when it is safe to talk to zmx are ordinary logic and get ordinary tests,
// and only fidelity against a real daemon needs the refusal.

describe("zmxChildEnv", () => {
  test("drops ZMX_SESSION", () => {
    const env = zmxChildEnv({ PATH: "/bin", ZMX_SESSION: "awp.some.session" });
    expect(env).not.toHaveProperty("ZMX_SESSION");
    expect(env.PATH).toBe("/bin");
  });

  test("drops undefined values rather than passing them through", () => {
    // process.env is typed with undefined values; a child env must not carry
    // keys with no value, which spawn would stringify as "undefined".
    const env = zmxChildEnv({ PATH: "/bin", EMPTY: undefined });
    expect(env).not.toHaveProperty("EMPTY");
  });

  test("leaves an env with no marker alone", () => {
    expect(zmxChildEnv({ PATH: "/bin", TERM: "xterm" })).toEqual({
      PATH: "/bin",
      TERM: "xterm",
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
