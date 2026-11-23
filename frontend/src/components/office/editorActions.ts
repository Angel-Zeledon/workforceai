// The editing rules of the office (what a drag, a placement and a key press do to the layout), without any renderer:
// EditorLayer (three) and the flat editor (flat/flatEditor.ts) both drive the layout store through these functions, so
// collisions, limits, the grid, history and the keys are the same in every style.
import type { ShopItem } from "@/lib/rewards";
import type { Agent } from "@/lib/types";
import { useStore } from "@/lib/store";
import { effective, pieceOf, useOfficeLayout, withPiece, withoutPiece, type Selection } from "@/lib/officeLayout";
import { FIXTURES, ZONE_DEFAULTS, fixturePose, hideFixture, moveFixture, newUid, zoneCenterOf, type FixtureId, type Layout, type ZoneName } from "@/lib/officeLayoutModel";
import { GRID, deskBox, snap, wallSnap, type Box } from "@/lib/officeGeometry";
import { deskOf } from "./registry";
import { fixtureBox, fixtureFits, floorObstacles, fits, fitsOnWall, pieceBox, turnFixture, zoneBox } from "./editorWorld";
import { pieceInfo, type PieceInfo } from "./pieceInfo";

export interface Ghost { item: ShopItem; x: number; z: number; rot: number; y: number; valid: boolean }
export interface Drag { kind: "piece" | "desk" | "zone" | "fixture"; id: string; snapshot: Layout; off: { x: number; z: number } | null; moved: boolean; valid: boolean; last: { x: number; z: number } }
/** What the actions read besides the layout store: the catalog and the team. */
export interface EditCtx { catalog: ShopItem[]; order: string[]; agents: Record<string, Agent | undefined> }

export const typing = (t: EventTarget | null) => t instanceof HTMLElement && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.tagName === "SELECT" || t.isContentEditable);

/** Where a piece with this footprint would go for a pointer on the floor at (px, pz), and whether it fits there. */
export function candidate(info: PieceInfo, px: number, pz: number, rot: number, s: number, grid: boolean, layout: Layout, catalog: ShopItem[], skip: { piece?: string; desk?: string }) {
  const w = info.w * s, d = info.d * s;
  if (info.mount === "wall") {
    const ws = wallSnap(px, pz, d);
    const box = { x: ws.x, z: ws.z, w, d, rot: ws.rot };
    return { x: ws.x, z: ws.z, rot: ws.rot, valid: fitsOnWall(box, layout, catalog, skip.piece) };
  }
  const x = grid ? snap(px) : px, z = grid ? snap(pz) : pz;
  const box = { x, z, w, d, rot };
  if (info.mount === "ceiling") return { x, z, rot, valid: fits(box, [], true) };
  const st = useStore.getState();
  const deskPos = Object.fromEntries(st.agentOrder.map((id, i) => [id, deskOf({ id, role: st.agents[id]?.role || "" }, i)]));
  return { x, z, rot, valid: fits(box, floorObstacles(layout, catalog, deskPos, skip), info.flat) };
}

const deskPositions = (ctx: EditCtx) => Object.fromEntries(ctx.order.map((id, i) => [id, deskOf({ id, role: ctx.agents[id]?.role || "" }, i)])) as Record<string, [number, number]>;

/** The drag of the current selection (a piece, desk, room or decoration), or null when nothing is selected. */
export function beginDrag(): Drag | null {
  const st = useOfficeLayout.getState();
  const selection = st.selection;
  if (!selection) return null;
  return { kind: selection.kind, id: selection.kind === "piece" ? selection.uid : selection.id, snapshot: st.draft as Layout, off: null, moved: false, valid: true, last: { x: 0, z: 0 } };
}

/** The pointer of a drag is at floor point `fp`: moves the dragged thing (grid, collisions and limits as in the editor). Returns whether it fits there. */
export function dragMove(d: Drag, fp: { x: number; z: number }, ctx: EditCtx): boolean | null {
  const st = useOfficeLayout.getState();
  const item = (id: string) => ctx.catalog.find((i) => i.id === id);
  if (d.kind === "piece") {
    const p = pieceOf(d.snapshot, d.id);
    const it = p && item(p.item);
    if (!p || !it) return null;
    if (!d.off) d.off = { x: p.x - fp.x, z: p.z - fp.z };
    const info = pieceInfo(it);
    const c = candidate(info, fp.x + d.off.x, fp.z + d.off.z, p.rot, p.s, st.grid, d.snapshot, ctx.catalog, { piece: p.uid });
    d.moved = d.moved || Math.hypot(c.x - p.x, c.z - p.z) > 0.01;
    d.valid = c.valid; d.last = { x: c.x, z: c.z };
    useOfficeLayout.setState({ draft: withPiece(d.snapshot, p.uid, { x: c.x, z: c.z, rot: c.rot, ...(info.mount !== "floor" ? { y: p.y ?? info.y } : {}) }) });
    return c.valid;
  }
  if (d.kind === "zone") {
    const name = d.id as ZoneName;
    const home = d.snapshot.zones[name] ?? { x: ZONE_DEFAULTS[name][0], z: ZONE_DEFAULTS[name][1] };
    if (!d.off) d.off = { x: home.x - fp.x, z: home.z - fp.z };
    const x = st.grid ? snap(fp.x + d.off.x) : fp.x + d.off.x, z = st.grid ? snap(fp.z + d.off.z) : fp.z + d.off.z;
    const valid = fits(zoneBox(name, d.snapshot, { x, z }), floorObstacles(d.snapshot, ctx.catalog, deskPositions(ctx), { zone: name }));
    d.moved = d.moved || Math.hypot(x - home.x, z - home.z) > 0.01;
    d.valid = valid; d.last = { x, z };
    useOfficeLayout.setState({ zoneGhost: { id: name, x, z, valid } });
    return valid;
  }
  if (d.kind === "fixture") {
    // decor in floor coordinates; a room part in its room's (it stays in its room and moves with it)
    const id = d.id as FixtureId;
    const cur = fixturePose(d.snapshot, id);
    const room = FIXTURES[id].room;
    const [cx, cz] = room ? zoneCenterOf(d.snapshot, room) : [0, 0];
    const lx = fp.x - cx, lz = fp.z - cz;
    if (!d.off) d.off = { x: cur.x - lx, z: cur.z - lz };
    const x = st.grid ? snap(lx + d.off.x) : lx + d.off.x, z = st.grid ? snap(lz + d.off.z) : lz + d.off.z;
    const valid = fixtureFits(id, d.snapshot, { x, z, rot: cur.rot });
    d.moved = d.moved || Math.hypot(x - cur.x, z - cur.z) > 0.01;
    d.valid = valid; d.last = { x, z };
    useOfficeLayout.setState({ draft: moveFixture(d.snapshot, id, { x, z, rot: cur.rot }) });
    return valid;
  }
  const cur = effective({ draft: d.snapshot, layout: d.snapshot });
  const a = ctx.agents[d.id];
  const home = deskOf({ id: d.id, role: a?.role || "" }, Math.max(0, ctx.order.indexOf(d.id)));
  if (!d.off) d.off = { x: home[0] - fp.x, z: home[1] - fp.z };
  const x = st.grid ? snap(fp.x + d.off.x) : fp.x + d.off.x, z = st.grid ? snap(fp.z + d.off.z) : fp.z + d.off.z;
  const valid = fits(deskBox(x, z), floorObstacles(cur, ctx.catalog, deskPositions(ctx), { desk: d.id }));
  d.moved = d.moved || Math.hypot(x - home[0], z - home[1]) > 0.01;
  d.valid = valid; d.last = { x, z };
  useOfficeLayout.setState({ deskGhost: { id: d.id, x, z, valid } });
  return valid;
}

/** The pointer was released: keeps the move when it fits (one history step) or puts the thing back. */
export function dragEnd(d: Drag) {
  const st = useOfficeLayout.getState();
  if (d.kind === "piece" || d.kind === "fixture") {
    if (d.moved && d.valid) useOfficeLayout.setState({ past: [...st.past, d.snapshot].slice(-80), future: [] });
    else useOfficeLayout.setState({ draft: d.snapshot });
  } else if (d.kind === "zone") {
    useOfficeLayout.setState({ zoneGhost: null });
    if (d.moved && d.valid) st.mutate((l) => ({ ...l, zones: { ...l.zones, [d.id]: { x: d.last.x, z: d.last.z } } }));
  } else {
    useOfficeLayout.setState({ deskGhost: null });
    if (d.moved && d.valid) st.mutate((l) => ({ ...l, desks: { ...l.desks, [d.id]: { ...l.desks[d.id], x: d.last.x, z: d.last.z } } }));
  }
  st.setDragging(false);
}

/** The ghost of the piece being placed with the pointer at `fp` (null when the piece is unknown). */
export function placingGhost(fp: { x: number; z: number }, rot: number, ctx: EditCtx): Ghost | null {
  const st = useOfficeLayout.getState();
  const it = st.placing ? ctx.catalog.find((i) => i.id === st.placing) : undefined;
  if (!it) return null;
  const info = pieceInfo(it);
  const c = candidate(info, fp.x, fp.z, rot, 1, st.grid, st.draft as Layout, ctx.catalog, {});
  return { item: it, x: c.x, z: c.z, rot: c.rot, y: info.y, valid: c.valid };
}

/** When the last piece was dropped: the click that ends the drop must not count as "a click on nothing" that deselects it. */
export const placed = { at: 0 };

/** A click on the floor drops the piece being placed (when it fits) and selects it. Returns whether it was placed. */
export function placeAt(fp: { x: number; z: number }, rot: number, ctx: EditCtx): boolean {
  const st = useOfficeLayout.getState();
  const g = placingGhost(fp, rot, ctx);
  if (!g || !g.valid) return false;
  const info = pieceInfo(g.item);
  const uid = newUid();
  st.mutate((l) => ({ ...l, pieces: [...l.pieces, { uid, item: g.item.id, x: g.x, z: g.z, rot: g.rot, s: 1, ...(info.mount !== "floor" ? { y: info.y } : {}) }] }));
  useOfficeLayout.setState({ placing: null, selection: { kind: "piece", uid } });
  placed.at = performance.now();
  return true;
}

function tryMove(uid: string, patch: { x?: number; z?: number; rot?: number }, catalog: ShopItem[]) {
  const st = useOfficeLayout.getState();
  const l = st.draft as Layout;
  const p = pieceOf(l, uid), it = p && catalog.find((i) => i.id === p.item);
  if (!p || !it) return;
  const info = pieceInfo(it);
  const x = patch.x ?? p.x, z = patch.z ?? p.z, r = patch.rot ?? p.rot;
  const c = candidate(info, x, z, r, p.s, false, l, catalog, { piece: uid });
  if (!c.valid) return;
  st.mutate((d) => withPiece(d, uid, { x: info.mount === "wall" ? c.x : x, z: info.mount === "wall" ? c.z : z, rot: info.mount === "wall" ? c.rot : r }), `move:${uid}`);
}

/** Hooks of the renderer for a key press: the rotation of the piece being placed and refreshing / clearing its ghost. */
export interface KeyHooks { rot: { current: number }; refreshGhost: () => void; clearGhost: () => void }

/** Ctrl+Z / Ctrl+Y history, R rotate, Delete remove, Esc cancel or deselect, arrows nudge. Returns true when the key was used. */
export function editorKey(ev: KeyboardEvent, catalog: ShopItem[], hooks: KeyHooks): boolean {
  if (typing(ev.target)) return false;
  const st = useOfficeLayout.getState();
  const k = ev.key.toLowerCase();
  const mod = ev.ctrlKey || ev.metaKey;
  if (mod && k === "z") { ev.preventDefault(); if (ev.shiftKey) st.redo(); else st.undo(); return true; }
  if (mod && k === "y") { ev.preventDefault(); st.redo(); return true; }
  if (k === "escape") { if (st.placing) { st.startPlacing(null); hooks.clearGhost(); } else if (st.selection) st.select(null); return true; }
  const sel: Selection = st.selection;
  if (k === "r") {
    if (st.placing) { hooks.rot.current += Math.PI / 2; hooks.refreshGhost(); }
    else if (sel?.kind === "piece") { const p = pieceOf(st.draft as Layout, sel.uid); if (p) tryMove(sel.uid, { rot: p.rot + Math.PI / 2 }, catalog); }
    else if (sel?.kind === "fixture") turnFixture(sel.id, Math.PI / 2);
    return true;
  }
  if ((k === "delete" || k === "backspace") && sel?.kind === "piece") { const uid = sel.uid; st.mutate((l) => withoutPiece(l, uid)); st.select(null); return true; }
  if ((k === "delete" || k === "backspace") && sel?.kind === "fixture") { const id = sel.id; st.mutate((l) => hideFixture(l, id)); st.select(null); return true; }
  const arrows: Record<string, [number, number]> = { arrowleft: [-1, 0], arrowright: [1, 0], arrowup: [0, -1], arrowdown: [0, 1] };
  if (arrows[k] && sel?.kind === "piece") {
    ev.preventDefault();
    const p = pieceOf(st.draft as Layout, sel.uid);
    if (p) tryMove(sel.uid, { x: p.x + arrows[k][0] * GRID, z: p.z + arrows[k][1] * GRID }, catalog);
    return true;
  }
  return false;
}

/** The footprint frame of the selected thing (a desk or room being dragged shows at its ghost position). */
export function selectionBox(
  selection: Selection, draft: Layout, ctx: EditCtx,
  deskGhost: { id: string; x: number; z: number } | null, zoneGhost: { id: string; x: number; z: number } | null,
): Box | null {
  if (!selection) return null;
  if (selection.kind === "piece") {
    const p = pieceOf(draft, selection.uid), it = p && ctx.catalog.find((i) => i.id === p.item);
    return p && it ? pieceBox(p, it) : null;
  }
  if (selection.kind === "zone") return zoneBox(selection.id, draft, zoneGhost?.id === selection.id ? zoneGhost : undefined);
  if (selection.kind === "fixture") return fixtureBox(selection.id, draft, fixturePose(draft, selection.id));
  const a = ctx.agents[selection.id];
  const [x, z] = deskGhost?.id === selection.id ? [deskGhost.x, deskGhost.z] : deskOf({ id: selection.id, role: a?.role || "" }, Math.max(0, ctx.order.indexOf(selection.id)));
  return deskBox(x, z);
}
