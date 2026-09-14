import { ContextMenu } from "@base-ui/react/context-menu";
import { Menu } from "@base-ui/react/menu";
import * as stylex from "@stylexjs/stylex";
import type { ReactNode } from "react";
import { typeset } from "./typeset";
import { colors, lift, text } from "./tokens.stylex";

// A row's menu, reachable two ways.
//
// ── why two roots, and not one menu with two triggers ──────────────────────
//
// Base UI's `ContextMenu.Root` *is* a `Menu.Root` — it renders one, wrapped in
// a context carrying the anchor the right click set. So nesting a
// `Menu.Trigger` inside it does open the same menu, and it opens it **at the
// last right-click point**: the positioner reads the anchor out of that
// context rather than off the trigger it was pressed on. Which means the ⋯
// would open a popup somewhere else on screen, or at 0,0 before any right
// click has happened.
//
// So there are two roots, and they are two components here rather than one —
// because the area and the button are in different places in the markup. A
// row's right-click area is the whole band, and its ⋯ belongs on the title
// line beside the name. One component owning both could only ever put the
// button at the end of the area.
//
// What is shared is the item list, passed to each as a function. The popup's
// chrome is stated once below, so what is duplicated is a wrapper and never a
// decision — the same argument as `Row` being shared with the style guide.
//
// ── both, because neither is enough ───────────────────────────────────────
//
// A context menu with no visible affordance is a feature only somebody who
// already knows it exists can find. A ⋯ revealed on hover is one that does not
// exist without a pointer — which is why it is `opacity: 0` and never
// `display: none`: an element outside the layout cannot be focused, and the
// keyboard mandate rests on it being reachable by Tab.

const styles = stylex.create({
  trigger: {
    flexShrink: 0,
    // Above the row's stretched target — see `stretch` in Sidebar.tsx, whose
    // `::after` covers the whole band. Without this the one control on the row
    // that is not "open it" would be under a transparent sheet, and pressing ⋯
    // would open the workspace instead of the menu.
    position: "relative",
    zIndex: 1,
    padding: "0 0.25rem",
    backgroundColor: "transparent",
    borderStyle: "none",
    color: colors.muted,
    font: "inherit",
    fontSize: text.small,
    lineHeight: 1,
    cursor: "pointer",
    opacity: 0,
    ":focus-visible": { opacity: 1 },
  },
  shown: { opacity: 1 },
  positioner: { zIndex: 20 },
  menu: {
    // Portalled, so the family is stated rather than inherited.
    minWidth: "10rem",
    padding: "0.25rem",
    backgroundColor: colors.surface,
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.35rem",
    color: colors.text,
    boxShadow: lift.high,
  },
  item: {
    display: "flex",
    alignItems: "center",
    padding: "0.3rem 0.5rem",
    borderRadius: "0.25rem",
    cursor: "pointer",
    backgroundColor: { default: "transparent", ":hover": colors.raised },
    outline: "none",
  },
  /** An act that cannot be taken back reads in the colour that says so. */
  danger: { color: colors.warn },
});

/** The row styles, so every menu in the window spells an item the same way. */
export const menuItem = styles.item;
export const menuDanger = styles.danger;

/** What both roots render into. Built on demand: each mounts its own copy. */
export type Items = () => ReactNode;

// `Menu.Portal`, `Positioner` and `Popup` under both roots, and that is not a
// shortcut: `ContextMenu.Portal` and the rest are re-exports of exactly these
// components. The positioner reads its anchor out of whichever root is above
// it, which is the whole of the difference between the two.
const popup = (items: Items) => (
  <Menu.Portal>
    <Menu.Positioner sideOffset={4} align="end" {...stylex.props(styles.positioner)}>
      <Menu.Popup {...stylex.props(typeset.label, styles.menu)}>{items()}</Menu.Popup>
    </Menu.Positioner>
  </Menu.Portal>
);

/**
 * The ⋯ button and the menu it opens.
 *
 * Placed by the caller, wherever the row wants it.
 */
export function More({
  label,
  shown,
  items,
  onOpen,
}: {
  /** What it is called to a screen reader. Names the thing, not the mark. */
  readonly label: string;
  /** The row is hovered. Focus reveals the button on its own. */
  readonly shown: boolean;
  readonly items: Items;
  /** Called when the menu opens, for whatever the caller resets. */
  readonly onOpen?: (() => void) | undefined;
}) {
  return (
    <Menu.Root
      onOpenChange={(open) => {
        if (open) onOpen?.();
      }}
    >
      <Menu.Trigger
        aria-label={label}
        title="more"
        {...stylex.props(styles.trigger, shown && styles.shown)}
      >
        ⋯
      </Menu.Trigger>
      {popup(items)}
    </Menu.Root>
  );
}

/**
 * The area a right click opens the same menu on.
 *
 * This element **is** the row — not a wrapper around one. A menu that added a
 * box of its own would put a div between the row and the flex it sits in, so
 * the caller hands over the style the row would have had.
 */
export function RightClick({
  items,
  onOpen,
  style,
  onPointerEnter,
  onPointerLeave,
  children,
}: {
  readonly items: Items;
  readonly onOpen?: (() => void) | undefined;
  /**
   * A list, not one entry: a row's styling is `stylex.props(base, on && lit)`
   * at every call site here, and resolving it before handing it over would
   * flatten two conditional styles into one class the caller cannot vary.
   */
  readonly style?: ReadonlyArray<stylex.StyleXStyles | false | undefined> | undefined;
  readonly onPointerEnter?: (() => void) | undefined;
  readonly onPointerLeave?: (() => void) | undefined;
  readonly children: ReactNode;
}) {
  return (
    <ContextMenu.Root
      onOpenChange={(open) => {
        if (open) onOpen?.();
      }}
    >
      <ContextMenu.Trigger
        onPointerEnter={onPointerEnter}
        onPointerLeave={onPointerLeave}
        {...stylex.props(...(style ?? []))}
      >
        {children}
      </ContextMenu.Trigger>
      {popup(items)}
    </ContextMenu.Root>
  );
}
