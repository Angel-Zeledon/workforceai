// Office visual styles (docs/architecture/office-editor.md, "Styles"). A style is a way of RENDERING the same office:
// geometry, surface texture, shading and motion change; the colors, the palette and the light never do (every color
// still comes from the catalog item or today's scene). One table per style plus the pure transforms the renderer applies
// on top of the existing specs, so no furniture, character or worn item is drawn twice.
// No THREE / React imports (unit tested with node, see officeTheme.test.mjs).
import type { OfficeStyle } from "@/lib/officeLayoutModel";

export type GeometryMode = "keep" | "blocks" | "lowpoly" | "soft";
export type AppearKind = "none" | "rise" | "stack" | "unfold" | "squish" | "pop";
export type AmbientKind = "none" | "paper";
/** Surface detail drawn by the shader. Procedural (smooth simplex noise, never a lattice of cells, so no blocky patches):
 *  0 none, 1 clay dents and finger marks (relief only), 2 paper / card grain, 10 toy-brick courses (thin seams),
 *  11 toy baseplate (a stud every 16 cm on upward faces: real relief, so each stud catches the light).
 *  Real materials (`pbr` texture set, triplanar): 3 wood, 4 brushed metal, 5 fabric, 8 concrete, 9 plaster.
 *  6 lacquer and 7 skin carry no detail (smooth; the finish and the reflection do the work). */
export type SurfaceKind = 0 | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 | 9 | 10 | 11;
/** Real CC0 texture sets (public/office/pbr, see CREDITS.md there): normal + albedo detail + roughness detail. */
export type PbrKind = "wood" | "concrete" | "plaster" | "fabric" | "metal";
export const PBR_KINDS: PbrKind[] = ["wood", "concrete", "plaster", "fabric", "metal"];
/** Size of one tile of each texture set, in meters (the texture repeats every this many meters on the surface). */
export const PBR_TILE: Record<PbrKind, number> = { wood: 1.1, concrete: 2.4, plaster: 1.6, fabric: 0.22, metal: 0.6 };
/** The surface kind each texture set stands for. */
export const PBR_SURFACE: Record<PbrKind, SurfaceKind> = { wood: 3, metal: 4, fabric: 5, concrete: 8, plaster: 9 };
/** Reflections of a style: none, or a small CC0 HDRI of a real room (public/office/env), loaded only while in use. */
export type EnvKind = "none" | "hdri";
/**
 * Post-processing of a style (one EffectComposer, never in "classic"): ambient occlusion (strength, radius in meters),
 * bloom on what glows (screens, lamps), a slight tilt-shift (miniature photo), SMAA antialiasing. The quality tier
 * switches the costly ones off on slow computers (officeQuality.ts).
 */
export interface PostLook { ao: number; aoRadius: number; bloom: number; tilt: number; smaa: boolean }
/** What a material belongs to: a piece of furniture or the room's built-ins, a character, the floor / ground, a wall. */
export type MatRole = "item" | "figure" | "ground" | "wall";
/** The ink pass: line thickness in device pixels, its color, how strongly it is laid over the picture, for paper the
 *  strength of a cut-out shadow lifted a few pixels off every object (0 = none) and, for cartoon, how much the lines
 *  "boil" (redrawn by hand a few times per second: `boil` pixels of jitter, `boilFps` new drawings per second). */
export interface Outline { px: number; color: string; alpha: number; shadow?: number; boil?: number; boilFps?: number }
/** Body proportions of the characters in a style (1 = classic): head size, torso width / height, limb thickness. */
export interface Proportions { head: number; torsoW: number; torsoH: number; limb: number }

/** How the characters move in a style. Every multiplier is 1 (and every extra 0/false) in "classic". */
export interface Motion {
  /** idle sway / head turns amplitude */
  idle: number;
  /** walk cycle amplitude (hips, arms, bob) */
  walk: number;
  /** walking speed */
  speed: number;
  /** turning speed */
  turn: number;
  /** how often idle fidgets happen */
  fidget: number;
  /** stop-motion: poses and positions only advance this many times per second (0 = every frame) */
  stopFps: number;
  /** stiff limbs: knees and elbows barely bend (toy figures) */
  rigid: boolean;
  /** depth of the body (paper cut-outs are flat) */
  depth: number;
  /** side-to-side rock while walking (paper puppets) */
  waddle: number;
  /** light flutter while standing (paper) */
  flutter: number;
  /** squash and stretch of the whole body (clay, cartoon) */
  squash: number;
  /** cartoon smear: the body leans and stretches along its motion while walking */
  smear: number;
  /** natural weight shift over the stance leg while walking (realistic) */
  weight: number;
  /** every joint follows its pose through a spring: a little anticipation and follow-through, never a jitter */
  spring?: boolean;
}

export interface StyleLook {
  geometry: GeometryMode;
  /** toy studs on the upward faces of boxes */
  studs: boolean;
  /** faceted (flat shaded) surfaces */
  flat: boolean;
  /** cel shading: the light falls in a few flat bands */
  cel: boolean;
  /** screen-space outlines (one full-screen pass): cartoon ink, papercraft cut edges */
  outline: Outline | null;
  /** surfaces darken toward their silhouette like a drawn crease (0..1) */
  ink: number;
  /** soft, slightly lumpy hand-made surfaces (vertex wobble, in meters) */
  lump: number;
  /** image-based reflections (realistic materials, glossy toy plastic) */
  env: EnvKind;
  /** post-processing (null: none, the frame is drawn straight, like "classic") */
  post: PostLook | null;
  appear: AppearKind;
  ambient: AmbientKind;
  motion: Motion;
  /** warm afternoon light over the scene (Maqueta) */
  warmLight?: boolean;
  /** drawn small and enlarged by whole pixels with a palette and an outline (Pixel) */
  pixel?: boolean;
  /** colors of the picker's preview tile (today's palette, drawn in the style) */
  swatch: [string, string, string];
  /** body proportions of the characters */
  body: Proportions;
}

const STILL: Motion = { idle: 1, walk: 1, speed: 1, turn: 1, fidget: 1, stopFps: 0, rigid: false, depth: 1, waddle: 0, flutter: 0, squash: 0, smear: 0, weight: 0 };
/** Today's colors: wall white, oak, the accent blue. Every tile shows them, only drawn differently. */
const TODAY: [string, string, string] = ["#f3f4f7", "#d9c4a3", "#4f7fe0"];

const SAME_BODY: Proportions = { head: 1, torsoW: 1, torsoH: 1, limb: 1 };
const BASE: StyleLook = {
  geometry: "keep", studs: false, flat: false, cel: false, outline: null, ink: 0, lump: 0, env: "none", post: null, appear: "none", ambient: "none", motion: STILL, swatch: TODAY,
  body: SAME_BODY,
};

export const STYLE_LOOKS: Record<OfficeStyle, StyleLook> = {
  classic: BASE,
  // Studio and Flat have their own renderers (components/office/studio, /flat); in the 3D scene (the editor's normal view)
  // they look like the current office.
  studio: BASE,
  flat: BASE,
  // Maqueta: a clay scale model. Matte warm materials, soft contact shadows, afternoon light and a tilt-shift blur. Nothing
  // flickers: the body is smoothed by springs (Character) instead of stop-motion.
  clay: {
    ...BASE, geometry: "soft", lump: 0.022, appear: "squish", warmLight: true,
    post: { ao: 1.6, aoRadius: 0.45, bloom: 0, tilt: 1, smaa: true },
    body: { head: 1.14, torsoW: 1.2, torsoH: 0.96, limb: 1.45 }, // chubby, big head, sausage arms and legs
    motion: { ...STILL, idle: 1.15, walk: 1.1, speed: 0.9, squash: 1.2, fidget: 1.1, spring: true },
  },
  // Pixel: pixel art in 3D. The picture is drawn small and enlarged by a whole number of screen pixels (PixelLayer), with a
  // few flat bands of light, a 1 px outline, hard shadows and poses at 6 per second, like a sprite.
  pixel: {
    ...BASE, cel: true, pixel: true,
    motion: { ...STILL, stopFps: 6 },
  },
};

export const lookOf = (s: OfficeStyle | undefined): StyleLook => STYLE_LOOKS[s ?? "classic"] ?? STYLE_LOOKS.classic;

// ------------------------------------------------------------------------------------------------ materials

/** A lit material as the renderer sees it (only what the styles change). Colors are never part of it. */
export interface MatIn {
  roughness: number; metalness: number; transparent: boolean;
  /** what the surface is when the role alone does not say: the floor's kind ("concrete", "wood", "parquet", "carpet",
   *  "tile", "marble") or "backdrop" for the ground outside the office */
  hint?: string;
}
export interface MatOut {
  roughness: number; metalness: number;
  flat: boolean; cel: boolean; ink: number; lump: number;
  /** surface detail: relief strength (`bump`) and how much it varies the shade of the color (`tex`, +-; the mean shade
   *  is kept, so an oak desk stays that oak) */
  surface: SurfaceKind; bump: number; tex: number;
  /** real texture set of the surface (null: procedural or none), its tile in meters and how much it varies roughness */
  pbr: PbrKind | null; tile: number; roughVar: number;
  /** how much the room reflection adds */
  envIntensity: number;
}

/**
 * The style's version of one material: finish, shading and surface, NEVER the color itself (`tex` only lets a texture
 * shade it a little lighter / darker, like real grain does). "classic" returns the material as it is.
 */
export function styleMaterial(m: MatIn, style: OfficeStyle, role: MatRole = "item"): MatOut {
  const look = lookOf(style);
  const out: MatOut = { roughness: m.roughness, metalness: m.metalness, flat: look.flat, cel: look.cel, ink: role === "ground" ? 0 : look.ink, lump: 0, surface: 0, bump: 0, tex: 0, pbr: null, tile: 1, roughVar: 0, envIntensity: 0 };
  const figure = role === "figure";
  switch (style) {
    case "clay": // matte warm clay: soft dents and finger marks in the relief only (no color stains); glass stays glass
      if (m.transparent) break;
      out.roughness = 0.82; out.metalness = 0; out.surface = 1;
      out.bump = figure ? 0.7 : role === "ground" ? 0.8 : 1;
      out.lump = role === "ground" ? 0 : look.lump * (figure ? 0.55 : 1);
      break;
    case "pixel": // flat bands of light, nothing shiny
      if (m.transparent) break;
      out.roughness = Math.max(0.85, m.roughness); out.metalness = 0;
      break;
  }
  return out;
}

// ------------------------------------------------------------------------------------------------ motion

/** Height of the pelvis standing (gestures.ts STAND_Y). */
const STAND = 0.4;
type Pose = Record<string, number>;

/**
 * The style's touch on a pose computed by gestures.computePose (mutates and returns it). Identity for "classic".
 * `walking` / `seated` describe the mode, `t` the character's clock, `stride` the walk phase, `mode` the full mode.
 */
export function stylePose(p: Pose, m: Motion, walking: boolean, seated: boolean, t: number, ph: number, stride: number, mode = ""): Pose {
  if (m === STILL) return p;
  if (walking) {
    if (m.walk !== 1) {
      p.pelvisY = STAND + (p.pelvisY - STAND) * m.walk;
      for (const k of ["lHipX", "rHipX", "lKneeX", "rKneeX", "lShX", "rShX", "torsoZ", "torsoY", "headY"]) p[k] *= m.walk;
    }
    const s = Math.sin(stride);
    if (m.waddle) { p.torsoZ += s * m.waddle; p.headZ += -s * m.waddle * 0.6; p.pelvisY += Math.abs(s) * 0.05; }
    if (m.weight) { p.torsoZ += s * 0.045 * m.weight; p.headZ -= s * 0.03 * m.weight; p.pelvisY -= Math.abs(Math.cos(stride)) * 0.012 * m.weight; }
    if (m.smear) { p.torsoX += 0.12 * m.smear; p.headX -= 0.06 * m.smear; } // leans into the run
  } else if (m.idle !== 1) {
    for (const k of ["headY", "headZ", "torsoZ", "torsoY"]) p[k] *= m.idle;
  }
  if (m.flutter && !walking) { p.torsoZ += Math.sin(t * 5.3 + ph) * 0.025 * m.flutter; p.headZ += Math.sin(t * 4.1 + ph * 2) * 0.03 * m.flutter; }
  if (m.rigid) {
    if (!seated) { p.lKneeX = 0; p.rKneeX = 0; }
    p.lElX *= 0.3; p.rElX *= 0.3;
  }
  if (m.squash && mode === "stand:completed") p.pelvisY += Math.abs(Math.sin(t * 5.5 + ph)) * 0.04 * m.squash; // bigger, rubbery hops
  return p;
}

/**
 * Squash and stretch of the whole body: [width/depth factor, height factor] (volume is kept). Walking bounces on every
 * step, standing breathes, a finished task jiggles. [1, 1] when the style has none.
 */
export function bodySquash(m: Motion, walking: boolean, t: number, ph: number, stride: number, mode = ""): [number, number] {
  if (!m.squash) return [1, 1];
  let k: number;
  if (walking) k = Math.cos(stride * 2) * 0.07;
  else if (mode === "stand:completed") k = Math.sin(t * 11 + ph) * 0.06;
  else k = Math.sin(t * 2.2 + ph) * 0.03; // breathing: the whole body swells and settles
  k *= m.squash;
  return [1 / Math.sqrt(1 + k), 1 + k];
}

/** True when a stop-motion character should take its next pose now (accumulates `dt` in `acc[0]`). */
export function stopMotionTick(acc: [number], dt: number, fps: number): boolean {
  if (!fps) return true;
  acc[0] += dt;
  if (acc[0] < 1 / fps) return false;
  acc[0] %= 1 / fps;
  return true;
}

// ------------------------------------------------------------------------------------------------ pieces appearing

export const APPEAR_SECONDS: Record<AppearKind, number> = { none: 0, rise: 0.8, stack: 0.7, unfold: 0.85, squish: 0.75, pop: 0.5 };

export interface AppearPose { y: number; sx: number; sy: number; sz: number; rx: number; ry: number; visible: boolean }
const REST: AppearPose = { y: 0, sx: 1, sy: 1, sz: 1, rx: 0, ry: 0, visible: true };

const easeInOutCubic = (u: number) => (u < 0.5 ? 4 * u * u * u : 1 - Math.pow(-2 * u + 2, 3) / 2);
const easeOutBack = (u: number, k = 1.7) => 1 + (k + 1) * Math.pow(u - 1, 3) + k * Math.pow(u - 1, 2);
const clamp01 = (v: number) => (v < 0 ? 0 : v > 1 ? 1 : v);

/** Where a piece is `u` (0..1) into its entrance. u >= 1 is the piece at rest. */
export function appearPose(kind: AppearKind, u: number): AppearPose {
  if (kind === "none" || u >= 1) return REST;
  const v = clamp01(u);
  switch (kind) {
    case "rise": { // realistic: lowered gently into place, no overshoot
      const e = easeInOutCubic(v);
      return { ...REST, y: 0.35 * (1 - e) };
    }
    case "stack": { // stop-motion: drops in whole steps, then clicks into place with a short squash
      const steps = 6;
      const q = Math.floor(v * steps) / steps;
      if (q < 0.67) return { ...REST, y: 1.4 * (1 - q / 0.67) };
      const sq = q < 0.84 ? 0.9 : 1;
      return { ...REST, sy: sq, sx: 2 - sq, sz: 2 - sq };
    }
    case "unfold": // pop-up book: lies flat on the floor and folds up around its base
      return { ...REST, rx: -(Math.PI / 2) * (1 - easeOutBack(v, 1.3)), sy: 0.6 + 0.4 * v };
    case "squish": { // a lump of clay plops down, squashes flat and springs back (volume kept)
      if (v < 0.3) { const d = v / 0.3; return { ...REST, y: 0.9 * (1 - d * d), sy: 1.15, sx: 0.93, sz: 0.93 }; }
      const w = (v - 0.3) / 0.7;
      const sy = 1 - Math.exp(-4 * w) * Math.cos(w * Math.PI * 3.2) * 0.45;
      return { ...REST, sy, sx: 1 / Math.sqrt(sy), sz: 1 / Math.sqrt(sy) };
    }
    case "pop": { // cartoon: stretches up out of nowhere, overshoots and settles
      const s = Math.max(0.001, easeOutBack(v, 2.6));
      const stretch = 1 + Math.sin(v * Math.PI) * 0.25;
      return { ...REST, sx: s / Math.sqrt(stretch), sz: s / Math.sqrt(stretch), sy: s * stretch };
    }
  }
  return REST;
}
