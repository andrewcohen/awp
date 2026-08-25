import * as stylex from "@stylexjs/stylex";
import { DesktopIcon } from "@phosphor-icons/react/Desktop";
import { MoonIcon } from "@phosphor-icons/react/Moon";
import { SunIcon } from "@phosphor-icons/react/Sun";
import { type Appearance, nextAppearance, setAppearance, useAppearance } from "./theme";
import { colors } from "./tokens.stylex";

// The control that says how the window should look.
//
// One button rather than three, because two of the three states are always
// wrong and a row of them spends the width of a sentence saying so. What a
// cycle costs is discoverability of the third step, which the title attribute
// buys back: it names the state you are in and the one the next press reaches.
//
//   ⌨  system → light → dark → system
//
// Imported per icon — `@phosphor-icons/react/Sun` — and not from the barrel.
// The barrel is ~1500 modules, which a production build tree-shakes and a dev
// server does not: importing it costs several seconds on every cold start for
// three glyphs.

const icons: Record<Appearance, typeof SunIcon> = {
  system: DesktopIcon,
  light: SunIcon,
  dark: MoonIcon,
};

const says: Record<Appearance, string> = {
  system: "following the system",
  light: "light",
  dark: "dark",
};

const styles = stylex.create({
  button: {
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    width: "1.75rem",
    height: "1.75rem",
    padding: 0,
    borderStyle: "none",
    borderRadius: "0.25rem",
    backgroundColor: { default: "transparent", ":hover": colors.border },
    // The icon is the only thing that carries a colour, and it inherits.
    color: "inherit",
    cursor: "pointer",
    // Full weight on hover, quiet otherwise. A control that is always present
    // and rarely used should not be competing with the session list for the
    // eye, and dimming is a cheaper way to say so than a smaller glyph.
    opacity: { default: 0.55, ":hover": 1, ":focus-visible": 1 },
  },
});

export function AppearanceToggle() {
  const appearance = useAppearance();
  const Icon = icons[appearance];
  const next = nextAppearance(appearance);

  return (
    <button
      type="button"
      // The accessible name is the state, not the verb. Screen-reader users get
      // told what the window is doing; the title adds what pressing will do.
      aria-label={`appearance: ${says[appearance]}`}
      title={`appearance: ${says[appearance]} — press for ${says[next]}`}
      onClick={() => setAppearance(next)}
      {...stylex.props(styles.button)}
    >
      {/* Phosphor sizes from the font by default, which here would be 13px and
          too small to read as a glyph rather than a smudge. */}
      <Icon size={15} weight="bold" aria-hidden />
    </button>
  );
}
