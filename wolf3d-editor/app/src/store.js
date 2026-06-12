// Application store: document state, undo/redo, and change notification.
// Deliberately framework-free; React components subscribe via
// useSyncExternalStore in useStore().

import {
  parseMapHead,
  parseGameMaps,
  parseVSwap,
  buildGameMaps,
  buildVSwap,
  encodeWall,
  encodeSprite,
  MAP_WIDTH,
} from '@wolf3d/codec';
import { AssetCache } from './game/assets.js';

/** @typedef {{plane: 0|1, code: number}} Brush */

const listeners = new Set();
let version = 0;

export const store = {
  screen: 'open',
  /** @type {string|null} */
  error: null,
  /** @type {null | {
   *   ext: string,
   *   rlewTag: number,
   *   levels: (import('@wolf3d/codec').LevelData|null)[],
   *   vswap: import('@wolf3d/codec').VSwap,
   *   vswapDirty: boolean,
   *   files: import('./io/files.js').GameFiles,
   * }} */
  game: null,
  /** @type {AssetCache|null} */
  assets: null,
  ui: {
    level: 0,
    zoom: 11,
    showObjects: true,
    showFloors: true,
    showGrid: true,
    /** @type {'draw'|'pick'} */
    tool: 'draw',
    /** @type {Brush} */
    lmb: { plane: 0, code: 1 },
    /** @type {Brush} */
    rmb: { plane: 0, code: 107 },
    /** @type {'all'|'easy'|'medium'|'hard'} */
    skillFilter: 'all',
    enemyOpts: { skill: /** @type {'easy'|'medium'|'hard'} */ ('medium'), mode: /** @type {'stand'|'patrol'} */ ('stand'), facing: 1 },
    /** @type {{x:number,y:number}|null} */
    hover: null,
    /** @type {'map'|'gfx'|'3d'|'playtest'} */
    panel: 'map',
    paletteTab: /** @type {'MAP'|'OBJ'} */ ('MAP'),
    dirty: false,
  },
  /** @type {{level:number, plane:number, before:Uint16Array, after:Uint16Array}[]} */
  undoStack: [],
  /** @type {{level:number, plane:number, before:Uint16Array, after:Uint16Array}[]} */
  redoStack: [],
};

export function notify() {
  version++;
  for (const l of listeners) l();
}

export function subscribe(fn) {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

export function getVersion() {
  return version;
}

/**
 * Open a set of game files in the editor.
 * @param {import('./io/files.js').GameFiles} files
 */
export function openGame(files) {
  try {
    const mapheadBytes = files.byName.get('maphead');
    const gamemapsBytes = files.byName.get('gamemaps') ?? files.byName.get('maptemp');
    const vswapBytes = files.byName.get('vswap');
    if (!mapheadBytes || !gamemapsBytes) {
      throw new Error('Need MAPHEAD and GAMEMAPS files (drop a game folder or mod ZIP).');
    }
    if (!vswapBytes) throw new Error('Need the VSWAP file for textures and sprites.');
    const head = parseMapHead(mapheadBytes);
    const levels = parseGameMaps(gamemapsBytes, head);
    const vswap = parseVSwap(vswapBytes);
    store.game = { ext: files.ext, rlewTag: head.rlewTag, levels, vswap, vswapDirty: false, files };
    store.assets = new AssetCache(vswap);
    store.ui.level = levels.findIndex(Boolean);
    if (store.ui.level < 0) store.ui.level = 0;
    store.ui.dirty = false;
    store.undoStack = [];
    store.redoStack = [];
    store.screen = 'editor';
    store.error = null;
  } catch (err) {
    store.error = err instanceof Error ? err.message : String(err);
  }
  notify();
}

/** Current level or null. */
export function currentLevel() {
  return store.game?.levels[store.ui.level] ?? null;
}

// ---- editing ----

/** @type {null | {level:number, plane:number, before:Uint16Array}} */
let gesture = null;

/**
 * Begin an edit gesture on a plane (captures undo snapshot).
 * @param {number} plane
 */
export function beginGesture(plane) {
  const lvl = currentLevel();
  if (!lvl) return;
  const arr = plane === 0 ? lvl.plane0 : lvl.plane1;
  gesture = { level: store.ui.level, plane, before: arr.slice() };
}

export function endGesture() {
  if (!gesture) return;
  const lvl = store.game?.levels[gesture.level];
  if (lvl) {
    const arr = gesture.plane === 0 ? lvl.plane0 : lvl.plane1;
    // Only record if something changed.
    let changed = false;
    for (let i = 0; i < arr.length; i++) {
      if (arr[i] !== gesture.before[i]) {
        changed = true;
        break;
      }
    }
    if (changed) {
      store.undoStack.push({ level: gesture.level, plane: gesture.plane, before: gesture.before, after: arr.slice() });
      if (store.undoStack.length > 256) store.undoStack.shift();
      store.redoStack = [];
      store.ui.dirty = true;
    }
  }
  gesture = null;
  notify();
}

/**
 * Set a tile during a gesture.
 * @param {number} plane @param {number} x @param {number} y @param {number} code
 */
export function setTile(plane, x, y, code) {
  const lvl = currentLevel();
  if (!lvl || x < 0 || y < 0 || x >= MAP_WIDTH || y >= MAP_WIDTH) return;
  const arr = plane === 0 ? lvl.plane0 : lvl.plane1;
  if (arr[y * MAP_WIDTH + x] === code) return;
  arr[y * MAP_WIDTH + x] = code;
  notify();
}

export function undo() {
  const rec = store.undoStack.pop();
  if (!rec) return;
  const lvl = store.game?.levels[rec.level];
  if (lvl) {
    const arr = rec.plane === 0 ? lvl.plane0 : lvl.plane1;
    arr.set(rec.before);
    store.redoStack.push(rec);
    store.ui.level = rec.level;
    store.ui.dirty = true;
  }
  notify();
}

export function redo() {
  const rec = store.redoStack.pop();
  if (!rec) return;
  const lvl = store.game?.levels[rec.level];
  if (lvl) {
    const arr = rec.plane === 0 ? lvl.plane0 : lvl.plane1;
    arr.set(rec.after);
    store.undoStack.push(rec);
    store.ui.level = rec.level;
    store.ui.dirty = true;
  }
  notify();
}

/**
 * Compile current document to game-file bytes (GAMEMAPS/MAPHEAD rebuilt,
 * everything else passed through from the loaded files).
 * @returns {{name: string, data: Uint8Array}[]}
 */
export function compileFiles() {
  const game = store.game;
  if (!game) return [];
  const { maphead, gamemaps } = buildGameMaps(game.levels, game.rlewTag);
  const ext = game.ext.toUpperCase();
  /** @type {{name: string, data: Uint8Array}[]} */
  const out = [
    { name: `MAPHEAD.${ext}`, data: maphead },
    { name: `GAMEMAPS.${ext}`, data: gamemaps },
  ];
  if (game.vswapDirty) out.push({ name: `VSWAP.${ext}`, data: buildVSwap(game.vswap) });
  for (const [name, data] of game.files.byName) {
    if (name === 'maphead' || name === 'gamemaps' || name === 'maptemp') continue;
    if (name === 'vswap' && game.vswapDirty) continue;
    out.push({ name: `${name.toUpperCase()}.${ext}`, data });
  }
  return out;
}

/**
 * Replace a wall texture's pixels (graphics studio).
 * @param {number} chunkIndex @param {Uint8Array} pixels 64*64 row-major
 */
export function setWallPixels(chunkIndex, pixels) {
  const game = store.game;
  if (!game || chunkIndex < 0 || chunkIndex >= game.vswap.spriteStart) return;
  game.vswap.chunks[chunkIndex] = encodeWall(pixels);
  game.vswapDirty = true;
  store.ui.dirty = true;
  store.assets?.invalidateWall(chunkIndex);
  notify();
}

/**
 * Replace a sprite's pixels (graphics studio).
 * @param {number} num sprite number @param {Uint8Array} pixels 64*64 row-major, 255 = transparent
 */
export function setSpritePixels(num, pixels) {
  const game = store.game;
  if (!game) return;
  const chunkIndex = game.vswap.spriteStart + num;
  if (chunkIndex < game.vswap.spriteStart || chunkIndex >= game.vswap.soundStart) return;
  game.vswap.chunks[chunkIndex] = encodeSprite(pixels);
  game.vswapDirty = true;
  store.ui.dirty = true;
  store.assets?.invalidateSprite(num);
  notify();
}

/**
 * Create a fresh level in an empty slot: solid grey-stone border, floor code
 * 6C inside, player start in the middle (the engine requires exactly one).
 * @param {number} slot
 */
export function createLevel(slot) {
  const game = store.game;
  if (!game || game.levels[slot]) return;
  const plane0 = new Uint16Array(MAP_WIDTH * MAP_WIDTH);
  const plane1 = new Uint16Array(MAP_WIDTH * MAP_WIDTH);
  for (let y = 0; y < MAP_WIDTH; y++) {
    for (let x = 0; x < MAP_WIDTH; x++) {
      const border = x === 0 || y === 0 || x === MAP_WIDTH - 1 || y === MAP_WIDTH - 1;
      plane0[y * MAP_WIDTH + x] = border ? 1 : 0x6c;
    }
  }
  plane1[32 * MAP_WIDTH + 32] = 19; // player start facing north
  game.levels[slot] = { name: `New Map ${slot + 1}`, plane0, plane1, plane2: null };
  store.ui.level = slot;
  store.ui.dirty = true;
  notify();
}

/**
 * Remove a level from its slot.
 * @param {number} slot
 */
export function deleteLevel(slot) {
  const game = store.game;
  if (!game || !game.levels[slot]) return;
  game.levels[slot] = null;
  store.undoStack = store.undoStack.filter((r) => r.level !== slot);
  store.redoStack = store.redoStack.filter((r) => r.level !== slot);
  if (store.ui.level === slot) {
    const next = game.levels.findIndex(Boolean);
    store.ui.level = next >= 0 ? next : 0;
  }
  store.ui.dirty = true;
  notify();
}

/** Mutate UI state and notify. @param {(ui: typeof store.ui) => void} fn */
export function updateUi(fn) {
  fn(store.ui);
  notify();
}
