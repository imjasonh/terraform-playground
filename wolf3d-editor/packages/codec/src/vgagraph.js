// VGAHEAD/VGAGRAPH codec: Huffman-compressed UI graphics (pics, fonts,
// tile8, endscreens, demos). Chunk addressing verified against
// CAL_SetupGrFile / CA_CacheGrChunk / CAL_ExpandGrChunk in WOLFSRC/ID_CA.C.

import { huffExpand, huffCompress } from './huffman.js';

/**
 * Parse VGAHEAD: 3-byte little-endian offsets, one per chunk (+ end sentinel
 * in most files). 0xFFFFFF = sparse chunk.
 * @param {Uint8Array} bytes
 * @returns {number[]} offsets (-1 for sparse)
 */
export function parseVgaHead(bytes) {
  const count = Math.floor(bytes.byteLength / 3);
  /** @type {number[]} */
  const offsets = [];
  for (let i = 0; i < count; i++) {
    const v = bytes[i * 3] | (bytes[i * 3 + 1] << 8) | (bytes[i * 3 + 2] << 16);
    offsets.push(v === 0xffffff ? -1 : v);
  }
  return offsets;
}

/**
 * Slice VGAGRAPH into raw (still-compressed) chunks using VGAHEAD offsets.
 * @param {Uint8Array} graph
 * @param {number[]} offsets
 * @returns {(Uint8Array|null)[]}
 */
export function splitVgaChunks(graph, offsets) {
  /** @type {(Uint8Array|null)[]} */
  const chunks = [];
  const n = offsets[offsets.length - 1] === graph.byteLength ? offsets.length - 1 : offsets.length;
  for (let i = 0; i < n; i++) {
    const start = offsets[i];
    if (start < 0) {
      chunks.push(null);
      continue;
    }
    let end = graph.byteLength;
    for (let j = i + 1; j <= n; j++) {
      const o = j < offsets.length ? offsets[j] : graph.byteLength;
      if (o >= 0) {
        end = o;
        break;
      }
    }
    chunks.push(graph.slice(start, end));
  }
  return chunks;
}

/**
 * Expand a normal VGAGRAPH chunk (u32 expanded length prefix + huffman data).
 * @param {Uint8Array} chunk
 * @param {import('./huffman.js').HuffNode[]} nodes
 * @returns {Uint8Array}
 */
export function expandVgaChunk(chunk, nodes) {
  const dv = new DataView(chunk.buffer, chunk.byteOffset, chunk.byteLength);
  const expanded = dv.getUint32(0, true);
  return huffExpand(chunk.subarray(4), expanded, nodes);
}

/**
 * Expand a chunk with an implicit expanded length (TILE8 block).
 * @param {Uint8Array} chunk
 * @param {number} expandedLength
 * @param {import('./huffman.js').HuffNode[]} nodes
 * @returns {Uint8Array}
 */
export function expandVgaChunkImplicit(chunk, expandedLength, nodes) {
  return huffExpand(chunk, expandedLength, nodes);
}

/**
 * Compress a chunk back (u32 expanded length prefix + huffman data).
 * @param {Uint8Array} data
 * @param {import('./huffman.js').HuffNode[]} nodes
 * @returns {Uint8Array}
 */
export function compressVgaChunk(data, nodes) {
  const body = huffCompress(data, nodes);
  const out = new Uint8Array(4 + body.byteLength);
  new DataView(out.buffer).setUint32(0, data.byteLength, true);
  out.set(body, 4);
  return out;
}

/**
 * Build VGAHEAD + VGAGRAPH from raw (compressed) chunks.
 * @param {(Uint8Array|null)[]} chunks
 * @returns {{vgahead: Uint8Array, vgagraph: Uint8Array}}
 */
export function buildVgaFiles(chunks) {
  let total = 0;
  for (const c of chunks) total += c ? c.byteLength : 0;
  const graph = new Uint8Array(total);
  const head = new Uint8Array((chunks.length + 1) * 3);
  let pos = 0;
  const put24 = (/** @type {number} */ i, /** @type {number} */ v) => {
    head[i * 3] = v & 0xff;
    head[i * 3 + 1] = (v >> 8) & 0xff;
    head[i * 3 + 2] = (v >> 16) & 0xff;
  };
  for (let i = 0; i < chunks.length; i++) {
    const c = chunks[i];
    if (!c) {
      put24(i, 0xffffff);
      continue;
    }
    put24(i, pos);
    graph.set(c, pos);
    pos += c.byteLength;
  }
  put24(chunks.length, pos);
  return { vgahead: head, vgagraph: graph };
}

/**
 * Parse the pictable (STRUCTPIC, chunk 0 expanded): width/height pairs.
 * @param {Uint8Array} data expanded chunk 0
 * @returns {{width:number, height:number}[]}
 */
export function parsePicTable(data) {
  const dv = new DataView(data.buffer, data.byteOffset, data.byteLength);
  /** @type {{width:number, height:number}[]} */
  const pics = [];
  for (let i = 0; i + 4 <= data.byteLength; i += 4) {
    pics.push({ width: dv.getUint16(i, true), height: dv.getUint16(i + 2, true) });
  }
  return pics;
}

/**
 * De-munge a VGA-planar pic into linear row-major pixels.
 * Plane p (of 4) holds pixels whose x % 4 == p.
 * @param {Uint8Array} data expanded pic data (width*height bytes)
 * @param {number} width
 * @param {number} height
 * @returns {Uint8Array} row-major palette indices
 */
export function demungePic(data, width, height) {
  const out = new Uint8Array(width * height);
  const planeSize = (width * height) >> 2;
  const quarterWidth = width >> 2;
  for (let p = 0; p < 4; p++) {
    for (let y = 0; y < height; y++) {
      for (let xq = 0; xq < quarterWidth; xq++) {
        out[y * width + (xq * 4 + p)] = data[p * planeSize + y * quarterWidth + xq];
      }
    }
  }
  return out;
}

/**
 * Re-munge linear row-major pixels into VGA-planar layout.
 * @param {Uint8Array} pixels row-major (width*height)
 * @param {number} width
 * @param {number} height
 * @returns {Uint8Array}
 */
export function mungePic(pixels, width, height) {
  const out = new Uint8Array(width * height);
  const planeSize = (width * height) >> 2;
  const quarterWidth = width >> 2;
  for (let p = 0; p < 4; p++) {
    for (let y = 0; y < height; y++) {
      for (let xq = 0; xq < quarterWidth; xq++) {
        out[p * planeSize + y * quarterWidth + xq] = pixels[y * width + (xq * 4 + p)];
      }
    }
  }
  return out;
}

/**
 * Parse a proportional font chunk: u16 height, i16 location[256], u8 width[256],
 * then byte-per-pixel glyph data at each location.
 * @param {Uint8Array} data expanded font chunk
 * @returns {{height:number, glyphs:({width:number, pixels:Uint8Array}|null)[]}}
 */
export function parseFont(data) {
  const dv = new DataView(data.buffer, data.byteOffset, data.byteLength);
  const height = dv.getUint16(0, true);
  /** @type {({width:number, pixels:Uint8Array}|null)[]} */
  const glyphs = new Array(256).fill(null);
  for (let c = 0; c < 256; c++) {
    const loc = dv.getInt16(2 + c * 2, true);
    const width = data[2 + 512 + c];
    if (width === 0 || loc <= 0) continue;
    const pixels = data.slice(loc, loc + width * height);
    glyphs[c] = { width, pixels };
  }
  return { height, glyphs };
}
