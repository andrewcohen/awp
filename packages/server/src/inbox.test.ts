import { describe, expect, it } from "vitest";
import type { PullRequest, Viewer } from "./github-parse";
import { bucketOf, inboxItems, reviewKey, reviewNumber, reviewOf, reviewWorkspace } from "./inbox";

// The precedence, pinned. Every clause of `bucketOf` is a decision somebody
// could reasonably make the other way, and none of them is visible by looking
// at the window — a row in the wrong section reads as a row.

const me: Viewer = { login: "me", teams: ["acme-corp/platform-team"] };

const pr = (over: Partial<PullRequest>): PullRequest => ({
  number: 1,
  headRef: "feature",
  headOid: "abc",
  baseRef: "main",
  title: "a change",
  author: "someone",
  url: "https://example.invalid/1",
  draft: false,
  ci: "passing",
  review: "none",
  mergeState: "clean",
  labels: [],
  requested: [],
  requestedTeams: [],
  reviewers: [],
  hasReviewComments: false,
  fork: undefined,
  ...over,
});

describe("which section a pull request lands in", () => {
  it("a review request wins over everything the PR itself says", () => {
    // Including its CI being red, and including it being yours. It names you,
    // and that is the whole rule.
    expect(bucketOf(pr({ requested: ["me"], ci: "failing", author: "me" }), me)).toBe(
      "needs-your-review",
    );
  });

  it("a team's request is a request", () => {
    // How whole repositories assign review, and the way a PR can be waiting on
    // somebody while naming nobody.
    expect(bucketOf(pr({ requestedTeams: ["acme-corp/platform-team"] }), me)).toBe(
      "needs-your-review",
    );
  });

  it("somebody else's PR that is not waiting on you is just open", () => {
    expect(bucketOf(pr({ author: "someone", review: "changes-requested" }), me)).toBe("other-open");
  });

  it("a draft of yours is yours, whatever its CI says", () => {
    // The clause that looks arbitrary and is not: a draft has not been
    // submitted, so its red CI is information rather than a task, and without
    // this every red draft would sit in "Needs action" beside work that is
    // actually blocked.
    expect(bucketOf(pr({ author: "me", draft: true, ci: "failing" }), me)).toBe("mine");
  });

  it("your PR with something to fix needs action", () => {
    const cases: ReadonlyArray<Partial<PullRequest>> = [
      { review: "changes-requested" },
      { ci: "failing" },
      { mergeState: "dirty" },
      { mergeState: "behind" },
    ];
    for (const one of cases) {
      expect(bucketOf(pr({ author: "me", ...one }), me)).toBe("needs-action");
    }
  });

  it("approved and green is ready to merge, and no checks counts as green", () => {
    expect(bucketOf(pr({ author: "me", review: "approved" }), me)).toBe("ready-to-merge");
    expect(bucketOf(pr({ author: "me", review: "approved", ci: "none" }), me)).toBe(
      "ready-to-merge",
    );
    // Pending is not green. A merge button pressed on a PR whose checks have
    // not finished is the thing this heading exists to stop.
    expect(bucketOf(pr({ author: "me", review: "approved", ci: "pending" }), me)).toBe("mine");
  });

  it("with no viewer, nothing is yours and nothing wants you", () => {
    // Not a defensive branch: it is what an unauthenticated `gh` produces, and
    // the reason the login is on the wire beside the rows.
    expect(bucketOf(pr({ author: "me", requested: ["me"] }), undefined)).toBe("other-open");
  });
});

/** Nothing has been reviewed yet, which is the state most rows are in. */
const none = () => undefined;

describe("the key a review's job is enqueued under", () => {
  it("round-trips the project and the number", () => {
    expect(reviewKey("thicket", 12)).toBe("review:thicket:12");
    expect(reviewOf(reviewKey("thicket", 12))).toEqual({ project: "thicket", number: 12 });
  });

  it("splits on the last colon, because a project name may hold one", () => {
    expect(reviewOf(reviewKey("odd:name", 7))).toEqual({ project: "odd:name", number: 7 });
  });

  it("is not confused by another kind of job, or by none", () => {
    expect(reviewOf("create-workspace:thicket")).toBeUndefined();
    expect(reviewOf(undefined)).toBeUndefined();
    expect(reviewOf("review:thicket:0")).toBeUndefined();
  });
});

describe("the order rows come out in", () => {
  it("sections first, then a re-review ahead of a first request", () => {
    const items = inboxItems(
      [
        {
          project: "thicket",
          repo: "/repos/thicket",
          prs: [
            pr({ number: 1, headRef: "a", requested: ["me"] }),
            pr({ number: 2, headRef: "b", requested: ["me"], reviewers: ["me"] }),
            pr({ number: 3, headRef: "c", author: "me", review: "approved" }),
          ],
        },
      ],
      me,
      none,
    );

    // Somebody acted on what you said and is waiting a second time, so it goes
    // above a request you have not looked at yet.
    expect(items.map((item) => item.number)).toEqual([2, 1, 3]);
  });

  it("a stack stays together, root first, under one heading", () => {
    const items = inboxItems(
      [
        {
          project: "thicket",
          repo: "/repos/thicket",
          prs: [
            // The tip is what is waiting on you; the base is somebody else's
            // and would otherwise sort into a different section entirely,
            // splitting the chain.
            pr({ number: 20, headRef: "tip", baseRef: "middle", requested: ["me"] }),
            pr({ number: 10, headRef: "middle", baseRef: "main", author: "elsewhere" }),
            pr({ number: 30, headRef: "solo", baseRef: "main", author: "elsewhere" }),
          ],
        },
      ],
      me,
      none,
    );

    const stacked = items.filter((item) => item.bucket === "needs-your-review");
    expect(stacked.map((item) => [item.number, item.depth])).toEqual([
      [10, 0],
      [20, 1],
    ]);
    // And the unrelated PR is not dragged in with them.
    expect(items.at(-1)?.number).toBe(30);
  });

  it("a PR on an ancestor that cannot merge is blocked", () => {
    const items = inboxItems(
      [
        {
          project: "thicket",
          repo: "/repos/thicket",
          prs: [
            pr({ number: 1, headRef: "base", baseRef: "main", author: "me", ci: "failing" }),
            pr({ number: 2, headRef: "top", baseRef: "base", author: "me", review: "approved" }),
          ],
        },
      ],
      me,
      none,
    );

    const top = items.find((item) => item.number === 2);
    expect(top?.blocked).toBe(true);
    expect(top?.depth).toBe(1);
    // Approved and green, and it still cannot land — which is why the field is
    // separate from the bucket rather than folded into it.
    const base = items.find((item) => item.number === 1);
    expect(base?.blocked).toBe(false);
  });

  it("a listing that suggests a cycle still answers", () => {
    // Cannot happen on GitHub — a branch has one PR — but a listing is a
    // hundred rows read out of a repository that is being pushed to. A walk
    // that trusted the data to be a tree would not return.
    const items = inboxItems(
      [
        {
          project: "thicket",
          repo: "/repos/thicket",
          prs: [
            pr({ number: 1, headRef: "a", baseRef: "b" }),
            pr({ number: 2, headRef: "b", baseRef: "a" }),
          ],
        },
      ],
      me,
      none,
    );
    expect(items).toHaveLength(2);
  });

  it("a row says which workspace is already reviewing it", () => {
    const items = inboxItems(
      [{ project: "thicket", repo: "/repos/thicket", prs: [pr({ number: 4 })] }],
      me,
      (project, number) =>
        project === "thicket" && number === 4
          ? { workspace: "pr-4", thread: "20260828-aaaa", job: "20260828-j0b1", moved: false }
          : undefined,
    );
    expect(items[0]).toMatchObject({
      workspace: "pr-4",
      thread: "20260828-aaaa",
      job: "20260828-j0b1",
    });
  });
});

describe("a review workspace's name", () => {
  it("round-trips the pull request number and nothing else", () => {
    // The name is the identity every idempotence check uses, which is why the
    // branch is not in it: a branch can be renamed or force-pushed while the
    // pull request stays the same one.
    expect(reviewWorkspace(12)).toBe("pr-12");
    expect(reviewNumber(reviewWorkspace(12))).toBe(12);
  });

  it("recognises the Go implementation's names too, which this machine is full of", () => {
    // `pr-<number>-<branch>` is what `ReviewWorkspaceName` minted, and every
    // review workspace made before amoeba has that shape. A reader that matched
    // only the new one would report those pull requests as unreviewed and offer
    // to build a second workspace beside the one already there.
    expect(reviewNumber("pr-2340-header-allowlist")).toBe(2340);
    expect(reviewNumber("pr-558-typed-router-ide")).toBe(558);
    // And it still only mints the short one: a branch in the name is an
    // identity that goes stale on a rename.
    expect(reviewWorkspace(558)).toBe("pr-558");
  });

  it("is not confused by a workspace that merely starts like one", () => {
    expect(reviewNumber("tiered-discounts")).toBeUndefined();
    // Digits required — somebody's own branch called `pr-fix-auth` is not a
    // review of pull request "fix".
    expect(reviewNumber("pr-fix-auth")).toBeUndefined();
    expect(reviewNumber("pr-0")).toBeUndefined();
    expect(reviewNumber("pr-")).toBeUndefined();
  });
});
