import React, { useEffect, useRef } from 'react';
import { WL6_CEILING, FLOOR_COLOR } from '@wolf3d/data';
import { store, currentLevel } from '../store.js';
import { useStoreVersion } from '../hooks.js';
import { Raycaster } from '../game/raycaster.js';

export function Preview3D() {
  useStoreVersion();
  const canvasRef = useRef(null);
  const stateRef = useRef({ keys: new Set(), caster: null, raf: 0 });

  const lvl = currentLevel();
  const levelIndex = store.ui.level;

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas || !lvl || !store.assets) return;
    const ceiling = WL6_CEILING[levelIndex] ?? 0x1d;
    const caster = new Raycaster(lvl, store.assets, ceiling, FLOOR_COLOR);
    const st = stateRef.current;
    st.caster = caster;

    const ctx = canvas.getContext('2d');
    const img = ctx.createImageData(caster.width, caster.height);
    let last = performance.now();

    const frame = (now) => {
      const dt = Math.min(0.05, (now - last) / 1000);
      last = now;
      const speed = (st.keys.has('ShiftLeft') ? 5.0 : 2.8) * dt;
      const turn = 2.4 * dt;
      if (st.keys.has('KeyA')) caster.angle -= turn;
      if (st.keys.has('KeyD')) caster.angle += turn;
      let fwd = 0;
      let strafe = 0;
      if (st.keys.has('KeyW') || st.keys.has('ArrowUp')) fwd += 1;
      if (st.keys.has('KeyS') || st.keys.has('ArrowDown')) fwd -= 1;
      if (st.keys.has('KeyQ')) strafe -= 1;
      if (st.keys.has('KeyE')) strafe += 1;
      if (fwd || strafe) {
        const cos = Math.cos(caster.angle);
        const sin = Math.sin(caster.angle);
        caster.move((cos * fwd - sin * strafe) * speed, (sin * fwd + cos * strafe) * speed);
      }
      caster.render(img);
      ctx.putImageData(img, 0, 0);
      st.raf = requestAnimationFrame(frame);
    };
    st.raf = requestAnimationFrame(frame);

    const down = (e) => {
      st.keys.add(e.code);
      if (['KeyW', 'KeyA', 'KeyS', 'KeyD', 'ArrowUp', 'ArrowDown'].includes(e.code)) e.preventDefault();
    };
    const up = (e) => st.keys.delete(e.code);
    const mouse = (e) => {
      if (document.pointerLockElement === canvas) caster.angle += e.movementX * 0.0035;
    };
    window.addEventListener('keydown', down);
    window.addEventListener('keyup', up);
    window.addEventListener('mousemove', mouse);
    return () => {
      cancelAnimationFrame(st.raf);
      window.removeEventListener('keydown', down);
      window.removeEventListener('keyup', up);
      window.removeEventListener('mousemove', mouse);
    };
  }, [lvl, levelIndex]);

  if (!lvl) return <div style={{ padding: 20 }}>No level selected.</div>;

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 10, background: '#000' }}>
      <canvas
        ref={canvasRef}
        width={320}
        height={200}
        style={{ width: 'min(96%, 960px)', aspectRatio: '320 / 200', cursor: 'crosshair', border: '1px solid var(--border)' }}
        onClick={(e) => e.currentTarget.requestPointerLock()}
      />
      <div style={{ color: 'var(--dim)', fontSize: 11 }}>
        Click to mouse-look · WASD move · Q/E strafe · Shift run — preview only (doors stay shut; press F5 to playtest the real
        game)
      </div>
    </div>
  );
}
