import type { CIState, MergeState, ReviewDecision } from "@awp-kit/protocol";

// The repair prompt: what is wrong with a pull request, said to an agent.
//
// Ported from the deck, where it was `C r`, and it is worth taking rather than
// rewriting because nearly every line of it is a decision somebody got wrong
// first. The two that matter most:
//
// **Tone follows ownership.** On your own pull request the agent is asked to
// *fix* — resolve the conflict, push the branch. On somebody else's it is asked
// to *look* — investigate and report, change no files, push nothing. Reviewing a
// stranger's PR should not have an agent start rebasing their branch.
//
// **An issue with no reviewer's angle is dropped, not translated.** The archive
// records the failure: the review prompt asked a reviewer to report how far
// behind its base someone else's branch was, which is the author's rebase and
// nothing a reviewer can act on. So `look` being absent *means* "not a
// reviewer's problem", and a new issue has to decide that on purpose rather than
// inherit a plausible-sounding review action.
//
// ── it is not sent, it is offered ──────────────────────────────────────────
//
// The deck deliberately did not dispatch this straight at the agent, and this
// returns a string for the same reason: it asks an agent to change code and push
// it, and the person whose branch that is should read the sentence first. The
// window puts it in an editable box.

/**
 * What the prompt needs to know, and nothing else.
 *
 * Its own shape rather than the wire's `PullRequest`, because the two do not
 * hold the same things: the viewer-relative answers — is it mine, is my review
 * wanted — are computed in the daemon against the `gh` login and never travel to
 * a client on this record. Taking them as arguments also makes every case here
 * a value a test can write down.
 */
export interface Repairable {
  readonly number: number;
  readonly url: string;
  /** `open`, `merged`, `closed`. Anything but open has nothing to repair. */
  readonly state: string;
  readonly headRef: string;
  readonly ci: CIState;
  readonly review: ReviewDecision;
  readonly mergeState: MergeState;
  /** A reviewer left notes, whatever GitHub's verdict says. */
  readonly hasReviewComments: boolean;
  /** The viewer opened it. */
  readonly mine: boolean;
  /** The viewer's review is wanted, and whether it has been wanted before. */
  readonly reviewRequested: boolean;
  readonly reviewRerequested: boolean;
}

/** One thing wrong, and what to do about it depending on whose PR this is. */
interface Issue {
  readonly label: string;
  /** The owner's action. */
  readonly fix: string;
  /**
   * The reviewer's action, or absent when there is none.
   *
   * Absent is meaningful: the issue is dropped from a review prompt entirely.
   * See the note above.
   */
  readonly look?: string;
  /**
   * Propose first, act after approval.
   *
   * Only review feedback sets it, and it gates the *whole* prompt when it is
   * present: an agent told to fix CI and answer a reviewer in one message should
   * not do half of it unprompted.
   */
  readonly gated?: boolean;
}

/**
 * Whether this pull request looks like your own work.
 *
 * By the head branch and the configured prefix, which is the same question the
 * inbox's link inference asks. An unset prefix answers true: with nothing to
 * compare against, the honest default is that this is your branch, because the
 * cost of being wrong that way is a prompt that offers to fix rather than one
 * that quietly declines to.
 */
export const looksMine = (pr: Repairable, prefix: string | undefined): boolean =>
  pr.mine || prefix === undefined || prefix === "" || pr.headRef.startsWith(`${prefix}/`);

/** Everything wrong with it, in the order a person would read them. */
const issuesOf = (pr: Repairable): ReadonlyArray<Issue> => {
  const issues: Array<Issue> = [];

  if (pr.mergeState === "dirty") {
    issues.push({
      label: "merge conflicts against its base branch",
      fix: "resolve the conflicts on this branch (rebase or merge the base in)",
      look: "identify which files conflict and give a one-line summary of why (e.g. both sides changed the same function)",
    });
  }
  if (pr.ci === "failing") {
    issues.push({
      label: "failing CI checks",
      fix: "diagnose the failing checks (`gh run list`, `gh run view`) and fix the underlying issues",
      look: "diagnose the failing checks via `gh run list` / `gh run view` and summarise the root cause",
    });
  }
  if (pr.mergeState === "behind") {
    issues.push({
      label: "an out-of-date base branch",
      fix: "update this branch with the latest base",
      // No review action, on purpose. Bringing a branch up to date is the
      // author's rebase; a reviewer cannot do it and reading the diff does not
      // depend on it.
    });
  }
  if (pr.review === "changes-requested" || pr.hasReviewComments) {
    const approved = pr.review === "approved";
    const label =
      pr.review === "changes-requested"
        ? "changes requested by a reviewer"
        : approved
          ? "review comments from a reviewer, on a PR that is already approved"
          : "review comments from a reviewer";
    // Reading and understanding only. Acting is what the gate below asks for
    // after approval, so this text deliberately stops short of "push".
    const fix =
      "read the review feedback (`gh pr view --comments`; `gh api repos/{owner}/{repo}/pulls/{n}/comments` for inline threads) and understand each point" +
      (approved
        ? ", then say which points are still open at the current head — the approval means some may already be addressed, and those need no further work"
        : "");
    issues.push({ label, fix, gated: true });
  }

  return issues;
};

/**
 * The issues a reviewer is asked about, which is a different list.
 *
 * A pending or repeated request for your review is review-tone only: a request
 * for *your* review cannot sit on your own pull request, so on one that looks
 * yours the signal is noise.
 */
const reviewerIssues = (pr: Repairable): ReadonlyArray<Issue> => {
  if (!pr.reviewRequested) {
    return [];
  }
  // A local read beats `gh pr diff`: parking the working copy on the head lets
  // the agent open files at the right revision, chase context and run tests,
  // where a raw patch allows none of that.
  const locally = `prefer a local read: \`jj git fetch\`, then \`jj new ${pr.headRef}@origin\` to park the working copy on the PR head (this does not touch the branch itself); fall back to \`gh pr diff\` if the head is not fetchable locally`;
  return pr.reviewRerequested
    ? [
        {
          label: "a RE-request for your review — you reviewed before and the author asked again",
          fix: "re-review the changes since your last pass and report your findings",
          look: `re-read your earlier feedback (\`gh pr view --comments\`), check whether each point was addressed at the current head, review what changed since your last pass, and report findings in chat — ${locally}`,
        },
      ]
    : [
        {
          label: "a pending request for your review",
          fix: "review the changes and report your findings",
          look: `review the changes and report findings in chat — ${locally}`,
        },
      ];
};

/**
 * A checkout that does not contain the pull request's head.
 *
 * Kept for both tones, unlike the rest: this is a property of the *local* copy
 * rather than of the pull request, so fetching and re-anchoring is safe whoever
 * owns the branch.
 */
const behind = (pr: Repairable): Issue => ({
  label: "new commits on origin that are not in your local copy of this branch",
  fix: `run \`jj git fetch\`, then align this workspace's working copy to the new origin tip (\`jj new ${pr.headRef}@origin\`) so later work builds on the latest`,
  look: `run \`jj git fetch\`, then align this workspace's working copy to the new origin tip (\`jj new ${pr.headRef}@origin\`) so you are reading the latest version`,
});

/**
 * The prompt, or the empty string when there is nothing wrong.
 *
 * **Empty is an answer**, and a caller has to treat it as "nothing to repair"
 * rather than sending a blank message. It is also why the issues are filtered
 * *before* the count is checked: dropping every issue has to mean there is
 * nothing to say, not a prompt with an empty list under it.
 */
export const repairPrompt = (
  pr: Repairable,
  options: { readonly mine: boolean; readonly moved: boolean },
): string => {
  if (pr.state !== "open") {
    return "";
  }

  const all = [
    ...issuesOf(pr),
    ...(options.mine ? [] : reviewerIssues(pr)),
    ...(options.moved ? [behind(pr)] : []),
  ];
  // Drop the author's chores from a reviewer's prompt. An issue with no review
  // action is one a reviewer cannot act on, and listing it hands them work that
  // belongs to whoever opened the pull request.
  const issues = options.mine ? all : all.filter((issue) => issue.look !== undefined);
  if (issues.length === 0) {
    return "";
  }

  const ref = `PR #${pr.number}${pr.url === "" ? "" : ` (${pr.url})`}`;

  if (!options.mine) {
    const rule =
      "You are reviewing this PR — the author is not you. Do NOT modify files, run jj/git mutations on the branch, or push.";
    const only = issues[0];
    if (issues.length === 1 && only !== undefined) {
      return `${ref} has ${only.label}. ${rule} Please ${only.look ?? only.fix}, and report back in chat.`;
    }
    return [
      `${ref} has multiple issues:`,
      ...issues.map((issue) => `- ${issue.label} — please ${issue.look ?? issue.fix}.`),
      `${rule} Report what you find in chat.`,
    ].join("\n");
  }

  if (issues.some((issue) => issue.gated === true)) {
    const only = issues[0];
    if (issues.length === 1 && only !== undefined) {
      return `${ref} has ${only.label}. Please ${only.fix}. Before changing anything, report back in chat with the problem and your proposed solution(s) for each point, and wait for my approval. Once I approve, address each point, push, reply to the review threads, and re-request review from the reviewer(s) who left it if needed.`;
    }
    return [
      `${ref} has multiple issues:`,
      ...issues.map((issue) => `- ${issue.label} — ${issue.fix}.`),
      "Before changing anything, report back in chat with the problem and your proposed solution(s) for each item, and wait for my approval. Once I approve, apply the fixes, push, reply to any review threads, and re-request review from the affected reviewer(s) if needed.",
    ].join("\n");
  }

  // Any of these fixes pushes commits, and under branch protection ("dismiss
  // stale reviews on push") that can drop an approval — leaving the PR silently
  // blocked. Harmless when there was no review to dismiss.
  const again =
    " If pushing new commits dismisses an existing review — or the change addresses a reviewer's comments — re-request review from the affected reviewer(s) once their feedback is addressed so the PR is not left blocked.";
  const only = issues[0];
  if (issues.length === 1 && only !== undefined) {
    return `${ref} has ${only.label}. Please ${only.fix}, then push the fix.${again}`;
  }
  return [
    `${ref} has multiple issues to address:`,
    ...issues.map((issue) => `- ${issue.label} — ${issue.fix}.`),
    `Address each of them, then push.${again}`,
  ].join("\n");
};
