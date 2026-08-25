import type { ColorScheme } from "@awp-kit/pane";
import * as stylex from "@stylexjs/stylex";
import { useSyncExternalStore } from "react";
import { readStored, writeStored } from "./remembered";
import { colors, hue } from "./tokens.stylex";

// What the window looks like, and who decided.
//
// Three values, not two, and the third is the point: "dark because the system
// is dark" and "dark because it was told to" render identically and behave
// differently. Only the first should follow the system when the system changes,
// so a boolean cannot hold this — the moment a user picks light on a dark
// machine, a boolean has forgotten that anything was ever automatic.
//
//   system ──▶ light ──▶ dark ──▶ system        the cycle the control walks
//     │          │         │
//     │          └─────────┴──▶ forceLight / forceDark, a class on the root
//     └──▶ nothing at all: the media query in tokens.stylex.ts already did it

export type Appearance = "system" | "light" | "dark";

export const appearances: ReadonlyArray<Appearance> = ["system", "light", "dark"];

/**
 * Every role the chrome has, taken from the variables themselves.
 *
 * This exists so that the two tables below cannot be partial. `createTheme`
 * accepts a subset without complaint, and a theme that omits a role leaves that
 * one variable at whatever the media query said — so forcing light on a dark
 * machine would paint five colours light and the sixth dark. That happened
 * within an hour of writing this file, to `warn`, and nothing failed: the
 * window merely had one wrong colour in a state nobody was looking at.
 *
 * A `VarGroup` carries bookkeeping members beside the variables, and `keyof`
 * cannot tell them apart, so they are named and removed. If StyleX grows
 * another one this stops compiling — which is the right way round: a
 * bookkeeping member mistaken for a colour is a type error, where a colour
 * mistaken for bookkeeping would be another silent hole.
 */
type NotAVariable = "__opaqueId" | "__tokens" | "description" | "toString" | "valueOf";

type ChromeRole = Exclude<Extract<keyof typeof colors, string>, NotAVariable>;

const latteChrome: Record<ChromeRole, string> = {
  base: hue.latteBase,
  text: hue.latteText,
  muted: hue.latteMuted,
  border: hue.latteBorder,
  live: hue.latteLive,
  warn: hue.latteWarn,
};

const macchiatoChrome: Record<ChromeRole, string> = {
  base: hue.macchiatoBase,
  text: hue.macchiatoText,
  muted: hue.macchiatoMuted,
  border: hue.macchiatoBorder,
  live: hue.macchiatoLive,
  warn: hue.macchiatoWarn,
};

/**
 * The explicit cases, as StyleX themes.
 *
 * `createTheme` restates every variable, so applying one of these outranks the
 * media query that produced the automatic value — which is exactly the override
 * a media query cannot express on its own. The system case has no theme at all,
 * because the absence of an override *is* following the system.
 */
const forceLight = stylex.createTheme(colors, latteChrome);
const forceDark = stylex.createTheme(colors, macchiatoChrome);

/**
 * `Theme` and not `StyleXStyles`. A theme sets variables where a style sets
 * properties, and StyleX types them apart so one cannot be passed where the
 * other is meant.
 */
type AppearanceTheme = typeof forceLight | typeof forceDark | undefined;

export const themeFor = (appearance: Appearance): AppearanceTheme =>
  appearance === "light" ? forceLight : appearance === "dark" ? forceDark : undefined;

// ── the preference itself ──────────────────────────────────────────────────

const KEY = "amoeba.appearance";
const QUERY = "(prefers-color-scheme: dark)";

const isAppearance = (value: unknown): value is Appearance =>
  value === "system" || value === "light" || value === "dark";

// Following the system is the right default when the window cannot remember
// being told otherwise — including when storage is refused outright.
const load = (): Appearance => {
  const stored = readStored(KEY);
  return isAppearance(stored) ? stored : "system";
};

let preference = load();

const listeners = new Set<() => void>();

/**
 * Tell the engine which way the window is dressed.
 *
 * global.css says `color-scheme: light dark`, which means "follow the system" —
 * correct for the system case and wrong for the other two, where the scrollbars
 * inside columns, the form controls and the canvas backdrop would keep the
 * system's appearance while everything the app paints had changed. Setting it
 * inline outranks the sheet; clearing it hands the decision back.
 */
const dress = (appearance: Appearance): void => {
  const root = globalThis.document?.documentElement;
  if (root === undefined) {
    return;
  }
  root.style.colorScheme = appearance === "system" ? "" : appearance;
};

dress(preference);

export const setAppearance = (appearance: Appearance): void => {
  if (appearance === preference) {
    return;
  }
  preference = appearance;
  dress(appearance);
  writeStored(KEY, appearance);
  for (const listener of listeners) {
    listener();
  }
};

export const nextAppearance = (appearance: Appearance): Appearance =>
  appearances[(appearances.indexOf(appearance) + 1) % appearances.length] ?? "system";

/**
 * One subscription for both hooks below.
 *
 * The resolved scheme changes for two unrelated reasons — the user picked
 * something, or the system did — and a hook that watched only one of them would
 * be right most of the time, which is the worst way for this to be wrong.
 */
const subscribe = (onChange: () => void): (() => void) => {
  const media = window.matchMedia(QUERY);
  media.addEventListener("change", onChange);
  listeners.add(onChange);
  return () => {
    media.removeEventListener("change", onChange);
    listeners.delete(onChange);
  };
};

const currentAppearance = (): Appearance => preference;

/**
 * The scheme actually in force, after the preference and the system have both
 * had their say.
 *
 * Exported as well as subscribed to, for effects that need the scheme without
 * depending on it: reading it is always current, where closing over a rendered
 * value ties the effect to re-running when it changes.
 *
 * Returns a primitive. useSyncExternalStore compares snapshots with Object.is,
 * so a fresh object every call is an infinite loop.
 */
export const currentColorScheme = (): ColorScheme =>
  preference === "system" ? (window.matchMedia(QUERY).matches ? "dark" : "light") : preference;

export const useAppearance = (): Appearance => useSyncExternalStore(subscribe, currentAppearance);

// useSyncExternalStore rather than useState + useEffect, because the preference
// is state that already exists outside React. The effect version reads it a
// frame late, which is a visible flash of the wrong theme on launch, and it is
// also the version React Compiler and StrictMode are entitled to run twice.
export const useColorScheme = (): ColorScheme =>
  useSyncExternalStore(subscribe, currentColorScheme);
