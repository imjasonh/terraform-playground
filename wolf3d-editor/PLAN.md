# Wolf3D Editor — Implementation Plan

A level editor and graphics studio for **Wolfenstein 3D** (1992), built to be *faithful to the
original game and to the editing culture that grew up around it*. The goal is not "a tile editor
inspired by Wolf3D" — it is an editor whose data model, constraints, vocabulary, and workflows are
exactly those of the original engine and of the classic community editors (MapEdit, WDC,
ChaosEdit, FloEdit, HWE), reading and writing the genuine game file formats.

Everything in this plan that describes engine behavior has been verified against id Software's
released source code ([`id-Software/wolf3d`](https://github.com/id-Software/wolf3d), `WOLFSRC/`),
primarily `WL_DEF.H`, `WL_GAME.C` (`ScanInfoPlane`, `SetupGameLevel`), `WL_ACT1.C` (`statinfo[]`,
doors, pushwalls), and `ID_CA.C` (file formats and compression). Citations to specific constants
and functions appear throughout.

---

## Table of Contents

1. [Vision and Fidelity Principles](#1-vision-and-fidelity-principles)
2. [Research Summary](#2-research-summary)
3. [Scope](#3-scope)
4. [Architecture and Technology](#4-architecture-and-technology)
5. [The Format Codec Library](#5-the-format-codec-library)
6. [The Project Model](#6-the-project-model)
7. [Map Editor — Detailed Specification](#7-map-editor--detailed-specification)
8. [Graphics Studio — Sprite, Texture, and UI Editing](#8-graphics-studio--sprite-texture-and-ui-editing)
9. [3D Preview and In-Browser Playtest](#9-3d-preview-and-in-browser-playtest)
10. [Validation Engine](#10-validation-engine)
11. [Testing Strategy](#11-testing-strategy)
12. [Milestones](#12-milestones)
13. [Risks and Mitigations](#13-risks-and-mitigations)
- [Appendix A — Wall Plane Codes (Plane 0)](#appendix-a--wall-plane-codes-plane-0)
- [Appendix B — Object Plane Codes (Plane 1)](#appendix-b--object-plane-codes-plane-1)
- [Appendix C — Engine Constants and Hard Limits](#appendix-c--engine-constants-and-hard-limits)
- [Appendix D — File Format Specifications](#appendix-d--file-format-specifications)
- [Appendix E — Per-Level Metadata Reference](#appendix-e--per-level-metadata-reference)
- [Appendix F — Keyboard Shortcuts](#appendix-f--keyboard-shortcuts)
- [Appendix G — Glossary](#appendix-g--glossary)

---

## 1. Vision and Fidelity Principles

### 1.1 What we are building

A **web app, entirely client-side** (plain JavaScript; Rust→WASM only where profiling proves
the need — §4.3), with full import/export of all game files and one-keystroke **in-browser
playtesting** of the compiled mod (§9.2). A single application with two halves:

1. **Map editor** — edits the 64×64, two-plane tile maps stored in `GAMEMAPS.*`/`MAPHEAD.*`,
   with the complete catalog of wall tiles, doors, floor codes, decorations, power-ups, keys,
   enemies (with difficulty and facing variants), patrol control points, and special markers
   exactly as the original engine consumes them.
2. **Graphics studio** — edits the game's actual art assets in place: wall textures and object
   sprites in `VSWAP.*`, and UI graphics (status bar, menus, fonts, title screens, intermission
   art) in `VGAGRAPH.*`, all in the game's fixed 256-color palette.

The output is **the game's own files**. A level made in this editor runs in vanilla DOS
Wolfenstein 3D v1.4 under DOSBox, and in Wolf4SDL/ECWolf, with no conversion step.

### 1.2 Fidelity principles (in priority order)

1. **The engine is the spec.** Where editors and folklore disagree, `WOLFSRC` decides. Every tile
   code, limit, and behavior the editor exposes maps 1:1 to a `case` label or constant in the id
   source. The editor must not invent abstractions that can express states the engine cannot load.
2. **Vanilla limits are enforced by default.** 64×64 maps, 149 enemies, 399 statics, 64 doors,
   wall codes < 64, floor codes 107–143. A "source port" profile may relax limits, but the default
   profile is DOS v1.4 and the validator treats its limits as errors.
3. **The editing vocabulary is the community's.** "Floor codes," "deaf guard," "pushwall,"
   "turning points," "ambush tile," "fake elevator" — the UI uses the names mappers have used
   since 1992 (MapEdit's legend), so 30 years of tutorials and design lore apply directly.
4. **Round-trip safety.** Opening and saving a file without edits must preserve it byte-for-byte
   at the *decompressed plane* level, and must never corrupt untouched levels or chunks. The
   editor is trusted with people's only copy of a 1994 mod.
5. **No id assets ship with the editor.** Users point the editor at their own game files
   (the shareware episode `*.WL1` is freely distributable and makes a zero-cost starting point).
   The "built-in catalog" is *metadata* — names, codes, behaviors, editor symbols — which the
   editor joins with textures/sprites loaded from the user's `VSWAP`.

### 1.3 Non-goals

- Not a general raycaster engine editor (no ROTT, no Blake Stone in v1 — though the formats are
  close and §3.2 reserves room).
- Not a source-code modding IDE. We edit data files only; per-level EXE tables (ceiling color,
  par time, music) are surfaced as reference data and exported for source-port configs, but we do
  not patch executables in v1.
- Sound/music editing (`AUDIOT`, digitized sounds in `VSWAP`) is a stretch milestone, not core.

---

## 2. Research Summary

### 2.1 How the original game stores a level (verified against `ID_CA.C`, `WL_GAME.C`)

- Every level is a **64×64 grid** of 16-bit values in **two planes** (a third plane slot exists in
  the headers; Wolf3D always loads exactly two and hard-codes 64×64 regardless of the header's
  width/height fields):
  - **Plane 0 ("walls"/MAP plane):** solid walls (codes 1–63 conceptually, 1–49 textured in
    vanilla), doors (90–101), the ambush/"deaf guard" tile (106), and floor/area codes (107–143).
  - **Plane 1 ("objects"/OBJ plane):** player starts (19–22), all decorations/items/power-ups
    (23–70 (+71)), patrol turning points (90–97), the pushwall marker (98), the victory-walk
    trigger (99), dead guard (124), and all enemies with difficulty/facing encoded in the code
    number (108–259).
- Levels live in `GAMEMAPS.ext`; a companion `MAPHEAD.ext` holds the RLEW tag (0xABCD) and 100
  level-offset slots. Planes are compressed with **RLEW**, then (v1.1+) **Carmack** compression.
  Full format specs in Appendix D.
- At load, `SetupGameLevel` turns plane-0 values < 107 into solid `tilemap` entries, spawns doors
  for 90–101, replaces ambush tiles (106) with a neighboring area code, and `ScanInfoPlane` walks
  plane 1 to spawn the player, statics, and actors. Difficulty filtering happens at spawn time via
  the code ranges (easy +0, medium +36, hard +72; mutants +18/+36 — see Appendix B).

### 2.2 Survey of classic editors (what "faithful" means in practice)

| Editor | Era | What it got right (we copy) | What it lacked (we fix) |
|---|---|---|---|
| **TED5** | id internal | The actual tool Wolf3D was made with: plane chunks, carmackizing, `MAPTEMP`/`GAMEMAPS` pipeline | Never public; platformer-oriented UI |
| **MapEdit 8.5** | DOS, 1992–94 | The canonical community workflow: single window, both planes overlaid, LMB/RMB tile assignment, floor-code tools (`Z`/`Alt+Z`/`Ctrl+Z` fills), statistics (`Alt+S`), legend driven by `MAPDATA`/`OBJDATA` definition files | No undo, no zoom, DOS-only, no graphics editing |
| **WDC** (Wolf3D Data Compiler) | Win, 2003+ | Project model (base data folder → output folder, "Compile All"), all-in-one (maps + graphics + sounds), map error checking, multi-format import/export | Small editing viewport; Windows-only |
| **ChaosEdit** | Win, 2002–07 | Real-time **3D preview** walk-through; all-in-one editing incl. VSWAP/VGAGRAPH | **No undo**; stability issues; abandoned |
| **FloEdit / FloEdit II** | Win | All-in-one editing; early Windows standard | Installer/64-bit issues; abandoned |
| **HWE** (Havoc's Wolf3D Editor) | Win, 2011–19 | "Modern MapEdit": zoom, undo/redo, search & replace tiles/objects, per-object statistics, copy/paste with flip/rotate, multi-game support, import/export of other editors' map formats | Map-only (no graphics editing) |

**Synthesis:** our editor is *MapEdit's editing model* + *WDC's project rigor and checks* +
*ChaosEdit's 3D preview* + *HWE's modern conveniences (undo, zoom, search/replace)* + a graphics
studio that replaces the separate VSWAP/VGAGRAPH tools (VSWAPED, WolfEdit, SpriteMaker).

### 2.3 Community design canon we encode as tooling

The mapping lore captured in B.J. Rowan's design guide, Warren Buss's MapEdit team notes, and the
DieHard Wolfers-era "Level Design Bible" is not just advice — much of it is *engine behavior*:

- **Floor codes are sound zones.** All guards on the same area code hear a shot anywhere on that
  code; open doors temporarily join two codes (`areaconnect` in `WL_ACT1.C`). The editor must make
  floor-code structure a first-class, visible, paintable layer with statistics — exactly like
  MapEdit's `F` toggle and `Alt+S` display.
- **Code 106 (hex 6A) is the deaf-guard tile**; **code 107 (hex 6B) doubles as the
  secret-elevator floor** (`AMBUSHTILE`, `ALTELEVATORTILE` in `WL_DEF.H`). Both get dedicated
  affordances and validator rules (e.g. "deaf code adjacent to a door makes the door invisible").
- **Secret rooms must share the entry room's floor code** or guards inside become statues.
- **Pushwalls travel 2–3 tiles nondeterministically**; secret areas must work for both.
- These and ~20 more rules form the validator rule set in §10.

---

## 3. Scope

### 3.1 Supported games and versions (v1)

| Target | Extensions | Levels | Notes |
|---|---|---|---|
| Wolfenstein 3D shareware | `.WL1` | 10 | Free to obtain; default onboarding path; v1.0 files are RLEW-only (no Carmack) — must detect |
| Wolfenstein 3D registered | `.WL3` (early 3-episode), `.WL6` | 30 / 60 | Primary target, v1.4 Apogee & GT/id releases (chunk counts differ slightly in `VGAGRAPH`) |
| Spear of Destiny + demo | `.SOD`, `.SDM` | 21 / 2 | Same engine; different object codes for bosses/statics (Appendix B includes the SOD deltas), different `VGAGRAPH` manifest |

### 3.2 Architecture allowance for later targets

Catalogs (tile/object/sprite/pic manifests) and limits are **data, not code** — JSON manifests
keyed by game profile. Blake Stone / Corridor 7 / Noah's Ark (HWE's list) become possible later by
adding manifests, without touching the editor core.

### 3.3 Deliverables

1. `packages/codec` — dependency-free JavaScript library: every file format, fully tested.
2. `packages/data` — game profiles: tile/object catalogs, sprite/pic manifests, palette, limits,
   per-level metadata reference tables (Appendices A–E rendered to JSON).
3. `app` — the editor application (map editor, graphics studio, 3D preview, **in-browser
   playtest**, validator).
4. Documentation: user guide written in community vocabulary, plus "format notes" derived from
   this plan.

---

## 4. Architecture and Technology

### 4.1 Platform decision: a web app, entirely client-side

**JavaScript + Vite SPA. No backend, no server-side processing of any kind. Files never leave
the machine.**

Rationale:

- **Licensing safety**: game data is loaded by the user from their own disk (File System Access
  API with drag-and-drop / file-input fallback); we never host or transmit id's assets. All
  parsing, editing, compiling, and playtesting happens in the browser tab.
- **Distribution**: a static site fits this repository's existing Cloudflare Worker deployment
  patterns (`cf-worker/`); zero-install matches the "double-click MAPEDIT.EXE" spirit. The app
  works offline once loaded (installable PWA with a service-worker cache).
- **Capability**: a 64×64 grid, 4096-byte textures, and a 320×200 raycaster are trivially within
  Canvas2D/WebGL budgets. ChaosEdit did the 3D preview on 2002 hardware.
- **Playtesting stays in the tab too** (§9.2): the compiled mod boots in an embedded
  DOSBox-compiled-to-WASM emulator running the user's own game executable — the real game, in
  the browser, against the files the editor just wrote.

### 4.2 Technology choices

| Concern | Choice | Notes |
|---|---|---|
| Language | **JavaScript** (modern ES modules) with JSDoc annotations + `// @ts-check` | Typed-array-heavy code is plain JS; JSDoc gives editor/CI type checking with no compile step. Rust→WASM only where profiling demands it (§4.4) |
| Build | Vite + pnpm workspaces | `packages/codec`, `packages/data`, `app` |
| UI framework | React | Panels/palettes/dialogs are form-heavy; map canvas and pixel editors are raw `<canvas>` components with imperative drawing, React only orchestrates |
| State | Single immutable document store (Zustand or hand-rolled reducer) | Undo/redo = snapshot stack of structural-shared plane arrays; `Uint16Array` copy of one plane is 8 KB — snapshotting is cheap, no need for cleverness |
| Map rendering | Canvas2D, dirty-rect repaint | 64×64 tiles at up to 32 px/tile; full repaint is already fast, dirty-rects keep it instant |
| 3D preview | Software raycaster rendered to a 320×200 (configurable) offscreen buffer, blitted scaled | Faithful by construction: same DDA algorithm class as `WL_DRAW.C`, dark/light wall faces, solid floor/ceiling colors |
| **In-browser playtest** | js-dos (DOSBox→Emscripten/WASM) booting the user's game EXE against the compiled output files | The genuine engine, zero behavioral approximation; see §9.2 |
| Pixel editing | Canvas2D with integer zoom (×1–×16), checkerboard for color 255 | |
| Persistence | Project folder on disk via File System Access API; OPFS/IndexedDB mirror + plain download/upload where FS Access is unavailable; project metadata in `project.json` | See §6 |
| Import/export | Per-file and whole-mod ZIP import/export, PNG sheets, JSON levels | See §6.3 |
| Tests | Vitest; golden-file round-trip tests against shareware data (see §11.4) | |

### 4.3 JavaScript first; Rust + WASM only where measured

Default to plain JavaScript everywhere. The workloads here are small: a map plane is 8 KB, a
wall texture 4 KB, the whole WL6 `VSWAP` ~1.5 MB — codecs, compressors, and the validator are
microseconds-to-milliseconds in JS with typed arrays. A Rust→WASM module is introduced **only**
when profiling on real data shows JS missing an interactivity budget, behind the same interface
so it is a drop-in. Pre-identified candidates, in likelihood order:

1. **Palette quantization + ordered dithering** of large imported images (multi-megapixel PNG →
   indexed 256) — the only plausibly heavy hot loop.
2. **Raycaster inner loop** at large canvas sizes (only if the faithful 320×200 buffer is ever
   raised significantly).
3. Carmack/RLEW/Huffman batch recompression of a whole 100-level set — almost certainly fine in
   JS; listed for completeness.

(The playtest emulator is third-party WASM already — that does not count against this rule.)

### 4.4 Module map

```
wolf3d-editor/
  packages/
    codec/            # binary formats, zero deps, plain JS (+ JSDoc types)
      src/maphead.js      # MAPHEAD read/write
      src/gamemaps.js     # GAMEMAPS level dir + plane chunks
      src/rlew.js         # RLEW expand/compress
      src/carmack.js      # Carmack expand/compress (near/far/escape)
      src/vswap.js        # VSWAP chunk dir, wall pages, sound pages
      src/sprite.js       # compiled-sprite decode/encode (posts)
      src/huffman.js      # VGADICT Huffman expand/compress
      src/vgagraph.js     # VGAHEAD/VGAGRAPH chunks, pictable, fonts, pics
      src/palette.js      # Wolf palette, nearest-color, remap tables
    data/
      profiles/wl6.json   # catalogs+limits per game (also wl1, wl3, sod, sdm)
      symbols/            # MapEdit-style editor glyphs (our own pixel art)
    app/
      src/document/       # project model, undo, dirty tracking
      src/io/             # FS Access / OPFS / upload-download, ZIP import-export
      src/mapeditor/      # canvas views, tools, palettes, stats
      src/gfxstudio/      # texture/sprite/pic/font editors
      src/preview3d/      # raycaster
      src/playtest/       # embedded DOSBox-WASM harness (§9.2)
      src/validate/       # rule engine (§10)
    quant-wasm/       # (only if §4.3 triggers) Rust image-quantization module
```

---

## 5. The Format Codec Library

Every format below is specified fully in Appendix D; this section defines the engineering
contract.

### 5.1 Codecs to implement

| Codec | Read | Write | Notes |
|---|---|---|---|
| `MAPHEAD.*` | ✔ | ✔ | RLEW tag + 100 `int32` offsets (0/-1 = empty slot); preserve trailing `tileinfo` bytes if present |
| `GAMEMAPS.*` | ✔ | ✔ | `TED5v1.0` signature; per-level 38-byte header (3 plane offsets, 3 plane lengths, width, height, 16-char name); detect & support `MAPTEMP` (uncarmackized) variant |
| RLEW | ✔ | ✔ | Word-level RLE with tag from MAPHEAD (0xABCD) |
| Carmack | ✔ | ✔ | Near (0xA7) / far (0xA8) references, count-0 escape for literal words with A7/A8 high bytes |
| `VSWAP.*` | ✔ | ✔ | Chunk directory (u32 offsets + u16 lengths), `spriteStart`/`soundStart` partitions; walls = raw 64×64 column-major; sprites = compiled posts; sounds = raw PCM pages + trailing info table (preserved verbatim in v1) |
| Compiled sprites | ✔ | ✔ | Decode to 64×64 indexed bitmap with transparency; encode back to minimal post representation (§8.3) |
| `VGADICT`/`VGAHEAD`/`VGAGRAPH.*` | ✔ | ✔ | Huffman tree (255 nodes), 3-byte offsets (0xFFFFFF = sparse), `STRUCTPIC` pictable, fonts, pics (VGA-planar "munged" layout), TILE8, endscreens, demos |
| Palette | ✔ | n/a | Canonical 256-entry Wolf palette as data; 6-bit VGA → 8-bit conversion; color 255 = transparent key |

### 5.2 Contracts

- **Lossless model:** `decode(bytes) -> Model` and `encode(Model) -> bytes` with the invariant
  `decode(encode(m)) deep-equals m` for all valid models (property-tested with random planes).
- **Decompressed-identity round trip:** for every level in a real `GAMEMAPS`,
  `expand(compress(expand(chunk))) === expand(chunk)` byte-for-byte. We do *not* promise
  byte-identical recompressed streams (TED5's compressor made different legal choices), but the
  game and all classic editors must accept our output. Our Carmack/RLEW compressors use greedy
  longest-match, which empirically meets or beats TED5 sizes.
- **Preservation:** chunks the user never touched are written back from the original bytes, not
  re-encoded (protects unknown/nonstandard chunks in old mods and keeps diffs minimal).
- **Version detection:** v1.0 maps (RLEW-only) detected per ModdingWiki rule — if the first
  u16 of a plane chunk equals the expected decompressed size (8192), no Carmack layer; also
  honored per-file via profile override.
- **Hard validation at the boundary:** corrupt offsets, overlapping chunks, and truncated planes
  produce structured errors with file/level/plane coordinates, never crashes.

---

## 6. The Project Model

### 6.1 Two workflows, both faithful

1. **Direct mode (MapEdit-style).** Open a game directory; edit `GAMEMAPS.WL6` in place; `Ctrl+S`
   writes it back (with automatic timestamped `.BAK` of every touched file, configurable count).
   This is how 1990s mappers worked and it must feel exactly that immediate.
2. **Project mode (WDC-style).** A project folder references a **base data folder** (pristine
   game files) and an **output folder**. Edits accumulate in the project; **Compile** (F10)
   produces the output game files. Non-destructive, diff-able, and the right model for "total
   conversion" mods. `project.json` records: game profile, base folder fingerprint (hashes),
   per-level dirty state, palette overrides, and graphics replacements (stored as PNG + metadata
   for git-friendliness, compiled to VSWAP/VGAGRAPH on output).

### 6.2 The document model

```js
/**
 * @typedef {Object} LevelDoc
 * @property {string} name           // 16 bytes, NUL-padded, from GAMEMAPS header
 * @property {Uint16Array} plane0    // 64*64 wall plane
 * @property {Uint16Array} plane1    // 64*64 object plane
 * @property {Uint16Array} [plane2]  // preserved if present (some mods use it); hidden unless enabled
 *
 * @typedef {Object} GameDoc
 * @property {GameProfile} profile   // wl1 | wl3 | wl6 | sod | sdm (catalogs, limits, manifests)
 * @property {(LevelDoc|null)[]} levels  // 100 slots, faithful to MAPHEAD
 * @property {VSwapDoc} vswap        // walls[], sprites[], sounds[] (sounds opaque in v1)
 * @property {VgaGraphDoc} vgagraph  // pictable, pics[], fonts[], tile8, endscreens, demos
 */
```

- **Undo/redo:** unlimited, per-document, grouped by gesture (a drag-paint is one undo step; a
  flood fill is one step). Map edits, graphics edits, and level-list operations all go through the
  same command bus. (This is the single biggest fix over MapEdit/ChaosEdit, and matches HWE.)
- **Level-list operations** (faithful to what mappers actually do): insert/delete/duplicate/move
  level slots, import/export single levels as standalone files (MapEdit floor-file interchange,
  plus our own JSON), copy a level between two open projects.

### 6.3 Import and export (everything in, everything out)

Because the app is entirely client-side, file exchange is a first-class feature, not an
afterthought. Every artifact the editor can hold can be brought in and taken out:

**Import:**
- A whole game/mod directory (File System Access directory picker) or a dropped set of files;
  a dropped **ZIP of a mod** is unpacked in-memory and opened directly (most classic mods are
  distributed exactly this way).
- Individual game files (`GAMEMAPS.*`+`MAPHEAD.*`, `VSWAP.*`, `VGAGRAPH.*`+`VGAHEAD.*`+
  `VGADICT.*`), auto-detected by name/extension/contents and merged into the open project.
- Single levels: MapEdit floor files, our JSON level format, and (M8) ChaosEdit/WDC/HWE map
  exports.
- Graphics: PNG/BMP (palette-quantized on entry) for any wall, sprite, pic, font glyph sheet.

**Export:**
- **"Download mod"** — one click produces a ZIP containing the complete compiled file set
  (`GAMEMAPS`/`MAPHEAD`/`VSWAP`/`VGAGRAPH`/`VGAHEAD`/`VGADICT` with correct extensions), ready
  to drop into a game directory, distribute, or feed back into this editor. This works even in
  browsers without File System Access (plain `<a download>`).
- Any individual compiled file; any single level as MapEdit floor file or JSON; any graphic or
  full sheet as PNG (×1/×4/×8); the validator report as Markdown; the project itself as a
  portable `.wolfproj.zip` (project.json + PNG-form graphics + level JSONs — the git-friendly
  form).
- Where File System Access is available, "save in place" and "compile to folder" write directly
  to disk; otherwise the editor mirrors state to OPFS/IndexedDB between sessions and exports via
  download.

---

## 7. Map Editor — Detailed Specification

### 7.1 Screen layout (MapEdit's, modernized)

- **Center: the map canvas.** 64×64 grid, zoom ×4–×32 px/tile (mouse wheel; `+`/`-`), optional
  grid lines, optional 8×8 sector guides. Scroll by drag (space/middle-button) — but at default
  window sizes the whole map fits, like MapEdit's full-screen view.
- **Right: the tile list**, with the two classic tabs:
  - **MAP** — walls 1–49 (rendered live from the loaded VSWAP, light face), doors 90–101 (icon +
    orientation), ambush 106, floor codes 107–143 (rendered as MapEdit-style hex-labeled color
    chips `6B`–`8F`).
  - **OBJ** — every object code from Appendix B, organized in the canonical groups: Start
    positions, Decorations, Treasure, Health/Ammo/Weapons, Keys, Guards (with
    difficulty/direction selectors), Bosses, Specials (turn points, pushwall, dead guard,
    victory trigger). Icons are the actual sprites from VSWAP, with our own arrow/glyph overlays
    for direction and special markers (we draw our own symbol set in `packages/data/symbols`,
    visually in the spirit of MapEdit's legend).
- **Bottom strip:** LMB/RMB assignments (see 7.2), hover readout — `(x, y)` plus the **exact
  plane values in decimal and hex** of both planes under the cursor and their catalog names, e.g.
  `(12,34)  P0: 0x6C (108) Floor code 6C   P1: 0x2E (46) Basket`.
- **Left (collapsible):** level list (100 slots with names and "in use" state), statistics panel,
  validator results.

### 7.2 The two-button editing model (faithful MapEdit behavior)

- Any tile-list entry can be **assigned to the left or right mouse button** (left-click assigns
  LMB; right-click assigns RMB — and `Shift`+hover "pick from map" works like MapEdit's RMB
  pickup). Painting on the canvas stamps the assigned code into the appropriate plane.
- Wall/door/floor entries write plane 0; object entries write plane 1; the canvas shows both
  planes composited (objects drawn over floors/walls). `O` toggles object display, `F` toggles
  floor-code display — same keys as MapEdit.
- **Clear level (`C`)** prompts and fills the map with the RMB assignment inside a configurable
  border (default: 1-tile Grey stone 1 ring, matching MapEdit's behavior of always keeping the
  rim solid).

### 7.3 Tools

| Tool | Behavior |
|---|---|
| Stamp/paint | Default; drag to paint; one undo step per drag |
| Line / Rect outline / Rect fill | With live preview; respects plane of assigned code |
| Flood fill | Bounded fill on matching code (plane-aware) |
| **Floor-code room fill (`Z`)** | MapEdit semantics: fills the room's open area with the selected floor code, *preserving* deaf-guard (106→under-actor) tiles; `Alt+Z` includes them; `Ctrl+Z` extends through pushwall-marked walls into secret areas |
| Select / copy / cut / paste | Rectangular, both planes or single plane; paste preview ghost; **flip horizontal/vertical and rotate 90°** with correct remapping of directional codes (player starts, enemy facings, turn-point arrows, door orientations swap 90↔91 etc.) — this remap table is part of the catalog data |
| Find & replace | "Replace wall 24 with 26 in this level / all levels"; works for any code, with count preview (HWE feature) |
| Measure | Tile distance readout (useful for pushwall clearance and patrol timing) |

### 7.4 Floor codes as a first-class layer

- Distinct pastel color per area code (deterministic), with hex label at zoom ≥ 16 px.
- **Sound-zone inspector:** click a floor code chip → all tiles of that code highlight, plus a
  list of all enemies standing on it ("who hears a shot fired here"), with deaf guards shown
  struck-through. This turns the most error-prone Wolf3D concept into something visible.
- Per-code usage counts in the statistics panel (MapEdit `Alt+S` parity).
- Dedicated chips for **106 deaf** ("gray X", placed *under* an enemy) and **107 secret
  elevator**, labeled with their lore names.

### 7.5 Difficulty and facing — the enemy placement UX

The encoding (easy +0 / medium +36 / hard +72; mutants +18/+36; facings E/N/W/S; stand vs.
patrol) is faithful but hostile to memorize, so:

- The OBJ palette shows one entry per enemy *type*; a persistent toolbar selects
  **Skill (Easy/Medium/Hard)**, **Mode (Standing/Patrolling)**, **Facing (E/N/W/S)** — the editor
  computes the code (and shows it: "SS, patrol, west, hard → **204**").
- On canvas, enemies render as their sprite + facing arrow, tinted by skill (the classic
  convention: white/easy, yellow/medium, red/hard), with patrol shown by a motion chevron.
- **Difficulty filter** view menu: show the level as it spawns on Easy / Medium / Hard ("Can you
  beat this floor on easy? There are 0 guards"). The stats panel counts kills/treasure per skill,
  reproducing what `Tab+C` debug shows in-game.
- **Patrol path tracing:** selecting a patrolling enemy traces its path (straight until wall or
  turn-point 90–97, turning as the arrows dictate, including diagonals), drawing the route and
  flagging dead-ends — making turn-point debugging visual instead of trial-and-error.

### 7.6 Doors, pushwalls, elevators — special-case affordances

- **Doors:** placing a door auto-picks orientation (90 vs 91 etc.) from neighboring walls, with
  manual override. Door rendering shows lock color (gold 92/93, silver 94/95, the unused
  lock3 96/97 / lock4 98/99 pairs available but flagged "not used by vanilla assets") and
  elevator doors 100/101.
- **Pushwall (object 98):** rendered as the wall texture + "secret" badge. Placement requires a
  wall under it; the editor shades the 2–3 tile travel corridor and the destination cells, and the
  validator enforces clearance (§10).
- **Elevator:** wall 21 is the live switch (E/W faces only — the engine only accepts USE from
  east/west, `WL_AGENT.C Cmd_Use`); wall 22 is the thrown/fake switch; wall 13 the level-entrance
  door; floor 107 under the player when the switch is thrown routes to the secret floor. The
  editor offers a one-click **"stamp elevator room"** scaffold (the canonical 3×3: entrance, rails,
  switch) exactly as it appears throughout the original episodes.

### 7.7 Statistics panel (MapEdit `Alt+S` + HWE object counts)

Live counts: enemies by type×skill, statics by category, treasure total and point value, ammo
units, health units, doors, pushwalls (= secret count), floor-code usage table, actor/static/door
budget bars against the vanilla limits (Appendix C). Boss levels show the "no ratios on boss
levels" note, faithful to `WL_INTER.C` behavior.

### 7.8 Map metadata

- Level **name** (16-byte header field; shown in editors/TED5, not in-game).
- Read-only reference (per game profile): episode/floor mapping, **par time**, **ceiling color**
  (with swatch — Appendix E), **music track**, secret-level return floor (`ElevatorBackTo[]`).
  Under a source-port profile these become editable and export to ECWolf `MAPINFO`.

---

## 8. Graphics Studio — Sprite, Texture, and UI Editing

One pixel-editing core, four asset views. All drawing happens in **indexed color against the
game's palette** — the editor never works in RGB internally, so output is exact by construction.

### 8.1 Shared pixel editor core

- Tools: pencil, line, rectangle (outline/fill), ellipse, flood fill, color picker (`Alt`),
  rectangular & lasso select, move/duplicate selection, flip/rotate, shift-wrap (critical for
  making walls tile), brush sizes 1–4.
- **Palette panel:** the 256-color Wolf palette laid out in its 16×16 grid; index readout in
  hex/dec; the palette's structured runs (each hue is a 8/16-step ramp) exposed as "ramps" so
  shading uses the same ramp stepping id's artists used. **Color 255 (`0xFF`, magenta) is the
  sprite transparency key** — rendered as checkerboard in sprite/pic contexts, paintable as a
  color only in wall context (walls have no transparency).
- **Remap tool:** select a region + source ramp → target ramp (the classic "recolor uniforms"
  technique used for guard variants).
- Import: PNG/BMP drag-in with nearest-color quantization to the palette (with optional ordered
  dithering), size enforcement per asset type. Export: PNG at ×1/×4/×8, and full sheet exports
  (all walls, all sprites in a grid) for external editing round trips — the "extract all" workflow
  the community built standalone tools for.
- Undo/redo integrated with the global stack; edits mark the owning chunk dirty for compile.

### 8.2 Wall texture editor (`VSWAP` walls)

- Catalog view of all wall chunks with names from Appendix A; vanilla WL6 has 106 wall chunks:
  49 texture **pairs** (light/dark) + 8 door textures.
- **Pair-aware editing:** each wall code = chunk `(code−1)×2` (light, drawn on N/S faces) and
  `(code−1)×2+1` (dark, E/W faces). The editor edits the light face and offers
  **auto-derive dark face** via the palette-ramp darkening map (one ramp step down, the same
  relationship the original pairs have), with manual override — pairs can also be unlinked.
- **Tiling preview:** live 2×2 / 4-directional wrap preview, plus an in-context preview rendering
  the texture on a wall corner in the 3D previewer.
- Door textures (door faces, door jamb/track, locked door, elevator door — light/dark each)
  edited the same way, labeled by their engine roles (`DOORWALL+0..7`).
- New walls can be appended up to the vanilla ceiling (wall codes ≤ 63 → at most 14 more pairs
  beyond 49; the editor enforces the `MAXWALLTILES 64` boundary and explains it).

### 8.3 Sprite editor (`VSWAP` sprites) — items, enemies, weapons

- Sprite browser grouped by the manifest (generated from the `WL_DEF.H` sprite enum): statics
  (`SPR_STAT_0..47`), guard/officer/SS/dog/mutant/boss animation sets, weapon hands
  (`SPR_KNIFEREADY..`), BJ victory run, etc. Each entry shows its linkage to object codes ("this
  is what object 50 looks like").
- Editing surface: 64×64 indexed bitmap with color-255 transparency, previewed over selectable
  floor colors (the actual per-level ceiling colors from Appendix E) and at in-game scales.
- **Animation preview:** the manifest carries frame sequences and 8-rotation sets for actors;
  the editor plays walk/pain/shoot/die cycles and provides onion-skinning and copy-between-
  rotations. (Faithfulness detail: rotation 0 faces the viewer; rotations proceed clockwise as in
  `WL_DRAW.C CalcRotate`.)
- **Post compiler:** on save, bitmaps are compiled to the engine's sprite format — left/right
  extent, per-column post lists of opaque spans, shared pixel pool (Appendix D.4) — using a
  minimal-posts encoder. The packed size is shown against the **4096-byte page budget**, with a
  warning band as it approaches the limit (vanilla's page manager assumptions; oversized sprites
  are the classic SpriteMaker failure mode). Decode(encode) round-trip is pixel-exact.
- Sprite count changes (adding frames) update the VSWAP directory and `spriteStart`/`soundStart`
  partitions correctly; the validator cross-checks manifest-expected counts.

### 8.4 UI graphics editor (`VGAGRAPH` pics) — menus, status bar, intermissions

- Chunk browser from the per-version pic manifest (generated from id's `GFXV_WL6.H` /
  `GFXV_WL1.H` / `GFXV_SOD.H` enums): title screen (320×200), PG-13 plaque, main-menu art,
  options screens, **status bar (320×40)** with its sub-elements (BJ face states 24×32 ×3 frames
  ×7 health bands + dead/god, weapon icons 48×24, key icons 8×16, number font), Get Psyched
  (224×48), intermission/victory art, episode select thumbnails, "Read This" pages.
- Dimensions come from the **pictable** (`STRUCTPIC` chunk 0); the editor treats dimensions as
  fixed under the vanilla profile (draw coordinates live in the EXE) and surfaces a warning if a
  source-port profile resizes.
- Pics are stored VGA-planar ("munged" 4-plane interleave); the codec de-munges to linear
  bitmaps and back (Appendix D.5).
- **Context preview:** status-bar elements preview composited into the real status bar; menu art
  previews over the menu background fill color; face frames play their idle animation.
- **Font editor** for the two proportional fonts (`STARTFONT` chunks): per-glyph width table,
  height, 256 glyph slots, byte-per-pixel glyph data; live "quick brown fox" preview in menu
  red/gray; used by Read-This and menus.
- **TILE8** (8×8 tile array chunk) grid editor (used for small in-game icons).
- **Endscreens** (80×25 text-mode B800 screens shown on exit) get a character/attribute editor
  with CP437 picker — a small thing, but a beloved authentic artifact. Demos (`T_DEMO0..3`) are
  preserved opaquely, with a "demos likely desync after map edits" advisory and a strip/replace
  action.

### 8.5 What "power-ups" means concretely (and how the studio reinforces the map editor)

Every bonus object in Appendix B (`bo_*` semantics from `WL_ACT1.C`/`WL_AGENT.C`) — dog food,
dinner, first aid, clips, machine gun, chaingun, cross, chalice, chest, crown, 1-up, gold/silver
keys, gibs — is one static sprite. Selecting one in the map editor's OBJ palette deep-links
("Edit sprite…") to the exact `SPR_STAT_n` chunk in the sprite editor, and vice versa. Catalog
metadata (points, health/ammo values, treasure/kill ratio participation) is displayed inline so
designers see *game meaning*, not just art.

---

## 9. 3D Preview and In-Browser Playtest

Two complementary in-tab experiences: a **live 3D preview** for instant editing feedback, and a
**real playtest** that boots the actual game against the compiled files. Neither requires
leaving the browser.

### 9.1 3D preview (ChaosEdit's killer feature, rebuilt properly)

- **Walk mode** (`F3` or a viewport toggle): WASD + mouse-look (yaw only — the engine has no
  pitch), collision on the tilemap, doors open with `Space`/`E` honoring locks (with a "give all
  keys" toggle), pushwalls animate their 2–3 tile slide, secret-elevator routing is announced.
- Renderer: classic column DDA raycaster at 320×200 (or 640×400) with:
  - light/dark wall pairs per face orientation (the engine's fake contrast),
  - recessed, sliding door rendering with jamb textures on flanking walls (bit-0x40 door sides),
  - sprite rendering with distance sort and post clipping, actor rotations facing the camera,
  - solid ceiling/floor colors from the level's profile entry (Appendix E),
  - optional authentic touches: 70 Hz tick simulation, view-size border, weapon hand overlay.
- **Sync:** the preview is live against the document — edit a wall in 2D and see it instantly;
  click a wall/sprite in 3D to select its tile in the 2D editor ("what code is this?").
- Enemies render at spawn positions per the selected difficulty filter; **no AI simulation** —
  the preview deliberately stops where approximation would begin. Real behavior belongs to the
  real engine, one keystroke away:

### 9.2 Playtest (`F5`): the genuine game, in the tab

- **How:** the editor embeds **js-dos (DOSBox compiled to WebAssembly)**. On `F5` it compiles
  the current document in-memory, mounts a virtual drive containing the user's game directory
  with the compiled `GAMEMAPS`/`MAPHEAD`/`VSWAP`/`VGAGRAPH`/`VGAHEAD`/`VGADICT` overlaid, and
  boots `WOLF3D.EXE`. This is **zero behavioral approximation** — guard AI, sound zones,
  pushwall physics, score screens, even the version's bugs are exactly real, because it *is* the
  real engine running the bytes the editor just wrote.
- **The executable comes from the user's own game directory** (the shareware download includes
  `WOLF3D.EXE`; registered/SOD users already have theirs) — consistent with the "no id assets
  shipped, nothing leaves the tab" rules. The emulator runtime (GPL DOSBox/js-dos) is bundled
  with the app and served statically.
- **Warp-to-level:** the game's own command line does the work — `WOLF3D.EXE TEDLEVEL <n>`
  (n = episode×10 + floor) jumps straight into the level being edited, skipping all menus, with
  skill selected by an extra `baby|easy|normal|hard` parameter. This is the launch-from-TED5
  hook id left in the engine (`tedlevel` in `ID_US_1.C`/`WL_MAIN.C`) — "playtest this floor" is
  one keypress from cursor to gameplay, the same loop id's own designers used.
- Playtest panel conveniences: skill picker, restart, "playtest from current 3D-preview
  position" (best-effort via tedlevel + a generated temporary start), per-session DOSBox config
  (cycles, sound on/off), and a capture button (canvas screenshot → PNG for sharing).
- **Fallback/alternative target:** an Emscripten build of **Wolf4SDL or ECWolf** as a second
  playtest engine for source-port profiles (better speed, no EXE needed if the port permits) —
  planned as an M8 option; js-dos vanilla is the fidelity baseline and ships first.

---

## 10. Validation Engine

Runs incrementally (background, per-edit) with results in the left panel and in-canvas badges;
full report on demand and at compile time. Severity: **Error** (engine will crash/level
unwinnable), **Warning** (known bug/bad practice), **Info** (design guidance). Every rule cites
its source (engine constant or community-documented bug). The complete v1 rule set:

**Structural / hard limits (Errors)** — constants in Appendix C:
1. Exactly one player start (19–22) per level.
2. Actor budget: enemies + dead guards + bosses ≤ **149** (`MAXACTORS 150` incl. player); Fake
   Hitler counts ×2; levels with a victory walk (object 99 / Hans / Gretel) need ≤ 148; warn
   (not error) on insufficient headroom with projectile bosses (Otto, Fettgesicht, Schabbs, Fake
   Hitler flames).
3. Static budget ≤ **399** (`MAXSTATS 400`), with Info-level headroom advice (enemy drops —
   clips, boss keys — consume free slots at runtime via `PlaceItemType`).
4. Door budget ≤ **64** (`MAXDOORS`; engine `Quit`s on the 65th); Warning at 63 (community
   guidance for stuck-door edge cases).
5. Map border: outer ring must be solid wall; Warning recommending 2-thick perimeter (flashing
   border bug).
6. Wall codes must be < 64 (`MAXWALLTILES`, and runtime door flags use bits 6–7 of `tilemap`);
   codes 50–63 trigger "no vanilla texture" Warnings; plane values 64–89 and unknown object
   codes are Errors.
7. Level name ≤ 15 chars + NUL.

**Doors and areas (Errors/Warnings):**
8. Doors must be flanked by solid walls along their axis; the two open sides must carry valid
   floor codes (door area is inherited from a neighbor at spawn, `SpawnDoor`).
9. No floor-code-less walkable tile ("Invalid tile" holes — missing area codes cause sound,
   pushwall, and rendering corruption; this is the #1 newbie bug).
10. Deaf tile (106) directly in front of/behind a door → invisible-door bug (Error).
11. Deaf tile must be under a *standing* enemy; deaf under patroller is ignored (Warning).
    Orthogonally adjacent deaf guards → paralysis bug (Warning).
12. Locked doors: required key must exist and be reachable without passing through that lock
    (graph reachability over walkable tiles + doors + 2/3-tile pushwall expansions); guards
    behind locked doors sharing the player's floor code can open them when alerted (Info, cites
    the classic "guards open locked doors" surprise).

**Secrets (Errors/Warnings):**
13. Pushwall marker (98) must sit on a solid wall tile; needs ≥ 2 tiles of clear travel; flags
    designs that *require* exactly 3 tiles (travel distance is unreliable — engine bug); pushwall
    into door tiles or map border is an Error; pushwall destination overlapping player-walk
    paths gets a softlock Warning.
14. Secret-room floor code must equal the entry room's code (statue-guards bug).
15. Secret count achievability: every 98 increments `secrettotal` at spawn — unmovable pushwalls
    make 100% secrets impossible (Warning).
16. Secret elevator: floor 107 only functions at an elevator switch; if any level routes to the
    secret floor, that episode's slot-10 map must exist.

**Completion (Errors):**
17. Level must be completable: reachable elevator switch (21) usable from an E/W-adjacent
    walkable tile, **or** a boss whose death ends the level (deathcam bosses: Schabbs, Fake
    Hitler/Hitler, Otto, Fettgesicht), **or** victory-walk trigger (99) reachable when
    Hans/Gretel present.
18. Player start inside an elevator must use fake-elevator wall 22, not 21 (instant-win bug —
    B.J. Rowan's rule).

**Actors and flow (Warnings/Info):**
19. Patrol tracing: patroller's path must not dead-end into walls without a turn arrow; arrows
    pointing into solid walls; patrol paths crossing blocking statics (actor freeze); patrol
    through doors (legal but flagged Info for sound-zone leakage).
20. Per-skill spawn audit: zero enemies/treasure/secrets on any skill (older versions choke on
    zero-ratio levels — Info, version-dependent); kill/treasure 100% achievability.
21. Visible-sprite density heuristic > ~56 in one connected area (DOS sprite-drop threshold) —
    Warning.
22. Ammo/health budget per skill vs. enemy HP pool (Info dashboard, not a rule).

The rule engine is table-driven where possible (rules declare the codes they watch) so SOD
profile swaps in its boss/static set without code changes.

---

## 11. Testing Strategy

1. **Codec round-trip (highest value):**
   - Property tests: random 64×64 plane data → RLEW/Carmack compress → expand → identical;
     random sprites/bitmaps → post-compile → decode → identical; Huffman with the real VGADICT
     trees.
   - Golden tests against real shareware v1.4 `*.WL1` data: decode all 10 levels, assert known
     facts (E1L1 player start at its known tile, guard counts, "Wolf1 Map1" name strings, etc.);
     re-encode and re-decode to identical planes; whole-file rewrite preserves untouched chunks
     bit-exact.
2. **In-game acceptance:** CI artifact "smoke mod" — a generated test level exercising every
   door type, pushwalls, deaf guards, secret elevator, each enemy class at each skill — booted
   in the embedded js-dos playtest (§9.2) by an automated Playwright run that asserts the game
   reaches gameplay (frame-hash heuristic on the canvas after `tedlevel` warp); hand-verified in
   vanilla DOSBox 1.4 and ECWolf at each milestone.
3. **Editor logic:** unit tests for code-computation (skill/facing → object code matrix from
   Appendix B as the fixture), paste rotation remaps, flood fills, validator rules (each rule
   gets a minimal fixture level that triggers it and one that doesn't).
4. **Fixtures & licensing in CI:** the shareware episode is freely distributable; we vendor the
   `*.WL1` v1.4 data files as test fixtures in the repo (with provenance note), so CI is
   self-contained. Registered `*.WL6` tests run locally only, keyed to files the developer
   provides.
5. **UI smoke tests:** Playwright flows — open shareware dir, edit E1L1, save, byte-compare
   expectations; pixel-edit a wall, compile, decode and compare; import a mod ZIP, "Download
   mod", and re-import the result (export/import round trip).

---

## 12. Milestones

Ordered so that every milestone ends with something a mapper could actually use.

- **M0 — Codec foundation.** `packages/codec` for MAPHEAD/GAMEMAPS/RLEW/Carmack with the full
  test battery (§11.1). Profiles for WL1/WL6 limits. *Exit:* round-trip green on shareware data.
- **M1 — Map viewer.** Load a game dir; browse all levels; composited plane rendering with real
  VSWAP wall textures (read-only VSWAP decode: walls + sprites for icons); hover readout; level
  list. *Exit:* E1L1 looks like the MapEdit screenshots, every code identified.
- **M2 — Editing core + playtest loop.** Tools (stamp/line/rect/fill/select/paste), LMB/RMB
  model, undo/redo, save in direct mode with backups, ZIP/file import and "Download mod" export
  (§6.3), and the embedded js-dos playtest with `tedlevel` warp (§9.2). *Exit:* author a new
  level start-to-finish and play it with `F5` without leaving the tab; the exported ZIP runs in
  desktop DOSBox and ECWolf.
- **M3 — Faithful UX layer.** Floor-code layer + room fills (`Z`/`Alt+Z`/`Ctrl+Z`), enemy
  skill/facing/mode toolbar with code computation, patrol tracing, difficulty filter, door/
  pushwall/elevator affordances, statistics panel, MapEdit keymap (Appendix F), find & replace.
- **M4 — Validation engine.** Full §10 rule set with in-canvas badges; reachability analysis;
  compile-time report. *Exit:* validator catches every bug class in the troubleshooting canon
  (fixture levels per rule).
- **M5 — VSWAP studio.** Write-capable VSWAP; wall pair editor with auto-darken and tiling
  preview; sprite editor with animation manifest, onion skin, post compiler with page-budget
  meter; PNG sheet import/export. *Exit:* reskin a guard and a wall, `F5`, see them in-game.
- **M6 — VGAGRAPH studio.** Huffman codec; pictable-aware pic editor with status-bar/menu
  context previews; font editor; TILE8; endscreen editor; demo preservation/strip. *Exit:* custom
  title screen + status bar + menu font running in-game.
- **M7 — 3D preview.** Raycaster walk mode with doors/pushwalls/sprites, 2D↔3D selection sync,
  difficulty-filtered actor display.
- **M8 — Project mode & interop.** WDC-style base/output compile workflow; single-level
  import/export incl. MapEdit floor-file format; SOD/SDM profiles end-to-end; Spear catalog
  (Appendix B SOD deltas); optional Wolf4SDL/ECWolf-WASM playtest engine; PWA offline install;
  polish, docs, sample freely-licensed asset pack for from-scratch mods.
- **M9 (stretch) — Audio.** AUDIOT/AUDIOHED (AdLib/PC-speaker/IMF) and VSWAP digitized sound
  replacement — completing WDC-class "all-in-one" coverage.

---

## 13. Risks and Mitigations

| Risk | Mitigation |
|---|---|
| **Copyright** — shipping id art/levels | Ship metadata + our own symbol glyphs only; users load their own game files; shareware fixtures limited to the freely-distributable episode; palette constants are uncopyrightable facts but we document provenance |
| **Carmack codec edge cases** (escape sequences, refs spanning words, TED5 quirks) | Implement from `CAL_CarmackExpand` semantics + ModdingWiki; fuzz with cross-validation against decompressed originals from all four game variants; keep "store uncompressed via escape" fallback in the encoder |
| **Version drift** (v1.0 RLEW-only maps, WL1/WL3/WL6/SOD VGAGRAPH chunk-count differences) | Detection heuristics + explicit per-profile manifests generated from id's `GFXV_*.H`; never hardcode chunk numbers in app code |
| **Sprite repack overruns** (packed sprite > page budget) | Live size meter, encoder optimizes post layout, hard warning at 4096 bytes; verify the exact vanilla page constraint against `ID_PM.C` during M5 and encode it in profiles |
| **Corrupting user mods** (ChaosEdit's reputation) | Untouched-chunk byte preservation, automatic rotating backups, atomic file writes (write-temp-rename), aggressive round-trip tests |
| **Scope creep toward a game engine** | The 3D preview deliberately omits AI/combat; real testing is the embedded real engine (`F5` js-dos playtest), so there is never a reason to grow the preview into an engine |
| **Playtest emulator integration** (js-dos API churn, audio/pointer-lock permissions, performance on low-end devices) | Pin a vetted js-dos release and wrap it behind a thin harness interface; playtest is additive — editing and export never depend on it; document the desktop DOSBox loop as the escape hatch |
| **Browser storage/API variance** (File System Access unavailable in Firefox/Safari) | Every workflow has an upload/download + OPFS path (§6.3); FS Access is an enhancement, not a requirement |
| **Fidelity erosion by convenience features** | Every UX nicety (auto-orientation, code computation, scaffolds) must reduce to plain plane values with no editor-only state; the saved file is always the single source of truth |

---

## Appendix A — Wall Plane Codes (Plane 0)

Verified semantics from `WL_DEF.H` / `SetupGameLevel` / `SpawnDoor`. Display names follow
MapEdit's `MAPDATA.WL6` conventions (minor wording varies between classic editors).

**Solid walls (1–49).** Wall code *n* renders VSWAP chunks `(n−1)×2` (light, N/S faces) and
`(n−1)×2+1` (dark, E/W faces).

| Code | Name | Code | Name |
|---|---|---|---|
| 1 | Grey stone 1 | 26 | Dirty brick 2 |
| 2 | Grey stone 2 | 27 | Grey brick 3 |
| 3 | Grey stone / flag | 28 | Grey brick / sign |
| 4 | Grey stone / Hitler portrait | 29 | Brown weave |
| 5 | Cell | 30 | Brown weave / blood 2 |
| 6 | Grey stone / eagle | 31 | Brown weave / blood 3 |
| 7 | Cell / skeleton | 32 | Brown weave / blood 1 |
| 8 | Blue stone 1 | 33 | Stained glass |
| 9 | Blue stone 2 | 34 | Blue wall / skull |
| 10 | Wood / eagle | 35 | Grey wall 1 |
| 11 | Wood / Hitler portrait | 36 | Blue wall / swastika |
| 12 | Wood | 37 | Grey wall / vent |
| 13 | Entrance to level | 38 | Multicolor brick |
| 14 | Steel / sign | 39 | Grey wall 2 |
| 15 | Steel | 40 | Blue wall |
| 16 | Landscape (window) | 41 | Blue brick / sign |
| 17 | Red brick | 42 | Brown marble 1 |
| 18 | Red brick / wreath | 43 | Grey wall / map |
| 19 | Purple | 44 | Brown stone 1 |
| 20 | Red brick / eagle | 45 | Brown stone 2 |
| 21 | **Elevator** (live switch; `ELEVATORTILE`; switch faces E/W; using it increments the tile to 22) | 46 | Brown marble 2 |
| 22 | **Fake/used elevator** (thrown switch on E/W faces; N/S faces blank dark grey) | 47 | Brown marble / flag |
| 23 | Wood / iron cross | 48 | Wood panel |
| 24 | Dirty brick 1 | 49 | Grey wall / Hitler poster |
| 25 | Purple / blood | | |

- **50–63:** legal for the engine (`MAXWALLTILES 64`) but no vanilla textures — editor flags.
- **64–89:** solid with undefined rendering — editor treats as invalid.
- **90–101 — Doors** (`SpawnDoor`; even = "vertical" door in a N–S wall run, odd = horizontal;
  lock = `(code−90)/2`):

| Codes (V/H) | Door | Key |
|---|---|---|
| 90 / 91 | Normal door | — |
| 92 / 93 | Locked door | Gold key (lock 1) |
| 94 / 95 | Locked door | Silver key (lock 2) |
| 96 / 97 | Locked door (lock 3) | unused by vanilla assets |
| 98 / 99 | Locked door (lock 4) | unused by vanilla assets |
| 100 / 101 | Elevator door | — |

Door texture chunks are the 8 immediately before `spriteStart` (`DOORWALL = spriteStart−8`):
+0/+1 normal door, +2/+3 door jamb/track, +4/+5 elevator door, +6/+7 locked door.

- **106 — Ambush / "deaf guard" tile** (`AMBUSHTILE`, MapEdit hex `6A`): placed on the wall
  plane under an enemy; sets the ambush flag and is replaced by an adjacent area code at load.
- **107–143 — Floor/area codes** (`AREATILE 107`, `NUMAREAS 37`; MapEdit hex `6B`–`8F`): room
  interiors and sound-propagation zones. **107** (`ALTELEVATORTILE`, hex `6B`) additionally
  routes to the secret level when the player throws an elevator switch standing on it.

## Appendix B — Object Plane Codes (Plane 1)

All verified against `ScanInfoPlane` (`WL_GAME.C`) and `statinfo[]` (`WL_ACT1.C`).

**Player starts** — `SpawnPlayer(x,y,NORTH+code−19)`:
19 = facing North, 20 = East, 21 = South, 22 = West. Exactly one per level.

**Statics, items, and power-ups (23–71)** — `SpawnStatic(x,y,code−23)`. "Block" = blocks
movement. Bonus semantics from `bo_*` handling in `WL_AGENT.C GetBonus`:

| Code | Object | Behavior |
|---|---|---|
| 23 | Puddle | dressing |
| 24 | Green barrel | block |
| 25 | Table & chairs | block |
| 26 | Floor lamp | block |
| 27 | Chandelier | dressing (ceiling) |
| 28 | Hanged skeleton | block (ceiling) |
| 29 | **Dog food** | +4 health (`bo_alpo`) |
| 30 | Red pillar | block |
| 31 | Tree | block |
| 32 | Flat skeleton | dressing |
| 33 | Sink | block |
| 34 | Potted plant | block |
| 35 | Urn | block |
| 36 | Bare table | block |
| 37 | Ceiling light | dressing |
| 38 | Kitchen utensils | dressing |
| 39 | Suit of armor | block |
| 40 | Hanging cage | block |
| 41 | Skeleton in cage | block |
| 42 | Relaxing skeleton | dressing |
| 43 | **Gold key** | `bo_key1` |
| 44 | **Silver key** | `bo_key2` |
| 45 | Bed | block |
| 46 | Basket | dressing |
| 47 | **Chicken dinner** | +10 health (`bo_food`) |
| 48 | **First aid kit** | +25 health (`bo_firstaid`) |
| 49 | **Ammo clip** | +8 rounds (`bo_clip`) |
| 50 | **Machine gun** | weapon + 6 rounds (`bo_machinegun`) |
| 51 | **Chain gun** | weapon + 6 rounds (`bo_chaingun`) |
| 52 | **Cross** | 100 pts (`bo_cross`, treasure) |
| 53 | **Chalice** | 500 pts (`bo_chalice`, treasure) |
| 54 | **Jeweled chest** | 1000 pts (`bo_bible`, treasure) |
| 55 | **Crown** | 5000 pts (`bo_crown`, treasure) |
| 56 | **Extra life (1-Up)** | full health, +25 rounds, +1 life; counts as treasure (`bo_fullheal`) |
| 57 | Pool of gibs | +1 health when ≤ 10 (`bo_gibs`) |
| 58 | Brown barrel | block |
| 59 | Water well | block |
| 60 | Empty well | block |
| 61 | Pool of gibs 2 | `bo_gibs` |
| 62 | Flag | block |
| 63 | "Call Apogee" / Aardwolf sign | block |
| 64–66 | Junk (bones/debris ×3) | dressing |
| 67 | Pots | dressing |
| 68 | Stove | block |
| 69 | Spear rack | block |
| 70 | Vines | dressing |
| 71 | Partial clip | +4 rounds (`bo_clip2`; enemy-drop item, placeable) |

*SOD replaces several dressing entries with gibs variants and extends the table:
71 marble pillar, 72 box of 25 clips (`bo_25clip`), 73 truck, 74 **Spear of Destiny**
(`bo_spear`), 75 partial clip.*

**Patrol turning points (90–97)** — `ICONARROWS 90`; read by patrolling actors each tile:
90 = East, 91 = NE, 92 = North, 93 = NW, 94 = West, 95 = SW, 96 = South, 97 = SE.

**Specials:** 98 = **pushwall marker** (`PUSHABLETILE`; on a solid wall; increments
`secrettotal`); 99 = **victory-walk end trigger** (`EXITTILE`; episode-end BJ run);
124 = **dead guard** (dressing actor; counts toward the 149-actor and kill totals).

**Enemies** — facing order for both stand and patrol is **East, North, West, South** (the spawn
dir is `code − base`, doubled into the 8-way dir enum). Skill encoding: the value spawns at its
listed skill *and above*.

| Enemy | Stand E/N/W/S (Easy) | Patrol E/N/W/S (Easy) | +Medium | +Hard |
|---|---|---|---|---|
| Guard (brown) | 108–111 | 112–115 | +36 → 144–151 | +72 → 180–187 |
| Officer (white) | 116–119 | 120–123 | +36 → 152–159 | +72 → 188–195 |
| SS (blue) | 126–129 | 130–133 | +36 → 162–169 | +72 → 198–205 |
| Dog | 134–137 | 138–141 | +36 → 170–177 | +72 → 206–213 |
| Mutant | 216–219 | 220–223 | **+18** → 234–241 | **+36** → 252–259 |

(Mutant hard-patrol codes 256–259 exceed one byte — plane values are 16-bit words.)

**Bosses (Wolf3D)** — no skill/facing variants: 160 Fake Hitler · 178 Adolf Hitler (mech →
revealed; counts as 2 actors/kills) · 179 General Fettgesicht · 196 Dr. Schabbs · 197 Gretel
Grösse · 214 Hans Grösse · 215 Otto Giftmacher. **Ghosts** (E3 Pac-Man secret level):
224 Blinky, 225 Clyde, 226 Pinky, 227 Inky.

**Bosses (SOD)**: 106 Spectre · 107 Angel of Death · 125 Trans Grösse · 142 Übermutant ·
143 Barnacle Wilhelm · 161 Death Knight.

## Appendix C — Engine Constants and Hard Limits

From `WL_DEF.H` unless noted:

| Constant | Value | Editor consequence |
|---|---|---|
| Map size | 64×64×2 planes, 16-bit values | fixed grid; playable 62×62 inside border |
| `MAXACTORS` | 150 (incl. player) | ≤ 149 placed actors; Fake Hitler ×2; victory-walk BJ needs a slot; projectiles share the pool at runtime |
| `MAXSTATS` | 400 | ≤ 399 statics + runtime drop headroom |
| `MAXDOORS` | 64 | engine fatal on the 65th door; door numbers must fit 6 bits |
| `MAXWALLTILES` | 64 | wall codes 1–63; bits 6–7 of runtime tilemap are door flags |
| `ICONARROWS` | 90 | turn-point base |
| `PUSHABLETILE` | 98 | pushwall marker |
| `EXITTILE` | 99 | victory-walk trigger |
| `ELEVATORTILE` | 21 | switch usable from E/W only (`Cmd_Use`) |
| `AMBUSHTILE` | 106 | deaf guard |
| `AREATILE` / `NUMAREAS` | 107 / 37 | floor codes 107–143 |
| `ALTELEVATORTILE` | 107 | secret-level floor |
| Level slots | 100 (`MAPHEAD`) | WL1 10, WL3 30, WL6 60, SOD 21, SDM 2 |
| RLEW tag | 0xABCD | from MAPHEAD |
| Visible sprites | ~56–64 practical | density Warning (DOS sprite drop) |
| Secret-level returns | `ElevatorBackTo[] = {1,1,7,3,5,3}` (`WL_GAME.C`) | per-episode return floor after the secret level |

Gameplay economy (for the stats dashboard): treasure 100/500/1000/5000 pts + 1-Up; extra life
each 40 000 pts; max 99 ammo, 9 lives, 100 health; clip 8, dropped clip 4, weapon pickup 6;
chaingun consumes 2 rounds/shot.

## Appendix D — File Format Specifications

### D.1 MAPHEAD
`u16 rlewTag` (0xABCD) · `i32 headerOffset[100]` (0 or −1 = unused) · optional legacy
`tileinfo[]` tail (preserve verbatim).

### D.2 GAMEMAPS
Optional `"TED5v1.0"` signature, then chunks in any order. Per level a 38-byte header:
`i32 planeStart[3]` · `u16 planeLength[3]` (compressed bytes) · `u16 width,height` (ignored by
the engine; always 64) · `char name[16]`. Wolf3D always loads planes 0 and 1.
Plane chunk (v1.1+): `u16 carmackExpandedLen`, Carmack data → RLEW block
(`u16 rlewExpandedLen` = 8192, RLEW data). v1.0: RLEW block only. `MAPTEMP` (TED5 intermediate):
RLEW only.

### D.3 RLEW / Carmack
- RLEW: stream of u16; `tag, count, value` encodes a run; tag itself encoded as a run of 1.
- Carmack: byte pairs `(count, type)`; `type 0xA7` near pointer (`u8` back-offset in words),
  `type 0xA8` far pointer (`u16` absolute word offset); `count==0` escapes a literal word whose
  high byte is A7/A8 (next byte = low byte); all other pairs are literal words.

### D.4 VSWAP
Header: `u16 numChunks, spriteStart, soundStart` · `u32 offset[numChunks]` ·
`u16 length[numChunks]` (offset 0 = sparse). Chunks `[0,spriteStart)` walls — 4096 bytes raw
8-bit, **column-major**. `[spriteStart,soundStart)` compiled sprites:
`u16 leftCol, rightCol` · `u16 colOfs[right−left+1]` (chunk-relative) → per-column post lists;
post = `u16 endRow×2, u16 poolIndex, u16 startRow×2`, list terminated by `u16 0`; opaque pixels
for rows `[start,end)` come from the shared pool indexed by `poolIndex+row`. Color 255
transparent by omission. `[soundStart,numChunks)` digitized sound pages (≤ 4096 bytes each) +
final page-info table — preserved verbatim until M9.

### D.5 VGA graphics trio
- `VGADICT`: 255 Huffman nodes × `{u16 bit0, u16 bit1}` (values < 256 = byte, ≥ 256 = node−256;
  root = node 254) + padding to 1024 bytes.
- `VGAHEAD`: 3-byte LE offset per chunk; 0xFFFFFF = sparse.
- `VGAGRAPH`: chunk 0 = `STRUCTPIC` pictable (`{u16 width,height}` × NUMPICS, expanded size
  implicit); most chunks prefixed `u32 expandedLen`; TILE8 block uses implicit sizing. Pics are
  4-plane VGA "munged": plane p (size w·h/4) holds pixels with `x mod 4 == p`. Fonts
  (`STARTFONT*`): `u16 height` · `i16 location[256]` · `u8 width[256]` · byte-per-pixel glyphs.
  Also: palettes (some versions), end-text (80×25 char+attr B800 screens), demo chunks
  (`T_DEMO0..3`: level byte, u16 length, input stream). Per-version chunk manifests generated
  from `GFXV_WL1.H`, `GFXV_WL6.H`, `GFXV_SOD.H`.

### D.6 Palette
256 × 6-bit VGA RGB (×4 → 8-bit, replicating top bits). Index 255 = sprite-transparent
magenta. Structured as hue ramps (the basis for the auto-darken and remap tools).

## Appendix E — Per-Level Metadata Reference

Stored in the EXE, not in map files — surfaced read-only under vanilla profiles, editable for
source-port export. Shipping tables (in `packages/data`): per-level **ceiling colors** for all
60 WL6 levels and 21 SOD levels (e.g. E1L1–E1L9 dark gray `0x1D`, E1L10 purple `0xBF`,
E2L8 dark red `0x2D`, E5L1 cyan `0x7D`, …— full chart already compiled from the community
ceiling-color reference, matching `vgaCeiling[]` in `WL_PLAY.C`); **par times**
(`parTimes[]`, `WL_INTER.C`); **music track per level** (`songs[]`, `WL_PLAY.C`);
episode → floor numbering (floors 1–8, boss 9, secret 10) and secret-level return floors.

## Appendix F — Keyboard Shortcuts

MapEdit parity (the muscle memory layer): `C` clear level · `O` toggle objects · `F` toggle
floor codes · `Z` room floor fill / `Alt+Z` include deaf tiles / `Ctrl+Z`* fill into secret
areas · `Alt+S` statistics. (*remapped to `Shift+Z` since `Ctrl+Z` is undo; a "classic keys"
toggle restores the original binding.)
Modern layer: `Ctrl+Z/Y` undo/redo · `Ctrl+C/X/V` clipboard · `[`/`]` rotate paste ·
`1/2/3` skill filter · `E/N/W/S`-arrow facing select · `F3` 3D preview · `F7` validate ·
`F10` compile · `+/-` zoom.

## Appendix G — Glossary

**Floor code / area** (107–143, sound zone) · **deaf guard** (ambush tile 106) · **pushwall**
(object 98 on a wall; the only "secret") · **turning point** (90–97 patrol arrows) ·
**holowall** (community term: walkable wall trick — *not* representable in vanilla data; editor
explicitly does not fake it) · **carmackized** (Carmack-compressed plane) · **RLEW tag**
(0xABCD) · **light/dark pair** (N/S vs E/W wall textures) · **post** (opaque sprite column
span) · **pictable** (`STRUCTPIC` dimensions chunk) · **Get Psyched** (load screen pic) ·
**dressing vs block** (non-blocking vs blocking static) · **MAPTEMP** (uncarmackized TED5
output) · **floor file** (MapEdit single-level export).
