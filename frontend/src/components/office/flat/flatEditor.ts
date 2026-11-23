// Editing the office inside the "Plano" style: pick a piece, desk, room or decoration with the pointer, drag it on the
// grid, turn it (R), remove it (Delete), place a new piece from the panel, undo / redo. The rules are the 3D editor's
// own (editorActions.ts + editorWorld.ts + useOfficeLayout); this file only adds what a screen needs: the inverse
// projection (isoMath.unIso), what is under the pointer, and the vector drawing of grid, frames and ghost.
import type * as PIXI from "pixi.js";
import { useRewards } from "@/lib/rewards";
import { useStore } from "@/lib/store";
import { useOfficeLayout, type Selection } from "@/lib/officeLayout";
import { FIXTURES, FIXTURE_IDS, ZONE_NAMES, type Layout } from "@/lib/officeLayoutModel";
import { FLOOR, GRID, corners, deskBox, type Box } from "@/lib/officeGeometry";
import { fixtureBox, pieceBox, zoneBox } from "../editorWorld";
import { pieceInfo } from "../pieceInfo";
import { beginDrag, dragEnd, dragMove, editorKey, placeAt, placingGhost, selectionBox, type Drag, type EditCtx, type Ghost } from "../editorActions";
import { deskOf } from "../registry";
import { drawPiece } from "./draw";
import { iso, pointInBox, unIso, S } from "./isoMath";

const SEL = 0x0a66c2, OK = 0x3fae7d, BAD = 0xe0524a;

export interface FlatEditorHost {
  P: typeof PIXI;
  /** the scaled container of the scene (the overlay lives in it, so it follows the fit) */
  world: PIXI.Container;
  canvas: HTMLCanvasElement;
  /** the fit scale and offset of the world container */
  fit(): { k: number; x: number; y: number };
  /** repaint the static scene now (the layout draft changed) */
  requestRebuild(): void;
}

export interface FlatEditor { update(): void; destroy(): void }

interface Hit { sel: NonNullable<Selection>; area: number; rank: number; depth: number }

export function createFlatEditor(host: FlatEditorHost): FlatEditor {
  const { P, world, canvas } = host;
  const gridG = new P.Graphics(), frameG = new P.Graphics(), ghostG = new P.Graphics();
  for (const g of [gridG, frameG, ghostG]) { g.zIndex = 8000; g.eventMode = "none"; world.addChild(g); }
  gridG.zIndex = 7999;
  let gridOn: boolean | null = null;
  let ghost: Ghost | null = null;
  let ghostKey = "";
  let drag: Drag | null = null;
  let selValid = true;
  let last: { x: number; z: number } | null = null;
  let down: { x: number; y: number } | null = null;
  const rot = { current: 0 };

  const ctx = (): EditCtx => { const S0 = useStore.getState(); return { catalog: useRewards.getState().catalog, order: S0.agentOrder, agents: S0.agents }; };
  const editing = () => useOfficeLayout.getState().editing;
  /** the floor point under a pointer event, taking the thing to be at height `h` */
  const floorAt = (ev: { clientX: number; clientY: number }, h = 0) => {
    const r = canvas.getBoundingClientRect(), f = host.fit();
    return unIso((ev.clientX - r.left - f.x) / f.k, (ev.clientY - r.top - f.y) / f.k, h);
  };

  /** What is under the pointer: pieces, desks and decoration first (smallest wins), then rugs, then the room floor. */
  const pick = (ev: { clientX: number; clientY: number }): Selection => {
    const st = useOfficeLayout.getState();
    const draft = st.draft as Layout;
    const c = ctx();
    const hits: Hit[] = [];
    const add = (sel: NonNullable<Selection>, box: Box | null, rank: number, h: number) => {
      if (!box) return;
      const p = floorAt(ev, h);
      if (pointInBox(box, p.x, p.z)) hits.push({ sel, area: box.round ? Math.PI * (box.w / 2) ** 2 : box.w * box.d, rank, depth: p.x + p.z });
    };
    for (const p of draft.pieces) {
      const it = c.catalog.find((i) => i.id === p.item);
      if (!it) continue;
      const info = pieceInfo(it);
      add({ kind: "piece", uid: p.uid }, pieceBox(p, it), info.flat ? 2 : 0, info.flat ? 0 : info.mount === "floor" ? 0.5 : (p.y ?? info.y));
    }
    c.order.forEach((id, i) => { const a = c.agents[id]; const [x, z] = deskOf({ id, role: a?.role || "" }, i); add({ kind: "desk", id }, deskBox(x, z), 0, 0.7); });
    for (const id of FIXTURE_IDS) add({ kind: "fixture", id }, fixtureBox(id, draft), 0, FIXTURES[id].level === "wall" ? 1.7 : FIXTURES[id].h / 2);
    for (const n of ZONE_NAMES) add({ kind: "zone", id: n }, zoneBox(n, draft), 3, 0);
    if (!hits.length) return null;
    // the room parts (table, counter, seats) are inside the room boxes: they are not decor ids, so a hit on a room part is found by its own box
    hits.sort((a, b) => a.rank - b.rank || a.area - b.area || b.depth - a.depth);
    return hits[0].sel;
  };

  const frame = (g: PIXI.Graphics, b: Box, color: number, bad: boolean) => {
    const pts: number[] = [];
    if (b.round) { for (let i = 0; i < 40; i++) { const a = (i / 40) * Math.PI * 2, p = iso(b.x + Math.cos(a) * b.w / 2, b.z + Math.sin(a) * b.w / 2, 0.02); pts.push(p.x, p.y); } }
    else for (const [x, z] of corners(b)) { const p = iso(x, z, 0.02); pts.push(p.x, p.y); }
    g.poly(pts).fill({ color, alpha: bad ? 0.38 : 0.14 }).stroke({ width: bad ? 3 : 2.2, color, alpha: 0.95, join: "round" });
  };

  const setGhost = (g: Ghost | null) => { ghost = g; };
  const refreshGhost = () => { if (last) setGhost(placingGhost(last, rot.current, ctx())); };

  // ------------------------------------------------------------------ pointer
  const onDown = (ev: PointerEvent) => {
    if (!editing() || ev.button !== 0) return;
    down = { x: ev.clientX, y: ev.clientY };
    const st = useOfficeLayout.getState();
    if (st.placing) return;
    const sel = pick(ev);
    st.select(sel);
    if (sel) { st.setDragging(true); drag = beginDrag(); canvas.style.cursor = "grabbing"; }
    else drag = null;
  };
  const onMove = (ev: PointerEvent) => {
    if (!editing()) return;
    const st = useOfficeLayout.getState();
    const fp = floorAt(ev);
    last = fp;
    if (st.placing) { setGhost(placingGhost(fp, rot.current, ctx())); canvas.style.cursor = "crosshair"; return; }
    if (!drag) { canvas.style.cursor = ev.buttons ? "default" : pick(ev) ? "grab" : "default"; return; }
    // the dragged thing is held at the height it was picked at: the offset between pointer and thing is kept by dragMove
    const valid = dragMove(drag, fp, ctx());
    if (valid !== null) selValid = valid;
  };
  const onUp = (ev: PointerEvent) => {
    if (drag) { const d = drag; drag = null; dragEnd(d); selValid = true; canvas.style.cursor = "grab"; }
    const d0 = down;
    down = null;
    const st = useOfficeLayout.getState();
    if (!editing() || !st.placing || !d0 || ev.button !== 0 || Math.hypot(ev.clientX - d0.x, ev.clientY - d0.y) > 5) return;
    const fp = (ev.target === canvas ? floorAt(ev) : null) || last;
    if (fp && placeAt(fp, rot.current, ctx())) setGhost(null);
  };
  const onKey = (ev: KeyboardEvent) => {
    if (!editing()) return;
    editorKey(ev, useRewards.getState().catalog, { rot, refreshGhost, clearGhost: () => setGhost(null) });
  };
  canvas.addEventListener("pointerdown", onDown);
  canvas.addEventListener("pointermove", onMove);
  window.addEventListener("pointerup", onUp);
  window.addEventListener("keydown", onKey);

  // the draft changes with every step of a drag: the static scene follows it at once
  let prevDraft: unknown = useOfficeLayout.getState().draft;
  const unsub = useOfficeLayout.subscribe((s) => {
    if (s.draft !== prevDraft) { prevDraft = s.draft; host.requestRebuild(); }
    if (!s.placing && ghost) setGhost(null);
    if (s.placing && !ghostKey) rot.current = 0;
    ghostKey = s.placing ?? "";
  });

  // ------------------------------------------------------------------ drawing (every frame while editing)
  const drawGrid = () => {
    gridG.clear();
    // one line every two grid cells keeps the floor readable
    const step = GRID * 2;
    for (let x = Math.ceil(FLOOR.x0 / step) * step; x <= FLOOR.x1; x += step) { const a = iso(x, FLOOR.z0), b = iso(x, FLOOR.z1); gridG.moveTo(a.x, a.y).lineTo(b.x, b.y); }
    for (let z = Math.ceil(FLOOR.z0 / step) * step; z <= FLOOR.z1; z += step) { const a = iso(FLOOR.x0, z), b = iso(FLOOR.x1, z); gridG.moveTo(a.x, a.y).lineTo(b.x, b.y); }
    gridG.stroke({ width: 1 / (S / 30), color: 0x5b7fd0, alpha: 0.2 });
  };

  return {
    update() {
      const st = useOfficeLayout.getState();
      const on = st.editing && !!st.draft;
      if (!on) {
        if (gridOn !== null) { gridG.clear(); frameG.clear(); ghostG.clear(); gridOn = null; }
        if (canvas.style.cursor) canvas.style.cursor = "";
        return;
      }
      if (gridOn !== st.grid) { gridOn = st.grid; if (st.grid) drawGrid(); else gridG.clear(); }
      frameG.clear(); ghostG.clear();
      const box = selectionBox(st.selection, st.draft as Layout, ctx(), st.deskGhost, st.zoneGhost);
      if (box) frame(frameG, box, selValid ? SEL : BAD, !selValid);
      if (ghost && st.placing) {
        const info = pieceInfo(ghost.item);
        frame(frameG, { x: ghost.x, z: ghost.z, w: info.w, d: info.d, rot: ghost.rot }, ghost.valid ? OK : BAD, !ghost.valid);
        drawPiece(ghostG, ghost.item, { x: ghost.x, z: ghost.z, rot: ghost.rot, s: 1, y: ghost.y });
        ghostG.alpha = 0.78;
      }
    },
    destroy() {
      canvas.removeEventListener("pointerdown", onDown);
      canvas.removeEventListener("pointermove", onMove);
      window.removeEventListener("pointerup", onUp);
      window.removeEventListener("keydown", onKey);
      unsub();
      for (const g of [gridG, frameG, ghostG]) g.destroy();
    },
  };
}
