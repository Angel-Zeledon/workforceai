// EXPERIMENTAL (spike /office-lab, desechable).
// Panel B, "Estudio": Three.js con modelos glTF y animación esquelética. Personajes CC0 de Quaternius (Ultimate Animated
// Character Pack) con clips grabados (Idle, Walk, Walk_Carry, SitDown, StandUp, Victory) mezclados con AnimationMixer
// (fundidos cruzados). Los gestos que el paquete no trae (teclear, hablar, entregar, asentir) son clips de fotogramas
// clave hechos aquí y mezclados en modo ADITIVO encima del clip base. Muebles CC0 de Kenney (Furniture Kit) con los
// materiales recoloreados a una paleta sobria. Luz de estudio: HDRI de la oficina, sombras suaves, AO y una cámara que
// se mueve despacio siguiendo al agente.
"use client";
import { Suspense, useEffect, useMemo, useRef } from "react";
import { Canvas, useFrame, useThree } from "@react-three/fiber";
import { Environment, useGLTF } from "@react-three/drei";
import { BubbleProjector, FpsDriver } from "./Bubble3D";
import { EffectComposer, N8AO, SMAA, ToneMapping, Vignette } from "@react-three/postprocessing";
import { ToneMappingMode } from "postprocessing";
import * as THREE from "three";
import { labTime } from "./clock";
import { PLAN, sample, type Frame } from "./script";

const K = 1.95; // Kenney -> metros
const BASE = "/office-lab";
const CHAR = { agent: `${BASE}/quaternius-characters/Suit_Male.glb`, partner: `${BASE}/quaternius-characters/Casual2_Female.glb`, boss: `${BASE}/quaternius-characters/OldClassy_Female.glb` };
const FOLDER = "#0A66C2";

/** Paleta sobria encima de los materiales de juguete de Kenney (madera naranja, tapizado rojo). */
const RECOLOR: Record<string, { c: string; r?: number; m?: number }> = {
  wood: { c: "#cbb59a", r: 0.7 }, woodDark: { c: "#8f7a63", r: 0.7 }, metal: { c: "#c9ced6", r: 0.4, m: 0.3 }, metalMedium: { c: "#59616d", r: 0.5, m: 0.2 },
  metalDark: { c: "#2a2f36", r: 0.45, m: 0.2 }, carpet: { c: "#3b4a5c", r: 1 }, carpetDarker: { c: "#2c3846", r: 1 }, plant: { c: "#5d8a6a", r: 0.8 }, lamp: { c: "#fff4dc", r: 0.6 },
};

function prepKenney(src: THREE.Object3D): THREE.Group {
  const o = src.clone(true);
  o.traverse((n) => {
    const m = n as THREE.Mesh;
    if (!m.isMesh) return;
    m.castShadow = true; m.receiveShadow = true;
    const mats = (Array.isArray(m.material) ? m.material : [m.material]).map((mt) => {
      const c = (mt as THREE.MeshStandardMaterial).clone();
      const rc = RECOLOR[c.name];
      if (rc) { c.color.set(rc.c); c.roughness = rc.r ?? c.roughness; c.metalness = rc.m ?? 0; }
      c.envMapIntensity = 0.7;
      return c;
    });
    m.material = Array.isArray(m.material) ? mats : mats[0];
  });
  o.scale.setScalar(K);
  o.updateMatrixWorld(true);
  const box = new THREE.Box3().setFromObject(o);
  const c = box.getCenter(new THREE.Vector3());
  o.position.set(-c.x, -box.min.y, -c.z);
  const g = new THREE.Group();
  g.add(o);
  g.userData.size = box.getSize(new THREE.Vector3());
  return g;
}

function Piece({ name, pos, rot = 0, y = 0, onReady }: { name: string; pos: [number, number]; rot?: number; y?: number; onReady?: (g: THREE.Group) => void }) {
  const { scene } = useGLTF(`${BASE}/kenney-furniture/${name}.glb`);
  const g = useMemo(() => prepKenney(scene), [scene]);
  useEffect(() => { onReady?.(g); }, [g, onReady]);
  return <primitive object={g} position={[pos[0], y, pos[1]]} rotation={[0, rot, 0]} />;
}

/** Pantalla que brilla y cambia mientras el agente teclea (un plano emisivo delante del monitor de Kenney). */
function Screen({ pos, rot, y, boss = false }: { pos: [number, number]; rot: number; y: number; boss?: boolean }) {
  const mat = useRef<THREE.MeshStandardMaterial>(null!);
  const tex = useMemo(() => {
    const c = document.createElement("canvas"); c.width = 128; c.height = 96;
    const t = new THREE.CanvasTexture(c); t.colorSpace = THREE.SRGBColorSpace;
    return { c, g: c.getContext("2d")!, t };
  }, []);
  useFrame(() => {
    const t = labTime(), f = sample(t);
    const on = boss ? 1 : f.screen;
    const { g, c } = tex;
    g.fillStyle = "#0f1720"; g.fillRect(0, 0, c.width, c.height);
    if (on > 0.02) {
      g.globalAlpha = on;
      g.fillStyle = "#e9f2fb"; g.fillRect(4, 4, c.width - 8, c.height - 8);
      g.fillStyle = FOLDER; g.fillRect(4, 4, c.width - 8, 12);
      const lines = boss ? 6 : Math.floor(((t * 3) % 9) + 1) * (f.typing > 0 ? 1 : 0) + (f.typing > 0 ? 0 : 6);
      for (let i = 0; i < Math.min(lines, 8); i++) { g.fillStyle = i % 3 === 2 ? "#9fb3c8" : "#4b5b6e"; g.fillRect(10, 22 + i * 8, 30 + ((i * 37) % 70), 4); }
      if (f.typing > 0 && !boss && Math.sin(t * 12) > 0) { g.fillStyle = "#0A66C2"; g.fillRect(10 + 30 + ((Math.min(lines, 8) - 1) * 37) % 70, 22 + (Math.min(lines, 8) - 1) * 8, 3, 5); }
      g.globalAlpha = 1;
    }
    tex.t.needsUpdate = true;
    mat.current.emissiveIntensity = 0.15 + on * (1.1 + (boss ? 0 : f.typing * 0.4));
  });
  return (
    <mesh position={[pos[0], y, pos[1]]} rotation={[0, rot, 0]}>
      <planeGeometry args={[0.66, 0.44]} />
      <meshStandardMaterial ref={mat} color="#000" emissive="#ffffff" emissiveMap={tex.t} map={tex.t} toneMapped={false} />
    </mesh>
  );
}

// ------------------------------------------------------------------------------------------------ personajes

type Role = "agent" | "partner" | "boss";
type BaseClip = "Idle" | "Walk" | "Walk_Carry" | "SitDown" | "StandUp" | "Victory" | "Seated";
type AddClip = "type" | "talk" | "reach" | "nod";
interface Want { base: BaseClip; /** segundos de guion desde que debe empezar el clip (clips de una vez) */ since: number; /** cuánto debe durar */ dur?: number; add: Partial<Record<AddClip, number>>; carry: boolean }

function wantFor(role: Role, f: Frame): Want {
  if (role === "agent") {
    const add: Want["add"] = {};
    const carry = f.folder === "agent";
    let base: BaseClip = "Idle", since = 0, dur: number | undefined;
    switch (f.phase) {
      case "enter": case "walkMeet": case "walkBack": base = "Walk"; break;
      case "walkBoss": base = "Walk_Carry"; break;
      case "sit": case "sitBack": if (f.local < 0.3) base = "Idle"; else { base = "SitDown"; since = f.local - 0.3; dur = 0.9; } break;
      case "type": base = "Seated"; add.type = f.typing; break;
      case "stand": base = "StandUp"; since = f.local; dur = 0.8; break;
      case "talk": base = "Idle"; if (f.bubble?.who === "agent") add.talk = 1; else if (f.bubble?.who === "partner") add.nod = 1; break;
      case "give": base = "Idle"; add.reach = f.reach; break;
      case "approve": base = "Idle"; break;
      case "celebrate": base = "Victory"; since = f.local; dur = 1.8; break;
    }
    return { base, since, dur, add, carry };
  }
  if (role === "partner") {
    const m = f.partner.mode;
    return { base: "Idle", since: 0, add: m === "listen" ? { nod: 1 } : m === "talk" ? { talk: 1 } : m === "hand" ? { reach: 1 } : {}, carry: m === "hand" };
  }
  const m = f.boss.mode;
  return { base: "Seated", since: 0, add: m === "work" ? { type: 1 } : m === "receive" ? { reach: Math.sin(Math.PI * Math.min(1, f.boss.u)) } : m === "approve" ? { nod: 1 } : m === "clap" ? { talk: 1 } : {}, carry: false };
}

const X = new THREE.Vector3(1, 0, 0), Y = new THREE.Vector3(0, 1, 0), Z = new THREE.Vector3(0, 0, 1);

interface Rig { root: THREE.Group; update: (f: Frame, dt: number, jump: boolean) => void; hand: THREE.Object3D | null; hips: THREE.Object3D | null }

function bone(model: THREE.Object3D, name: string) {
  return model.getObjectByName(name) ?? model.getObjectByName(name.replace(/\./g, "")) ?? null;
}

/**
 * Un clip aditivo hecho a mano: para cada hueso, giros alrededor de ejes del PERSONAJE (X adelante/atrás, Y girar,
 * Z de lado) convertidos al espacio local del hueso en la pose de referencia, así no dependemos de cómo está orientado
 * cada hueso del esqueleto.
 */
function additiveClip(name: string, model: THREE.Object3D, dur: number, n: number, spec: Record<string, (u: number) => [number, number, number]>) {
  const tracks: THREE.KeyframeTrack[] = [];
  const times: number[] = [];
  for (let i = 0; i <= n; i++) times.push((dur * i) / n);
  const qw = new THREE.Quaternion(), inv = new THREE.Quaternion(), q = new THREE.Quaternion(), qa = new THREE.Quaternion();
  for (const [bn, fn] of Object.entries(spec)) {
    const b = bone(model, bn);
    if (!b) continue;
    b.getWorldQuaternion(qw);
    inv.copy(qw).invert();
    const ax = X.clone().applyQuaternion(inv), ay = Y.clone().applyQuaternion(inv), az = Z.clone().applyQuaternion(inv);
    const vals: number[] = [];
    for (const t of times) {
      const [rx, ry, rz] = fn(t / dur);
      q.setFromAxisAngle(ax, rx);
      q.multiply(qa.setFromAxisAngle(ay, ry));
      q.multiply(qa.setFromAxisAngle(az, rz));
      vals.push(q.x, q.y, q.z, q.w);
    }
    tracks.push(new THREE.QuaternionKeyframeTrack(`${b.name}.quaternion`, times, vals));
  }
  const clip = new THREE.AnimationClip(name, dur, tracks, THREE.AdditiveAnimationBlendMode);
  return clip;
}

const TAU = Math.PI * 2;

/** Piel y ropa por personaje (nombres de material del paquete de Quaternius). */
const LOOKS: Record<Role, { colors: Record<string, string>; hide?: string[] }> = {
  // agente: piel morena media, traje azul marino, camisa blanca
  agent: { colors: { Skin: "#a8714d", Black: "#26303d", Shirt: "#f3f5f7", Details: "#3b4452", Belt: "#1d2127", Hair: "#24180f" } },
  // compañera: piel oscura, chaqueta gris azulado, pantalón grafito
  partner: { colors: { Skin: "#6a4330", Shirt: "#6c7b8e", Pants: "#2c3542", Belt: "#2a211b", Hair: "#17110d" } },
  // jefa: piel clara, blusa marfil, pantalón ciruela, sin el sombrero del modelo
  boss: { colors: { Skin: "#eec6a3", Shirt: "#ece6dc", Pants: "#4d3f57", Detail: "#7a4b5c", Belt: "#3b2c22", Hair: "#6f5f52" }, hide: ["Hat"] },
};

function buildRig(scene: THREE.Object3D, animations: THREE.AnimationClip[], role: Role): Rig {
  const model = scene;
  // el paquete trae la piel casi negra (material "Skin" de maniquí) y ropa de colores de juego: tonos de piel humanos
  // variados y ropa de oficina sobria, por personaje
  const look = LOOKS[role];
  model.traverse((n) => {
    const m = n as THREE.Mesh;
    if (!m.isMesh) return;
    m.castShadow = true; m.receiveShadow = true; m.frustumCulled = false;
    const mat = m.material as THREE.MeshStandardMaterial;
    const c = look.colors[mat.name];
    if (c) mat.color.set(c);
    if (mat.name === "Skin") mat.roughness = 0.62;
    mat.envMapIntensity = 0.6;
    if (look.hide?.includes(mat.name)) m.visible = false;
  });
  // altura ~1.72 m
  model.updateMatrixWorld(true);
  const box = new THREE.Box3().setFromObject(model);
  const h = box.max.y - box.min.y;
  model.scale.multiplyScalar(1.72 / (h || 1));
  const root = new THREE.Group();
  root.add(model);
  const mixer = new THREE.AnimationMixer(model);
  const clip = (n: string) => animations.find((a) => a.name === n)!;
  const actions: Record<string, THREE.AnimationAction> = {};
  for (const n of ["Idle", "Walk", "Walk_Carry", "SitDown", "StandUp", "Victory"]) actions[n] = mixer.clipAction(clip(n));
  // "Seated": el último fotograma de SitDown, sostenido
  const sd = clip("SitDown");
  const seated = THREE.AnimationUtils.subclip(sd, "Seated", Math.max(0, Math.floor(sd.duration * 30) - 2), Math.floor(sd.duration * 30), 30);
  actions.Seated = mixer.clipAction(seated);

  // poses de referencia para los ejes de los clips aditivos
  const refPose = (n: string, at: number) => {
    mixer.stopAllAction();
    const a = mixer.clipAction(clip(n)); a.reset().play(); a.time = at; mixer.update(0);
    root.updateMatrixWorld(true);
  };
  const add: Record<AddClip, THREE.AnimationAction> = {} as never;
  refPose("SitDown", sd.duration - 0.01);
  const type = additiveClip("type", model, 0.5, 10, {
    "UpperArm.L": () => [-0.45, 0, 0], "UpperArm.R": () => [-0.45, 0, 0],
    "LowerArm.L": (u) => [-0.55 + Math.sin(u * TAU * 2) * 0.09, 0, 0], "LowerArm.R": (u) => [-0.55 + Math.sin(u * TAU * 2 + Math.PI) * 0.09, 0, 0],
    "Head": (u) => [0.12 + Math.sin(u * TAU) * 0.02, 0, 0],
  });
  add.type = mixer.clipAction(type);
  refPose("Idle", 0);
  const talk = additiveClip("talk", model, 2.4, 24, {
    "UpperArm.R": (u) => [-0.75 + Math.sin(u * TAU * 2) * 0.22, 0, -0.15 + Math.sin(u * TAU) * 0.12],
    "LowerArm.R": (u) => [-0.8 + Math.sin(u * TAU * 3) * 0.3, 0, 0],
    "UpperArm.L": (u) => [-0.35 + Math.sin(u * TAU + 1) * 0.15, 0, 0.1],
    "LowerArm.L": (u) => [-0.6 + Math.sin(u * TAU * 2 + 1) * 0.2, 0, 0],
    "Head": (u) => [Math.sin(u * TAU * 4) * 0.05, Math.sin(u * TAU) * 0.15, 0],
    "Torso": (u) => [0, Math.sin(u * TAU) * 0.08, 0],
  });
  add.talk = mixer.clipAction(talk);
  const reach = additiveClip("reach", model, 1, 2, {
    "UpperArm.R": () => [-1.15, 0, 0.1], "LowerArm.R": () => [-0.25, 0, 0], "Head": () => [0.15, 0, 0], "Torso": () => [0.12, 0, 0],
  });
  add.reach = mixer.clipAction(reach);
  const nod = additiveClip("nod", model, 1.1, 22, { "Head": (u) => [(1 - Math.cos(u * TAU * 2)) * 0.12, 0, 0], "Neck": (u) => [(1 - Math.cos(u * TAU * 2)) * 0.05, 0, 0] });
  add.nod = mixer.clipAction(nod);
  mixer.stopAllAction();
  for (const a of Object.values(add)) { a.play(); a.setEffectiveWeight(0); }

  // la carpeta en la mano derecha
  const hand = bone(model, "Fist.R");
  let folder: THREE.Mesh | null = null;
  if (hand) {
    root.updateMatrixWorld(true);
    const s = hand.getWorldScale(new THREE.Vector3()).x || 1;
    folder = new THREE.Mesh(new THREE.BoxGeometry(0.25 / s, 0.02 / s, 0.32 / s), new THREE.MeshStandardMaterial({ color: FOLDER, roughness: 0.55 }));
    folder.castShadow = true;
    folder.position.set(0, 0.06 / s, 0.0);
    folder.visible = false;
    hand.add(folder);
  }

  let cur: BaseClip | "" = "";
  const weights: Record<AddClip, number> = { type: 0, talk: 0, reach: 0, nod: 0 };
  const update = (f: Frame, dt: number, jump: boolean) => {
    const w = wantFor(role, f);
    if (w.base !== cur || jump) {
      const next = actions[w.base];
      const once = w.base === "SitDown" || w.base === "StandUp" || w.base === "Victory";
      next.reset();
      next.setLoop(once ? THREE.LoopOnce : THREE.LoopRepeat, Infinity);
      next.clampWhenFinished = true;
      next.timeScale = once && w.dur ? next.getClip().duration / w.dur : 1;
      if (once) next.time = Math.min(next.getClip().duration, w.since * next.timeScale);
      next.play();
      if (jump || !cur) { for (const [k, a] of Object.entries(actions)) if (k !== w.base) a.stop(); next.setEffectiveWeight(1); }
      else { next.fadeIn(0.28); actions[cur].fadeOut(0.28); }
      cur = w.base;
    }
    if (w.base === "Walk" || w.base === "Walk_Carry") actions[w.base].timeScale = 0.45 + 0.75 * f.speed;
    for (const k of Object.keys(weights) as AddClip[]) {
      const target = w.add[k] ?? 0;
      weights[k] += (target - weights[k]) * (jump ? 1 : Math.min(1, dt * 7));
      add[k].setEffectiveWeight(weights[k]);
    }
    if (folder) folder.visible = w.carry;
    mixer.update(dt);
  };
  return { root, update, hand, hips: bone(model, "Hips") };
}

function Person({ role }: { role: Role }) {
  const { scene, animations } = useGLTF(CHAR[role]);
  const rig = useMemo(() => buildRig(scene, animations, role), [scene, animations, role]);
  const last = useRef<number | null>(null);
  useFrame(() => {
    const t = labTime(), f = sample(t);
    const prev = last.current;
    last.current = t;
    const dt = prev === null ? 0 : t - prev;
    const jump = prev === null || dt < 0 || dt > 0.25 || Math.floor(t / 27) !== Math.floor(prev / 27);
    if (role === "agent") { rig.root.position.set(f.pos[0], 0, f.pos[1]); rig.root.rotation.y = f.yaw; }
    else if (role === "partner") { rig.root.position.set(PLAN.partner[0], 0, PLAN.partner[1]); rig.root.rotation.y = f.partner.yaw; }
    else { rig.root.position.set(PLAN.boss[0], 0, PLAN.boss[1]); rig.root.rotation.y = 0; }
    rig.update(f, jump ? 0 : dt, jump);
  });
  return <primitive object={rig.root} />;
}

function LooseFolder() {
  const ref = useRef<THREE.Mesh>(null!);
  useFrame(() => {
    const f = sample(labTime());
    ref.current.visible = f.folder === "table" || f.folder === "boss";
    if (f.folder === "table") ref.current.position.set(PLAN.meeting.pos[0] + 0.2, 0.735, PLAN.meeting.pos[1] - 0.1);
    else ref.current.position.set(PLAN.bossDesk.pos[0] - 0.35, 0.77, PLAN.bossDesk.pos[1] + 0.1);
  });
  return <mesh ref={ref} castShadow rotation={[0, 0.4, 0]}><boxGeometry args={[0.25, 0.02, 0.32]} /><meshStandardMaterial color={FOLDER} roughness={0.55} /></mesh>;
}

/** Cámara de cine: un plano general que respira (dolly lento) y reencuadra suavemente hacia donde está el agente. */
function CineCam() {
  const cam = useThree((s) => s.camera);
  const tgt = useRef(new THREE.Vector3(0, 0.8, 0));
  const tmp = useMemo(() => new THREE.Vector3(), []);
  useFrame((_, rdt) => {
    const t = labTime(), f = sample(t);
    tmp.set(f.pos[0] * 0.45, 0.85, f.pos[1] * 0.35 - 0.2);
    tgt.current.lerp(tmp, Math.min(1, rdt * 1.2));
    const a = 0.32 + Math.sin(t * 0.11) * 0.1;
    const r = 9.6 + Math.sin(t * 0.07) * 0.5;
    cam.position.set(tgt.current.x * 0.5 + Math.sin(a) * r, 5.4 + Math.sin(t * 0.09) * 0.25, tgt.current.z * 0.4 + Math.cos(a) * r);
    cam.lookAt(tgt.current);
  });
  return null;
}

function Room() {
  const { x0, x1, z0, z1 } = PLAN.room;
  const w = x1 - x0, d = z1 - z0;
  return (
    <group>
      <mesh rotation={[-Math.PI / 2, 0, 0]} position={[(x0 + x1) / 2, 0, (z0 + z1) / 2]} receiveShadow><planeGeometry args={[w + 6, d + 6]} /><meshStandardMaterial color="#e7e4df" roughness={0.55} /></mesh>
      <mesh position={[(x0 + x1) / 2, 1.5, z0 - 0.05]} receiveShadow><boxGeometry args={[w, 3, 0.1]} /><meshStandardMaterial color="#f4f3f1" roughness={0.9} /></mesh>
      <mesh position={[x0 - 0.05, 1.5, (z0 + z1) / 2]} receiveShadow><boxGeometry args={[0.1, 3, d]} /><meshStandardMaterial color="#efeeec" roughness={0.9} /></mesh>
      {/* zócalo y una franja de color corporativo */}
      <mesh position={[(x0 + x1) / 2, 0.05, z0 + 0.01]}><boxGeometry args={[w, 0.1, 0.02]} /><meshStandardMaterial color="#d3d0cb" /></mesh>
      <mesh position={[-1.2, 2.25, z0 + 0.01]}><boxGeometry args={[2.2, 0.06, 0.02]} /><meshStandardMaterial color={FOLDER} roughness={0.5} /></mesh>
    </group>
  );
}

function Furniture() {
  const P = PLAN;
  return (
    <>
      {/* el agente: escritorio (frente hacia +z), monitor, teclado, silla */}
      <Piece name="desk" pos={P.desk.pos} rot={Math.PI} />
      <Piece name="computerScreen" pos={[P.desk.pos[0], P.desk.pos[1] - 0.12]} y={0.74} rot={0} />
      <Screen pos={[P.desk.pos[0], P.desk.pos[1] - 0.105]} y={0.74 + 0.31} rot={0} />
      <Piece name="computerKeyboard" pos={[P.desk.pos[0], P.desk.pos[1] + 0.2]} y={0.74} rot={0} />
      <Piece name="computerMouse" pos={[P.desk.pos[0] + 0.42, P.desk.pos[1] + 0.2]} y={0.74} rot={0} />
      <Piece name="chairDesk" pos={[P.seat[0], P.seat[1] + 0.05]} rot={0} />
      {/* la jefa */}
      <Piece name="desk" pos={P.bossDesk.pos} rot={0} />
      <Piece name="laptop" pos={[P.bossDesk.pos[0] + 0.15, P.bossDesk.pos[1] - 0.05]} y={0.74} rot={Math.PI} />
      <Piece name="lampSquareTable" pos={[P.bossDesk.pos[0] + 0.55, P.bossDesk.pos[1] - 0.15]} y={0.74} />
      <Piece name="chairDesk" pos={[P.boss[0], P.boss[1] - 0.05]} rot={Math.PI} />
      {/* reunión */}
      <Piece name="rugRectangle" pos={P.meeting.pos} rot={0} />
      <Piece name="tableRound" pos={P.meeting.pos} />
      <Piece name="chair" pos={[P.meeting.pos[0], P.meeting.pos[1] - 0.95]} rot={0} />
      <Piece name="chair" pos={[P.meeting.pos[0], P.meeting.pos[1] + 0.95]} rot={Math.PI} />
      {/* ambiente */}
      <Piece name="bookcaseOpen" pos={[-0.4, -3.35]} />
      <Piece name="bookcaseOpen" pos={[0.45, -3.35]} />
      <Piece name="pottedPlant" pos={[-4.7, -3.1]} />
      <Piece name="pottedPlant" pos={[4.7, 2.9]} />
      <Piece name="lampRoundFloor" pos={[4.6, -3.1]} />
      <Piece name="coatRackStanding" pos={[-4.85, 1.6]} />
      <Piece name="trashcan" pos={[-1.3, -2.0]} />
      <Piece name="loungeSofa" pos={[-3.2, 2.8]} rot={Math.PI} />
      <LooseFolder />
    </>
  );
}

function Scene({ bubble }: { bubble: React.RefObject<HTMLDivElement | null> }) {
  return (
    <>
      <color attach="background" args={["#f2f1ef"]} />
      <fog attach="fog" args={["#f2f1ef", 16, 30]} />
      <Environment files="/office/env/room_512.hdr" environmentIntensity={0.55} />
      <hemisphereLight args={["#ffffff", "#d9d3ca", 0.35]} />
      <directionalLight castShadow position={[6, 9, 5]} intensity={2.1} color="#fff6ea" shadow-mapSize={[1024, 1024]} shadow-bias={-0.0004} shadow-normalBias={0.02} shadow-radius={6}
        shadow-camera-left={-7} shadow-camera-right={7} shadow-camera-top={6} shadow-camera-bottom={-6} shadow-camera-near={1} shadow-camera-far={30} />
      <Room />
      <Furniture />
      <Person role="agent" />
      <Person role="partner" />
      <Person role="boss" />
      <BubbleProjector el={bubble} standH={2.05} seatH={1.65} />
      <CineCam />
      <EffectComposer multisampling={0} depthBuffer>
        <N8AO halfRes quality="performance" aoRadius={0.5} intensity={2.2} distanceFalloff={0.8} />
        <ToneMapping mode={ToneMappingMode.ACES_FILMIC} />
        <SMAA />
        <Vignette offset={0.3} darkness={0.35} />
      </EffectComposer>
    </>
  );
}

export default function PanelB({ active }: { active: boolean }) {
  const bubble = useRef<HTMLDivElement>(null);
  return (
    <div className="absolute inset-0 overflow-hidden">
      <Canvas shadows="soft" dpr={1} frameloop="demand" camera={{ position: [4, 5.4, 9], fov: 34, near: 0.3, far: 60 }} gl={{ antialias: false, powerPreference: "high-performance" }}>
        <Suspense fallback={null}><Scene bubble={bubble} /></Suspense>
        <FpsDriver fps={30} active={active} />
      </Canvas>
      <div ref={bubble} style={{ opacity: 0, transition: "opacity .25s" }} className="pointer-events-none absolute left-0 top-0 whitespace-nowrap rounded-[10px] bg-white/95 px-3 py-1.5 text-[11.5px] font-medium text-slate-800 shadow-[0_6px_18px_rgba(15,23,42,0.18)] ring-1 ring-slate-900/5" />
    </div>
  );
}
