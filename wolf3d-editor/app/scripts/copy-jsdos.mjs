// Copies the js-dos runtime (UMD bundle + DOSBox wasm) from node_modules into
// public/ so the playtest works fully self-hosted, with no CDN dependency.
import { cpSync, mkdirSync, existsSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const src = join(here, '..', 'node_modules', 'js-dos', 'dist');
const dest = join(here, '..', 'public', 'jsdos');

if (!existsSync(src)) {
  console.error('js-dos dist not found; run pnpm install first');
  process.exit(1);
}
mkdirSync(dest, { recursive: true });
cpSync(src, dest, { recursive: true });
console.log('copied js-dos runtime to public/jsdos');
