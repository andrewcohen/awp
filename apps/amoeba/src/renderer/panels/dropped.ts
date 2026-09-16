// Dropping a file on a text box writes down where it is.

/**
 * The absolute paths behind a drag, in the order they were dropped.
 *
 * Empty in a plain browser and empty for a drag that carries no files — a
 * selection from another page, an image dragged out of a website. The renderer
 * cannot find a path on its own, so this is the bridge's answer or nothing:
 * see `pathForFile` in the preload for why the page is not allowed to know.
 */
export const droppedPaths = (transfer: DataTransfer | null): ReadonlyArray<string> => {
  const forFile = typeof window === "undefined" ? undefined : window.awpHost?.pathForFile;
  if (forFile === undefined || transfer === null) {
    return [];
  }
  return [...transfer.files].map((file) => forFile(file)).filter((path) => path !== "");
};

/**
 * `text` with `insert` put in at the selection, and where the caret goes after.
 *
 * Separated by a space from whatever it lands against, on both sides — two
 * paths run together are one string nothing can take apart, and a path against
 * the end of a sentence is a word that word is not. Nothing is added where
 * there is already a space, or at an end of the text.
 *
 * The path goes in **raw**, spaces and all. Quoting it was the first answer and
 * is a shell's spelling of a path: what reads this is a person or a model, and
 * a sentence with `'/Users/…/Screen Shots/b.png'` in it has punctuation in it
 * that belongs to a language nobody here is writing.
 */
export const spliced = (
  text: string,
  insert: string,
  from: number,
  to: number,
): { readonly text: string; readonly caret: number } => {
  const before = text.slice(0, from);
  const after = text.slice(to);
  const lead = before === "" || /\s$/u.test(before) ? "" : " ";
  const trail = after === "" || /^\s/u.test(after) ? "" : " ";
  const middle = `${lead}${insert}${trail}`;
  return {
    text: `${before}${middle}${after}`,
    caret: before.length + middle.length - trail.length,
  };
};

/**
 * The handlers a text box needs to accept a file as its path.
 *
 * ── why `onDragOver` is not optional ──────────────────────────────────────
 *
 * A drop target is one that *cancels* dragover. Without it the browser never
 * fires `drop` at all and the default takes over — which in Chromium is
 * navigating the window to the file, replacing the renderer with a picture of
 * somebody's screenshot and no way back but a reload. So the cancel is what
 * makes this safe as much as what makes it work, and the drop is cancelled
 * whether or not a path came back for the same reason.
 *
 * The caret is put back after the value has been applied, which is a frame
 * later: React owns the box, so setting `selectionStart` in the handler would
 * be setting it on text that is about to be replaced.
 */
export const acceptsFiles = (
  value: string,
  onValue: (next: string) => void,
): {
  readonly onDragOver: (event: React.DragEvent<HTMLTextAreaElement>) => void;
  readonly onDrop: (event: React.DragEvent<HTMLTextAreaElement>) => void;
} => ({
  onDragOver: (event) => {
    event.preventDefault();
  },
  onDrop: (event) => {
    event.preventDefault();
    const paths = droppedPaths(event.dataTransfer);
    if (paths.length === 0) {
      return;
    }
    const box = event.currentTarget;
    // The caret is where the pointer let go, which the browser has already
    // moved for us — and the end of the text when the box was never focused.
    const from = box.selectionStart ?? value.length;
    const to = box.selectionEnd ?? from;
    const { text, caret } = spliced(value, paths.join(" "), from, to);
    onValue(text);
    requestAnimationFrame(() => {
      box.focus();
      box.setSelectionRange(caret, caret);
    });
  },
});

/**
 * The handlers a whole panel needs to accept a file on behalf of its composer.
 *
 * ── a drop target the size of the thing somebody is looking at ────────────
 *
 * {@link acceptsFiles} puts these on the textarea, which makes the box the
 * only place a file may be let go of — and the box is one line at the bottom
 * of a column somebody is reading. Asked for in those words: the whole pane
 * should take a drop, and it should not matter whether the composer is
 * focused.
 *
 * The composer keeps its own handlers as well, and that is not redundant: a
 * drop **on** the box has a caret to land at, which is the one thing this
 * cannot know. Both see the same event, because the box's handler does not
 * stop propagation — so this one asks whether the drop was already taken and
 * does nothing when it was. Without that the path is spliced twice, once at
 * the caret and once at the end.
 *
 * ── it answers where the caret should go, and does not put it there ───────
 *
 * Focusing means reading a ref, and a ref may not be read while rendering —
 * which is what building these handlers is. So the caret comes back and the
 * caller moves it from inside its own event handler, where the rule allows
 * it. That also keeps this file free of the DOM it would otherwise have to
 * hold on to across a frame.
 *
 * `undefined` for a drop that carried nothing usable — which is every drop in
 * a plain browser, where the bridge is absent. It is cancelled all the same:
 * the alternative is Chromium navigating the window to the file, replacing
 * the renderer with a picture of somebody's screenshot and no way back but a
 * reload. That is the failure `main.tsx` cancels at `window` for.
 */
export const acceptsFilesInto = (
  value: string,
  onValue: (next: string) => void,
): {
  readonly onDragOver: (event: React.DragEvent<HTMLElement>) => void;
  readonly onDrop: (event: React.DragEvent<HTMLElement>) => number | undefined;
} => ({
  onDragOver: (event) => {
    event.preventDefault();
  },
  onDrop: (event) => {
    // Already handled by the textarea's own, which had a caret to use.
    if (event.defaultPrevented) {
      return undefined;
    }
    event.preventDefault();
    const paths = droppedPaths(event.dataTransfer);
    if (paths.length === 0) {
      return undefined;
    }
    // No caret, because the pointer was let go somewhere that has none. The
    // end of the draft is the honest place for it — and the caller focuses
    // afterwards, so what somebody does next is type.
    const at = value.length;
    const { text, caret } = spliced(value, paths.join(" "), at, at);
    onValue(text);
    return caret;
  },
});
