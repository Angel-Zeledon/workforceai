"use client";
import { Suspense, useRef } from "react";
import { Canvas, useFrame } from "@react-three/fiber";
import { OrbitControls } from "@react-three/drei";
import * as THREE from "three";
import type { OrbitControls as OrbitControlsImpl } from "three-stdlib";
import { useStore } from "@/lib/store";
import { OfficeEnv } from "./OfficeEnv";
import { Character } from "./Character";
import { Links } from "./Links";
import { LabelLayer } from "./LabelLayer";
import { LabelSolver } from "./LabelSolver";

/** Cámara casi isométrica, ligeramente cenital, con zoom y rotación acotados. */
function Rig() {
  const ref = useRef<OrbitControlsImpl>(null);
  useFrame(() => {
    const c = ref.current;
    if (!c) return;
    const t = c.target;
    t.x = THREE.MathUtils.clamp(t.x, -7, 7);
    t.z = THREE.MathUtils.clamp(t.z, -5, 6);
    t.y = THREE.MathUtils.clamp(t.y, 0, 1.2);
  });
  return (
    <OrbitControls
      ref={ref as never}
      target={[0.5, 0.4, -0.3]}
      enableDamping dampingFactor={0.08}
      minDistance={11} maxDistance={30}
      minPolarAngle={0.55} maxPolarAngle={1.1}
      minAzimuthAngle={-0.7} maxAzimuthAngle={0.7}
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
  return (
    <div className="relative h-full w-full">
      <Canvas
        shadows="soft"
        dpr={[1, 1.75]}
        camera={{ position: [7, 16, 19], fov: 32, near: 0.5, far: 160 }}
        gl={{ antialias: true, powerPreference: "high-performance" }}
        onPointerMissed={() => select(null)}
      >
        <Suspense fallback={null}>
          <OfficeEnv />
          <Cast />
          <Links />
        </Suspense>
        <LabelSolver />
        <Rig />
      </Canvas>
      <LabelLayer />
    </div>
  );
}
