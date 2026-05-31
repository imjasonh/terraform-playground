const COOKIE_NAMES = ["ubid-main", "at-main", "x-main", "sid", "session-id"];
const READ_ORIGINS = [
  "https://read.amazon.com",
  "https://read.amazon.co.uk",
  "https://read.amazon.ca",
  "https://read.amazon.de",
];

const EMPTY_TOKENS = {
  ubid: "",
  at: "",
  xMain: "",
  sid: "",
  sessionId: "",
  deviceToken: "",
  deviceType: "",
  amazonDomain: "amazon.com",
};

/** @type {typeof EMPTY_TOKENS} */
let extractedTokens = { ...EMPTY_TOKENS };

function resolveAmazonBase() {
  return extractedTokens.amazonDomain.startsWith("http")
    ? extractedTokens.amazonDomain
    : `https://www.${extractedTokens.amazonDomain}`;
}

function hasSessionCookies() {
  return Boolean(
    extractedTokens.ubid ||
    extractedTokens.sid ||
    extractedTokens.sessionId ||
    extractedTokens.at,
  );
}

function getAuthStatus() {
  const missing = [];
  if (!hasSessionCookies()) missing.push("Amazon session cookies");
  if (!extractedTokens.deviceToken)
    missing.push("device token (open Cloud Reader once)");

  return {
    authenticated: hasSessionCookies(),
    ready: hasSessionCookies() && Boolean(extractedTokens.deviceToken),
    missing,
    amazonDomain: extractedTokens.amazonDomain,
    hasDeviceToken: Boolean(extractedTokens.deviceToken),
  };
}

async function persistCredentials() {
  await chrome.storage.local.set({
    kindleCredentials: { ...extractedTokens, updatedAt: Date.now() },
  });
}

async function loadStoredCredentials() {
  const { kindleCredentials } =
    await chrome.storage.local.get("kindleCredentials");
  if (kindleCredentials && typeof kindleCredentials === "object") {
    extractedTokens = { ...EMPTY_TOKENS, ...kindleCredentials };
  }
}

async function fetchAmazonCookies() {
  const base = resolveAmazonBase();
  const readBase = base.replace("www.", "read.");

  for (const origin of [base, readBase, ...READ_ORIGINS]) {
    for (const name of COOKIE_NAMES) {
      try {
        const cookie = await chrome.cookies.get({ url: origin, name });
        if (!cookie?.value) continue;
        if (name === "ubid-main") extractedTokens.ubid = cookie.value;
        if (name === "at-main") extractedTokens.at = cookie.value;
        if (name === "x-main") extractedTokens.xMain = cookie.value;
        if (name === "sid") extractedTokens.sid = cookie.value;
        if (name === "session-id") extractedTokens.sessionId = cookie.value;
      } catch {
        /* ignore per-origin failures */
      }
    }
  }

  await persistCredentials();
  return getAuthStatus();
}

chrome.webRequest.onBeforeRequest.addListener(
  (details) => {
    try {
      const url = new URL(details.url);
      const serialNumber = url.searchParams.get("serialNumber");
      const deviceType = url.searchParams.get("deviceType");
      if (serialNumber) extractedTokens.deviceToken = serialNumber;
      if (deviceType) extractedTokens.deviceType = deviceType;
      if (serialNumber || deviceType) {
        fetchAmazonCookies();
      }
      if (url.hostname.includes("amazon.")) {
        extractedTokens.amazonDomain = url.hostname.replace(/^www\./, "");
      }
    } catch {
      /* ignore malformed URLs */
    }
  },
  {
    urls: [
      "*://*.amazon.com/*/getDeviceToken*",
      "*://read.amazon.com/*",
      "*://read.amazon.co.uk/*",
    ],
  },
);

async function getReadTab() {
  const tabs = await chrome.tabs.query({
    url: [
      "*://read.amazon.com/*",
      "*://read.amazon.co.uk/*",
      "*://read.amazon.ca/*",
      "*://read.amazon.de/*",
    ],
  });
  return tabs[0];
}

async function ensureContentScript(tabId) {
  try {
    await chrome.scripting.executeScript({
      target: { tabId },
      files: ["content.js"],
    });
    return true;
  } catch (e) {
    console.warn("[KindleSync] failed to inject content.js:", e);
    return false;
  }
}

async function messageContentScript(message) {
  const tab = await getReadTab();
  if (!tab?.id) {
    return {
      error:
        "Open Kindle Cloud Reader (read.amazon.com) in a tab while signed in, then refresh.",
    };
  }
  // Attach Amazon-specific auth context that the content script needs to
  // set on protected endpoints (startReading, kindle-library, etc.).
  const enriched = {
    ...message,
    auth: {
      deviceToken: extractedTokens.deviceToken,
      sessionId: extractedTokens.sessionId,
    },
  };
  try {
    return await chrome.tabs.sendMessage(tab.id, enriched);
  } catch (firstErr) {
    // Content script not present (e.g. tab loaded before extension reload).
    // Try injecting it once and retry.
    const injected = await ensureContentScript(tab.id);
    if (!injected) {
      return {
        error: `Could not reach Kindle tab: ${firstErr.message}. Refresh read.amazon.com.`,
      };
    }
    try {
      return await chrome.tabs.sendMessage(tab.id, enriched);
    } catch (e) {
      return {
        error: `Could not reach Kindle tab after injecting content script: ${e.message}. Refresh read.amazon.com.`,
      };
    }
  }
}

async function loadHistory() {
  const { kindleHistory } = await chrome.storage.local.get("kindleHistory");
  return kindleHistory && typeof kindleHistory === "object"
    ? kindleHistory
    : {};
}

async function saveHistory(history) {
  await chrome.storage.local.set({ kindleHistory: history });
}

function appendPoint(history, asin, title, timestamp, progress) {
  const key = asin || "_default";
  const list = history[key] || [];
  const entry = {
    timestamp,
    progress,
    title: title || undefined,
  };
  const id = `${entry.timestamp}|${entry.progress}`;
  const filtered = list.filter((p) => `${p.timestamp}|${p.progress}` !== id);
  filtered.push(entry);
  filtered.sort((a, b) => new Date(a.timestamp) - new Date(b.timestamp));
  return { ...history, [key]: filtered };
}

function broadcastSyncProgress(payload) {
  // Fire-and-forget broadcast to dashboard / popup. Ignore "no receiver" errors.
  try {
    chrome.runtime
      .sendMessage({ action: "syncProgress", ...payload })
      .catch(() => {});
  } catch {
    /* sendMessage not available */
  }
}

async function enrichLibraryWithProgress(library, { concurrency = 5 } = {}) {
  // The /kindle-library/search endpoint usually omits per-book progress, so we
  // back-fill by calling startReading for each ASIN. Books that have never been
  // opened return progress 0 and get filtered out by the caller.
  const targets = library.filter((b) => b.asin);
  const results = new Map();
  if (!targets.length) return results;

  const queue = targets.slice();
  let done = 0;
  const total = targets.length;

  broadcastSyncProgress({ phase: "enrich", done: 0, total });

  const worker = async () => {
    while (queue.length) {
      const book = queue.shift();
      try {
        const detail = await messageContentScript({
          action: "fetchProgress",
          asin: book.asin,
        });
        if (detail && !detail.error && detail.point) {
          results.set(book.asin, {
            title: detail.title || book.title,
            timestamp: detail.point.timestamp,
            progress: detail.point.progress,
          });
        }
      } catch {
        /* skip individual book failures */
      }
      done++;
      broadcastSyncProgress({ phase: "enrich", done, total });
    }
  };

  const workerCount = Math.max(1, Math.min(concurrency, targets.length));
  await Promise.all(Array.from({ length: workerCount }, worker));
  broadcastSyncProgress({ phase: "done", done: total, total });
  return results;
}

async function syncLibraryToHistory(history) {
  const contentRes = await messageContentScript({ action: "fetchProgress" });
  if (contentRes?.error) {
    return { error: contentRes.error, history };
  }

  if (!contentRes?.library?.length) {
    return {
      error: "No books returned from Kindle. Sign in on read.amazon.com.",
      history,
    };
  }

  const library = contentRes.library.filter((b) => b.asin);

  // First, record anything the library payload already gave us directly.
  let next = history;
  const alreadyKnown = new Set();
  for (const book of library) {
    if (book.percentageRead != null && book.percentageRead > 0) {
      next = appendPoint(
        next,
        book.asin,
        book.title,
        book.syncDate || new Date().toISOString(),
        book.percentageRead,
      );
      alreadyKnown.add(book.asin);
    }
  }

  // Back-fill the rest from startReading (the library endpoint usually has no
  // percentageRead field, so this is what actually populates the dashboard).
  const needsEnrichment = library.filter((b) => !alreadyKnown.has(b.asin));
  if (needsEnrichment.length) {
    const enriched = await enrichLibraryWithProgress(needsEnrichment);
    for (const [asin, info] of enriched) {
      if (info.progress > 0) {
        next = appendPoint(
          next,
          asin,
          info.title,
          info.timestamp,
          info.progress,
        );
      }
    }
  }

  await saveHistory(next);
  return { history: next, library };
}

async function syncBookDetail(history, asin) {
  if (!asin) return { history };
  const contentRes = await messageContentScript({
    action: "fetchProgress",
    asin,
  });
  if (contentRes?.error || !contentRes?.point) {
    return { history, detailError: contentRes?.error };
  }
  const next = appendPoint(
    history,
    contentRes.asin,
    contentRes.title,
    contentRes.point.timestamp,
    contentRes.point.progress,
  );
  await saveHistory(next);
  return {
    history: next,
    asin: contentRes.asin,
    title: contentRes.title,
    point: contentRes.point,
  };
}

async function clearAuth({ clearHistory = false } = {}) {
  extractedTokens = { ...EMPTY_TOKENS };
  await chrome.storage.local.remove("kindleCredentials");
  if (clearHistory) {
    await chrome.storage.local.remove("kindleHistory");
  }
  return { cleared: true, auth: getAuthStatus() };
}

async function refreshAndSync({ asin } = {}) {
  const auth = await fetchAmazonCookies();
  if (!auth.authenticated) {
    return {
      error:
        "No Amazon session found. Sign in at amazon.com, open read.amazon.com, then click Refresh.",
      auth,
    };
  }

  let history = await loadHistory();
  const libResult = await syncLibraryToHistory(history);
  if (libResult.error && !libResult.library?.length) {
    return { error: libResult.error, auth, history: libResult.history };
  }
  history = libResult.history;

  let detail;
  const targetAsin =
    asin || libResult.library?.find((b) => b.percentageRead > 0)?.asin;
  if (targetAsin) {
    detail = await syncBookDetail(history, targetAsin);
    history = detail.history;
  }

  return {
    auth: getAuthStatus(),
    history,
    library: libResult.library,
    asin: detail?.asin || targetAsin,
    title: detail?.title,
  };
}

async function handleMessage(request, sendResponse) {
  const action = request?.action;

  if (action === "ping") {
    sendResponse({ ok: true, extensionId: chrome.runtime.id });
    return;
  }

  if (action === "getAuthStatus") {
    await fetchAmazonCookies();
    sendResponse({ auth: getAuthStatus() });
    return;
  }

  if (action === "getKindleCredentials") {
    await fetchAmazonCookies();
    sendResponse({
      auth: getAuthStatus(),
      credentials: { ...extractedTokens },
    });
    return;
  }

  if (action === "getHistory") {
    sendResponse({ history: await loadHistory() });
    return;
  }

  if (action === "clearAuth") {
    const result = await clearAuth({
      clearHistory: Boolean(request.clearHistory),
    });
    sendResponse(result);
    return;
  }

  if (action === "refreshAndSync" || action === "syncReadingProgress") {
    const result = await refreshAndSync({ asin: request.asin });
    sendResponse(result);
    return;
  }

  sendResponse({ error: "Unknown action" });
}

function listen(handler) {
  return (request, sender, sendResponse) => {
    handler(request, sendResponse).catch((err) => {
      sendResponse({ error: err.message, auth: getAuthStatus() });
    });
    return true;
  };
}

chrome.runtime.onMessage.addListener((request, sender, sendResponse) => {
  const allowed = [
    "ping",
    "getAuthStatus",
    "getKindleCredentials",
    "getHistory",
    "clearAuth",
    "refreshAndSync",
    "syncReadingProgress",
  ];
  if (!allowed.includes(request?.action)) return false;
  listen(handleMessage)(request, sender, sendResponse);
  return true;
});

chrome.runtime.onInstalled.addListener(async () => {
  await loadStoredCredentials();
  await fetchAmazonCookies();
});

loadStoredCredentials();
