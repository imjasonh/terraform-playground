// MAPHEAD + GAMEMAPS codec.
// Structures verified against mapfiletype/maptype in WOLFSRC/ID_CA.H and the
// load path in CAL_SetupMapFile / CA_CacheMap (ID_CA.C).

import { rlewExpand, rlewCompress } from './rlew.js';
import { carmackExpand, carmackCompress } from './carmack.js';

export const MAP_WIDTH = 64;
export const MAP_HEIGHT = 64;
export const MAP_PLANE_BYTES = MAP_WIDTH * MAP_HEIGHT * 2; // 8192
export const NUM_LEVEL_SLOTS = 100;
const LEVEL_HEADER_BYTES = 38; // 3*i32 + 3*u16 + u16 + u16 + char[16]

/**
 * @typedef {Object} MapHead
 * @property {number} rlewTag
 * @property {Int32Array} offsets   100 entries; 0 or <=0 means unused slot
 * @property {Uint8Array} tail      unused trailing bytes (tileinfo), preserved
 */

/**
 * Parse MAPHEAD.
 * @param {Uint8Array} bytes
 * @returns {MapHead}
 */
export function parseMapHead(bytes) {
  if (bytes.byteLength < 2 + NUM_LEVEL_SLOTS * 4) {
    throw new Error(`MAPHEAD too small (${bytes.byteLength} bytes)`);
  }
  const dv = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  const rlewTag = dv.getUint16(0, true);
  const offsets = new Int32Array(NUM_LEVEL_SLOTS);
  for (let i = 0; i < NUM_LEVEL_SLOTS; i++) offsets[i] = dv.getInt32(2 + i * 4, true);
  const tail = bytes.slice(2 + NUM_LEVEL_SLOTS * 4);
  return { rlewTag, offsets, tail };
}

/**
 * @typedef {Object} LevelData
 * @property {string} name
 * @property {Uint16Array} plane0
 * @property {Uint16Array} plane1
 * @property {Uint16Array|null} plane2
 */

/**
 * Decode one plane chunk. Auto-detects RLEW-only (v1.0/MAPTEMP) vs
 * Carmack+RLEW: if the first u16 equals the expanded plane size, the chunk is
 * RLEW-only (ModdingWiki GameMaps detection rule).
 * @param {Uint8Array} chunk
 * @param {number} rlewTag
 * @returns {Uint16Array}
 */
export function decodePlane(chunk, rlewTag) {
  const dv = new DataView(chunk.buffer, chunk.byteOffset, chunk.byteLength);
  const first = dv.getUint16(0, true);
  /** @type {Uint8Array} */
  let rlewBlock;
  if (first === MAP_PLANE_BYTES) {
    rlewBlock = chunk; // [u16 expandedLen][rlew data]
  } else {
    const carmackLen = first;
    rlewBlock = carmackExpand(chunk.subarray(2), carmackLen);
  }
  const rdv = new DataView(rlewBlock.buffer, rlewBlock.byteOffset, rlewBlock.byteLength);
  const expanded = rdv.getUint16(0, true);
  const raw = rlewExpand(rlewBlock.subarray(2), expanded, rlewTag);
  const words = new Uint16Array(expanded >> 1);
  for (let i = 0; i < words.length; i++) words[i] = raw[i * 2] | (raw[i * 2 + 1] << 8);
  return words;
}

/**
 * Encode a plane: RLEW then Carmack, with both length prefixes.
 * @param {Uint16Array} plane
 * @param {number} rlewTag
 * @param {boolean} [carmackize=true]
 * @returns {Uint8Array}
 */
export function encodePlane(plane, rlewTag, carmackize = true) {
  const raw = new Uint8Array(plane.length * 2);
  for (let i = 0; i < plane.length; i++) {
    raw[i * 2] = plane[i] & 0xff;
    raw[i * 2 + 1] = plane[i] >> 8;
  }
  const rlew = rlewCompress(raw, rlewTag);
  const rlewBlock = new Uint8Array(2 + rlew.byteLength);
  rlewBlock[0] = raw.byteLength & 0xff;
  rlewBlock[1] = raw.byteLength >> 8;
  rlewBlock.set(rlew, 2);
  if (!carmackize) return rlewBlock;
  const car = carmackCompress(rlewBlock);
  const out = new Uint8Array(2 + car.byteLength);
  out[0] = rlewBlock.byteLength & 0xff;
  out[1] = rlewBlock.byteLength >> 8;
  out.set(car, 2);
  return out;
}

/**
 * Parse GAMEMAPS using a parsed MAPHEAD.
 * @param {Uint8Array} bytes GAMEMAPS contents
 * @param {MapHead} head
 * @returns {(LevelData|null)[]} 100 slots
 */
export function parseGameMaps(bytes, head) {
  const dv = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  /** @type {(LevelData|null)[]} */
  const levels = new Array(NUM_LEVEL_SLOTS).fill(null);
  for (let i = 0; i < NUM_LEVEL_SLOTS; i++) {
    const ofs = head.offsets[i];
    if (ofs <= 0 || ofs >= bytes.byteLength) continue;
    const planeStart = [dv.getInt32(ofs, true), dv.getInt32(ofs + 4, true), dv.getInt32(ofs + 8, true)];
    const planeLen = [dv.getUint16(ofs + 12, true), dv.getUint16(ofs + 14, true), dv.getUint16(ofs + 16, true)];
    let name = '';
    for (let c = 0; c < 16; c++) {
      const b = bytes[ofs + 22 + c];
      if (b === 0) break;
      name += String.fromCharCode(b);
    }
    const readPlane = (/** @type {number} */ p) => {
      if (planeLen[p] === 0 || planeStart[p] <= 0) return null;
      return decodePlane(bytes.subarray(planeStart[p], planeStart[p] + planeLen[p]), head.rlewTag);
    };
    const plane0 = readPlane(0);
    const plane1 = readPlane(1);
    if (!plane0 || !plane1) continue;
    levels[i] = { name, plane0, plane1, plane2: readPlane(2) };
  }
  return levels;
}

/**
 * Serialize levels back into a {maphead, gamemaps} byte pair.
 * @param {(LevelData|null)[]} levels 100 slots
 * @param {number} rlewTag
 * @param {{carmackize?: boolean, tail?: Uint8Array}} [opts]
 * @returns {{maphead: Uint8Array, gamemaps: Uint8Array}}
 */
export function buildGameMaps(levels, rlewTag, opts = {}) {
  const carmackize = opts.carmackize !== false;
  /** @type {number[]} */
  const out = [];
  const pushBytes = (/** @type {Uint8Array|number[]} */ b) => {
    for (let i = 0; i < b.length; i++) out.push(b[i]);
  };
  pushBytes(Array.from('TED5v1.0').map((c) => c.charCodeAt(0)));

  const offsets = new Int32Array(NUM_LEVEL_SLOTS);
  for (let i = 0; i < NUM_LEVEL_SLOTS; i++) {
    const lvl = levels[i];
    if (!lvl) continue;
    const planes = [lvl.plane0, lvl.plane1, lvl.plane2 ?? new Uint16Array(MAP_WIDTH * MAP_HEIGHT)];
    const encoded = planes.map((p) => encodePlane(p, rlewTag, carmackize));
    const starts = encoded.map((e) => {
      const s = out.length;
      pushBytes(e);
      return s;
    });
    const hdr = new Uint8Array(LEVEL_HEADER_BYTES);
    const hdv = new DataView(hdr.buffer);
    for (let p = 0; p < 3; p++) {
      hdv.setInt32(p * 4, starts[p], true);
      hdv.setUint16(12 + p * 2, encoded[p].byteLength, true);
    }
    hdv.setUint16(18, MAP_WIDTH, true);
    hdv.setUint16(20, MAP_HEIGHT, true);
    for (let c = 0; c < 16; c++) hdr[22 + c] = c < lvl.name.length ? lvl.name.charCodeAt(c) & 0x7f : 0;
    offsets[i] = out.length;
    pushBytes(hdr);
  }

  const tail = opts.tail ?? new Uint8Array(0);
  const maphead = new Uint8Array(2 + NUM_LEVEL_SLOTS * 4 + tail.byteLength);
  const mdv = new DataView(maphead.buffer);
  mdv.setUint16(0, rlewTag, true);
  for (let i = 0; i < NUM_LEVEL_SLOTS; i++) mdv.setInt32(2 + i * 4, offsets[i], true);
  maphead.set(tail, 2 + NUM_LEVEL_SLOTS * 4);

  return { maphead, gamemaps: Uint8Array.from(out) };
}
