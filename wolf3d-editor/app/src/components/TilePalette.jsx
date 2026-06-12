import React, { useCallback } from 'react';
import {
  WALL_NAMES,
  DOORS,
  AMBUSH_TILE,
  AREA_TILE,
  LAST_AREA_TILE,
  STATICS,
  ENEMIES,
  BOSSES,
  GHOSTS,
  ENEMY_FACINGS,
  enemyCode,
  PUSHWALL,
  EXIT_TRIGGER,
  DEAD_GUARD,
  TURN_FIRST,
  TURN_DIRECTIONS,
} from '@wolf3d/data';
import { store, updateUi } from '../store.js';
import { useStoreVersion } from '../hooks.js';
import { floorCodeColor } from '../game/assets.js';

/** A selectable palette cell. */
function Cell({ brush, label, children, title }) {
  useStoreVersion();
  const { lmb, rmb } = store.ui;
  const isL = lmb.plane === brush.plane && lmb.code === brush.code;
  const isR = rmb.plane === brush.plane && rmb.code === brush.code;
  const onMouse = useCallback(
    (e) => {
      e.preventDefault();
      updateUi((ui) => {
        if (e.button === 2) ui.rmb = brush;
        else ui.lmb = brush;
      });
    },
    [brush],
  );
  return (
    <div
      title={`${title ?? label} (code ${brush.code})`}
      onMouseDown={onMouse}
      onContextMenu={(e) => e.preventDefault()}
      style={{
        width: 44,
        height: 44,
        position: 'relative',
        border: isL ? '2px solid var(--accent)' : isR ? '2px solid var(--warn)' : '1px solid var(--border)',
        borderRadius: 4,
        overflow: 'hidden',
        cursor: 'pointer',
        background: '#15151c',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        flexShrink: 0,
      }}
    >
      {children}
      {label && (
        <div
          style={{
            position: 'absolute',
            bottom: 0,
            left: 0,
            right: 0,
            fontSize: 8,
            textAlign: 'center',
            background: 'rgba(0,0,0,.55)',
            overflow: 'hidden',
            whiteSpace: 'nowrap',
            textOverflow: 'ellipsis',
          }}
        >
          {label}
        </div>
      )}
    </div>
  );
}

function CanvasIcon({ canvas }) {
  const ref = useCallback(
    (el) => {
      if (el && canvas) {
        const ctx = el.getContext('2d');
        ctx.imageSmoothingEnabled = false;
        ctx.clearRect(0, 0, 40, 40);
        ctx.drawImage(canvas, 0, 0, 40, 40);
      }
    },
    [canvas],
  );
  return <canvas ref={ref} width={40} height={40} />;
}

function Section({ title, children }) {
  return (
    <div style={{ marginBottom: 10 }}>
      <div style={{ color: 'var(--dim)', fontSize: 11, margin: '6px 2px' }}>{title}</div>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>{children}</div>
    </div>
  );
}

function MapTab() {
  const assets = store.assets;
  return (
    <>
      <Section title={`WALLS (1–${WALL_NAMES.length})`}>
        {WALL_NAMES.map((name, i) => {
          const code = i + 1;
          const tex = assets?.wallForCode(code);
          return (
            <Cell key={code} brush={{ plane: 0, code }} label={String(code)} title={name}>
              {tex ? <CanvasIcon canvas={tex} /> : <span style={{ color: 'var(--dim)', fontSize: 9 }}>n/a</span>}
            </Cell>
          );
        })}
      </Section>
      <Section title="DOORS (90–101)">
        {DOORS.map((d) => {
          const kind = d.lock === 5 ? 2 : d.lock > 0 ? 3 : 0;
          const tex = assets?.doorCanvas(kind);
          return (
            <Cell key={d.code} brush={{ plane: 0, code: d.code }} label={d.orientation === 'vertical' ? '║' : '═'} title={d.name}>
              {tex ? <CanvasIcon canvas={tex} /> : null}
              {d.lock >= 1 && d.lock <= 2 && (
                <div
                  style={{
                    position: 'absolute',
                    top: 2,
                    right: 2,
                    width: 9,
                    height: 9,
                    borderRadius: 2,
                    background: d.lock === 1 ? '#ffd24a' : '#c0c0d0',
                  }}
                />
              )}
            </Cell>
          );
        })}
      </Section>
      <Section title="SPECIAL">
        <Cell brush={{ plane: 0, code: AMBUSH_TILE }} label="deaf 6A" title="Deaf guard / ambush tile (place under an enemy)">
          <span style={{ fontSize: 18, color: '#aaa' }}>✕</span>
        </Cell>
        <Cell brush={{ plane: 0, code: 0 }} label="erase" title="Nothing (engine-invalid; use a floor code instead)">
          <span style={{ fontSize: 14, color: 'var(--danger)' }}>∅</span>
        </Cell>
      </Section>
      <Section title="FLOOR CODES (6B–8F) — sound zones">
        {Array.from({ length: LAST_AREA_TILE - AREA_TILE + 1 }, (_, i) => {
          const code = AREA_TILE + i;
          const hex = code.toString(16).toUpperCase();
          return (
            <Cell
              key={code}
              brush={{ plane: 0, code }}
              label={hex}
              title={code === AREA_TILE ? `Floor code ${hex} — also the SECRET ELEVATOR code` : `Floor code ${hex}`}
            >
              <div style={{ width: 40, height: 40, background: floorCodeColor(code) }} />
              {code === AREA_TILE && <div style={{ position: 'absolute', top: 1, right: 3, fontSize: 9 }}>★</div>}
            </Cell>
          );
        })}
      </Section>
    </>
  );
}

function ObjTab() {
  useStoreVersion();
  const assets = store.assets;
  const { enemyOpts } = store.ui;
  const setOpts = (patch) =>
    updateUi((ui) => {
      Object.assign(ui.enemyOpts, patch);
    });

  const spriteCell = (code, sprName, label, title) => (
    <Cell key={code} brush={{ plane: 1, code }} label={label} title={title}>
      {assets?.spriteByName(sprName) ? (
        <CanvasIcon canvas={assets.spriteByName(sprName)} />
      ) : (
        <span style={{ color: 'var(--dim)', fontSize: 9 }}>n/a</span>
      )}
    </Cell>
  );

  const categories = [
    ['decoration', 'DECORATIONS'],
    ['treasure', 'TREASURE'],
    ['health', 'HEALTH'],
    ['ammo', 'AMMO'],
    ['weapon', 'WEAPONS'],
    ['key', 'KEYS'],
  ];

  return (
    <>
      <Section title="PLAYER START (one per level)">
        {['N', 'E', 'S', 'W'].map((d, i) => (
          <Cell key={d} brush={{ plane: 1, code: 19 + i }} label={`start ${d}`} title={`Player start facing ${d}`}>
            <span style={{ fontSize: 16, color: '#37e057' }}>{d === 'N' ? '↑' : d === 'E' ? '→' : d === 'S' ? '↓' : '←'}</span>
          </Cell>
        ))}
        <Cell brush={{ plane: 1, code: 0 }} label="erase" title="Remove object">
          <span style={{ fontSize: 14, color: 'var(--danger)' }}>∅</span>
        </Cell>
      </Section>

      <Section title="ENEMIES">
        <div style={{ display: 'flex', gap: 4, marginBottom: 6, flexWrap: 'wrap', width: '100%' }}>
          {['easy', 'medium', 'hard'].map((s) => (
            <button key={s} className={enemyOpts.skill === s ? 'active' : ''} onClick={() => setOpts({ skill: s })}>
              {s}
            </button>
          ))}
          {['stand', 'patrol'].map((m) => (
            <button key={m} className={enemyOpts.mode === m ? 'active' : ''} onClick={() => setOpts({ mode: m })}>
              {m}
            </button>
          ))}
          {ENEMY_FACINGS.map((f, i) => (
            <button key={f} className={enemyOpts.facing === i ? 'active' : ''} onClick={() => setOpts({ facing: i })}>
              {f}
            </button>
          ))}
        </div>
        {ENEMIES.map((e) => {
          const code = enemyCode(e, enemyOpts.mode, enemyOpts.facing, enemyOpts.skill);
          return spriteCell(
            code,
            e.sprite,
            `${e.name} ${code}`,
            `${e.name}, ${enemyOpts.mode === 'stand' ? 'standing' : 'patrolling'} ${ENEMY_FACINGS[enemyOpts.facing]}, ${enemyOpts.skill}+ — code ${code}`,
          );
        })}
        {spriteCell(DEAD_GUARD, 'SPR_GRD_DEAD', 'dead 124', 'Dead guard (counts toward actor & kill totals)')}
      </Section>

      <Section title="BOSSES">
        {BOSSES.map((b) => spriteCell(b.code, b.sprite, String(b.code), `${b.name}${b.deathEndsLevel ? ' — death ends level' : ''}`))}
        {GHOSTS.map((b) => spriteCell(b.code, b.sprite, String(b.code), `${b.name} (E3 secret level ghost)`))}
      </Section>

      <Section title="SPECIALS">
        {TURN_DIRECTIONS.map((d, i) => (
          <Cell key={d} brush={{ plane: 1, code: TURN_FIRST + i }} label={`turn ${d}`} title={`Patrol turning point ${d}`}>
            <span style={{ fontSize: 13, color: '#4ad0ff' }}>
              {{ E: '→', NE: '↗', N: '↑', NW: '↖', W: '←', SW: '↙', S: '↓', SE: '↘' }[d]}
            </span>
          </Cell>
        ))}
        <Cell brush={{ plane: 1, code: PUSHWALL }} label="push 98" title="Pushwall marker (place ON a wall tile) — the secret">
          <span style={{ fontSize: 13, color: '#ffd24a' }}>▣</span>
        </Cell>
        <Cell brush={{ plane: 1, code: EXIT_TRIGGER }} label="endgame" title="Victory-walk trigger (episode end)">
          <span style={{ fontSize: 13, color: '#ff7ad0' }}>X</span>
        </Cell>
      </Section>

      {categories.map(([cat, title]) => (
        <Section key={cat} title={title}>
          {STATICS.filter((s) => s.category === cat).map((s) =>
            spriteCell(
              s.code,
              s.sprite,
              String(s.code),
              `${s.name}${s.kind === 'block' ? ' (blocks movement)' : ''}${s.effect ? ` — ${s.effect}` : ''}${s.points ? ` — ${s.points} pts` : ''}`,
            ),
          )}
        </Section>
      ))}
    </>
  );
}

export function TilePalette() {
  useStoreVersion();
  const tab = store.ui.paletteTab;
  return (
    <div
      style={{
        width: 270,
        borderLeft: '1px solid var(--border)',
        background: 'var(--panel)',
        display: 'flex',
        flexDirection: 'column',
        minHeight: 0,
      }}
    >
      <div style={{ display: 'flex', gap: 4, padding: 6, borderBottom: '1px solid var(--border)' }}>
        {['MAP', 'OBJ'].map((t) => (
          <button
            key={t}
            className={tab === t ? 'active' : ''}
            style={{ flex: 1 }}
            onClick={() =>
              updateUi((ui) => {
                ui.paletteTab = t;
              })
            }
          >
            {t}
          </button>
        ))}
      </div>
      <div style={{ overflowY: 'auto', padding: 6, flex: 1 }}>{tab === 'MAP' ? <MapTab /> : <ObjTab />}</div>
      <div style={{ padding: 6, borderTop: '1px solid var(--border)', fontSize: 10, color: 'var(--dim)' }}>
        Left-click a cell → LMB brush · right-click → RMB brush
        <br />
        Shift+click on map picks up a tile
      </div>
    </div>
  );
}
