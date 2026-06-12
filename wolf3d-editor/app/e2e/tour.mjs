// Screenshot tour: captures every editor surface against the bundled demo.
// Usage: node app/e2e/tour.mjs (with `vite preview` running on :4173)
import { chromium } from 'playwright-core';
import fs from 'node:fs';

const OUT = process.env.TOUR_OUT ?? '/tmp/tour';
fs.mkdirSync(OUT, { recursive: true });

const browser = await chromium.launch({
  executablePath: process.env.CHROME_PATH ?? '/usr/local/bin/google-chrome',
  args: ['--no-sandbox'],
});
const page = await browser.newPage({ viewport: { width: 1600, height: 1000 } });
const shot = (name) => page.screenshot({ path: `${OUT}/${name}.png` });

await page.goto(process.env.SMOKE_URL ?? 'http://localhost:4173/');
await page.waitForSelector('text=WOLF3D EDITOR');
await shot('01-open-screen');

await page.click('text=Open shareware demo');
await page.waitForSelector('text=STATISTICS');
await page.waitForTimeout(400);
await shot('02-map-editor');

// Zoom in for detail and hover an enemy area.
for (let i = 0; i < 5; i++) await page.click('button:has-text("+")');
await page.waitForTimeout(300);
const canvas = await page.$('canvas');
const box = await canvas.boundingBox();
await page.mouse.move(box.x + 300, box.y + 300);
await page.waitForTimeout(200);
await shot('03-map-zoomed');

// Floor codes off (texture-pure view).
await page.click('button:has-text("FLR")');
await page.waitForTimeout(200);
await shot('04-map-no-floorcodes');
await page.click('button:has-text("FLR")');

// OBJ palette with enemy matrix controls.
await page.locator('button', { hasText: /^OBJ$/ }).last().click();
await page.waitForSelector('text=ENEMIES');
await page.click('button:has-text("hard")');
await page.click('button:has-text("patrol")');
await page.waitForTimeout(200);
await shot('05-obj-palette');

// Paint a few guards onto the map: select guard cell, stamp on floor tiles.
const guardCell = await page.$('[title*="Guard, patrolling"]');
if (guardCell) {
  await guardCell.click();
  // find open floor: click a few spots
  await page.mouse.click(box.x + 21 * 16, box.y + 30 * 16);
  await page.mouse.click(box.x + 23 * 16, box.y + 30 * 16);
}
await page.waitForTimeout(200);
await shot('06-painting-enemies');

// Boss level (E1L9) with skill filter.
await page.click('text=Wolf1 Boss');
await page.waitForTimeout(300);
await shot('07-boss-level');

// 3D preview: walk forward through the elevator door area.
await page.click('text=Wolf1 Map1');
await page.click('button:has-text("3D Preview")');
await page.waitForTimeout(600);
await shot('08-3d-start');
// Turn and walk.
await page.keyboard.down('KeyA');
await page.waitForTimeout(450);
await page.keyboard.up('KeyA');
await page.keyboard.down('KeyW');
await page.waitForTimeout(1400);
await page.keyboard.up('KeyW');
await page.waitForTimeout(200);
await shot('09-3d-walking');
await page.keyboard.down('KeyA');
await page.waitForTimeout(300);
await page.keyboard.up('KeyA');
await page.keyboard.down('KeyW');
await page.waitForTimeout(1200);
await page.keyboard.up('KeyW');
await page.waitForTimeout(200);
await shot('10-3d-corridor');

// Graphics studio: walls grid, sprite editor, pics browser.
await page.click('button:has-text("Graphics")');
await page.waitForSelector('text=wall chunks');
await page.waitForTimeout(400);
await shot('11-gfx-walls');
await page.click('button:has-text("SPRITES")');
await page.waitForTimeout(400);
// pick the guard sprite for the editor view
const grd = await page.$('[title*="GRD_S_1"]');
if (grd) await grd.click();
await page.waitForTimeout(300);
await shot('12-gfx-sprite-editor');
await page.click('button:has-text("PICS")');
await page.waitForTimeout(800);
await shot('13-gfx-pics');

// Playtest panel (guidance without an EXE).
await page.click('button:has-text("Playtest")');
await page.waitForTimeout(300);
await shot('14-playtest-panel');

await browser.close();
console.log('tour written to', OUT);
