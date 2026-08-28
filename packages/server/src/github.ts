// GitHub, as much of it as awp asks about.
//
// A tag with a fake, like every other service here, and the line it draws is
// the one that matters for testing: everything above depends on this and
// nothing above knows that `gh` is a binary. `github-cli.ts` is the only file
// that does.
//
// ── why gh and not the API ─────────────────────────────────────────────────
// Because the authentication is already solved and is not awp's to hold. `gh`
// keeps a token in the user's keychain, refreshes it, and works against
// enterprise hosts without being told they exist. A client here would need a
// token of its own, a place to keep it, and a story for both — for the same
// answers.
//
// ── every method takes a repository ────────────────────────────────────────
// The same rule as `Jj`, and for the same reason: `gh` resolves the repository
// from the remote of whatever directory it runs in, and the daemon's own
// directory is a real repository. A method that did not take one could reach
// the wrong project by accident. It is a structural refusal rather than a
// convention.

import { Context, Data, Effect } from "effect";
import type { PullRequest, PullRequestDetail, Viewer } from "./github-parse";

export type { PullRequest, PullRequestDetail, Remark, Viewer } from "./github-parse";

/**
 * `gh` could not answer, said in `gh`'s own words where there are any.
 *
 * A declared failure and not a defect, because every cause is ordinary: `gh` is
 * not installed, the token expired, the remote is not GitHub at all. The inbox
 * shows the sentence beside the project it belongs to and keeps the other
 * projects' rows — see `InboxSource` in the contract.
 */
export class GithubError extends Data.TaggedError("GithubError")<{
  readonly op: string;
  readonly reason: string;
  readonly cause?: unknown;
}> {}

/** A repository as GitHub names it. */
export interface Repository {
  readonly owner: string;
  readonly repo: string;
}

/**
 * A repository's open pull requests, and whether anything had to be given up to
 * read them.
 *
 * `degraded` is a sentence rather than a flag because the only thing to do with
 * it is show it: it says which signal is missing and why, on a project whose
 * rows are otherwise complete. See the fallback in `github-cli.ts`.
 */
export interface Listing {
  readonly prs: ReadonlyArray<PullRequest>;
  readonly degraded: string | undefined;
}

export class Github extends Context.Service<
  Github,
  {
    /**
     * Every open pull request in this repository.
     *
     * Open only, and the projection drops anything else — see `pullRequest` in
     * github-parse.ts. Nothing viewer-relative is decided here: the request and
     * reviewer lists come back as they are, so one answer can serve a cache
     * that is keyed by repository and nothing else.
     */
    readonly pullRequests: (repo: string) => Effect.Effect<Listing, GithubError>;

    /**
     * Who `gh` is signed in as.
     *
     * The teams are best-effort inside the implementation: reading them needs
     * the `read:org` scope, which `gh auth login` does not grant by default, and
     * a login with no teams is far more useful than a failure. What that costs
     * is that team-assigned reviews stay quiet, which is why the implementation
     * logs the sentence telling a person how to fix it.
     */
    readonly viewer: () => Effect.Effect<Viewer, GithubError>;

    /**
     * One pull request, with its description and its conversation.
     *
     * A second call rather than a field on the listing, and the split is about
     * cost: `gh pr list` asks for a hundred at once and cannot afford a body,
     * this asks for one and cannot do without it. **Not restricted to open
     * ones** — a panel is opened on a PR that merged an hour ago, and so is a
     * briefing.
     */
    readonly pullRequest: (
      repo: string,
      number: number,
    ) => Effect.Effect<PullRequestDetail | undefined, GithubError>;

    /**
     * Whether this checkout has a remote on a host `gh` knows.
     *
     * Asked before the pull requests are, and it is the difference between a
     * project the inbox has nothing to say about and one it failed on. A vault
     * of notes has no remote; an internal repository may be on a host nobody
     * has logged into. Both are ordinary, neither is actionable, and `gh` can
     * only report either as an error — see `onKnownHost`.
     *
     * Answers false rather than failing when the directory is not a git
     * repository at all, which is what a jj workspace with no colocated git
     * looks like.
     */
    readonly isGithub: (repo: string) => Effect.Effect<boolean, GithubError>;

    /** Which GitHub repository a local checkout is, for telling forks apart. */
    readonly repository: (repo: string) => Effect.Effect<Repository, GithubError>;

    /**
     * Bring a pull request's head branch down to `refs/heads/<headRef>`.
     *
     * Only needed for a fork: an origin PR's head arrives with `jj git fetch`.
     * `git` and not `gh`, because what is wanted is the ref in the local
     * repository — but the URL is derived from GitHub's owner and name, which is
     * why it lives on this service rather than on `Jj`. See `forkFetchUrl` for
     * the part that is not obvious.
     */
    readonly fetchFork: (
      repo: string,
      head: { readonly owner: string; readonly repo: string; readonly ref: string },
    ) => Effect.Effect<void, GithubError>;
  }
>()("awp/Github") {}
