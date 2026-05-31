// Guard against double-injection: when the service worker injects content.js
// programmatically into a tab that already had it auto-injected, we'd otherwise
// register duplicate listeners and observers.
if (window.__kindleSyncContentLoaded) {
  console.log("[KindleSync] content.js already loaded; skipping re-init");
} else {
  window.__kindleSyncContentLoaded = true;
  initKindleSyncContent();
}

function initKindleSyncContent() {
  const CLIENT_VERSION = "20000100";

  // Auth context provided per-message by background.js. We cache the
  // deviceSessionToken returned by /register/getDeviceToken so subsequent
  // requests can include the x-adp-session-token header that Amazon's
  // reader endpoints require (otherwise we get 403).
  let _authCtx = { deviceToken: "", sessionId: "", adpSessionToken: "" };

  function readBase() {
    return window.location.origin;
  }

  function authHeaders() {
    const headers = {};
    if (_authCtx.sessionId) headers["x-amzn-sessionid"] = _authCtx.sessionId;
    if (_authCtx.adpSessionToken)
      headers["x-adp-session-token"] = _authCtx.adpSessionToken;
    return headers;
  }

  async function fetchJson(url, options = {}) {
    const res = await fetch(url, {
      credentials: "include",
      headers: {
        Accept: "application/json",
        ...authHeaders(),
        ...options.headers,
      },
      ...options,
    });
    if (!res.ok) {
      throw new Error(`Kindle request failed (${res.status}): ${url}`);
    }
    return res.json();
  }

  async function ensureDeviceSession() {
    if (_authCtx.adpSessionToken) return _authCtx.adpSessionToken;
    if (!_authCtx.deviceToken) return null;
    const params = new URLSearchParams({
      serialNumber: _authCtx.deviceToken,
      deviceType: _authCtx.deviceToken,
    });
    const url = `${readBase()}/service/web/register/getDeviceToken?${params.toString()}`;
    try {
      const info = await fetchJson(url);
      if (info?.deviceSessionToken) {
        _authCtx.adpSessionToken = info.deviceSessionToken;
        console.log("[KindleSync] obtained deviceSessionToken");
      }
      return _authCtx.adpSessionToken || null;
    } catch (e) {
      console.warn("[KindleSync] getDeviceToken failed:", e);
      return null;
    }
  }

  async function fetchLibrary() {
    const base = readBase();
    const url = `${base}/kindle-library/search?query=&libraryType=BOOKS&sortType=recency&querySize=50`;
    const body = await fetchJson(url);
    const items = body.itemsList || [];
    if (items[0]) {
      console.log("[KindleSync] FULL first library item:", items[0]);
      console.log("[KindleSync] library item keys:", Object.keys(items[0]));
      console.log(
        "[KindleSync] library response top-level keys:",
        Object.keys(body),
      );
    }
    return items.map((item) => ({
      asin: item.asin,
      title: item.title,
      percentageRead:
        item.percentageRead != null
          ? Number(item.percentageRead)
          : item.readingProgress?.percentageRead != null
            ? Number(item.readingProgress.percentageRead)
            : null,
      syncDate:
        item.progress?.syncDate || item.readingProgress?.syncDate || null,
    }));
  }

  let _loggedStartReading = false;

  async function fetchBookProgress(asin) {
    const base = readBase();
    const startUrl = `${base}/service/mobile/reader/startReading?asin=${encodeURIComponent(
      asin,
    )}&clientVersion=${CLIENT_VERSION}`;
    const info = await fetchJson(startUrl);
    if (!_loggedStartReading) {
      _loggedStartReading = true;
      console.log("[KindleSync] FULL first startReading response:", info);
      console.log("[KindleSync] startReading keys:", Object.keys(info || {}));
    }

    // Metadata fetch is best-effort: many books expose endPosition directly on
    // the startReading response, so we don't want to throw if metadataUrl is
    // missing or unreachable. The URL is pre-signed by Amazon for S3, so we
    // explicitly omit cookies (S3 returns Access-Control-Allow-Origin: * and
    // CORS forbids that with credentialed requests).
    let meta = {};
    if (info.metadataUrl) {
      try {
        const metaRes = await fetch(info.metadataUrl, {
          credentials: "omit",
          mode: "cors",
        });
        const metaText = await metaRes.text();
        const jsonMatch = metaText.match(/\{[\s\S]*\}/);
        if (jsonMatch) meta = JSON.parse(jsonMatch[0]);
      } catch (e) {
        console.warn(
          `[KindleSync] metadata fetch failed for ${asin}:`,
          e.message,
        );
      }
    }

    // Amazon uses position -1 (and null syncTime / deviceName) to mean
    // "web reader has no cached Whispersync data for this book". Treat that
    // as absence of data rather than 0% progress.
    const rawPosition = info.lastPageReadData?.position;
    const hasPosition = typeof rawPosition === "number" && rawPosition >= 0;
    const position = hasPosition ? rawPosition : 0;
    const endPosition = meta.endPosition ?? info.endPosition ?? 0;
    const startPosition = meta.startPosition ?? 0;
    const reportedOnDevice = info.lastPageReadData?.deviceName;

    let percentageRead = 0;
    if (endPosition > 0) {
      const rough = (position - startPosition) / (endPosition - startPosition);
      percentageRead = Math.min(
        100,
        Math.max(0, Number((rough * 100).toFixed(2))),
      );
    } else if (typeof info.percentageRead === "number") {
      percentageRead = Math.min(100, Math.max(0, Number(info.percentageRead)));
    }

    const syncTime = info.lastPageReadData?.syncTime;

    // Per-book diagnostic. Look for books you've read on a Kindle device but
    // never opened in the web reader; these will tell us whether the position
    // is missing from Amazon or just missing from `startReading`.
    console.log(
      `[KindleSync] book ${asin}`,
      JSON.stringify({
        title: info.title,
        position,
        startPosition,
        endPosition,
        percentageRead,
        syncTime,
        reportedOnDevice,
        hasMetadataUrl: Boolean(info.metadataUrl),
        metaKeys: Object.keys(meta),
        infoLastPageReadKeys: Object.keys(info.lastPageReadData || {}),
      }),
    );
    return {
      asin,
      title: info.title,
      point: {
        timestamp: syncTime
          ? new Date(syncTime).toISOString()
          : new Date().toISOString(),
        progress: percentageRead,
      },
    };
  }

  chrome.runtime.onMessage.addListener((request, _sender, sendResponse) => {
    if (request?.action !== "fetchProgress") return;

    (async () => {
      try {
        if (request.auth) {
          if (request.auth.deviceToken)
            _authCtx.deviceToken = request.auth.deviceToken;
          if (request.auth.sessionId)
            _authCtx.sessionId = request.auth.sessionId;
        }
        // Make sure we have an x-adp-session-token before hitting protected
        // endpoints; without it Amazon returns 403 from startReading.
        await ensureDeviceSession();

        if (request.asin) {
          const detail = await fetchBookProgress(request.asin);
          console.log(
            "[KindleSync] fetchBookProgress result for",
            request.asin,
            detail,
          );
          sendResponse(detail);
          return;
        }
        const library = await fetchLibrary();
        sendResponse({ library });
      } catch (e) {
        console.warn("[KindleSync] fetchProgress error:", e);
        sendResponse({ error: e.message });
      }
    })();

    return true;
  });

  // Observe Whispersync-style payloads when the Cloud Reader loads them.
  const progressObserver = new PerformanceObserver((list) => {
    for (const entry of list.getEntriesByType("resource")) {
      const name = entry.name || "";
      if (!/progress/i.test(name)) continue;
      chrome.runtime
        .sendMessage({ action: "networkProgressHint", url: name })
        .catch(() => {});
    }
  });

  try {
    progressObserver.observe({ type: "resource", buffered: true });
  } catch {
    /* PerformanceObserver may be unavailable */
  }
} // end initKindleSyncContent
