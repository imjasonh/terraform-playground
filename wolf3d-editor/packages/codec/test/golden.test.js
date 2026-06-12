// Golden tests against the freely-distributable Wolfenstein 3D shareware
// v1.4 data files (fixtures/wl1). These verify the codecs against bytes the
// real game ships and loads.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import {
  parseMapHead,
  parseGameMaps,
  buildGameMaps,
  parseVSwap,
  buildVSwap,
  decodeWall,
  encodeWall,
  decodeSprite,
  encodeSprite,
  parseHuffDict,
  parseVgaHead,
  splitVgaChunks,
  expandVgaChunk,
  compressVgaChunk,
  huffExpand,
  parsePicTable,
  demungePic,
  mungePic,
} from '../src/index.js';

const fix = (/** @type {string} */ name) =>
  new Uint8Array(readFileSync(fileURLToPath(new URL(`../../../fixtures/wl1/${name}`, import.meta.url))));

const maphead = fix('maphead.wl1');
const gamemaps = fix('gamemaps.wl1');
const vswapBytes = fix('vswap.wl1');
const vgadict = fix('vgadict.wl1');
const vgahead = fix('vgahead.wl1');
const vgagraph = fix('vgagraph.wl1');

describe('GAMEMAPS.WL1 golden', () => {
  const head = parseMapHead(maphead);
  const levels = parseGameMaps(gamemaps, head);

  it('has the expected RLEW tag and 10 levels', () => {
    expect(head.rlewTag).toBe(0xabcd);
    expect(levels.filter(Boolean)).toHaveLength(10);
  });

  it('decodes E1L1 with known facts', () => {
    const l1 = levels[0];
    expect(l1).toBeTruthy();
    if (!l1) return;
    expect(l1.name).toBe('Wolf1 Map1');
    expect(l1.plane0).toHaveLength(64 * 64);
    expect(l1.plane1).toHaveLength(64 * 64);
    // Exactly one player start (codes 19-22) on every shareware level.
    for (const lvl of levels) {
      if (!lvl) continue;
      let starts = 0;
      for (const v of lvl.plane1) if (v >= 19 && v <= 22) starts++;
      expect(starts).toBe(1);
    }
    // E1L1 starts in the well-known elevator: there must be an elevator wall
    // (21) somewhere and the border ring must be solid wall (1..89).
    expect(Array.from(l1.plane0).some((v) => v === 21)).toBe(true);
    for (let x = 0; x < 64; x++) {
      const top = l1.plane0[x];
      const bottom = l1.plane0[63 * 64 + x];
      expect(top).toBeGreaterThan(0);
      expect(top).toBeLessThan(90);
      expect(bottom).toBeGreaterThan(0);
      expect(bottom).toBeLessThan(90);
    }
  });

  it('round-trips: rebuild + reparse yields identical planes and names', () => {
    const rebuilt = buildGameMaps(levels, head.rlewTag);
    const head2 = parseMapHead(rebuilt.maphead);
    const levels2 = parseGameMaps(rebuilt.gamemaps, head2);
    for (let i = 0; i < 100; i++) {
      const a = levels[i];
      const b = levels2[i];
      expect(!!a).toBe(!!b);
      if (!a || !b) continue;
      expect(b.name).toBe(a.name);
      expect(b.plane0).toEqual(a.plane0);
      expect(b.plane1).toEqual(a.plane1);
    }
  });
});

describe('VSWAP.WL1 golden', () => {
  const swap = parseVSwap(vswapBytes);

  it('has the expected chunk layout', () => {
    // Shareware v1.4: 64 wall textures? It has walls then sprites then sounds.
    expect(swap.spriteStart).toBeGreaterThan(0);
    expect(swap.soundStart).toBeGreaterThan(swap.spriteStart);
    expect(swap.numChunks).toBe(swap.chunks.length);
    // All wall chunks present are exactly 4096 bytes.
    for (let i = 0; i < swap.spriteStart; i++) {
      const c = swap.chunks[i];
      if (c) expect(c.byteLength).toBe(4096);
    }
  });

  it('wall encode/decode round-trips', () => {
    for (let i = 0; i < swap.spriteStart; i++) {
      const c = swap.chunks[i];
      if (!c) continue;
      expect(encodeWall(decodeWall(c))).toEqual(c);
    }
  });

  it('sprite decode -> encode -> decode is pixel-identical for all sprites', () => {
    for (let i = swap.spriteStart; i < swap.soundStart; i++) {
      const c = swap.chunks[i];
      if (!c) continue;
      const pixels = decodeSprite(c);
      const reencoded = encodeSprite(pixels);
      expect(decodeSprite(reencoded)).toEqual(pixels);
    }
  });

  it('vswap container round-trips byte-identically', () => {
    const rebuilt = buildVSwap(swap);
    const swap2 = parseVSwap(rebuilt);
    expect(swap2.numChunks).toBe(swap.numChunks);
    expect(swap2.spriteStart).toBe(swap.spriteStart);
    expect(swap2.soundStart).toBe(swap.soundStart);
    for (let i = 0; i < swap.chunks.length; i++) {
      expect(swap2.chunks[i]).toEqual(swap.chunks[i]);
    }
  });
});

describe('VGAGRAPH.WL1 golden', () => {
  const nodes = parseHuffDict(vgadict);
  const offsets = parseVgaHead(vgahead);
  const chunks = splitVgaChunks(vgagraph, offsets);

  it('expands the pictable (chunk 0)', () => {
    const c0 = chunks[0];
    expect(c0).toBeTruthy();
    if (!c0) return;
    const table = parsePicTable(expandVgaChunk(c0, nodes));
    expect(table.length).toBeGreaterThan(100); // WL1 1.4 has ~130+ pics
    // All pic dimensions are sane VGA sizes.
    for (const pic of table) {
      expect(pic.width).toBeGreaterThan(0);
      expect(pic.width).toBeLessThanOrEqual(320);
      expect(pic.height).toBeGreaterThan(0);
      expect(pic.height).toBeLessThanOrEqual(200);
    }
    // The 320x200 title screen and 320x40 status bar exist.
    expect(table.some((p) => p.width === 320 && p.height === 200)).toBe(true);
    expect(table.some((p) => p.width === 320 && p.height === 40)).toBe(true);
  });

  it('huffman compress with the file dictionary round-trips', () => {
    const c0 = chunks[0];
    if (!c0) return;
    const data = expandVgaChunk(c0, nodes);
    const recompressed = compressVgaChunk(data, nodes);
    expect(expandVgaChunk(recompressed, nodes)).toEqual(data);
  });

  it('pic munge/demunge round-trips', () => {
    const c0 = chunks[0];
    if (!c0) return;
    const table = parsePicTable(expandVgaChunk(c0, nodes));
    // Probe several pic chunks; pics start at STRUCTPIC+3 (after the two
    // fonts) in WL1 v1.4 = chunk 3.
    for (let pic = 0; pic < 5; pic++) {
      const chunk = chunks[3 + pic];
      if (!chunk || !table[pic]) continue;
      const { width, height } = table[pic];
      const data = expandVgaChunk(chunk, nodes);
      if (data.byteLength !== width * height) continue;
      const linear = demungePic(data, width, height);
      expect(mungePic(linear, width, height)).toEqual(data);
    }
  });
});
