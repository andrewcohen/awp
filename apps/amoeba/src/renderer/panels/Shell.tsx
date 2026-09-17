import type { ColorScheme } from "@awp-kit/pane";
import type { SessionInfo } from "@awp-kit/protocol";
import { Tabs } from "@base-ui/react/tabs";
import { PlusIcon } from "@phosphor-icons/react/Plus";
import { TerminalWindowIcon } from "@phosphor-icons/react/TerminalWindow";
import { XIcon } from "@phosphor-icons/react/X";
import * as stylex from "@stylexjs/stylex";
import { useEffect, useState } from "react";
import { Nothing } from "./Nothing";
import { shellsOf } from "./shells";
import { Pane } from "./Pane";
import { closeShell, listSessions, onReconnect, openShell, said } from "../data/daemon";
import { typeset } from "../design/typeset";
import { colors, space, text } from "../design/tokens.stylex";

// A workspace's shells, one strip of them.
//
// ── what this is for, and why it is not the agent's terminal ───────────────
//
// The stage draws the one session a workspace is *about* — its agent. A shell
// is the other thing somebody needs while work is happening: run the test
// themselves, read a log, check what the agent just claimed. Until now that
// meant leaving the window.
//
// ── two terminals, and the pane package had to learn that first ────────────
//
// @awp-kit/pane kept one Terminal for the window and re-parented it on mount,
// so a second pane would have taken the canvas away from the stage and left it
// blank. It keeps one per *slot* now — see terminal.ts for why a slot is a
// place in the layout rather than a session. This panel is `accessory`, the
// stage is `stage`, and switching tabs in here re-attaches the accessory
// terminal exactly as switching workspaces re-attaches the stage's.
//
// So: **one shell on screen at a time**, whatever the strip says. That is not a
// limitation being worked around — an accessory column is one column wide and
// two terminals in it would each be too narrow to read.

const styles = stylex.create({
  column: { display: "flex", flexDirection: "column", height: "100%", minHeight: 0 },
  // A second strip under the column's own, and the same rules apply: no border
  // under it, smaller type, and it scrolls sideways rather than wrapping. A
  // wrapping strip changes the height of the chrome as shells are opened, which
  // moves the terminal under it — and a terminal that reflows because a tab
  // arrived is a terminal that redraws whatever is running in it.
  list: {
    display: "flex",
    alignItems: "center",
    flexShrink: 0,
    gap: "0.25rem",
    paddingInline: "0.5rem",
    paddingBlock: "0.35rem",
    overflowX: "auto",
    scrollbarWidth: "none",
  },
  tab: {
    display: "flex",
    alignItems: "center",
    gap: "0.3rem",
    flexShrink: 0,
    padding: "0.15rem 0.45rem",
    backgroundColor: "transparent",
    borderStyle: "none",
    borderRadius: "0.25rem",
    color: colors.muted,
    fontSize: text.small,
    whiteSpace: "nowrap",
    cursor: "pointer",
    transitionProperty: "background-color, color",
    transitionDuration: "100ms",
    ":hover": { color: colors.text },
    userSelect: "none",
    WebkitUserSelect: "none",
  },
  tabOn: { backgroundColor: colors.raised, color: colors.accent },
  spacer: { flex: 1 },
  /** Ends the shell on screen. See the note at the control itself. */
  shut: {
    display: "flex",
    alignItems: "center",
    flexShrink: 0,
    padding: "0.2rem 0.3rem",
    backgroundColor: "transparent",
    borderStyle: "none",
    borderRadius: "0.25rem",
    color: colors.muted,
    cursor: "pointer",
    transitionProperty: "color",
    transitionDuration: "100ms",
    ":hover": { color: colors.warn },
  },
  // `+` is a tab-shaped control in a strip of tabs, and deliberately outside
  // the tab set — it selects nothing, so it must not join the roving tab stop
  // or the arrow keys would step onto it and Base UI would look for a panel
  // that is not there.
  add: {
    display: "flex",
    alignItems: "center",
    flexShrink: 0,
    padding: "0.2rem 0.3rem",
    backgroundColor: "transparent",
    borderStyle: "none",
    borderRadius: "0.25rem",
    color: colors.muted,
    cursor: "pointer",
    transitionProperty: "color",
    transitionDuration: "100ms",
    ":hover": { color: colors.text },
    ":disabled": { opacity: 0.4, cursor: "default" },
  },
  panel: { flex: 1, minHeight: 0, display: "flex", flexDirection: "column" },
  // The refusal, in the sentence the daemon wrote. A shell that would not open
  // is the one moment this panel has something to say.
  failure: {
    padding: `0.35rem ${space.gutter}`,
    margin: 0,
    color: colors.warn,
    fontSize: text.small,
    whiteSpace: "pre-wrap",
  },
});

export function Shell({
  project,
  workspace,
  scheme,
}: {
  readonly project: string | undefined;
  readonly workspace: string | undefined;
  readonly scheme: ColorScheme;
}) {
  const [sessions, setSessions] = useState<ReadonlyArray<SessionInfo>>([]);
  const [picked, setPicked] = useState<string | undefined>(undefined);
  const [opening, setOpening] = useState(false);
  const [failure, setFailure] = useState("");

  // Listed on mount rather than subscribed to. There is no session feed — see
  // `useSessions`, where the same decision is argued out — and this panel is
  // unmounted whenever another tab is selected, so opening it *is* the ask.
  // Nothing else on this machine opens a shell in somebody's workspace, so the
  // only writer is the button below, which re-lists for itself.
  useEffect(() => {
    let alive = true;
    const load = (): void => {
      listSessions()
        .then((listed) => {
          if (alive) {
            setSessions(listed);
          }
        })
        .catch((error: unknown) => {
          if (alive) {
            setFailure(said(error));
          }
        });
    };
    load();
    // A list is an answer, not a feed: a daemon that restarted took every
    // session's attachment with it and nothing arrives to say so.
    const stop = onReconnect(load);
    return () => {
      alive = false;
      stop();
    };
  }, []);

  const shells = shellsOf(sessions, project, workspace);
  // Derived rather than corrected by an effect. A picked shell that has gone —
  // closed here, or exited under somebody's `exit` — falls back to the first
  // rather than selecting nothing, which is what Base UI draws for a value none
  // of its tabs carry: no panel at all, which reads as the column being broken.
  const open =
    shells.find((one) => one.session.name === picked)?.session.name ?? shells[0]?.session.name;
  const showing = shells.find((one) => one.session.name === open);

  const add = (): void => {
    if (project === undefined || workspace === undefined || opening) {
      return;
    }
    setOpening(true);
    setFailure("");
    openShell(project, workspace)
      .then(async (name) => {
        // Selected before the listing arrives, so pressing `+` moves to the new
        // shell rather than leaving somebody on the old one while the daemon is
        // asked what exists. The name is the daemon's answer, so there is no
        // guess in it.
        setPicked(name);
        setSessions(await listSessions());
      })
      .catch((error: unknown) => {
        setFailure(said(error));
      })
      .finally(() => {
        setOpening(false);
      });
  };

  // Re-asks what exists. Shared by the close button and by a shell exiting
  // under somebody's `exit` or ctrl-D, because the two are the same event
  // reached by different routes and the panel's response to both is the same
  // question.
  const relist = (): void => {
    listSessions()
      .then(setSessions)
      .catch((error: unknown) => {
        setFailure(said(error));
      });
  };

  const shut = (name: string): void => {
    setFailure("");
    closeShell(name)
      .then(async () => {
        // The pane detaches because the tab goes, and the tab goes because the
        // session is no longer listed. Nothing here has to unwind the
        // attachment: the Attach stream's lifetime is the request's, so
        // unmounting the pane is what kills it.
        setSessions(await listSessions());
      })
      .catch((error: unknown) => {
        setFailure(said(error));
      });
  };

  if (project === undefined || workspace === undefined) {
    return (
      <Nothing
        mark={<TerminalWindowIcon size={22} weight="duotone" />}
        say="no workspace open"
        hint="a shell is opened in a workspace's checkout, so there has to be one"
      />
    );
  }

  return (
    <Tabs.Root
      value={open ?? ""}
      onValueChange={(value) => {
        setPicked(String(value));
      }}
      {...stylex.props(styles.column)}
    >
      <Tabs.List {...stylex.props(styles.list)}>
        {shells.map(({ session, n }) => (
          <Tabs.Tab
            key={session.name}
            value={session.name}
            title={session.name}
            {...stylex.props(typeset.control, styles.tab, session.name === open && styles.tabOn)}
          >
            shell {n}
          </Tabs.Tab>
        ))}

        <button
          type="button"
          data-nav-item
          disabled={opening}
          aria-label="open another shell"
          title="open another shell"
          onClick={add}
          {...stylex.props(styles.add)}
        >
          <PlusIcon size={13} aria-hidden />
        </button>

        <span {...stylex.props(styles.spacer)} />

        {/* ── the close is a control on the strip, not a cross on each tab ───

            Three reasons, and the first is the one that decides it. A Base UI
            tab *is* a `<button>`, so a button inside one is a button inside a
            button — invalid markup, and clicks that belong to two owners. A
            cross on every tab is also a row of things to get rid of, and one
            under the pointer on the way to the tab beside it ends a shell
            somebody was aiming past.

            So: one control, acting on the shell that is on screen, which is
            the only one where "close this" needs no explaining. Outside the
            tab set deliberately — it selects nothing, and the arrow keys
            stepping onto it would have Base UI looking for a panel that is
            not there. */}
        {showing !== undefined && (
          <button
            type="button"
            data-nav-item
            aria-label={`close shell ${String(showing.n)}`}
            title={`close shell ${String(showing.n)}`}
            onClick={() => {
              shut(showing.session.name);
            }}
            {...stylex.props(styles.shut)}
          >
            <XIcon size={12} aria-hidden />
          </button>
        )}
      </Tabs.List>

      {failure !== "" && <p {...stylex.props(styles.failure)}>{failure}</p>}

      {showing === undefined ? (
        <Nothing
          mark={<TerminalWindowIcon size={22} weight="duotone" />}
          say="no shell here yet"
          hint="+ opens one in this workspace's checkout. It is a zmx session, so it outlives the window and is still there after a reload"
        />
      ) : (
        <Tabs.Panel value={showing.session.name} {...stylex.props(styles.panel)}>
          {/* Keyed by the session, so moving between shells remounts the pane
              and re-attaches rather than writing a second session's bytes into
              the screen the first left behind. */}
          <Pane
            key={showing.session.name}
            slot="accessory"
            session={showing.session.name}
            fixture=""
            scheme={scheme}
            // ctrl-D is a close, and nothing else reports it. The strip is
            // drawn from a listing taken at mount, so a shell somebody exited
            // left a tab behind and a pane frozen on its last frame — the
            // session is `ended`, which `shellsOf` drops, but only once this
            // panel asks again. This is the ask, and it is the *only* notice:
            // there is no session feed, and the Attach stream ending is a
            // success rather than a failure.
            onEnded={relist}
          />
        </Tabs.Panel>
      )}
    </Tabs.Root>
  );
}
