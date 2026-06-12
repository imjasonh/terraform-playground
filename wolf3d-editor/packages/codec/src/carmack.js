// Carmack compression: word-oriented LZ with near (0xA7) and far (0xA8)
// pointers. Semantics verified against CAL_CarmackExpand in WOLFSRC/ID_CA.C.
//
// Stream is a sequence of little-endian u16. For each word:
//   high byte 0xA7 -> near pointer: low byte = word count; next BYTE is the
//     relative offset (in words) back from the current output position.
//   high byte 0xA8 -> far pointer: low byte = word count; next WORD is the
//     absolute offset (in words) from the start of the output.
//   count of 0 escapes a literal word whose high byte is A7/A8: the next
//     byte is the literal's low byte.
//   anything else -> literal word.

const NEARTAG = 0xa7;
const FARTAG = 0xa8;

/**
 * Expand Carmack-compressed data.
 * @param {Uint8Array} src compressed stream
 * @param {number} expandedByteLength expected output size in bytes
 * @returns {Uint8Array} expanded bytes
 */
export function carmackExpand(src, expandedByteLength) {
  const outWords = new Uint16Array(expandedByteLength >> 1);
  let si = 0;
  let oi = 0; // word index
  const read8 = () => {
    if (si >= src.byteLength) throw new Error('Carmack: truncated input');
    return src[si++];
  };
  const read16 = () => {
    const lo = read8();
    const hi = read8();
    return lo | (hi << 8);
  };
  while (oi < outWords.length) {
    const lo = read8();
    const hi = read8();
    if (hi === NEARTAG) {
      const count = lo;
      if (count === 0) {
        outWords[oi++] = read8() | (NEARTAG << 8);
      } else {
        const dist = read8();
        let from = oi - dist;
        if (from < 0) throw new Error('Carmack: bad near pointer');
        for (let i = 0; i < count; i++) outWords[oi++] = outWords[from++];
      }
    } else if (hi === FARTAG) {
      const count = lo;
      if (count === 0) {
        outWords[oi++] = read8() | (FARTAG << 8);
      } else {
        let from = read16();
        if (from > oi) throw new Error('Carmack: bad far pointer');
        for (let i = 0; i < count; i++) outWords[oi++] = outWords[from++];
      }
    } else {
      outWords[oi++] = lo | (hi << 8);
    }
  }
  return new Uint8Array(outWords.buffer, 0, expandedByteLength);
}

/**
 * Compress data with Carmack encoding (greedy longest-match).
 * @param {Uint8Array} src raw bytes (length must be even)
 * @returns {Uint8Array} compressed bytes
 */
export function carmackCompress(src) {
  if (src.byteLength % 2 !== 0) throw new Error('Carmack: input must be word-aligned');
  const n = src.byteLength >> 1;
  const words = new Uint16Array(n);
  for (let i = 0; i < n; i++) words[i] = src[i * 2] | (src[i * 2 + 1] << 8);

  /** @type {number[]} */
  const out = [];
  const push8 = (/** @type {number} */ b) => out.push(b & 0xff);
  const push16 = (/** @type {number} */ w) => {
    out.push(w & 0xff, (w >> 8) & 0xff);
  };

  // Index of positions by word value for match search.
  /** @type {Map<number, number[]>} */
  const index = new Map();

  let i = 0;
  while (i < n) {
    // Find longest previous match starting at i.
    let bestLen = 0;
    let bestPos = -1;
    const cands = index.get(words[i]);
    if (cands) {
      // Scan most recent candidates first; cap work per position.
      for (let c = cands.length - 1, tried = 0; c >= 0 && tried < 64; c--, tried++) {
        const p = cands[c];
        let len = 0;
        const maxLen = Math.min(255, n - i);
        while (len < maxLen && words[p + len] === words[i + len]) len++;
        if (len > bestLen) {
          bestLen = len;
          bestPos = p;
          if (len === 255) break;
        }
      }
    }

    const dist = i - bestPos;
    const nearOk = bestLen >= 2 && dist <= 255;
    const farOk = bestLen >= 3 && bestPos <= 0xffff;

    if (nearOk || farOk) {
      if (nearOk) {
        push8(bestLen);
        push8(NEARTAG);
        push8(dist);
      } else {
        push8(bestLen);
        push8(FARTAG);
        push16(bestPos);
      }
      for (let k = 0; k < bestLen; k++) {
        let arr = index.get(words[i + k]);
        if (!arr) index.set(words[i + k], (arr = []));
        arr.push(i + k);
      }
      i += bestLen;
    } else {
      const w = words[i];
      const hi = w >> 8;
      if (hi === NEARTAG || hi === FARTAG) {
        // Escape: count 0 + tag + low byte.
        push8(0);
        push8(hi);
        push8(w & 0xff);
      } else {
        push16(w);
      }
      let arr = index.get(w);
      if (!arr) index.set(w, (arr = []));
      arr.push(i);
      i++;
    }
  }
  return Uint8Array.from(out);
}
