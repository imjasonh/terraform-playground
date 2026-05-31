/**
 * Normalize Kindle / reading-progress JSON into { timestamp, progress } points.
 * Supports multiple payload shapes from pasted JSON, extension sync, or API snippets.
 */

const PROGRESS_KEYS = [
  'progress',
  'percentageRead',
  'percentage',
  'percent',
  'P',
  'p',
  'completion',
  'progressPercent',
];

const TIMESTAMP_KEYS = [
  'timestamp',
  'syncDate',
  'syncTime',
  'time',
  't',
  'date',
  'reportedAt',
  'epoch',
];

function pick(obj, keys) {
  if (!obj || typeof obj !== 'object') return undefined;
  for (const key of keys) {
    if (obj[key] !== undefined && obj[key] !== null) return obj[key];
  }
  return undefined;
}

function toProgress(value) {
  if (value === undefined || value === null || value === '') return null;
  const n = Number(value);
  if (Number.isNaN(n)) return null;
  if (n <= 1 && n >= 0) return n * 100;
  return Math.min(100, Math.max(0, n));
}

function toTimestamp(value) {
  if (value === undefined || value === null) return null;
  if (typeof value === 'number') {
    const ms = value < 1e12 ? value * 1000 : value;
    const d = new Date(ms);
    return Number.isNaN(d.getTime()) ? null : d;
  }
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? null : d;
}

function pointFromRecord(record) {
  const rawProgress = pick(record, PROGRESS_KEYS);
  const rawTime = pick(record, TIMESTAMP_KEYS);

  if (rawProgress === undefined && record.lastPageReadData) {
    const lpr = record.lastPageReadData;
    const pos = lpr.position;
    const end = record.endPosition;
    if (typeof pos === 'number' && typeof end === 'number' && end > 0) {
      const pct = (pos / end) * 100;
      const ts = toTimestamp(lpr.syncTime ?? lpr.syncDate);
      if (ts) return { timestamp: ts, progress: pct };
    }
  }

  const progress = toProgress(rawProgress);
  const timestamp = toTimestamp(rawTime);
  if (progress === null || !timestamp) return null;
  return { timestamp, progress };
}

function extractArray(payload) {
  if (Array.isArray(payload)) return payload;
  if (!payload || typeof payload !== 'object') return [];

  const nestedKeys = [
    'progress_to_completion',
    'progressToCompletion',
    'history',
    'snapshots',
    'points',
    'data',
    'readings',
    'entries',
  ];

  for (const key of nestedKeys) {
    if (Array.isArray(payload[key])) return payload[key];
  }

  if (payload.progress && typeof payload.progress === 'object') {
    const p = pointFromRecord({
      ...payload,
      syncDate: payload.progress.syncDate ?? payload.progress.syncTime,
      progress: payload.percentageRead ?? payload.progress.position,
    });
    return p ? [p] : [];
  }

  const values = Object.values(payload);
  if (values.length && values.every((v) => v && typeof v === 'object' && ('p' in v || 'progress' in v || 't' in v))) {
    return values.map((v) => ({
      timestamp: v.t ?? v.timestamp,
      progress: v.p ?? v.progress,
    }));
  }

  return [];
}

/**
 * @param {unknown} raw - parsed JSON
 * @returns {{ points: { timestamp: Date, progress: number }[], errors: string[] }}
 */
export function normalizeReadingData(raw) {
  const errors = [];
  const records = extractArray(raw);

  if (!records.length) {
    errors.push('No reading progress points found. Expected an array or progress_to_completion field.');
    return { points: [], errors };
  }

  const points = [];
  for (const record of records) {
    const point = pointFromRecord(record);
    if (point) points.push(point);
  }

  if (!points.length) {
    errors.push('Could not map any records to timestamp + progress fields.');
  }

  points.sort((a, b) => a.timestamp - b.timestamp);

  const deduped = [];
  for (const p of points) {
    const last = deduped[deduped.length - 1];
    if (
      last &&
      last.timestamp.getTime() === p.timestamp.getTime() &&
      Math.abs(last.progress - p.progress) < 0.01
    ) {
      continue;
    }
    deduped.push(p);
  }

  return { points: deduped, errors };
}

/**
 * @param {{ timestamp: Date, progress: number }[]} points
 * @param {'calendar' | 'daysFromStart'} mode
 */
export function toChartSeries(points, mode = 'calendar') {
  if (!points.length) {
    return { labels: [], values: [], daysFromStart: [] };
  }

  const start = points[0].timestamp.getTime();
  const labels = [];
  const values = [];
  const daysFromStart = [];

  for (const p of points) {
    values.push(Number(p.progress.toFixed(2)));
    if (mode === 'daysFromStart') {
      const days = (p.timestamp.getTime() - start) / (1000 * 60 * 60 * 24);
      daysFromStart.push(Number(days.toFixed(2)));
      labels.push(days.toFixed(1));
    } else {
      labels.push(p.timestamp.toLocaleString(undefined, {
        month: 'short',
        day: 'numeric',
        year: 'numeric',
        hour: '2-digit',
        minute: '2-digit',
      }));
    }
  }

  return { labels, values, daysFromStart };
}

export function mergeHistory(existing, incoming) {
  const combined = [...(existing || []), ...(incoming || [])];
  const { points } = normalizeReadingData(
    combined.map((p) => ({
      timestamp: p.timestamp instanceof Date ? p.timestamp.toISOString() : p.timestamp,
      progress: p.progress,
    }))
  );
  return points;
}
