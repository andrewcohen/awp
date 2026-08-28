import type { ReviewTarget } from "@awp-kit/protocol";
import { Data, Effect } from "effect";
import type { Github } from "./github";
import type { Jj, JjError } from "./jj";
import { localBookmarks } from "./jj-parse";

// Making a pull request's head exist locally, and saying what to call it.
//
// Shared by the two things that need it, which is the whole reason it is a file:
// the `fetch` step of `create-workspace`, which does this before making a
// checkout, and `WorkspaceRepair`, which does it again when the pull request has
// moved since. Those are the same three questions — fetch, is the branch here,
// what is its revset — and two answers to them would drift the first time one
// side learned something the other did not.
//
// ── the revset a branch turns into depends on how it arrived ───────────────
//
//   from origin    `feature@origin` — jj does not track a fetched branch
//                  locally by default, so the bare name is not a revision
//   from a fork    `feature` — git wrote refs/heads, so jj imports it local
//
// **The remote one wins when both exist.** A local bookmark of the same name is
// somebody's own copy and may be behind the pull request, and reviewing a stale
// branch is worse than not reviewing because nothing about it says so.

export interface HeadOf {
  readonly repo: string;
  readonly review: ReviewTarget;
}

/**
 * The head could not be made to exist locally.
 *
 * A `Data.TaggedError` rather than a plain class, so callers select it with
 * `Effect.catchTag` — which is a type guard — instead of reading `_tag` off it.
 * See AGENTS.md: the rule firing there is usually a sign of reaching past an API
 * that already exists.
 */
export class HeadMissing extends Data.TaggedError("HeadMissing")<{
  readonly reason: string;
}> {}

/**
 * Fetch, and answer with the revset the pull request's head is now called.
 *
 * `jj git import` after a fork fetch, or jj cannot see the ref: jj caches its
 * view of the git refs per operation, and nothing about the symptom points at it
 * — the bookmark is simply not in `bookmark list` and the revision "does not
 * exist".
 */
export const fetchHead = (
  jj: Jj["Service"],
  github: Github["Service"],
  { repo, review }: HeadOf,
): Effect.Effect<string, JjError | HeadMissing> =>
  Effect.gen(function* () {
    yield* jj.fetch(repo);

    if (review.fork !== undefined) {
      // A fork's head is not on `origin`, so the fetch above did not bring it
      // down and the base would name a branch nothing has heard of.
      yield* github
        .fetchFork(repo, {
          owner: review.fork.owner,
          repo: review.fork.repo,
          ref: review.headRef,
        })
        .pipe(Effect.mapError((error) => new HeadMissing({ reason: error.reason })));
      yield* jj.importGit(repo);
    }

    const all = yield* jj.bookmarks(repo);
    const remote = all.find((entry) => entry.name === review.headRef && entry.remote !== undefined);
    if (remote !== undefined) {
      return `${review.headRef}@${remote.remote}`;
    }
    if (localBookmarks(all).some((entry) => entry.name === review.headRef)) {
      return review.headRef;
    }
    return yield* Effect.fail(
      new HeadMissing({
        reason: `fetched, and ${review.headRef} — the head of #${review.number} — is still not a bookmark in this repository`,
      }),
    );
  });
