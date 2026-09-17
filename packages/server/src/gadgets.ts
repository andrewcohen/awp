import type { Gadget, GadgetHead } from "@awp-kit/protocol";
import { GadgetRefused, gadgetAddress, gadgetName, gadgetScope } from "@awp-kit/protocol";
import type { Migration } from "@awp-kit/store";
import { Db, attempt } from "@awp-kit/store";
import { compile } from "@mdx-js/mdx";
import { Clock, Context, Effect, Layer, PubSub, Ref, Stream } from "effect";
import remarkGfm from "remark-gfm";

// The documents an agent wrote, compiled.
//
// ── what a gadget is for ──────────────────────────────────────────────────
//
// `Pages` points the accessory column at somebody else's page. This is the
// other half of the same column: a small self-contained thing an agent writes
// and a person reads — a table it wants looked at, a chart of what it
// measured, a comparison with a toggle on it. The agent writes MDX and this
// compiles it; the window draws it in a panel of its own.
//
// ── a thread's gadgets are a set, which is why they are not a page ────────
//
// They rode the page feed first, and that was one address per thread: the
// second gadget an agent wrote took the first one's place, and the first is
// usually the one being asked about. So the panel keeps a strip and this keeps
// a map, and the feed says *one more exists* rather than *look here now*.
//
// ── the source is written down, the compile is not ───────────────────────
//
// `Pages` keeps no table because replaying a navigation would move somebody's
// page on launch. A gadget is content rather than an event, so that argument
// does not carry: forgetting one loses a document somebody was told to look
// at, and it was lost on every restart — which in this repo is several times
// an hour.
//
// What the table holds is the **source**. The compile stays in memory:
//
//   list   answers from the table and compiles nothing — the strip opens
//          without reading a single document
//   read   compiles on a miss and keeps it, so the one tab somebody clicks
//          is the only one that pays
//
// Storing the output instead would put a compiler's artifact in a table that
// outlives the compiler: the day `@mdx-js` changes what it expects in
// `arguments[0]`, every stored gadget is a document running against a shape
// nothing hands it any more. A source cannot go stale that way.
//
// ── the compile is here, at the moment of writing ─────────────────────────
//
// See `Gadget.code` in the contract for the whole argument. The short of it:
// a document that does not compile is a refusal handed back to the agent
// holding the source, in the compiler's own words — not a blank column an
// hour later.

/**
 * The gadgets table.
 *
 * `thread` is nullable, because a gadget written outside a thread is addressed
 * without one. `list` therefore asks with `is ?` rather than `= ?` — a null
 * compared with `=` matches nothing, including other nulls, so those rows
 * would be written and then never found.
 *
 * A rewrite deletes before it inserts, which is the move the map makes and for
 * the same reason: `rowid` is what breaks the tie between two gadgets written
 * inside one millisecond, and the one written second has to sort first.
 */
export const migrations: ReadonlyArray<Migration> = [
  {
    name: "gadgets.001-initial",
    up: [
      `create table gadgets (
         address text primary key,
         thread  text,
         name    text not null,
         title   text not null,
         source  text not null,
         at      integer not null
       ) strict`,
      `create index gadgets_thread on gadgets (thread)`,
    ],
  },
];

export class Gadgets extends Context.Service<
  Gadgets,
  {
    /**
     * Compile a document and keep it under its name, replacing whatever that
     * name held.
     *
     * The thread is the caller's to resolve, for the reason it is on `Pages`:
     * a directory is not a thread and only the handler holds the tables that
     * turn one into the other.
     */
    readonly show: (
      thread: string | undefined,
      name: string,
      source: string,
    ) => Effect.Effect<Gadget, GadgetRefused>;
    /** The document at an address, or a refusal naming what is missing. */
    readonly read: (address: string) => Effect.Effect<Gadget, GadgetRefused>;
    /**
     * A thread's gadgets, newest first, without their documents.
     *
     * Newest first because the strip is read left to right and the one just
     * written is the one somebody was told to look at. A thread is never
     * handed another's: the address carries it, so the filter is on a field
     * and not on a convention.
     */
    readonly list: (thread: string | undefined) => Effect.Effect<ReadonlyArray<GadgetHead>>;
    /** Every gadget written from now on. Nothing is replayed — see the note above. */
    readonly changes: () => Stream.Stream<GadgetHead>;
  }
>()("awp/Gadgets") {}

/**
 * Refuse `import` where the author can still do something about it.
 *
 * MDX does not refuse it: with `outputFormat: "function-body"` an import
 * becomes `await import(_resolveDynamicMdxSpecifier(…))` and a check for
 * `options.baseUrl` that throws *at run time*, in the window, with a sentence
 * about an option the agent has never heard of. There is no module graph for a
 * document held in memory to resolve against, so the honest answer is at the
 * top: no, and here is why, on the line it is on.
 *
 * The estree and not the source text: `import` also begins `important`, and a
 * fenced code block full of imports is prose about imports.
 */
const noImports = () => (tree: unknown) => {
  const root = tree as { readonly children?: ReadonlyArray<Esm> };
  for (const node of root.children ?? []) {
    if (node.type !== "mdxjsEsm") {
      continue;
    }
    for (const statement of node.data?.estree?.body ?? []) {
      const kind = statement.type;
      const external =
        kind === "ImportDeclaration" ||
        ((kind === "ExportNamedDeclaration" || kind === "ExportAllDeclaration") &&
          statement.source != null);
      if (external) {
        const line = node.position?.start?.line;
        throw new Error(
          `import is not available in a gadget${line === undefined ? "" : ` (line ${String(line)})`}` +
            " — there is no module graph to resolve it against. Define what you need in the " +
            `document itself, or use what a gadget is handed: ${gadgetScope.join(", ")}.`,
        );
      }
    }
  }
};

/**
 * The document's first heading, for the tab to say.
 *
 * Read from the mdast while it is already being walked, rather than asked for
 * as an argument: a title the agent passes separately is a title that can
 * disagree with the document under it, and there is no way to notice. This one
 * cannot — it *is* the heading, in the author's own words.
 *
 * The first heading at any depth and not specifically an `#`: a document that
 * opens at `##` is unusual and not wrong, and nothing here should force a
 * level on prose. Text nodes only, so a heading with an inline component in it
 * contributes the words around it and none of the component.
 */
const firstHeading = (into: { title: string | undefined }) => () => (tree: unknown) => {
  const root = tree as { readonly children?: ReadonlyArray<Prose> };
  for (const node of root.children ?? []) {
    if (node.type === "heading") {
      const said = words(node).trim().replace(/\s+/gu, " ");
      if (said !== "") {
        into.title = said;
      }
      return;
    }
  }
};

/** Every text node under one, joined. */
const words = (node: Prose): string =>
  typeof node.value === "string" ? node.value : (node.children ?? []).map(words).join("");

interface Prose {
  readonly type: string;
  readonly value?: unknown;
  readonly children?: ReadonlyArray<Prose>;
}

interface Esm {
  readonly type: string;
  readonly position?: { readonly start?: { readonly line?: number } };
  readonly data?: {
    readonly estree?: {
      readonly body?: ReadonlyArray<{ readonly type: string; readonly source?: unknown }>;
    };
  };
}

/**
 * What went wrong, as one line.
 *
 * A compiler error carries the sentence and the place separately, and the
 * place is the half that makes it actionable — an author reading "Unexpected
 * end of file" with no line has to guess which of forty lines it is about.
 */
const why = (error: unknown): string => {
  const one = error as { readonly message?: unknown; readonly line?: unknown } | null;
  const message = typeof one?.message === "string" ? one.message : String(error);
  return typeof one?.line === "number" ? `${message} (line ${String(one.line)})` : message;
};

/**
 * MDX → the body of a function this window can build.
 *
 * `function-body` rather than a module, because the renderer has no bundler at
 * hand and a module would have to be fetched as one. What comes back is
 * JavaScript that expects its jsx runtime as `arguments[0]` and returns the
 * document's exports — see `gadget-run.ts`, which is the other half of this
 * decision and the place the scope is decided.
 *
 * `remark-gfm` for the reason `Markdown.tsx` has it: tables, task lists and
 * strikethrough are what the dialect a model writes actually contains, and a
 * gadget is very often a table.
 *
 * The title comes back with the code because the walk that finds it is this
 * one. Parsing the source a second time to read a heading would be a second
 * parser, with its own idea of what a heading is — and the two would agree
 * until the day a document put one inside a fence.
 */
export const compileGadget = (
  source: string,
): Effect.Effect<{ readonly code: string; readonly title: string | undefined }, GadgetRefused> =>
  Effect.tryPromise({
    try: async () => {
      // Written into by the plugin during the compile, read after it. A remark
      // plugin's only channel is the tree it is handed and the file it is run
      // against; a holder is the smaller of the two.
      const found: { title: string | undefined } = { title: undefined };
      const code = String(
        await compile(source, {
          outputFormat: "function-body",
          remarkPlugins: [remarkGfm, noImports, firstHeading(found)],
        }),
      );
      return { code, title: found.title };
    },
    catch: (error) => new GadgetRefused({ reason: why(error) }),
  });

/** A gadget without its document, which is what a strip is drawn from. */
const headOf = ({ code: _code, ...head }: Gadget): GadgetHead => head;

/** The same, out of a row: `thread` is absent rather than null. */
const headFrom = (row: Record<string, unknown>): GadgetHead => ({
  address: String(row["address"]),
  ...(typeof row["thread"] === "string" ? { thread: row["thread"] } : {}),
  name: String(row["name"]),
  title: String(row["title"]),
  at: Number(row["at"]),
});

export const make = Effect.gen(function* () {
  const db = yield* Db;
  // The compiled documents, which the table deliberately does not hold. A
  // cache and not the truth: every entry can be rebuilt from its row.
  const held = yield* Ref.make(new Map<string, Gadget>());
  // Dropping, and small, for the reason `Pages` gives: a subscriber is a
  // socket, and one that has stopped reading is a window that has gone away.
  // A dropped event costs less here than it does there — the panel opens from
  // `list`, so a window that missed one is a window one click from seeing it.
  const hub = yield* PubSub.dropping<GadgetHead>(16);

  const forget = db.prepare("delete from gadgets where address = ?");
  const remember = db.prepare(
    "insert into gadgets (address, thread, name, title, source, at) values (?, ?, ?, ?, ?, ?)",
  );
  const readOne = db.prepare("select * from gadgets where address = ?");
  const readHeads = db.prepare(
    "select address, thread, name, title, at from gadgets where thread is ? order by at desc, rowid desc",
  );

  const keep = (gadget: Gadget) =>
    Ref.update(held, (all) => {
      // Removed before it is set, so a rewrite takes the newest position in
      // the map rather than keeping the one its first writing had. Insertion
      // order is the tie-break when the table cannot be read.
      const next = new Map(all);
      next.delete(gadget.address);
      return next.set(gadget.address, gadget);
    });

  /** What memory alone can answer, which is what is left when a read fails. */
  const listHeld = (thread: string | undefined) =>
    Effect.map(Ref.get(held), (all) =>
      [...all.values()]
        .toReversed()
        .filter((gadget) => gadget.thread === thread)
        .toSorted((a, b) => b.at - a.at)
        .map(headOf),
    );

  return {
    show: (thread: string | undefined, name: string, source: string) =>
      Effect.gen(function* () {
        if (!gadgetName.test(name)) {
          return yield* Effect.fail(
            new GadgetRefused({
              reason:
                `${name === "" ? "a gadget needs a name" : `${name} is not a gadget name`} — ` +
                "lower case letters, digits and dashes, starting with a letter or a digit: " +
                "cost-table, run-3-latency",
            }),
          );
        }
        if (source.trim() === "") {
          return yield* Effect.fail(
            new GadgetRefused({ reason: "a gadget needs a document — pass the MDX source" }),
          );
        }
        const { code, title } = yield* compileGadget(source);
        // The clock and not `Date.now()`, for the reason `Pages` gives: `at`
        // is what tells one showing of a gadget from the next, and a test that
        // could not control it would assert on a real timestamp.
        const at = yield* Clock.currentTimeMillis;
        const gadget: Gadget = {
          address: gadgetAddress(thread, name),
          ...(thread === undefined ? {} : { thread }),
          name,
          // A document with no heading falls back to the name the agent chose,
          // which is never empty and is already a phrase — `run-3-latency`
          // reads as a tab. The alternative is a tab saying "untitled", which
          // is a word nobody wrote about a document somebody did.
          title: title ?? name,
          at,
          code,
        };
        // Written down, and ignored if it could not be. A gadget that
        // compiled is one this daemon can serve, and refusing it because a row
        // would not write would throw away the half that worked — what is lost
        // is the next restart, which is exactly where this stood before the
        // table existed.
        yield* Effect.ignore(
          attempt("remember the gadget", () => {
            forget.run(gadget.address);
            remember.run(gadget.address, thread ?? null, name, gadget.title, source, at);
          }),
        );
        yield* keep(gadget);
        yield* PubSub.publish(hub, headOf(gadget));
        return gadget;
      }),

    read: (address: string) =>
      Effect.gen(function* () {
        const all = yield* Ref.get(held);
        const found = all.get(address);
        if (found !== undefined) {
          return found;
        }
        const rows = yield* Effect.orElseSucceed(
          attempt("read the gadget", () => readOne.all(address)),
          (): ReadonlyArray<Record<string, unknown>> => [],
        );
        const row = rows[0];
        if (row === undefined) {
          // One cause now, where there were two: a restart used to lose every
          // gadget, and the sentence had to cover it. What is left is a name
          // nobody wrote — a window holding an address from a thread whose
          // gadget was replaced under a different name, most often.
          return yield* Effect.fail(
            new GadgetRefused({
              reason: `no gadget at ${address} — nothing has been written under that name. Ask the agent for it.`,
            }),
          );
        }
        // Compiled here and not on the way up: a daemon that rebuilt every
        // stored gadget at startup would pay for the documents nobody opens,
        // and this is the call that has somebody waiting on it.
        //
        // The title is the stored one rather than the one this compile found.
        // They are the same walk over the same source — but the strip is
        // already drawn from the row, and a tab that disagreed with itself
        // would be a bug nobody could see the cause of.
        const { code } = yield* compileGadget(String(row["source"]));
        const gadget: Gadget = { ...headFrom(row), code };
        yield* keep(gadget);
        return gadget;
      }),

    list: (thread: string | undefined) =>
      // `rowid desc` after `at desc` is the tie-break: two gadgets written
      // inside one millisecond are ordered by which was written second. `at`
      // has millisecond resolution and an agent writing a pair of them does so
      // in a loop, so the tie is the ordinary case rather than the exotic one.
      attempt("list the gadgets", () => readHeads.all(thread ?? null)).pipe(
        Effect.map((rows) => rows.map(headFrom)),
        // Memory is what is left when the table cannot be read — not an empty
        // list, which would say "this thread has none" about a thread that
        // has some.
        Effect.catch(() => listHeld(thread)),
      ),

    changes: () => Stream.fromPubSub(hub),
  };
});

export const layer: Layer.Layer<Gadgets, never, Db> = Layer.effect(Gadgets)(make);
