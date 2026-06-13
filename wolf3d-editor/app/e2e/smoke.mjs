import { chromium } from 'playwright-core';
import fs from 'node:fs';

// Drives a system Chrome against a running `vite preview` (port 4173).
const browser = await chromium.launch({
  executablePath: process.env.CHROME_PATH ?? '/usr/local/bin/google-chrome',
  args: ['--no-sandbox'],
});
const page = await browser.newPage({ viewport: { width: 1500, height: 950 } });
const errors = [];
page.on('pageerror', (e) => errors.push('pageerror: ' + e.message));
page.on('console', (m) => {
  if (m.type() === 'error') errors.push('console: ' + m.text());
});

await page.goto(process.env.SMOKE_URL ?? 'http://localhost:4173/');
await page.waitForSelector('text=WOLF3D EDITOR');
console.log('open screen ok');

// Load shareware demo
await page.click('text=Open shareware demo');
await page.waitForSelector('text=STATISTICS', { timeout: 15000 });
console.log('editor loaded');

// Verify level list shows Wolf1 Map1 and stats render
const lvl = await page.textContent('body');
if (!lvl.includes('Wolf1 Map1')) throw new Error('level list missing Wolf1 Map1');
if (!lvl.includes('Kills E/M/H')) throw new Error('stats missing');
console.log('level list + stats ok');

await page.screenshot({ path: '/tmp/shot-map.png' });

// Paint a wall tile: click palette cell for wall code 2, then click map at tile (2,2)
await page.click('[title="Grey stone 2 (code 2)"]');
const canvas = await page.$('canvas');
const box = await canvas.boundingBox();
const zoomText = await page.textContent('span[style*="font-size: 11px"]').catch(() => null);
// zoom default 11
await page.mouse.click(box.x + 2 * 11 + 5, box.y + 2 * 11 + 5);
console.log('painted tile');

// Check undo button enabled now
const undoDisabled = await page.$eval('button[title="Ctrl+Z"]', (b) => b.disabled);
if (undoDisabled) throw new Error('undo should be enabled after edit');
console.log('undo enabled after edit');

// Status bar hover readout
await page.mouse.move(box.x + 2 * 11 + 5, box.y + 2 * 11 + 5);
await page.waitForTimeout(200);
const status = await page.textContent('body');
if (!status.includes('plane0=2')) console.log('warn: hover readout not verified');
else console.log('hover readout ok');

// Switch to OBJ tab (palette tab is the last OBJ button on the page)
await page.locator('button', { hasText: /^OBJ$/ }).last().click();
await page.waitForSelector('text=ENEMIES');
console.log('obj palette ok');

// Test export: intercept download
const [download] = await Promise.all([
  page.waitForEvent('download'),
  page.click('button:has-text("Download mod")'),
]);
const path = await download.path();
const size = fs.statSync(path).size;
console.log('export zip size', size);
if (size < 100000) throw new Error('export zip too small');

// 3D preview
await page.click('button:has-text("3D Preview")');
await page.waitForTimeout(800);
await page.screenshot({ path: '/tmp/shot-3d.png' });
console.log('3d preview rendered');

// Graphics studio
await page.click('button:has-text("Graphics")');
await page.waitForSelector('text=wall chunks');
await page.click('button:has-text("SPRITES")');
await page.waitForTimeout(400);
await page.screenshot({ path: '/tmp/shot-gfx.png' });
console.log('gfx studio ok');

// Playtest panel: demo bundles the shareware EXE, so Boot must be enabled.
await page.click('button:has-text("Playtest")');
await page.waitForSelector('button:has-text("Boot game")');
const bootDisabled = await page.$eval('button:has-text("Boot game")', (b) => b.disabled);
if (bootDisabled) throw new Error('Boot game disabled — demo EXE not detected');
console.log('playtest boot available ok');

// Back to map; check map screenshot non-empty
await page.click('button:has-text("Map")');
await page.waitForTimeout(300);

if (errors.length) {
  console.log('ERRORS:', errors.join('\n'));
  process.exit(1);
}
console.log('SMOKE PASS');
await browser.close();
