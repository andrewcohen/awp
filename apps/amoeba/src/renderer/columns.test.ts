import { describe, expect, it } from "vitest";
import { ACCESSORY, AGENT_MIN, DIVIDER, SIDEBAR, fitColumns } from "./columns";

const agentWidth = (container: number, c: { sidebar: number; accessory: number }) =>
  container - 2 * DIVIDER - c.sidebar - c.accessory;

describe("fitColumns", () => {
  it("gives both columns what they asked for when there is room", () => {
    expect(fitColumns(1600, { sidebar: 220, accessory: 280 })).toEqual({
      sidebar: 220,
      accessory: 280,
    });
  });

  it("holds each column to its floor", () => {
    expect(fitColumns(4000, { sidebar: 10, accessory: 10 })).toEqual({
      sidebar: SIDEBAR.min,
      accessory: ACCESSORY.min,
    });
  });

  // No maximum. A column grows until its neighbours hit their own floors, and
  // that is the only thing stopping it.
  it("lets a column grow until the agent and its neighbour are at their floors", () => {
    const got = fitColumns(4000, { sidebar: 160, accessory: 9999 });
    expect(got.accessory).toBe(4000 - 2 * DIVIDER - AGENT_MIN - SIDEBAR.min);
    expect(agentWidth(4000, got)).toBe(AGENT_MIN);
  });

  it("takes from the accessory before the sidebar", () => {
    // Both asked for 400, and the budget is 150 short. The accessory can absorb
    // all of that on its own — it has 200 to give before it hits its floor — so
    // the sidebar should not be touched at all.
    const container = 2 * DIVIDER + AGENT_MIN + 650;
    const got = fitColumns(container, { sidebar: 400, accessory: 400 });
    expect(got.sidebar).toBe(400);
    expect(got.accessory).toBe(250);
  });

  it("only starves the sidebar once the accessory is at its floor", () => {
    const container = 2 * DIVIDER + AGENT_MIN + SIDEBAR.min + ACCESSORY.min + 50;
    const got = fitColumns(container, { sidebar: 480, accessory: 640 });
    expect(got.accessory).toBe(ACCESSORY.min);
    expect(got.sidebar).toBe(SIDEBAR.min + 50);
  });

  // The property that matters: a terminal asked to be zero columns wide is not
  // a small terminal. Everything above exists to keep this true.
  it("holds the agent column at its floor for every width that can afford one", () => {
    const affordable = 2 * DIVIDER + AGENT_MIN + SIDEBAR.min + ACCESSORY.min;
    for (let container = affordable; container <= 3000; container += 7) {
      const got = fitColumns(container, { sidebar: 220, accessory: 280 });
      expect(agentWidth(container, got)).toBeGreaterThanOrEqual(AGENT_MIN);
    }
  });

  it("never lets the columns exceed the window, at any width", () => {
    for (let container = 0; container <= 3000; container += 7) {
      const got = fitColumns(container, { sidebar: 480, accessory: 640 });
      expect(got.sidebar + got.accessory + 2 * DIVIDER).toBeLessThanOrEqual(
        Math.max(container, 2 * DIVIDER),
      );
      expect(got.sidebar).toBeGreaterThanOrEqual(0);
      expect(got.accessory).toBeGreaterThanOrEqual(0);
    }
  });

  it("does not shrink a column because its neighbour was asked to grow", () => {
    const wide = fitColumns(2000, { sidebar: 300, accessory: 280 });
    const wider = fitColumns(2000, { sidebar: 400, accessory: 280 });
    expect(wider.sidebar).toBeGreaterThan(wide.sidebar);
    expect(wider.accessory).toBe(wide.accessory);
  });

  it("returns a squeezed column to where its owner put it", () => {
    const want = { sidebar: 420, accessory: 600 };
    expect(fitColumns(900, want).accessory).toBeLessThan(want.accessory);
    expect(fitColumns(2200, want)).toEqual(want);
  });
});
