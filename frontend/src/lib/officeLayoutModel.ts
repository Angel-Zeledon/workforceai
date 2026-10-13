/**
 * The office layout document (docs/architecture/office-editor.md) and the pure rules around it: defaults, normalizing
 * what the server sent, and the palettes the editor offers. No React, no imports of the app: unit tested with node.
 */
export type FloorKind = "concrete" | "wood" | "parquet" | "carpet" | "tile" | "marble";
export type Mood = "natural" | "warm" | "cool" | "sunset" | "night";
export interface DeskCfg { x?: number; z?: number; desk?: string; chair?: string }
export interface Piece { uid: string; item: string; x: number; z: number; rot: number; s: number; y?: number }
export interface Layout {
  v: 1;
  walls: { color: string; trim: string };
  floor: { kind: FloorKind; tint: string };
  light: { mood: Mood; temp: number; brightness: number };
  desks: Record<string, DeskCfg>;
  pieces: Piece[];
  seeded?: boolean;
}

export const FLOOR_KINDS: FloorKind[] = ["concrete", "wood", "parquet", "carpet", "tile", "marble"];
export const MOODS: Mood[] = ["natural", "warm", "cool", "sunset", "night"];

export const DEFAULT_LAYOUT: Layout = {
  v: 1,
  walls: { color: "#f3f4f7", trim: "#3a414c" },
  floor: { kind: "concrete", tint: "#ffffff" },
  light: { mood: "natural", temp: 0, brightness: 1 },
  desks: {},
  pieces: [],
};

/** Wall palettes: soft, lived-in tones first, then bolder ones. */
export const WALL_COLORS = ["#f3f4f7", "#efe9df", "#e6ddcc", "#f2e3d5", "#e9d4d0", "#d9e4dc", "#d5e1ea", "#cfd8ee", "#e1d6ec", "#3f4a5a", "#2f3b3a", "#5b3f3f"];
export const TRIM_COLORS = ["#3a414c", "#2a2f38", "#8a6a4a", "#d9c4a3", "#e8e8e8", "#6b7a6e", "#44546a", "#7a4a4a"];
export const FLOOR_TINTS = ["#ffffff", "#f1e2c8", "#d9bf9a", "#b98f66", "#8a6a4a", "#c9d3dc", "#a9b7c6", "#7e8c99", "#e8c9c9", "#c6d9c6"];

const HEX = /^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$/;
export const isHex = (s: unknown): s is string => typeof s === "string" && HEX.test(s);
const num = (v: unknown, lo: number, hi: number, fallback: number) => (typeof v === "number" && Number.isFinite(v) ? Math.min(hi, Math.max(lo, v)) : fallback);
const hex = (v: unknown, fallback: string) => (isHex(v) ? v : fallback);

/** A layout from whatever the server (or localStorage) returned: unknown fields are dropped, bad ones fall back to the defaults. */
export function normalizeLayout(raw: unknown): Layout {
  const r = (raw && typeof raw === "object" ? raw : {}) as Record<string, any>;
  const out: Layout = {
    v: 1,
    walls: { color: hex(r.walls?.color, DEFAULT_LAYOUT.walls.color), trim: hex(r.walls?.trim, DEFAULT_LAYOUT.walls.trim) },
    floor: { kind: FLOOR_KINDS.includes(r.floor?.kind) ? r.floor.kind : "concrete", tint: hex(r.floor?.tint, "#ffffff") },
    light: { mood: MOODS.includes(r.light?.mood) ? r.light.mood : "natural", temp: num(r.light?.temp, -1, 1, 0), brightness: num(r.light?.brightness, 0.5, 1.5, 1) },
    desks: {},
    pieces: [],
  };
  if (r.seeded) out.seeded = true;
  if (r.desks && typeof r.desks === "object") {
    for (const [id, d] of Object.entries(r.desks as Record<string, any>)) {
      if (!d || typeof d !== "object") continue;
      const c: DeskCfg = {};
      if (typeof d.x === "number" && typeof d.z === "number" && Number.isFinite(d.x) && Number.isFinite(d.z)) { c.x = d.x; c.z = d.z; }
      if (typeof d.desk === "string" && d.desk) c.desk = d.desk;
      if (typeof d.chair === "string" && d.chair) c.chair = d.chair;
      if (Object.keys(c).length) out.desks[id] = c;
    }
  }
  if (Array.isArray(r.pieces)) {
    const seen = new Set<string>();
    for (const p of r.pieces) {
      if (!p || typeof p.uid !== "string" || typeof p.item !== "string" || seen.has(p.item)) continue;
      if (typeof p.x !== "number" || typeof p.z !== "number") continue;
      seen.add(p.item);
      out.pieces.push({ uid: p.uid, item: p.item, x: p.x, z: p.z, rot: num(p.rot, -100, 100, 0), s: num(p.s, 0.6, 1.6, 1), ...(typeof p.y === "number" ? { y: num(p.y, 0, 4, 0) } : {}) });
    }
  }
  return out;
}

export const newUid = () => "p_" + Math.random().toString(36).slice(2, 8) + Date.now().toString(36).slice(-3);

/** Two layouts that look the same (used to know whether there is anything to save). */
export const sameLayout = (a: Layout, b: Layout) => JSON.stringify(a) === JSON.stringify(b);

/**
 * Light of the room: a mood preset, tuned by the temperature (-1 warm .. +1 cool) and brightness sliders.
 * Colors are hex strings; `sunDir` lowers the sun for the evening look.
 */
export interface LightMix { amb: number; ambColor: string; hemi: number; hemiSky: string; hemiGround: string; sun: number; sunColor: string; bg: string; sunPos: [number, number, number] }
export const MOOD_BASE: Record<Mood, LightMix> = {
  natural: { amb: 0.95, ambColor: "#f5f7fb", hemi: 0.85, hemiSky: "#f3f6fb", hemiGround: "#c6ccd5", sun: 1.55, sunColor: "#fff8ee", bg: "#8d97a7", sunPos: [10, 18, 12] },
  warm: { amb: 0.9, ambColor: "#ffe4c6", hemi: 0.8, hemiSky: "#ffeed9", hemiGround: "#d6bfa2", sun: 1.5, sunColor: "#ffd9a6", bg: "#a19181", sunPos: [10, 18, 12] },
  cool: { amb: 0.95, ambColor: "#e3edff", hemi: 0.9, hemiSky: "#e6f0ff", hemiGround: "#b9c6dc", sun: 1.5, sunColor: "#e1edff", bg: "#8190a8", sunPos: [10, 18, 12] },
  sunset: { amb: 0.7, ambColor: "#ffc9a3", hemi: 0.6, hemiSky: "#ffd6b3", hemiGround: "#8a6a66", sun: 1.7, sunColor: "#ff9c5a", bg: "#9a7d78", sunPos: [18, 8, 6] },
  night: { amb: 0.5, ambColor: "#b3bdd0", hemi: 0.5, hemiSky: "#9aa7c2", hemiGround: "#2a3140", sun: 0.55, sunColor: "#b9c4de", bg: "#151a24", sunPos: [10, 18, 12] },
};

/** Where the 12 original office slots stand (the 3D office draws slot items here until a layout is saved). */
export const LEGACY_SLOTS: Record<string, { pos: [number, number, number]; rot?: number }> = {
  shelf_1: { pos: [-10.2, 0, -9.3] },
  plant_1: { pos: [-7.6, 0, -9.0] },
  poster_1: { pos: [-5.4, 0, -9.0] },
  aquarium_1: { pos: [-3.0, 0, -9.2] },
  lamp_1: { pos: [-0.6, 0, -9.0] },
  poster_2: { pos: [1.4, 0, -9.0] },
  plant_2: { pos: [3.0, 0, -9.0] },
  trophy_1: { pos: [13.0, 0, 2.6], rot: -Math.PI / 2 },
  arcade_1: { pos: [13.2, 0, 5.4], rot: -Math.PI / 2 },
  rug_1: { pos: [-2.5, 0, 4.2] },
  coffee_1: { pos: [11.0, 0.95, -0.35], rot: -Math.PI / 2 },
  window_1: { pos: [-3.4, 1.85, -9.797] },
};
