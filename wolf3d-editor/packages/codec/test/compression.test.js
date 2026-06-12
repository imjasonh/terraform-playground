import { describe, it, expect } from 'vitest';
import { rlewExpand, rlewCompress } from '../src/rlew.js';
import { carmackExpand, carmackCompress } from '../src/carmack.js';

const TAG = 0xabcd;

/** @param {number} n @param {() => number} gen */
function wordsToBytes(n, gen) {
  const out = new Uint8Array(n * 2);
  for (let i = 0; i < n; i++) {
    const w = gen() & 0xffff;
    out[i * 2] = w & 0xff;
    out[i * 2 + 1] = w >> 8;
  }
  return out;
}

/** Mulberry32 PRNG for reproducible tests. @param {number} seed */
function rng(seed) {
  let a = seed;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return (t ^ (t >>> 14)) >>> 0;
  };
}

describe('RLEW', () => {
  it('round-trips random data', () => {
    for (let seed = 1; seed <= 20; seed++) {
      const r = rng(seed);
      // Mix of runs and noise, like real map planes.
      const src = wordsToBytes(4096, () => (r() % 4 === 0 ? r() & 0xffff : 0x6b));
      const compressed = rlewCompress(src, TAG);
      const expanded = rlewExpand(compressed, src.byteLength, TAG);
      expect(expanded).toEqual(src);
    }
  });

  it('escapes the tag word itself', () => {
    const src = wordsToBytes(4, () => TAG);
    const compressed = rlewCompress(src, TAG);
    expect(rlewExpand(compressed, src.byteLength, TAG)).toEqual(src);
  });

  it('compresses runs', () => {
    const src = wordsToBytes(4096, () => 0x0001);
    const compressed = rlewCompress(src, TAG);
    expect(compressed.byteLength).toBe(6); // tag, count, value
  });
});

describe('Carmack', () => {
  it('round-trips random data', () => {
    for (let seed = 1; seed <= 20; seed++) {
      const r = rng(seed);
      const src = wordsToBytes(4096, () => (r() % 3 === 0 ? r() & 0xffff : (r() & 7) + 0x6a00));
      const compressed = carmackCompress(src);
      expect(carmackExpand(compressed, src.byteLength)).toEqual(src);
    }
  });

  it('round-trips words with A7/A8 high bytes (escape path)', () => {
    const vals = [0xa700, 0xa7ff, 0xa801, 0xa8a8, 0x1234, 0xa7a7];
    let i = 0;
    const src = wordsToBytes(vals.length, () => vals[i++]);
    const compressed = carmackCompress(src);
    expect(carmackExpand(compressed, src.byteLength)).toEqual(src);
  });

  it('round-trips highly repetitive data', () => {
    const src = wordsToBytes(4096, () => 0x6b);
    const compressed = carmackCompress(src);
    expect(compressed.byteLength).toBeLessThan(200);
    expect(carmackExpand(compressed, src.byteLength)).toEqual(src);
  });
});
