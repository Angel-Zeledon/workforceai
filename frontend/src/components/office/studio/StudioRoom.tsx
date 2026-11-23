"use client";
// The room of the "Estudio" office: walls, floor, windows, the rooms' mats, workstations, the built-in furniture and the
// organization's own pieces, all read from the REAL layout. Furniture is Kenney's CC0 kit recolored to a sober palette
// where a model exists, and plain sober shapes where it does not.
import { useEffect, useMemo, useRef } from "react";
import { useFrame } from "@react-three/fiber";
import * as THREE from "three";
import { useStore } from "@/lib/store";
import { useRewards, type ShopItem } from "@/lib/rewards";
import {
  FIXTURES, FIXTURE_IDS, LEGACY_SLOTS, ZONE_NAMES, ZONE_SIZE, fixtureWorld, restOn, zoneCenterOf, type FixtureId, type Layout,
} from "@/lib/officeLayoutModel";
import { usePreferences } from "@/lib/preferences";
import { roleMeta } from "@/lib/meta";
import { pieceInfo } from "../pieceInfo";
import { seatOf } from "../registry";
import { disposeMaterial, type Assets, type FurnitureName } from "./studioAssets";
import { FLOOR_TILE } from "./studioLogic";

const K = 1.95; // Kenney units -> meters
export const FLOOR = { w: 28.4, d: 20.4 };
const OAK = "#cbb59a", GRAPHITE = "#3a414c", GRAPHITE_L = "#59616d", STEEL = "#b3bac4";

/** The sober palette laid over the toy colors of the Kenney kit (material name -> color). */
const RECOLOR: Record<string, { c: string; r?: number; m?: number }> = {
  wood: { c: OAK, r: 0.7 }, woodDark: { c: "#8f7a63", r: 0.7 }, metal: { c: "#c9ced6", r: 0.4, m: 0.3 }, metalMedium: { c: "#59616d", r: 0.5, m: 0.2 },
  metalDark: { c: "#2a2f36", r: 0.45, m: 0.2 }, carpet: { c: "#3b4a5c", r: 1 }, carpetDarker: { c: "#2c3846", r: 1 }, plant: { c: "#5d8a6a", r: 0.8 }, lamp: { c: "#fff4dc", r: 0.6 },
};

// ------------------------------------------------------------------------------------------------ small building blocks

/** A box (sizes in meters, `y` is its BOTTOM). */
function Box({ w, h, d, x = 0, y = 0, z = 0, ry = 0, color, rough = 0.7, metal = 0, opacity = 1, emissive, cast = true }: {
  w: number; h: number; d: number; x?: number; y?: number; z?: number; ry?: number; color: string; rough?: number; metal?: number; opacity?: number; emissive?: string; cast?: boolean;
}) {
  return (
    <mesh position={[x, y + h / 2, z]} rotation={[0, ry, 0]} castShadow={cast} receiveShadow>
      <boxGeometry args={[w, h, d]} />
      <meshStandardMaterial color={color} roughness={rough} metalness={metal} transparent={opacity < 1} opacity={opacity} emissive={emissive ?? "#000000"} emissiveIntensity={emissive ? 0.6 : 0} />
    </mesh>
  );
}
/** A cylinder (`y` is its bottom). */
function Cyl({ r, h, x = 0, y = 0, z = 0, color, rough = 0.7, metal = 0, r2 }: { r: number; h: number; x?: number; y?: number; z?: number; color: string; rough?: number; metal?: number; r2?: number }) {
  return (
    <mesh position={[x, y + h / 2, z]} castShadow receiveShadow>
      <cylinderGeometry args={[r, r2 ?? r, h, 24]} />
      <meshStandardMaterial color={color} roughness={rough} metalness={metal} />
    </mesh>
  );
}

/** A Kenney model with its materials recolored; `fit` stretches it to a footprint (width x depth) and a height. */
function prepare(src: THREE.Object3D, colors: Record<string, string> | undefined, emissive: string | undefined): { g: THREE.Group; size: THREE.Vector3; mats: THREE.Material[] } {
  const o = src.clone(true);
  const mats: THREE.Material[] = [];
  o.traverse((n) => {
    const m = n as THREE.Mesh;
    if (!m.isMesh) return;
    m.castShadow = true; m.receiveShadow = true;
    const list = (Array.isArray(m.material) ? m.material : [m.material]).map((mt) => {
      const c = (mt as THREE.MeshStandardMaterial).clone();
      const rc = RECOLOR[c.name];
      if (rc) { c.color.set(rc.c); c.roughness = rc.r ?? c.roughness; c.metalness = rc.m ?? 0; }
      const own = colors?.[c.name];
      if (own) c.color.set(own);
      if (emissive && c.name === "lamp") { c.emissive.set(emissive); c.emissiveIntensity = 0.9; }
      c.envMapIntensity = 0.7;
      mats.push(c);
      return c;
    });
    m.material = Array.isArray(m.material) ? list : list[0];
  });
  o.scale.setScalar(K);
  o.updateMatrixWorld(true);
  const box = new THREE.Box3().setFromObject(o);
  const c = box.getCenter(new THREE.Vector3());
  o.position.set(-c.x, -box.min.y, -c.z);
  const g = new THREE.Group();
  g.add(o);
  return { g, size: box.getSize(new THREE.Vector3()), mats };
}

export interface ModelProps {
  a: Assets; name: FurnitureName; x?: number; y?: number; z?: number; rot?: number;
  /** stretch to this footprint / height (meters); undefined keeps the model's own size */
  w?: number; d?: number; h?: number; s?: number; colors?: Record<string, string>; emissive?: string;
}
export function Model({ a, name, x = 0, y = 0, z = 0, rot = 0, w, d, h, s = 1, colors, emissive }: ModelProps) {
  const key = colors ? JSON.stringify(colors) : "";
  const made = useMemo(() => prepare(a.furniture[name], colors, emissive), // eslint-disable-line react-hooks/exhaustive-deps -- `key` stands for colors
    [a, name, key, emissive]);
  useEffect(() => () => { for (const m of made.mats) disposeMaterial(m); }, [made]);
  const sx = (w ? w / made.size.x : 1) * s, sz = (d ? d / made.size.z : 1) * s, sy = (h ? h / made.size.y : 1) * s;
  return <primitive object={made.g} position={[x, y, z]} rotation={[0, rot, 0]} scale={[sx, sy, sz]} />;
}

// ------------------------------------------------------------------------------------------------ floor and walls

const FLOOR_BASE: Record<string, { base: string; line: string }> = {
  concrete: { base: "#e3e7ed", line: "#d2d8e1" }, wood: { base: "#e2c9a4", line: "#cdb088" }, parquet: { base: "#d9b88c", line: "#c0a070" },
  carpet: { base: "#c3ccd9", line: "#b4bfce" }, tile: { base: "#f0f2f5", line: "#d5dae2" }, marble: { base: "#f1efec", line: "#d6d1ca" },
};
const FLOOR_FINISH: Record<string, { rough: number; metal: number }> = {
  concrete: { rough: 0.75, metal: 0.02 }, wood: { rough: 0.6, metal: 0 }, parquet: { rough: 0.55, metal: 0 }, carpet: { rough: 1, metal: 0 }, tile: { rough: 0.35, metal: 0.04 }, marble: { rough: 0.25, metal: 0.05 },
};

/** One tile of the floor drawn in a small canvas (256 px: the whole floor repeats it, so memory stays tiny). */
function floorTexture(kind: string): THREE.CanvasTexture {
  const S = 256;
  const c = document.createElement("canvas");
  c.width = c.height = S;
  const g = c.getContext("2d")!;
  const look = FLOOR_BASE[kind] ?? FLOOR_BASE.concrete;
  const tile = FLOOR_TILE[kind] ?? FLOOR_TILE.concrete;
  let seed = 7;
  const rnd = () => { seed = (seed * 16807) % 2147483647; return seed / 2147483647; };
  g.fillStyle = look.base; g.fillRect(0, 0, S, S);
  const planks = (x: number, y: number, w: number, h: number, vertical: boolean, n: number) => {
    for (let i = 0; i < n; i++) {
      g.fillStyle = `rgba(0,0,0,${0.02 + rnd() * 0.06})`;
      if (vertical) g.fillRect(x + (w / n) * i, y, w / n, h); else g.fillRect(x, y + (h / n) * i, w, h / n);
      g.strokeStyle = look.line; g.lineWidth = 1.2;
      g.beginPath();
      if (vertical) { g.moveTo(x + (w / n) * i, y); g.lineTo(x + (w / n) * i, y + h); } else { g.moveTo(x, y + (h / n) * i); g.lineTo(x + w, y + (h / n) * i); }
      g.stroke();
    }
  };
  switch (tile.pattern) {
    case "grid": g.strokeStyle = look.line; g.lineWidth = 2.5; g.strokeRect(0, 0, S, S); break;
    case "planks": planks(0, 0, S, S, false, 4); break;
    case "checker": planks(0, 0, S / 2, S / 2, false, 4); planks(S / 2, 0, S / 2, S / 2, true, 4); planks(0, S / 2, S / 2, S / 2, true, 4); planks(S / 2, S / 2, S / 2, S / 2, false, 4); break;
    case "veins":
      g.strokeStyle = look.line; g.lineWidth = 1.4;
      for (let i = 0; i < 9; i++) { g.globalAlpha = 0.25 + rnd() * 0.4; g.beginPath(); g.moveTo(rnd() * S, rnd() * S); g.bezierCurveTo(rnd() * S, rnd() * S, rnd() * S, rnd() * S, rnd() * S, rnd() * S); g.stroke(); }
      g.globalAlpha = 1; break;
    default: break;
  }
  for (let i = 0; i < 900; i++) { g.fillStyle = `rgba(${rnd() > 0.5 ? "255,255,255" : "0,0,0"},0.025)`; g.fillRect(rnd() * S, rnd() * S, 2, 2); }
  const t = new THREE.CanvasTexture(c);
  t.wrapS = t.wrapT = THREE.RepeatWrapping;
  t.repeat.set(FLOOR.w / tile.m, FLOOR.d / tile.m);
  t.colorSpace = THREE.SRGBColorSpace;
  t.anisotropy = 4;
  return t;
}

const GLASS = "#c9d8e6";
function GlassBand({ length, vertical = false }: { length: number; vertical?: boolean }) {
  const n = Math.max(1, Math.round(length / 2.6)), pane = length / n, H = 1.2;
  return (
    <group rotation={[0, vertical ? Math.PI / 2 : 0, 0]}>
      <mesh><boxGeometry args={[length, H, 0.05]} /><meshStandardMaterial color={GLASS} emissive={GLASS} emissiveIntensity={0.14} roughness={0.12} metalness={0.1} /></mesh>
      {Array.from({ length: n + 1 }).map((_, i) => <mesh key={i} position={[-length / 2 + i * pane, 0, 0.02]}><boxGeometry args={[0.05, H + 0.06, 0.07]} /><meshStandardMaterial color={GRAPHITE} roughness={0.5} metalness={0.3} /></mesh>)}
      <mesh position={[0, H / 2, 0.02]}><boxGeometry args={[length, 0.05, 0.07]} /><meshStandardMaterial color={GRAPHITE} roughness={0.5} metalness={0.3} /></mesh>
      <mesh position={[0, -H / 2, 0.02]}><boxGeometry args={[length, 0.05, 0.07]} /><meshStandardMaterial color={GRAPHITE} roughness={0.5} metalness={0.3} /></mesh>
    </group>
  );
}

export function RoomShell({ layout, ground }: { layout: Layout; ground: string }) {
  const { kind, tint } = layout.floor;
  const { color, trim, windows } = layout.walls;
  const tex = useMemo(() => floorTexture(kind), [kind]);
  useEffect(() => () => tex.dispose(), [tex]);
  const fin = FLOOR_FINISH[kind] ?? FLOOR_FINISH.concrete;
  const skirt = useMemo(() => "#" + new THREE.Color(color).lerp(new THREE.Color("#000000"), 0.1).getHexString(), [color]);
  const { w, d } = FLOOR;
  return (
    <group>
      {/* the ground around the office and the slab it stands on */}
      <mesh rotation={[-Math.PI / 2, 0, 0]} position={[0, -0.42, 0]} receiveShadow><circleGeometry args={[70, 48]} /><meshStandardMaterial color={ground} roughness={1} /></mesh>
      <mesh position={[0, -0.22, 0]} receiveShadow castShadow><boxGeometry args={[w + 0.6, 0.4, d + 0.6]} /><meshStandardMaterial color="#8d97a6" roughness={0.9} /></mesh>
      <mesh position={[0, -0.03, 0]} receiveShadow><boxGeometry args={[w, 0.1, d]} /><meshStandardMaterial map={tex} color={tint} roughness={fin.rough} metalness={fin.metal} /></mesh>
      {/* back wall (-z) and left wall (-x): low, with the window band and the skirting */}
      <mesh position={[0, 1.5, -d / 2]} castShadow receiveShadow><boxGeometry args={[w, 3, 0.3]} /><meshStandardMaterial color={color} roughness={0.95} /></mesh>
      <mesh position={[-w / 2, 1.5, 0]} castShadow receiveShadow><boxGeometry args={[0.3, 3, d]} /><meshStandardMaterial color={color} roughness={0.95} /></mesh>
      <mesh position={[0, 0.25, -d / 2 + 0.16]}><boxGeometry args={[w, 0.5, 0.04]} /><meshStandardMaterial color={skirt} roughness={0.9} /></mesh>
      <mesh position={[-w / 2 + 0.16, 0.25, 0]}><boxGeometry args={[0.04, 0.5, d]} /><meshStandardMaterial color={skirt} roughness={0.9} /></mesh>
      <mesh position={[0, 2.96, -d / 2 + 0.16]}><boxGeometry args={[w, 0.08, 0.04]} /><meshStandardMaterial color={trim} roughness={0.6} /></mesh>
      <mesh position={[-w / 2 + 0.16, 2.96, 0]}><boxGeometry args={[0.04, 0.08, d]} /><meshStandardMaterial color={trim} roughness={0.6} /></mesh>
      {windows && <group position={[-3.4, 1.9, -d / 2 + 0.17]}><GlassBand length={20.6} /></group>}
      {windows && <group position={[-w / 2 + 0.17, 1.9, 0]}><GlassBand length={17} vertical /></group>}
    </group>
  );
}

/** The rooms' mats and the workstations' mats: a slightly darker panel on the floor that marks each area. */
export function Mats({ layout, desks }: { layout: Layout; desks: { pos: [number, number] }[] }) {
  const tone: Record<string, string> = { meeting: "#cfd6df", approvals: "#d5dae1", cafe: "#e1d8c8" };
  return (
    <group>
      {ZONE_NAMES.map((n) => {
        const [cx, cz] = zoneCenterOf(layout, n), z = ZONE_SIZE[n];
        return <mesh key={n} position={[cx + z.dx, 0.012, cz + z.dz]} receiveShadow><boxGeometry args={[z.w - 0.2, 0.014, z.d - 0.2]} /><meshStandardMaterial color={tone[n]} roughness={1} /></mesh>;
      })}
      {desks.map((d, i) => <mesh key={i} position={[d.pos[0] + 0.1, 0.012, d.pos[1] + 0.95]} receiveShadow><boxGeometry args={[3.5, 0.014, 2.6]} /><meshStandardMaterial color="#c9d0da" roughness={1} /></mesh>)}
    </group>
  );
}

// ------------------------------------------------------------------------------------------------ workstations

/** The glow lines of a working screen, drawn once and shared by every desk. */
let barsTex: THREE.CanvasTexture | null = null;
function bars(): THREE.CanvasTexture {
  if (barsTex) return barsTex;
  const c = document.createElement("canvas");
  c.width = 128; c.height = 96;
  const g = c.getContext("2d")!;
  g.fillStyle = "#0A66C2"; g.fillRect(8, 8, 112, 8);
  for (let i = 0; i < 7; i++) { g.fillStyle = i % 3 === 2 ? "#9fb3c8" : "#ffffff"; g.fillRect(12, 24 + i * 9, 34 + ((i * 41) % 70), 4); }
  barsTex = new THREE.CanvasTexture(c);
  barsTex.colorSpace = THREE.SRGBColorSpace;
  return barsTex;
}
export function disposeShared() { barsTex?.dispose(); barsTex = null; }

export interface Desk { id: string; pos: [number, number]; deskItem?: ShopItem; chairItem?: ShopItem }

/** One workstation: desk, monitor that wakes with the work, keyboard, mouse and the chair facing the desk. */
function Workstation({ a, desk }: { a: Assets; desk: Desk }) {
  const [dx, dz] = desk.pos;
  const [sx, sz] = seatOf(desk.pos);
  const screen = useRef<THREE.MeshBasicMaterial>(null!);
  const lines = useRef<THREE.MeshBasicMaterial>(null!);
  const track = useRef<THREE.Mesh>(null!);
  const fill = useRef<THREE.Mesh>(null!);
  const accent = roleMeta(useStore.getState().agents[desk.id]?.role).color;
  useFrame(({ clock }) => {
    const S = useStore.getState();
    const ag = S.agents[desk.id];
    if (!ag || !screen.current) return;
    const st = ag.state, reduce = usePreferences.getState().prefs.reduceMotion;
    const on = st === "working" || st === "thinking" || st === "reviewing" || st === "waiting" || st === "talking";
    const assigned = st === "idle" && Object.values(S.tasks).some((tk) => tk.agent_id === desk.id && (tk.status === "pending" || tk.status === "running"));
    const lvl = st === "working" ? 1 : on ? 0.85 : assigned ? 0.7 : st === "awaiting_approval" ? 0.75 : st === "error" || st === "blocked" ? 0.8 : 0.35;
    screen.current.color.set(st === "error" || st === "blocked" ? "#e7a29c" : st === "awaiting_approval" ? "#e8cd93" : "#b9dcf5").multiplyScalar(lvl);
    lines.current.opacity = on || st === "error" ? (st === "working" && !reduce ? 0.7 + 0.3 * Math.sin(clock.elapsedTime * 3 + dx) : 0.85) : 0;
    const busy = !!ag.current_task_id && (st === "working" || st === "thinking" || st === "reviewing");
    track.current.visible = busy; fill.current.visible = busy;
    if (busy) { const f = Math.max(0.03, Math.min(1, (ag.progress || 0) / 100)); fill.current.scale.x = f; fill.current.position.x = -0.3 + (0.6 * f) / 2; }
  });
  const top = 0.74;
  return (
    <group>
      <Model a={a} name="desk" x={dx} z={dz} rot={Math.PI} w={2.2} d={0.95} colors={desk.deskItem?.color ? { wood: desk.deskItem.color } : undefined} />
      <Model a={a} name="computerScreen" x={dx} y={top} z={dz - 0.12} />
      <group position={[dx, top + 0.31, dz - 0.105]}>
        <mesh><planeGeometry args={[0.66, 0.44]} /><meshBasicMaterial ref={screen} color="#b9dcf5" toneMapped={false} /></mesh>
        <mesh position={[0, 0, 0.002]}><planeGeometry args={[0.62, 0.4]} /><meshBasicMaterial ref={lines} map={bars()} transparent opacity={0} toneMapped={false} depthWrite={false} /></mesh>
        <mesh ref={track} position={[0, -0.19, 0.004]} visible={false}><planeGeometry args={[0.6, 0.03]} /><meshBasicMaterial color="#5b6f87" transparent opacity={0.6} toneMapped={false} /></mesh>
        <mesh ref={fill} position={[0, -0.19, 0.005]} visible={false}><planeGeometry args={[0.6, 0.03]} /><meshBasicMaterial color="#16a34a" toneMapped={false} /></mesh>
      </group>
      <Model a={a} name="computerKeyboard" x={dx} y={top} z={dz + 0.2} />
      <Model a={a} name="computerMouse" x={dx + 0.5} y={top} z={dz + 0.2} />
      {/* the desk's edge in the role's color, a small tag of whose desk it is */}
      <Box w={2.0} h={0.012} d={0.03} x={dx} y={top - 0.002} z={dz + 0.46} color={accent} cast={false} rough={0.5} />
      <Model a={a} name="chairDesk" x={sx} z={sz + 0.05} rot={Math.PI} colors={desk.chairItem?.color ? { carpet: desk.chairItem.color } : undefined} />
    </group>
  );
}

export function Workstations({ a, desks }: { a: Assets; desks: Desk[] }) {
  return <>{desks.map((d) => <Workstation key={d.id} a={a} desk={d} />)}</>;
}

// ------------------------------------------------------------------------------------------------ built-in furniture

function Fixture({ a, id, layout, tableAt, pending }: { a: Assets; id: FixtureId; layout: Layout; tableAt: [number, number]; pending: number }) {
  const w = fixtureWorld(layout, id);
  if (!w) return null;
  const def = FIXTURES[id];
  const rest = def.level === "top" ? restOn(layout, id) : { on: null, y: 0 };
  const { x, z, rot } = w;
  const tint = def.tint;
  switch (id) {
    case "plant_a": case "plant_b": case "plant_c": case "plant_d": case "cafe_plant":
      return <Model a={a} name="pottedPlant" x={x} z={z} rot={rot} s={def.s * 1.15} colors={tint ? { plant: tint } : undefined} />;
    case "rack":
      return (
        <group position={[x, 0, z]} rotation={[0, rot, 0]}>
          <Box w={0.9} h={2} d={0.8} color="#3d4551" rough={0.5} metal={0.3} />
          <Box w={0.78} h={1.86} d={0.02} z={0.41} y={0.07} color="#1f242b" rough={0.3} />
          {Array.from({ length: 8 }).map((_, i) => <Box key={i} w={0.62} h={0.04} d={0.02} z={0.425} y={0.2 + i * 0.2} color="#47505d" cast={false} />)}
        </group>
      );
    case "meeting_table":
      return <Model a={a} name="tableRound" x={x} z={z} rot={rot} w={2.9} d={2.9} h={0.76} />;
    case "meeting_seat_1": case "meeting_seat_2": case "meeting_seat_3": case "meeting_seat_4": case "meeting_seat_5": case "meeting_seat_6":
      // the chair faces the table: its back is on the far side
      return <Model a={a} name="chair" x={x} z={z} rot={Math.atan2(tableAt[0] - x, tableAt[1] - z)} />;
    case "meeting_screen":
      return (
        <group position={[x, 0, z]} rotation={[0, rot, 0]}>
          <Box w={3.4} h={1.6} d={0.08} y={0.9} color="#2c323b" rough={0.4} />
          <Box w={3.2} h={1.4} d={0.01} y={1.0} z={0.045} color="#e9eef5" rough={0.3} cast={false} />
          {[0, 1, 2, 3, 4].map((i) => <Box key={i} w={0.32} h={0.2 + (i % 3) * 0.14} d={0.01} x={-1.1 + i * 0.55} y={1.35} z={0.052} color={i % 2 ? "#9fb0d8" : "#6f86c9"} cast={false} />)}
        </group>
      );
    case "approvals_desk":
      return (
        <group position={[x, 0, z]} rotation={[0, rot, 0]}>
          <Box w={0.05} h={0.66} d={0.8} x={-1.12} color={GRAPHITE} /> <Box w={0.05} h={0.66} d={0.8} x={1.12} color={GRAPHITE} />
          <Box w={2.2} h={0.66} d={0.04} z={-0.38} color={GRAPHITE_L} />
          <Box w={2.4} h={0.06} d={1.0} y={0.66} color="#d9c4a3" rough={0.6} />
          <Box w={0.9} h={0.02} d={0.5} y={0.72} color="#e3e7ec" cast={false} />
          {/* the pile of pending approvals: a sheet per approval, up to six, with the brand-blue folder on top */}
          {pending > 0 && (
            <group position={[-0.6, 0.72, -0.05]}>
              {Array.from({ length: Math.min(6, pending) }).map((_, i) => <Box key={i} w={0.3} h={0.014} d={0.4} y={i * 0.016} ry={((i * 37) % 11) * 0.02 - 0.1} color="#f8f9fb" rough={0.9} />)}
              <Box w={0.31} h={0.012} d={0.41} y={Math.min(6, pending) * 0.016} color="#0A66C2" rough={0.55} />
            </group>
          )}
        </group>
      );
    case "approvals_chair":
      return <Model a={a} name="chairDesk" x={x} z={z} rot={Math.PI + rot} />;
    case "cafe_counter":
      return (
        <group position={[x, 0, z]} rotation={[0, rot, 0]}>
          <Box w={0.7} h={0.9} d={3.8} color={GRAPHITE_L} rough={0.55} metal={0.15} />
          <Box w={0.78} h={0.05} d={3.88} y={0.9} color="#d9c4a3" rough={0.55} />
        </group>
      );
    case "cafe_machine": {
      return (
        <group position={[x, rest.y, z]} rotation={[0, rot, 0]}>
          <Box w={0.42} h={0.55} d={0.5} color="#2c323b" rough={0.4} metal={0.2} />
          <Box w={0.1} h={0.04} d={0.02} y={0.34} z={0.26} color="#e8943a" emissive="#e8943a" cast={false} />
        </group>
      );
    }
    case "cafe_mug_1": case "cafe_mug_2": case "cafe_mug_3": case "cafe_table_mug": {
      const c = { cafe_mug_1: "#f4efe6", cafe_mug_2: "#d8a090", cafe_mug_3: "#9bb8d9", cafe_table_mug: "#f4efe6" }[id];
      return <Cyl r={0.045} h={0.1} x={x} z={z} y={rest.y} color={c} rough={0.45} />;
    }
    case "cafe_pot": return <Cyl r={0.11} h={0.26} x={x} z={z} y={rest.y} color="#c9d3dc" metal={0.3} rough={0.4} />;
    case "cafe_table":
      return (
        <group position={[x, 0, z]}>
          <Cyl r={0.06} h={0.92} color={GRAPHITE} metal={0.3} /> <Cyl r={0.2} h={0.03} color={GRAPHITE} metal={0.3} />
          <Cyl r={0.42} h={0.05} y={0.92} color="#d9c4a3" rough={0.55} />
        </group>
      );
  }
  return null;
}

export function Fixtures({ a, layout }: { a: Assets; layout: Layout }) {
  const pending = useStore((s) => Object.values(s.approvals).filter((x) => x.status === "pending").length);
  const t = fixtureWorld(layout, "meeting_table");
  const tableAt: [number, number] = t ? [t.x, t.z] : zoneCenterOf(layout, "meeting");
  return <>{FIXTURE_IDS.map((id) => <Fixture key={id} a={a} id={id} layout={layout} tableAt={tableAt} pending={pending} />)}</>;
}

// ------------------------------------------------------------------------------------------------ the organization's pieces

const HEIGHT: Record<string, number> = { table: 0.75, sofa: 0.8, cabinet: 1.1, divider: 1.6, appliance: 0.9, aquarium: 0.7, arcade: 1.8, trophy: 0.9, shelf: 1.7, chair: 0.9 };

function PieceView({ a, item, x, z, rot, s, y }: { a: Assets; item: ShopItem; x: number; z: number; rot: number; s: number; y?: number }) {
  const info = pieceInfo(item);
  const c = item.color || "#c4aa84", c2 = item.color2 || "#8f9aab";
  const w = info.w * s, d = info.d * s;
  const wall = info.mount === "wall";
  const body = (() => {
    switch (item.category) {
      case "plant": return <Model a={a} name="pottedPlant" s={(item.id === "plant_palm" ? 1.5 : 1.1) * s} colors={{ plant: item.color || "#5d8a6a" }} />;
      case "lamp": return <Model a={a} name="lampRoundFloor" s={1.2 * s} emissive={item.color || "#fff1d6"} />;
      case "rug": return (<><Box w={6.4 * s} h={0.016} d={2.6 * s} y={0.008} color={c} rough={1} cast={false} /><Box w={5.9 * s} h={0.004} d={2.1 * s} y={0.022} color={c2} rough={1} cast={false} /></>);
      case "coffee": return <Cyl r={0.07} h={0.11} color="#f4efe6" rough={0.45} />;
      case "poster": return <Box w={1.1 * s} h={0.8 * s} d={0.03} y={y || 1.3} color={c} rough={0.8} />;
      case "shelf": return <Model a={a} name="bookcaseOpen" w={2 * s} d={0.5 * s} h={1.7 * s} />;
      case "aquarium": return (<><Box w={w} h={0.7} d={0.6 * s} color={GRAPHITE_L} /><Box w={w - 0.1} h={0.6} d={0.55 * s} y={0.62} color="#9fd3e6" opacity={0.6} rough={0.1} /></>);
      case "arcade": return (<><Box w={0.95} h={1.8} d={0.95} color={c} rough={0.5} /><Box w={0.64} h={0.5} d={0.02} y={1.1} z={0.48} color="#0f172a" cast={false} /></>);
      case "trophy": return (<><Box w={1.6} h={0.9} d={0.55} color="#cfc3ad" />{[-1, 0, 1].map((i) => <Cyl key={i} r={0.07} h={0.22} x={i * 0.45} y={0.9} color="#d8b24a" metal={0.6} rough={0.3} />)}</>);
      case "window": return null;
      case "table": return (<><Box w={w} h={0.05} d={d} y={0.7} color={c} rough={0.55} />{[-1, 1].flatMap((sx) => [-1, 1].map((sz) => <Box key={`${sx}${sz}`} w={0.05} h={0.7} d={0.05} x={sx * (w / 2 - 0.08)} z={sz * (d / 2 - 0.08)} color={c2} metal={0.3} />))}</>);
      case "sofa": return <Model a={a} name="loungeSofa" w={w} d={d} colors={{ carpet: c, wood: c2 }} />;
      case "cabinet": return <Box w={w} h={(HEIGHT.cabinet) * s} d={d} color={c} rough={0.6} />;
      case "divider": return <Box w={w} h={1.6} d={Math.max(0.06, d)} color={c} rough={0.8} />;
      case "whiteboard": return (<><Box w={w} h={1.0} d={0.04} y={y || 1.2} color="#f8f9fb" rough={0.3} /><Box w={w + 0.06} h={0.03} d={0.05} y={(y || 1.2) - 0.015} color={STEEL} metal={0.3} cast={false} /></>);
      case "desk": return <Model a={a} name="desk" rot={Math.PI} w={w} d={d} colors={{ wood: c }} />;
      case "chair": return <Model a={a} name="chairDesk" rot={Math.PI} colors={{ carpet: c }} />;
      case "light": return info.mount === "floor" ? <Model a={a} name="lampRoundFloor" s={1.2} emissive={c} /> : <Box w={Math.max(0.3, w)} h={0.12} d={Math.max(0.3, d)} y={info.mount === "ceiling" ? 2.7 : (y || 1.8)} color={c} emissive={c} cast={false} />;
      default: return <Box w={w} h={(HEIGHT[item.category] ?? 0.8) * s} d={d} color={c} rough={0.7} />;
    }
  })();
  void wall;
  return <group position={[x, wall || item.category === "poster" ? 0 : (y ?? 0), z]} rotation={[0, rot, 0]}>{body}</group>;
}

export function Pieces({ a, layout }: { a: Assets; layout: Layout }) {
  const catalog = useRewards((s) => s.catalog);
  const office = useRewards((s) => s.office);
  const items = useMemo(() => {
    const byId = new Map(catalog.map((i) => [i.id, i] as const));
    const out: { key: string; item: ShopItem; x: number; z: number; rot: number; s: number; y?: number }[] = [];
    if (layout.seeded) {
      for (const p of layout.pieces) { const item = byId.get(p.item); if (item) out.push({ key: p.uid, item, x: p.x, z: p.z, rot: p.rot, s: p.s, y: p.y }); }
    } else {
      for (const [slot, itemId] of Object.entries(office)) {
        const spot = LEGACY_SLOTS[slot], item = byId.get(itemId);
        if (spot && item) out.push({ key: slot, item, x: spot.pos[0], z: spot.pos[2], rot: spot.rot ?? 0, s: 1, y: spot.pos[1] });
      }
    }
    return out;
  }, [catalog, office, layout.pieces, layout.seeded]);
  return <>{items.map((p) => <PieceView key={p.key} a={a} item={p.item} x={p.x} z={p.z} rot={p.rot} s={p.s} y={p.y} />)}</>;
}

