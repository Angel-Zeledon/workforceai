/**
 * The office layout document (docs/architecture/office-editor.md) and the pure rules around it: defaults, normalizing
 * what the server sent, and the palettes the editor offers. No React, no imports of the app: unit tested with node.
 */
export type FloorKind = "concrete" | "wood" | "parquet" | "carpet" | "tile" | "marble";
export type Mood = "natural" | "warm" | "cool" | "sunset" | "night";
/**
 * Visual style of the whole office (scene, furniture, what the characters wear and how everything moves). "classic" is
 * the original look and the default; the rest are drawn by a transform layer (components/office/officeTheme.ts).
 */
export type OfficeStyle = "classic" | "bricks" | "clay" | "paper" | "toon" | "real";
export const OFFICE_STYLES: OfficeStyle[] = ["classic", "bricks", "clay", "paper", "toon", "real"];
export interface DeskCfg { x?: number; z?: number; desk?: string; chair?: string }
export interface Piece { uid: string; item: string; x: number; z: number; rot: number; s: number; y?: number }
export type ZoneName = "meeting" | "approvals" | "cafe";
/** The original decoration standing on its own (absolute coordinates). */
export type DecorId = "plant_a" | "plant_b" | "plant_c" | "plant_d" | "rack";
/** The furniture inside the three rooms (coordinates relative to the room's center, so moving the room carries it). */
export type RoomPartId =
  | "meeting_table" | "meeting_seat_1" | "meeting_seat_2" | "meeting_seat_3" | "meeting_seat_4" | "meeting_seat_5" | "meeting_seat_6" | "meeting_screen"
  | "approvals_desk" | "approvals_chair"
  | "cafe_counter" | "cafe_machine" | "cafe_mug_1" | "cafe_mug_2" | "cafe_mug_3" | "cafe_pot" | "cafe_table" | "cafe_table_mug" | "cafe_plant";
export type FixtureId = DecorId | RoomPartId;
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
  /** The built-in decoration (plants, rack) and the furniture of the rooms moved, turned or taken away (room parts: relative to their room). */
  fixtures: Partial<Record<FixtureId, FixtureCfg>>;
  /** Visual style; absent = "classic" (the original look). */
  style?: OfficeStyle;
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
  if (OFFICE_STYLES.includes(r.style) && r.style !== "classic") out.style = r.style;
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
/**
 * How a built-in object stands: `floor` on the floor; `top` small things that rest on a surface (the counter, the high
 * table) or on the floor when nothing is under them; `wall` hangs (the meeting screen) and collides with nothing.
 */
export type FixtureLevel = "floor" | "top" | "wall";
/**
 * A built-in object: where it stands (absolute for the decor, relative to its room's center for room parts), its scale
 * and tint, how much floor it takes (`w` x `d`), the height of its grab handle (`h`), and for surfaces the height of
 * their top (`surface`: what other things rest on).
 */
export interface FixtureDef { x: number; z: number; rot: number; s: number; tint?: string; w: number; d: number; h: number; level: FixtureLevel; room?: ZoneName; surface?: number }
const decor = (x: number, z: number, s: number, w: number, tint?: string): FixtureDef => ({ x, z, rot: 0, s, w, d: w, h: 1.8, level: "floor", ...(tint ? { tint } : {}) });
const part = (room: ZoneName, x: number, z: number, w: number, d: number, h: number, level: FixtureLevel = "floor", extra: Partial<FixtureDef> = {}): FixtureDef => ({ x, z, rot: 0, s: 1, w, d, h, level, room, ...extra });
/** The six meeting seats stand in a ring of radius 2.25 around the table (officeState.meetingSeat uses the same angles). */
const seat = (k: number): FixtureDef => { const a = (k / 6) * Math.PI * 2 + 0.3; return part("meeting", Math.sin(a) * 2.25, Math.cos(a) * 2.25, 0.6, 0.6, 0.45); };
/** The built-in objects: where they stand, their scale and tint, and how much floor they take. */
export const FIXTURES: Record<FixtureId, FixtureDef> = {
  plant_a: decor(-12.8, -8.6, 1.5, 0.8),
  plant_b: decor(-12.8, 8.4, 1.1, 0.6, "#8aa28c"),
  plant_c: decor(4.4, -8.8, 1.2, 0.7, "#8ba08d"),
  plant_d: decor(12.8, 8.6, 1.3, 0.7, "#7a9680"),
  rack: { x: -12.9, z: -0.5, rot: Math.PI / 2, s: 1, w: 0.9, d: 0.8, h: 1.8, level: "floor" },
  // meeting room: round table, six seats around it, the screen on the back wall
  meeting_table: part("meeting", 0, 0, 2.4, 2.4, 0.75), // round (1.45 m radius): a square a little smaller than it, so the seats fit around it
  meeting_seat_1: seat(0), meeting_seat_2: seat(1), meeting_seat_3: seat(2), meeting_seat_4: seat(3), meeting_seat_5: seat(4), meeting_seat_6: seat(5),
  meeting_screen: part("meeting", 0, -3.9, 3.4, 0.1, 1.7, "wall"),
  // approvals: the counter with the pile of pending approvals, and the user's chair behind it
  approvals_desk: part("approvals", 0, 0.1, 2.4, 1.0, 0.85),
  approvals_chair: part("approvals", 0, 1.5, 0.7, 0.7, 0.5),
  // cafeteria: the bar along the east edge with the machine, mugs and pot on it; a high table with a mug; a plant
  cafe_counter: part("cafe", 1.75, 0, 0.78, 3.88, 0.95, "floor", { surface: 0.95 }),
  cafe_machine: part("cafe", 1.8, -0.9, 0.42, 0.5, 0.55, "top"),
  cafe_mug_1: part("cafe", 1.7, 0.1, 0.12, 0.12, 0.12, "top"),
  cafe_mug_2: part("cafe", 1.7, 0.4, 0.12, 0.12, 0.12, "top"),
  cafe_mug_3: part("cafe", 1.7, 0.7, 0.12, 0.12, 0.12, "top"),
  cafe_pot: part("cafe", 1.8, 1.3, 0.24, 0.24, 0.26, "top"),
  cafe_table: part("cafe", -0.6, 0, 0.84, 0.84, 1.0, "floor", { surface: 0.97 }),
  cafe_table_mug: part("cafe", -0.5, 0.1, 0.12, 0.12, 0.12, "top"),
  cafe_plant: part("cafe", -1.6, 1.8, 0.6, 0.6, 1.0, "floor", { s: 0.8, tint: "#8aa28c" }),
};
export const FIXTURE_IDS = Object.keys(FIXTURES) as FixtureId[];
export const DECOR_IDS = FIXTURE_IDS.filter((id) => !FIXTURES[id].room) as DecorId[];
/** The parts of each room, in a stable order (the meeting seats 1..6 are taken in this order). */
export const ROOM_PARTS: Record<ZoneName, RoomPartId[]> = {
  meeting: FIXTURE_IDS.filter((id) => FIXTURES[id].room === "meeting") as RoomPartId[],
  approvals: FIXTURE_IDS.filter((id) => FIXTURES[id].room === "approvals") as RoomPartId[],
  cafe: FIXTURE_IDS.filter((id) => FIXTURES[id].room === "cafe") as RoomPartId[],
};
export const isRoomPart = (id: FixtureId): id is RoomPartId => !!FIXTURES[id].room;
/** Seats of the meeting room in order (seat k of the meeting is the k-th one still in the room). */
export const MEETING_SEATS = ROOM_PARTS.meeting.filter((id) => id.startsWith("meeting_seat_"));

export interface Pose { x: number; z: number; rot: number }
/** A floor rectangle (same shape as officeGeometry.Box). */
export interface FootBox { x: number; z: number; w: number; d: number; rot: number }

/** Center of a room in a layout: where the organization moved it, or its built-in place. */
export function zoneCenterOf(l: Layout, name: ZoneName): [number, number] {
  const z = l.zones[name];
  return z ? [z.x, z.z] : ZONE_DEFAULTS[name];
}
/** Footprint of a room relative to its own center. */
export function roomRect(name: ZoneName): FootBox {
  const s = ZONE_SIZE[name];
  return { x: s.dx, z: s.dz, w: s.w, d: s.d, rot: 0 };
}
/** Where a built-in object stands in its own frame (room-relative for room parts, absolute for the decor). */
export function fixturePose(l: Layout, id: FixtureId): Pose & { hidden: boolean } {
  const f = FIXTURES[id], c = l.fixtures[id];
  return { x: c?.x ?? f.x, z: c?.z ?? f.z, rot: c?.rot ?? f.rot, hidden: !!c?.hidden };
}
/** Where a built-in object stands on the floor plan (null when taken away). `center` overrides its room's center (a room being dragged). */
export function fixtureWorld(l: Layout, id: FixtureId, center?: readonly [number, number]): Pose | null {
  const p = fixturePose(l, id);
  if (p.hidden) return null;
  const room = FIXTURES[id].room;
  if (!room) return { x: p.x, z: p.z, rot: p.rot };
  const [cx, cz] = center ?? zoneCenterOf(l, room);
  return { x: cx + p.x, z: cz + p.z, rot: p.rot };
}
/** Floor rectangle of a built-in object at a pose (in the same frame as the pose). */
export const partFootprint = (id: FixtureId, at: Pose): FootBox => ({ x: at.x, z: at.z, w: FIXTURES[id].w, d: FIXTURES[id].d, rot: at.rot });

/** Local offset (lx, lz) of a frame turned by `rot`, in the parent frame (the same convention as three.js rotation.y). */
export const turn = (lx: number, lz: number, rot: number): [number, number] => [lx * Math.cos(rot) + lz * Math.sin(rot), -lx * Math.sin(rot) + lz * Math.cos(rot)];
/** Whether a point lies on a rotated rectangle. */
function onBox(px: number, pz: number, b: FootBox): boolean {
  const [lx, lz] = turn(px - b.x, pz - b.z, -b.rot);
  return Math.abs(lx) <= b.w / 2 + 1e-9 && Math.abs(lz) <= b.d / 2 + 1e-9;
}

/**
 * What a small (`top`) object rests on: the highest surface of its room under its center (`at` overrides its
 * position), or the floor when there is none (also when that surface was taken away: nothing floats).
 */
export function restOn(l: Layout, id: FixtureId, at?: { x: number; z: number }): { on: RoomPartId | null; y: number } {
  const def = FIXTURES[id];
  if (def.level !== "top" || !def.room) return { on: null, y: 0 };
  const p = at ?? fixturePose(l, id);
  let best: { on: RoomPartId | null; y: number } = { on: null, y: 0 };
  for (const s of ROOM_PARTS[def.room]) {
    const sd = FIXTURES[s];
    if (!sd.surface || s === id) continue;
    const sp = fixturePose(l, s);
    if (sp.hidden || !onBox(p.x, p.z, partFootprint(s, sp))) continue;
    if (sd.surface > best.y) best = { on: s, y: sd.surface };
  }
  return best;
}
/** The small objects resting on a surface (they move and turn with it). */
export function carriedBy(l: Layout, id: FixtureId): RoomPartId[] {
  const room = FIXTURES[id].room;
  if (!room || !FIXTURES[id].surface || fixturePose(l, id).hidden) return [];
  return ROOM_PARTS[room].filter((o) => o !== id && !fixturePose(l, o).hidden && restOn(l, o).on === id);
}

/** The stored entry of an object at a pose (rotation only when not zero, nothing else). */
const entry = (p: Pose, hidden = false): FixtureCfg => ({ x: p.x, z: p.z, ...(p.rot ? { rot: p.rot } : {}), ...(hidden ? { hidden: true } : {}) });
/**
 * Moves / turns a built-in object (in its own frame). A surface carries what rests on it: those objects keep their
 * place on it, turned with it. Anything else stays where it is.
 */
export function moveFixture(l: Layout, id: FixtureId, to: Pose): Layout {
  const from = fixturePose(l, id);
  const fixtures = { ...l.fixtures, [id]: entry(to) };
  const dr = to.rot - from.rot;
  for (const o of carriedBy(l, id)) {
    const op = fixturePose(l, o);
    // offset from the surface in the surface's own frame, then back out with its new pose
    const [lx, lz] = turn(op.x - from.x, op.z - from.z, -from.rot);
    const [wx, wz] = turn(lx, lz, to.rot);
    fixtures[o] = entry({ x: to.x + wx, z: to.z + wz, rot: op.rot + dr });
  }
  return { ...l, fixtures };
}
/** Takes a built-in object away (it keeps its spot for when it is put back; what rested on it now stands on the floor). */
export function hideFixture(l: Layout, id: FixtureId): Layout {
  return { ...l, fixtures: { ...l.fixtures, [id]: entry(fixturePose(l, id), true) } };
}
/** Puts a built-in object back where it was taken from (nothing stored when that is its built-in spot). */
export function showFixture(l: Layout, id: FixtureId): Layout {
  const p = fixturePose(l, id), f = FIXTURES[id];
  const fixtures = { ...l.fixtures };
  if (p.x === f.x && p.z === f.z && p.rot === f.rot) delete fixtures[id];
  else fixtures[id] = entry(p);
  return { ...l, fixtures };
}
/** Back to its built-in spot (what it carries goes with it). */
export function resetFixture(l: Layout, id: FixtureId): Layout {
  const f = FIXTURES[id];
  const moved = moveFixture(l, id, { x: f.x, z: f.z, rot: f.rot });
  const fixtures = { ...moved.fixtures };
  delete fixtures[id];
  return { ...moved, fixtures };
}

/**
 * The room-relative rectangles a room part at `at` must not overlap: the other parts of its room on the same level
 * (floor things with floor things and with small things standing on the floor; small things on a surface with the
 * others on that surface). A surface ignores what it carries; the wall screen collides with nothing.
 */
export function partNeighbors(l: Layout, id: FixtureId, at: Pose): FootBox[] {
  const def = FIXTURES[id];
  if (!def.room || def.level === "wall") return [];
  const level = def.level === "top" ? restOn(l, id, at).on : null;
  const carried = new Set<FixtureId>(carriedBy(l, id));
  const out: FootBox[] = [];
  for (const o of ROOM_PARTS[def.room]) {
    if (o === id || carried.has(o)) continue;
    const od = FIXTURES[o], op = fixturePose(l, o);
    if (op.hidden || od.level === "wall") continue;
    if ((od.level === "top" ? restOn(l, o).on : null) !== level) continue;
    out.push(partFootprint(o, op));
  }
  return out;
}

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
