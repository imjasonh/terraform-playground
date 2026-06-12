// Wall plane (plane 0) catalog for Wolfenstein 3D.
// Engine semantics verified against WL_DEF.H / SetupGameLevel / SpawnDoor;
// display names follow MapEdit's MAPDATA conventions.

/** Wall code n uses VSWAP chunks (n-1)*2 (light, N/S) and (n-1)*2+1 (dark, E/W). */
export const WALL_NAMES = [
  'Grey stone 1',
  'Grey stone 2',
  'Grey stone / flag',
  'Grey stone / Hitler portrait',
  'Cell',
  'Grey stone / eagle',
  'Cell / skeleton',
  'Blue stone 1',
  'Blue stone 2',
  'Wood / eagle',
  'Wood / Hitler portrait',
  'Wood',
  'Entrance to level',
  'Steel / sign',
  'Steel',
  'Landscape (window)',
  'Red brick',
  'Red brick / wreath',
  'Purple',
  'Red brick / eagle',
  'Elevator',
  'Fake/used elevator',
  'Wood / iron cross',
  'Dirty brick 1',
  'Purple / blood',
  'Dirty brick 2',
  'Grey brick 3',
  'Grey brick / sign',
  'Brown weave',
  'Brown weave / blood 2',
  'Brown weave / blood 3',
  'Brown weave / blood 1',
  'Stained glass',
  'Blue wall / skull',
  'Grey wall 1',
  'Blue wall / swastika',
  'Grey wall / vent',
  'Multicolor brick',
  'Grey wall 2',
  'Blue wall',
  'Blue brick / sign',
  'Brown marble 1',
  'Grey wall / map',
  'Brown stone 1',
  'Brown stone 2',
  'Brown marble 2',
  'Brown marble / flag',
  'Wood panel',
  'Grey wall / Hitler poster',
];

// Engine tile constants (WL_DEF.H).
export const ELEVATOR_TILE = 21;
export const AMBUSH_TILE = 106; // "deaf guard" tile, MapEdit hex 6A
export const AREA_TILE = 107; // first floor code; also ALTELEVATORTILE (secret elevator)
export const NUM_AREAS = 37; // floor codes 107..143
export const LAST_AREA_TILE = AREA_TILE + NUM_AREAS - 1; // 143
export const MAX_WALL_TILES = 64; // wall codes must stay below this

export const DOOR_FIRST = 90;
export const DOOR_LAST = 101;

/**
 * @typedef {Object} DoorInfo
 * @property {number} code
 * @property {'vertical'|'horizontal'} orientation vertical = door in a N-S wall run
 * @property {number} lock 0=none, 1=gold, 2=silver, 3/4=unused vanilla, 5=elevator
 * @property {string} name
 */

/** @type {DoorInfo[]} */
export const DOORS = [
  { code: 90, orientation: 'vertical', lock: 0, name: 'Door (vertical)' },
  { code: 91, orientation: 'horizontal', lock: 0, name: 'Door (horizontal)' },
  { code: 92, orientation: 'vertical', lock: 1, name: 'Gold-locked door (vertical)' },
  { code: 93, orientation: 'horizontal', lock: 1, name: 'Gold-locked door (horizontal)' },
  { code: 94, orientation: 'vertical', lock: 2, name: 'Silver-locked door (vertical)' },
  { code: 95, orientation: 'horizontal', lock: 2, name: 'Silver-locked door (horizontal)' },
  { code: 96, orientation: 'vertical', lock: 3, name: 'Locked door 3 (vertical, unused)' },
  { code: 97, orientation: 'horizontal', lock: 3, name: 'Locked door 3 (horizontal, unused)' },
  { code: 98, orientation: 'vertical', lock: 4, name: 'Locked door 4 (vertical, unused)' },
  { code: 99, orientation: 'horizontal', lock: 4, name: 'Locked door 4 (horizontal, unused)' },
  { code: 100, orientation: 'vertical', lock: 5, name: 'Elevator door (vertical)' },
  { code: 101, orientation: 'horizontal', lock: 5, name: 'Elevator door (horizontal)' },
];

/** @param {number} code @returns {boolean} */
export function isDoor(code) {
  return code >= DOOR_FIRST && code <= DOOR_LAST;
}

/** @param {number} code @returns {boolean} */
export function isFloorCode(code) {
  return code >= AREA_TILE && code <= LAST_AREA_TILE;
}

/** @param {number} code @returns {boolean} solid wall as the engine sees it (tile < AREATILE, not a door) */
export function isSolidWall(code) {
  return code >= 1 && code < AREA_TILE && !isDoor(code) && code !== AMBUSH_TILE;
}

/**
 * Describe a plane-0 value for status readouts.
 * @param {number} code
 * @returns {string}
 */
export function describeWallCode(code) {
  if (code === 0) return 'Nothing (no floor code!)';
  if (code <= WALL_NAMES.length) return WALL_NAMES[code - 1];
  if (code < DOOR_FIRST) return code < MAX_WALL_TILES ? `Wall ${code} (no vanilla texture)` : `Invalid (${code})`;
  if (isDoor(code)) return /** @type {DoorInfo} */ (DOORS.find((d) => d.code === code)).name;
  if (code === AMBUSH_TILE) return 'Deaf guard / ambush tile (6A)';
  if (isFloorCode(code)) {
    const hex = code.toString(16).toUpperCase();
    return code === AREA_TILE ? `Floor code ${hex} (secret elevator)` : `Floor code ${hex}`;
  }
  return `Invalid (${code})`;
}
