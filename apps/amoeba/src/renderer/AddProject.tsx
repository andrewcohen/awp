import { Dialog } from "@base-ui/react/dialog";
import { FolderIcon } from "@phosphor-icons/react/Folder";
import type { Thread } from "@awp-kit/protocol";
import * as stylex from "@stylexjs/stylex";
import { useState } from "react";
import { Chip } from "./Chip";
import { said, startThread } from "./daemon";
import { useGrow } from "./grow";
import { typeset } from "./typeset";
import { colors, lift, text, timing } from "./tokens.stylex";
import { useProjects } from "./useProjects";

// Adding a second repository to a piece of work already under way.
//
// ── the thread is the unit, and this is the way in ─────────────────────────
//
// `thread_members` has held `(project, workspace)` pairs since it existed, and
// `create-workspace` has always taken a thread id — so a thread across two
// repositories was expressible in the store long before anything could ask for
// one. The cmd+N modal can name several projects at once, which covers
// starting that way; this covers the case that actually happens, which is
// realising halfway through that the api half needs doing too.
//
// ── what is deliberately not asked for ────────────────────────────────────
//
//   the workspace name   taken from the sibling, by the daemon. A thread whose
//                        checkouts are `tabular-exports` here and
//                        `export-tables` there does not read as one piece of
//                        work, and a model asked twice from one sentence will
//                        do exactly that
//   the base             this project's own trunk. The thread's bookmark is in
//                        another repository and `baseOfThread` refuses it by
//                        name — so trunk is not a fallback here, it is the
//                        answer
//   the face, the model  the thread's own, not asked again. This is one more
//                        checkout of work already set up, not a new decision
//
// What is left is the two things only a person knows: which repository, and
// what this half of the work is.
//
// ── the brief is the whole of what the new agent gets ──────────────────────
//
// The daemon passes `description` through as the workspace's prompt — see the
// `named` block in handlers.ts, where skipping the naming step also skips the
// prompt that step would have written. So an empty box here is an agent
// briefed with nothing, sitting in a fresh checkout. It is required for that
// reason, and the placeholder says what it is for rather than inviting a
// title.

const styles = stylex.create({
  backdrop: { position: "fixed", inset: 0, backgroundColor: "rgba(0, 0, 0, 0.45)" },
  popup: {
    // Nothing pops — the window's mandate. From just under, so it reads as
    // coming forward rather than growing, and the keyframe carries the
    // centring translate because the two compose.
    animationName: stylex.keyframes({
      from: { opacity: 0, transform: "translate(-50%, -50%) scale(0.97)" },
      to: { opacity: 1, transform: "translate(-50%, -50%) scale(1)" },
    }),
    animationDuration: { default: timing.enter, "@media (prefers-reduced-motion: reduce)": "0s" },
    animationTimingFunction: timing.spring,
    position: "fixed",
    top: "50%",
    left: "50%",
    transform: "translate(-50%, -50%)",
    zIndex: 30,
    display: "flex",
    flexDirection: "column",
    gap: "0.75rem",
    width: "min(34rem, calc(100vw - 3rem))",
    padding: "1rem",
    backgroundColor: colors.surface,
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.5rem",
    color: colors.text,
    boxShadow: lift.high,
  },
  title: { margin: 0 },
  said: { margin: 0, color: colors.muted, fontSize: text.small },
  bar: { display: "flex", alignItems: "center", gap: "0.4rem" },
  chipIcon: { color: colors.muted },
  brief: {
    // `flex: 1` with `minWidth: 0` is the pair — a full-width child beside
    // anything else is this window's commonest horizontal scrollbar.
    width: "100%",
    minWidth: 0,
    boxSizing: "border-box",
    padding: "0.5rem",
    backgroundColor: colors.base,
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: { default: colors.border, ":focus": colors.accent },
    borderRadius: "0.35rem",
    color: colors.text,
    resize: "none",
    outline: "none",
  },
  buttons: { display: "flex", gap: "0.5rem", justifyContent: "flex-end" },
  button: {
    padding: "0.35rem 0.75rem",
    backgroundColor: colors.raised,
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.3rem",
    color: colors.text,
    cursor: "pointer",
  },
  go: { backgroundColor: colors.accent, borderColor: colors.accent, color: colors.base },
  failure: { color: colors.warn, fontSize: text.small },
});

export function AddProject({
  thread,
  open,
  onOpenChange,
  onStarted,
}: {
  readonly thread: Thread;
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  /** A workspace is being built, so the thread list is out of date. */
  readonly onStarted: () => void;
}) {
  const projects = useProjects();
  const [brief, setBrief] = useState("");
  const [failure, setFailure] = useState<string | undefined>();
  const [busy, setBusy] = useState(false);
  const box = useGrow(brief);

  // ── a project the thread already holds is not offered ────────────────────
  //
  // The daemon refuses it by name — two workspaces in one repository for one
  // thread is a real thing to want and is not what this control means, and the
  // create would land on the directory the sibling occupies. Refusing it here
  // as well is not a second implementation of that rule: the daemon's refusal
  // is the one that decides, and this only declines to offer a row whose only
  // outcome is that sentence.
  const taken = new Set(thread.members.map((member) => member.project));
  const spare = projects.projects.filter((one) => !taken.has(one.name));
  const [project, setProject] = useState<string | undefined>(undefined);
  const chosen =
    project !== undefined && spare.some((one) => one.name === project) ? project : spare[0]?.name;
  const from = spare.find((one) => one.name === chosen)?.root;

  const go = () => {
    if (chosen === undefined || from === undefined || brief.trim() === "") return;
    setBusy(true);
    setFailure(undefined);
    startThread({ description: brief.trim(), project: chosen, from, thread: thread.id })
      .then(() => {
        onOpenChange(false);
        setBrief("");
        // The job is what does the work; this says the list should be read
        // again, so the row appears while it is being built rather than when
        // the last step happens to finish.
        onStarted();
      })
      .catch((error: unknown) => setFailure(said(error)))
      .finally(() => setBusy(false));
  };

  const title = thread.title === "" ? "this thread" : thread.title;

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Backdrop {...stylex.props(styles.backdrop)} />
        <Dialog.Popup {...stylex.props(typeset.prose, styles.popup)}>
          <Dialog.Title {...stylex.props(typeset.heading, styles.title)}>
            Add a project to {title}
          </Dialog.Title>

          {spare.length === 0 ? (
            <p {...stylex.props(styles.said)}>
              {projects.projects.length === 0
                ? "No projects yet. Point awp at a repository from the new-thread window."
                : "This thread already has a workspace in every project awp knows about."}
            </p>
          ) : (
            <>
              <div {...stylex.props(styles.bar)}>
                <Chip
                  id="add-project-project"
                  label={chosen ?? ""}
                  title="the repository the new workspace is made in"
                  value={chosen ?? ""}
                  onChange={setProject}
                  options={spare.map((one) => ({ value: one.name, label: one.name }))}
                  icon={<FolderIcon size={11} {...stylex.props(styles.chipIcon)} />}
                  disabled={busy}
                />
                {/* Said rather than left to be discovered from the result.
                    Somebody who expects this to follow the thread's branch
                    should find out here and not from a diff. */}
                <span {...stylex.props(styles.said)}>
                  from its own main line, and named{" "}
                  {thread.members[0]?.workspace ?? "after its sibling"}
                </span>
              </div>

              <textarea
                ref={box}
                value={brief}
                onChange={(event) => setBrief(event.target.value)}
                placeholder="what this repository's half of the work is — the agent is briefed with this"
                disabled={busy}
                rows={1}
                {...stylex.props(typeset.prose, styles.brief)}
              />
            </>
          )}

          {failure !== undefined && <div {...stylex.props(styles.failure)}>{failure}</div>}

          <div {...stylex.props(styles.buttons)}>
            <Dialog.Close {...stylex.props(typeset.label, styles.button)}>cancel</Dialog.Close>
            <button
              type="button"
              disabled={busy || chosen === undefined || brief.trim() === ""}
              onClick={go}
              {...stylex.props(typeset.label, styles.button, styles.go)}
            >
              {busy ? "starting…" : "add"}
            </button>
          </div>
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
