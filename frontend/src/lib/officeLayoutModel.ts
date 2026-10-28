/**
 * The office layout document (docs/architecture/office-editor.md) and the pure rules around it: defaults, normalizing
 * what the server sent, and the palettes the editor offers. No React, no imports of the app: unit tested with node.
 */
export type FloorKind = "concrete" | "wood" | "parquet" | "carpet" | "tile" | "marble";
export type Mood = "natural" | "warm" | "cool" | "sunset" | "night";
export interface DeskCfg { x?: number; z?: number; desk?: string; chair?: string }
export interface Piece { uid: string; item: string; x: number; z: number; rot: number; s: number; y?: number }
export type ZoneName = "meeting" | "approvals" | "cafe";
export type FixtureId = "plant_a" | "plant_b" | "plant_c" | "plant_d" | "rack";
export interface Spot { x: number; z: number }
export interface FixtureCfg { x: number; z: number; rot?: number; hidden?: boolean }
export interface Layout {
  v: 1;
  walls: { color: string; trim: string; windows: boolean };
  floor: { kind: FloorKind; tint: string };
  light: { mood: Mood; temp: number; brightness: number };
  desks: Record<string, DeskCfg>;
  pieces: Piece[];
  seeded?: boolean;
  /** The three rooms moved from their built-in place (center of each room). */
  zones: Partial<Record<ZoneName, Spot>>;
  /** The built-in decoration (plants, rack) moved, turned or taken away. */
  fixtures: Partial<Record<FixtureId, FixtureCfg>>;
}

export const FLOOR_KINDS: FloorKind[] = ["concrete", "wood", "parquet", "carpet", "tile", "marble"];
export const MOODS: Mood[] = ["natural", "warm", "cool", "sunset", "night"];

export const DEFAULT_LAYOUT: Layout = {
  v: 1,
  walls: { color: "#f3f4f7", trim: "#3a414c", windows: true },
  floor: { kind: "concrete", tint: "#ffffff" },
  light: { mood: "natural", temp: 0, brightness: 1 },
  desks: {},
  pieces: [],
  zones: {},
  fixtures: {},
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
    walls: { color: hex(r.walls?.color, DEFAULT_LAYOUT.walls.color), trim: hex(r.walls?.trim, DEFAULT_LAYOUT.walls.trim), windows: r.walls?.windows !== false },
    floor: { kind: FLOOR_KINDS.includes(r.floor?.kind) ? r.floor.kind : "concrete", tint: hex(r.floor?.tint, "#ffffff") },
    light: { mood: MOODS.includes(r.light?.mood) ? r.light.mood : "natural", temp: num(r.light?.temp, -1, 1, 0), brightness: num(r.light?.brightness, 0.5, 1.5, 1) },
    desks: {},
    pieces: [],
    zones: {},
    fixtures: {},
  };
  if (r.seeded) out.seeded = true;
  if (r.zones && typeof r.zones === "object") {
    for (const name of ZONE_NAMES) { const z = r.zones[name]; if (z && Number.isFinite(z.x) && Number.isFinite(z.z)) out.zones[name] = { x: z.x, z: z.z }; }
  }
  if (r.fixtures && typeof r.fixtures === "object") {
    for (const id of Object.keys(FIXTURES) as FixtureId[]) {
      const f = r.fixtures[id];
      if (f && Number.isFinite(f.x) && Number.isFinite(f.z)) out.fixtures[id] = { x: f.x, z: f.z, ...(Number.isFinite(f.rot) && f.rot ? { rot: f.rot } : {}), ...(f.hidden ? { hidden: true } : {}) };
    }
  }
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

export const ZONE_NAMES: ZoneName[] = ["meeting", "approvals", "cafe"];
/** Where the three rooms stand until the organization moves them (the center of each room). */
export const ZONE_DEFAULTS: Record<ZoneName, [number, number]> = { meeting: [9.5, -6.2], approvals: [9.5, 5.4], cafe: [9.2, 1.2] };
/** Footprint of each room on the floor plan, relative to its center (`dx`, `dz`). */
export const ZONE_SIZE: Record<ZoneName, { w: number; d: number; dx: number; dz: number }> = {
  meeting: { w: 7.8, d: 7.4, dx: 0, dz: 0 },
  approvals: { w: 5.6, d: 4.8, dx: 0, dz: 0.5 },
  cafe: { w: 4.1, d: 4.6, dx: 0.1, dz: 0 },
};
/** The built-in decoration: where it stands, its scale and tint, and how much floor it takes. */
export const FIXTURES: Record<FixtureId, { x: number; z: number; rot: number; s: number; tint?: string; w: number; d: number }> = {
  plant_a: { x: -12.8, z: -8.6, rot: 0, s: 1.5, w: 0.8, d: 0.8 },
  plant_b: { x: -12.8, z: 8.4, rot: 0, s: 1.1, tint: "#8aa28c", w: 0.6, d: 0.6 },
  plant_c: { x: 4.4, z: -8.8, rot: 0, s: 1.2, tint: "#8ba08d", w: 0.7, d: 0.7 },
  plant_d: { x: 12.8, z: 8.6, rot: 0, s: 1.3, tint: "#7a9680", w: 0.7, d: 0.7 },
  rack: { x: -12.9, z: -0.5, rot: Math.PI / 2, s: 1, w: 0.9, d: 0.8 },
};
export const FIXTURE_IDS = Object.keys(FIXTURES) as FixtureId[];

/**
 * "Automatic (real time)" light (B5): the room follows the viewer's local clock. Night until dawn (5-8 h), daylight
 * (8-17 h), dusk (17-21 h), night again. Between keyframes the presets are blended, so the change is gradual.
 */
export type DayPhase = "dawn" | "day" | "dusk" | "night";
const DAY_KEYS: [number, Mood][] = [[0, "night"], [5, "night"], [6.5, "sunset"], [8.5, "natural"], [17, "natural"], [19, "sunset"], [21, "night"], [24, "night"]];

/** Phase of the day for a local hour (fractional, 0..24). */
export function dayPhase(hour: number): DayPhase {
  const h = ((hour % 24) + 24) % 24;
  if (h >= 5 && h < 8) return "dawn";
  if (h >= 8 && h < 17) return "day";
  if (h >= 17 && h < 21) return "dusk";
  return "night";
}

function lerpHex(a: string, b: string, t: number): string {
  const pa = parseInt(a.slice(1), 16), pb = parseInt(b.slice(1), 16);
  const ch = (p: number, s: number) => (p >> s) & 255;
  const mix = (s: number) => Math.round(ch(pa, s) + (ch(pb, s) - ch(pa, s)) * t);
  return "#" + ((1 << 24) | (mix(16) << 16) | (mix(8) << 8) | mix(0)).toString(16).slice(1);
}

/** The light mix for a local hour: a blend of the two presets around it. */
export function timeOfDayMix(hour: number): LightMix & { phase: DayPhase } {
  const h = ((hour % 24) + 24) % 24;
  let i = 0;
  while (i < DAY_KEYS.length - 2 && h >= DAY_KEYS[i + 1][0]) i++;
  const [h0, m0] = DAY_KEYS[i], [h1, m1] = DAY_KEYS[i + 1];
  const t = h1 > h0 ? Math.min(1, Math.max(0, (h - h0) / (h1 - h0))) : 0;
  const a = MOOD_BASE[m0], b = MOOD_BASE[m1];
  const n = (x: number, y: number) => x + (y - x) * t;
  return {
    phase: dayPhase(h),
    amb: n(a.amb, b.amb), ambColor: lerpHex(a.ambColor, b.ambColor, t), hemi: n(a.hemi, b.hemi), hemiSky: lerpHex(a.hemiSky, b.hemiSky, t),
    hemiGround: lerpHex(a.hemiGround, b.hemiGround, t), sun: n(a.sun, b.sun), sunColor: lerpHex(a.sunColor, b.sunColor, t), bg: lerpHex(a.bg, b.bg, t),
    sunPos: [n(a.sunPos[0], b.sunPos[0]), n(a.sunPos[1], b.sunPos[1]), n(a.sunPos[2], b.sunPos[2])],
  };
}

/** Local hour (fractional) of a date in the viewer's time zone. */
export const localHour = (d: Date): number => d.getHours() + d.getMinutes() / 60;
