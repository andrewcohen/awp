import type { Project } from "@awp-kit/protocol";
import { ArrowUpIcon } from "@phosphor-icons/react/ArrowUp";
import { FolderIcon } from "@phosphor-icons/react/Folder";
import * as stylex from "@stylexjs/stylex";
import { useEffect, useState } from "react";
import { XIcon } from "@phosphor-icons/react/X";
import { forgetProject, importProject, projectCandidates } from "./daemon";
import { colors, text } from "./tokens.stylex";

// Telling awp about a repository.
//
// Two routes in, and the order they are drawn in is the order they are worth:
//
//   a path       works on any machine, with no configuration at all
//   a candidate  a repository found under `deck.project_roots` — a
//                convenience over the first, and empty for most people
//
// The path field is therefore first and always present, and the found list is
// whatever happens to be below it. Leading with the list would have made the
// panel look broken for anyone with no roots configured, which is the majority
// and includes anybody importing their first project.
//
// The imported ones are listed here too, each with a way to forget it. Not a
// third route in — the same panel seen from the other side, and the only place
// in the window where a project is a thing rather than a chip's value. Forget
// takes no workspace, session or thread with it; it is a statement about this
// list alone, which is what makes it safe to put beside a name.
//
// **The daemon resolves the repository, not this.** A path inside a project is
// enough, and that is not a convenience: `jj root` inside a secondary workspace
// answers with the *workspace*, so a client resolving its own would sometimes
// import a checkout as though it were the project. See `ProjectImport`.

const styles = stylex.create({
  panel: { display: "flex", flexDirection: "column", gap: "0.6rem", padding: "0.8rem 0.9rem" },

  row: { display: "flex", alignItems: "center", gap: "0.4rem" },

  field: {
    flex: 1,
    // The pair. Without it a long path pushes the button off the row rather
    // than being clipped — a flex item will not shrink below its content.
    minWidth: 0,
    padding: "0.35rem 0.5rem",
    backgroundColor: colors.surface,
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.3rem",
    color: colors.text,
    fontFamily: text.mono,
    fontSize: text.body,
    outline: "none",
  },

  go: {
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    flexShrink: 0,
    width: "1.7rem",
    height: "1.7rem",
    padding: 0,
    backgroundColor: colors.border,
    borderStyle: "none",
    borderRadius: "0.85rem",
    color: colors.text,
    cursor: "pointer",
  },
  goShut: { opacity: 0.35, cursor: "default" },

  heading: { color: colors.muted, fontSize: text.small },

  found: {
    display: "flex",
    flexDirection: "column",
    // A list of repositories on a real machine is long. It scrolls in its own
    // box rather than growing the dialog past the window.
    maxHeight: "11rem",
    overflowY: "auto",
    overflowX: "hidden",
  },

  candidate: {
    display: "flex",
    alignItems: "baseline",
    gap: "0.5rem",
    width: "100%",
    padding: "0.25rem 0.35rem",
    backgroundColor: "transparent",
    borderStyle: "none",
    borderRadius: "0.25rem",
    color: colors.text,
    fontSize: text.body,
    textAlign: "start",
    cursor: "pointer",
    ":hover": { backgroundColor: colors.border },
  },

  // The path beside the name. Truncated rather than wrapped: it is there to
  // tell two repositories of the same name apart, and the end of it is the
  // half that does that.
  where: {
    flex: 1,
    minWidth: 0,
    overflow: "hidden",
    textOverflow: "ellipsis",
    whiteSpace: "nowrap",
    color: colors.muted,
    // A path, so monospace — it is the thing that tells two repositories of
    // the same name apart, and it is the thing somebody will type elsewhere.
    fontFamily: text.mono,
    fontSize: text.small,
    textAlign: "end",
  },

  // A row, not a button: nothing happens when it is clicked, and giving it a
  // button's hover would say otherwise.
  mine: { cursor: "default", ":hover": { backgroundColor: "transparent" } },

  drop: {
    display: "flex",
    alignItems: "center",
    flexShrink: 0,
    padding: "0.1rem",
    backgroundColor: "transparent",
    borderStyle: "none",
    borderRadius: "0.2rem",
    color: colors.muted,
    cursor: "pointer",
    ":hover": { color: colors.warn },
  },

  failure: { color: colors.warn, fontSize: text.small, lineHeight: 1.4 },
});

export function ImportProject({
  projects,
  onImported,
}: {
  /** What is already imported, so it can be listed and forgotten. */
  readonly projects: ReadonlyArray<Project>;
  readonly onImported: () => void;
}) {
  const [path, setPath] = useState("");
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | undefined>();
  const [found, setFound] = useState<ReadonlyArray<Project>>([]);

  // Asked for once, when the panel opens. It walks somebody's filesystem, so
  // it is not part of the project list and is not refreshed on a timer.
  useEffect(() => {
    let live = true;
    projectCandidates()
      .then((all) => {
        if (live) {
          setFound(all);
        }
      })
      // Silent, deliberately: no roots configured is the ordinary case, and a
      // walk that failed still leaves the path field working. A red line here
      // would report a broken panel that is not broken.
      .catch(() => undefined);
    return () => {
      live = false;
    };
  }, []);

  // Only the imported ones. A project the sessions merely imply has no row to
  // forget — offering one would be offering a button that does nothing, since
  // the next listing derives it again.
  const mine = projects.filter((one) => one.importedAt !== undefined);

  const drop = (name: string): void => {
    if (busy) {
      return;
    }
    setBusy(true);
    setFailure(undefined);
    forgetProject(name)
      .then(() => {
        onImported();
        setBusy(false);
      })
      .catch((error: unknown) => {
        setFailure(String(error));
        setBusy(false);
      });
  };

  const send = (wanted: string): void => {
    if (busy || wanted.trim() === "") {
      return;
    }
    setBusy(true);
    setFailure(undefined);
    importProject(wanted)
      .then(() => {
        setPath("");
        // Reloads the list in the parent. The imported row has to appear before
        // the project chip can select it, and the reply alone does not put it
        // anywhere the chip can see.
        onImported();
        setBusy(false);
      })
      .catch((error: unknown) => {
        // The daemon's own sentence — it names the directory and says whether
        // it is missing or merely not a repository. One composed here would say
        // less and could be wrong.
        setFailure(String(error));
        setBusy(false);
      });
  };

  return (
    <div {...stylex.props(styles.panel)}>
      <div {...stylex.props(styles.row)}>
        <input
          {...stylex.props(styles.field)}
          value={path}
          placeholder="path to a repository"
          spellCheck={false}
          autoComplete="off"
          disabled={busy}
          onChange={(event) => setPath(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              // Or the dialog around this takes it as a submit of the thread
              // that has not been written yet.
              event.preventDefault();
              send(path);
            }
          }}
        />
        <button
          type="button"
          {...stylex.props(styles.go, (busy || path.trim() === "") && styles.goShut)}
          title="import this path"
          disabled={busy || path.trim() === ""}
          onClick={() => send(path)}
        >
          <ArrowUpIcon size={13} weight="bold" />
        </button>
      </div>

      {failure !== undefined && <div {...stylex.props(styles.failure)}>{failure}</div>}

      {mine.length > 0 && (
        <>
          <div {...stylex.props(styles.heading)}>imported</div>
          <div {...stylex.props(styles.found)}>
            {mine.map((one) => (
              <div key={one.root} {...stylex.props(styles.candidate, styles.mine)}>
                <FolderIcon size={11} />
                <span>{one.name}</span>
                <span {...stylex.props(styles.where)}>{one.root}</span>
                <button
                  type="button"
                  data-nav-item
                  {...stylex.props(styles.drop)}
                  title={`forget ${one.name} — nothing else is removed`}
                  disabled={busy}
                  onClick={() => drop(one.name)}
                >
                  <XIcon size={11} />
                </button>
              </div>
            ))}
          </div>
        </>
      )}

      {found.length > 0 && (
        <>
          <div {...stylex.props(styles.heading)}>found under your project roots</div>
          <div {...stylex.props(styles.found)}>
            {found.map((one) => (
              <button
                key={one.root}
                type="button"
                data-nav-item
                {...stylex.props(styles.candidate)}
                disabled={busy}
                onClick={() => send(one.root)}
              >
                <FolderIcon size={11} />
                <span>{one.name}</span>
                <span {...stylex.props(styles.where)}>{one.root}</span>
              </button>
            ))}
          </div>
        </>
      )}
    </div>
  );
}
