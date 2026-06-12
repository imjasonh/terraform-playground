// Minimal dependency-free ZIP support.
// Writer emits stored (method 0) entries; reader handles stored and deflate
// (method 8, via the browser's DecompressionStream).

const CRC_TABLE = (() => {
  const t = new Uint32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    t[n] = c >>> 0;
  }
  return t;
})();

/** @param {Uint8Array} data */
export function crc32(data) {
  let c = 0xffffffff;
  for (let i = 0; i < data.length; i++) c = CRC_TABLE[(c ^ data[i]) & 0xff] ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}

/**
 * Build a ZIP file (all entries stored uncompressed — game data is already
 * compressed and stays byte-exact).
 * @param {{name: string, data: Uint8Array}[]} entries
 * @returns {Uint8Array}
 */
export function buildZip(entries) {
  const encoder = new TextEncoder();
  /** @type {{nameBytes: Uint8Array, data: Uint8Array, crc: number, offset: number}[]} */
  const records = [];
  let size = 0;
  for (const e of entries) {
    const nameBytes = encoder.encode(e.name);
    records.push({ nameBytes, data: e.data, crc: crc32(e.data), offset: size });
    size += 30 + nameBytes.length + e.data.length;
  }
  const centralStart = size;
  for (const r of records) size += 46 + r.nameBytes.length;
  const centralSize = size - centralStart;
  size += 22;

  const out = new Uint8Array(size);
  const dv = new DataView(out.buffer);
  let pos = 0;
  for (const r of records) {
    dv.setUint32(pos, 0x04034b50, true);
    dv.setUint16(pos + 4, 20, true); // version needed
    dv.setUint16(pos + 8, 0, true); // method: stored
    dv.setUint32(pos + 14, r.crc, true);
    dv.setUint32(pos + 18, r.data.length, true);
    dv.setUint32(pos + 22, r.data.length, true);
    dv.setUint16(pos + 26, r.nameBytes.length, true);
    out.set(r.nameBytes, pos + 30);
    out.set(r.data, pos + 30 + r.nameBytes.length);
    pos += 30 + r.nameBytes.length + r.data.length;
  }
  for (const r of records) {
    dv.setUint32(pos, 0x02014b50, true);
    dv.setUint16(pos + 4, 20, true);
    dv.setUint16(pos + 6, 20, true);
    dv.setUint32(pos + 16, r.crc, true);
    dv.setUint32(pos + 20, r.data.length, true);
    dv.setUint32(pos + 24, r.data.length, true);
    dv.setUint16(pos + 28, r.nameBytes.length, true);
    dv.setUint32(pos + 42, r.offset, true);
    out.set(r.nameBytes, pos + 46);
    pos += 46 + r.nameBytes.length;
  }
  dv.setUint32(pos, 0x06054b50, true);
  dv.setUint16(pos + 8, records.length, true);
  dv.setUint16(pos + 10, records.length, true);
  dv.setUint32(pos + 12, centralSize, true);
  dv.setUint32(pos + 16, centralStart, true);
  return out;
}

/**
 * Read a ZIP file. Supports stored and deflate entries.
 * @param {Uint8Array} bytes
 * @returns {Promise<{name: string, data: Uint8Array}[]>}
 */
export async function readZip(bytes) {
  const dv = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  // Find end-of-central-directory.
  let eocd = -1;
  for (let i = bytes.length - 22; i >= Math.max(0, bytes.length - 65558); i--) {
    if (dv.getUint32(i, true) === 0x06054b50) {
      eocd = i;
      break;
    }
  }
  if (eocd < 0) throw new Error('Not a ZIP file');
  const count = dv.getUint16(eocd + 10, true);
  let pos = dv.getUint32(eocd + 16, true);
  const decoder = new TextDecoder();
  /** @type {{name: string, data: Uint8Array}[]} */
  const entries = [];
  for (let i = 0; i < count; i++) {
    if (dv.getUint32(pos, true) !== 0x02014b50) throw new Error('Bad central directory');
    const method = dv.getUint16(pos + 10, true);
    const compSize = dv.getUint32(pos + 20, true);
    const nameLen = dv.getUint16(pos + 28, true);
    const extraLen = dv.getUint16(pos + 30, true);
    const commentLen = dv.getUint16(pos + 32, true);
    const localOfs = dv.getUint32(pos + 42, true);
    const name = decoder.decode(bytes.subarray(pos + 46, pos + 46 + nameLen));
    // Local header: skip its own name/extra lengths.
    const lNameLen = dv.getUint16(localOfs + 26, true);
    const lExtraLen = dv.getUint16(localOfs + 28, true);
    const dataStart = localOfs + 30 + lNameLen + lExtraLen;
    const raw = bytes.slice(dataStart, dataStart + compSize);
    if (!name.endsWith('/')) {
      if (method === 0) {
        entries.push({ name, data: raw });
      } else if (method === 8) {
        const ds = new DecompressionStream('deflate-raw');
        const stream = new Blob([raw]).stream().pipeThrough(ds);
        const data = new Uint8Array(await new Response(stream).arrayBuffer());
        entries.push({ name, data });
      } else {
        throw new Error(`Unsupported ZIP method ${method} for ${name}`);
      }
    }
    pos += 46 + nameLen + extraLen + commentLen;
  }
  return entries;
}

/**
 * Trigger a browser download of bytes.
 * @param {string} filename
 * @param {Uint8Array} bytes
 */
export function download(filename, bytes) {
  const ab = new ArrayBuffer(bytes.byteLength);
  new Uint8Array(ab).set(bytes);
  const blob = new Blob([ab], { type: 'application/octet-stream' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 5000);
}
