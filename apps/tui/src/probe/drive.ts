#!/usr/bin/env bun
// Drive the picker through a pty and say what the other process saw.
//
// Nothing here is a unit test's job. A picker that lists sessions, hands the
// keyboard to one, and gives it back is three processes deep — this process,
// the TUI, and the `zmx attach` client it spawned — and every question worth
// asking is about the last one.
//
// ── what makes this safe to run ─────────────────────────────────────────────
//
// The TUI is started with `ZA_TUI_ONLY=poc.`, so the only rows it can list are
// the ones this probe made. A session takes its size from whoever is looking at
// it, and a probe that could pick a person's session would reflow the terminal
// they are typing in — the hazard AGENTS.md records for browser probes, one
// layer down. A guard on the property that matters beats a refusal.

import { openPty, quiet, until, wait } from "./pty";

const SESSION = "poc.opentui.shell";
const STATS = "/tmp/za-tui-stats.json";
const ESC = String.fromCodePoint(27);
const DETACH = String.fromCodePoint(28);

const zmx = async (args: string[]) => {
  const proc = Bun.spawn(["zmx", ...args], {
    env: { ...process.env, ZMX_SESSION: "" },
    stdout: "pipe",
    stderr: "pipe",
  });
  const [out, err, code] = await Promise.all([
    new Response(proc.stdout).text(),
    new Response(proc.stderr).text(),
    proc.exited,
  ]);
  return { out, err, code };
};

/** Nothing outside the names this probe mints. */
const ours = (name: string) => name.startsWith("poc.");

const say = (label: string, value: unknown) =>
  console.log(`  ${label.padEnd(16)}${typeof value === "string" ? value : JSON.stringify(value)}`);

const stats = async () => {
  const file = Bun.file(STATS);
  if (!(await file.exists())) return null;
  const text = await file.text();
  return text === "" ? null : JSON.parse(text);
};

// `until` takes a synchronous predicate and the stats are a file, so one poller
// keeps the last reading where a predicate can see it.
const painted: { mode?: string; bytes?: number } = {};
const poll = setInterval(() => {
  void stats().then((s) => {
    if (s === null) return;
    painted.mode = s.mode;
    painted.bytes = s.bytes;
  });
}, 100);

const main = async () => {
  if (!ours(SESSION)) throw new Error(`refusing to touch ${SESSION}`);
  await Bun.write(STATS, "");

  console.log("setup");
  const made = await zmx(["run", SESSION, "-d", "bash", "--norc", "-i"]);
  if (made.code !== 0) throw new Error(`zmx run: ${made.err.trim()}`);
  say("session", SESSION);

  const tui = openPty(["bun", "src/za-tui.ts"], {
    cols: 120,
    rows: 34,
    env: { ZA_TUI_ONLY: "poc.", ZA_TUI_STATS: STATS },
  });

  try {
    console.log("picker");
    await until("the picker to list the session", () => tui.saw(SESSION));
    say("listed", tui.saw("1 of 1 sessions") ? "1 of 1" : "listed, count unread");
    say("footer", tui.saw("enter attach") ? "enter attach · ctrl-x kill" : "MISSING");

    console.log("attach");
    tui.send("\r");
    await until("a shell prompt inside the session", () => tui.saw("bash-"), 10_000);
    // The bar is asserted on with no sleep in front of it: `attach` writes it
    // itself now, and a check that waited would pass either way. The mode comes
    // from the meter's own file, which is a quarter second behind by design.
    say("bar", tui.saw(`${SESSION} ·`) ? "names the session, offers detach" : "MISSING");
    await until("the meter to report the mode", () => painted.mode === "attached", 5000);
    say("mode", painted.mode);

    const before = tui.bytes();
    tui.send("echo hello-from-poc\r");
    await until("the command to echo back", () => tui.saw("hello-from-poc"));
    say("typed", `echo hello-from-poc -> ${tui.bytes() - before} bytes painted`);

    console.log("throughput");
    // 40MB of the same line: enough to run for seconds, and every line is a
    // full-width write, so the grid does real work rather than scrolling blanks.
    const idle = await stats();
    // The same 40MB `probe:ceiling` pushes through a pty with no rendering at
    // all, so the two numbers are comparable. The end condition is the byte
    // counter going quiet — waiting for a word the command line contains times
    // the echo of the command and reports a rate ten times too high.
    // Timed off the TUI's own output, which moves with every frame it paints —
    // the stats file only lands four times a second, and a drain that finishes
    // inside one of those windows measures as having taken no time at all.
    const midway = (async () => {
      // Nine hundred rather than four: `fps` is counted once a second and the
      // earlier sample landed inside the window's own reset, reading 0 during a
      // drain that was plainly rendering.
      await wait(900);
      return stats();
    })();
    tui.send("yes 'opentui embedded terminal throughput check line' | head -c 40000000\r");
    const drain = await quiet(() => tui.bytes(), tui.bytes());
    const during = await midway;
    const drained = await stats();
    const megabytes = ((drained?.bytes ?? 0) - (idle?.bytes ?? 0)) / 1024 / 1024;
    const frames = (drained?.frameCount ?? 0) - (idle?.frameCount ?? 0);
    say("fps", `${during?.fps} mid-drain · ${(frames / drain.elapsed).toFixed(0)} over the drain`);
    say(
      "frame ms",
      `avg ${during?.averageFrameMs?.toFixed(2)} max ${drained?.maxFrameMs?.toFixed(2)}`,
    );
    say(
      "read",
      `${megabytes.toFixed(1)}MB in ${drain.elapsed.toFixed(1)}s = ${(
        megabytes / drain.elapsed
      ).toFixed(1)}MB/s`,
    );
    // The interesting ratio: what came in against what this process had to
    // write out. opentui paints frames rather than forwarding bytes, so the
    // outer terminal sees a fraction of the spew.
    say("painted", `${(drain.megabytes * 1024).toFixed(0)}KB out to the outer terminal`);
    say("peak window", `${((drained?.peakBytesPerSecond ?? 0) / 1024 / 1024).toFixed(1)}MB/s`);
    say("reads", drained?.reads);

    // ── the same volume, with nothing repeating ─────────────────────────────
    //
    // `yes` is a cell-diffing renderer's best case: every line is the line
    // above it, so most frames have nothing to write and the outer terminal
    // saw 19KB for 38.9MB in. Base64 of /dev/urandom changes every cell on
    // every line, which is the number worth quoting.
    console.log("throughput, unrepeating");
    const beforeRandom = await stats();
    const midwayRandom = (async () => {
      await wait(900);
      return stats();
    })();
    tui.send("head -c 12000000 /dev/urandom | base64\r");
    const randomDrain = await quiet(() => tui.bytes(), tui.bytes());
    const duringRandom = await midwayRandom;
    const afterRandom = await stats();
    const randomMegabytes = ((afterRandom?.bytes ?? 0) - (beforeRandom?.bytes ?? 0)) / 1024 / 1024;
    const randomFrames = (afterRandom?.frameCount ?? 0) - (beforeRandom?.frameCount ?? 0);
    say(
      "fps",
      `${duringRandom?.fps} mid-drain · ${(randomFrames / randomDrain.elapsed).toFixed(0)} over the drain`,
    );
    say(
      "frame ms",
      `avg ${duringRandom?.averageFrameMs?.toFixed(2)} max ${afterRandom?.maxFrameMs?.toFixed(2)}`,
    );
    say(
      "read",
      `${randomMegabytes.toFixed(1)}MB in ${randomDrain.elapsed.toFixed(1)}s = ${(
        randomMegabytes / randomDrain.elapsed
      ).toFixed(1)}MB/s`,
    );
    say("painted", `${(randomDrain.megabytes * 1024).toFixed(0)}KB out to the outer terminal`);

    console.log("keystrokes");
    tui.send("clear\r");
    await wait(500);
    for (let i = 0; i < 40; i++) {
      tui.send("x");
      await wait(60);
    }
    tui.send(ESC);
    await wait(400);
    const keys = await stats();
    say("echo ms", `p50 ${keys?.echoP50?.toFixed(1)} p95 ${keys?.echoP95?.toFixed(1)}`);
    say("paint ms", `p50 ${keys?.paintP50?.toFixed(1)} p95 ${keys?.paintP95?.toFixed(1)}`);
    say("samples", keys?.samples);
    say("cells", keys?.cells);

    console.log("resize");
    tui.resize(90, 26);
    await wait(800);
    const resized = await stats();
    say("cells", resized?.cells);

    console.log("detach");
    tui.send(DETACH);
    // Ask the process what mode it is in, rather than reading paint order out
    // of a scraped buffer. The first version of this check asserted that
    // "enter attach" was painted after the shell prompt, which is a claim about
    // the order of two strings in a stream and not about the mode at all — it
    // failed on a detach that worked.
    await until("the picker to come back", () => painted.mode === "pick", 10_000);
    say("mode", painted.mode);
    say("picker", tui.saw("enter attach") ? "listing again" : "MISSING");

    console.log("still there");
    const listed = await zmx(["ls"]);
    say(SESSION, listed.out.includes(SESSION) ? "alive, and its client is gone" : "GONE");
  } finally {
    tui.send(ESC);
    await wait(200);
    await tui.close();
    clearInterval(poll);
    if (ours(SESSION)) await zmx(["kill", SESSION, "--force"]);
  }
};

await main();
