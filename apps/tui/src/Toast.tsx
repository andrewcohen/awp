// The notice itself: bottom right, over whatever is there, and gone again.
//
// Absolutely positioned rather than a row in the layout, because a notice
// that took a line would move the transcript every time it appeared — and
// what it is announcing is usually a selection somebody is still looking at.

import { useEffect, useState } from "react";
import { CHROME } from "./theme";
import { onNotice } from "./notices";

/** Long enough to read four words, short enough not to be furniture. */
const SHOWN_MS = 1600;

export const Toast = () => {
  const [said, setSaid] = useState("");

  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const stop = onNotice((text) => {
      setSaid(text);
      // Restarted rather than queued: a second copy while the first notice
      // is up is one gesture superseding another, not two things to read.
      if (timer !== undefined) clearTimeout(timer);
      timer = setTimeout(() => setSaid(""), SHOWN_MS);
    });
    return () => {
      stop();
      if (timer !== undefined) clearTimeout(timer);
    };
  }, []);

  if (said === "") return undefined;
  return (
    <box
      position="absolute"
      right={2}
      bottom={2}
      height={1}
      paddingLeft={1}
      paddingRight={1}
      backgroundColor={CHROME.bar}
    >
      <text fg={CHROME.text} wrapMode="none" content={said} />
    </box>
  );
};
