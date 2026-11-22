// EXPERIMENTAL (spike /office-lab, desechable).
// Panel A, "Actual": la oficina de hoy como referencia. Reutiliza tal cual los muebles de producción (DeskSet, ChairMesh),
// la luz "natural" del modelo de oficina y la misma matemática de poses (computePose / applyGesture de gestures.ts);
// el personaje es una reproducción mínima del de Character.tsx (mismas primitivas y articulaciones), porque el real
// depende de la tienda en vivo de agentes.
"use client";
import { useMemo, useRef } from "react";
import { Canvas, useFrame } from "@react-three/fiber";
import { Html, RoundedBox } from "@react-three/drei";
import * as THREE from "three";
import { ChairMesh, DeskSet } from "../office/Furniture";
import { DESK_H } from "../office/OfficeEnv";
import { GESTURE_DURATION, KEYS, applyGesture, computePose, zeroPose, type GestureKind, type Pose } from "../office/gestures";
import { MOOD_BASE } from "@/lib/officeLayoutModel";
import { labTime } from "./clock";
import { PLAN, sample, type Frame } from "./script";

const BODY_SCALE = 1.2;
const L = MOOD_BASE.natural;

interface Drive { pos: [number, number]; yaw: number; mode: string; stride: number; amp: number; gesture: GestureKind | null; gStart: number; folder: boolean; check: boolean; talk: boolean }
type Driver = (f: Frame, t: number) => Drive;

const agentDrive = (() => {
  let g: GestureKind | null = null, gStart = 0, last = "";
  return (f: Frame, t: number): Drive => {
    let mode = "stand:idle";
    let want: GestureKind | null = null;
    if (f.walking) mode = "walk";
    else if (f.seated > 0.5) mode = f.typing > 0.05 ? "seat:working" : "seat:idle";
    else if (f.phase === "talk") { mode = f.bubble?.who === "agent" ? "stand:talking" : "stand:idle"; if (f.bubble?.who === "partner") want = "nod"; }
    else if (f.phase === "give") want = "point";
    else if (f.phase === "celebrate") mode = "stand:completed";
    const key = `${f.phase}:${want}:${f.bubble?.who}`;
    if (key !== last) { last = key; g = want; gStart = t; }
    return { pos: f.pos, yaw: f.yaw, mode, stride: f.stride * 3.6, amp: f.speed, gesture: g, gStart, folder: f.folder === "agent", check: f.phase === "celebrate" || f.phase === "approve", talk: f.bubble?.who === "agent" };
  };
})();

const partnerDrive = (() => {
  let g: GestureKind | null = null, gStart = 0, last = "";
  return (f: Frame, t: number): Drive => {
    const m = f.partner.mode;
    const want: GestureKind | null = m === "listen" ? "nod" : m === "hand" ? "point" : null;
    if (m !== last) { last = m; g = want; gStart = t; }
    return { pos: PLAN.partner, yaw: f.partner.yaw, mode: m === "talk" ? "stand:talking" : "stand:idle", stride: 0, amp: 0, gesture: g, gStart, folder: f.folder === "table" && m === "hand", check: false, talk: m === "talk" };
  };
})();

const bossDrive = (() => {
  let g: GestureKind | null = null, gStart = 0, last = "";
  return (f: Frame, t: number): Drive => {
    const m = f.boss.mode;
    const want: GestureKind | null = m === "approve" ? "nod" : m === "clap" ? "wave" : m === "look" ? "lookAt" : null;
    if (m !== last) { last = m; g = want; gStart = t; }
    const mode = m === "work" ? "seat:working" : m === "receive" || m === "approve" ? "seat:reviewing" : "seat:idle";
    return { pos: PLAN.boss, yaw: 0, mode, stride: 0, amp: 0, gesture: g, gStart, folder: false, check: false, talk: f.bubble?.who === "boss" };
  };
})();

/** Reproducción mínima del personaje de Character.tsx: cuerpo redondo, cabeza grande, mismas articulaciones. */
function MiniChar({ drive, colors }: { drive: Driver; colors: { blazer: string; pants: string; skin: string; hair: string; shirt: string } }) {
  const root = useRef<THREE.Group>(null!), pelvis = useRef<THREE.Group>(null!), torso = useRef<THREE.Group>(null!), head = useRef<THREE.Group>(null!);
  const lSh = useRef<THREE.Group>(null!), rSh = useRef<THREE.Group>(null!), lEl = useRef<THREE.Group>(null!), rEl = useRef<THREE.Group>(null!);
  const lHip = useRef<THREE.Group>(null!), rHip = useRef<THREE.Group>(null!), lKnee = useRef<THREE.Group>(null!), rKnee = useRef<THREE.Group>(null!);
  const folder = useRef<THREE.Mesh>(null!), check = useRef<THREE.Group>(null!), mouth = useRef<THREE.Mesh>(null!);
  const st = useMemo(() => ({ pose: zeroPose(), target: zeroPose(), mode: "", blend: 1, last: 0 }), []);
  useFrame(() => {
    const t = labTime();
    const f = sample(t);
    const d = drive(f, t);
    const dt = Math.min(0.1, Math.max(0, t - st.last)); st.last = t;
    if (d.mode !== st.mode) { st.mode = d.mode; st.blend = 0; }
    st.blend = Math.min(1, st.blend + dt / 0.45);
    const target: Pose = computePose(st.target, d.mode, t, 0.7, 1, d.stride, d.amp);
    if (d.gesture) applyGesture(target, d.gesture, (t - d.gStart) / GESTURE_DURATION[d.gesture], t, 0);
    const walking = d.mode === "walk";
    const k = Math.min(1, dt * (walking ? 14 : 8) * (0.4 + 0.6 * st.blend));
    for (const key of KEYS) st.pose[key] += (target[key] - st.pose[key]) * k;
    const c = st.pose;
    root.current.position.set(d.pos[0], 0, d.pos[1]);
    root.current.rotation.y = d.yaw;
    pelvis.current.position.y = c.pelvisY;
    torso.current.rotation.set(c.torsoX, c.torsoY, c.torsoZ);
    head.current.rotation.set(c.headX, c.headY, c.headZ);
    lSh.current.rotation.set(c.lShX, 0, c.lShZ); lEl.current.rotation.x = c.lElX;
    rSh.current.rotation.set(c.rShX, 0, -c.rShZ); rEl.current.rotation.x = c.rElX;
    lHip.current.rotation.x = c.lHipX; rHip.current.rotation.x = c.rHipX;
    lKnee.current.rotation.x = c.lKneeX; rKnee.current.rotation.x = c.rKneeX;
    folder.current.visible = d.folder;
    check.current.visible = d.check;
    check.current.position.y = (c.pelvisY + 0.87) * BODY_SCALE + 0.3 + Math.sin(t * 3) * 0.03;
    mouth.current.scale.y = d.talk ? 0.5 + Math.abs(Math.sin(t * 13)) * 1.2 : 0.45;
  });
  const { blazer, pants, skin, hair, shirt } = colors;
  const S = (c: string, r = 0.8) => <meshStandardMaterial color={c} roughness={r} />;
  return (
    <group ref={root}>
      <mesh rotation={[-Math.PI / 2, 0, 0]} position={[0, 0.025, 0]}><circleGeometry args={[0.42, 24]} /><meshBasicMaterial color="#000" transparent opacity={0.16} depthWrite={false} /></mesh>
      <group scale={BODY_SCALE}>
        <group ref={pelvis}>
          <mesh castShadow position={[0, -0.02, 0]} scale={[1.08, 1, 1]}><sphereGeometry args={[0.19, 16, 12]} />{S(pants, 0.85)}</mesh>
          {([[0.1, lHip, lKnee], [-0.1, rHip, rKnee]] as const).map(([x, hipRef, kneeRef], i) => (
            <group key={i} ref={hipRef} position={[x, -0.02, 0]}>
              <mesh><sphereGeometry args={[0.085, 12, 10]} />{S(pants, 0.85)}</mesh>
              <mesh castShadow position={[0, -0.1, 0]}><capsuleGeometry args={[0.085, 0.06, 4, 10]} />{S(pants, 0.85)}</mesh>
              <group ref={kneeRef} position={[0, -0.2, 0]}>
                <mesh><sphereGeometry args={[0.076, 12, 10]} />{S(pants, 0.85)}</mesh>
                <mesh castShadow position={[0, -0.06, 0]}><capsuleGeometry args={[0.075, 0.03, 4, 10]} />{S(pants, 0.85)}</mesh>
                <mesh castShadow position={[0, -0.12, 0.04]} scale={[1, 0.7, 1.5]}><sphereGeometry args={[0.09, 12, 10]} />{S("#6b4a35", 0.6)}</mesh>
              </group>
            </group>
          ))}
          <group ref={torso}>
            <mesh castShadow position={[0, 0.16, 0]} scale={[1.08, 1, 0.9]}><capsuleGeometry args={[0.2, 0.04, 6, 14]} />{S(blazer)}</mesh>
            <mesh position={[0, 0.14, 0.165]} scale={[1, 1.25, 0.3]}><sphereGeometry args={[0.1, 12, 10]} />{S(shirt, 0.9)}</mesh>
            <mesh position={[0, 0.33, 0]}><cylinderGeometry args={[0.05, 0.06, 0.07, 12]} />{S(skin)}</mesh>
            {([[0.24, lSh, lEl], [-0.24, rSh, rEl]] as const).map(([x, shRef, elRef], i) => (
              <group key={i} ref={shRef} position={[x, 0.28, 0]}>
                <mesh><sphereGeometry args={[0.072, 12, 10]} />{S(blazer)}</mesh>
                <mesh castShadow position={[0, -0.1, 0]}><capsuleGeometry args={[0.07, 0.1, 4, 10]} />{S(blazer)}</mesh>
                <group ref={elRef} position={[0, -0.2, 0]}>
                  <mesh><sphereGeometry args={[0.064, 12, 10]} />{S(blazer)}</mesh>
                  <mesh castShadow position={[0, -0.1, 0]}><capsuleGeometry args={[0.062, 0.06, 4, 10]} />{S(blazer)}</mesh>
                  <mesh castShadow position={[0, -0.215, 0]}><sphereGeometry args={[0.075, 12, 10]} />{S(skin)}</mesh>
                  {i === 1 && <mesh ref={folder} position={[0.04, -0.27, 0.1]} rotation={[0.3, 0, 0.1]} visible={false}><boxGeometry args={[0.24, 0.3, 0.02]} /><meshStandardMaterial color="#0A66C2" roughness={0.6} /></mesh>}
                </group>
              </group>
            ))}
            <group ref={head} position={[0, 0.32, 0]} scale={1.75}>
              <mesh castShadow position={[0, 0.15, 0]}><sphereGeometry args={[0.165, 24, 18]} />{S(skin, 0.75)}</mesh>
              <mesh position={[0, 0.2, -0.02]} scale={[1.05, 0.85, 1.05]}><sphereGeometry args={[0.165, 20, 14, 0, Math.PI * 2, 0, Math.PI / 2]} />{S(hair, 0.9)}</mesh>
              <mesh position={[0.058, 0.17, 0.145]} scale={[1, 1.35, 0.5]}><sphereGeometry args={[0.022, 10, 8]} /><meshBasicMaterial color="#4a3626" /></mesh>
              <mesh position={[-0.058, 0.17, 0.145]} scale={[1, 1.35, 0.5]}><sphereGeometry args={[0.022, 10, 8]} /><meshBasicMaterial color="#4a3626" /></mesh>
              <mesh position={[0.1, 0.1, 0.12]} scale={[1.2, 0.8, 0.4]}><sphereGeometry args={[0.025, 8, 6]} /><meshBasicMaterial color="#ff9fa8" transparent opacity={0.65} /></mesh>
              <mesh position={[-0.1, 0.1, 0.12]} scale={[1.2, 0.8, 0.4]}><sphereGeometry args={[0.025, 8, 6]} /><meshBasicMaterial color="#ff9fa8" transparent opacity={0.65} /></mesh>
              <mesh ref={mouth} position={[0, 0.095, 0.155]} scale={[1.6, 0.45, 0.5]}><sphereGeometry args={[0.02, 10, 8]} /><meshBasicMaterial color="#8a3b3b" /></mesh>
            </group>
          </group>
        </group>
      </group>
      {/* el icono de "completado" de producción (círculo verde con la marca) */}
      <group ref={check} visible={false}>
        <mesh rotation={[Math.PI / 2, 0, 0]}><cylinderGeometry args={[0.2, 0.2, 0.05, 24]} /><meshStandardMaterial color="#5fcf86" emissive="#5fcf86" emissiveIntensity={0.35} /></mesh>
      </group>
    </group>
  );
}

/** El monitor de producción (marco grafito, pantalla celeste con barras); al teclear las barras corren y la pantalla se aviva. */
function Monitor({ boss = false }: { boss?: boolean }) {
  const screen = useRef<THREE.MeshBasicMaterial>(null!);
  const bars = useRef<THREE.Group>(null!);
  const off = useMemo(() => new THREE.Color("#5d6878"), []), on = useMemo(() => new THREE.Color("#bfe6ff"), []), hot = useMemo(() => new THREE.Color("#e4f4ff"), []);
  useFrame(() => {
    const t = labTime();
    const f = sample(t);
    const s = boss ? 1 : f.screen;
    screen.current.color.copy(off).lerp(on, s).lerp(hot, boss ? 0 : f.typing * (0.5 + 0.5 * Math.sin(t * 9)));
    bars.current.visible = s > 0.4;
    bars.current.children.forEach((b, i) => { b.scale.x = boss ? 0.7 : 0.35 + 0.65 * (((i * 0.37 + t * (f.typing ? 0.9 : 0)) % 1)); b.position.x = -(1 - b.scale.x) * 0.24; });
  });
  return (
    <group position={[0, DESK_H, -0.12]}>
      <mesh castShadow position={[0, 0.01, 0]}><boxGeometry args={[0.26, 0.02, 0.18]} /><meshStandardMaterial color="#b3bac4" metalness={0.3} roughness={0.5} /></mesh>
      <mesh castShadow position={[0, 0.17, 0]}><boxGeometry args={[0.04, 0.3, 0.03]} /><meshStandardMaterial color="#b3bac4" metalness={0.3} roughness={0.5} /></mesh>
      <RoundedBox args={[0.98, 0.58, 0.04]} radius={0.02} smoothness={3} castShadow position={[0, 0.5, 0]}><meshStandardMaterial color="#2c323b" roughness={0.5} /></RoundedBox>
      <mesh position={[0, 0.5, 0.024]}><planeGeometry args={[0.9, 0.5]} /><meshBasicMaterial ref={screen} color="#bfe6ff" toneMapped={false} /></mesh>
      <group ref={bars} position={[0, 0.5, 0.03]}>
        {[0.15, 0.07, -0.01, -0.09, -0.17].map((y, i) => (
          <mesh key={i} position={[0, y, 0]}><planeGeometry args={[0.48, 0.04]} /><meshBasicMaterial color="#ffffff" transparent opacity={0.85} toneMapped={false} /></mesh>
        ))}
      </group>
      <RoundedBox args={[0.46, 0.02, 0.16]} radius={0.01} smoothness={2} castShadow position={[0, 0.01, 0.37]}><meshStandardMaterial color="#d9dde3" /></RoundedBox>
    </group>
  );
}

/** La carpeta suelta: en la mesa de reunión hasta que el agente la toma, luego sobre el escritorio de la jefa. */
function LooseFolder() {
  const ref = useRef<THREE.Mesh>(null!);
  useFrame(() => {
    const f = sample(labTime());
    ref.current.visible = f.folder === "table" || f.folder === "boss";
    if (f.folder === "table") ref.current.position.set(PLAN.meeting.pos[0] + 0.25, 0.75, PLAN.meeting.pos[1]);
    else ref.current.position.set(PLAN.bossDesk.pos[0] - 0.5, DESK_H + 0.015, PLAN.bossDesk.pos[1] + 0.15);
  });
  return <mesh ref={ref} rotation={[-Math.PI / 2, 0, 0.3]}><boxGeometry args={[0.3, 0.24, 0.02]} /><meshStandardMaterial color="#0A66C2" roughness={0.6} /></mesh>;
}

function Bubbles() {
  const ref = useRef<HTMLDivElement>(null);
  const grp = useRef<THREE.Group>(null!);
  useFrame(() => {
    const f = sample(labTime());
    const b = f.bubble;
    if (!ref.current) return;
    ref.current.style.opacity = b ? "1" : "0";
    if (!b) return;
    if (ref.current.textContent !== b.text) ref.current.textContent = b.text;
    const p = b.who === "agent" ? f.pos : b.who === "partner" ? PLAN.partner : PLAN.boss;
    grp.current.position.set(p[0], b.who === "boss" ? 2.0 : 2.35, p[1]);
  });
  return (
    <group ref={grp}>
      <Html center zIndexRange={[10, 0]} style={{ pointerEvents: "none" }}>
        <div ref={ref} className="whitespace-nowrap rounded-lg border border-slate-200 bg-white px-2.5 py-1 text-[11px] font-semibold text-slate-700 shadow-sm transition-opacity" />
      </Html>
    </group>
  );
}

function Room() {
  const { x0, x1, z0, z1 } = PLAN.room;
  const w = x1 - x0, d = z1 - z0;
  return (
    <group>
      <RoundedBox args={[w, 0.12, d]} radius={0.04} smoothness={2} position={[(x0 + x1) / 2, -0.06, (z0 + z1) / 2]} receiveShadow><meshStandardMaterial color="#eef0f3" roughness={1} /></RoundedBox>
      {/* zonas como en producción: alfombra de la sala de reuniones y de aprobaciones */}
      <mesh rotation={[-Math.PI / 2, 0, 0]} position={[PLAN.meeting.pos[0], 0.012, PLAN.meeting.pos[1]]} receiveShadow><circleGeometry args={[1.55, 48]} /><meshStandardMaterial color="#cfd6df" roughness={1} /></mesh>
      <mesh rotation={[-Math.PI / 2, 0, 0]} position={[PLAN.meeting.pos[0], 0.016, PLAN.meeting.pos[1]]}><ringGeometry args={[1.5, 1.55, 64]} /><meshStandardMaterial color="#8d99ab" roughness={1} /></mesh>
      <RoundedBox args={[2.8, 0.02, 2.6]} radius={0.01} smoothness={2} position={[PLAN.bossDesk.pos[0], 0.011, PLAN.bossDesk.pos[1] + 0.5]} receiveShadow><meshStandardMaterial color="#d5dae1" roughness={1} /></RoundedBox>
      {/* paredes bajas al fondo y a la izquierda */}
      <mesh position={[(x0 + x1) / 2, 1.1, z0 - 0.06]} receiveShadow castShadow><boxGeometry args={[w, 2.2, 0.12]} /><meshStandardMaterial color="#f3f4f7" roughness={0.95} /></mesh>
      <mesh position={[x0 - 0.06, 1.1, (z0 + z1) / 2]} receiveShadow castShadow><boxGeometry args={[0.12, 2.2, d]} /><meshStandardMaterial color="#f3f4f7" roughness={0.95} /></mesh>
      {/* mesa de reunión redonda */}
      <group position={[PLAN.meeting.pos[0], 0, PLAN.meeting.pos[1]]}>
        <mesh castShadow receiveShadow position={[0, 0.72, 0]}><cylinderGeometry args={[PLAN.meeting.r, PLAN.meeting.r, 0.05, 40]} /><meshStandardMaterial color="#d9c4a3" roughness={0.8} /></mesh>
        <mesh castShadow position={[0, 0.36, 0]}><cylinderGeometry args={[0.06, 0.06, 0.7, 12]} /><meshStandardMaterial color="#3a414c" /></mesh>
        <mesh position={[0, 0.02, 0]}><cylinderGeometry args={[0.32, 0.36, 0.04, 24]} /><meshStandardMaterial color="#3a414c" /></mesh>
      </group>
    </group>
  );
}

function Scene() {
  return (
    <>
      <color attach="background" args={[L.bg]} />
      <ambientLight intensity={L.amb} color={L.ambColor} />
      <hemisphereLight args={[L.hemiSky, L.hemiGround, L.hemi]} />
      <directionalLight castShadow position={L.sunPos} intensity={L.sun} color={L.sunColor} shadow-mapSize={[1024, 1024]} shadow-bias={-0.0005} shadow-radius={7}
        shadow-camera-left={-9} shadow-camera-right={9} shadow-camera-top={8} shadow-camera-bottom={-8} shadow-camera-near={1} shadow-camera-far={60} />
      <Room />
      <group position={[PLAN.desk.pos[0], 0, PLAN.desk.pos[1]]}><DeskSet accent="#0A66C2"><Monitor /></DeskSet></group>
      <group position={[PLAN.seat[0], 0, PLAN.seat[1]]}><ChairMesh /></group>
      <group position={[PLAN.bossDesk.pos[0], 0, PLAN.bossDesk.pos[1]]} rotation={[0, Math.PI, 0]}><DeskSet accent="#b98a34"><Monitor boss /></DeskSet></group>
      <group position={[PLAN.boss[0], 0, PLAN.boss[1]]} rotation={[0, Math.PI, 0]}><ChairMesh /></group>
      <LooseFolder />
      <MiniChar drive={agentDrive} colors={{ blazer: "#2f5f9e", pants: "#3a414c", skin: "#f1c7a5", hair: "#4a3626", shirt: "#ffffff" }} />
      <MiniChar drive={partnerDrive} colors={{ blazer: "#6b7c8f", pants: "#59616d", skin: "#d9a27e", hair: "#22252b", shirt: "#e8edf3" }} />
      <MiniChar drive={bossDrive} colors={{ blazer: "#7a4b5c", pants: "#3a414c", skin: "#f3d1b5", hair: "#8a5a3a", shirt: "#fff6f0" }} />
      <Bubbles />
    </>
  );
}

export default function PanelA({ active }: { active: boolean }) {
  return (
    <Canvas shadows="soft" dpr={[1, 1.5]} frameloop={active ? "always" : "never"} camera={{ position: [4.6, 10.5, 12.5], fov: 32, near: 0.5, far: 80 }}
      onCreated={({ camera }) => camera.lookAt(0, 0.4, -0.2)} gl={{ antialias: true }}>
      <Scene />
    </Canvas>
  );
}
