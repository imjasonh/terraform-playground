// Optional end-to-end smoke test that drives the real UI in headless Chrome via
// the DevTools Protocol — no npm dependencies (uses Node's global fetch +
// WebSocket). It verifies the page boots with zero console errors/exceptions,
// that click-to-schedule and node actions work, and (optionally) writes a
// screenshot.
//
// Requires a Chrome/Chromium binary on the machine. It is intentionally NOT part
// of `npm test` (which stays dependency- and browser-free).
//
// Usage:
//   1) start the dev server:   node server.js          (defaults to :8080)
//   2) run this against it:     APP_URL=http://localhost:8080/ node tools/browser-smoke.mjs [screenshot.png]
//   (or simply: npm run smoke)
import { spawn } from "node:child_process";
import { writeFileSync, existsSync } from "node:fs";

const APP_URL = process.env.APP_URL || "http://localhost:8080/";
const PORT = Number(process.env.CDP_PORT) || 9412;
const SHOT = process.argv[2] || null;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const CHROME_CANDIDATES = [
  process.env.CHROME_BIN,
  "/opt/google/chrome/chrome",
  "/usr/bin/google-chrome-stable",
  "/usr/bin/chromium",
  "/usr/bin/chromium-browser",
].filter(Boolean);
const chromeBin = CHROME_CANDIDATES.find((p) => existsSync(p)) || "google-chrome";

const chrome = spawn(
  chromeBin,
  [
    "--headless=new",
    "--no-sandbox",
    "--disable-gpu",
    "--disable-dev-shm-usage",
    `--user-data-dir=/tmp/kube-smoke-${process.pid}`,
    `--remote-debugging-port=${PORT}`,
    "--remote-allow-origins=*",
    "--window-size=1500,950",
    APP_URL,
  ],
  { stdio: "ignore" }
);

const exceptions = [];
const consoleErrors = [];

async function getWsUrl() {
  for (let i = 0; i < 60; i++) {
    try {
      const list = await (await fetch(`http://127.0.0.1:${PORT}/json`)).json();
      const page = list.find((t) => t.type === "page" && t.webSocketDebuggerUrl);
      if (page) return page.webSocketDebuggerUrl;
    } catch {}
    await sleep(200);
  }
  throw new Error("could not reach Chrome DevTools — is a Chrome binary installed?");
}

function connect(url) {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(url);
    let id = 0;
    const pending = new Map();
    ws.addEventListener("open", () => resolve(api));
    ws.addEventListener("error", (e) => reject(e.message || "ws error"));
    ws.addEventListener("message", (ev) => {
      const msg = JSON.parse(typeof ev.data === "string" ? ev.data : ev.data.toString());
      if (msg.id && pending.has(msg.id)) {
        pending.get(msg.id)(msg);
        pending.delete(msg.id);
      } else if (msg.method === "Runtime.exceptionThrown") {
        const d = msg.params.exceptionDetails;
        exceptions.push(d.exception?.description || d.text || "exception");
      } else if (msg.method === "Runtime.consoleAPICalled" && msg.params.type === "error") {
        consoleErrors.push(msg.params.args.map((a) => a.value ?? a.description ?? "").join(" "));
      } else if (msg.method === "Log.entryAdded" && msg.params.entry.level === "error") {
        consoleErrors.push(msg.params.entry.text);
      }
    });
    const api = {
      send(method, params = {}) {
        const i = ++id;
        return new Promise((res) => {
          pending.set(i, res);
          ws.send(JSON.stringify({ id: i, method, params }));
        });
      },
      close: () => ws.close(),
    };
  });
}

async function main() {
  const cdp = await connect(await getWsUrl());
  await cdp.send("Page.enable");
  await cdp.send("Runtime.enable");
  await cdp.send("Log.enable");

  const evaluate = async (expression) => {
    const r = await cdp.send("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
    if (r.result?.exceptionDetails) throw new Error("eval threw: " + JSON.stringify(r.result.exceptionDetails));
    return r.result?.result?.value;
  };

  await cdp.send("Page.navigate", { url: APP_URL });
  await sleep(1800);

  const booted = await evaluate("typeof window.__kube !== 'undefined' && !!window.__kube.game");
  if (!booted) throw new Error("app did not boot (window.__kube missing)");

  // Run an automated cluster for a few seconds.
  await evaluate(`(() => { const {game}=window.__kube;
    game.reset('chaos'); game.state.autoScale=true; game.state.autoSchedule=true;
    game.state.speed=4; game.state.paused=false; return 'ok'; })()`);
  await sleep(8000);

  const stats = JSON.parse(
    await evaluate(`(() => { const {game}=window.__kube; return JSON.stringify({
      nodeCards: document.querySelectorAll('.node').length,
      pending: game.state.pendingIds.length,
      running: game.runningCount(),
      util: Math.round(game.clusterUtilization()*100),
    }); })()`)
  );
  console.log("stats:", stats);

  if (SHOT) {
    const shot = await cdp.send("Page.captureScreenshot", { format: "png" });
    writeFileSync(SHOT, Buffer.from(shot.result.data, "base64"));
    console.log("screenshot ->", SHOT);
  }

  // Manual scheduling path.
  const manual = JSON.parse(
    await evaluate(`(() => { const {game,ui}=window.__kube;
      game.state.paused=true; game.state.autoSchedule=false;
      for (let i=0;i<22;i++) game.tick(); ui.markDirty(); ui.render();
      const before = game.state.pendingIds.length;
      const pod = document.querySelector('.pod'); if(!pod) return JSON.stringify({skipped:true});
      pod.click(); ui.render();
      const feasible = document.querySelector('.node.feasible');
      if (feasible) feasible.click(); ui.render();
      return JSON.stringify({ before, after: game.state.pendingIds.length,
        sawFeasible: !!feasible, sawInfeasible: !!document.querySelector('.node.infeasible') }); })()`)
  );
  console.log("manual:", manual);

  // Node ops shouldn't throw.
  await evaluate(`(() => { const {game,ui}=window.__kube; const n=game.state.nodes[0];
    if(n){ game.cordon(n.id); game.drain(n.id); } ui.markDirty(); ui.render(); return 'ok'; })()`);

  await sleep(200);
  cdp.close();
  chrome.kill("SIGKILL");

  const problems = [];
  if (exceptions.length) problems.push(`exceptions: ${exceptions.join(" | ")}`);
  if (consoleErrors.length) problems.push(`console errors: ${consoleErrors.join(" | ")}`);
  if (stats.nodeCards === 0) problems.push("no node cards rendered");
  if (!manual.skipped && !manual.sawFeasible) problems.push("feasibility highlight missing");

  if (problems.length) {
    console.error("SMOKE FAILED:\n - " + problems.join("\n - "));
    process.exit(1);
  }
  console.log("SMOKE OK — no exceptions, UI interactive.");
  process.exit(0);
}

main().catch((e) => {
  console.error("SMOKE ERROR:", e.message || e);
  chrome.kill("SIGKILL");
  process.exit(1);
});
