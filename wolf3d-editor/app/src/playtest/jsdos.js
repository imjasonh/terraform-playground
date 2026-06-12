// In-browser playtest: boots the user's own game EXE in js-dos (DOSBox
// compiled to WebAssembly), against the files the editor just compiled.
// The emulator runtime is loaded on demand from the js-dos CDN; the game
// files never leave the tab (the bundle is a local blob URL).

import { buildZip } from '../io/zip.js';

const JSDOS_JS = 'https://v8.js-dos.com/latest/js-dos.js';
const JSDOS_CSS = 'https://v8.js-dos.com/latest/js-dos.css';

let loaded = null;

/** Load the js-dos runtime once. */
function loadRuntime() {
  if (loaded) return loaded;
  loaded = new Promise((resolve, reject) => {
    const css = document.createElement('link');
    css.rel = 'stylesheet';
    css.href = JSDOS_CSS;
    document.head.appendChild(css);
    const script = document.createElement('script');
    script.src = JSDOS_JS;
    script.onload = () => {
      if (window.Dos) resolve(window.Dos);
      else reject(new Error('js-dos loaded but Dos() missing'));
    };
    script.onerror = () => reject(new Error('Could not load the js-dos runtime (offline? CDN blocked?)'));
    document.head.appendChild(script);
  });
  return loaded;
}

/**
 * Find the game executable among the loaded extras.
 * @param {Map<string, Uint8Array>} extras
 * @returns {string|null}
 */
export function findGameExe(extras) {
  for (const candidate of ['wolf3d.exe', 'spear.exe', 'sdm.exe', 'wolf.exe']) {
    if (extras.has(candidate)) return candidate;
  }
  for (const name of extras.keys()) {
    if (name.endsWith('.exe')) return name;
  }
  return null;
}

/**
 * Build a .jsdos bundle (a ZIP with .jsdos/dosbox.conf) containing the
 * compiled game files plus everything else from the user's game dir.
 * @param {{name: string, data: Uint8Array}[]} compiled
 * @param {Map<string, Uint8Array>} extras
 * @param {string} exe
 * @param {{tedlevel?: number, skill?: 'baby'|'easy'|'normal'|'hard'}} opts
 * @returns {Uint8Array}
 */
export function buildBundle(compiled, extras, exe, opts = {}) {
  const args = [];
  if (opts.tedlevel !== undefined) args.push('tedlevel', String(opts.tedlevel));
  if (opts.skill) args.push(opts.skill);
  const conf = [
    '[cpu]',
    'cycles=max',
    '',
    '[autoexec]',
    '@echo off',
    'mount c .',
    'c:',
    `${exe.toUpperCase()} ${args.join(' ')}`.trim(),
  ].join('\n');

  /** @type {{name: string, data: Uint8Array}[]} */
  const entries = [{ name: '.jsdos/dosbox.conf', data: new TextEncoder().encode(conf) }];
  const seen = new Set();
  for (const f of compiled) {
    entries.push({ name: f.name, data: f.data });
    seen.add(f.name.toLowerCase());
  }
  for (const [name, data] of extras) {
    if (seen.has(name)) continue;
    entries.push({ name: name.toUpperCase(), data });
  }
  return buildZip(entries);
}

/** @type {{stop: () => Promise<void>}|null} */
let instance = null;

/**
 * Boot a bundle into the given element. Returns a stop function.
 * @param {HTMLElement} el
 * @param {Uint8Array} bundle
 */
export async function bootBundle(el, bundle) {
  const Dos = await loadRuntime();
  await stopPlaytest();
  const ab = new ArrayBuffer(bundle.byteLength);
  new Uint8Array(ab).set(bundle);
  const url = URL.createObjectURL(new Blob([ab], { type: 'application/zip' }));
  el.innerHTML = '';
  const props = Dos(el, {
    url,
    autoStart: true,
    noCloud: true,
    kiosk: true,
  });
  instance = {
    stop: async () => {
      try {
        await props.stop?.();
      } catch {
        // ignore teardown races
      }
      URL.revokeObjectURL(url);
    },
  };
  return instance;
}

export async function stopPlaytest() {
  if (instance) {
    const i = instance;
    instance = null;
    await i.stop();
  }
}
