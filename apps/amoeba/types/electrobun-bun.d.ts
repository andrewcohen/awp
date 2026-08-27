// A types-only stub for `electrobun/bun`.
//
// Electrobun publishes raw TypeScript as its entry point — `exports` maps to
// `dist/api/bun/index.ts`, not a `.d.ts` — so `tsc` typechecks the library's
// own source as part of this project. It does not pass: its FFI layer assigns
// `bigint | Pointer` where a `Pointer` is expected, and a newer `@types/bun`
// than it was written against makes that an error. `skipLibCheck` cannot help,
// because these are `.ts` files rather than declarations.
//
// So this file declares the surface amoeba actually uses, and tsconfig maps the
// import to it. Runtime is unaffected: Bun loads the real module, and this is
// only ever consulted by the typechecker.
//
// The cost is honest and worth naming: if Electrobun changes these signatures,
// nothing here notices. Keep the surface minimal so that stays a small risk,
// and delete the whole file once upstream typechecks cleanly against our
// settings — a one-line tsconfig change away.

declare module "electrobun/bun" {
  export interface WindowFrame {
    x: number;
    y: number;
    width: number;
    height: number;
  }

  export interface BrowserWindowOptions {
    title?: string;
    url?: string | null;
    html?: string | null;
    preload?: string | null;
    frame?: Partial<WindowFrame>;
    titleBarStyle?: "hidden" | "hiddenInset" | "default";
    transparent?: boolean;
    hidden?: boolean;
  }

  export class BrowserWindow {
    constructor(options?: BrowserWindowOptions);
    readonly id: number;
    close(): void;
    /** The window's real frame, read from the native side rather than cached. */
    getFrame(): { x: number; y: number; width: number; height: number };
    setSize(width: number, height: number): void;
    /** The window's own web view. See the View menu in menu.ts. */
    readonly webview: {
      reload(): void;
      /**
       * Electrobun's own inspector, which docks into this window.
       *
       * Not Safari's. Attaching Safari's Web Inspector to this view leaves it
       * short and it does not recover — see the View menu.
       */
      toggleDevTools(): void;
      /** Fire and forget: the native call has no completion, so nothing comes back. */
      executeJavascript(js: string): void;
    };
  }

  /**
   * A menu item, narrowed to the two shapes this app builds.
   *
   * A role and an action are alternatives upstream — the library's own comment
   * says "application menus can either have an action or a role, not both".
   * The Edit and Window items are roles, because the point there is to hand
   * the keyboard back to AppKit's own text editing rather than to do anything
   * ourselves; the View items are actions, because reloading and re-fitting
   * the window are this app's own and AppKit has no role for either.
   */
  export type ApplicationMenuItemConfig =
    | { type: "divider" | "separator" }
    | {
        type?: "normal";
        label?: string;
        role?: string;
        action?: string;
        accelerator?: string;
        enabled?: boolean;
        checked?: boolean;
        hidden?: boolean;
        tooltip?: string;
        submenu?: Array<ApplicationMenuItemConfig>;
      };

  export const ApplicationMenu: {
    setApplicationMenu(menu: Array<ApplicationMenuItemConfig>): void;
    /**
     * Every click on an item that carries an `action`.
     *
     * `unknown` rather than a shape, because this crosses from the native side
     * and the library types it as `unknown` too. The handler in menu.ts checks
     * what it got instead of asserting — an unrecognised action inside a menu
     * handler must do nothing, not throw somewhere nothing would report it.
     */
    on(name: "application-menu-clicked", handler: (event: unknown) => void): void;
  };
}
