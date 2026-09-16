import type { Message, Thread } from "@awp-kit/protocol";
import * as stylex from "@stylexjs/stylex";
import { useState } from "react";
import { useMessages } from "../data/useMessages";
import { endLabel, endsOf, halfFor } from "./messaging";
import { typeset } from "../design/typeset";
import { colors, space, text } from "../design/tokens.stylex";

// What the agents have said to each other.
//
// ── a record, not a queue ──────────────────────────────────────────────────
//
// Nothing here is actionable and that is the design: a message is delivered
// without anybody approving it, so this is where you find out what was said,
// not where you decide whether it may be. The approval step was considered and
// dropped — what it bought was that nothing surprising happens, and what it
// cost was a chore on every exchange. The guard that replaced it is a cap on
// how much one thread may say in an hour, which needs no screen.
//
// So the one thing this has to do well is read in order, and the one mark worth
// drawing is whether the recipient has actually picked it up. A message can sit
// unread for a long time legitimately — the agent it is for is mid-turn, and
// nothing interrupts a turn — so `waiting` is a state rather than a warning,
// and it is muted rather than coloured.
//
// ── grouped by thread, because that is the only grouping it has ────────────
//
// A message never leaves its thread, so the thread is not one axis among
// several — it is the whole address space. Sender and recipient inside a group
// are then just two of its checkouts, and the pair reads as a direction rather
// than as two unrelated names.

const styles = stylex.create({
  // ── no padding on the TOP of a scroller with a sticky child in it ───────
  //
  // `position: sticky` pins to the scrollport, which is the padding box — so a
  // `padding-top` here is a gap the heading is pinned *below*, and everything
  // scrolling past shows through it. That is the bleed above the heading: not a
  // sizing mistake on the heading at all, a strip of the scroller the heading
  // was never covering.
  //
  // So the top gutter moves onto the heading, where it is background rather
  // than empty space. The other three stay.
  list: {
    flex: 1,
    minHeight: 0,
    overflowY: "auto",
    paddingLeft: space.gutter,
    paddingRight: space.gutter,
    paddingBottom: space.gutter,
  },

  empty: {
    padding: space.gutter,
    color: colors.muted,
    // Two lines, and the second one is the part worth saying: an empty list
    // here is the ordinary state and says nothing is wrong. Without the
    // sentence it reads as a feature that is not working.
    textAlign: "center",
  },
  quiet: { color: colors.muted, fontSize: text.small },

  group: { marginBottom: "1.25rem" },
  thread: {
    margin: 0,
    color: colors.muted,
    position: "sticky",
    top: 0,
    // A sticky element makes its own stacking context but does not win on
    // paint order: every row is later in the DOM, so without this they draw
    // over the heading they are sliding under.
    zIndex: 1,
    backgroundColor: colors.surface,
    // The scroller's top gutter, moved here — see `list`. Padding rather than
    // the margin this had, because a margin is transparent and a row scrolling
    // through it is the same bleed one gap lower.
    paddingTop: space.gutter,
    paddingBottom: "0.35rem",
  },

  message: {
    display: "flex",
    flexDirection: "column",
    gap: "0.25rem",
    paddingTop: "0.5rem",
    paddingBottom: "0.5rem",
    borderTopWidth: 1,
    borderTopStyle: "solid",
    borderTopColor: colors.border,
  },
  head: { display: "flex", alignItems: "baseline", gap: "0.5rem", flexWrap: "wrap" },
  // The direction, as one phrase. Mono because both halves are identifiers and
  // the arrow between them is only legible if they line up.
  route: { color: colors.text },
  // `white-space: pre-wrap` and not markdown. A message is a few lines one
  // agent typed for another, and rendering it as a document would be this
  // window deciding what somebody else's prose meant — the same argument that
  // keeps the body out of the recipient's prompt.
  //
  // `maxWidth` in characters rather than on the dialog, which is the whole
  // reason the dialog can be wide: a line of prose past about 70 characters is
  // one the eye loses its place returning from, and a narrow *window* is the
  // wrong way to enforce that — it shrinks the headings and the routes too.
  body: { margin: 0, whiteSpace: "pre-wrap", color: colors.text, maxWidth: "66ch" },
});

/** `14:32`, or `Sep 15 14:32` for anything that is not today. */
const when = (at: number, now: number): string => {
  const sent = new Date(at);
  const today = new Date(now);
  const sameDay =
    sent.getFullYear() === today.getFullYear() &&
    sent.getMonth() === today.getMonth() &&
    sent.getDate() === today.getDate();
  const clock = sent.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
  return sameDay
    ? clock
    : `${sent.toLocaleDateString(undefined, { month: "short", day: "numeric" })} ${clock}`;
};

/**
 * Where a message is up to, as a phrase rather than a badge.
 *
 * Three states and only two of them are ever interesting. `read` is the
 * ordinary end and says nothing, so it draws nothing at all — a mark on every
 * row is a mark that carries no information.
 */
const mark = (message: Message): string | undefined => {
  if (message.readAt !== undefined) {
    return undefined;
  }
  return message.notifiedAt === undefined ? "waiting — that agent is busy" : "delivered, unread";
};

export function Messages({ threads }: { readonly threads: ReadonlyArray<Thread> }) {
  const { messages, failure } = useMessages();

  // The titles, so a group has a name rather than an id. A message outlives the
  // thread being archived — the store cascades on delete and archiving is not
  // one — so a title that cannot be found is drawn as the id rather than as
  // nothing.
  // Plain, not `useMemo`: the React Compiler runs over this tree and
  // react-doctor refuses manual memoization in code it manages.
  const titles = new Map(threads.map((thread) => [thread.id, thread.title]));

  // ── newest first, inside a group as well as between them ────────────────
  //
  // This read forwards within a thread at first, on the analogy of a chat. The
  // analogy is wrong: a chat is something you are *in*, so you are already at
  // the bottom of it and the next line arrives under your eye. Nothing here
  // autoscrolls and nobody is mid-conversation — this is opened cold to answer
  // "what just happened", and the answer to that is at the top.
  //
  // So the daemon's own order is kept all the way down, and the cost is a
  // two-line exchange read bottom-up. That is the smaller cost: the alternative
  // puts the thing somebody opened the panel for at the end of a scroll.
  const grouped = new Map<string, Message[]>();
  for (const message of messages) {
    grouped.set(message.thread, [...(grouped.get(message.thread) ?? []), message]);
  }
  // The half to draw is decided per group — see `halfFor`. A thread whose
  // checkouts all share a workspace name draws `grove → redwood`, which is the
  // ordinary case and the one this got wrong: every row read
  // `testing-multi → testing-multi`.
  const groups = [...grouped].map(([thread, held]) => ({
    thread,
    held,
    half: halfFor(endsOf(held)),
  }));

  // Read once, in a lazy initializer rather than during the render: the clock
  // is impure and a component that read it on every pass would redraw every
  // timestamp whenever anything else changed. What it costs is a viewer left
  // open past midnight still calling yesterday "today", which is a dialog
  // somebody opens and closes.
  const [now] = useState(() => Date.now());

  if (failure !== undefined) {
    return (
      <div {...stylex.props(typeset.prose, styles.empty)}>
        <div>no daemon</div>
        <div {...stylex.props(styles.quiet)}>{failure}</div>
      </div>
    );
  }

  if (messages.length === 0) {
    return (
      <div {...stylex.props(typeset.prose, styles.empty)}>
        <div>nothing said yet</div>
        <div {...stylex.props(styles.quiet)}>
          agents in the same thread reach each other with awp_message
        </div>
      </div>
    );
  }

  return (
    <div {...stylex.props(typeset.prose, styles.list)}>
      {groups.map((group) => (
        <section key={group.thread} {...stylex.props(styles.group)}>
          <h3 {...stylex.props(typeset.subhead, styles.thread)}>
            {titles.get(group.thread) ?? group.thread}
          </h3>
          {group.held.map((message) => {
            const state = mark(message);
            return (
              <article key={message.id} {...stylex.props(styles.message)}>
                <div {...stylex.props(styles.head)}>
                  <span {...stylex.props(typeset.address, styles.route)}>
                    {endLabel(message.from, group.half)} → {endLabel(message.to, group.half)}
                  </span>
                  <span {...stylex.props(styles.quiet)}>{when(message.sentAt, now)}</span>
                  {state !== undefined && <span {...stylex.props(styles.quiet)}>{state}</span>}
                </div>
                <p {...stylex.props(styles.body)}>{message.body}</p>
              </article>
            );
          })}
        </section>
      ))}
    </div>
  );
}
