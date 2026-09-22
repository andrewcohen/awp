import { Effect } from "effect";
import { Jj, type JjError } from "./jj";
import { Projects, type ProjectStoreError } from "./projects";

/**
 * The two variables an agent is told which workspace it is in by.
 *
 * The status hooks in a person's Claude Code settings are gated on them, so an
 * agent started without them reports nothing — and an agent started with the
 * *daemon's* reports about whatever workspace the daemon was launched from.
 * `childEnv` passes everything it does not know about, so leaving these out is
 * inheriting them. Every agent the daemon starts is given both.
 */
export const agentEnv = (
  workspace: string,
  root: string,
): { readonly AWP_WORKSPACE: string; readonly AWP_REPO_ROOT: string } => ({
  AWP_WORKSPACE: workspace,
  AWP_REPO_ROOT: root,
});

/**
 * The repository a workspace is a checkout of.
 *
 * The imported project's root, else jj's answer for the directory.
 * `sourceRoot` is what makes the fallback right: a secondary workspace is not
 * the repository it is a checkout of.
 */
export const repoRoot = (
  project: string,
  dir: string,
): Effect.Effect<string, JjError | ProjectStoreError, Jj | Projects> =>
  Effect.gen(function* () {
    const imported = (yield* (yield* Projects).list()).find((one) => one.name === project);
    return imported?.root ?? (yield* (yield* Jj).sourceRoot(dir));
  });
