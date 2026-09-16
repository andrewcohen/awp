import { describe, expect, it } from "vitest";
import type { Job } from "@awp-kit/jobs";
import { progressKey } from "./refresh";

const job = (id: string, status: Job["status"], done: ReadonlyArray<string> = []): Job =>
  ({ id, status, done, createdAt: new Date(0) }) as Job;

describe("progressKey", () => {
  it("moves when a job completes a step, without the job stopping", () => {
    // The whole bug. A create job's session lands at step 3 and its thread
    // claim at step 4, and on a chat-face create the job then sits in `brief`
    // for the length of the agent's first turn. Keyed on completion, the
    // sidebar drew "nothing yet" over a thread whose workspace was on disk.
    const atSession = progressKey([job("a", "running", ["workspace", "bookmark", "session"])]);
    const atClaim = progressKey([
      job("a", "running", ["workspace", "bookmark", "session", "claim"]),
    ]);
    expect(atClaim).not.toBe(atSession);
  });

  it("still moves when a job stops", () => {
    const before = progressKey([job("a", "running", ["one"])]);
    expect(progressKey([job("a", "succeeded", ["one"])])).not.toBe(before);
  });

  it("does not move when nothing about the jobs changed", () => {
    // A re-read per render would be a socket round trip per render.
    const rows = [job("a", "running", ["one"]), job("b", "queued")];
    expect(progressKey(rows)).toBe(progressKey([...rows]));
  });

  it("does not depend on the order the jobs arrive in", () => {
    // The listing and the change feed disagree about order, so an
    // order-dependent key would re-read the sessions on nothing at all.
    expect(progressKey([job("a", "succeeded"), job("b", "failed")])).toBe(
      progressKey([job("b", "failed"), job("a", "succeeded")]),
    );
  });

  it("a clear then a completion is not the same as before the clear", () => {
    // Why this is not a count. Clearing the panel deletes terminal rows, so a
    // count falls from 2 to 0 and the next job to finish returns it to 1 — a
    // number it has already been, and therefore no refresh for the one job
    // somebody is waiting on.
    const afterAClear = [job("c", "succeeded")];
    expect(afterAClear.length).toBe(1);
    expect(progressKey([job("a", "succeeded")])).not.toBe(progressKey(afterAClear));
  });

  it("a record with no steps yet is still a row", () => {
    // `done` is absent on a job the moment it is enqueued, and a key that
    // threw there would take the refresh down with it.
    expect(progressKey([{ id: "a", status: "queued" } as Job])).toBe("a:queued:0");
  });
});
