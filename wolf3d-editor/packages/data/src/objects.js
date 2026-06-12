// Object plane (plane 1) catalog for Wolfenstein 3D.
// Verified against ScanInfoPlane (WL_GAME.C) and statinfo[] (WL_ACT1.C).

/**
 * @typedef {Object} StaticInfo
 * @property {number} code plane-1 value
 * @property {string} name
 * @property {'dressing'|'block'|'bonus'} kind
 * @property {string} [bonus] bo_* semantic for pickups
 * @property {string} [effect] human description of pickup effect
 * @property {number} [points] treasure score
 * @property {boolean} [treasure] counts toward treasure ratio
 * @property {string} sprite SPR_* name for the icon
 * @property {'decoration'|'treasure'|'health'|'ammo'|'weapon'|'key'} category
 */

/** @type {StaticInfo[]} */
export const STATICS = [
  { code: 23, name: 'Puddle', kind: 'dressing', sprite: 'SPR_STAT_0', category: 'decoration' },
  { code: 24, name: 'Green barrel', kind: 'block', sprite: 'SPR_STAT_1', category: 'decoration' },
  { code: 25, name: 'Table & chairs', kind: 'block', sprite: 'SPR_STAT_2', category: 'decoration' },
  { code: 26, name: 'Floor lamp', kind: 'block', sprite: 'SPR_STAT_3', category: 'decoration' },
  { code: 27, name: 'Chandelier', kind: 'dressing', sprite: 'SPR_STAT_4', category: 'decoration' },
  { code: 28, name: 'Hanged skeleton', kind: 'block', sprite: 'SPR_STAT_5', category: 'decoration' },
  { code: 29, name: 'Dog food', kind: 'bonus', bonus: 'bo_alpo', effect: '+4 health', sprite: 'SPR_STAT_6', category: 'health' },
  { code: 30, name: 'Red pillar', kind: 'block', sprite: 'SPR_STAT_7', category: 'decoration' },
  { code: 31, name: 'Tree', kind: 'block', sprite: 'SPR_STAT_8', category: 'decoration' },
  { code: 32, name: 'Flat skeleton', kind: 'dressing', sprite: 'SPR_STAT_9', category: 'decoration' },
  { code: 33, name: 'Sink', kind: 'block', sprite: 'SPR_STAT_10', category: 'decoration' },
  { code: 34, name: 'Potted plant', kind: 'block', sprite: 'SPR_STAT_11', category: 'decoration' },
  { code: 35, name: 'Urn', kind: 'block', sprite: 'SPR_STAT_12', category: 'decoration' },
  { code: 36, name: 'Bare table', kind: 'block', sprite: 'SPR_STAT_13', category: 'decoration' },
  { code: 37, name: 'Ceiling light', kind: 'dressing', sprite: 'SPR_STAT_14', category: 'decoration' },
  { code: 38, name: 'Kitchen utensils', kind: 'dressing', sprite: 'SPR_STAT_15', category: 'decoration' },
  { code: 39, name: 'Suit of armor', kind: 'block', sprite: 'SPR_STAT_16', category: 'decoration' },
  { code: 40, name: 'Hanging cage', kind: 'block', sprite: 'SPR_STAT_17', category: 'decoration' },
  { code: 41, name: 'Skeleton in cage', kind: 'block', sprite: 'SPR_STAT_18', category: 'decoration' },
  { code: 42, name: 'Relaxing skeleton', kind: 'dressing', sprite: 'SPR_STAT_19', category: 'decoration' },
  { code: 43, name: 'Gold key', kind: 'bonus', bonus: 'bo_key1', effect: 'opens gold locks', sprite: 'SPR_STAT_20', category: 'key' },
  { code: 44, name: 'Silver key', kind: 'bonus', bonus: 'bo_key2', effect: 'opens silver locks', sprite: 'SPR_STAT_21', category: 'key' },
  { code: 45, name: 'Bed', kind: 'block', sprite: 'SPR_STAT_22', category: 'decoration' },
  { code: 46, name: 'Basket', kind: 'dressing', sprite: 'SPR_STAT_23', category: 'decoration' },
  { code: 47, name: 'Chicken dinner', kind: 'bonus', bonus: 'bo_food', effect: '+10 health', sprite: 'SPR_STAT_24', category: 'health' },
  { code: 48, name: 'First aid kit', kind: 'bonus', bonus: 'bo_firstaid', effect: '+25 health', sprite: 'SPR_STAT_25', category: 'health' },
  { code: 49, name: 'Ammo clip', kind: 'bonus', bonus: 'bo_clip', effect: '+8 rounds', sprite: 'SPR_STAT_26', category: 'ammo' },
  { code: 50, name: 'Machine gun', kind: 'bonus', bonus: 'bo_machinegun', effect: 'weapon +6 rounds', sprite: 'SPR_STAT_27', category: 'weapon' },
  { code: 51, name: 'Chain gun', kind: 'bonus', bonus: 'bo_chaingun', effect: 'weapon +6 rounds', sprite: 'SPR_STAT_28', category: 'weapon' },
  { code: 52, name: 'Cross', kind: 'bonus', bonus: 'bo_cross', points: 100, treasure: true, sprite: 'SPR_STAT_29', category: 'treasure' },
  { code: 53, name: 'Chalice', kind: 'bonus', bonus: 'bo_chalice', points: 500, treasure: true, sprite: 'SPR_STAT_30', category: 'treasure' },
  { code: 54, name: 'Jeweled chest', kind: 'bonus', bonus: 'bo_bible', points: 1000, treasure: true, sprite: 'SPR_STAT_31', category: 'treasure' },
  { code: 55, name: 'Crown', kind: 'bonus', bonus: 'bo_crown', points: 5000, treasure: true, sprite: 'SPR_STAT_32', category: 'treasure' },
  { code: 56, name: 'Extra life', kind: 'bonus', bonus: 'bo_fullheal', effect: 'full health, +25 rounds, +1 life', treasure: true, sprite: 'SPR_STAT_33', category: 'treasure' },
  { code: 57, name: 'Pool of gibs', kind: 'bonus', bonus: 'bo_gibs', effect: '+1 health when low', sprite: 'SPR_STAT_34', category: 'health' },
  { code: 58, name: 'Brown barrel', kind: 'block', sprite: 'SPR_STAT_35', category: 'decoration' },
  { code: 59, name: 'Water well', kind: 'block', sprite: 'SPR_STAT_36', category: 'decoration' },
  { code: 60, name: 'Empty well', kind: 'block', sprite: 'SPR_STAT_37', category: 'decoration' },
  { code: 61, name: 'Pool of gibs 2', kind: 'bonus', bonus: 'bo_gibs', effect: '+1 health when low', sprite: 'SPR_STAT_38', category: 'health' },
  { code: 62, name: 'Flag', kind: 'block', sprite: 'SPR_STAT_39', category: 'decoration' },
  { code: 63, name: 'Call Apogee sign', kind: 'block', sprite: 'SPR_STAT_40', category: 'decoration' },
  { code: 64, name: 'Junk (bones 1)', kind: 'dressing', sprite: 'SPR_STAT_41', category: 'decoration' },
  { code: 65, name: 'Junk (bones 2)', kind: 'dressing', sprite: 'SPR_STAT_42', category: 'decoration' },
  { code: 66, name: 'Junk (bones 3)', kind: 'dressing', sprite: 'SPR_STAT_43', category: 'decoration' },
  { code: 67, name: 'Pots', kind: 'dressing', sprite: 'SPR_STAT_44', category: 'decoration' },
  { code: 68, name: 'Stove', kind: 'block', sprite: 'SPR_STAT_45', category: 'decoration' },
  { code: 69, name: 'Spear rack', kind: 'block', sprite: 'SPR_STAT_46', category: 'decoration' },
  { code: 70, name: 'Vines', kind: 'dressing', sprite: 'SPR_STAT_47', category: 'decoration' },
  { code: 71, name: 'Partial clip', kind: 'bonus', bonus: 'bo_clip2', effect: '+4 rounds', sprite: 'SPR_STAT_26', category: 'ammo' },
];

// Player starts: SpawnPlayer(x, y, NORTH + code - 19).
export const PLAYER_START_FIRST = 19;
export const PLAYER_START_LAST = 22;
/** Facing order for player starts (codes 19..22). */
export const PLAYER_START_FACINGS = ['North', 'East', 'South', 'West'];

// Patrol turning points (ICONARROWS 90): direction per code 90..97.
export const TURN_FIRST = 90;
export const TURN_LAST = 97;
export const TURN_DIRECTIONS = ['E', 'NE', 'N', 'NW', 'W', 'SW', 'S', 'SE'];

export const PUSHWALL = 98; // PUSHABLETILE: secret pushwall marker
export const EXIT_TRIGGER = 99; // EXITTILE: victory-walk trigger
export const DEAD_GUARD = 124;

/**
 * Enemy spawn-code matrix. Facing order is East, North, West, South
 * (SpawnStand/SpawnPatrol dir = code - base). A code spawns at its listed
 * skill and above.
 * @typedef {Object} EnemyType
 * @property {string} id
 * @property {string} name
 * @property {number} standBase easy standing base code (facing E)
 * @property {number} patrolBase easy patrolling base code (facing E)
 * @property {number} mediumDelta added for medium skill
 * @property {number} hardDelta added for hard skill
 * @property {string} sprite SPR_* icon (standing, facing viewer)
 * @property {number} hp hit points (gd_medium column of starthitpoints)
 */

/** @type {EnemyType[]} */
export const ENEMIES = [
  { id: 'guard', name: 'Guard', standBase: 108, patrolBase: 112, mediumDelta: 36, hardDelta: 72, sprite: 'SPR_GRD_S_1', hp: 25 },
  { id: 'officer', name: 'Officer', standBase: 116, patrolBase: 120, mediumDelta: 36, hardDelta: 72, sprite: 'SPR_OFC_S_1', hp: 50 },
  { id: 'ss', name: 'SS', standBase: 126, patrolBase: 130, mediumDelta: 36, hardDelta: 72, sprite: 'SPR_SS_S_1', hp: 100 },
  { id: 'dog', name: 'Dog', standBase: 134, patrolBase: 138, mediumDelta: 36, hardDelta: 72, sprite: 'SPR_DOG_W1_1', hp: 1 },
  { id: 'mutant', name: 'Mutant', standBase: 216, patrolBase: 220, mediumDelta: 18, hardDelta: 36, sprite: 'SPR_MUT_S_1', hp: 55 },
];

export const ENEMY_FACINGS = ['E', 'N', 'W', 'S'];
export const SKILLS = ['easy', 'medium', 'hard'];

/**
 * Compute the plane-1 code for an enemy placement.
 * @param {EnemyType} type
 * @param {'stand'|'patrol'} mode
 * @param {0|1|2|3} facing index into ENEMY_FACINGS
 * @param {'easy'|'medium'|'hard'} skill
 * @returns {number}
 */
export function enemyCode(type, mode, facing, skill) {
  const base = mode === 'stand' ? type.standBase : type.patrolBase;
  const delta = skill === 'easy' ? 0 : skill === 'medium' ? type.mediumDelta : type.hardDelta;
  return base + delta + facing;
}

/**
 * @typedef {Object} BossInfo
 * @property {number} code
 * @property {string} name
 * @property {string} sprite
 * @property {number} actorCost slots consumed against MAXACTORS
 * @property {boolean} deathEndsLevel triggers level end / deathcam
 */

/** Wolf3D bosses (no skill/facing variants). @type {BossInfo[]} */
export const BOSSES = [
  { code: 160, name: 'Fake Hitler', sprite: 'SPR_FAKE_W1', actorCost: 1, deathEndsLevel: false },
  { code: 178, name: 'Adolf Hitler', sprite: 'SPR_MECHA_W1', actorCost: 2, deathEndsLevel: true },
  { code: 179, name: 'General Fettgesicht', sprite: 'SPR_FAT_W1', actorCost: 1, deathEndsLevel: true },
  { code: 196, name: 'Dr. Schabbs', sprite: 'SPR_SCHABB_W1', actorCost: 1, deathEndsLevel: true },
  { code: 197, name: 'Gretel Grösse', sprite: 'SPR_GRETEL_W1', actorCost: 1, deathEndsLevel: false },
  { code: 214, name: 'Hans Grösse', sprite: 'SPR_BOSS_W1', actorCost: 1, deathEndsLevel: false },
  { code: 215, name: 'Otto Giftmacher', sprite: 'SPR_GIFT_W1', actorCost: 1, deathEndsLevel: true },
];

/** Pac-Man ghosts (E3 secret level). @type {BossInfo[]} */
export const GHOSTS = [
  { code: 224, name: 'Blinky', sprite: 'SPR_BLINKY_W1', actorCost: 1, deathEndsLevel: false },
  { code: 225, name: 'Clyde', sprite: 'SPR_CLYDE_W1', actorCost: 1, deathEndsLevel: false },
  { code: 226, name: 'Pinky', sprite: 'SPR_PINKY_W1', actorCost: 1, deathEndsLevel: false },
  { code: 227, name: 'Inky', sprite: 'SPR_INKY_W1', actorCost: 1, deathEndsLevel: false },
];

/**
 * Decode a plane-1 value into a structured description.
 * @param {number} code
 * @returns {{kind:string, label:string, sprite?:string, facing?:string, skill?:string, mode?:string}|null}
 */
export function describeObjectCode(code) {
  if (code === 0) return null;
  if (code >= PLAYER_START_FIRST && code <= PLAYER_START_LAST) {
    return { kind: 'player', label: `Player start (facing ${PLAYER_START_FACINGS[code - 19]})` };
  }
  const stat = STATICS.find((s) => s.code === code);
  if (stat) return { kind: 'static', label: stat.name, sprite: stat.sprite };
  if (code >= TURN_FIRST && code <= TURN_LAST) {
    return { kind: 'turn', label: `Turn point ${TURN_DIRECTIONS[code - TURN_FIRST]}`, facing: TURN_DIRECTIONS[code - TURN_FIRST] };
  }
  if (code === PUSHWALL) return { kind: 'pushwall', label: 'Pushwall (secret)' };
  if (code === EXIT_TRIGGER) return { kind: 'exit', label: 'Victory-walk trigger' };
  if (code === DEAD_GUARD) return { kind: 'deadguard', label: 'Dead guard', sprite: 'SPR_GRD_DEAD' };
  for (const e of ENEMIES) {
    for (const skill of /** @type {('easy'|'medium'|'hard')[]} */ (SKILLS)) {
      for (const mode of /** @type {('stand'|'patrol')[]} */ (['stand', 'patrol'])) {
        for (let f = 0; f < 4; f++) {
          if (enemyCode(e, mode, /** @type {0|1|2|3} */ (f), skill) === code) {
            return {
              kind: 'enemy',
              label: `${e.name}, ${mode === 'stand' ? 'standing' : 'patrolling'} ${ENEMY_FACINGS[f]}, ${skill}+`,
              sprite: e.sprite,
              facing: ENEMY_FACINGS[f],
              skill,
              mode,
            };
          }
        }
      }
    }
  }
  const boss = [...BOSSES, ...GHOSTS].find((b) => b.code === code);
  if (boss) return { kind: 'boss', label: boss.name, sprite: boss.sprite };
  return { kind: 'unknown', label: `Unknown object (${code})` };
}
