// The `gh` CLI as a Github.
//
// The only file that knows the service is a binary, the same arrangement
// `jj-cli.ts` has. Three properties are worth reading before editing it.
//
// **The exit code is the answer.** Every call goes through `capture`, never
// `spawner.string`, because `string` discards the exit code — and `gh` reports
// every refusal by printing to stderr and exiting non-zero. Through `string` an
// unauthenticated `gh pr list` is a successful empty answer, which is exactly
// what a repository with no open pull requests looks like. That was the bug in
// `zmx.ts` and it would be a worse one here, because "no PRs" is a state a
// person will believe.
//
// **Every call names its repository by running IN it**, and that is not the
// shape `Jj` uses. jj's `-R` takes a path; `gh -R` takes `OWNER/REPO` and
// refuses a directory outright:
//
//   gh pr list -R /Users/…/thicket
//   expected the "[HOST/]OWNER/REPO" format, got "/Users/…/thicket"
//
// So the repository is the child process's working directory, from which `gh`
// resolves owner and name off the remote. Which is what the Go implementation
// did — its runner took a directory — and is why that detail is worth stating
// here rather than being inferred from the neighbouring service.
//
// **A missing binary and a refusal are different sentences.** `gh` not being
// installed is the one failure where `gh`'s own words do not exist, so it is
// named explicitly — otherwise the inbox reports "command failed" for the most
// likely first-run problem there is.

import { homedir } from "node:os";
import { join } from "node:path";
import { Effect, FileSystem, Layer, Result } from "effect";
import { ChildProcess, ChildProcessSpawner } from "effect/unstable/process";
import { Github, GithubError, type Listing, type Repository } from "./github";
import {
  EXPENSIVE_FIELD,
  PR_DETAIL_FIELDS,
  PR_FIELDS,
  PR_FIELDS_CHEAP,
  knownHosts,
  onKnownHost,
  type PullRequest,
  type PullRequestDetail,
  type RawPullRequest,
  forkFetchUrl,
  forkOf,
  pullRequest,
  pullRequestDetail,
} from "./github-parse";
import { capture, said } from "./run";

/**
 * gh's answer, parsed, or a refusal naming the call rather than the parser.
 *
 * An empty answer is the fallback rather than a failure: `gh` prints nothing at
 * all for some flags and an empty list is a legitimate answer to every question
 * asked here.
 */
const json = <A>(op: string, out: string, fallback: A): Effect.Effect<A, GithubError> =>
  Effect.try({
    try: () => (out.trim() === "" ? fallback : (JSON.parse(out) as A)),
    catch: (cause) =>
      new GithubError({
        op,
        reason: `${op}: gh answered with something that is not JSON`,
        cause,
      }),
  });

/** How many open pull requests a repository's inbox is built from. */
const LIMIT = 100;

const make = Effect.gen(function* () {
  const spawner = yield* ChildProcessSpawner.ChildProcessSpawner;
  const fs = yield* FileSystem.FileSystem;

  const run = (op: string, cwd: string, command: string, args: ReadonlyArray<string>) =>
    capture(spawner, ChildProcess.make(command, [...args], { cwd })).pipe(
      Effect.mapError(
        (cause) =>
          new GithubError({
            op,
            reason: `${op}: ${command} failed (is it installed and on PATH?)`,
            cause,
          }),
      ),
      Effect.flatMap((captured) =>
        captured.exitCode === 0
          ? Effect.succeed(captured.stdout)
          : // gh's own sentence. It says which repository, and whether the
            // problem is the token, the remote or the network, and no message
            // composed at this distance would say it better.
            Effect.fail(new GithubError({ op, reason: `${op}: ${said(captured)}` })),
      ),
    );

  // Read once, at the layer's construction, because it is a property of the
  // machine rather than of a request: `gh auth login` writes this file, and
  // nobody logs into an enterprise host while a daemon is running. A daemon
  // restart is what picks up a new one, which is the same story as the agent
  // command in the config.
  const hostsFile = join(homedir(), ".config", "gh", "hosts.yml");
  const hosts = knownHosts(
    yield* fs.readFileString(hostsFile).pipe(Effect.orElseSucceed(() => undefined)),
  );

  const repository = (repo: string): Effect.Effect<Repository, GithubError> =>
    Effect.gen(function* () {
      const op = "read the repository";
      const out = yield* run(op, repo, "gh", ["repo", "view", "--json", "owner,name"]);
      const raw = yield* json<{ owner?: { login?: string }; name?: string }>(op, out, {});
      const owner = (raw.owner?.login ?? "").trim();
      const name = (raw.name ?? "").trim();
      if (owner === "" || name === "") {
        return yield* Effect.fail(
          new GithubError({ op, reason: `${op}: gh did not name the repository` }),
        );
      }
      return { owner, repo: name };
    });

  return {
    pullRequest: (
      repo: string,
      number: number,
    ): Effect.Effect<PullRequestDetail | undefined, GithubError> =>
      Effect.gen(function* () {
        const op = `read pull request #${number}`;
        const out = yield* run(op, repo, "gh", [
          "pr",
          "view",
          String(number),
          "--json",
          PR_DETAIL_FIELDS,
        ]);
        const raw = yield* json<Parameters<typeof pullRequestDetail>[0]>(op, out, {});
        return pullRequestDetail(raw);
      }),

    isGithub: (repo: string) =>
      Effect.gen(function* () {
        // `git remote -v` and not `gh`: what is being decided is whether to ask
        // gh at all, and the local answer is definitive, costs milliseconds and
        // cannot fail in an interesting way. A directory that is not a git
        // repository answers with a non-zero exit, which is `false` here rather
        // than a sentence somebody has to read every refresh.
        const out = yield* run("read the remotes", repo, "git", ["remote", "-v"]).pipe(
          Effect.orElseSucceed(() => ""),
        );
        const urls = out
          .split("\n")
          .map((line) => line.split(/\s+/u)[1] ?? "")
          .filter((url) => url !== "");
        return onKnownHost(urls, hosts);
      }),

    pullRequests: (repo: string): Effect.Effect<Listing, GithubError> =>
      Effect.gen(function* () {
        const op = "list the pull requests";
        // `--state open` and not `--state all`. Asking for everything makes
        // GitHub compute `statusCheckRollup` for a hundred mostly-closed PRs
        // that nothing renders, which the archive measured at ~7s against ~2s
        // on a busy repository. The inbox has nothing to say about a merged PR.
        const list = (fields: string) =>
          run(op, repo, "gh", [
            "pr",
            "list",
            "--state",
            "open",
            "--limit",
            String(LIMIT),
            "--json",
            fields,
          ]);

        // ── the fallback, and what it is for ──────────────────────────────
        //
        // `mergeStateStatus` makes GitHub compute mergeability for every pull
        // request in the answer, and on a busy repository that exceeds their own
        // time limit — the query *fails*, it does not answer slowly. Measured:
        // the full field set errored on a repository with a hundred open PRs
        // where the same query without that one field answered in 4.6s.
        //
        // So the choice is between one repository losing its conflicts signal
        // and that repository having no inbox at all. It asks for everything,
        // and asks again without the expensive field when that is refused.
        const whole = yield* Effect.result(list(PR_FIELDS));
        const out = Result.isSuccess(whole) ? whole.success : yield* list(PR_FIELDS_CHEAP);
        const degraded = Result.isSuccess(whole)
          ? undefined
          : `GitHub would not compute ${EXPENSIVE_FIELD} for ${LIMIT} pull requests here, so conflicts and behind-base are unknown`;
        const raws = yield* json<ReadonlyArray<RawPullRequest>>(op, out, []);
        const all = raws
          .map((raw) => pullRequest(raw))
          .filter((pr): pr is PullRequest => pr !== undefined);

        // Which of them are from forks needs to know what this repository is,
        // and that is one extra call — so it is made once per listing rather
        // than per PR, and only when there is something to attribute.
        if (all.length === 0) {
          return { prs: all, degraded };
        }
        // A failure here loses the fork/not-fork distinction and nothing else,
        // so it is worth less than the listing: every PR is then treated as
        // living on this repository, which is right for the overwhelming
        // majority and costs a fetch that finds nothing for the rest.
        const self = yield* repository(repo).pipe(Effect.orElseSucceed(() => undefined));
        return { prs: all.map((pr) => ({ ...pr, fork: forkOf(pr, self) })), degraded };
      }),

    viewer: () =>
      Effect.gen(function* () {
        // `gh api user` is about the token rather than about a repository, so
        // there is no repository to run it in — the daemon's own directory will
        // do, and is the one place in this file that is true of.
        const cwd = process.cwd();
        const login = (yield* run("read the gh login", cwd, "gh", [
          "api",
          "user",
          "--jq",
          ".login",
        ])).trim();
        if (login === "") {
          return yield* Effect.fail(
            new GithubError({
              op: "read the gh login",
              reason: "gh is not signed in — `gh auth login` fixes it",
            }),
          );
        }
        // Best effort, deliberately. Reading teams needs the `read:org` scope,
        // which `gh auth login` does not grant by default, and a login with no
        // teams is far more useful than no login at all. What it costs is that
        // a review requested from a team stays quiet.
        const teams = yield* run("read the gh teams", cwd, "gh", [
          "api",
          "user/teams",
          "--paginate",
          "--jq",
          `.[] | (.organization.login + "/" + .slug)`,
        ]).pipe(Effect.orElseSucceed(() => ""));
        return {
          login,
          teams: teams
            .split("\n")
            .map((line) => line.trim())
            .filter((line) => line !== ""),
        };
      }),

    repository,

    fetchFork: (
      repo: string,
      head: { readonly owner: string; readonly repo: string; readonly ref: string },
    ) =>
      Effect.gen(function* () {
        const op = `fetch ${head.owner}/${head.repo} ${head.ref}`;
        // origin's URL first, because the fetch URL has to be the same shape —
        // see `forkFetchUrl`, which is where the reason is written down.
        const origin = yield* run(op, repo, "git", ["remote", "get-url", "origin"]).pipe(
          Effect.orElseSucceed(() => ""),
        );
        const url = forkFetchUrl(origin, head.owner, head.repo);
        yield* run(op, repo, "git", [
          "fetch",
          "--no-tags",
          url,
          `${head.ref}:refs/heads/${head.ref}`,
        ]);
      }),
  };
});

export const layer: Layer.Layer<
  Github,
  never,
  ChildProcessSpawner.ChildProcessSpawner | FileSystem.FileSystem
> = Layer.effect(Github)(make);
