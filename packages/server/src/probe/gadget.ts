// A gadget, written to a daemon the way an agent writes one.
//
//     bun run probe:gadget                      ws://127.0.0.1:5284, the branch
//     bun run probe:gadget ws://127.0.0.1:5274  the instance in use
//
// The default is the *second* instance and not the one in use, because unlike
// `probe:ask` this one writes: it points a thread's web panel somewhere, which
// is a real page moving in front of whoever is looking at that window. The
// argument is there for when that is what you want.
//
// It runs no zmx, touches no session and starts nothing, so it is safe from
// inside a zmx session. What it is for is the half no test can reach: a
// document compiled by one process, built by `new Function` in another, and
// rendered by React into a column — three processes and a string of
// JavaScript between them.
//
// Two documents, and the second is the point of the first:
//
//   probe-latency   prose, a table, and a component with state in it. If the
//                   button counts, the scope is real.
//   probe-broken    throws on render. The column must name it and stay a
//                   column — an empty panel is the failure this catches.

import { Effect } from "effect";
import * as client from "@awp-kit/protocol/client";

const url = process.argv[2] ?? "ws://127.0.0.1:5284";
const from = process.argv[3] ?? process.cwd();

// `React`, `colors`, `text` and `space` are in scope because the contract says
// so — `gadgetScope` — and nothing else is. The button is the evidence: a
// hook, a token and a re-render, none of which survive a document that is only
// markdown.
const latency = `# Latency by run

Three runs of the parser, in milliseconds. Written by a probe, and nothing here
is a real project.

| run          | p50 | p99 |
| ------------ | --- | --- |
| thicket      |  14 |  91 |
| orchard      |  12 |  77 |
| harbor-works |  21 | 140 |

<Counter />

export function Counter() {
  const [pressed, setPressed] = React.useState(0)
  return (
    <button
      type="button"
      onClick={() => setPressed(pressed + 1)}
      style={{
        color: colors.accent,
        backgroundColor: colors.raised,
        fontSize: text.small,
        border: \`1px solid \${colors.border}\`,
        borderRadius: "0.25rem",
        padding: "0.2rem 0.6rem",
        cursor: "pointer",
      }}
    >
      pressed {pressed} times
    </button>
  )
}
`;

const broken = `# This one throws

The component below fails on its first render. What should be on screen is the
sentence and the name of this gadget — not an empty column.

<Boom />

export function Boom() {
  throw new Error("a gadget can throw, and the column must say which one did")
}
`;

const program = Effect.gen(function* () {
  const rpc = yield* client.AwpClient;
  console.log(`${url}  from ${from}`);
  for (const [name, source] of [
    ["probe-latency", latency],
    ["probe-broken", broken],
  ] as const) {
    const page = yield* rpc.GadgetShow({ from, name, source });
    console.log(
      `  ${name.padEnd(14)}${page.url}  thread ${page.thread ?? "(none — the loose bucket)"}`,
    );
    const found = yield* rpc.GadgetRead({ address: page.url });
    // The size, because it is the one number that says a compile happened: the
    // MDX above is prose and what came back is a function body.
    console.log(`  ${"".padEnd(14)}${found.code.length} bytes of javascript`);
  }
  console.log("\n  open the web panel of that thread, or paste the address into its bar");
});

await Effect.runPromise(Effect.scoped(program).pipe(Effect.provide(client.layerClient(url))));
