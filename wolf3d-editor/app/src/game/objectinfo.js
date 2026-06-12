// Precomputed plane-1 code descriptions (describeObjectCode is loop-based;
// cache the full 16-bit-feasible range we care about).

import { describeObjectCode, ENEMIES, SKILLS, enemyCode } from '@wolf3d/data';

/** @type {Map<number, ReturnType<typeof describeObjectCode>>} */
const cache = new Map();

/** @param {number} code */
export function objectInfo(code) {
  if (!cache.has(code)) cache.set(code, describeObjectCode(code));
  return cache.get(code) ?? null;
}

/**
 * Minimum skill at which an enemy code spawns ('easy' codes spawn always).
 * Returns null for non-enemy codes.
 * @param {number} code
 * @returns {'easy'|'medium'|'hard'|null}
 */
export function enemyMinSkill(code) {
  const info = objectInfo(code);
  if (!info || info.kind !== 'enemy') return null;
  return /** @type {'easy'|'medium'|'hard'} */ (info.skill);
}

/**
 * Does this plane-1 code spawn at the given skill filter?
 * @param {number} code
 * @param {'all'|'easy'|'medium'|'hard'} filter
 */
export function spawnsAtSkill(code, filter) {
  if (filter === 'all') return true;
  const min = enemyMinSkill(code);
  if (!min) return true;
  const order = { easy: 0, medium: 1, hard: 2 };
  return order[min] <= order[filter];
}

/** All enemy codes for a given type id, useful for search/replace. */
export function allEnemyCodes() {
  /** @type {number[]} */
  const codes = [];
  for (const e of ENEMIES) {
    for (const skill of /** @type {('easy'|'medium'|'hard')[]} */ (SKILLS)) {
      for (const mode of /** @type {('stand'|'patrol')[]} */ (['stand', 'patrol'])) {
        for (let f = 0; f < 4; f++) codes.push(enemyCode(e, mode, /** @type {0|1|2|3} */ (f), skill));
      }
    }
  }
  return codes;
}
