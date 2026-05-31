const HISTORY_KEY = 'kindle-reading-history';
const CREDENTIALS_KEY = 'kindle-credentials-meta';
const SELECTED_BOOK_KEY = 'kindle-selected-book';

export function loadHistory() {
  try {
    const raw = localStorage.getItem(HISTORY_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw);
    return typeof parsed === 'object' && parsed !== null ? parsed : {};
  } catch {
    return {};
  }
}

export function saveHistory(historyByBook) {
  localStorage.setItem(HISTORY_KEY, JSON.stringify(historyByBook));
}

export function loadSelectedBook() {
  return localStorage.getItem(SELECTED_BOOK_KEY) || '';
}

export function saveSelectedBook(asin) {
  if (asin) localStorage.setItem(SELECTED_BOOK_KEY, asin);
  else localStorage.removeItem(SELECTED_BOOK_KEY);
}

export function loadCredentialsMeta() {
  try {
    const raw = localStorage.getItem(CREDENTIALS_KEY);
    return raw ? JSON.parse(raw) : null;
  } catch {
    return null;
  }
}

export function saveCredentialsMeta(meta) {
  if (!meta) {
    localStorage.removeItem(CREDENTIALS_KEY);
    return;
  }
  localStorage.setItem(
    CREDENTIALS_KEY,
    JSON.stringify({
      ...meta,
      updatedAt: new Date().toISOString(),
    })
  );
}

export function historyToJson(historyByBook, asin) {
  if (asin && historyByBook[asin]) {
    return JSON.stringify(
      historyByBook[asin].map((p) => ({
        timestamp: p.timestamp,
        progress: p.progress,
      })),
      null,
      2
    );
  }
  return JSON.stringify(historyByBook, null, 2);
}

export function appendSnapshot(historyByBook, asin, title, point) {
  const key = asin || '_default';
  const list = historyByBook[key] || [];
  const entry = {
    timestamp: point.timestamp instanceof Date ? point.timestamp.toISOString() : point.timestamp,
    progress: point.progress,
    title: title || undefined,
  };
  const merged = [...list, entry];
  const seen = new Set();
  const deduped = merged.filter((item) => {
    const id = `${item.timestamp}|${item.progress}`;
    if (seen.has(id)) return false;
    seen.add(id);
    return true;
  });
  deduped.sort((a, b) => new Date(a.timestamp) - new Date(b.timestamp));
  return { ...historyByBook, [key]: deduped };
}
