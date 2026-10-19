"use client";
import { Suspense, useEffect, useRef } from "react";
import { Canvas, useFrame } from "@react-three/fiber";
import { OrbitControls } from "@react-three/drei";
import * as THREE from "three";
import type { OrbitControls as OrbitControlsImpl } from "three-stdlib";
import { useStore } from "@/lib/store";
import { useCost } from "@/lib/cost";
import { OfficeEnv } from "./OfficeEnv";
import { Character } from "./Character";
import { Links } from "./Links";
import { Folders } from "./Folders";
import { startProjectsRealtime } from "@/lib/projects/store";
import { LabelLayer } from "./LabelLayer";
import { LabelSolver } from "./LabelSolver";
import { CoinFloats } from "./CoinFloats";
import { OfficeItems } from "./OfficeItems";
import { CoinAnnouncer } from "../shop/Coin";
import { EditorLayer } from "./EditorLayer";
import { useOfficeLayout } from "@/lib/officeLayout";
import { useRewards } from "@/lib/rewards";
import { PeopleLayer } from "../presence/PeopleLayer";

const VIEW = { pos: new THREE.Vector3(7, 16, 19), target: new THREE.Vector3(0.5, 0.4, -0.3) };
/** The editor looks down on the floor plan so the grid and the footprints read clearly. */
const EDIT = { pos: new THREE.Vector3(0, 25, 15), target: new THREE.Vector3(0, 0, 0.8) };

/** Cámara casi isométrica, ligeramente cenital, con zoom y rotación acotados (más abierta y cenital en modo edición). */
function Rig() {
  const ref = useRef<OrbitControlsImpl>(null);
  const editing = useOfficeLayout((s) => s.editing);
  const dragging = useOfficeLayout((s) => s.dragging);
  const tween = useRef<{ t: number; fromPos: THREE.Vector3; fromTarget: THREE.Vector3; to: typeof VIEW } | null>(null);
  const first = useRef(true);
  useEffect(() => {
    if (first.current) { first.current = false; return; }
    const c = ref.current;
    if (!c) return;
    tween.current = { t: 0, fromPos: c.object.position.clone(), fromTarget: c.target.clone(), to: editing ? EDIT : VIEW };
  }, [editing]);
  useFrame((_, dt) => {
    const c = ref.current;
    if (!c) return;
    const tw = tween.current;
    if (tw) {
      tw.t = Math.min(1, tw.t + dt / 0.9);
      const e = 1 - Math.pow(1 - tw.t, 3);
      c.object.position.lerpVectors(tw.fromPos, tw.to.pos, e);
      c.target.lerpVectors(tw.fromTarget, tw.to.target, e);
      if (tw.t >= 1) tween.current = null;
    } else {
      const t = c.target;
      const wide = useOfficeLayout.getState().editing;
      t.x = THREE.MathUtils.clamp(t.x, wide ? -10 : -7, wide ? 10 : 7);
      t.z = THREE.MathUtils.clamp(t.z, wide ? -8 : -5, wide ? 8 : 6);
      t.y = THREE.MathUtils.clamp(t.y, 0, 1.2);
    }
  });
  return (
    <OrbitControls
      ref={ref as never}
      target={[0.5, 0.4, -0.3]}
      enabled={!dragging}
      enableDamping dampingFactor={0.08}
      minDistance={editing ? 8 : 11} maxDistance={editing ? 38 : 30}
      minPolarAngle={editing ? 0.08 : 0.55} maxPolarAngle={editing ? 1.2 : 1.1}
      minAzimuthAngle={editing ? -1.4 : -0.7} maxAzimuthAngle={editing ? 1.4 : 0.7}
      panSpeed={0.7} rotateSpeed={0.45} zoomSpeed={0.7}
    />
  );
}

function Cast() {
  const order = useStore((s) => s.agentOrder);
  return <>{order.map((id, i) => <Character key={id} id={id} index={i} />)}</>;
}

export default function OfficeScene() {
  const select = useStore((s) => s.select);
  // project summaries / planning phases feed the meeting room (reference-counted with the Projects screen)
  useEffect(() => startProjectsRealtime(), []);
  // the organization's own layout (walls, floor, light, desks, furniture) and the catalog its pieces come from
  useEffect(() => { void useRewards.getState().load(); void useOfficeLayout.getState().load(); }, []);
  // remaining budget per agent (energy bars): refreshed here because spend changes without a budget.* event
  useEffect(() => {
    const { loadStatus } = useCost.getState();
    void loadStatus();
    const i = setInterval(() => void useCost.getState().loadStatus(), 20000);
    return () => clearInterval(i);
  }, []);
  return (
    <div className="relative h-full w-full">
      <Canvas
        shadows="soft"
        dpr={[1, 1.75]}
        camera={{ position: [7, 16, 19], fov: 32, near: 0.5, far: 160 }}
        gl={{ antialias: true, powerPreference: "high-performance" }}
        onPointerMissed={() => {
          const ed = useOfficeLayout.getState();
          if (ed.editing) { if (!ed.placing && !ed.dragging) ed.select(null); } else select(null);
        }}
      >
        <Suspense fallback={null}>
          <OfficeEnv />
          <Cast />
          <Links />
          <Folders />
          <OfficeItems />
          <EditorLayer />
          <CoinFloats />
          <PeopleLayer /> {/* human presence: the people of the office walk next to the agents */}
        </Suspense>
        <LabelSolver />
        <Rig />
      </Canvas>
      <LabelLayer />
      <CoinAnnouncer />
    </div>
  );
}
