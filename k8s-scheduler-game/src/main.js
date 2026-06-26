// Entry point: build the game + UI and drive the clock.
//
// Two independent loops:
//   - a sim loop (setTimeout) that advances the simulation at
//     TICKS_PER_SECOND × speed when not paused;
//   - a render loop (requestAnimationFrame) that repaints only when the UI is
//     marked dirty, so it stays cheap and never fights an in-progress drag.

import { Game } from "./engine.js";
import { UI } from "./ui.js";
import { TICKS_PER_SECOND } from "./types.js";

const game = new Game("steady");
const ui = new UI(game);

// expose for debugging in the console
window.__kube = { game, ui };

function simLoop() {
  const s = game.state;
  if (!s.paused) {
    game.tick();
    ui.markDirty();
  }
  const delay = s.paused ? 120 : 1000 / (TICKS_PER_SECOND * s.speed);
  setTimeout(simLoop, delay);
}

function renderLoop() {
  ui.render();
  requestAnimationFrame(renderLoop);
}

ui.markDirty();
ui.render();
simLoop();
requestAnimationFrame(renderLoop);
