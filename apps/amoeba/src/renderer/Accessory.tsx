import { Tabs } from "@base-ui/react/tabs";
import * as stylex from "@stylexjs/stylex";
import { type ReactNode, useState } from "react";
import { Diff } from "./Diff";
import { Jobs } from "./Jobs";
import { Web } from "./Web";
import { debugTools } from "./debug";
import type { ColorScheme } from "@awp-kit/pane";
import { colors, text } from "./tokens.stylex";

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
// The diff comes first and opens by default, jobs after it, the debug tools
// last. All three are "opened on purpose" panels except the meter, which is
// the one opened when something feels wrong — but only one of them is looked
// at continuously while working, and that is the diff. Jobs is read when a job
// is running, which is a few seconds a day and always with the jobs count in
// the status bar already saying so.
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
  { id: "jobs", label: "jobs", render: () => <Jobs /> },
  // Takes no context, for now. A default address per workspace is the obvious
  // next thing and is deliberately not guessed at — see `rememberedPage`.
  { id: "web", label: "web", render: () => <Web /> },
  ...debugTools.map((tool) => ({ id: tool.id, label: tool.label, render: tool.render })),
];

const styles = stylex.create({
  column: { display: "flex", flexDirection: "column", height: "100%", minHeight: 0 },
  list: {
    display: "flex",
    flexShrink: 0,
    gap: "0.25rem",
    padding: "0.3rem 0.5rem 0",
  },
  tab: {
    padding: "0.2rem 0.5rem",
    backgroundColor: "transparent",
    borderStyle: "none",
    borderRadius: "0.2rem",
    color: colors.muted,
    font: "inherit",
    fontSize: text.small,
    cursor: "pointer",
  },
  tabOn: {
    backgroundColor: colors.border,
    color: colors.text,
  },
  // The panel scrolls, not the column and certainly not the window.
  panel: { flex: 1, minHeight: 0, overflowY: "auto" },
});

export function Accessory(context: PanelContext) {
  // Controlled, rather than letting Base UI keep the value to itself. StyleX
  // resolves its styles at render — `stylex.props(a, on && b)` — so which tab
  // is selected has to be a value this component can read. Base UI still owns
  // the behaviour: the arrow keys, the roving tab stop and the aria wiring all
  // work the same whether or not the value is held here.
  const [open, setOpen] = useState<string>(panels[0]?.id ?? "");

  return (
    <Tabs.Root
      value={open}
      onValueChange={(value) => setOpen(String(value))}
      {...stylex.props(styles.column)}
    >
      <Tabs.List {...stylex.props(styles.list)}>
        {panels.map((panel) => (
          <Tabs.Tab
            key={panel.id}
            value={panel.id}
            {...stylex.props(styles.tab, panel.id === open && styles.tabOn)}
          >
            {panel.label}
          </Tabs.Tab>
        ))}
      </Tabs.List>

      {panels.map((panel) => (
        <Tabs.Panel key={panel.id} value={panel.id} {...stylex.props(styles.panel)}>
          {panel.render(context)}
        </Tabs.Panel>
      ))}
    </Tabs.Root>
  );
}
