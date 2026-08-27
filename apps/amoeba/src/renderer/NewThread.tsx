import type { Effort, Project, ThreadBase } from "@awp-kit/protocol";
import { Dialog } from "@base-ui/react/dialog";
import { Select } from "@base-ui/react/select";
import { ArrowUpIcon } from "@phosphor-icons/react/ArrowUp";
import { CaretDownIcon } from "@phosphor-icons/react/CaretDown";
import { FolderIcon } from "@phosphor-icons/react/Folder";
import { FolderPlusIcon } from "@phosphor-icons/react/FolderPlus";
import { GitBranchIcon } from "@phosphor-icons/react/GitBranch";
import * as stylex from "@stylexjs/stylex";
import { useEffect, useState } from "react";
import { startThread, threadBases } from "./daemon";
import { ImportProject } from "./ImportProject";
import { useOverlay } from "./overlays";
import { colors, text } from "./tokens.stylex";

// Starting a thread: a composer, not a form.
//
// The shape is a chat message box, and that is the whole design decision. What
// a person supplies here is one piece of prose — everything else on the screen
// is a setting *about* that prose, and settings framed as form fields make the
// prose look like one field among six. Framed as a composer, the sentence is
// the screen and the settings are furniture around it:
//
//   ┌──────────────────────────────────────────────┐
//   │  ▤ awp ▾    ⑂ trunk ▾                        │  where it lands
//   ├──────────────────────────────────────────────┤
//   │  ┌──────────────────────────────┐   ┌────┐   │
//   │  │ what are you working on?     │   │ ↑  │   │  the thing you write
//   │  └──────────────────────────────┘   └────┘   │
//   ├──────────────────────────────────────────────┤
//   │  opus ▾   high ▾               naming it…    │  how it runs
//   └──────────────────────────────────────────────┘
//
// The two bars are not the same kind of thing and are not interchangeable.
// The top one is about the *result* — which project, which revision — and is
// read before typing. The bottom one is about the *agent*, and is read rarely
// because the config already answers it. Destination above, machinery below; a
// setting that moved between them would be in the wrong one.
//
// ── what this replaced ─────────────────────────────────────────────────────
// An inline box in the sidebar with a single field. The project came from
// whichever row was selected, so starting a thread in a project meant first
// clicking a workspace in it — and with nothing selected the button was
// disabled outright, which is a dead end on a freshly opened window.
//
// ── Base UI, and why every control here is one ─────────────────────────────
// Dialog and Select ship no styles; what they ship is the behaviour. The
// dialog traps focus, restores it on close, closes on Escape and makes the
// rest of the page inert. The chips own arrow keys, typeahead, the
// `aria-expanded`/`aria-activedescendant` wiring, and the portal that keeps a
// popup from being clipped by whatever it opens inside.
//
// Appearance stays StyleX. See AGENTS.md: Base UI for behaviour, StyleX for
// how it looks, and no third thing.

/** Everything `claude --effort` accepts, in the order it reads as a scale. */
const EFFORTS: ReadonlyArray<Effort> = ["low", "medium", "high", "xhigh", "max"];

/**
 * The aliases, not the full ids.
 *
 * `--model` takes either, and a chip is not the place for `claude-opus-5`. A
 * pinned full id is still reachable through the config, which is where a
 * decision that outlives one thread belongs anyway.
 */
const MODELS: ReadonlyArray<string> = ["opus", "sonnet", "haiku"];

/**
 * The sentinel for "whatever the config says".
 *
 * Deliberately not any real value: choosing nothing has to be different from
 * choosing the same thing the config chose, or the config could never win. It
 * becomes `undefined` on the wire, which is what `agentWith` reads as "leave
 * the argv alone".
 */
const INHERIT = "";

/**
 * The project's main line, and what the chip falls back to.
 *
 * jj resolves it through the remote's default bookmark and then main, master,
 * trunk, so the usual answer needs no configuration. It is spelled out here
 * only as a default while the daemon's list is still arriving.
 */
const TRUNK = "trunk()";

/** What the window knew when the modal was opened. */
export interface NewThreadRequest {
  /** Which project to open on — the selected row's, when there is one. */
  readonly project: string | undefined;
  /**
   * The workspace on screen, if any.
   *
   * Matched against the bases the daemon reports, so cmd+shift+N can start on
   * the branch a person is standing in. A name and not a revset: only the
   * daemon knows the bookmark prefix that turns one into the other.
   */
  readonly workspace: string | undefined;
  /** True when opened with cmd+shift+N, which asks to branch from that one. */
  readonly fromWorkspace: boolean;
}

const styles = stylex.create({
  // Dimmed rather than blurred. The window behind is a terminal, and blurring
  // legible text is a way of making it look broken.
  backdrop: { position: "fixed", inset: 0, backgroundColor: "rgba(0, 0, 0, 0.4)" },

  popup: {
    position: "fixed",
    // Above centre. A dialog centred exactly reads as low, because the eye
    // takes the middle of a window to be above its middle.
    top: "38%",
    left: "50%",
    transform: "translate(-50%, -50%)",
    display: "flex",
    flexDirection: "column",
    width: "min(38rem, calc(100vw - 4rem))",
    backgroundColor: colors.surface,
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.5rem",
    color: colors.text,
    fontSize: text.body,
    boxShadow: "0 1rem 3rem rgba(0, 0, 0, 0.35)",
    // No padding of its own. Each band pads itself, so the rules between them
    // run the full width — a rule stopping short of the edge reads as a
    // mistake rather than as a division.
  },

  // The accessible name. On screen the chips already say where it lands and
  // the placeholder says what to do, so a heading would be a fourth band
  // repeating both — but a dialog with no name is one a screen reader
  // announces as nothing at all.
  hidden: {
    position: "absolute",
    width: 1,
    height: 1,
    margin: -1,
    padding: 0,
    overflow: "hidden",
    clipPath: "inset(50%)",
    whiteSpace: "nowrap",
  },

  bar: {
    display: "flex",
    alignItems: "center",
    gap: "0.4rem",
    flexShrink: 0,
    padding: "0.45rem 0.6rem",
  },
  barTop: { borderBottomWidth: 1, borderBottomStyle: "solid", borderBottomColor: colors.border },
  barBottom: { borderTopWidth: 1, borderTopStyle: "solid", borderTopColor: colors.border },

  // A chip: the control reduced to its value. No box, no label, no fixed
  // width — the row reads as a phrase about where this is going rather than as
  // a set of inputs, and the border appears only when it is pointed at.
  chip: {
    display: "flex",
    alignItems: "center",
    gap: "0.3rem",
    padding: "0.15rem 0.4rem",
    backgroundColor: "transparent",
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: { default: "transparent", ":hover": colors.border },
    borderRadius: "0.25rem",
    color: colors.text,
    font: "inherit",
    fontSize: text.small,
    cursor: "pointer",
  },
  chipIcon: { flexShrink: 0, color: colors.muted },
  chipCaret: { flexShrink: 0, color: colors.muted, opacity: 0.7 },
  // The bottom bar's pair are the quieter ones while they say nothing but
  // their own name. Choosing a value is what makes one worth noticing.
  chipQuiet: { color: colors.muted },

  // The composer. Aligned to the bottom, so the button stays beside the last
  // line as the box grows rather than floating beside the first.
  composer: {
    display: "flex",
    alignItems: "flex-end",
    gap: "0.5rem",
    padding: "0.6rem",
  },
  brief: {
    flex: 1,
    minWidth: 0,
    minHeight: "3.5rem",
    maxHeight: "14rem",
    padding: "0.25rem",
    backgroundColor: "transparent",
    // Borderless on purpose: the popup's own edge is the box. A border here
    // would be a box inside a box, which is what made the first attempt read
    // as a form.
    borderStyle: "none",
    outlineStyle: "none",
    resize: "none",
    color: colors.text,
    font: "inherit",
    // The one thing on this screen a person is actually composing, so it is
    // the one thing set above body size.
    fontSize: text.lead,
    lineHeight: 1.5,
  },
  send: {
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    flexShrink: 0,
    width: "1.9rem",
    height: "1.9rem",
    padding: 0,
    backgroundColor: colors.border,
    borderStyle: "none",
    borderRadius: "0.95rem",
    color: colors.text,
    cursor: "pointer",
  },
  sendShut: { opacity: 0.35, cursor: "default" },

  // Takes the slack in the bottom bar, so the chips stay left whatever it says.
  status: {
    flex: 1,
    minWidth: 0,
    overflow: "hidden",
    textOverflow: "ellipsis",
    whiteSpace: "nowrap",
    textAlign: "right",
    color: colors.muted,
    fontSize: text.small,
  },
  warn: { color: colors.warn },

  positioner: { zIndex: 10 },
  menu: {
    padding: "0.2rem",
    backgroundColor: colors.surface,
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.25rem",
    color: colors.text,
    // Monospace, and it is the one menu in the window that keeps it: what this
    // mostly lists is bookmark names, and a bookmark is an address — something
    // a person will type at jj afterwards. The model and effort menus share the
    // component and get it too, which is the cost of one component for four
    // chips and is a smaller cost than four components.
    fontFamily: text.mono,
    fontSize: text.small,
    boxShadow: "0 0.5rem 1.5rem rgba(0, 0, 0, 0.3)",
  },
  item: {
    display: "flex",
    alignItems: "center",
    gap: "0.4rem",
    padding: "0.2rem 0.4rem",
    borderRadius: "0.15rem",
    cursor: "pointer",
    // Base UI sets the attribute; what it looks like is this file's business.
    ":is([data-highlighted])": { backgroundColor: colors.border },
  },
  tick: { width: "0.75rem", flexShrink: 0, color: colors.muted, fontSize: text.small },
  empty: {
    padding: "0.9rem 0.9rem 0",
    color: colors.muted,
    fontSize: text.small,
    lineHeight: 1.5,
  },

  // Square, where the chips beside it are phrases. It is a verb, not a value,
  // and giving it a chip's shape would put it in the sentence the bar reads as.
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
});

/**
 * A value, as a chip that opens a list.
 *
 * One component for all four, because four copies of the
 * portal/positioner/popup stack is four chances for one of them to be spelled
 * differently — and the one that differs is the one whose popup gets clipped.
 */
function Chip<T extends string>({
  id,
  label,
  title,
  value,
  onChange,
  options,
  icon,
  quiet,
  disabled,
}: {
  readonly id: string;
  /** What the chip reads as, which is not always the raw value. */
  readonly label: string;
  readonly title: string;
  readonly value: T;
  readonly onChange: (value: T) => void;
  /** In the order they should be read. */
  readonly options: ReadonlyArray<{ readonly value: T; readonly label: string }>;
  readonly icon?: React.ReactNode | undefined;
  readonly quiet?: boolean | undefined;
  readonly disabled?: boolean | undefined;
}) {
  return (
    <Select.Root
      value={value}
      // Base UI types the value as whatever it was given, so it arrives here
      // unnarrowed. This is the only cast on the screen, and it is here rather
      // than at four call sites: every value the popup can emit came out of
      // `options`, so T is exactly what it is.
      onValueChange={(next) => onChange(String(next) as T)}
      disabled={disabled ?? false}
    >
      <Select.Trigger
        id={id}
        title={title}
        {...stylex.props(styles.chip, quiet === true && styles.chipQuiet)}
      >
        {icon}
        {/* The chip shows the label, which for a revset is not the value —
            `trunk()` is spelled `trunk` here and the title says the rest. */}
        <span>{label}</span>
        <CaretDownIcon size={9} weight="bold" {...stylex.props(styles.chipCaret)} />
      </Select.Trigger>

      {/* Portalled, so nothing it opens inside can clip it. */}
      <Select.Portal>
        <Select.Positioner sideOffset={4} align="start" {...stylex.props(styles.positioner)}>
          <Select.Popup {...stylex.props(styles.menu)}>
            {options.map((option) => (
              <Select.Item key={option.value} value={option.value} {...stylex.props(styles.item)}>
                <Select.ItemIndicator {...stylex.props(styles.tick)}>✓</Select.ItemIndicator>
                <Select.ItemText>{option.label}</Select.ItemText>
              </Select.Item>
            ))}
          </Select.Popup>
        </Select.Positioner>
      </Select.Portal>
    </Select.Root>
  );
}

/**
 * The composer. Mounted when the dialog opens, unmounted when it closes.
 *
 * That is what makes `useState` initialisers the right place for the defaults:
 * they run exactly when the modal is opened, so cmd+shift+N's base and the
 * selected row's project are read at the moment they were meant. An effect
 * syncing them would be a second source for the same values, and the two would
 * disagree the first time the selection moved with the modal open.
 */
function Composer({
  request,
  projects,
  onClose,
  onStarted,
  onProjects,
}: {
  readonly request: NewThreadRequest;
  readonly projects: ReadonlyArray<Project>;
  readonly onClose: () => void;
  readonly onStarted: () => void;
  /** Read the project list again — an import happened. */
  readonly onProjects: () => void;
}) {
  const first = projects[0]?.name ?? "";
  const [project, setProject] = useState(
    projects.some((p) => p.name === request.project) ? (request.project ?? first) : first,
  );
  const [typed, setTyped] = useState("");
  // A revset: `trunk()`, or a bookmark name the daemon offered.
  const [base, setBase] = useState(TRUNK);
  const [bases, setBases] = useState<ReadonlyArray<ThreadBase>>([]);
  const [model, setModel] = useState(INHERIT);
  const [effort, setEffort] = useState<Effort | typeof INHERIT>(INHERIT);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | undefined>();
  const [importing, setImporting] = useState(false);

  // The repository root, which is a directory *inside* the project as far as
  // the daemon is concerned — it resolves it again with `sourceRoot` rather
  // than trusting what a client sends. See `ThreadStart`.
  const from = projects.find((p) => p.name === project)?.root;

  // Fetched per project, because a bookmark belongs to a repository. An effect
  // and not a value: this is a request, and the project it is for can change
  // while the modal is open.
  useEffect(() => {
    if (from === undefined) {
      return;
    }
    let live = true;
    threadBases(from)
      .then((found) => {
        if (!live) {
          return;
        }
        setBases(found);
        // Preselected only for cmd+shift+N, and only once the list is here —
        // the client cannot compose `<prefix>/<name>` itself, so the match has
        // to wait for the daemon to say which base is that workspace's.
        const here = found.find((entry) => entry.workspace === request.workspace);
        if (request.fromWorkspace && here !== undefined) {
          setBase(here.revset);
        }
      })
      .catch(() => {
        // Left as trunk. A base list that cannot be fetched is not a reason to
        // refuse to start a thread on the project's main line.
      });
    return () => {
      live = false;
    };
  }, [from, request.workspace, request.fromWorkspace]);

  const described = typed.trim();
  const ready = from !== undefined && described !== "" && !busy;

  const submit = () => {
    if (!ready) {
      return;
    }
    setBusy(true);
    setFailure(undefined);
    startThread({
      description: described,
      project,
      from,
      base,
      model: model === INHERIT ? undefined : model,
      effort: effort === INHERIT ? undefined : effort,
    })
      .then(() => {
        onStarted();
        onClose();
      })
      .catch((error: unknown) => {
        setFailure(String(error));
        setBusy(false);
      });
  };

  if (projects.length === 0) {
    // The import panel *is* the empty state, rather than a sentence explaining
    // why the modal is useless with a way to fix it somewhere else. This was
    // that sentence for a while, and it named no route out of the situation
    // because there was not one: a project existed only where a session was
    // already running, so a machine with none had no way to get its first.
    return (
      <>
        <div {...stylex.props(styles.empty)}>
          No projects yet. Point awp at a repository — any directory inside it will do.
        </div>
        <ImportProject projects={projects} onImported={onProjects} />
      </>
    );
  }

  return (
    <>
      {/* Where it lands. Read before typing, which is why it is above. */}
      <div {...stylex.props(styles.bar, styles.barTop)}>
        <Chip
          id="new-thread-project"
          label={project}
          title="the project the workspace is made in"
          value={project}
          onChange={setProject}
          options={projects.map((p) => ({ value: p.name, label: p.name }))}
          icon={<FolderIcon size={11} {...stylex.props(styles.chipIcon)} />}
          disabled={busy}
        />
        {/* Beside the project it adds to, because that is what it is about.
            A toggle rather than a second dialog: a modal over a modal is a
            stack to get out of, and the panel it opens is four lines tall. */}
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
        {/* Where the work starts. Every local bookmark in the project, which
            is what a person recognises — and what the first version got wrong
            by offering *threads*: most workspaces on a real machine belong to
            none, so the list was empty exactly when someone was standing in a
            branch they wanted to continue from. */}
        <Chip
          id="new-thread-base"
          label={bases.find((entry) => entry.revset === base)?.label ?? "trunk"}
          title={
            base === TRUNK
              ? "trunk() — jj resolves the remote's default bookmark, then main, master, trunk"
              : `branch from ${base}`
          }
          value={base}
          onChange={setBase}
          options={
            bases.length === 0
              ? [{ value: TRUNK, label: "trunk" }]
              : bases.map((entry) => ({ value: entry.revset, label: entry.label }))
          }
          icon={<GitBranchIcon size={11} {...stylex.props(styles.chipIcon)} />}
          disabled={busy}
        />
      </div>

      {/* Between the bars rather than over them, so what it adds to stays on
          screen while it is open. */}
      {importing && <ImportProject projects={projects} onImported={onProjects} />}

      {/* The thing you write, and the one button that acts on it. */}
      <div {...stylex.props(styles.composer)}>
        <textarea
          // Focus on mount, which is the moment the modal opened. A callback
          // ref rather than `autoFocus`, which react-doctor flags because the
          // attribute fires on any render the element mounts in.
          ref={(node) => node?.focus()}
          value={typed}
          disabled={busy}
          rows={2}
          placeholder="what are you working on?"
          onChange={(event) => setTyped(event.target.value)}
          onKeyDown={(event) => {
            // Enter sends and shift+enter is a newline, which is the chat
            // convention this box is shaped like — and the same rule the pane
            // already follows for the agent's own prompt. A brief is usually
            // one sentence; the newline is there for when it is not.
            if (event.key === "Enter" && !event.shiftKey) {
              event.preventDefault();
              submit();
            }
          }}
          {...stylex.props(styles.brief)}
        />
        <button
          type="button"
          disabled={!ready}
          title={ready ? "start the thread (enter)" : "say what you are working on first"}
          aria-label="start the thread"
          onClick={submit}
          {...stylex.props(styles.send, !ready && styles.sendShut)}
        >
          <ArrowUpIcon size={13} weight="bold" />
        </button>
      </div>

      {/* How it runs. Read rarely, because the config already answers it. */}
      <div {...stylex.props(styles.bar, styles.barBottom)}>
        <Chip
          id="new-thread-model"
          label={model === INHERIT ? "model" : model}
          title="which model the agent runs, or the one the config already names"
          value={model}
          onChange={setModel}
          options={[
            { value: INHERIT, label: "from settings" },
            ...MODELS.map((name) => ({ value: name, label: name })),
          ]}
          quiet={model === INHERIT}
          disabled={busy}
        />
        <Chip
          id="new-thread-effort"
          label={effort === INHERIT ? "effort" : effort}
          title="how hard the agent is asked to think"
          value={effort}
          onChange={setEffort}
          options={[
            { value: INHERIT, label: "from settings" },
            ...EFFORTS.map((level) => ({ value: level, label: level })),
          ]}
          quiet={effort === INHERIT}
          disabled={busy}
        />

        <div {...stylex.props(styles.status, failure !== undefined && styles.warn)}>
          {/* "naming it" is what the ten seconds actually are — a model turning
              the brief into a workspace name — and saying so is what makes the
              wait read as work rather than as a hang. */}
          {failure ?? (busy ? "naming it…" : "")}
        </div>
      </div>
    </>
  );
}

/**
 * The dialog, mounted only while it is open.
 *
 * Mount-on-open rather than a `keepMounted` popup, and it is the same decision
 * as the one in `Composer`'s doc comment seen from outside: a modal that exists
 * while shut is a modal holding what was typed into it last time. What it gives
 * up is an exit animation, which this window has nowhere else.
 */
export function NewThread({
  request,
  projects,
  onClose,
  onStarted,
  onProjects,
}: {
  readonly request: NewThreadRequest | undefined;
  readonly projects: ReadonlyArray<Project>;
  readonly onClose: () => void;
  readonly onStarted: () => void;
  readonly onProjects: () => void;
}) {
  // Before the early return, because a hook cannot be called conditionally —
  // and the argument is the condition, which is what the hook is shaped for.
  //
  // Announced by the dialog rather than counted by whatever draws over it: the
  // web panel cannot see a portal that renders outside its subtree, and even a
  // full-window `pointer-events` probe would be guessing at what a modal is.
  // The component that knows it opened a modal is this one.
  useOverlay(request !== undefined);

  if (request === undefined) {
    return null;
  }

  return (
    <Dialog.Root
      open
      onOpenChange={(next) => {
        if (!next) {
          onClose();
        }
      }}
    >
      <Dialog.Portal>
        <Dialog.Backdrop {...stylex.props(styles.backdrop)} />
        <Dialog.Popup {...stylex.props(styles.popup)}>
          <Dialog.Title {...stylex.props(styles.hidden)}>new thread</Dialog.Title>
          <Composer
            request={request}
            projects={projects}
            onClose={onClose}
            onStarted={onStarted}
            onProjects={onProjects}
          />
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
