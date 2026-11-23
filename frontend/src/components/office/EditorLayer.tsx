"use client";
import { useEffect, useMemo, useRef, useState } from "react";
import { useThree } from "@react-three/fiber";
import { Line } from "@react-three/drei";
import * as THREE from "three";
import { useRewards } from "@/lib/rewards";
import { useStore } from "@/lib/store";
import { useOfficeLayout } from "@/lib/officeLayout";
import { restOn, type Layout } from "@/lib/officeLayoutModel";
import { corners, type Box } from "@/lib/officeGeometry";
import { ItemMesh } from "./OfficeItems";
import { pieceInfo } from "./pieceInfo";
import { beginDrag, dragEnd, dragMove, editorKey, placeAt, placingGhost, selectionBox, type Drag, type Ghost } from "./editorActions";

const OK = "#3fae7d", BAD = "#e0524a", SEL = "#4f7fe0";

/** Outline of a footprint on the floor (or on the surface a small thing rests on: `y`). */
/** The footprint under a selected, dragged or placed thing: its outline (a circle for round things) and a fill. When it
 *  does not fit, the fill turns into a clear red shadow: letting go there puts it back where it was. */
function Frame({ box, color, y = 0 }: { box: Box; color: string; y?: number }) {
  const pts = useMemo(() => {
    if (box.round) {
      const r = box.w / 2;
      return Array.from({ length: 49 }, (_, i) => { const a = (i / 48) * Math.PI * 2; return new THREE.Vector3(box.x + Math.cos(a) * r, 0.06 + y, box.z + Math.sin(a) * r); });
    }
    return [...corners(box), corners(box)[0]].map(([x, z]) => new THREE.Vector3(x, 0.06 + y, z));
  }, [box, y]);
  const bad = color === BAD;
  return (
    <>
      <Line points={pts} color={color} lineWidth={bad ? 3.5 : 2.5} />
      <mesh position={[box.x, 0.045 + y, box.z]} rotation={[-Math.PI / 2, 0, -box.rot]} renderOrder={bad ? 10 : 0}>
        {box.round ? <circleGeometry args={[box.w / 2, 48]} /> : <planeGeometry args={[box.w, box.d]} />}
        <meshBasicMaterial color={color} transparent opacity={bad ? 0.42 : 0.14} depthWrite={false} depthTest={!bad} toneMapped={false} />
      </mesh>
    </>
  );
}

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
  const zoneGhost = useOfficeLayout((s) => s.zoneGhost);
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

  const ctx = () => ({ catalog, order, agents });
  const refreshGhost = () => { const fp = lastFp.current; if (fp) setGhost(placingGhost(fp, rot.current, ctx())); };

  // the drag starts when a piece / desk reports a pointer down (they set `dragging`)
  useEffect(() => {
    if (!dragging || !selection) { drag.current = null; return; }
    drag.current = beginDrag();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dragging]);

  useEffect(() => {
    const el = gl.domElement;
    const move = (ev: PointerEvent) => {
      const fp = floorPoint(ev);
      if (!fp) return;
      lastFp.current = fp;
      if (useOfficeLayout.getState().placing) { const g = placingGhost(fp, rot.current, ctx()); if (g) setGhost(g); return; }
      const d = drag.current;
      if (!d) return;
      const valid = dragMove(d, fp, ctx());
      if (valid !== null) setSelValid(valid);
    };
    const up = () => {
      const d = drag.current;
      if (d) { drag.current = null; dragEnd(d); setSelValid(true); }
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
      if (fp && placeAt(fp, rot.current, ctx())) setGhost(null);
    };
    el.addEventListener("pointerdown", pdown);
    el.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
    el.addEventListener("pointerup", pup);
    return () => { el.removeEventListener("pointerdown", pdown); el.removeEventListener("pointermove", move); window.removeEventListener("pointerup", up); el.removeEventListener("pointerup", pup); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [catalog, agents, order, gl, camera]);

  // keyboard: R rotate, Delete remove, Esc cancel / deselect, arrows nudge, Ctrl+Z / Ctrl+Y history (editorActions.editorKey)
  useEffect(() => {
    const key = (ev: KeyboardEvent) => { editorKey(ev, catalog, { rot, refreshGhost, clearGhost: () => setGhost(null) }); };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [catalog, agents, order]);

  // selection frame
  const selBox = useMemo<Box | null>(
    () => selectionBox(selection, draft, { catalog, order, agents }, deskGhost, zoneGhost),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [selection, draft, deskGhost, zoneGhost, catalog, agents, order],
  );

  const ghostBox: Box | null = ghost && placing ? { x: ghost.x, z: ghost.z, w: pieceInfo(ghost.item).w, d: pieceInfo(ghost.item).d, rot: ghost.rot } : null;
  useEffect(() => { if (!placing) setGhost(null); else rot.current = 0; }, [placing]);

  return (
    <>
      {grid && <gridHelper args={[28, 56, "#5b7fd0", "#9fb0d8"]} position={[0, 0.04, 0]} material-transparent material-opacity={0.22} material-depthWrite={false} />}
      {selBox && <Frame box={selBox} color={selValid ? SEL : BAD} y={selection?.kind === "fixture" ? restOn(draft, selection.id).y : 0} />}
      {ghost && ghostBox && placing && (
        <>
          <group position={[ghost.x, ghost.y, ghost.z]} rotation={[0, ghost.rot, 0]}><ItemMesh item={ghost.item} /></group>
          <Frame box={ghostBox} color={ghost.valid ? OK : BAD} />
        </>
      )}
    </>
  );
}
