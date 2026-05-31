import { normalizeReadingData, toChartSeries } from './parse.js';
import { renderProgressChart, destroyChart, computeStats } from './chart.js';
import {
  loadHistory,
  saveHistory,
  loadSelectedBook,
  saveSelectedBook,
  loadCredentialsMeta,
  saveCredentialsMeta,
  historyToJson,
  appendSnapshot,
} from './storage.js';
import { extensionRequest, detectExtensionId, isExtensionAvailable } from './extension-bridge.js';

const jsonInput = document.getElementById('jsonInput');
const bookSelect = document.getElementById('bookSelect');
const xAxisMode = document.getElementById('xAxisMode');
const showTrend = document.getElementById('showTrend');
const statsEl = document.getElementById('stats');
const statusEl = document.getElementById('status');
const extensionIdInput = document.getElementById('extensionId');
const credentialsStatus = document.getElementById('credentialsStatus');

let historyByBook = loadHistory();

function setStatus(message, type = 'info') {
  statusEl.textContent = message;
  statusEl.dataset.type = type;
}

function refreshBookSelect() {
  const keys = Object.keys(historyByBook);
  const selected = loadSelectedBook();
  bookSelect.innerHTML = '<option value="">— All pasted data —</option>';
  for (const asin of keys) {
    const entries = historyByBook[asin];
    const title = entries?.[0]?.title || asin;
    const opt = document.createElement('option');
    opt.value = asin;
    opt.textContent = `${title} (${entries?.length ?? 0} points)`;
    bookSelect.appendChild(opt);
  }
  if (selected && keys.includes(selected)) {
    bookSelect.value = selected;
    jsonInput.value = historyToJson(historyByBook, selected);
  }
}

function getParsedPoints() {
  let raw;
  try {
    raw = JSON.parse(jsonInput.value.trim() || '[]');
  } catch (e) {
    setStatus(`Invalid JSON: ${e.message}`, 'error');
    return null;
  }

  const { points, errors } = normalizeReadingData(raw);
  if (errors.length && !points.length) {
    setStatus(errors.join(' '), 'error');
    return null;
  }
  if (errors.length) setStatus(errors.join(' '), 'warn');
  else setStatus(`Loaded ${points.length} data point(s).`, 'success');
  return points;
}

function renderFromInput() {
  const points = getParsedPoints();
  if (!points?.length) {
    destroyChart();
    statsEl.textContent = '';
    return;
  }

  const mode = xAxisMode.value === 'days' ? 'daysFromStart' : 'calendar';
  const series = toChartSeries(points, mode);
  const bookTitle = bookSelect.selectedOptions[0]?.textContent?.split(' (')[0];

  renderProgressChart(document.getElementById('progressChart'), {
    ...series,
    bookTitle: bookSelect.value ? bookTitle : undefined,
  }, {
    xLabel: mode === 'daysFromStart' ? 'Days from first sync (t)' : 'Timeline',
    showTrend: showTrend.checked,
  });

  const stats = computeStats(series.values);
  if (stats) {
    statsEl.textContent = `Started at ${stats.startPercent.toFixed(1)}% → now ${stats.currentPercent.toFixed(1)}% (+${stats.totalGain.toFixed(1)}%). Active reading jumps: ~${stats.readingSessions}.`;
  }
}

function saveCurrentToHistory() {
  const points = getParsedPoints();
  if (!points?.length) return;

  const asin = bookSelect.value || prompt('Book ASIN or short id for this series:', '_default');
  if (!asin) return;

  const title = prompt('Book title (optional):', '') || undefined;
  let next = { ...historyByBook };
  for (const p of points) {
    next = appendSnapshot(next, asin, title, {
      timestamp: p.timestamp.toISOString(),
      progress: p.progress,
    });
  }
  historyByBook = next;
  saveHistory(historyByBook);
  saveSelectedBook(asin);
  refreshBookSelect();
  bookSelect.value = asin;
  setStatus(`Saved ${points.length} point(s) under “${asin}”.`, 'success');
}

async function loadBookFromHistory() {
  const asin = bookSelect.value;
  saveSelectedBook(asin);
  if (!asin) return;
  jsonInput.value = historyToJson(historyByBook, asin);
  renderFromInput();
}

async function tryExtensionCredentials() {
  try {
    const res = await extensionRequest('getKindleCredentials');
    const filled = res?.credentials;
    if (!filled?.ubid && !filled?.sid) {
      credentialsStatus.textContent =
        'Extension connected but cookies are empty. Open read.amazon.com while signed in.';
      return;
    }
    saveCredentialsMeta({
      hasCookies: true,
      hasDeviceToken: Boolean(filled.deviceToken),
      amazonDomain: filled.amazonDomain,
    });
    credentialsStatus.textContent = `Credentials captured (${filled.amazonDomain}). Device token: ${filled.deviceToken ? 'yes' : 'pending — refresh Kindle Cloud Reader'}.`;
    setStatus('Credentials loaded from extension (stored only in extension storage).', 'success');
  } catch (e) {
    credentialsStatus.textContent = e.message;
    setStatus(e.message, 'error');
  }
}

async function syncFromExtension() {
  const asin = bookSelect.value;
  try {
    const res = await extensionRequest('syncReadingProgress', { asin: asin || undefined });
    if (res?.history) {
      historyByBook = { ...historyByBook, ...res.history };
      saveHistory(historyByBook);
      refreshBookSelect();
    }
    if (res?.asin) {
      saveSelectedBook(res.asin);
      bookSelect.value = res.asin;
      jsonInput.value = historyToJson(historyByBook, res.asin);
      renderFromInput();
      const last = res.points?.[res.points.length - 1];
      setStatus(
        `Synced “${res.title || res.asin}” at ${last?.progress?.toFixed?.(1) ?? '?'}%.`,
        'success'
      );
      return;
    }
    if (res?.library?.length) {
      setStatus(`Library sync: ${res.library.length} book(s) updated.`, 'success');
      if (!asin && Object.keys(historyByBook).length) {
        const first = Object.keys(historyByBook)[0];
        bookSelect.value = first;
        jsonInput.value = historyToJson(historyByBook, first);
        renderFromInput();
      }
      return;
    }
    setStatus(res?.message || 'Sync completed with no new data.', 'info');
  } catch (e) {
    setStatus(e.message, 'error');
  }
}

async function detectExtension() {
  const manualId = extensionIdInput.value.trim();
  if (!manualId) {
    setStatus('Paste the extension ID from the Kindle Chart Sync popup, then click Detect again.', 'warn');
    return;
  }
  localStorage.setItem('kindle-extension-id', manualId);
  if (!isExtensionAvailable()) {
    setStatus('Open this app at http://localhost:8080 (not file://) so Chrome can connect to the extension.', 'warn');
    return;
  }
  const id = await detectExtensionId();
  if (id) {
    setStatus(`Extension connected: ${id}`, 'success');
    await tryExtensionCredentials();
  } else {
    setStatus('Could not reach extension. Check the ID and that Kindle Chart Sync is enabled.', 'error');
  }
}

document.getElementById('renderBtn').addEventListener('click', renderFromInput);
document.getElementById('saveHistoryBtn').addEventListener('click', saveCurrentToHistory);
document.getElementById('clearInputBtn').addEventListener('click', () => {
  jsonInput.value = '';
  destroyChart();
  statsEl.textContent = '';
  setStatus('');
});
document.getElementById('exportHistoryBtn').addEventListener('click', () => {
  const blob = new Blob([JSON.stringify(historyByBook, null, 2)], { type: 'application/json' });
  const a = document.createElement('a');
  a.href = URL.createObjectURL(blob);
  a.download = 'kindle-reading-history.json';
  a.click();
  URL.revokeObjectURL(a.href);
});
bookSelect.addEventListener('change', loadBookFromHistory);
xAxisMode.addEventListener('change', renderFromInput);
showTrend.addEventListener('change', renderFromInput);
document.getElementById('detectExtensionBtn').addEventListener('click', detectExtension);
document.getElementById('fetchCredentialsBtn').addEventListener('click', tryExtensionCredentials);
document.getElementById('syncExtensionBtn').addEventListener('click', syncFromExtension);

const meta = loadCredentialsMeta();
if (meta) {
  credentialsStatus.textContent = `Last credential check: ${meta.updatedAt || 'unknown'}`;
}

refreshBookSelect();

if (!jsonInput.value.trim()) {
  jsonInput.value = `[
  {"timestamp": "2026-05-01T08:00:00Z", "progress": 0},
  {"timestamp": "2026-05-05T20:30:00Z", "progress": 15},
  {"timestamp": "2026-05-12T22:15:00Z", "progress": 45},
  {"timestamp": "2026-05-20T07:45:00Z", "progress": 80},
  {"timestamp": "2026-05-25T23:00:00Z", "progress": 100}
]`;
}

const storedExtId = localStorage.getItem('kindle-extension-id');
if (storedExtId) extensionIdInput.value = storedExtId;
