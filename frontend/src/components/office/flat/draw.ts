// Vector illustration of the real office for the "Plano" style: room, floor, walls, desks, chairs, the built-in fixtures
// and every catalog category, as isometric blocks with the lab's ink outline. Pixi is only a type here (the scene passes
// the Graphics). Everything is drawn from the real layout / catalog data; no script.
import type * as PIXI from "pixi.js";
import type { ShopItem } from "@/lib/rewards";
import type { FixtureId, Layout, Piece } from "@/lib/officeLayoutModel";
import { FIXTURES } from "@/lib/officeLayoutModel";
import { FLOOR } from "@/lib/officeGeometry";
import { applSpec, cabSpec, chairSpec, deskSpec, divSpec, footprint, lightSpec, sofaSpec, tableSpec, tone, wbSpec, DESK_DEFAULT, CHAIR_DEFAULT } from "../furnitureStyle";
import { CS, S, faces, floorLook, hexToNum, iso, mixNum, shade, turn } from "./isoMath";

const INK = 0x1f2a37;
const ACCENT = 0x0a66c2;
const OAK = 0xd9c4a3, GRAPHITE = 0x3a414c, GRAPHITE_L = 0x59616d, STEEL = 0xb3bac4;
type Pt = { x: number; y: number };
type Faces = { top: number; left: number; right: number };
const asFaces = (c: number | Faces): Faces => (typeof c === "number" ? faces(c) : c);

// ------------------------------------------------------------------------------------------------ primitives

export function poly(g: PIXI.Graphics, pts: Pt[], fill: number, alpha = 1, stroke = true, ink = 0.7) {
  g.poly(pts.flatMap((p) => [p.x, p.y])).fill({ color: fill, alpha });
  if (stroke) g.stroke({ width: 1.1, color: INK, alpha: ink, join: "round" });
}

/** A block turned by `rot` about Y: the faces that look at the camera and its top. `y0` is the height of its bottom. */
export function box(g: PIXI.Graphics, cx: number, cz: number, w: number, d: number, h: number, y0: number, color: number | Faces, rot = 0, alpha = 1) {
  const f = asFaces(color);
  const loc: [number, number][] = [[-w / 2, -d / 2], [w / 2, -d / 2], [w / 2, d / 2], [-w / 2, d / 2]];
  const wp = loc.map(([lx, lz]) => { const [dx, dz] = turn(lx, lz, rot); return [cx + dx, cz + dz] as [number, number]; });
  const normals: [number, number][] = [[0, -1], [1, 0], [0, 1], [-1, 0]];
  for (let i = 0; i < 4; i++) {
    const [nlx, nlz] = normals[i];
    const [nx, nz] = turn(nlx, nlz, rot);
    if (nx + nz <= 0.01) continue; // faces away from the camera
    const a = wp[i], b = wp[(i + 1) % 4];
    const t = Math.max(0, Math.min(1, nx / (nx + nz)));
    poly(g, [iso(a[0], a[1], y0), iso(b[0], b[1], y0), iso(b[0], b[1], y0 + h), iso(a[0], a[1], y0 + h)], mixNum(f.left, f.right, t), alpha);
  }
  poly(g, wp.map(([x, z]) => iso(x, z, y0 + h)), f.top, alpha);
}

/** An upright cylinder (round tables, stools, pots). */
export function cyl(g: PIXI.Graphics, cx: number, cz: number, r: number, h: number, y0: number, color: number | Faces, alpha = 1) {
  const f = asFaces(color);
  const rx = r * S * Math.SQRT2 * CS, ry = r * S * Math.SQRT2 * 0.5;
  const b = iso(cx, cz, y0), t = iso(cx, cz, y0 + h);
  g.ellipse(b.x, b.y, rx, ry).fill({ color: f.left, alpha });
  g.rect(b.x - rx, t.y, rx * 2, b.y - t.y).fill({ color: f.left, alpha });
  g.moveTo(b.x - rx, t.y).lineTo(b.x - rx, b.y).moveTo(b.x + rx, t.y).lineTo(b.x + rx, b.y).stroke({ width: 1.1, color: INK, alpha: 0.7 });
  g.ellipse(b.x, b.y, rx, ry).stroke({ width: 1.1, color: INK, alpha: 0.4 });
  g.ellipse(t.x, t.y, rx, ry).fill({ color: f.top, alpha }).stroke({ width: 1.1, color: INK, alpha: 0.7 });
}

/** A flat shape on the floor / a vertical quad on a turned plane: local (lx, y) on the face at local depth `lz`. */
function faceQuad(g: PIXI.Graphics, cx: number, cz: number, rot: number, lx0: number, lx1: number, lz: number, y0: number, y1: number, color: number, alpha = 1, stroke = false) {
  const pt = (lx: number, y: number) => { const [dx, dz] = turn(lx, lz, rot); return iso(cx + dx, cz + dz, y); };
  poly(g, [pt(lx0, y0), pt(lx1, y0), pt(lx1, y1), pt(lx0, y1)], color, alpha, stroke);
}
function flatRect(g: PIXI.Graphics, cx: number, cz: number, w: number, d: number, rot: number, y: number, fill: number, alpha = 1, stroke = false) {
  const loc: [number, number][] = [[-w / 2, -d / 2], [w / 2, -d / 2], [w / 2, d / 2], [-w / 2, d / 2]];
  poly(g, loc.map(([lx, lz]) => { const [dx, dz] = turn(lx, lz, rot); return iso(cx + dx, cz + dz, y); }), fill, alpha, stroke, 0.35);
}
function flatRing(g: PIXI.Graphics, cx: number, cz: number, r: number, y: number, color: number, width = 1.4, alpha = 1) {
  const c = iso(cx, cz, y);
  g.ellipse(c.x, c.y, r * S * Math.SQRT2 * CS, r * S * Math.SQRT2 * 0.5).stroke({ width, color, alpha });
}

/** A piece's own frame: everything below is placed relative to it, turned and scaled with it. */
interface Fr { x: number; z: number; rot: number; s: number; y: number }
const at = (f: Fr, lx: number, lz: number): [number, number] => { const [dx, dz] = turn(lx * f.s, lz * f.s, f.rot); return [f.x + dx, f.z + dz]; };
const fbox = (g: PIXI.Graphics, f: Fr, lx: number, lz: number, w: number, d: number, h: number, y0: number, color: number | Faces, alpha = 1) => { const [x, z] = at(f, lx, lz); box(g, x, z, w * f.s, d * f.s, h * f.s, f.y + y0 * f.s, color, f.rot, alpha); };
const fcyl = (g: PIXI.Graphics, f: Fr, lx: number, lz: number, r: number, h: number, y0: number, color: number | Faces, alpha = 1) => { const [x, z] = at(f, lx, lz); cyl(g, x, z, r * f.s, h * f.s, f.y + y0 * f.s, color, alpha); };
const fface = (g: PIXI.Graphics, f: Fr, lx0: number, lx1: number, lz: number, y0: number, y1: number, color: number, alpha = 1) => faceQuad(g, f.x, f.z, f.rot, lx0 * f.s, lx1 * f.s, lz * f.s, f.y + y0 * f.s, f.y + y1 * f.s, color, alpha);
const fglobe = (g: PIXI.Graphics, f: Fr, lx: number, lz: number, y: number, r: number, color: number) => { const [x, z] = at(f, lx, lz); const c = iso(x, z, f.y + y * f.s); g.circle(c.x, c.y, r * f.s * S).fill(color).stroke({ width: 1.1, color: INK, alpha: 0.6 }); };

// ------------------------------------------------------------------------------------------------ room

/** The floor slab with its pattern, and the two back walls (north and west) with trim and windows. */
export function drawRoom(g: PIXI.Graphics, layout: Layout) {
  const { x0, x1, z0, z1 } = { x0: FLOOR.x0 - 0.15, x1: FLOOR.x1 + 0.15, z0: FLOOR.z0 - 0.15, z1: FLOOR.z1 + 0.15 };
  const look = floorLook(layout.floor.kind, layout.floor.tint);
  // the slab: a visible thickness under the floor
  const sf = faces(0x8d97a6);
  poly(g, [iso(x0, z1, -0.3), iso(x1, z1, -0.3), iso(x1, z1, 0), iso(x0, z1, 0)], sf.left);
  poly(g, [iso(x1, z0, -0.3), iso(x1, z1, -0.3), iso(x1, z1, 0), iso(x1, z0, 0)], sf.right);
  poly(g, [iso(x0, z0), iso(x1, z0), iso(x1, z1), iso(x0, z1)], look.base);
  switch (look.pattern) {
    case "grid": case "planks": {
      const st = look.step;
      if (look.pattern === "grid") for (let x = Math.ceil(x0 / st) * st; x < x1; x += st) g.moveTo(iso(x, z0).x, iso(x, z0).y).lineTo(iso(x, z1).x, iso(x, z1).y);
      for (let z = Math.ceil(z0 / st) * st; z < z1; z += st) g.moveTo(iso(x0, z).x, iso(x0, z).y).lineTo(iso(x1, z).x, iso(x1, z).y);
      g.stroke({ width: 1, color: look.line, alpha: look.pattern === "planks" ? 0.8 : 1 });
      break;
    }
    case "checker": {
      const st = 2;
      let i = 0;
      for (let x = Math.floor(x0 / st) * st; x < x1; x += st, i++) for (let z = Math.floor(z0 / st) * st, j = 0; z < z1; z += st, j++) {
        if ((i + j) % 2) continue;
        const ax = Math.max(x, x0), bx = Math.min(x + st, x1), az = Math.max(z, z0), bz = Math.min(z + st, z1);
        g.poly([iso(ax, az).x, iso(ax, az).y, iso(bx, az).x, iso(bx, az).y, iso(bx, bz).x, iso(bx, bz).y, iso(ax, bz).x, iso(ax, bz).y]).fill({ color: look.line, alpha: 0.45 });
      }
      break;
    }
    case "veins": {
      for (let i = 0; i < 9; i++) {
        const sx = x0 + ((i * 37) % 100) / 100 * (x1 - x0), sz = z0 + ((i * 61) % 100) / 100 * (z1 - z0);
        g.moveTo(iso(sx, sz).x, iso(sx, sz).y).lineTo(iso(sx + 3 + (i % 3), sz + 2.4 - (i % 2)).x, iso(sx + 3 + (i % 3), sz + 2.4 - (i % 2)).y);
      }
      g.stroke({ width: 1, color: look.line, alpha: 0.8 });
      break;
    }
    default: break;
  }
  g.poly([iso(x0, z0).x, iso(x0, z0).y, iso(x1, z0).x, iso(x1, z0).y, iso(x1, z1).x, iso(x1, z1).y, iso(x0, z1).x, iso(x0, z1).y]).stroke({ width: 1.1, color: INK, alpha: 0.5, join: "round" });

  // walls: thick low walls with skirting, a trim line and the glass band
  const wallColor = hexToNum(layout.walls.color, 0xf3f4f7), trim = hexToNum(layout.walls.trim, GRAPHITE);
  const H = 3, T = 0.3;
  const north = { x0: -14.2 - T / 2, x1: 14.2 + T / 2, z: -10 };
  const wf = faces(wallColor);
  // north wall (inner face looks at +z)
  poly(g, [iso(north.x0, north.z + T / 2), iso(north.x1, north.z + T / 2), iso(north.x1, north.z + T / 2, H), iso(north.x0, north.z + T / 2, H)], wf.left);
  poly(g, [iso(north.x1, north.z - T / 2), iso(north.x1, north.z + T / 2), iso(north.x1, north.z + T / 2, H), iso(north.x1, north.z - T / 2, H)], wf.right);
  poly(g, [iso(north.x0, north.z - T / 2, H), iso(north.x1, north.z - T / 2, H), iso(north.x1, north.z + T / 2, H), iso(north.x0, north.z + T / 2, H)], wf.top);
  // west wall (inner face looks at +x)
  const wz0 = -10 - T / 2, wz1 = 10 + T / 2, wx = -14.2;
  poly(g, [iso(wx + T / 2, wz0), iso(wx + T / 2, wz1), iso(wx + T / 2, wz1, H), iso(wx + T / 2, wz0, H)], wf.right);
  poly(g, [iso(wx - T / 2, wz0, H), iso(wx + T / 2, wz0, H), iso(wx + T / 2, wz1, H), iso(wx - T / 2, wz1, H)], wf.top);
  const skirt = shade(wallColor, 0.9);
  const zi = north.z + T / 2, xi = wx + T / 2;
  poly(g, [iso(north.x0, zi - 0.01), iso(north.x1, zi - 0.01), iso(north.x1, zi - 0.01, 0.5), iso(north.x0, zi - 0.01, 0.5)], skirt, 1, false);
  poly(g, [iso(xi - 0.01, wz0), iso(xi - 0.01, wz1), iso(xi - 0.01, wz1, 0.5), iso(xi - 0.01, wz0, 0.5)], shade(skirt, 0.9), 1, false);
  g.moveTo(iso(north.x0, zi, 2.96).x, iso(north.x0, zi, 2.96).y).lineTo(iso(north.x1, zi, 2.96).x, iso(north.x1, zi, 2.96).y).stroke({ width: 3, color: trim, alpha: 0.9 });
  g.moveTo(iso(xi, wz0, 2.96).x, iso(xi, wz0, 2.96).y).lineTo(iso(xi, wz1, 2.96).x, iso(xi, wz1, 2.96).y).stroke({ width: 3, color: trim, alpha: 0.9 });
  if (layout.walls.windows) {
    const glass = 0xd3e4f3;
    // north: the band from x = -13.7 to 6.9, in panes of ~2.6 m
    const nPanes = Math.max(1, Math.round(20.6 / 2.6)), pane = 20.6 / nPanes;
    poly(g, [iso(-13.7, zi, 1.3), iso(6.9, zi, 1.3), iso(6.9, zi, 2.5), iso(-13.7, zi, 2.5)], glass, 1, true, 0.55);
    for (let i = 0; i <= nPanes; i++) { const x = -13.7 + i * pane; g.moveTo(iso(x, zi).x, iso(x, zi, 1.3).y).lineTo(iso(x, zi).x, iso(x, zi, 2.5).y); }
    g.stroke({ width: 1.6, color: GRAPHITE_L, alpha: 0.85 });
    // west: from z = -8.5 to 8.5
    const wPanes = Math.max(1, Math.round(17 / 2.6)), wp = 17 / wPanes;
    poly(g, [iso(xi, -8.5, 1.3), iso(xi, 8.5, 1.3), iso(xi, 8.5, 2.5), iso(xi, -8.5, 2.5)], glass, 1, true, 0.55);
    for (let i = 0; i <= wPanes; i++) { const z = -8.5 + i * wp; g.moveTo(iso(xi, z).x, iso(xi, z, 1.3).y).lineTo(iso(xi, z).x, iso(xi, z, 2.5).y); }
    g.stroke({ width: 1.6, color: GRAPHITE_L, alpha: 0.85 });
    // a soft reflection on the panes
    poly(g, [iso(-9, zi, 1.35), iso(-7.6, zi, 1.35), iso(-8.6, zi, 2.45), iso(-10, zi, 2.45)], 0xffffff, 0.35, false);
    poly(g, [iso(xi, -3, 1.35), iso(xi, -1.6, 1.35), iso(xi, -2.6, 2.45), iso(xi, -4, 2.45)], 0xffffff, 0.35, false);
  }
}

/** The floor mats of the three rooms and of each workstation (flat, under everything). */
export function drawMat(g: PIXI.Graphics, kind: "meeting" | "approvals" | "cafe" | "desk", cx: number, cz: number) {
  switch (kind) {
    case "meeting":
      flatRect(g, cx, cz, 7.6, 7.2, 0, 0.012, 0xcfd6df, 1, true);
      flatRing(g, cx, cz, 3.0, 0.02, 0x8d99ab, 1.2, 0.8);
      break;
    case "approvals": flatRect(g, cx, cz + 0.5, 5.4, 4.6, 0, 0.012, 0xd5dae1, 1, true); break;
    case "cafe": flatRect(g, cx + 0.1, cz, 3.9, 4.4, 0, 0.012, 0xe1d8c8, 1, true); break;
    case "desk": flatRect(g, cx + 0.1, cz + 0.95, 3.5, 2.6, 0, 0.012, 0xc4cbd6, 0.9, true); break;
  }
}

// ------------------------------------------------------------------------------------------------ desks and chairs

/** A workstation's desk (the catalog desk the organization picked, or the original), without the monitor. */
export function drawDesk(g: PIXI.Graphics, x: number, z: number, item: ShopItem | undefined, accent: number) {
  const sp = item ? deskSpec(item) : DESK_DEFAULT;
  const top = hexToNum(tone(item, sp.top), OAK), leg = hexToNum(tone(item, sp.leg), GRAPHITE);
  const panel = sp.panel ? hexToNum(tone(item, sp.panel), GRAPHITE_L) : null;
  const W = 2.2, D = 0.95, H = sp.h, th = Math.max(0.04, sp.thick);
  const f: Fr = { x, z, rot: 0, s: 1, y: 0 };
  if (sp.legs === "panel" || sp.legs === "slab" || sp.legs === "pedestal") {
    fbox(g, f, -W / 2 + 0.06, 0, 0.08, D - 0.1, H - th, 0, leg);
    fbox(g, f, W / 2 - 0.06, 0, 0.08, D - 0.1, H - th, 0, leg);
  } else {
    for (const sx of [-1, 1]) for (const sz of [-1, 1]) fbox(g, f, sx * (W / 2 - 0.06), sz * (D / 2 - 0.06), 0.05, 0.05, H - th, 0, leg);
  }
  if (panel !== null) fbox(g, f, 0, -D / 2 + 0.05, W - 0.2, 0.03, H * 0.5, H * 0.38, panel);
  fbox(g, f, 0, 0, W, D, th, H - th, top);
  if (sp.wing) fbox(g, f, W / 2 - 0.25, D / 2 + 0.35, 0.8, 0.7, th, H - th, top);
  if (sp.rim) fbox(g, f, 0, D / 2 - 0.01, W - 0.1, 0.02, 0.025, H - th - 0.03, hexToNum(tone(item, sp.rim), accent));
  else fbox(g, f, 0, D / 2 - 0.01, W - 0.1, 0.02, 0.02, H - th - 0.02, accent); // the edge in the role colour, like the 3D desk
  // keyboard and mouse
  fbox(g, f, 0, 0.25, 0.46, 0.16, 0.02, H, 0xe7eaee);
  fbox(g, f, 0.4, 0.25, 0.07, 0.11, 0.025, H, 0xe7eaee);
}
/** Where the monitor's screen is, for the live content: its center on the desk and the screen's size (meters). */
export const MONITOR = { dz: -0.12, w: 0.86, h: 0.5, y0: 0.98 };
export function drawMonitorBody(g: PIXI.Graphics, x: number, z: number, deskH: number) {
  const mz = z + MONITOR.dz;
  box(g, x, mz, 0.26, 0.18, 0.02, deskH, 0xb3bac4);
  box(g, x, mz, 0.04, 0.03, 0.22, deskH + 0.02, 0xb3bac4);
  box(g, x, mz, 0.98, 0.04, 0.58, deskH + 0.2, 0x2c323b);
}
export function deskHeightOf(item: ShopItem | undefined): number { return item ? deskSpec(item).h : DESK_DEFAULT.h; }

/** The chair: the base and seat (under the person) and the backrest (in front of their back, seen from the camera). */
export function drawChair(low: PIXI.Graphics, back: PIXI.Graphics, x: number, z: number, item: ShopItem | undefined) {
  const sp = item ? chairSpec(item) : CHAIR_DEFAULT;
  const seat = hexToNum(tone(item, sp.seat), GRAPHITE_L), bk = hexToNum(tone(item, sp.back), GRAPHITE), base = hexToNum(tone(item, sp.base), GRAPHITE);
  const f: Fr = { x, z, rot: 0, s: 1, y: 0 };
  switch (sp.kind) {
    case "ball": fcyl(low, f, 0, 0, 0.26, 0.5, 0, seat); return;
    case "bean": fbox(low, f, 0, 0, 0.7, 0.7, 0.4, 0, seat); return;
    case "cube": fbox(low, f, 0, 0, 0.5, 0.5, 0.46, 0, seat); return;
    case "stool": case "kneel": fcyl(low, f, 0, 0, 0.02, 0.4, 0, base); fcyl(low, f, 0, 0, 0.24, 0.07, 0.43, seat); return;
    default: break;
  }
  fcyl(low, f, 0, 0, 0.03, 0.42, 0, base);
  fbox(low, f, 0, 0, 0.6, 0.06, 0.04, 0.02, base);
  fbox(low, f, 0, 0, 0.06, 0.6, 0.04, 0.02, base);
  fbox(low, f, 0, 0, 0.5, 0.48, 0.08, 0.42, seat);
  const bh = sp.kind === "throne" ? 0.62 : sp.head ? 0.5 : 0.34;
  fbox(back, f, 0, 0.23, 0.48, 0.05, bh, 0.5, bk);
  if (sp.arms) { fbox(back, f, -0.27, 0.0, 0.04, 0.34, 0.04, 0.62, base); fbox(back, f, 0.27, 0.0, 0.04, 0.34, 0.04, 0.62, base); }
}

// ------------------------------------------------------------------------------------------------ built-in fixtures

function plantAt(g: PIXI.Graphics, f: Fr, tint: number | null) {
  const green = tint ?? 0x6f9a7d;
  fcyl(g, f, 0, 0, 0.2, 0.36, 0, 0xe5e7eb);
  const c = (() => { const [x, z] = at(f, 0, 0); return iso(x, z, f.y + 0.7 * f.s); })();
  const k = f.s * S / 30;
  for (const [dx, dy, r, sh] of [[-9, 6, 11, 0.9], [9, 4, 10, 1], [0, -10, 12, 1.08], [-3, 12, 8, 0.95]] as const) g.circle(c.x + dx * k, c.y + dy * k, r * k).fill(shade(green, sh)).stroke({ width: 1, color: INK, alpha: 0.4 });
}

/** One built-in object (a piece of a room or a piece of decor) at its world pose; `y` is the height of the surface it rests on. */
export function drawFixture(g: PIXI.Graphics, id: FixtureId, x: number, z: number, rot: number, y: number) {
  const def = FIXTURES[id];
  const f: Fr = { x, z, rot, s: 1, y };
  switch (id) {
    case "rack": {
      fbox(g, f, 0, 0, 0.9, 0.8, 2.0, 0, 0x3d4551);
      fface(g, f, -0.39, 0.39, 0.41, 0.07, 1.93, 0x1f242b);
      for (let i = 0; i < 9; i++) { fface(g, f, -0.31, 0.31, 0.415, 0.2 + i * 0.19, 0.23 + i * 0.19, 0x47505d, 1); fglobe(g, f, 0.3, 0.42, 0.26 + i * 0.19, 0.012, i % 4 === 1 ? 0x8aa0c6 : 0x7aa790); }
      return;
    }
    case "plant_a": case "plant_b": case "plant_c": case "plant_d": case "cafe_plant":
      plantAt(g, { ...f, s: def.s }, def.tint ? hexToNum(def.tint) : null); return;
    case "meeting_table":
      fcyl(g, f, 0, 0, 0.22, 0.64, 0, GRAPHITE);
      fcyl(g, f, 0, 0, 1.45, 0.06, 0.66, OAK);
      return;
    case "meeting_seat_1": case "meeting_seat_2": case "meeting_seat_3": case "meeting_seat_4": case "meeting_seat_5": case "meeting_seat_6":
      fcyl(g, f, 0, 0, 0.3, 0.38, 0, Number(id.slice(-1)) % 2 ? GRAPHITE : GRAPHITE_L); return;
    case "meeting_screen":
      fbox(g, f, 0, 0, 3.4, 0.08, 1.6, 0.9, 0x2c323b);
      fface(g, f, -1.6, 1.6, 0.045, 1.0, 2.4, 0xe9eef5);
      for (let i = 0; i < 5; i++) { const h = 0.2 + (i % 3) * 0.14; fface(g, f, -1.1 + i * 0.55 - 0.16, -1.1 + i * 0.55 + 0.16, 0.05, 1.35, 1.35 + h, [0x6f86c9, 0x9fb0d8, 0x6f86c9, 0xb9c5e2, 0x8196cf][i]); }
      return;
    case "approvals_desk":
      fbox(g, f, -1.12, 0, 0.05, 0.8, 0.66, 0, GRAPHITE);
      fbox(g, f, 1.12, 0, 0.05, 0.8, 0.66, 0, GRAPHITE);
      fbox(g, f, 0, -0.38, 2.2, 0.04, 0.66, 0, GRAPHITE_L);
      fbox(g, f, 0, 0, 2.4, 1.0, 0.06, 0.66, OAK);
      fbox(g, f, 0, 0, 0.9, 0.5, 0.02, 0.72, 0xe3e7ec);
      return;
    case "approvals_chair":
      fcyl(g, f, 0, 0, 0.04, 0.26, 0, STEEL); fcyl(g, f, 0, 0, 0.32, 0.08, 0.26, GRAPHITE_L); return;
    case "cafe_counter":
      fbox(g, f, 0, 0, 0.7, 3.8, 0.9, 0, GRAPHITE_L);
      fbox(g, f, 0, 0, 0.78, 3.88, 0.05, 0.9, OAK);
      return;
    case "cafe_machine":
      fbox(g, f, 0, 0, 0.42, 0.5, 0.55, 0, 0x2c323b);
      fglobe(g, f, 0, 0.26, 0.38, 0.03, 0xe8943a);
      return;
    case "cafe_mug_1": case "cafe_mug_2": case "cafe_mug_3": case "cafe_table_mug":
      fcyl(g, f, 0, 0, 0.05, 0.1, 0, { cafe_mug_1: 0xf4efe6, cafe_mug_2: 0xe0735a, cafe_mug_3: 0x5a95e8, cafe_table_mug: 0xf4efe6 }[id]); return;
    case "cafe_pot": fcyl(g, f, 0, 0, 0.11, 0.24, 0, 0xc9d3dc); return;
    case "cafe_table": fcyl(g, f, 0, 0, 0.07, 0.94, 0, GRAPHITE); fcyl(g, f, 0, 0, 0.42, 0.05, 0.92, OAK); return;
  }
}

// ------------------------------------------------------------------------------------------------ catalog pieces

/** Whether a piece lies flat on the floor (people walk over it): it is drawn under everything. */
export const isFlatPiece = (item: ShopItem) => item.category === "rug";

/** One catalog piece at its place (an approximation per category, from the same specs the 3D scene uses). */
export function drawPiece(g: PIXI.Graphics, item: ShopItem, p: Pick<Piece, "x" | "z" | "rot" | "s"> & { y?: number }) {
  const f: Fr = { x: p.x, z: p.z, rot: p.rot, s: p.s, y: p.y ?? 0 };
  const c1 = hexToNum(item.color, 0xc4aa84), c2 = hexToNum(item.color2, shade(c1, 0.75));
  switch (item.category) {
    // --- the categories that existed before the editor
    case "plant": { const palm = item.id === "plant_palm"; plantAt(g, { ...f, s: f.s * (palm ? 1.3 : 0.9) }, c1); return; }
    case "lamp": fcyl(g, f, 0, 0, 0.12, 0.03, 0, GRAPHITE); fcyl(g, f, 0, 0, 0.02, 1.35, 0.03, STEEL); fcyl(g, f, 0, 0, 0.24, 0.3, 1.2, shade(mixNum(c1, 0xfff1d6, 0.5), 1)); return;
    case "rug": {
      const [x, z] = at(f, 0, 0);
      flatRect(g, x, z, 6.4 * f.s, 2.6 * f.s, f.rot, 0.016, c1, 1, true);
      flatRect(g, x, z, 5.9 * f.s, 2.1 * f.s, f.rot, 0.02, c2, 0.55);
      return;
    }
    case "coffee": fcyl(g, f, 0, 0, 0.07, 0.11, 0, 0xf4efe6); fcyl(g, f, 0, 0, 0.055, 0.005, 0.105, 0x5a4336); return;
    case "poster": fbox(g, f, 0, 0, 1.1, 0.03, 0.8, 1.3, 0xffffff); fface(g, f, -0.45, 0.45, 0.02, 1.38, 2.02, c1); fface(g, f, -0.3, 0.3, 0.021, 1.5, 1.7, c2); return;
    case "shelf": {
      fbox(g, f, 0, 0, 2.0, 0.5, 1.5, 0, hexToNum("#d6c7b0"));
      for (const y of [0.45, 0.9, 1.35]) fface(g, f, -0.95, 0.95, 0.26, y, y + 0.04, 0xb3a184);
      for (let i = 0; i < 9; i++) fface(g, f, -0.9 + i * 0.2, -0.9 + i * 0.2 + 0.1, 0.27, 0.48 + (i % 3) * 0.45, 0.76 + (i % 3) * 0.45, [0x94a3b8, ACCENT, 0xcbd5e1, 0x64748b][i % 4]);
      return;
    }
    case "aquarium": fbox(g, f, 0, 0, item.id === "aquarium_large" ? 2.3 : 1.5, 0.7, 0.6, 0, GRAPHITE_L); fbox(g, f, 0, 0, item.id === "aquarium_large" ? 2.2 : 1.4, 0.6, 0.6, 0.6, 0x9fd3e6, 0.75); return;
    case "arcade": fbox(g, f, 0, 0, 0.95, 0.95, 1.8, 0, shade(c1, 0.8)); fface(g, f, -0.32, 0.32, 0.48, 1.1, 1.6, 0x0f172a); fface(g, f, -0.26, 0.26, 0.49, 1.18, 1.52, mixNum(c2, 0xffffff, 0.3), 0.9); return;
    case "trophy": fbox(g, f, 0, 0, 1.6, 0.55, 0.9, 0, 0xcfc3ad); fface(g, f, -0.7, 0.7, 0.28, 0.5, 0.85, 0xe9eef5, 0.8); for (let i = -1; i <= 1; i++) fcyl(g, f, i * 0.45, 0, 0.07, 0.22, 0.95, 0xe3b73a); return;
    case "window": return; // the view behind the glass is part of the wall
    default: break;
  }
  // --- the editor's furniture
  switch (item.category) {
    case "table": {
      const sp = tableSpec(item);
      const top = hexToNum(tone(item, sp.top), OAK), leg = hexToNum(tone(item, sp.leg), GRAPHITE);
      if (sp.shape === "rect") {
        for (const sx of [-1, 1]) for (const sz of [-1, 1]) fbox(g, f, sx * (sp.w / 2 - 0.05), sz * (sp.d / 2 - 0.05), 0.04, 0.04, sp.h - sp.thick, 0, leg);
        fbox(g, f, 0, 0, sp.w, sp.d, sp.thick, sp.h - sp.thick, top, sp.topFin === "glass" ? 0.7 : 1);
      } else {
        fcyl(g, f, 0, 0, Math.max(0.05, sp.w * 0.12), sp.h - sp.thick, 0, leg);
        fcyl(g, f, 0, 0, sp.w / 2, sp.thick, sp.h - sp.thick, top, sp.topFin === "glass" ? 0.7 : 1);
      }
      return;
    }
    case "sofa": {
      const sp = sofaSpec(item);
      const seat = hexToNum(tone(item, sp.seat), 0x64748b), frame = hexToNum(tone(item, sp.frame), 0x4b5563), cush = hexToNum(tone(item, sp.cush), seat);
      if (sp.kind === "pouf" || sp.kind === "egg") { fcyl(g, f, 0, 0, sp.w / 2, 0.4, 0, seat); return; }
      if (sp.kind === "bench") { fbox(g, f, 0, 0, sp.w, sp.d, 0.08, 0.38, seat); for (const sx of [-1, 1]) fbox(g, f, sx * (sp.w / 2 - 0.05), 0, 0.05, sp.d - 0.06, 0.38, 0, frame); return; }
      fbox(g, f, 0, 0, sp.w, sp.d, 0.4, 0, frame);
      for (let i = 0; i < sp.seats; i++) fbox(g, f, -sp.w / 2 + (sp.w / sp.seats) * (i + 0.5), -0.03, sp.w / sp.seats - 0.06, sp.d - 0.28, 0.12, 0.4, cush);
      fbox(g, f, 0, sp.d / 2 - 0.1, sp.w, 0.2, 0.42, 0.4, seat);
      if (sp.arms !== "none") for (const sx of [-1, 1]) fbox(g, f, sx * (sp.w / 2 - 0.1), 0, 0.2, sp.d, 0.28, 0.4, seat);
      if (sp.kind === "corner") fbox(g, f, -sp.w / 2 + 0.45, -sp.d / 2 - 0.4, 0.9, 0.8, 0.4, 0, frame);
      return;
    }
    case "cabinet": {
      const sp = cabSpec(item);
      const body = hexToNum(tone(item, sp.body), 0xc4aa84), front = hexToNum(tone(item, sp.front), body);
      fbox(g, f, 0, 0, sp.w, sp.d, sp.h, sp.legs ? 0.1 : 0, body);
      const rows = Math.max(1, sp.n);
      for (let i = 0; i < rows; i++) {
        const y0 = (sp.legs ? 0.1 : 0) + 0.04 + (i * (sp.h - 0.08)) / rows;
        fface(g, f, -sp.w / 2 + 0.03, sp.w / 2 - 0.03, sp.d / 2 + 0.005, y0, y0 + (sp.h - 0.08) / rows - 0.02, i % 2 ? shade(front, 0.94) : front);
      }
      if (sp.legs) for (const sx of [-1, 1]) fbox(g, f, sx * (sp.w / 2 - 0.06), 0, 0.04, sp.d - 0.1, 0.1, 0, 0x3a414c);
      return;
    }
    case "divider": {
      const sp = divSpec(item);
      const body = hexToNum(tone(item, sp.body), 0xb9c2cf);
      fbox(g, f, 0, 0, sp.w, Math.max(0.06, sp.d), sp.h, 0.02, body, sp.kind === "glass" ? 0.55 : 1);
      if (sp.kind === "plants" || sp.kind === "living") for (let i = 0; i < 4; i++) fglobe(g, f, -sp.w / 2 + 0.25 + i * ((sp.w - 0.5) / 3), 0, sp.h - 0.1, 0.18, 0x6f9a7d);
      return;
    }
    case "whiteboard": {
      const sp = wbSpec(item);
      const y = f.y || 1.0;
      const frame: Fr = { ...f, y: 0 };
      fbox(g, frame, 0, 0, sp.w + 0.08, 0.05, sp.h + 0.08, y - 0.04, 0xb3bac4);
      fface(g, frame, -sp.w / 2, sp.w / 2, 0.03, y, y + sp.h, sp.kind === "cork" ? 0xc9a57a : sp.kind === "chalk" ? 0x2f3b3a : sp.kind === "glass" ? 0xdbeafe : 0xf8fafc);
      if (sp.kind === "kanban") for (let i = 0; i < 3; i++) fface(g, frame, -sp.w / 2 + 0.1 + i * (sp.w / 3), -sp.w / 2 + 0.1 + i * (sp.w / 3) + 0.4, 0.035, y + 0.15, y + sp.h - 0.15, [0xfde68a, 0xbfdbfe, 0xbbf7d0][i]);
      else for (let i = 0; i < 3; i++) fface(g, frame, -sp.w / 2 + 0.15, -sp.w / 2 + 0.15 + sp.w * (0.45 - i * 0.1), 0.035, y + sp.h - 0.2 - i * 0.18, y + sp.h - 0.16 - i * 0.18, i === 0 ? ACCENT : 0x94a3b8);
      return;
    }
    case "appliance": {
      const sp = applSpec(item);
      const body = hexToNum(tone(item, "c1"), 0xe5e7eb);
      if (sp.kind === "coatrack") { fcyl(g, f, 0, 0, 0.02, 1.7, 0, GRAPHITE); fcyl(g, f, 0, 0, 0.2, 0.03, 0, GRAPHITE); return; }
      if (sp.kind === "cooler") { fcyl(g, f, 0, 0, 0.17, 0.8, 0, body); fcyl(g, f, 0, 0, 0.14, 0.3, 0.85, 0x9fd3e6, 0.8); return; }
      if (sp.kind === "purifier") { fcyl(g, f, 0, 0, sp.w / 2, sp.h, 0, body); return; }
      fbox(g, f, 0, 0, sp.w, sp.d, sp.h, 0, body);
      if (sp.kind === "vending") fface(g, f, -sp.w / 2 + 0.08, sp.w / 2 - 0.35, sp.d / 2 + 0.005, 0.5, sp.h - 0.2, 0x0f172a, 0.85);
      else if (sp.kind === "fridge") fface(g, f, -sp.w / 2, sp.w / 2, sp.d / 2 + 0.005, sp.h * 0.62, sp.h * 0.64, shade(body, 0.7));
      else fface(g, f, -sp.w / 2 + 0.06, sp.w / 2 - 0.06, sp.d / 2 + 0.005, sp.h * 0.55, sp.h * 0.7, 0x64748b);
      return;
    }
    case "light": {
      const sp = lightSpec(item);
      const glow = hexToNum(tone(item, sp.glow?.tone ?? "#ffe2b0"), 0xffe2b0);
      if (sp.mount === "ceiling") {
        const top = f.y > 0 ? f.y : 2.4;
        const [x, z] = at(f, 0, 0);
        const a = iso(x, z, top + 0.6), b = iso(x, z, top);
        g.moveTo(a.x, a.y).lineTo(b.x, b.y).stroke({ width: 1.2, color: INK, alpha: 0.6 });
        const fr: Fr = { ...f, y: top - 0.2 };
        if (sp.kind === "cone") fcyl(g, fr, 0, 0, 0.25, 0.2, 0, glow); else fglobe(g, fr, 0, 0, 0.05, 0.22, glow);
        return;
      }
      if (sp.mount === "wall") { const y = f.y || 1.7; fbox(g, { ...f, y: 0 }, 0, 0, Math.min(sp.w, 1.2), 0.08, 0.12, y, glow); return; }
      fcyl(g, f, 0, 0, 0.1, 0.03, 0, GRAPHITE); fcyl(g, f, 0, 0, 0.015, sp.kind === "table" ? 0.4 : 1.4, 0.03, STEEL);
      fcyl(g, f, 0, 0, 0.2, 0.26, sp.kind === "table" ? 0.4 : 1.4, glow);
      return;
    }
    case "desk": drawDesk(g, p.x, p.z, item, ACCENT); return;
    case "chair": drawChair(g, g, p.x, p.z, item); return;
    default: { const fp = footprint(item); fbox(g, f, 0, 0, fp.w, fp.d, 0.6, 0, c1); }
  }
}

/** The pile of papers on the approvals desk (it grows with the pending approvals). */
export function drawPile(g: PIXI.Graphics, x: number, z: number, rot: number, y: number, n: number, bob: number) {
  const f: Fr = { x, z, rot, s: 1, y: y + 0.72 + bob };
  for (let i = 0; i < Math.min(n, 6); i++) fbox(g, f, (i % 3 - 1) * 0.3, 0, 0.24, 0.32, 0.02, Math.floor(i / 3) * 0.025, 0xf7f4ea);
}
