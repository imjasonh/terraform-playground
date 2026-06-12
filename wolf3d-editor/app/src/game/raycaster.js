// Software raycaster for the 3D preview: classic DDA over the 64x64 tile
// grid, light/dark wall pairs per face, billboard sprites, solid
// floor/ceiling colors — the same rendering model as WL_DRAW.C.

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
    if (isSolid(t) && !isDoor(t)) return false;
    if (isDoor(t)) return false; // doors stay closed in the preview; use the playtest for real play
    // Blocking statics.
    const o = this.level.plane1[Math.floor(y) * MAP_WIDTH + Math.floor(x)];
    const st = STATICS.find((s) => s.code === o);
    if (st && st.kind === 'block') return false;
    return true;
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
        if ((t >= 1 && t < 106 && !isFloorCode(t) && t !== AMBUSH_TILE) || isDoor(t)) hit = t;
      }
      if (!hit) {
        this.zbuffer[col] = Infinity;
        continue;
      }

      const dist = Math.max(
        0.01,
        (side === 0 ? sideX - deltaX : sideY - deltaY) * Math.cos(rayAngle - this.angle),
      );
      this.zbuffer[col] = dist;
      const wallHeight = Math.min(height * 8, Math.floor(height / dist));
      const drawStart = Math.max(0, Math.floor(half - wallHeight / 2));
      const drawEnd = Math.min(height - 1, Math.floor(half + wallHeight / 2));

      // Texture pick: light face for N/S (side 1), dark for E/W (side 0).
      let texPixels = null;
      if (isDoor(hit)) {
        const door = DOORS.find((d) => d.code === hit);
        const kind = door && door.lock === 5 ? 2 : door && door.lock > 0 ? 3 : 0;
        texPixels = this.assets.wallPixelsAt(this.assets.doorWallBase + kind * 2 + (side === 0 ? 1 : 0));
      } else {
        texPixels = this.assets.wallPixelsAt((hit - 1) * 2 + (side === 0 ? 1 : 0));
      }

      let wallX = side === 0 ? this.y + dist * sin : this.x + dist * cos;
      wallX -= Math.floor(wallX);
      let texX = Math.floor(wallX * WALL_SIZE);
      if ((side === 0 && cos > 0) || (side === 1 && sin < 0)) texX = WALL_SIZE - texX - 1;

      for (let y = drawStart; y <= drawEnd; y++) {
        const texY = Math.floor(((y - (half - wallHeight / 2)) / wallHeight) * WALL_SIZE) & 63;
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
