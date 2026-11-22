// EXPERIMENTAL (spike /office-lab, desechable).
// Panel D, "Maqueta": miniatura de plastilina en Three.js. Una maqueta cálida sobre una mesa de madera clara, con luz de
// tarde suave y desenfoque de miniatura (tilt-shift). El movimiento es fluido y con intención: las poses clave del guion
// (puppet.ts) se persiguen con MUELLES por articulación (anticipación, ligero sobreimpulso y seguimiento: la cabeza y los
// brazos llegan un poco después que el cuerpo), el cuerpo se inclina con la aceleración y los personajes parpadean y
// sonríen. Nada tiembla: ni las figuras, ni la luz. Figuras de plastilina, muebles de cartón y balsa.
"use client";
import { useMemo, useRef } from "react";
import { Canvas, useFrame } from "@react-three/fiber";
import { EffectComposer, TiltShift2, ToneMapping, Vignette } from "@react-three/postprocessing";
import { ToneMappingMode } from "postprocessing";
import * as THREE from "three";
import { labTime } from "./clock";
import { BubbleProjector, FpsDriver } from "./Bubble3D";
import { PLAN, sample, type Frame } from "./script";
import { JOINTS, poseFor, zero, type PPose, type Role } from "./puppet";

/** Variación fija por pieza (no cambia con el tiempo). */
const rnd = (n: number) => { const x = Math.sin(n * 127.1 + 311.7) * 43758.5453; return x - Math.floor(x); };

/** Plastilina: desplaza los vértices con un ruido suave para que no haya superficies perfectas. */
function lumpy<G extends THREE.BufferGeometry>(g: G, amt = 0.008, seed = 1): G {
  const p = g.attributes.position as THREE.BufferAttribute;
  const v = new THREE.Vector3();
  for (let i = 0; i < p.count; i++) {
    v.fromBufferAttribute(p, i);
    const n = Math.sin(v.x * 23 + seed) * Math.sin(v.y * 19 + seed * 2) * Math.sin(v.z * 21 + seed * 3) + 0.5 * Math.sin(v.x * 51 + v.y * 47 + seed);
    const len = v.length() || 1;
    v.addScaledVector(v.clone().divideScalar(len), n * amt);
    p.setXYZ(i, v.x, v.y, v.z);
  }
  g.computeVertexNormals();
  return g;
}

const clay = (color: string) => <meshStandardMaterial color={color} roughness={0.7} metalness={0} />;

// ------------------------------------------------------------------------------------------------ figuras

interface Look { skin: string; top: string; bottom: string; hair: string; shoes: string; long?: boolean }
const LOOKS: Record<Role, Look> = {
  agent: { skin: "#c48a62", top: "#3f5f8f", bottom: "#4a4f58", hair: "#3a2618", shoes: "#4a3426" },
  partner: { skin: "#8a5a3e", top: "#c9894f", bottom: "#4a4f58", hair: "#2a1a12", shoes: "#3b2c22", long: true },
  boss: { skin: "#f1cfae", top: "#9a5b6e", bottom: "#4a4f58", hair: "#9a6a44", shoes: "#5a3e2c", long: true },
};

function Figure({ role }: { role: Role }) {
  const L = LOOKS[role];
  const g = useMemo(() => {
    const s = role === "agent" ? 1 : role === "partner" ? 2 : 3;
    return {
      thigh: lumpy(new THREE.CapsuleGeometry(0.085, 0.3, 4, 10), 0.006, s),
      shin: lumpy(new THREE.CapsuleGeometry(0.075, 0.3, 4, 10), 0.006, s + 1),
      foot: lumpy(new THREE.SphereGeometry(0.08, 12, 8), 0.006, s + 2),
      torso: lumpy(new THREE.CapsuleGeometry(0.2, 0.26, 6, 14), 0.01, s + 3),
      arm: lumpy(new THREE.CapsuleGeometry(0.065, 0.18, 4, 10), 0.005, s + 4),
      hand: lumpy(new THREE.SphereGeometry(0.06, 10, 8), 0.006, s + 5),
      head: lumpy(new THREE.SphereGeometry(0.16, 20, 16), 0.008, s + 6),
      hair: lumpy(new THREE.SphereGeometry(0.168, 20, 12, 0, Math.PI * 2, 0, Math.PI * 0.55), 0.012, s + 7),
    };
  }, [role]);
  const r = {
    root: useRef<THREE.Group>(null!), body: useRef<THREE.Group>(null!), pelvis: useRef<THREE.Group>(null!), torso: useRef<THREE.Group>(null!), head: useRef<THREE.Group>(null!),
    lSh: useRef<THREE.Group>(null!), rSh: useRef<THREE.Group>(null!), lEl: useRef<THREE.Group>(null!), rEl: useRef<THREE.Group>(null!),
    lHip: useRef<THREE.Group>(null!), rHip: useRef<THREE.Group>(null!), lKnee: useRef<THREE.Group>(null!), rKnee: useRef<THREE.Group>(null!),
    folder: useRef<THREE.Mesh>(null!), eyes: useRef<THREE.Group>(null!),
  };
  const sp = useMemo(() => ({ x: zero(), v: zero(), last: -1, vel: 0, lean: 0, blinkAt: 2 + rnd(role.length) * 3 }), [role]);
  useFrame(() => {
    const t = labTime();
    const f: Frame = sample(t);
    const target: PPose = poseFor(role, f, t);
    const first = sp.last < 0;
    let dt = first ? 0 : t - sp.last;
    const jump = first || dt < 0 || dt > 0.5;
    sp.last = t;
    if (jump) dt = 0;
    // muelles por articulación: el cuerpo llega rápido, cabeza y antebrazos un poco después (seguimiento)
    for (const k of JOINTS) {
      if (jump) { sp.x[k] = target[k]; sp.v[k] = 0; continue; }
      const soft = k.startsWith("head") || k.endsWith("ElX") ? 0.6 : k.startsWith("l") || k.startsWith("r") ? 0.8 : 1;
      const stiff = 140 * soft, damp = 2 * Math.sqrt(stiff) * 0.62;
      const steps = Math.max(1, Math.ceil(dt * 120));
      const h = dt / steps;
      for (let i = 0; i < steps; i++) { const a = stiff * (target[k] - sp.x[k]) - damp * sp.v[k]; sp.v[k] += a * h; sp.x[k] += sp.v[k] * h; }
    }
    const p = sp.x;
    const pos = role === "agent" ? f.pos : role === "partner" ? PLAN.partner : PLAN.boss;
    const yaw = role === "agent" ? f.yaw : role === "partner" ? f.partner.yaw : 0;
    // se inclina con la aceleración: hacia delante al arrancar, hacia atrás al frenar
    const vel = role === "agent" ? f.speed : 0;
    if (dt > 0) { const acc = (vel - sp.vel) / dt; sp.lean += (Math.max(-3, Math.min(3, acc)) * 0.035 - sp.lean) * Math.min(1, dt * 6); }
    sp.vel = vel;
    r.root.current.position.set(pos[0], 0, pos[1]);
    r.root.current.rotation.y = yaw;
    r.body.current.position.y = p.y;
    r.pelvis.current.position.y = p.pelvis;
    r.torso.current.rotation.set(p.torsoX + sp.lean, p.torsoY, p.torsoZ);
    r.head.current.rotation.set(p.headX - sp.lean * 0.5, p.headY, p.headZ);
    // parpadeo
    if (t > sp.blinkAt + 0.14 || t < sp.blinkAt - 8) sp.blinkAt = t + 2.5 + rnd(Math.floor(t) + role.length) * 3;
    r.eyes.current.scale.y = t > sp.blinkAt && t < sp.blinkAt + 0.14 ? 0.15 : 1;
    r.lSh.current.rotation.set(p.lShX, 0, p.lShZ); r.rSh.current.rotation.set(p.rShX, 0, -p.rShZ);
    r.lEl.current.rotation.x = p.lElX; r.rEl.current.rotation.x = p.rElX;
    r.lHip.current.rotation.x = p.lHipX; r.rHip.current.rotation.x = p.rHipX;
    r.lKnee.current.rotation.x = p.lKneeX; r.rKnee.current.rotation.x = p.rKneeX;
    r.folder.current.visible = role === "agent" ? f.folder === "agent" : role === "partner" ? f.partner.mode === "hand" : false;
  });
  const M = (c: string, geo: THREE.BufferGeometry, pos: [number, number, number] = [0, 0, 0], sc?: [number, number, number]) => (
    <mesh geometry={geo} position={pos} scale={sc} castShadow receiveShadow>{clay(c)}</mesh>
  );
  return (
    <group ref={r.root}>
      <group ref={r.body}>
        <group ref={r.pelvis}>
          {M(L.bottom, g.torso, [0, 0.02, 0], [0.85, 0.42, 0.8])}
          {([[0.1, r.lHip, r.lKnee], [-0.1, r.rHip, r.rKnee]] as const).map(([x, hip, knee], i) => (
            <group key={i} ref={hip} position={[x, -0.02, 0]}>
              {M(L.bottom, g.thigh, [0, -0.2, 0])}
              <group ref={knee} position={[0, -0.43, 0]}>
                {M(L.bottom, g.shin, [0, -0.2, 0])}
                {M(L.shoes, g.foot, [0, -0.43, 0.05], [1, 0.6, 1.5])}
              </group>
            </group>
          ))}
          <group ref={r.torso}>
            {M(L.top, g.torso, [0, 0.3, 0], [1, 1, 0.78])}
            {role === "agent" && M("#f2efe9", g.hand, [0, 0.5, 0.13], [0.9, 1.2, 0.35])}
            {([[0.25, r.lSh, r.lEl], [-0.25, r.rSh, r.rEl]] as const).map(([x, sh, el], i) => (
              <group key={i} ref={sh} position={[x, 0.5, 0]}>
                {M(L.top, g.arm, [0, -0.14, 0])}
                <group ref={el} position={[0, -0.28, 0]}>
                  {M(L.top, g.arm, [0, -0.12, 0], [0.95, 0.95, 0.95])}
                  {M(L.skin, g.hand, [0, -0.27, 0])}
                  {i === 1 && <mesh ref={r.folder} position={[0, -0.3, 0.12]} rotation={[0.25, 0, 0]} castShadow visible={false}><boxGeometry args={[0.24, 0.3, 0.035]} />{clay("#2f6db3")}</mesh>}
                </group>
              </group>
            ))}
            <group ref={r.head} position={[0, 0.62, 0]}>
              {M(L.skin, g.head, [0, 0.14, 0])}
              {M(L.hair, g.hair, [0, 0.17, -0.015], [1.02, 1, 1.04])}
              {L.long && M(L.hair, g.head, [0, 0.07, -0.08], [0.95, 0.9, 0.55])}
              <group ref={r.eyes} position={[0, 0.165, 0]}>
                {[0.055, -0.055].map((x) => (
                  <group key={x} position={[x, 0, 0.142]}>
                    <mesh scale={[1, 1.15, 0.6]}><sphereGeometry args={[0.026, 12, 10]} /><meshStandardMaterial color="#fbf8f2" roughness={0.4} /></mesh>
                    <mesh position={[0, -0.002, 0.014]}><sphereGeometry args={[0.014, 10, 8]} /><meshStandardMaterial color="#3a2a20" roughness={0.25} /></mesh>
                    <mesh position={[0.005, 0.006, 0.026]}><sphereGeometry args={[0.004, 6, 4]} /><meshBasicMaterial color="#ffffff" /></mesh>
                  </group>
                ))}
              </group>
              <mesh position={[0, 0.085, 0.14]} rotation={[0.25, 0, Math.PI]}><torusGeometry args={[0.035, 0.008, 6, 14, Math.PI]} /><meshStandardMaterial color="#9a4a40" roughness={0.6} /></mesh>
              <mesh position={[0.085, 0.11, 0.125]} scale={[1, 0.7, 0.4]}><sphereGeometry args={[0.025, 8, 6]} /><meshBasicMaterial color="#e98f7f" transparent opacity={0.35} /></mesh>
              <mesh position={[-0.085, 0.11, 0.125]} scale={[1, 0.7, 0.4]}><sphereGeometry args={[0.025, 8, 6]} /><meshBasicMaterial color="#e98f7f" transparent opacity={0.35} /></mesh>
              {M(L.skin, g.hand, [0, 0.11, 0.16], [0.35, 0.4, 0.4])}
            </group>
          </group>
        </group>
      </group>
    </group>
  );
}

// ------------------------------------------------------------------------------------------------ la maqueta

/** Caja "hecha a mano": cada pieza lleva un giro y desplazamiento mínimos, fijos. */
function Card({ a, p, c, seed = 1, rough = 0.9 }: { a: [number, number, number]; p: [number, number, number]; c: string; seed?: number; rough?: number }) {
  const r = (rnd(seed) - 0.5) * 0.03;
  return <mesh position={p} rotation={[0, r, (rnd(seed + 9) - 0.5) * 0.012]} castShadow receiveShadow><boxGeometry args={a} /><meshStandardMaterial color={c} roughness={rough} /></mesh>;
}

const BALSA = "#d9c29b", CARD = "#b8b0a3", DARK = "#3d3a37", FELT = "#8a9a86";

function Desk({ at, rot = 0, seed }: { at: [number, number]; rot?: number; seed: number }) {
  return (
    <group position={[at[0], 0, at[1]]} rotation={[0, rot, 0]}>
      <Card a={[1.5, 0.05, 0.75]} p={[0, 0.72, 0]} c={BALSA} seed={seed} />
      {[[-0.68, -0.32], [0.68, -0.32], [-0.68, 0.32], [0.68, 0.32]].map(([x, z], i) => <Card key={i} a={[0.05, 0.7, 0.05]} p={[x, 0.35, z]} c={DARK} seed={seed + i + 1} />)}
      <Card a={[1.38, 0.32, 0.03]} p={[0, 0.5, -0.33]} c={CARD} seed={seed + 7} />
    </group>
  );
}
function Chair({ at, rot = 0, seed }: { at: [number, number]; rot?: number; seed: number }) {
  return (
    <group position={[at[0], 0, at[1]]} rotation={[0, rot, 0]}>
      <Card a={[0.46, 0.07, 0.46]} p={[0, 0.44, 0]} c="#4b4f57" seed={seed} />
      <Card a={[0.44, 0.48, 0.06]} p={[0, 0.72, 0.22]} c="#4b4f57" seed={seed + 1} />
      <Card a={[0.06, 0.4, 0.06]} p={[0, 0.2, 0]} c={DARK} seed={seed + 2} />
      <Card a={[0.5, 0.04, 0.08]} p={[0, 0.02, 0]} c={DARK} seed={seed + 3} />
      <Card a={[0.08, 0.04, 0.5]} p={[0, 0.02, 0]} c={DARK} seed={seed + 4} />
    </group>
  );
}

function Screen() {
  const mat = useRef<THREE.MeshStandardMaterial>(null!);
  useFrame(() => {
    const t = labTime(), f = sample(t);
    mat.current.emissiveIntensity = 0.1 + f.screen * (0.9 + f.typing * 0.15 * (0.5 + 0.5 * Math.sin(t * 5)));
  });
  return (
    <group position={[PLAN.desk.pos[0], 0.745, PLAN.desk.pos[1] - 0.12]}>
      <Card a={[0.18, 0.02, 0.14]} p={[0, 0.01, 0]} c={DARK} seed={40} />
      <Card a={[0.04, 0.22, 0.04]} p={[0, 0.12, 0]} c={DARK} seed={41} />
      <Card a={[0.66, 0.42, 0.05]} p={[0, 0.42, 0]} c="#2b2a29" seed={42} />
      <mesh position={[0, 0.42, 0.027]}><planeGeometry args={[0.58, 0.34]} /><meshStandardMaterial ref={mat} color="#1a2230" emissive="#cfe3ff" emissiveIntensity={0.1} roughness={0.4} /></mesh>
      <Card a={[0.46, 0.025, 0.15]} p={[0, 0.012, 0.36]} c="#e9e4da" seed={43} />
    </group>
  );
}

function Loose() {
  const folder = useRef<THREE.Mesh>(null!);
  const stamp = useRef<THREE.Group>(null!);
  useFrame(() => {
    const t = labTime(), f = sample(t);
    folder.current.visible = (f.folder === "table" && f.partner.mode !== "hand") || f.folder === "boss";
    if (f.folder === "table") folder.current.position.set(PLAN.meeting.pos[0] + 0.15, 0.775, PLAN.meeting.pos[1] - 0.05);
    else folder.current.position.set(PLAN.bossDesk.pos[0] - 0.35, 0.765, PLAN.bossDesk.pos[1] + 0.12);
    // la ficha verde de "aprobado" aparece con un rebote suave (easeOutBack), flota y se encoge al final
    const u = Math.min(1, Math.max(0, (t - 19.1) / 0.5)), o = Math.min(1, Math.max(0, (t - 23.0) / 0.4));
    const back = 1 + 2.2 * Math.pow(u - 1, 3) + 1.2 * Math.pow(u - 1, 2);
    const k = f.approved && t < 23.4 ? back * (1 - o) : 0;
    stamp.current.visible = k > 0.01;
    stamp.current.scale.setScalar(Math.max(0.01, k));
    stamp.current.position.y = 0.95 + Math.sin(t * 2.5) * 0.02;
  });
  return (
    <>
      <mesh ref={folder} castShadow rotation={[0, 0.35, 0]}><boxGeometry args={[0.26, 0.03, 0.33]} />{clay("#2f6db3")}</mesh>
      <group ref={stamp} position={[PLAN.bossDesk.pos[0] - 0.35, 0.95, PLAN.bossDesk.pos[1] + 0.12]}>
        <mesh castShadow rotation={[Math.PI / 2, 0, 0]}><cylinderGeometry args={[0.13, 0.13, 0.04, 20]} />{clay("#3f8f5a")}</mesh>
        <mesh position={[-0.03, -0.01, 0.025]} rotation={[0, 0, 0.8]}><boxGeometry args={[0.08, 0.03, 0.02]} />{clay("#f4efe6")}</mesh>
        <mesh position={[0.03, 0.02, 0.025]} rotation={[0, 0, -0.85]}><boxGeometry args={[0.15, 0.03, 0.02]} />{clay("#f4efe6")}</mesh>
      </group>
    </>
  );
}

function Plant({ at, seed }: { at: [number, number]; seed: number }) {
  const leaf = useMemo(() => lumpy(new THREE.SphereGeometry(0.2, 12, 10), 0.03, seed), [seed]);
  return (
    <group position={[at[0], 0, at[1]]}>
      <Card a={[0.32, 0.36, 0.32]} p={[0, 0.18, 0]} c="#c46f4c" seed={seed} rough={0.95} />
      {[[0, 0.55, 0, 1], [0.12, 0.72, 0.05, 0.8], [-0.1, 0.78, -0.05, 0.75], [0.02, 0.92, 0.02, 0.6]].map(([x, y, z, s], i) => (
        <mesh key={i} geometry={leaf} position={[x, y, z]} scale={s} castShadow>{clay("#5f7f55")}</mesh>
      ))}
    </group>
  );
}

function Set() {
  const { x0, x1, z0, z1 } = PLAN.room;
  const w = x1 - x0, d = z1 - z0;
  return (
    <>
      {/* mesa de estudio sobre la que está la maqueta */}
      <mesh position={[0, -0.16, 0]} receiveShadow><boxGeometry args={[w + 5, 0.2, d + 4]} /><meshStandardMaterial color="#d9b994" roughness={0.75} /></mesh>
      {/* base de la maqueta (cartón pluma con canto blanco) */}
      <mesh position={[(x0 + x1) / 2, -0.03, (z0 + z1) / 2]} receiveShadow><boxGeometry args={[w, 0.06, d]} /><meshStandardMaterial color="#e9e4da" roughness={0.95} /></mesh>
      <mesh position={[(x0 + x1) / 2, 0.002, (z0 + z1) / 2]} rotation={[-Math.PI / 2, 0, 0]} receiveShadow><planeGeometry args={[w - 0.1, d - 0.1]} /><meshStandardMaterial color="#cfc6b7" roughness={1} /></mesh>
      {/* paredes de cartón, cortadas bajas */}
      <Card a={[w, 1.6, 0.06]} p={[(x0 + x1) / 2, 0.8, z0 - 0.03]} c="#efe9df" seed={2} />
      <Card a={[0.06, 1.6, d]} p={[x0 - 0.03, 0.8, (z0 + z1) / 2]} c="#e6dfd3" seed={3} />
      <Card a={[2.0, 0.9, 0.02]} p={[-0.9, 1.05, z0 + 0.01]} c="#bcd0e0" seed={4} rough={0.4} />
      <Card a={[0.9, 1.5, 0.02]} p={[x0 + 0.01, 0.75, PLAN.door[1]]} c="#a58d6f" seed={5} />
      {/* alfombra de fieltro de la reunión */}
      <mesh position={[PLAN.meeting.pos[0], 0.01, PLAN.meeting.pos[1]]} rotation={[-Math.PI / 2, 0, 0]} receiveShadow><circleGeometry args={[1.45, 28]} /><meshStandardMaterial color={FELT} roughness={1} /></mesh>
      <Desk at={PLAN.desk.pos} seed={10} />
      <Chair at={PLAN.seat} seed={20} />
      <Screen />
      <Desk at={PLAN.bossDesk.pos} rot={Math.PI} seed={30} />
      <Chair at={PLAN.boss} rot={Math.PI} seed={50} />
      <group position={[PLAN.bossDesk.pos[0] + 0.15, 0.745, PLAN.bossDesk.pos[1] - 0.05]}>
        <Card a={[0.42, 0.02, 0.3]} p={[0, 0.01, 0]} c="#9a9ea6" seed={60} />
        <Card a={[0.42, 0.28, 0.02]} p={[0, 0.15, -0.14]} c="#9a9ea6" seed={61} />
      </group>
      {/* mesa redonda de balsa */}
      <group position={[PLAN.meeting.pos[0], 0, PLAN.meeting.pos[1]]}>
        <mesh position={[0, 0.74, 0]} castShadow receiveShadow><cylinderGeometry args={[PLAN.meeting.r, PLAN.meeting.r, 0.05, 16]} /><meshStandardMaterial color={BALSA} roughness={0.9} /></mesh>
        <Card a={[0.08, 0.72, 0.08]} p={[0, 0.36, 0]} c={DARK} seed={70} />
      </group>
      <Plant at={[-4.7, -3.1]} seed={80} />
      <Plant at={[4.7, 2.9]} seed={81} />
      <Card a={[1.6, 1.4, 0.35]} p={[-0.2, 0.7, -3.4]} c={BALSA} seed={90} />
      {[0.35, 0.8, 1.2].map((y, i) => <Card key={i} a={[1.5, 0.03, 0.32]} p={[-0.2, y, -3.38]} c="#c7ae86" seed={91 + i} />)}
      <Loose />
    </>
  );
}

/** Luz de tarde, cálida y estable. */
function Lights() {
  return (
    <>
      <ambientLight intensity={0.7} color="#fff1e0" />
      <hemisphereLight args={["#fff4e6", "#d9c3a5", 0.9]} />
      <directionalLight castShadow position={[5, 9, 7]} intensity={2.0} color="#ffe2bd"
        shadow-mapSize={[1024, 1024]} shadow-bias={-0.0006} shadow-normalBias={0.02} shadow-radius={8}
        shadow-camera-left={-8} shadow-camera-right={8} shadow-camera-top={7} shadow-camera-bottom={-7} shadow-camera-near={1} shadow-camera-far={30} />
      <directionalLight position={[-6, 5, 4]} intensity={0.6} color="#ffeedd" />
    </>
  );
}

export default function PanelD({ active }: { active: boolean }) {
  const bubble = useRef<HTMLDivElement>(null);
  return (
    <div className="absolute inset-0 overflow-hidden">
      <Canvas shadows="soft" dpr={1} frameloop="demand" camera={{ position: [5.8, 4.6, 8.2], fov: 30, near: 0.3, far: 60 }}
        onCreated={({ camera }) => camera.lookAt(0.1, 0.5, -0.4)} gl={{ antialias: false }}>
        <color attach="background" args={["#f4e9dc"]} />
        <Lights />
        <Set />
        <Figure role="agent" />
        <Figure role="partner" />
        <Figure role="boss" />
        <BubbleProjector el={bubble} standH={2.15} seatH={1.75} />
        <EffectComposer multisampling={0}>
          <TiltShift2 blur={0.14} taper={0.6} start={[0, 0.52]} end={[1, 0.52]} samples={8} />
          <ToneMapping mode={ToneMappingMode.ACES_FILMIC} />
          <Vignette offset={0.35} darkness={0.18} />
        </EffectComposer>
        <FpsDriver fps={30} active={active} />
      </Canvas>
      <div ref={bubble} style={{ opacity: 0, transition: "opacity .3s" }} className="pointer-events-none absolute left-0 top-0 whitespace-nowrap rounded-[12px] bg-[#fffaf2] px-3 py-1.5 text-[11.5px] font-medium text-[#5a3e2c] shadow-[0_4px_14px_rgba(120,80,40,0.18)] ring-1 ring-[#e9d8c2]" />
    </div>
  );
}
