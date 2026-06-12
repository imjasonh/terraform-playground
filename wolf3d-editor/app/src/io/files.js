// Game-file loading: accepts dropped files, directory picks, or a mod ZIP,
// and assembles a GameFiles bag keyed by canonical name.

import { readZip } from './zip.js';

export const KNOWN_BASENAMES = [
  'maphead', 'gamemaps', 'maptemp', 'vswap', 'vgadict', 'vgahead', 'vgagraph',
  'audiohed', 'audiot',
];

export const GAME_EXTENSIONS = ['wl1', 'wl3', 'wl6', 'sod', 'sdm'];

/**
 * @typedef {Object} GameFiles
 * @property {string} ext detected extension (lowercase, e.g. 'wl1')
 * @property {Map<string, Uint8Array>} byName canonical lowercase name -> bytes (e.g. 'vswap')
 * @property {Map<string, Uint8Array>} extras everything else found alongside (incl. EXE), original lowercase names
 */

/**
 * Classify and collect a set of named buffers into GameFiles.
 * @param {{name: string, data: Uint8Array}[]} files
 * @returns {GameFiles}
 */
export function collectGameFiles(files) {
  /** @type {Map<string, Uint8Array>} */
  const byName = new Map();
  /** @type {Map<string, Uint8Array>} */
  const extras = new Map();
  /** @type {Map<string, number>} */
  const extVotes = new Map();

  for (const f of files) {
    const base = f.name.split('/').pop() ?? f.name;
    const lower = base.toLowerCase();
    const dot = lower.lastIndexOf('.');
    const stem = dot >= 0 ? lower.slice(0, dot) : lower;
    const ext = dot >= 0 ? lower.slice(dot + 1) : '';
    if (KNOWN_BASENAMES.includes(stem) && GAME_EXTENSIONS.includes(ext)) {
      byName.set(stem, f.data);
      extVotes.set(ext, (extVotes.get(ext) ?? 0) + 1);
    } else {
      extras.set(lower, f.data);
    }
  }
  let ext = 'wl6';
  let best = 0;
  for (const [e, v] of extVotes) {
    if (v > best) {
      best = v;
      ext = e;
    }
  }
  return { ext, byName, extras };
}

/**
 * Expand any ZIPs in a dropped file list, then collect.
 * @param {File[]} fileList
 * @returns {Promise<GameFiles>}
 */
export async function loadFromFiles(fileList) {
  /** @type {{name: string, data: Uint8Array}[]} */
  const flat = [];
  for (const file of fileList) {
    const data = new Uint8Array(await file.arrayBuffer());
    if (file.name.toLowerCase().endsWith('.zip')) {
      try {
        flat.push(...(await readZip(data)));
        continue;
      } catch {
        // fall through: treat as a plain file
      }
    }
    flat.push({ name: file.name, data });
  }
  return collectGameFiles(flat);
}

/**
 * Load via the File System Access directory picker.
 * @returns {Promise<GameFiles|null>} null if unsupported/cancelled
 */
export async function loadFromDirectory() {
  if (!('showDirectoryPicker' in window)) return null;
  /** @type {FileSystemDirectoryHandle} */
  let dir;
  try {
    dir = await window.showDirectoryPicker({ id: 'wolf3d-game-dir' });
  } catch {
    return null;
  }
  /** @type {{name: string, data: Uint8Array}[]} */
  const flat = [];
  for await (const entry of dir.values()) {
    if (entry.kind !== 'file') continue;
    const file = await entry.getFile();
    if (file.size > 32 * 1024 * 1024) continue;
    flat.push({ name: file.name, data: new Uint8Array(await file.arrayBuffer()) });
  }
  return collectGameFiles(flat);
}

/**
 * Load the bundled shareware demo data (served from /demo).
 * @returns {Promise<GameFiles>}
 */
export async function loadDemo() {
  const names = ['maphead', 'gamemaps', 'vswap', 'vgadict', 'vgahead', 'vgagraph', 'audiohed', 'audiot'];
  /** @type {{name: string, data: Uint8Array}[]} */
  const flat = [];
  for (const n of names) {
    const res = await fetch(`${import.meta.env.BASE_URL}demo/${n}.wl1`);
    if (!res.ok) throw new Error(`demo file missing: ${n}.wl1`);
    flat.push({ name: `${n}.wl1`, data: new Uint8Array(await res.arrayBuffer()) });
  }
  return collectGameFiles(flat);
}
