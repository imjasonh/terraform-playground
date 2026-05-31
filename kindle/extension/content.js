const CLIENT_VERSION = '20000100';

function readBase() {
  return window.location.origin;
}

async function fetchJson(url, options = {}) {
  const res = await fetch(url, {
    credentials: 'include',
    headers: {
      Accept: 'application/json',
      ...options.headers,
    },
    ...options,
  });
  if (!res.ok) {
    throw new Error(`Kindle request failed (${res.status}): ${url}`);
  }
  return res.json();
}

async function fetchLibrary() {
  const base = readBase();
  const url = `${base}/kindle-library/search?query=&libraryType=BOOKS&sortType=recency&querySize=50`;
  const body = await fetchJson(url);
  const items = body.itemsList || [];
  return items.map((item) => ({
    asin: item.asin,
    title: item.title,
    percentageRead:
      item.percentageRead != null
        ? Number(item.percentageRead)
        : item.readingProgress?.percentageRead,
    syncDate: item.progress?.syncDate || new Date().toISOString(),
  }));
}

async function fetchBookProgress(asin) {
  const base = readBase();
  const startUrl = `${base}/service/mobile/reader/startReading?asin=${encodeURIComponent(
    asin
  )}&clientVersion=${CLIENT_VERSION}`;
  const info = await fetchJson(startUrl);
  const metaUrl = info.metadataUrl;
  if (!metaUrl) throw new Error('No metadata URL in startReading response');

  const metaRes = await fetch(metaUrl, { credentials: 'include' });
  const metaText = await metaRes.text();
  const jsonMatch = metaText.match(/\{[\s\S]*\}/);
  const meta = jsonMatch ? JSON.parse(jsonMatch[0]) : {};

  const position = info.lastPageReadData?.position ?? 0;
  const endPosition = meta.endPosition ?? info.endPosition ?? 1;
  const startPosition = meta.startPosition ?? 0;
  const rough = (startPosition + position) / endPosition;
  const percentageRead = Math.min(100, Math.max(0, Number((rough * 100).toFixed(2))));
  const syncTime = info.lastPageReadData?.syncTime;

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
  if (request?.action !== 'fetchProgress') return;

  (async () => {
    try {
      if (request.asin) {
        const detail = await fetchBookProgress(request.asin);
        sendResponse(detail);
        return;
      }
      const library = await fetchLibrary();
      sendResponse({ library });
    } catch (e) {
      sendResponse({ error: e.message });
    }
  })();

  return true;
});

// Observe Whispersync-style payloads when the Cloud Reader loads them.
const progressObserver = new PerformanceObserver((list) => {
  for (const entry of list.getEntriesByType('resource')) {
    const name = entry.name || '';
    if (!/progress/i.test(name)) continue;
    chrome.runtime.sendMessage({ action: 'networkProgressHint', url: name }).catch(() => {});
  }
});

try {
  progressObserver.observe({ type: 'resource', buffered: true });
} catch {
  /* PerformanceObserver may be unavailable */
}
