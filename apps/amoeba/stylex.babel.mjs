// The StyleX Babel options, in one place because two passes need them.
//
// StyleX compiles a `stylex.create` call into class names and hands the rules
// out as Babel metadata, which the bundler throws away. So the rules are
// recovered by a second pass — the PostCSS plugin — which re-reads the same
// source files with its own Babel and keeps the metadata instead of the code.
//
//   vite  ─▶ babel(stylex) ─▶ class names in the bundle
//   css   ─▶ babel(stylex) ─▶ the rules those class names refer to
//
// Both arms must be given identical options. `dev` in particular changes the
// class names themselves, so the two passes disagreeing about it produces
// markup with class names that no rule matches: an element with correct
// structure and no styling at all, which looks like a StyleX bug and is a
// configuration one. Hence this file rather than the same literal twice.

import { resolve } from "node:path";
import styleX from "@stylexjs/babel-plugin";

// The monorepo root, not the app. StyleX turns the path of a `.stylex.ts` file
// into a stable variable name, and a token defined in a package but used here
// must hash the same on both sides — which it would not if each measured from
// its own directory.
export const rootDir = resolve(import.meta.dirname, "../..");

// The same test the PostCSS plugin applies to itself. Reading the flag from
// somewhere else — vite's `mode`, an argument — is how the two arms drift.
const dev = process.env.NODE_ENV === "development";

export const styleXPlugin = [
  styleX,
  {
    // The rules go to the PostCSS pass, not into a <style> tag the bundle
    // writes at startup.
    runtimeInjection: false,
    // Readable class names and a `data-style-src` back to the source line,
    // while the chrome is being designed. Production gets the hashes.
    dev,
    unstable_moduleResolution: { type: "commonJS", rootDir },
  },
];

// Enough Babel to *parse* the renderer, and no more.
//
// The PostCSS pass wants metadata, not output — it discards the code Babel
// generates — so the type annotations never need stripping and no preset is
// required. Without this it parses TypeScript as JavaScript and dies on the
// first `import type`.
export const parserOpts = {
  plugins: [["typescript", { isTSX: true, disallowAmbiguousJSXLike: true }], "jsx"],
};
