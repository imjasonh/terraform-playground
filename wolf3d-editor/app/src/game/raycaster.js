// Software raycaster for the 3D preview: classic DDA over the 64x64 tile
// grid, light/dark wall pairs per face, sliding doors with jamb faces,
// billboard sprites, solid floor/ceiling colors — the same rendering model
// as WL_DRAW.C.

import { MAP_WIDTH, WOLF_PALETTE, WALL_SIZE } from '@wolf3d/codec';
import { isDoor, isFloorCode, AMBUSH_TILE, DOORS, STATICS, DEAD_GUARD, BOSSES, GHOSTS } from '@wolf3d/data';
import { spriteIndex, ENEMIES } from '@wolf3d/data';
import { objectInfo } from './objectinfo.js';

const FOV = Math.PI / 3; // ~60 degrees, close to the original's 64-degree fov

/** Is this plane-0 value a wall the ray should hit? */
function isSolid(code) {
  return code >= 1 && code < 106 && !isDoor(code) ? true : isDoor(code);
}

export class Raycaster {
  /**
   * @param {import('@wolf3d/codec').LevelData} level
   * @param {import('./assets.js').AssetCache} assets
   * @param {number} ceilingColor palette index
   * @param {number} floorColor palette index
   */
  constructor(level, assets, ceilingColor, floorColor) {
    this.level = level;
    this.assets = assets;
    this.ceiling = ceilingColor;
    this.floor = floorColor;
    this.width = 320;
    this.height = 200;
    this.zbuffer = new Float32Array(this.width);
    /** Door states, keyed by tile index.
     * @type {Map<number, {vertical: boolean, openness: number, state: 'closed'|'opening'|'open'|'closing', timer: number}>} */
    this.doors = new Map();
    for (let i = 0; i < level.plane0.length; i++) {
      const t = level.plane0[i];
      if (isDoor(t)) {
        // Even codes (90, 92, ...) are "vertical" doors: the slab runs N-S
        // (plane x = tile + 0.5); odd codes run E-W (plane y = tile + 0.5).
        this.doors.set(i, { vertical: t % 2 === 0, openness: 0, state: 'closed', timer: 0 });
      }
    }
    /** @type {string|null} transient HUD message (e.g. door feedback) */
    this.message = null;
    this.messageTimer = 0;
    // Find player start.
    this.x = 32.5;
    this.y = 32.5;
    this.angle = 0;
    for (let i = 0; i < level.plane1.length; i++) {
      const v = level.plane1[i];
      if (v >= 19 && v <= 22) {
        this.x = (i % MAP_WIDTH) + 0.5;
        this.y = Math.floor(i / MAP_WIDTH) + 0.5;
        // 19=N 20=E 21=S 22=W; screen-space: angle 0 = +x (east), y grows south.
        this.angle = [-Math.PI / 2, 0, Math.PI / 2, Math.PI][v - 19];
        break;
      }
    }
    this.collectSprites();
  }

  collectSprites() {
    /** @type {{x:number, y:number, sprite:number}[]} */
    this.sprites = [];
    const { level } = this;
    for (let i = 0; i < level.plane1.length; i++) {
      const code = level.plane1[i];
      if (code === 0) continue;
      const info = objectInfo(code);
      if (!info) continue;
      let sprName = null;
      if (info.kind === 'static' || info.kind === 'enemy' || info.kind === 'boss' || info.kind === 'deadguard') {
        sprName = info.sprite ?? null;
      }
      if (!sprName) continue;
      const idx = spriteIndex(sprName);
      if (idx < 0) continue;
      this.sprites.push({ x: (i % MAP_WIDTH) + 0.5, y: Math.floor(i / MAP_WIDTH) + 0.5, sprite: idx });
    }
  }

  /** @param {number} x @param {number} y */
  tileAt(x, y) {
    if (x < 0 || y < 0 || x >= MAP_WIDTH || y >= MAP_WIDTH) return 1;
    return this.level.plane0[Math.floor(y) * MAP_WIDTH + Math.floor(x)];
  }

  /** Can the player occupy (x, y)? */
  walkable(x, y) {
    const t = this.tileAt(x, y);
    if (isDoor(t)) {
      const door = this.doors.get(Math.floor(y) * MAP_WIDTH + Math.floor(x));
      return !!door && door.openness > 0.85;
    }
    if (isSolid(t)) return false;
    // Blocking statics.
    const o = this.level.plane1[Math.floor(y) * MAP_WIDTH + Math.floor(x)];
    const st = STATICS.find((s) => s.code === o);
    if (st && st.kind === 'block') return false;
    return true;
  }

  /**
   * Use key (Space): operate the door directly ahead, like the engine's
   * Cmd_Use. Locked doors open too — the preview has no inventory.
   */
  use() {
    const cos = Math.cos(this.angle);
    const sin = Math.sin(this.angle);
    // Check the facing-adjacent tile (dominant axis, like CheckAction).
    const tx = Math.floor(this.x) + (Math.abs(cos) > Math.abs(sin) ? Math.sign(cos) : 0);
    const ty = Math.floor(this.y) + (Math.abs(cos) > Math.abs(sin) ? 0 : Math.sign(sin));
    const idx = ty * MAP_WIDTH + tx;
    const door = this.doors.get(idx);
    if (!door) return;
    if (door.state === 'closed' || door.state === 'closing') {
      door.state = 'opening';
      const code = this.level.plane0[idx];
      const lock = code >= 92 && code <= 99 ? 'locked (opens freely in preview)' : null;
      this.setMessage(lock ?? 'door opened');
    } else {
      // Don't slam a door on the player.
      if (Math.floor(this.x) !== tx || Math.floor(this.y) !== ty) door.state = 'closing';
    }
  }

  /** @param {string} text */
  setMessage(text) {
    this.message = text;
    this.messageTimer = 2;
  }

  /**
   * Advance door animations and timers.
   * @param {number} dt seconds
   */
  update(dt) {
    const DOOR_SPEED = 1 / 0.85; // fully open in ~0.85s, close to the original
    for (const [idx, door] of this.doors) {
      if (door.state === 'opening') {
        door.openness = Math.min(1, door.openness + dt * DOOR_SPEED);
        if (door.openness === 1) {
          door.state = 'open';
          door.timer = 0;
        }
      } else if (door.state === 'open') {
        door.timer += dt;
        const playerInside = Math.floor(this.x) + Math.floor(this.y) * MAP_WIDTH === idx;
        if (door.timer > 4 && !playerInside) door.state = 'closing';
      } else if (door.state === 'closing') {
        door.openness = Math.max(0, door.openness - dt * DOOR_SPEED);
        if (door.openness === 0) door.state = 'closed';
      }
    }
    if (this.messageTimer > 0) {
      this.messageTimer -= dt;
      if (this.messageTimer <= 0) this.message = null;
    }
  }

  /**
   * Move with simple wall sliding.
   * @param {number} dx @param {number} dy
   */
  move(dx, dy) {
    const r = 0.2;
    const nx = this.x + dx;
    const ny = this.y + dy;
    if (this.walkable(nx + Math.sign(dx) * r, this.y)) this.x = nx;
    if (this.walkable(this.x, ny + Math.sign(dy) * r)) this.y = ny;
  }

  /**
   * Render one frame into an ImageData buffer.
   * @param {ImageData} img 320x200
   */
  render(img) {
    const { width, height } = this;
    const data = img.data;
    const half = height / 2;

    // Ceiling / floor.
    const cr = WOLF_PALETTE[this.ceiling * 3];
    const cg = WOLF_PALETTE[this.ceiling * 3 + 1];
    const cb = WOLF_PALETTE[this.ceiling * 3 + 2];
    const fr = WOLF_PALETTE[this.floor * 3];
    const fg = WOLF_PALETTE[this.floor * 3 + 1];
    const fb = WOLF_PALETTE[this.floor * 3 + 2];
    for (let y = 0; y < height; y++) {
      const top = y < half;
      for (let x = 0; x < width; x++) {
        const o = (y * width + x) * 4;
        data[o] = top ? cr : fr;
        data[o + 1] = top ? cg : fg;
        data[o + 2] = top ? cb : fb;
        data[o + 3] = 255;
      }
    }

    // Walls.
    for (let col = 0; col < width; col++) {
      const rayAngle = this.angle + Math.atan(((col / width) * 2 - 1) * Math.tan(FOV / 2));
      const cos = Math.cos(rayAngle);
      const sin = Math.sin(rayAngle);

      let mapX = Math.floor(this.x);
      let mapY = Math.floor(this.y);
      const deltaX = Math.abs(1 / (cos || 1e-9));
      const deltaY = Math.abs(1 / (sin || 1e-9));
      const stepX = cos < 0 ? -1 : 1;
      const stepY = sin < 0 ? -1 : 1;
      let sideX = cos < 0 ? (this.x - mapX) * deltaX : (mapX + 1 - this.x) * deltaX;
      let sideY = sin < 0 ? (this.y - mapY) * deltaY : (mapY + 1 - this.y) * deltaY;

      let hit = 0;
      let side = 0; // 0 = x-side (E/W face -> dark), 1 = y-side (N/S face -> light)
      let guard = 0;
      let rawDist = 0; // unprojected distance along the ray to the hit
      let texX = 0;
      let texPixels = null;
      let cameFromDoor = false;

      while (!hit && guard++ < 256) {
        if (sideX < sideY) {
          sideX += deltaX;
          mapX += stepX;
          side = 0;
        } else {
          sideY += deltaY;
          mapY += stepY;
          side = 1;
        }
        const t = this.tileAt(mapX, mapY);

        if (isDoor(t)) {
          // Recessed sliding slab on the tile's center line.
          const door = this.doors.get(mapY * MAP_WIDTH + mapX);
          if (!door) continue;
          let planeDist;
          let frac; // position along the slab, 0..1
          if (door.vertical) {
            planeDist = (mapX + 0.5 - this.x) / (cos || 1e-9);
            const hitY = this.y + planeDist * sin;
            if (Math.floor(hitY) !== mapY) {
              cameFromDoor = true;
              continue; // ray slips past the slab into the jamb gap
            }
            frac = hitY - mapY;
          } else {
            planeDist = (mapY + 0.5 - this.y) / (sin || 1e-9);
            const hitX = this.x + planeDist * cos;
            if (Math.floor(hitX) !== mapX) {
              cameFromDoor = true;
              continue;
            }
            frac = hitX - mapX;
          }
          if (planeDist <= 0) {
            cameFromDoor = true;
            continue;
          }
          // The door slides sideways into the jamb; only frac >= openness
          // is still covered by the slab.
          if (frac < door.openness) {
            cameFromDoor = true;
            continue;
          }
          hit = t;
          rawDist = planeDist;
          const doorInfo = DOORS.find((d) => d.code === t);
          const kind = doorInfo && doorInfo.lock === 5 ? 2 : doorInfo && doorInfo.lock > 0 ? 3 : 0;
          // Door faces use the light/dark pair to match the slab orientation.
          texPixels = this.assets.wallPixelsAt(this.assets.doorWallBase + kind * 2 + (door.vertical ? 1 : 0));
          texX = Math.min(WALL_SIZE - 1, Math.floor((frac - door.openness) * WALL_SIZE));
          break;
        }

        if (t >= 1 && t < 106 && !isFloorCode(t) && t !== AMBUSH_TILE) {
          hit = t;
          rawDist = side === 0 ? sideX - deltaX : sideY - deltaY;
          // Walls adjacent to a door tile show the jamb texture on the face
          // inside the doorway, like the engine's DOORWALL handling.
          if (cameFromDoor) {
            texPixels = this.assets.wallPixelsAt(this.assets.doorWallBase + 2 + (side === 0 ? 1 : 0));
          } else {
            texPixels = this.assets.wallPixelsAt((t - 1) * 2 + (side === 0 ? 1 : 0));
          }
          let wallX = side === 0 ? this.y + rawDist * sin : this.x + rawDist * cos;
          wallX -= Math.floor(wallX);
          texX = Math.floor(wallX * WALL_SIZE);
          if ((side === 0 && cos > 0) || (side === 1 && sin < 0)) texX = WALL_SIZE - texX - 1;
          break;
        }

        cameFromDoor = false;
      }
      if (!hit) {
        this.zbuffer[col] = Infinity;
        continue;
      }

      const dist = Math.max(0.01, rawDist * Math.cos(rayAngle - this.angle));
      this.zbuffer[col] = dist;
      const wallHeight = height / dist;
      const wallTop = half - wallHeight / 2; // exact, unrounded
      const drawStart = Math.max(0, Math.ceil(wallTop));
      const drawEnd = Math.min(height - 1, Math.floor(half + wallHeight / 2));

      for (let y = drawStart; y <= drawEnd; y++) {
        // Clamp instead of wrapping: rounding at the slice edges must not
        // sample the opposite edge of the texture (caused dashed-line
        // artifacts along the tops of walls).
        const texY = Math.min(WALL_SIZE - 1, Math.max(0, Math.floor(((y + 0.5 - wallTop) / wallHeight) * WALL_SIZE)));
        let r = 90;
        let g = 90;
        let b = 90;
        if (texPixels) {
          const p = texPixels[texY * WALL_SIZE + texX];
          r = WOLF_PALETTE[p * 3];
          g = WOLF_PALETTE[p * 3 + 1];
          b = WOLF_PALETTE[p * 3 + 2];
        } else if (side === 0) {
          r = g = b = 70;
        }
        const o = (y * width + col) * 4;
        data[o] = r;
        data[o + 1] = g;
        data[o + 2] = b;
      }
    }

    // Sprites, far to near.
    const order = this.sprites
      .map((s) => ({ s, d: (s.x - this.x) ** 2 + (s.y - this.y) ** 2 }))
      .sort((a, b) => b.d - a.d);
    const cosA = Math.cos(this.angle);
    const sinA = Math.sin(this.angle);
    for (const { s } of order) {
      const dx = s.x - this.x;
      const dy = s.y - this.y;
      // Camera space.
      const depth = dx * cosA + dy * sinA;
      if (depth < 0.2) continue;
      const lateral = -dx * sinA + dy * cosA;
      const screenX = Math.floor((width / 2) * (1 + lateral / (depth * Math.tan(FOV / 2))));
      const size = Math.min(height * 6, Math.floor(height / depth));
      const px = this.assets.spritePixelsAt(s.sprite);
      if (!px) continue;
      const top = Math.floor(half - size / 2);
      for (let sx = 0; sx < size; sx++) {
        const col = screenX - size / 2 + sx;
        if (col < 0 || col >= width) continue;
        if (this.zbuffer[col] < depth) continue;
        const texX = Math.floor((sx / size) * WALL_SIZE);
        for (let sy = 0; sy < size; sy++) {
          const row = top + sy;
          if (row < 0 || row >= height) continue;
          const texY = Math.floor((sy / size) * WALL_SIZE);
          const p = px[texY * WALL_SIZE + texX];
          if (p === 255) continue;
          const o = (row * width + col) * 4;
          data[o] = WOLF_PALETTE[p * 3];
          data[o + 1] = WOLF_PALETTE[p * 3 + 1];
          data[o + 2] = WOLF_PALETTE[p * 3 + 2];
        }
      }
    }
  }
}
