import React, { useEffect, useRef, useCallback } from 'react';
import { MAP_WIDTH } from '@wolf3d/codec';
import {
  isDoor,
  isFloorCode,
  DOORS,
  AMBUSH_TILE,
  PLAYER_START_FIRST,
  PLAYER_START_LAST,
  TURN_FIRST,
  TURN_LAST,
  TURN_DIRECTIONS,
  PUSHWALL,
  EXIT_TRIGGER,
} from '@wolf3d/data';
import { store, currentLevel, beginGesture, endGesture, setTile, updateUi } from '../store.js';
import { useStoreVersion } from '../hooks.js';
import { floorCodeColor, paletteColor } from '../game/assets.js';
import { objectInfo, spawnsAtSkill, enemyMinSkill } from '../game/objectinfo.js';

const SKILL_COLOR = { easy: '#e8e8e8', medium: '#ffd24a', hard: '#ff5a5a' };
const DIR_ANGLE = { E: 0, NE: -45, N: -90, NW: -135, W: 180, SW: 135, S: 90, SE: 45 };

/** Draw a small direction arrow centered in the tile. */
function drawArrow(ctx, px, py, size, dir, color) {
  const angle = ((DIR_ANGLE[dir] ?? 0) * Math.PI) / 180;
  ctx.save();
  ctx.translate(px + size / 2, py + size / 2);
  ctx.rotate(angle);
  ctx.strokeStyle = color;
  ctx.fillStyle = color;
  ctx.lineWidth = Math.max(1, size / 12);
  const r = size * 0.3;
  ctx.beginPath();
  ctx.moveTo(-r, 0);
  ctx.lineTo(r * 0.4, 0);
  ctx.stroke();
  ctx.beginPath();
  ctx.moveTo(r, 0);
  ctx.lineTo(r * 0.1, -r * 0.5);
  ctx.lineTo(r * 0.1, r * 0.5);
  ctx.closePath();
  ctx.fill();
  ctx.restore();
}

export function MapCanvas() {
  useStoreVersion();
  const canvasRef = useRef(null);
  const paintingRef = useRef(/** @type {null | {button: number, plane: number, code: number}} */ (null));

  const draw = useCallback(() => {
    const canvas = canvasRef.current;
    const lvl = currentLevel();
    const assets = store.assets;
    if (!canvas || !lvl || !assets) return;
    const { zoom, showObjects, showFloors, showGrid, skillFilter, hover } = store.ui;
    const size = MAP_WIDTH * zoom;
    if (canvas.width !== size) {
      canvas.width = size;
      canvas.height = size;
    }
    const ctx = canvas.getContext('2d');
    ctx.imageSmoothingEnabled = false;
    ctx.fillStyle = '#101016';
    ctx.fillRect(0, 0, size, size);

    for (let y = 0; y < MAP_WIDTH; y++) {
      for (let x = 0; x < MAP_WIDTH; x++) {
        const code = lvl.plane0[y * MAP_WIDTH + x];
        const px = x * zoom;
        const py = y * zoom;
        if (code === 0) {
          // No floor code: engine-invalid walkable space. Show as black hole.
          ctx.fillStyle = '#000';
          ctx.fillRect(px, py, zoom, zoom);
        } else if (isDoor(code)) {
          const door = DOORS.find((d) => d.code === code);
          const kind = door.lock === 5 ? 2 : door.lock > 0 ? 3 : 0;
          const tex = assets.doorCanvas(kind);
          ctx.fillStyle = '#222';
          ctx.fillRect(px, py, zoom, zoom);
          if (tex) {
            if (door.orientation === 'vertical') {
              ctx.drawImage(tex, px + zoom * 0.33, py, zoom * 0.34, zoom);
            } else {
              ctx.drawImage(tex, px, py + zoom * 0.33, zoom, zoom * 0.34);
            }
          }
          if (door.lock === 1 || door.lock === 2) {
            ctx.fillStyle = door.lock === 1 ? '#ffd24a' : '#c0c0d0';
            ctx.fillRect(px + zoom * 0.4, py + zoom * 0.4, zoom * 0.2, zoom * 0.2);
          }
        } else if (code === AMBUSH_TILE) {
          ctx.fillStyle = '#3a3a44';
          ctx.fillRect(px, py, zoom, zoom);
          ctx.strokeStyle = '#aaa';
          ctx.lineWidth = Math.max(1, zoom / 12);
          ctx.beginPath();
          ctx.moveTo(px + zoom * 0.25, py + zoom * 0.25);
          ctx.lineTo(px + zoom * 0.75, py + zoom * 0.75);
          ctx.moveTo(px + zoom * 0.75, py + zoom * 0.25);
          ctx.lineTo(px + zoom * 0.25, py + zoom * 0.75);
          ctx.stroke();
        } else if (isFloorCode(code)) {
          if (showFloors) {
            ctx.fillStyle = floorCodeColor(code, 0.55);
            ctx.fillRect(px, py, zoom, zoom);
            if (zoom >= 16) {
              ctx.fillStyle = 'rgba(255,255,255,.5)';
              ctx.font = `${Math.floor(zoom * 0.34)}px monospace`;
              ctx.textAlign = 'center';
              ctx.textBaseline = 'middle';
              ctx.fillText(code.toString(16).toUpperCase(), px + zoom / 2, py + zoom / 2);
            }
          } else {
            ctx.fillStyle = '#26262e';
            ctx.fillRect(px, py, zoom, zoom);
          }
        } else {
          // Solid wall.
          const tex = assets.wallForCode(code);
          if (tex) {
            ctx.drawImage(tex, px, py, zoom, zoom);
          } else {
            ctx.fillStyle = '#5a3a5a';
            ctx.fillRect(px, py, zoom, zoom);
            if (zoom >= 14) {
              ctx.fillStyle = '#fff';
              ctx.font = `${Math.floor(zoom * 0.36)}px monospace`;
              ctx.textAlign = 'center';
              ctx.textBaseline = 'middle';
              ctx.fillText(String(code), px + zoom / 2, py + zoom / 2);
            }
          }
        }
      }
    }

    // Objects overlay.
    if (showObjects) {
      for (let y = 0; y < MAP_WIDTH; y++) {
        for (let x = 0; x < MAP_WIDTH; x++) {
          const code = lvl.plane1[y * MAP_WIDTH + x];
          if (code === 0) continue;
          const px = x * zoom;
          const py = y * zoom;
          const info = objectInfo(code);
          if (!info) continue;
          const faded = !spawnsAtSkill(code, skillFilter);
          ctx.globalAlpha = faded ? 0.22 : 1;

          if (code >= PLAYER_START_FIRST && code <= PLAYER_START_LAST) {
            ctx.fillStyle = '#37e057';
            ctx.beginPath();
            ctx.arc(px + zoom / 2, py + zoom / 2, zoom * 0.32, 0, Math.PI * 2);
            ctx.fill();
            const dirs = ['N', 'E', 'S', 'W'];
            drawArrow(ctx, px, py, zoom, dirs[code - 19], '#063');
          } else if (code >= TURN_FIRST && code <= TURN_LAST && info.kind === 'turn') {
            drawArrow(ctx, px, py, zoom, TURN_DIRECTIONS[code - TURN_FIRST], '#4ad0ff');
          } else if (code === PUSHWALL) {
            ctx.strokeStyle = '#ffd24a';
            ctx.lineWidth = Math.max(1.5, zoom / 9);
            ctx.strokeRect(px + 2, py + 2, zoom - 4, zoom - 4);
            if (zoom >= 14) {
              ctx.fillStyle = '#ffd24a';
              ctx.font = `bold ${Math.floor(zoom * 0.42)}px monospace`;
              ctx.textAlign = 'center';
              ctx.textBaseline = 'middle';
              ctx.fillText('S', px + zoom / 2, py + zoom / 2);
            }
          } else if (code === EXIT_TRIGGER) {
            ctx.fillStyle = '#ff7ad0';
            ctx.font = `bold ${Math.floor(zoom * 0.5)}px monospace`;
            ctx.textAlign = 'center';
            ctx.textBaseline = 'middle';
            ctx.fillText('X', px + zoom / 2, py + zoom / 2);
          } else if (info.sprite) {
            const sprite = store.assets?.spriteByName(info.sprite);
            if (sprite) {
              ctx.drawImage(sprite, px, py, zoom, zoom);
            } else {
              ctx.fillStyle = '#888';
              ctx.fillRect(px + zoom * 0.25, py + zoom * 0.25, zoom * 0.5, zoom * 0.5);
            }
            if (info.kind === 'enemy') {
              const skill = enemyMinSkill(code);
              ctx.strokeStyle = SKILL_COLOR[skill] ?? '#fff';
              ctx.lineWidth = Math.max(1, zoom / 14);
              ctx.strokeRect(px + 0.5, py + 0.5, zoom - 1, zoom - 1);
              if (info.facing) drawArrow(ctx, px, py + zoom * 0.28, zoom * 0.5, info.facing, SKILL_COLOR[skill] ?? '#fff');
              if (info.mode === 'patrol' && zoom >= 12) {
                ctx.fillStyle = SKILL_COLOR[skill] ?? '#fff';
                ctx.font = `bold ${Math.floor(zoom * 0.3)}px monospace`;
                ctx.textAlign = 'left';
                ctx.textBaseline = 'top';
                ctx.fillText('»', px + 2, py + 1);
              }
            }
          } else {
            ctx.fillStyle = '#ff5af0';
            ctx.fillRect(px + zoom * 0.3, py + zoom * 0.3, zoom * 0.4, zoom * 0.4);
          }
          ctx.globalAlpha = 1;
        }
      }
    }

    if (showGrid && zoom >= 8) {
      ctx.strokeStyle = 'rgba(255,255,255,.07)';
      ctx.lineWidth = 1;
      ctx.beginPath();
      for (let i = 0; i <= MAP_WIDTH; i++) {
        ctx.moveTo(i * zoom + 0.5, 0);
        ctx.lineTo(i * zoom + 0.5, size);
        ctx.moveTo(0, i * zoom + 0.5);
        ctx.lineTo(size, i * zoom + 0.5);
      }
      ctx.stroke();
    }

    if (hover) {
      ctx.strokeStyle = 'var(--accent)';
      ctx.strokeStyle = '#4a9eff';
      ctx.lineWidth = 2;
      ctx.strokeRect(hover.x * zoom + 1, hover.y * zoom + 1, zoom - 2, zoom - 2);
    }
  }, []);

  useEffect(() => {
    draw();
  });

  const tileAt = useCallback((e) => {
    const canvas = canvasRef.current;
    const rect = canvas.getBoundingClientRect();
    const x = Math.floor((e.clientX - rect.left) / store.ui.zoom);
    const y = Math.floor((e.clientY - rect.top) / store.ui.zoom);
    if (x < 0 || y < 0 || x >= MAP_WIDTH || y >= MAP_WIDTH) return null;
    return { x, y };
  }, []);

  const onMouseDown = useCallback(
    (e) => {
      const pos = tileAt(e);
      if (!pos) return;
      const lvl = currentLevel();
      if (!lvl) return;
      e.preventDefault();
      // Shift+click: pick tile from map into the pressed button.
      if (e.shiftKey) {
        const objCode = lvl.plane1[pos.y * MAP_WIDTH + pos.x];
        const brush =
          objCode !== 0 && store.ui.showObjects
            ? { plane: 1, code: objCode }
            : { plane: 0, code: lvl.plane0[pos.y * MAP_WIDTH + pos.x] };
        updateUi((ui) => {
          if (e.button === 2) ui.rmb = brush;
          else ui.lmb = brush;
        });
        return;
      }
      const brush = e.button === 2 ? store.ui.rmb : store.ui.lmb;
      paintingRef.current = { button: e.button, plane: brush.plane, code: brush.code };
      beginGesture(brush.plane);
      setTile(brush.plane, pos.x, pos.y, brush.code);
    },
    [tileAt],
  );

  const onMouseMove = useCallback(
    (e) => {
      const pos = tileAt(e);
      const prev = store.ui.hover;
      if ((pos?.x !== prev?.x || pos?.y !== prev?.y) && (pos || prev)) {
        updateUi((ui) => {
          ui.hover = pos;
        });
      }
      if (paintingRef.current && pos) {
        setTile(paintingRef.current.plane, pos.x, pos.y, paintingRef.current.code);
      }
    },
    [tileAt],
  );

  const onMouseUp = useCallback(() => {
    if (paintingRef.current) {
      paintingRef.current = null;
      endGesture();
    }
  }, []);

  const onWheel = useCallback((e) => {
    if (!e.ctrlKey) return;
    e.preventDefault();
    updateUi((ui) => {
      ui.zoom = Math.max(4, Math.min(32, ui.zoom + (e.deltaY < 0 ? 1 : -1)));
    });
  }, []);

  useEffect(() => {
    const el = canvasRef.current;
    el?.addEventListener('wheel', onWheel, { passive: false });
    return () => el?.removeEventListener('wheel', onWheel);
  }, [onWheel]);

  return (
    <div style={{ overflow: 'auto', flex: 1, background: '#101016' }}>
      <canvas
        ref={canvasRef}
        style={{ display: 'block', cursor: 'crosshair' }}
        onMouseDown={onMouseDown}
        onMouseMove={onMouseMove}
        onMouseUp={onMouseUp}
        onMouseLeave={() => {
          onMouseUp();
          updateUi((ui) => {
            ui.hover = null;
          });
        }}
        onContextMenu={(e) => e.preventDefault()}
      />
    </div>
  );
}
