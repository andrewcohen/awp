import { describe, expect, it } from "vitest";
import type { Job } from "@awp-kit/jobs";
import { finishedKey } from "./refresh";

const job = (id: string, status: Job["status"]): Job =>
  ({ id, status, createdAt: new Date(0) }) as Job;

describe("finishedKey", () => {
  it("ignores a job that is still going", () => {
    expect(finishedKey([job("a", "queued"), job("b", "running")])).toBe("");
  });

  it("changes when a job stops", () => {
    const before = finishedKey([job("a", "running")]);
    expect(finishedKey([job("a", "succeeded")])).not.toBe(before);
  });

  it("does not depend on the order the jobs arrive in", () => {
    // The listing and the change feed disagree about order, so an
    // order-dependent key would re-read the sessions on nothing at all.
    expect(finishedKey([job("a", "succeeded"), job("b", "failed")])).toBe(
      finishedKey([job("b", "failed"), job("a", "succeeded")]),
    );
  });

  it("a clear then a completion is not the same as before the clear", () => {
    // The whole reason this is not a count. Clearing the panel deletes
    // terminal rows, so a count falls from 2 to 0 and the next job to finish
    // returns it to 1 — a number it has already been, and therefore no change
    // and no refresh, for the one job somebody is waiting on.
    const two = [job("a", "succeeded"), job("b", "succeeded")];
    const afterAClear = [job("c", "succeeded")];
    // A count says these two are the same state, which is the bug.
    expect(afterAClear.length).toBe(1);
    expect(finishedKey([job("a", "succeeded")])).not.toBe(finishedKey(afterAClear));
    expect(finishedKey(two)).not.toBe(finishedKey(afterAClear));
  });
});
