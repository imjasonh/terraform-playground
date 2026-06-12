import React, { useCallback, useState } from 'react';
import { loadFromFiles, loadFromDirectory, loadDemo } from '../io/files.js';
import { openGame, store } from '../store.js';
import { useStoreVersion } from '../hooks.js';

export function OpenScreen() {
  useStoreVersion();
  const [busy, setBusy] = useState(false);
  const [dragOver, setDragOver] = useState(false);

  const handleFiles = useCallback(async (files) => {
    setBusy(true);
    try {
      openGame(await loadFromFiles(files));
    } finally {
      setBusy(false);
    }
  }, []);

  const onDrop = useCallback(
    async (e) => {
      e.preventDefault();
      setDragOver(false);
      await handleFiles([...e.dataTransfer.files]);
    },
    [handleFiles],
  );

  const onPickDir = useCallback(async () => {
    setBusy(true);
    try {
      const files = await loadFromDirectory();
      if (files) openGame(files);
    } finally {
      setBusy(false);
    }
  }, []);

  const onDemo = useCallback(async () => {
    setBusy(true);
    try {
      openGame(await loadDemo());
    } catch (err) {
      store.error = String(err);
    } finally {
      setBusy(false);
    }
  }, []);

  return (
    <div
      style={{
        height: '100%',
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        justifyContent: 'center',
        gap: 16,
      }}
      onDragOver={(e) => {
        e.preventDefault();
        setDragOver(true);
      }}
      onDragLeave={() => setDragOver(false)}
      onDrop={onDrop}
    >
      <h1 style={{ fontSize: 28, letterSpacing: 2, margin: 0, color: '#c33' }}>WOLF3D EDITOR</h1>
      <div style={{ color: 'var(--dim)' }}>
        A Wolfenstein 3D level editor, faithful to the original. Entirely in your browser — files never leave this tab.
      </div>
      <div
        style={{
          border: `2px dashed ${dragOver ? 'var(--accent)' : 'var(--border)'}`,
          borderRadius: 12,
          padding: '48px 64px',
          textAlign: 'center',
          background: dragOver ? 'rgba(74,158,255,.08)' : 'var(--panel)',
        }}
      >
        <div style={{ marginBottom: 12, fontSize: 15 }}>
          Drop your game files here
          <br />
          <span style={{ color: 'var(--dim)', fontSize: 12 }}>
            (a game folder's contents, individual MAPHEAD/GAMEMAPS/VSWAP/VGAGRAPH files, or a mod ZIP)
          </span>
        </div>
        <div style={{ display: 'flex', gap: 8, justifyContent: 'center' }}>
          <label>
            <input
              type="file"
              multiple
              style={{ display: 'none' }}
              onChange={(e) => e.target.files && handleFiles([...e.target.files])}
            />
            <span
              role="button"
              style={{
                background: 'var(--panel2)',
                border: '1px solid var(--border)',
                borderRadius: 4,
                padding: '6px 12px',
                cursor: 'pointer',
              }}
            >
              Choose files…
            </span>
          </label>
          {'showDirectoryPicker' in window && <button onClick={onPickDir}>Open game folder…</button>}
          <button onClick={onDemo} disabled={busy}>
            {busy ? 'Loading…' : 'Open shareware demo (E1)'}
          </button>
        </div>
      </div>
      {store.error && <div style={{ color: 'var(--danger)' }}>{store.error}</div>}
      <div style={{ color: 'var(--dim)', fontSize: 11, maxWidth: 560, textAlign: 'center' }}>
        Supports Wolfenstein 3D (.WL1 shareware / .WL6 registered) and Spear of Destiny (.SOD/.SDM) data files. The
        shareware demo episode is freely distributable; registered game data must come from your own copy.
      </div>
    </div>
  );
}
