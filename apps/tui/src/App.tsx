// Three screens and one way between them.
//
//   threads ──enter──▶ chat        ctrl-\ back
//           ──s──────▶ terminal    ctrl-\ back
//
// A screen at a time rather than columns: this is a POC of the two halves —
// the conversation and the pty — and putting them side by side in an 80-column
// terminal would prove neither.

import { useState } from "react";
import { Chat } from "./Chat";
import { type Place, Threads } from "./Threads";
import { Term } from "./Term";
import { Toast } from "./Toast";

type View =
  | { readonly kind: "threads" }
  | { readonly kind: "chat"; readonly place: Place }
  | { readonly kind: "terminal"; readonly place: Place };

export const App = ({ onQuit }: { onQuit: () => void }) => {
  const [view, setView] = useState<View>({ kind: "threads" });
  // Held here rather than in the list, because the list unmounts when a screen
  // opens over it — coming back put the cursor on the first row every time,
  // which is the wrong row for anybody who navigated away from it.
  const [at, setAt] = useState(0);
  const back = () => setView({ kind: "threads" });

  // Keyed by the checkout, so opening another one is another component with
  // its own empty state rather than a reset inside an effect.
  const screen =
    view.kind === "chat" ? (
      <Chat
        key={`${view.place.project}/${view.place.workspace}`}
        place={view.place}
        onBack={back}
        onQuit={onQuit}
      />
    ) : view.kind === "terminal" ? (
      <Term place={view.place} onBack={back} onQuit={onQuit} />
    ) : (
      <Threads
        at={at}
        onMove={setAt}
        onOpen={(place) => setView({ kind: "chat", place })}
        onTerminal={(place) => setView({ kind: "terminal", place })}
        onQuit={onQuit}
      />
    );

  // ── the toast sits above every screen ─────────────────────────────────
  //
  // Here rather than inside one of them, because what it announces belongs
  // to none: a copy is a gesture over whatever is on screen, and the three
  // screens replace each other. It is absolutely positioned, so this box
  // costs the layout nothing.
  return (
    <box flexGrow={1} flexDirection="column">
      {screen}
      <Toast />
    </box>
  );
};
