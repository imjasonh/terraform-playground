// RLEW: word-oriented run-length encoding used by TED5/Wolf3D map planes.
// Semantics verified against CA_RLEWexpand in WOLFSRC/ID_CA.C.

/**
 * Expand RLEW-compressed data.
 * @param {Uint8Array} src compressed stream (sequence of little-endian u16)
 * @param {number} expandedByteLength expected output size in bytes
 * @param {number} tag RLEW tag word (from MAPHEAD, 0xABCD for Wolf3D)
 * @returns {Uint8Array} expanded bytes
 */
export function rlewExpand(src, expandedByteLength, tag) {
  const out = new Uint8Array(expandedByteLength);
  const dv = new DataView(src.buffer, src.byteOffset, src.byteLength);
  let si = 0;
  let oi = 0;
  while (oi < expandedByteLength) {
    if (si + 2 > src.byteLength) throw new Error('RLEW: truncated input');
    const w = dv.getUint16(si, true);
    si += 2;
    if (w === tag) {
      if (si + 4 > src.byteLength) throw new Error('RLEW: truncated run');
      const count = dv.getUint16(si, true);
      const value = dv.getUint16(si + 2, true);
      si += 4;
      for (let i = 0; i < count; i++) {
        if (oi + 2 > expandedByteLength) throw new Error('RLEW: overrun');
        out[oi] = value & 0xff;
        out[oi + 1] = value >> 8;
        oi += 2;
      }
    } else {
      out[oi] = w & 0xff;
      out[oi + 1] = w >> 8;
      oi += 2;
    }
  }
  return out;
}

/**
 * Compress data with RLEW. Runs of >= 4 identical words are encoded; the tag
 * word itself is always encoded as a run (even of length 1), matching TED5's
 * output rules.
 * @param {Uint8Array} src raw bytes (length must be even)
 * @param {number} tag RLEW tag word
 * @returns {Uint8Array} compressed bytes
 */
export function rlewCompress(src, tag) {
  if (src.byteLength % 2 !== 0) throw new Error('RLEW: input must be word-aligned');
  const n = src.byteLength / 2;
  const words = new Uint16Array(n);
  for (let i = 0; i < n; i++) words[i] = src[i * 2] | (src[i * 2 + 1] << 8);

  /** @type {number[]} */
  const out = [];
  const pushWord = (/** @type {number} */ w) => {
    out.push(w & 0xff, (w >> 8) & 0xff);
  };

  let i = 0;
  while (i < n) {
    const value = words[i];
    let run = 1;
    while (i + run < n && words[i + run] === value && run < 0xffff) run++;
    if (run >= 4 || value === tag) {
      pushWord(tag);
      pushWord(run);
      pushWord(value);
    } else {
      for (let k = 0; k < run; k++) pushWord(value);
    }
    i += run;
  }
  return Uint8Array.from(out);
}
