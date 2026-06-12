// Huffman codec for VGADICT/VGAGRAPH (and AUDIOHED-era id files).
// Tree semantics verified against CAL_HuffExpand in WOLFSRC/ID_CA.C:
// 255 nodes of {u16 bit0, u16 bit1}; values < 256 are literal bytes, values
// >= 256 reference node (value - 256); the root is node 254. Bits are
// consumed LSB-first within each input byte.

/**
 * @typedef {Object} HuffNode
 * @property {number} bit0
 * @property {number} bit1
 */

/**
 * Parse a 1024-byte VGADICT into 255 nodes.
 * @param {Uint8Array} bytes
 * @returns {HuffNode[]}
 */
export function parseHuffDict(bytes) {
  const dv = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  /** @type {HuffNode[]} */
  const nodes = [];
  for (let i = 0; i < 255; i++) {
    nodes.push({ bit0: dv.getUint16(i * 4, true), bit1: dv.getUint16(i * 4 + 2, true) });
  }
  return nodes;
}

/**
 * Serialize 255 nodes to a 1024-byte VGADICT.
 * @param {HuffNode[]} nodes
 * @returns {Uint8Array}
 */
export function buildHuffDict(nodes) {
  const out = new Uint8Array(1024);
  const dv = new DataView(out.buffer);
  for (let i = 0; i < 255; i++) {
    dv.setUint16(i * 4, nodes[i].bit0, true);
    dv.setUint16(i * 4 + 2, nodes[i].bit1, true);
  }
  return out;
}

/**
 * Huffman-expand `src` to `expandedLength` bytes using the dictionary.
 * @param {Uint8Array} src
 * @param {number} expandedLength
 * @param {HuffNode[]} nodes
 * @returns {Uint8Array}
 */
export function huffExpand(src, expandedLength, nodes) {
  const out = new Uint8Array(expandedLength);
  let oi = 0;
  let node = nodes[254];
  let si = 0;
  let bit = 1;
  let byte = src[0];
  while (oi < expandedLength) {
    const value = byte & bit ? node.bit1 : node.bit0;
    if (bit === 0x80) {
      si++;
      byte = src[si];
      bit = 1;
    } else {
      bit <<= 1;
    }
    if (value < 256) {
      out[oi++] = value;
      node = nodes[254];
    } else {
      node = nodes[value - 256];
    }
  }
  return out;
}

/**
 * Build a byte -> bit-path code table from the dictionary (DFS from root).
 * @param {HuffNode[]} nodes
 * @returns {({bits:number[], len:number}|null)[]} 256 entries
 */
export function buildCodeTable(nodes) {
  /** @type {({bits:number[], len:number}|null)[]} */
  const table = new Array(256).fill(null);
  /** @type {{value:number, path:number[]}[]} */
  const stack = [{ value: 254 + 256, path: [] }];
  while (stack.length) {
    const { value, path } = /** @type {{value:number, path:number[]}} */ (stack.pop());
    if (value < 256) {
      table[value] = { bits: path, len: path.length };
      continue;
    }
    const node = nodes[value - 256];
    stack.push({ value: node.bit0, path: [...path, 0] });
    stack.push({ value: node.bit1, path: [...path, 1] });
  }
  return table;
}

/**
 * Huffman-compress with an existing dictionary (the standard workflow:
 * VGADICT trees are reused, not rebuilt).
 * @param {Uint8Array} src
 * @param {HuffNode[]} nodes
 * @returns {Uint8Array}
 */
export function huffCompress(src, nodes) {
  const table = buildCodeTable(nodes);
  /** @type {number[]} */
  const out = [];
  let acc = 0;
  let nbits = 0;
  for (let i = 0; i < src.byteLength; i++) {
    const code = table[src[i]];
    if (!code) throw new Error(`huffman: byte ${src[i]} unreachable in dictionary`);
    for (const b of code.bits) {
      if (b) acc |= 1 << nbits;
      nbits++;
      if (nbits === 8) {
        out.push(acc);
        acc = 0;
        nbits = 0;
      }
    }
  }
  if (nbits > 0) out.push(acc);
  return Uint8Array.from(out);
}
