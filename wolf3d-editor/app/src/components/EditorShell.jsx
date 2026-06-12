import React, { useCallback, useEffect } from 'react';
import { describeWallCode, levelLabel } from '@wolf3d/data';
import { MAP_WIDTH } from '@wolf3d/codec';
import { store, currentLevel, updateUi, undo, redo, compileFiles } from '../store.js';
import { useStoreVersion } from '../hooks.js';
import { buildZip, download } from '../io/zip.js';
import { MapCanvas } from './MapCanvas.jsx';
import { TilePalette } from './TilePalette.jsx';
import { SidePanel } from './SidePanel.jsx';
import { Preview3D } from './Preview3D.jsx';
import { Playtest } from './Playtest.jsx';
import { GfxStudio } from './GfxStudio.jsx';
import { objectInfo } from '../game/objectinfo.js';

function brushLabel(brush) {
  if (brush.plane === 0) return `MAP ${brush.code}: ${describeWallCode(brush.code)}`;
  const info = objectInfo(brush.code);
  return `OBJ ${brush.code}: ${info ? info.label : brush.code === 0 ? 'erase' : '?'}`;
}

function StatusBar() {
  useStoreVersion();
  const lvl = currentLevel();
  const { hover, lmb, rmb } = store.ui;
  let hoverText = '';
  if (lvl && hover) {
    const w = lvl.plane0[hover.y * MAP_WIDTH + hover.x];
    const o = lvl.plane1[hover.y * MAP_WIDTH + hover.x];
    const oInfo = objectInfo(o);
    hoverText = `(${hover.x},${hover.y})  plane0=${w} ${describeWallCode(w)}${o ? `  ·  plane1=${o} ${oInfo?.label ?? ''}` : ''}`;
  }
  return (
    <div
      style={{
        display: 'flex',
        gap: 16,
        padding: '4px 10px',
        borderTop: '1px solid var(--border)',
        background: 'var(--panel)',
        fontSize: 11,
        whiteSpace: 'nowrap',
        overflow: 'hidden',
      }}
    >
      <span style={{ color: 'var(--accent)' }}>L: {brushLabel(lmb)}</span>
      <span style={{ color: 'var(--warn)' }}>R: {brushLabel(rmb)}</span>
      <span style={{ marginLeft: 'auto', color: 'var(--dim)', overflow: 'hidden', textOverflow: 'ellipsis' }}>{hoverText}</span>
    </div>
  );
}

export function EditorShell() {
  useStoreVersion();
  const game = store.game;
  const lvl = currentLevel();
  const ui = store.ui;

  const exportMod = useCallback(() => {
    const files = compileFiles();
    if (files.length === 0) return;
    download(`wolf3d-mod-${Date.now()}.zip`, buildZip(files));
  }, []);

  const onKey = useCallback(
    (e) => {
      if (e.target instanceof HTMLInputElement || e.target instanceof HTMLSelectElement) return;
      if ((e.ctrlKey || e.metaKey) && e.code === 'KeyZ') {
        e.preventDefault();
        e.shiftKey ? redo() : undo();
      } else if ((e.ctrlKey || e.metaKey) && e.code === 'KeyY') {
        e.preventDefault();
        redo();
      } else if ((e.ctrlKey || e.metaKey) && e.code === 'KeyS') {
        e.preventDefault();
        exportMod();
      } else if (e.code === 'F5') {
        e.preventDefault();
        updateUi((u) => {
          u.panel = 'playtest';
        });
      } else if (e.code === 'Tab' && store.ui.panel === 'map') {
        e.preventDefault();
        updateUi((u) => {
          u.paletteTab = u.paletteTab === 'MAP' ? 'OBJ' : 'MAP';
        });
      } else if (e.code === 'KeyO' && !e.ctrlKey && !e.metaKey && store.ui.panel === 'map') {
        updateUi((u) => {
          u.showObjects = !u.showObjects;
        });
      } else if (e.code === 'KeyF' && !e.ctrlKey && !e.metaKey && store.ui.panel === 'map') {
        updateUi((u) => {
          u.showFloors = !u.showFloors;
        });
      }
    },
    [exportMod],
  );

  useEffect(() => {
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onKey]);

  // Warn before losing unsaved work.
  useEffect(() => {
    const handler = (e) => {
      if (store.ui.dirty) {
        e.preventDefault();
        e.returnValue = '';
      }
    };
    window.addEventListener('beforeunload', handler);
    return () => window.removeEventListener('beforeunload', handler);
  }, []);

  if (!game) return null;

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column' }}>
      {/* Toolbar */}
      <div
        style={{
          display: 'flex',
          gap: 6,
          alignItems: 'center',
          padding: 6,
          borderBottom: '1px solid var(--border)',
          background: 'var(--panel)',
          flexWrap: 'wrap',
        }}
      >
        <span style={{ color: '#c33', fontWeight: 'bold', letterSpacing: 1, marginRight: 6 }}>WOLF3D</span>
        {['map', 'gfx', '3d', 'playtest'].map((p) => (
          <button
            key={p}
            className={ui.panel === p ? 'active' : ''}
            onClick={() =>
              updateUi((u) => {
                u.panel = p;
              })
            }
          >
            {p === 'map' ? 'Map' : p === 'gfx' ? 'Graphics' : p === '3d' ? '3D Preview' : 'Playtest (F5)'}
          </button>
        ))}
        <div style={{ width: 1, height: 20, background: 'var(--border)', margin: '0 4px' }} />
        <button onClick={undo} disabled={store.undoStack.length === 0} title="Ctrl+Z">
          ↶ Undo
        </button>
        <button onClick={redo} disabled={store.redoStack.length === 0} title="Ctrl+Shift+Z">
          ↷ Redo
        </button>
        <div style={{ width: 1, height: 20, background: 'var(--border)', margin: '0 4px' }} />
        {ui.panel === 'map' && (
          <>
            <button
              className={ui.showObjects ? 'active' : ''}
              onClick={() =>
                updateUi((u) => {
                  u.showObjects = !u.showObjects;
                })
              }
              title="Toggle object layer (O)"
            >
              OBJ
            </button>
            <button
              className={ui.showFloors ? 'active' : ''}
              onClick={() =>
                updateUi((u) => {
                  u.showFloors = !u.showFloors;
                })
              }
              title="Toggle floor-code layer (F)"
            >
              FLR
            </button>
            <button
              className={ui.showGrid ? 'active' : ''}
              onClick={() =>
                updateUi((u) => {
                  u.showGrid = !u.showGrid;
                })
              }
            >
              GRID
            </button>
            <select
              value={ui.skillFilter}
              onChange={(e) =>
                updateUi((u) => {
                  u.skillFilter = e.target.value;
                })
              }
              title="Skill filter: dim enemies that don't spawn at this difficulty"
            >
              <option value="all">all skills</option>
              <option value="easy">easy view</option>
              <option value="medium">medium view</option>
              <option value="hard">hard view</option>
            </select>
            <button
              onClick={() =>
                updateUi((u) => {
                  u.zoom = Math.max(4, u.zoom - 1);
                })
              }
            >
              −
            </button>
            <span style={{ color: 'var(--dim)', fontSize: 11 }}>{ui.zoom}px</span>
            <button
              onClick={() =>
                updateUi((u) => {
                  u.zoom = Math.min(32, u.zoom + 1);
                })
              }
            >
              +
            </button>
          </>
        )}
        <div style={{ marginLeft: 'auto', display: 'flex', gap: 6, alignItems: 'center' }}>
          {lvl && (
            <input
              type="text"
              value={lvl.name}
              maxLength={15}
              onChange={(e) => {
                lvl.name = e.target.value;
                updateUi((u) => {
                  u.dirty = true;
                });
              }}
              style={{ width: 140 }}
              title={`Level name (GAMEMAPS header) — ${levelLabel(ui.level)}`}
            />
          )}
          {ui.dirty && <span style={{ color: 'var(--warn)' }}>●</span>}
          <button onClick={exportMod} title="Ctrl+S — compile GAMEMAPS/MAPHEAD (+VSWAP if edited) and download as ZIP">
            ⬇ Download mod
          </button>
        </div>
      </div>

      {/* Main area */}
      <div style={{ flex: 1, display: 'flex', minHeight: 0 }}>
        {ui.panel === 'map' && (
          <>
            <SidePanel />
            <MapCanvas />
            <TilePalette />
          </>
        )}
        {ui.panel === '3d' && <Preview3D />}
        {ui.panel === 'playtest' && <Playtest />}
        {ui.panel === 'gfx' && <GfxStudio />}
      </div>

      {ui.panel === 'map' && <StatusBar />}
    </div>
  );
}
