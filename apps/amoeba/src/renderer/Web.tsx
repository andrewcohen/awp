import { ArrowClockwiseIcon } from "@phosphor-icons/react/ArrowClockwise";
import { CaretLeftIcon } from "@phosphor-icons/react/CaretLeft";
import { CaretRightIcon } from "@phosphor-icons/react/CaretRight";
import { CrosshairSimpleIcon } from "@phosphor-icons/react/CrosshairSimple";
import { WarningCircleIcon } from "@phosphor-icons/react/WarningCircle";
import { XIcon } from "@phosphor-icons/react/X";
import * as stylex from "@stylexjs/stylex";
import { useEffect, useRef, useState } from "react";
import { type Picked, messageFrom, pickerSource, stopSource } from "./annotate";
import { addressFor } from "./browse";
import { sendNote } from "./daemon";
import { type HostWebview, createWebview, focusHostWindow, hostWebviewAvailable } from "./host";
import { useOverlaysOpen } from "./overlays";
import { pageKey, usePages } from "./usePages";
import { colors, text } from "./tokens.stylex";

// A browser in the accessory column.
//
// ── a native webview, not an iframe ───────────────────────────────────────
//
// An `<iframe>` is one import and works in every context this code runs in,
// and it is the wrong answer: most of what a person wants beside an agent —
// a docs site, a dashboard, an issue tracker — sends `X-Frame-Options` or a
// `frame-ancestors` policy and renders as a blank rectangle with a console
// error nobody sees. A panel that works for localhost and nothing else is a
// panel that gets opened twice.
//
// So it is a real browser view — a `WebContentsView` the main process creates
// and positions over the renderer at the rectangle this panel's box occupies.
// `host.ts` is the near half of that; `src/electron/webviews.ts` is the far one.
//
// Three consequences follow, and each one shapes something below.
//
// **It is not a DOM element that clips and stacks.** It floats above the page
// at the rectangle it is told to occupy, so it cannot be hidden by an ancestor
// with `overflow: hidden` or covered by a dialog. What saves this is that Base
// UI unmounts a hidden tab panel: switching tabs runs this effect's cleanup,
// which tears the native view down. The cost is that the page is reloaded when
// the tab comes back, which is why the address is remembered.
//
// **It does not exist outside the app window.** In a plain browser — the dev
// server opened in a tab, and every Playwright probe — there is no bridge and
// nothing to render. That case says so in words rather than showing an empty
// box, because an empty box is also what a page that failed to load looks like.
//
// **It is driven by methods, not by props.** So it is created imperatively and
// held in a ref, the same shape the pane uses, rather than through JSX — which
// would model as an element something that is not one.
//
// The same fact is why this panel watches `useOverlaysOpen`: a native webview
// is drawn over the renderer by another process, so it is in front of every
// dialog whatever any `z-index` says, and the only thing that puts a modal in
// front of it is not drawing it. See overlays.ts for the rest of that.

// Whether a native webview can be made at all. False in a plain browser — see
// host.ts, which owns both halves of that answer.
const available = hostWebviewAvailable;

/**
 * The last navigation this window acted on, as its timestamp.
 *
 * Module scope, for the reason every other module-scope guard here is: it has
 * to outlive the component. The panel is unmounted on every tab switch, so a
 * ref would be reset each time and the newest request — which is still sitting
 * in the atom, unchanged, because nothing has asked for a page since — would be
 * acted on again. What that looks like is the page reloading every time
 * somebody opens the tab.
 */
let acted = 0;

const styles = stylex.create({
  panel: { display: "flex", flexDirection: "column", height: "100%", minHeight: 0 },
  bar: {
    display: "flex",
    alignItems: "center",
    flexShrink: 0,
    gap: "0.25rem",
    padding: "0.3rem 0.5rem",
    borderBottomWidth: 1,
    borderBottomStyle: "solid",
    borderBottomColor: colors.border,
  },
  button: {
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    flexShrink: 0,
    width: "1.4rem",
    height: "1.4rem",
    padding: 0,
    backgroundColor: "transparent",
    borderStyle: "none",
    borderRadius: "0.2rem",
    color: {
      default: colors.muted,
      ":hover": colors.text,
    },
    cursor: "pointer",
  },
  // `flex: 1` with `minWidth: 0`, which is the pair. Either alone is the bug
  // that grows a horizontal scrollbar out of a long address.
  address: {
    flex: 1,
    minWidth: 0,
    padding: "0.1rem 0.4rem",
    backgroundColor: colors.base,
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.2rem",
    color: colors.text,
    font: "inherit",
    fontSize: text.small,
  },
  buttonOn: {
    backgroundColor: colors.border,
    color: colors.text,
  },
  // The composer, between the bar and the page. It takes height from the stage
  // rather than floating over it — a native webview is drawn on top of
  // everything this window renders, so there is no floating to be done.
  note: {
    display: "flex",
    flexDirection: "column",
    flexShrink: 0,
    gap: "0.3rem",
    padding: "0.4rem 0.5rem",
    borderBottomWidth: 1,
    borderBottomStyle: "solid",
    borderBottomColor: colors.border,
  },
  picked: {
    display: "flex",
    alignItems: "center",
    gap: "0.4rem",
    minWidth: 0,
  },
  // `flex: 1` with `minWidth: 0`. A selector is long and must clip, not push.
  label: {
    flex: 1,
    minWidth: 0,
    overflow: "hidden",
    textOverflow: "ellipsis",
    whiteSpace: "nowrap",
    color: colors.text,
    fontSize: text.small,
  },
  body: {
    minHeight: "3.4rem",
    padding: "0.25rem 0.4rem",
    backgroundColor: colors.base,
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.2rem",
    color: colors.text,
    font: "inherit",
    fontSize: text.small,
    resize: "vertical",
  },
  // What went wrong with the page, between the bar and the stage. It takes
  // height rather than floating: a native view is drawn over everything this
  // window renders, so there is no floating to be done.
  trouble: {
    display: "flex",
    alignItems: "center",
    flexShrink: 0,
    gap: "0.4rem",
    padding: "0.3rem 0.5rem",
    borderBottomWidth: 1,
    borderBottomStyle: "solid",
    borderBottomColor: colors.border,
    color: colors.warn,
    fontSize: text.small,
  },
  row: { display: "flex", alignItems: "center", gap: "0.4rem" },
  hint: { flex: 1, minWidth: 0, color: colors.muted, fontSize: text.small },
  send: {
    flexShrink: 0,
    padding: "0.2rem 0.5rem",
    backgroundColor: colors.border,
    borderStyle: "none",
    borderRadius: "0.2rem",
    color: colors.text,
    font: "inherit",
    fontSize: text.small,
    cursor: "pointer",
  },
  off: { opacity: 0.5, cursor: "default" },
  // The rectangle the native webview is told to occupy. It has no children of
  // its own — what fills it is drawn by another process, over the top.
  stage: { flex: 1, minHeight: 0 },
  said: {
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    height: "100%",
    padding: "1rem",
    color: colors.muted,
    fontSize: text.small,
    textAlign: "center",
  },
});

/**
 * @param project   the open session's workspace, when it is one of ours. A
 *                  note is typed at *that* workspace's agent, so without the
 *                  pair there is nobody to send to and the picker says so
 *                  rather than offering a button that cannot work.
 */
export function Web({
  project,
  workspace,
  thread,
  shown,
}: {
  readonly project: string | undefined;
  readonly workspace: string | undefined;
  /** The thread this panel's page belongs to, or nothing claims the session. */
  readonly thread: string | undefined;
  /**
   * Whether this is the panel on screen.
   *
   * ── the panel is `keepMounted`, and this is why it needs telling ────────
   *
   * Every other panel in the strip is unmounted when it is not selected. This
   * one is not, because the thing it draws is a `WebContentsView` the main
   * process paints over the window: hiding it by unmounting made a native
   * overlay depend on React choosing to unmount, and anything that stopped
   * that — a hot reload that failed to apply, measured — left a page over the
   * column with nothing able to reach it.
   *
   * So the view now lives as long as the column and is hidden by this fact.
   * A `ResizeObserver` reading 0x0 would nearly do it, and "nearly" is the
   * problem: it is a measurement of a consequence where this is the cause.
   * The box is still watched, for the folded-column case that has no other
   * tell.
   */
  readonly shown: boolean;
}) {
  const stage = useRef<HTMLDivElement>(null);
  const view = useRef<HostWebview | undefined>(undefined);

  // ── a page per thread, not per window ────────────────────────────────────
  //
  // Both of these used to be one value. A thread is a piece of work and the
  // page beside it is part of that work — the ticket, the preview, the failing
  // build — so moving between threads carried the wrong page along.
  //
  // Maps rather than state re-seeded by an effect: an effect watching `thread`
  // renders the previous thread's page for a frame first, and for this panel a
  // frame of the wrong page is a native webview told to load it.
  //
  // `pages` is what is loaded and is remembered across launches. `drafts` is
  // what is in the box, which is not the same thing — a half-typed address is
  // neither, and the two only agree after enter or a navigation — and is
  // deliberately not persisted: a URL somebody started typing and left is not
  // a page they chose.
  // ── the pages are the window's, not this component's ────────────────────
  //
  // They used to be `useState` here, and the panel was the only thing that
  // could move them. Both halves of that are now wrong: an agent can ask for a
  // page (`awp_browse`, over `PageChanges`), and this component is unmounted
  // whenever somebody is looking at another tab — which is exactly when an
  // agent has something to show. See usePages.ts, which owns the subscription
  // for the window's life.
  //
  // `drafts` stays local and deliberately so. A half-typed address is not a
  // page anybody chose, it is not persisted, and nothing outside this box has
  // any business in it.
  const { pages, asked, remember } = usePages();
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const key = pageKey(thread);
  const here: string | undefined = pages[key];
  const typed = drafts[key] ?? here ?? "";
  const setHere = (url: string) => {
    remember(thread, url);
  };
  const setTyped = (draft: string) => {
    setDrafts((was) => ({ ...was, [key]: draft }));
  };
  const [can] = useState(available);

  // Something modal is open, and this panel is drawn over the top of it.
  const covered = useOverlaysOpen();

  // The panel's own rectangle has no size.
  //
  // `OverlaySyncController.sync()` returns early when the element measures
  // 0x0 — the library says so in its own source — so the native view keeps
  // whatever rectangle it last had. Collapsing the accessory column is exactly
  // that case: the column goes to zero, the webview does not, and a page is
  // left drawn over the rest of the window with everything under it unreachable.
  // The reported symptom was the diff's revision list refusing to be dragged,
  // which is a sentence about a different panel entirely.
  //
  // Watched here rather than plumbed down as a `collapsed` prop, because the
  // property that matters is *the box having no size*, and folding a column is
  // only one of the ways to get there.
  const [flat, setFlat] = useState(false);

  useEffect(() => {
    const parent = stage.current;
    if (parent === null) {
      return;
    }
    const watch = new ResizeObserver((entries) => {
      const rect = entries[0]?.contentRect;
      setFlat(rect === undefined || rect.width === 0 || rect.height === 0);
    });
    watch.observe(parent);
    return () => watch.disconnect();
  }, []);

  // Three reasons to stop drawing it, and they are different questions with
  // one answer — the view has a single switch:
  //
  //   not shown   another panel is selected. The authoritative fact, and the
  //               only one that is a cause rather than a consequence
  //   covered     a modal is open, and a native view is in front of every one
  //   flat        the box has no size — a folded column, which nothing else
  //               announces
  const away = !shown || covered || flat;

  // ── the annotator ────────────────────────────────────────────────────────
  //
  // Three states, and they are a cycle rather than a set: idle, armed, and
  // holding something picked. The middle one lives in the *page*, so what is
  // kept here is only this window's belief about it — which is why the picker
  // reports a cancel (see annotate.ts) instead of leaving the two to disagree.
  const [armed, setArmed] = useState(false);
  const [picked, setPicked] = useState<Picked | undefined>(undefined);
  const [remark, setRemark] = useState("");
  const [sending, setSending] = useState(false);
  const [said, setSaid] = useState<string | undefined>(undefined);
  // What went wrong with the page itself, which is a different subject from
  // `said` — that one is about a note being delivered to an agent. A page that
  // refused to load and a note that failed to send are two unrelated sentences,
  // and one state holding both would show the wrong one at the wrong moment.
  const [trouble, setTrouble] = useState<string | undefined>(undefined);
  const canSend = project !== undefined && workspace !== undefined;

  // The same fact as `armed`, readable from a listener installed once.
  //
  // The element is built in a mount-only effect, so every handler it registers
  // closes over the first render's state forever. A ref is the value those
  // handlers can actually read — and re-arming after a navigation is exactly a
  // handler needing to know something that changed after it was written.
  const armedRef = useRef(false);
  useEffect(() => {
    armedRef.current = armed;
  }, [armed]);

  const arm = () => {
    setSaid(undefined);
    setArmed(true);
    view.current?.executeJavascript(pickerSource());
  };

  const disarm = () => {
    setArmed(false);
    view.current?.executeJavascript(stopSource());
  };

  // Drop the note and take the highlight off the page with it. Used by the
  // dismiss button and after a successful send — a composer left holding a
  // delivered note reads as one that failed to go.
  const forget = () => {
    setPicked(undefined);
    setRemark("");
    setArmed(false);
    view.current?.executeJavascript(stopSource());
  };

  // ── somebody asked for a page ────────────────────────────────────────────
  //
  // `asked` is the last navigation anybody requested, and "anybody" is the
  // point: an agent calling `awp_browse` is the case this whole path exists
  // for. Acted on by its `at` rather than its url, because asking for the page
  // already loaded is a real request — it means reload — and two value-equal
  // urls are otherwise one event.
  //
  // Only when it names this panel's thread. A request for another thread's page
  // is already recorded in `pages` by the watcher, and it shows when somebody
  // moves to that thread; navigating here would put another piece of work's
  // page in front of them.
  useEffect(() => {
    if (asked === undefined || asked.at <= acted || pageKey(asked.thread) !== key) {
      return;
    }
    // Consumed even when there is no view yet to receive it, which is the case
    // for a panel nobody has opened. Nothing is lost: the watcher has already
    // recorded the page in `pages`, and the view is created with `here` — so
    // the first open lands on it. Leaving it unconsumed would instead reload
    // the page the moment somebody opened the tab.
    acted = asked.at;
    setTyped(asked.url);
    setTrouble(undefined);
    view.current?.loadURL(asked.url);
    // `setTyped` and `setTrouble` are this component's setters and are stable
    // in the ways that matter; keying on them would run this on every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [asked, key]);

  useEffect(() => {
    const element = view.current;
    if (element === undefined) {
      return;
    }
    element.toggleHidden(away);
    // Forced on the way back, rather than waiting to be noticed. While hidden
    // the element's rectangle is zero, and the tag's own sync loop only polls
    // every 100ms — so without this the page returns a tenth of a second after
    // the dialog goes, which reads as the panel being slow to wake up.
    if (!away) {
      element.syncDimensions(true);
    }
  }, [away]);

  // ── nothing is made until somebody opens the tab ────────────────────────
  //
  // `keepMounted` puts this component in the tree from the moment the column
  // renders, and a native view is a process. So the view is built the first
  // time the panel is actually looked at, and from then on it stays: the flag
  // only ever goes false → true, so the effect below still runs exactly once
  // and keeps the history the back button walks.
  const [made, setMade] = useState(shown);
  useEffect(() => {
    if (shown) {
      setMade(true);
    }
  }, [shown]);

  useEffect(() => {
    const parent = stage.current;
    if (parent === null || !available() || !made) {
      return;
    }

    // Built here rather than declared in JSX: a native view is driven by
    // methods and has no props, and its rectangle is this box's.
    // `key`: one native view per window for this panel, enforced by the main
    // process. Reported as "a slim styleguide web view hanging out over the
    // left pane" and as the page "replicating" when devtools opened — both are
    // a view nothing in this renderer still holds, so nothing here could have
    // taken either down. See `createWebview`.
    const element = createWebview(parent, { url: here, key: "web-panel" });
    view.current = element;

    // The address bar follows the page, not the other way round. A link
    // followed inside the webview is a navigation this side never asked for,
    // and an address bar that kept saying where the page started would be
    // lying about where it is.
    const navigated = (detail: unknown) => {
      const url = (detail as { readonly url?: string } | undefined)?.url;
      if (typeof url === "string" && url !== "") {
        setHere(url);
        setTyped(url);
        // A page arrived, so whatever the last one could not do is over.
        setTrouble(undefined);
      }
    };
    element.on("did-navigate", navigated);

    // The page did not load, and a native view says nothing about it — it just
    // draws whatever it has, which for a refused connection is a blank
    // rectangle over the column. The words are the whole feature.
    const failed = (detail: unknown) => {
      const one = detail as { readonly description?: string; readonly url?: string } | undefined;
      const why = one?.description ?? "the page did not load";
      const at = one?.url ?? "";
      setTrouble(at === "" ? why : `${why} — ${at}`);
    };
    element.on("did-fail-load", failed);

    // What the picker says back. The only wire from that process to this one.
    const heard = (detail: unknown) => {
      const message = messageFrom(detail);
      if (message === undefined) {
        // Not ours. This is the page's channel and any script on any site can
        // put anything down it.
        return;
      }
      if (message.kind === "cancelled") {
        setArmed(false);
        return;
      }
      setArmed(false);
      setPicked(message.picked);
      setRemark("");
      setSaid(undefined);
      // The click that picked was in the *page*, so the keyboard is in that
      // process. The box below focuses itself on mount and would still be
      // deaf without this — see `focusHostWindow`.
      focusHostWindow();
    };
    element.on("host-message", heard);

    // A navigation is a new document, and the picker was in the old one.
    //
    // Re-injected on `dom-ready` rather than `did-navigate`: the second says a
    // navigation was committed, which is before there is a `document.body` to
    // append a highlight to. Only while armed — re-arming a picker somebody
    // turned off would put a highlight back on a page they are trying to read.
    const ready = () => {
      if (armedRef.current) {
        element.executeJavascript(pickerSource());
      }
    };
    element.on("dom-ready", ready);

    return () => {
      // The listeners come off as well as the view going down. It is redundant
      // — `destroy` clears them — and it is written anyway, because a
      // subscription whose cleanup is implied by someone else's teardown is one
      // that becomes a leak the day their teardown changes.
      element.off("did-navigate", navigated);
      element.off("did-fail-load", failed);
      element.off("host-message", heard);
      element.off("dom-ready", ready);
      // This is what tears the native view down. Nothing else does, and a view
      // nothing holds is a page floating over the window for the life of the
      // process — see the orphan note in host.ts.
      element.destroy();
      view.current = undefined;
    };
    // Once, when the panel is first looked at. `here` is read at creation to
    // restore the last page, and afterwards navigation goes through `loadURL`
    // — rebuilding the view on every address change would throw away the
    // history the back button exists to walk. `made` only goes false → true.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [made]);

  const deliver = () => {
    if (picked === undefined || project === undefined || workspace === undefined) {
      return;
    }
    const body = remark.trim();
    if (body === "" || sending) {
      // A note with no remark is an address with nothing said. Refused here as
      // well as by the disabled button, because Enter reaches this directly.
      return;
    }
    setSending(true);
    setSaid(undefined);
    sendNote(project, workspace, { ...picked, body })
      .then(() => {
        forget();
        setSaid("sent to the agent");
      })
      .catch((error: unknown) => {
        // Said out loud, in the panel. The workspace's agent may have ended,
        // which is the ordinary case and reads as nothing happening otherwise.
        setSaid(error instanceof Error ? error.message : String(error));
      })
      .finally(() => setSending(false));
  };

  const go = (typedText: string) => {
    const url = addressFor(typedText);
    if (url === undefined) {
      return;
    }
    setHere(url);
    setTyped(url);
    // Cleared on the way out rather than on the way in: a stale complaint
    // sitting over a page that is loading reads as the new address failing too.
    setTrouble(undefined);
    view.current?.loadURL(url);
  };

  return (
    <div {...stylex.props(styles.panel)}>
      <div {...stylex.props(styles.bar)}>
        <button
          type="button"
          aria-label="back"
          title="back"
          {...stylex.props(styles.button)}
          onClick={() => view.current?.goBack()}
        >
          <CaretLeftIcon size={13} weight="bold" aria-hidden />
        </button>
        <button
          type="button"
          aria-label="forward"
          title="forward"
          {...stylex.props(styles.button)}
          onClick={() => view.current?.goForward()}
        >
          <CaretRightIcon size={13} weight="bold" aria-hidden />
        </button>
        <button
          type="button"
          aria-label="reload"
          title="reload"
          {...stylex.props(styles.button)}
          onClick={() => view.current?.reload()}
        >
          <ArrowClockwiseIcon size={13} weight="bold" aria-hidden />
        </button>
        <input
          aria-label="address"
          placeholder="address, or something to search for"
          value={typed}
          spellCheck={false}
          {...stylex.props(styles.address)}
          onChange={(event) => setTyped(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              event.preventDefault();
              go(typed);
            }
            if (event.key === "Escape") {
              // Back to what is actually loaded, rather than clearing. Escape
              // in an address bar means "forget what I was typing".
              event.stopPropagation();
              setTyped(here ?? "");
            }
          }}
          // An `<input>` already keeps ctrl+h/j/k on macOS — see navigation.ts
          // — and this is the one field in the window where that matters most,
          // because an address is edited character by character.
          onFocus={(event) => event.target.select()}
        />
        <button
          type="button"
          aria-label={armed ? "stop picking an element" : "pick an element"}
          aria-pressed={armed}
          title={
            can
              ? armed
                ? "click an element in the page, or press escape"
                : "point at an element and tell the agent about it"
              : "this needs the app window"
          }
          disabled={!can}
          {...stylex.props(styles.button, armed && styles.buttonOn, !can && styles.off)}
          onClick={() => (armed ? disarm() : arm())}
        >
          <CrosshairSimpleIcon size={13} weight="bold" aria-hidden />
        </button>
      </div>

      {trouble === undefined ? undefined : (
        <div {...stylex.props(styles.trouble)} role="status">
          <WarningCircleIcon size={13} weight="bold" aria-hidden />
          {/* `flex: 1` with `minWidth: 0`. A URL is long and must clip, not
              push the dismiss button off the end of the row. */}
          <span title={trouble} {...stylex.props(styles.label)}>
            {trouble}
          </span>
          <button
            type="button"
            aria-label="dismiss"
            title="dismiss"
            {...stylex.props(styles.button)}
            onClick={() => setTrouble(undefined)}
          >
            <XIcon size={12} weight="bold" aria-hidden />
          </button>
        </div>
      )}

      {picked === undefined ? undefined : (
        <div {...stylex.props(styles.note)}>
          <div {...stylex.props(styles.picked)}>
            <CrosshairSimpleIcon size={12} weight="bold" aria-hidden />
            {/* The label a person recognises, and the selector as the tooltip.
                The second is the one an agent can act on and the one nobody
                wants to read, which is exactly what a title attribute is for. */}
            <span title={picked.selector} {...stylex.props(styles.label)}>
              {picked.label}
            </span>
            <button
              type="button"
              aria-label="discard this note"
              title="discard"
              {...stylex.props(styles.button)}
              onClick={forget}
            >
              <XIcon size={12} weight="bold" aria-hidden />
            </button>
          </div>
          <textarea
            aria-label="what to say about this element"
            placeholder="leave a comment"
            value={remark}
            // Focused on mount, which is the moment a pick arrived: somebody
            // who has just aimed at an element is about to type about it. A
            // callback ref rather than `autoFocus`, which react-doctor flags
            // because the attribute fires on any render the element mounts in.
            ref={(node) => node?.focus()}
            {...stylex.props(styles.body)}
            onChange={(event) => setRemark(event.target.value)}
            onKeyDown={(event) => {
              // cmd/ctrl+enter sends, plain enter is a newline. A remark is
              // often two sentences, and a composer that submits on Enter is
              // one that delivers the first half of them.
              if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
                event.preventDefault();
                deliver();
              }
              if (event.key === "Escape") {
                event.stopPropagation();
                forget();
              }
            }}
          />
          <div {...stylex.props(styles.row)}>
            <span {...stylex.props(styles.hint)}>
              {canSend ? (said ?? "⌘⏎ to send") : "no workspace open to send to"}
            </span>
            <button
              type="button"
              disabled={!canSend || remark.trim() === "" || sending}
              {...stylex.props(
                styles.send,
                (!canSend || remark.trim() === "" || sending) && styles.off,
              )}
              onClick={deliver}
            >
              {sending ? "sending…" : "send"}
            </button>
          </div>
        </div>
      )}

      {can ? (
        <div ref={stage} {...stylex.props(styles.stage)} />
      ) : (
        <div {...stylex.props(styles.said)}>
          this panel needs the app window — a native webview is not something a browser tab can make
        </div>
      )}
    </div>
  );
}
