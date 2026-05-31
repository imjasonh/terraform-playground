import { historyEntriesToPoints, toChartSeries } from "./parse.js";
import { renderProgressChart, destroyChart, computeStats } from "./chart.js";
import {
  loadHistory,
  saveHistory,
  loadSelectedBook,
  saveSelectedBook,
  saveCredentialsMeta,
  clearLocalAppData,
} from "./storage.js";
import { extensionRequest } from "./extension-bridge.js";

const bookSelect = document.getElementById("bookSelect");
const xAxisMode = document.getElementById("xAxisMode");
const showTrend = document.getElementById("showTrend");
const statsEl = document.getElementById("stats");
const statusEl = document.getElementById("status");
const authStatusEl = document.getElementById("authStatus");

let historyByBook = loadHistory();
let syncing = false;

// Live progress events from background.js during library back-fill.
chrome.runtime.onMessage.addListener((msg) => {
  if (msg?.action !== "syncProgress") return;
  if (msg.phase === "done") {
    // Final status is set by refreshAll's success path; don't overwrite it.
    return;
  }
  setStatus(`Fetching reading progress ${msg.done}/${msg.total}…`);
});

function setStatus(message, type = "info") {
  statusEl.textContent = message;
  statusEl.dataset.type = type;
}

function setAuthStatus(text, type = "info") {
  authStatusEl.textContent = text;
  authStatusEl.dataset.type = type;
}

function booksWithProgress() {
  return Object.entries(historyByBook).filter(([, entries]) =>
    entries?.some((e) => e.progress > 0),
  );
}

function refreshBookSelect(preferredAsin) {
  const pairs = booksWithProgress();
  const selected = preferredAsin || loadSelectedBook();

  bookSelect.innerHTML = "";
  if (!pairs.length) {
    const opt = document.createElement("option");
    opt.value = "";
    opt.textContent = "No books yet — connect Amazon and refresh";
    bookSelect.appendChild(opt);
    return;
  }

  for (const [asin, entries] of pairs) {
    const title = entries.find((e) => e.title)?.title || asin;
    const opt = document.createElement("option");
    opt.value = asin;
    opt.textContent = `${title} (${entries.length} points)`;
    bookSelect.appendChild(opt);
  }

  const pick = pairs.find(([a]) => a === selected)?.[0] || pairs[0][0];
  bookSelect.value = pick;
  saveSelectedBook(pick);
}

function renderSelectedBook() {
  const asin = bookSelect.value;
  if (!asin) {
    destroyChart();
    statsEl.textContent = "";
    return;
  }

  const points = historyEntriesToPoints(historyByBook, asin);
  if (!points.length) {
    destroyChart();
    statsEl.textContent =
      "No progress snapshots yet for this book. Click Refresh after reading.";
    return;
  }

  const mode = xAxisMode.value === "days" ? "daysFromStart" : "calendar";
  const series = toChartSeries(points, mode);
  const bookTitle = bookSelect.selectedOptions[0]?.textContent?.split(" (")[0];

  renderProgressChart(
    document.getElementById("progressChart"),
    { ...series, bookTitle },
    {
      xLabel:
        mode === "daysFromStart" ? "Days from first sync (t)" : "Timeline",
      showTrend: showTrend.checked,
    },
  );

  const stats = computeStats(series.values);
  if (stats) {
    statsEl.textContent = `Started at ${stats.startPercent.toFixed(1)}% → now ${stats.currentPercent.toFixed(1)}% (+${stats.totalGain.toFixed(1)}%). Reading sessions: ~${stats.readingSessions}.`;
  }
}

async function applyAuthStatus(auth) {
  if (!auth?.authenticated) {
    setAuthStatus(
      "Not signed in — open Kindle Cloud Reader while logged into Amazon.",
      "warn",
    );
    return false;
  }

  if (!auth.ready) {
    setAuthStatus(
      `Session found; still need: ${auth.missing?.join(", ") || "device token"}. Open read.amazon.com and reload that tab.`,
      "warn",
    );
    return true;
  }

  setAuthStatus(`Connected (${auth.amazonDomain || "amazon.com"}).`, "success");
  saveCredentialsMeta({
    hasCookies: true,
    hasDeviceToken: auth.hasDeviceToken,
    amazonDomain: auth.amazonDomain,
  });
  return true;
}

async function refreshAll({ asin } = {}) {
  if (syncing) return;
  syncing = true;
  setStatus("Syncing library from Kindle…");
  const refreshBtn = document.getElementById("refreshBtn");
  if (refreshBtn) refreshBtn.disabled = true;

  try {
    const res = await extensionRequest("refreshAndSync", {
      asin: asin || bookSelect.value || undefined,
    });

    if (res?.error) {
      setStatus(res.error, "error");
      if (res.auth) await applyAuthStatus(res.auth);
      return;
    }

    if (res?.auth) await applyAuthStatus(res.auth);

    if (res?.history) {
      historyByBook = { ...historyByBook, ...res.history };
      saveHistory(historyByBook);
    }

    const target = res?.asin || bookSelect.value;
    refreshBookSelect(target);
    if (target) {
      bookSelect.value = target;
      saveSelectedBook(target);
    }
    renderSelectedBook();
    setStatus(
      res?.library?.length
        ? `Updated ${res.library.length} book(s) from your library.`
        : "Sync complete.",
      "success",
    );
  } catch (e) {
    setStatus(e.message, "error");
    setAuthStatus(e.message, "error");
  } finally {
    syncing = false;
    if (refreshBtn) refreshBtn.disabled = false;
  }
}

async function checkAuth() {
  try {
    const res = await extensionRequest("getAuthStatus");
    return res?.auth;
  } catch {
    return null;
  }
}

async function signOut({ clearHistory = false } = {}) {
  try {
    await extensionRequest("clearAuth", { clearHistory });
  } catch {
    /* extension may be unreachable */
  }
  clearLocalAppData({ history: clearHistory });
  historyByBook = clearHistory ? {} : loadHistory();
  if (!clearHistory) await loadHistoryFromExtension();
  saveCredentialsMeta(null);
  destroyChart();
  statsEl.textContent = "";
  setStatus(
    clearHistory
      ? "Signed out and cleared all local data."
      : "Signed out. Re-open Cloud Reader to connect again.",
    "info",
  );
  setAuthStatus("Signed out.", "info");
  refreshBookSelect();
}

function openCloudReader() {
  window.open("https://read.amazon.com/kindle-library", "_blank", "noopener");
}

async function loadHistoryFromExtension() {
  try {
    const res = await extensionRequest("getHistory");
    if (res?.history && typeof res.history === "object") {
      historyByBook = res.history;
      saveHistory(historyByBook);
    }
  } catch {
    /* not connected yet */
  }
}

async function bootstrap() {
  await loadHistoryFromExtension();
  const auth = await checkAuth();
  if (auth) await applyAuthStatus(auth);
  refreshBookSelect();
  renderSelectedBook();
  if (auth?.authenticated) await refreshAll();
}

document
  .getElementById("refreshBtn")
  ?.addEventListener("click", () => refreshAll());
document
  .getElementById("openKindleBtn")
  ?.addEventListener("click", openCloudReader);
document
  .getElementById("signOutBtn")
  ?.addEventListener("click", () => signOut({ clearHistory: false }));
document.getElementById("resetAllBtn")?.addEventListener("click", async () => {
  if (
    !confirm(
      "Clear Amazon session and delete all reading history on this device?",
    )
  )
    return;
  await signOut({ clearHistory: true });
});
document.getElementById("connectBtn")?.addEventListener("click", () => {
  openCloudReader();
  setTimeout(() => refreshAll(), 3000);
});

bookSelect.addEventListener("change", async () => {
  const asin = bookSelect.value;
  saveSelectedBook(asin);
  renderSelectedBook();
  if (asin) await refreshAll({ asin });
});

xAxisMode.addEventListener("change", renderSelectedBook);
showTrend.addEventListener("change", renderSelectedBook);

document.getElementById("exportHistoryBtn")?.addEventListener("click", () => {
  const blob = new Blob([JSON.stringify(historyByBook, null, 2)], {
    type: "application/json",
  });
  const a = document.createElement("a");
  a.href = URL.createObjectURL(blob);
  a.download = "kindle-reading-history.json";
  a.click();
  URL.revokeObjectURL(a.href);
});

refreshBookSelect();
bootstrap();
