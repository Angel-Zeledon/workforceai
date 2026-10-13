"use client";
import { useEffect, useMemo, useRef, useState } from "react";
import { useThree } from "@react-three/fiber";
import { Line } from "@react-three/drei";
import * as THREE from "three";
import { useRewards, type ShopItem } from "@/lib/rewards";
import { useStore } from "@/lib/store";
import { effective, pieceOf, useOfficeLayout, withPiece, withoutPiece } from "@/lib/officeLayout";
import { newUid, type Layout } from "@/lib/officeLayoutModel";
import { GRID, corners, deskBox, snap, wallSnap, type Box } from "@/lib/officeGeometry";
import { deskOf } from "./registry";
import { ItemMesh } from "./OfficeItems";
import { floorObstacles, fits, fitsOnWall, pieceBox } from "./editorWorld";
import { pieceInfo, type PieceInfo } from "./pieceInfo";

const OK = "#3fae7d", BAD = "#e0524a", SEL = "#4f7fe0";

interface Ghost { item: ShopItem; x: number; z: number; rot: number; y: number; valid: boolean }
interface Drag { kind: "piece" | "desk"; id: string; snapshot: Layout; off: { x: number; z: number } | null; moved: boolean; valid: boolean; last: { x: number; z: number } }

/** Outline of a footprint on the floor. */
function Frame({ box, color }: { box: Box; color: string }) {
  const pts = useMemo(() => [...corners(box), corners(box)[0]].map(([x, z]) => new THREE.Vector3(x, 0.06, z)), [box]);
  return (
    <>
      <Line points={pts} color={color} lineWidth={2.5} />
      <mesh position={[box.x, 0.045, box.z]} rotation={[-Math.PI / 2, 0, -box.rot]}>
        <planeGeometry args={[box.w, box.d]} />
        <meshBasicMaterial color={color} transparent opacity={0.14} depthWrite={false} toneMapped={false} />
      </mesh>
    </>
  );
}

/** Where a piece with this footprint would go for a pointer on the floor at (px, pz), and whether it fits there. */
function candidate(info: PieceInfo, px: number, pz: number, rot: number, s: number, grid: boolean, layout: Layout, catalog: ShopItem[], skip: { piece?: string; desk?: string }) {
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

const typing = (t: EventTarget | null) => t instanceof HTMLElement && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.tagName === "SELECT" || t.isContentEditable);

/** Everything the editor draws and handles inside the 3D scene: grid, selection frame, placement ghost, drag, keys. */
export function EditorLayer() {
  const editing = useOfficeLayout((s) => s.editing);
  return editing ? <Inner /> : null;
}

function Inner() {
  const { camera, gl } = useThree();
  const draft = useOfficeLayout((s) => s.draft) as Layout;
  const selection = useOfficeLayout((s) => s.selection);
  const placing = useOfficeLayout((s) => s.placing);
  const dragging = useOfficeLayout((s) => s.dragging);
  const grid = useOfficeLayout((s) => s.grid);
  const deskGhost = useOfficeLayout((s) => s.deskGhost);
  const catalog = useRewards((s) => s.catalog);
  const order = useStore((s) => s.agentOrder);
  const agents = useStore((s) => s.agents);
  const [ghost, setGhost] = useState<Ghost | null>(null);
  const [selValid, setSelValid] = useState(true);
  const rot = useRef(0);
  const lastFp = useRef<{ x: number; z: number } | null>(null);
  const drag = useRef<Drag | null>(null);
  const down = useRef<{ x: number; y: number } | null>(null);

  const ray = useMemo(() => new THREE.Raycaster(), []);
  const plane = useMemo(() => new THREE.Plane(new THREE.Vector3(0, 1, 0), 0), []);
  const ndc = useMemo(() => new THREE.Vector2(), []);
  const hit = useMemo(() => new THREE.Vector3(), []);
  const floorPoint = (ev: PointerEvent) => {
    const r = gl.domElement.getBoundingClientRect();
    ndc.set(((ev.clientX - r.left) / r.width) * 2 - 1, -((ev.clientY - r.top) / r.height) * 2 + 1);
    ray.setFromCamera(ndc, camera);
    return ray.ray.intersectPlane(plane, hit) ? { x: hit.x, z: hit.z } : null;
  };
  const item = (id: string) => catalog.find((i) => i.id === id);

  // the drag starts when a piece / desk reports a pointer down (they set `dragging`)
  useEffect(() => {
    if (!dragging || !selection) { drag.current = null; return; }
    const st = useOfficeLayout.getState();
    drag.current = { kind: selection.kind, id: selection.kind === "piece" ? selection.uid : selection.id, snapshot: st.draft as Layout, off: null, moved: false, valid: true, last: { x: 0, z: 0 } };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dragging]);

  useEffect(() => {
    const el = gl.domElement;
    const move = (ev: PointerEvent) => {
      const fp = floorPoint(ev);
      if (!fp) return;
      lastFp.current = fp;
      const st = useOfficeLayout.getState();
      const layout = st.draft as Layout;
      if (st.placing) {
        const it = item(st.placing);
        if (!it) return;
        const info = pieceInfo(it);
        const c = candidate(info, fp.x, fp.z, rot.current, 1, st.grid, layout, catalog, {});
        setGhost({ item: it, x: c.x, z: c.z, rot: c.rot, y: info.y, valid: c.valid });
        return;
      }
      const d = drag.current;
      if (!d) return;
      if (d.kind === "piece") {
        const p = pieceOf(d.snapshot, d.id);
        const it = p && item(p.item);
        if (!p || !it) return;
        if (!d.off) d.off = { x: p.x - fp.x, z: p.z - fp.z };
        const info = pieceInfo(it);
        const c = candidate(info, fp.x + d.off.x, fp.z + d.off.z, p.rot, p.s, st.grid, d.snapshot, catalog, { piece: p.uid });
        d.moved = d.moved || Math.hypot(c.x - p.x, c.z - p.z) > 0.01;
        d.valid = c.valid; d.last = { x: c.x, z: c.z };
        useOfficeLayout.setState({ draft: withPiece(d.snapshot, p.uid, { x: c.x, z: c.z, rot: c.rot, ...(info.mount !== "floor" ? { y: p.y ?? info.y } : {}) }) });
        setSelValid(c.valid);
      } else {
        const cur = effective({ draft: d.snapshot, layout: d.snapshot });
        const a = agents[d.id];
        const home = deskOf({ id: d.id, role: a?.role || "" }, Math.max(0, order.indexOf(d.id)));
        if (!d.off) d.off = { x: home[0] - fp.x, z: home[1] - fp.z };
        const x = st.grid ? snap(fp.x + d.off.x) : fp.x + d.off.x, z = st.grid ? snap(fp.z + d.off.z) : fp.z + d.off.z;
        const deskPos = Object.fromEntries(order.map((id, i) => [id, deskOf({ id, role: agents[id]?.role || "" }, i)]));
        const valid = fits(deskBox(x, z), floorObstacles(cur, catalog, deskPos, { desk: d.id }));
        d.moved = d.moved || Math.hypot(x - home[0], z - home[1]) > 0.01;
        d.valid = valid; d.last = { x, z };
        useOfficeLayout.setState({ deskGhost: { id: d.id, x, z, valid } });
        setSelValid(valid);
      }
    };
    const up = () => {
      const st = useOfficeLayout.getState();
      const d = drag.current;
      if (d) {
        drag.current = null;
        if (d.kind === "piece") {
          if (d.moved && d.valid) useOfficeLayout.setState({ past: [...st.past, d.snapshot].slice(-80), future: [] });
          else useOfficeLayout.setState({ draft: d.snapshot });
        } else {
          useOfficeLayout.setState({ deskGhost: null });
          if (d.moved && d.valid) st.mutate((l) => ({ ...l, desks: { ...l.desks, [d.id]: { ...l.desks[d.id], x: d.last.x, z: d.last.z } } }));
        }
        setSelValid(true);
        st.setDragging(false);
      }
      down.current = null;
    };
    // a click (press and release in place) on the floor drops the piece being placed
    const pdown = (ev: PointerEvent) => { if (ev.button === 0) down.current = { x: ev.clientX, y: ev.clientY }; };
    const pup = (ev: PointerEvent) => {
      const st = useOfficeLayout.getState();
      const d0 = down.current;
      down.current = null;
      if (!st.placing || !d0 || ev.button !== 0 || Math.hypot(ev.clientX - d0.x, ev.clientY - d0.y) > 5) return;
      const fp = floorPoint(ev) || lastFp.current;
      const it = item(st.placing);
      if (!fp || !it) return;
      const info = pieceInfo(it);
      const c = candidate(info, fp.x, fp.z, rot.current, 1, st.grid, st.draft as Layout, catalog, {});
      if (!c.valid) return;
      const uid = newUid();
      st.mutate((l) => ({ ...l, pieces: [...l.pieces, { uid, item: it.id, x: c.x, z: c.z, rot: c.rot, s: 1, ...(info.mount !== "floor" ? { y: info.y } : {}) }] }));
      useOfficeLayout.setState({ placing: null, selection: { kind: "piece", uid } });
      setGhost(null);
    };
    el.addEventListener("pointerdown", pdown);
    el.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
    el.addEventListener("pointerup", pup);
    return () => { el.removeEventListener("pointerdown", pdown); el.removeEventListener("pointermove", move); window.removeEventListener("pointerup", up); el.removeEventListener("pointerup", pup); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [catalog, agents, order, gl, camera]);

  // keyboard: R rotate, Delete remove, Esc cancel / deselect, arrows nudge, Ctrl+Z / Ctrl+Y history
  useEffect(() => {
    const key = (ev: KeyboardEvent) => {
      if (typing(ev.target)) return;
      const st = useOfficeLayout.getState();
      const k = ev.key.toLowerCase();
      const mod = ev.ctrlKey || ev.metaKey;
      if (mod && k === "z") { ev.preventDefault(); if (ev.shiftKey) st.redo(); else st.undo(); return; }
      if (mod && k === "y") { ev.preventDefault(); st.redo(); return; }
      if (k === "escape") { if (st.placing) { st.startPlacing(null); setGhost(null); } else if (st.selection) st.select(null); return; }
      if (k === "r") {
        if (st.placing) {
          rot.current += Math.PI / 2;
          const fp = lastFp.current, it = item(st.placing);
          if (fp && it) { const info = pieceInfo(it); const c = candidate(info, fp.x, fp.z, rot.current, 1, st.grid, st.draft as Layout, catalog, {}); setGhost({ item: it, x: c.x, z: c.z, rot: c.rot, y: info.y, valid: c.valid }); }
        } else if (st.selection?.kind === "piece") rotateSelected(Math.PI / 2);
        return;
      }
      if ((k === "delete" || k === "backspace") && st.selection?.kind === "piece") { const uid = st.selection.uid; st.mutate((l) => withoutPiece(l, uid)); st.select(null); return; }
      const arrows: Record<string, [number, number]> = { arrowleft: [-1, 0], arrowright: [1, 0], arrowup: [0, -1], arrowdown: [0, 1] };
      if (arrows[k] && st.selection?.kind === "piece") { ev.preventDefault(); nudge(arrows[k][0] * GRID, arrows[k][1] * GRID); }
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [catalog]);

  function tryMove(uid: string, patch: { x?: number; z?: number; rot?: number }) {
    const st = useOfficeLayout.getState();
    const l = st.draft as Layout;
    const p = pieceOf(l, uid), it = p && item(p.item);
    if (!p || !it) return;
    const info = pieceInfo(it);
    const x = patch.x ?? p.x, z = patch.z ?? p.z, r = patch.rot ?? p.rot;
    const c = candidate(info, x, z, r, p.s, false, l, catalog, { piece: uid });
    if (!c.valid) return;
    st.mutate((d) => withPiece(d, uid, { x: info.mount === "wall" ? c.x : x, z: info.mount === "wall" ? c.z : z, rot: info.mount === "wall" ? c.rot : r }), `move:${uid}`);
  }
  function rotateSelected(by: number) { const s = useOfficeLayout.getState().selection; if (s?.kind === "piece") { const p = pieceOf(useOfficeLayout.getState().draft as Layout, s.uid); if (p) tryMove(s.uid, { rot: p.rot + by }); } }
  function nudge(dx: number, dz: number) { const s = useOfficeLayout.getState().selection; if (s?.kind === "piece") { const p = pieceOf(useOfficeLayout.getState().draft as Layout, s.uid); if (p) tryMove(s.uid, { x: p.x + dx, z: p.z + dz }); } }

  // selection frame
  const selBox = useMemo<Box | null>(() => {
    if (!selection) return null;
    if (selection.kind === "piece") {
      const p = pieceOf(draft, selection.uid), it = p && item(p.item);
      return p && it ? pieceBox(p, it) : null;
    }
    const a = agents[selection.id];
    const [x, z] = deskGhost?.id === selection.id ? [deskGhost.x, deskGhost.z] : deskOf({ id: selection.id, role: a?.role || "" }, Math.max(0, order.indexOf(selection.id)));
    return deskBox(x, z);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selection, draft, deskGhost, catalog, agents, order]);

  const ghostBox: Box | null = ghost && placing ? { x: ghost.x, z: ghost.z, w: pieceInfo(ghost.item).w, d: pieceInfo(ghost.item).d, rot: ghost.rot } : null;
  useEffect(() => { if (!placing) setGhost(null); else rot.current = 0; }, [placing]);

  return (
    <>
      {grid && <gridHelper args={[28, 56, "#5b7fd0", "#9fb0d8"]} position={[0, 0.04, 0]} material-transparent material-opacity={0.22} material-depthWrite={false} />}
      {selBox && <Frame box={selBox} color={selValid ? SEL : BAD} />}
      {ghost && ghostBox && placing && (
        <>
          <group position={[ghost.x, ghost.y, ghost.z]} rotation={[0, ghost.rot, 0]}><ItemMesh item={ghost.item} /></group>
          <Frame box={ghostBox} color={ghost.valid ? OK : BAD} />
        </>
      )}
    </>
  );
}
