import type { Job } from "@awp-kit/jobs";
import type { SessionInfo, Thread } from "@awp-kit/protocol";
import { Tabs } from "@base-ui/react/tabs";
import * as stylex from "@stylexjs/stylex";
import { useState } from "react";
import { Inbox } from "./Inbox";
import { Sidebar } from "./Sidebar";
import { rememberLeft, rememberedLeft } from "./remembered";
import { colors, text } from "./tokens.stylex";
import type { Facts } from "./useFacts";

// The left column: what is running here, or what is waiting elsewhere.
//
// Two tabs, and the split between them is the split between the two kinds of
// list awp holds:
//
//   work    threads and their workspaces — this machine, these sessions
//   inbox   open pull requests — GitHub, other people, other machines
//
// ── why the inbox is here and not in the accessory column ─────────────────
//
// Because it is a list of work to *pick from*, and picking is what this column
// is for. Every row in the work tab opens something; every row in the inbox
// starts something and then opens it. The accessory column is about the thing
// already on screen — the diff of this workspace, a page beside it — so an
// inbox there would be the one panel that had nothing to do with the session in
// the middle.
//
// Base UI's Tabs rather than two buttons, the same choice the accessory column
// made and for the same reasons: arrow keys between tabs, Home and End to the
// ends, one tab stop for the strip rather than one per panel, and the
// role/aria-selected/aria-controls wiring that makes a screen reader announce
// this as a set of panels. None of that is visible and all of it is what gets
// skipped when a tab strip is hand-rolled.
//
// **A hidden panel is unmounted**, which the inbox leans on: opening the tab
// mounts it, and its hook asks the daemon then. Nothing polls, and nothing
// fetches for a tab nobody is looking at.

const styles = stylex.create({
  column: { display: "flex", flexDirection: "column", height: "100%", minHeight: 0 },
  // ── this strip is NOT part of the window's header band ──────────────────
  //
  // The other two columns each begin with one, all `space.titlebar` tall so
  // they line up: the corner strip over the sidebar, the agent's header, the
  // panels' tab strip. The sidebar's share of that band is the corner strip —
  // which holds the traffic lights and both fold controls, belongs to the
  // window rather than to this column, and never folds.
  //
  // So these tabs sit *under* it and are deliberately shorter than a header.
  // Matching `space.titlebar` here would stack two full-height bands in a
  // 260px column and make the strip that is chrome for the window and the
  // strip that switches this column's content look like the same thing.
  //
  // The border is not decoration: a control touching the thing it controls has
  // no edge, which is the same note the panels' tab strip carries.
  list: {
    display: "flex",
    alignItems: "center",
    flexShrink: 0,
    gap: "0.25rem",
    padding: "0.35rem 0.5rem",
    borderBottomWidth: 1,
    borderBottomStyle: "solid",
    borderBottomColor: colors.border,
  },
  tab: {
    padding: "0.2rem 0.55rem",
    backgroundColor: "transparent",
    borderStyle: "none",
    borderRadius: "0.25rem",
    color: colors.muted,
    font: "inherit",
    fontSize: text.small,
    fontWeight: text.medium,
    cursor: "pointer",
    transitionProperty: "background-color, color",
    transitionDuration: "100ms",
    ":hover": { color: colors.text },
    // A tab is a control, not prose: without this a double-click to switch
    // selects the word instead. Both spellings, because WebKit reports the
    // unprefixed one as empty while honouring the prefixed one.
    userSelect: "none",
    WebkitUserSelect: "none",
  },
  // The accent as the text and a fill it can be read on — one of the two places
  // in the window the accent is spent, and it answers the same question the
  // other does: this, here.
  on: { backgroundColor: colors.raised, color: colors.accent },
  panel: { flex: 1, minHeight: 0, display: "flex", flexDirection: "column" },
});

export function LeftColumn({
  sessions,
  facts,
  jobs,
  threads,
  selected,
  onSelect,
  onNew,
  onThreadsChanged,
  onOpenWorkspace,
  failure,
}: {
  readonly sessions: ReadonlyArray<SessionInfo>;
  readonly facts: Facts;
  /** Every job the window knows about, for the inbox's row progress. */
  readonly jobs: ReadonlyArray<Job>;
  readonly threads: ReadonlyArray<Thread>;
  readonly selected: string | undefined;
  readonly onSelect: (session: SessionInfo) => void;
  readonly onNew: () => void;
  readonly onThreadsChanged: () => void;
  /** Go to a workspace's agent, named rather than handed as a session: an
   * inbox row knows the pair and not which session is running. */
  readonly onOpenWorkspace: (project: string, workspace: string) => void;
  readonly failure: string | undefined;
}) {
  // Controlled, because StyleX resolves its styles at render — `stylex.props(a,
  // on && b)` — so which tab is selected has to be a value this component can
  // read. Base UI still owns the keyboard and the aria wiring.
  const [open, setOpen] = useState<string>(rememberedLeft);

  return (
    <Tabs.Root
      value={open}
      onValueChange={(value) => {
        const tab = String(value);
        setOpen(tab);
        rememberLeft(tab);
      }}
      {...stylex.props(styles.column)}
    >
      <Tabs.List {...stylex.props(styles.list)}>
        <Tabs.Tab value="work" {...stylex.props(styles.tab, open === "work" && styles.on)}>
          work
        </Tabs.Tab>
        <Tabs.Tab value="inbox" {...stylex.props(styles.tab, open === "inbox" && styles.on)}>
          inbox
        </Tabs.Tab>
      </Tabs.List>

      <Tabs.Panel value="work" {...stylex.props(styles.panel)}>
        <Sidebar
          sessions={sessions}
          facts={facts}
          threads={threads}
          selected={selected}
          onSelect={onSelect}
          onNew={onNew}
          onThreadsChanged={onThreadsChanged}
          failure={failure}
        />
      </Tabs.Panel>

      <Tabs.Panel value="inbox" {...stylex.props(styles.panel)}>
        <Inbox jobs={jobs} onOpen={onOpenWorkspace} onStarted={onThreadsChanged} />
      </Tabs.Panel>
    </Tabs.Root>
  );
}
