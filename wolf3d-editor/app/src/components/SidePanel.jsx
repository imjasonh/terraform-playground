import React from 'react';
import { levelLabel, LIMITS } from '@wolf3d/data';
import { store, currentLevel, updateUi } from '../store.js';
import { useStoreVersion } from '../hooks.js';
import { levelStats } from '../game/stats.js';
import { floorCodeColor } from '../game/assets.js';

function Budget({ label, used, max }) {
  const pct = Math.min(100, (used / max) * 100);
  const color = used > max ? 'var(--danger)' : pct > 85 ? 'var(--warn)' : 'var(--accent)';
  return (
    <div style={{ marginBottom: 4 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 11 }}>
        <span>{label}</span>
        <span style={{ color }}>{used}/{max}</span>
      </div>
      <div style={{ height: 4, background: '#15151c', borderRadius: 2 }}>
        <div style={{ width: `${pct}%`, height: 4, background: color, borderRadius: 2 }} />
      </div>
    </div>
  );
}

export function SidePanel() {
  useStoreVersion();
  const game = store.game;
  const lvl = currentLevel();
  if (!game) return null;
  const stats = lvl ? levelStats(lvl) : null;

  return (
    <div
      style={{
        width: 230,
        borderRight: '1px solid var(--border)',
        background: 'var(--panel)',
        display: 'flex',
        flexDirection: 'column',
        minHeight: 0,
      }}
    >
      <div style={{ color: 'var(--dim)', fontSize: 11, padding: '8px 8px 4px' }}>
        LEVELS ({game.levels.filter(Boolean).length} in {game.ext.toUpperCase()})
      </div>
      <div style={{ overflowY: 'auto', maxHeight: '38%', borderBottom: '1px solid var(--border)' }}>
        {game.levels.map((l, i) =>
          l ? (
            <div
              key={i}
              onClick={() =>
                updateUi((ui) => {
                  ui.level = i;
                })
              }
              style={{
                padding: '3px 10px',
                cursor: 'pointer',
                background: store.ui.level === i ? 'var(--accent)' : 'transparent',
                color: store.ui.level === i ? '#08111f' : 'var(--text)',
                whiteSpace: 'nowrap',
                overflow: 'hidden',
                textOverflow: 'ellipsis',
              }}
            >
              <span style={{ opacity: 0.6, marginRight: 6 }}>{levelLabel(i)}</span>
              {l.name || '(unnamed)'}
            </div>
          ) : null,
        )}
      </div>

      {stats && (
        <div style={{ overflowY: 'auto', padding: 8, flex: 1 }}>
          <div style={{ color: 'var(--dim)', fontSize: 11, marginBottom: 6 }}>STATISTICS (Alt+S)</div>
          <Budget label="Actors (hard)" used={stats.actorsBySkill.hard} max={LIMITS.maxActors} />
          <Budget label="Statics" used={stats.statics} max={LIMITS.maxStatics} />
          <Budget label="Doors" used={stats.doors} max={LIMITS.maxDoors} />
          <table style={{ fontSize: 11, width: '100%', marginTop: 8, borderSpacing: 0 }}>
            <tbody>
              <tr>
                <td style={{ color: 'var(--dim)' }}>Kills E/M/H</td>
                <td style={{ textAlign: 'right' }}>
                  {stats.killsBySkill.easy}/{stats.killsBySkill.medium}/{stats.killsBySkill.hard}
                </td>
              </tr>
              <tr>
                <td style={{ color: 'var(--dim)' }}>Treasure</td>
                <td style={{ textAlign: 'right' }}>
                  {stats.treasureCount} ({stats.treasurePoints} pts)
                </td>
              </tr>
              <tr>
                <td style={{ color: 'var(--dim)' }}>Secrets (pushwalls)</td>
                <td style={{ textAlign: 'right' }}>{stats.pushwalls}</td>
              </tr>
              <tr>
                <td style={{ color: 'var(--dim)' }}>Ammo / Health units</td>
                <td style={{ textAlign: 'right' }}>
                  {stats.ammoUnits} / {stats.healthUnits}
                </td>
              </tr>
              <tr>
                <td style={{ color: 'var(--dim)' }}>Player starts</td>
                <td style={{ textAlign: 'right', color: stats.playerStarts === 1 ? 'inherit' : 'var(--danger)' }}>
                  {stats.playerStarts}
                </td>
              </tr>
            </tbody>
          </table>

          <div style={{ color: 'var(--dim)', fontSize: 11, margin: '10px 0 4px' }}>FLOOR CODES IN USE</div>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 3 }}>
            {[...stats.floorCodes.entries()]
              .sort((a, b) => a[0] - b[0])
              .map(([code, count]) => (
                <div
                  key={code}
                  title={`Floor code ${code.toString(16).toUpperCase()}: ${count} tiles`}
                  style={{
                    background: floorCodeColor(code),
                    borderRadius: 3,
                    padding: '1px 5px',
                    fontSize: 10,
                  }}
                >
                  {code.toString(16).toUpperCase()}·{count}
                </div>
              ))}
          </div>

          {stats.issues.length > 0 && (
            <>
              <div style={{ color: 'var(--danger)', fontSize: 11, margin: '10px 0 4px' }}>ISSUES</div>
              {stats.issues.map((iss, i) => (
                <div key={i} style={{ color: 'var(--danger)', fontSize: 11 }}>
                  • {iss}
                </div>
              ))}
            </>
          )}
        </div>
      )}
    </div>
  );
}
