#!/usr/bin/env bun
// `za`, drawn by opentui instead of by fzf.
//
// The fish function pipes `zmx ls` through awk into fzf and then hands the
// terminal over to `zmx attach`. That last step is the interesting one: fzf
// exits, and the session takes over the whole terminal. Nothing is left around
// it, so there is nowhere to put anything else — which is the whole reason to
// try this: an EmbeddedTerminalRenderable is a session inside a layout, with a
// list beside it and a meter under it.
//
//   ┌ za ─────────────────────────────────────────┐    ┌ za ── attached ──────┐
//   │ zmx ❯ tabular                               │    │ ● thicket/pr-2418    │
//   │ ● thicket  pr-2418   agent   2m  claude     │ →  │ $ …                  │
//   │ · orchard  lantern   editor  1h  nvim .     │    │                      │
//   │ · lantern  default   agent   3d  claude     │    │                      │
//   └ 3 of 10 · enter attach · ctrl-x kill ───────┘    └ 41fps 0.9ms · ctrl-\ ┘
//
// ── the two zmx rules, and which one this file is under ─────────────────────
//
// This is a thing a person runs from a plain terminal, so it is the *spawning*
// case: strip the marker, always. It is not the probing case — it makes no
// claim about being outside a session — but it does refuse to attach to the
// session it is running in, because a session takes its size from whoever is
// looking at it and that one is looking at itself.
//
// A probe driving this must not pick somebody's real session, and the guard for
// that is `ZA_TUI_ONLY`: a prefix the listing is filtered to. A guard on the
// property that matters is stronger than a refusal that has to be remembered.

import {
  BoxRenderable,
  createCliRenderer,
  EmbeddedTerminalRenderable,
  InputRenderable,
  InputRenderableEvents,
  type KeyEvent,
  TextRenderable,
} from "@opentui/core";
import { homedir } from "node:os";
import { age, listRows, matches, type Row, shortCmd } from "./rows";
import { rename } from "node:fs/promises";
import { Meter, ms, rate } from "./meter";

const CHROME = {
  base: "#1e2030",
  raised: "#24273a",
  text: "#cad3f5",
  muted: "#6e738d",
  accent: "#f5a97f",
  live: "#a6da95",
  bar: "#363a4f",
};

const only = process.env.ZA_TUI_ONLY ?? "";
const self = process.env.ZMX_SESSION ?? "";

const renderer = await createCliRenderer({
  // ctrl+c belongs to whatever is running in the session, not to this process.
  exitOnCtrlC: false,
  targetFps: Number(process.env.ZA_TUI_FPS ?? 60),
  gatherStats: true,
});

const root = new BoxRenderable(renderer, {
  id: "root",
  flexGrow: 1,
  flexDirection: "column",
  backgroundColor: CHROME.base,
});
renderer.root.add(root);

const header = new TextRenderable(renderer, {
  id: "header",
  height: 1,
  content: " za · opentui ",
  fg: CHROME.base,
  bg: CHROME.accent,
});
root.add(header);

// ── the picker ──────────────────────────────────────────────────────────────

const picker = new BoxRenderable(renderer, { id: "picker", flexGrow: 1, flexDirection: "column" });
const queryRow = new BoxRenderable(renderer, { id: "query-row", height: 1, flexDirection: "row" });
const prompt = new TextRenderable(renderer, {
  id: "prompt",
  width: 6,
  content: "zmx > ",
  fg: CHROME.accent,
});
const query = new InputRenderable(renderer, {
  id: "query",
  flexGrow: 1,
  placeholder: "filter",
  backgroundColor: CHROME.base,
  textColor: CHROME.text,
});
queryRow.add(prompt);
queryRow.add(query);
picker.add(queryRow);

const list = new BoxRenderable(renderer, {
  id: "list",
  flexGrow: 1,
  flexDirection: "column",
  // The wheel moves the selection rather than scrolling under it: this list is
  // a thing you pick from, and a highlight that stayed put while the rows slid
  // past would answer the wheel by pointing at something else.
  onMouseScroll(event) {
    const scroll = event.scroll;
    if (scroll === undefined || mode !== "pick") return;
    const step = Math.max(1, Math.round(scroll.delta));
    if (scroll.direction === "up") index = Math.max(0, index - step);
    else if (scroll.direction === "down")
      index = Math.min(Math.max(0, shown.length - 1), index + step);
    else return;
    paint();
  },
});
picker.add(list);
root.add(picker);

// ── the attachment ──────────────────────────────────────────────────────────

const attached = new BoxRenderable(renderer, {
  id: "attached",
  flexGrow: 1,
  flexDirection: "column",
  visible: false,
});
const attachedBar = new TextRenderable(renderer, {
  id: "attached-bar",
  height: 1,
  content: "",
  fg: CHROME.muted,
});
const termHost = new BoxRenderable(renderer, {
  id: "term-host",
  flexGrow: 1,
  flexDirection: "column",
});
attached.add(attachedBar);
attached.add(termHost);
root.add(attached);

const footer = new TextRenderable(renderer, {
  id: "footer",
  height: 1,
  content: "",
  fg: CHROME.muted,
  bg: CHROME.bar,
});
root.add(footer);

// ── state ───────────────────────────────────────────────────────────────────

/** Fit a field to its column, with an ellipsis where it did not go. */
const pad = (text: string, width: number) =>
  text.length > width ? `${text.slice(0, width - 1)}…` : text.padEnd(width);

type Mode = "pick" | "attached";
let mode: Mode = "pick";
let rows: Row[] = [];
let shown: Row[] = [];
let index = 0;
let offset = 0;
let notice = "";
const meter = new Meter();
const lines: TextRenderable[] = [];

let child: ReturnType<typeof Bun.spawn> | null = null;
let terminal: EmbeddedTerminalRenderable | null = null;
let current: Row | null = null;

const refresh = async () => {
  try {
    rows = await listRows(only);
    notice = "";
  } catch (error) {
    rows = [];
    // The CLI's own sentence, not one composed here.
    notice = error instanceof Error ? error.message : String(error);
  }
  paint();
};

const paint = () => {
  shown = rows.filter((row) => matches(row, query.value));
  if (index >= shown.length) index = Math.max(0, shown.length - 1);

  const capacity = Math.max(1, list.height);
  if (index < offset) offset = index;
  if (index >= offset + capacity) offset = index - capacity + 1;

  const width = (pick: (row: Row) => string) =>
    shown.reduce((max, row) => Math.max(max, pick(row).length), 0);
  const wProject = Math.min(
    18,
    width((row) => row.project),
  );
  const wWorkspace = Math.min(
    26,
    width((row) => row.workspace),
  );
  const wKind = Math.min(
    8,
    width((row) => row.kind),
  );

  while (lines.length < capacity) {
    const line = new TextRenderable(renderer, {
      id: `row-${lines.length}`,
      height: 1,
      content: "",
    });
    lines.push(line);
    list.add(line);
  }
  while (lines.length > capacity) {
    const line = lines.pop()!;
    list.remove(line);
    line.destroy();
  }

  for (const [at, line] of lines.entries()) {
    const row = shown[offset + at];
    if (row === undefined) {
      line.content = "";
      continue;
    }
    const here = offset + at === index;
    const mark = row.clients > 0 ? "●" : "·";
    line.content = ` ${here ? ">" : " "} ${mark} ${pad(row.project, wProject)} ${pad(
      row.workspace,
      wWorkspace,
    )} ${pad(row.kind, wKind)} ${age(row.created).padStart(3)}  ${shortCmd(row.cmd, 40)}`;
    line.fg = here ? CHROME.accent : row.clients > 0 ? CHROME.live : CHROME.text;
    line.bg = here ? CHROME.raised : CHROME.base;
  }

  const scope = only === "" ? "" : ` · only ${only}*`;
  header.content = ` za · opentui · ${shown.length} of ${rows.length} sessions${scope} `;
  footer.content =
    notice === "" ? " enter attach · ctrl-x kill · ctrl-r reload · esc quit" : ` ${notice}`;
};

// ── attaching ───────────────────────────────────────────────────────────────

const detach = () => {
  if (mode !== "attached") return;
  terminal?.blur();
  child?.kill();
  child?.terminal?.close();
  child = null;
  if (terminal !== null) {
    termHost.remove(terminal);
    terminal.destroy();
    terminal = null;
  }
  current = null;
  mode = "pick";
  attached.visible = false;
  picker.visible = true;
  query.focus();
  void refresh();
};

const attach = (row: Row) => {
  // A session takes its size from whoever is looking at it, and this process is
  // being looked at from inside one. Attaching would size that session to this
  // layout's inner column, which is the hijack every zmx rule here is about.
  if (self !== "" && row.name === self) {
    notice = `refusing to attach to ${row.name}: this process is running in it`;
    paint();
    return;
  }

  const term = new EmbeddedTerminalRenderable(renderer, {
    id: "terminal",
    width: "100%",
    flexGrow: 1,
    maxScrollback: Number(process.env.ZA_TUI_SCROLLBACK ?? 10_000),
    onData(data) {
      child?.terminal?.write(data);
    },
    onTerminalResize(cols, rowCount) {
      child?.terminal?.resize(cols, rowCount);
    },
    onScreenChange() {
      meter.painted();
    },
  });
  termHost.add(term);
  terminal = term;
  current = row;

  child = Bun.spawn(["zmx", "attach", row.name], {
    cwd: homedir(),
    env: {
      ...process.env,
      TERM: "xterm-256color",
      COLORTERM: "truecolor",
      // Present and empty, never absent. Left out, `zmx attach` resolves the
      // marker and switches the *calling* client — the session this process is
      // running in — instead of making one.
      ZMX_SESSION: "",
    },
    terminal: {
      cols: Math.max(1, term.width),
      rows: Math.max(1, term.height),
      data(_pty, data) {
        meter.read(data.byteLength);
        term.write(data);
      },
    },
    onExit() {
      // The session ended or the client was detached from elsewhere. Going back
      // to the picker beats leaving a dead grid on screen, which is what a
      // terminal that failed to start also looks like.
      if (mode === "attached") detach();
    },
  });

  // Said here rather than left to the meter's interval: for up to 250ms after
  // attaching the bar was blank, which is what a session that failed to
  // attach also looks like. The probe raced exactly that gap.
  attachedBar.content = ` ${row.name} · ctrl-\\ detach`;
  mode = "attached";
  picker.visible = false;
  attached.visible = true;
  term.focus();
};

const kill = async (row: Row) => {
  const proc = Bun.spawn(["zmx", "kill", row.name, "--force"], {
    env: { ...process.env, ZMX_SESSION: "" },
    stdout: "pipe",
    stderr: "pipe",
  });
  const [err, code] = await Promise.all([new Response(proc.stderr).text(), proc.exited]);
  if (code !== 0) notice = err.trim() === "" ? `zmx kill exited ${code}` : err.trim();
  await refresh();
};

// ── keys ────────────────────────────────────────────────────────────────────
//
// Global handlers run before the focused renderable's, and can preventDefault
// to stop it seeing the key at all — which is what makes one chord recoverable
// from inside an attached session. ctrl+\ because that is already zmx's detach.

const isDetachChord = (key: KeyEvent) =>
  key.ctrl && (key.name === "\\" || key.sequence === String.fromCodePoint(28));

renderer.keyInput.on("keypress", (key: KeyEvent) => {
  if (mode === "attached") {
    if (isDetachChord(key)) {
      key.preventDefault();
      key.stopPropagation();
      detach();
      return;
    }
    meter.keyed();
    return;
  }

  const consume = () => {
    key.preventDefault();
    key.stopPropagation();
  };

  if (key.name === "escape" || (key.ctrl && key.name === "c")) {
    consume();
    renderer.destroy();
    process.exit(0);
  }
  if (key.name === "up" || (key.ctrl && key.name === "k")) {
    consume();
    index = Math.max(0, index - 1);
    paint();
    return;
  }
  if (key.name === "down" || (key.ctrl && key.name === "j")) {
    consume();
    index = Math.min(Math.max(0, shown.length - 1), index + 1);
    paint();
    return;
  }
  if (key.name === "return" || key.name === "linefeed") {
    consume();
    const row = shown[index];
    if (row !== undefined) attach(row);
    return;
  }
  if (key.ctrl && key.name === "x") {
    consume();
    const row = shown[index];
    if (row !== undefined) void kill(row);
    return;
  }
  if (key.ctrl && key.name === "r") {
    consume();
    void refresh();
  }
});

query.on(InputRenderableEvents.INPUT, () => {
  index = 0;
  offset = 0;
  paint();
});

// ── the meter ───────────────────────────────────────────────────────────────

// Where a probe reads the numbers from. Screen-scraping the footer would be
// reading this process's own rendering of them, which is one more thing that
// can be wrong between the measurement and the reader.
const statsFile = process.env.ZA_TUI_STATS ?? "";

const writeAtomic = async (path: string, text: string) => {
  const temporary = `${path}.tmp`;
  await Bun.write(temporary, text);
  await rename(temporary, path);
};

setInterval(() => {
  const stats = renderer.getStats();
  meter.tick(stats.averageFrameTime);
  if (statsFile !== "") {
    const s = meter.summary();
    // Written beside and renamed over: a reader that opens the file while
    // Bun.write has truncated it and not yet written gets an empty string,
    // which parses to nothing and reads as "the feature is off".
    void writeAtomic(
      statsFile,
      JSON.stringify({
        mode,
        session: current?.name ?? null,
        fps: stats.fps,
        frameCount: stats.frameCount,
        averageFrameMs: stats.averageFrameTime,
        maxFrameMs: stats.maxFrameTime,
        peakFrameMs: meter.peakFrameMs,
        cells: { cols: terminal?.width ?? 0, rows: terminal?.height ?? 0 },
        bytes: meter.bytes,
        reads: meter.reads,
        bytesPerSecond: meter.bytesPerSecond,
        peakBytesPerSecond: meter.peakBytesPerSecond,
        ...s,
      }),
    );
  }
  if (mode !== "attached") return;
  const s = meter.summary();
  footer.content =
    ` ${stats.fps}fps  frame ${ms(stats.averageFrameTime)}/${ms(meter.peakFrameMs)}ms` +
    `  in ${rate(meter.bytesPerSecond)}/${rate(meter.peakBytesPerSecond)}` +
    `  echo ${ms(s.echoP50)}/${ms(s.echoP95)}ms  paint ${ms(s.paintP50)}/${ms(s.paintP95)}ms` +
    `  n=${s.samples}`;
}, 250);

renderer.start();
await refresh();
query.focus();
paint();
