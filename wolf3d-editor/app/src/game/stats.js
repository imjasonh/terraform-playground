// Level statistics, mirroring what the engine counts in ScanInfoPlane /
// the end-of-level ratios (WL_INTER.C) and the vanilla limits.

import { MAP_WIDTH } from '@wolf3d/codec';
import { STATICS, ENEMIES, BOSSES, GHOSTS, isDoor, DEAD_GUARD, PUSHWALL, LIMITS } from '@wolf3d/data';
import { objectInfo, enemyMinSkill } from './objectinfo.js';

/**
 * @param {import('@wolf3d/codec').LevelData} lvl
 * @returns {{
 *   playerStarts: number,
 *   doors: number,
 *   pushwalls: number,
 *   statics: number,
 *   actorsBySkill: {easy:number, medium:number, hard:number},
 *   killsBySkill: {easy:number, medium:number, hard:number},
 *   treasureCount: number,
 *   treasurePoints: number,
 *   floorCodes: Map<number, number>,
 *   ammoUnits: number,
 *   healthUnits: number,
 *   issues: string[],
 * }}
 */
export function levelStats(lvl) {
  let playerStarts = 0;
  let doors = 0;
  let pushwalls = 0;
  let statics = 0;
  let treasureCount = 0;
  let treasurePoints = 0;
  let ammoUnits = 0;
  let healthUnits = 0;
  const actorsBySkill = { easy: 0, medium: 0, hard: 0 };
  const killsBySkill = { easy: 0, medium: 0, hard: 0 };
  /** @type {Map<number, number>} */
  const floorCodes = new Map();
  /** @type {string[]} */
  const issues = [];

  const bossByCode = new Map([...BOSSES, ...GHOSTS].map((b) => [b.code, b]));
  const staticByCode = new Map(STATICS.map((s) => [s.code, s]));

  for (let i = 0; i < MAP_WIDTH * MAP_WIDTH; i++) {
    const w = lvl.plane0[i];
    if (isDoor(w)) doors++;
    if (w >= 107 && w <= 143) floorCodes.set(w, (floorCodes.get(w) ?? 0) + 1);

    const o = lvl.plane1[i];
    if (o === 0) continue;
    if (o >= 19 && o <= 22) playerStarts++;
    else if (o === PUSHWALL) pushwalls++;
    else if (o === DEAD_GUARD) {
      for (const s of ['easy', 'medium', 'hard']) {
        actorsBySkill[s]++;
        killsBySkill[s]++;
      }
    } else if (staticByCode.has(o)) {
      statics++;
      const st = staticByCode.get(o);
      if (st.treasure) {
        treasureCount++;
        treasurePoints += st.points ?? 0;
      }
      if (st.bonus === 'bo_clip') ammoUnits += 8;
      if (st.bonus === 'bo_clip2') ammoUnits += 4;
      if (st.bonus === 'bo_machinegun' || st.bonus === 'bo_chaingun') ammoUnits += 6;
      if (st.bonus === 'bo_alpo') healthUnits += 4;
      if (st.bonus === 'bo_food') healthUnits += 10;
      if (st.bonus === 'bo_firstaid') healthUnits += 25;
    } else if (bossByCode.has(o)) {
      const b = bossByCode.get(o);
      for (const s of ['easy', 'medium', 'hard']) {
        actorsBySkill[s] += b.actorCost;
        killsBySkill[s] += b.actorCost;
      }
    } else {
      const min = enemyMinSkill(o);
      if (min) {
        const skills = min === 'easy' ? ['easy', 'medium', 'hard'] : min === 'medium' ? ['medium', 'hard'] : ['hard'];
        for (const s of skills) {
          actorsBySkill[s]++;
          killsBySkill[s]++;
        }
      } else if (o >= 90 && o <= 97) {
        // turn points: no cost
      } else if (o === 99) {
        // exit trigger
      } else if (!objectInfo(o) || objectInfo(o)?.kind === 'unknown') {
        issues.push(`Unknown object code ${o}`);
      }
    }
  }

  if (playerStarts !== 1) issues.push(`${playerStarts} player starts (must be exactly 1)`);
  if (doors > LIMITS.maxDoors) issues.push(`${doors} doors (engine max ${LIMITS.maxDoors})`);
  if (statics > LIMITS.maxStatics) issues.push(`${statics} statics (engine max ${LIMITS.maxStatics})`);
  if (actorsBySkill.hard > LIMITS.maxActors) issues.push(`${actorsBySkill.hard} actors on hard (engine max ${LIMITS.maxActors})`);

  return {
    playerStarts,
    doors,
    pushwalls,
    statics,
    actorsBySkill,
    killsBySkill,
    treasureCount,
    treasurePoints,
    floorCodes,
    ammoUnits,
    healthUnits,
    issues,
  };
}

/** Enemy count breakdown per type at a skill. @param {import('@wolf3d/codec').LevelData} lvl @param {'easy'|'medium'|'hard'} skill */
export function enemyBreakdown(lvl, skill) {
  /** @type {Map<string, number>} */
  const counts = new Map();
  const order = { easy: 0, medium: 1, hard: 2 };
  for (let i = 0; i < lvl.plane1.length; i++) {
    const info = objectInfo(lvl.plane1[i]);
    if (info?.kind === 'enemy' && order[info.skill] <= order[skill]) {
      const type = ENEMIES.find((e) => e.sprite === info.sprite);
      const key = type?.name ?? 'enemy';
      counts.set(key, (counts.get(key) ?? 0) + 1);
    }
  }
  return counts;
}
