import type { PullRequest } from "@awp-kit/protocol";
import { ArrowSquareOutIcon } from "@phosphor-icons/react/ArrowSquareOut";
import { ArrowsClockwiseIcon } from "@phosphor-icons/react/ArrowsClockwise";
import * as stylex from "@stylexjs/stylex";
import { useState } from "react";
import { Markdown } from "./Markdown";
import { repair } from "./daemon";
import { colors, space, text } from "./tokens.stylex";
import { usePullRequest } from "./usePullRequest";

// The pull request the open workspace is about.
//
// First among the panels when there is one, and absent otherwise — see
// `Accessory`. That is the whole of its placement argument: a review workspace
// exists *because* of a pull request, so while one is open the PR is the subject
// and the diff is a way of reading it. In every other workspace this tab does
// not exist at all, rather than sitting there saying "no pull request", which
// would be a permanent empty room in the one column a person switches most.
//
// ── what it shows, and in what order ──────────────────────────────────────
//
//   #2418 the title            the identity, and the only accent on the panel
//   open · +120 −8 · 3 files   what it is and how big — the reviewer's first
//                              question, and the one the diff cannot answer
//                              until it has been read
//   ci · review · merge        the three states that decide the next move
//   base ← head                where it lands, which is the stack in one line
//   the description            as the author wrote it
//   the conversation           what has already been said, so a reviewer does
//                              not repeat it
//
// **The description is markdown, and is rendered as markdown.** It was
// preformatted text first, on the argument that a renderer is a dependency
// carried for one panel — and what that actually looked like was `## Summary`
// and `- [ ] done` as literal characters, which is most of a PR body. See
// `Markdown.tsx` for why the renderer is a library rather than a hundred lines
// here.

const styles = stylex.create({
  panel: { display: "flex", flexDirection: "column", height: "100%", minHeight: 0 },
  body: {
    flex: 1,
    minHeight: 0,
    overflowY: "auto",
    overflowX: "hidden",
    padding: `0.6rem ${space.gutter}`,
  },
  quiet: { color: colors.muted, fontSize: text.small },
  trouble: { color: colors.warn, fontSize: text.small, lineHeight: 1.5 },

  head: { display: "flex", alignItems: "baseline", gap: "0.4rem" },
  // The number is the one accent on this panel, and it earns it the way the
  // sidebar's does: it is the thing pointing outside the window, and there is
  // exactly one of it here rather than one per row.
  number: { flexShrink: 0, color: colors.accent, fontFamily: text.mono, fontSize: text.small },
  title: { fontSize: text.lead, fontWeight: text.medium, lineHeight: 1.35 },
  // What pushes the controls to the far edge. Its own element rather than
  // `margin-inline-start: auto` on the first button, because there are two of
  // them and the rule would have to know which is first.
  spacer: { flex: 1 },
  // An icon-only control: square, so a row of them is a row of equal targets,
  // and no gap because there is nothing beside the glyph.
  icon: {
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    width: "1.4rem",
    height: "1.4rem",
    padding: 0,
    backgroundColor: "transparent",
    borderStyle: "none",
    borderRadius: "0.2rem",
    color: colors.muted,
    font: "inherit",
    cursor: "pointer",
    ":hover": { color: colors.text },
  },
  // Reading. Said by dimming rather than by a word or a spinner — the panel's
  // rule, and the jobs panel's before it.
  busy: { color: colors.border, cursor: "default" },
  open: {
    display: "flex",
    alignItems: "center",
    gap: "0.2rem",
    padding: 0,
    backgroundColor: "transparent",
    borderStyle: "none",
    color: colors.muted,
    font: "inherit",
    fontSize: text.small,
    cursor: "pointer",
    ":hover": { color: colors.text },
  },

  facts: {
    display: "flex",
    flexWrap: "wrap",
    gap: "0.5rem",
    marginBlockStart: "0.4rem",
    color: colors.muted,
    fontSize: text.small,
  },
  mono: { fontFamily: text.mono },
  added: { color: colors.live },
  removed: { color: colors.warn },
  bad: { color: colors.warn },
  waiting: { color: colors.waiting },
  live: { color: colors.live },

  // Its own band, because it is the one thing here a person may need to act on.
  // `waiting` rather than `warn`: nothing is broken — the checkout is simply
  // behind, which is an ordinary consequence of somebody pushing.
  // The band, and the three things in it: a glyph, a sentence, an action. Its
  // own ground so it reads as a notice rather than as the first line of the
  // pull request, and `waiting` rather than `warn` because nothing is broken —
  // a checkout goes behind whenever somebody pushes.
  moved: {
    display: "flex",
    alignItems: "center",
    gap: "0.4rem",
    marginBlockEnd: "0.6rem",
    padding: "0.4rem 0.5rem",
    backgroundColor: colors.surface,
    borderStartStartRadius: "0.25rem",
    borderStartEndRadius: "0.25rem",
    borderEndStartRadius: "0.25rem",
    borderEndEndRadius: "0.25rem",
    color: colors.waiting,
    fontSize: text.small,
    lineHeight: 1.4,
  },
  // The sentence takes the room and wraps; the button keeps its width. `flex: 1`
  // with `minWidth: 0` is the pair — either alone is the bug that grows a
  // horizontal scrollbar. See the layout rules in AGENTS.md.
  movedSays: { flex: 1, minWidth: 0 },
  button: {
    flexShrink: 0,
    padding: "0.1rem 0.4rem",
    backgroundColor: "transparent",
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.2rem",
    color: colors.text,
    font: "inherit",
    fontSize: text.small,
    cursor: "pointer",
  },
  section: {
    marginBlockStart: "0.9rem",
    color: colors.muted,
    fontSize: text.small,
    fontWeight: text.strong,
  },
  // Only the space around it. The markdown renderer owns everything inside —
  // `pre-wrap` here would keep the source's own newlines *as well as* the
  // paragraphs it makes of them, and double-space the whole body.
  description: { marginBlockStart: "0.5rem" },
  remark: {
    marginBlockStart: "0.5rem",
    paddingInlineStart: "0.5rem",
    borderInlineStartWidth: 2,
    borderInlineStartStyle: "solid",
    borderInlineStartColor: colors.border,
  },
  who: { display: "flex", gap: "0.35rem", color: colors.muted, fontSize: text.small },
  // What the last press said. One line, and it is this panel's words rather
  // than somebody else's, so nothing about it is markdown.
  said: { marginBlockEnd: "0.6rem", color: colors.muted, fontSize: text.small },
  // The prompt, before it is anybody's problem but the reader's.
  compose: { display: "flex", flexDirection: "column", gap: "0.35rem", marginBlockEnd: "0.7rem" },
  // What was said, quoted. `pre-wrap` because the line breaks are part of the
  // instruction, and `muted` because it is a record rather than something to
  // act on — the acting is the agent's now.
  box: {
    padding: "0.4rem 0.45rem",
    maxHeight: "14rem",
    overflowY: "auto",
    backgroundColor: colors.base,
    borderRadius: "0.25rem",
    color: colors.muted,
    fontFamily: text.mono,
    fontSize: text.small,
    lineHeight: 1.5,
    whiteSpace: "pre-wrap",
  },
});

/** The three states that decide a next move, and only when they say something. */
const facts = (pr: PullRequest): ReadonlyArray<{ says: string; tone: keyof typeof tones }> => {
  const out: Array<{ says: string; tone: keyof typeof tones }> = [];
  if (pr.draft) {
    out.push({ says: "draft", tone: "waiting" });
  }
  if (pr.ci !== "none") {
    out.push({
      says: `ci ${pr.ci}`,
      tone: pr.ci === "failing" ? "bad" : pr.ci === "pending" ? "waiting" : "live",
    });
  }
  if (pr.review !== "none") {
    out.push({
      says: pr.review.replaceAll("-", " "),
      tone:
        pr.review === "approved" ? "live" : pr.review === "changes-requested" ? "bad" : "waiting",
    });
  }
  // Only the two that mean something a person acts on. `blocked` on GitHub
  // usually means "not approved yet", which the line above already said.
  if (pr.mergeState === "dirty") {
    out.push({ says: "conflicts", tone: "bad" });
  }
  if (pr.mergeState === "behind") {
    out.push({ says: "behind its base", tone: "waiting" });
  }
  return out;
};

const tones = { bad: styles.bad, waiting: styles.waiting, live: styles.live } as const;

/**
 * Whether there is anything to repair.
 *
 * The same set the prompt is built from — see `repair.ts` — asked here only to
 * decide whether to offer the button. Deliberately not a second copy of that
 * file's rules: this answers "is something wrong", and *what to say about it* is
 * the daemon's, which is why the button fetches a sentence rather than composing
 * one.
 */
const wrong = (pr: PullRequest): boolean =>
  pr.state === "open" &&
  (pr.ci === "failing" ||
    pr.mergeState === "dirty" ||
    pr.mergeState === "behind" ||
    pr.review === "changes-requested" ||
    pr.hasReviewComments);

/** The shortest true line about why the button is there. */
const wrongSays = (pr: PullRequest): string =>
  [
    pr.ci === "failing" ? "CI is failing" : undefined,
    pr.mergeState === "dirty" ? "conflicts with its base" : undefined,
    pr.mergeState === "behind" ? "behind its base" : undefined,
    pr.review === "changes-requested"
      ? "changes requested"
      : pr.hasReviewComments
        ? "a reviewer left notes"
        : undefined,
    pr.moved ? "this checkout is behind" : undefined,
  ]
    .filter((one): one is string => one !== undefined)
    .join(" · ");

const when = (at: Date | undefined): string =>
  at === undefined ? "" : at.toISOString().slice(0, 10);

export function Pr({
  project,
  number,
}: {
  readonly project: string | undefined;
  /** Which pull request. The panel is not rendered at all without one. */
  readonly number: number | undefined;
}) {
  const { pr, reading, failure, reload } = usePullRequest(project, number);
  // The repair prompt, once asked for: `undefined` is "not asked", the empty
  // string is "asked, and there is nothing wrong". Editable, because it is going
  // to be typed at an agent and the person whose branch it is may want it to say
  // something else.
  const [prompt, setPrompt] = useState<string | undefined>();
  const [owner, setOwner] = useState(true);
  const [asking, setAsking] = useState(false);
  /** Whatever the last press had to say — `sent`, or a refusal. */
  const [said, setSaid] = useState<string | undefined>();

  // ── stale beats empty ────────────────────────────────────────────────────
  //
  // A failure only replaces the panel when there is nothing to replace: with a
  // pull request already on screen the sentence goes *beside* it, because a
  // refresh that failed has not made what was there untrue.
  if (failure !== undefined && pr === undefined) {
    return (
      <div {...stylex.props(styles.panel)}>
        <div {...stylex.props(styles.body, styles.trouble)}>{failure}</div>
      </div>
    );
  }
  // The one genuinely blank state left: a pull request nobody has looked at yet.
  // Everything else keeps the last answer and says `refreshing` in the header —
  // see `usePullRequest`, and the atoms behind it.
  if (pr === undefined) {
    return (
      <div {...stylex.props(styles.panel)}>
        <div {...stylex.props(styles.body, styles.quiet)}>{reading ? "refreshing" : ""}</div>
      </div>
    );
  }
  if (pr === null) {
    // Said plainly. A thread can name a pull request that was deleted, or that
    // lives in a repository `gh` cannot reach from here.
    return (
      <div {...stylex.props(styles.panel)}>
        <div {...stylex.props(styles.body, styles.quiet)}>
          gh has no pull request #{number} in {project}
        </div>
      </div>
    );
  }

  return (
    <div {...stylex.props(styles.panel)}>
      <div {...stylex.props(styles.body)}>
        {/* A refresh that failed, over content that is still true. Said here
            rather than in place of it. */}
        {failure !== undefined && <div {...stylex.props(styles.trouble)}>{failure}</div>}

        {/* ── what is wrong with it, and what to say about that ─────────────
        
            Above the title, because it is the only thing on this panel asking to
            be acted on and a person reading the description has already scrolled
            past it. While a review is open this panel sits beside the diff.
        
            The button does not send anything. It asks the daemon for the
            sentence and puts it in a box — see `repair.ts` for why: on your own
            pull request that sentence tells an agent to resolve conflicts, fix
            CI and push, and the person whose branch it is should read it first.
            The tone differs on somebody else's, and the label says which. */}
        {(pr.moved || wrong(pr)) && (
          <div {...stylex.props(styles.moved)}>
            <span {...stylex.props(styles.movedSays)}>{wrongSays(pr)}</span>
            <button
              type="button"
              title="tell the agent about this"
              disabled={asking}
              onClick={() => {
                setAsking(true);
                setPrompt(undefined);
                setSaid(undefined);
                repair(project ?? "", pr.number)
                  .then((done) => {
                    setPrompt(done.prompt);
                    setOwner(done.mine);
                    setSaid(
                      done.prompt === ""
                        ? "nothing to repair"
                        : `sent to ${done.workspace ?? "the agent"}`,
                    );
                    return done;
                  })
                  .catch((error: unknown) => setSaid(String(error)))
                  .finally(() => setAsking(false));
              }}
              {...stylex.props(styles.button)}
            >
              {asking ? "sending" : "repair"}
            </button>
          </div>
        )}

        {/* What was said, after the fact. Read-only: the decision was the press,
            and a box to edit afterwards would be offering to change a message
            the agent already has. Monospace, because the line breaks in it are
            part of what the agent was told. */}
        {said !== undefined && <div {...stylex.props(styles.said)}>{said}</div>}
        {prompt !== undefined && prompt !== "" && (
          <div {...stylex.props(styles.compose)}>
            <div {...stylex.props(styles.quiet)}>
              {owner
                ? "asked to fix it and push"
                : "asked to investigate and report, and to change nothing"}
            </div>
            <div {...stylex.props(styles.box)}>{prompt}</div>
          </div>
        )}

        <div {...stylex.props(styles.head)}>
          <span {...stylex.props(styles.number)}>#{pr.number}</span>

          {/* The number reads left and the controls read right, with the row
              between them empty. They were adjacent, which put a
              `#2418 refresh github` cluster in the corner that scans as three
              words of one thing — and the number is an identity, not a label
              on a button. */}
          <span {...stylex.props(styles.spacer)} />

          {/* The cache is what makes switching tabs instant, and this is how a
              person says "that is not what it says any more" — a description
              gets edited and a comment arrives while somebody is reading.
              
              Icon only. The word said nothing the arrows do not, and it was the
              wider half of a control in a 280px column. `aria-label` is not
              optional once the text is gone: a button whose whole content is an
              `aria-hidden` icon has no accessible name at all. */}
          <button
            type="button"
            aria-label={reading ? "refreshing this pull request" : "read this pull request again"}
            title={reading ? "refreshing" : "read this pull request again"}
            disabled={reading}
            onClick={() => reload(true)}
            {...stylex.props(styles.icon, reading && styles.busy)}
          >
            <ArrowsClockwiseIcon size={14} aria-hidden />
          </button>
          {/* The one word worth saying, and only while it is true. "refresh" on
              a button says what the arrows already say; "refreshing" says
              something the arrows cannot — that it is happening now. */}
          {reading && <span {...stylex.props(styles.quiet)}>refreshing</span>}
          {/* The one control: the browser. Everything else here is reading. */}
          <button
            type="button"
            title={pr.url}
            // `noopener`, which is not a nicety here: without it the page that
            // opens gets a handle on this window through `opener`, and the page
            // is GitHub rendering somebody else's pull request body.
            onClick={() => window.open(pr.url, "_blank", "noopener,noreferrer")}
            {...stylex.props(styles.open)}
          >
            <ArrowSquareOutIcon size={13} aria-hidden />
            github
          </button>
        </div>
        <div {...stylex.props(styles.title)}>{pr.title}</div>

        <div {...stylex.props(styles.facts)}>
          <span>{pr.state}</span>
          <span>{pr.author}</span>
          {/* The size, which is the reviewer's first question and the one the
              diff cannot answer until it has been read. */}
          <span>
            <span {...stylex.props(styles.added)}>+{pr.additions}</span>{" "}
            <span {...stylex.props(styles.removed)}>−{pr.deletions}</span>
            {` · ${pr.files} ${pr.files === 1 ? "file" : "files"}`}
          </span>
          {facts(pr).map((fact) => (
            <span key={fact.says} {...stylex.props(tones[fact.tone])}>
              {fact.says}
            </span>
          ))}
        </div>

        <div {...stylex.props(styles.facts, styles.mono)}>
          <span>
            {pr.baseRef} ← {pr.headRef}
          </span>
          {pr.labels.map((label) => (
            <span key={label}>{label}</span>
          ))}
        </div>

        {/* No `description` heading above it. Every repository's template
            starts with one of its own — `## Summary`, `### What changed` — so
            the label was a heading above a heading, saying less than the one it
            was introducing. The gap does the same work. */}
        {pr.body !== "" && (
          <div {...stylex.props(styles.description)}>
            <Markdown>{pr.body}</Markdown>
          </div>
        )}

        {pr.remarks.length > 0 && (
          <>
            <div {...stylex.props(styles.section)}>
              {pr.remarks.length} {pr.remarks.length === 1 ? "remark" : "remarks"}
            </div>
            {pr.remarks.map((remark, at) => (
              <div key={`${remark.author}:${String(at)}`} {...stylex.props(styles.remark)}>
                <div {...stylex.props(styles.who)}>
                  <span>{remark.author}</span>
                  {remark.verdict !== undefined && <span>{remark.verdict}</span>}
                  <span>{when(remark.at)}</span>
                </div>
                {/* A remark is markdown too — a reviewer quoting code in a
                    comment is the ordinary case, and it is the one thing in a
                    review conversation that must not be flattened. */}
                <Markdown>{remark.body}</Markdown>
              </div>
            ))}
          </>
        )}
      </div>
    </div>
  );
}
