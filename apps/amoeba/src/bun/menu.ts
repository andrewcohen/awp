import { ApplicationMenu } from "electrobun/bun";
import type { BrowserWindow } from "electrobun/bun";

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
// ── the View menu, and the bug it exists for ──────────────────────────────
//
// Docking Safari's Web Inspector against this window leaves the app short —
// and *stays* short after the inspector is closed again. The window is
// `hiddenInset` and its web view is created with `autoResize`, so the view is
// supposed to follow its container; the inspector docks by taking a share of
// that container, and on undock the view does not take the share back.
//
// Nothing in the renderer can repair that. `html`, `body` and `#root` are all
// `height: 100%`, which is correct and is exactly why: the page is faithfully
// filling a view that is the wrong size. The size is native, so the repair has
// to be.
//
// **`fit` nudges the window by a pixel and back.** AppKit re-lays-out on a
// frame *change*, so re-applying the frame it already has does nothing — which
// was the first version, and it did nothing very convincingly. One pixel is
// enough to make the change real and small enough that nobody sees it.
//
// It is a menu item rather than something automatic because there is no event
// to hang it on: the inspector docking is not something this process is told
// about. A person who has just closed the inspector knows, and ⌥⌘R is there.
//
// `Reload` is in the same menu and is not for this — a reload re-runs the
// renderer against the same wrong view and stays short. It is there because a
// window with no reload is a window that has to be relaunched to pick up a
// change, which in development is most of the time.
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

/**
 * Make the web view take the window's size again, and say what it found.
 *
 * ── measured, after the first version was asserted ─────────────────────────
 *
 * The view *does* follow the window. Checked by shrinking the window 120px and
 * putting it back, with the page reporting its own viewport at each step:
 *
 *   window 3424x1393   view 3424x1393
 *   window 3424x1273   view 3424x1273    ← follows
 *   window 3424x1393   view 3424x1393    ← and back
 *
 * So `autoResize` is live and a nudge propagates. What that also means is that
 * this can only repair a view that has desynced from a *correct* window — if
 * the window itself is short, this faithfully restores the short size, which is
 * the shape of a fix that does nothing while looking like it should.
 *
 * Hence the log. When it does not help, the numbers say which of the two is
 * wrong, and that is a question nothing in this process can answer on its own:
 * `executeJavascript` has no completion, so the page has to volunteer.
 *
 * A pixel out and back, because AppKit re-lays-out on a *change* and not on an
 * assignment — and the two frames have to be two events, so the second goes on
 * the next turn of the loop rather than immediately.
 */
const fit = (window: BrowserWindow): void => {
  const { width, height } = window.getFrame();
  console.log(`[amoeba] fit: the window says ${width}x${height}`);
  window.webview.executeJavascript(
    "console.log('[amoeba] fit: the view says ' + innerWidth + 'x' + innerHeight)",
  );
  window.setSize(width, height - 1);
  setTimeout(() => window.setSize(width, height), 0);
};

export const installMenu = (window: BrowserWindow): void => {
  ApplicationMenu.on("application-menu-clicked", (event: unknown) => {
    // The event is whatever the native side sent, so its shape is checked
    // rather than asserted — an unknown action must do nothing rather than
    // throw inside a menu handler, where nothing would report it.
    const action = (event as { readonly data?: { readonly action?: unknown } } | undefined)?.data
      ?.action;
    if (action === "view:fit") {
      fit(window);
    }
    if (action === "view:reload") {
      window.webview.reload();
    }
    if (action === "view:devtools") {
      window.webview.toggleDevTools();
    }
  });

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
      label: "View",
      submenu: [
        { label: "Reload", accelerator: "CommandOrControl+R", action: "view:reload" },
        // The repair for the Web Inspector leaving the view short. See above.
        { label: "Fit to Window", accelerator: "CommandOrControl+Alt+R", action: "view:fit" },
        { type: "divider" },
        // Electrobun's own, which docks into this window. Safari's Web
        // Inspector is what leaves the view short — see the note above.
        {
          label: "Toggle Developer Tools",
          accelerator: "CommandOrControl+Alt+I",
          action: "view:devtools",
        },
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
