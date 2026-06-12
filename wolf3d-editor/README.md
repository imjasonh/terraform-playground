# Wolf3D Editor

A Wolfenstein 3D level editor and graphics studio, faithful to the original
game, running **entirely client-side in the browser**. Written in plain
JavaScript (ES modules + JSDoc); see [PLAN.md](./PLAN.md) for the full design.

## What works today

- **Codec** (`packages/codec`): byte-faithful readers/writers for the original
  formats — RLEW + Carmack compression, MAPHEAD/GAMEMAPS, VSWAP (walls,
  compiled sprites), Huffman + VGADICT/VGAHEAD/VGAGRAPH (pictable, pics,
  fonts), and the 256-color game palette. Verified by golden tests against
  the real shareware data files.
- **Catalogs** (`packages/data`): the complete vanilla tile/object reference —
  49 walls, 12 doors, floor codes 6B–8F, all 49 statics with pickup semantics,
  the full enemy spawn-code matrix (skill x mode x facing), bosses, ghosts,
  specials (pushwall, deaf tile, exit trigger, dead guard), engine limits and
  the per-level ceiling color table — all transcribed from the released
  WOLFSRC engine source.
- **Editor app** (`app`):
  - Open a game folder, individual files, or a mod ZIP (WL1/WL3/WL6/SOD);
    a bundled freely-distributable shareware demo loads with one click.
  - MapEdit-style dual-plane editing: LMB/RMB brushes, shift-click pick,
    object overlay, floor-code overlay with hex labels, skill-filter view,
    zoom, grid, undo/redo, level renaming.
  - Live stats dashboard with engine budgets (actors/statics/doors), kills /
    treasure / secrets per skill, ammo & health economy, floor-code usage,
    and validation issues (player starts, limit overruns).
  - **Graphics studio**: browse/edit wall textures and sprites with a
    palette-locked pixel editor, PNG export/import (quantized to the Wolf
    palette, index 255 transparency), VGAGRAPH pic browser with PNG export.
  - **3D preview**: instant software-raycast walkthrough of the current level
    (textured walls, billboard sprites, per-level ceiling colors).
  - **Playtest** (F5): boots your own game EXE in DOSBox-WASM (js-dos) with
    the freshly compiled files overlaid, warping straight to the current
    level via the engine's `TEDLEVEL` parameter at a chosen difficulty.
  - **Export**: compiles GAMEMAPS/MAPHEAD (and VSWAP when graphics were
    edited) and downloads a ready-to-run mod ZIP. Everything you load stays
    in the tab — there is no server.

## Develop

```sh
pnpm install
pnpm test          # codec golden tests + catalog tests
pnpm dev           # editor at http://localhost:5173
pnpm build         # static site in app/dist (deployable anywhere)
node app/e2e/smoke.mjs   # browser smoke test against `vite preview`
```

## Layout

- `packages/codec` — file-format codecs (no DOM dependencies)
- `packages/data` — game catalogs, limits, sprite manifest
- `app` — Vite + React editor application
- `fixtures/wl1` — shareware v1.0 data files used as golden-test fixtures
  (see `fixtures/wl1/PROVENANCE.md`)
- `PLAN.md` — full implementation plan and format/semantics reference
