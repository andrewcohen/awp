import type { ColorScheme } from "@awp-kit/pane";
import type { SessionInfo } from "@awp-kit/protocol";
import { serviceKind } from "@awp-kit/protocol";
import { Tabs } from "@base-ui/react/tabs";
import { ArrowClockwiseIcon } from "@phosphor-icons/react/ArrowClockwise";
import { ArrowSquareOutIcon } from "@phosphor-icons/react/ArrowSquareOut";
import { PlayIcon } from "@phosphor-icons/react/Play";
import { PlusIcon } from "@phosphor-icons/react/Plus";
import { StopIcon } from "@phosphor-icons/react/Stop";
import { TerminalWindowIcon } from "@phosphor-icons/react/TerminalWindow";
import { XIcon } from "@phosphor-icons/react/X";
import * as stylex from "@stylexjs/stylex";
import { useEffect, useState } from "react";
import { Nothing } from "./Nothing";
import { shellsOf } from "./shells";
import { Pane } from "./Pane";
import {
  closeShell,
  listServices,
  startService,
  stopService,
  listSessions,
  onReconnect,
  openPage,
  openShell,
  said,
} from "../data/daemon";
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
  /**
   * A service, told from a shell before it is read.
   *
   * A mark rather than a colour: the strip already spends colour on which tab
   * is *open*, and a second hue there would be two things to learn in a row of
   * five items. `::before` because the name is the tab's whole content and
   * putting a glyph in the markup would put it in the accessible name too —
   * what a screen reader should hear is `dev`, which the title attribute
   * qualifies.
   */
  service: {
    "::before": {
      content: "'▸'",
      marginInlineEnd: "0.3rem",
      fontSize: text.small,
      opacity: 0.65,
    },
  },
  /**
   * The port, on the tab, quieter than the name.
   *
   * Dimmer rather than smaller: the strip is already at `text.small` and a
   * second size in a row of five items reads as two kinds of thing. What a
   * person scans for is the name; the number is what they came back for.
   */
  port: {
    marginInlineStart: "0.3rem",
    opacity: 0.7,
    fontVariantNumeric: "tabular-nums",
  },
  /**
   * A declared service that is not running.
   *
   * Dimmed rather than hidden, which is the whole reason the strip is built
   * from the declaration list instead of from the sessions: "stopped" and
   * "never declared" are different facts, and only one of them is something a
   * person can act on. The mark stays, so the row does not change shape when
   * it comes up.
   */
  tabOff: { opacity: 0.45 },
  spacer: { flex: 1 },
  /** Opens the service on screen in the web panel. Accent, not muted: it is
      the one control here that does something somebody came to the strip for. */
  open: {
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
    ":hover": { color: colors.accent },
  },
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

/** One declared service, as the daemon answers for it. */
type Service = Awaited<ReturnType<typeof listServices>>[number];

/** How often the strip re-asks. See the effect. */
const REFRESH_MS = 3000;

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
  /** Every service this checkout declares, running or not. */
  const [services, setServices] = useState<ReadonlyArray<Service>>([]);
  const [picked, setPicked] = useState<string | undefined>(undefined);
  const [opening, setOpening] = useState(false);
  /** The service a start or stop is in flight for, so it is pressed once. */
  const [busy, setBusy] = useState("");
  const [failure, setFailure] = useState("");

  // ── one poll, because nothing here is an event ───────────────────────────
  //
  // Three things this strip draws change with nothing to announce them: a
  // session started from somewhere else, a service that stopped, and the port
  // a server binds seconds after its session starts. There is no session feed
  // — see `useSessions` — and a port is not a change anybody can push.
  //
  // So it asks on a timer, and the timer's cost is bounded by the thing that
  // used to be the argument against it: Base UI unmounts a hidden tab, so this
  // runs only while somebody is looking at it. The earlier version stopped
  // once every port was known, which was cheaper and wrong — a service started
  // by the agent while the panel was open never appeared, and tabbing away and
  // back was the only way to see it.
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
      if (project === undefined || workspace === undefined) {
        return;
      }
      listServices(project, workspace)
        .then((found) => {
          if (alive) {
            setServices(found);
          }
        })
        .catch(() => {
          // Silent, and deliberately not the same as the listing above. A
          // checkout with no `.awp/config.json` is the ordinary case, and the
          // column's one failure line is for something somebody can act on.
        });
    };
    load();
    const timer = setInterval(load, REFRESH_MS);
    // A list is an answer, not a feed: a daemon that restarted took every
    // session's attachment with it and nothing arrives to say so.
    const stop = onReconnect(load);
    return () => {
      alive = false;
      clearInterval(timer);
      stop();
    };
  }, [project, workspace]);

  const shells = shellsOf(sessions, project, workspace);
  // ── the strip is the declaration, not the session list ───────────────────
  //
  // A service that is up is a session and could be found among them, which is
  // how this started. It cannot tell a service that stopped from one that was
  // never declared, and those are the two states where somebody wants the
  // strip most: the second is nothing to say, the first is a thing to press.
  const declared = [...services].toSorted((a, b) => a.name.localeCompare(b.name));
  // A service's tab is named for the service and not for its session, because
  // a stopped one has no session and the value has to survive it starting.
  // `serviceKind` is the daemon's own spelling of the same thing, so the two
  // cannot drift apart.
  const tabs = [
    ...shells.map((one) => ({ value: one.session.name, session: one.session.name })),
    ...declared.map((one) => ({
      value: serviceKind(one.name),
      session: one.running ? one.session : undefined,
    })),
  ];
  // Derived rather than corrected by an effect. A picked tab that has gone —
  // a shell closed here, or exited under somebody's `exit` — falls back to the
  // first rather than selecting nothing, which is what Base UI draws for a
  // value none of its tabs carry: no panel at all, which reads as the column
  // being broken.
  const open = tabs.find((one) => one.value === picked)?.value ?? tabs[0]?.value;
  // What the pane attaches to, which is a service's session as readily as a
  // shell's: attaching is how a dev server's log gets tailed. Undefined for a
  // service that is declared and down, which is a panel with something to say
  // rather than a terminal.
  const showing = tabs.find((one) => one.value === open)?.session;
  // Three questions the tab cannot answer alone, and each control reads one:
  // `closable` is shells only — a service is *stopped*, a different call
  // meaning a different thing — and `onService` is the other side of that.
  const closable = shells.find((one) => one.session.name === open);
  const onService = declared.find((one) => serviceKind(one.name) === open);
  const from = sessions.find((one) => one.name === onService?.session)?.startDir;
  const openable =
    onService?.port === undefined || from === undefined
      ? undefined
      : { from, url: `http://localhost:${String(onService.port)}` };

  // Re-asks what exists. Shared by every control here and by a shell exiting
  // under somebody's `exit` or ctrl-D, because those are the same event reached
  // by different routes and the panel's response to all of them is the same
  // question.
  const relist = (): void => {
    listSessions()
      .then(setSessions)
      .catch((error: unknown) => {
        setFailure(said(error));
      });
    if (project !== undefined && workspace !== undefined) {
      listServices(project, workspace)
        .then(setServices)
        .catch(() => {
          // See the effect.
        });
    }
  };

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

  // ── start, stop and restart are one shape ────────────────────────────────
  //
  // Each is a call naming a declared service and then the same question — what
  // exists now — because none of them is announced: stopping a service ends a
  // session nothing feeds back, and starting one binds a port some seconds
  // later. Restart is the pair in order rather than a third call, so there is
  // one place where "stopped, then started" is what it means.
  //
  // The refusal is the daemon's sentence, in the column's one failure line. A
  // name this checkout does not declare is the case it is written for, and it
  // names what the checkout *does* declare.
  const act = (name: string, work: (project: string, workspace: string) => Promise<unknown>) => {
    if (project === undefined || workspace === undefined || busy !== "") {
      return;
    }
    setBusy(name);
    setFailure("");
    work(project, workspace)
      .then(() => {
        // Selected before the listing arrives, the same as `+`: pressing start
        // is asking to watch the thing start.
        setPicked(serviceKind(name));
        relist();
      })
      .catch((error: unknown) => {
        setFailure(said(error));
      })
      .finally(() => {
        setBusy("");
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

        {/* After the shells and before `+`, so the control that adds one stays
            beside the things it adds to. A service is not opened from here —
            it is declared in `.awp/config.json` and started by name, which is
            what keeps the set of commands this window can run to the set
            somebody wrote down. The controls on the right start and stop the
            one on screen; neither of them can name anything else. */}
        {declared.map((one) => {
          const value = serviceKind(one.name);
          return (
            <Tabs.Tab
              key={value}
              value={value}
              title={
                one.running
                  ? `${one.name} — running${one.port === undefined ? ", no port yet" : ` on ${String(one.port)}`}`
                  : `${one.name} — stopped. ${one.command}`
              }
              {...stylex.props(
                typeset.control,
                styles.tab,
                styles.service,
                !one.running && styles.tabOff,
                value === open && styles.tabOn,
              )}
            >
              {one.name}
              {/* Text, not a link, and the control that opens it is on the
                  strip — a Base UI tab *is* a `<button>`, which is the same
                  argument that put the close out here rather than on each tab.
                  Absent rather than a placeholder while a server is still
                  binding: a number that appears is read as news, where `:—`
                  turning into `:5273` is a thing to notice twice. */}
              {one.port !== undefined && (
                <span {...stylex.props(styles.port)}>:{String(one.port)}</span>
              )}
            </Tabs.Tab>
          );
        })}

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

        {/* ── one set of controls, acting on what is on screen ──────────────

            Every control here is outside the tab set and acts on the open tab,
            for the reason the close was: a Base UI tab *is* a `<button>`, so a
            button inside one is a button inside a button — invalid markup and
            clicks with two owners. A row of them on every tab is also a row of
            things to press by accident on the way to the tab beside it, and
            two of these stop a server somebody else is watching.

            Never more than two at once: a service that is up offers restart
            and stop, one that is down offers start. */}
        {onService !== undefined && !onService.running && (
          <button
            type="button"
            data-nav-item
            disabled={busy !== ""}
            aria-label={`start ${onService.name}`}
            title={`start ${onService.name} — ${onService.command}`}
            onClick={() => {
              act(onService.name, (p, w) => startService(p, w, onService.name));
            }}
            {...stylex.props(styles.open)}
          >
            <PlayIcon size={12} weight="fill" aria-hidden />
          </button>
        )}

        {onService?.running === true && (
          <>
            <button
              type="button"
              data-nav-item
              disabled={busy !== ""}
              aria-label={`restart ${onService.name}`}
              title={`restart ${onService.name}`}
              onClick={() => {
                act(onService.name, async (p, w) => {
                  await stopService(p, w, onService.name);
                  return startService(p, w, onService.name);
                });
              }}
              {...stylex.props(styles.add)}
            >
              <ArrowClockwiseIcon size={12} aria-hidden />
            </button>
            <button
              type="button"
              data-nav-item
              disabled={busy !== ""}
              aria-label={`stop ${onService.name}`}
              title={`stop ${onService.name}`}
              onClick={() => {
                act(onService.name, (p, w) => stopService(p, w, onService.name));
              }}
              {...stylex.props(styles.shut)}
            >
              <StopIcon size={12} weight="fill" aria-hidden />
            </button>
          </>
        )}

        {/* Through `openPage` rather than into the panel's own state, because
            the other subscribers are the point — a second window on this
            thread, and the agent, are looking at the same page, and the daemon
            is the only place that can tell all of them. `startDir` is the
            session's own directory, which is the workspace's checkout: the
            daemon resolves the thread from it, the same binding every
            agent-facing call uses. */}
        {openable !== undefined && (
          <button
            type="button"
            data-nav-item
            aria-label={`open ${openable.url} in the web panel`}
            title={`open ${openable.url}`}
            onClick={() => {
              openPage(openable.from, openable.url).catch((error: unknown) => {
                setFailure(said(error));
              });
            }}
            {...stylex.props(styles.open)}
          >
            <ArrowSquareOutIcon size={12} aria-hidden />
          </button>
        )}

        {closable !== undefined && (
          <button
            type="button"
            data-nav-item
            aria-label={`close shell ${String(closable.n)}`}
            title={`close shell ${String(closable.n)}`}
            onClick={() => {
              shut(closable.session.name);
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
          say={onService === undefined ? "no shell here yet" : `${onService.name} is not running`}
          hint={
            onService === undefined
              ? "+ opens one in this workspace's checkout. It is a zmx session, so it outlives the window and is still there after a reload"
              : `${onService.command} — start it with the control on the strip. It runs in a session of its own, so it outlives this window and the agent both`
          }
        />
      ) : (
        <Tabs.Panel value={open ?? ""} {...stylex.props(styles.panel)}>
          {/* Keyed by the session, so moving between shells remounts the pane
              and re-attaches rather than writing a second session's bytes into
              the screen the first left behind. */}
          <Pane
            key={showing}
            slot="accessory"
            session={showing}
            fixture=""
            scheme={scheme}
            // ctrl-D is a close, and nothing else reports it. A shell somebody
            // exited leaves a tab behind and a pane frozen on its last frame —
            // the session is `ended`, which `shellsOf` drops, but only once
            // this panel asks again. This is the notice: the Attach stream
            // ending is a success rather than a failure, so nothing else reads
            // it as one.
            onEnded={relist}
          />
        </Tabs.Panel>
      )}
    </Tabs.Root>
  );
}
