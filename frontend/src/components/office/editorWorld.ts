import type { ShopItem } from "@/lib/rewards";
import type { Layout, Piece } from "@/lib/officeLayoutModel";
import { MEETING_CENTER, USER_POS } from "@/lib/meta";
import { WALKWAY, bounds, deskBox, insideFloor, overlap, type Box } from "@/lib/officeGeometry";
import { CAFE_CENTER } from "./officeState";
import { pieceInfo } from "./pieceInfo";

/** The three fixed rooms (meeting, approvals, cafeteria): nothing is built over them. */
export const ZONES: Box[] = [
  { x: MEETING_CENTER[0], z: MEETING_CENTER[1], w: 7.8, d: 7.4, rot: 0 },
  { x: USER_POS[0], z: USER_POS[2] + 0.5, w: 5.6, d: 4.8, rot: 0 },
  { x: CAFE_CENTER[0] + 0.1, z: CAFE_CENTER[1], w: 4.1, d: 4.6, rot: 0 },
];

/** Footprint of a placed piece (scale and rotation applied). */
export function pieceBox(p: Pick<Piece, "x" | "z" | "rot" | "s">, item: ShopItem): Box {
  const info = pieceInfo(item);
  return { x: p.x, z: p.z, w: info.w * p.s, d: info.d * p.s, rot: p.rot };
}

/** Everything a floor piece or a desk must not overlap, except the thing being moved. */
export function floorObstacles(
  layout: Layout, catalog: ShopItem[], deskPos: Record<string, [number, number]>, skip: { piece?: string; desk?: string },
): Box[] {
  const out: Box[] = [...ZONES, WALKWAY];
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
