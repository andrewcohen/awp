import { describe, expect, it } from "vitest";
import {
  type RawCheck,
  forkFetchUrl,
  knownHosts,
  onKnownHost,
  remoteHost,
  latestPerName,
  pullRequest,
  reviewRequested,
  rollup,
  teamMatches,
} from "./github-parse";

/** A completed CheckRun, unless a test says otherwise. */
const run = (over: Partial<RawCheck>): RawCheck => ({
  name: "build",
  status: "COMPLETED",
  ...over,
});

describe("rolling the checks up into one signal", () => {
  it("no checks is not a failure", () => {
    expect(rollup([])).toBe("none");
    expect(rollup(undefined)).toBe("none");
  });

  it("one red check makes the whole thing red", () => {
    // Even with nine still running: a PR with a failure is one to go and look
    // at now, not once the rest have finished.
    expect(
      rollup([run({ conclusion: "FAILURE" }), run({ name: "test", status: "IN_PROGRESS" })]),
    ).toBe("failing");
  });

  it("a StatusContext reports through `state` rather than `conclusion`", () => {
    expect(rollup([{ context: "ci/legacy", state: "FAILURE" }])).toBe("failing");
    expect(rollup([{ context: "ci/legacy", state: "PENDING" }])).toBe("pending");
    expect(rollup([{ context: "ci/legacy", state: "SUCCESS" }])).toBe("passing");
  });

  it("skipped and neutral are not failures", () => {
    expect(
      rollup([run({ conclusion: "SKIPPED" }), run({ name: "b", conclusion: "NEUTRAL" })]),
    ).toBe("passing");
  });

  it("a cancelled run superseded by a green one is green", () => {
    // The rollup holds both while the PR page shows one. Without the reduction
    // the stale CANCELLED marks the PR failing for ever, which is a red badge
    // nothing a person does will clear.
    expect(
      rollup([
        run({ conclusion: "CANCELLED", completedAt: "2026-08-28T09:00:00Z" }),
        run({ conclusion: "SUCCESS", completedAt: "2026-08-28T09:10:00Z" }),
      ]),
    ).toBe("passing");
  });

  it("an in-flight run has no conclusion and is kept as itself", () => {
    // `completedAt` is empty while a run is in flight, so the start time is
    // what the reduction compares.
    const kept = latestPerName([
      run({ conclusion: "SUCCESS", completedAt: "2026-08-28T09:00:00Z" }),
      { name: "build", status: "IN_PROGRESS", startedAt: "2026-08-28T09:05:00Z" },
    ]);
    expect(kept).toHaveLength(1);
    expect(kept[0]?.status).toBe("IN_PROGRESS");
  });

  it("a row with no name at all is not collapsed against another", () => {
    const kept = latestPerName([{ conclusion: "SUCCESS" }, { conclusion: "FAILURE" }]);
    expect(kept).toHaveLength(2);
  });
});

describe("projecting one row of gh's answer", () => {
  it("drops anything that is not an open pull request", () => {
    expect(pullRequest({ number: 1, state: "MERGED" })).toBeUndefined();
    expect(pullRequest({ number: 1, state: "CLOSED" })).toBeUndefined();
    // A merged PR with a review request on it would otherwise read as "needs
    // your review" for ever.
    expect(pullRequest({ state: "OPEN" })).toBeUndefined();
  });

  it("separates a user request from a team request", () => {
    // gh mixes Users and Teams in one list, and which field is set is the only
    // thing that says which kind a row is.
    const found = pullRequest({
      number: 3,
      state: "OPEN",
      reviewRequests: [{ login: "me" }, { slug: "acme-corp/platform-team" }],
    });
    expect(found?.requested).toEqual(["me"]);
    expect(found?.requestedTeams).toEqual(["acme-corp/platform-team"]);
  });

  it("reads feedback that never moved GitHub's verdict", () => {
    // A COMMENTED review leaves `reviewDecision` at REVIEW_REQUIRED, so this is
    // the only signal that catches "somebody gave you notes".
    const found = pullRequest({
      number: 4,
      state: "OPEN",
      reviewDecision: "REVIEW_REQUIRED",
      reviews: [{ state: "COMMENTED" }],
    });
    expect(found?.hasReviewComments).toBe(true);
    expect(found?.review).toBe("review-required");
  });

  it("an approval and a dismissed review are not open feedback", () => {
    const found = pullRequest({
      number: 5,
      state: "OPEN",
      reviews: [{ state: "APPROVED" }, { state: "DISMISSED" }],
    });
    expect(found?.hasReviewComments).toBe(false);
  });
});

describe("matching a team", () => {
  it("compares org-qualified names", () => {
    expect(teamMatches("acme-corp/platform-team", "acme-corp/platform-team")).toBe(true);
    expect(teamMatches("acme-corp/platform-team", "other-corp/platform-team")).toBe(false);
  });

  it("an unqualified request falls back to the slug, and only that way round", () => {
    expect(teamMatches("platform-team", "acme-corp/platform-team")).toBe(true);
    // The direction that must not match: one org's `platform` is not another's,
    // and allowing it would report a PR in a repository the viewer has no part
    // in as waiting on them.
    expect(teamMatches("acme-corp/platform", "other-corp/platform")).toBe(false);
  });

  it("nobody signed in is nobody's review request", () => {
    const asked = { requested: ["me"], requestedTeams: [] } as never;
    expect(reviewRequested(asked, undefined)).toBe(false);
    expect(reviewRequested(asked, { login: "", teams: [] })).toBe(false);
  });
});

describe("the URL a fork's head is fetched from", () => {
  // Measured against the failure it exists to avoid: fetching a private fork
  // over https from a machine set up for ssh does not fail as an auth error, it
  // stops and asks for a username on a terminal nobody is watching.
  it("keeps the shape origin already has", () => {
    expect(forkFetchUrl("git@github.com:acme/widgets.git", "someone", "widgets")).toBe(
      "git@github.com:someone/widgets.git",
    );
    expect(forkFetchUrl("https://github.com/acme/widgets.git", "someone", "widgets")).toBe(
      "https://github.com/someone/widgets.git",
    );
    // An enterprise host, which is the case a hard-coded github.com breaks
    // outright rather than merely inconveniently.
    expect(forkFetchUrl("ssh://git@git.acme.example/acme/widgets.git", "someone", "widgets")).toBe(
      "ssh://git@git.acme.example/someone/widgets.git",
    );
  });

  it("falls back to github.com over https when origin says nothing", () => {
    expect(forkFetchUrl("", "someone", "widgets")).toBe("https://github.com/someone/widgets.git");
  });
});

describe("whether a repository is even on GitHub", () => {
  // Asked before `gh` is, because "no GitHub remote" is not a failure and `gh`
  // can only report it as one.
  it("reads the host out of both spellings a remote comes in", () => {
    expect(remoteHost("git@github.com:acme/widgets.git")).toBe("github.com");
    expect(remoteHost("ssh://git@git.acme.example/acme/widgets.git")).toBe("git.acme.example");
    expect(remoteHost("https://github.com/acme/widgets.git")).toBe("github.com");
    expect(remoteHost("https://user:token@github.com/acme/widgets.git")).toBe("github.com");
    // A path is not a host. This is the case that decides the shape: a
    // repository whose remote is a directory has to answer no rather than
    // guessing.
    expect(remoteHost("/Users/someone/code/widgets")).toBe("");
    expect(remoteHost("")).toBe("");
  });

  it("matches a host exactly, never by suffix", () => {
    expect(onKnownHost(["git@github.com:acme/widgets.git"], ["github.com"])).toBe(true);
    // Ends with the right string and is not GitHub.
    expect(onKnownHost(["https://github.com.evil.example/acme/w.git"], ["github.com"])).toBe(false);
  });

  it("no remotes at all is not on GitHub", () => {
    // The vault-of-notes case, and the scratch repository case.
    expect(onKnownHost([], ["github.com"])).toBe(false);
    expect(onKnownHost(["/Users/someone/notes"], ["github.com"])).toBe(false);
  });

  it("an enterprise host counts once gh has been logged into it", () => {
    const hosts = knownHosts(
      ["github.com:", "    git_protocol: ssh", "git.acme.example:", "    user: someone"].join("\n"),
    );
    expect(hosts).toContain("git.acme.example");
    expect(onKnownHost(["git@git.acme.example:acme/widgets.git"], hosts)).toBe(true);
  });

  it("github.com is known even with no hosts file to read", () => {
    expect(knownHosts(undefined)).toEqual(["github.com"]);
  });
});
