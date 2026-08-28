import { Tabs } from "@base-ui/react/tabs";
import { SidebarSimpleIcon } from "@phosphor-icons/react/SidebarSimple";
import * as stylex from "@stylexjs/stylex";
import { type ReactNode, useState } from "react";
import { Diff } from "./Diff";
import { Jobs } from "./Jobs";
import { Pr } from "./Pr";
import { Tasks } from "./Tasks";
import { Web } from "./Web";
import { debugTools } from "./debug";
import type { ColorScheme } from "@awp-kit/pane";
import { rememberPanel, rememberedPanels } from "./remembered";
import { colors, space, text } from "./tokens.stylex";

// The accessory column: a set of panels, one at a time.
//
// Base UI's Tabs rather than the pair of buttons this replaced, and the reason
// is not that the buttons looked wrong. A tab strip has a keyboard contract —
// arrow keys move between tabs, Home and End jump to the ends, and the strip is
// one tab stop rather than one per panel — plus the `role`/`aria-selected`/
// `aria-controls` wiring that makes a screen reader announce it as a set of
// panels instead of as loose buttons. All of that is behaviour, none of it is
// visible, and every bit of it is what gets skipped when it is hand-rolled.
//
// Base UI ships no styles, so what it costs is exactly the markup and the
// behaviour: the appearance is still StyleX, and still this file's business.
//
// Ordered by how often a person turns to them, left to right, and the debug
// tools last because they are the ones opened when something feels wrong
// rather than on purpose.
//
//   diff   looked at continuously while working, and opens by default
//   tasks  what the agent is about to do, and the one control that changes
//          what happens next — reached for between pieces of work
//   web    reached for while reading a diff — docs, an issue, a dashboard
//   jobs   read when a job is running, which is a few seconds a day, and
//          always with the count in the status bar already saying so
//   debug  opened when something feels wrong, never on purpose
//
// Ordered by how often a person turns to them and not by how much each one
// decides. Tasks was briefly first on the second argument — it is the only
// panel here that changes what happens next — and that is the wrong axis: a
// tab strip is paid for in reaches, and the diff is looked at continuously
// while a task list is consulted between pieces of work.
//
// ── why the panels take an argument now ──────────────────────────────────
//
// Jobs and the meter are about the window: the jobs list is the daemon's, and
// the meter is this process's. The diff is about *what is on screen* — it
// diffs the workspace the open session belongs to — so it is the first panel
// here that cannot be rendered from nothing. Hence a context rather than a
// prop drilled into one entry: the next panel of this kind (a shell, a
// webview) will want the same directory, and a second signature for the same
// question is how the two drift.
//
// ── a hidden panel is unmounted, and that is a feature ──────────────────────
//
// Base UI's Tabs.Panel defaults to `keepMounted: false`, so only the selected
// panel is in the tree. The diff leans on it: opening the tab remounts the
// panel, its effects run, and the patch it shows is taken at the moment
// someone asked to see one — which is why nothing in it polls.

export interface PanelContext {
  /**
   * The pull request the open workspace is about, when its thread names one.
   *
   * Present is what makes the `PR` tab exist — see `panelsFor`. Absent is the
   * ordinary case and the tab is then not there at all, rather than sitting in
   * the strip saying "no pull request": this is the column somebody switches
   * most, and a permanently empty room in it costs a keystroke every time.
   */
  readonly pr: { readonly project: string; readonly number: number } | undefined;
  /** A directory in the open session's workspace, or nothing is open. */
  readonly dir: string | undefined;
  /**
   * The open session's workspace, when it is one of ours.
   *
   * Absent for a session awp did not make, and the diff panel is honest about
   * what that costs: the patch still renders — a directory is all jj needs —
   * and the comments do not, because a comment is filed against a workspace and
   * a foreign session has no name to file it under.
   */
  readonly project: string | undefined;
  readonly workspace: string | undefined;
  /**
   * The thread the open session belongs to, or nothing claims it.
   *
   * In the context rather than beside it because two panels want it for the
   * same reason: a preference belongs to the *work*, not to the checkout. The
   * tab strip files which panel is open under it, and the web panel files
   * which page is loaded.
   */
  readonly thread: string | undefined;
  readonly scheme: ColorScheme;
}

interface Panel {
  readonly id: string;
  readonly label: string;
  readonly render: (context: PanelContext) => ReactNode;
}

const panels: ReadonlyArray<Panel> = [
  {
    id: "diff",
    label: "diff",
    render: ({ dir, project, workspace, scheme }) => (
      <Diff dir={dir} project={project} workspace={workspace} scheme={scheme} />
    ),
  },
  {
    id: "tasks",
    label: "tasks",
    render: ({ dir, project, workspace }) => (
      <Tasks dir={dir} project={project} workspace={workspace} />
    ),
  },
  // The pair, and nothing else. The page itself is not per-workspace — a
  // default address per workspace is the obvious next thing and is deliberately
  // not guessed at, see `rememberedPages` — but a *note* about the page is typed
  // at an agent, and an agent belongs to a workspace.
  {
    id: "web",
    label: "web",
    render: ({ project, workspace, thread }) => (
      <Web project={project} workspace={workspace} thread={thread} />
    ),
  },
  { id: "jobs", label: "jobs", render: () => <Jobs /> },
  ...debugTools.map((tool) => ({ id: tool.id, label: tool.label, render: tool.render })),
];

/**
 * The panels for this context, which is the fixed list plus one.
 *
 * **First when it exists**, and that is the placement argument rather than a
 * preference: a review workspace exists *because* of a pull request, so while
 * one is open the PR is the subject and the diff is a way of reading it. The
 * label carries the number because a tab reading `pr` says nothing a person did
 * not already know, and `PR #2418` is the one thing on the strip pointing outside
 * the window.
 */
const panelsFor = (context: PanelContext): ReadonlyArray<Panel> =>
  context.pr === undefined
    ? panels
    : [
        {
          id: "pr",
          label: `PR #${String(context.pr.number)}`,
          render: ({ pr }) => <Pr project={pr?.project} number={pr?.number} />,
        },
        ...panels,
      ];

const styles = stylex.create({
  column: { display: "flex", flexDirection: "column", height: "100%", minHeight: 0 },
  // Room on all four sides, and the fourth is the one that was missing.
  //
  // The strip used to have no bottom padding at all, so whatever the selected
  // panel put first — the diff's revision row, the web panel's address bar —
  // sat directly against the tabs and read as part of them. A tab strip is a
  // control, and a control touching the thing it controls has no edge.
  // Sized to `space.titlebar` rather than to its own padding, so it lines up
  // with the corner strip and the agent's header. The three are one band of
  // chrome drawn in three pieces; a pixel of disagreement between them reads
  // as a rendering fault rather than as three columns.
  list: {
    display: "flex",
    alignItems: "center",
    flexShrink: 0,
    gap: "0.25rem",
    height: space.titlebar,
    paddingInline: "0.5rem",
    borderBottomWidth: 1,
    borderBottomStyle: "solid",
    borderBottomColor: colors.border,
  },
  spacer: { flex: 1 },
  // Sized and coloured like a tab rather than like a button, because it sits
  // in a row of tabs and anything else there reads as a different kind of
  // thing arriving from somewhere else.
  fold: {
    display: "flex",
    alignItems: "center",
    padding: "0.2rem 0.35rem",
    backgroundColor: "transparent",
    borderStyle: "none",
    borderRadius: "0.25rem",
    color: colors.muted,
    cursor: "pointer",
    transitionProperty: "color",
    transitionDuration: "100ms",
    ":hover": { color: colors.text },
  },
  /** The sidebar's glyph, turned round to point at the edge it acts on. */
  mirrored: { transform: "scaleX(-1)" },
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
    // A tab that does nothing on hover reads as a label. The window is full of
    // words; the ones that respond to a pointer have to say so before it
    // arrives, not after it clicks.
    transitionProperty: "background-color, color",
    transitionDuration: "100ms",
    ":hover": { color: colors.text },
    // A tab is a control, not prose. Without this a double-click to switch
    // tabs selects the word instead, and a drag across the strip leaves three
    // labels highlighted — which reads as the window being in a text mode it
    // is not.
    //
    // Both spellings, because WebKit is the engine this ships on and it
    // reports the unprefixed property as the empty string while honouring the
    // prefixed one. See the note in AGENTS.md: check selection by selecting.
    userSelect: "none",
    WebkitUserSelect: "none",
  },
  // The selected tab: a fill it can be read on, and the accent as the *text*
  // rather than as the fill. One of the two places in the window the accent is
  // spent — the other is the selected sidebar row's edge — and both answer the
  // same question, which is "this, here".
  tabOn: {
    backgroundColor: colors.raised,
    color: colors.accent,
  },
  // The panel scrolls, not the column and certainly not the window.
  panel: { flex: 1, minHeight: 0, overflowY: "auto" },
});

export function Accessory({ onFold, ...context }: PanelContext & { readonly onFold: () => void }) {
  // What every per-thread preference here is filed under. Not the workspace: a
  // thread holding two checkouts of the same branch is one thing somebody is
  // doing, and having the diff open in one and the browser in the other is not
  // a distinction anybody drew on purpose.
  const { thread } = context;
  // Controlled, rather than letting Base UI keep the value to itself. StyleX
  // resolves its styles at render — `stylex.props(a, on && b)` — so which tab
  // is selected has to be a value this component can read. Base UI still owns
  // the behaviour: the arrow keys, the roving tab stop and the aria wiring all
  // work the same whether or not the value is held here.
  //
  // ── one choice per thread, not one per window ────────────────────────────
  //
  // A single value for the whole window is wrong in the way that only shows up
  // once there is more than one thread: the diff is what you want while
  // reviewing one piece of work and the browser is what you want while
  // building another, and moving between them re-answered a question that had
  // already been answered for each.
  //
  // A map keyed by thread, seeded from storage once. Derived rather than
  // synchronised: an effect watching `thread` would render the previous
  // thread's panel for a frame first, which is the panel flickering as a row
  // is clicked.
  const [byThread, setByThread] = useState<Record<string, string>>(rememberedPanels);
  // The strip is no longer a constant: a workspace whose thread names a pull
  // request has one more tab, and it is the first. See `panelsFor`.
  const shown = panelsFor(context);
  const first = shown[0]?.id ?? "";
  // The empty string for a session no thread claims — one bucket they share,
  // which is the honest answer: there is no thread to tell them apart by.
  const key = thread ?? "";
  // A stored id that no longer names a tab falls back rather than selecting
  // nothing. It happens for two reasons now and the failure is the same silent,
  // total one either way — Base UI renders no panel at all for a value none of
  // its tabs carry, which reads as the column being broken:
  //
  //   the strip changed      "meter" became "debug"
  //   the strip is dynamic   a thread whose PR panel was open, then a workspace
  //                          with no pull request
  //
  // Checked against `shown` rather than `panels`, which is what makes the second
  // case work: the PR tab exists for some selections and not others.
  const stored = byThread[key];
  const open = stored !== undefined && shown.some((panel) => panel.id === stored) ? stored : first;

  return (
    <Tabs.Root
      value={open}
      onValueChange={(value) => {
        const chosen = String(value);
        setByThread((was) => ({ ...was, [key]: chosen }));
        rememberPanel(thread, chosen);
      }}
      {...stylex.props(styles.column)}
    >
      <Tabs.List {...stylex.props(styles.list)}>
        {shown.map((panel) => (
          <Tabs.Tab
            key={panel.id}
            value={panel.id}
            {...stylex.props(styles.tab, panel.id === open && styles.tabOn)}
          >
            {panel.label}
          </Tabs.Tab>
        ))}

        <span {...stylex.props(styles.spacer)} />

        {/* ── the control that folds this column, on this column ─────────────
        
            It used to be in a window-wide top bar, at the window's right edge,
            which was the right edge of *this* column only by coincidence. Now
            the strip is the column's own, so the control acts on the thing it
            is drawn in.
        
            Folded, it goes with the column — which is the hole every
            self-hosted control has. `AgentBar` renders the other half: the
            same button, in the same place on screen, once the panels are no
            longer occupying it. One control at the boundary, drawn on
            whichever side of it still exists.
        
            Deliberately outside `Tabs.List`'s tab set: it is not a tab and
            must not join the roving tab stop, or the arrow keys would step
            onto it and Base UI would try to select a panel that is not there.
            `data-nav-item` is what puts it in ctrl+j/k's reach instead. */}
        <button
          type="button"
          data-nav-item
          aria-label="hide the panels"
          title="hide the panels"
          aria-pressed
          onClick={onFold}
          {...stylex.props(styles.fold)}
        >
          <SidebarSimpleIcon size={15} aria-hidden {...stylex.props(styles.mirrored)} />
        </button>
      </Tabs.List>

      {shown.map((panel) => (
        <Tabs.Panel key={panel.id} value={panel.id} {...stylex.props(styles.panel)}>
          {panel.render(context)}
        </Tabs.Panel>
      ))}
    </Tabs.Root>
  );
}
