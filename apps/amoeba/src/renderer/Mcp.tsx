import { Dialog } from "@base-ui/react/dialog";
import type { McpStatus } from "@awp-kit/protocol";
import * as stylex from "@stylexjs/stylex";
import { useEffect, useState } from "react";
import { mcpStatus, said } from "./daemon";
import { useOverlay } from "./overlays";
import { typeset } from "./typeset";
import { colors, lift, text, timing } from "./tokens.stylex";

// `/mcp`: what the agent on the other end of this conversation was handed.
//
// ── the honest half, said as the honest half ──────────────────────────────
//
// The daemon hands every conversation an MCP server on `session/new`,
// `session/load` and `session/fork` — no file on disk, no edit to anybody's
// config — so what it knows for certain is *what it handed over*. Whether the
// agent's own MCP client accepted the handshake and listed the tools is not
// something ACP reports back, and there is no call that asks.
//
// So this panel says which half it is showing, in a sentence, rather than
// drawing a green tick that would be a guess. A status light that cannot be
// wrong is worth less than one that says what it does not know: the failure
// this would hide — an agent that never connected — looks exactly like an
// agent that connected and was never asked to use a tool.
//
// ── the two fields worth having on screen ────────────────────────────────
//
//   cwd   the whole of the server's scope. No tool takes a workspace
//         argument, so this path is *why* a conversation cannot reach another
//         checkout — the sentence "what can this agent touch", as a value
//   url   which daemon the spawned server talks to. A second instance's
//         agents reaching the instance somebody is working in is a real
//         failure with nothing else on screen to show it

const styles = stylex.create({
  backdrop: { position: "fixed", inset: 0, backgroundColor: "rgba(0, 0, 0, 0.4)" },
  popup: {
    // ── it arrives, rather than being there ─────────────────────────────
    //
    // Nothing pops: the window's mandate, and a dialog is the largest
    // thing in it that appears. Scale from just under, so it reads as
    // coming forward rather than as growing — and the transform is
    // composed with the centring translate, which is why the keyframe
    // carries both.
    animationName: stylex.keyframes({
      from: { opacity: 0, transform: "translate(-50%, -50%) scale(0.97)" },
      to: { opacity: 1, transform: "translate(-50%, -50%) scale(1)" },
    }),
    animationDuration: { default: timing.enter, "@media (prefers-reduced-motion: reduce)": "0s" },
    animationTimingFunction: timing.spring,
    position: "fixed",
    top: "38%",
    left: "50%",
    transform: "translate(-50%, -50%)",
    display: "flex",
    flexDirection: "column",
    gap: "0.75rem",
    width: "min(40rem, calc(100vw - 6rem))",
    // The dialog is a thing to read rather than a panel, and it is capped so a
    // long tool list scrolls inside it instead of growing past the window.
    maxHeight: "min(34rem, calc(100vh - 8rem))",
    padding: "1rem",
    backgroundColor: colors.surface,
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.4rem",
    color: colors.text,
    boxShadow: lift.high,
  },
  head: { display: "flex", alignItems: "baseline", gap: "0.5rem" },
  title: { margin: 0 },
  // `flex: 1` with `minWidth: 0`. The pair, so the close button stays on the row.
  spacer: { flex: 1, minWidth: 0 },
  close: {
    flexShrink: 0,
    padding: "0.15rem 0.5rem",
    backgroundColor: { default: "transparent", ":hover": colors.border },
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.2rem",
    color: colors.muted,
    font: "inherit",
    fontSize: text.small,
    cursor: "pointer",
  },
  note: { margin: 0, color: colors.muted, fontSize: text.small },
  scroll: { minHeight: 0, overflowY: "auto", overflowX: "hidden" },
  facts: {
    display: "grid",
    // A label column that fits the longest of four short words, and the value
    // taking the rest. `minmax(0, 1fr)` rather than `1fr`, or a long command
    // line makes the grid wider than the dialog.
    gridTemplateColumns: "5rem minmax(0, 1fr)",
    gap: "0.2rem 0.6rem",
    alignItems: "baseline",
  },
  label: { color: colors.muted, fontSize: text.small },
  // A path, a command and a url are all addresses, so they are monospace — and
  // they wrap rather than clipping, because the whole value is the point here.
  value: {
    minWidth: 0,
    overflowWrap: "anywhere",
  },
  tools: { display: "flex", flexDirection: "column", gap: "0.4rem" },
  tool: { display: "flex", flexDirection: "column", gap: "0.1rem" },
  says: { color: colors.muted, fontSize: text.small },
  bad: { color: colors.warn, fontSize: text.small },
});

/**
 * @param project  the workspace whose conversation this is about. The status is
 *                 the same for every conversation in a workspace, because the
 *                 server is bound to the workspace and nothing else.
 */
export function Mcp({
  project,
  workspace,
  onClose,
}: {
  readonly project: string;
  readonly workspace: string;
  readonly onClose: () => void;
}) {
  // Announced rather than detected: the web panel is a native view drawn over
  // everything this window renders, and it cannot see a portal outside its own
  // subtree. The component that knows it opened a modal is this one.
  useOverlay(true);

  const [status, setStatus] = useState<McpStatus | undefined>(undefined);
  const [failure, setFailure] = useState<string | undefined>(undefined);

  useEffect(() => {
    let gone = false;
    void mcpStatus(project, workspace)
      .then((found) => {
        if (!gone) {
          setStatus(found);
        }
      })
      .catch((error: unknown) => {
        if (!gone) {
          // `said`, not `String(error)`: every refusal in the contract is a
          // tagged error carrying `reason` and setting no `message`, so the
          // obvious rendering is the tag and nothing else.
          setFailure(said(error));
        }
      });
    return () => {
      gone = true;
    };
  }, [project, workspace]);

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
        <Dialog.Popup {...stylex.props(typeset.prose, styles.popup)}>
          <div {...stylex.props(styles.head)}>
            <Dialog.Title {...stylex.props(typeset.heading, styles.title)}>mcp</Dialog.Title>
            <span {...stylex.props(styles.says)}>
              {project}/{workspace}
            </span>
            <span {...stylex.props(styles.spacer)} />
            <Dialog.Close {...stylex.props(styles.close)}>close</Dialog.Close>
          </div>

          <p {...stylex.props(styles.note)}>
            Handed to this conversation on every open — no file on disk, and no edit to your config.
            Whether the agent&apos;s own client accepted it is not something the adapter reports, so
            what is below is what was passed, not what was acknowledged.
          </p>

          {failure !== undefined && <p {...stylex.props(styles.bad)}>{failure}</p>}

          {status === undefined ? (
            failure === undefined ? (
              // A word, not a spinner — the panel's rule, and the call is one
              // round trip with no disk in it.
              <p {...stylex.props(styles.note)}>reading…</p>
            ) : undefined
          ) : (
            <div {...stylex.props(styles.scroll)}>
              <div {...stylex.props(styles.facts)}>
                <span {...stylex.props(styles.label)}>server</span>
                <span {...stylex.props(typeset.address, styles.value)}>{status.name}</span>
                <span {...stylex.props(styles.label)}>bound to</span>
                <span {...stylex.props(typeset.address, styles.value)}>{status.cwd}</span>
                <span {...stylex.props(styles.label)}>daemon</span>
                <span {...stylex.props(typeset.address, styles.value)}>{status.url}</span>
                <span {...stylex.props(styles.label)}>started as</span>
                <span {...stylex.props(typeset.address, styles.value)}>
                  {[status.command, ...status.args].join(" ")}
                </span>
              </div>

              <p {...stylex.props(styles.note)}>
                {status.tools.length} tool{status.tools.length === 1 ? "" : "s"}, every one scoped
                to the directory above — none of them takes a workspace argument, so this
                conversation has no way to reach another checkout.
              </p>

              <div {...stylex.props(styles.tools)}>
                {status.tools.map((tool) => (
                  <div key={tool.name} {...stylex.props(styles.tool)}>
                    <span {...stylex.props(typeset.address)}>{tool.name}</span>
                    <span {...stylex.props(styles.says)}>{tool.description}</span>
                  </div>
                ))}
              </div>
            </div>
          )}
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
