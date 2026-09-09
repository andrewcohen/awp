import { BrowserWindow, Menu } from "electron";
import type { MenuItemConstructorOptions } from "electron";

// The Edit menu, which is not a menu.
//
// The argument is unchanged from the electrobun shell and is worth restating,
// because it is the reason this file is not optional furniture: on macOS cmd+V
// is not a key the way ctrl+j is. It is the *key equivalent of a menu item*,
// and AppKit turns it into the `paste:` action only if some item claims it. An
// app with no menu bar has no such item, so cmd+V reaches the web view as a
// plain keydown and nothing pastes.
//
// That is what made dictation fail: Handy transcribes speech, puts the text on
// the clipboard and synthesises cmd+V, and `navigator.clipboard.readText()` —
// the workaround in `clipboard.ts` — is gated behind a permission prompt that a
// person can click and a dictation tool cannot. With the item present, the
// system performs a real paste and the page gets a `paste` event carrying the
// text, which route one in `clipboard.ts` already handles.
//
// It was never only the pane: cut, copy, paste, select-all and undo are
// supplied by the system to any focused field once the items exist, so the
// address bar, the thread composer and the diff comment box were all broken
// too.
//
// ── what is deliberately not here ─────────────────────────────────────────
//
// No File menu, and nothing claiming cmd+N or cmd+shift+N. Those chords are the
// window's own — they open the thread composers — and a menu item claiming one
// takes the key before the renderer ever sees it. A menu is a set of claims on
// the keyboard, and this window already made some.
//
// The accelerators are spelled out. Electron does supply defaults for roles,
// unlike electrobun's layer, and they are written anyway so that the claim this
// menu is making is readable in the file that makes it.

/**
 * Make the web view take the window's size again.
 *
 * Carried over from the electrobun shell, where docking Safari's Web Inspector
 * left the view short and it did not recover on undock. Electron's devtools are
 * a different implementation and are not known to do this, so **this is parity
 * rather than a fix for a bug observed here** — it is one menu item and a
 * pixel, and removing it would be removing the only repair available if some
 * other native inset does the same thing.
 *
 * A pixel out and back, because AppKit re-lays-out on a *change* and not on an
 * assignment — reapplying the frame it already has does nothing, which was the
 * first version and did nothing very convincingly.
 */
const fit = (window: BrowserWindow): void => {
  // Defaulted, not asserted: `getSize` is a tuple to TypeScript and an array
  // to `noUncheckedIndexedAccess`, and a window with no size is not a case.
  const [width = 0, height = 0] = window.getSize();
  console.log(`[amoeba] fit: the window says ${width}x${height}`);
  void window.webContents
    .executeJavaScript("[innerWidth, innerHeight]")
    .then((view: unknown) => console.log(`[amoeba] fit: the view says ${JSON.stringify(view)}`))
    .catch(() => undefined);
  window.setSize(width, height - 1);
  setTimeout(() => window.setSize(width, height), 0);
};

/**
 * The window a menu item should act on, or nothing.
 *
 * ── the menu outlives the window it was built for ────────────────────────
 *
 * On macOS the app now stays running with no window — closing one is not
 * quitting, see `window-all-closed` in main.ts — and the menu bar is still
 * there while it does. So `window` closed over here can be destroyed by the
 * time somebody presses cmd+R, and `webContents` on a destroyed window throws
 * in the main process, where nothing renders the error.
 *
 * The focused window first, because with several open the menu means the one
 * in front rather than the one this template was built for.
 */
const acting = (window: BrowserWindow): BrowserWindow | undefined => {
  const focused = BrowserWindow.getFocusedWindow();
  if (focused !== null && !focused.isDestroyed()) {
    return focused;
  }
  return window.isDestroyed() ? undefined : window;
};

export const installMenu = (window: BrowserWindow): void => {
  const template: Array<MenuItemConstructorOptions> = [
    {
      label: "amoeba",
      submenu: [
        { role: "about" },
        { type: "separator" },
        { role: "hide", accelerator: "CommandOrControl+H" },
        { role: "hideOthers" },
        // electrobun spelled this `showAll`; Electron's name for the same
        // NSApplication selector is `unhide`.
        { role: "unhide" },
        { type: "separator" },
        { role: "quit", accelerator: "CommandOrControl+Q" },
      ],
    },
    {
      label: "Edit",
      submenu: [
        { role: "undo", accelerator: "CommandOrControl+Z" },
        { role: "redo", accelerator: "CommandOrControl+Shift+Z" },
        { type: "separator" },
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
        {
          label: "Reload",
          accelerator: "CommandOrControl+R",
          click: () => acting(window)?.webContents.reload(),
        },
        {
          label: "Fit to Window",
          accelerator: "CommandOrControl+Alt+R",
          click: () => {
            const on = acting(window);
            if (on !== undefined) {
              fit(on);
            }
          },
        },
        { type: "separator" },
        {
          label: "Toggle Developer Tools",
          accelerator: "CommandOrControl+Alt+I",
          click: () => acting(window)?.webContents.toggleDevTools(),
        },
      ],
    },
    {
      label: "Window",
      submenu: [
        { role: "minimize", accelerator: "CommandOrControl+M" },
        { role: "zoom" },
        { type: "separator" },
        { role: "close", accelerator: "CommandOrControl+W" },
      ],
    },
  ];

  Menu.setApplicationMenu(Menu.buildFromTemplate(template));
};
