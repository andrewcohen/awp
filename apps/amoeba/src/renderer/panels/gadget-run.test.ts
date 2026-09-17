import { gadgetScope } from "@awp-kit/protocol";
import { describe, expect, test } from "vitest";
import { GadgetBroke, runGadget } from "./gadget-run";

// The binding between two processes, tested from the side that does the
// binding.
//
// The bodies below are written by hand in the shape MDX's `function-body`
// output has — `arguments[0]` for the jsx runtime, a `return` of the exports
// with `default` as the component. That shape is not assumed: `gadgets.test.ts`
// compiles a real document and asserts the compiler still emits it. What is
// tested here is the half that shape meets — which names a document can see,
// in which order, and what happens when it cannot see one.

const runtime = { jsx: (type: unknown, props: unknown) => ({ type, props }) };

/** A document that renders whatever expression it is given. */
const showing = (expression: string): string =>
  `"use strict";
   const {jsx: _jsx} = arguments[0];
   function MDXContent() { return _jsx("p", { children: ${expression} }); }
   return { default: MDXContent };`;

describe("running a compiled document", () => {
  test("the default export comes back as something React can render", async () => {
    const doc = await runGadget(showing('"hello"'), runtime, {
      React: {},
      colors: {},
      text: {},
      space: {},
    });
    expect(typeof doc).toBe("function");
    expect(doc({})).toEqual({ type: "p", props: { children: "hello" } });
  });

  test("every name the contract promises is in scope, and holds the value handed in", async () => {
    // The whole of what "there is no registry" means: a document defines its
    // own components, and this list is all such a component can reach for. A
    // name here that is not bound is a gadget that throws on its first render.
    const handed = {
      React: "the-react",
      colors: "the-colors",
      text: "the-text",
      space: "the-space",
    };
    for (const name of gadgetScope) {
      const doc = await runGadget(showing(name), runtime, handed);
      expect(doc({})).toEqual({
        type: "p",
        props: { children: handed[name as keyof typeof handed] },
      });
    }
  });

  test("the runtime is the first argument, whatever the scope is", async () => {
    // MDX's output reads `arguments[0]`, so the order is not a convention this
    // side may revisit: the runtime goes first and the scope follows it. A
    // scope name accidentally placed first is a document whose every element
    // fails to build, with no sentence naming the cause.
    const doc = await runGadget(
      `"use strict";
       const runtimeIsFirst = arguments[0].jsx !== undefined;
       return { default: () => runtimeIsFirst };`,
      runtime,
      { React: {}, colors: {}, text: {}, space: {} },
    );
    expect(doc({})).toBe(true);
  });
});

describe("what it refuses", () => {
  test("a name the contract lists and the window did not hand over", async () => {
    // Refused rather than bound to `undefined`, because the document would
    // then fail on `colors.accent` — a sentence about the window, arriving in
    // a boundary that blames the gadget.
    await expect(runGadget(showing('"hi"'), runtime, { React: {} })).rejects.toThrow(GadgetBroke);
    await expect(runGadget(showing('"hi"'), runtime, { React: {} })).rejects.toThrow(/colors/u);
  });

  test("a document that throws while it is being built", async () => {
    // Before React is involved, so no error boundary can see it: a component
    // defined at the top of a document, referring to something that is not
    // there. This is the half of "a gadget that throws names itself" that
    // cannot be a boundary.
    await expect(
      runGadget('"use strict"; missing.thing(); return {};', runtime, {
        React: {},
        colors: {},
        text: {},
        space: {},
      }),
    ).rejects.toThrow(GadgetBroke);
  });

  test("compiled output with no document in it", async () => {
    await expect(
      runGadget('"use strict"; return { notDefault: 1 };', runtime, {
        React: {},
        colors: {},
        text: {},
        space: {},
      }),
    ).rejects.toThrow(/no document/u);
  });
});
