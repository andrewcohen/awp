import { Context, Data, Effect, Layer, Schema } from "effect";
import { ChildProcess, ChildProcessSpawner } from "effect/unstable/process";
import { capture, said } from "./run";

// Turning "add tiered discounts to checkout" into the arguments for a workspace.
//
// One headless model call, answering with JSON. Ported from the Go
// implementation's workspace_intent.go, whose shape is worth taking whole
// because three of its decisions are not obvious.
//
// ── the model chooses, it does not author ──────────────────────────────────
// Every field is checked against something local before it is used. That is
// what makes it safe to act on an answer nobody read first.
//
// ── the JSON is fished out, not parsed ─────────────────────────────────────
// "Answer with JSON and nothing else" is a request, not a guarantee. Measured
// 2026-08-26: with a global CLAUDE.md in place, a reply came back beginning
// "Dearest Mister Duck," before the object, and another wrapped it in a ```json
// fence. So the outermost braces are located and the rest discarded.
//
// ── the flags are a trap ───────────────────────────────────────────────────
// `--tools`, `--mcp-config` and `--allowed-tools` are **variadic**, so a prompt
// that follows one is swallowed as another value for it. `claude -p --tools ""
// "$PROMPT"` makes the prompt a tool name, leaves no prompt, and exits 1 with
// empty output. Every flag here is written `--flag=value` for that reason, and
// the prompt is always last.
//
// Measured while getting that wrong three times: disabling tools does **not**
// make the call faster (12.6s vs 13.7s median over four runs each — inside the
// noise). It is passed because a naming call that cannot reach for Bash is one
// that cannot do anything surprising, which is a better reason than speed.

export class IntentError extends Data.TaggedError("IntentError")<{
  readonly reason: string;
  readonly cause?: unknown;
}> {}

/**
 * What the model is asked for.
 *
 * `name` is a directory and a jj workspace name; `label` is what the sidebar
 * shows; `prompt` is what gets typed into the new agent session.
 */
export const Intent = Schema.Struct({
  name: Schema.String,
  label: Schema.String,
  prompt: Schema.String,
});

export type Intent = (typeof Intent)["Type"];

export class WorkspaceIntent extends Context.Service<
  WorkspaceIntent,
  {
    readonly resolve: (description: string, project: string) => Effect.Effect<Intent, IntentError>;
  }
>()("awp/WorkspaceIntent") {}

/**
 * How long to wait.
 *
 * Generous for a one-shot prompt that answers in about seven seconds, and short
 * enough that a wedged or offline agent is a failure rather than a hang. The
 * spread is real: four timed runs came back 11.0, 11.2, 13.9 and 28.0 seconds,
 * so a bound near the median would fail on a bad draw.
 */
export const TIMEOUT = "45 seconds";

/** Small and fast, because this is an extraction and not a piece of work. */
const MODEL = "haiku";

const prompt = (description: string, project: string): string =>
  [
    "Turn a developer's description of what they want to work on into the",
    "arguments for creating a workspace for it.",
    "",
    "What they wrote:",
    description,
    "",
    `The project is ${project}.`,
    "",
    "Answer with a JSON object and nothing else — no prose, no code fence:",
    '{"name": "...", "label": "...", "prompt": "..."}',
    "",
    "  name    A directory name: lowercase, hyphen-separated, letters digits",
    "          and hyphens only, a handful of words. It has to work as a path.",
    "  label   The same thing as a short human-readable phrase. This is what",
    "          the sidebar shows, so it may have spaces and capitals.",
    "  prompt  What to tell the coding agent that will do the work. Write it as",
    "          an instruction to that agent. Keep the developer's meaning and",
    "          their specifics — a PR number, a file, a symbol they named. Do",
    "          not invent requirements they did not state.",
  ].join("\n");

/**
 * The JSON object inside whatever came back.
 *
 * Outermost braces rather than parsing the whole reply, because the reply is
 * not reliably only JSON. See the note at the top.
 */
export const findObject = (output: string): unknown => {
  const start = output.indexOf("{");
  const end = output.lastIndexOf("}");
  if (start < 0 || end < start) {
    return undefined;
  }
  try {
    return JSON.parse(output.slice(start, end + 1)) as unknown;
  } catch {
    return undefined;
  }
};

/**
 * A directory name, whatever the model said.
 *
 * Re-derived locally rather than trusted: the model is choosing, and this is
 * the check that makes acting on its choice safe. The rules are the Go
 * implementation's `NormalizeName` — lowercase, `_` and spaces to `-`,
 * everything outside `[a-z0-9-]` to `-`, runs collapsed, ends trimmed.
 */
export const slug = (text: string, words = 5, max = 48): string => {
  const kept = text.trim().split(/\s+/u).slice(0, words).join(" ");
  const cleaned = kept
    .toLowerCase()
    .replaceAll("_", "-")
    .replaceAll(/[^a-z0-9-]+/gu, "-")
    .replaceAll(/-+/gu, "-")
    .replaceAll(/^-|-$/gu, "");
  return cleaned.length > max ? cleaned.slice(0, max).replaceAll(/-+$/gu, "") : cleaned;
};

const text = (value: unknown): string => (typeof value === "string" ? value.trim() : "");

/** The model's answer, with every field checked against something local. */
export const validate = (raw: unknown, description: string): Intent | undefined => {
  if (typeof raw !== "object" || raw === null) {
    return undefined;
  }
  const reply = raw as Record<string, unknown>;

  // Re-slugged, not trusted. A model that answered "Tiered Discounts!" would
  // otherwise become a directory name with a capital and an exclamation mark.
  const name = slug(text(reply["name"]) === "" ? description : text(reply["name"]));
  if (name === "") {
    return undefined;
  }
  return {
    name,
    // The typed text is the fallback for both, so a model that answered only
    // `name` still produces a complete result rather than blank fields.
    label: text(reply["label"]) === "" ? description.trim() : text(reply["label"]),
    prompt: text(reply["prompt"]) === "" ? description.trim() : text(reply["prompt"]),
  };
};

const make = Effect.gen(function* () {
  const spawner = yield* ChildProcessSpawner.ChildProcessSpawner;

  return {
    resolve: (description: string, project: string) =>
      Effect.gen(function* () {
        const asked = description.trim();
        if (asked === "") {
          return yield* Effect.fail(
            new IntentError({ reason: "nothing typed — describe what you want to work on" }),
          );
        }

        const captured = yield* capture(
          spawner,
          ChildProcess.make("claude", [
            "-p",
            `--model=${MODEL}`,
            // `=` and not a space. See the note at the top: this flag is
            // variadic and eats the prompt otherwise.
            "--tools=",
            prompt(asked, project),
          ]),
        ).pipe(
          Effect.timeoutOrElse({
            duration: TIMEOUT,
            orElse: () =>
              Effect.fail(new IntentError({ reason: `claude did not answer within ${TIMEOUT}` })),
          }),
          Effect.mapError((error) =>
            error instanceof IntentError
              ? error
              : new IntentError({ reason: "could not run claude", cause: error }),
          ),
        );

        if (captured.exitCode !== 0) {
          return yield* Effect.fail(new IntentError({ reason: `claude: ${said(captured)}` }));
        }

        const found = validate(findObject(captured.stdout), asked);
        if (found === undefined) {
          return yield* Effect.fail(
            new IntentError({
              reason: `claude did not answer with usable JSON: ${said(captured)}`,
            }),
          );
        }
        return found;
      }),
  };
});

export const layer = Layer.effect(WorkspaceIntent)(make);
