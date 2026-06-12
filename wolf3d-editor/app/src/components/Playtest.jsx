import React, { useEffect, useRef, useState } from 'react';
import { store, compileFiles } from '../store.js';
import { useStoreVersion } from '../hooks.js';
import { findGameExe, buildBundle, bootBundle, stopPlaytest } from '../playtest/jsdos.js';

export function Playtest() {
  useStoreVersion();
  const hostRef = useRef(null);
  const [error, setError] = useState(null);
  const [running, setRunning] = useState(false);
  const [skill, setSkill] = useState('normal');
  const [warp, setWarp] = useState(true);

  const game = store.game;
  const exe = game ? findGameExe(game.files.extras) : null;

  const start = async () => {
    if (!game || !exe || !hostRef.current) return;
    setError(null);
    try {
      const compiled = compileFiles();
      // tedlevel numbering: episode*10 + floor (0-based level slot already is that).
      const bundle = buildBundle(compiled, game.files.extras, exe, {
        tedlevel: warp ? store.ui.level : undefined,
        skill: warp ? skill : undefined,
      });
      await bootBundle(hostRef.current, bundle);
      setRunning(true);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  useEffect(() => {
    return () => {
      stopPlaytest();
    };
  }, []);

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minHeight: 0 }}>
      <div style={{ display: 'flex', gap: 8, padding: 8, alignItems: 'center', borderBottom: '1px solid var(--border)' }}>
        <button onClick={start} disabled={!exe}>
          {running ? 'Restart' : 'Boot game'}
        </button>
        <label style={{ display: 'flex', gap: 4, alignItems: 'center' }}>
          <input type="checkbox" checked={warp} onChange={(e) => setWarp(e.target.checked)} />
          warp to current level (TEDLEVEL {store.ui.level})
        </label>
        <select value={skill} onChange={(e) => setSkill(e.target.value)} disabled={!warp}>
          <option value="baby">Can I play, Daddy?</option>
          <option value="easy">Don't hurt me.</option>
          <option value="normal">Bring 'em on!</option>
          <option value="hard">I am Death incarnate!</option>
        </select>
        {running && (
          <button
            onClick={async () => {
              await stopPlaytest();
              setRunning(false);
              if (hostRef.current) hostRef.current.innerHTML = '';
            }}
          >
            Stop
          </button>
        )}
      </div>
      {!exe && (
        <div style={{ padding: 16, color: 'var(--warn)' }}>
          No game executable found. To playtest in the browser, open a game folder that includes the game EXE
          (e.g. WOLF3D.EXE — the shareware download includes it). The editor's compiled files are overlaid onto your
          folder automatically. Without an EXE you can still use "Download mod" and run it in DOSBox yourself.
        </div>
      )}
      {error && <div style={{ padding: 12, color: 'var(--danger)' }}>{error}</div>}
      <div ref={hostRef} style={{ flex: 1, minHeight: 0, background: '#000' }} />
      <div style={{ padding: 6, fontSize: 11, color: 'var(--dim)' }}>
        Runs your own game EXE in DOSBox-WASM (js-dos, self-hosted with this app). Game files stay in this tab — the
        bundle is a local blob.
      </div>
    </div>
  );
}
