import type { ShopItem } from "@/lib/rewards";
import { FIXTURES, FIXTURE_IDS, ZONE_DEFAULTS, ZONE_NAMES, ZONE_SIZE, type FixtureId, type Layout, type Piece, type ZoneName } from "@/lib/officeLayoutModel";
import { WALKWAY, bounds, deskBox, insideFloor, overlap, type Box } from "@/lib/officeGeometry";
import { pieceInfo } from "./pieceInfo";

/** Floor box of a room (where it stands now: moved by the organization or in its built-in place). */
export function zoneBox(name: ZoneName, layout: Layout, at?: { x: number; z: number }): Box {
  const c = at ?? layout.zones[name] ?? { x: ZONE_DEFAULTS[name][0], z: ZONE_DEFAULTS[name][1] };
  const s = ZONE_SIZE[name];
  return { x: c.x + s.dx, z: c.z + s.dz, w: s.w, d: s.d, rot: 0 };
}
/** Floor box of a built-in decoration, or null when the organization took it away. */
export function fixtureBox(id: FixtureId, layout: Layout, at?: { x: number; z: number; rot?: number }): Box | null {
  const c = layout.fixtures[id];
  if (!at && c?.hidden) return null;
  const f = FIXTURES[id];
  return { x: at?.x ?? c?.x ?? f.x, z: at?.z ?? c?.z ?? f.z, w: f.w, d: f.d, rot: at?.rot ?? c?.rot ?? f.rot };
}

/** Footprint of a placed piece (scale and rotation applied). */
export function pieceBox(p: Pick<Piece, "x" | "z" | "rot" | "s">, item: ShopItem): Box {
  const info = pieceInfo(item);
  return { x: p.x, z: p.z, w: info.w * p.s, d: info.d * p.s, rot: p.rot };
}

/** Everything a floor piece, desk, room or decoration must not overlap, except the thing being moved. Rooms may cross the walkway. */
export function floorObstacles(
  layout: Layout, catalog: ShopItem[], deskPos: Record<string, [number, number]>, skip: { piece?: string; desk?: string; zone?: ZoneName; fixture?: FixtureId },
): Box[] {
  const out: Box[] = skip.zone ? [] : [WALKWAY];
  for (const n of ZONE_NAMES) if (n !== skip.zone) out.push(zoneBox(n, layout));
  for (const id of FIXTURE_IDS) { if (id === skip.fixture) continue; const b = fixtureBox(id, layout); if (b) out.push(b); }
  for (const p of layout.pieces) {
    if (p.uid === skip.piece) continue;
    const item = catalog.find((i) => i.id === p.item);
    if (!item) continue;
    const info = pieceInfo(item);
    if (info.flat || info.mount !== "floor") continue;
    out.push(pieceBox(p, item));
  }
  for (const [id, [x, z]] of Object.entries(deskPos)) if (id !== skip.desk) out.push(deskBox(x, z));
  return out;
}

/** Whether a floor box fits: inside the floor and clear of every obstacle. Flat pieces (rugs) only need to stay inside. */
export function fits(box: Box, obstacles: Box[], flat = false): boolean {
  if (!insideFloor(box)) return false;
  if (flat) return true;
  return !obstacles.some((o) => overlap(box, o));
}

/** Wall pieces only collide with other pieces on the same wall (same facing, overlapping along the wall). */
export function fitsOnWall(box: Box, layout: Layout, catalog: ShopItem[], skipUid?: string): boolean {
  const b = bounds(box);
  if (b.x0 < -14.1 || b.x1 > 14.1 || b.z0 < -10 || b.z1 > 10) return false;
  for (const p of layout.pieces) {
    if (p.uid === skipUid) continue;
    const item = catalog.find((i) => i.id === p.item);
    if (!item || pieceInfo(item).mount !== "wall") continue;
    if (overlap(box, pieceBox(p, item), 0.01)) return false;
  }
  return true;
}
