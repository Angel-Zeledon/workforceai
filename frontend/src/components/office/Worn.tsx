"use client";
import { useEffect, useMemo, useRef } from "react";
import { useFrame } from "@react-three/fiber";
import * as THREE from "three";
import { useRewards, type ShopItem } from "@/lib/rewards";
import { usePreferences } from "@/lib/preferences";
import {
  badgeSpec, cansSpec, darken, glassesSpec, hash, hatShape, lighten, luma, mix, outfitSpec, scarfSpec,
  type Deco, type HatSpec, type OutfitSpec,
} from "./itemStyle";

/**
 * Shop items worn by a character (docs/architecture/rewards.md): low-poly meshes parented to the head / torso
 * groups of <Character/>. The shape of every item comes from ./itemStyle (one spec per id, shared with the flat shop
 * preview); the colors come from the catalog. Geometry is static: nothing is allocated per frame (only the shoulder
 * companion bobs by mutating a ref).
 */
export type Worn = Record<string, string> | undefined;

/** The slots the agent wears (stable reference until its equipment changes). */
export function useWorn(agentId: string): Worn {
  return useRewards((s) => s.equip[agentId]);
}

function useItems(worn: Worn): Record<string, ShopItem | undefined> {
  const catalog = useRewards((s) => s.catalog);
  return useMemo(() => {
    const out: Record<string, ShopItem | undefined> = {};
    if (!worn) return out;
    for (const [slot, id] of Object.entries(worn)) out[slot] = catalog.find((i) => i.id === id);
    return out;
  }, [worn, catalog]);
}

/** The outfit worn (if any) and the way it is cut: the jacket color, shirt, trousers and trim all come from here. */
export function useOutfit(worn: Worn, fallbackColor: string): OutfitSpec & { color: string; worn: boolean } {
  const item = useItems(worn).outfit;
  return useMemo(() => {
    if (!item) {
      const s = outfitSpec("outfit_default", fallbackColor, undefined, "common");
      return { ...s, style: "blazer", pants: "#9a7a5c", shirt: "#fffaf0", color: fallbackColor, worn: false };
    }
    return { ...outfitSpec(item.id, item.color || fallbackColor, item.color2, item.rarity), color: item.color || fallbackColor, worn: true };
  }, [item, fallbackColor]);
}

/** Which of the role's own accessories a worn item replaces (so two pairs of glasses never overlap). */
export function suppressesAccessory(worn: Worn, kind: string): boolean {
  if (!worn) return false;
  if (worn.face && (kind === "glasses" || kind === "roundglasses")) return true;
  if (worn.ears && (kind === "headphones" || kind === "headset")) return true;
  if (worn.hat && (kind === "hardhat" || kind === "beanie")) return true;
  return false;
}

type Finish = "matte" | "knit" | "metal" | "glow" | "glass";
function Mat({ c, f = "matte", op }: { c: string; f?: Finish; op?: number }) {
  switch (f) {
    case "knit": return <meshStandardMaterial color={c} roughness={1} />;
    case "metal": return <meshStandardMaterial color={c} metalness={0.7} roughness={0.32} />;
    case "glow": return <meshStandardMaterial color={c} emissive={c} emissiveIntensity={0.7} roughness={0.5} />;
    case "glass": return <meshStandardMaterial color={c} transparent opacity={op ?? 0.5} roughness={0.12} metalness={0.15} />;
    default: return <meshStandardMaterial color={c} roughness={0.72} />;
  }
}
const Emit = ({ c, k = 0.6 }: { c: string; k?: number }) => <meshStandardMaterial color={c} emissive={c} emissiveIntensity={k} roughness={0.4} />;

// ------------------------------------------------------------------ hats

/** y of the top of the crown, where pompoms, stems and flowers sit. */
function topOf(s: HatSpec): number {
  return s.crown === "cone" ? s.h : s.crown === "puff" ? s.h + 0.04 : s.crown === "none" ? 0.02 : s.crown === "jester" ? 0.05 : s.h + 0.012;
}

function CrownShape({ s, c1, c2 }: { s: HatSpec; c1: string; c2: string }) {
  const r = s.r ?? 0.19;
  const f = s.finish ?? "matte";
  const h = s.h;
  switch (s.crown) {
    case "dome":
      return <mesh castShadow scale={[1, h / r, 1]}><sphereGeometry args={[r, 22, 12, 0, Math.PI * 2, 0, Math.PI / 2]} /><Mat c={c1} f={f} /></mesh>;
    case "cyl": case "trunc": case "flat":
      return <mesh castShadow position={[0, h / 2, 0]}><cylinderGeometry args={[s.rTop ?? r, r, h, 22]} /><Mat c={c1} f={f} /></mesh>;
    case "cone": {
      const lean = (s.curl ?? 0) < 0 ? -(s.curl ?? 0) * 0.28 : 0;
      return (
        <group rotation={[0, 0, -lean]}>
          <mesh castShadow position={[0, h / 2, 0]}><coneGeometry args={[r, h, 20]} /><Mat c={c1} f={f} /></mesh>
        </group>
      );
    }
    case "puff":
      return (
        <group>
          <mesh castShadow position={[0, h * 0.2, 0]}><cylinderGeometry args={[r * 0.88, r, h * 0.4, 20]} /><Mat c={c1} f={f} /></mesh>
          <mesh castShadow position={[0, h * 0.58, 0]} scale={[1, 0.82, 1]}><sphereGeometry args={[r * 1.02, 18, 14]} /><Mat c={c1} f={f} /></mesh>
          <mesh castShadow position={[0, h * 0.45, 0]} scale={[1.14, 0.5, 1.14]}><sphereGeometry args={[r * 0.8, 14, 10]} /><Mat c={c1} f={f} /></mesh>
        </group>
      );
    case "pumpkin":
      return (
        <group position={[0, h * 0.45, 0]}>
          {[0, 1, 2, 3].map((i) => <mesh key={i} castShadow rotation={[0, (i * Math.PI) / 4, 0]} scale={[1, 0.82, 0.5 + (i % 2) * 0.1]}><sphereGeometry args={[r * 1.02, 16, 12]} /><Mat c={i % 2 ? darken(c1, 0.1) : c1} /></mesh>)}
        </group>
      );
    case "mushroom":
      return (
        <group>
          <mesh castShadow scale={[1, h / 0.22, 1]}><sphereGeometry args={[0.24, 22, 12, 0, Math.PI * 2, 0, Math.PI / 2]} /><Mat c={c1} /></mesh>
          <mesh position={[0, -0.005, 0]} rotation={[Math.PI / 2, 0, 0]} scale={[1, 1, 0.1]}><cylinderGeometry args={[0.235, 0.235, 0.04, 22]} /><Mat c={mix(c2, "#e8d9b0", 0.6)} /></mesh>
        </group>
      );
    case "board":
      return (
        <group>
          <mesh castShadow position={[0, 0.025, 0]}><cylinderGeometry args={[r * 0.78, r * 0.86, 0.07, 20]} /><Mat c={c1} /></mesh>
          <mesh castShadow position={[0, 0.075, 0]} rotation={[0, Math.PI / 4, 0]}><boxGeometry args={[0.37, 0.022, 0.37]} /><Mat c={c1} /></mesh>
          <mesh position={[0, 0.09, 0]}><sphereGeometry args={[0.02, 8, 6]} /><Mat c={c2} f="metal" /></mesh>
        </group>
      );
    case "crown": {
      const n = 5;
      return (
        <group>
          <mesh castShadow position={[0, 0.02, 0]}><cylinderGeometry args={[0.15, 0.17, 0.07, 18]} /><Mat c={c1} f="metal" /></mesh>
          {Array.from({ length: n }, (_, i) => {
            const a = (i / n) * Math.PI * 2;
            return <group key={i}>
              <mesh castShadow position={[Math.sin(a) * 0.145, 0.095, Math.cos(a) * 0.145]}><coneGeometry args={[0.04, 0.11, 6]} /><Mat c={c1} f="metal" /></mesh>
              <mesh position={[Math.sin(a) * 0.145, 0.158, Math.cos(a) * 0.145]}><sphereGeometry args={[0.014, 8, 6]} /><Emit c={c2} k={0.5} /></mesh>
            </group>;
          })}
          <mesh position={[0, 0.02, 0.172]}><sphereGeometry args={[0.024, 8, 8]} /><Emit c={c2} k={0.6} /></mesh>
        </group>
      );
    }
    case "tiara":
      return (
        <group position={[0, 0.0, 0.02]}>
          <mesh rotation={[Math.PI / 2, 0, 0]} position={[0, 0.015, 0]}><torusGeometry args={[0.172, 0.011, 8, 28, Math.PI * 1.05]} /><Mat c={mix(c1, "#ffffff", 0.3)} f="metal" /></mesh>
          {[-0.09, 0, 0.09].map((x, i) => (
            <mesh key={x} position={[x, 0.05 + (i === 1 ? 0.035 : 0), 0.155 - Math.abs(x) * 0.5]}><coneGeometry args={[0.022, i === 1 ? 0.09 : 0.06, 6]} /><Mat c={c1} f="glass" op={0.75} /></mesh>
          ))}
        </group>
      );
    case "wreath":
      return (
        <group position={[0, 0.0, 0]}>
          <mesh rotation={[Math.PI / 2, 0, 0]}><torusGeometry args={[r * 0.97, 0.014, 6, 28]} /><Mat c={darken(c1, 0.35)} /></mesh>
          {Array.from({ length: 12 }, (_, i) => {
            const a = (i / 12) * Math.PI * 2;
            return <mesh key={i} position={[Math.sin(a) * r * 0.97, 0.012, Math.cos(a) * r * 0.97]} rotation={[0.4, a, i % 2 ? 0.5 : -0.5]} scale={[1.7, 0.55, 0.9]}><sphereGeometry args={[0.03, 8, 6]} /><Mat c={i % 3 === 0 ? lighten(c1, 0.15) : c1} /></mesh>;
          })}
        </group>
      );
    case "turban":
      return (
        <group>
          <mesh castShadow scale={[1, h / 0.19, 1]}><sphereGeometry args={[0.205, 20, 12, 0, Math.PI * 2, 0, Math.PI / 2]} /><Mat c={c1} f="knit" /></mesh>
          {[0, 1, 2].map((i) => <mesh key={i} position={[0, 0.03 + i * 0.04, 0]} rotation={[Math.PI / 2 + (i - 1) * 0.28, 0, (i - 1) * 0.22]} scale={[1, 0.92, 1]}><torusGeometry args={[0.2 - i * 0.022, 0.026, 8, 24]} /><Mat c={i === 1 ? c2 : c1} f="knit" /></mesh>)}
          <mesh position={[0, 0.17, 0.0]} scale={[1.2, 0.9, 1.1]}><sphereGeometry args={[0.07, 10, 8]} /><Mat c={c1} f="knit" /></mesh>
        </group>
      );
    case "jester":
      return (
        <group>
          <mesh position={[0, 0.0, 0]} rotation={[Math.PI / 2, 0, 0]}><torusGeometry args={[0.185, 0.02, 8, 24]} /><Mat c={c2} /></mesh>
          <mesh castShadow position={[0, 0.075, 0]}><sphereGeometry args={[0.19, 18, 10, 0, Math.PI * 2, 0, Math.PI / 2.4]} /><Mat c={c1} f="knit" /></mesh>
          {[-1, 1].map((sd) => (
            <group key={sd} position={[sd * 0.06, 0.1, 0]} rotation={[0, 0, -sd * 0.95]}>
              <mesh castShadow position={[0, 0.12, 0]}><coneGeometry args={[0.07, 0.26, 12]} /><Mat c={sd < 0 ? c1 : c2} f="knit" /></mesh>
              <mesh position={[0, 0.265, 0]}><sphereGeometry args={[0.028, 10, 8]} /><Mat c={sd < 0 ? c2 : c1} f="metal" /></mesh>
            </group>
          ))}
        </group>
      );
    case "bicorne":
      return (
        <group>
          <mesh castShadow position={[0, 0.03, 0]} scale={[1.75, 0.55, 0.85]}><sphereGeometry args={[0.17, 20, 12, 0, Math.PI * 2, 0, Math.PI / 2]} /><Mat c={c1} /></mesh>
          {[-1, 1].map((sd) => <mesh key={sd} castShadow position={[sd * 0.3, 0.075, 0]} rotation={[0, 0, sd * -0.75]}><coneGeometry args={[0.07, 0.16, 8]} /><Mat c={c1} /></mesh>)}
          <mesh position={[0, 0.05, 0.14]} rotation={[0.3, 0, 0]} scale={[2.4, 0.5, 0.5]}><sphereGeometry args={[0.05, 10, 8]} /><Mat c={c2} f="metal" /></mesh>
        </group>
      );
    case "lobes":
      return (
        <group>
          {[-1, 1].map((sd) => <mesh key={sd} castShadow position={[sd * 0.075, 0.0, 0]} scale={[1, h / 0.12, 1]}><sphereGeometry args={[0.115, 14, 10, 0, Math.PI * 2, 0, Math.PI / 2]} /><Mat c={c1} f="knit" /></mesh>)}
          <mesh position={[0, 0.0, 0]} rotation={[Math.PI / 2, 0, 0]}><torusGeometry args={[0.18, 0.013, 6, 24]} /><Mat c={c2} /></mesh>
        </group>
      );
    case "nemes":
      return (
        <group>
          <mesh castShadow scale={[1, h / 0.19, 1]}><sphereGeometry args={[0.195, 20, 12, 0, Math.PI * 2, 0, Math.PI / 2]} /><Mat c={c1} /></mesh>
          {[-1, 1].map((sd) => <mesh key={sd} castShadow position={[sd * 0.175, -0.1, 0.02]} rotation={[0, 0, sd * 0.08]}><boxGeometry args={[0.07, 0.24, 0.11]} /><Mat c={c1} /></mesh>)}
          {[-1, 1].map((sd) => [0, 1, 2].map((i) => <mesh key={`${sd}${i}`} position={[sd * 0.2, -0.04 - i * 0.07, 0.02]}><boxGeometry args={[0.012, 0.03, 0.115]} /><Mat c={c2} f="metal" /></mesh>))}
        </group>
      );
    case "cloud":
      return (
        <group>
          {[[0, 0.06, 0, 0.12], [-0.1, 0.03, 0.03, 0.09], [0.1, 0.03, 0.02, 0.09], [0.0, 0.05, 0.11, 0.08], [-0.05, 0.1, -0.03, 0.08], [0.06, 0.11, -0.02, 0.075]].map(([x, y, z, rr], i) => (
            <mesh key={i} castShadow position={[x, y, z]}><sphereGeometry args={[rr, 12, 10]} /><Mat c={i % 2 ? lighten(c1, 0.15) : c1} f="knit" /></mesh>
          ))}
        </group>
      );
    case "donut":
      return (
        <group position={[0, 0.07, 0]}>
          <mesh castShadow rotation={[Math.PI / 2, 0, 0]}><torusGeometry args={[0.11, 0.06, 12, 24]} /><Mat c={c1} /></mesh>
          <mesh position={[0, 0.026, 0]} rotation={[Math.PI / 2, 0, 0]} scale={[1, 1, 0.55]}><torusGeometry args={[0.11, 0.062, 12, 24]} /><Mat c={c2} /></mesh>
        </group>
      );
    case "cupcake":
      return (
        <group>
          <mesh castShadow position={[0, 0.04, 0]}><cylinderGeometry args={[0.17, 0.12, 0.08, 14]} /><Mat c={mix(c1, "#c0a080", 0.5)} /></mesh>
          {[0, 1, 2].map((i) => <mesh key={i} castShadow position={[0, 0.1 + i * 0.045, 0]}><torusGeometry args={[0.13 - i * 0.032, 0.036, 8, 20]} /><Mat c={c1} /></mesh>)}
          <mesh position={[0, 0.25, 0]}><sphereGeometry args={[0.03, 10, 8]} /><Emit c="#e63946" k={0.2} /></mesh>
        </group>
      );
    case "berries":
      return (
        <group position={[0, 0.0, 0]}>
          {[[0, 0.1, 0], [0.09, 0.05, 0.04], [-0.09, 0.05, 0.04], [0.04, 0.05, -0.09], [-0.05, 0.06, -0.08], [0.0, 0.05, 0.1], [0.1, 0.04, -0.05], [-0.11, 0.04, -0.04]].map(([x, y, z], i) => (
            <mesh key={i} castShadow position={[x, y, z]}><sphereGeometry args={[0.062, 12, 10]} /><Mat c={i % 3 === 0 ? lighten(c1, 0.12) : c1} /></mesh>
          ))}
        </group>
      );
    case "shako":
      return (
        <group>
          <mesh castShadow position={[0, h / 2, 0]}><cylinderGeometry args={[r * 0.88, r, h, 20]} /><Mat c={c1} /></mesh>
          <mesh castShadow position={[0, h + 0.005, 0]}><cylinderGeometry args={[r * 1.0, r * 0.88, 0.03, 20]} /><Mat c={mix(c1, c2, 0.25)} /></mesh>
          <mesh position={[0, 0.012, 0.0]} rotation={[Math.PI / 2, 0, 0]}><torusGeometry args={[r * 1.0, 0.012, 6, 22]} /><Mat c={c2} f="metal" /></mesh>
        </group>
      );
    default:
      return null;
  }
}

/** Leaf-shaped blob used by leaves, petals and feathers. */
function Leaf({ p, rot, s, c }: { p: [number, number, number]; rot: [number, number, number]; s: [number, number, number]; c: string }) {
  return <mesh position={p} rotation={rot} scale={s}><sphereGeometry args={[0.04, 10, 8]} /><Mat c={c} /></mesh>;
}

function HatDeco({ d, s, c1, c2, top }: { d: Deco; s: HatSpec; c1: string; c2: string; top: number }) {
  const r = s.r ?? 0.19;
  switch (d) {
    case "pompom":
      return <mesh castShadow position={[0, top + 0.03, 0]}><sphereGeometry args={[0.048, 12, 10]} /><Mat c={s.crown === "cone" ? c2 : mix(c2, "#ffffff", 0.2)} f="knit" /></mesh>;
    case "feather": case "plume": {
      const big = d === "plume";
      return (
        <group position={[0.12, 0.06, -0.04]} rotation={[0.2, 0, -0.85]}>
          <mesh castShadow position={[0, 0.1, 0]} scale={[1, 1, 0.35]}><capsuleGeometry args={[big ? 0.036 : 0.026, big ? 0.24 : 0.16, 4, 10]} /><Mat c={c2} /></mesh>
          {big && <mesh position={[0.02, 0.2, 0]} rotation={[0, 0, 0.35]} scale={[1, 1, 0.35]}><capsuleGeometry args={[0.03, 0.16, 4, 8]} /><Mat c={lighten(c2, 0.2)} /></mesh>}
          <mesh position={[0, 0.0, 0]}><cylinderGeometry args={[0.004, 0.004, 0.1, 5]} /><Mat c={darken(c2, 0.4)} /></mesh>
        </group>
      );
    }
    case "fan":
      return (
        <group position={[0, 0.06, -0.12]} rotation={[0.1, 0, 0]}>
          {[-0.9, -0.45, 0, 0.45, 0.9].map((a, i) => (
            <group key={i} rotation={[0, 0, a]}>
              <mesh position={[0, 0.17, 0]} scale={[0.8, 1.7, 0.3]}><sphereGeometry args={[0.065, 12, 10]} /><Mat c={i % 2 ? c1 : c2} /></mesh>
              <mesh position={[0, 0.27, 0.012]} scale={[1, 1.2, 0.5]}><sphereGeometry args={[0.022, 8, 6]} /><Emit c="#1d4ed8" k={0.5} /></mesh>
            </group>
          ))}
        </group>
      );
    case "horns":
      return <>{[-1, 1].map((sd) => (
        <group key={sd} position={[sd * 0.175, 0.06, 0]} rotation={[0, 0, -sd * 0.55]}>
          <mesh castShadow position={[0, 0.07, 0]}><coneGeometry args={[0.04, 0.17, 10]} /><Mat c={c2} f={s.finish === "metal" ? "matte" : "matte"} /></mesh>
          <mesh position={[0, 0.0, 0]}><sphereGeometry args={[0.042, 10, 8]} /><Mat c={darken(c2, 0.2)} /></mesh>
        </group>
      ))}</>;
    case "bunny":
      return <>{[-1, 1].map((sd) => (
        <group key={sd} position={[sd * 0.075, 0.05, -0.02]} rotation={[sd * 0.12, 0, -sd * 0.22]}>
          <mesh castShadow position={[0, 0.18, 0]} scale={[0.7, 1, 0.3]}><capsuleGeometry args={[0.06, 0.2, 6, 12]} /><Mat c={c1} /></mesh>
          <mesh position={[0, 0.18, 0.014]} scale={[0.4, 0.9, 0.2]}><capsuleGeometry args={[0.055, 0.17, 6, 10]} /><Mat c={c2} /></mesh>
        </group>
      ))}</>;
    case "cat":
      return <>{[-1, 1].map((sd) => (
        <group key={sd} position={[sd * 0.12, 0.075, -0.01]} rotation={[0, 0, -sd * 0.35]}>
          <mesh castShadow position={[0, 0.045, 0]} scale={[1, 1, 0.45]}><coneGeometry args={[0.065, 0.13, 4]} /><Mat c={c1} /></mesh>
          <mesh position={[0, 0.04, 0.014]} scale={[1, 1, 0.3]}><coneGeometry args={[0.04, 0.08, 4]} /><Mat c={c2} /></mesh>
        </group>
      ))}</>;
    case "stars": {
      const pts: [number, number, number][] = s.crown === "cone"
        ? [[0.06, s.h * 0.45, 0.07], [-0.05, s.h * 0.3, 0.1], [0.0, s.h * 0.68, 0.05]] : [[0.1, 0.07, 0.1], [-0.09, 0.09, 0.1], [0.0, 0.14, 0.06]];
      return <>{pts.map((p, i) => <mesh key={i} position={p} rotation={[0.4, 0.6, 0]}><octahedronGeometry args={[0.022, 0]} /><Emit c={c2} k={0.7} /></mesh>)}</>;
    }
    case "flower": {
      const big = s.crown === "dome" && s.swap;
      const n = big ? 10 : 5, pr = big ? 0.065 : 0.034, ro = big ? 0.085 : 0.05;
      return (
        <group position={big ? [0, s.h + 0.03, 0] : [0.09, s.h + 0.04, 0.08]} rotation={big ? [0, 0, 0] : [0.5, 0, 0.3]}>
          {Array.from({ length: n }, (_, i) => { const a = (i / n) * Math.PI * 2; return <mesh key={i} position={[Math.sin(a) * ro, 0, Math.cos(a) * ro]} scale={[1, 0.45, 1.3]} rotation={[0, a, 0]}><sphereGeometry args={[pr, 10, 8]} /><Mat c={c1} /></mesh>; })}
          <mesh position={[0, 0.012, 0]}><sphereGeometry args={[big ? 0.07 : 0.03, 12, 10]} /><Mat c={c2} /></mesh>
        </group>
      );
    }
    case "leaf":
      return (
        <group position={[0.02, top, 0]}>
          <Leaf p={[0.06, 0.03, 0.02]} rot={[0, 0.2, -0.7]} s={[2.2, 0.5, 1]} c={c2} />
          <Leaf p={[-0.05, 0.035, 0.0]} rot={[0, -0.3, 0.8]} s={[2.0, 0.5, 1]} c={lighten(c2, 0.15)} />
        </group>
      );
    case "stem":
      return <mesh position={[0.01, top + 0.012, 0]} rotation={[0, 0, 0.2]}><cylinderGeometry args={[0.012, 0.016, 0.05, 8]} /><Mat c={c2} /></mesh>;
    case "sprout":
      return (
        <group position={[0, top, 0]}>
          <mesh position={[0, 0.04, 0]}><cylinderGeometry args={[0.007, 0.01, 0.08, 6]} /><Mat c={c1} /></mesh>
          <Leaf p={[0.05, 0.09, 0]} rot={[0, 0, -0.9]} s={[2.4, 0.5, 1]} c={c1} />
          <Leaf p={[-0.05, 0.095, 0]} rot={[0, 0, 0.9]} s={[2.4, 0.5, 1]} c={lighten(c1, 0.15)} />
        </group>
      );
    case "wings":
      return <>{[-1, 1].map((sd) => (
        <group key={sd} position={[sd * 0.2, 0.06, -0.02]} rotation={[0, 0, -sd * 0.6]}>
          {[0, 1, 2].map((i) => <mesh key={i} position={[sd * 0.02 + sd * i * 0.012, 0.05 + i * 0.045, 0]} rotation={[0, 0, -sd * (0.25 + i * 0.15)]} scale={[0.5, 1.5 - i * 0.2, 0.25]}><sphereGeometry args={[0.04, 8, 6]} /><Mat c={i === 1 ? lighten(c2, 0.2) : c2} f="metal" /></mesh>)}
        </group>
      ))}</>;
    case "ball":
      return s.crown === "jester" ? null
        : <mesh position={[0, top + 0.05, 0]}><icosahedronGeometry args={[0.055, 1]} /><meshStandardMaterial color="#e8eef5" metalness={0.95} roughness={0.12} flatShading emissive={c2} emissiveIntensity={0.2} /></mesh>;
    case "halo":
      return <mesh position={[0, 0.2, 0]} rotation={[Math.PI / 2 - 0.12, 0, 0]}><torusGeometry args={[0.13, 0.018, 10, 32]} /><Emit c={c1} k={1.1} /></mesh>;
    case "spikes":
      return <>{Array.from({ length: 7 }, (_, i) => { const a = (i / 7) * Math.PI * 2; const rr = 0.09; return (
        <mesh key={i} position={[Math.sin(a) * rr, s.h * 0.82, Math.cos(a) * rr - 0.01]} rotation={[Math.cos(a) * 0.5, 0, -Math.sin(a) * 0.5]}><coneGeometry args={[0.022, 0.08, 6]} /><Mat c={c2} f={s.crown === "dome" && s.finish === "glow" ? "glow" : "matte"} /></mesh>); })}</>;
    case "tassel":
      return s.crown === "board"
        ? <group position={[0.2, 0.06, 0.1]}><mesh><cylinderGeometry args={[0.005, 0.005, 0.1, 5]} /><Mat c={c2} /></mesh><mesh position={[0, -0.075, 0]}><sphereGeometry args={[0.02, 8, 6]} /><Mat c={c2} f="metal" /></mesh></group>
        : <group position={[0.07, s.h + 0.0, 0.02]} rotation={[0, 0, -0.6]}><mesh position={[0.08, 0.0, 0]} rotation={[0, 0, Math.PI / 2]}><cylinderGeometry args={[0.006, 0.006, 0.14, 5]} /><Mat c={c2} /></mesh><mesh position={[0.17, -0.02, 0]}><capsuleGeometry args={[0.014, 0.05, 4, 6]} /><Mat c={c2} /></mesh></group>;
    case "dots": {
      const n = 9;
      return <>{Array.from({ length: n }, (_, i) => {
        const a = (i / n) * Math.PI * 2 + 0.3, el = 0.45 + (i % 3) * 0.3;
        const rr = (s.crown === "mushroom" ? 0.24 : r) * Math.cos(el);
        const y = (s.h) * Math.sin(el) * (s.crown === "donut" ? 0.4 : 1) + (s.crown === "donut" ? 0.1 : 0);
        return <mesh key={i} position={[Math.sin(a) * rr * 0.96, y, Math.cos(a) * rr * 0.96]} scale={[1, 1, 0.5]} rotation={[0, a, 0]}><sphereGeometry args={[s.crown === "mushroom" ? 0.038 : 0.022, 8, 6]} /><Mat c={c2} /></mesh>;
      })}</>;
    }
    case "bow":
      return <group position={s.crown === "none" ? [0.15, 0.06, 0.07] : [0.12, 0.035, 0.12]} rotation={[0, 0.5, 0]}>
        {[-1, 1].map((sd) => <mesh key={sd} position={[sd * 0.032, 0, 0]} scale={[1.4, 1, 0.6]}><sphereGeometry args={[0.03, 8, 6]} /><Mat c={c2} /></mesh>)}
        <mesh><sphereGeometry args={[0.016, 8, 6]} /><Mat c={c2} /></mesh>
      </group>;
    case "antenna":
      return <group position={[0, top, 0]}><mesh position={[0, 0.05, 0]}><cylinderGeometry args={[0.005, 0.005, 0.1, 5]} /><Mat c="#59616d" /></mesh><mesh position={[0, 0.11, 0]}><sphereGeometry args={[0.016, 8, 6]} /><Emit c={c2} k={1} /></mesh></group>;
    case "goggles":
      return <group position={[0, 0.07, 0.15]} rotation={[-0.35, 0, 0]}>
        <mesh rotation={[Math.PI / 2, 0, 0]} scale={[1, 1, 0.5]}><torusGeometry args={[0.18, 0.012, 6, 22, Math.PI * 1.1]} /><Mat c="#3a2a1a" /></mesh>
        {[0.06, -0.06].map((x) => <mesh key={x} position={[x, 0, 0.03]} rotation={[Math.PI / 2, 0, 0]}><cylinderGeometry args={[0.045, 0.045, 0.03, 14]} /><Mat c={c2} f="glass" op={0.8} /></mesh>)}
      </group>;
    case "flaps":
      return <>{[-1, 1].map((sd) => <mesh key={sd} castShadow position={[sd * 0.175, -0.04, 0.0]} rotation={[0, 0, sd * 0.12]} scale={[0.4, 1.3, 1]}><sphereGeometry args={[0.07, 10, 8]} /><Mat c={darken(c1, 0.12)} f="knit" /></mesh>)}</>;
    case "moon":
      return <mesh position={[-0.07, top * 0.7 + 0.02, 0.12]} rotation={[0, 0.2, 0.3]}><torusGeometry args={[0.04, 0.014, 6, 16, Math.PI * 1.5]} /><Emit c={c2} k={0.6} /></mesh>;
    case "crest":
      return <>{Array.from({ length: 6 }, (_, i) => <mesh key={i} position={[0, s.h * 0.95 + (i === 2 || i === 3 ? 0.03 : 0.0) + 0.012, 0.13 - i * 0.052]} scale={[0.45, 1.5 + (i === 2 || i === 3 ? 0.8 : 0), 1]}><sphereGeometry args={[0.035, 8, 6]} /><Mat c={c2} /></mesh>)}</>;
    case "bolt":
      return <group position={[0.0, 0.05, 0.13]} rotation={[0.2, 0, 0]}>
        <mesh position={[0.01, 0.03, 0]} rotation={[0, 0, 0.5]}><boxGeometry args={[0.02, 0.07, 0.015]} /><Emit c={c2} k={0.9} /></mesh>
        <mesh position={[-0.008, -0.025, 0]} rotation={[0, 0, 0.5]}><boxGeometry args={[0.02, 0.06, 0.015]} /><Emit c={c2} k={0.9} /></mesh>
      </group>;
    case "gem":
      return <mesh position={[0, s.crown === "cupcake" ? 0.26 : s.crown === "turban" ? 0.1 : s.crown === "shako" ? 0.07 : 0.045, s.crown === "cupcake" ? 0 : (s.r ?? 0.19) * 0.95]}><octahedronGeometry args={[0.028, 0]} /><Emit c={c2} k={0.5} /></mesh>;
    case "shield":
      return <mesh position={[0, 0.07, (s.r ?? 0.19) * 0.93]} rotation={[0.35, 0, 0]} scale={[1, 1.1, 0.35]}><sphereGeometry args={[0.052, 10, 8]} /><Mat c={c2} f="metal" /></mesh>;
    case "lava":
      return <>{[[0.07, 0.1, 0.12], [-0.08, 0.12, 0.08], [0, 0.15, -0.04], [0.1, 0.09, -0.07]].map((p, i) => <mesh key={i} position={p as [number, number, number]} scale={[1.6, 0.5, 1]}><sphereGeometry args={[0.02, 8, 6]} /><Emit c={c2} k={1.2} /></mesh>)}</>;
    case "slit":
      return <mesh position={[0, 0.07, (s.r ?? 0.19) * 0.97]} scale={[1, 1, 0.4]}><boxGeometry args={[0.2, 0.018, 0.03]} /><meshBasicMaterial color="#10131a" /></mesh>;
    case "stripes": {
      if (s.front) return <mesh position={[0, s.h * 0.98, 0]} scale={[0.3, 0.7, 1.9]}><boxGeometry args={[0.1, 0.03, 0.2]} /><Mat c={c2} /></mesh>;
      const cols = s.crown === "dome" && s.h > 0.15 && c1 === "#7ae582" ? ["#f72585", "#ffca3a", "#4cc9f0", "#8338ec"] : [c2, c2];
      return <>{cols.map((c, i) => <mesh key={i} position={[0, 0.04 + i * 0.035 + (s.crown === "nemes" ? 0.02 : 0), 0]} rotation={[Math.PI / 2, 0, 0]}><torusGeometry args={[Math.max(0.05, (s.r ?? 0.19) * (0.98 - i * 0.2)), 0.012, 6, 22]} /><Mat c={c} /></mesh>)}</>;
    }
    case "back":
      return <mesh castShadow position={[0, -0.03, -(s.r ?? 0.19) * 0.9]} scale={[s.crown === "dome" && (s.r ?? 0.19) > 0.2 ? 1.5 : 0.9, 1.2, 0.5]}><sphereGeometry args={[0.07, 10, 8]} /><Mat c={s.crown === "dome" && s.band ? c2 : darken(c1, 0.1)} /></mesh>;
    default:
      return null;
  }
}

function Hat({ item }: { item: ShopItem }) {
  const s = useMemo(() => hatShape(item.id), [item.id]);
  const base = item.color || "#d9534f";
  const alt = item.color2 || (luma(base) > 0.6 ? darken(base, 0.45) : lighten(base, 0.5));
  const c1 = s.swap ? alt : base, c2 = s.swap ? base : alt;
  const r = s.r ?? 0.19;
  const top = topOf(s);
  const tilt = item.id === "hat_beret" || item.id === "hat_blueberet" ? 0.2 : 0;
  return (
    <group position={[0, 0.2, 0]}>
      <group rotation={[0, 0, tilt]} position={[tilt * 0.3, 0, 0]}>
        <CrownShape s={s} c1={c1} c2={c2} />
      </group>
      {!!s.brim && s.brim > 0 && (s.curl ?? 0) >= 0 && <mesh castShadow position={[0, -0.005, 0]}><cylinderGeometry args={[s.brim, s.brim, 0.018, 28]} /><Mat c={s.crown === "board" ? c1 : c1} f={s.finish === "metal" ? "metal" : "matte"} /></mesh>}
      {!!s.brim && s.brim > 0 && (s.curl ?? 0) > 0.2 && [-1, 1].map((sd) => <mesh key={sd} position={[sd * s.brim! * 0.95, 0.012 + (s.curl ?? 0) * 0.03, 0]} rotation={[0, 0, sd * -0.3]} scale={[0.5, 1, 1.6]}><sphereGeometry args={[0.05 + (s.curl ?? 0) * 0.02, 10, 8]} /><Mat c={c1} /></mesh>)}
      {!!s.brim && (s.curl ?? 0) < 0 && <mesh castShadow position={[0, -0.03, 0]} scale={[1, 0.09 + Math.abs(s.curl ?? 0) * 0.07, 1]}><sphereGeometry args={[s.brim, 24, 8, 0, Math.PI * 2, 0, Math.PI / 2]} /><Mat c={c1} f={s.finish === "knit" ? "knit" : "matte"} /></mesh>}
      {!!s.front && <mesh castShadow position={[0, 0.005, r * 0.8 + (s.front ?? 0) * 0.3]} scale={[1, 0.17, 1]}><sphereGeometry args={[(s.front ?? 0) * 0.9, 16, 10]} /><Mat c={c1} /></mesh>}
      {s.band && !["cone", "wreath", "none", "pumpkin", "mushroom", "board", "cupcake", "turban"].includes(s.crown) && (
        <mesh position={[0, 0.02, 0]} rotation={[Math.PI / 2, 0, 0]}><torusGeometry args={[(s.crown === "trunc" || s.crown === "cyl" ? r : r * 0.99) * 1.0, 0.014, 6, 28]} /><Mat c={c2} /></mesh>
      )}
      {s.band && s.crown === "cone" && <mesh position={[0, 0.03, 0]} rotation={[Math.PI / 2, 0, 0]}><torusGeometry args={[r * 0.96, 0.015, 6, 28]} /><Mat c={c2} /></mesh>}
      {s.band && s.crown === "none" && (
        <mesh position={[0, 0.03, 0]} rotation={[Math.PI / 2 - 0.12, 0, 0]}><torusGeometry args={[0.178, 0.02, 8, 28]} /><Mat c={c2 === base ? c1 : c1} f="knit" /></mesh>
      )}
      {(s.deco ?? []).map((d) => <HatDeco key={d} d={d} s={s} c1={c1} c2={c2} top={top} />)}
    </group>
  );
}

// ------------------------------------------------------------------ glasses

function Face({ item }: { item: ShopItem }) {
  const sp = useMemo(() => glassesSpec(item.id), [item.id]);
  const c = item.color || "#1d2128";
  const dark = luma(c) < 0.18;
  const frameC = sp.metal ? mix(c, "#f0d9a0", 0.35) : dark ? "#14171c" : darken(c, 0.25);
  const lens = dark ? "#0b0d11" : mix(c, "#10202c", 0.45);
  const clear = 1 - sp.tint; // 0 = dark lens, 1 = clear
  const lensMat = <meshStandardMaterial color={sp.tint > 0.6 ? lens : lighten(c, 0.5)} transparent opacity={0.3 + sp.tint * 0.62} roughness={0.08} metalness={0.2} emissive={sp.glow ? c : "#000000"} emissiveIntensity={sp.glow ? 0.55 : 0} />;
  const frameMat = <meshStandardMaterial color={frameC} roughness={sp.metal ? 0.3 : 0.6} metalness={sp.metal ? 0.8 : 0.05} emissive={sp.glow ? c : "#000000"} emissiveIntensity={sp.glow ? 0.35 : 0} />;
  const xs = [0.058, -0.058];
  const arms = <>{xs.map((x) => <mesh key={x} position={[Math.sign(x) * 0.108, 0, -0.05]}><boxGeometry args={[0.007, 0.007, 0.2]} />{frameMat}</mesh>)}</>;
  const bridge = <mesh position={[0, 0.012, 0]}><boxGeometry args={[0.04, 0.008, 0.008]} />{frameMat}</mesh>;
  void clear;
  const Y = 0.172, Z = 0.157;
  switch (sp.frame) {
    case "round": case "monocle": {
      const list = sp.frame === "monocle" ? [-0.058] : xs;
      return <group position={[0, Y, Z]}>
        {list.map((x) => <group key={x} position={[x, 0, 0]}><mesh><torusGeometry args={[0.04, 0.0075, 8, 22]} />{frameMat}</mesh><mesh rotation={[Math.PI / 2, 0, 0]}><cylinderGeometry args={[0.039, 0.039, 0.006, 20]} />{lensMat}</mesh></group>)}
        {sp.frame === "round" ? <>{bridge}{arms}</> : <mesh position={[-0.058, -0.075, 0]} rotation={[0, 0, 0.1]}><cylinderGeometry args={[0.0025, 0.0025, 0.07, 4]} />{frameMat}</mesh>}
      </group>;
    }
    case "square": case "d3": case "pixel": {
      const w = sp.frame === "pixel" ? 0.092 : 0.082, hgt = sp.frame === "pixel" ? 0.04 : 0.056;
      return <group position={[0, Y, Z]}>
        {xs.map((x, i) => <group key={x} position={[x, 0, 0]}>
          <mesh><boxGeometry args={[w, hgt, 0.012]} />{frameMat}</mesh>
          <mesh position={[0, 0, 0.007]}><boxGeometry args={[w - 0.014, hgt - 0.014, 0.004]} />{sp.frame === "d3" ? <meshStandardMaterial color={i === 0 ? "#e63946" : "#2ec4ff"} transparent opacity={0.8} /> : lensMat}</mesh>
        </group>)}
        {sp.frame === "pixel" && <mesh position={[0, 0.0, 0]}><boxGeometry args={[0.06, 0.03, 0.012]} />{frameMat}</mesh>}
        {sp.frame === "d3" && <mesh position={[0, 0, -0.001]}><boxGeometry args={[0.2, 0.012, 0.01]} /><meshStandardMaterial color="#f4f1ea" /></mesh>}
        {bridge}{arms}
      </group>;
    }
    case "aviator":
      return <group position={[0, Y - 0.004, Z]}>
        {xs.map((x) => <group key={x} position={[x, 0, 0]}>
          <mesh scale={[1, 1.12, 0.25]}><sphereGeometry args={[0.046, 14, 10]} />{lensMat}</mesh>
          <mesh scale={[1, 1.12, 1]}><torusGeometry args={[0.046, 0.005, 6, 22]} />{frameMat}</mesh>
        </group>)}
        <mesh position={[0, 0.02, 0]}><boxGeometry args={[0.045, 0.006, 0.006]} />{frameMat}</mesh>{bridge}{arms}
      </group>;
    case "cat":
      return <group position={[0, Y, Z]}>
        {xs.map((x) => <group key={x} position={[x, 0, 0]} rotation={[0, 0, Math.sign(x) * -0.2]}>
          <mesh scale={[1.25, 0.82, 0.3]}><sphereGeometry args={[0.04, 14, 10]} />{lensMat}</mesh>
          <mesh scale={[1.25, 0.82, 1]}><torusGeometry args={[0.04, 0.0075, 6, 22]} />{frameMat}</mesh>
          <mesh position={[Math.sign(x) * 0.052, 0.03, 0]} rotation={[0, 0, Math.sign(x) * -0.6]}><coneGeometry args={[0.014, 0.04, 4]} />{frameMat}</mesh>
        </group>)}
        {bridge}{arms}
      </group>;
    case "star":
      return <group position={[0, Y, Z + 0.001]}>
        {xs.map((x) => <mesh key={x} position={[x, 0, 0]} rotation={[Math.PI / 2, 0, 0]}><cylinderGeometry args={[0.05, 0.05, 0.014, 5]} /><meshStandardMaterial color={c} metalness={0.4} roughness={0.3} emissive={c} emissiveIntensity={0.3} /></mesh>)}
        {bridge}{arms}
      </group>;
    case "heart":
      return <group position={[0, Y, Z]}>
        {xs.map((x) => <group key={x} position={[x, 0, 0]} scale={[1, 1, 0.3]}>
          {[-1, 1].map((sd) => <mesh key={sd} position={[sd * 0.016, 0.014, 0]}><sphereGeometry args={[0.025, 10, 8]} /><meshStandardMaterial color={c} roughness={0.35} transparent opacity={0.85} /></mesh>)}
          <mesh position={[0, -0.016, 0]} rotation={[0, 0, Math.PI]} scale={[1.05, 1, 1]}><coneGeometry args={[0.04, 0.05, 4]} /><meshStandardMaterial color={c} roughness={0.35} transparent opacity={0.85} /></mesh>
        </group>)}
        {bridge}{arms}
      </group>;
    case "visor":
      return <group position={[0, Y + 0.002, Z + 0.003]}>
        <mesh scale={[1, 0.36, 0.25]}><sphereGeometry args={[0.155, 18, 10]} /><meshStandardMaterial color={c} emissive={c} emissiveIntensity={sp.glow ? 0.7 : 0.35} transparent opacity={0.82} roughness={0.2} /></mesh>
        <mesh position={[0, 0.0, 0.008]} scale={[0.88, 0.05, 0.1]}><sphereGeometry args={[0.15, 12, 6]} /><meshBasicMaterial color="#ffffff" transparent opacity={0.45} /></mesh>
      </group>;
    case "goggles": case "welder": case "ski": {
      const big = sp.frame === "ski";
      return <group position={[0, Y, Z - 0.002]}>
        {big
          ? <mesh scale={[1, 0.42, 0.28]}><sphereGeometry args={[0.15, 18, 12]} />{lensMat}</mesh>
          : xs.map((x) => <group key={x} position={[x, 0, 0]}>
            <mesh rotation={[Math.PI / 2, 0, 0]}><cylinderGeometry args={[0.05, 0.05, 0.034, 18]} />{frameMat}</mesh>
            <mesh position={[0, 0, 0.018]} rotation={[Math.PI / 2, 0, 0]}><cylinderGeometry args={[0.038, 0.038, 0.01, 18]} />{lensMat}</mesh>
            {sp.frame === "welder" && <mesh position={[0, 0.045, 0.01]} rotation={[0.3, 0, 0]}><boxGeometry args={[0.08, 0.016, 0.03]} />{frameMat}</mesh>}
          </group>)}
        {big && <mesh rotation={[Math.PI / 2, 0, 0]} scale={[1.02, 0.46, 1]}><torusGeometry args={[0.15, 0.01, 6, 28]} />{frameMat}</mesh>}
        <mesh position={[0, 0.0, -0.05]} rotation={[Math.PI / 2, 0, 0]} scale={[1, 1.05, 1]}><torusGeometry args={[0.16, 0.012, 6, 24]} /><meshStandardMaterial color={darken(c, 0.5)} roughness={0.9} /></mesh>
        {!big && <mesh><boxGeometry args={[0.04, 0.014, 0.014]} />{frameMat}</mesh>}
      </group>;
    }
    case "shutter":
      return <group position={[0, Y, Z]}>
        {xs.map((x) => <group key={x} position={[x, 0, 0]}>
          {[-0.016, 0, 0.016].map((y) => <mesh key={y} position={[0, y, 0]}><boxGeometry args={[0.088, 0.011, 0.01]} /><meshStandardMaterial color={c} roughness={0.4} emissive={sp.glow ? c : "#000000"} emissiveIntensity={sp.glow ? 0.8 : 0} /></mesh>)}
          <mesh position={[0, 0, -0.004]}><boxGeometry args={[0.092, 0.05, 0.004]} />{frameMat}</mesh>
        </group>)}
        {bridge}{arms}
      </group>;
    case "hex":
      return <group position={[0, Y, Z]}>
        {xs.map((x) => <group key={x} position={[x, 0, 0]} rotation={[Math.PI / 2, 0, Math.PI / 6]}>
          <mesh><cylinderGeometry args={[0.046, 0.046, 0.012, 6]} />{frameMat}</mesh>
          <mesh position={[0, 0.005, 0]}><cylinderGeometry args={[0.036, 0.036, 0.012, 6]} />{lensMat}</mesh>
        </group>)}
        {bridge}{arms}
      </group>;
    case "diamond":
      return <group position={[0, Y, Z]}>
        {xs.map((x) => <group key={x} position={[x, 0, 0]} rotation={[Math.PI / 2, 0, Math.PI / 4]}>
          <mesh><cylinderGeometry args={[0.05, 0.05, 0.012, 4]} />{frameMat}</mesh>
          <mesh position={[0, 0.005, 0]}><cylinderGeometry args={[0.038, 0.038, 0.012, 4]} />{lensMat}</mesh>
        </group>)}
        {bridge}{arms}
      </group>;
    default: // rimless
      return <group position={[0, Y, Z]}>
        {xs.map((x) => <mesh key={x} position={[x, 0, 0]} rotation={[Math.PI / 2, 0, 0]}><cylinderGeometry args={[0.04, 0.04, 0.005, 18]} />{lensMat}</mesh>)}
        {bridge}{arms}
      </group>;
  }
}

// ------------------------------------------------------------------ headphones

function Ears({ item }: { item: ShopItem }) {
  const sp = useMemo(() => cansSpec(item.id), [item.id]);
  const c = item.color || "#4cd9a6";
  const cup = sp.metal ? <meshStandardMaterial color={c} metalness={0.75} roughness={0.25} /> : sp.glow ? <meshStandardMaterial color={c} emissive={c} emissiveIntensity={0.7} roughness={0.4} /> : <meshStandardMaterial color={c} roughness={0.55} />;
  const bandC = sp.kind === "muffs" ? "#f4f1ea" : sp.kind === "retro" ? mix(c, "#2a1d12", 0.7) : "#2c323b";
  const cushion = <meshStandardMaterial color={darken(c, 0.5)} roughness={1} />;
  if (sp.kind === "buds") {
    return <group position={[0, 0.15, 0]}>
      {[1, -1].map((sd) => <group key={sd} position={[sd * 0.164, -0.02, 0.01]}>
        <mesh><sphereGeometry args={[0.03, 10, 8]} /><meshStandardMaterial color="#f4f1ea" roughness={0.4} /></mesh>
        <mesh position={[sd * 0.012, -0.05, 0.006]} rotation={[0, 0, sd * 0.1]}><capsuleGeometry args={[0.009, 0.06, 4, 8]} /><meshStandardMaterial color="#f4f1ea" roughness={0.4} /></mesh>
        <mesh position={[sd * 0.018, 0.0, 0]} scale={[0.5, 1, 1]}><sphereGeometry args={[0.022, 8, 6]} />{cup}</mesh>
      </group>)}
    </group>;
  }
  const big = sp.kind === "retro" ? 0.085 : sp.kind === "dj" ? 0.075 : 0.065;
  return (
    <group position={[0, 0.15, 0]}>
      <mesh><torusGeometry args={[0.185, sp.kind === "muffs" ? 0.012 : sp.kind === "retro" ? 0.022 : 0.016, 8, 26, Math.PI]} /><meshStandardMaterial color={bandC} roughness={0.7} metalness={sp.metal ? 0.4 : 0} /></mesh>
      {sp.kind === "retro" && <mesh position={[0, 0.185, 0]} scale={[2.2, 0.5, 1]}><sphereGeometry args={[0.03, 10, 8]} />{cushion}</mesh>}
      {sp.kind === "cat" && [-1, 1].map((sd) => <mesh key={sd} position={[sd * 0.09, 0.17, 0]} rotation={[0, 0, -sd * 0.3]}><coneGeometry args={[0.04, 0.07, 4]} />{cup}</mesh>)}
      {sp.kind === "neon" && <mesh position={[0, 0, 0]}><torusGeometry args={[0.186, 0.006, 6, 26, Math.PI]} /><meshStandardMaterial color={c} emissive={c} emissiveIntensity={1.4} /></mesh>}
      {[0.185, -0.185].map((x) => {
        const sd = Math.sign(x);
        return <group key={x} position={[x, 0, 0]} rotation={sp.kind === "dj" ? [0, 0, sd * 0.1] : [0, 0, 0]}>
          {sp.kind === "muffs"
            ? <mesh rotation={[0, 0, Math.PI / 2]} scale={[0.6, 1, 1]}><sphereGeometry args={[0.078, 14, 10]} /><meshStandardMaterial color={c} roughness={1} /></mesh>
            : <>
              <mesh rotation={[0, 0, Math.PI / 2]}><cylinderGeometry args={[big, big, 0.05, 18]} />{cup}</mesh>
              <mesh position={[-sd * 0.03, 0, 0]} rotation={[0, 0, Math.PI / 2]}><cylinderGeometry args={[big * 0.9, big * 0.9, 0.02, 18]} />{cushion}</mesh>
              <mesh position={[sd * 0.028, 0, 0]} rotation={[0, 0, Math.PI / 2]}><torusGeometry args={[big * 0.62, 0.007, 6, 20]} /><meshStandardMaterial color={sp.glow ? c : lighten(c, 0.5)} emissive={sp.glow ? c : "#000000"} emissiveIntensity={sp.glow ? 1.2 : 0} metalness={sp.metal ? 0.8 : 0} roughness={0.3} /></mesh>
            </>}
        </group>;
      })}
    </group>
  );
}

/** Hat, glasses and headphones: children of the head group. */
export function HeadWear({ worn }: { worn: Worn }) {
  const it = useItems(worn);
  return (
    <>
      {it.hat && <Hat item={it.hat} />}
      {it.face && <Face item={it.face} />}
      {it.ears && <Ears item={it.ears} />}
    </>
  );
}

// ------------------------------------------------------------------ outfit, scarf, badge (children of the torso group)

const FRONT = 0.186;
/** Radius of the torso capsule at height y, so hems, belts and stripes hug the body instead of floating off it. */
const ringR = (y: number) => Math.sqrt(Math.max(0.0004, 0.04 - Math.max(0, Math.abs(y - 0.16) - 0.02) ** 2));

/** The cut of the jacket / hoodie / vest...: lapels, pockets, hems and buttons drawn over the torso capsule. */
export function OutfitWear({ o, fallback }: { o: OutfitSpec & { color: string; worn: boolean }; fallback: string }) {
  const c = o.color || fallback;
  const rib = darken(c, 0.18);
  const m = (col: string, rough = 0.8) => <meshStandardMaterial color={col} roughness={o.shiny ? 0.3 : rough} metalness={o.shiny ? 0.35 : 0} />;
  const trim = <meshStandardMaterial color={o.trim} roughness={o.shiny ? 0.25 : 0.6} metalness={o.shiny ? 0.7 : 0.05} emissive={o.shiny ? o.trim : "#000000"} emissiveIntensity={o.shiny ? 0.25 : 0} />;
  const button = (y: number, x = 0) => <mesh key={`${y}${x}`} position={[x, y, FRONT + 0.012]} scale={[1, 1, 0.5]}><sphereGeometry args={[0.014, 8, 6]} />{trim}</mesh>;
  const hem = <mesh position={[0, 0.0, 0]} rotation={[Math.PI / 2, 0, 0]} scale={[1.08, 0.9, 1]}><torusGeometry args={[ringR(0.0) - 0.006, 0.02, 8, 28]} />{m(rib)}</mesh>;
  const stripe = o.stripe && (
    <mesh position={[0, 0.17, 0]} rotation={[Math.PI / 2, 0, 0]} scale={[1.08, 0.9, 1]}><torusGeometry args={[ringR(0.17) - 0.006, 0.012, 6, 28]} />{trim}</mesh>
  );
  switch (o.style) {
    case "hoodie":
      return <>
        <mesh position={[0, 0.3, -0.07]} rotation={[0.35, 0, 0]} scale={[1.15, 0.85, 0.7]}><torusGeometry args={[0.13, 0.055, 10, 22]} />{m(c)}</mesh>
        <mesh position={[0, 0.3, -0.1]} scale={[1.1, 0.7, 0.5]}><sphereGeometry args={[0.12, 12, 10]} />{m(darken(c, 0.12))}</mesh>
        <mesh position={[0, 0.07, FRONT - 0.005]} scale={[1, 1, 0.4]}><boxGeometry args={[0.2, 0.085, 0.05]} />{m(darken(c, 0.08))}</mesh>
        {[0.035, -0.035].map((x) => <mesh key={x} position={[x, 0.21, FRONT + 0.004]}><cylinderGeometry args={[0.006, 0.006, 0.14, 5]} />{trim}</mesh>)}
        {[0.035, -0.035].map((x) => <mesh key={`t${x}`} position={[x, 0.135, FRONT + 0.006]}><sphereGeometry args={[0.012, 6, 6]} />{trim}</mesh>)}
        {hem}{stripe}
      </>;
    case "cardigan":
      return <>
        <mesh position={[0, 0.2, FRONT - 0.012]} scale={[0.55, 1, 0.3]}><sphereGeometry args={[0.1, 10, 8]} /><meshStandardMaterial color={o.shirt} roughness={0.9} /></mesh>
        {[0.14, 0.085, 0.03].map((y) => button(y))}
        {[0.1, -0.1].map((x) => <mesh key={x} position={[x, 0.05, FRONT - 0.008]} scale={[1, 1, 0.3]}><boxGeometry args={[0.07, 0.065, 0.04]} />{m(darken(c, 0.1))}</mesh>)}
        <mesh position={[0, 0.15, FRONT - 0.002]} rotation={[0, 0, 0]} scale={[1, 1, 0.25]}><boxGeometry args={[0.012, 0.22, 0.04]} />{m(rib)}</mesh>
        {hem}{stripe}
      </>;
    case "vest":
      return <>
        <mesh position={[0, 0.215, FRONT - 0.015]} scale={[0.5, 1, 0.3]}><sphereGeometry args={[0.1, 10, 8]} /><meshStandardMaterial color={o.shirt} roughness={0.9} /></mesh>
        <mesh position={[0, 0.17, FRONT + 0.002]} scale={[0.5, 1.5, 0.3]}><sphereGeometry args={[0.03, 8, 6]} />{trim}</mesh>
        <mesh position={[0, 0.265, FRONT - 0.005]}><sphereGeometry args={[0.022, 8, 6]} />{trim}</mesh>
        {[0.1, 0.055, 0.01].map((y) => button(y))}
        <mesh position={[0, 0.01, 0]} rotation={[Math.PI / 2, 0, 0]} scale={[1.08, 0.9, 1]}><torusGeometry args={[ringR(0.01) - 0.006, 0.014, 6, 28]} />{trim}</mesh>
        {stripe}
      </>;
    case "jersey":
      return <>
        <mesh position={[0, 0.33, 0.01]} rotation={[Math.PI / 2, 0, 0]} scale={[1, 0.88, 1]}><torusGeometry args={[0.062, 0.02, 8, 22]} />{trim}</mesh>
        <mesh position={[0, 0.13, FRONT - 0.002]} rotation={[Math.PI / 2, 0, 0]} scale={[1, 1, 0.3]}><cylinderGeometry args={[0.062, 0.062, 0.04, 16]} />{trim}</mesh>
        <mesh position={[0, 0.13, FRONT + 0.02]}><boxGeometry args={[0.014, 0.05, 0.006]} /><meshStandardMaterial color={luma(o.trim) > 0.5 ? "#222" : "#fff"} /></mesh>
        <mesh position={[0, 0.25, 0]} rotation={[Math.PI / 2, 0, 0]} scale={[1.08, 0.9, 1]}><torusGeometry args={[ringR(0.25) - 0.006, 0.012, 6, 28]} />{trim}</mesh>
        {hem}
      </>;
    case "overalls":
      return <>
        <mesh position={[0, 0.1, FRONT - 0.008]} scale={[1, 1, 0.3]}><boxGeometry args={[0.2, 0.2, 0.05]} />{m(c)}</mesh>
        <mesh position={[0, 0.12, FRONT + 0.014]} scale={[1, 1, 0.3]}><boxGeometry args={[0.09, 0.06, 0.03]} />{m(darken(c, 0.12))}</mesh>
        {[0.07, -0.07].map((x) => <group key={x}>
          <mesh position={[x, 0.22, FRONT - 0.014]}><boxGeometry args={[0.03, 0.1, 0.012]} />{m(c)}</mesh>
          <mesh position={[x, 0.17, 0]} rotation={[0, -Math.PI / 2, 0]} scale={[1, 0.96, 1]}><torusGeometry args={[0.197, 0.014, 6, 24, Math.PI * 0.78]} />{m(c)}</mesh>
          <mesh position={[x, 0.2, FRONT + 0.0]} scale={[1, 1, 0.5]}><sphereGeometry args={[0.014, 8, 6]} />{trim}</mesh>
        </group>)}
      </>;
    case "trench":
      return <>
        {[1, -1].map((sd) => <mesh key={sd} position={[sd * 0.065, 0.22, FRONT - 0.004]} rotation={[0, 0, -sd * 0.42]} scale={[1, 1, 0.3]}><boxGeometry args={[0.07, 0.2, 0.04]} />{m(darken(c, 0.15))}</mesh>)}
        <mesh position={[0, 0.3, -0.12]} scale={[1.6, 0.5, 0.4]}><boxGeometry args={[0.2, 0.1, 0.08]} />{m(darken(c, 0.15))}</mesh>
        <mesh position={[0, 0.07, 0]} rotation={[Math.PI / 2, 0, 0]} scale={[1.08, 0.9, 1]}><torusGeometry args={[ringR(0.07) - 0.006, 0.016, 6, 28]} />{trim}</mesh>
        <mesh position={[0.03, 0.07, FRONT + 0.004]} scale={[1, 1, 0.4]}><boxGeometry args={[0.04, 0.034, 0.014]} />{trim}</mesh>
        {[0.04, -0.04].map((x) => [0.17, 0.12, 0.02].map((y) => button(y, x)))}
        {[0.2, -0.2].map((x) => <mesh key={x} position={[x * 0.9, 0.3, 0]} scale={[1, 0.5, 2]}><boxGeometry args={[0.06, 0.02, 0.1]} />{trim}</mesh>)}
        {stripe}
      </>;
    default: // blazer
      return <>
        {[1, -1].map((sd) => <mesh key={sd} position={[sd * 0.058, 0.215, FRONT - 0.008]} rotation={[0, 0, -sd * 0.38]} scale={[1, 1, 0.3]}><boxGeometry args={[0.058, 0.2, 0.04]} />{m(darken(c, o.worn ? 0.1 : 0.16), 0.7)}</mesh>)}
        {[0.1, 0.045].map((y) => button(y))}
        {o.worn && <mesh position={[0.1, 0.15, FRONT - 0.004]} rotation={[0, 0, 0.12]} scale={[1, 1, 0.3]}><boxGeometry args={[0.04, 0.026, 0.03]} />{trim}</mesh>}
        {stripe}
      </>;
  }
}

/** Scarf / bow tie and badge: children of the torso group. */
export function BodyWear({ worn }: { worn: Worn }) {
  const it = useItems(worn);
  const neck = it.neck, badge = it.badge;
  return (
    <>
      {neck && <Neck item={neck} />}
      {badge && <Badge item={badge} />}
    </>
  );
}

function Neck({ item }: { item: ShopItem }) {
  const c = item.color || "#d9534f";
  const c2 = item.color2 || (luma(c) > 0.55 ? darken(c, 0.4) : lighten(c, 0.55));
  const sp = useMemo(() => scarfSpec(item.id, !!item.color2), [item.id, item.color2]);
  if (sp.bow) {
    return (
      <group position={[0, 0.285, 0.185]}>
        <mesh><sphereGeometry args={[0.028, 8, 8]} /><meshStandardMaterial color={c} metalness={0.5} roughness={0.3} /></mesh>
        {[0.05, -0.05].map((x) => <mesh key={x} position={[x, 0, 0]} scale={[1.5, 1, 0.55]}><sphereGeometry args={[0.04, 8, 8]} /><meshStandardMaterial color={c} metalness={0.5} roughness={0.3} /></mesh>)}
      </group>
    );
  }
  const rings = (cols: string[], gap = 0.014) => cols.map((col, i) => (
    <mesh key={i} position={[0, 0.012 - i * gap, 0]} rotation={[Math.PI / 2, 0, 0]} scale={[1, 0.88, 1]}><torusGeometry args={[0.1655 + (i % 2) * 0.003, 0.014, 8, 24]} /><meshStandardMaterial color={col} roughness={1} /></mesh>
  ));
  const tailC = (i: number) => (sp.pattern === "stripes" || sp.pattern === "bands") && i % 2 ? c2 : c;
  return (
    <group position={[0, 0.285, 0.01]}>
      <mesh rotation={[Math.PI / 2, 0, 0]} scale={[1, 0.88, 1]}><torusGeometry args={[0.165, 0.048, 10, 24]} /><meshStandardMaterial color={c} roughness={1} /></mesh>
      {sp.pattern === "stripes" && rings([c2, c2], 0.03)}
      {sp.pattern === "bands" && rings([c2], 0)}
      {sp.pattern === "rainbow" && rings(["#f4895a", "#e8b53a", "#4cb987", "#5a95e8"])}
      {sp.pattern === "tartan" && <>{rings([c2, darken(c, 0.35)], 0.028)}{[-0.06, 0.0, 0.06].map((x) => <mesh key={x} position={[x * 2, 0.02, 0.165]} rotation={[0.1, 0, 0]}><boxGeometry args={[0.014, 0.07, 0.014]} /><meshStandardMaterial color={c2} roughness={1} /></mesh>)}</>}
      {sp.pattern === "dots" && Array.from({ length: 10 }, (_, i) => { const a = (i / 10) * Math.PI * 2; return <mesh key={i} position={[Math.sin(a) * 0.165, 0.045, Math.cos(a) * 0.145]} scale={[1, 1, 0.5]} rotation={[0, a, 0]}><sphereGeometry args={[0.014, 6, 6]} /><meshStandardMaterial color={c2} /></mesh>; })}
      {sp.knot && <mesh position={[0.075, -0.015, 0.155]} scale={[1, 1.1, 0.85]}><sphereGeometry args={[0.04, 10, 8]} /><meshStandardMaterial color={c} roughness={1} /></mesh>}
      {[0, 1].map((i) => (
        <group key={i} position={[0.07 + i * 0.022, -0.1 - i * 0.02, 0.172 + i * 0.012]} rotation={[0, 0, 0.12 - i * 0.2]}>
          <mesh><boxGeometry args={[0.075, 0.2 + i * 0.04, 0.026]} /><meshStandardMaterial color={tailC(i)} roughness={1} /></mesh>
          {sp.pattern === "stripes" && <mesh position={[0, -0.06 - i * 0.02, 0.002]}><boxGeometry args={[0.077, 0.022, 0.028]} /><meshStandardMaterial color={c2} roughness={1} /></mesh>}
          {sp.pattern === "tartan" && <mesh position={[0, 0.0, 0.002]}><boxGeometry args={[0.02, 0.2 + i * 0.04, 0.028]} /><meshStandardMaterial color={c2} roughness={1} /></mesh>}
          {sp.pattern === "rainbow" && ["#f4895a", "#e8b53a", "#4cb987", "#5a95e8"].map((cc, k) => <mesh key={k} position={[0, 0.07 - k * 0.04, 0.002]}><boxGeometry args={[0.077, 0.014, 0.028]} /><meshStandardMaterial color={cc} roughness={1} /></mesh>)}
          {sp.fringe && Array.from({ length: 5 }, (_, k) => <mesh key={k} position={[-0.03 + k * 0.015, -0.1 - i * 0.02 - 0.02, 0.0]}><cylinderGeometry args={[0.004, 0.004, 0.04, 4]} /><meshStandardMaterial color={sp.pattern === "plain" ? c : c2} roughness={1} /></mesh>)}
        </group>
      ))}
    </group>
  );
}

function Badge({ item }: { item: ShopItem }) {
  const sp = useMemo(() => badgeSpec(item.id), [item.id]);
  const c = item.color || "#f2c94c";
  const c2 = item.color2 || (luma(c) > 0.6 ? darken(c, 0.4) : "#fffaf0");
  const face = <meshStandardMaterial color={c} metalness={0.45} roughness={0.3} />;
  const inner = <meshStandardMaterial color={c2} metalness={0.2} roughness={0.5} />;
  const flat: [number, number, number] = [Math.PI / 2, 0, 0];
  return (
    <group position={[-0.11, 0.2, FRONT + 0.008]} rotation={[0.1, 0, 0]} scale={1.35}>
      {item.id === "badge_medal" && <mesh position={[0, 0.06, -0.004]}><boxGeometry args={[0.03, 0.07, 0.006]} />{inner}</mesh>}
      {sp.frame === "circle" && <mesh rotation={flat}><cylinderGeometry args={[0.04, 0.04, 0.012, 20]} />{face}</mesh>}
      {sp.frame === "ribbon" && <mesh rotation={flat}><cylinderGeometry args={[0.04, 0.04, 0.012, 20]} />{face}</mesh>}
      {sp.frame === "hex" && <mesh rotation={flat}><cylinderGeometry args={[0.044, 0.044, 0.012, 6]} />{face}</mesh>}
      {sp.frame === "star" && <mesh rotation={[Math.PI / 2, 0, Math.PI / 10]}><cylinderGeometry args={[0.048, 0.048, 0.012, 5]} />{face}</mesh>}
      {sp.frame === "diamond" && <mesh rotation={[Math.PI / 2, 0, Math.PI / 4]}><cylinderGeometry args={[0.05, 0.05, 0.012, 4]} />{face}</mesh>}
      {sp.frame === "shield" && <group><mesh position={[0, 0.012, 0]} scale={[1, 1, 0.3]}><boxGeometry args={[0.075, 0.05, 0.04]} />{face}</mesh><mesh position={[0, -0.028, 0]} rotation={[0, Math.PI / 4, Math.PI]} scale={[1, 1, 0.32]}><coneGeometry args={[0.04, 0.05, 4]} />{face}</mesh></group>}
      {sp.frame === "heart" && <group scale={[1, 1, 0.3]}>{[-1, 1].map((sd) => <mesh key={sd} position={[sd * 0.02, 0.014, 0]}><sphereGeometry args={[0.03, 10, 8]} />{face}</mesh>)}<mesh position={[0, -0.016, 0]} rotation={[0, 0, Math.PI]}><coneGeometry args={[0.05, 0.06, 4]} />{face}</mesh></group>}
      {sp.ring && sp.frame !== "star" && sp.frame !== "heart" && <mesh position={[0, 0, 0.007]}><torusGeometry args={[0.036, 0.004, 6, 22]} />{inner}</mesh>}
      <mesh position={[0, 0, 0.011]} scale={[1, 1, 0.5]}><sphereGeometry args={[0.02, 10, 8]} />{inner}</mesh>
    </group>
  );
}

// ------------------------------------------------------------------ buddy

/** Small companion on the right shoulder; gently bobs unless the user asked for reduced motion. */
export function Buddy({ worn }: { worn: Worn }) {
  const it = useItems(worn).buddy;
  const g = useRef<THREE.Group>(null);
  const base = useRef(0.435);
  useEffect(() => { if (g.current) g.current.position.y = base.current; }, [it]);
  useFrame(({ clock }) => {
    if (!g.current) return;
    const reduce = usePreferences.getState().prefs.reduceMotion;
    g.current.position.y = base.current + (reduce ? 0 : Math.sin(clock.elapsedTime * 3.1) * 0.006);
  });
  if (!it) return null;
  const c1 = it.color || "#f7d046", c2 = it.color2 || "#f4895a";
  const kind = it.id.startsWith("buddy_duck") ? "duck" : it.id.startsWith("buddy_robot") ? "robot" : "parrot";
  const std = (color: string, extra: Partial<THREE.MeshStandardMaterialParameters> = {}) => <meshStandardMaterial color={color} roughness={0.7} {...extra} />;
  const eyes = (z: number, y: number, x = 0.016) => [x, -x].map((xx) => <mesh key={xx} position={[xx, y, z]}><sphereGeometry args={[0.005, 6, 6]} /><meshBasicMaterial color="#1d2128" /></mesh>);
  return (
    <group ref={g} position={[-0.2, 0.435, 0.03]} scale={0.95}>
      {kind === "duck" && (
        <>
          <mesh castShadow scale={[1, 0.85, 1.2]}><sphereGeometry args={[0.052, 12, 10]} />{std(c1)}</mesh>
          <mesh castShadow position={[0, 0.06, 0.03]}><sphereGeometry args={[0.036, 10, 8]} />{std(c1)}</mesh>
          <mesh position={[0, 0.058, 0.07]} scale={[1.2, 0.5, 1]}><sphereGeometry args={[0.017, 8, 6]} />{std(c2 === c1 ? "#f4895a" : "#f4895a")}</mesh>
          {eyes(0.06, 0.07)}
          {[1, -1].map((sd) => <mesh key={sd} position={[sd * 0.048, 0.0, -0.005]} rotation={[0, 0, sd * 0.5]} scale={[0.4, 1, 1.3]}><sphereGeometry args={[0.036, 8, 6]} />{std(darken(c1, 0.1))}</mesh>)}
          <mesh position={[0, 0.01, -0.062]} rotation={[0.6, 0, 0]} scale={[1, 0.6, 1.3]}><coneGeometry args={[0.02, 0.04, 6]} />{std(c1)}</mesh>
          <mesh position={[0, 0.098, 0.03]} rotation={[0.2, 0, 0]} scale={[0.6, 1.4, 0.6]}><sphereGeometry args={[0.012, 6, 6]} />{std(c1)}</mesh>
        </>
      )}
      {kind === "robot" && (
        <>
          <mesh castShadow><boxGeometry args={[0.08, 0.07, 0.07]} />{std(c1, { metalness: 0.4, roughness: 0.4 })}</mesh>
          <mesh position={[0, 0.06, 0]}><cylinderGeometry args={[0.004, 0.004, 0.05, 6]} />{std("#59616d")}</mesh>
          <mesh position={[0, 0.088, 0]}><sphereGeometry args={[0.011, 8, 6]} /><meshBasicMaterial color={c2} toneMapped={false} /></mesh>
          {[0.02, -0.02].map((x) => <mesh key={x} position={[x, 0.006, 0.037]}><sphereGeometry args={[0.011, 8, 6]} /><meshBasicMaterial color={c2} toneMapped={false} /></mesh>)}
          <mesh position={[0, -0.016, 0.036]}><boxGeometry args={[0.03, 0.006, 0.004]} /><meshBasicMaterial color="#10131a" /></mesh>
          {[1, -1].map((sd) => <mesh key={sd} position={[sd * 0.044, 0, 0]}><cylinderGeometry args={[0.009, 0.009, 0.014, 8]} rotation-z={Math.PI / 2} />{std("#59616d")}</mesh>)}
          <mesh position={[0, -0.04, 0]}><boxGeometry args={[0.05, 0.012, 0.05]} />{std(darken(c1, 0.3), { metalness: 0.4 })}</mesh>
        </>
      )}
      {kind === "parrot" && (
        <>
          <mesh castShadow scale={[0.9, 1.35, 0.9]}><sphereGeometry args={[0.045, 12, 10]} />{std(c1)}</mesh>
          <mesh castShadow position={[0, 0.07, 0.01]}><sphereGeometry args={[0.032, 10, 8]} />{std(c1)}</mesh>
          <mesh position={[0, 0.067, 0.045]} rotation={[Math.PI / 2, 0, 0]}><coneGeometry args={[0.013, 0.03, 6]} />{std("#f2c94c")}</mesh>
          {eyes(0.032, 0.08, 0.014)}
          <mesh position={[0, -0.01, -0.03]} rotation={[0.4, 0, 0]}><boxGeometry args={[0.03, 0.09, 0.012]} />{std(c2)}</mesh>
          {[1, -1].map((sd) => <mesh key={sd} position={[sd * 0.04, 0.0, 0]} rotation={[0, 0, sd * 0.2]} scale={[0.35, 1.3, 0.9]}><sphereGeometry args={[0.035, 8, 6]} />{std(mix(c1, c2, 0.5))}</mesh>)}
          <mesh position={[0, 0.1, 0.0]} rotation={[0.2, 0, 0]} scale={[0.5, 1.5, 0.5]}><sphereGeometry args={[0.012, 6, 6]} />{std(c2)}</mesh>
        </>
      )}
    </group>
  );
}

// unused re-export keeps the old name available to callers that only need the jacket color
export function useOutfitColor(worn: Worn): string | undefined {
  return useItems(worn).outfit?.color;
}
void hash;
