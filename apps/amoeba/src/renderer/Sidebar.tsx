import type { SessionInfo } from "@awp-kit/protocol";
import type { Chrome } from "@awp-kit/pane";

// The list of sessions, and which of them can be opened.
//
// A row that cannot be attached to says why, and the sentence comes from the
// daemon rather than from here. Only the daemon can know that one of these is
// the session awp itself is running in, and re-deriving the rest would be a
// second copy of a rule — the copy that drifts is always the one nobody tests.

const shortName = (name: string): string => {
  // `awp.project.workspace.kind` is an address, not a label, and the project
  // repeats on every row. What distinguishes them is the middle.
  const parts = name.split(".");
  return parts.length >= 4 ? parts.slice(1, -1).join(".") : name;
};

export function Sidebar({
  sessions,
  selected,
  onSelect,
  chrome,
  failure,
}: {
  readonly sessions: ReadonlyArray<SessionInfo>;
  readonly selected: string | undefined;
  readonly onSelect: (session: SessionInfo) => void;
  readonly chrome: Chrome;
  readonly failure: string | undefined;
}) {
  if (failure !== undefined) {
    return (
      <div style={{ padding: "3rem 1rem 1rem", color: chrome.muted, lineHeight: 1.6 }}>
        <div style={{ marginBottom: "0.75rem" }}>no daemon</div>
        <div style={{ fontSize: 11, opacity: 0.8 }}>{failure}</div>
        <div style={{ fontSize: 11, opacity: 0.8, marginTop: "0.75rem" }}>
          start it with <code>bun run daemon</code>
        </div>
      </div>
    );
  }

  return (
    <div style={{ padding: "2.5rem 0 1rem", height: "100%", overflowY: "auto" }}>
      {sessions.length === 0 && (
        <div style={{ padding: "0.5rem 1rem", color: chrome.muted }}>no sessions</div>
      )}
      {sessions.map((session) => {
        const open = session.refusal === undefined;
        const active = session.name === selected;
        return (
          <button
            key={session.name}
            type="button"
            disabled={!open}
            // The reason is the tooltip as well as the subtitle. A row that
            // will not say why it is disabled is worse than no row at all.
            title={session.refusal ?? session.cmd}
            onClick={() => onSelect(session)}
            style={{
              display: "block",
              width: "100%",
              textAlign: "left",
              padding: "0.35rem 1rem",
              border: "none",
              background: active ? chrome.border : "transparent",
              color: open ? chrome.text : chrome.muted,
              font: "inherit",
              cursor: open ? "pointer" : "default",
              opacity: open ? 1 : 0.55,
            }}
          >
            <div style={{ display: "flex", gap: "0.5rem", alignItems: "baseline" }}>
              <span
                aria-hidden
                style={{ color: session.ended ? chrome.muted : "#a6da95", fontSize: 9 }}
              >
                ●
              </span>
              <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                {shortName(session.name)}
              </span>
            </div>
            {session.refusal !== undefined && (
              <div
                style={{
                  fontSize: 10,
                  color: chrome.muted,
                  paddingLeft: "1rem",
                  whiteSpace: "normal",
                  lineHeight: 1.4,
                }}
              >
                {session.refusal}
              </div>
            )}
          </button>
        );
      })}
    </div>
  );
}
