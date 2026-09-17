import { gadgetScope } from "@awp-kit/protocol";
import type { ReactNode } from "react";

// Running a document the daemon compiled.
//
// ── what arrives here is a function body, and that is the whole design ────
//
// MDX is compiled in the daemon (see `gadgets.ts`, and `Gadget.code` in the
// contract for why there rather than here). What comes back is JavaScript in
// the shape MDX calls `function-body`: it reads its jsx runtime out of
// `arguments[0]` and returns the document's exports, with `default` as the
// component.
//
// So the window builds a function around it, and *that* is where the scope is
// decided — the parameters it declares are exactly what an inline component in
// the document can see. `arguments[0]` is the first parameter whatever it is
// named, so the runtime goes first and everything a gadget is handed follows
// it, in the order the contract lists.
//
// ── this is `new Function`, and it is worth saying why that is not new ────
//
// A gadget is arbitrary JavaScript written by the agent, evaluated in the
// renderer. That is not an escalation: the same agent already has a shell in
// the workspace this window is looking at, and anything it could reach from
// here it could reach more easily from there. What it does mean is that a
// gadget can *throw*, and a throw with nobody to catch it is a blank column —
// which is why `Gadget.tsx` puts a boundary round the result and this returns
// a refusal rather than letting one escape.
//
// Its own file, no tokens imported, for the reason `browse.ts` gives: a module
// that reaches `tokens.stylex` cannot be loaded by a test outside the Babel
// pass. The values are `Gadget.tsx`'s to supply; the binding is here.

/** What a compiled document returns. `default` is the component. */
export interface GadgetModule {
  readonly default?: unknown;
}

/**
 * A document, ready to render.
 *
 * `components` is how MDX resolves the tags a markdown document produces — a
 * paragraph, a heading, a table cell — so the window's own markdown styling
 * is handed in the same way a gadget's own components are defined: as a map
 * the document consults rather than a stylesheet it inherits.
 */
export type GadgetDoc = (props: {
  readonly components?: Readonly<Record<string, unknown>>;
}) => ReactNode;

/**
 * The document's own error, as a sentence.
 *
 * A gadget that throws while it is being *built* — a component defined at the
 * top of the document referring to something that is not there — throws before
 * React is involved, so no error boundary can see it. This is that half.
 */
export class GadgetBroke extends Error {
  constructor(reason: string) {
    super(reason);
    this.name = "GadgetBroke";
  }
}

// `AsyncFunction` is not a global. MDX's function-body may await — a document
// with no imports never does, but the shape is the compiler's to choose and an
// async wrapper costs a microtask.
const AsyncFunction = Object.getPrototypeOf(async () => {}).constructor as new (
  ...args: ReadonlyArray<string>
) => (...args: ReadonlyArray<unknown>) => Promise<GadgetModule>;

/**
 * Build a compiled gadget, with the jsx runtime and the scope it is handed.
 *
 * @param handed  a value per name in {@link gadgetScope}. A name in the
 *                contract with nothing behind it is refused here rather than
 *                bound to `undefined`: the document would then fail on
 *                `colors.accent` with `cannot read properties of undefined`,
 *                which is a sentence about the window and not about the
 *                document.
 */
export const runGadget = async (
  code: string,
  runtime: Readonly<Record<string, unknown>>,
  handed: Readonly<Record<string, unknown>>,
): Promise<GadgetDoc> => {
  const missing = gadgetScope.filter((name) => !(name in handed));
  if (missing.length > 0) {
    throw new GadgetBroke(`the window did not hand this gadget ${missing.join(", ")}`);
  }
  let made: GadgetModule;
  try {
    const build = new AsyncFunction("_runtime", ...gadgetScope, code);
    made = await build(runtime, ...gadgetScope.map((name) => handed[name]));
  } catch (error) {
    throw new GadgetBroke(error instanceof Error ? error.message : String(error));
  }
  if (typeof made.default !== "function") {
    // A document with no content compiles to a component that renders nothing,
    // so this is not "the gadget was empty" — it is a compiled output that is
    // not a gadget at all, which means the two halves have drifted.
    throw new GadgetBroke("the compiled gadget has no document in it");
  }
  return made.default as GadgetDoc;
};
