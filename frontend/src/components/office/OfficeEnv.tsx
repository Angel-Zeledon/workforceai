"use client";
import { useEffect, useMemo, useRef, useState } from "react";
import { useFrame } from "@react-three/fiber";
import { RoundedBox } from "@react-three/drei";
import * as THREE from "three";
import { useStore } from "@/lib/store";
import { usePreferences } from "@/lib/preferences";
import { roleMeta, MEETING_CENTER, USER_POS } from "@/lib/meta";
import { deskOf, seatOf } from "./registry";

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
  const [dx, dz] = deskOf({ id, role: agent?.role || "" }, index);
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
      {/* escritorio: tablero de madera clara mate, canto fino, patas de acero grafito */}
      <RoundedBox args={[2.2, 0.06, 0.95]} radius={0.015} smoothness={3} castShadow receiveShadow position={[0, T - 0.03, 0]}><meshStandardMaterial color={OAK} roughness={0.8} /></RoundedBox>
      <mesh position={[0, T - 0.075, 0.44]}><boxGeometry args={[2.18, 0.03, 0.03]} /><meshStandardMaterial color={meta.color} roughness={0.6} /></mesh>
      <mesh castShadow position={[0, T - 0.32, -0.4]}><boxGeometry args={[2.0, 0.5, 0.025]} /><meshStandardMaterial color={GRAPHITE_L} roughness={0.85} /></mesh>
      {[-1.02, 1.02].map((s) => (
        <group key={s}>
          <mesh castShadow position={[s, (T - 0.06) / 2, 0.33]}><boxGeometry args={[0.05, T - 0.06, 0.05]} /><meshStandardMaterial color={GRAPHITE} roughness={0.5} metalness={0.3} /></mesh>
          <mesh castShadow position={[s, (T - 0.06) / 2, -0.33]}><boxGeometry args={[0.05, T - 0.06, 0.05]} /><meshStandardMaterial color={GRAPHITE} roughness={0.5} metalness={0.3} /></mesh>
        </group>
      ))}
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
      {/* silla de oficina: asiento y respaldo grafito, base de acero */}
      <group position={[sx - dx, 0, sz - dz]}>
        <mesh castShadow position={[0, 0.3, 0]}><cylinderGeometry args={[0.31, 0.3, 0.08, 28]} /><meshStandardMaterial color={GRAPHITE_L} roughness={0.9} /></mesh>
        <RoundedBox args={[0.54, 0.5, 0.07]} radius={0.03} smoothness={3} castShadow position={[0, 0.65, 0.33]}><meshStandardMaterial color={GRAPHITE} roughness={0.9} /></RoundedBox>
        <mesh position={[0, 0.14, 0]}><cylinderGeometry args={[0.035, 0.035, 0.22, 10]} /><meshStandardMaterial color={STEEL} metalness={0.4} roughness={0.5} /></mesh>
        <mesh position={[0, 0.03, 0]}><cylinderGeometry args={[0.26, 0.28, 0.035, 5]} /><meshStandardMaterial color={GRAPHITE} metalness={0.3} roughness={0.6} /></mesh>
      </group>
      <FloorSign pos={[0, 0.03, -0.95]} text={meta.label} color={meta.color} />
    </group>
  );
}

function MeetingRoom() {
  const [cx, cz] = MEETING_CENTER;
  return (
    <group position={[cx, 0, cz]}>
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
  const [ux, , uz] = USER_POS;
  return (
    <group position={[ux, 0, uz]}>
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

/** Ventanal: marco fino grafito + vidrio claro; los montantes dividen el paño. */
function GlassBand({ length, horizontal = true }: { length: number; horizontal?: boolean }) {
  const n = Math.max(1, Math.round(length / 2.6));
  const pane = length / n;
  const H = 1.55;
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
  return (
    <group position={position}>
      <RoundedBox args={args} radius={0.03} smoothness={2} castShadow receiveShadow><meshStandardMaterial color={WALL} roughness={0.95} /></RoundedBox>
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

interface LightPreset { bg: string; fog: string; amb: number; ambColor: string; hemi: number; hemiSky: string; hemiGround: string; sun: number; sunColor: string }
// "ambient" is the base look (soft neutral daylight); "day" (lights on) is brighter and flatter for UI readability; "night" is dim and cool slate.
const LIGHT_PRESETS: Record<"ambient" | "day" | "night", LightPreset> = {
  ambient: { bg: "#8d97a7", fog: "#8d97a7", amb: 0.95, ambColor: "#f5f7fb", hemi: 0.85, hemiSky: "#f3f6fb", hemiGround: "#c6ccd5", sun: 1.55, sunColor: "#fff8ee" },
  day: { bg: "#9ca6b5", fog: "#9ca6b5", amb: 1.45, ambColor: "#ffffff", hemi: 1.15, hemiSky: "#ffffff", hemiGround: "#d6dbe2", sun: 1.9, sunColor: "#ffffff" },
  night: { bg: "#151a24", fog: "#151a24", amb: 0.5, ambColor: "#b3bdd0", hemi: 0.5, hemiSky: "#9aa7c2", hemiGround: "#2a3140", sun: 0.55, sunColor: "#b9c4de" },
};

/** Scene lights, background and fog driven by the `lighting` preference, eased toward the target preset. */
function LightRig() {
  const mode = usePreferences((s) => s.prefs.lighting);
  const amb = useRef<THREE.AmbientLight>(null);
  const hemi = useRef<THREE.HemisphereLight>(null);
  const sun = useRef<THREE.DirectionalLight>(null);
  const bg = useRef<THREE.Color>(null);
  const fog = useRef<THREE.Fog>(null);
  const tmp = useMemo(() => new THREE.Color(), []);
  useFrame((_, dt) => {
    const p = LIGHT_PRESETS[mode] ?? LIGHT_PRESETS.ambient;
    const reduce = usePreferences.getState().prefs.reduceMotion;
    const k = reduce ? 1 : 1 - Math.exp(-Math.min(dt, 0.1) * 4);
    if (amb.current) { amb.current.intensity += (p.amb - amb.current.intensity) * k; amb.current.color.lerp(tmp.set(p.ambColor), k); }
    if (hemi.current) {
      hemi.current.intensity += (p.hemi - hemi.current.intensity) * k;
      hemi.current.color.lerp(tmp.set(p.hemiSky), k);
      hemi.current.groundColor.lerp(tmp.set(p.hemiGround), k);
    }
    if (sun.current) { sun.current.intensity += (p.sun - sun.current.intensity) * k; sun.current.color.lerp(tmp.set(p.sunColor), k); }
    if (bg.current) bg.current.lerp(tmp.set(p.bg), k);
    if (fog.current) fog.current.color.lerp(tmp.set(p.fog), k);
  });
  const init = LIGHT_PRESETS[mode] ?? LIGHT_PRESETS.ambient;
  return (
    <>
      <color ref={bg} attach="background" args={[init.bg]} />
      <fog ref={fog} attach="fog" args={[init.fog, 40, 105]} />
      <ambientLight ref={amb} intensity={init.amb} color={init.ambColor} />
      <hemisphereLight ref={hemi} args={[init.hemiSky, init.hemiGround, init.hemi]} />
      <directionalLight
        ref={sun} castShadow position={[10, 18, 12]} intensity={init.sun} color={init.sunColor}
        shadow-mapSize={[2048, 2048]} shadow-bias={-0.0005} shadow-radius={7}
        shadow-camera-left={-18} shadow-camera-right={18} shadow-camera-top={14} shadow-camera-bottom={-14} shadow-camera-near={1} shadow-camera-far={60}
      />
    </>
  );
}

export function OfficeEnv() {
  const order = useStore((s) => s.agentOrder);
  const concrete = useConcrete();
  const backdrop = useBackdrop();
  return (
    <>
      <LightRig />

      {/* fondo: disco con degradado neutro (sin cielo) y losa de hormigón pulido */}
      <mesh rotation={[-Math.PI / 2, 0, 0]} position={[0, -0.42, 0]} receiveShadow><circleGeometry args={[95, 64]} /><meshStandardMaterial map={backdrop} roughness={1} /></mesh>
      <RoundedBox args={[29, 0.4, 21]} radius={0.04} smoothness={3} position={[0, -0.22, 0]} receiveShadow castShadow><meshStandardMaterial color="#8d97a6" roughness={0.9} /></RoundedBox>
      <RoundedBox args={[28.4, 0.1, 20.4]} radius={0.02} smoothness={2} position={[0, -0.03, 0]} receiveShadow><meshStandardMaterial map={concrete} roughness={0.78} /></RoundedBox>

      {/* paredes bajas con ventanal y zócalo */}
      <Wall args={[28.4, 3, 0.3]} position={[0, 1.5, -10]}>
        <group position={[-3.4, 0.35, 0.17]}><GlassBand length={20.6} /></group>
        <mesh position={[0, -1.25, 0.16]}><boxGeometry args={[28.4, 0.5, 0.04]} /><meshStandardMaterial color={SKIRT} roughness={0.9} /></mesh>
        <mesh position={[0, 1.46, 0.16]}><boxGeometry args={[28.4, 0.08, 0.04]} /><meshStandardMaterial color={GRAPHITE} roughness={0.6} /></mesh>
      </Wall>
      <Wall args={[0.3, 3, 20.4]} position={[-14.2, 1.5, 0]}>
        <mesh position={[0.16, -1.25, 0]}><boxGeometry args={[0.04, 0.5, 20.4]} /><meshStandardMaterial color={SKIRT} roughness={0.9} /></mesh>
        <mesh position={[0.16, 1.46, 0]}><boxGeometry args={[0.04, 0.08, 20.4]} /><meshStandardMaterial color={GRAPHITE} roughness={0.6} /></mesh>
        <group position={[0.17, 0.35, 0]}><GlassBand length={17} horizontal={false} /></group>
      </Wall>

      <Plant position={[-12.8, 0, -8.6]} s={1.5} />
      <Plant position={[-12.8, 0, 8.4]} tint="#8aa28c" s={1.1} />
      <Plant position={[4.4, 0, -8.8]} s={1.2} tint="#8ba08d" />
      <Plant position={[12.8, 0, 8.6]} s={1.3} tint="#7a9680" />
      <Rack position={[-12.9, 0, -0.5]} rotation={Math.PI / 2} />

      {order.map((id, i) => <Desk key={id} id={id} index={i} />)}
      <MeetingRoom />
      <ApprovalsZone />
    </>
  );
}
