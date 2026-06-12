// VSWAP codec: page file holding wall textures, compiled sprites, and
// digitized sounds. Layout verified against CAL_SetupGrFile-era PM code
// (WOLFSRC/ID_PM.C) and the community VSWAP documentation.

export const WALL_SIZE = 64; // walls and sprites are 64x64
export const WALL_BYTES = WALL_SIZE * WALL_SIZE; // 4096, column-major

/**
 * @typedef {Object} VSwap
 * @property {number} numChunks
 * @property {number} spriteStart  index of first sprite chunk
 * @property {number} soundStart   index of first sound chunk
 * @property {(Uint8Array|null)[]} chunks raw chunk bytes (null = sparse)
 */

/**
 * Parse a VSWAP file into raw chunks.
 * @param {Uint8Array} bytes
 * @returns {VSwap}
 */
export function parseVSwap(bytes) {
  const dv = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  const numChunks = dv.getUint16(0, true);
  const spriteStart = dv.getUint16(2, true);
  const soundStart = dv.getUint16(4, true);
  /** @type {(Uint8Array|null)[]} */
  const chunks = new Array(numChunks).fill(null);
  for (let i = 0; i < numChunks; i++) {
    const ofs = dv.getUint32(6 + i * 4, true);
    const len = dv.getUint16(6 + numChunks * 4 + i * 2, true);
    if (ofs === 0) continue;
    chunks[i] = bytes.slice(ofs, ofs + len);
  }
  return { numChunks, spriteStart, soundStart, chunks };
}

/**
 * Serialize a VSWAP structure back to bytes.
 * @param {VSwap} swap
 * @returns {Uint8Array}
 */
export function buildVSwap(swap) {
  const n = swap.chunks.length;
  const headerSize = 6 + n * 4 + n * 2;
  let total = headerSize;
  for (const c of swap.chunks) total += c ? c.byteLength : 0;
  const out = new Uint8Array(total);
  const dv = new DataView(out.buffer);
  dv.setUint16(0, n, true);
  dv.setUint16(2, swap.spriteStart, true);
  dv.setUint16(4, swap.soundStart, true);
  let pos = headerSize;
  for (let i = 0; i < n; i++) {
    const c = swap.chunks[i];
    if (!c) {
      dv.setUint32(6 + i * 4, 0, true);
      dv.setUint16(6 + n * 4 + i * 2, 0, true);
      continue;
    }
    dv.setUint32(6 + i * 4, pos, true);
    dv.setUint16(6 + n * 4 + i * 2, c.byteLength, true);
    out.set(c, pos);
    pos += c.byteLength;
  }
  return out;
}

/**
 * Decode a wall texture chunk (4096 bytes, column-major) to row-major pixels.
 * @param {Uint8Array} chunk
 * @returns {Uint8Array} 64*64 row-major palette indices
 */
export function decodeWall(chunk) {
  if (chunk.byteLength < WALL_BYTES) throw new Error('wall chunk too small');
  const out = new Uint8Array(WALL_BYTES);
  for (let x = 0; x < WALL_SIZE; x++) {
    for (let y = 0; y < WALL_SIZE; y++) {
      out[y * WALL_SIZE + x] = chunk[x * WALL_SIZE + y];
    }
  }
  return out;
}

/**
 * Encode row-major pixels to a wall chunk (column-major).
 * @param {Uint8Array} pixels 64*64 row-major palette indices
 * @returns {Uint8Array}
 */
export function encodeWall(pixels) {
  const out = new Uint8Array(WALL_BYTES);
  for (let x = 0; x < WALL_SIZE; x++) {
    for (let y = 0; y < WALL_SIZE; y++) {
      out[x * WALL_SIZE + y] = pixels[y * WALL_SIZE + x];
    }
  }
  return out;
}

export const TRANSPARENT = 255; // palette index 0xFF is sprite-transparent

/**
 * Decode a compiled sprite chunk into a 64x64 row-major bitmap where
 * transparent texels are 255.
 * Format: u16 leftpix, u16 rightpix, u16 colOfs[right-left+1] (chunk-relative)
 * then post lists: {u16 endY*2, u16 poolOfs, u16 startY*2}*, 0-terminated.
 * Pixel for row y of a post comes from chunk[(int16)poolOfs + y].
 * @param {Uint8Array} chunk
 * @returns {Uint8Array} 64*64 row-major pixels (255 = transparent)
 */
export function decodeSprite(chunk) {
  const dv = new DataView(chunk.buffer, chunk.byteOffset, chunk.byteLength);
  const left = dv.getUint16(0, true);
  const right = dv.getUint16(2, true);
  if (left > 63 || right > 63 || right < left) throw new Error('sprite: bad extents');
  const out = new Uint8Array(WALL_BYTES).fill(TRANSPARENT);
  for (let x = left; x <= right; x++) {
    let ofs = dv.getUint16(4 + (x - left) * 2, true);
    for (;;) {
      const endY2 = dv.getUint16(ofs, true);
      if (endY2 === 0) break;
      const poolOfs = dv.getInt16(ofs + 2, true);
      const startY2 = dv.getUint16(ofs + 4, true);
      ofs += 6;
      const endY = endY2 >> 1;
      const startY = startY2 >> 1;
      for (let y = startY; y < endY; y++) {
        out[y * WALL_SIZE + x] = chunk[(poolOfs + y) & 0xffff];
      }
    }
  }
  return out;
}

/**
 * Compile a 64x64 row-major bitmap (255 = transparent) into the engine's
 * sprite format. Layout: header, column offset table, pixel pool, then post
 * tables (everything addressed explicitly, so any conforming reader works).
 * @param {Uint8Array} pixels 64*64 row-major (255 = transparent)
 * @returns {Uint8Array}
 */
export function encodeSprite(pixels) {
  // Find horizontal extent.
  let left = -1;
  let right = -1;
  for (let x = 0; x < WALL_SIZE; x++) {
    let any = false;
    for (let y = 0; y < WALL_SIZE; y++) {
      if (pixels[y * WALL_SIZE + x] !== TRANSPARENT) {
        any = true;
        break;
      }
    }
    if (any) {
      if (left === -1) left = x;
      right = x;
    }
  }
  if (left === -1) {
    left = 32;
    right = 32; // fully transparent sprite: single empty column
  }

  /** @type {{x:number, posts:{start:number,end:number}[]}[]} */
  const columns = [];
  for (let x = left; x <= right; x++) {
    /** @type {{start:number,end:number}[]} */
    const posts = [];
    let y = 0;
    while (y < WALL_SIZE) {
      while (y < WALL_SIZE && pixels[y * WALL_SIZE + x] === TRANSPARENT) y++;
      if (y >= WALL_SIZE) break;
      const start = y;
      while (y < WALL_SIZE && pixels[y * WALL_SIZE + x] !== TRANSPARENT) y++;
      posts.push({ start, end: y });
    }
    columns.push({ x, posts });
  }

  const numCols = right - left + 1;
  const headerSize = 4 + numCols * 2;
  let poolSize = 0;
  let postTableSize = 0;
  for (const col of columns) {
    for (const p of col.posts) poolSize += p.end - p.start;
    postTableSize += col.posts.length * 6 + 2;
  }
  const total = headerSize + poolSize + postTableSize;
  if (total > 0xffff) throw new Error('sprite: compiled size exceeds 64KB addressing');

  const out = new Uint8Array(total);
  const dv = new DataView(out.buffer);
  dv.setUint16(0, left, true);
  dv.setUint16(2, right, true);

  let poolPos = headerSize;
  let postPos = headerSize + poolSize;
  for (let i = 0; i < columns.length; i++) {
    const col = columns[i];
    dv.setUint16(4 + i * 2, postPos, true);
    for (const p of col.posts) {
      // chunk[poolOfs + y] must equal pixel(y); pixels for this post start at
      // poolPos and correspond to y = p.start.
      const poolOfs = (poolPos - p.start) & 0xffff;
      dv.setUint16(postPos, p.end * 2, true);
      dv.setUint16(postPos + 2, poolOfs, true);
      dv.setUint16(postPos + 4, p.start * 2, true);
      postPos += 6;
      for (let y = p.start; y < p.end; y++) out[poolPos++] = pixels[y * WALL_SIZE + col.x];
    }
    dv.setUint16(postPos, 0, true);
    postPos += 2;
  }
  return out;
}
