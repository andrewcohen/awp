// The inbox's read model: which section a pull request belongs in, where it
// sits in its stack, and what order the rows come out in.
//
// Pure, and tested without a subprocess or a socket — the same split
// `deckdata` had in the Go implementation, and for the same reason: every rule
// in here is a rule about precedence, and precedence is exactly what a test can
// pin and a person cannot check by looking.

import type { InboxBucket, InboxItem } from "@awp-kit/protocol";
import { inboxBuckets } from "@awp-kit/protocol";
import type { PullRequest, Viewer } from "./github-parse";
import { authored, reviewRequested, reviewRerequested } from "./github-parse";

/**
 * Which section an open pull request belongs in.
 *
 * The archive's precedence, kept exactly, because it was locked by tests there
 * and every clause of it is a decision:
 *
 *   a review request wins outright   it names you, whatever else is true of
 *                                    the PR — including its CI being red
 *   not yours → other open           nothing here is your move
 *   a draft of yours → mine          a draft is not submitted, so its CI is
 *                                    information rather than a task
 *   changes requested, CI red,       something to fix
 *   or it will not merge
 *   approved and green → ready       one keypress from done
 *   anything else of yours → mine    the ball is with the reviewers
 *
 * The draft check preceding the CI check is the clause that looks arbitrary and
 * is not: without it every red draft would sit in "Needs action" beside work
 * that is actually blocked.
 *
 * ── the merge queue is deliberately not read ───────────────────────────────
 * The archive also treated "in the merge queue" as ready-to-merge. That signal
 * exists only in GraphQL — `gh pr list --json` does not expose it — so it cost
 * a second query per repository per refresh for a state that lasts minutes. A
 * queued PR that is approved and green already reads as ready here; one that is
 * queued without being either lands in "Mine", which is a row in the right
 * place with the wrong heading rather than a row nobody can find.
 */
export const bucketOf = (pr: PullRequest, viewer: Viewer | undefined): InboxBucket => {
  if (reviewRequested(pr, viewer)) {
    return "needs-your-review";
  }
  if (!authored(pr, viewer)) {
    return "other-open";
  }
  if (pr.draft) {
    return "mine";
  }
  if (
    pr.review === "changes-requested" ||
    pr.ci === "failing" ||
    pr.mergeState === "dirty" ||
    pr.mergeState === "behind"
  ) {
    return "needs-action";
  }
  if (
    pr.review === "approved" &&
    (pr.ci === "passing" || pr.ci === "none") &&
    pr.mergeState === "clean"
  ) {
    return "ready-to-merge";
  }
  return "mine";
};

/** Where a bucket sits in the running order. */
const rank = (bucket: InboxBucket): number => {
  const found = inboxBuckets.indexOf(bucket);
  return found < 0 ? inboxBuckets.length : found;
};

/**
 * One pull request's place in its stack.
 *
 * A stack edge is one PR's base branch being another open PR's head branch, so
 * the whole graph is already in the listing — no extra query, and no synthesis.
 * That is the payoff of the inbox being a list of pull requests rather than of
 * workspaces: the deck needed a third virtual-row pass here purely because a
 * stack's middle link is often somebody else's PR, which its rows could not
 * represent.
 *
 * `blocked` is about landing, not about reviewing: an ancestor that is not
 * ready to merge means this one cannot land yet, however good it is.
 */
interface Placed {
  readonly depth: number;
  readonly blocked: boolean;
  /** The head branch of the stack's root — what keeps a stack contiguous. */
  readonly root: string;
  /** How many open pull requests share that root, this one included. */
  readonly members: number;
  /** The most actionable bucket in the whole stack, which is the heading
   * every member of it draws under. */
  readonly section: InboxBucket;
}

/**
 * Where each PR of one repository sits in its stack.
 *
 * A cycle cannot happen on GitHub — a PR's base is a branch, and a branch has
 * one PR — but a *listing* can suggest one, because the answer is a hundred
 * rows out of a repository that is being pushed to while it is read. So the
 * walk counts its steps and stops, rather than trusting the data to be a tree.
 */
const place = (
  prs: ReadonlyArray<PullRequest>,
  buckets: ReadonlyMap<number, InboxBucket>,
): Map<number, Placed> => {
  const byHead = new Map<string, PullRequest>();
  for (const pr of prs) {
    if (pr.headRef !== "") {
      byHead.set(pr.headRef, pr);
    }
  }

  /** The chain from a PR up to its root, nearest ancestor first. */
  const ancestors = (pr: PullRequest): ReadonlyArray<PullRequest> => {
    const out: Array<PullRequest> = [];
    const seen = new Set<number>([pr.number]);
    let at = byHead.get(pr.baseRef);
    while (at !== undefined && !seen.has(at.number) && out.length < prs.length) {
      out.push(at);
      seen.add(at.number);
      at = byHead.get(at.baseRef);
    }
    return out;
  };

  // Two passes, and the second one is the reason for the first: a stack's
  // section is its *whole* chain's, so it cannot be decided while walking one
  // row's ancestors — the row that makes a stack somebody's problem is often
  // its tip, and the base is a stranger's PR that would otherwise section
  // several headings away and split the chain.
  //
  // That is exactly what the first version of this did, and the test named "a
  // stack stays together" is what caught it: the tip reported
  // `needs-your-review` and its base `other-open`, so the two drew under
  // different headings with the chain broken between them.
  const chains = new Map<number, ReadonlyArray<PullRequest>>();
  for (const pr of prs) {
    chains.set(pr.number, ancestors(pr));
  }

  /** The head branch of a row's topmost open ancestor — the stack's identity. */
  const rootOf = (pr: PullRequest): string => (chains.get(pr.number)?.at(-1) ?? pr).headRef;

  const sections = new Map<string, InboxBucket>();
  const sizes = new Map<string, number>();
  for (const pr of prs) {
    const root = rootOf(pr);
    sizes.set(root, (sizes.get(root) ?? 0) + 1);
    const mine = buckets.get(pr.number) ?? "other-open";
    const best = sections.get(root);
    if (best === undefined || rank(mine) < rank(best)) {
      sections.set(root, mine);
    }
  }

  const placed = new Map<number, Placed>();
  for (const pr of prs) {
    const chain = chains.get(pr.number) ?? [];
    const root = rootOf(pr);
    placed.set(pr.number, {
      depth: chain.length,
      // About landing, not about reviewing: an ancestor that is not ready to
      // merge means this one cannot land yet, however good it is.
      blocked: chain.some(
        (ancestor) => (buckets.get(ancestor.number) ?? "other-open") !== "ready-to-merge",
      ),
      root,
      members: sizes.get(root) ?? 1,
      section: sections.get(root) ?? buckets.get(pr.number) ?? "other-open",
    });
  }
  return placed;
};

/** One project's pull requests, and what awp knows locally about them. */
export interface Source {
  readonly project: string;
  readonly repo: string;
  readonly prs: ReadonlyArray<PullRequest>;
}

/**
 * What awp already has for a pull request: a workspace, a thread, a job.
 *
 * A function rather than a table, because the caller holds those records and
 * this file holds none. `undefined` means nothing has been started — which is
 * what makes the row offer to create rather than to open.
 *
 * Every field is separately absent, and each absence means something different:
 *
 *   workspace  nothing to open yet. A job may still be building one
 *   thread     the create job has not reached its claim step, or somebody
 *              detached the workspace by hand
 *   job        no record of one — never started, or the record was cleared
 */
export interface Claimed {
  readonly workspace: string | undefined;
  readonly thread: string | undefined;
  readonly job: string | undefined;
  /**
   * The workspace does not contain what the pull request now is.
   *
   * On the claim rather than computed here because answering it means asking jj
   * whether one commit is an ancestor of another, and this file holds no
   * services — the same reason the rest of `Claimed` is a function's answer.
   */
  readonly moved: boolean;
}

export type Claim = (project: string, number: number) => Claimed | undefined;

/**
 * The idempotency key a review's job is enqueued under.
 *
 * Here rather than in the handler because two call sites need the identical
 * string — `ReviewStart` to refuse a second job, `InboxList` to find the one a
 * row is waiting on — and a key composed twice is a key that eventually differs
 * by a colon. It names the *project*, not the repository, because a project is
 * what the rest of awp is addressed by and two projects can hold one PR number.
 */
export const reviewKey = (project: string, number: number): string => `review:${project}:${number}`;

/**
 * The pull request a job's key is about, or nothing if it is not a review's.
 *
 * The inverse, beside it, because the two directions are the same fact and a
 * format read in one file and written in another is a format that drifts by a
 * colon. `InboxList` reads it: matching a job by its key is one string
 * comparison per job, where reading the job's stored input would be a schema
 * decode per job on every listing.
 *
 * A project name may contain anything a directory basename can, so the split is
 * on the *last* colon rather than the first.
 */
export const reviewOf = (
  key: string | undefined,
): { readonly project: string; readonly number: number } | undefined => {
  const found = /^review:(.+):(\d+)$/u.exec(key ?? "");
  const project = found?.[1];
  const number = Number(found?.[2]);
  if (project === undefined || project === "" || !Number.isSafeInteger(number) || number <= 0) {
    return undefined;
  }
  return { project, number };
};

/**
 * Every row, sectioned and in render order.
 *
 * The order, and what each part of it is for:
 *
 *   1  section          the heading a row appears under
 *   2  re-review first  inside "Needs your review", somebody acted on what you
 *                       said and is waiting a second time
 *   3  project, then the stack's root, then depth — which is what keeps a
 *      stack's members together and in root → tip order
 *   4  number           a stable tie-break, so the list does not shuffle
 *                       between two refreshes that read the same thing
 *
 * Sorted here rather than by the client for the reason the contract gives: a
 * rule with two implementations has one that drifts, and this one has four
 * clauses.
 */
export const inboxItems = (
  sources: ReadonlyArray<Source>,
  viewer: Viewer | undefined,
  claimed: Claim,
): ReadonlyArray<InboxItem> => {
  const rows: Array<InboxItem & { readonly root: string }> = [];

  for (const source of sources) {
    const buckets = new Map<number, InboxBucket>(
      source.prs.map((pr) => [pr.number, bucketOf(pr, viewer)]),
    );
    const placed = place(source.prs, buckets);

    for (const pr of source.prs) {
      const at = placed.get(pr.number);
      const claim = claimed(source.project, pr.number);
      rows.push({
        project: source.project,
        repo: source.repo,
        number: pr.number,
        title: pr.title,
        author: pr.author,
        url: pr.url,
        headRef: pr.headRef,
        baseRef: pr.baseRef,
        draft: pr.draft,
        ci: pr.ci,
        review: pr.review,
        mergeState: pr.mergeState,
        labels: pr.labels,
        mine: authored(pr, viewer),
        reviewRequested: reviewRequested(pr, viewer),
        reviewRerequested: reviewRerequested(pr, viewer),
        hasReviewComments: pr.hasReviewComments,
        // The row's own bucket is the section it appears under, which for a
        // stacked PR is the stack's rather than its own. Two fields would let
        // a client draw a row under one heading and label it with another.
        bucket: at?.section ?? buckets.get(pr.number) ?? "other-open",
        depth: at?.depth ?? 0,
        blocked: at?.blocked ?? false,
        workspace: claim?.workspace,
        thread: claim?.thread,
        job: claim?.job,
        // False rather than absent for a row with no workspace: there is
        // nothing there to be stale, and a tri-state would have the client
        // deciding what "unknown" should look like.
        moved: claim?.moved ?? false,
        // Only when the stack has more than one member. A tree of one is not a
        // tree, and the client draws guides on exactly the rows that have this.
        stack: (at?.members ?? 1) > 1 ? at?.root : undefined,
        root: at?.root ?? pr.headRef,
      });
    }
  }

  const ordered = rows.toSorted((a, b) => {
    const section = rank(a.bucket) - rank(b.bucket);
    if (section !== 0) {
      return section;
    }
    if (a.bucket === "needs-your-review" && a.reviewRerequested !== b.reviewRerequested) {
      return a.reviewRerequested ? -1 : 1;
    }
    const project = a.project.localeCompare(b.project);
    if (project !== 0) {
      return project;
    }
    const root = a.root.localeCompare(b.root);
    if (root !== 0) {
      return root;
    }
    const depth = a.depth - b.depth;
    return depth !== 0 ? depth : a.number - b.number;
  });

  // `root` is the sort's, not the client's: it is an implementation of
  // contiguity and would be one more field for a renderer to wonder about.
  return ordered.map(({ root: _root, ...item }) => item);
};

/** A review workspace's name, and the identity every idempotence check uses. */
export const reviewWorkspace = (number: number): string => `pr-${number}`;

/**
 * The pull request a review workspace is for, or nothing if it is not one.
 *
 * ── minting and recognising are not the same rule ─────────────────────────
 *
 * {@link reviewWorkspace} mints `pr-<number>` and nothing more: a branch can be
 * renamed or force-pushed while the pull request stays the same one, so a name
 * carrying the branch is an identity that can go stale.
 *
 * This has to be *wider* than that, because the machine is already full of the
 * Go implementation's names — `ReviewWorkspaceName` there was
 * `pr-<number>-<branch>`, and the sessions on this machine say so:
 *
 *   awp.thicket.pr-2340-header-allowlist-6fb6.agent
 *   awp.orchard.pr-558-typed-router-ide-bfad.agent
 *
 * A reader that only matched the new shape would report every one of those pull
 * requests as unreviewed, and the row's action would offer to build a *second*
 * workspace beside the one already there. The suffix is therefore accepted and
 * ignored: the number is the identity in both spellings.
 *
 * Digits are required after `pr-`, so a workspace somebody called `pr-fix-auth`
 * is not mistaken for a review.
 */
export const reviewNumber = (workspace: string): number | undefined => {
  const found = /^pr-(\d+)(?:-.*)?$/u.exec(workspace.trim());
  if (found !== null) {
    const number = Number(found[1]);
    if (Number.isSafeInteger(number) && number > 0) {
      return number;
    }
  }
  return undefined;
};
