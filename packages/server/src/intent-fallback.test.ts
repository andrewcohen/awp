import { describe, expect, it } from "vitest";
import { nameFrom } from "./intent";

// The name made here when the model cannot be reached.
//
// Kept out of intent.test.ts because that file is about the model call — the
// flags, the fishing of JSON out of prose, the timeout. This is a pure function
// over a sentence, and the two have nothing to say to each other.

describe("naming a workspace without the model", () => {
  it("takes the first four words", () => {
    expect(nameFrom("add tiered discounts to checkout").name).toBe("add-tiered-discounts-to");
  });

  it("keeps digits and drops punctuation", () => {
    // A sentence naming a path or a status code is the ordinary case, and both
    // arrive full of characters a directory name cannot hold.
    expect(nameFrom("fix the 500 on /api/orders").name).toBe("fix-the-500-on");
  });

  it("answers something for a sentence with no words in it", () => {
    // `workspace` rather than a random string: the second time it happens, the
    // collision is the useful signal.
    expect(nameFrom("   ").name).toBe("workspace");
    expect(nameFrom("!!! ??? ...").name).toBe("workspace");
  });

  it("is deterministic, because the step is safe to run twice", () => {
    // A retry after a failed later step must produce the same name, or the
    // second attempt builds a second workspace beside the first.
    const said = "rewrite the importer";
    expect(nameFrom(said)).toEqual(nameFrom(said));
  });

  it("keeps the whole sentence as the label", () => {
    // The name is a path segment and part of a session name, so it is cut
    // short. A label has room, and losing the sentence there would be losing
    // it for nothing.
    const said = "add tiered discounts to checkout for the enterprise plan";
    expect(nameFrom(said).label).toBe(said);
    expect(nameFrom(said).prompt).toBe(said);
  });

  it("trims the label but not into nothing", () => {
    expect(nameFrom("  tidy up  ").label).toBe("tidy up");
    expect(nameFrom("   ").label).toBe("workspace");
  });

  it("lowercases, because a name is an address and case is not part of it", () => {
    expect(nameFrom("Fix The Importer").name).toBe("fix-the-importer");
  });
});
