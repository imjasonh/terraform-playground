// Boots the real game in the in-app js-dos playtest and captures frames.
// Requires a folder with the installed shareware (incl. WOLF3D.EXE), passed
// as WOLF_DIR. Usage: WOLF_DIR=/path/to/wolf3d node app/e2e/playtest.mjs
import { chromium } from 'playwright-core';
import fs from 'node:fs';
import path from 'node:path';

// Without WOLF_DIR, the bundled shareware demo (which includes the EXE) is used.
const WOLF_DIR = process.env.WOLF_DIR ?? null;
const OUT = process.env.TOUR_OUT ?? '/tmp/tour';
fs.mkdirSync(OUT, { recursive: true });

const browser = await chromium.launch({
  executablePath: process.env.CHROME_PATH ?? '/usr/local/bin/google-chrome',
  args: ['--no-sandbox'],
});
const page = await browser.newPage({ viewport: { width: 1600, height: 1000 } });
page.on('console', (m) => {
  if (m.type() === 'error') console.log('console-error:', m.text().slice(0, 200));
});
page.on('pageerror', (e) => console.log('pageerror:', String(e).slice(0, 200)));

await page.goto(process.env.SMOKE_URL ?? 'http://localhost:4173/');
await page.waitForSelector('text=WOLF3D EDITOR');

if (WOLF_DIR) {
  // Load a full game install (with EXE) through the file input.
  const files = fs.readdirSync(WOLF_DIR).map((f) => path.join(WOLF_DIR, f));
  await page.setInputFiles('input[type=file]', files);
  console.log('game folder loaded (incl. EXE)');
} else {
  await page.click('text=Open shareware demo');
  console.log('bundled demo loaded');
}
await page.waitForSelector('text=STATISTICS');

// Open playtest, boot warped to E1L1 on normal.
await page.click('button:has-text("Playtest")');
await page.waitForSelector('button:has-text("Boot game")');
const disabled = await page.$eval('button:has-text("Boot game")', (b) => b.disabled);
if (disabled) throw new Error('Boot disabled: EXE not detected');
await page.click('button:has-text("Boot game")');
console.log('booting...');

// Give DOSBox-WASM time to boot through signon/briefing into the level.
for (const [t, name] of [
  [9000, '15-playtest-boot'],
  [7000, '16-playtest-ingame'],
]) {
  await page.waitForTimeout(t);
  await page.screenshot({ path: `${OUT}/${name}.png` });
  console.log('shot', name);
}

// Play a little: walk forward, turn, open the elevator-area door (space).
const dosCanvas = await page.$('canvas');
if (dosCanvas) await dosCanvas.click();
await page.keyboard.press('Enter');
await page.waitForTimeout(1500);
await page.keyboard.down('ArrowUp');
await page.waitForTimeout(1600);
await page.keyboard.up('ArrowUp');
await page.keyboard.press('Space');
await page.waitForTimeout(900);
await page.keyboard.down('ArrowUp');
await page.waitForTimeout(1500);
await page.keyboard.up('ArrowUp');
await page.keyboard.down('ArrowLeft');
await page.waitForTimeout(500);
await page.keyboard.up('ArrowLeft');
await page.waitForTimeout(400);
await page.screenshot({ path: `${OUT}/17-playtest-playing.png` });
console.log('shot 17-playtest-playing');

await page.keyboard.down('ArrowUp');
await page.waitForTimeout(1500);
await page.keyboard.up('ArrowUp');
await page.keyboard.press('ControlLeft'); // fire
await page.waitForTimeout(600);
await page.screenshot({ path: `${OUT}/18-playtest-action.png` });
console.log('shot 18-playtest-action');

await browser.close();
console.log('playtest tour done');
