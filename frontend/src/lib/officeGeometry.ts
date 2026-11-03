/**
 * Pure geometry of the office editor (no React, no three): footprints, snapping, overlap tests and the free-floor rules.
 * Kept free of imports so it can be unit tested with `node --test`. Units are world meters; rotation is about Y.
 */
/** A floor footprint: a rotated rectangle, or a circle of diameter `w` when `round` (tables, seats, mugs, plants). */
export interface Box { x: number; z: number; w: number; d: number; rot: number; round?: boolean }

/** The walkable slab (walls sit just outside it). */
export const FLOOR = { x0: -13.9, x1: 13.9, z0: -9.7, z1: 9.7 };
/** Wall faces pieces can hang on: the north wall (z = -10) and the west wall (x = -14.2). */
export const WALL_N = { z: -9.84, rot: 0 };
export const WALL_W = { x: -14.04, rot: Math.PI / 2 };

export const GRID = 0.25;
export const snap = (v: number, step = GRID) => Math.round(v / step) * step;
export const clamp = (v: number, a: number, b: number) => Math.min(b, Math.max(a, v));
/** Rotation snapped to quarter turns. */
export const snapRot = (r: number) => Math.round(r / (Math.PI / 2)) * (Math.PI / 2);

/** Corners of a rotated rectangle (counter-clockwise). */
export function corners(b: Box): [number, number][] {
  const c = Math.cos(b.rot), s = Math.sin(b.rot), hw = b.w / 2, hd = b.d / 2;
  return ([[-hw, -hd], [hw, -hd], [hw, hd], [-hw, hd]] as [number, number][]).map(([px, pz]) => [b.x + px * c + pz * s, b.z - px * s + pz * c]);
}

/** Separating-axis overlap of two rotated rectangles; `margin` shrinks both (touching edges do not collide). */
export function overlap(a: Box, b: Box, margin = 0.02): boolean {
  if (a.round || b.round) return roundOverlap(a, b, margin);
  const shrink = (r: Box): Box => ({ ...r, w: Math.max(0.01, r.w - margin * 2), d: Math.max(0.01, r.d - margin * 2) });
  const A = corners(shrink(a)), B = corners(shrink(b));
  for (const poly of [A, B]) {
    for (let i = 0; i < 4; i++) {
      const p = poly[i], q = poly[(i + 1) % 4];
      const nx = -(q[1] - p[1]), nz = q[0] - p[0];
      let minA = Infinity, maxA = -Infinity, minB = Infinity, maxB = -Infinity;
      for (const [x, z] of A) { const v = x * nx + z * nz; if (v < minA) minA = v; if (v > maxA) maxA = v; }
      for (const [x, z] of B) { const v = x * nx + z * nz; if (v < minB) minB = v; if (v > maxB) maxB = v; }
      if (maxA <= minB || maxB <= minA) return false;
    }
  }
  return true;
}

/** Axis-aligned bounds of a rotated rectangle. */
/** Overlap when one or both footprints are circles (diameter `w`): circle-circle by distance, circle-rectangle by the
 *  rectangle's closest point to the circle's center. `margin` shrinks both, like the rectangle test. */
function roundOverlap(a: Box, b: Box, margin: number): boolean {
  if (a.round && b.round) return Math.hypot(a.x - b.x, a.z - b.z) < a.w / 2 + b.w / 2 - margin * 2;
  const c = a.round ? a : b, r = a.round ? b : a;
  const cos = Math.cos(r.rot), sin = Math.sin(r.rot), dx = c.x - r.x, dz = c.z - r.z;
  // the circle's center in the rectangle's own frame (inverse of corners())
  const lx = dx * cos - dz * sin, lz = dx * sin + dz * cos;
  const hw = Math.max(0.005, r.w / 2 - margin), hd = Math.max(0.005, r.d / 2 - margin);
  const qx = Math.max(-hw, Math.min(hw, lx)), qz = Math.max(-hd, Math.min(hd, lz));
  return Math.hypot(lx - qx, lz - qz) < Math.max(0.005, c.w / 2 - margin);
}

export function bounds(b: Box) {
  if (b.round) { const r = b.w / 2; return { x0: b.x - r, x1: b.x + r, z0: b.z - r, z1: b.z + r }; }
  const cs = corners(b);
  return { x0: Math.min(...cs.map((c) => c[0])), x1: Math.max(...cs.map((c) => c[0])), z0: Math.min(...cs.map((c) => c[1])), z1: Math.max(...cs.map((c) => c[1])) };
}

export const insideFloor = (b: Box) => { const r = bounds(b); return r.x0 >= FLOOR.x0 && r.x1 <= FLOOR.x1 && r.z0 >= FLOOR.z0 && r.z1 <= FLOOR.z1; };

/** The central walkway the characters cross between the desk rows: nothing stands in it. */
export const WALKWAY: Box = { x: 0, z: -1.3, w: 29, d: 1.7, rot: 0 };

/** Moves a box fully inside the floor. */
export function keepInside(b: Box): Box {
  const r = bounds(b);
  let { x, z } = b;
  if (r.x0 < FLOOR.x0) x += FLOOR.x0 - r.x0;
  if (r.x1 > FLOOR.x1) x -= r.x1 - FLOOR.x1;
  if (r.z0 < FLOOR.z0) z += FLOOR.z0 - r.z0;
  if (r.z1 > FLOOR.z1) z -= r.z1 - FLOOR.z1;
  return { ...b, x, z };
}

/** Footprint of a workstation: desk, chair and the space to push the chair back. */
export function deskBox(x: number, z: number): Box { return { x, z: z + 0.6, w: 2.4, d: 2.3, rot: 0 }; }

/** Which wall a point is nearest to, and where a piece hanging on it sits (position + facing). */
export function wallSnap(x: number, z: number, depth: number): { x: number; z: number; rot: number; wall: "n" | "w" } {
  const dn = Math.abs(z - WALL_N.z), dw = Math.abs(x - WALL_W.x);
  if (dn <= dw) return { x: clamp(x, FLOOR.x0 + 0.5, FLOOR.x1 - 0.5), z: WALL_N.z + depth / 2, rot: 0, wall: "n" };
  return { x: WALL_W.x + depth / 2, z: clamp(z, FLOOR.z0 + 0.5, FLOOR.z1 - 0.5), rot: Math.PI / 2, wall: "w" };
}
