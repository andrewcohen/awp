// The contract, implemented over the services.
//
// Deliberately thin, and the thinness is the point: everything here is either a
// call into a service or a translation between the daemon's vocabulary and the
// client's. Anything that needed a decision would belong in a service, where it
// could be tested without an rpc around it.
//
// Kept apart from the transport for the same reason. These handlers are a
// Layer, so `RpcTest` can drive them with no socket at all — which is how the
// contract's own tests run, and how these do.

import { Jobs } from "@awp-kit/jobs";
import {
  AttachRefused,
  AwpRpcs,
  ChatUnavailable,
  CreateWorkspace as CreateWorkspaceSchema,
  DiffUnavailable,
  JobNotFound,
  NoAgent,
  type PageNote,
  type Project,
  ProjectImportFailed,
  ReviewFileFailed,
  ReviewStartFailed,
  type ReviewComment,
  type SessionIdentity,
  type SessionInfo,
  SessionNotFound,
  ThreadNotFound,
  ThreadStartFailed,
} from "@awp-kit/protocol";
import { homedir } from "node:os";
import { basename } from "node:path";
import { Clock, Effect, FileSystem, Option, Path, Schema, Stream } from "effect";
import { Chat } from "./chat";
import { InboxFeed } from "./inbox-feed";
import { type Repairable, looksMine, repairPrompt } from "./repair";
import { authored, reviewRequested, reviewRerequested } from "./github-parse";
import { type Claim, reviewKey, reviewNumber, reviewOf, reviewWorkspace } from "./inbox";
import { Jj } from "./jj";
import { archiveThreadRef } from "./jobs/archive-thread";
import { createWorkspaceRef, workspacePath } from "./jobs/create-workspace";
import { Settings, agentWith } from "./settings";
import { localBookmarks } from "./jj-parse";
import { sessionName } from "./naming";
import { Projects, discover, expand, nearestRepo } from "./projects";
import { Reviews, commentId } from "./reviews";
import { WorkspaceState } from "./workspace-state";
import { Threads } from "./threads";

/** The kind a review is delivered to. Matches PRIMARY in the renderer. */
const AGENT = "agent";

/**
 * A batch of comments, as one thing to say to an agent.
 *
 * ── the shape is the whole feature ─────────────────────────────────────────
 * This is the only place a review becomes language, and what it looks like
 * decides whether the agent can act on it. Three rules, each of which is a
 * mistake avoided rather than a preference:
 *
 * **Grouped by file, in file order.** Six comments across three files read as
 * three pieces of work; the same six interleaved read as six. An agent given a
 * flat list opens the same file three times.
 *
 * **The anchor is `path:line`, not prose.** That is the form every tool in the
 * agent's hands already takes — it can be pasted into an editor, a grep, a jump
 * — and a sentence like "in the third function of the sidebar" is a thing it
 * has to resolve before it can start.
 *
 * **The side is named only when it is a deletion.** Almost every comment is on
 * a line that is being added or kept, and saying "on the added line" against
 * every one of them is a phrase repeated down the whole prompt. Saying it for
 * the rare case is what makes it carry information.
 *
 * The heading says how many and stops. An agent counts as well as anyone, and a
 * paragraph of framing before the content is a paragraph it has to read past.
 */
export const reviewPrompt = (comments: ReadonlyArray<ReviewComment>): string => {
  const byPath = new Map<string, ReviewComment[]>();
  for (const comment of comments) {
    byPath.set(comment.path, [...(byPath.get(comment.path) ?? []), comment]);
  }

  const files = [...byPath]
    .toSorted(([a], [b]) => a.localeCompare(b))
    .map(([path, found]) => {
      const lines = found
        .toSorted((a, b) => a.line - b.line)
        .map((comment) => {
          // `path:12` for a line and `path:12-18` for a block. The same
          // spelling an editor, a stack trace and a GitHub link all use, so it
          // is a string an agent can act on without being told what it is.
          const where =
            comment.endLine > comment.line
              ? `${path}:${comment.line}-${comment.endLine}`
              : `${path}:${comment.line}`;
          const side =
            comment.side === "deletions"
              ? comment.endLine > comment.line
                ? " (on the removed lines)"
                : " (on the removed line)"
              : "";
          return `- ${where}${side}\n  ${comment.body.trim()}`;
        });
      return lines.join("\n");
    });

  const count = comments.length === 1 ? "1 comment" : `${comments.length} comments`;

  // Which revision, said once at the top rather than on every line.
  //
  // Without it `Diff.tsx:71` is an instruction to look at the working copy,
  // which is what an agent will do — and if the comment was made against a
  // commit three back, the line it names has moved or does not exist. The
  // agent then either edits the wrong line or reports that the comment makes
  // no sense, and neither failure says what actually happened.
  //
  // `@` is spelled out rather than passed through. It is jj's revset for a
  // workspace's working-copy commit and it is meaningful to jj, but a prompt is
  // read by something that may not be standing in this repository at all.
  const where = [...new Set(comments.map((one) => one.revision))].toSorted();
  const against =
    where.length === 1 && where[0] === WORKING_COPY
      ? "on the working copy"
      : `on ${where.map((one) => (one === WORKING_COPY ? "the working copy" : one)).join(", ")}`;

  return `Review feedback — ${count} ${against}:\n\n${files.join("\n\n")}`;
};

/**
 * How long an element's own text may be before it is cut.
 *
 * Enough to recognise a heading, a button or a paragraph's opening; short
 * enough that pointing at `<body>` does not paste the page into a terminal.
 * The truncation is marked, because a sentence that stops mid-word with no
 * ellipsis reads as the element being broken rather than the quote being long.
 */
const TEXT_CAP = 240;

/** `text`, fit to {@link TEXT_CAP}, with the cut said out loud. */
export const capped = (text: string): string => {
  const one = text.trim().replaceAll(/\s+/gu, " ");
  return one.length <= TEXT_CAP ? one : `${one.slice(0, TEXT_CAP - 1)}…`;
};

/**
 * One page note, as something to say to an agent.
 *
 * Four lines, in the order they answer the questions an agent actually asks:
 * what page, which element, what it said, and what is wrong with it. The
 * selector is on its own line and unquoted prose is kept away from it, because
 * it is the one field meant to be pasted into a tool rather than read.
 *
 * The remark goes **last**. Everything above it is address; a person opening
 * with "the padding is wrong" and then reading three lines of provenance has
 * to hold the complaint while parsing the location. An agent reads top to
 * bottom too, and the last line is the one it acts on.
 *
 * Every field but the first four is dropped when empty rather than printed as
 * `""`. An icon button has no text, a page that is not React has no components,
 * and a line saying so is a line about nothing.
 *
 * **`source` goes above `selector`, and that ordering is the useful part.**
 * When the page is one this repo built, StyleX has already written down the
 * file and the line — so the agent is handed somewhere to open rather than a
 * selector it would have to search the codebase for. The selector stays, for
 * the pages where it is all there is.
 */
export const notePrompt = (note: PageNote): string => {
  const said = capped(note.text);
  const react = capped(note.react ?? "");
  const source = capped(note.source ?? "");
  const lines = [
    "— a note about an element on a page",
    `page: ${note.url}`,
    `element: ${note.label}`,
    ...(source === "" ? [] : [`styles: ${source}`]),
    ...(react === "" ? [] : [`components: ${react}`]),
    `selector: ${note.selector}`,
    ...(said === "" ? [] : [`text: ${said}`]),
    "",
    note.body.trim(),
  ];
  return lines.join("\n");
};
import { refusalFor } from "./attachment";
import { readTasks, taskPrompt } from "./agent-tasks";
import { Multiplexer, type Session, identities } from "./multiplexer";
import { currentZmxSession } from "./zmx-session";
import { Sessions } from "./sessions";
import { changesUnder } from "./watch";

/**
 * The daemon's `Session` as the client is promised it.
 *
 * A function and not a cast. The two types are allowed to drift — one is what
 * zmx reports and the other is what a client is owed — and this is the single
 * place that would stop compiling when they do.
 */
const toWire = (
  session: Session,
  ownSession: string | undefined,
  identity: SessionIdentity | undefined,
): SessionInfo => ({
  name: session.name,
  pid: session.pid,
  clients: session.clients,
  startDir: session.startDir,
  ended: session.ended,
  exitCode: session.exitCode,
  created: session.created,
  cmd: session.cmd,
  labels: session.labels,
  identity,
  refusal: refusalFor(session, session.name, ownSession),
});

/**
 * The project's main line.
 *
 * jj resolves it through the remote's default bookmark and then main, master,
 * trunk — so the usual answer needs no configuration and no guessing here.
 */
const TRUNK = "trunk()";

/**
 * What a diff panel calls a stack: the working copy, and everything since the
 * main line.
 *
 * `@` is named separately rather than left to `trunk()..@` to cover the case
 * where the working copy *is* on trunk — a fresh workspace with nothing done
 * in it yet. That is a stack of one empty commit, which is the honest answer,
 * and it is not the same as an empty list.
 */
const STACK = "@ | trunk()..@";

/**
 * The fallback when the revset above will not resolve.
 *
 * `trunk()` is a revset alias with a definition that depends on the
 * repository — the remote's default bookmark, then main, then master, then
 * `root()` — and jj refuses the *whole* expression when it cannot settle on
 * one, as it does when the name it lands on is conflicted. A panel that showed
 * an error there would be showing an error about a repository whose diff it
 * can read perfectly well, so the second attempt drops the part that needed a
 * trunk and keeps the row that matters.
 *
 * When this fails too, the directory is not a repository, and *that* is worth
 * reporting — which is what happens, because nothing catches the second one.
 */
const NO_TRUNK = "@";

/** How many commits a listing hands back unless the client asks for fewer. */
const STACK_LIMIT = 50;

/**
 * What a stack patch calls itself.
 *
 * Not a change id, because a range is not a revision. It is on the answer so a
 * client can tell a stack patch apart from a working-copy one — which it
 * otherwise could not, since the two share their line numbers by design.
 */
const STACK_REVISION = "stack";

/**
 * The revision that means "the files as they are on disk right now".
 *
 * The literal `@` and not a change id, deliberately — see `Diff` in the
 * contract. It is also what the reply echoes, so a client can tell the live
 * answer apart from a historical one it asked for by name.
 */
const WORKING_COPY = "@";

/**
 * The workspace a bookmark names, if it names one of awp's.
 *
 * Convention, not a lookup: `create-workspace` composes a bookmark as
 * `<prefix>/<workspace>`, so the prefix comes back off. It is a guess in
 * exactly one direction — a person's own `andrew/some-branch` looks the
 * same and will be reported as a workspace called `some-branch`. That is
 * harmless: the field decides which entry is preselected and which thread
 * gets recorded as the parent, and being wrong means neither happens.
 */
const workspaceOf = (bookmark: string, prefix: string | undefined): string | undefined =>
  prefix !== undefined && bookmark.startsWith(`${prefix}/`)
    ? bookmark.slice(prefix.length + 1)
    : undefined;

export const layer = AwpRpcs.toLayer(
  Effect.gen(function* () {
    const mux = yield* Multiplexer;
    const sessions = yield* Sessions;
    const jobs = yield* Jobs;
    const threads = yield* Threads;
    const reviews = yield* Reviews;
    const projects = yield* Projects;
    const inbox = yield* InboxFeed;
    const facts = yield* WorkspaceState;
    const config = yield* Settings;
    const jj = yield* Jj;
    const chat = yield* Chat;
    // Taken once, here, rather than per request. A handler's return value has
    // to name no requirements — the rpc layer is what settles them — so the
    // watcher's file system is closed over instead of being asked for inside
    // the stream.
    const files = yield* FileSystem.FileSystem;
    // Path for the same reason, and provided back to `discover` below rather
    // than left in its requirements: a handler's effect must name none.
    const paths = yield* Path.Path;

    /**
     * The revision a thread's work is at, for a thread branching off it.
     *
     * **The bookmark, not the working copy**, and that is the whole point of
     * this function. `<name>@` is jj's revset for a workspace's working-copy
     * commit, which carries whatever is half-finished in it *right now* — so a
     * thread based on it inherits someone's uncommitted edits, which is not
     * what "branch off this work" means. The bookmark is where the work is
     * named, and it moves when a person decides it should:
     *
     *   andrew/tiered-discounts   the branch, moved deliberately     ← this
     *   tiered-discounts@         the working copy, moving constantly
     *
     * The fallback to the working copy is deliberate rather than a failure. A
     * thread created with no `bookmark_prefix` configured has no bookmark at
     * all, and refusing there would make the feature unavailable to anyone who
     * has not set one. Starting from the working copy is worse; having nothing
     * to start from is worse still.
     *
     * Only the first member is consulted. A thread can hold several workspaces
     * and there is no rule yet for which of them a child should follow; the
     * first is the one it was created with, which is the only one this can
     * claim to know something about.
     *
     * `repo` is the repository the *new* workspace is being made in, and the
     * parent's workspace has to live in it — a revision is only meaningful
     * inside one repository. Refused rather than resolved across projects,
     * which would produce a revset jj cannot find and a failure one backoff
     * later inside the job.
     */
    /** The thread holding this workspace, if any does. */
    const threadOwning = (workspace: string | undefined, project: string) =>
      workspace === undefined
        ? Effect.succeed(undefined)
        : threads.list().pipe(
            Effect.orDie,
            Effect.map(
              (all) =>
                all.find(
                  (thread) =>
                    thread.archivedAt === undefined &&
                    thread.members.some(
                      (member) => member.project === project && member.workspace === workspace,
                    ),
                )?.id,
            ),
          );

    const baseOfThread = (id: string, project: string, repo: string) =>
      Effect.gen(function* () {
        const found = yield* threads.list().pipe(Effect.orDie);
        const thread = found.find((entry) => entry.id === id);
        const member = thread?.members[0];
        if (member === undefined) {
          // A thread with no workspace yet has no work to branch from. Its own
          // sentence rather than a silent fall back to trunk, which would put
          // the new thread somewhere the person did not ask for and say
          // nothing about it.
          return yield* Effect.fail(
            new ThreadStartFailed({
              reason:
                thread === undefined
                  ? `no thread ${id} to start from`
                  : `"${thread.title}" has no workspace yet, so there is nothing to start from`,
            }),
          );
        }

        if (member.project !== project) {
          return yield* Effect.fail(
            new ThreadStartFailed({
              reason: `"${thread?.title ?? id}" is in ${member.project}, so ${project} cannot start from it`,
            }),
          );
        }

        const settings = yield* config.read();
        const prefix = settings.bookmarkPrefix;
        if (prefix === undefined) {
          return `${member.workspace}@`;
        }

        // Asked rather than assumed. The prefix says what awp *would* have
        // named it; only jj says whether that bookmark is there, and a
        // revision that does not exist fails inside the job — one backoff
        // later, in a message about the wrong thing.
        const wanted = `${prefix}/${member.workspace}`;
        const all = yield* jj
          .bookmarks(repo)
          .pipe(Effect.mapError((error) => new ThreadStartFailed({ reason: error.reason })));
        return localBookmarks(all).some((entry) => entry.name === wanted)
          ? wanted
          : `${member.workspace}@`;
      });

    /**
     * Whether a workspace's working copy has a pull request's head behind it.
     *
     * ── `present()`, and why it is one call rather than three ────────────────
     *
     * Asked as "is the head an ancestor of `@`" and not "is it equal to `@`",
     * because a person who has committed something of their own on top is still
     * reviewing the right code.
     *
     * Without `present()` an absent commit is an *error* — `Revision
     * \`deadbeef…\` doesn't exist`, which is exactly what a force-push leaves
     * behind — and the caller would have to tell that apart from a broken
     * directory by reading jj's prose. With it, an absent commit is an empty
     * answer, which is the same conclusion as having something older: this
     * checkout does not contain what the pull request is.
     *
     * A directory that is not a workspace at all answers `true` — "nothing to
     * repair" — rather than reporting a stale checkout that is not there. Saying
     * nothing is the safer of the two wrong answers.
     */
    const containsHead = (
      member: { readonly project: string; readonly workspace: string },
      headOid: string,
    ) =>
      jj
        .revisions({
          dir: workspacePath(member.project, member.workspace),
          revset: `present(${headOid}) & ::@`,
          limit: 1,
        })
        .pipe(
          Effect.map((found) => found.length > 0),
          Effect.orElseSucceed(() => true),
        );

    /**
     * Which workspace a directory is in, or a refusal saying it is in none.
     *
     * `~/.awp/workspaces/<project>/<workspace>` is the convention every other
     * part of awp already reads — `suggestedBy` in multiplexer.ts recovers a
     * session's identity from exactly this shape — so a directory inside one
     * resolves without asking anything.
     *
     * A refusal and not a guess. The alternative is what the Go implementation
     * did by accident: an agent that ran the command in the source repository
     * filed seven findings into that repository's own review, and both sides
     * reported success. A sentence naming the directory is the only thing that
     * makes that visible from the agent's end.
     */
    const workspaceAt = (from: string) =>
      Effect.gen(function* () {
        const root = paths.join(homedir(), ".awp", "workspaces");
        const full = paths.resolve(from);
        if (full !== root && !full.startsWith(`${root}/`)) {
          return yield* Effect.fail(
            new ReviewFileFailed({
              reason: `${full} is not inside an awp workspace — run this from the workspace being reviewed`,
            }),
          );
        }
        const [project, workspace] = full.slice(root.length + 1).split("/");
        if (project === undefined || workspace === undefined || workspace === "") {
          return yield* Effect.fail(
            new ReviewFileFailed({
              reason: `${full} is the workspaces directory itself, not a workspace in it`,
            }),
          );
        }
        return { project, workspace, dir: paths.join(root, project, workspace) };
      });

    // A job the client named and the daemon has never heard of. Its own
    // failure rather than a defect: asking about a job that was cleaned up, or
    // typing an id, is a question with a negative answer.
    const known = (id: string) =>
      jobs.get(id).pipe(
        Effect.orDie,
        Effect.flatMap((job) =>
          job === undefined ? Effect.fail(new JobNotFound({ job: id })) : Effect.succeed(job),
        ),
      );

    /**
     * Every project awp knows about: the imported rows, plus what the running
     * sessions imply.
     *
     * A closure rather than the body of `ProjectList`, because the inbox is
     * over projects too and a second way of working out which those are would
     * be a second answer to the same question. Merged here rather than in a
     * client because only the daemon holds both halves; the imported row wins,
     * being the one that survives a restart and the one `forget` applies to.
     */
    const allProjects = () =>
      Effect.gen(function* () {
        const imported = yield* projects.list().pipe(Effect.orDie);
        const recorded = new Set(imported.map((one) => one.name));
        const all = yield* mux.list().pipe(Effect.orDie);
        const found = identities(all);

        // One entry per project, so the jj call below happens once per
        // project rather than once per session — a machine with thirty
        // sessions has perhaps four projects.
        const dirs = new Map<string, string>();
        for (const session of all) {
          const project = found.get(session.name)?.project;
          const from = session.startDir;
          if (project === undefined || from === undefined || from === "") continue;
          if (recorded.has(project) || dirs.has(project)) continue;
          dirs.set(project, from);
        }

        const derived: Project[] = [];
        for (const [name, from] of dirs) {
          const root = yield* jj.sourceRoot(from).pipe(Effect.option);
          if (Option.isSome(root)) {
            derived.push({ name, root: root.value, importedAt: undefined });
          }
        }

        return [...imported, ...derived.toSorted((a, b) => a.name.localeCompare(b.name))];
      });

    return {
      // No declared error, so a failure here is a defect. That is the honest
      // shape: listing fails when zmx is missing or unrunnable, which is the
      // daemon being broken rather than a question with a negative answer. An
      // empty list is what "no sessions" looks like.
      SessionList: () =>
        mux.list().pipe(
          Effect.map((all) => {
            // Read once per listing rather than once per session: it is the
            // same answer for all of them, and it comes from this process's
            // environment, which does not change while a list is being built.
            const own = currentZmxSession();
            // Resolved for the listing rather than per session: repairing an
            // unlabelled session's workspace needs its labelled siblings, which
            // only exist in the context of the whole list.
            const found = identities(all);
            return all.map((session) => toWire(session, own, found.get(session.name)));
          }),
          Effect.orDie,
        ),

      Attach: ({ session, cols, rows }) =>
        Stream.unwrap(
          sessions.attach(session, { cols, rows }).pipe(
            // Two error channels here, and they need different answers. This
            // one is the stream's: the pty failing partway through a session
            // that attached successfully. There is nothing a client can do
            // about it and no refusal to render, so it becomes a defect.
            Effect.map(Stream.orDie),
            // And this one is the attach's, handled a tag at a time.
            //
            // `catchTags` and not `catchTag` followed by `orDie`: orDie is not
            // selective, so it would convert the AttachRefused just created
            // into a defect, and the client would see the daemon crash instead
            // of a session politely declining to open. Listing every tag also
            // means a new failure mode in a service below is a type error here
            // rather than a silent reclassification.
            Effect.catchTags({
              // Declining is a normal outcome the client is expected to show:
              // the session ended, or it is the daemon's own. It crosses the
              // wire as itself, reason and all.
              AttachError: (error) =>
                Effect.fail(new AttachRefused({ session, reason: error.reason })),
              // zmx missing or unrunnable, and a pty that would not open. The
              // daemon being broken, rather than the request being refusable.
              MultiplexerError: (error) => Effect.die(error),
              PtyError: (error) => Effect.die(error),
            }),
          ),
        ),

      Write: ({ session, data }) =>
        sessions.write(session, data).pipe(
          Effect.catchTags({
            NotAttached: () => Effect.fail(new SessionNotFound({ session })),
            PtyError: (error) => Effect.die(error),
          }),
        ),

      Resize: ({ session, cols, rows }) =>
        sessions.resize(session, { cols, rows }).pipe(
          Effect.catchTags({
            NotAttached: () => Effect.fail(new SessionNotFound({ session })),
            PtyError: (error) => Effect.die(error),
          }),
        ),

      // No declared error, for the same reason SessionList has none: a store
      // that cannot be read is the daemon being broken, and an empty list is
      // what "no jobs" looks like.
      JobList: () => jobs.list().pipe(Effect.orDie),

      // The stream's lifetime is the request's, so a client that goes away
      // unsubscribes from the feed. Nothing has to notice the disconnection.
      JobChanges: () => jobs.changes,

      // The conversation, and the two calls that add to it.
      //
      // A refusal here is a sentence, not a defect: the adapter not being
      // installed and there being no claude on the PATH are both things a
      // person fixes, and both are what `ChatError` already carries. Dying
      // would take the window's socket with it for a condition it could have
      // rendered.
      ChatOpen: ({ project, workspace }) =>
        Stream.unwrap(
          chat
            .open(project, workspace)
            .pipe(Effect.mapError((error) => new ChatUnavailable({ reason: error.reason }))),
        ),

      ChatSend: ({ project, workspace, text }) =>
        chat
          .send(project, workspace, text)
          .pipe(Effect.mapError((error) => new ChatUnavailable({ reason: error.reason }))),

      ChatAnswer: ({ project, workspace, request, option }) =>
        chat
          .answer(project, workspace, request, option)
          .pipe(Effect.mapError((error) => new ChatUnavailable({ reason: error.reason }))),

      ChatConfig: ({ project, workspace }) =>
        chat
          .config(project, workspace)
          .pipe(Effect.mapError((error) => new ChatUnavailable({ reason: error.reason }))),

      ChatSet: ({ project, workspace, option, value }) =>
        chat
          .set(project, workspace, option, value)
          .pipe(Effect.mapError((error) => new ChatUnavailable({ reason: error.reason }))),

      JobLog: ({ job }) => known(job).pipe(Effect.flatMap(() => jobs.log(job).pipe(Effect.orDie))),

      // `retry` returns the record unchanged for a job that is still running,
      // which is a truer answer than a failure — it *is* already trying.
      JobRetry: ({ job }) =>
        known(job).pipe(
          Effect.flatMap(() => jobs.retry(job).pipe(Effect.orDie)),
          Effect.flatMap((updated) =>
            updated === undefined ? Effect.fail(new JobNotFound({ job })) : Effect.succeed(updated),
          ),
        ),

      JobCancel: ({ job }) =>
        known(job).pipe(Effect.flatMap(() => jobs.cancel(job).pipe(Effect.orDie))),

      JobClear: () => jobs.forgetFinished().pipe(Effect.orDie),

      // ── the diff panel's two calls ──────────────────────────────────────

      Revisions: ({ from, limit }) =>
        jj.revisions({ dir: from, revset: STACK, limit: limit ?? STACK_LIMIT }).pipe(
          // Retried with a smaller question rather than reported. See NO_TRUNK:
          // the first refusal is about the revset, not about the repository.
          Effect.catchTag("JjError", () => jj.revisions({ dir: from, revset: NO_TRUNK, limit: 1 })),
          Effect.mapError((error) => new DiffUnavailable({ reason: error.reason })),
        ),

      /**
       * The patch, and the rule about snapshotting on the way.
       *
       * A revision named by the client is history: it cannot change, so the
       * read passes `--ignore-working-copy` and writes nothing. No revision
       * means the working copy, which *does* change and changes for the reason
       * the panel is open — so that one read is allowed to snapshot first.
       *
       * `@` arriving explicitly is treated as absent. It is the same request
       * said a different way, and the alternative is a call that silently
       * returns yesterday's answer because of which spelling was used.
       */
      // ── one revision, or everything since the main line ─────────────────
      //
      // The stack is `--from trunk() --to @`: the net effect of the work,
      // which is what a person reviews before shipping. Not a concatenation of
      // the commits — a file touched in three of them appears once, with its
      // final shape, and the ordering is what the revision list is for.
      //
      // **Snapshotted, like the working copy and unlike a named revision.**
      // `@` is the working-copy commit, so the far end of the range is the
      // files on disk; without the snapshot a workspace where an agent has
      // edited six files and run no jj command would diff as though it had
      // not. That also makes the line numbers in a stack patch the same line
      // numbers the working copy's patch has, which is what lets a comment on
      // one be anchored to the other — see WORKING_COPY below.
      //
      // The fallback is the same one the revision list has and for the same
      // reason: `trunk()` does not resolve in a repository with no main line,
      // and answering "no diff" there would be a panel that looks broken in a
      // fresh repo. See NO_TRUNK.
      Diff: ({ from, revision, stack }) => {
        if (stack === true) {
          return jj.diff({ dir: from, revision: NO_TRUNK, from: TRUNK, snapshot: true }).pipe(
            Effect.catchTag("JjError", () =>
              jj.diff({ dir: from, revision: NO_TRUNK, snapshot: true }),
            ),
            // Named `stack` on the way back, so a client can tell the answer
            // apart from a working-copy patch that happens to look the same.
            Effect.map((patch) => ({ revision: STACK_REVISION, patch })),
            Effect.mapError((error) => new DiffUnavailable({ reason: error.reason })),
          );
        }
        const at = revision === undefined || revision === WORKING_COPY ? WORKING_COPY : revision;
        return jj.diff({ dir: from, revision: at, snapshot: at === WORKING_COPY }).pipe(
          Effect.map((patch) => ({ revision: at, patch })),
          Effect.mapError((error) => new DiffUnavailable({ reason: error.reason })),
        );
      },

      /**
       * A tick per burst of writes under the workspace.
       *
       * The client asks for the patch it wants when one arrives — see the
       * contract, and `watch.ts` for why `.jj` is not watched.
       */
      WorkspaceChanges: ({ from }) =>
        changesUnder(from).pipe(Stream.provideService(FileSystem.FileSystem, files)),

      // Returns the record rather than the workspace, because the work outlives
      // the request. Whether it succeeded is a question for JobChanges.
      WorkspaceCreate: (payload) => jobs.enqueue(createWorkspaceRef, payload).pipe(Effect.orDie),

      /**
       * The one call the new-thread box makes: resolve, make the thread,
       * enqueue the job.
       *
       * In this order for a reason. The thread is made *after* the model
       * answers, so a failed resolve leaves nothing behind — an empty thread
       * called "add tiered dis…" would be litter a person then has to tidy.
       */
      ThreadBases: ({ from }) =>
        Effect.gen(function* () {
          const repo = yield* jj
            .sourceRoot(from)
            .pipe(Effect.mapError((error) => new ThreadStartFailed({ reason: error.reason })));

          const settings = yield* config.read();
          const all = yield* jj
            .bookmarks(repo)
            .pipe(Effect.mapError((error) => new ThreadStartFailed({ reason: error.reason })));

          // Local only. A name that exists solely on a remote cannot be
          // branched from without fetching first, so offering it would be
          // offering a failure.
          // ── what trunk() is actually called ────────────────────────────
          //
          // The row used to be labelled "trunk", which names the *method* and
          // not the place: `trunk()` is jj's alias for the remote's default
          // bookmark, then main, then master. A person branching from it wants
          // to read the name they would say out loud.
          //
          // Resolved by asking where trunk() points and looking that commit up
          // in the bookmark list already in hand — no second call. Measured on
          // this machine, where the two are not the same commit:
          //
          //   trunk()                 9f239c56
          //   main   remote origin →  9f239c56    ← the match, and the label
          //   main   local        →  158b02fe    behind by a fetch
          //
          // A local match is preferred when there is one, because a local name
          // is what a person types. Remote is spelled the way jj spells it.
          const trunkAt = yield* jj
            .revisions({ dir: repo, revset: TRUNK, limit: 1 })
            .pipe(Effect.orElseSucceed(() => []));
          const at = trunkAt[0]?.commitId;
          const pointing =
            at === undefined || at === "" ? [] : all.filter((entry) => entry.target === at);
          const named = pointing.find((entry) => entry.remote === undefined) ?? pointing[0];
          // A repository with no bookmark at its main line still has a main
          // line, so the row stays and is named for what it is. An empty label
          // would be worse than a generic one.
          const trunkLabel =
            named === undefined
              ? "main line"
              : named.remote === undefined
                ? named.name
                : `${named.name}@${named.remote}`;

          const bookmarks = localBookmarks(all)
            // The local bookmark trunk() resolved to would otherwise appear
            // twice under one name, once with the robust revset and once with
            // its own. The trunk row is the one to keep: it survives the
            // bookmark being moved or renamed.
            .filter((entry) => entry.name !== trunkLabel)
            .map((entry) => ({
              revset: entry.name,
              label: entry.name,
              workspace: workspaceOf(entry.name, settings.bookmarkPrefix),
            }));

          return [
            { revset: TRUNK, label: trunkLabel, workspace: undefined },
            ...bookmarks.toSorted((a, b) => a.label.localeCompare(b.label)),
          ];
        }),

      ThreadStart: ({ description, project, from, parent, base, model, effort }) =>
        Effect.gen(function* () {
          // Refused before anything else, and cheaply. Naming happens inside
          // the job now, so this is no longer caught by the model declining an
          // empty sentence — and a thread whose whole content is a blank line
          // is litter a person then has to tidy.
          if (description.trim() === "") {
            return yield* Effect.fail(
              new ThreadStartFailed({ reason: "say what you are working on first" }),
            );
          }

          // A directory into the repository it belongs to. `jj root` would
          // answer with a workspace, which is the wrong thing to create a
          // second workspace from.
          const repo = yield* jj
            .sourceRoot(from)
            .pipe(Effect.mapError((error) => new ThreadStartFailed({ reason: error.reason })));

          const settings = yield* config.read();

          // Where the new workspace starts. An explicit revision wins; then a
          // parent thread, resolved to *its* bookmark; then the main line.
          const startFrom =
            base ?? (parent === undefined ? TRUNK : yield* baseOfThread(parent, project, repo));

          // Which thread this follows on from. Named outright when a caller
          // said so; otherwise recovered from the base, because the window
          // picks a *bookmark* and a bookmark names a workspace, and a
          // workspace may belong to a thread.
          //
          // Recovered rather than required, so branching off a workspace that
          // no thread has claimed still works — which is the ordinary case on
          // a machine whose workspaces predate threads, and was the whole
          // reason the picker used to come up empty.
          const followsFrom =
            parent ??
            (startFrom === TRUNK
              ? undefined
              : yield* threadOwning(workspaceOf(startFrom, settings.bookmarkPrefix), project));

          // Titled with what was typed, because nothing better exists yet. The
          // job's first step asks a model for a proper one and renames it —
          // which is a title that improves ten seconds later, rather than a
          // window that will not close for ten seconds.
          const thread = yield* threads.create(description.trim(), followsFrom).pipe(Effect.orDie);

          const job = yield* jobs
            .enqueue(createWorkspaceRef, {
              thread: thread.id,
              // Recorded, not re-derived. `followsFrom` was resolved from the
              // chosen base a moment ago and the job may have to rebuild this
              // thread on a retry — see the `thread` step. A resumed job has
              // only its record.
              threadParent: followsFrom,
              project,
              description,
              repo,
              base: startFrom,
              agent: agentWith(settings, { model, effort }),
            })
            .pipe(Effect.orDie);

          return { thread, job };
        }),

      /**
       * Every open pull request, sectioned and ordered. See `inbox.ts`.
       *
       * Thin, like every handler here: the projects come from `allProjects`,
       * the rows and their cache from `InboxFeed`, and the only thing composed
       * on the spot is the join between the two records awp holds — a thread
       * and its members — and the pull request a workspace's name identifies.
       */
      InboxList: ({ refresh }) =>
        Effect.gen(function* () {
          const projectList = yield* allProjects();
          const held = yield* threads.list().pipe(Effect.orDie);
          const running = yield* jobs.list().pipe(Effect.orDie);
          const live = yield* mux.list().pipe(Effect.orDie);

          // ── what awp already has for a pull request, from three records ────
          //
          // All three are keyed by the workspace *name*, which is the whole
          // reason it is `pr-<number>` and nothing more — see
          // `reviewWorkspace`. Nothing extra is stored to make a row idempotent.
          //
          //   a thread member      the review is finished, or its claim landed
          //   a live session       the workspace EXISTS and can be opened,
          //                        which happens a step earlier than the claim
          //   a job with the key   one is being built, or one failed
          //
          // The session is the source that was missing. The claim is the create
          // job's second-to-last step, so a row built on threads alone said
          // nothing for the thirty seconds between the session appearing and
          // the job ending — the window a person is actually watching.
          const found = new Map<
            string,
            {
              workspace: string | undefined;
              thread: string | undefined;
              job: string | undefined;
              moved: boolean;
            }
          >();
          const at = (project: string, number: number) => {
            const key = `${project}:${number}`;
            const already = found.get(key);
            if (already !== undefined) {
              return already;
            }
            const fresh = {
              workspace: undefined,
              thread: undefined,
              job: undefined,
              moved: false,
            };
            found.set(key, fresh);
            return fresh;
          };
          const note = (
            project: string,
            number: number,
            what: Partial<{ workspace: string; thread: string; job: string; moved: boolean }>,
          ) => {
            found.set(`${project}:${number}`, { ...at(project, number), ...what });
          };

          for (const thread of held) {
            if (thread.archivedAt !== undefined) {
              continue;
            }
            for (const member of thread.members) {
              const number = reviewNumber(member.workspace);
              if (number !== undefined) {
                note(member.project, number, { workspace: member.workspace, thread: thread.id });
              }
            }
          }

          // The recorded link, which is the claim rather than the convention.
          // After the name-based recovery above so it wins: a thread that says
          // outright which pull request it is about beats a workspace whose
          // name happens to encode one, and the two only disagree when somebody
          // has renamed or re-linked something deliberately.
          for (const thread of held) {
            if (thread.archivedAt !== undefined) {
              continue;
            }
            for (const pr of thread.prs) {
              note(pr.project, pr.number, { thread: thread.id });
            }
          }

          // The unshortened truth, from the labels awp wrote — a session name
          // is shortened and cannot be split back into its parts.
          const identified = identities(live);
          for (const session of live) {
            const identity = identified.get(session.name);
            const number = identity === undefined ? undefined : reviewNumber(identity.workspace);
            if (identity !== undefined && number !== undefined) {
              note(identity.project, number, { workspace: identity.workspace });
            }
          }

          for (const job of running) {
            // By key rather than by the job's stored input: one string
            // comparison per job, where the input is a schema decode per job on
            // every listing. See `reviewOf`.
            const about = reviewOf(job.key);
            if (about !== undefined) {
              note(about.project, about.number, { job: job.id });
            }
          }

          const claimed: Claim = (project, number) => found.get(`${project}:${number}`);

          const answer = yield* inbox.read({
            projects: projectList,
            refresh: refresh === true,
            claimed,
            contains: containsHead,
          });

          // ── a pull request opened for work that already had a workspace ────
          //
          // The commonest case there is, and nothing above finds it: somebody
          // starts a thread, works, pushes, opens a PR. The workspace is not
          // called `pr-<n>` — it is called after the work — and no link was
          // recorded, because at the moment the thread was made there was no
          // pull request to link.
          //
          // What identifies it is the head branch. awp names a workspace's
          // bookmark `<prefix>/<workspace>`, so a PR whose head ref is
          // `andrew/typed-router-headers` is the pull request *for* the
          // workspace `typed-router-headers` — and there is no ambiguity
          // in it, because the prefix is this person's own.
          //
          // **It is recorded, not merely reported**, and this is a write during
          // a read — which this codebase is otherwise careful about (see the
          // note on `--ignore-working-copy`). The argument for it: the
          // conclusion is certain rather than inferred, the write is idempotent
          // and only ever adds, and recording it is what makes the link survive
          // the branch being renamed — which is exactly when the evidence
          // disappears. Deriving it every time instead would mean the PR panel
          // and the sidebar each need the pull request list to answer "which PR
          // is this workspace about", and the list is the one thing they do not
          // have.
          const settings = yield* config.read();
          const prefix = settings.bookmarkPrefix;
          const items = yield* Effect.forEach(answer.items, (item) =>
            Effect.gen(function* () {
              if (item.thread !== undefined || item.workspace !== undefined) {
                return item;
              }
              const workspace = workspaceOf(item.headRef, prefix);
              if (workspace === undefined) {
                return item;
              }
              const holding = held.find(
                (thread) =>
                  thread.archivedAt === undefined &&
                  thread.members.some(
                    (member) => member.project === item.project && member.workspace === workspace,
                  ),
              );
              if (holding === undefined) {
                return item;
              }
              yield* threads
                .link(holding.id, { project: item.project, number: item.number })
                .pipe(Effect.ignore);
              // The workspace as well as the thread: the row's action is
              // decided by whether there is something to open, and there is —
              // it said "makes a workspace" for a workspace already on disk.
              return { ...item, workspace, thread: holding.id };
            }),
          );

          return { ...answer, items };
        }),

      /**
       * One pull request, by project and number. See the contract.
       *
       * `ReviewStartFailed` is reused rather than a fourth error type minted:
       * every reason is the same reason — awp does not know that project, or
       * `gh` would not answer — and the sentence is what a panel shows.
       */
      PullRequestView: ({ project, number, refresh }) =>
        Effect.gen(function* () {
          const projectList = yield* allProjects();
          const found = projectList.find((one) => one.name === project);
          if (found === undefined) {
            return yield* Effect.fail(
              new ReviewStartFailed({
                project,
                number,
                reason: `awp knows no project called ${project}`,
              }),
            );
          }
          // Through the feed, not straight at `gh`: the panel is unmounted every
          // time somebody looks at the diff instead, so an uncached read here
          // would be a second of nothing on every tab switch.
          const detail = yield* inbox
            .detail(found.root, number, refresh === true)
            .pipe(
              Effect.mapError(
                (error) => new ReviewStartFailed({ project, number, reason: error.reason }),
              ),
            );
          if (detail === undefined) {
            return undefined;
          }

          // Which workspace is reviewing it, and whether that checkout still
          // contains it. Both are on the answer so the panel can offer the
          // repair without the inbox having been read at all — it is opened
          // *from* a workspace, and the inbox may never have been looked at.
          //
          // The recorded link first, then the `pr-<n>` naming, which is the same
          // order `InboxList` uses and for the same reason: the link is the
          // claim and the name is the convention.
          const held = yield* threads.list().pipe(Effect.orDie);
          const live = held.filter((thread) => thread.archivedAt === undefined);
          const linked = live.find((thread) =>
            thread.prs.some((pr) => pr.project === project && pr.number === number),
          );
          const named = live.find((thread) =>
            thread.members.some(
              (member) => member.project === project && reviewNumber(member.workspace) === number,
            ),
          );
          const member =
            linked?.members.find((one) => one.project === project) ??
            named?.members.find(
              (one) => one.project === project && reviewNumber(one.workspace) === number,
            );

          const moved =
            member === undefined
              ? false
              : !(yield* containsHead({ project, workspace: member.workspace }, detail.headOid));

          return { ...detail, project, workspace: member?.workspace, moved };
        }),

      /**
       * The prompt for what is wrong with a pull request. See `repair.ts`.
       *
       * Built from the *listing* entry rather than the detail, because the
       * viewer-relative answers live there — who asked for a review, and who
       * has already given one — and from the `gh` login, which decides the tone.
       * Neither reaches a client on `PullRequest`, and neither should: they are
       * an answer about this machine's login.
       */
      PullRequestRepair: ({ project, number }) =>
        Effect.gen(function* () {
          const projectList = yield* allProjects();
          const found = projectList.find((one) => one.name === project);
          if (found === undefined) {
            return yield* Effect.fail(
              new ReviewStartFailed({
                project,
                number,
                reason: `awp knows no project called ${project}`,
              }),
            );
          }

          const pr = yield* inbox
            .find(found.root, number)
            .pipe(
              Effect.mapError(
                (error) => new ReviewStartFailed({ project, number, reason: error.reason }),
              ),
            );
          if (pr === undefined) {
            return yield* Effect.fail(
              new ReviewStartFailed({
                project,
                number,
                reason: `#${number} is not an open pull request in ${project}`,
              }),
            );
          }

          const who = yield* inbox.who();
          const settings = yield* config.read();
          const target: Repairable = {
            number: pr.number,
            url: pr.url,
            // The listing is open pull requests only, so anything it answers
            // with is open. Stated rather than assumed, because `repairPrompt`
            // refuses on any other state and a caller reading it should see why
            // that branch is unreachable from here.
            state: "open",
            headRef: pr.headRef,
            ci: pr.ci,
            review: pr.review,
            mergeState: pr.mergeState,
            hasReviewComments: pr.hasReviewComments,
            mine: authored(pr, who),
            reviewRequested: reviewRequested(pr, who),
            reviewRerequested: reviewRerequested(pr, who),
          };

          // Whether the local checkout is behind, which is an issue in both
          // tones — it is a fact about this copy rather than about the pull
          // request. Only askable when a workspace exists.
          const held = yield* threads.list().pipe(Effect.orDie);
          const member = held
            .filter((thread) => thread.archivedAt === undefined)
            .flatMap((thread) =>
              thread.prs.some((one) => one.project === project && one.number === number)
                ? thread.members.filter((one) => one.project === project)
                : [],
            )[0];
          const moved =
            member === undefined
              ? false
              : !(yield* containsHead({ project, workspace: member.workspace }, pr.headOid));

          const mine = target.mine || looksMine(target, settings.bookmarkPrefix);
          const prompt = repairPrompt(target, { mine, moved });
          if (prompt === "") {
            // Nothing wrong, so nothing said. Answered rather than refused: the
            // button was pressed on a pull request that is fine, which is a
            // thing to be told rather than an error.
            return { prompt, mine, workspace: undefined };
          }
          if (member === undefined) {
            // There is something to say and nowhere to say it. `NoAgent` names
            // the pull request's own project and number rather than a workspace,
            // because the missing thing *is* the workspace.
            return yield* Effect.fail(new NoAgent({ project, workspace: `#${String(number)}` }));
          }

          const name = sessionName(project, member.workspace, AGENT);
          const session = yield* mux.lookup(name).pipe(Effect.orDie);
          if (session === undefined || session.ended) {
            return yield* Effect.fail(new NoAgent({ project, workspace: member.workspace }));
          }
          yield* mux.send(name, prompt).pipe(Effect.orDie);
          return { prompt, mine, workspace: member.workspace };
        }),

      /**
       * Make a thread and a workspace for reviewing a pull request, once.
       *
       * ── idempotent by two mechanisms, and it needs both ──────────────────
       *
       *   the thread holding `pr-<n>`   a review that finished. Its job record
       *                                 may have been cleared, and the
       *                                 workspace is still there
       *   the job's idempotency key     a review that is still being built.
       *                                 The claim is the job's second-to-last
       *                                 step, so a running job holds a thread
       *                                 no member lookup can find
       *
       * The key is also what makes the button safe to press twice in one
       * second: `enqueue` answers with the first job rather than making a
       * second. What that costs is spelled out below, at the one place it can
       * be seen — a thread made a moment before losing that race.
       */
      ReviewStart: ({ project, number }) =>
        Effect.gen(function* () {
          const declined = (reason: string) => new ReviewStartFailed({ project, number, reason });

          const projectList = yield* allProjects();
          const found = projectList.find((one) => one.name === project);
          if (found === undefined) {
            return yield* Effect.fail(declined(`awp knows no project called ${project}`));
          }

          const workspace = reviewWorkspace(number);
          // Shared with `InboxList`, which has to find the same job — see
          // `reviewKey`.
          const key = reviewKey(project, number);

          const held = yield* threads.list().pipe(Effect.orDie);
          const running = yield* jobs.list().pipe(Effect.orDie);
          const already = running.find((job) => job.key === key);

          const holding = held.find(
            (thread) =>
              thread.archivedAt === undefined &&
              thread.members.some(
                (member) => member.project === project && member.workspace === workspace,
              ),
          );
          if (holding !== undefined) {
            return { thread: holding, job: already, workspace, created: false };
          }

          if (already !== undefined) {
            // The job's own record, decoded, because the thread it is building
            // for is on it and nowhere else this can reach: the claim that
            // would make the thread findable by member is that job's
            // second-to-last step.
            const input = yield* Schema.decodeUnknownEffect(CreateWorkspaceSchema)(
              already.input,
            ).pipe(Effect.option);
            const owner = Option.isSome(input)
              ? held.find((thread) => thread.id === input.value.thread)
              : undefined;
            if (owner !== undefined) {
              return { thread: owner, job: already, workspace, created: false };
            }
          }

          const pr = yield* inbox
            .find(found.root, number)
            .pipe(Effect.mapError((error) => declined(error.reason)));
          if (pr === undefined) {
            return yield* Effect.fail(
              declined(`#${number} is not an open pull request in ${project}`),
            );
          }

          const settings = yield* config.read();
          // `#123 title`, which is what the sidebar shows. Not asked of a model,
          // unlike a thread started from a sentence: GitHub has already been
          // given a title for this work by the person who opened the PR, and
          // paraphrasing it would only make the row harder to match against the
          // page it came from.
          const label = `#${number} ${pr.title}`.trim();

          const thread = yield* threads.create(label).pipe(Effect.orDie);
          // Linked here, at the moment the thread exists, so the sidebar and
          // the inbox row can name the pull request immediately rather than
          // waiting for a job that takes half a minute. The job restores the
          // link if a rollback takes the thread — see the `thread` step, which
          // is the one place a thread is rebuilt.
          yield* threads.link(thread.id, { project, number }).pipe(Effect.ignore);

          const job = yield* jobs
            .enqueue(
              createWorkspaceRef,
              {
                thread: thread.id,
                project,
                description: label,
                // Named here, so the job's `name` step finds it already decided
                // and skips the model — a review's name is `pr-<number>` by
                // definition, and ten seconds spent asking for one would be ten
                // seconds spent inventing a name that must not vary.
                //
                // No `bookmark` for the same reason, and it falls out rather
                // than being suppressed: the bookmark is composed by the step
                // that names, so a job that skips naming has none. `pr-123` is
                // not a branch anybody should push.
                workspace,
                label,
                repo: found.root,
                // A branch name, and not a revision yet. It does not exist
                // locally until the fetch step has run, which is the step that
                // resolves it — see `create-workspace.ts`.
                base: pr.headRef,
                review: {
                  number,
                  headRef: pr.headRef,
                  ...(pr.fork === undefined ? {} : { fork: pr.fork }),
                },
                agent: agentWith(settings, {}),
              },
              { key },
            )
            .pipe(Effect.orDie);

          // Lost the race: `enqueue` answered with a job that already existed,
          // so the thread created a moment ago is litter. Removed only if it is
          // still empty, which it is — nothing has had time to claim it — and
          // the earlier job's thread is the one to answer with.
          const recorded = yield* Schema.decodeUnknownEffect(CreateWorkspaceSchema)(job.input).pipe(
            Effect.option,
          );
          const owns = Option.isSome(recorded) && recorded.value.thread === thread.id;
          if (!owns) {
            yield* threads.deleteIfEmpty(thread.id).pipe(Effect.ignore);
            const earlier = Option.isSome(recorded)
              ? (yield* threads.list().pipe(Effect.orDie)).find(
                  (one) => one.id === recorded.value.thread,
                )
              : undefined;
            return { thread: earlier ?? thread, job, workspace, created: false };
          }

          return { thread, job, workspace, created: true };
        }),

      // Threads. A store that cannot be read or written is the daemon being
      // broken — the disk is full, or the file is owned by someone else — so
      // ThreadStoreError dies here rather than crossing the wire. ThreadNotFound
      // does cross it: naming a thread that is not there is a question with a
      // negative answer, which is a different thing entirely.
      WorkspaceFactsChanges: () => facts.changes(),

      /**
       * The imported projects, plus the ones the running sessions imply.
       *
       * Merged here rather than in the window because only the daemon holds
       * both halves, and because the two can name the same repository: an
       * imported project someone then started work in appears in both, and the
       * imported row is the one to keep — it is the one that survives a
       * restart and the one `forget` applies to.
       *
       * A derived project is dropped when its directory will not resolve to a
       * repository. That is not a rare edge: a session's `startDir` is where it
       * was launched, which for an old session may be a workspace that has
       * since been removed, and offering it would be offering a failure two
       * screens later.
       */
      ProjectList: () => allProjects(),

      /**
       * What is under the configured roots and not imported yet.
       *
       * The global config's roots, not a project's — `read()` with no
       * repository, because this question is asked from a window that is not
       * standing in one. That is the case the optional argument on `read` was
       * for.
       */
      ProjectCandidates: () =>
        Effect.gen(function* () {
          const settings = yield* config.read();
          const home = homedir();
          const roots = settings.projectRoots.map((one) => expand(one, home));
          const under = yield* discover(roots).pipe(
            Effect.provideService(FileSystem.FileSystem, files),
            Effect.provideService(Path.Path, paths),
          );
          const imported = yield* projects.list().pipe(Effect.orDie);
          const already = new Set(imported.map((one) => one.name));
          return under.filter((one) => !already.has(one.name));
        }),

      /**
       * Resolve a path to a repository and write it down.
       *
       * Every refusal is a sentence about the path rather than a tag to branch
       * on — see {@link ProjectImportFailed}. The empty check is first because
       * an empty string is what a submitted blank field looks like, and it
       * would otherwise walk up from the daemon's own working directory and
       * answer with *this* repository, which is the worst available success.
       */
      ProjectImport: ({ path }) =>
        Effect.gen(function* () {
          const wanted = path.trim();
          if (wanted === "") {
            return yield* Effect.fail(new ProjectImportFailed({ path, reason: "no path given" }));
          }
          const full = expand(wanted, homedir());
          if (!paths.isAbsolute(full)) {
            // A relative path would resolve against the daemon's working
            // directory, which is a real repository and is not one the person
            // typing can see. Refused by name rather than silently answered.
            return yield* Effect.fail(
              new ProjectImportFailed({ path: full, reason: "give a full path" }),
            );
          }
          // Up to the nearest `.jj` first, because `jj -R <dir> root` does not
          // walk: `-R` names a repository exactly, and a person naming a
          // directory inside their project would otherwise be told there is no
          // repository in a directory that is plainly inside one. See
          // `nearestRepo`.
          const near = yield* nearestRepo(full).pipe(
            Effect.provideService(FileSystem.FileSystem, files),
            Effect.provideService(Path.Path, paths),
          );
          if (near === undefined) {
            return yield* Effect.fail(
              new ProjectImportFailed({
                path: full,
                reason: `no jj repository at ${full} or above it`,
              }),
            );
          }
          // Still through `sourceRoot`, because the nearest `.jj` may be a
          // *secondary workspace* — one of awp's own checkouts — and importing
          // that would record a workspace as though it were the project. Only
          // this resolves the pointer back to the source repository.
          const root = yield* jj.sourceRoot(near).pipe(
            Effect.mapError(
              (error) =>
                new ProjectImportFailed({
                  path: full,
                  // jj's own sentence, which names the directory and says
                  // whether it is missing or merely not a repository. A
                  // message composed here would say less and could be wrong.
                  reason: error.reason,
                }),
            ),
          );
          return yield* projects.record(basename(root), root).pipe(
            // The store failing is a defect; a name being taken is the
            // person's to see. Two catches rather than an `orDie` after one,
            // because an `orDie` at the end would kill the failure the line
            // above had just carefully constructed.
            Effect.catchTag("ProjectStoreError", (error) => Effect.die(error)),
            Effect.catchTag("ProjectNameTaken", (taken) =>
              Effect.fail(
                new ProjectImportFailed({
                  path: full,
                  reason: `a project called ${taken.name} is already imported, from ${taken.held}`,
                }),
              ),
            ),
          );
        }),

      ProjectForget: ({ name }) => projects.forget(name).pipe(Effect.orDie),

      // ── archived threads do not come back on this call ──────────────────
      //
      // The store returns them — its own comment says "the caller filters" —
      // and until now no caller did. Measured before it was fixed: twenty of
      // twenty-nine threads had `archived_at` set and every one was still in
      // the sidebar, so archiving had been written and never read for the life
      // of the store.
      //
      // Filtered here rather than in the store, because the store is also what
      // `restore` and the archive job read, and both of those have to be able
      // to see a thread that has been put away.
      ThreadList: () =>
        threads.list().pipe(
          Effect.map((all) => all.filter((thread) => thread.archivedAt === undefined)),
          Effect.orDie,
        ),

      ThreadCreate: ({ title }) => threads.create(title).pipe(Effect.orDie),

      ThreadRename: ({ thread, title }) =>
        threads.rename(thread, title).pipe(Effect.catchTag("ThreadStoreError", Effect.die)),

      ThreadArchive: ({ thread, archived }) =>
        threads.archive(thread, archived).pipe(Effect.catchTag("ThreadStoreError", Effect.die)),

      // The destructive half, and a job because of it — see archive-thread.ts.
      // `ThreadArchive` above is the reversible flag; this kills sessions,
      // forgets workspaces and removes directories, which is work with a
      // progress panel rather than a promise.
      ThreadArchiveStart: (payload) =>
        Effect.gen(function* () {
          const all = yield* threads.list().pipe(Effect.orDie);
          // Refused here rather than inside the job, so a thread that is not
          // there is a reply the button can show instead of a job record that
          // exists only to fail.
          const thread = all.find((one) => one.id === payload.thread);
          if (thread === undefined) {
            return yield* Effect.fail(new ThreadNotFound({ thread: payload.thread }));
          }
          // The title is added here rather than sent by the client: the daemon
          // has just looked the thread up, and a client-supplied caption is a
          // second copy of something already in hand.
          const job = yield* jobs
            .enqueue(archiveThreadRef, { ...payload, title: thread.title })
            .pipe(Effect.orDie);
          return { job: job.id };
        }),

      ThreadAttach: ({ thread, member }) =>
        threads.attach(thread, member).pipe(Effect.catchTag("ThreadStoreError", Effect.die)),

      ThreadDetach: ({ thread, member }) =>
        threads.detach(thread, member).pipe(Effect.catchTag("ThreadStoreError", Effect.die)),

      // Which pull requests a thread is about. A claim somebody made, so it is
      // written down rather than parsed back out of a workspace name — see
      // `ThreadPr` in the contract.
      ThreadLinkPr: ({ thread, pr }) =>
        threads.link(thread, pr).pipe(Effect.catchTag("ThreadStoreError", Effect.die)),

      ThreadUnlinkPr: ({ thread, pr }) =>
        threads.unlink(thread, pr).pipe(Effect.catchTag("ThreadStoreError", Effect.die)),

      // Reviews. Same treatment as threads: a store that cannot be read is the
      // daemon being broken rather than a case a client can do anything about.
      ReviewList: ({ project, workspace }) => reviews.list(project, workspace).pipe(Effect.orDie),

      ReviewAdd: (payload) =>
        Effect.gen(function* () {
          // The id and the timestamp are minted here, not sent. Two windows
          // would otherwise mint ids from two clocks, and the panel's ordering
          // is `created_at`.
          // `Clock`, not `new Date()`, so a test can hold the clock still —
          // the ordering the panel reads is `createdAt`, and two comments made
          // in the same tick have to be distinguishable by it.
          const at = new Date(yield* Clock.currentTimeMillis);
          return yield* reviews.add({
            ...payload,
            id: commentId(at, Math.random()),
            // The window's own comments are a person's. The other author files
            // through `ReviewFile`, which is the call that has a directory
            // rather than a pair.
            author: "human",
            kind: payload.kind ?? "comment",
            // No anchor text: the panel knows which line it is on, so there is
            // nothing that could have gone stale between the click and this.
            text: undefined,
            createdAt: at,
            // Always a draft. Nothing on this contract can create a comment the
            // agent has already been told about, which is what makes `sentAt`
            // trustworthy as "it heard this".
            sentAt: undefined,
          });
        }).pipe(Effect.orDie),

      /**
       * Which review a directory is in. The read half of `ReviewFile`.
       *
       * Answered before anything is filed, because "am I about to write to the
       * right review" is the question that lost seven findings in the Go
       * implementation when nothing could ask it.
       */
      ReviewAt: ({ from }) =>
        Effect.gen(function* () {
          const at = yield* workspaceAt(from);
          const comments = yield* reviews.list(at.project, at.workspace).pipe(Effect.orDie);
          return { ...at, comments };
        }),

      ReviewFile: (payload) =>
        Effect.gen(function* () {
          const at = yield* workspaceAt(payload.from);

          if (payload.body.trim() === "") {
            return yield* Effect.fail(new ReviewFileFailed({ reason: "the finding has no body" }));
          }
          if (payload.line < 1) {
            return yield* Effect.fail(
              new ReviewFileFailed({ reason: `line ${payload.line} is not a line` }),
            );
          }

          // The path is checked against the workspace, and the line against the
          // file. Both are refusals rather than stored guesses: a finding is
          // read by a person against the code, and one pointing at a line that
          // is not there is worse than no finding — it reads as a comment about
          // whatever now occupies that number.
          const full = paths.join(at.dir, payload.path);
          if (!full.startsWith(`${at.dir}/`)) {
            return yield* Effect.fail(
              new ReviewFileFailed({ reason: `${payload.path} is outside the workspace` }),
            );
          }
          const lines = yield* files.readFileString(full).pipe(
            Effect.map((whole) => whole.split("\n")),
            Effect.mapError(
              () =>
                new ReviewFileFailed({
                  reason: `no file ${payload.path} in ${at.project}/${at.workspace}`,
                }),
            ),
          );
          const found = lines[payload.line - 1];
          if (found === undefined) {
            return yield* Effect.fail(
              new ReviewFileFailed({
                reason: `${payload.path} has ${lines.length} lines, so line ${payload.line} is not one`,
              }),
            );
          }
          // Compared with the ends trimmed. An agent quoting a line has almost
          // certainly not preserved its indentation exactly, and refusing over
          // whitespace would refuse a correct finding.
          if (payload.text !== undefined && payload.text.trim() !== found.trim()) {
            return yield* Effect.fail(
              new ReviewFileFailed({
                reason: `${payload.path}:${payload.line} reads "${found.trim()}", not "${payload.text.trim()}" — the line has moved`,
              }),
            );
          }

          const at2 = new Date(yield* Clock.currentTimeMillis);
          const endLine = payload.endLine ?? payload.line;
          const kind = payload.kind ?? "comment";
          const comment = yield* reviews
            .add({
              id: commentId(at2, Math.random()),
              project: at.project,
              workspace: at.workspace,
              // The working copy: a finding is about the checkout as it stands,
              // which is what the agent has been reading.
              revision: "@",
              path: payload.path,
              side: payload.side ?? "additions",
              line: payload.line,
              endLine: endLine < payload.line ? payload.line : endLine,
              body: payload.body.trim(),
              // This call exists for an agent; a person passing `--author human`
              // is filing on their own behalf from a terminal, which is theirs
              // to say rather than this handler's to assume.
              author: payload.author ?? "agent",
              kind,
              text: found,
              createdAt: at2,
              // Already delivered, in the direction that matters: a finding is
              // written *for* the person, so there is nobody left to send it to.
              // See `ReviewComment.author`.
              sentAt: at2,
            })
            .pipe(Effect.orDie);

          const span =
            comment.endLine > comment.line
              ? `${comment.line}-${comment.endLine}`
              : `${comment.line}`;
          return {
            comment,
            where: `added a ${kind} to ${at.project}/${at.workspace} on ${payload.path}:${span}`,
          };
        }),

      ReviewRemove: ({ comment }) => reviews.remove(comment).pipe(Effect.orDie),

      ReviewSend: ({ project, workspace }) =>
        Effect.gen(function* () {
          // The session is resolved *before* anything is marked. A workspace
          // whose agent has ended has nothing to type into, and marking first
          // would lose the drafts to a delivery that never happened — the
          // failure this orders itself to avoid.
          const name = sessionName(project, workspace, AGENT);
          const found = yield* mux.lookup(name).pipe(Effect.orDie);
          if (found === undefined || found.ended) {
            return yield* Effect.fail(new NoAgent({ project, workspace }));
          }

          const drafts = yield* reviews.list(project, workspace).pipe(Effect.orDie);
          const unsent = drafts.filter((comment) => comment.sentAt === undefined);
          if (unsent.length === 0) {
            // Nothing to say, so nothing is typed. Sending an empty review
            // would put a prompt in front of an agent that asks it to do
            // nothing, which it will nonetheless answer.
            return { sent: [], prompt: "" };
          }

          const prompt = reviewPrompt(unsent);
          yield* mux.send(name, prompt).pipe(Effect.orDie);

          // Marked only after the send succeeded. The other order is the one
          // that silently eats a review when zmx is not there.
          const at = new Date(yield* Clock.currentTimeMillis);
          const sent = yield* reviews.markSent(project, workspace, at).pipe(Effect.orDie);
          return { sent, prompt };
        }),

      // The agent's own list, read off disk. No error channel: absence,
      // an agent that kept no list, and an agent that is not Claude Code are
      // all the empty array. See `readTasks`.
      TaskList: ({ from }) => readTasks(from),

      // Same shape as NoteSend, and the same order for the same reason: the
      // agent is looked up before the prompt is composed, so "there is nobody
      // to tell" is a refusal rather than a send into nothing.
      TaskSend: ({ project, workspace, task }) =>
        Effect.gen(function* () {
          const name = sessionName(project, workspace, AGENT);
          const found = yield* mux.lookup(name).pipe(Effect.orDie);
          if (found === undefined || found.ended) {
            return yield* Effect.fail(new NoAgent({ project, workspace }));
          }

          const prompt = taskPrompt(task);
          yield* mux.send(name, prompt).pipe(Effect.orDie);
          return prompt;
        }),

      NoteSend: ({ project, workspace, note }) =>
        Effect.gen(function* () {
          // Same order as ReviewSend, for a weaker version of the same reason:
          // nothing here is marked, so a failed send loses only the composer's
          // contents — but it loses those to a person who is still looking at
          // the element, and "it went nowhere" has to be sayable.
          const name = sessionName(project, workspace, AGENT);
          const found = yield* mux.lookup(name).pipe(Effect.orDie);
          if (found === undefined || found.ended) {
            return yield* Effect.fail(new NoAgent({ project, workspace }));
          }

          const prompt = notePrompt(note);
          yield* mux.send(name, prompt).pipe(Effect.orDie);
          return prompt;
        }),
    };
  }),
);
