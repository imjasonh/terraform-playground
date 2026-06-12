export * from './walls.js';
export * from './objects.js';
export * from './limits.js';
export * from './levels.js';
export { SPRITE_NAMES } from './sprites-wolf3d.js';

import { SPRITE_NAMES } from './sprites-wolf3d.js';

/**
 * Resolve a SPR_* name to its VSWAP sprite index (chunk = spriteStart + index).
 * @param {string} name
 * @returns {number} -1 if unknown
 */
export function spriteIndex(name) {
  return SPRITE_NAMES.indexOf(name);
}
