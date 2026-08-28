import { ArrowDownIcon } from "@phosphor-icons/react/ArrowDown";
import { ArrowsClockwiseIcon } from "@phosphor-icons/react/ArrowsClockwise";
import { ChatCircleIcon } from "@phosphor-icons/react/ChatCircle";
import { ChatCircleDotsIcon } from "@phosphor-icons/react/ChatCircleDots";
import { ChatCircleTextIcon } from "@phosphor-icons/react/ChatCircleText";
import { CheckCircleIcon } from "@phosphor-icons/react/CheckCircle";
import { FileDashedIcon } from "@phosphor-icons/react/FileDashed";
import { HourglassIcon } from "@phosphor-icons/react/Hourglass";
import { ProhibitIcon } from "@phosphor-icons/react/Prohibit";
import { WarningIcon } from "@phosphor-icons/react/Warning";
import { XCircleIcon } from "@phosphor-icons/react/XCircle";
import { type Job, isTerminal } from "@awp-kit/jobs";
import type { InboxItem } from "@awp-kit/protocol";
import { bucketLabel, inboxBuckets } from "@awp-kit/protocol";
import * as stylex from "@stylexjs/stylex";
import { useState } from "react";
import { startReview } from "./daemon";
import { guide } from "./stacks";
import { colors, space, text } from "./tokens.stylex";
import { useInbox } from "./useInbox";

// Every open pull request, sectioned by what the next move is.
//
// Ported from the deck's inbox scope. The sections, their order and the order of
// the rows inside them all arrive decided — see `inbox.ts` in the daemon — so
// this file is only about what a row says and what pressing it does.
//
// ── what a row has to carry, and what it must not ─────────────────────────
//
//   #2340 tiered discounts for checkout          ← the identity, and the words
//   thicket · someone · feature/discounts        ← where, whose, which branch
//
// Two lines, the same rule as the workspace strip above it and for the same
// reason: the title is the field that can be arbitrarily long and the one that
// cannot be reconstructed from the others, so it gets a line to itself.
//
// **The number is monospace and the title is not.** A PR number is an address —
// somebody will type it into `gh` or read it out — and the two families in this
// window are split on exactly that line. It is also the one place a row points
// outside the window, which is why it is allowed the accent.
//
// **A chip only appears when it says something.** A green row shows no CI chip
// at all: "passing" on every row is a column of the same word, and a column of
// the same word is one the eye stops reading — which costs the one row where it
// says something else. The chips are therefore all bad news, plus the two
// states that are neither ("draft", "blocked").
//
// ── pressing a row ─────────────────────────────────────────────────────────
//
// One action, and which one it is depends on a record rather than on a mode:
//
//   no workspace yet   review  → a thread and a workspace, in the background
//   already reviewing  open    → go to its agent
//
// The daemon is what makes that safe: `ReviewStart` answers with the same
// thread and job when the work is already under way, so a second press is not a
// second workspace. See its doc in the contract for the two records that
// guarantee it. The row does not have to know, and deliberately does not track
// what it has started — a window reloaded mid-create would forget, and the
// daemon would not.

const styles = stylex.create({
  panel: { display: "flex", flexDirection: "column", height: "100%", minHeight: 0 },
  controls: {
    display: "flex",
    alignItems: "baseline",
    gap: "0.4rem",
    padding: `0.35rem ${space.gutter}`,
  },
  button: {
    padding: "0.15rem 0.45rem",
    backgroundColor: "transparent",
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.2rem",
    color: colors.muted,
    font: "inherit",
    fontSize: text.small,
    cursor: "pointer",
  },
  when: { flex: 1, minWidth: 0, color: colors.muted, fontSize: text.small },
  list: {
    flex: 1,
    minHeight: 0,
    overflowY: "auto",
    // Never sideways — a long PR title is a title to truncate, not a column to
    // scroll. See the layout rules in AGENTS.md.
    overflowX: "hidden",
    paddingBottom: "0.5rem",
  },
  section: {
    padding: `0.5rem ${space.gutter} 0.2rem`,
    color: colors.muted,
    fontSize: text.small,
    fontWeight: text.strong,
  },
  empty: { padding: `0.5rem ${space.gutter}`, color: colors.muted, fontSize: text.small },
  trouble: {
    padding: `0.35rem ${space.gutter}`,
    color: colors.warn,
    fontSize: text.small,
    lineHeight: 1.5,
  },

  // The row is the button: the whole strip is the target, and there is exactly
  // one thing it does. A row with a control inside it would need the row itself
  // to do something else, and there is nothing else for it to do.
  row: {
    display: "block",
    width: "100%",
    padding: `${space.row} ${space.gutter}`,
    backgroundColor: "transparent",
    borderStyle: "none",
    color: colors.text,
    font: "inherit",
    textAlign: "start",
    cursor: "pointer",
    ":hover": { backgroundColor: colors.surface },
  },
  // No `nested` padding any more: the guide characters *are* the indent, and
  // two mechanisms for one offset is how the tree and the text stop agreeing
  // about where a level begins.
  first: { display: "flex", alignItems: "baseline", gap: "0.4rem" },
  // Monospace, because the guides of consecutive rows have to line up as
  // columns — in a proportional face `│  ` is a different width from `└─ ` and
  // the tree bends. Muted, because it is structure rather than content: the
  // same reasoning as the sidebar's one dot per row carrying the only hue.
  guide: {
    flexShrink: 0,
    color: colors.muted,
    fontFamily: text.mono,
    fontSize: text.small,
    whiteSpace: "pre",
    userSelect: "none",
    WebkitUserSelect: "none",
  },
  // ── the number is monospace and NOT the accent ──────────────────────────
  //
  // It was the accent, on the argument that a PR number is the one thing on the
  // row pointing outside the window — which is exactly why the sidebar's PR
  // chip does carry it. In the sidebar a number is an exception: most rows have
  // none. Here every row is a pull request, so the number is the *baseline*,
  // and an accent on the baseline is thirty accents in a column. Reported as
  // "too much orange", and the general rule is worth keeping: **an accent marks
  // a deviation from the rows around it, so the same field earns it in one list
  // and not in another.**
  //
  // Monospace still says what it needs to: this is an address, and somebody
  // will type it somewhere else.
  number: { flexShrink: 0, color: colors.muted, fontFamily: text.mono, fontSize: text.small },
  title: {
    flex: 1,
    minWidth: 0,
    overflow: "hidden",
    textOverflow: "ellipsis",
    whiteSpace: "nowrap",
  },
  second: {
    display: "flex",
    gap: "0.4rem",
    overflow: "hidden",
    color: colors.muted,
    fontSize: text.small,
  },
  where: { overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" },
  branch: {
    flexShrink: 1,
    minWidth: 0,
    overflow: "hidden",
    textOverflow: "ellipsis",
    whiteSpace: "nowrap",
    fontFamily: text.mono,
  },
  // A slot, not a margin. The icon is absent for the ordinary row and the
  // titles still have to line up, so the width is the row's and not the icon's.
  lead: {
    display: "flex",
    flexShrink: 0,
    justifyContent: "center",
    width: "1rem",
  },
  chips: { display: "flex", flexShrink: 0, alignItems: "center", gap: "0.3rem" },
  bad: { color: colors.warn },
  waiting: { color: colors.waiting },
  asked: { color: colors.asked },
  live: { color: colors.live },
  muted: { color: colors.muted },
  // What is happening to this row — see `doing`. Words rather than a spinner,
  // and monospace so a step name changing does not shuffle the row's width.
  state: { flexShrink: 0, fontFamily: text.mono, fontSize: text.small },
});

/**
 * The one icon a row leads with, and the priority order it is chosen by.
 *
 * The deck's rule, kept: **the baseline state has no icon at all.** An open
 * pull request with green CI and no review asked of anybody is the ordinary
 * case, and painting it teaches the eye to skim past the icon column — which
 * costs the one row that deviates. The slot stays its width either way, so the
 * titles below still line up.
 *
 * Priority, most-urgent first, and every clause is a state a person acts on
 * differently:
 *
 *   ✗ ci red            go and look now
 *   ⧗ ci running        nothing to do yet
 *   ● changes requested somebody wants work from you, on your own PR
 *   ◌ asked again       you reviewed it, and the author has come back
 *   ○ review requested  a first request
 *   ✓ approved          one press from done
 *   ▤ draft             not submitted, so its CI is information
 *
 * Two chat icons rather than one for the review states, and the pair is the
 * deck's too: a hollow bubble is "somebody is asking", a dotted one is "asking
 * again". They read as conversation, which is what a review is, where a tick or
 * a flag reads as a verdict.
 */
const lead = (
  item: InboxItem,
):
  | { readonly Icon: typeof WarningIcon; readonly tone: Tone; readonly says: string }
  | undefined => {
  if (item.ci === "failing") {
    return { Icon: XCircleIcon, tone: "bad", says: "CI failing" };
  }
  if (item.ci === "pending") {
    return { Icon: HourglassIcon, tone: "waiting", says: "CI running" };
  }
  if (item.review === "changes-requested") {
    return { Icon: ChatCircleIcon, tone: "bad", says: "changes requested" };
  }
  if (item.reviewRerequested) {
    return { Icon: ChatCircleDotsIcon, tone: "asked", says: "review requested again" };
  }
  if (item.reviewRequested) {
    return { Icon: ChatCircleIcon, tone: "asked", says: "your review is requested" };
  }
  if (item.review === "approved") {
    return { Icon: CheckCircleIcon, tone: "live", says: "approved" };
  }
  if (item.draft) {
    return { Icon: FileDashedIcon, tone: "muted", says: "draft" };
  }
  return undefined;
};

type Tone = "bad" | "waiting" | "asked" | "live" | "muted";

/**
 * What is left over: the states the leading icon could not also say.
 *
 * The lead is one icon by design, and a pull request is often two things at
 * once — waiting on your review *and* in conflict with its base. These are the
 * second facts, drawn small and after the branch, and there are deliberately
 * few of them: each is something that changes what a person does next.
 *
 * `notes` is only on your own pull request and only when GitHub's verdict did
 * not move, which is precisely the case a reviewer's comment leaves invisible
 * everywhere else — see `InboxItem.hasReviewComments`.
 */
const also = (
  item: InboxItem,
): ReadonlyArray<{
  readonly key: string;
  readonly Icon: typeof WarningIcon;
  readonly tone: Tone;
  readonly says: string;
}> => {
  const out: Array<{
    key: string;
    Icon: typeof WarningIcon;
    tone: Tone;
    says: string;
  }> = [];
  if (item.mergeState === "dirty") {
    out.push({ key: "dirty", Icon: WarningIcon, tone: "bad", says: "conflicts with its base" });
  }
  if (item.mergeState === "behind") {
    out.push({ key: "behind", Icon: ArrowDownIcon, tone: "waiting", says: "behind its base" });
  }
  if (item.hasReviewComments && item.mine && item.review !== "changes-requested") {
    out.push({
      key: "notes",
      Icon: ChatCircleTextIcon,
      tone: "waiting",
      says: "a reviewer left notes",
    });
  }
  if (item.blocked) {
    out.push({
      key: "blocked",
      Icon: ProhibitIcon,
      tone: "muted",
      says: "an earlier PR in its stack cannot merge yet",
    });
  }
  // The one mark here that is about *this machine* rather than about GitHub, and
  // the reason it earns a place beside the others: a review workspace that does
  // not contain the pull request's head is a review of code the PR no longer
  // has — and every other thing on screen looks exactly as it did.
  if (item.moved) {
    out.push({
      key: "moved",
      Icon: ArrowsClockwiseIcon,
      tone: "waiting",
      says: "the pull request has moved since this workspace was made",
    });
  }
  return out;
};

/**
 * A tone as a style. One place, so the two icon sets cannot drift apart.
 *
 * `undefined` is a real answer rather than a default: the ordinary row has no
 * leading icon, so it has no colour either, and the slot it leaves is
 * deliberately blank.
 */
const toned = (tone: Tone | undefined): stylex.StyleXStyles | undefined => {
  switch (tone) {
    case "bad":
      return styles.bad;
    case "waiting":
      return styles.waiting;
    case "asked":
      return styles.asked;
    case "live":
      return styles.live;
    case "muted":
      return styles.muted;
    default:
      return undefined;
  }
};

/**
 * The row, in words.
 *
 * Icons alone are not the whole story: this is what a pointer hovering the row
 * reads, and what a screen reader is given — every icon on the row is
 * `aria-hidden`, because an icon that announces itself in the middle of a title
 * makes the title unreadable. The states are said here instead, once, in the
 * order they are drawn.
 */
const spoken = (item: InboxItem, state: Doing | undefined): string => {
  const said = [lead(item)?.says, ...also(item).map((mark) => mark.says)].filter(
    (one): one is string => one !== undefined,
  );
  const what =
    item.workspace !== undefined
      ? `open ${item.workspace}`
      : state?.why !== undefined
        ? state.why
        : state !== undefined
          ? `#${item.number} — ${state.says}`
          : `review #${item.number} — makes a workspace`;
  return said.length === 0 ? what : `${what} · ${said.join(" · ")}`;
};

/**
 * What is happening to this row, in the words it shows on the right.
 *
 * ── the press used to say nothing at all ──────────────────────────────────
 *
 * A review is a job — a fetch, a workspace, a session, hooks, a claim — and
 * that is half a minute of work. The row's only state was "does a thread hold
 * `pr-<n>`", which the claim sets **second to last**, so pressing it produced
 * no visible change until everything was over. Indistinguishable from a button
 * that does not work, and pressing it again was the natural response.
 *
 * Four states, and each is a different thing to do next:
 *
 *   starting    the call is in flight. Local, and only for that moment
 *   <step> N/M  the job is running. Which step, because "fetch" and
 *               "bootstrap" wait on very different things
 *   failed      the job stopped. The row says so rather than leaving it to
 *               the jobs panel, and the sentence is on the hover
 *   open        there is a workspace to go to
 *
 * **No spinner**, which is the jobs panel's rule and holds harder here: the
 * word already says it is running, and an animation in a list somebody leaves
 * open all day is a list that never settles.
 */
interface Doing {
  readonly says: string;
  readonly tone: Tone | undefined;
  /** The failure, for the row's hover. */
  readonly why?: string | undefined;
}

const doing = (item: InboxItem, jobs: ReadonlyArray<Job>, starting: boolean): Doing | undefined => {
  if (starting) {
    return { says: "starting…", tone: "muted" };
  }
  const job = item.job === undefined ? undefined : jobs.find((one) => one.id === item.job);
  if (job !== undefined && !isTerminal(job.status)) {
    // The step, not a percentage. `job.step` is what is running now and
    // `done.at(-1)` is what a queued-again job last finished — the same
    // fallback the jobs panel uses, so the two never disagree about where a
    // job got to.
    const at = job.step ?? job.done.at(-1);
    return {
      says: at === undefined ? "starting…" : `${at} ${job.done.length}/${job.steps.length}`,
      tone: "muted",
    };
  }
  if (item.workspace !== undefined) {
    return { says: "open", tone: "live" };
  }
  if (job !== undefined && job.status === "failed") {
    // Only when there is no workspace: a job that failed *after* building one —
    // a bootstrap hook, say — leaves something worth opening, and "failed" on a
    // row that can be opened is the less useful of the two things to say.
    return { says: "failed", tone: "bad", why: job.error };
  }
  if (job !== undefined && job.status === "cancelled") {
    return { says: "cancelled", tone: "muted" };
  }
  return undefined;
};

/** `09:14`, which is all a reading taken minutes ago needs to say. */
const clock = (at: Date): string =>
  `${String(at.getHours()).padStart(2, "0")}:${String(at.getMinutes()).padStart(2, "0")}`;

export function Inbox({
  jobs,
  onOpen,
  onStarted,
}: {
  /** Every job the window knows about — see `doing`. */
  readonly jobs: ReadonlyArray<Job>;
  /** Go to a workspace's agent. The window owns the address — see App.tsx. */
  readonly onOpen: (project: string, workspace: string) => void;
  /** A review was started, so the threads and jobs App holds are out of date. */
  readonly onStarted: () => void;
}) {
  const { inbox, reading, failure, reload } = useInbox();
  // The refusal from the last press, if there was one. One at a time, because
  // there is one pointer and the sentence is about what it just did.
  const [refused, setRefused] = useState<string | undefined>();
  // Which rows have a `ReviewStart` in flight, by `project:number`.
  //
  // Local and deliberately short-lived: it covers the moment between the press
  // and the reply — which is a `gh` call, so it is not instant — and is handed
  // over to the job the moment the daemon names one. A row still in here after
  // the reply would be a second source of truth about the same work.
  const [starting, setStarting] = useState<ReadonlySet<string>>(new Set());

  const press = (item: InboxItem): void => {
    if (item.workspace !== undefined) {
      onOpen(item.project, item.workspace);
      return;
    }
    const key = `${item.project}:${item.number}`;
    setRefused(undefined);
    // Said before the call, because the call is a round trip and a press that
    // leaves the row unchanged reads as a press that missed.
    setStarting((all) => new Set(all).add(key));
    startReview(item.project, item.number)
      .then((started) => {
        // No navigation, which is the new-thread box's precedent: the job is
        // what has the progress, and going to a workspace whose session does
        // not exist yet would land on an empty pane and read as a failure.
        //
        // The thread strip and the list are both re-read: the thread appeared
        // above, and this row's action has changed from create to watch.
        onStarted();
        reload();
        return started;
      })
      .catch((error: unknown) => {
        setRefused(String(error));
      })
      .finally(() => {
        // Handed over to the job, whether the call worked or not. A row left
        // saying "starting…" after a refusal is a row that lies quietly.
        setStarting((all) => {
          const next = new Set(all);
          next.delete(key);
          return next;
        });
      });
  };

  if (failure !== undefined) {
    return <div {...stylex.props(styles.trouble)}>{failure}</div>;
  }

  const items = inbox?.items ?? [];
  // Read once here rather than per section: the sections are a fixed list and
  // most of them are empty on any given day.
  const bySection = new Map(
    inboxBuckets.map((bucket) => [bucket, items.filter((item) => item.bucket === bucket)] as const),
  );
  const newest = (inbox?.sources ?? [])
    .map((source) => source.fetchedAt)
    .filter((at): at is Date => at !== undefined)
    .toSorted((a, b) => b.getTime() - a.getTime())[0];
  const broken = (inbox?.sources ?? []).filter((source) => source.failure !== undefined);

  return (
    <div {...stylex.props(styles.panel)}>
      <div {...stylex.props(styles.controls)}>
        <span {...stylex.props(styles.when)}>
          {/* The rows stay on screen while this says so, which is what the
              atoms are for — see `useInbox`. So it reads as an annotation on a
              list rather than as a replacement for one. */}
          {reading ? "refreshing" : newest === undefined ? "" : `read at ${clock(newest)}`}
        </span>
        <button
          type="button"
          onClick={() => reload(true)}
          disabled={reading}
          {...stylex.props(styles.button)}
        >
          refresh
        </button>
      </div>

      <div {...stylex.props(styles.list)}>
        {/* Said before the rows, because it changes what the rows *mean*: with
            no login every viewer-relative section is empty, and an inbox that is
            empty for that reason looks exactly like one with nothing in it. */}
        {inbox !== undefined && inbox.viewer === undefined && (
          <div {...stylex.props(styles.trouble)}>
            gh is not signed in, so nothing here can be yours or waiting on you —{" "}
            <code>gh auth login</code>
          </div>
        )}

        {/* One project's failure keeps the rest of the list. The sentence is
            gh's own, and it names the repository. */}
        {broken.map((source) => (
          <div key={source.project} {...stylex.props(styles.trouble)}>
            {source.project}: {source.failure}
          </div>
        ))}

        {/* Rows arrived, and one signal in them did not. Muted rather than
            warn: nothing is broken and nothing needs fixing — it is a fact
            about what these rows can say. Silence would be worse than either:
            a clean-looking inbox for a repository where nothing is *able* to
            report a conflict. */}
        {(inbox?.sources ?? [])
          .filter((source) => source.degraded !== undefined)
          .map((source) => (
            <div key={`${source.project}-degraded`} {...stylex.props(styles.empty)}>
              {source.project}: {source.degraded}
            </div>
          ))}

        {refused !== undefined && <div {...stylex.props(styles.trouble)}>{refused}</div>}

        {inbox !== undefined && items.length === 0 && (
          <div {...stylex.props(styles.empty)}>no open pull requests</div>
        )}

        {inboxBuckets.map((bucket) => {
          const rows = bySection.get(bucket) ?? [];
          if (rows.length === 0) {
            return undefined;
          }
          return (
            <div key={bucket}>
              <div {...stylex.props(styles.section)}>
                {bucketLabel(bucket)} ({rows.length})
              </div>
              {rows.map((item, index) => {
                const icon = lead(item);
                const state = doing(item, jobs, starting.has(`${item.project}:${item.number}`));
                const lines = guide(rows, index);
                return (
                  <button
                    key={`${item.project}:${item.number}`}
                    type="button"
                    // Opt-in navigation: ctrl+j/k step through these, and a list
                    // of every focusable element is a list nobody can predict.
                    // See navigation.ts.
                    data-nav-item
                    title={spoken(item, state)}
                    onClick={() => press(item)}
                    {...stylex.props(styles.row)}
                  >
                    <span {...stylex.props(styles.first)}>
                      {/* The tree, drawn from the list rather than the row —
                          see `guide`. Empty for everything that is not stacked,
                          and for the root of a stack. */}
                      {lines !== "" && (
                        <span aria-hidden {...stylex.props(styles.guide)}>
                          {lines}
                        </span>
                      )}
                      {/* The state, before the number, so a column of rows can be
                        read down rather than across. Empty for the ordinary
                        case — see `lead`. */}
                      <span
                        // `title` on the wrapper and not on the icon: it is an
                        // `<svg>`, and a `title` attribute on one is not a
                        // tooltip — SVG wants a `<title>` child, which Phosphor
                        // does not take. The span is the hoverable thing anyway.
                        title={icon?.says}
                        {...stylex.props(styles.lead, toned(icon?.tone))}
                      >
                        {icon !== undefined && <icon.Icon size={14} weight="bold" aria-hidden />}
                      </span>
                      <span {...stylex.props(styles.number)}>#{item.number}</span>
                      <span {...stylex.props(styles.title)}>{item.title}</span>
                      {state !== undefined && (
                        <span title={state.why} {...stylex.props(styles.state, toned(state.tone))}>
                          {state.says}
                        </span>
                      )}
                    </span>
                    <span {...stylex.props(styles.second)}>
                      <span {...stylex.props(styles.where)}>
                        {item.project} · {item.author}
                      </span>
                      <span {...stylex.props(styles.branch)}>{item.headRef}</span>
                      <span {...stylex.props(styles.chips)}>
                        {also(item).map((mark) => (
                          <span
                            key={mark.key}
                            title={mark.says}
                            {...stylex.props(toned(mark.tone))}
                          >
                            <mark.Icon size={13} aria-hidden />
                          </span>
                        ))}
                      </span>
                    </span>
                  </button>
                );
              })}
            </div>
          );
        })}
      </div>
    </div>
  );
}
