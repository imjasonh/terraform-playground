// Per-level metadata that lives in the EXE, surfaced read-only.
// Ceiling colors: vgaCeiling[] in WL_PLAY.C; par times: parTimes[] in WL_INTER.C.

/** WL6 ceiling color palette index per level slot (60 levels). */
export const WL6_CEILING = [
  0x1d, 0x1d, 0x1d, 0x1d, 0x1d, 0x1d, 0x1d, 0x1d, 0x1d, 0xbf,
  0x4e, 0x4e, 0x4e, 0x1d, 0x8d, 0x4e, 0x1d, 0x2d, 0x1d, 0x8d,
  0x1d, 0x1d, 0x1d, 0x1d, 0x1d, 0x2d, 0xdd, 0x1d, 0x1d, 0x98,
  0x1d, 0x9d, 0x2d, 0xdd, 0xdd, 0x9d, 0x2d, 0x4d, 0x1d, 0xdd,
  0x7d, 0x1d, 0x2d, 0x2d, 0xdd, 0xd7, 0x1d, 0x1d, 0x1d, 0x2d,
  0x1d, 0x1d, 0x1d, 0x1d, 0xdd, 0xdd, 0x7d, 0xdd, 0xdd, 0xdd,
];

/** WL1 (shareware episode 1) ceiling colors: first 10 entries of WL6 table. */
export const WL1_CEILING = WL6_CEILING.slice(0, 10);

/** Default floor color used by the renderer (the engine clears to 0x19). */
export const FLOOR_COLOR = 0x19;

/** Secret-level return floors per episode (ElevatorBackTo[] in WL_GAME.C). */
export const ELEVATOR_BACK_TO = [1, 1, 7, 3, 5, 3];

/**
 * Describe a level slot for WL1/WL3/WL6 numbering: floors 1-8, boss 9, secret 10.
 * @param {number} slot 0-based level slot
 * @returns {string}
 */
export function levelLabel(slot) {
  const episode = Math.floor(slot / 10) + 1;
  const floor = (slot % 10) + 1;
  const suffix = floor === 9 ? ' (boss)' : floor === 10 ? ' (secret)' : '';
  return `E${episode}L${floor}${suffix}`;
}
