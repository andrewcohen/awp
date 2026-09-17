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
// Three documents, which is also the point: they arrive as three tabs in one
// thread's strip, so writing the second no longer erases the first.
//
//   probe-latency   prose, a table, and a component with state in it. If the
//                   button counts, the scope is real.
//   probe-gates     two controls over one set of rows, and bars drawn from
//                   the data. What a gadget is actually for.
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

// A document with something to say, rather than a document proving a mechanism
// works. The numbers are real: one run of each gate on this machine on
// 2026-09-17, warm, in milliseconds of wall clock. Two controls over one set of
// rows is the shape most gadgets will have — a table a person can interrogate
// beats a table they have to read in the order it was written.
const gates = `# What each gate costs

One run of each, warm, in milliseconds of wall clock. The last column is what
that gate catches that no other one does — which is the column that decides
whether a slow gate is worth its time.

<Gates />

The order above is not the order to run them in. Cheap gates first is a rule
about the *first* failure a person sees, not about the total.

export function Gates() {
  const rows = [
    { gate: "fmt", ms: 857, only: "nothing — it rewrites rather than reports" },
    { gate: "lint", ms: 366, only: "a cycle, a node builtin in the renderer" },
    { gate: "typecheck", ms: 2564, only: "every contract between the packages" },
    { gate: "test", ms: 5608, only: "behaviour, and the rules-file budget" },
    { gate: "doctor", ms: 5775, only: "accessibility, keys, placeholder labels" },
  ]
  const [by, setBy] = React.useState("cost")
  const [share, setShare] = React.useState(false)

  const total = rows.reduce((sum, row) => sum + row.ms, 0)
  const most = Math.max(...rows.map((row) => row.ms))
  const sorted = [...rows].sort((a, b) =>
    by === "cost" ? b.ms - a.ms : a.gate.localeCompare(b.gate),
  )

  const control = (on) => ({
    color: on ? colors.accent : colors.muted,
    backgroundColor: on ? colors.raised : "transparent",
    fontSize: text.small,
    border: "none",
    borderRadius: "0.25rem",
    padding: "0.15rem 0.5rem",
    cursor: "pointer",
  })

  return (
    <div style={{ fontSize: text.small }}>
      <div style={{ display: "flex", gap: "0.25rem", marginBottom: "0.6rem" }}>
        <button type="button" style={control(by === "cost")} onClick={() => setBy("cost")}>
          by cost
        </button>
        <button type="button" style={control(by === "name")} onClick={() => setBy("name")}>
          by name
        </button>
        <span style={{ flex: 1 }} />
        <button type="button" style={control(share)} onClick={() => setShare(!share)}>
          {share ? "share of the run" : "milliseconds"}
        </button>
      </div>

      {sorted.map((row) => (
        <div key={row.gate} style={{ marginBottom: "0.5rem" }}>
          <div style={{ display: "flex", justifyContent: "space-between" }}>
            <span style={{ color: colors.text }}>{row.gate}</span>
            <span style={{ color: colors.muted }}>
              {share ? Math.round((row.ms / total) * 100) + "%" : row.ms + "ms"}
            </span>
          </div>
          <div
            style={{
              height: "0.3rem",
              marginTop: "0.2rem",
              borderRadius: "0.15rem",
              backgroundColor: colors.raised,
            }}
          >
            <div
              style={{
                width: Math.round((row.ms / most) * 100) + "%",
                height: "100%",
                borderRadius: "0.15rem",
                backgroundColor: colors.accent,
              }}
            />
          </div>
          <div style={{ color: colors.muted, marginTop: "0.15rem" }}>{row.only}</div>
        </div>
      ))}

      <div style={{ color: colors.muted, marginTop: "0.6rem" }}>
        {total}ms in total, and {Math.round(((total - most) / total) * 100)}% of it before the
        slowest one has finished.
      </div>
    </div>
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
    ["probe-gates", gates],
    ["probe-broken", broken],
  ] as const) {
    const head = yield* rpc.GadgetShow({ from, name, source });
    console.log(
      `  ${name.padEnd(14)}${head.address}  thread ${head.thread ?? "(none — the loose bucket)"}`,
    );
    // The title nobody passed. Printed because it is what the tab will say,
    // and the only way to be wrong about it is to not look.
    console.log(`  ${"".padEnd(14)}tab reads "${head.title}"`);
    const found = yield* rpc.GadgetRead({ address: head.address });
    // The size, because it is the one number that says a compile happened: the
    // MDX above is prose and what came back is a function body.
    console.log(`  ${"".padEnd(14)}${found.code.length} bytes of javascript`);
  }
  console.log("\n  open the gadgets panel of that thread — all three are tabs in it");
});

await Effect.runPromise(Effect.scoped(program).pipe(Effect.provide(client.layerClient(url))));
