import { describe, it, expect } from 'vitest';
import { buildZip, readZip, crc32 } from '../src/io/zip.js';

describe('zip container', () => {
  it('round-trips entries', async () => {
    const entries = [
      { name: 'GAMEMAPS.WL6', data: Uint8Array.from({ length: 5000 }, (_, i) => (i * 7) & 0xff) },
      { name: 'MAPHEAD.WL6', data: Uint8Array.from({ length: 402 }, (_, i) => i & 0xff) },
      { name: 'dir/readme.txt', data: new TextEncoder().encode('hello wolf') },
    ];
    const zip = buildZip(entries);
    const back = await readZip(zip);
    expect(back).toHaveLength(3);
    for (let i = 0; i < entries.length; i++) {
      expect(back[i].name).toBe(entries[i].name);
      expect(back[i].data).toEqual(entries[i].data);
    }
  });

  it('computes standard CRC32', () => {
    // CRC32 of "123456789" is 0xCBF43926 (the canonical check value).
    expect(crc32(new TextEncoder().encode('123456789'))).toBe(0xcbf43926);
  });
});
