import { describe, expect, it } from "vitest";
import { type Repairable, looksMine, repairPrompt } from "./repair";

// The two tones, and the rule that a reviewer is not handed the author's chores.
// Every case here is a wording decision the archive got wrong first — see the
// file's own notes — so what is pinned is the *shape* of the sentence rather
// than its exact words.

const pr = (over: Partial<Repairable>): Repairable => ({
  number: 412,
  url: "https://example.invalid/pull/412",
  state: "open",
  headRef: "andrew/lantern",
  ci: "passing",
  review: "none",
  mergeState: "clean",
  hasReviewComments: false,
  mine: false,
  reviewRequested: false,
  reviewRerequested: false,
  ...over,
});

describe("nothing to repair", () => {
  it("an open pull request with nothing wrong says nothing", () => {
    // Empty is an answer, and the caller has to treat it as "nothing to repair"
    // rather than sending a blank message.
    expect(repairPrompt(pr({ mine: true }), { mine: true, moved: false })).toBe("");
  });

  it("a merged one says nothing either", () => {
    expect(repairPrompt(pr({ state: "merged", ci: "failing" }), { mine: true, moved: false })).toBe(
      "",
    );
  });
});

describe("your own pull request: fix it", () => {
  it("names the problem and asks for a push", () => {
    const prompt = repairPrompt(pr({ ci: "failing", mine: true }), { mine: true, moved: false });

    expect(prompt).toContain("PR #412");
    expect(prompt).toContain("failing CI checks");
    expect(prompt).toContain("push");
    // The reminder that a push can dismiss an approval under branch protection,
    // leaving the PR silently blocked.
    expect(prompt).toContain("re-request review");
  });

  it("review feedback makes the whole prompt propose-first", () => {
    // The gate exists because an agent told to fix CI *and* answer a reviewer
    // should not do half of it unprompted.
    const prompt = repairPrompt(pr({ ci: "failing", hasReviewComments: true, mine: true }), {
      mine: true,
      moved: false,
    });

    expect(prompt).toContain("wait for my approval");
    expect(prompt).toContain("failing CI checks");
  });

  it("an approved PR with comments asks which points are still open", () => {
    // Approving and still wanting something are not exclusive, and the approval
    // means some of it may already be addressed — so the honest instruction is
    // to check each point rather than to assume all of them.
    const prompt = repairPrompt(pr({ review: "approved", hasReviewComments: true, mine: true }), {
      mine: true,
      moved: false,
    });

    expect(prompt).toContain("already approved");
    expect(prompt).toContain("still open");
  });
});

describe("somebody else's pull request: look at it", () => {
  it("forbids touching the branch", () => {
    const prompt = repairPrompt(pr({ ci: "failing" }), { mine: false, moved: false });

    expect(prompt).toContain("You are reviewing this PR");
    expect(prompt).toContain("Do NOT modify files");
    expect(prompt).toContain("report");
    // And it does not ask for a push, which is the whole distinction.
    expect(prompt).not.toContain("then push the fix");
  });

  it("drops the author's chores rather than translating them", () => {
    // The recorded failure: a reviewer was asked to report how far behind its
    // base someone else's branch was, which is the author's rebase and nothing a
    // reviewer can act on.
    const prompt = repairPrompt(pr({ mergeState: "behind" }), { mine: false, moved: false });

    expect(prompt).toBe("");
  });

  it("a re-request asks about the delta, not the whole diff", () => {
    const prompt = repairPrompt(pr({ reviewRequested: true, reviewRerequested: true }), {
      mine: false,
      moved: false,
    });

    expect(prompt).toContain("RE-request");
    expect(prompt).toContain("since your last pass");
    // A local read, because a raw patch cannot be opened, chased or run.
    expect(prompt).toContain("jj new andrew/lantern@origin");
  });

  it("a request for your review is not offered on your own PR", () => {
    // A request for *your* review cannot sit on your own pull request, so on one
    // that looks yours the signal is noise.
    expect(
      repairPrompt(pr({ reviewRequested: true, mine: true }), { mine: true, moved: false }),
    ).toBe("");
  });
});

describe("a checkout that is behind", () => {
  it("is an issue in both tones, because it is about the local copy", () => {
    // Fetching and re-anchoring is safe whoever owns the branch, which is why
    // this one survives the reviewer filter.
    for (const mine of [true, false]) {
      const prompt = repairPrompt(pr({ mine }), { mine, moved: true });
      expect(prompt).toContain("jj git fetch");
      expect(prompt).toContain("andrew/lantern@origin");
    }
  });
});

describe("whose branch it looks like", () => {
  it("the prefix decides, and no prefix means yours", () => {
    expect(looksMine(pr({}), "andrew")).toBe(true);
    expect(looksMine(pr({ headRef: "someone/lantern" }), "andrew")).toBe(false);
    // With nothing to compare against the honest default is that it is yours:
    // the cost of being wrong that way is a prompt that offers to fix rather
    // than one that quietly declines to.
    expect(looksMine(pr({ headRef: "someone/lantern" }), undefined)).toBe(true);
  });
});
