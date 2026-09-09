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
  if (view.kind === "chat") {
    return (
      <Chat
        key={`${view.place.project}/${view.place.workspace}`}
        place={view.place}
        onBack={back}
        onQuit={onQuit}
      />
    );
  }
  if (view.kind === "terminal") return <Term place={view.place} onBack={back} onQuit={onQuit} />;
  return (
    <Threads
      at={at}
      onMove={setAt}
      onOpen={(place) => setView({ kind: "chat", place })}
      onTerminal={(place) => setView({ kind: "terminal", place })}
      onQuit={onQuit}
    />
  );
};
