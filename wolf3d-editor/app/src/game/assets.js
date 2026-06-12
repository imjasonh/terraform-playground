// Decoded-asset cache: turns VSWAP chunks into canvases for the editor.

import { decodeWall, decodeSprite, WALL_SIZE, WOLF_PALETTE } from '@wolf3d/codec';
import { spriteIndex } from '@wolf3d/data';

/**
 * Convert 64x64 palette-indexed pixels to a canvas (255 = transparent).
 * @param {Uint8Array} pixels
 * @param {boolean} transparent
 * @returns {HTMLCanvasElement}
 */
export function pixelsToCanvas(pixels, transparent) {
  const canvas = document.createElement('canvas');
  canvas.width = WALL_SIZE;
  canvas.height = WALL_SIZE;
  const ctx = canvas.getContext('2d');
  const img = ctx.createImageData(WALL_SIZE, WALL_SIZE);
  for (let i = 0; i < pixels.length; i++) {
    const p = pixels[i];
    if (transparent && p === 255) {
      img.data[i * 4 + 3] = 0;
      continue;
    }
    img.data[i * 4] = WOLF_PALETTE[p * 3];
    img.data[i * 4 + 1] = WOLF_PALETTE[p * 3 + 1];
    img.data[i * 4 + 2] = WOLF_PALETTE[p * 3 + 2];
    img.data[i * 4 + 3] = 255;
  }
  ctx.putImageData(img, 0, 0);
  return canvas;
}

export class AssetCache {
  /** @param {import('@wolf3d/codec').VSwap} vswap */
  constructor(vswap) {
    this.vswap = vswap;
    /** @type {Map<number, HTMLCanvasElement|null>} */
    this.wallCanvases = new Map();
    /** @type {Map<number, Uint8Array|null>} */
    this.wallPixels = new Map();
    /** @type {Map<number, HTMLCanvasElement|null>} */
    this.spriteCanvases = new Map();
    /** @type {Map<number, Uint8Array|null>} */
    this.spritePixels = new Map();
  }

  get numWalls() {
    return this.vswap.spriteStart;
  }

  get numSprites() {
    return this.vswap.soundStart - this.vswap.spriteStart;
  }

  /**
   * Raw pixels for wall chunk index (row-major), or null if absent.
   * @param {number} chunkIndex
   */
  wallPixelsAt(chunkIndex) {
    if (!this.wallPixels.has(chunkIndex)) {
      const chunk = chunkIndex >= 0 && chunkIndex < this.vswap.spriteStart ? this.vswap.chunks[chunkIndex] : null;
      this.wallPixels.set(chunkIndex, chunk ? decodeWall(chunk) : null);
    }
    return this.wallPixels.get(chunkIndex) ?? null;
  }

  /**
   * Canvas for a wall chunk index, or null.
   * @param {number} chunkIndex
   */
  wallCanvasAt(chunkIndex) {
    if (!this.wallCanvases.has(chunkIndex)) {
      const px = this.wallPixelsAt(chunkIndex);
      this.wallCanvases.set(chunkIndex, px ? pixelsToCanvas(px, false) : null);
    }
    return this.wallCanvases.get(chunkIndex) ?? null;
  }

  /**
   * Light-face texture canvas for a wall code (1-63).
   * @param {number} code
   */
  wallForCode(code) {
    return this.wallCanvasAt((code - 1) * 2);
  }

  /** Door face texture chunk indices: DOORWALL = spriteStart - 8. */
  get doorWallBase() {
    return this.vswap.spriteStart - 8;
  }

  /**
   * Door texture canvas: kind 0=normal, 1=jamb, 2=elevator, 3=locked.
   * @param {number} kind
   * @param {boolean} [dark]
   */
  doorCanvas(kind, dark = false) {
    return this.wallCanvasAt(this.doorWallBase + kind * 2 + (dark ? 1 : 0));
  }

  /**
   * Sprite pixels by sprite number (row-major, 255 = transparent).
   * @param {number} num
   */
  spritePixelsAt(num) {
    if (!this.spritePixels.has(num)) {
      const chunk =
        num >= 0 && this.vswap.spriteStart + num < this.vswap.soundStart
          ? this.vswap.chunks[this.vswap.spriteStart + num]
          : null;
      let px = null;
      if (chunk && chunk.byteLength >= 4) {
        try {
          px = decodeSprite(chunk);
        } catch {
          px = null;
        }
      }
      this.spritePixels.set(num, px);
    }
    return this.spritePixels.get(num) ?? null;
  }

  /**
   * Sprite canvas by sprite number.
   * @param {number} num
   */
  spriteCanvasAt(num) {
    if (!this.spriteCanvases.has(num)) {
      const px = this.spritePixelsAt(num);
      this.spriteCanvases.set(num, px ? pixelsToCanvas(px, true) : null);
    }
    return this.spriteCanvases.get(num) ?? null;
  }

  /**
   * Sprite canvas by SPR_* name.
   * @param {string} name
   */
  spriteByName(name) {
    const idx = spriteIndex(name);
    return idx >= 0 ? this.spriteCanvasAt(idx) : null;
  }

  /** Drop cached decodes for a wall chunk (after an edit). @param {number} chunkIndex */
  invalidateWall(chunkIndex) {
    this.wallPixels.delete(chunkIndex);
    this.wallCanvases.delete(chunkIndex);
  }

  /** Drop cached decodes for a sprite (after an edit). @param {number} num */
  invalidateSprite(num) {
    this.spritePixels.delete(num);
    this.spriteCanvases.delete(num);
  }
}

/**
 * CSS color string for a palette index.
 * @param {number} index
 */
export function paletteColor(index) {
  return `rgb(${WOLF_PALETTE[index * 3]},${WOLF_PALETTE[index * 3 + 1]},${WOLF_PALETTE[index * 3 + 2]})`;
}

/** Deterministic pastel color for a floor code, for the floor-code layer. */
export function floorCodeColor(code, alpha = 1) {
  const hue = ((code - 107) * 53) % 360;
  return `hsla(${hue}, 45%, 38%, ${alpha})`;
}
