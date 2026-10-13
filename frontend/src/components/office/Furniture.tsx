"use client";
import * as THREE from "three";
import { RoundedBox } from "@react-three/drei";
import type { ShopItem } from "@/lib/rewards";
import {
  CEIL, DESK_DEFAULT, applSpec, cabSpec, chairSpec, deskSpec, divSpec, lightSpec, sofaSpec, tableSpec, tone, wbSpec,
  type ChairSpec, type DeskSpec, type Finish,
} from "./furnitureStyle";
import { darken, hash, lighten, mix } from "./itemStyle";

/**
 * Office furniture from the shop (docs/architecture/office-editor.md): parametric low-poly builders driven by the specs in
 * furnitureStyle.ts, so every id has its own look. Static geometry (nothing is allocated per frame); real lights are NOT
 * created here, the editor spawns them from `lightOf`. Floor pieces have their origin at the floor-center of the
 * footprint and face +z; wall pieces start at y = 0 with their back plane at z = -d/2; ceiling pieces hang from CEIL.
 */
type V3 = [number, number, number];
const GRAPHITE = "#3a414c";
const GRAPHITE_L = "#59616d";
const STEEL = "#b3bac4";
const BOOKS = ["#d9534f", "#5a95e8", "#f2c94c", "#4cb987", "#8f78c9", "#f4895a", "#45b8cc", "#ee7aa8"];

const FIN: Record<Finish, Partial<THREE.MeshStandardMaterialParameters>> = {
  wood: { roughness: 0.8 }, lacquer: { roughness: 0.2 }, metal: { roughness: 0.35, metalness: 0.6 }, glass: { roughness: 0.08, transparent: true, opacity: 0.32 },
  stone: { roughness: 0.25 }, fabric: { roughness: 1 }, leather: { roughness: 0.42 }, plastic: { roughness: 0.5 }, felt: { roughness: 1 },
  frame: { roughness: 0.5, metalness: 0.3 }, board: { roughness: 0.85 },
};
const M = (color: string, fin: Finish = "plastic", extra: Partial<THREE.MeshStandardMaterialParameters> = {}) => <meshStandardMaterial color={color} {...FIN[fin]} {...extra} />;
const MB = (color: string, extra: Partial<THREE.MeshBasicMaterialParameters> = {}) => <meshBasicMaterial color={color} toneMapped={false} {...extra} />;

interface Prim { p?: V3; r?: V3; c: string; f?: Finish; x?: Partial<THREE.MeshStandardMaterialParameters>; s?: boolean; basic?: boolean; sc?: V3 }
const mat = (q: Prim) => (q.basic ? MB(q.c, q.x as Partial<THREE.MeshBasicMaterialParameters>) : M(q.c, q.f, q.x));
const shadow = (q: Prim) => (q.s ?? q.f !== "glass");
/** Box of size `a`. With `rad`, a rounded box. */
function Bx(q: Prim & { a: V3; rad?: number }) {
  return q.rad
    ? <RoundedBox args={q.a} radius={Math.min(q.rad, Math.min(...q.a) / 2.2)} smoothness={2} castShadow={shadow(q)} position={q.p} rotation={q.r}>{mat(q)}</RoundedBox>
    : <mesh castShadow={shadow(q)} position={q.p} rotation={q.r}><boxGeometry args={q.a} />{mat(q)}</mesh>;
}
/** Cylinder / cone frustum: top radius, bottom radius, height. */
function Cy(q: Prim & { t: number; b?: number; h: number; n?: number; open?: boolean }) {
  return (
    <mesh castShadow={shadow(q)} position={q.p} rotation={q.r}>
      <cylinderGeometry args={[q.t, q.b ?? q.t, q.h, q.n ?? 18, 1, q.open]} />
      {q.open ? <meshStandardMaterial color={q.c} {...FIN[q.f ?? "plastic"]} {...q.x} side={THREE.DoubleSide} /> : mat(q)}
    </mesh>
  );
}
function Sp(q: Prim & { rad: number; n?: number }) {
  return <mesh castShadow={shadow(q)} position={q.p} rotation={q.r} scale={q.sc}><sphereGeometry args={[q.rad, q.n ?? 14, 10]} />{mat(q)}</mesh>;
}
/** A thin bar between two points in the XY plane (neon tubes, wires, trellis). */
function Seg({ a, b, z = 0, w = 0.02, c, basic = true, f }: { a: [number, number]; b: [number, number]; z?: number; w?: number; c: string; basic?: boolean; f?: Finish }) {
  const dx = b[0] - a[0], dy = b[1] - a[1];
  return <Bx p={[(a[0] + b[0]) / 2, (a[1] + b[1]) / 2, z]} r={[0, 0, Math.atan2(dy, dx)]} a={[Math.hypot(dx, dy), w, w]} c={c} basic={basic} f={f} s={false} />;
}
const STAR5 = [0, 1, 2, 3, 4].map((i) => (i / 5) * Math.PI * 2);

/** Deterministic pseudo-random list in 0..1 (stable across renders). */
function rnd(seed: string, n: number): number[] {
  let h = hash(seed);
  return Array.from({ length: n }, () => { h = Math.imul(h ^ (h >>> 15), 2246822507) >>> 0; h = Math.imul(h ^ (h >>> 13), 3266489909) >>> 0; return ((h ^ (h >>> 16)) >>> 0) / 4294967296; });
}

// ================================================================ desks

function XFrame({ x, H, dz, t, c, f }: { x: number; H: number; dz: number; t: number; c: string; f: Finish }) {
  const ang = Math.atan2(dz, H), len = Math.hypot(H, dz);
  return (
    <group position={[x, H / 2, 0]}>
      <Bx r={[ang, 0, 0]} a={[t, len, t * 0.8]} c={c} f={f} />
      <Bx r={[-ang, 0, 0]} a={[t, len, t * 0.8]} c={c} f={f} />
    </group>
  );
}

function DeskLegs({ sp, leg, H }: { sp: DeskSpec; leg: string; H: number }) {
  const f = sp.legFin;
  switch (sp.legs) {
    case "post":
      return <>{[-1.02, 1.02].map((s) => [0.33, -0.33].map((z) => <Bx key={`${s}${z}`} p={[s, H / 2, z]} a={[0.05, H, 0.05]} c={leg} f={f} />))}</>;
    case "splay":
      return <>{[-1, 1].map((sx) => [-1, 1].map((sz) => <Cy key={`${sx}${sz}`} p={[sx * 0.95, H / 2, sz * 0.3]} r={[-sz * 0.1, 0, sx * 0.07]} t={0.032} b={0.022} h={H + 0.02} n={10} c={leg} f={f} />))}</>;
    case "panel":
      return <>{[-1, 1].map((s) => <Bx key={s} p={[s * 1.05, H / 2, 0]} a={[0.04, H, 0.85]} c={leg} f={f} />)}</>;
    case "sled":
      return (
        <>
          {[-1, 1].map((s) => (
            <group key={s} position={[s * 1.0, 0, 0]}>
              {[-0.38, 0.38].map((z) => <Bx key={z} p={[0, H / 2, z]} a={[0.03, H, 0.03]} c={leg} f={f} />)}
              <Bx p={[0, 0.015, 0]} a={[0.03, 0.03, 0.82]} c={leg} f={f} />
              <Bx p={[0, H - 0.02, 0]} a={[0.03, 0.03, 0.8]} c={leg} f={f} />
            </group>
          ))}
        </>
      );
    case "hairpin": {
      const th = Math.atan2(0.34, H), L = Math.hypot(H, 0.34);
      return (
        <>
          {[-1, 1].map((sx) => (
            <group key={sx} position={[sx * 0.92, 0, 0]}>
              {[-1, 1].map((sz) => <Cy key={sz} p={[0, H / 2, sz * 0.17]} r={[-sz * th, 0, 0]} t={0.011} h={L} n={6} c={leg} f={f} />)}
              <Bx p={[0, H - 0.005, 0]} a={[0.05, 0.01, 0.5]} c={leg} f={f} />
            </group>
          ))}
          <Bx p={[0, 0.2, -0.17]} a={[1.84, 0.012, 0.012]} c={leg} f={f} />
        </>
      );
    }
    case "pipe":
      return (
        <>
          {[-1, 1].map((sx) => [-1, 1].map((sz) => (
            <group key={`${sx}${sz}`} position={[sx * 0.98, 0, sz * 0.34]}>
              <Cy p={[0, H / 2, 0]} t={0.03} h={H} n={12} c={leg} f={f} />
              <Cy p={[0, 0.006, 0]} t={0.065} h={0.012} n={12} c={leg} f={f} />
              <Sp p={[0, 0.16, 0]} rad={0.036} c={leg} f={f} />
            </group>
          )))}
          {[-1, 1].map((sx) => <Cy key={sx} p={[sx * 0.98, 0.16, 0]} r={[Math.PI / 2, 0, 0]} t={0.022} h={0.68} n={10} c={leg} f={f} />)}
          <Cy p={[0, 0.16, -0.34]} r={[0, 0, Math.PI / 2]} t={0.022} h={1.96} n={10} c={leg} f={f} />
        </>
      );
    case "x":
      return (
        <>
          {[-1, 1].map((s) => <XFrame key={s} x={s * 0.98} H={H} dz={0.7} t={0.05} c={leg} f={f} />)}
          <Cy p={[0, H / 2, 0]} r={[0, 0, Math.PI / 2]} t={0.018} h={1.96} n={8} c={leg} f={f} />
        </>
      );
    case "slab":
      return <>{[-1, 1].map((s) => <Bx key={s} p={[s * 0.98, H / 2, 0]} a={[0.1, H, 0.8]} c={leg} f={f} rad={0.01} />)}</>;
    case "lift":
      return (
        <>
          {[-1, 1].map((s) => (
            <group key={s} position={[s * 0.85, 0, 0]}>
              <Bx p={[0, 0.03, 0]} a={[0.08, 0.06, 0.85]} c={leg} f={f} />
              <Bx p={[0, H * 0.27, 0]} a={[0.1, H * 0.54, 0.1]} c={leg} f={f} />
              <Bx p={[0, H * 0.7, 0]} a={[0.07, H * 0.46, 0.07]} c={lighten(leg, 0.25)} f={f} />
            </group>
          ))}
          <Bx p={[0, H - 0.04, 0]} a={[1.7, 0.05, 0.08]} c={leg} f={f} />
        </>
      );
    case "pedestal":
      return (
        <>
          <Bx p={[0.74, H / 2, 0]} a={[0.55, H, 0.85]} c={leg} f={f} />
          {[0, 1, 2].map((i) => (
            <group key={i} position={[0.74, H - 0.13 - i * 0.17, 0.43]}>
              <Bx a={[0.5, 0.14, 0.012]} c={lighten(leg, 0.08)} f={f} />
              <Bx p={[0, 0, 0.012]} a={[0.16, 0.014, 0.014]} c="#c9a24a" f="metal" />
            </group>
          ))}
          <Bx p={[-1.03, H / 2, 0]} a={[0.05, H, 0.85]} c={leg} f={f} />
        </>
      );
  }
}

/**
 * Desk top + legs + modesty panel. `item` undefined reproduces the original oak desk exactly (the monitor, keyboard and
 * props are passed as `children`, positioned for a 0.72 m top; taller or shorter desks lift them accordingly).
 */
export function DeskSet({ item, accent, children }: { item?: ShopItem; accent: string; children?: React.ReactNode }) {
  const sp = item ? deskSpec(item) : DESK_DEFAULT;
  const top = tone(item, sp.top), leg = tone(item, sp.leg), T = sp.h, th = sp.thick, H = T - th;
  const panel = sp.panel ? tone(item, sp.panel) : null;
  const rim = sp.rim ? tone(item, sp.rim, "#c9a24a") : null;
  const veins = sp.veins ? rnd(item?.id ?? "v", 6) : null;
  return (
    <group>
      <RoundedBox args={[2.2, th, 0.95]} radius={Math.min(0.015, th / 3)} smoothness={3} castShadow receiveShadow position={[0, T - th / 2, 0]}>{M(top, sp.topFin)}</RoundedBox>
      {veins && veins.map((v, i) => <mesh key={i} position={[(v - 0.5) * 1.6, T + 0.001, (veins[(i + 2) % 6] - 0.5) * 0.6]} rotation={[-Math.PI / 2, 0, v * 2 - 1]}><planeGeometry args={[0.5 + v * 0.6, 0.012]} /><meshBasicMaterial color={darken(top, 0.16)} transparent opacity={0.6} /></mesh>)}
      <mesh position={[0, T - th - 0.015, 0.44]}><boxGeometry args={[2.18, 0.03, 0.03]} /><meshStandardMaterial color={accent} roughness={0.6} /></mesh>
      {rim && (sp.glow
        ? <>
            <mesh position={[0, T - th - 0.04, 0.455]}><boxGeometry args={[2.1, 0.012, 0.012]} /><meshBasicMaterial color={rim} toneMapped={false} /></mesh>
            <mesh position={[0, 0.012, 0]}><boxGeometry args={[2.0, 0.008, 0.8]} /><meshBasicMaterial color={rim} transparent opacity={0.35} toneMapped={false} /></mesh>
          </>
        : <Bx p={[0, T - th / 2, 0.476]} a={[2.2, th * 0.55, 0.014]} c={rim} f="metal" />)}
      {panel && <mesh castShadow position={[0, T - th - 0.26, -0.4]}><boxGeometry args={[2.0, 0.5, 0.025]} />{M(panel, sp.topFin === "lacquer" ? "lacquer" : "board")}</mesh>}
      <DeskLegs sp={sp} leg={leg} H={H} />
      {sp.wing && (
        <>
          <RoundedBox args={[0.7, th, 0.7]} radius={Math.min(0.015, th / 3)} smoothness={3} castShadow receiveShadow position={[0.75, T - th / 2, -0.825]}>{M(top, sp.topFin)}</RoundedBox>
          {[0.43, 1.07].map((x) => <Bx key={x} p={[x, H / 2, -1.14]} a={[0.05, H, 0.05]} c={leg} f={sp.legFin} />)}
        </>
      )}
      <group position={[0, T - DESK_DEFAULT.h, 0]}>{children}</group>
    </group>
  );
}

// ================================================================ chairs

function StarBase({ c, f, hub = 0.26, y = 0.03 }: { c: string; f: Finish; hub?: number; y?: number }) {
  return (
    <>
      <Cy p={[0, y + 0.1, 0]} t={0.03} b={0.04} h={0.2} n={10} c={c} f={f} />
      {STAR5.map((a) => (
        <group key={a} rotation={[0, a, 0]}>
          <Bx p={[0, y, hub / 2]} a={[0.045, 0.03, hub]} c={c} f={f} />
          <Sp p={[0, 0.025, hub]} rad={0.028} c="#23272e" />
        </group>
      ))}
    </>
  );
}
function Legs4({ x, z, h, c, f, t = 0.028, b = 0.018, sp = 0.08 }: { x: number; z: number; h: number; c: string; f: Finish; t?: number; b?: number; sp?: number }) {
  return <>{[-1, 1].map((sx) => [-1, 1].map((sz) => <Cy key={`${sx}${sz}`} p={[sx * x, h / 2, sz * z]} r={[-sz * sp, 0, sx * sp]} t={t} b={b} h={h} n={8} c={c} f={f} />))}</>;
}

/** Chair seat at the origin (floor-center), back toward +z, seat top about 0.34 so seated characters line up. */
export function ChairMesh({ item }: { item?: ShopItem }) {
  if (!item) {
    return (
      <group>
        <mesh castShadow position={[0, 0.3, 0]}><cylinderGeometry args={[0.31, 0.3, 0.08, 28]} /><meshStandardMaterial color={GRAPHITE_L} roughness={0.9} /></mesh>
        <RoundedBox args={[0.54, 0.5, 0.07]} radius={0.03} smoothness={3} castShadow position={[0, 0.65, 0.33]}><meshStandardMaterial color={GRAPHITE} roughness={0.9} /></RoundedBox>
        <mesh position={[0, 0.14, 0]}><cylinderGeometry args={[0.035, 0.035, 0.22, 10]} /><meshStandardMaterial color={STEEL} metalness={0.4} roughness={0.5} /></mesh>
        <mesh position={[0, 0.03, 0]}><cylinderGeometry args={[0.26, 0.28, 0.035, 5]} /><meshStandardMaterial color={GRAPHITE} metalness={0.3} roughness={0.6} /></mesh>
      </group>
    );
  }
  const sp = chairSpec(item);
  const seat = tone(item, sp.seat), back = tone(item, sp.back), base = tone(item, sp.base), trim = tone(item, sp.trim ?? "c2");
  return <group>{chairBody(sp, seat, back, base, trim)}</group>;
}

function chairBody(sp: ChairSpec, seat: string, back: string, base: string, trim: string): React.ReactNode {
  const sf = sp.seatFin, bf = sp.baseFin;
  switch (sp.kind) {
    case "task": {
      const mesh = sp.mesh;
      return (
        <>
          <Bx p={[0, 0.3, 0]} a={[0.52, 0.08, 0.5]} rad={0.03} c={seat} f={sf} />
          <Bx p={[0, 0.68, 0.26]} r={[0.08, 0, 0]} a={[0.5, 0.52, 0.045]} rad={0.02} c={back} f={mesh ? "plastic" : sf} />
          {mesh && <Bx p={[0, 0.68, 0.235]} r={[0.08, 0, 0]} a={[0.4, 0.42, 0.02]} c={lighten(back, 0.18)} f="fabric" x={{ roughness: 1 }} />}
          {sp.head && <Bx p={[0, 1.02, 0.27]} r={[0.1, 0, 0]} a={[0.3, 0.14, 0.06]} rad={0.03} c={back} f={sf} />}
          {sp.arms && [-1, 1].map((s) => (
            <group key={s}>
              <Bx p={[s * 0.3, 0.5, 0.02]} a={[0.05, 0.035, 0.3]} c={base} f={bf} />
              <Bx p={[s * 0.3, 0.41, 0.12]} a={[0.03, 0.18, 0.03]} c={base} f={bf} />
            </group>
          ))}
          <Cy p={[0, 0.24, 0]} t={0.07} b={0.05} h={0.05} c={base} f={bf} />
          <StarBase c={base} f={bf} />
        </>
      );
    }
    case "exec":
      return (
        <>
          <Bx p={[0, 0.28, 0]} a={[0.6, 0.14, 0.58]} rad={0.05} c={seat} f={sf} />
          <Bx p={[0, 0.8, 0.28]} r={[0.12, 0, 0]} a={[0.58, 0.9, 0.13]} rad={0.06} c={back} f={sf} />
          {[0.52, 0.8, 1.08].map((y) => <Bx key={y} p={[0, y, 0.215]} r={[0.12, 0, 0]} a={[0.5, 0.012, 0.012]} c={darken(back, 0.4)} f="plastic" s={false} />)}
          {[-1, 1].map((s) => (
            <group key={s}>
              <Bx p={[s * 0.34, 0.5, 0.02]} a={[0.08, 0.07, 0.46]} rad={0.03} c={back} f={sf} />
              <Bx p={[s * 0.34, 0.39, 0.1]} a={[0.04, 0.18, 0.05]} c={base} f={bf} />
            </group>
          ))}
          <Cy p={[0, 0.2, 0]} t={0.08} b={0.06} h={0.1} c={base} f={bf} />
          <StarBase c={base} f={bf} hub={0.3} />
        </>
      );
    case "gaming":
      return (
        <>
          <Bx p={[0, 0.28, 0]} a={[0.54, 0.1, 0.52]} rad={0.04} c={seat} f={sf} />
          {[-1, 1].map((s) => <Bx key={s} p={[s * 0.29, 0.33, 0]} a={[0.09, 0.14, 0.5]} rad={0.04} c={darken(seat, 0.25)} f={sf} />)}
          <Bx p={[0, 0.82, 0.29]} r={[0.14, 0, 0]} a={[0.5, 0.95, 0.1]} rad={0.04} c={darken(back, 0.2)} f={sf} />
          {[-1, 1].map((s) => <Bx key={s} p={[s * 0.275, 0.72, 0.27]} r={[0.14, 0, 0]} a={[0.07, 0.6, 0.15]} rad={0.03} c={back} f={sf} />)}
          <Bx p={[0, 0.8, 0.236]} r={[0.14, 0, 0]} a={[0.12, 0.75, 0.014]} c={trim} f="plastic" />
          <Bx p={[0, 1.2, 0.27]} r={[0.14, 0, 0]} a={[0.28, 0.14, 0.08]} rad={0.04} c={trim} f={sf} />
          <Bx p={[0, 0.52, 0.255]} r={[0.14, 0, 0]} a={[0.26, 0.12, 0.05]} rad={0.03} c={trim} f={sf} />
          {sp.arms && [-1, 1].map((s) => (
            <group key={s}>
              <Bx p={[s * 0.35, 0.52, 0.02]} a={[0.07, 0.035, 0.3]} c="#23272e" f="plastic" />
              <Bx p={[s * 0.35, 0.42, 0.1]} a={[0.03, 0.2, 0.03]} c="#23272e" f="plastic" />
            </group>
          ))}
          <Cy p={[0, 0.2, 0]} t={0.05} b={0.04} h={0.12} c="#23272e" f="metal" />
          <StarBase c="#23272e" f="plastic" hub={0.3} />
          <Cy p={[0, 0.08, 0]} t={0.045} h={0.01} c={trim} f="plastic" />
        </>
      );
    case "stool":
      return (
        <>
          <Cy p={[0, 0.33, 0]} t={0.23} b={0.22} h={0.05} n={28} c={seat} f={sf} />
          <Legs4 x={0.17} z={0.17} h={0.31} c={base} f={bf} t={0.026} b={0.018} />
          <mesh position={[0, 0.14, 0]} rotation={[Math.PI / 2, 0, 0]}><torusGeometry args={[0.2, 0.014, 6, 22]} />{M(base, bf)}</mesh>
        </>
      );
    case "wood":
      return (
        <>
          <Bx p={[0, 0.31, 0]} a={[0.46, 0.045, 0.44]} rad={0.012} c={seat} f={sf} />
          {[-1, 1].map((sx) => [-1, 1].map((sz) => <Bx key={`${sx}${sz}`} p={[sx * 0.2, 0.145, sz * 0.19]} a={[0.04, 0.29, 0.04]} c={base} f={bf} />))}
          {[-1, 1].map((s) => <Bx key={s} p={[s * 0.2, 0.6, 0.19]} a={[0.04, 0.58, 0.04]} c={base} f={bf} />)}
          {[0.5, 0.64, 0.78].map((y) => <Bx key={y} p={[0, y, 0.19]} a={[0.38, 0.08, 0.02]} c={back} f={bf} />)}
          <Bx p={[0, 0.12, 0]} a={[0.36, 0.02, 0.02]} c={base} f={bf} />
        </>
      );
    case "ball":
      return (
        <>
          <Sp p={[0, 0.15, 0]} rad={0.2} sc={[1.15, 1, 1.15]} n={20} c={seat} f={sf} />
          <mesh position={[0, 0.02, 0]} rotation={[Math.PI / 2, 0, 0]}><torusGeometry args={[0.18, 0.025, 8, 22]} />{M(base, "plastic")}</mesh>
          <Cy p={[0, 0.34, 0]} t={0.012} b={0.012} h={0.02} c={trim} f="plastic" />
        </>
      );
    case "bean":
      return (
        <>
          <Sp p={[0, 0.17, 0]} rad={1} sc={[0.36, 0.17, 0.34]} n={18} c={seat} f={sf} />
          <Sp p={[0, 0.34, 0.2]} r={[0.5, 0, 0]} rad={1} sc={[0.3, 0.22, 0.14]} n={16} c={seat} f={sf} />
          <Sp p={[0.2, 0.26, 0.06]} rad={1} sc={[0.1, 0.1, 0.18]} n={10} c={darken(seat, 0.1)} f={sf} />
          <Sp p={[-0.2, 0.26, 0.06]} rad={1} sc={[0.1, 0.1, 0.18]} n={10} c={darken(seat, 0.1)} f={sf} />
          <mesh position={[0, 0.5, 0.26]} rotation={[0.3, 0, 0]}><torusGeometry args={[0.05, 0.012, 6, 14]} />{M(trim, "fabric")}</mesh>
        </>
      );
    case "cube":
      return (
        <>
          <Bx p={[0, 0.16, 0]} a={[0.5, 0.32, 0.5]} rad={0.06} c={seat} f="fabric" />
          <Bx p={[0, 0.33, 0]} a={[0.44, 0.035, 0.44]} rad={0.015} c={lighten(seat, 0.2)} f="fabric" />
          <Bx p={[0, 0.06, 0.255]} a={[0.5, 0.02, 0.01]} c={trim} f="fabric" s={false} />
        </>
      );
    case "kneel":
      return (
        <>
          <Bx p={[0, 0.32, 0.05]} r={[-0.28, 0, 0]} a={[0.44, 0.08, 0.4]} rad={0.035} c={seat} f={sf} />
          <Bx p={[0, 0.2, -0.27]} r={[0.25, 0, 0]} a={[0.4, 0.09, 0.16]} rad={0.04} c={seat} f={sf} />
          {[-1, 1].map((s) => (
            <group key={s}>
              <Bx p={[s * 0.17, 0.025, -0.05]} a={[0.04, 0.04, 0.8]} c={base} f={bf} />
              <Bx p={[s * 0.17, 0.2, 0.17]} r={[-0.28, 0, 0]} a={[0.035, 0.34, 0.035]} c={base} f={bf} />
              <Bx p={[s * 0.17, 0.1, -0.28]} r={[0.25, 0, 0]} a={[0.035, 0.22, 0.035]} c={base} f={bf} />
            </group>
          ))}
          <Bx p={[0, 0.16, 0.12]} a={[0.34, 0.02, 0.02]} c={base} f={bf} s={false} />
          <Bx p={[0, 0.055, -0.38]} a={[0.4, 0.03, 0.03]} c={trim} f="wood" />
        </>
      );
    case "velvet":
      return (
        <>
          <Bx p={[0, 0.27, 0.02]} a={[0.58, 0.16, 0.54]} rad={0.04} c={seat} f={sf} />
          <Bx p={[0, 0.62, 0.3]} r={[0.07, 0, 0]} a={[0.6, 0.5, 0.12]} rad={0.05} c={seat} f={sf} />
          {[-1, 1].map((s) => <Bx key={s} p={[s * 0.3, 0.44, 0.04]} a={[0.09, 0.26, 0.5]} rad={0.04} c={darken(seat, 0.06)} f={sf} />)}
          {[0.5, 0.62, 0.74].map((y, r) => [-0.14, 0.14].map((x) => <Sp key={`${r}${x}`} p={[x + (r === 1 ? 0.14 * Math.sign(-x) * 0 : 0), y, 0.24]} rad={0.014} c={base} f="metal" n={6} />))}
          <Legs4 x={0.24} z={0.22} h={0.19} c={base} f={bf} t={0.022} b={0.012} sp={0.12} />
        </>
      );
    case "shell":
      return (
        <>
          <mesh castShadow position={[0, 0.34, 0]} scale={[1, 0.5, 1]}><sphereGeometry args={[0.31, 22, 10, 0, Math.PI * 2, Math.PI / 2, Math.PI / 2]} /><meshStandardMaterial color={seat} {...FIN[sf]} side={THREE.DoubleSide} /></mesh>
          <mesh castShadow position={[0, 0.6, 0.02]}><cylinderGeometry args={[0.3, 0.3, 0.46, 22, 1, true, -Math.PI * 0.42, Math.PI * 0.84]} /><meshStandardMaterial color={seat} {...FIN[sf]} side={THREE.DoubleSide} /></mesh>
          <Legs4 x={0.18} z={0.17} h={0.25} c={base} f={bf} t={0.016} b={0.011} sp={0.13} />
        </>
      );
    case "throne":
      return (
        <>
          <Bx p={[0, 0.17, 0]} a={[0.64, 0.14, 0.6]} rad={0.03} c={back} f={bf} />
          <Bx p={[0, 0.28, 0]} a={[0.58, 0.1, 0.54]} rad={0.04} c={seat} f={sf} />
          <Bx p={[0, 0.98, 0.31]} a={[0.64, 1.24, 0.1]} rad={0.04} c={back} f={bf} />
          <Bx p={[0, 0.86, 0.255]} a={[0.46, 0.84, 0.04]} rad={0.03} c={seat} f={sf} />
          {[-1, 1].map((s) => (
            <group key={s}>
              <Bx p={[s * 0.35, 0.5, 0.02]} a={[0.08, 0.07, 0.52]} rad={0.03} c={back} f={bf} />
              <Bx p={[s * 0.35, 0.38, -0.2]} a={[0.06, 0.2, 0.06]} c={back} f={bf} />
              <Sp p={[s * 0.35, 0.5, -0.27]} rad={0.05} c={back} f={bf} n={10} />
              <Sp p={[s * 0.3, 0.04, -0.25]} rad={0.05} c={back} f={bf} n={10} />
              <Sp p={[s * 0.3, 0.04, 0.25]} rad={0.05} c={back} f={bf} n={10} />
            </group>
          ))}
          {[-1, 0, 1].map((i) => <Cy key={i} p={[i * 0.22, 1.67 - Math.abs(i) * 0.06, 0.31]} t={0.0} b={0.07} h={0.18} n={4} c={back} f={bf} />)}
          <Sp p={[0, 1.4, 0.255]} rad={0.045} c={trim} f="glass" x={{ opacity: 1, transparent: false, emissive: trim, emissiveIntensity: 0.5 }} n={10} />
        </>
      );
  }
}

// ================================================================ tables

function TableLegs({ sp, c, H, fin }: { sp: ReturnType<typeof tableSpec>; c: string; H: number; fin: Finish }) {
  const hx = sp.w / 2, hz = sp.d / 2;
  switch (sp.legs) {
    case "post":
      return <>{sp.shape === "hex"
        ? [0, 1, 2].map((i) => <Cy key={i} p={[Math.sin(i * 2.094 + 0.5) * 0.22, H / 2, Math.cos(i * 2.094 + 0.5) * 0.22]} t={0.02} h={H} n={8} c={c} f={fin} />)
        : [-1, 1].map((sx) => [-1, 1].map((sz) => <Bx key={`${sx}${sz}`} p={[sx * (hx - 0.06), H / 2, sz * (hz - 0.05)]} a={[0.04, H, 0.04]} c={c} f={fin} />))}</>;
    case "splay":
      return <Legs4 x={hx - 0.1} z={hz - 0.08} h={H} c={c} f={fin} t={0.03} b={0.02} sp={0.1} />;
    case "hairpin":
      return <Legs4 x={hx - 0.08} z={hz - 0.08} h={H} c={c} f={fin} t={0.012} b={0.01} sp={0.1} />;
    case "pedestal":
      return (
        <>
          <Cy p={[0, H / 2, 0]} t={0.07} b={sp.w > 1.1 ? 0.09 : 0.07} h={H} n={18} c={c} f={fin} />
          <Cy p={[0, 0.015, 0]} t={0.27} b={0.3} h={0.03} n={26} c={c} f={fin} />
        </>
      );
    case "trestle":
      return (
        <>
          {[-1, 1].map((s) => (
            <group key={s} position={[s * (hx - 0.3), 0, 0]}>
              <Bx p={[0, H / 2, 0]} a={[0.06, H, hz * 1.2]} c={c} f={fin} />
              <Bx p={[0, 0.02, 0]} a={[0.1, 0.04, hz * 1.7]} c={c} f={fin} />
            </group>
          ))}
          <Bx p={[0, H - 0.14, 0]} a={[sp.w - 0.6, 0.05, 0.05]} c={c} f={fin} />
        </>
      );
    case "x":
      return (
        <>
          {[-1, 1].map((s) => <XFrame key={s} x={s * (hx - (sp.extra === "net" ? 0.4 : 0.14))} H={H} dz={hz * 1.3} t={sp.extra === "net" ? 0.07 : 0.04} c={c} f={fin} />)}
          <Cy p={[0, H / 2, 0]} r={[0, 0, Math.PI / 2]} t={0.016} h={sp.w - (sp.extra === "net" ? 0.8 : 0.28)} n={8} c={c} f={fin} />
        </>
      );
    case "cross":
      return (
        <>
          <Cy p={[0, H / 2, 0]} t={0.035} h={H} n={12} c={c} f={fin} />
          <Bx p={[0, 0.02, 0]} a={[0.5, 0.04, 0.06]} c={c} f={fin} />
          <Bx p={[0, 0.02, 0]} r={[0, Math.PI / 2, 0]} a={[0.5, 0.04, 0.06]} c={c} f={fin} />
          <mesh position={[0, 0.26, 0]} rotation={[Math.PI / 2, 0, 0]}><torusGeometry args={[0.16, 0.012, 6, 20]} />{M(c, fin)}</mesh>
        </>
      );
    default: return null;
  }
}

function Table({ item }: { item: ShopItem }) {
  const sp = tableSpec(item);
  const top = tone(item, sp.top), leg = tone(item, sp.leg), th = sp.thick, H = sp.h - th;
  const veins = sp.veins ? rnd(item.id, 6) : null;
  return (
    <group>
      {sp.shape === "rect"
        ? <Bx p={[0, sp.h - th / 2, 0]} a={[sp.w, th, sp.d]} rad={0.012} c={top} f={sp.topFin} />
        : <Cy p={[0, sp.h - th / 2, 0]} t={sp.w / 2} h={th} n={sp.shape === "hex" ? 6 : 32} c={top} f={sp.topFin} />}
      {veins && veins.map((v, i) => <mesh key={i} position={[(v - 0.5) * sp.w * 0.5, sp.h + 0.001, (veins[(i + 2) % 6] - 0.5) * sp.w * 0.5]} rotation={[-Math.PI / 2, 0, v * 3]}><planeGeometry args={[0.35 + v * 0.3, 0.01]} /><meshBasicMaterial color={darken(top, 0.16)} transparent opacity={0.6} /></mesh>)}
      <TableLegs sp={sp} c={leg} H={H} fin={sp.legFin} />
      {sp.extra === "shelf" && <Bx p={[0, 0.13, 0]} a={[sp.w - 0.14, 0.025, sp.d - 0.1]} c={mix(top, leg, 0.15)} f={sp.topFin} />}
      {sp.extra === "net" && (
        <>
          <Bx p={[0, sp.h + 0.0015, 0]} a={[sp.w - 0.04, 0.002, 0.012]} c="#ffffff" basic s={false} />
          <Bx p={[0, sp.h + 0.0015, 0]} a={[0.012, 0.002, sp.d - 0.04]} c="#ffffff" basic s={false} />
          <mesh position={[0, sp.h + 0.08, 0]}><boxGeometry args={[0.006, 0.15, sp.d + 0.12]} /><meshStandardMaterial color="#f4f6f8" transparent opacity={0.55} roughness={1} /></mesh>
          <Bx p={[0, sp.h + 0.155, 0]} a={[0.01, 0.014, sp.d + 0.12]} c="#f4f6f8" f="plastic" s={false} />
          {[-1, 1].map((s) => <Bx key={s} p={[0, sp.h + 0.08, s * (sp.d / 2 + 0.065)]} a={[0.02, 0.17, 0.02]} c="#2c323b" f="metal" />)}
          <Sp p={[0.5, sp.h + 0.02, 0.2]} rad={0.02} c="#ffffff" f="plastic" n={8} />
        </>
      )}
    </group>
  );
}

// ================================================================ sofas

function Sofa({ item }: { item: ShopItem }) {
  const sp = sofaSpec(item);
  const body = tone(item, sp.seat), cush = tone(item, sp.cush), fr = tone(item, sp.frame);
  const f = sp.fin, w = sp.w, d = sp.d;
  const seatTop = (lh: number) => lh + 0.22 + 0.14;
  const legsFor = (lh: number, xs: number, zs: number) => (lh > 0 ? sp.legs === "wood"
    ? <Legs4 x={xs} z={zs} h={lh} c={fr} f="wood" t={0.03} b={0.02} sp={0.1} />
    : <>{[-1, 1].map((sx) => [-1, 1].map((sz) => <Cy key={`${sx}${sz}`} p={[sx * xs, lh / 2, sz * zs]} t={0.015} h={lh} n={6} c={fr} f="metal" />))}</> : null);
  switch (sp.kind) {
    case "pouf":
      return (
        <>
          <Cy p={[0, 0.18, 0]} t={0.3} b={0.31} h={0.36} n={28} c={body} f={f} />
          <Cy p={[0, 0.37, 0]} t={0.27} b={0.3} h={0.04} n={28} c={lighten(body, 0.12)} f={f} />
          <mesh position={[0, 0.33, 0]} rotation={[Math.PI / 2, 0, 0]}><torusGeometry args={[0.295, 0.008, 5, 28]} />{M(darken(body, 0.25), f)}</mesh>
          <mesh position={[0.1, 0.4, 0.05]} rotation={[Math.PI / 2, 0, 0]}><torusGeometry args={[0.03, 0.008, 6, 10]} />{M(darken(body, 0.25), f)}</mesh>
        </>
      );
    case "bench":
      return (
        <>
          <Bx p={[0, 0.44, 0]} a={[w, 0.06, d]} rad={0.015} c={body} f={f} />
          <Bx p={[0, 0.405, 0]} a={[w - 0.1, 0.02, d - 0.08]} c={darken(body, 0.2)} f={f} />
          {[-1, 1].map((s) => (
            <group key={s} position={[s * (w / 2 - 0.12), 0, 0]}>
              <Bx p={[0, 0.2, d / 2 - 0.06]} a={[0.035, 0.4, 0.035]} c={fr} f="metal" />
              <Bx p={[0, 0.2, -d / 2 + 0.06]} a={[0.035, 0.4, 0.035]} c={fr} f="metal" />
              <Bx p={[0, 0.38, 0]} a={[0.035, 0.035, d - 0.08]} c={fr} f="metal" />
            </group>
          ))}
          <Bx p={[0, 0.14, 0]} a={[w - 0.3, 0.02, 0.02]} c={fr} f="metal" s={false} />
        </>
      );
    case "egg":
      return (
        <>
          <Cy p={[0, 0.03, 0]} t={0.3} b={0.34} h={0.06} n={26} c={fr} f="metal" />
          <Cy p={[0, 0.2, 0]} t={0.05} h={0.3} c={fr} f="metal" />
          <mesh castShadow position={[0, 0.8, 0]} scale={[0.93, 1.05, 0.93]}><sphereGeometry args={[0.5, 26, 18, Math.PI / 2 + 0.75, Math.PI * 2 - 1.5, 0.12, Math.PI - 0.2]} /><meshStandardMaterial color={body} {...FIN[f]} roughness={0.7} side={THREE.DoubleSide} /></mesh>
          <Sp p={[0, 0.5, 0.02]} rad={1} sc={[0.36, 0.1, 0.34]} n={14} c={cush} f="fabric" />
          <Sp p={[0, 0.75, -0.2]} r={[-0.25, 0, 0]} rad={1} sc={[0.3, 0.32, 0.08]} n={14} c={cush} f="fabric" />
        </>
      );
    case "modular":
      return (
        <>
          {[-1, 0, 1].map((i) => {
            const mw = (w - 0.06) / 3, col = i === 0 ? cush : body;
            return (
              <group key={i} position={[i * (mw + 0.03), 0, 0]}>
                <Bx p={[0, 0.21, 0.03]} a={[mw, 0.3, d - 0.08]} rad={0.04} c={col} f={f} />
                <Bx p={[0, 0.4, 0.05]} a={[mw - 0.06, 0.1, d - 0.2]} rad={0.04} c={lighten(col, 0.1)} f={f} />
                <Bx p={[0, 0.5, -d / 2 + 0.14]} r={[-0.1, 0, 0]} a={[mw - 0.02, 0.4, 0.2]} rad={0.05} c={col} f={f} />
              </group>
            );
          })}
        </>
      );
    case "corner": {
      const mainD = 0.95, cw = 0.95;
      return (
        <>
          <Bx p={[0, 0.22, -d / 2 + mainD / 2]} a={[w, 0.28, mainD]} rad={0.04} c={body} f={f} />
          <Bx p={[w / 2 - cw / 2, 0.22, -d / 2 + mainD + (d - mainD) / 2 - 0.01]} a={[cw, 0.28, d - mainD]} rad={0.04} c={body} f={f} />
          {[-1, 0, 1].map((i) => <Bx key={i} p={[-0.4 + i * 0.64 - (w - 2.4) * 0, 0.42, -d / 2 + mainD / 2 + 0.05]} a={[0.62, 0.12, mainD - 0.18]} rad={0.04} c={lighten(body, 0.08)} f={f} />)}
          <Bx p={[w / 2 - cw / 2, 0.42, -d / 2 + mainD + (d - mainD) / 2]} a={[cw - 0.14, 0.12, d - mainD - 0.14]} rad={0.04} c={lighten(body, 0.08)} f={f} />
          <Bx p={[0, 0.58, -d / 2 + 0.12]} r={[-0.1, 0, 0]} a={[w - 0.1, 0.42, 0.2]} rad={0.06} c={body} f={f} />
          <Bx p={[-w / 2 + 0.1, 0.45, -d / 2 + mainD / 2]} a={[0.2, 0.56, mainD]} rad={0.06} c={darken(body, 0.06)} f={f} />
          <Bx p={[w / 2 - 0.05, 0.4, -d / 2 + mainD + (d - mainD) / 2]} a={[0.1, 0.34, d - mainD]} rad={0.04} c={darken(body, 0.06)} f={f} />
          <Bx p={[-0.9, 0.62, -d / 2 + 0.3]} r={[-0.2, 0.3, 0.1]} a={[0.36, 0.36, 0.1]} rad={0.04} c={cush} f="fabric" />
        </>
      );
    }
    default: {
      const arms = sp.arms, aw = arms === "none" ? 0 : arms === "slim" ? 0.1 : 0.2;
      const lh = sp.legs === "none" ? 0 : sp.kind === "chester" ? 0.08 : 0.12;
      const inner = w - aw * 2, n = sp.seats, cw = inner / n;
      const bh = sp.kind === "chester" ? 0.58 : 0.42, ah = sp.kind === "chester" ? 0.5 : arms === "slim" ? 0.3 : 0.36;
      const st = seatTop(lh);
      return (
        <>
          <Bx p={[0, lh + 0.11, 0]} a={[w, 0.22, d]} rad={0.04} c={body} f={f} />
          {Array.from({ length: n }).map((_, i) => <Bx key={i} p={[-inner / 2 + cw * (i + 0.5), lh + 0.29, 0.06]} a={[cw - 0.02, 0.14, d - 0.2]} rad={0.045} c={lighten(body, sp.tuft ? 0 : 0.07)} f={f} />)}
          {Array.from({ length: n }).map((_, i) => <Bx key={i} p={[-inner / 2 + cw * (i + 0.5), st + bh / 2 - 0.02, -d / 2 + 0.13]} r={[-0.12, 0, 0]} a={[cw - 0.02, bh, 0.2]} rad={0.06} c={lighten(body, sp.tuft ? 0 : 0.04)} f={f} />)}
          {arms !== "none" && [-1, 1].map((s) => (
            <group key={s}>
              <Bx p={[s * (w / 2 - aw / 2), lh + 0.22 + ah / 2 - 0.02, 0]} a={[aw, ah, d]} rad={0.05} c={darken(body, 0.05)} f={f} />
              {arms === "roll" && <Cy p={[s * (w / 2 - aw / 2), lh + 0.22 + ah - 0.02, 0]} r={[Math.PI / 2, 0, 0]} t={aw * 0.62} h={d - 0.02} n={16} c={darken(body, 0.05)} f={f} />}
            </group>
          ))}
          {sp.tuft && [0.36, 0.5, 0.64].map((y, r) => Array.from({ length: n * 2 + 1 }).map((_, i) => (
            <Sp key={`${r}${i}`} p={[-inner / 2 + (inner / (n * 2 + 1)) * (i + 0.5) + (r === 1 ? 0.05 : 0), lh + y + 0.22, -d / 2 + 0.235]} rad={0.014} n={6} c={fr} f="metal" />
          )))}
          {sp.kind !== "chester" && sp.legs !== "none" && sp.fin !== "leather" && <Bx p={[-w / 2 + aw + 0.2, st + 0.16, -d / 2 + 0.3]} r={[-0.2, 0.3, 0.12]} a={[0.34, 0.34, 0.1]} rad={0.04} c={cush} f="fabric" />}
          {sp.kind === "armchair" && <Bx p={[0, st + 0.14, -d / 2 + 0.3]} r={[-0.2, 0, 0.5]} a={[0.3, 0.3, 0.09]} rad={0.04} c={cush} f="fabric" />}
          {legsFor(lh, w / 2 - 0.1, d / 2 - 0.1)}
        </>
      );
    }
  }
}

// ================================================================ cabinets

/** Books on a shelf from x0 to x1 (deterministic from the seed). */
function Books({ x0, x1, y, d, seed, max = 0.3 }: { x0: number; x1: number; y: number; d: number; seed: string; max?: number }) {
  const r = rnd(seed, 40);
  const out: React.ReactNode[] = [];
  let x = x0 + 0.02, i = 0;
  while (x < x1 - 0.06 && i < 30) {
    const w = 0.035 + r[i] * 0.03, h = Math.min(max, 0.18 + r[i + 1] * 0.12);
    if (r[i + 2] > 0.88) { x += 0.08; i += 3; continue; }
    out.push(<mesh key={i} castShadow position={[x + w / 2, y + h / 2, 0]}><boxGeometry args={[w, h, d * 0.72]} />{M(BOOKS[Math.floor(r[i + 3] * BOOKS.length)], "plastic", { roughness: 0.9 })}</mesh>);
    x += w + 0.004; i += 4;
  }
  return <>{out}</>;
}

function OpenShelf({ w, h, d, n, body, back, seed, books = true }: { w: number; h: number; d: number; n: number; body: string; back: string; seed: string; books?: boolean }) {
  const t = 0.03, gap = (h - t) / n;
  return (
    <>
      {[-1, 1].map((s) => <Bx key={s} p={[s * (w / 2 - t / 2), h / 2, 0]} a={[t, h, d]} c={body} f="wood" />)}
      {Array.from({ length: n + 1 }).map((_, i) => <Bx key={i} p={[0, t / 2 + i * gap, 0]} a={[w - t * 2, t, d]} c={body} f="wood" />)}
      <Bx p={[0, h / 2, -d / 2 + 0.01]} a={[w - t * 2, h - t, 0.015]} c={back} f="board" s={false} />
      {books && Array.from({ length: n }).map((_, i) => <group key={i} position={[0, t + i * gap + 0.002, 0]}><Books x0={-w / 2 + t} x1={w / 2 - t} y={0} d={d} seed={seed + i} max={gap - 0.07} /></group>)}
    </>
  );
}

function Cabinet({ item }: { item: ShopItem }) {
  const sp = cabSpec(item);
  const body = tone(item, sp.body), front = tone(item, sp.front), handle = tone(item, sp.handle), { w, h, d, n } = sp;
  switch (sp.kind) {
    case "filing": {
      const dh = (h - 0.12) / n;
      return (
        <>
          <Bx p={[0, h / 2, 0]} a={[w, h, d]} rad={0.012} c={body} f="metal" />
          {Array.from({ length: n }).map((_, i) => (
            <group key={i} position={[0, 0.06 + dh * (i + 0.5), d / 2 + 0.006]}>
              <Bx a={[w - 0.05, dh - 0.02, 0.014]} rad={0.006} c={lighten(front, 0.06)} f="metal" />
              <Bx p={[0, dh * 0.22, 0.012]} a={[0.2, 0.022, 0.02]} c={handle} f="metal" />
              <Bx p={[0, -dh * 0.15, 0.009]} a={[0.12, 0.04, 0.004]} c="#f4f6f8" f="plastic" s={false} />
            </group>
          ))}
          <Bx p={[0, 0.03, 0]} a={[w - 0.02, 0.06, d - 0.02]} c={darken(body, 0.45)} f="plastic" />
        </>
      );
    }
    case "lowbook":
      return <OpenShelf w={w} h={h} d={d} n={2} body={body} back={darken(body, 0.15)} seed={item.id} />;
    case "tallbook":
      return <OpenShelf w={w} h={h} d={d} n={n} body={body} back={tone(item, "c2")} seed={item.id} />;
    case "locker": {
      const cw = w / n;
      return (
        <>
          <Bx p={[0, h / 2 + 0.04, 0]} a={[w, h - 0.08, d]} c={body} f="metal" />
          <Bx p={[0, 0.04, 0]} a={[w - 0.04, 0.08, d - 0.06]} c="#2c323b" f="plastic" />
          {Array.from({ length: n }).map((_, i) => (
            <group key={i} position={[-w / 2 + cw * (i + 0.5), h / 2 + 0.04, d / 2 + 0.008]}>
              <Bx a={[cw - 0.025, h - 0.14, 0.016]} c={front} f="metal" />
              {[0, 1, 2, 3].map((v) => <Bx key={v} p={[0, h / 2 - 0.2 - v * 0.04, 0.01]} a={[cw * 0.5, 0.012, 0.006]} c={darken(front, 0.5)} f="plastic" s={false} />)}
              <Bx p={[cw * 0.28, -0.02, 0.014]} a={[0.03, 0.12, 0.02]} c={STEEL} f="metal" />
              <Bx p={[0, h / 2 - 0.38, 0.01]} a={[0.06, 0.04, 0.006]} c="#f4f6f8" f="plastic" s={false} />
            </group>
          ))}
        </>
      );
    }
    case "sideboard":
    case "credenza": {
      const lh = 0.18, bh = h - lh - 0.03, cw = (w - 0.06) / n, cred = sp.kind === "credenza";
      return (
        <>
          <Legs4 x={w / 2 - 0.1} z={d / 2 - 0.08} h={lh} c={tone(item, "c2")} f={cred ? "wood" : "metal"} t={0.03} b={0.016} sp={0.12} />
          <Bx p={[0, lh + bh / 2, 0]} a={[w, bh, d]} c={body} f="wood" />
          <Bx p={[0, h - 0.015, 0]} a={[w + 0.03, 0.03, d + 0.03]} rad={0.01} c={lighten(front, 0.04)} f="wood" />
          {Array.from({ length: n }).map((_, i) => (
            <group key={i} position={[-w / 2 + 0.03 + cw * (i + 0.5), lh + bh / 2, d / 2 + (cred ? (i % 2 ? 0.02 : 0.008) : 0.008)]}>
              <Bx a={[cw - 0.015, bh - 0.04, 0.016]} c={front} f="wood" />
              {cred
                ? <Bx p={[(i % 2 ? -1 : 1) * cw * 0.35, 0, 0.012]} a={[0.015, 0.14, 0.012]} c={handle} f="metal" />
                : <Cy p={[(i === 1 ? 0 : i === 0 ? 1 : -1) * cw * 0.36, bh * 0.28, 0.02]} r={[Math.PI / 2, 0, 0]} t={0.014} h={0.03} n={10} c={handle} f="metal" />}
            </group>
          ))}
        </>
      );
    }
    case "wardrobe": {
      const hd = tone(item, "c2");
      return (
        <>
          <Bx p={[0, h / 2 + 0.03, 0]} a={[w, h - 0.06, d]} c={body} f="lacquer" />
          <Bx p={[0, 0.03, 0.01]} a={[w - 0.04, 0.06, d - 0.04]} c={darken(body, 0.2)} f="lacquer" />
          <Bx p={[0, h - 0.025, 0]} a={[w + 0.04, 0.05, d + 0.03]} rad={0.01} c={body} f="lacquer" />
          {[-1, 1].map((s) => (
            <group key={s} position={[s * (w / 4 - 0.004), h / 2, d / 2 + 0.008]}>
              <Bx a={[w / 2 - 0.03, h - 0.2, 0.018]} rad={0.006} c={lighten(body, 0.04)} f="lacquer" />
              <Bx p={[-s * 0.06, 0, 0.014]} a={[0.016, 0.34, 0.016]} c={hd} f="metal" />
              <Bx a={[w / 2 - 0.13, h - 0.3, 0.004]} p={[0, 0, 0.011]} c={darken(body, 0.05)} f="lacquer" s={false} />
            </group>
          ))}
        </>
      );
    }
    case "cubes": {
      const t = 0.03, cols = 3, rows = 3, cw = (w - t) / cols, rh = (h - t) / rows;
      const r = rnd(item.id, 9);
      const binCols = [front, "#f2b44a", "#6fb9a4", "#e8855a"];
      return (
        <>
          {Array.from({ length: cols + 1 }).map((_, i) => <Bx key={i} p={[-w / 2 + t / 2 + i * cw, h / 2, 0]} a={[t, h, d]} c={body} f="lacquer" />)}
          {Array.from({ length: rows + 1 }).map((_, i) => <Bx key={i} p={[0, t / 2 + i * rh, 0]} a={[w, t, d]} c={body} f="lacquer" />)}
          <Bx p={[0, h / 2, -d / 2 + 0.008]} a={[w - t, h - t, 0.012]} c={darken(body, 0.1)} f="board" s={false} />
          {Array.from({ length: rows * cols }).map((_, k) => {
            const cx = -w / 2 + t / 2 + cw * ((k % cols) + 0.5), cy = t / 2 + rh * (Math.floor(k / cols) + 0.5), v = r[k];
            if (v < 0.35) return null;
            return v < 0.75
              ? <Bx key={k} p={[cx, cy - 0.02, 0.02]} a={[cw - 0.1, rh - 0.12, d - 0.08]} rad={0.015} c={binCols[Math.floor(v * 10) % binCols.length]} f="fabric" />
              : <group key={k} position={[cx, cy - rh / 2 + t / 2, 0]}><Books x0={-cw / 2 + 0.04} x1={cw / 2 - 0.04} y={0} d={d} seed={item.id + k} max={rh - 0.1} /></group>;
          })}
        </>
      );
    }
    case "vitrine": {
      const gold = tone(item, "c2");
      return (
        <>
          <Bx p={[0, 0.06, 0]} a={[w, 0.12, d]} c={body} f="wood" />
          <Bx p={[0, h - 0.04, 0]} a={[w + 0.03, 0.08, d + 0.03]} rad={0.01} c={body} f="wood" />
          {[-1, 1].map((s) => <Bx key={s} p={[s * (w / 2 - 0.02), h / 2, 0]} a={[0.04, h, d]} c={body} f="wood" />)}
          <Bx p={[0, h / 2, -d / 2 + 0.01]} a={[w - 0.08, h - 0.1, 0.015]} c={darken(body, 0.25)} f="board" s={false} />
          {[0.5, 0.95, 1.4].map((y, i) => (
            <group key={y}>
              <Bx p={[0, y, 0]} a={[w - 0.08, 0.015, d - 0.06]} c="#cfe6ee" f="glass" />
              {i === 0 && <Cy p={[-0.2, y + 0.1, 0]} t={0.07} b={0.035} h={0.18} c={gold} f="metal" />}
              {i === 0 && <Bx p={[-0.2, y + 0.01, 0]} a={[0.16, 0.03, 0.12]} c={body} f="wood" />}
              {i === 1 && <Sp p={[0.1, y + 0.1, 0]} rad={0.09} c="#5a95e8" f="glass" x={{ opacity: 0.8 }} />}
              {i === 1 && <Bx p={[-0.2, y + 0.07, 0]} a={[0.18, 0.14, 0.12]} c="#b9c2cc" f="metal" />}
              {i === 2 && <Cy p={[0.12, y + 0.09, 0]} t={0.055} b={0.04} h={0.16} c="#d9534f" f="plastic" />}
              {i === 2 && <Books x0={-0.4} x1={0} y={y + 0.01} d={d} seed={item.id} max={0.22} />}
              {i === 3 && null}
            </group>
          ))}
          <Bx p={[0, h / 2 + 0.02, d / 2 - 0.02]} a={[w - 0.08, h - 0.2, 0.01]} c="#cfe6ee" f="glass" />
          <Bx p={[w / 2 - 0.08, h / 2, d / 2 - 0.005]} a={[0.012, 0.22, 0.014]} c={gold} f="metal" />
          <mesh position={[0, h - 0.09, 0]}><boxGeometry args={[w - 0.1, 0.012, d - 0.1]} /><meshBasicMaterial color="#fff1d6" toneMapped={false} /></mesh>
        </>
      );
    }
    case "rolling": {
      const dh = (h - 0.1) / n;
      return (
        <>
          {[-1, 1].map((sx) => [-1, 1].map((sz) => <group key={`${sx}${sz}`} position={[sx * (w / 2 - 0.06), 0, sz * (d / 2 - 0.07)]}><Sp p={[0, 0.03, 0]} rad={0.03} c="#23272e" n={8} /></group>))}
          <Bx p={[0, 0.07 + (h - 0.1) / 2, 0]} a={[w, h - 0.1, d]} c={body} f="metal" />
          <Bx p={[0, h - 0.015, 0]} a={[w + 0.01, 0.03, d + 0.01]} c={lighten(body, 0.2)} f="metal" />
          {Array.from({ length: n }).map((_, i) => (
            <group key={i} position={[0, 0.1 + dh * (i + 0.5), d / 2 + 0.006]}>
              <Bx a={[w - 0.04, dh - 0.02, 0.012]} c={lighten(front, 0.08)} f="metal" />
              <Bx p={[0, 0.0, 0.012]} a={[0.16, 0.018, 0.016]} c={STEEL} f="metal" />
            </group>
          ))}
        </>
      );
    }
  }
}

// ================================================================ dividers

function Divider({ item }: { item: ShopItem }) {
  const sp = divSpec(item);
  const body = tone(item, sp.body), fr = tone(item, sp.frame), { w, h, d } = sp;
  switch (sp.kind) {
    case "fabric":
      return (
        <>
          <Bx p={[0, h / 2 + 0.03, 0]} a={[w, h - 0.06, 0.06]} rad={0.02} c={body} f="fabric" />
          {[-0.4, 0, 0.4].map((x) => <Bx key={x} p={[x * (w / 1.6), h / 2 + 0.03, 0.032]} a={[0.008, h - 0.2, 0.004]} c={darken(body, 0.2)} f="fabric" s={false} />)}
          {[-1, 1].map((s) => <Bx key={s} p={[s * (w / 2 - 0.2), 0.015, 0]} a={[0.08, 0.03, 0.42]} c={fr} f="metal" />)}
          <Bx p={[0, h + 0.0, 0]} a={[w + 0.02, 0.02, 0.07]} rad={0.008} c={fr} f="metal" />
        </>
      );
    case "folding": {
      const pw = w / 3, a = 0.55, r = rnd(item.id, 3);
      return (
        <>
          {[-1, 0, 1].map((i, k) => (
            <group key={i} position={[i * pw * Math.cos(a) * 0.98, 0, k % 2 ? 0.16 : -0.1]} rotation={[0, k % 2 ? -a : a, 0]}>
              <Bx p={[0, h / 2, 0]} a={[pw, h, 0.03]} c={fr} f="wood" />
              <Bx p={[0, h / 2, 0.003]} a={[pw - 0.07, h - 0.1, 0.034]} c={body} f="fabric" />
              {[0.33, 0.66].map((y) => <Bx key={y} p={[0, h * y, 0.021]} a={[pw - 0.07, 0.014, 0.006]} c={fr} f="wood" s={false} />)}
              <Bx p={[0, h / 2, 0.021]} a={[0.014, h - 0.1, 0.006]} c={fr} f="wood" s={false} />
              <Sp p={[(r[k] - 0.5) * 0.1, h * 0.8, 0.026]} rad={0.04} sc={[1, 1, 0.2]} c={mix(body, fr, 0.45)} f="fabric" n={8} />
            </group>
          ))}
        </>
      );
    }
    case "slats": {
      const n = 14;
      return (
        <>
          {[0.03, h - 0.03].map((y) => <Bx key={y} p={[0, y, 0]} a={[w, 0.06, 0.1]} c={fr} f="wood" />)}
          {Array.from({ length: n }).map((_, i) => <Bx key={i} p={[-w / 2 + 0.07 + (i * (w - 0.14)) / (n - 1), h / 2, 0]} a={[0.05, h - 0.1, 0.07]} c={body} f="wood" />)}
          {[-1, 1].map((s) => <Bx key={s} p={[s * (w / 2 - 0.15), 0.015, 0]} a={[0.08, 0.03, 0.4]} c={fr} f="wood" />)}
        </>
      );
    }
    case "cubes": {
      const t = 0.03, cols = 4, rows = 3, cw = (w - t) / cols, rh = (h - t) / rows, r = rnd(item.id, 12);
      return (
        <>
          {Array.from({ length: cols + 1 }).map((_, i) => <Bx key={i} p={[-w / 2 + t / 2 + i * cw, h / 2, 0]} a={[t, h, d]} c={body} f="lacquer" />)}
          {Array.from({ length: rows + 1 }).map((_, i) => <Bx key={i} p={[0, t / 2 + i * rh, 0]} a={[w, t, d]} c={body} f="lacquer" />)}
          {Array.from({ length: cols * rows }).map((_, k) => {
            const cx = -w / 2 + t / 2 + cw * ((k % cols) + 0.5), cy = t + rh * Math.floor(k / cols), v = r[k];
            if (v < 0.4) return null;
            if (v < 0.65) return <group key={k} position={[cx, cy, 0]}><Cy p={[0, 0.07, 0]} t={0.07} b={0.055} h={0.14} c="#c4aa84" f="plastic" /><Sp p={[0, 0.2, 0]} rad={0.1} sc={[1, 0.9, 1]} c="#5f9a62" f="fabric" n={8} /></group>;
            if (v < 0.85) return <group key={k} position={[cx - cw / 2 + 0.03, cy, 0]}><Books x0={0} x1={cw - 0.06} y={0} d={d} seed={item.id + k} max={rh - 0.08} /></group>;
            return <Bx key={k} p={[cx, cy + 0.08, 0]} a={[cw - 0.12, 0.14, d - 0.1]} rad={0.012} c={fr} f="fabric" />;
          })}
        </>
      );
    }
    case "glass":
      return (
        <>
          <Bx p={[0, h / 2 + 0.05, 0]} a={[w - 0.04, h - 0.1, 0.02]} c={body} f="glass" x={{ opacity: 0.3 }} />
          {[0.05, h - 0.05].map((y) => <Bx key={y} p={[0, y, 0]} a={[w, 0.04, 0.04]} c={fr} f="metal" />)}
          {[-1, 1].map((s) => <Bx key={s} p={[s * (w / 2 - 0.02), h / 2, 0]} a={[0.04, h - 0.04, 0.04]} c={fr} f="metal" />)}
          {[-1, 1].map((s) => <Bx key={s} p={[s * (w / 2 - 0.2), 0.015, 0]} a={[0.08, 0.03, 0.4]} c={fr} f="metal" />)}
          <Bx p={[-0.25, h * 0.55, 0.012]} a={[0.5, 0.01, 0.002]} c={lighten(body, 0.5)} basic s={false} />
        </>
      );
    case "felt": {
      const n = 9;
      return (
        <>
          <Bx p={[0, h / 2 + 0.08, 0]} a={[w, h - 0.16, 0.1]} rad={0.04} c={body} f="felt" />
          {Array.from({ length: n }).map((_, i) => <Bx key={i} p={[-w / 2 + (w / n) * (i + 0.5), h / 2 + 0.08, 0.052]} a={[0.012, h - 0.3, 0.006]} c={darken(body, 0.22)} f="felt" s={false} />)}
          {[-1, 1].map((s) => (
            <group key={s} position={[s * (w / 2 - 0.2), 0, 0]}>
              {[-0.12, 0.12].map((z) => <Cy key={z} p={[0, 0.04, z]} t={0.018} b={0.014} h={0.08} n={8} c={fr} f="wood" />)}
              <Bx p={[0, 0.085, 0]} a={[0.05, 0.016, 0.3]} c={fr} f="wood" />
            </group>
          ))}
        </>
      );
    }
    case "plants": {
      const green = ["#4f8a62", "#5f9a62", "#3f7a52", "#6aa86a"], r = rnd(item.id, 24);
      return (
        <>
          {[-1, 0, 1].map((i) => (
            <group key={i} position={[i * (w / 3), 0, 0]}>
              <Bx p={[0, 0.2, 0]} a={[w / 3 - 0.06, 0.4, d]} c={body} f="wood" />
              <Bx p={[0, 0.405, 0]} a={[w / 3 - 0.1, 0.02, d - 0.06]} c="#4a3a2c" f="fabric" s={false} />
              {Array.from({ length: 4 }).map((_, k) => <Sp key={k} p={[(r[i * 4 + k + 4] - 0.5) * 0.4, 0.62 + r[i * 4 + k + 8] * 0.9, (r[i * 4 + k] - 0.5) * 0.1]} rad={0.2} sc={[1, 0.85, 0.8]} c={green[k % 4]} f="fabric" n={9} />)}
            </group>
          ))}
          {[-0.6, 0, 0.6].map((x) => <Bx key={x} p={[x * (w / 1.8), 1.0, -d / 2 + 0.03]} a={[0.02, 1.2, 0.02]} c={fr} f="wood" />)}
          {[0.7, 1.0, 1.3].map((y) => <Bx key={y} p={[0, y, -d / 2 + 0.03]} a={[w - 0.2, 0.014, 0.014]} c={fr} f="wood" s={false} />)}
        </>
      );
    }
    case "living": {
      const cols = 7, rows = 8, r = rnd(item.id, cols * rows * 2), green = ["#3f7a52", "#4f8a62", "#5f9a62", "#6aa86a", "#2f6a46"];
      return (
        <>
          <Bx p={[0, h / 2 + 0.04, -0.02]} a={[w, h - 0.08, d - 0.04]} c="#2c323b" f="frame" />
          <Bx p={[0, 0.02, 0]} a={[w + 0.04, 0.04, d + 0.02]} c={fr} f="frame" />
          <Bx p={[0, h - 0.02, 0]} a={[w + 0.04, 0.04, d + 0.02]} c={fr} f="frame" />
          {Array.from({ length: cols * rows }).map((_, k) => {
            const x = -w / 2 + 0.12 + ((k % cols) * (w - 0.24)) / (cols - 1) + (r[k] - 0.5) * 0.06, y = 0.18 + (Math.floor(k / cols) * (h - 0.36)) / (rows - 1) + (r[k + 56] - 0.5) * 0.06;
            return <Sp key={k} p={[x, y, d / 2 - 0.05 + r[k] * 0.04]} rad={0.1 + r[k + 56] * 0.04} sc={[1, 0.9, 0.55]} c={green[k % green.length]} f="fabric" n={7} />;
          })}
          {[0.25, 0.55, 0.8].map((v, i) => <Sp key={i} p={[-w / 2 + w * v, h * (0.3 + i * 0.22), d / 2 + 0.02]} rad={0.035} c={["#ee7aa8", "#f2c94c", "#f4f6f8"][i]} f="plastic" n={8} />)}
        </>
      );
    }
  }
}

// ================================================================ whiteboards (wall)

function Whiteboard({ item }: { item: ShopItem }) {
  const sp = wbSpec(item);
  const c1 = tone(item, "c1"), c2 = tone(item, "c2"), { w, h } = sp, z = -0.03;
  const notes = ["#f2c94c", "#ee7aa8", "#7fc8f0", "#9ad87a"];
  switch (sp.kind) {
    case "white":
      return (
        <>
          <Bx p={[0, h / 2, z + 0.02]} a={[w, h, 0.04]} rad={0.01} c={c2} f="metal" />
          <Bx p={[0, h / 2, z + 0.044]} a={[w - 0.06, h - 0.06, 0.008]} c={c1} f="lacquer" />
          <Bx p={[-0.4, h * 0.62, z + 0.05]} a={[0.36, 0.2, 0.002]} c="#5a95e8" basic s={false} />
          <Bx p={[0.25, h * 0.62, z + 0.05]} a={[0.36, 0.2, 0.002]} c="#d9534f" basic s={false} />
          <Bx p={[-0.075, h * 0.62, z + 0.05]} a={[0.24, 0.012, 0.002]} c="#2c323b" basic s={false} />
          <Bx p={[-0.2, h * 0.3, z + 0.05]} a={[0.9, 0.012, 0.002]} c="#2c323b" basic s={false} />
          <Bx p={[0, 0.0, z + 0.06]} a={[w * 0.6, 0.03, 0.06]} c={c2} f="metal" />
          {[-0.2, -0.1, 0.05].map((x, i) => <Cy key={x} p={[x, 0.03, z + 0.07]} r={[0, 0, Math.PI / 2]} t={0.012} h={0.1} n={8} c={["#2c323b", "#d9534f", "#5a95e8"][i]} f="plastic" />)}
        </>
      );
    case "cork": {
      const r = rnd(item.id, 12);
      return (
        <>
          <Bx p={[0, h / 2, z + 0.02]} a={[w, h, 0.04]} rad={0.012} c={c2} f="wood" />
          <Bx p={[0, h / 2, z + 0.042]} a={[w - 0.08, h - 0.08, 0.006]} c={c1} f="fabric" />
          {Array.from({ length: 6 }).map((_, i) => (
            <group key={i} position={[-w / 2 + 0.2 + (i % 3) * (w - 0.4) / 2 + (r[i] - 0.5) * 0.1, h * (i < 3 ? 0.68 : 0.32) + (r[i + 6] - 0.5) * 0.08, z + 0.05]} rotation={[0, 0, (r[i] - 0.5) * 0.4]}>
              <Bx a={[0.2, 0.16, 0.004]} c={i % 2 ? "#fffaf0" : notes[i % 4]} f="plastic" s={false} />
              <Sp p={[0, 0.06, 0.006]} rad={0.012} c="#d9534f" f="plastic" n={6} />
            </group>
          ))}
        </>
      );
    }
    case "chalk":
      return (
        <>
          <Bx p={[0, h / 2, z + 0.025]} a={[w, h, 0.05]} rad={0.012} c={c2} f="wood" />
          <Bx p={[0, h / 2, z + 0.052]} a={[w - 0.1, h - 0.1, 0.006]} c={c1} f="fabric" />
          <Seg a={[-0.45, h * 0.62]} b={[0.1, h * 0.62]} z={z + 0.058} w={0.01} c="#e9eef5" />
          <Seg a={[-0.45, h * 0.46]} b={[0.3, h * 0.46]} z={z + 0.058} w={0.01} c="#e9eef5" />
          <Seg a={[-0.45, h * 0.3]} b={[-0.05, h * 0.3]} z={z + 0.058} w={0.01} c="#f2c94c" />
          <mesh position={[0.35, h * 0.72, z + 0.058]}><torusGeometry args={[0.07, 0.006, 5, 18]} /><meshBasicMaterial color="#e9eef5" toneMapped={false} /></mesh>
          <Bx p={[0, 0.0, z + 0.06]} a={[w * 0.7, 0.025, 0.05]} c={c2} f="wood" />
          <Bx p={[0.2, 0.025, z + 0.065]} a={[0.05, 0.015, 0.015]} c="#f4f6f8" f="plastic" s={false} />
        </>
      );
    case "kanban": {
      const cw = (w - 0.1) / 3;
      return (
        <>
          <Bx p={[0, h / 2, z + 0.02]} a={[w, h, 0.04]} rad={0.01} c={darken(c1, 0.3)} f="metal" />
          <Bx p={[0, h / 2, z + 0.044]} a={[w - 0.06, h - 0.06, 0.008]} c={c1} f="lacquer" />
          {[-1, 0, 1].map((i) => <Bx key={i} p={[i * cw, h - 0.14, z + 0.05]} a={[cw - 0.08, 0.06, 0.002]} c={["#9aa6ba", "#5a95e8", "#4cb987"][i + 1]} basic s={false} />)}
          {[-0.5, 0.5].map((x) => <Bx key={x} p={[x * cw * 1.0 + (x < 0 ? -0.0 : 0) + (x > 0 ? 0 : 0) , h / 2 - 0.05, z + 0.05]} a={[0.006, h - 0.3, 0.002]} c="#9aa6ba" basic s={false} />)}
          {[0, 1, 2, 3, 4, 5, 6].map((k) => {
            const col = k < 3 ? -1 : k < 5 ? 0 : 1, row = k < 3 ? k : k < 5 ? k - 3 : k - 5;
            return <Bx key={k} p={[col * cw + (row % 2 ? 0.03 : -0.03), h - 0.3 - row * 0.2, z + 0.052]} r={[0, 0, (row % 2 ? 1 : -1) * 0.04]} a={[0.18, 0.15, 0.004]} c={notes[(k + col + 1) % 4]} f="plastic" s={false} />;
          })}
        </>
      );
    }
    case "glass":
      return (
        <>
          <Bx p={[0, h / 2, z + 0.03]} a={[w, h, 0.012]} c={c1} f="glass" x={{ opacity: 0.38 }} />
          {[[-1, -1], [1, -1], [-1, 1], [1, 1]].map(([sx, sy]) => <Cy key={`${sx}${sy}`} p={[sx * (w / 2 - 0.04), h / 2 + sy * (h / 2 - 0.04), z + 0.015]} r={[Math.PI / 2, 0, 0]} t={0.022} h={0.03} n={12} c={c2} f="metal" />)}
          <Bx p={[-0.3, h * 0.62, z + 0.04]} a={[0.3, 0.18, 0.002]} c={c2} basic s={false} />
          <Bx p={[0.25, h * 0.45, z + 0.04]} a={[0.5, 0.01, 0.002]} c="#ee4fa0" basic s={false} />
          <Bx p={[-0.1, h * 0.25, z + 0.04]} a={[0.7, 0.01, 0.002]} c="#2c323b" basic s={false} />
          <Bx p={[0, 0, z + 0.05]} a={[w * 0.4, 0.02, 0.04]} c={c2} f="metal" />
        </>
      );
    case "smart": {
      const bars = [0.35, 0.55, 0.42, 0.7, 0.5];
      return (
        <>
          <Bx p={[0, h / 2, z + 0.03]} a={[w, h, 0.06]} rad={0.02} c={c1} f="plastic" />
          <Bx p={[0, h / 2, z + 0.062]} a={[w - 0.1, h - 0.1, 0.002]} c="#0f1a2a" basic s={false} />
          {bars.map((v, i) => <Bx key={i} p={[-0.55 + i * 0.16, h * 0.2 + (v * h * 0.5) / 2, z + 0.064]} a={[0.1, v * h * 0.5, 0.002]} c={i === 3 ? c2 : "#5a95e8"} basic s={false} />)}
          <Seg a={[0.1, h * 0.55]} b={[0.4, h * 0.65]} z={z + 0.064} w={0.015} c={c2} />
          <Seg a={[0.4, h * 0.65]} b={[0.65, h * 0.5]} z={z + 0.064} w={0.015} c={c2} />
          <Bx p={[0.45, h * 0.85, z + 0.064]} a={[0.4, 0.05, 0.002]} c="#e9eef5" basic s={false} />
          <Bx p={[0, 0.015, z + 0.05]} a={[w * 0.35, 0.03, 0.04]} c={c1} f="plastic" />
        </>
      );
    }
  }
}

// ================================================================ appliances

function Appliance({ item }: { item: ShopItem }) {
  const sp = applSpec(item);
  const c1 = tone(item, "c1"), c2 = tone(item, "c2"), { w, d, h } = sp;
  switch (sp.kind) {
    case "printer":
      return (
        <>
          <Bx p={[0, 0.3, 0]} a={[0.6, 0.6, 0.5]} rad={0.012} c="#cfd5dd" f="frame" />
          <Bx p={[0, 0.3, 0.255]} a={[0.54, 0.54, 0.008]} c="#b9c1cc" f="frame" />
          <Bx p={[0, 0.3, 0.262]} a={[0.01, 0.5, 0.004]} c="#8a929e" f="plastic" s={false} />
          <Bx p={[0.22, 0.31, 0.27]} a={[0.02, 0.1, 0.02]} c={STEEL} f="metal" />
          <Bx p={[0, 0.77, 0]} a={[0.66, 0.34, 0.54]} rad={0.03} c={c1} f="plastic" />
          <Bx p={[0.18, 0.88, 0.2]} r={[-0.35, 0, 0]} a={[0.2, 0.07, 0.02]} c="#1d2128" f="plastic" s={false} />
          <Bx p={[0.18, 0.885, 0.215]} r={[-0.35, 0, 0]} a={[0.12, 0.03, 0.004]} c="#7fc8f0" basic s={false} />
          <Sp p={[0.3, 0.83, 0.24]} rad={0.012} c="#4cb987" basic n={6} />
          <Bx p={[0, 0.68, 0.34]} r={[0.25, 0, 0]} a={[0.5, 0.012, 0.18]} c={darken(c1, 0.12)} f="plastic" />
          <Bx p={[-0.05, 0.69, 0.38]} r={[0.25, 0, 0]} a={[0.36, 0.006, 0.12]} c="#ffffff" f="plastic" s={false} />
          <Bx p={[0, 0.96, -0.1]} a={[0.5, 0.01, 0.2]} c="#ffffff" f="plastic" s={false} />
        </>
      );
    case "shredder":
      return (
        <>
          <Bx p={[0, 0.27, 0]} a={[0.42, 0.46, 0.3]} rad={0.02} c={c1} f="plastic" />
          <Bx p={[0, 0.54, 0]} a={[w, 0.12, d]} rad={0.03} c={c2} f="metal" />
          <Bx p={[0, 0.605, 0]} a={[0.3, 0.012, 0.025]} c="#0c0e12" basic s={false} />
          <Bx p={[0, 0.34, d / 2 - 0.004]} a={[0.24, 0.16, 0.008]} c="#cfe6ee" f="glass" />
          <Bx p={[0.14, 0.52, d / 2 + 0.003]} a={[0.03, 0.025, 0.012]} c="#d9534f" basic s={false} />
          {[-1, 1].map((s) => <Sp key={s} p={[s * 0.17, 0.025, 0.1]} rad={0.025} c="#23272e" n={8} />)}
          {[-1, 1].map((s) => <Sp key={s} p={[s * 0.17, 0.025, -0.1]} rad={0.025} c="#23272e" n={8} />)}
        </>
      );
    case "coatrack": {
      const coat = ["#4a5568", "#8c2f45", "#2f6b4f"][hash(item.id) % 3];
      return (
        <>
          <Cy p={[0, 0.015, 0]} t={0.26} b={0.28} h={0.03} n={22} c={c2} f="metal" />
          <Cy p={[0, 0.9, 0]} t={0.022} h={1.75} n={10} c={c1} f="wood" />
          {Array.from({ length: 6 }).map((_, i) => {
            const a = (i / 6) * Math.PI * 2, lo = i % 2;
            return (
              <group key={i} position={[0, 1.62 - lo * 0.12, 0]} rotation={[0, a, 0]}>
                <Cy p={[0, 0.05, 0.1]} r={[Math.PI / 2 - 0.5, 0, 0]} t={0.012} h={0.22} n={6} c={c2} f="metal" />
                <Sp p={[0, 0.12, 0.2]} rad={0.022} c={c2} f="metal" n={8} />
              </group>
            );
          })}
          <Cy p={[0, 1.76, 0]} t={0.03} b={0.022} h={0.04} c={c2} f="metal" />
          <Bx p={[0.2, 1.38, 0.0]} r={[0, 0.3, 0.05]} a={[0.1, 0.62, 0.28]} rad={0.03} c={coat} f="fabric" />
          <Sp p={[0.13, 1.7, 0.1]} rad={0.1} sc={[1, 0.45, 1]} c="#c9a24a" f="fabric" n={10} />
        </>
      );
    }
    case "bin":
      return (
        <>
          {[-1, 1].map((s) => (
            <group key={s} position={[s * 0.22, 0, 0]}>
              <Cy p={[0, 0.34, 0]} t={0.17} b={0.14} h={0.68} n={22} c={s < 0 ? c1 : c2} f="plastic" />
              <Cy p={[0, 0.7, 0]} t={0.185} b={0.18} h={0.05} n={22} c={darken(s < 0 ? c1 : c2, 0.2)} f="plastic" />
              <Bx p={[0, 0.69, 0.12]} a={[0.16, 0.02, 0.1]} c="#0c0e12" f="plastic" s={false} />
              <mesh position={[0, 0.38, 0.153]} rotation={[0, 0, s * 0.1]}><torusGeometry args={[0.06, 0.009, 4, 3]} /><meshBasicMaterial color="#ffffff" toneMapped={false} /></mesh>
              <Bx p={[0, 0.2, 0.155]} a={[0.13, 0.05, 0.004]} c="#ffffff" basic s={false} />
            </group>
          ))}
        </>
      );
    case "cooler":
      return (
        <>
          <Bx p={[0, 0.48, 0]} a={[0.36, 0.96, 0.36]} rad={0.02} c={c1} f="plastic" />
          <Bx p={[0, 0.58, 0.185]} a={[0.3, 0.18, 0.008]} c="#2c323b" f="plastic" />
          <Cy p={[-0.06, 0.62, 0.21]} r={[Math.PI / 2, 0, 0]} t={0.016} h={0.05} c="#5a95e8" f="plastic" />
          <Cy p={[0.06, 0.62, 0.21]} r={[Math.PI / 2, 0, 0]} t={0.016} h={0.05} c="#d9534f" f="plastic" />
          <Bx p={[0, 0.45, 0.2]} a={[0.2, 0.012, 0.06]} c="#2c323b" f="metal" />
          <Bx p={[0, 0.96, 0]} a={[0.37, 0.03, 0.37]} c={darken(c1, 0.1)} f="plastic" />
          <Cy p={[0, 1.07, 0]} t={0.15} b={0.15} h={0.26} n={20} c={c2} f="glass" x={{ opacity: 0.55 }} />
          <Cy p={[0, 1.04, 0]} t={0.14} b={0.14} h={0.18} n={20} c="#7fc8f0" f="glass" x={{ opacity: 0.5 }} s={false} />
          <Cy p={[0, 1.2, 0]} t={0.05} b={0.1} h={0.06} n={16} c={c2} f="glass" x={{ opacity: 0.6 }} />
        </>
      );
    case "purifier":
      return (
        <>
          <Cy p={[0, 0.43, 0]} t={0.15} b={0.16} h={0.86} n={26} c={c1} f="plastic" />
          <Cy p={[0, 0.02, 0]} t={0.17} b={0.17} h={0.04} n={26} c="#2c323b" f="plastic" />
          <Cy p={[0, 0.89, 0]} t={0.15} b={0.15} h={0.06} n={26} c="#2c323b" f="plastic" />
          {[-0.08, -0.03, 0.02, 0.07].map((x) => <Bx key={x} p={[x, 0.921, 0]} a={[0.012, 0.004, 0.26]} c="#14181e" f="plastic" s={false} />)}
          {Array.from({ length: 8 }).map((_, i) => <Bx key={i} p={[0, 0.5 + (i - 3.5) * 0.045, 0.151]} a={[0.18, 0.012, 0.01]} c={darken(c1, 0.3)} f="plastic" s={false} />)}
          <Sp p={[0.08, 0.76, 0.15]} rad={0.014} c={c2} basic n={8} />
          <mesh position={[0, 0.2, 0.152]}><circleGeometry args={[0.04, 16]} /><meshBasicMaterial color={c2} toneMapped={false} /></mesh>
        </>
      );
    case "fridge":
      return (
        <>
          <Bx p={[0, h / 2 + 0.02, 0]} a={[w, h - 0.04, d]} rad={0.04} c={c1} f="metal" x={{ roughness: 0.4, metalness: 0.4 }} />
          <Bx p={[0, 0.05, 0.02]} a={[w - 0.06, 0.1, d - 0.06]} c="#23272e" f="plastic" />
          <Bx p={[0, h * 0.72, d / 2 + 0.002]} a={[w - 0.02, 0.012, 0.006]} c={darken(c1, 0.5)} f="plastic" s={false} />
          <Bx p={[-w / 2 + 0.06, h * 0.82, d / 2 + 0.03]} a={[0.025, 0.4, 0.03]} c={c2} f="metal" />
          <Bx p={[-w / 2 + 0.06, h * 0.45, d / 2 + 0.03]} a={[0.025, 0.55, 0.03]} c={c2} f="metal" />
          <Bx p={[0.18, h * 0.9, d / 2 + 0.006]} a={[0.1, 0.03, 0.004]} c="#7fc8f0" basic s={false} />
        </>
      );
    case "copier":
      return (
        <>
          <Bx p={[0, 0.4, 0]} a={[w, 0.74, d]} rad={0.02} c={c1} f="plastic" />
          <Bx p={[0, 0.8, -0.02]} a={[w, 0.08, d - 0.04]} rad={0.015} c={c2} f="plastic" />
          <Bx p={[0, 0.848, -0.02]} a={[w - 0.1, 0.012, d - 0.14]} c="#cfe6ee" f="glass" />
          <Bx p={[0, 0.9, d / 2 - 0.12]} r={[-0.3, 0, 0]} a={[0.5, 0.12, 0.05]} c="#1d2128" f="plastic" />
          <Bx p={[0, 0.9, d / 2 - 0.097]} r={[-0.3, 0, 0]} a={[0.26, 0.08, 0.004]} c="#7fc8f0" basic s={false} />
          {[0, 1].map((i) => <Bx key={i} p={[0, 0.12 + i * 0.22, d / 2 + 0.004]} a={[w - 0.1, 0.18, 0.012]} rad={0.01} c={darken(c1, 0.08)} f="plastic" />)}
          {[0, 1].map((i) => <Bx key={i} p={[0, 0.17 + i * 0.22, d / 2 + 0.014]} a={[0.2, 0.016, 0.012]} c={STEEL} f="metal" />)}
          <Bx p={[w / 2 - 0.05, 0.6, d / 2 + 0.003]} a={[0.12, 0.02, 0.006]} c="#0c0e12" f="plastic" s={false} />
          {[-1, 1].map((s) => <Sp key={s} p={[s * 0.38, 0.02, 0.3]} rad={0.025} c="#23272e" n={8} />)}
        </>
      );
    case "safe":
      return (
        <>
          <Bx p={[0, 0.04 + 0.33, 0]} a={[w, 0.66, d]} rad={0.02} c={c1} f="metal" />
          <Bx p={[0, 0.37, d / 2 + 0.004]} a={[w - 0.1, 0.56, 0.016]} rad={0.01} c={lighten(c1, 0.12)} f="metal" />
          <Cy p={[-0.0, 0.4, d / 2 + 0.03]} r={[Math.PI / 2, 0, 0]} t={0.085} b={0.085} h={0.03} n={24} c={c2} f="metal" />
          <Cy p={[0, 0.4, d / 2 + 0.05]} r={[Math.PI / 2, 0, 0]} t={0.03} h={0.03} n={12} c="#23272e" f="plastic" />
          {Array.from({ length: 10 }).map((_, i) => <Sp key={i} p={[Math.sin((i / 10) * 6.28) * 0.07, 0.4 + Math.cos((i / 10) * 6.28) * 0.07, d / 2 + 0.047]} rad={0.006} c="#23272e" n={4} />)}
          <Bx p={[0.16, 0.25, d / 2 + 0.03]} a={[0.1, 0.03, 0.03]} c={c2} f="metal" />
          <Bx p={[0.16, 0.5, d / 2 + 0.02]} a={[0.1, 0.04, 0.004]} c="#0f1a2a" basic s={false} />
          {[0.28, 0.5].map((y) => <Cy key={y} p={[-w / 2 + 0.01, y, d / 2 - 0.02]} t={0.014} h={0.08} n={8} c={c2} f="metal" />)}
          {[-1, 1].map((s) => [-1, 1].map((t) => <Bx key={`${s}${t}`} p={[s * (w / 2 - 0.05), 0.02, t * (d / 2 - 0.05)]} a={[0.06, 0.04, 0.06]} c="#23272e" f="plastic" />))}
        </>
      );
    case "vending": {
      const r = rnd(item.id, 40);
      const cols = ["#d9534f", "#f2c94c", "#5a95e8", "#4cb987", "#f4895a", "#8f78c9"];
      return (
        <>
          <Bx p={[0, h / 2, 0]} a={[w, h, d]} rad={0.03} c={c1} f="plastic" />
          <Bx p={[-0.12, 1.12, d / 2 - 0.005]} a={[0.62, 1.26, 0.012]} c="#14181e" f="plastic" />
          {[0, 1, 2, 3, 4].map((row) => (
            <group key={row} position={[-0.12, 0.64 + row * 0.24, d / 2 - 0.12]}>
              <Bx p={[0, -0.02, 0]} a={[0.58, 0.012, 0.2]} c="#8a929e" f="metal" s={false} />
              {[0, 1, 2, 3].map((k) => <Bx key={k} p={[-0.22 + k * 0.145, 0.07, 0]} a={[0.1, 0.13 + r[row * 4 + k] * 0.05, 0.1]} rad={0.012} c={cols[Math.floor(r[row * 4 + k + 20] * 6)]} f="plastic" s={false} />)}
            </group>
          ))}
          <Bx p={[-0.12, 1.12, d / 2 + 0.006]} a={[0.62, 1.26, 0.006]} c="#cfe6ee" f="glass" x={{ opacity: 0.18 }} s={false} />
          <Bx p={[0.34, 1.15, d / 2 + 0.006]} a={[0.2, 0.62, 0.02]} c="#2c323b" f="plastic" />
          {[0, 1, 2].map((i) => [0, 1, 2].map((j) => <Bx key={`${i}${j}`} p={[0.285 + i * 0.055, 1.28 - j * 0.07, d / 2 + 0.02]} a={[0.04, 0.04, 0.01]} c={c2} basic s={false} />))}
          <Bx p={[0.34, 1.0, d / 2 + 0.02]} a={[0.1, 0.02, 0.004]} c="#0c0e12" basic s={false} />
          <Bx p={[-0.04, 0.3, d / 2 - 0.004]} a={[0.7, 0.2, 0.014]} c="#1d2128" f="plastic" />
          <Bx p={[-0.04, 0.3, d / 2 + 0.006]} a={[0.5, 0.12, 0.004]} c="#0c0e12" f="plastic" s={false} />
          <Bx p={[0, h - 0.1, d / 2 + 0.002]} a={[w - 0.1, 0.12, 0.01]} c={c2} basic s={false} />
          <Bx p={[0, 0.05, 0.02]} a={[w - 0.06, 0.1, d - 0.06]} c="#23272e" f="plastic" />
        </>
      );
    }
  }
}

// ================================================================ lights

const WARM = "#ffd9a0";
function Glow({ c, p, rad }: { c: string; p: V3; rad: number }) {
  return (
    <>
      <Sp p={p} rad={rad} c={c} basic n={10} s={false} />
      <mesh position={p}><sphereGeometry args={[rad * 2, 10, 8]} /><meshBasicMaterial color={c} transparent opacity={0.16} depthWrite={false} toneMapped={false} /></mesh>
    </>
  );
}
function Cord({ y, c }: { y: number; c: string }) {
  return (
    <>
      <Cy p={[0, (y + CEIL) / 2, 0]} t={0.005} h={CEIL - y} n={5} c={c} f="plastic" s={false} />
      <Cy p={[0, CEIL - 0.02, 0]} t={0.05} h={0.04} n={14} c={c} f="metal" />
    </>
  );
}

function Light({ item }: { item: ShopItem }) {
  const sp = lightSpec(item);
  const c1 = tone(item, "c1"), c2 = tone(item, "c2"), g = sp.glow ? tone(item, sp.glow.tone, WARM) : WARM;
  switch (sp.kind) {
    case "sconce":
      return (
        <>
          <Cy p={[0, 0.15, -0.075]} r={[Math.PI / 2, 0, 0]} t={0.07} h={0.02} n={18} c={c1} f="metal" />
          <Cy p={[0, 0.15, -0.035]} r={[Math.PI / 2, 0, 0]} t={0.015} h={0.07} n={8} c={c1} f="metal" />
          <Cy p={[0, 0.2, 0.0]} t={0.1} b={0.055} h={0.2} n={18} open c={c1} f="metal" x={{ emissive: g, emissiveIntensity: 0.35 }} />
          <Glow c={g} p={[0, 0.2, 0]} rad={0.035} />
        </>
      );
    case "cone":
      return (
        <>
          <Cord y={CEIL - 0.9} c={c2} />
          <Cy p={[0, CEIL - 1.02, 0]} t={0.04} b={0.26} h={0.24} n={22} open c={c1} f="metal" x={{ emissive: g, emissiveIntensity: 0.25 }} />
          <Cy p={[0, CEIL - 0.9, 0]} t={0.03} h={0.05} n={10} c={c2} f="metal" />
          <Glow c={g} p={[0, CEIL - 1.1, 0]} rad={0.05} />
          <Cy p={[0, CEIL - 1.133, 0]} t={0.255} h={0.004} n={22} c="#fff1d6" basic s={false} />
        </>
      );
    case "table":
      return (
        <>
          <Cy p={[0, 0.48, 0]} t={0.24} h={0.03} n={24} c={c2} f="wood" />
          <Legs4 x={0.13} z={0.13} h={0.47} c={c2} f="wood" t={0.02} b={0.015} sp={0.12} />
          <Cy p={[0, 0.54, 0]} t={0.07} b={0.08} h={0.09} n={14} c={c1} f="metal" />
          <Cy p={[0, 0.72, 0]} t={0.012} h={0.26} n={8} c={c1} f="metal" />
          <Cy p={[0, 0.84, 0]} t={0.1} b={0.16} h={0.2} n={20} open c="#f4efe6" f="fabric" x={{ emissive: g, emissiveIntensity: 0.35 }} />
          <Glow c={g} p={[0, 0.84, 0]} rad={0.04} />
        </>
      );
    case "string": {
      const n = 9, pts = Array.from({ length: n }, (_, i): [number, number] => { const t = i / (n - 1); return [-0.95 + t * 1.9, 0.3 - 0.2 * (1 - (2 * t - 1) ** 2)]; });
      return (
        <>
          {pts.slice(1).map((p, i) => <Seg key={i} a={pts[i]} b={p} z={-0.01} w={0.006} c="#23272e" />)}
          {pts.map((p, i) => i > 0 && i < n - 1 && <group key={i} position={[p[0], p[1] - 0.045, -0.01]}><Glow c={i % 2 ? g : "#fff1d6"} p={[0, 0, 0]} rad={0.028} /></group>)}
          {[0, n - 1].map((i) => <Sp key={i} p={[pts[i][0], pts[i][1], -0.02]} rad={0.015} c={c1} f="metal" n={6} />)}
        </>
      );
    }
    case "uplight":
      return (
        <>
          <Cy p={[0, 0.015, 0]} t={0.17} b={0.18} h={0.03} n={24} c={c2} f="metal" />
          <Cy p={[0, 0.8, 0]} t={0.013} h={1.56} n={8} c={c2} f="metal" />
          <Cy p={[0, 1.62, 0]} t={0.17} b={0.06} h={0.24} n={22} open c={c1} f="lacquer" x={{ emissive: g, emissiveIntensity: 0.2 }} />
          <mesh position={[0, 1.71, 0]} rotation={[-Math.PI / 2, 0, 0]}><circleGeometry args={[0.165, 20]} /><meshBasicMaterial color="#fff1d6" toneMapped={false} /></mesh>
          <Glow c={g} p={[0, 1.66, 0]} rad={0.04} />
        </>
      );
    case "globe":
      return (
        <>
          <Cord y={CEIL - 1.0} c={c2} />
          <Cy p={[0, CEIL - 0.92, 0]} t={0.035} b={0.05} h={0.1} n={12} c={c2} f="metal" />
          <Sp p={[0, CEIL - 1.18, 0]} rad={0.22} n={22} c={c1} f="glass" x={{ opacity: 0.85, emissive: g, emissiveIntensity: 0.55, transparent: true }} s={false} />
          <Glow c={g} p={[0, CEIL - 1.18, 0]} rad={0.07} />
        </>
      );
    case "neon": {
      const w = 0.95, h = 0.6, z = 0.012, c = c1;
      const outline: [[number, number], [number, number]][] = [[[0.025, 0.025], [w - 0.025, 0.025]], [[w - 0.025, 0.025], [w - 0.025, h - 0.025]], [[w - 0.025, h - 0.025], [0.025, h - 0.025]], [[0.025, h - 0.025], [0.025, 0.025]]];
      const bolt: [number, number][] = [[0.56, 0.52], [0.36, 0.3], [0.52, 0.3], [0.38, 0.09]];
      const o = (p: [number, number]): [number, number] => [p[0] - w / 2, p[1]];
      return (
        <>
          <Bx p={[0, h / 2, -0.015]} a={[w + 0.06, h + 0.06, 0.012]} c="#14181e" f="glass" x={{ opacity: 0.5 }} s={false} />
          {outline.map(([a, b], i) => <Seg key={i} a={o(a)} b={o(b)} z={z} w={0.022} c={c} />)}
          {outline.map(([a, b], i) => <Seg key={`g${i}`} a={o(a)} b={o(b)} z={z - 0.004} w={0.06} c={c} basic />)}
          {bolt.slice(1).map((p, i) => <Seg key={`b${i}`} a={o(bolt[i])} b={o(p)} z={z} w={0.024} c={lighten(c, 0.35)} />)}
          {[0.1, 0.9].map((v) => <Cy key={v} p={[(v - 0.5) * w, 0.0, -0.02]} r={[Math.PI / 2, 0, 0]} t={0.01} h={0.03} n={6} c="#8a929e" f="metal" />)}
        </>
      );
    }
    case "arc":
      return (
        <>
          <Bx p={[-0.5, 0.06, 0]} a={[0.4, 0.12, 0.28]} rad={0.012} c="#e9e6e0" f="stone" />
          <Cy p={[-0.5, 0.75, 0]} t={0.014} h={1.4} n={8} c={c2} f="metal" />
          <group position={[-0.4, 1.45, 0]} rotation={[0, 0, Math.PI / 4]}>
            <mesh castShadow><torusGeometry args={[0.6, 0.014, 6, 30, (Math.PI * 3) / 4]} />{M(c2, "metal")}</mesh>
          </group>
          <Cy p={[0.52, 1.8, 0]} t={0.03} h={0.1} n={10} c={c2} f="metal" />
          <Cy p={[0.52, 1.68, 0]} t={0.05} b={0.18} h={0.2} n={22} open c={c1} f="lacquer" x={{ emissive: g, emissiveIntensity: 0.25 }} />
          <Glow c={g} p={[0.52, 1.64, 0]} rad={0.045} />
        </>
      );
    case "chandelier": {
      const ring = Array.from({ length: 8 }, (_, i) => (i / 8) * Math.PI * 2), crystals = rnd(item.id, 16);
      return (
        <>
          <Cord y={CEIL - 0.55} c={c2} />
          <Cy p={[0, CEIL - 0.7, 0]} t={0.04} b={0.07} h={0.3} n={12} c={c2} f="metal" />
          <mesh position={[0, CEIL - 0.78, 0]} rotation={[Math.PI / 2, 0, 0]}><torusGeometry args={[0.4, 0.016, 6, 32]} />{M(c2, "metal")}</mesh>
          <mesh position={[0, CEIL - 0.6, 0]} rotation={[Math.PI / 2, 0, 0]}><torusGeometry args={[0.2, 0.012, 6, 24]} />{M(c2, "metal")}</mesh>
          {[0, Math.PI / 2, Math.PI, -Math.PI / 2].map((a) => <Bx key={a} p={[Math.sin(a) * 0.1, CEIL - 0.6, Math.cos(a) * 0.1]} r={[0, a, 0]} a={[0.012, 0.012, 0.2]} c={c2} f="metal" s={false} />)}
          {ring.map((a, i) => (
            <group key={i} position={[Math.sin(a) * 0.4, CEIL - 0.78, Math.cos(a) * 0.4]}>
              <Cy p={[0, 0.05, 0]} t={0.016} h={0.1} n={8} c="#fffaf0" f="plastic" />
              <Sp p={[0, 0.115, 0]} rad={0.016} sc={[1, 1.5, 1]} c="#ffe9a8" basic n={6} s={false} />
              <mesh position={[0, -0.07 - crystals[i] * 0.05, 0]}><octahedronGeometry args={[0.03 + crystals[i + 8] * 0.015]} /><meshStandardMaterial color="#d9efff" roughness={0.05} transparent opacity={0.75} /></mesh>
            </group>
          ))}
          <Glow c={g} p={[0, CEIL - 0.74, 0]} rad={0.08} />
          <mesh position={[0, CEIL - 1.0, 0]}><octahedronGeometry args={[0.07]} /><meshStandardMaterial color="#d9efff" roughness={0.05} transparent opacity={0.75} /></mesh>
        </>
      );
    }
    case "moon": {
      const r = rnd(item.id, 8);
      return (
        <>
          <Cy p={[0, 0.3, -0.065]} r={[Math.PI / 2, 0, 0]} t={0.07} h={0.012} n={14} c="#8a929e" f="metal" />
          <mesh position={[0, 0.3, -0.07]} rotation={[Math.PI / 2, 0, 0]} scale={[1, 0.45, 1]}>
            <sphereGeometry args={[0.28, 28, 14, 0, Math.PI * 2, 0, Math.PI / 2]} />
            <meshStandardMaterial color={c1} emissive={c1} emissiveIntensity={0.55} roughness={0.9} />
          </mesh>
          {[[0.07, 0.4, 0.07], [-0.1, 0.25, 0.05], [0.1, 0.22, 0.045], [-0.05, 0.4, 0.04]].map(([x, y, z], i) => (
            <mesh key={i} position={[x, y, z]} scale={[1, 1, 0.3]}><sphereGeometry args={[0.03 + r[i] * 0.025, 10, 6]} /><meshStandardMaterial color={darken(c1, 0.18)} emissive={darken(c1, 0.3)} emissiveIntensity={0.3} roughness={1} /></mesh>
          ))}
          <mesh position={[0, 0.3, -0.07]} scale={[1, 1, 0.5]}><sphereGeometry args={[0.36, 14, 8]} /><meshBasicMaterial color={g} transparent opacity={0.1} depthWrite={false} toneMapped={false} /></mesh>
        </>
      );
    }
  }
}

// ================================================================ the contract

/** Table, sofa, cabinet, divider, whiteboard, appliance or light, from its spec. */
export function PieceMesh({ item }: { item: ShopItem }) {
  switch (item.category) {
    case "table": return <Table item={item} />;
    case "sofa": return <Sofa item={item} />;
    case "cabinet": return <Cabinet item={item} />;
    case "divider": return <Divider item={item} />;
    case "whiteboard": return <Whiteboard item={item} />;
    case "appliance": return <Appliance item={item} />;
    case "light": return <Light item={item} />;
    case "desk": return <DeskSet item={item} accent={tone(item, "c2")} />;
    case "chair": return <ChairMesh item={item} />;
    default: return null;
  }
}
