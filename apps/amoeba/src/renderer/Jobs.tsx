import { type Job, isTerminal } from "@awp-kit/jobs";
import * as stylex from "@stylexjs/stylex";
import { useState } from "react";
import { cancelJob, clearJobs, jobLog, retryJob } from "./daemon";
import { colors, text } from "./tokens.stylex";
import { useJobs } from "./useJobs";

// What the jobs are doing, and what to do about them.
//
// Visibility was the reason the jobs system was built, so this panel is the
// half that delivers it. Four things a person actually wants from a job, in the
// order they want them:
//
//   1. is it moving         status, and which step of how many
//   2. why did it stop      the failure, in the sentence the step wrote
//   3. what is left behind  `cleanup: dirty` — the only state needing a human
//   4. can I make it go     retry, cancel
//
// A row shows the first three without being opened. The log is behind a click
// because a job that worked has nothing to say and a panel of collapsed logs is
// a panel of noise; a job that failed is the one worth opening, and its failure
// is already on the row telling you to.
//
// The four demo buttons that used to sit at the top are gone, along with the
// `demo` kind and the `JobDemo` call behind them. They existed so this panel
// could be looked at while nothing real enqueued anything, and creating a
// workspace is now real; scaffolding kept past the thing it was holding up is
// just furniture in the way.
//
// What replaced them is one button that takes something *away*. A jobs list
// only grows — every workspace ever made leaves a row — so the panel that
// needed a way to put jobs in now needs a way to get them out.
//
// **Clear does not mean clear.** The daemon keeps anything queued or running,
// and anything whose rollback left something behind; see `JobClear` in the
// contract. That rule lives there rather than here because a rule about which
// records may be destroyed is not one to have two copies of — and the reply is
// a count so this button can say what actually happened when rows stay put.

const styles = stylex.create({
  panel: { display: "flex", flexDirection: "column", height: "100%", minHeight: 0 },
  controls: {
    display: "flex",
    flexWrap: "wrap",
    gap: "0.3rem",
    padding: "0.4rem 0.6rem",
    borderBottomWidth: 1,
    borderBottomStyle: "solid",
    borderBottomColor: colors.border,
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
    fontSize: text.tiny,
    cursor: "pointer",
  },
  kept: { alignSelf: "center", color: colors.muted, fontSize: text.tiny },
  list: { flex: 1, minHeight: 0, overflowY: "auto", padding: "0.4rem 0" },
  empty: { padding: "0.5rem 0.6rem", color: colors.muted, fontSize: text.small },

  row: { padding: "0.3rem 0.6rem" },
  head: { display: "flex", alignItems: "baseline", gap: "0.4rem" },
  // The title takes the room, because it is the only field on the row that can
  // be arbitrarily long and the only one that cannot be reconstructed.
  title: {
    flex: 1,
    minWidth: 0,
    overflow: "hidden",
    textOverflow: "ellipsis",
    whiteSpace: "nowrap",
    padding: 0,
    backgroundColor: "transparent",
    borderStyle: "none",
    color: colors.text,
    font: "inherit",
    fontSize: text.small,
    textAlign: "start",
    cursor: "pointer",
  },
  meta: {
    display: "flex",
    gap: "0.4rem",
    paddingInlineStart: "1rem",
    color: colors.muted,
    fontSize: text.tiny,
  },
  dot: { flexShrink: 0, width: "0.75rem" },
  live: { color: colors.live },
  warn: { color: colors.warn },
  muted: { color: colors.muted },
  log: {
    margin: "0.2rem 0 0.4rem 1rem",
    padding: "0.3rem 0.4rem",
    maxHeight: "12rem",
    overflow: "auto",
    backgroundColor: colors.border,
    color: colors.text,
    fontSize: text.tiny,
    lineHeight: 1.5,
    whiteSpace: "pre-wrap",
  },
});

// The glyph carries the state and the colour carries whether it needs you. A
// spinner would be a third thing saying "running" and would also animate a
// panel that is often left open.
const GLYPH: Record<Job["status"], string> = {
  queued: "○",
  running: "◐",
  succeeded: "●",
  failed: "●",
  cancelled: "○",
};

const hue = (job: Job) => {
  if (job.status === "failed") {
    return styles.warn;
  }
  if (job.status === "running" || job.status === "succeeded") {
    return styles.live;
  }
  return styles.muted;
};

/** Where a job got to, said the same way whether or not it is still going. */
const progress = (job: Job): string => {
  if (job.status === "succeeded") {
    return `${job.steps.length} steps`;
  }
  const at = job.step ?? job.done.at(-1);
  return at === undefined ? `${job.steps.length} steps` : `${at} of ${job.steps.length}`;
};

export function Jobs() {
  const { jobs, failure, refresh } = useJobs();
  const [open, setOpen] = useState<string | undefined>();
  const [lines, setLines] = useState<ReadonlyArray<string>>([]);
  const [kept, setKept] = useState<string | undefined>();

  // Counted here rather than asked of the daemon, so the button can say how
  // many before it is pressed. The daemon still decides — this is the same
  // rule read from the records the window already holds, and if the two ever
  // disagree the message below is what says so.
  const clearable = jobs.filter((job) => isTerminal(job.status) && job.cleanup !== "dirty").length;

  const clear = () => {
    setKept(undefined);
    clearJobs()
      .then((gone) => {
        refresh();
        // Said only when it is surprising. Clearing four of four needs no
        // commentary; clearing four of seven does, because the three left are
        // the ones a person has to do something about.
        const left = jobs.length - gone;
        setKept(left > 0 ? `${left} still running or needing a hand` : undefined);
      })
      .catch((error: unknown) => setKept(String(error)));
  };

  const show = (job: Job) => {
    if (open === job.id) {
      setOpen(undefined);
      return;
    }
    setOpen(job.id);
    setLines([]);
    // Fetched on open rather than streamed with the record. A log is the one
    // part of a job that is unbounded, and sending every line to every client
    // would put a job's output on the wire whether anyone was reading it or not.
    jobLog(job.id)
      .then(setLines)
      .catch((error: unknown) => setLines([String(error)]));
  };

  return (
    <div {...stylex.props(styles.panel)}>
      {/* Nothing to clear, nothing to say. A control bar that is always there
          offering an action that would do nothing is a bar the eye learns to
          skip, which costs the one moment it exists for. */}
      {clearable > 0 && (
        <div {...stylex.props(styles.controls)}>
          <button type="button" {...stylex.props(styles.button)} onClick={clear}>
            clear {clearable} finished
          </button>
          {kept !== undefined && <span {...stylex.props(styles.kept)}>{kept}</span>}
        </div>
      )}

      <div {...stylex.props(styles.list)}>
        {failure !== undefined && <div {...stylex.props(styles.empty)}>no daemon</div>}
        {failure === undefined && jobs.length === 0 && (
          <div {...stylex.props(styles.empty)}>no jobs</div>
        )}
        {jobs.map((job) => (
          <Row key={job.id} job={job} open={open === job.id} lines={lines} onOpen={show} />
        ))}
      </div>
    </div>
  );
}

function Row({
  job,
  open,
  lines,
  onOpen,
}: {
  readonly job: Job;
  readonly open: boolean;
  readonly lines: ReadonlyArray<string>;
  readonly onOpen: (job: Job) => void;
}) {
  const stopped = job.status !== "running" && job.status !== "queued";

  return (
    <div {...stylex.props(styles.row)}>
      <div {...stylex.props(styles.head)}>
        <span aria-hidden {...stylex.props(styles.dot, hue(job))}>
          {GLYPH[job.status]}
        </span>
        <button type="button" {...stylex.props(styles.title)} onClick={() => onOpen(job)}>
          {job.title}
        </button>
        {stopped ? (
          <button
            type="button"
            {...stylex.props(styles.button)}
            onClick={() => void retryJob(job.id)}
          >
            retry
          </button>
        ) : (
          <button
            type="button"
            {...stylex.props(styles.button)}
            onClick={() => void cancelJob(job.id)}
          >
            cancel
          </button>
        )}
      </div>

      <div {...stylex.props(styles.meta)}>
        <span>{job.kind}</span>
        <span>{progress(job)}</span>
        {job.attempt > 1 && (
          <span>
            attempt {job.attempt}/{job.attempts}
          </span>
        )}
        {job.cleanup === "dirty" && (
          // Louder than the failure itself, and on purpose: a failed job asks
          // for a retry, a dirty one asks for a person.
          <span {...stylex.props(styles.warn)}>rollback incomplete</span>
        )}
      </div>

      {job.error !== undefined && (
        <div {...stylex.props(styles.meta, styles.warn)}>{job.error}</div>
      )}

      {open && <pre {...stylex.props(styles.log)}>{lines.join("\n")}</pre>}
    </div>
  );
}
