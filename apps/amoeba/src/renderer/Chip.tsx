import { Select } from "@base-ui/react/select";
import { CaretDownIcon } from "@phosphor-icons/react/CaretDown";
import * as stylex from "@stylexjs/stylex";
import type React from "react";
import { colors, text } from "./tokens.stylex";

// A small select that reads as a chip rather than as a form field.
//
// Lifted out of `NewThread.tsx` when the chat needed the same control for its
// model, effort and permission mode. Nothing about it was specific to the
// modal — it was simply where it was first needed — and a second copy would
// be two things to keep looking the same.
//
// Base UI owns the behaviour: the arrow keys, the typeahead, the roving tab
// stop, the focus return and the portal. The portal is the one that shows up
// as a visual bug rather than an accessibility one — a popup inside a
// scrolling column is clipped by it without one.

export function Chip<T extends string>({
  id,
  label,
  title,
  value,
  onChange,
  options,
  icon,
  quiet,
  disabled,
}: {
  readonly id: string;
  /** What the chip reads as, which is not always the raw value. */
  readonly label: string;
  readonly title: string;
  readonly value: T;
  readonly onChange: (value: T) => void;
  /** In the order they should be read. */
  readonly options: ReadonlyArray<{ readonly value: T; readonly label: string }>;
  readonly icon?: React.ReactNode | undefined;
  readonly quiet?: boolean | undefined;
  readonly disabled?: boolean | undefined;
}) {
  return (
    <Select.Root
      value={value}
      // Base UI types the value as whatever it was given, so it arrives here
      // unnarrowed. This is the only cast on the screen, and it is here rather
      // than at four call sites: every value the popup can emit came out of
      // `options`, so T is exactly what it is.
      onValueChange={(next) => onChange(String(next) as T)}
      disabled={disabled ?? false}
    >
      <Select.Trigger
        id={id}
        title={title}
        {...stylex.props(styles.chip, quiet === true && styles.chipQuiet)}
      >
        {icon}
        {/* The chip shows the label, which for a revset is not the value —
            `trunk()` arrives labelled with the bookmark it resolves to, and
            the title says the rest. */}
        <span>{label}</span>
        <CaretDownIcon size={9} weight="bold" {...stylex.props(styles.chipCaret)} />
      </Select.Trigger>

      {/* Portalled, so nothing it opens inside can clip it. */}
      <Select.Portal>
        <Select.Positioner sideOffset={4} align="start" {...stylex.props(styles.positioner)}>
          <Select.Popup {...stylex.props(styles.menu)}>
            {options.map((option) => (
              <Select.Item key={option.value} value={option.value} {...stylex.props(styles.item)}>
                <Select.ItemIndicator {...stylex.props(styles.tick)}>✓</Select.ItemIndicator>
                <Select.ItemText>{option.label}</Select.ItemText>
              </Select.Item>
            ))}
          </Select.Popup>
        </Select.Positioner>
      </Select.Portal>
    </Select.Root>
  );
}

const styles = stylex.create({
  chip: {
    display: "flex",
    alignItems: "center",
    gap: "0.3rem",
    padding: "0.15rem 0.4rem",
    backgroundColor: "transparent",
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: { default: "transparent", ":hover": colors.border },
    borderRadius: "0.25rem",
    color: colors.text,
    font: "inherit",
    fontSize: text.small,
    cursor: "pointer",
  },
  chipQuiet: { color: colors.muted },
  chipCaret: { flexShrink: 0, color: colors.muted, opacity: 0.7 },
  positioner: { zIndex: 10 },
  menu: {
    padding: "0.2rem",
    backgroundColor: colors.surface,
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.25rem",
    color: colors.text,
    // Monospace, and it is the one menu in the window that keeps it: what this
    // mostly lists is bookmark names, and a bookmark is an address — something
    // a person will type at jj afterwards. The model and effort menus share the
    // component and get it too, which is the cost of one component for four
    // chips and is a smaller cost than four components.
    fontFamily: text.mono,
    fontSize: text.small,
    boxShadow: "0 0.5rem 1.5rem rgba(0, 0, 0, 0.3)",
  },
  item: {
    display: "flex",
    alignItems: "center",
    gap: "0.4rem",
    padding: "0.2rem 0.4rem",
    borderRadius: "0.15rem",
    cursor: "pointer",
    // Base UI sets the attribute; what it looks like is this file's business.
    ":is([data-highlighted])": { backgroundColor: colors.border },
  },
  tick: { width: "0.75rem", flexShrink: 0, color: colors.muted, fontSize: text.small },
});
