import React, { useCallback, useEffect, useRef, useState } from 'react';
import { WALL_SIZE, WOLF_PALETTE, TRANSPARENT_INDEX, nearestColor, parseHuffDict, parseVgaHead, splitVgaChunks, expandVgaChunk, parsePicTable, demungePic } from '@wolf3d/codec';
import { SPRITE_NAMES, WALL_NAMES } from '@wolf3d/data';
import { store, setWallPixels, setSpritePixels } from '../store.js';
import { useStoreVersion } from '../hooks.js';
import { pixelsToCanvas, paletteColor } from '../game/assets.js';

/** Export 64x64 indexed pixels (or any canvas) as a PNG download. */
function exportPng(canvas, filename) {
  canvas.toBlob((blob) => {
    if (!blob) return;
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = filename;
    a.click();
    setTimeout(() => URL.revokeObjectURL(url), 5000);
  }, 'image/png');
}

/** Quantize an imported image file to 64x64 wolf-palette pixels. */
async function importPng(file, transparent) {
  const bmp = await createImageBitmap(file);
  const canvas = document.createElement('canvas');
  canvas.width = WALL_SIZE;
  canvas.height = WALL_SIZE;
  const ctx = canvas.getContext('2d');
  ctx.imageSmoothingEnabled = false;
  ctx.drawImage(bmp, 0, 0, WALL_SIZE, WALL_SIZE);
  const img = ctx.getImageData(0, 0, WALL_SIZE, WALL_SIZE);
  const out = new Uint8Array(WALL_SIZE * WALL_SIZE);
  for (let i = 0; i < out.length; i++) {
    const a = img.data[i * 4 + 3];
    if (transparent && a < 128) {
      out[i] = TRANSPARENT_INDEX;
    } else {
      out[i] = nearestColor(img.data[i * 4], img.data[i * 4 + 1], img.data[i * 4 + 2]);
    }
  }
  return out;
}

function PaletteGrid({ selected, onPick, allowTransparent }) {
  return (
    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(16, 14px)', gap: 1 }}>
      {Array.from({ length: 256 }, (_, i) => (
        <div
          key={i}
          title={`#${i} (${i.toString(16).toUpperCase()})${i === 255 ? ' — transparent key' : ''}`}
          onClick={() => (i !== 255 || allowTransparent) && onPick(i)}
          style={{
            width: 14,
            height: 14,
            background: i === 255 && allowTransparent ? 'repeating-conic-gradient(#666 0 25%, #333 0 50%) 0 0/8px 8px' : paletteColor(i),
            outline: selected === i ? '2px solid #fff' : 'none',
            outlineOffset: -1,
            cursor: i !== 255 || allowTransparent ? 'pointer' : 'not-allowed',
            opacity: i === 255 && !allowTransparent ? 0.25 : 1,
          }}
        />
      ))}
    </div>
  );
}

function PixelEditor({ pixels, transparent, onCommit, name }) {
  const [color, setColor] = useState(transparent ? TRANSPARENT_INDEX : 30);
  const [work, setWork] = useState(() => pixels.slice());
  const canvasRef = useRef(null);
  const paintingRef = useRef(false);
  const zoom = 7;

  useEffect(() => {
    setWork(pixels.slice());
  }, [pixels]);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext('2d');
    // checkerboard for transparency
    for (let y = 0; y < WALL_SIZE; y++) {
      for (let x = 0; x < WALL_SIZE; x++) {
        const p = work[y * WALL_SIZE + x];
        if (transparent && p === TRANSPARENT_INDEX) {
          ctx.fillStyle = (x + y) % 2 ? '#3a3a3a' : '#2c2c2c';
        } else {
          ctx.fillStyle = paletteColor(p);
        }
        ctx.fillRect(x * zoom, y * zoom, zoom, zoom);
      }
    }
  }, [work, transparent]);

  const paint = useCallback(
    (e) => {
      const rect = canvasRef.current.getBoundingClientRect();
      const x = Math.floor((e.clientX - rect.left) / zoom);
      const y = Math.floor((e.clientY - rect.top) / zoom);
      if (x < 0 || y < 0 || x >= WALL_SIZE || y >= WALL_SIZE) return;
      if (e.altKey) {
        setColor(work[y * WALL_SIZE + x]);
        return;
      }
      setWork((w) => {
        if (w[y * WALL_SIZE + x] === color) return w;
        const next = w.slice();
        next[y * WALL_SIZE + x] = color;
        return next;
      });
    },
    [color, work],
  );

  const dirty = work.some((v, i) => v !== pixels[i]);

  return (
    <div style={{ display: 'flex', gap: 12, padding: 12, alignItems: 'flex-start' }}>
      <div>
        <canvas
          ref={canvasRef}
          width={WALL_SIZE * zoom}
          height={WALL_SIZE * zoom}
          style={{ border: '1px solid var(--border)', cursor: 'crosshair' }}
          onMouseDown={(e) => {
            paintingRef.current = true;
            paint(e);
          }}
          onMouseMove={(e) => paintingRef.current && paint(e)}
          onMouseUp={() => (paintingRef.current = false)}
          onMouseLeave={() => (paintingRef.current = false)}
        />
        <div style={{ fontSize: 11, color: 'var(--dim)', marginTop: 4 }}>Click/drag to paint · Alt+click to pick color</div>
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
        <PaletteGrid selected={color} onPick={setColor} allowTransparent={transparent} />
        <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
          <button disabled={!dirty} onClick={() => onCommit(work)}>
            Apply to VSWAP
          </button>
          <button disabled={!dirty} onClick={() => setWork(pixels.slice())}>
            Revert
          </button>
          <button onClick={() => exportPng(pixelsToCanvas(work, transparent), `${name}.png`)}>Export PNG</button>
          <label>
            <input
              type="file"
              accept="image/*"
              style={{ display: 'none' }}
              onChange={async (e) => {
                const f = e.target.files?.[0];
                if (f) setWork(await importPng(f, transparent));
                e.target.value = '';
              }}
            />
            <span role="button" style={{ background: 'var(--panel2)', border: '1px solid var(--border)', borderRadius: 4, padding: '4px 10px', cursor: 'pointer' }}>
              Import PNG…
            </span>
          </label>
        </div>
        <div style={{ fontSize: 11, color: 'var(--dim)', maxWidth: 260 }}>
          Imports are scaled to 64×64 and quantized to the Wolf palette.
          {transparent && ' Transparent pixels become palette index 255 (the engine transparency key).'}
        </div>
      </div>
    </div>
  );
}

function Thumb({ canvas, label, selected, onClick }) {
  const ref = useCallback(
    (el) => {
      if (el && canvas) {
        const ctx = el.getContext('2d');
        ctx.imageSmoothingEnabled = false;
        ctx.clearRect(0, 0, 48, 48);
        ctx.drawImage(canvas, 0, 0, 48, 48);
      }
    },
    [canvas],
  );
  return (
    <div
      onClick={onClick}
      title={label}
      style={{
        width: 52,
        cursor: 'pointer',
        border: selected ? '2px solid var(--accent)' : '1px solid var(--border)',
        borderRadius: 4,
        padding: 1,
        background: '#15151c',
      }}
    >
      {canvas ? <canvas ref={ref} width={48} height={48} /> : <div style={{ width: 48, height: 48 }} />}
      <div style={{ fontSize: 8, textAlign: 'center', overflow: 'hidden', whiteSpace: 'nowrap', textOverflow: 'ellipsis' }}>{label}</div>
    </div>
  );
}

function PicsBrowser() {
  const game = store.game;
  const [pics, setPics] = useState(null);
  useEffect(() => {
    if (!game) return;
    const dict = game.files.byName.get('vgadict');
    const headBytes = game.files.byName.get('vgahead');
    const graph = game.files.byName.get('vgagraph');
    if (!dict || !headBytes || !graph) {
      setPics({ error: 'VGADICT/VGAHEAD/VGAGRAPH not loaded.' });
      return;
    }
    try {
      const nodes = parseHuffDict(dict);
      const chunks = splitVgaChunks(graph, parseVgaHead(headBytes));
      const table = parsePicTable(expandVgaChunk(chunks[0], nodes));
      /** @type {{canvas: HTMLCanvasElement, label: string}[]} */
      const decoded = [];
      // Pics begin after STRUCTPIC + fonts. Scan chunks; identify pic chunks
      // by matching expanded size to a pictable entry.
      let picIndex = 0;
      for (let c = 1; c < chunks.length && picIndex < table.length; c++) {
        const chunk = chunks[c];
        if (!chunk || chunk.byteLength < 4) continue;
        const { width, height } = table[picIndex] ?? {};
        if (!width) break;
        let data;
        try {
          data = expandVgaChunk(chunk, nodes);
        } catch {
          continue;
        }
        if (data.byteLength !== width * height) continue;
        const linear = demungePic(data, width, height);
        const canvas = document.createElement('canvas');
        canvas.width = width;
        canvas.height = height;
        const ctx = canvas.getContext('2d');
        const img = ctx.createImageData(width, height);
        for (let i = 0; i < linear.length; i++) {
          img.data[i * 4] = WOLF_PALETTE[linear[i] * 3];
          img.data[i * 4 + 1] = WOLF_PALETTE[linear[i] * 3 + 1];
          img.data[i * 4 + 2] = WOLF_PALETTE[linear[i] * 3 + 2];
          img.data[i * 4 + 3] = 255;
        }
        ctx.putImageData(img, 0, 0);
        decoded.push({ canvas, label: `pic ${picIndex} (${width}×${height})` });
        picIndex++;
      }
      setPics({ decoded });
    } catch (err) {
      setPics({ error: String(err) });
    }
  }, [game]);

  if (!pics) return <div style={{ padding: 16, color: 'var(--dim)' }}>Decoding VGAGRAPH…</div>;
  if (pics.error) return <div style={{ padding: 16, color: 'var(--warn)' }}>{pics.error}</div>;
  return (
    <div style={{ padding: 12, overflowY: 'auto', display: 'flex', flexWrap: 'wrap', gap: 10, alignContent: 'flex-start' }}>
      {pics.decoded.map((p, i) => (
        <div key={i} style={{ textAlign: 'center' }}>
          <canvas
            ref={(el) => {
              if (el) {
                el.getContext('2d').drawImage(p.canvas, 0, 0);
              }
            }}
            width={p.canvas.width}
            height={p.canvas.height}
            style={{ border: '1px solid var(--border)', maxWidth: 320, cursor: 'pointer' }}
            onClick={() => exportPng(p.canvas, `${p.label.replaceAll(' ', '_')}.png`)}
            title={`${p.label} — click to export PNG`}
          />
          <div style={{ fontSize: 10, color: 'var(--dim)' }}>{p.label}</div>
        </div>
      ))}
      <div style={{ width: '100%', fontSize: 11, color: 'var(--dim)' }}>
        UI graphics browser (read-only in this version — pic re-import lands with the VGAGRAPH writer). Click a pic to
        export it as PNG.
      </div>
    </div>
  );
}

export function GfxStudio() {
  useStoreVersion();
  const [tab, setTab] = useState('walls');
  const [sel, setSel] = useState(0);
  const assets = store.assets;
  if (!assets) return null;

  const numWalls = assets.numWalls;
  const numSprites = assets.numSprites;

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minHeight: 0 }}>
      <div style={{ display: 'flex', gap: 4, padding: 8, borderBottom: '1px solid var(--border)' }}>
        {['walls', 'sprites', 'pics'].map((t) => (
          <button
            key={t}
            className={tab === t ? 'active' : ''}
            onClick={() => {
              setTab(t);
              setSel(0);
            }}
          >
            {t.toUpperCase()}
          </button>
        ))}
        <div style={{ marginLeft: 'auto', color: 'var(--dim)', fontSize: 11, alignSelf: 'center' }}>
          {numWalls} wall chunks · {numSprites} sprites
        </div>
      </div>

      {tab === 'pics' ? (
        <PicsBrowser />
      ) : (
        <div style={{ flex: 1, display: 'flex', minHeight: 0 }}>
          <div
            style={{
              width: 340,
              overflowY: 'auto',
              padding: 8,
              display: 'flex',
              flexWrap: 'wrap',
              gap: 4,
              alignContent: 'flex-start',
              borderRight: '1px solid var(--border)',
            }}
          >
            {tab === 'walls'
              ? Array.from({ length: numWalls }, (_, i) => (
                  <Thumb
                    key={i}
                    canvas={assets.wallCanvasAt(i)}
                    label={`${i}${i % 2 ? ' D' : ' L'}${Math.floor(i / 2) < WALL_NAMES.length ? ` ${WALL_NAMES[Math.floor(i / 2)]}` : ''}`}
                    selected={sel === i}
                    onClick={() => setSel(i)}
                  />
                ))
              : Array.from({ length: numSprites }, (_, i) => (
                  <Thumb
                    key={i}
                    canvas={assets.spriteCanvasAt(i)}
                    label={SPRITE_NAMES[i] ? SPRITE_NAMES[i].replace('SPR_', '') : `spr ${i}`}
                    selected={sel === i}
                    onClick={() => setSel(i)}
                  />
                ))}
          </div>
          <div style={{ flex: 1, overflowY: 'auto' }}>
            {tab === 'walls' ? (
              assets.wallPixelsAt(sel) ? (
                <PixelEditor
                  key={`wall-${sel}`}
                  pixels={assets.wallPixelsAt(sel)}
                  transparent={false}
                  name={`wall_${sel}`}
                  onCommit={(px) => setWallPixels(sel, px)}
                />
              ) : (
                <div style={{ padding: 16, color: 'var(--dim)' }}>Empty chunk.</div>
              )
            ) : assets.spritePixelsAt(sel) ? (
              <PixelEditor
                key={`spr-${sel}`}
                pixels={assets.spritePixelsAt(sel)}
                transparent={true}
                name={SPRITE_NAMES[sel] ?? `sprite_${sel}`}
                onCommit={(px) => setSpritePixels(sel, px)}
              />
            ) : (
              <div style={{ padding: 16, color: 'var(--dim)' }}>Sprite not present in this data set.</div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
