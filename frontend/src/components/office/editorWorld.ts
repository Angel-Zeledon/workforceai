import type { ShopItem } from "@/lib/rewards";
import { DECOR_IDS, FIXTURES, ZONE_DEFAULTS, ZONE_NAMES, ZONE_SIZE, fixturePose, moveFixture, partFootprint, partNeighbors, roomRect, zoneCenterOf, type FixtureId, type Layout, type Piece, type Pose, type ZoneName } from "@/lib/officeLayoutModel";
import { WALKWAY, bounds, deskBox, insideFloor, overlap, type Box } from "@/lib/officeGeometry";
import { pieceInfo } from "./pieceInfo";
import { useStore } from "@/lib/store";
import { useRewards } from "@/lib/rewards";
import { useOfficeLayout } from "@/lib/officeLayout";
import { deskOf } from "./registry";

/** Floor box of a room (where it stands now: moved by the organization or in its built-in place). */
export function zoneBox(name: ZoneName, layout: Layout, at?: { x: number; z: number }): Box {
  const c = at ?? layout.zones[name] ?? { x: ZONE_DEFAULTS[name][0], z: ZONE_DEFAULTS[name][1] };
  const s = ZONE_SIZE[name];
  return { x: c.x + s.dx, z: c.z + s.dz, w: s.w, d: s.d, rot: 0 };
}
/**
 * World floor box of a built-in object (decor or room part), or null when the organization took it away. `at` is a
 * pose in the object's own frame (room-relative for room parts) and wins over the stored one.
 */
export function fixtureBox(id: FixtureId, layout: Layout, at?: Partial<Pose>): Box | null {
  const p = fixturePose(layout, id);
  if (!at && p.hidden) return null;
  const room = FIXTURES[id].room;
  const [cx, cz] = room ? zoneCenterOf(layout, room) : [0, 0];
  return partFootprint(id, { x: (at?.x ?? p.x) + cx, z: (at?.z ?? p.z) + cz, rot: at?.rot ?? p.rot });
}

/** How far a room part may stick out of its room's footprint (the meeting screen hangs on the wall just behind it). */
const ROOM_MARGIN = 0.3;
/**
 * Whether a room part fits at a room-relative pose: on the floor, inside its room (it moves with the room, so it
 * stays in it) and clear of the other parts of the room on its level (officeLayoutModel.partNeighbors).
 */
export function partFits(id: FixtureId, layout: Layout, at: Pose): boolean {
  const room = FIXTURES[id].room;
  if (!room) return false;
  const box = partFootprint(id, at);
  const b = bounds(box), r = bounds(roomRect(room));
  if (b.x0 < r.x0 - ROOM_MARGIN || b.x1 > r.x1 + ROOM_MARGIN || b.z0 < r.z0 - ROOM_MARGIN || b.z1 > r.z1 + ROOM_MARGIN) return false;
  const [cx, cz] = zoneCenterOf(layout, room);
  if (FIXTURES[id].level !== "wall" && !insideFloor({ ...box, x: box.x + cx, z: box.z + cz })) return false;
  return !partNeighbors(layout, id, at).some((o) => overlap(box, o));
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
  // the parts of the rooms are inside the room boxes above: only the decor standing on its own is an obstacle
  for (const id of DECOR_IDS) { if (id === skip.fixture) continue; const b = fixtureBox(id, layout); if (b) out.push(b); }
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

/** Whether a built-in object fits at a pose of its own frame: decor against everything on the floor, a room part inside its room. */
export function fixtureFits(id: FixtureId, l: Layout, at: Pose): boolean {
  if (FIXTURES[id].room) return partFits(id, l, at);
  const st = useStore.getState();
  const deskPos = Object.fromEntries(st.agentOrder.map((i, k) => [i, deskOf({ id: i, role: st.agents[i]?.role || "" }, k)]));
  const box = fixtureBox(id, l, at);
  return !!box && fits(box, floorObstacles(l, useRewards.getState().catalog, deskPos, { fixture: id }));
}
/** Turns a built-in object a quarter (R key and the panel's button); nothing happens when it would not fit. */
export function turnFixture(id: FixtureId, by = Math.PI / 2) {
  const st = useOfficeLayout.getState();
  const l = st.draft;
  if (!l) return;
  const cur = fixturePose(l, id);
  const to = { x: cur.x, z: cur.z, rot: cur.rot + by };
  if (fixtureFits(id, l, to)) st.mutate((d) => moveFixture(d, id, to), `rot:${id}`);
}
