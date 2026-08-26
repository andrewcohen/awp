import type { Job } from "@awp-kit/jobs";
import * as stylex from "@stylexjs/stylex";
import { useState } from "react";
import { cancelJob, enqueueDemo, jobLog, retryJob } from "./daemon";
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
// The demo controls at the top are scaffolding, and go with the `demo` kind and
// the `JobDemo` call. Nothing in awp enqueues real work yet, and a panel with
// no way to put anything in it cannot be checked by eye at all.
//
// Their labels say what a person would see happen, not what the runner calls
// it. "fail dirty" is a sentence about compensation stopping partway, which is
// meaningful to `runner.ts` and to nobody standing in front of the window.
//
// The row itself is deliberately **not** designed yet. Every field it could
// show is a fixture right now — the title describes a payload, the steps are
// called "step 1", nothing has a real duration or a real reason for failing —
// so laying it out against this data means laying it out twice. It gets built
// when there is a job worth looking at.

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

// A click that starts a demo job. Failures are dropped on purpose: the only way
// this can fail is the daemon being absent, which the list beside it already
// says in a full sentence.
const demo = (payload: Parameters<typeof enqueueDemo>[0]) => () => {
  void enqueueDemo(payload).catch(() => {});
};

export function Jobs() {
  const { jobs, failure } = useJobs();
  const [open, setOpen] = useState<string | undefined>();
  const [lines, setLines] = useState<ReadonlyArray<string>>([]);

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
      <div {...stylex.props(styles.controls)}>
        <button
          type="button"
          {...stylex.props(styles.button)}
          onClick={demo({ pace: 400, retryable: false, undoFails: false })}
        >
          one that works
        </button>
        <button
          type="button"
          {...stylex.props(styles.button)}
          onClick={demo({ pace: 400, failAt: 3, retryable: true, undoFails: false })}
        >
          one that fails once, then works
        </button>
        <button
          type="button"
          {...stylex.props(styles.button)}
          onClick={demo({ pace: 400, failAt: 3, retryable: false, undoFails: false })}
        >
          one that gives up and undoes itself
        </button>
        <button
          type="button"
          {...stylex.props(styles.button)}
          onClick={demo({ pace: 400, failAt: 3, retryable: false, undoFails: true })}
        >
          one that gives up and cannot undo itself
        </button>
      </div>

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
