import { describe, it, expect } from 'vitest';
import {
  WALL_NAMES,
  DOORS,
  STATICS,
  ENEMIES,
  enemyCode,
  describeObjectCode,
  describeWallCode,
  SPRITE_NAMES,
  spriteIndex,
  AMBUSH_TILE,
  AREA_TILE,
} from '../src/index.js';

describe('wall catalog', () => {
  it('has 49 vanilla wall names', () => {
    expect(WALL_NAMES).toHaveLength(49);
  });
  it('door codes are 90..101 alternating orientation', () => {
    expect(DOORS.map((d) => d.code)).toEqual([90, 91, 92, 93, 94, 95, 96, 97, 98, 99, 100, 101]);
    for (const d of DOORS) {
      expect(d.orientation).toBe(d.code % 2 === 0 ? 'vertical' : 'horizontal');
    }
  });
  it('describes special tiles', () => {
    expect(describeWallCode(AMBUSH_TILE)).toContain('Deaf');
    expect(describeWallCode(AREA_TILE)).toContain('secret elevator');
    expect(describeWallCode(21)).toBe('Elevator');
  });
});

describe('object catalog', () => {
  it('statics cover codes 23..71 contiguously', () => {
    expect(STATICS.map((s) => s.code)).toEqual(Array.from({ length: 49 }, (_, i) => 23 + i));
  });
  it('static sprites map to SPR_STAT_n = sprite 2+n', () => {
    // From WL_DEF.H: SPR_STAT_0 is sprite 2.
    expect(spriteIndex('SPR_STAT_0')).toBe(2);
    expect(spriteIndex('SPR_GRD_S_1')).toBe(50);
    expect(spriteIndex('SPR_DOG_W1_1')).toBe(99);
    expect(spriteIndex('SPR_SS_S_1')).toBe(138);
    expect(spriteIndex('SPR_MUT_S_1')).toBe(187);
    expect(spriteIndex('SPR_OFC_S_1')).toBe(238);
  });

  it('enemy code matrix matches ScanInfoPlane ranges', () => {
    const guard = /** @type {import('../src/objects.js').EnemyType} */ (ENEMIES.find((e) => e.id === 'guard'));
    expect(enemyCode(guard, 'stand', 0, 'easy')).toBe(108);
    expect(enemyCode(guard, 'stand', 3, 'easy')).toBe(111);
    expect(enemyCode(guard, 'patrol', 0, 'easy')).toBe(112);
    expect(enemyCode(guard, 'stand', 0, 'medium')).toBe(144);
    expect(enemyCode(guard, 'stand', 0, 'hard')).toBe(180);

    const ss = /** @type {import('../src/objects.js').EnemyType} */ (ENEMIES.find((e) => e.id === 'ss'));
    expect(enemyCode(ss, 'stand', 0, 'easy')).toBe(126);
    expect(enemyCode(ss, 'patrol', 0, 'hard')).toBe(202);

    // Mutants use +18/+36 (ScanInfoPlane: 216,238 ranges with smaller deltas).
    const mut = /** @type {import('../src/objects.js').EnemyType} */ (ENEMIES.find((e) => e.id === 'mutant'));
    expect(enemyCode(mut, 'stand', 0, 'easy')).toBe(216);
    expect(enemyCode(mut, 'stand', 0, 'medium')).toBe(234);
    expect(enemyCode(mut, 'stand', 0, 'hard')).toBe(252);
  });

  it('describes well-known object codes', () => {
    expect(describeObjectCode(19)?.kind).toBe('player');
    expect(describeObjectCode(98)?.kind).toBe('pushwall');
    expect(describeObjectCode(99)?.kind).toBe('exit');
    expect(describeObjectCode(124)?.kind).toBe('deadguard');
    expect(describeObjectCode(43)?.label).toBe('Gold key');
    expect(describeObjectCode(180)?.label).toContain('Guard');
    expect(describeObjectCode(180)?.label).toContain('hard');
    expect(describeObjectCode(214)?.label).toBe('Hans Grösse');
  });

  it('sprite manifest has the full non-SPEAR enum', () => {
    expect(SPRITE_NAMES).toHaveLength(435);
    expect(SPRITE_NAMES[0]).toBe('SPR_DEMO');
    expect(SPRITE_NAMES[434]).toBe('SPR_CHAINATK4');
  });
});
