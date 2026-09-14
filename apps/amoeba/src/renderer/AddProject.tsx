import { Dialog } from "@base-ui/react/dialog";
import { FolderIcon } from "@phosphor-icons/react/Folder";
import { FolderPlusIcon } from "@phosphor-icons/react/FolderPlus";
import type { Thread } from "@awp-kit/protocol";
import * as stylex from "@stylexjs/stylex";
import { useState } from "react";
import { Chip } from "./Chip";
import { ImportProject } from "./ImportProject";
import { said, startThread } from "./daemon";
import { useGrow } from "./grow";
import { typeset } from "./typeset";
import { colors, layer, lift, text, timing } from "./tokens.stylex";
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
    zIndex: layer.modal,
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
  /**
   * The import toggle, beside the picker it adds a row to.
   *
   * The same control `NewThread` carries, and it had to come here for a
   * reason the empty case makes plain: a thread already holding a checkout in
   * every project awp knows about said so and offered nothing, which is a
   * dead end in the one dialog whose whole subject is *which repository*. A
   * toggle rather than a second dialog — a modal over a modal is a stack to
   * get out of, and the panel it opens is four lines tall.
   */
  add: {
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    flexShrink: 0,
    width: "1.4rem",
    height: "1.4rem",
    padding: 0,
    backgroundColor: "transparent",
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: "transparent",
    borderRadius: "0.25rem",
    color: colors.muted,
    cursor: "pointer",
    ":hover": { borderColor: colors.border, color: colors.text },
  },
  addOn: { backgroundColor: colors.border, color: colors.text },
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
  const [importing, setImporting] = useState(false);
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

          {/* ── the bar is always here, and the import with it ───────────────

              It used to be inside the `spare.length > 0` branch, so a thread
              already holding a checkout in every known project got a sentence
              saying exactly that and no control at all — a dead end in the one
              dialog whose entire subject is *which repository*. The way out of
              that state is to import a repository, so the way to import one
              cannot be behind the state it resolves. */}
          <div {...stylex.props(styles.bar)}>
            {spare.length > 0 && (
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
            )}
            {/* Beside the picker it adds a row to, which is where `NewThread`
                puts the same control and for the same reason. */}
            <button
              type="button"
              {...stylex.props(styles.add, importing && styles.addOn)}
              title={importing ? "stop importing" : "import another project"}
              aria-pressed={importing}
              disabled={busy}
              onClick={() => setImporting(!importing)}
            >
              <FolderPlusIcon size={12} />
            </button>
            {/* Said rather than left to be discovered from the result.
                Somebody who expects this to follow the thread's branch should
                find out here and not from a diff. */}
            <span {...stylex.props(styles.said)}>
              {spare.length > 0 ? (
                <>
                  from its own main line, and named{" "}
                  {thread.members[0]?.workspace ?? "after its sibling"}
                </>
              ) : projects.projects.length === 0 ? (
                "No projects yet — import one."
              ) : (
                "Every project awp knows about is already in this thread."
              )}
            </span>
          </div>

          {importing && <ImportProject projects={projects.projects} onImported={projects.reload} />}

          {spare.length > 0 && (
            <>
              <textarea
                ref={box}
                value={brief}
                onChange={(event) => setBrief(event.target.value)}
                // ── the hint has to fit the box it is in ──────────────────
                //
                // `useGrow` measures `scrollHeight`, which is a property of
                // the *value* — a placeholder is painted and contributes
                // nothing to it. So a one-line box with a placeholder that
                // wraps clips its own hint, with nothing on screen saying so,
                // and the empty state is the state this dialog opens in.
                //
                // Shortened rather than the box grown: one line at rest is
                // what every other composer in this window is, and the half
                // that did not fit was explaining the field rather than
                // naming it. It is on the tooltip, where it costs no pixels
                // until it is asked for.
                placeholder="what this repository's half of the work is"
                title="the new agent is briefed with this, and nothing else"
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
