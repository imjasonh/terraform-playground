import { historyEntriesToPoints, toChartSeries } from './parse.js';
import { renderProgressChart, destroyChart, computeStats } from './chart.js';
import {
  loadHistory,
  saveHistory,
  loadSelectedBook,
  saveSelectedBook,
  saveCredentialsMeta,
  clearLocalAppData,
} from './storage.js';
import {
  extensionRequest,
  isExtensionAvailable,
  isExtensionPage,
} from './extension-bridge.js';

const bookSelect = document.getElementById('bookSelect');
const xAxisMode = document.getElementById('xAxisMode');
const showTrend = document.getElementById('showTrend');
const statsEl = document.getElementById('stats');
const statusEl = document.getElementById('status');
const authStatusEl = document.getElementById('authStatus');
const setupPanel = document.getElementById('setupPanel');
const mainPanel = document.getElementById('mainPanel');
const extensionIdInput = document.getElementById('extensionId');

let historyByBook = loadHistory();
let syncing = false;

function setStatus(message, type = 'info') {
  statusEl.textContent = message;
  statusEl.dataset.type = type;
}

function setAuthStatus(text, type = 'info') {
  authStatusEl.textContent = text;
  authStatusEl.dataset.type = type;
}

function booksWithProgress() {
  return Object.entries(historyByBook).filter(([, entries]) =>
    entries?.some((e) => e.progress > 0)
  );
}

function refreshBookSelect(preferredAsin) {
  const pairs = booksWithProgress();
  const selected = preferredAsin || loadSelectedBook();

  bookSelect.innerHTML = '';
  if (!pairs.length) {
    const opt = document.createElement('option');
    opt.value = '';
    opt.textContent = 'No books yet — connect Amazon and refresh';
    bookSelect.appendChild(opt);
    return;
  }

  for (const [asin, entries] of pairs) {
    const title = entries.find((e) => e.title)?.title || asin;
    const opt = document.createElement('option');
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
    statsEl.textContent = '';
    return;
  }

  const points = historyEntriesToPoints(historyByBook, asin);
  if (!points.length) {
    destroyChart();
    statsEl.textContent = 'No progress snapshots yet for this book. Click Refresh after reading.';
    return;
  }

  const mode = xAxisMode.value === 'days' ? 'daysFromStart' : 'calendar';
  const series = toChartSeries(points, mode);
  const bookTitle = bookSelect.selectedOptions[0]?.textContent?.split(' (')[0];

  renderProgressChart(
    document.getElementById('progressChart'),
    { ...series, bookTitle },
    {
      xLabel: mode === 'daysFromStart' ? 'Days from first sync (t)' : 'Timeline',
      showTrend: showTrend.checked,
    }
  );

  const stats = computeStats(series.values);
  if (stats) {
    statsEl.textContent = `Started at ${stats.startPercent.toFixed(1)}% → now ${stats.currentPercent.toFixed(1)}% (+${stats.totalGain.toFixed(1)}%). Reading sessions: ~${stats.readingSessions}.`;
  }
}

function showPanels({ connected }) {
  if (setupPanel) setupPanel.hidden = connected;
  if (mainPanel) mainPanel.hidden = !connected;
}

async function applyAuthStatus(auth) {
  if (!auth?.authenticated) {
    setAuthStatus('Not signed in — open Kindle Cloud Reader while logged into Amazon.', 'warn');
    showPanels({ connected: false });
    return false;
  }

  if (!auth.ready) {
    setAuthStatus(
      `Session found; still need: ${auth.missing?.join(', ') || 'device token'}. Open read.amazon.com and reload that tab.`,
      'warn'
    );
    showPanels({ connected: true });
    return true;
  }

  setAuthStatus(`Connected (${auth.amazonDomain || 'amazon.com'}).`, 'success');
  saveCredentialsMeta({
    hasCookies: true,
    hasDeviceToken: auth.hasDeviceToken,
    amazonDomain: auth.amazonDomain,
  });
  showPanels({ connected: true });
  return true;
}

async function refreshAll({ asin } = {}) {
  if (syncing) return;
  syncing = true;
  setStatus('Syncing library from Kindle…');
  const refreshBtn = document.getElementById('refreshBtn');
  if (refreshBtn) refreshBtn.disabled = true;

  try {
    const res = await extensionRequest('refreshAndSync', { asin: asin || bookSelect.value || undefined });

    if (res?.error) {
      setStatus(res.error, 'error');
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
        : 'Sync complete.',
      'success'
    );
  } catch (e) {
    setStatus(e.message, 'error');
    setAuthStatus(e.message, 'error');
    showPanels({ connected: false });
  } finally {
    syncing = false;
    if (refreshBtn) refreshBtn.disabled = false;
  }
}

async function checkAuth() {
  try {
    const res = await extensionRequest('getAuthStatus');
    return res?.auth;
  } catch {
    return null;
  }
}

async function signOut({ clearHistory = false } = {}) {
  try {
    await extensionRequest('clearAuth', { clearHistory });
  } catch {
    /* extension may be unreachable */
  }
  clearLocalAppData({ history: clearHistory });
  historyByBook = clearHistory ? {} : loadHistory();
  if (!clearHistory) await loadHistoryFromExtension();
  saveCredentialsMeta(null);
  destroyChart();
  statsEl.textContent = '';
  setStatus(clearHistory ? 'Signed out and cleared all local data.' : 'Signed out. Re-open Cloud Reader to connect again.', 'info');
  setAuthStatus('Signed out.', 'info');
  showPanels({ connected: false });
  refreshBookSelect();
}

function openCloudReader() {
  window.open('https://read.amazon.com/kindle-library', '_blank', 'noopener');
}

async function saveExtensionId() {
  const id = extensionIdInput?.value?.trim();
  if (!id) return false;
  localStorage.setItem('kindle-extension-id', id);
  return true;
}

async function loadHistoryFromExtension() {
  try {
    const res = await extensionRequest('getHistory');
    if (res?.history && typeof res.history === 'object') {
      historyByBook = res.history;
      saveHistory(historyByBook);
    }
  } catch {
    /* not connected yet */
  }
}

async function bootstrap() {
  if (isExtensionPage()) {
    showPanels({ connected: true });
    await loadHistoryFromExtension();
    const auth = await checkAuth();
    if (auth) await applyAuthStatus(auth);
    refreshBookSelect();
    renderSelectedBook();
    if (auth?.authenticated) await refreshAll();
    return;
  }

  if (!isExtensionAvailable()) {
    setAuthStatus('Install the Chrome extension and open this page via the extension popup.', 'warn');
    showPanels({ connected: false });
    return;
  }

  const storedId = localStorage.getItem('kindle-extension-id');
  if (extensionIdInput && storedId) extensionIdInput.value = storedId;

  if (!storedId) {
    setAuthStatus('Open the dashboard from the extension popup (recommended), or enter your extension ID below.', 'warn');
    showPanels({ connected: false });
    return;
  }

  await loadHistoryFromExtension();
  refreshBookSelect();
  renderSelectedBook();

  const auth = await checkAuth();
  if (auth?.ready) {
    await applyAuthStatus(auth);
    await refreshAll();
  } else if (auth?.authenticated) {
    await applyAuthStatus(auth);
    setStatus('Open read.amazon.com, then click Refresh.', 'warn');
  } else {
    showPanels({ connected: false });
    setAuthStatus('Session expired or missing. Open Cloud Reader and sign in.', 'warn');
  }
}

document.getElementById('refreshBtn')?.addEventListener('click', () => refreshAll());
document.getElementById('openKindleBtn')?.addEventListener('click', openCloudReader);
document.getElementById('signOutBtn')?.addEventListener('click', () => signOut({ clearHistory: false }));
document.getElementById('resetAllBtn')?.addEventListener('click', async () => {
  if (!confirm('Clear Amazon session and delete all reading history on this device?')) return;
  await signOut({ clearHistory: true });
});
document.getElementById('connectBtn')?.addEventListener('click', async () => {
  openCloudReader();
  if (!isExtensionPage() && extensionIdInput) {
    await saveExtensionId();
  }
  setTimeout(() => refreshAll(), 3000);
});
document.getElementById('saveExtensionIdBtn')?.addEventListener('click', async () => {
  if (!(await saveExtensionId())) {
    setStatus('Enter your extension ID first.', 'warn');
    return;
  }
  await bootstrap();
});

bookSelect.addEventListener('change', async () => {
  const asin = bookSelect.value;
  saveSelectedBook(asin);
  renderSelectedBook();
  if (asin) await refreshAll({ asin });
});

xAxisMode.addEventListener('change', renderSelectedBook);
showTrend.addEventListener('change', renderSelectedBook);

document.getElementById('exportHistoryBtn')?.addEventListener('click', () => {
  const blob = new Blob([JSON.stringify(historyByBook, null, 2)], { type: 'application/json' });
  const a = document.createElement('a');
  a.href = URL.createObjectURL(blob);
  a.download = 'kindle-reading-history.json';
  a.click();
  URL.revokeObjectURL(a.href);
});

refreshBookSelect();
bootstrap();
