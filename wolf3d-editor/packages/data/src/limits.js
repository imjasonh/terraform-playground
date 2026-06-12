// Vanilla DOS engine limits (WL_DEF.H) used by the stats panel and validator.
export const LIMITS = {
  maxActors: 149, // MAXACTORS 150 including the player
  maxStatics: 399, // MAXSTATS 400
  maxDoors: 64, // MAXDOORS; engine Quit()s on the 65th
  maxWallTiles: 64, // MAXWALLTILES
  mapSize: 64,
  visibleSpriteGuideline: 56,
};

// Gameplay economy for the stats dashboard (WL_AGENT.C GetBonus).
export const ECONOMY = {
  clipRounds: 8,
  droppedClipRounds: 4,
  weaponPickupRounds: 6,
  maxAmmo: 99,
  maxHealth: 100,
  maxLives: 9,
  extraLifeEveryPoints: 40000,
};
