import type { ShopItem } from "@/lib/rewards";
import { darken, hash, lighten } from "./itemStyle";

/**
 * Shape specs for the office furniture (docs/architecture/office-editor.md). The catalog only carries an id, a rarity and
 * one or two colors; the SHAPE of every desk, chair, table, sofa, cabinet, divider, whiteboard, appliance and light lives
 * here so the 3D model (Furniture.tsx) and the flat shop preview (FurnitureArt.tsx) draw the same thing from one
 * description. Specs are keyed by the id without its category prefix. An id missing from a table falls back to a spec
 * derived from its hash, never to a single "default" look.
 *
 * Units are meters. Floor pieces: origin at the floor-center of the footprint, front faces +z. Wall pieces (whiteboards,
 * sconces, signs): origin at the bottom-center, back plane at z = -d/2, the editor sets the height (`y`). Ceiling pieces
 * hang from the ceiling (CEIL) down to their own height, so they are placed on the floor plan like any other piece.
 */
export const CEIL = 3;

export type Mount = "floor" | "wall" | "ceiling";
export type Finish = "wood" | "lacquer" | "metal" | "glass" | "stone" | "fabric" | "leather" | "plastic" | "felt" | "frame" | "board";
/** A color role resolved against the item ("c1"/"c2" its two catalog colors, "d"/"l" darker/lighter) or a literal hex. */
export type Tone = string;

/** Resolves a tone against the item's colors. */
export function tone(item: ShopItem | undefined, t: Tone, fallback = "#c4aa84"): string {
  if (t[0] === "#") return t;
  const c1 = item?.color || fallback;
  const c2 = item?.color2 || darken(c1, 0.3);
  switch (t) {
    case "c1": return c1;
    case "c2": return c2;
    case "d1": return darken(c1, 0.25);
    case "d2": return darken(c2, 0.25);
    case "l1": return lighten(c1, 0.35);
    case "l2": return lighten(c2, 0.35);
    default: return c1;
  }
}

const key = (item: ShopItem) => item.id.slice(item.category.length + 1);
function pick<T>(table: Record<string, T>, item: ShopItem): T {
  const k = key(item);
  if (table[k]) return table[k];
  const all = Object.values(table);
  return all[hash(item.id) % all.length];
}

// ---------------------------------------------------------------- desks

export type DeskLegs = "post" | "splay" | "panel" | "sled" | "hairpin" | "pipe" | "x" | "slab" | "lift" | "pedestal";
export interface DeskSpec {
  top: Tone; topFin: Finish; thick: number; h: number;
  legs: DeskLegs; leg: Tone; legFin: Finish;
  panel: Tone | null;       // modesty panel behind the legs
  rim?: Tone;               // glowing strip under the front edge (gaming) or brass edge
  glow?: boolean;           // the rim is emissive
  wing?: boolean;           // L-shaped return
  veins?: boolean;          // stone veins
}
/** The desk every workstation has until the organization picks another (OfficeEnv's original geometry). */
export const DESK_DEFAULT: DeskSpec = { top: "#d9c4a3", topFin: "wood", thick: 0.06, h: 0.72, legs: "post", leg: "#3a414c", legFin: "frame", panel: "#59616d" };
const D = (o: Partial<DeskSpec> & Pick<DeskSpec, "top" | "legs" | "leg">): DeskSpec => ({ topFin: "wood", thick: 0.05, h: 0.72, legFin: "metal", panel: null, ...o });
const DESKS: Record<string, DeskSpec> = {
  scandinavian: D({ top: "c1", legs: "splay", leg: "d1", legFin: "wood", thick: 0.04, panel: "c2" }),
  walnut: D({ top: "c1", legs: "panel", leg: "c2", legFin: "wood", panel: "c2" }),
  white_lacquer: D({ top: "c1", topFin: "lacquer", legs: "sled", leg: "c2", panel: "c1" }),
  black_steel: D({ top: "c1", topFin: "lacquer", thick: 0.04, legs: "sled", leg: "c1", panel: "c1" }),
  bamboo: D({ top: "c1", legs: "post", leg: "c2", legFin: "wood", panel: "c2" }),
  birch: D({ top: "c1", legs: "hairpin", leg: "c2" }),
  pastel_mint: D({ top: "c1", topFin: "lacquer", legs: "splay", leg: "c2", legFin: "plastic", panel: "c1" }),
  pastel_pink: D({ top: "c1", topFin: "lacquer", legs: "panel", leg: "c2", legFin: "plastic", panel: "c1" }),
  pastel_sky: D({ top: "c1", topFin: "lacquer", legs: "sled", leg: "c2", panel: "c1" }),
  industrial_pipe: D({ top: "c1", thick: 0.07, legs: "pipe", leg: "c2" }),
  glass: D({ top: "c1", topFin: "glass", thick: 0.02, legs: "sled", leg: "c2", legFin: "metal" }),
  standing: D({ top: "c1", topFin: "lacquer", thick: 0.04, h: 0.92, legs: "lift", leg: "c2" }),
  l_shaped: D({ top: "c1", legs: "post", leg: "c2", panel: "c2", wing: true }),
  gamer: D({ top: "c1", topFin: "lacquer", legs: "x", leg: "c1", panel: "c1", rim: "c2", glow: true }),
  marble: D({ top: "c1", topFin: "stone", thick: 0.06, legs: "slab", leg: "c2", veins: true }),
  executive: D({ top: "c1", topFin: "lacquer", thick: 0.07, legs: "pedestal", leg: "c1", legFin: "wood", panel: "c1", rim: "c2" }),
};
export function deskSpec(item: ShopItem): DeskSpec { return pick(DESKS, item); }

// ---------------------------------------------------------------- chairs

export type ChairKind = "task" | "exec" | "gaming" | "stool" | "wood" | "ball" | "bean" | "cube" | "kneel" | "velvet" | "shell" | "throne";
export interface ChairSpec { kind: ChairKind; seat: Tone; seatFin: Finish; back: Tone; base: Tone; baseFin: Finish; arms?: boolean; head?: boolean; mesh?: boolean; trim?: Tone }
const CH = (kind: ChairKind, o: Partial<ChairSpec> = {}): ChairSpec => ({ kind, seat: "c1", seatFin: "fabric", back: "c1", base: "c2", baseFin: "metal", ...o });
const CHAIRS: Record<string, ChairSpec> = {
  mesh_graphite: CH("task", { mesh: true, arms: true, back: "#2c323b" }),
  mesh_teal: CH("task", { mesh: true, arms: true, back: "d1", base: "c2" }),
  stool_oak: CH("stool", { seatFin: "wood", baseFin: "wood" }),
  wood_classic: CH("wood", { seatFin: "wood", baseFin: "wood" }),
  ball_blue: CH("ball", { seatFin: "plastic" }),
  cube_mint: CH("cube", { seat: "c1", trim: "c2" }),
  bean_bag: CH("bean", { seatFin: "fabric", trim: "c2" }),
  ergo_orange: CH("task", { mesh: true, arms: true, head: true, back: "l1" }),
  kneeling_birch: CH("kneel", { seat: "#4a5568", seatFin: "fabric", base: "c1", baseFin: "wood", trim: "c2" }),
  gaming_red: CH("gaming", { seatFin: "leather", back: "c1", trim: "c2", arms: true, head: true }),
  velvet_emerald: CH("velvet", { seatFin: "fabric", base: "c2", baseFin: "metal", arms: true }),
  shell_white: CH("shell", { seatFin: "plastic", base: "c2", baseFin: "wood" }),
  executive_leather: CH("exec", { seatFin: "leather", back: "c1", base: "c2", arms: true, head: true }),
  throne_gold: CH("throne", { seat: "c2", seatFin: "fabric", back: "c1", base: "c1", baseFin: "metal", arms: true, trim: "c2" }),
};
export function chairSpec(item: ShopItem): ChairSpec { return pick(CHAIRS, item); }

// ---------------------------------------------------------------- tables

export type TableLegs = "post" | "splay" | "pedestal" | "trestle" | "hairpin" | "x" | "cross" | "net";
export interface TableSpec { shape: "rect" | "round" | "hex"; w: number; d: number; h: number; thick: number; top: Tone; topFin: Finish; legs: TableLegs; leg: Tone; legFin: Finish; veins?: boolean; extra?: "net" | "shelf" }
const T = (shape: TableSpec["shape"], w: number, d: number, h: number, legs: TableLegs, o: Partial<TableSpec> = {}): TableSpec =>
  ({ shape, w, d, h, thick: 0.04, top: "c1", topFin: "wood", legs, leg: "c2", legFin: "metal", ...o });
const TABLES: Record<string, TableSpec> = {
  coffee_oak: T("rect", 1.0, 0.55, 0.42, "splay", { legFin: "wood", leg: "d1", extra: "shelf" }),
  side_round: T("round", 0.5, 0.5, 0.5, "hairpin", { topFin: "lacquer" }),
  console: T("rect", 1.3, 0.35, 0.8, "post", { leg: "c2", thick: 0.035, extra: "shelf" }),
  high_bar: T("round", 0.7, 0.7, 1.05, "cross", { topFin: "lacquer" }),
  round_white: T("round", 1.2, 1.2, 0.74, "pedestal", { topFin: "lacquer", thick: 0.04 }),
  glass_coffee: T("rect", 1.1, 0.6, 0.4, "x", { topFin: "glass", thick: 0.02 }),
  meeting_long: T("rect", 2.8, 1.1, 0.74, "trestle", { thick: 0.05 }),
  nesting_hex: T("hex", 0.8, 0.8, 0.4, "post", { topFin: "lacquer", thick: 0.04 }),
  marble_round: T("round", 1.0, 1.0, 0.74, "pedestal", { topFin: "stone", veins: true, thick: 0.05, legFin: "metal" }),
  pingpong: T("rect", 2.4, 1.3, 0.76, "x", { topFin: "plastic", thick: 0.04, extra: "net", legFin: "frame" }),
};
export function tableSpec(item: ShopItem): TableSpec { return pick(TABLES, item); }

// ---------------------------------------------------------------- sofas

export type SofaKind = "sofa" | "armchair" | "bench" | "pouf" | "corner" | "egg" | "chester" | "modular";
export interface SofaSpec { kind: SofaKind; w: number; d: number; seats: number; seat: Tone; frame: Tone; cush: Tone; fin: Finish; arms: "block" | "roll" | "slim" | "none"; legs: "wood" | "metal" | "none"; tuft?: boolean }
const SO = (kind: SofaKind, w: number, d: number, o: Partial<SofaSpec> = {}): SofaSpec =>
  ({ kind, w, d, seats: 2, seat: "c1", frame: "d1", cush: "c2", fin: "fabric", arms: "block", legs: "wood", ...o });
const SOFAS: Record<string, SofaSpec> = {
  pouf_coral: SO("pouf", 0.6, 0.6, { seats: 1, arms: "none", legs: "none" }),
  bench_oak: SO("bench", 1.4, 0.45, { seats: 2, arms: "none", fin: "wood", seat: "c1", legs: "metal", frame: "c2" }),
  armchair_mustard: SO("armchair", 0.95, 0.9, { seats: 1, arms: "block" }),
  two_seater_grey: SO("sofa", 1.7, 0.9, { seats: 2, arms: "slim", legs: "metal", frame: "#2c323b" }),
  corner_blue: SO("corner", 2.4, 1.7, { seats: 3, arms: "block", legs: "none" }),
  loveseat_velvet: SO("sofa", 1.5, 0.85, { seats: 2, arms: "roll", legs: "metal", frame: "#c9a24a" }),
  leather_brown: SO("sofa", 2.0, 0.95, { seats: 3, arms: "block", fin: "leather", legs: "metal", frame: "#2c323b" }),
  modular_mint: SO("modular", 2.6, 0.9, { seats: 3, arms: "none", legs: "none" }),
  egg_chair: SO("egg", 0.95, 0.9, { seats: 1, arms: "none", legs: "metal", frame: "#c0c6cf" }),
  chesterfield: SO("chester", 2.1, 0.95, { seats: 3, arms: "roll", fin: "leather", tuft: true, frame: "d1" }),
};
export function sofaSpec(item: ShopItem): SofaSpec { return pick(SOFAS, item); }

// ---------------------------------------------------------------- cabinets

export type CabKind = "filing" | "lowbook" | "locker" | "sideboard" | "wardrobe" | "tallbook" | "cubes" | "credenza" | "vitrine" | "rolling";
export interface CabSpec { kind: CabKind; w: number; h: number; d: number; n: number; body: Tone; front: Tone; handle: Tone; legs?: boolean; fin: Finish }
const CB = (kind: CabKind, w: number, h: number, d: number, n: number, o: Partial<CabSpec> = {}): CabSpec =>
  ({ kind, w, h, d, n, body: "c1", front: "c1", handle: "#c0c6cf", fin: "wood", ...o });
const CABS: Record<string, CabSpec> = {
  filing_grey: CB("filing", 0.55, 1.3, 0.62, 4, { fin: "metal", front: "c1", body: "c1" }),
  bookcase_low: CB("lowbook", 1.2, 0.8, 0.35, 2, {}),
  locker_blue: CB("locker", 1.2, 1.8, 0.5, 3, { fin: "metal", front: "c1", body: "d1" }),
  sideboard_walnut: CB("sideboard", 1.6, 0.8, 0.45, 3, { front: "c1", body: "d1", handle: "c2", legs: true }),
  wardrobe_white: CB("wardrobe", 1.2, 2.0, 0.6, 2, { fin: "lacquer", handle: "c2" }),
  bookcase_tall: CB("tallbook", 0.9, 1.9, 0.32, 5, {}),
  storage_cubes: CB("cubes", 1.2, 1.2, 0.4, 3, { body: "c1", front: "c2", fin: "lacquer" }),
  credenza_teak: CB("credenza", 1.8, 0.75, 0.45, 4, { front: "c1", body: "d1", handle: "c2", legs: true }),
  glass_display: CB("vitrine", 1.0, 1.9, 0.45, 4, { body: "c1", front: "c2", fin: "wood" }),
  drawers_rolling: CB("rolling", 0.45, 0.6, 0.55, 3, { fin: "metal" }),
};
export function cabSpec(item: ShopItem): CabSpec { return pick(CABS, item); }

// ---------------------------------------------------------------- dividers

export type DivKind = "fabric" | "folding" | "slats" | "cubes" | "glass" | "felt" | "plants" | "living";
export interface DivSpec { kind: DivKind; w: number; h: number; d: number; body: Tone; frame: Tone }
const DV = (kind: DivKind, w: number, h: number, d: number, o: Partial<DivSpec> = {}): DivSpec => ({ kind, w, h, d, body: "c1", frame: "c2", ...o });
const DIVS: Record<string, DivSpec> = {
  fabric_grey: DV("fabric", 1.6, 1.6, 0.1),
  screen_folding: DV("folding", 1.7, 1.7, 0.35),
  wood_slats: DV("slats", 1.6, 1.8, 0.1),
  cube_shelf: DV("cubes", 1.6, 1.4, 0.35),
  glass_panel: DV("glass", 1.6, 1.7, 0.1),
  felt_acoustic: DV("felt", 1.4, 1.6, 0.12),
  plant_boxes: DV("plants", 1.8, 1.8, 0.4),
  living_wall: DV("living", 2.0, 2.1, 0.2),
};
export function divSpec(item: ShopItem): DivSpec { return pick(DIVS, item); }

// ---------------------------------------------------------------- whiteboards (wall)

export type WbKind = "white" | "cork" | "chalk" | "kanban" | "glass" | "smart";
export interface WbSpec { kind: WbKind; w: number; h: number }
const WBS: Record<string, WbSpec> = {
  classic: { kind: "white", w: 1.6, h: 1.0 },
  cork: { kind: "cork", w: 1.2, h: 0.9 },
  chalkboard: { kind: "chalk", w: 1.4, h: 0.95 },
  kanban: { kind: "kanban", w: 1.8, h: 1.0 },
  glass: { kind: "glass", w: 1.6, h: 1.0 },
  smart: { kind: "smart", w: 1.9, h: 1.1 },
};
export function wbSpec(item: ShopItem): WbSpec { return pick(WBS, item); }

// ---------------------------------------------------------------- appliances

export type ApplKind = "printer" | "shredder" | "coatrack" | "bin" | "cooler" | "purifier" | "fridge" | "copier" | "safe" | "vending";
export interface ApplSpec { kind: ApplKind; w: number; d: number; h: number }
const APPL: Record<string, ApplSpec> = {
  printer: { kind: "printer", w: 0.7, d: 0.6, h: 0.95 },
  shredder: { kind: "shredder", w: 0.45, d: 0.35, h: 0.6 },
  coat_rack: { kind: "coatrack", w: 0.55, d: 0.55, h: 1.8 },
  recycling_bin: { kind: "bin", w: 0.9, d: 0.4, h: 0.75 },
  water_cooler: { kind: "cooler", w: 0.4, d: 0.4, h: 1.15 },
  air_purifier: { kind: "purifier", w: 0.32, d: 0.32, h: 0.95 },
  fridge: { kind: "fridge", w: 0.65, d: 0.65, h: 1.7 },
  copier: { kind: "copier", w: 0.9, d: 0.75, h: 1.05 },
  safe: { kind: "safe", w: 0.55, d: 0.5, h: 0.7 },
  vending: { kind: "vending", w: 0.95, d: 0.85, h: 1.85 },
};
export function applSpec(item: ShopItem): ApplSpec { return pick(APPL, item); }

// ---------------------------------------------------------------- lights

export type LightKind = "sconce" | "cone" | "table" | "string" | "uplight" | "globe" | "neon" | "arc" | "chandelier" | "moon";
export interface LightSpec { kind: LightKind; mount: Mount; w: number; d: number; glow: { tone: Tone; intensity: number; distance: number } | null }
const LT = (kind: LightKind, mount: Mount, w: number, d: number, glow: LightSpec["glow"]): LightSpec => ({ kind, mount, w, d, glow });
const LIGHTS: Record<string, LightSpec> = {
  sconce_brass: LT("sconce", "wall", 0.22, 0.16, { tone: "#ffd9a0", intensity: 0.7, distance: 3.5 }),
  pendant_cone: LT("cone", "ceiling", 0.5, 0.5, { tone: "#ffe2b0", intensity: 0.9, distance: 5 }),
  table_lamp: LT("table", "floor", 0.5, 0.5, { tone: "#ffd9a0", intensity: 0.9, distance: 4 }),
  string_lights: LT("string", "wall", 2.0, 0.06, { tone: "#ffd9a0", intensity: 0.6, distance: 4 }),
  floor_uplight: LT("uplight", "floor", 0.4, 0.4, { tone: "#fff1d6", intensity: 1.1, distance: 5.5 }),
  pendant_globe: LT("globe", "ceiling", 0.5, 0.5, { tone: "#fff1d6", intensity: 1.0, distance: 5 }),
  neon_sign: LT("neon", "wall", 1.0, 0.06, { tone: "c1", intensity: 0.8, distance: 3.5 }),
  arc_floor: LT("arc", "floor", 1.3, 0.5, { tone: "#fff1d6", intensity: 1.0, distance: 5 }),
  chandelier: LT("chandelier", "ceiling", 0.95, 0.95, { tone: "#ffe2b0", intensity: 1.7, distance: 7 }),
  moon: LT("moon", "wall", 0.6, 0.14, { tone: "#ffe9b8", intensity: 0.7, distance: 4 }),
};
export function lightSpec(item: ShopItem): LightSpec { return pick(LIGHTS, item); }

// ---------------------------------------------------------------- the contract

/** Floor footprint in meters, before rotation and scale. */
export function footprint(item: ShopItem): { w: number; d: number } {
  switch (item.category) {
    case "desk": return { w: 2.2, d: 0.95 };
    case "chair": return { w: 0.6, d: 0.6 };
    case "table": { const s = tableSpec(item); return { w: s.w, d: s.d }; }
    case "sofa": { const s = sofaSpec(item); return { w: s.w, d: s.d }; }
    case "cabinet": { const s = cabSpec(item); return { w: s.w, d: s.d }; }
    case "divider": { const s = divSpec(item); return { w: s.w, d: s.d }; }
    case "whiteboard": { const s = wbSpec(item); return { w: s.w, d: 0.06 }; }
    case "appliance": { const s = applSpec(item); return { w: s.w, d: s.d }; }
    case "light": { const s = lightSpec(item); return { w: s.w, d: s.d }; }
    default: return { w: 1, d: 1 };
  }
}

export function mountOf(item: ShopItem): Mount {
  if (item.category === "whiteboard") return "wall";
  if (item.category === "light") return lightSpec(item).mount;
  return "floor";
}

/** The real light a piece casts (the editor decides how many to spawn); null = decorative only. */
export function lightOf(item: ShopItem): { color: string; intensity: number; distance: number } | null {
  if (item.category !== "light") return null;
  const g = lightSpec(item).glow;
  return g ? { color: tone(item, g.tone, "#ffd9a0"), intensity: g.intensity, distance: g.distance } : null;
}
