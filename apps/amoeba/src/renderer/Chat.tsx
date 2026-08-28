import type { ChatConfigOption } from "@awp-kit/protocol";
import * as stylex from "@stylexjs/stylex";
import { useCallback, useEffect, useRef, useState } from "react";
import { Chip } from "./Chip";
import {
  type Asked,
  type Conversation,
  type Item,
  type Ran,
  type Said,
  fold,
  nothing,
  verb,
} from "./conversation";
import { Markdown } from "./Markdown";
import { chatAnswer, chatConfig, chatSend, chatSet, watchChat } from "./daemon";
import { colors, text } from "./tokens.stylex";

// The agent as a conversation rather than as a picture of one.
//
// The pane draws whatever the program on the other end of a pty decided to
// paint. This draws records: a message, a tool call with a status that moves,
// a permission request with buttons on it. What that buys is everything the
// terminal cannot be asked — see chat.ts in the daemon, which carries the
// measurement.
//
// ── an update is a patch, not a row ────────────────────────────────────────
//
// One `cat` arrived as five updates sharing one id: pending with a generic
// title, then the command, then the output, then completed. So the reducer
// below merges by id, and a chunk of text appends to the message above it
// rather than starting a new one. Appending each update as its own row is the
// obvious first version and draws one tool call five times.

/** What a tool's status looks like at a glance. */
const mark = (status: string): string =>
  status === "completed" ? "✓" : status === "failed" ? "✗" : "…";

export const Chat = ({
  project,
  workspace,
}: {
  readonly project: string;
  readonly workspace: string;
}) => {
  const [held, setHeld] = useState<Conversation>(nothing);
  const items = held.items;
  const [draft, setDraft] = useState("");
  const [config, setConfig] = useState<ReadonlyArray<ChatConfigOption>>([]);
  const bottom = useRef<HTMLDivElement>(null);

  useEffect(
    () =>
      watchChat(project, workspace, (update) => {
        setHeld((current) => fold(current, update));
      }),
    [project, workspace],
  );

  useEffect(() => {
    // Asked once, when the panel opens. There is no call that answers "what
    // are my options" on the adapter either — they arrive with the session —
    // so the daemon holds them and this is a read of that.
    let gone = false;
    void chatConfig(project, workspace)
      .then((options) => {
        if (!gone) {
          setConfig(options);
        }
      })
      .catch(() => {
        // A conversation that could not be opened has no settings, and the
        // stream above is where that failure is already said out loud.
      });
    return () => {
      gone = true;
    };
  }, [project, workspace]);

  // How far the conversation has got, as one value.
  //
  // The count alone is not enough: an answer arrives as chunks appended to the
  // message already at the bottom, so the list stops growing while the text
  // still does — and the view would stall halfway through every reply.
  const tail = items.at(-1);
  const grown = `${String(items.length)}:${String(
    tail === undefined
      ? 0
      : tail.kind === "said"
        ? tail.text.length
        : tail.kind === "ran"
          ? tail.output.length
          : 0,
  )}`;

  useEffect(() => {
    // Follow the tail. `block: "end"` rather than a scrollTop assignment, so a
    // person who has scrolled up to read something is not yanked back by the
    // browser's own smooth behaviour fighting theirs.
    if (grown !== "0:0") {
      bottom.current?.scrollIntoView({ block: "end" });
    }
  }, [grown]);

  const say = useCallback(() => {
    const words = draft.trim();
    if (words === "") {
      return;
    }
    setDraft("");
    // Shown immediately rather than waiting for the daemon to echo it back.
    // A message that appears only once the agent has acknowledged it reads as
    // a send button that did nothing.
    setHeld((current) => fold(current, { kind: "message", role: "user", text: words }));
    void chatSend(project, workspace, words);
  }, [draft, project, workspace]);

  return (
    <div {...stylex.props(styles.chat)} data-column-part="chat">
      <div {...stylex.props(styles.scroll)}>
        {items.length === 0 && !held.running ? (
          <p {...stylex.props(styles.nothing)}>nothing said yet</p>
        ) : (
          items.map((item) => (
            <Row key={item.key} item={item} project={project} workspace={workspace} />
          ))
        )}

        {/* A word, not a spinner. The jobs panel's rule holds here for the same
            reason: the word already says it is running, and a spinner beside a
            transcript that is itself moving is one animation too many.

            On the wire rather than inferred from silence, because silence is
            also what an agent that answered with nothing looks like. */}
        {held.running && <p {...stylex.props(styles.working)}>working…</p>}
        {held.stopped !== undefined && (
          <p {...stylex.props(styles.stopped)}>the turn ended: {held.stopped}</p>
        )}
        <div ref={bottom} />
      </div>

      <div {...stylex.props(styles.composer)}>
        <div {...stylex.props(styles.box)}>
          <textarea
            {...stylex.props(styles.input)}
            value={draft}
            rows={2}
            placeholder="say something"
            onChange={(event) => setDraft(event.target.value)}
            onKeyDown={(event) => {
              // The same rule the pane has: Return sends, shift+Return is a
              // newline. A composer where Return inserts a line is one where
              // every message needs a second gesture to leave.
              if (event.key === "Enter" && !event.shiftKey) {
                event.preventDefault();
                say();
              }
            }}
          />
          <div {...stylex.props(styles.under)}>
            <span {...stylex.props(styles.hint)}>
              {draft.includes("\n") ? "shift+return for a new line" : ""}
            </span>
            <button
              type="button"
              data-nav-item
              {...stylex.props(styles.send, draft.trim() === "" && styles.shut)}
              onClick={say}
              disabled={draft.trim() === ""}
            >
              send
            </button>
          </div>
        </div>

        {/* ── what this session is running as ──────────────────────────────

            Under the composer rather than in the agent bar, because these are
            facts about the *session* and the bar is the window's own chrome.
            They also read in the right order down here: what you are about to
            say, and then who is about to answer it.

            One shape for all of them. The adapter answers `mode`, `model`,
            `effort` and `fast` as four selects with the same fields, so there
            is nothing bespoke per setting — and a fifth appearing upstream is
            a row that shows up rather than a thing to add here. */}
        {(config.length > 0 || held.full !== undefined) && (
          <div {...stylex.props(styles.settings)}>
            {config.map((option) => (
              <Chip
                key={option.id}
                id={`chat-${option.id}`}
                label={nameOf(option)}
                title={option.description ?? option.name}
                value={option.currentValue}
                onChange={(value) => {
                  // Painted before the daemon answers, and corrected by the
                  // answer. A select that snaps back for a moment reads as a
                  // control that did not take.
                  setConfig((all) =>
                    all.map((one) =>
                      one.id === option.id ? { ...one, currentValue: value } : one,
                    ),
                  );
                  void chatSet(project, workspace, option.id, value)
                    .then(setConfig)
                    .catch(() => setConfig(config));
                }}
                options={option.values.map((value) => ({ value: value.value, label: value.name }))}
                quiet
              />
            ))}

            <span {...stylex.props(styles.spacer)} />

            {/* Nothing until it is worth saying, which is the status bar's
                rule. A percentage that is on screen from the first message is
                furniture, and teaches the eye to skip the corner it will
                eventually need to look at. */}
            {held.full !== undefined && held.full >= FULL_ENOUGH && (
              <span {...stylex.props(styles.full, held.full >= NEARLY_FULL && styles.warn)}>
                {Math.round(held.full * 100)}% context
              </span>
            )}
          </div>
        )}
      </div>
    </div>
  );
};

/**
 * How a setting reads on its chip.
 *
 * The value's own name, and the setting's name when the value would not say
 * which setting it belongs to. `Manual` and `Opus` name themselves; `On` and
 * `Off` do not, so fast mode says `fast: off`.
 */
const nameOf = (option: ChatConfigOption): string => {
  const current = option.values.find((value) => value.value === option.currentValue);
  const said = current?.name ?? option.currentValue;
  return said === "On" || said === "Off" || said === "Default"
    ? `${option.name.toLowerCase()}: ${said.toLowerCase()}`
    : said;
};

/** Past this, the context figure is worth a person seeing. */
const FULL_ENOUGH = 0.5;

/** Past this, it is worth them minding. */
const NEARLY_FULL = 0.85;

const Row = ({
  item,
  project,
  workspace,
}: {
  readonly item: Item;
  readonly project: string;
  readonly workspace: string;
}) => {
  if (item.kind === "said") {
    return <Message item={item} />;
  }
  if (item.kind === "ran") {
    return <Tool item={item} />;
  }
  return <Permission item={item} project={project} workspace={workspace} />;
};

const Message = ({ item }: { readonly item: Said }) => (
  <div {...stylex.props(styles.item, styles.said, item.role === "thought" && styles.thought)}>
    <span {...stylex.props(styles.who, item.role === "user" && styles.mine)}>
      {item.role === "user" ? "you" : item.role === "thought" ? "thinking" : "agent"}
    </span>
    {/* ── markdown for what the agent wrote, and not for what you wrote ────

        An agent answers in markdown — headings, lists, fenced code — and drawn
        as text that is most of the reply showing its own syntax. `Markdown.tsx`
        already exists for the PR panel and is reused whole: it builds React
        elements rather than HTML, so there is no sanitiser to get right.

        Your own message is drawn as text on purpose. It is exactly what you
        typed, and rendering it would mean a message containing `# ` silently
        becoming a heading — which is a window editing what somebody said. */}
    {item.role === "agent" ? (
      <Markdown>{item.text}</Markdown>
    ) : (
      <p {...stylex.props(styles.words)}>{item.text}</p>
    )}
  </div>
);

const Tool = ({ item }: { readonly item: Ran }) => {
  // Shut by default, and open once for anything that went wrong.
  //
  // Output is usually long and usually uninteresting — the row already says
  // what ran and whether it worked. What a person opens it for is the case
  // where it did not, so that case opens itself.
  const [open, setOpen] = useState(item.status === "failed");

  return (
    <div {...stylex.props(styles.item, styles.ran)}>
      <span {...stylex.props(styles.status, item.status === "failed" && styles.failed)}>
        {mark(item.status)}
      </span>
      <div {...stylex.props(styles.grow)}>
        <button
          type="button"
          data-nav-item
          disabled={item.output === ""}
          onClick={() => setOpen((was) => !was)}
          {...stylex.props(styles.command)}
        >
          <span {...stylex.props(styles.verb)}>{verb(item)}</span>
          <span {...stylex.props(styles.what)}>{item.title}</span>
        </button>
        {open &&
          item.output !== "" && (
            // Plain text, and that is a known gap rather than a choice: a tool
            // that edits a file answers with a patch, and this window already
            // renders patches properly in the diff panel. `@pierre/diffs` is
            // where that goes — see #102 — and it is worth doing there rather
            // than reaching for a second highlighter.
            <pre {...stylex.props(styles.output)}>{item.output}</pre>
          )}
      </div>
    </div>
  );
};

const Permission = ({
  item,
  project,
  workspace,
}: {
  readonly item: Asked;
  readonly project: string;
  readonly workspace: string;
}) => {
  // Answered once. The daemon forgets the request as soon as it is replied to,
  // so a second press is a refusal from the other end — and a row that still
  // looks pressable after it has been answered is one somebody will press.
  const [answered, setAnswered] = useState<string | undefined>(undefined);

  return (
    <div {...stylex.props(styles.item, styles.asked)}>
      <p {...stylex.props(styles.question)}>{item.title}</p>
      {answered === undefined ? (
        <div {...stylex.props(styles.options)}>
          {item.options.map((option) => (
            <button
              key={option.id}
              type="button"
              data-nav-item
              {...stylex.props(
                styles.option,
                option.kind.startsWith("allow") && styles.allow,
                option.kind.startsWith("reject") && styles.reject,
              )}
              onClick={() => {
                setAnswered(option.name);
                void chatAnswer(project, workspace, item.key, option.id);
              }}
            >
              {option.name}
            </button>
          ))}
        </div>
      ) : (
        <p {...stylex.props(styles.answered)}>{answered}</p>
      )}
    </div>
  );
};

const styles = stylex.create({
  chat: {
    display: "flex",
    flexDirection: "column",
    flex: 1,
    minHeight: 0,
    // A page, not a panel. This is the one surface in the window somebody
    // reads for minutes rather than glances at, and it is white in the light
    // theme and the deepest grey in the dark one — see `page` in the tokens.
    backgroundColor: colors.page,
  },
  scroll: {
    flex: 1,
    minHeight: 0,
    overflowY: "auto",
    // The window's rule: a horizontal scrollbar is a layout that was allowed
    // to be wider than the column holding it. Output wraps instead.
    overflowX: "hidden",
    // Room, and more of it than a panel gets. This column is the one somebody
    // reads for minutes at a time rather than glances at, and text pressed
    // against a column edge is what makes a transcript tiring — the gutter is
    // what the eye returns to at the start of every line.
    paddingBlock: "1.25rem",
    paddingInline: "1.25rem",
    display: "flex",
    flexDirection: "column",
    // Between turns, not between lines. A message and the tool call it caused
    // are one thought and want to sit together; two turns want air.
    gap: "1.1rem",
  },
  nothing: { color: colors.muted, fontFamily: text.ui, fontSize: text.small },
  item: {
    // Nothing pops. Every item in this list appears while somebody is looking
    // at the list, which is exactly the case the mandate is about.
    animationName: stylex.keyframes({
      from: { opacity: 0, transform: "translateY(4px)" },
      to: { opacity: 1, transform: "none" },
    }),
    // The window's duration and curve, written out rather than read from
    // `columns.ts`. An identifier inside `stylex.create` is resolved by StyleX
    // and must come from a `.stylex.ts` file — interpolating an ordinary
    // constant is a build error about theming rules, which is not what is
    // wrong. A dynamic style would take it at runtime, and a dynamic style per
    // item in a list is a class per item.
    animationDuration: { default: "260ms", "@media (prefers-reduced-motion: reduce)": "0s" },
    animationTimingFunction: "cubic-bezier(0.32, 0.72, 0, 1)",
  },
  said: { display: "flex", flexDirection: "column", gap: "0.15rem" },
  thought: { opacity: 0.7 },
  who: {
    fontFamily: text.ui,
    fontSize: text.small,
    fontWeight: text.medium,
    color: colors.muted,
  },
  mine: { color: colors.accent },
  words: {
    fontFamily: text.ui,
    fontSize: text.body,
    color: colors.text,
    whiteSpace: "pre-wrap",
    // A flex child will not shrink below its content, and a long unbroken
    // token is content. Both of these, or one URL widens the column.
    minWidth: 0,
    overflowWrap: "anywhere",
  },
  ran: { display: "flex", gap: "0.5rem", alignItems: "flex-start" },
  status: {
    fontFamily: text.mono,
    fontSize: text.small,
    color: colors.muted,
    paddingTop: "0.1rem",
  },
  grow: { flex: 1, minWidth: 0 },
  // A row, and a button, because its output folds. Reset the UA's own button
  // look by long-hand: StyleX drops `border` and `background` shorthands in
  // silence, which leaves macOS's 2px outset bevel around every tool call.
  command: {
    display: "flex",
    gap: "0.4rem",
    alignItems: "baseline",
    width: "100%",
    textAlign: "start",
    padding: 0,
    borderStyle: "none",
    backgroundColor: "transparent",
    cursor: { default: "pointer", ":disabled": "default" },
  },
  /** What sort of thing it was — ui, because it is a word and not an address. */
  verb: {
    flexShrink: 0,
    fontFamily: text.ui,
    fontSize: text.small,
    color: colors.muted,
  },
  /** What it actually did — mono, because it is a command. */
  what: {
    flex: 1,
    minWidth: 0,
    fontFamily: text.mono,
    fontSize: text.small,
    color: colors.text,
    overflowWrap: "anywhere",
  },
  failed: { color: colors.warn },
  answered: { fontFamily: text.ui, fontSize: text.small, color: colors.muted },
  working: {
    fontFamily: text.ui,
    fontSize: text.small,
    color: colors.muted,
    // It appears and disappears, so it moves. The mandate is the window's, and
    // a line that blinks into a transcript somebody is reading is the case it
    // exists for.
    animationName: stylex.keyframes({
      from: { opacity: 0 },
      to: { opacity: 1 },
    }),
    animationDuration: { default: "260ms", "@media (prefers-reduced-motion: reduce)": "0s" },
    animationTimingFunction: "cubic-bezier(0.32, 0.72, 0, 1)",
  },
  stopped: { fontFamily: text.ui, fontSize: text.small, color: colors.muted },
  output: {
    fontFamily: text.mono,
    fontSize: text.small,
    color: colors.muted,
    whiteSpace: "pre-wrap",
    overflowWrap: "anywhere",
    marginTop: "0.25rem",
    maxHeight: "12rem",
    overflowY: "auto",
  },
  asked: {
    borderStyle: "solid",
    borderWidth: 1,
    borderColor: colors.accent,
    borderRadius: "0.375rem",
    padding: "0.6rem",
    display: "flex",
    flexDirection: "column",
    gap: "0.5rem",
    backgroundColor: colors.surface,
  },
  question: {
    fontFamily: text.mono,
    fontSize: text.small,
    color: colors.text,
    overflowWrap: "anywhere",
  },
  options: { display: "flex", gap: "0.4rem", flexWrap: "wrap" },
  option: {
    fontFamily: text.ui,
    fontSize: text.small,
    padding: "0.25rem 0.6rem",
    borderRadius: "0.25rem",
    borderStyle: "solid",
    borderWidth: 1,
    borderColor: colors.border,
    backgroundColor: colors.raised,
    color: colors.text,
    cursor: "pointer",
  },
  allow: { borderColor: colors.live, color: colors.live },
  reject: { borderColor: colors.warn, color: colors.warn },
  // ── the composer ────────────────────────────────────────────────────────
  //
  // One box with the button inside it rather than a field and a button side by
  // side. Two controls in a row makes the field look short and puts the send
  // where a person's eye is not — at the end of a line they are not looking at
  // — where inside the box it sits under the last word they typed.
  composer: {
    padding: "0.9rem 1.25rem 1.1rem",
    borderTopStyle: "solid",
    borderTopWidth: 1,
    borderTopColor: colors.border,
    backgroundColor: colors.page,
  },
  box: {
    display: "flex",
    flexDirection: "column",
    gap: "0.4rem",
    padding: "0.55rem 0.6rem 0.5rem",
    borderStyle: "solid",
    borderWidth: 1,
    // The whole box takes the focus ring, because the whole box is the
    // control. `:focus-within` and not `:focus` — the thing being focused is
    // the textarea inside it.
    borderColor: { default: colors.border, ":focus-within": colors.accent },
    borderRadius: "0.5rem",
    // One step off the page, so the box somebody types into is visibly a
    // control sitting on the document rather than part of it.
    backgroundColor: colors.surface,
    transitionProperty: "border-color",
    transitionDuration: { default: "160ms", "@media (prefers-reduced-motion: reduce)": "0s" },
  },
  input: {
    width: "100%",
    minWidth: 0,
    resize: "none",
    fontFamily: text.ui,
    fontSize: text.body,
    lineHeight: 1.5,
    color: colors.text,
    backgroundColor: "transparent",
    borderStyle: "none",
    padding: 0,
    outline: "none",
  },
  under: { display: "flex", alignItems: "center", gap: "0.5rem" },
  /** What Return does, said once and quietly rather than in the placeholder. */
  hint: { flex: 1, minWidth: 0, fontFamily: text.ui, fontSize: text.small, color: colors.muted },
  send: {
    fontFamily: text.ui,
    fontSize: text.small,
    fontWeight: text.medium,
    padding: "0.25rem 0.7rem",
    borderRadius: "0.3rem",
    borderStyle: "none",
    backgroundColor: colors.accent,
    color: colors.base,
    cursor: "pointer",
  },
  /** Present and plainly unavailable, rather than gone. */
  shut: { backgroundColor: colors.border, color: colors.muted, cursor: "default" },
  settings: {
    display: "flex",
    alignItems: "center",
    gap: "0.35rem",
    marginTop: "0.55rem",
    // Wrapping rather than scrolling: this is inside the composer, and the
    // window's rule is that nothing grows a sideways scrollbar. Four chips in
    // a narrow agent column become two rows.
    flexWrap: "wrap",
  },
  spacer: { flex: 1 },
  full: { fontFamily: text.ui, fontSize: text.small, color: colors.muted },
  warn: { color: colors.warn },
});
