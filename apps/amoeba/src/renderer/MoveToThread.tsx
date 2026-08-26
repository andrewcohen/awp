import { Menu } from "@base-ui/react/menu";
import type { Thread } from "@awp-kit/protocol";
import * as stylex from "@stylexjs/stylex";
import { useState } from "react";
import { attachThread, createThread, detachThread } from "./daemon";
import { colors, text } from "./tokens.stylex";
import type { Workspace } from "./workspaces";

// Taking an existing workspace into a thread.
//
// ── the gap this closes ────────────────────────────────────────────────────
// A thread was only ever made by *starting* one, which creates its first
// workspace as part of the job. That covers everything from now on and nothing
// from before: every workspace on a machine that has been used predates threads
// and falls into "not in a thread", with no way out. Twenty-three of the
// twenty-six rows here.
//
// Nothing new reaches the daemon for this. `ThreadCreate`, `ThreadAttach` and
// `ThreadDetach` have been on the wire since threads landed and were reachable
// by nothing at all.
//
// ── why a menu, and why it is not a button per row ─────────────────────────
// The action belongs to one workspace, so it has to be on the row; a row is two
// lines already and a visible control on every one of twenty-six would be a
// column of identical glyphs beside the names they crowd out. So the trigger
// appears on hover and on keyboard focus — the second is not optional, or the
// feature does not exist for anyone not using a pointer.
//
// Base UI owns the menu, which is the whole reason it is a dependency: the
// arrow keys, the typeahead, the roving tab stop, the escape handling, the
// focus return to the trigger, and the portal — without which this popup would
// be clipped by the scrolling column it opens inside.
//
// ── one claim, and no detach-then-attach ───────────────────────────────────
// A workspace belongs to at most one thread, enforced by a UNIQUE constraint on
// `thread_members`. `attach` is one `on conflict do update`, so moving between
// threads is a single call that cannot half happen. Choosing a thread from here
// never detaches first, deliberately: two calls is where a workspace ends up in
// neither.

const styles = stylex.create({
  trigger: {
    flexShrink: 0,
    padding: "0 0.25rem",
    backgroundColor: "transparent",
    borderStyle: "none",
    color: colors.muted,
    font: "inherit",
    fontSize: text.small,
    lineHeight: 1,
    cursor: "pointer",
    // Hidden until wanted, but never `display: none` — a control that is not
    // in the layout cannot be focused, and the keyboard is the half of this
    // that hover would otherwise take away.
    opacity: 0,
    ":focus-visible": { opacity: 1 },
  },
  shown: { opacity: 1 },
  positioner: { zIndex: 20 },
  menu: {
    minWidth: "12rem",
    maxHeight: "18rem",
    overflowY: "auto",
    padding: "0.25rem",
    backgroundColor: colors.base,
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.35rem",
    color: colors.text,
    fontFamily: text.mono,
    fontSize: text.small,
    boxShadow: "0 0.5rem 1.5rem rgba(0, 0, 0, 0.35)",
  },
  item: {
    display: "flex",
    alignItems: "center",
    gap: "0.4rem",
    padding: "0.25rem 0.4rem",
    borderRadius: "0.15rem",
    cursor: "pointer",
    ":is([data-highlighted])": { backgroundColor: colors.border },
  },
  label: {
    padding: "0.3rem 0.4rem 0.15rem",
    color: colors.muted,
    fontSize: text.tiny,
  },
  tick: { width: "0.8rem", flexShrink: 0, color: colors.live },
  name: { flex: 1, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" },
  rule: { height: 1, margin: "0.25rem 0", backgroundColor: colors.border },
  failure: { padding: "0.25rem 0.4rem", color: colors.warn, fontSize: text.tiny },
});

export function MoveToThread({
  workspace,
  threads,
  current,
  shown,
  onChanged,
}: {
  readonly workspace: Workspace;
  /** Live threads only — an archived one is not somewhere to put work. */
  readonly threads: ReadonlyArray<Thread>;
  /** The thread holding it now, if any. */
  readonly current: Thread | undefined;
  /** The row is hovered. Focus reveals the trigger on its own — see `trigger`. */
  readonly shown: boolean;
  readonly onChanged: () => void;
}) {
  const [failure, setFailure] = useState<string | undefined>();

  // The identity, not the row's display name. `project` and `workspace` are
  // what `thread_members` is keyed on, and the row's name is a *rendering* — a
  // `default` workspace is shown as its project.
  const id = workspace.sessions[0]?.identity;
  if (id === undefined || workspace.foreign) {
    // A session awp did not create is not a workspace and cannot be claimed.
    // Rendering a disabled control would be offering something that can never
    // work; rendering nothing says the same thing more honestly.
    return null;
  }
  const member = { project: id.project, workspace: id.workspace };

  const run = (work: () => Promise<unknown>) => {
    setFailure(undefined);
    work()
      .then(onChanged)
      .catch((error: unknown) => {
        setFailure(error instanceof Error ? error.message : String(error));
      });
  };

  return (
    <Menu.Root>
      <Menu.Trigger
        aria-label={`put ${workspace.name} in a thread`}
        title="put in a thread"
        {...stylex.props(styles.trigger, shown && styles.shown)}
      >
        ⋯
      </Menu.Trigger>

      <Menu.Portal>
        <Menu.Positioner sideOffset={4} align="end" {...stylex.props(styles.positioner)}>
          <Menu.Popup {...stylex.props(styles.menu)}>
            <div {...stylex.props(styles.label)}>put in a thread</div>

            {threads.map((thread) => (
              <Menu.Item
                key={thread.id}
                onClick={() => run(() => attachThread(thread.id, member))}
                {...stylex.props(styles.item)}
              >
                <span aria-hidden {...stylex.props(styles.tick)}>
                  {thread.id === current?.id ? "✓" : ""}
                </span>
                <span {...stylex.props(styles.name)}>
                  {thread.title === "" ? "untitled" : thread.title}
                </span>
              </Menu.Item>
            ))}

            {threads.length > 0 && <div {...stylex.props(styles.rule)} />}

            {/* Named after the work, because there is nowhere to type a name
                from a menu and a dialog for one field is a dialog too many.
                The label is what the person called it if there is one, and the
                slug otherwise — see `Workspace.label`. */}
            <Menu.Item
              onClick={() =>
                run(async () => {
                  const thread = await createThread(workspace.label ?? workspace.name);
                  return attachThread(thread.id, member);
                })
              }
              {...stylex.props(styles.item)}
            >
              <span aria-hidden {...stylex.props(styles.tick)} />
              <span {...stylex.props(styles.name)}>new thread</span>
            </Menu.Item>

            {current !== undefined && (
              <Menu.Item
                onClick={() => run(() => detachThread(current.id, member))}
                {...stylex.props(styles.item)}
              >
                <span aria-hidden {...stylex.props(styles.tick)} />
                <span {...stylex.props(styles.name)}>take out of {current.title}</span>
              </Menu.Item>
            )}

            {/* In the menu rather than a toast. Whatever failed, failed to the
                thing that was just clicked, and this is where the eye is. */}
            {failure !== undefined && <div {...stylex.props(styles.failure)}>{failure}</div>}
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
}
