const COOKIE_NAMES = ['ubid-main', 'at-main', 'x-main', 'sid', 'session-id'];
const READ_ORIGINS = [
  'https://read.amazon.com',
  'https://read.amazon.co.uk',
  'https://read.amazon.ca',
  'https://read.amazon.de',
];

/** @type {Record<string, string>} */
let amazonOrigins = ['https://www.amazon.com'];

/** @type {{
 *   ubid: string; at: string; xMain: string; sid: string; sessionId: string;
 *   deviceToken: string; deviceType: string; amazonDomain: string;
 * }} */
let extractedTokens = {
  ubid: '',
  at: '',
  xMain: '',
  sid: '',
  sessionId: '',
  deviceToken: '',
  deviceType: '',
  amazonDomain: 'amazon.com',
};

function resolveAmazonBase() {
  return extractedTokens.amazonDomain.startsWith('http')
    ? extractedTokens.amazonDomain
    : `https://www.${extractedTokens.amazonDomain}`;
}

async function fetchAmazonCookies() {
  const base = resolveAmazonBase();
  const readBase = base.replace('www.', 'read.');

  for (const origin of [base, readBase, ...READ_ORIGINS]) {
    for (const name of COOKIE_NAMES) {
      try {
        const cookie = await chrome.cookies.get({ url: origin, name });
        if (!cookie?.value) continue;
        if (name === 'ubid-main') extractedTokens.ubid = cookie.value;
        if (name === 'at-main') extractedTokens.at = cookie.value;
        if (name === 'x-main') extractedTokens.xMain = cookie.value;
        if (name === 'sid') extractedTokens.sid = cookie.value;
        if (name === 'session-id') extractedTokens.sessionId = cookie.value;
      } catch {
        /* ignore per-origin failures */
      }
    }
  }

  await chrome.storage.local.set({
    kindleCredentials: { ...extractedTokens, updatedAt: Date.now() },
  });
}

chrome.webRequest.onBeforeRequest.addListener(
  (details) => {
    try {
      const url = new URL(details.url);
      const serialNumber = url.searchParams.get('serialNumber');
      const deviceType = url.searchParams.get('deviceType');
      if (serialNumber) extractedTokens.deviceToken = serialNumber;
      if (deviceType) extractedTokens.deviceType = deviceType;
      if (serialNumber || deviceType) {
        fetchAmazonCookies();
      }
      if (url.hostname.includes('amazon.')) {
        extractedTokens.amazonDomain = url.hostname.replace(/^www\./, '');
        amazonOrigins = [`https://www.${extractedTokens.amazonDomain}`];
      }
    } catch {
      /* ignore malformed URLs */
    }
  },
  {
    urls: [
      '*://*.amazon.com/*/getDeviceToken*',
      '*://read.amazon.com/*',
      '*://read.amazon.co.uk/*',
    ],
  }
);

async function getReadTab() {
  const tabs = await chrome.tabs.query({
    url: ['*://read.amazon.com/*', '*://read.amazon.co.uk/*', '*://read.amazon.ca/*'],
  });
  return tabs[0];
}

async function messageContentScript(message) {
  const tab = await getReadTab();
  if (!tab?.id) {
    return {
      error:
        'Open Kindle Cloud Reader (read.amazon.com) in a tab while signed in, then try again.',
    };
  }
  try {
    return await chrome.tabs.sendMessage(tab.id, message);
  } catch (e) {
    return {
      error: `Could not reach Kindle tab: ${e.message}. Refresh read.amazon.com.`,
    };
  }
}

async function loadHistory() {
  const { kindleHistory } = await chrome.storage.local.get('kindleHistory');
  return kindleHistory && typeof kindleHistory === 'object' ? kindleHistory : {};
}

async function saveHistory(history) {
  await chrome.storage.local.set({ kindleHistory: history });
}

function appendPoint(history, asin, title, timestamp, progress) {
  const key = asin || '_default';
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

async function handleMessage(request, sendResponse) {
  const action = request?.action;

  if (action === 'ping') {
    sendResponse({ ok: true, extensionId: chrome.runtime.id });
    return;
  }

  if (action === 'getKindleCredentials') {
    await fetchAmazonCookies();
    sendResponse({ credentials: { ...extractedTokens } });
    return;
  }

  if (action === 'syncReadingProgress') {
    const contentRes = await messageContentScript({
      action: 'fetchProgress',
      asin: request.asin,
    });

    if (contentRes?.error) {
      sendResponse({ message: contentRes.error });
      return;
    }

    let history = await loadHistory();

    if (contentRes?.library?.length) {
      for (const book of contentRes.library) {
        if (book.asin && book.percentageRead != null) {
          history = appendPoint(
            history,
            book.asin,
            book.title,
            book.syncDate || new Date().toISOString(),
            book.percentageRead
          );
        }
      }
      await saveHistory(history);
      sendResponse({ library: contentRes.library, history });
      return;
    }

    if (contentRes?.point && contentRes?.asin) {
      history = appendPoint(
        history,
        contentRes.asin,
        contentRes.title,
        contentRes.point.timestamp,
        contentRes.point.progress
      );
      await saveHistory(history);
      sendResponse({
        asin: contentRes.asin,
        title: contentRes.title,
        points: [contentRes.point],
        history,
      });
      return;
    }

    sendResponse({ message: 'No progress returned from Kindle tab.' });
    return;
  }

  sendResponse({ error: 'Unknown action' });
}

function listen(handler) {
  return (request, sender, sendResponse) => {
    handler(request, sendResponse).catch((err) => {
      sendResponse({ error: err.message });
    });
    return true;
  };
}

chrome.runtime.onMessageExternal.addListener(listen(handleMessage));
chrome.runtime.onMessage.addListener((request, sender, sendResponse) => {
  const allowed = [
    'ping',
    'getKindleCredentials',
    'syncReadingProgress',
  ];
  if (!allowed.includes(request?.action)) return false;
  listen(handleMessage)(request, sender, sendResponse);
  return true;
});

chrome.runtime.onInstalled.addListener(() => {
  fetchAmazonCookies();
});
