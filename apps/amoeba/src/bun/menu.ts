import { ApplicationMenu } from "electrobun/bun";

// The Edit menu, which is not a menu.
//
// ── what a missing Edit menu actually costs ───────────────────────────────
//
// On macOS, cmd+V is not a key the way ctrl+j is. It is the *key equivalent* of
// a menu item, and AppKit turns it into the `paste:` action only if some menu
// item claims it. An app with no menu bar has no such item, so cmd+V arrives at
// the web view as a plain keydown and nothing pastes.
//
// This window had no menu at all, and the pane worked around it by reading the
// clipboard itself:
//
//   navigator.clipboard.readText()
//
// which WebKit gates behind a permission prompt — the small native "Paste"
// button that appears next to the cursor. A person pressing cmd+V can click it.
// Dictation cannot: Handy transcribes speech, puts the text on the clipboard
// and synthesises cmd+V, and the prompt is a wall it has no way through. The
// symptom was speaking at the terminal and getting a paste menu.
//
// The roles below map to NSResponder selectors — `undo:`, `paste:`,
// `selectAll:` — so with the item present AppKit performs a real paste and
// WebKit raises a `paste` event carrying the text. No permission, no prompt,
// and `clipboard.ts` already handles that event. See the note there about
// preferring it and falling back.
//
// ── it was never only about the pane ──────────────────────────────────────
//
// Every text field in this window had the same hole: the address bar, the
// thread composer, the diff comment box. cut, copy, paste, select-all and undo
// are supplied by the system to any focused field once the items exist, and
// none of them worked. A macOS app with no Edit menu is broken for text
// everywhere in it, not just where somebody noticed.
//
// ── what is deliberately not here ─────────────────────────────────────────
//
// No File menu, and no item claiming cmd+N. That chord is the window's own —
// it opens the new-thread composer — and a menu item claiming it would take it
// before the renderer ever saw the key. The same reasoning rules out anything
// on cmd+shift+N. A menu is a set of claims on the keyboard, and this window
// already made some.
//
// The accelerators are spelled out rather than left to the role. Nothing in the
// JS layer assigns a default one, and a Paste item with no key equivalent looks
// completely correct in the menu bar while fixing nothing at all.

export const installMenu = (): void => {
  ApplicationMenu.setApplicationMenu([
    {
      label: "amoeba",
      submenu: [
        { role: "about" },
        { type: "divider" },
        { role: "hide", accelerator: "CommandOrControl+H" },
        { role: "hideOthers" },
        { role: "showAll" },
        { type: "divider" },
        { role: "quit", accelerator: "CommandOrControl+Q" },
      ],
    },
    {
      label: "Edit",
      submenu: [
        { role: "undo", accelerator: "CommandOrControl+Z" },
        { role: "redo", accelerator: "CommandOrControl+Shift+Z" },
        { type: "divider" },
        { role: "cut", accelerator: "CommandOrControl+X" },
        { role: "copy", accelerator: "CommandOrControl+C" },
        // The one this whole file exists for.
        { role: "paste", accelerator: "CommandOrControl+V" },
        { role: "pasteAndMatchStyle", accelerator: "CommandOrControl+Shift+V" },
        { role: "selectAll", accelerator: "CommandOrControl+A" },
      ],
    },
    {
      label: "Window",
      submenu: [
        { role: "minimize", accelerator: "CommandOrControl+M" },
        { role: "zoom" },
        { type: "divider" },
        { role: "close", accelerator: "CommandOrControl+W" },
      ],
    },
  ]);
};
