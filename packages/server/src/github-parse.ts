// What `gh` said, turned into what awp holds.
//
// Pure, and separate from the CLI for the same reason `jj-parse.ts` is: every
// interesting rule in here is a rule about GitHub's vocabulary, and none of it
// needs a subprocess to test. The one place a rule needed a live check —
// whether a fork URL keeps somebody's credentials working — is called out where
// it happens.

import type { CIState, MergeState, ReviewDecision } from "@awp-kit/protocol";

/**
 * One entry of gh's `statusCheckRollup`, as much of it as the rollup reads.
 *
 * Heterogeneous by design: gh returns CheckRun rows (`name`, `conclusion`,
 * `status`) and StatusContext rows (`context`, `state`) in one list, and which
 * fields are set is what says which kind a row is.
 */
export interface RawCheck {
  readonly name?: string;
  readonly context?: string;
  readonly conclusion?: string;
  readonly status?: string;
  readonly state?: string;
  readonly startedAt?: string;
  readonly completedAt?: string;
}

/** One row of `gh pr list --json …`, as much of it as this file reads. */
export interface RawPullRequest {
  readonly number?: number;
  readonly headRefName?: string;
  readonly headRefOid?: string;
  readonly baseRefName?: string;
  readonly title?: string;
  readonly url?: string;
  readonly author?: { readonly login?: string };
  readonly state?: string;
  readonly isDraft?: boolean;
  readonly reviewDecision?: string;
  readonly statusCheckRollup?: ReadonlyArray<RawCheck>;
  readonly mergeStateStatus?: string;
  readonly reviewRequests?: ReadonlyArray<{ readonly login?: string; readonly slug?: string }>;
  readonly latestReviews?: ReadonlyArray<{ readonly author?: { readonly login?: string } }>;
  readonly reviews?: ReadonlyArray<{ readonly state?: string }>;
  readonly labels?: ReadonlyArray<{ readonly name?: string }>;
  readonly headRepositoryOwner?: { readonly login?: string };
  readonly headRepository?: { readonly name?: string };
}

/**
 * The one field a busy repository cannot afford, and what it costs to drop.
 *
 * `mergeStateStatus` makes GitHub compute mergeability for **every** pull
 * request in the answer, and on a repository with a hundred open ones that
 * exceeds their own time limit — the query fails outright rather than answering
 * slowly:
 *
 *   the whole field set (18)      GraphQL: Something went wrong while executing
 *                                 your query. Please include `5900:3F6B02:…`
 *   without `reviews`             the same
 *   without `mergeStateStatus`    12 rows in 4.6s
 *
 * Measured on a real repository, and it is worth knowing which way round the
 * cost lies: the field is not slow, it is *fatal*. So the listing asks for it
 * and falls back to asking without it, which is the difference between one
 * repository having no conflict signal and it having no inbox at all.
 */
export const EXPENSIVE_FIELD = "mergeStateStatus";

/** The fields `gh pr list` is asked for. One string, so nothing drifts. */
export const PR_FIELDS = [
  "number",
  "headRefName",
  "headRefOid",
  "baseRefName",
  "title",
  "url",
  "author",
  "state",
  "isDraft",
  "reviewDecision",
  "statusCheckRollup",
  "mergeStateStatus",
  "reviewRequests",
  "latestReviews",
  "reviews",
  "labels",
  "headRepositoryOwner",
  "headRepository",
].join(",");

/** The same list without the field that can kill the query. See above. */
export const PR_FIELDS_CHEAP = PR_FIELDS.split(",")
  .filter((field) => field !== EXPENSIVE_FIELD)
  .join(",");

/**
 * A pull request as the daemon holds it: gh's answer, with nothing viewer-
 * relative decided yet.
 *
 * The request and reviewer *lists* survive to here — unlike on the wire, where
 * they are already reduced to `mine` and `reviewRequested` — because reducing
 * them needs a viewer, and a cache keyed by repository must not also be keyed
 * by who was signed in when it was filled.
 */
export interface PullRequest {
  readonly number: number;
  readonly headRef: string;
  readonly headOid: string;
  readonly baseRef: string;
  readonly title: string;
  readonly author: string;
  readonly url: string;
  readonly draft: boolean;
  readonly ci: CIState;
  readonly review: ReviewDecision;
  readonly mergeState: MergeState;
  readonly labels: ReadonlyArray<string>;
  /** Logins whose review is requested. A re-request puts a reviewer back here. */
  readonly requested: ReadonlyArray<string>;
  /** Teams whose review is requested, org-qualified as GitHub reports them. */
  readonly requestedTeams: ReadonlyArray<string>;
  /** Logins with a review on record. In both lists means "asked again". */
  readonly reviewers: ReadonlyArray<string>;
  readonly hasReviewComments: boolean;
  /** The head repository when it is not this one — a fork. */
  readonly fork: { readonly owner: string; readonly repo: string } | undefined;
}

/** Who `gh` is signed in as, and which teams they are in. */
export interface Viewer {
  readonly login: string;
  /**
   * Org-qualified team slugs. Empty is ordinary rather than a failure — a token
   * without `read:org` cannot read them, and most repositories never request a
   * team's review.
   */
  readonly teams: ReadonlyArray<string>;
}

const text = (value: string | undefined): string => (value ?? "").trim();

/**
 * Only the latest run of each check.
 *
 * When a push cancels an in-flight workflow and the re-run completes, GitHub's
 * rollup holds **both** rows under the same name while the PR page shows one.
 * Without this reduction the stale `CANCELLED` marks the PR as failing for
 * ever. Keyed by check name (or status context); the later timestamp wins, with
 * list position breaking a tie. An unnamed row is kept as it is.
 */
export const latestPerName = (checks: ReadonlyArray<RawCheck>): ReadonlyArray<RawCheck> => {
  // `completedAt` is empty while a run is in flight, so the start is the
  // fallback. RFC3339 timestamps compare correctly as strings.
  const at = (check: RawCheck): string => text(check.completedAt) || text(check.startedAt);
  const out: Array<RawCheck> = [];
  const indexOf = new Map<string, number>();
  for (const check of checks) {
    const name = text(check.name) || text(check.context);
    if (name === "") {
      out.push(check);
      continue;
    }
    const found = indexOf.get(name);
    if (found === undefined) {
      indexOf.set(name, out.length);
      out.push(check);
      continue;
    }
    if (at(check) >= at(out[found] as RawCheck)) {
      out[found] = check;
    }
  }
  return out;
};

/**
 * The whole rollup as one signal.
 *
 * Failing wins outright and pending only decides once nothing has failed, which
 * is the ordering a reader wants: a PR with one red check and nine still
 * running is a PR to go and look at.
 */
export const rollup = (checks: ReadonlyArray<RawCheck> | undefined): CIState => {
  if (checks === undefined || checks.length === 0) {
    return "none";
  }
  let pending = false;
  for (const check of latestPerName(checks)) {
    switch (text(check.conclusion)) {
      case "FAILURE":
      case "TIMED_OUT":
      case "CANCELLED":
      case "ACTION_REQUIRED":
      case "STARTUP_FAILURE":
        return "failing";
      default:
        break;
    }
    switch (text(check.state)) {
      case "FAILURE":
      case "ERROR":
        return "failing";
      case "PENDING":
      case "EXPECTED":
        pending = true;
        break;
      default:
        break;
    }
    // A CheckRun in flight: no conclusion yet and a status that is not COMPLETED.
    const status = text(check.status);
    if (text(check.conclusion) === "" && status !== "" && status !== "COMPLETED") {
      pending = true;
    }
  }
  return pending ? "pending" : "passing";
};

const decisionOf = (value: string | undefined): ReviewDecision => {
  switch (text(value)) {
    case "APPROVED":
      return "approved";
    case "CHANGES_REQUESTED":
      return "changes-requested";
    case "REVIEW_REQUIRED":
      return "review-required";
    default:
      return "none";
  }
};

const mergeStateOf = (value: string | undefined): MergeState => {
  switch (text(value)) {
    case "CLEAN":
      return "clean";
    case "DIRTY":
      return "dirty";
    case "BEHIND":
      return "behind";
    case "BLOCKED":
      return "blocked";
    case "DRAFT":
      return "draft";
    case "UNSTABLE":
      return "unstable";
    case "HAS_HOOKS":
      return "has-hooks";
    default:
      return "unknown";
  }
};

/**
 * A reviewer left something to read.
 *
 * `COMMENTED` and `CHANGES_REQUESTED` only. `APPROVED` is not feedback to act
 * on, `PENDING` has not been submitted, and `DISMISSED` has been superseded.
 */
const hasReviewComments = (reviews: RawPullRequest["reviews"]): boolean =>
  (reviews ?? []).some((review) => {
    const state = text(review.state);
    return state === "COMMENTED" || state === "CHANGES_REQUESTED";
  });

/**
 * One row of gh's answer, projected — or `undefined` when it is not an open PR.
 *
 * **Open only, and dropped here rather than filtered by the caller.** The
 * inbox has nothing to say about a merged PR, and every bucket rule below
 * assumes the PR is open: a closed one with a review request on it would read
 * as "needs your review" for ever.
 */
export const pullRequest = (raw: RawPullRequest): PullRequest | undefined => {
  const number = raw.number;
  if (number === undefined || number <= 0 || text(raw.state) !== "OPEN") {
    return undefined;
  }
  const owner = text(raw.headRepositoryOwner?.login);
  const repo = text(raw.headRepository?.name);
  return {
    number,
    headRef: text(raw.headRefName),
    headOid: text(raw.headRefOid),
    baseRef: text(raw.baseRefName),
    title: text(raw.title),
    author: text(raw.author?.login),
    url: text(raw.url),
    draft: raw.isDraft === true,
    ci: rollup(raw.statusCheckRollup),
    review: decisionOf(raw.reviewDecision),
    mergeState: mergeStateOf(raw.mergeStateStatus),
    labels: (raw.labels ?? []).map((label) => text(label.name)).filter((name) => name !== ""),
    requested: (raw.reviewRequests ?? [])
      .map((request) => text(request.login))
      .filter((login) => login !== ""),
    requestedTeams: (raw.reviewRequests ?? [])
      .map((request) => text(request.slug))
      .filter((slug) => slug !== ""),
    reviewers: (raw.latestReviews ?? [])
      .map((review) => text(review.author?.login))
      .filter((login) => login !== ""),
    hasReviewComments: hasReviewComments(raw.reviews),
    // Owner and name are always reported; whether they name a *fork* is a
    // question the caller answers by comparing them to the repository gh was
    // run in — which `forkOf` does, because only there is that known.
    fork: owner === "" || repo === "" ? undefined : { owner, repo },
  };
};

/**
 * The fork the head is on, or nothing when the head is on this repository.
 *
 * Compared case-insensitively: GitHub treats `Acme/Widgets` and `acme/widgets`
 * as one repository, and a case difference between what gh reports for the PR
 * and what it reports for the repo would make every PR look like a fork — and
 * every fetch a second, pointless one.
 */
export const forkOf = (
  pr: PullRequest,
  repository: { readonly owner: string; readonly repo: string } | undefined,
): { readonly owner: string; readonly repo: string } | undefined => {
  if (pr.fork === undefined || repository === undefined) {
    return undefined;
  }
  const same =
    pr.fork.owner.toLowerCase() === repository.owner.toLowerCase() &&
    pr.fork.repo.toLowerCase() === repository.repo.toLowerCase();
  return same ? undefined : pr.fork;
};

const contains = (all: ReadonlyArray<string>, one: string): boolean =>
  all.some((entry) => entry.toLowerCase() === one.toLowerCase());

/**
 * Whether a requested team names one of the viewer's.
 *
 * Both sides org-qualified, which is how GitHub reports a request
 * (`acme-corp/platform-team`) and how the viewer's own list is built. An
 * unqualified request falls back to matching the bare slug — the field is
 * documented as a slug and nothing guarantees the qualification — but **only in
 * that direction.** Comparing bare slugs both ways would make one org's
 * `platform` team match another's, and report a PR in a repository the viewer
 * has no part in as waiting on them.
 */
export const teamMatches = (requested: string, mine: string): boolean => {
  const want = requested.toLowerCase();
  const have = mine.toLowerCase();
  if (want === have) {
    return true;
  }
  if (want.includes("/")) {
    return false;
  }
  const slash = have.lastIndexOf("/");
  return slash >= 0 && have.slice(slash + 1) === want;
};

/** The viewer authored it. */
export const authored = (pr: PullRequest, viewer: Viewer | undefined): boolean =>
  viewer !== undefined && viewer.login !== "" && contains([pr.author], viewer.login);

/**
 * The PR asks the viewer for a review — by name, or through a team.
 *
 * A team request is not an edge case: it is how whole repositories assign
 * review, and it is the way a PR can be waiting on somebody while naming
 * nobody.
 */
export const reviewRequested = (pr: PullRequest, viewer: Viewer | undefined): boolean => {
  if (viewer === undefined || viewer.login === "") {
    return false;
  }
  if (contains(pr.requested, viewer.login)) {
    return true;
  }
  return pr.requestedTeams.some((requested) =>
    viewer.teams.some((mine) => teamMatches(requested, mine)),
  );
};

/** Asked again: requested, and already has a review on record. */
export const reviewRerequested = (pr: PullRequest, viewer: Viewer | undefined): boolean =>
  reviewRequested(pr, viewer) &&
  viewer !== undefined &&
  viewer.login !== "" &&
  contains(pr.reviewers, viewer.login);

/** One row of `gh pr view --json comments` or `…reviews`. */
export interface RawRemark {
  readonly author?: { readonly login?: string };
  readonly body?: string;
  readonly state?: string;
  readonly createdAt?: string;
  readonly submittedAt?: string;
}

/** The fields one pull request is read with. Body and remarks, unlike the list. */
export const PR_DETAIL_FIELDS = [
  "number",
  "title",
  "body",
  "url",
  "author",
  "state",
  "isDraft",
  "baseRefName",
  "headRefName",
  "headRefOid",
  "reviewDecision",
  "mergeStateStatus",
  "statusCheckRollup",
  "labels",
  "comments",
  "reviews",
  "additions",
  "deletions",
  "changedFiles",
].join(",");

/** Something somebody said on a pull request, whether a comment or a review. */
export interface Remark {
  readonly author: string;
  readonly body: string;
  /** `commented`, `changes requested`, `approved` — or absent for a comment. */
  readonly verdict: string | undefined;
  readonly at: Date | undefined;
}

/**
 * One pull request, in as much detail as a panel and a briefing need.
 *
 * A superset of {@link PullRequest} rather than a second shape of the same
 * thing, because the two are read differently: the listing asks for a hundred
 * at once and cannot afford a body, and this asks for one and cannot do without
 * it. The overlap is projected by the same functions, so the classification a
 * row shows and the one this shows cannot disagree.
 */
export interface PullRequestDetail {
  readonly number: number;
  readonly title: string;
  /** The description, as markdown. Empty is ordinary. */
  readonly body: string;
  readonly url: string;
  readonly author: string;
  /** `open`, `merged` or `closed` — unlike the listing, this one may be over. */
  readonly state: string;
  readonly draft: boolean;
  readonly baseRef: string;
  readonly headRef: string;
  readonly headOid: string;
  readonly ci: CIState;
  readonly review: ReviewDecision;
  readonly mergeState: MergeState;
  readonly labels: ReadonlyArray<string>;
  /**
   * A reviewer left something to act on.
   *
   * Computed the same way the listing's is, and here as well as there because a
   * panel deciding whether to offer a repair must not have to re-derive it from
   * the remarks — that would be a second reading of "which reviews count", and
   * the two would disagree about a dismissed one.
   */
  readonly hasReviewComments: boolean;
  /** Issue comments and review bodies, oldest first, in one list. */
  readonly remarks: ReadonlyArray<Remark>;
  readonly additions: number;
  readonly deletions: number;
  readonly files: number;
}

const remark = (raw: RawRemark, verdict: string | undefined): Remark | undefined => {
  const body = (raw.body ?? "").trim();
  if (body === "") {
    // A review with no body is a verdict and not a remark — an approval with
    // nothing said. Counting it as something somebody wrote would put empty
    // bullets in a briefing.
    return undefined;
  }
  const when = text(raw.submittedAt) || text(raw.createdAt);
  const at = when === "" ? undefined : new Date(when);
  return {
    author: text(raw.author?.login),
    body,
    verdict,
    at: at !== undefined && !Number.isNaN(at.getTime()) ? at : undefined,
  };
};

/** How a review's state reads in a sentence, or absent for a plain comment. */
const verdictOf = (state: string | undefined): string | undefined => {
  switch (text(state)) {
    case "APPROVED":
      return "approved";
    case "CHANGES_REQUESTED":
      return "changes requested";
    case "COMMENTED":
      return "commented";
    case "DISMISSED":
      return "dismissed";
    default:
      return undefined;
  }
};

export const pullRequestDetail = (
  raw: RawPullRequest & {
    readonly body?: string;
    readonly comments?: ReadonlyArray<RawRemark>;
    readonly additions?: number;
    readonly deletions?: number;
    readonly changedFiles?: number;
  },
): PullRequestDetail | undefined => {
  const number = raw.number;
  if (number === undefined || number <= 0) {
    return undefined;
  }
  const remarks = [
    ...(raw.comments ?? []).map((one) => remark(one, undefined)),
    ...((raw.reviews ?? []) as ReadonlyArray<RawRemark>).map((one) =>
      remark(one, verdictOf(one.state)),
    ),
  ]
    .filter((one): one is Remark => one !== undefined)
    // Oldest first, and undated last: a conversation reads in the order it
    // happened, and gh returns comments and reviews as two lists that have to
    // be interleaved to be one.
    .toSorted((a, b) => (a.at?.getTime() ?? Infinity) - (b.at?.getTime() ?? Infinity));

  return {
    number,
    title: text(raw.title),
    body: (raw.body ?? "").trim(),
    url: text(raw.url),
    author: text(raw.author?.login),
    // Lower-cased rather than mapped to a union: `open`, `merged` and `closed`
    // are the whole set and a panel shows the word. A union would be a third
    // spelling of the same three strings.
    state: text(raw.state).toLowerCase(),
    draft: raw.isDraft === true,
    baseRef: text(raw.baseRefName),
    headRef: text(raw.headRefName),
    headOid: text(raw.headRefOid),
    ci: rollup(raw.statusCheckRollup),
    review: decisionOf(raw.reviewDecision),
    mergeState: mergeStateOf(raw.mergeStateStatus),
    labels: (raw.labels ?? []).map((label) => text(label.name)).filter((name) => name !== ""),
    hasReviewComments: hasReviewComments(raw.reviews),
    remarks,
    additions: raw.additions ?? 0,
    deletions: raw.deletions ?? 0,
    files: raw.changedFiles ?? 0,
  };
};

/**
 * The host a git remote points at, lower-cased, or `""` for a path.
 *
 * Both spellings a git remote comes in, because a repository configured either
 * way is ordinary:
 *
 *   ssh://git@github.com/acme/widgets.git   a scheme, so the host follows //
 *   git@github.com:acme/widgets.git         scp-like: the colon before the
 *                                           path is what makes it one
 *   /Users/someone/code/widgets             a local path — no host at all
 *
 * A string with no colon is a path, which is the case that decides the shape of
 * this function: asking "is this GitHub" of a repository whose remote is a
 * directory has to answer no rather than guessing.
 */
export const remoteHost = (url: string): string => {
  const value = url.trim();
  const scheme = value.indexOf("://");
  if (scheme >= 0) {
    let rest = value.slice(scheme + 3);
    const at = rest.lastIndexOf("@");
    if (at >= 0) {
      // Credentials or a user, both of which sit before the host — but only
      // when the `@` is before the path, or an `@` inside a path would eat it.
      const slash = rest.indexOf("/");
      if (slash < 0 || at < slash) {
        rest = rest.slice(at + 1);
      }
    }
    const [host = ""] = rest.split("/");
    return (host.split(":")[0] ?? "").toLowerCase();
  }
  let rest = value;
  const at = rest.lastIndexOf("@");
  if (at >= 0) {
    rest = rest.slice(at + 1);
  }
  const colon = rest.indexOf(":");
  return colon < 0 ? "" : rest.slice(0, colon).toLowerCase();
};

/**
 * Whether any of a repository's remotes points at a host `gh` knows.
 *
 * ── why this is asked before `gh` is ──────────────────────────────────────
 * Because "this repository has no GitHub remote" is not a failure, and `gh`
 * can only report it as one. A vault of notes, a scratch repo with no remote at
 * all, an internal repository on a host nobody has logged into — every one of
 * them produced a red sentence in the inbox on every refresh:
 *
 *   orchard: no git remotes found
 *   harbor-works: none of the git remotes configured for this repository
 *                 point to a known GitHub host
 *
 * Both are true and neither is actionable: the repository is working exactly as
 * intended and has no pull requests to have. A panel that complains about it
 * permanently is a panel whose warnings get skipped, which costs the one project
 * whose token really has expired.
 *
 * Hosts are compared exactly rather than by suffix. `github.com.evil.example`
 * ends with the right string and is not GitHub.
 */
export const onKnownHost = (
  remotes: ReadonlyArray<string>,
  hosts: ReadonlyArray<string>,
): boolean => {
  const known = new Set(hosts.map((host) => host.trim().toLowerCase()).filter((h) => h !== ""));
  return remotes.some((remote) => {
    const host = remoteHost(remote);
    return host !== "" && known.has(host);
  });
};

/**
 * The hosts named in `gh`'s own hosts file, plus github.com.
 *
 * `~/.config/gh/hosts.yml` is where `gh auth login` records what it knows, and
 * its top level is one key per host:
 *
 *   github.com:
 *       git_protocol: ssh
 *
 * Parsed with a regex rather than a YAML dependency, because the only thing
 * wanted from the file is its top-level keys — a parser for the rest would be a
 * dependency carried for a value never read. A file that cannot be read is not
 * a problem: github.com is always included, which is the answer for everybody
 * who has never logged into an enterprise host.
 */
export const knownHosts = (hostsYml: string | undefined): ReadonlyArray<string> => {
  const found = new Set(["github.com"]);
  for (const line of (hostsYml ?? "").split("\n")) {
    const key = /^([A-Za-z0-9][A-Za-z0-9.-]*):\s*$/u.exec(line);
    if (key !== null && key[1] !== undefined) {
      found.add(key[1].toLowerCase());
    }
  }
  return [...found];
};

/**
 * A git URL for `<owner>/<repo>` in the same shape as `origin`.
 *
 * **Not `https://github.com/…` unconditionally**, and this is the one rule here
 * that came from a real failure rather than from reading GitHub's fields. A
 * person's credentials are tied to a URL shape: an ssh remote means an ssh key,
 * an https remote means a credential helper. Fetching a private fork over https
 * from a machine set up for ssh does not fail as an auth error — it stops and
 * asks for a username on a terminal nobody is watching:
 *
 *   Device not configured: fatal: could not read Username
 *
 * So the scheme, host and user come from whatever `origin` already is, and only
 * the path is replaced. An origin that cannot be parsed falls back to
 * github.com over https, which is right for the public case and is the only
 * thing left to guess.
 */
export const forkFetchUrl = (origin: string, owner: string, repo: string): string => {
  const path = `${owner}/${repo}.git`;
  const url = origin.trim();
  // scp-like — `git@github.com:acme/widgets.git`. A colon before the path is
  // what makes it one, and it has no scheme, so this is checked first.
  const scp = /^([^/@:]+@)?([^/:]+):(?!\/)/u.exec(url);
  if (scp !== null) {
    return `${scp[1] ?? ""}${scp[2] ?? ""}:${path}`;
  }
  const scheme = /^([a-z][a-z0-9+.-]*:\/\/)([^/]+)\//iu.exec(url);
  if (scheme !== null) {
    return `${scheme[1] ?? ""}${scheme[2] ?? ""}/${path}`;
  }
  return `https://github.com/${path}`;
};
