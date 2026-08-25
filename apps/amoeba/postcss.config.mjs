// StyleX's stylesheet is assembled here, not by the bundler.
//
// The plugin re-reads the source files, collects the rules the Babel pass
// extracted, and replaces the `@stylex;` directive in stylex.css with the whole
// generated sheet. See stylex.babel.mjs for why there are two passes at all.
//
// `include` has to cover everything the bundler's Babel pass will touch. A file
// compiled by one and missed by the other produces class names with no rules
// behind them.

import { parserOpts, styleXPlugin } from "./stylex.babel.mjs";

export default {
  plugins: {
    "@stylexjs/postcss-plugin": {
      // Relative to cwd, which for the renderer's build is apps/amoeba.
      include: ["src/**/*.{ts,tsx}", "../../packages/*/src/**/*.{ts,tsx}"],
      // `configFile: false` because there is no babel.config.js to find and the
      // plugin would otherwise search for one on every build.
      babelConfig: { babelrc: false, configFile: false, parserOpts, plugins: [styleXPlugin] },
    },
  },
};
