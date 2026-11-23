// Pure maths of the "Plano" office style (isometric illustration drawn by hand in PixiJS): the (x, z) -> screen projection,
// colour helpers, floor palettes and the mapping from the 3D joint angles (gestures.ts) to the 2D figure.
// No Pixi / React / app imports: it runs under `node --test` (see isoMath.test.mjs).

/** Pixels per meter before the scene is fitted to the canvas. */
export const S = 30;
export const CS = Math.cos(Math.PI / 6);
/** World (x, z, height) -> scene pixels. The camera looks from +x, +z: what is greater x + z is closer (lower on screen). */
export const iso = (x: number, z: number, h = 0) => ({ x: (x - z) * CS * S, y: (x + z) * 0.5 * S - h * S });
/** Inverse of iso(): the floor point (x, z) that is drawn at scene pixels (sx, sy) when it is at height `h` above the floor. */
export const unIso = (sx: number, sy: number, h = 0) => {
  const a = sx / (CS * S), b = (2 * (sy + h * S)) / S; // a = x - z, b = x + z
  return { x: (a + b) / 2, z: (b - a) / 2 };
};
/** Whether a floor point is inside a footprint (a rotated rectangle, or a circle of diameter w when round), as officeGeometry.corners() lays it out. */
export function pointInBox(b: { x: number; z: number; w: number; d: number; rot: number; round?: boolean }, px: number, pz: number): boolean {
  const dx = px - b.x, dz = pz - b.z;
  if (b.round) return Math.hypot(dx, dz) <= b.w / 2;
  const c = Math.cos(b.rot), s = Math.sin(b.rot);
  const lx = dx * c - dz * s, lz = dx * s + dz * c; // the point in the box own frame (inverse of corners())
  return Math.abs(lx) <= b.w / 2 && Math.abs(lz) <= b.d / 2;
}
/** Depth key for sorting: greater is closer to the camera. */
export const depthOf = (x: number, z: number) => x + z;

/** Offset (lx, lz) of a frame turned by `rot` in world axes (the three.js rotation.y convention used by the layout). */
export const turn = (lx: number, lz: number, rot: number): [number, number] => [lx * Math.cos(rot) + lz * Math.sin(rot), -lx * Math.sin(rot) + lz * Math.cos(rot)];

// ------------------------------------------------------------------------------------------------ colours

/** "#rgb" / "#rrggbb" -> 0xrrggbb (anything else falls back to `fallback`). */
export function hexToNum(hex: string | undefined, fallback = 0x9aa4b2): number {
  if (typeof hex !== "string") return fallback;
  const m = /^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$/.exec(hex.trim());
  if (!m) return fallback;
  let h = m[1];
  if (h.length === 3) h = h.split("").map((c) => c + c).join("");
  return parseInt(h, 16);
}
const ch = (c: number, s: number) => (c >> s) & 255;
const pack = (r: number, g: number, b: number) => ((Math.round(Math.max(0, Math.min(255, r))) << 16) | (Math.round(Math.max(0, Math.min(255, g))) << 8) | Math.round(Math.max(0, Math.min(255, b)))) >>> 0;
/** Multiplies the channels (f < 1 darkens, f > 1 lightens, clamped). */
export const shade = (c: number, f: number) => pack(ch(c, 16) * f, ch(c, 8) * f, ch(c, 0) * f);
export const mixNum = (a: number, b: number, t: number) => pack(ch(a, 16) + (ch(b, 16) - ch(a, 16)) * t, ch(a, 8) + (ch(b, 8) - ch(a, 8)) * t, ch(a, 0) + (ch(b, 0) - ch(a, 0)) * t);
/** Multiplies two colours channel by channel (a floor tint over its base colour). */
export const multiplyNum = (a: number, b: number) => pack((ch(a, 16) * ch(b, 16)) / 255, (ch(a, 8) * ch(b, 8)) / 255, (ch(a, 0) * ch(b, 0)) / 255);
/** The three faces of a block from one base colour: top (lightest), face toward +z (left on screen), face toward +x (right, darkest). */
export const faces = (base: number) => ({ top: mixNum(base, 0xffffff, 0.14), left: shade(base, 0.9), right: shade(base, 0.76) });

export type FloorKindLite = "concrete" | "wood" | "parquet" | "carpet" | "tile" | "marble";
export interface FloorLook { base: number; line: number; pattern: "grid" | "planks" | "checker" | "plain" | "veins"; step: number }
const FLOORS: Record<FloorKindLite, FloorLook> = {
  concrete: { base: 0xe3e7ed, line: 0xd2d8e1, pattern: "grid", step: 2 },
  wood: { base: 0xe2c9a4, line: 0xd0b48c, pattern: "planks", step: 0.5 },
  parquet: { base: 0xd9b88c, line: 0xc4a074, pattern: "checker", step: 1 },
  carpet: { base: 0xc3ccd9, line: 0xb4bfce, pattern: "plain", step: 1 },
  tile: { base: 0xf0f2f5, line: 0xd5dae2, pattern: "grid", step: 1 },
  marble: { base: 0xf1efec, line: 0xdcd8d2, pattern: "veins", step: 2 },
};
/** The floor colours for a layout floor (kind + tint multiplies the base like the 3D texture does). */
export function floorLook(kind: string, tint: string): FloorLook {
  const f = FLOORS[(kind in FLOORS ? kind : "concrete") as FloorKindLite];
  const t = hexToNum(tint, 0xffffff);
  return { ...f, base: multiplyNum(f.base, t), line: multiplyNum(f.line, t) };
}

// ------------------------------------------------------------------------------------------------ figure

/** The 3D joint angles the animation layer produces (the subset the flat figure uses). */
export interface PoseLike { pelvisY: number; torsoX: number; headX: number; lShX: number; rShX: number; lElX: number; rElX: number; lHipX: number; rHipX: number; lKneeX: number; rKneeX: number }
export interface Pose2 {
  hipY: number; lean: number; legA: [number, number]; knee: [number, number]; armA: [number, number]; elbow: [number, number];
  head: number; sx: number; sy: number; seated: boolean;
}
export const STAND_HIP = 30;
export const SEAT_HIP = 18;
const clampN = (v: number, lo: number, hi: number) => (v < lo ? lo : v > hi ? hi : v);

/**
 * 3D angles (rotation about X, forward = negative for limbs, positive for the torso) -> the side-on figure: limb angles
 * are measured from "down" toward where the figure looks. The standing pelvis height is 0.4 m (gestures.STAND_Y).
 */
export function mapPose(p: PoseLike, standY = 0.4): Pose2 {
  const seated = p.lHipX < -1 && p.rHipX < -1;
  return {
    hipY: seated ? SEAT_HIP : STAND_HIP + clampN((p.pelvisY - standY) * 80, -6, 8),
    lean: clampN(p.torsoX, -0.4, 0.7),
    legA: [-p.lHipX, -p.rHipX], knee: [-p.lKneeX, -p.rKneeX],
    armA: [-p.lShX, -p.rShX], elbow: [-p.lElX, -p.rElX],
    head: clampN(p.headX, -0.5, 0.7), sx: 1, sy: 1, seated,
  };
}

/** Which way the figure faces on screen: `dir` 1 looks to the right of the screen, -1 to the left; `back` = seen from behind. */
export function facing(yaw: number): { dir: 1 | -1; back: boolean } {
  const dx = Math.sin(yaw), dz = Math.cos(yaw);
  const sx = dx - dz, sy = (dx + dz) * 0.5;
  return { dir: sx >= 0 ? 1 : -1, back: sy < -0.05 };
}

/** Skin tones people actually have: a custom value outside the warm, mid-luminance band falls back to a neutral tone. */
export function humanSkin(hex: string | undefined): number {
  const c = hexToNum(hex, 0xe0ac86);
  const r = ch(c, 16), g = ch(c, 8), b = ch(c, 0);
  const warm = r >= g && g >= b - 6; // skin is warm: red >= green >= blue
  const lum = 0.2126 * r + 0.7152 * g + 0.0722 * b;
  return warm && lum >= 70 && lum <= 235 ? c : 0xe0ac86;
}
