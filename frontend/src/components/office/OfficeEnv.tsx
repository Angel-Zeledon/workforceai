"use client";
import { useEffect, useMemo, useRef, useState } from "react";
import { useFrame } from "@react-three/fiber";
import { RoundedBox } from "@react-three/drei";
import * as THREE from "three";
import { useStore } from "@/lib/store";
import { useCost } from "@/lib/cost";
import { usePreferences } from "@/lib/preferences";
import { roleMeta } from "@/lib/meta";
import { useT } from "@/lib/i18n";
import { deskOf, seatOf } from "./registry";
import { effective, useOfficeLayout } from "@/lib/officeLayout";
import { FIXTURES, FIXTURE_IDS, MOOD_BASE, ZONE_DEFAULTS, ZONE_SIZE, type FixtureId, type FloorKind, type LightMix, type ZoneName } from "@/lib/officeLayoutModel";
import { useRewards } from "@/lib/rewards";
import { ChairMesh, DeskSet } from "./Furniture";

export const DESK_H = 0.72;

/** Paleta del entorno: grises piedra/slate, madera clara mate y metal grafito. Nada compite con los personajes. */
const OAK = "#d9c4a3";
const OAK_D = "#c4aa84";
const GRAPHITE = "#3a414c";
const GRAPHITE_L = "#59616d";
const STEEL = "#b3bac4";
const WALL = "#f3f4f7";
const SKIRT = "#d3d8df";
const GLASS = "#c9d8e6";
const PAPER = "#f8f9fb";

const mix = (c: string, to: string, t: number) => new THREE.Color(c).lerp(new THREE.Color(to), t).getStyle();

/** Hormigón pulido en baldosas grandes con juntas finas, generado en canvas (sin assets externos). */
function useConcrete() {
  return useMemo(() => {
    const S = 256;
    const c = document.createElement("canvas");
    c.width = c.height = S;
    const g = c.getContext("2d")!;
    g.fillStyle = "#e7eaee"; g.fillRect(0, 0, S, S);
    // veladuras suaves + grano muy leve
    for (let i = 0; i < 26; i++) {
      const x = Math.random() * S, y = Math.random() * S, r = 30 + Math.random() * 70;
      const gr = g.createRadialGradient(x, y, 0, x, y, r);
      const dark = Math.random() > 0.5;
      gr.addColorStop(0, dark ? "rgba(120,130,145,0.07)" : "rgba(255,255,255,0.14)");
      gr.addColorStop(1, "rgba(255,255,255,0)");
      g.fillStyle = gr; g.fillRect(0, 0, S, S);
    }
    const img = g.getImageData(0, 0, S, S);
    for (let i = 0; i < img.data.length; i += 4) {
      const n = (Math.random() - 0.5) * 7;
      img.data[i] += n; img.data[i + 1] += n; img.data[i + 2] += n;
    }
    g.putImageData(img, 0, 0);
    // juntas
    g.fillStyle = "rgba(80,92,110,0.30)";
    g.fillRect(0, 0, S, 2); g.fillRect(0, 0, 2, S);
    const t = new THREE.CanvasTexture(c);
    t.wrapS = t.wrapT = THREE.RepeatWrapping;
    t.repeat.set(14, 10);
    t.colorSpace = THREE.SRGBColorSpace;
    t.anisotropy = 8;
    return t;
  }, []);
}

const floorCache = new Map<FloorKind, THREE.CanvasTexture>();
function noise(g: CanvasRenderingContext2D, S: number, amp: number) {
  const img = g.getImageData(0, 0, S, S);
  for (let i = 0; i < img.data.length; i += 4) { const n = (Math.random() - 0.5) * amp; img.data[i] += n; img.data[i + 1] += n; img.data[i + 2] += n; }
  g.putImageData(img, 0, 0);
}
/** Floor material textures (light base colors: the layout's tint multiplies them). Concrete keeps its own generator. */
function floorTexture(kind: FloorKind): THREE.CanvasTexture {
  const hit = floorCache.get(kind);
  if (hit) return hit;
  const S = 512;
  const c = document.createElement("canvas");
  c.width = c.height = S;
  const g = c.getContext("2d")!;
  let repeat: [number, number] = [14, 10];
  if (kind === "wood") {
    // long boards with grain and staggered joints
    const rows = 8, h = S / rows;
    for (let r = 0; r < rows; r++) {
      const shade = 168 + Math.round(Math.random() * 26);
      g.fillStyle = `rgb(${shade + 14},${shade - 4},${shade - 38})`; g.fillRect(0, r * h, S, h);
      g.strokeStyle = "rgba(90,55,25,0.12)"; g.lineWidth = 1;
      for (let k = 0; k < 9; k++) { const y = r * h + 3 + Math.random() * (h - 6); g.beginPath(); g.moveTo(0, y); for (let x = 0; x <= S; x += 64) g.lineTo(x, y + Math.sin(x / 90 + k) * 1.4); g.stroke(); }
      g.fillStyle = "rgba(60,35,15,0.45)"; g.fillRect(0, r * h, S, 1.5);
      const jx = (r * 173) % S; g.fillRect(jx, r * h, 1.5, h);
    }
    repeat = [9, 7];
  } else if (kind === "parquet") {
    // herringbone: pairs of blocks at right angles
    const bw = 64, bh = 16;
    for (let y = -bh; y < S + bh; y += bh) for (let x = -bw; x < S + bw; x += bh) {
      const shade = 150 + Math.round(Math.random() * 40);
      g.fillStyle = `rgb(${shade + 28},${shade - 2},${shade - 50})`;
      const horizontal = ((x / bh + y / bh) / 4) % 2 < 1;
      if (horizontal) g.fillRect(x, y, bw, bh); else g.fillRect(x, y, bh, bw);
    }
    g.strokeStyle = "rgba(60,35,15,0.35)"; g.lineWidth = 1;
    for (let k = 0; k <= S; k += bh) { g.beginPath(); g.moveTo(k, 0); g.lineTo(k, S); g.stroke(); g.beginPath(); g.moveTo(0, k); g.lineTo(S, k); g.stroke(); }
    repeat = [8, 6];
  } else if (kind === "carpet") {
    g.fillStyle = "#b9bec6"; g.fillRect(0, 0, S, S);
    noise(g, S, 34);
    g.fillStyle = "rgba(255,255,255,0.07)"; for (let y = 0; y < S; y += 3) g.fillRect(0, y, S, 1);
    repeat = [12, 9];
  } else if (kind === "tile") {
    g.fillStyle = "#e9edf1"; g.fillRect(0, 0, S, S);
    for (let i = 0; i < 40; i++) { g.fillStyle = `rgba(${150 + Math.random() * 60},${160 + Math.random() * 60},${170 + Math.random() * 60},0.06)`; g.fillRect(Math.random() * S, Math.random() * S, 90, 90); }
    g.fillStyle = "rgba(95,108,125,0.55)"; g.fillRect(0, 0, S, 3); g.fillRect(0, 0, 3, S); g.fillRect(0, S / 2, S, 2); g.fillRect(S / 2, 0, 2, S);
    repeat = [9, 7];
  } else { // marble
    g.fillStyle = "#eceef1"; g.fillRect(0, 0, S, S);
    for (let k = 0; k < 14; k++) {
      g.strokeStyle = `rgba(${90 + Math.random() * 40},${100 + Math.random() * 40},${118 + Math.random() * 40},${0.12 + Math.random() * 0.2})`;
      g.lineWidth = 0.6 + Math.random() * 1.8; g.beginPath();
      let x = Math.random() * S, y = 0; g.moveTo(x, y);
      while (y < S) { y += 18 + Math.random() * 26; x += (Math.random() - 0.5) * 70; g.lineTo(x, y); }
      g.stroke();
    }
    noise(g, S, 6);
    g.fillStyle = "rgba(110,120,135,0.35)"; g.fillRect(0, 0, S, 2); g.fillRect(0, 0, 2, S);
    repeat = [7, 5];
  }
  const t = new THREE.CanvasTexture(c);
  t.wrapS = t.wrapT = THREE.RepeatWrapping;
  t.repeat.set(repeat[0], repeat[1]);
  t.colorSpace = THREE.SRGBColorSpace;
  t.anisotropy = 8;
  floorCache.set(kind, t);
  return t;
}
const FLOOR_FINISH: Record<FloorKind, { rough: number; metal: number }> = {
  concrete: { rough: 0.78, metal: 0 }, wood: { rough: 0.55, metal: 0 }, parquet: { rough: 0.5, metal: 0 },
  carpet: { rough: 1, metal: 0 }, tile: { rough: 0.3, metal: 0.05 }, marble: { rough: 0.2, metal: 0.1 },
};

/** Degradado radial neutro para el suelo exterior (reemplaza al "cielo"): claro al centro, más tenue hacia los bordes. */
function useBackdrop() {
  return useMemo(() => {
    const S = 512;
    const c = document.createElement("canvas");
    c.width = c.height = S;
    const g = c.getContext("2d")!;
    const gr = g.createRadialGradient(S / 2, S / 2, S * 0.08, S / 2, S / 2, S / 2);
    gr.addColorStop(0, "#b5bdca");
    gr.addColorStop(0.4, "#a9b2c0");
    gr.addColorStop(1, "#8d97a7");
    g.fillStyle = gr; g.fillRect(0, 0, S, S);
    const t = new THREE.CanvasTexture(c);
    t.colorSpace = THREE.SRGBColorSpace;
    return t;
  }, []);
}

/**
 * Flat sign on the floor, drawn into a canvas texture. It deliberately avoids drei's <Html transform>: that
 * creates a separate React root inside the scene, and unmounting the Canvas (office -> dashboard) then threw
 * "removeChild" errors because the DOM node was already gone.
 */
function FloorSign({ pos, text, color, sub }: { pos: [number, number, number]; text: string; color: string; sub?: string }) {
  const [size, setSize] = useState({ w: 1, h: 1 });
  const tex = useMemo(() => {
    const t = new THREE.CanvasTexture(document.createElement("canvas"));
    t.colorSpace = THREE.SRGBColorSpace;
    t.anisotropy = 4;
    return t;
  }, []);
  useEffect(() => {
    let alive = true;
    const draw = () => {
      if (!alive) return;
      const c = tex.image as HTMLCanvasElement;
      const g = c.getContext("2d");
      if (!g) return;
      const family = (typeof document !== "undefined" && getComputedStyle(document.body).fontFamily) || "Inter, system-ui, sans-serif";
      const S = 2, H = 36, padX = 14, bar = 4; // CSS-px layout, drawn at 2x
      const main = `600 15px ${family}`, small = `500 12px ${family}`;
      g.font = main; const tw = g.measureText(text).width;
      let sw = 0; if (sub) { g.font = small; sw = g.measureText(sub).width + 10; }
      const W = Math.ceil(tw + sw + padX * 2 + bar + 6);
      c.width = W * S; c.height = H * S;
      g.scale(S, S);
      g.clearRect(0, 0, W, H);
      // tarjeta blanca con borde fino y una barra lateral del color del rol
      g.beginPath(); g.roundRect(0.5, 0.5, W - 1, H - 1, 7);
      g.fillStyle = "rgba(255,255,255,0.94)"; g.fill();
      g.lineWidth = 1; g.strokeStyle = "rgba(120,131,148,0.55)"; g.stroke();
      g.save(); g.beginPath(); g.roundRect(0.5, 0.5, W - 1, H - 1, 7); g.clip();
      g.fillStyle = color; g.fillRect(0, 0, bar, H); g.restore();
      g.textBaseline = "middle"; g.textAlign = "left";
      g.font = main; g.fillStyle = "#151b26"; g.fillText(text, padX + bar, H / 2 + 0.5);
      if (sub) { g.font = small; g.fillStyle = "#5b6678"; g.fillText(sub, padX + bar + tw + 10, H / 2 + 0.5); }
      tex.needsUpdate = true;
      setSize({ w: W * 0.0105, h: H * 0.0105 }); // 1 CSS px ~ 0.0105 world units
    };
    draw();
    document.fonts?.ready.then(draw).catch(() => {});
    return () => { alive = false; };
  }, [tex, text, sub, color]);
  useEffect(() => () => tex.dispose(), [tex]);
  return (
    <mesh position={pos} rotation={[-Math.PI / 2, 0, 0]} renderOrder={1}>
      <planeGeometry args={[size.w, size.h]} />
      <meshBasicMaterial map={tex} transparent depthWrite={false} toneMapped={false} />
    </mesh>
  );
}

/** Planta discreta: maceta de hormigón y hojas lanceoladas en verde apagado. */
function Plant({ position, s = 1, tint = "#7f9a86" }: { position: [number, number, number]; s?: number; tint?: string }) {
  const leaves = [
    [0, 0, 0.62, 0], [0.09, 0.08, 0.5, 0.35], [-0.1, -0.05, 0.54, -0.4], [0.02, -0.11, 0.44, 0.15], [-0.07, 0.1, 0.4, -0.2], [0.12, -0.06, 0.34, 0.5],
  ];
  return (
    <group position={position} scale={s}>
      <mesh castShadow position={[0, 0.2, 0]}><cylinderGeometry args={[0.24, 0.19, 0.4, 24]} /><meshStandardMaterial color="#d5d9df" roughness={0.9} /></mesh>
      <mesh position={[0, 0.4, 0]}><cylinderGeometry args={[0.22, 0.22, 0.02, 24]} /><meshStandardMaterial color="#6c6a63" roughness={1} /></mesh>
      {leaves.map(([x, z, h, tilt], i) => (
        <mesh key={i} castShadow position={[x, 0.4 + h / 2, z]} rotation={[tilt * 0.4, i, tilt]} scale={[1, 1, 0.45]}>
          <coneGeometry args={[0.07 + (i % 3) * 0.012, h, 6]} />
          <meshStandardMaterial color={mix(tint, "#ffffff", i * 0.045)} roughness={0.85} />
        </mesh>
      ))}
    </group>
  );
}

function DeskProp({ role, accent }: { role: string; accent: string }) {
  const y = DESK_H;
  switch (role) {
    case "legal":
      return (<group position={[0.8, y, -0.15]}>{[0, 1, 2].map((i) => (<RoundedBox key={i} args={[0.28 - i * 0.02, 0.05, 0.2]} radius={0.01} smoothness={2} castShadow position={[0, 0.025 + i * 0.05, 0]}><meshStandardMaterial color={["#9aa6ba", "#7f8ca3", "#bcc4d1"][i]} /></RoundedBox>))}</group>);
    case "accounting":
      return (<RoundedBox args={[0.2, 0.04, 0.26]} radius={0.01} smoothness={2} castShadow position={[0.8, y + 0.02, 0.1]} rotation={[0, 0.3, 0]}><meshStandardMaterial color={GRAPHITE_L} /></RoundedBox>);
    case "hr":
      return <Plant position={[0.85, y, -0.2]} s={0.4} tint="#8da58f" />;
    case "analyst":
      return (
        <group position={[-0.85, y, -0.25]} rotation={[0, 0.35, 0]}>
          <RoundedBox args={[0.55, 0.38, 0.03]} radius={0.015} smoothness={2} castShadow position={[0, 0.3, 0]}><meshStandardMaterial color={PAPER} /></RoundedBox>
          {[0, 1, 2, 3].map((i) => (<mesh key={i} position={[-0.18 + i * 0.12, 0.2 + i * 0.03 + (0.08 + i * 0.06) / 2 - 0.04, 0.02]}><boxGeometry args={[0.07, 0.08 + i * 0.06, 0.01]} /><meshBasicMaterial color={accent} /></mesh>))}
        </group>
      );
    case "operations":
      return (<group position={[0.8, y, 0]}><mesh castShadow position={[0, 0.05, 0]}><cylinderGeometry args={[0.2, 0.22, 0.1, 20]} /><meshStandardMaterial color="#d4c29a" /></mesh><mesh position={[0, 0.12, 0]}><sphereGeometry args={[0.15, 14, 8, 0, Math.PI * 2, 0, Math.PI / 2]} /><meshStandardMaterial color="#c9b383" /></mesh></group>);
    case "sales":
      return (<group position={[0.82, y, -0.1]}><mesh castShadow position={[0, 0.07, 0]}><cylinderGeometry args={[0.1, 0.08, 0.14, 18]} /><meshStandardMaterial color="#f3f4f6" /></mesh><mesh position={[0, 0.15, 0]}><cylinderGeometry args={[0.085, 0.085, 0.015, 18]} /><meshStandardMaterial color="#5a4a3f" /></mesh></group>);
    default:
      return (<mesh castShadow position={[0.8, y + 0.04, 0]}><sphereGeometry args={[0.08, 12, 10]} /><meshStandardMaterial color="#f3f4f6" /></mesh>);
  }
}

function Desk({ id, index }: { id: string; index: number }) {
  const agent = useStore((s) => s.agents[id]);
  const bars = useRef<THREE.Group>(null!);
  const screen = useRef<THREE.MeshBasicMaterial>(null!);
  const progTrack = useRef<THREE.Mesh>(null!);
  const progFill = useRef<THREE.Mesh>(null!);
  const meta = roleMeta(agent?.role);
  const cfg = useOfficeLayout((s) => effective(s).desks[id]);
  const editing = useOfficeLayout((s) => s.editing);
  const catalog = useRewards((s) => s.catalog);
  const deskItem = cfg?.desk ? catalog.find((i) => i.id === cfg.desk) : undefined;
  const chairItem = cfg?.chair ? catalog.find((i) => i.id === cfg.chair) : undefined;
  const ghost = useOfficeLayout((s) => (s.deskGhost?.id === id ? s.deskGhost : null));
  const home = deskOf({ id, role: agent?.role || "" }, index);
  const [dx, dz] = ghost ? [ghost.x, ghost.z] : home;
  const [sx, sz] = seatOf([dx, dz]);
  useFrame(({ clock }) => {
    const S = useStore.getState();
    const ag = S.agents[id];
    const st = ag?.state;
    const reduce = usePreferences.getState().prefs.reduceMotion;
    const t = reduce ? 0 : clock.elapsedTime;
    const on = st === "working" || st === "thinking" || st === "reviewing" || st === "waiting" || st === "talking";
    // monitor wakes up as soon as a task is assigned to this agent (task.created), before work starts
    const assigned = st === "idle" && Object.values(S.tasks).some((tk) => tk.agent_id === id && (tk.status === "pending" || tk.status === "running"));
    const lvl = st === "working" ? 1 : on ? 0.8 : assigned ? 0.6 : st === "awaiting_approval" ? 0.7 : st === "error" || st === "blocked" ? 0.8 : 0.35;
    const col = st === "error" || st === "blocked" ? "#e7a29c" : st === "awaiting_approval" ? "#e8cd93" : "#a9c9e6";
    screen.current.color.set(col).multiplyScalar(lvl);
    bars.current.visible = on || st === "error";
    if (bars.current.visible) {
      bars.current.children.forEach((m, i) => {
        const w = st === "working" && !reduce ? 0.35 + 0.65 * Math.abs(Math.sin(t * (1.5 + i * 0.7) + i)) : 0.55 + 0.1 * Math.sin(i);
        m.scale.x = w; m.position.x = -0.24 + (0.48 * w) / 2;
      });
    }
    // progress bar under the text lines: real agent.progress of the task being worked on
    const busy = !!ag?.current_task_id && (st === "working" || st === "thinking" || st === "reviewing");
    progTrack.current.visible = busy; progFill.current.visible = busy;
    if (busy) {
      const f = Math.max(0.03, Math.min(1, (ag.progress || 0) / 100));
      progFill.current.scale.x = f; progFill.current.position.x = -0.24 + (0.48 * f) / 2;
    }
  });
  if (!agent) return null;
  const T = DESK_H;
  return (
    <group position={[dx, 0, dz]}>
      {/* alfombra técnica del puesto: panel plano en un tono casi neutro del rol */}
      <RoundedBox args={[3.5, 0.02, 2.6]} radius={0.01} smoothness={2} position={[0.1, 0.011, 0.95]} receiveShadow><meshStandardMaterial color="#c4cbd6" roughness={1} /></RoundedBox>
      {/* escritorio: tablero, canto en el color del rol, panel frontal y patas (estilo elegido en el editor) */}
      <DeskSet item={deskItem} accent={meta.color}>
      {/* monitor (pantalla hacia +z) */}
      <group position={[0, T, -0.12]}>
        <mesh castShadow position={[0, 0.01, 0]}><boxGeometry args={[0.26, 0.02, 0.18]} /><meshStandardMaterial color={STEEL} metalness={0.3} roughness={0.5} /></mesh>
        <mesh castShadow position={[0, 0.17, 0]}><boxGeometry args={[0.04, 0.3, 0.03]} /><meshStandardMaterial color={STEEL} metalness={0.3} roughness={0.5} /></mesh>
        <RoundedBox args={[0.98, 0.58, 0.04]} radius={0.02} smoothness={3} castShadow position={[0, 0.5, 0]}><meshStandardMaterial color="#2c323b" roughness={0.5} /></RoundedBox>
        <mesh position={[0, 0.5, 0.024]}><planeGeometry args={[0.9, 0.5]} /><meshBasicMaterial ref={screen} color="#bfe6ff" toneMapped={false} /></mesh>
        <group ref={bars} position={[0, 0.5, 0.03]}>
          {[0.15, 0.07, -0.01, -0.09, -0.17].map((y, i) => (
            <mesh key={i} position={[0, y, 0]}><planeGeometry args={[0.48, 0.04]} /><meshBasicMaterial color="#ffffff" transparent opacity={0.85} toneMapped={false} /></mesh>
          ))}
        </group>
        <mesh ref={progTrack} position={[0, 0.5 - 0.215, 0.03]} visible={false}><planeGeometry args={[0.48, 0.035]} /><meshBasicMaterial color="#5b6f87" transparent opacity={0.55} toneMapped={false} /></mesh>
        <mesh ref={progFill} position={[0, 0.5 - 0.215, 0.032]} visible={false}><planeGeometry args={[0.48, 0.035]} /><meshBasicMaterial color="#7cc8a6" toneMapped={false} /></mesh>
      </group>
      {/* teclado y ratón */}
      <RoundedBox args={[0.46, 0.02, 0.16]} radius={0.01} smoothness={2} castShadow position={[0, T + 0.01, 0.25]}><meshStandardMaterial color="#d9dde3" /></RoundedBox>
      <RoundedBox args={[0.07, 0.025, 0.11]} radius={0.012} smoothness={2} castShadow position={[0.4, T + 0.0125, 0.25]}><meshStandardMaterial color="#d9dde3" /></RoundedBox>
      <DeskProp role={agent.role} accent={meta.color} />
      </DeskSet>
      {/* silla de oficina (estilo elegido en el editor) */}
      <group position={[sx - dx, 0, sz - dz]}><ChairMesh item={chairItem} /></group>
      {editing && (
        <mesh position={[0, 0.7, 0.55]} onPointerDown={(ev) => { ev.stopPropagation(); const st = useOfficeLayout.getState(); if (st.placing) return; st.select({ kind: "desk", id }); st.setDragging(true); }}
          onPointerOver={() => { document.body.style.cursor = "grab"; }} onPointerOut={() => { document.body.style.cursor = ""; }}>
          <boxGeometry args={[2.4, 1.4, 2.2]} /><meshBasicMaterial transparent opacity={0} depthWrite={false} />
        </mesh>
      )}
      <FloorSign pos={[0, 0.03, -0.95]} text={meta.label} color={meta.color} />
    </group>
  );
}

/** Where a room is: moved by the organization (the dragged preview while it is being moved) or in its built-in place. */
function useZone(name: ZoneName): [number, number] {
  const z = useOfficeLayout((s) => effective(s).zones[name]);
  const g = useOfficeLayout((s) => (s.zoneGhost?.id === name ? s.zoneGhost : null));
  return g ? [g.x, g.z] : z ? [z.x, z.z] : ZONE_DEFAULTS[name];
}

/** In edit mode the whole room is one grab handle (its floor footprint, a little taller than the furniture). */
function ZoneHit({ name }: { name: ZoneName }) {
  const editing = useOfficeLayout((s) => s.editing);
  if (!editing) return null;
  const z = ZONE_SIZE[name];
  return (
    <mesh position={[z.dx, 0.5, z.dz]}
      onPointerDown={(ev) => { ev.stopPropagation(); const st = useOfficeLayout.getState(); if (st.placing) return; st.select({ kind: "zone", id: name }); st.setDragging(true); }}
      onPointerOver={() => { document.body.style.cursor = "grab"; }} onPointerOut={() => { document.body.style.cursor = ""; }}>
      <boxGeometry args={[z.w, 1, z.d]} /><meshBasicMaterial transparent opacity={0} depthWrite={false} />
    </mesh>
  );
}

function MeetingRoom() {
  const [cx, cz] = useZone("meeting");
  return (
    <group position={[cx, 0, cz]}>
      <ZoneHit name="meeting" />
      <RoundedBox args={[7.6, 0.02, 7.2]} radius={0.01} smoothness={2} position={[0, 0.011, 0]} receiveShadow><meshStandardMaterial color="#cfd6df" roughness={1} /></RoundedBox>
      <mesh rotation={[-Math.PI / 2, 0, 0]} position={[0, 0.024, 0]}><ringGeometry args={[3.0, 3.05, 64]} /><meshStandardMaterial color="#8d99ab" roughness={1} /></mesh>
      <mesh castShadow receiveShadow position={[0, 0.66, 0]}><cylinderGeometry args={[1.45, 1.45, 0.06, 48]} /><meshStandardMaterial color={OAK} roughness={0.8} /></mesh>
      <mesh castShadow position={[0, 0.33, 0]}><cylinderGeometry args={[0.14, 0.3, 0.64, 20]} /><meshStandardMaterial color={GRAPHITE} roughness={0.6} metalness={0.2} /></mesh>
      {Array.from({ length: 6 }).map((_, i) => {
        const a = (i / 6) * Math.PI * 2 + 0.3;
        return (
          <group key={i} position={[Math.sin(a) * 2.25, 0, Math.cos(a) * 2.25]}>
            <mesh castShadow position={[0, 0.2, 0]}><cylinderGeometry args={[0.3, 0.28, 0.38, 28]} /><meshStandardMaterial color={i % 2 ? GRAPHITE_L : GRAPHITE} roughness={0.9} /></mesh>
          </group>
        );
      })}
      {/* pantalla de la sala (en la pared del fondo) */}
      <RoundedBox args={[3.4, 1.6, 0.08]} radius={0.03} smoothness={3} position={[0, 1.7, -3.9]} castShadow><meshStandardMaterial color="#2c323b" roughness={0.5} /></RoundedBox>
      <mesh position={[0, 1.7, -3.855]}><planeGeometry args={[3.2, 1.4]} /><meshBasicMaterial color="#e9eef5" toneMapped={false} /></mesh>
      {[0, 1, 2, 3, 4].map((i) => (<mesh key={i} position={[-1.1 + i * 0.55, 1.35 + (0.2 + (i % 3) * 0.14) / 2 - 0.1, -3.85]}><planeGeometry args={[0.32, 0.2 + (i % 3) * 0.14]} /><meshBasicMaterial color={["#6f86c9", "#9fb0d8", "#6f86c9", "#b9c5e2", "#8196cf"][i]} toneMapped={false} /></mesh>))}
      <FloorSign pos={[0, 0.04, 3.1]} text="Sala de reuniones" color="#5a7fd0" />
    </group>
  );
}

function ApprovalsZone() {
  const pending = useStore((s) => Object.values(s.approvals).filter((a) => a.status === "pending").length);
  const pulse = useRef<THREE.Mesh>(null!);
  const stack = useRef<THREE.Group>(null!);
  useFrame(({ clock }) => {
    const t = clock.elapsedTime;
    const m = pulse.current.material as THREE.MeshBasicMaterial;
    const has = Object.values(useStore.getState().approvals).some((a) => a.status === "pending");
    const reduce = usePreferences.getState().prefs.reduceMotion;
    const k = reduce ? 0 : 1;
    m.opacity = has ? 0.5 + Math.abs(Math.sin(t * 2.2)) * 0.35 * k : 0.28;
    pulse.current.scale.setScalar(has ? 1 + Math.sin(t * 2.2) * 0.04 * k : 1);
    stack.current.position.y = DESK_H + 0.14 + (has ? Math.abs(Math.sin(t * 2.4)) * 0.05 * k : 0);
  });
  const [ux, uz] = useZone("approvals");
  return (
    <group position={[ux, 0, uz]}>
      <ZoneHit name="approvals" />
      <RoundedBox args={[5.4, 0.02, 4.6]} radius={0.01} smoothness={2} position={[0, 0.011, 0.5]} receiveShadow><meshStandardMaterial color="#d5dae1" roughness={1} /></RoundedBox>
      <mesh ref={pulse} rotation={[-Math.PI / 2, 0, 0]} position={[0, 0.03, 0.4]}><ringGeometry args={[2.4, 2.45, 64]} /><meshBasicMaterial color="#b98a34" transparent opacity={0.3} toneMapped={false} /></mesh>
      <RoundedBox args={[2.4, 0.06, 1.0]} radius={0.015} smoothness={3} castShadow receiveShadow position={[0, DESK_H - 0.03, 0.1]}><meshStandardMaterial color={OAK} roughness={0.8} /></RoundedBox>
      <RoundedBox args={[2.2, DESK_H - 0.06, 0.04]} radius={0.01} smoothness={2} castShadow position={[0, (DESK_H - 0.06) / 2, -0.28]}><meshStandardMaterial color={GRAPHITE_L} roughness={0.85} /></RoundedBox>
      {[-1.12, 1.12].map((s) => (
        <mesh key={s} castShadow position={[s, (DESK_H - 0.06) / 2, 0.1]}><boxGeometry args={[0.05, DESK_H - 0.06, 0.8]} /><meshStandardMaterial color={GRAPHITE} roughness={0.5} metalness={0.3} /></mesh>
      ))}
      <group ref={stack} position={[0, DESK_H + 0.14, 0.1]}>
        {Array.from({ length: Math.min(pending, 6) }).map((_, i) => (
          <RoundedBox key={i} args={[0.24, 0.02, 0.32]} radius={0.006} smoothness={2} castShadow position={[(i % 3 - 1) * 0.3, -0.12 + Math.floor(i / 3) * 0.025, 0]} rotation={[0, i * 0.4, 0]}>
            <meshStandardMaterial color="#f7f4ea" emissive="#c9993f" emissiveIntensity={0.18} />
          </RoundedBox>
        ))}
      </group>
      <RoundedBox args={[0.9, 0.02, 0.5]} radius={0.01} smoothness={2} position={[0, DESK_H + 0.01, 0.1]}><meshStandardMaterial color="#e3e7ec" /></RoundedBox>
      <mesh castShadow position={[0, 0.3, 1.5]}><cylinderGeometry args={[0.34, 0.3, 0.08, 28]} /><meshStandardMaterial color={GRAPHITE_L} roughness={0.9} /></mesh>
      <mesh position={[0, 0.14, 1.5]}><cylinderGeometry args={[0.035, 0.035, 0.22, 10]} /><meshStandardMaterial color={STEEL} metalness={0.4} roughness={0.5} /></mesh>
      <FloorSign pos={[0, 0.04, -1.1]} text="Zona de aprobaciones" color="#b98a34" sub={pending ? `${pending} pendiente${pending > 1 ? "s" : ""}` : "sin pendientes"} />
    </group>
  );
}

/** Small coffee corner where agents whose work is paused by a budget cap wait (see officeState.zoneFor). */
function Cafeteria() {
  const { t } = useT();
  const [cx, cz] = useZone("cafe");
  const led = useRef<THREE.MeshBasicMaterial>(null!);
  useFrame(({ clock }) => {
    const paused = Object.keys(useCost.getState().alerts).length > 0;
    const reduce = usePreferences.getState().prefs.reduceMotion;
    led.current.opacity = paused ? (reduce ? 1 : 0.55 + Math.abs(Math.sin(clock.elapsedTime * 2)) * 0.45) : 0.5;
  });
  return (
    <group position={[cx, 0, cz]}>
      <ZoneHit name="cafe" />
      <RoundedBox args={[3.9, 0.02, 4.4]} radius={0.01} smoothness={2} position={[0.1, 0.011, 0]} receiveShadow><meshStandardMaterial color="#e1d8c8" roughness={1} /></RoundedBox>
      {/* counter along the east edge */}
      <RoundedBox args={[0.7, 0.9, 3.8]} radius={0.03} smoothness={3} castShadow receiveShadow position={[1.75, 0.45, 0]}><meshStandardMaterial color={GRAPHITE_L} roughness={0.8} /></RoundedBox>
      <RoundedBox args={[0.78, 0.05, 3.88]} radius={0.015} smoothness={2} castShadow position={[1.75, 0.925, 0]}><meshStandardMaterial color={OAK} roughness={0.7} /></RoundedBox>
      {/* coffee machine */}
      <group position={[1.8, 0.95, -0.9]}>
        <RoundedBox args={[0.42, 0.55, 0.5]} radius={0.04} smoothness={3} castShadow position={[0, 0.275, 0]}><meshStandardMaterial color="#2c323b" roughness={0.5} metalness={0.3} /></RoundedBox>
        <mesh position={[-0.215, 0.38, 0]}><boxGeometry args={[0.012, 0.07, 0.07]} /><meshBasicMaterial ref={led} color="#e8943a" transparent toneMapped={false} /></mesh>
        <mesh position={[-0.215, 0.18, 0]}><boxGeometry args={[0.02, 0.1, 0.12]} /><meshStandardMaterial color={STEEL} metalness={0.5} roughness={0.4} /></mesh>
      </group>
      {/* mugs and a pot on the counter */}
      {[0.1, 0.4, 0.7].map((z, i) => (
        <mesh key={i} castShadow position={[1.7, 1.0, z]}><cylinderGeometry args={[0.05, 0.043, 0.1, 12]} /><meshStandardMaterial color={["#f4efe6", "#e0735a", "#5a95e8"][i]} roughness={0.6} /></mesh>
      ))}
      <mesh castShadow position={[1.8, 1.07, 1.3]}><cylinderGeometry args={[0.1, 0.12, 0.24, 14]} /><meshStandardMaterial color="#c9d3dc" roughness={0.35} metalness={0.5} /></mesh>
      {/* small high table */}
      <mesh castShadow position={[-0.6, 0.95, 0]}><cylinderGeometry args={[0.42, 0.42, 0.05, 28]} /><meshStandardMaterial color={OAK} roughness={0.7} /></mesh>
      <mesh castShadow position={[-0.6, 0.47, 0]}><cylinderGeometry args={[0.05, 0.14, 0.94, 14]} /><meshStandardMaterial color={GRAPHITE} roughness={0.6} metalness={0.2} /></mesh>
      <mesh castShadow position={[-0.5, 1.02, 0.1]}><cylinderGeometry args={[0.05, 0.043, 0.1, 12]} /><meshStandardMaterial color="#f4efe6" roughness={0.6} /></mesh>
      <Plant position={[-1.6, 0, 1.8]} s={0.8} tint="#8aa28c" />
      <FloorSign pos={[0.2, 0.04, 1.95]} text={t("office.cafeteria")} color="#c98a4a" />
    </group>
  );
}

/** Ventanal: marco fino grafito + vidrio claro; los montantes dividen el paño. */
function GlassBand({ length, horizontal = true }: { length: number; horizontal?: boolean }) {
  const n = Math.max(1, Math.round(length / 2.6));
  const pane = length / n;
  const H = 1.2;
  return (
    <group rotation={[0, horizontal ? 0 : Math.PI / 2, 0]}>
      <mesh position={[0, 0, 0]}><boxGeometry args={[length, H, 0.06]} /><meshStandardMaterial color={GLASS} emissive={GLASS} emissiveIntensity={0.18} roughness={0.2} /></mesh>
      {Array.from({ length: n + 1 }).map((_, i) => (
        <mesh key={i} position={[-length / 2 + i * pane, 0, 0.02]}><boxGeometry args={[0.05, H + 0.06, 0.08]} /><meshStandardMaterial color={GRAPHITE} roughness={0.5} metalness={0.3} /></mesh>
      ))}
      <mesh position={[0, H / 2, 0.02]}><boxGeometry args={[length, 0.05, 0.08]} /><meshStandardMaterial color={GRAPHITE} roughness={0.5} metalness={0.3} /></mesh>
      <mesh position={[0, -H / 2, 0.02]}><boxGeometry args={[length, 0.05, 0.08]} /><meshStandardMaterial color={GRAPHITE} roughness={0.5} metalness={0.3} /></mesh>
    </group>
  );
}

function Wall({ args, position, children }: { args: [number, number, number]; position: [number, number, number]; children?: React.ReactNode }) {
  const color = useOfficeLayout((s) => effective(s).walls.color);
  return (
    <group position={position}>
      <RoundedBox args={args} radius={0.03} smoothness={2} castShadow receiveShadow><meshStandardMaterial color={color} roughness={0.95} /></RoundedBox>
      {children}
    </group>
  );
}

/** Armario técnico (rack) discreto: caja grafito con frente de vidrio oscuro y pequeños indicadores. */
function Rack({ position, rotation = 0 }: { position: [number, number, number]; rotation?: number }) {
  return (
    <group position={position} rotation={[0, rotation, 0]}>
      <RoundedBox args={[0.9, 2.0, 0.8]} radius={0.02} smoothness={2} castShadow position={[0, 1.0, 0]}><meshStandardMaterial color="#3d4551" roughness={0.6} /></RoundedBox>
      <mesh position={[0, 1.0, 0.41]}><planeGeometry args={[0.78, 1.86]} /><meshStandardMaterial color="#1f242b" roughness={0.3} metalness={0.2} /></mesh>
      {Array.from({ length: 9 }).map((_, i) => (
        <group key={i} position={[0, 0.2 + i * 0.19, 0.415]}>
          <mesh><planeGeometry args={[0.62, 0.03]} /><meshBasicMaterial color="#47505d" /></mesh>
          <mesh position={[0.3, 0.06, 0]}><circleGeometry args={[0.013, 8]} /><meshBasicMaterial color={i % 4 === 1 ? "#8aa0c6" : "#7aa790"} toneMapped={false} /></mesh>
        </group>
      ))}
    </group>
  );
}

const WARM = new THREE.Color("#ffb878"), COOL = new THREE.Color("#b8d4ff");
/** Mixes a color toward warm (temp < 0) or cool (temp > 0): the lighting-temperature slider of the editor. */
function tint(out: THREE.Color, hex: string, temp: number) {
  out.set(hex);
  if (temp < 0) out.lerp(WARM, Math.min(1, -temp) * 0.8);
  else if (temp > 0) out.lerp(COOL, Math.min(1, temp) * 0.8);
  return out;
}

/** The mix the room should have now: the organization's mood (layout) tuned by the temperature and brightness bars. */
function useLightTarget(): LightMix & { temp: number } {
  const light = useOfficeLayout((s) => effective(s).light);
  return useMemo(() => {
    const base = MOOD_BASE[light.mood] ?? MOOD_BASE.natural;
    const k = light.brightness;
    return { ...base, amb: base.amb * k, hemi: base.hemi * k, sun: base.sun * k, temp: light.mood === "night" ? 0 : light.temp };
  }, [light.mood, light.brightness, light.temp]);
}

/** Scene lights, background and fog driven by the layout's light and the `lighting` preference, eased toward the target. */
function LightRig() {
  const target = useLightTarget();
  const amb = useRef<THREE.AmbientLight>(null);
  const hemi = useRef<THREE.HemisphereLight>(null);
  const sun = useRef<THREE.DirectionalLight>(null);
  const bg = useRef<THREE.Color>(null);
  const fog = useRef<THREE.Fog>(null);
  const tmp = useMemo(() => new THREE.Color(), []);
  useFrame((_, dt) => {
    const p = target;
    const reduce = usePreferences.getState().prefs.reduceMotion;
    const k = reduce ? 1 : 1 - Math.exp(-Math.min(dt, 0.1) * 4);
    if (amb.current) { amb.current.intensity += (p.amb - amb.current.intensity) * k; amb.current.color.lerp(tint(tmp, p.ambColor, p.temp), k); }
    if (hemi.current) {
      hemi.current.intensity += (p.hemi - hemi.current.intensity) * k;
      hemi.current.color.lerp(tint(tmp, p.hemiSky, p.temp), k);
      hemi.current.groundColor.lerp(tmp.set(p.hemiGround), k);
    }
    if (sun.current) {
      sun.current.intensity += (p.sun - sun.current.intensity) * k;
      sun.current.color.lerp(tint(tmp, p.sunColor, p.temp), k);
      sun.current.position.x += (p.sunPos[0] - sun.current.position.x) * k;
      sun.current.position.y += (p.sunPos[1] - sun.current.position.y) * k;
      sun.current.position.z += (p.sunPos[2] - sun.current.position.z) * k;
    }
    if (bg.current) bg.current.lerp(tmp.set(p.bg), k);
    if (fog.current) fog.current.color.lerp(tmp.set(p.bg), k);
  });
  const init = target;
  return (
    <>
      <color ref={bg} attach="background" args={[init.bg]} />
      <fog ref={fog} attach="fog" args={[init.bg, 40, 105]} />
      <ambientLight ref={amb} intensity={init.amb} color={init.ambColor} />
      <hemisphereLight ref={hemi} args={[init.hemiSky, init.hemiGround, init.hemi]} />
      <directionalLight
        ref={sun} castShadow position={init.sunPos} intensity={init.sun} color={init.sunColor}
        shadow-mapSize={[2048, 2048]} shadow-bias={-0.0005} shadow-radius={7}
        shadow-camera-left={-18} shadow-camera-right={18} shadow-camera-top={14} shadow-camera-bottom={-14} shadow-camera-near={1} shadow-camera-far={60}
      />
    </>
  );
}

/** The built-in plants and rack: they can be moved, turned or taken away in the office editor. */
function Fixtures() {
  const cfg = useOfficeLayout((s) => effective(s).fixtures);
  const editing = useOfficeLayout((s) => s.editing);
  return (
    <>
      {FIXTURE_IDS.map((id) => {
        const base = FIXTURES[id];
        const c = cfg[id];
        if (c?.hidden) return null;
        const x = c?.x ?? base.x, z = c?.z ?? base.z, rot = c?.rot ?? base.rot;
        return (
          <group key={id} position={[x, 0, z]} rotation={[0, rot, 0]}>
            {id === "rack" ? <Rack position={[0, 0, 0]} /> : <Plant position={[0, 0, 0]} s={base.s} tint={base.tint} />}
            {editing && (
              <mesh position={[0, 0.9, 0]}
                onPointerDown={(ev) => { ev.stopPropagation(); const st = useOfficeLayout.getState(); if (st.placing) return; st.select({ kind: "fixture", id: id as FixtureId }); st.setDragging(true); }}
                onPointerOver={() => { document.body.style.cursor = "grab"; }} onPointerOut={() => { document.body.style.cursor = ""; }}>
                <boxGeometry args={[base.w + 0.2, 1.8, base.d + 0.2]} /><meshBasicMaterial transparent opacity={0} depthWrite={false} />
              </mesh>
            )}
          </group>
        );
      })}
    </>
  );
}

export function OfficeEnv() {
  const order = useStore((s) => s.agentOrder);
  const concrete = useConcrete();
  const backdrop = useBackdrop();
  const layoutFloor = useOfficeLayout((s) => effective(s).floor);
  const walls = useOfficeLayout((s) => effective(s).walls);
  const floorMap = layoutFloor.kind === "concrete" ? concrete : floorTexture(layoutFloor.kind);
  const finish = FLOOR_FINISH[layoutFloor.kind];
  const skirt = useMemo(() => mix(walls.color, "#000000", 0.1), [walls.color]);
  return (
    <>
      <LightRig />

      {/* fondo: disco con degradado neutro (sin cielo) y losa de hormigón pulido */}
      <mesh rotation={[-Math.PI / 2, 0, 0]} position={[0, -0.42, 0]} receiveShadow><circleGeometry args={[95, 64]} /><meshStandardMaterial map={backdrop} roughness={1} /></mesh>
      <RoundedBox args={[29, 0.4, 21]} radius={0.04} smoothness={3} position={[0, -0.22, 0]} receiveShadow castShadow><meshStandardMaterial color="#8d97a6" roughness={0.9} /></RoundedBox>
      <RoundedBox args={[28.4, 0.1, 20.4]} radius={0.02} smoothness={2} position={[0, -0.03, 0]} receiveShadow><meshStandardMaterial map={floorMap} color={layoutFloor.tint} roughness={finish.rough} metalness={finish.metal} /></RoundedBox>

      {/* paredes bajas con ventanal y zócalo */}
      <Wall args={[28.4, 3, 0.3]} position={[0, 1.5, -10]}>
        {walls.windows && <group position={[-3.4, 0.4, 0.17]}><GlassBand length={20.6} /></group>}
        <mesh position={[0, -1.25, 0.16]}><boxGeometry args={[28.4, 0.5, 0.04]} /><meshStandardMaterial color={skirt} roughness={0.9} /></mesh>
        <mesh position={[0, 1.46, 0.16]}><boxGeometry args={[28.4, 0.08, 0.04]} /><meshStandardMaterial color={walls.trim} roughness={0.6} /></mesh>
      </Wall>
      <Wall args={[0.3, 3, 20.4]} position={[-14.2, 1.5, 0]}>
        <mesh position={[0.16, -1.25, 0]}><boxGeometry args={[0.04, 0.5, 20.4]} /><meshStandardMaterial color={skirt} roughness={0.9} /></mesh>
        <mesh position={[0.16, 1.46, 0]}><boxGeometry args={[0.04, 0.08, 20.4]} /><meshStandardMaterial color={walls.trim} roughness={0.6} /></mesh>
        {walls.windows && <group position={[0.17, 0.4, 0]}><GlassBand length={17} horizontal={false} /></group>}
      </Wall>

      <Fixtures />

      {order.map((id, i) => <Desk key={id} id={id} index={i} />)}
      <MeetingRoom />
      <ApprovalsZone />
      <Cafeteria />
    </>
  );
}
