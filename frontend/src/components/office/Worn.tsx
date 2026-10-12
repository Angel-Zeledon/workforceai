"use client";
import { useEffect, useMemo, useRef } from "react";
import { useFrame } from "@react-three/fiber";
import * as THREE from "three";
import { useRewards, type ShopItem } from "@/lib/rewards";
import { usePreferences } from "@/lib/preferences";

/**
 * Shop items worn by a character (docs/architecture/rewards.md): simple low-poly meshes parented to the
 * head / torso groups of <Character/>. Static geometry: nothing is allocated per frame (only the shoulder
 * companion bobs by mutating a ref). Item colors come from the catalog (data); the shapes are keyed by item id.
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

/** Jacket color when an outfit is worn (the role color otherwise). */
export function useOutfitColor(worn: Worn): string | undefined {
  return useItems(worn).outfit?.color;
}

/** Which of the role's own accessories a worn item replaces (so two pairs of glasses never overlap). */
export function suppressesAccessory(worn: Worn, kind: string): boolean {
  if (!worn) return false;
  if (worn.face && (kind === "glasses" || kind === "roundglasses")) return true;
  if (worn.ears && (kind === "headphones" || kind === "headset")) return true;
  if (worn.hat && (kind === "hardhat" || kind === "beanie")) return true;
  return false;
}

const std = (color: string, extra: Partial<THREE.MeshStandardMaterialParameters> = {}) => <meshStandardMaterial color={color} roughness={0.7} {...extra} />;

function Hat({ item }: { item: ShopItem }) {
  const c1 = item.color || "#d9534f", c2 = item.color2 || "#f4f1ea";
  switch (item.id) {
    case "hat_cap":
      return (
        <group position={[0, 0.2, 0]}>
          <mesh castShadow><sphereGeometry args={[0.19, 20, 12, 0, Math.PI * 2, 0, Math.PI * 0.5]} />{std(c1)}</mesh>
          <mesh position={[0, 0.01, 0.17]} scale={[1, 0.18, 1]}><sphereGeometry args={[0.13, 14, 8]} />{std(c1)}</mesh>
        </group>
      );
    case "hat_beanie":
      return (
        <group position={[0, 0.2, 0]}>
          <mesh castShadow><sphereGeometry args={[0.19, 20, 12, 0, Math.PI * 2, 0, Math.PI * 0.55]} />{std(c1, { roughness: 1 })}</mesh>
          <mesh position={[0, 0.0, 0]} rotation={[Math.PI / 2, 0, 0]}><torusGeometry args={[0.185, 0.03, 8, 24]} />{std("#f4f1ea", { roughness: 1 })}</mesh>
          <mesh position={[0, 0.2, 0]}><sphereGeometry args={[0.045, 10, 8]} />{std("#f4f1ea", { roughness: 1 })}</mesh>
        </group>
      );
    case "hat_party":
      return (
        <group position={[0.02, 0.2, 0]} rotation={[0, 0, -0.12]}>
          <mesh castShadow position={[0, 0.14, 0]}><coneGeometry args={[0.1, 0.3, 14]} />{std(c1)}</mesh>
          <mesh position={[0, 0.1, 0]}><torusGeometry args={[0.072, 0.012, 6, 16]} />{std(c2)}</mesh>
          <mesh position={[0, 0.3, 0]}><sphereGeometry args={[0.03, 8, 8]} />{std(c2)}</mesh>
        </group>
      );
    case "hat_chef":
      return (
        <group position={[0, 0.2, 0]}>
          <mesh castShadow position={[0, 0.03, 0]}><cylinderGeometry args={[0.165, 0.17, 0.08, 18]} />{std(c1, { roughness: 0.9 })}</mesh>
          <mesh castShadow position={[0, 0.15, 0]} scale={[1, 0.8, 1]}><sphereGeometry args={[0.17, 16, 12]} />{std(c1, { roughness: 0.9 })}</mesh>
        </group>
      );
    case "hat_graduate":
      return (
        <group position={[0, 0.2, 0]}>
          <mesh castShadow position={[0, 0.02, 0]}><cylinderGeometry args={[0.15, 0.17, 0.08, 16]} />{std(c1)}</mesh>
          <mesh castShadow position={[0, 0.085, 0]} rotation={[0, Math.PI / 4, 0]}><boxGeometry args={[0.36, 0.022, 0.36]} />{std(c1)}</mesh>
          <mesh position={[0.17, 0.0, 0.17]}><cylinderGeometry args={[0.006, 0.006, 0.12, 6]} />{std(c2)}</mesh>
          <mesh position={[0.17, -0.07, 0.17]}><sphereGeometry args={[0.017, 8, 6]} />{std(c2)}</mesh>
        </group>
      );
    case "hat_top":
      return (
        <group position={[0, 0.2, 0]}>
          <mesh castShadow position={[0, 0.0, 0]}><cylinderGeometry args={[0.215, 0.215, 0.025, 20]} />{std(c1)}</mesh>
          <mesh castShadow position={[0, 0.12, 0]}><cylinderGeometry args={[0.125, 0.135, 0.22, 18]} />{std(c1)}</mesh>
          <mesh position={[0, 0.045, 0]}><cylinderGeometry args={[0.138, 0.14, 0.05, 18]} />{std(c2)}</mesh>
        </group>
      );
    default: // crown
      return (
        <group position={[0, 0.215, 0]}>
          <mesh castShadow><cylinderGeometry args={[0.15, 0.17, 0.07, 16]} />{std(c1, { metalness: 0.6, roughness: 0.3 })}</mesh>
          {[0, 1, 2, 3, 4].map((i) => {
            const a = (i / 5) * Math.PI * 2;
            return <mesh key={i} castShadow position={[Math.sin(a) * 0.145, 0.085, Math.cos(a) * 0.145]}><coneGeometry args={[0.04, 0.1, 6]} />{std(c1, { metalness: 0.6, roughness: 0.3 })}</mesh>;
          })}
          <mesh position={[0, 0.0, 0.17]}><sphereGeometry args={[0.025, 8, 8]} />{std(c2, { emissive: c2, emissiveIntensity: 0.3 })}</mesh>
        </group>
      );
  }
}

function Face({ item }: { item: ShopItem }) {
  const c1 = item.color || "#1d2128";
  const lens = [0.058, -0.058];
  switch (item.id) {
    case "glasses_star":
      return (
        <group position={[0, 0.17, 0.158]}>
          {lens.map((x) => <mesh key={x} position={[x, 0, 0]} rotation={[Math.PI / 2, 0, 0]}><cylinderGeometry args={[0.048, 0.048, 0.012, 5]} />{std(c1, { metalness: 0.4, roughness: 0.3, emissive: c1, emissiveIntensity: 0.25 })}</mesh>)}
          <mesh><boxGeometry args={[0.05, 0.008, 0.008]} />{std(c1)}</mesh>
        </group>
      );
    case "glasses_visor":
      return (
        <group position={[0, 0.172, 0.16]}>
          <mesh scale={[1, 0.34, 0.25]}><sphereGeometry args={[0.15, 16, 10]} /><meshStandardMaterial color={c1} emissive={c1} emissiveIntensity={0.45} transparent opacity={0.8} roughness={0.2} /></mesh>
        </group>
      );
    case "glasses_heart":
      return (
        <group position={[0, 0.17, 0.158]}>
          {lens.map((x) => <mesh key={x} position={[x, 0, 0]} scale={[1.1, 0.95, 0.3]}><sphereGeometry args={[0.045, 12, 10]} />{std(c1, { roughness: 0.4 })}</mesh>)}
          <mesh><boxGeometry args={[0.04, 0.008, 0.008]} />{std(c1)}</mesh>
        </group>
      );
    default: // sunglasses
      return (
        <group position={[0, 0.17, 0.158]}>
          {lens.map((x) => <mesh key={x} position={[x, 0, 0]}><boxGeometry args={[0.085, 0.058, 0.012]} /><meshStandardMaterial color={c1} roughness={0.15} metalness={0.3} /></mesh>)}
          <mesh position={[0, 0.012, 0]}><boxGeometry args={[0.04, 0.01, 0.01]} />{std(c1)}</mesh>
        </group>
      );
  }
}

function Ears({ item }: { item: ShopItem }) {
  const c1 = item.color || "#4cd9a6";
  const muffs = item.id === "earmuffs_pink";
  const gold = item.id === "headphones_gold";
  return (
    <group position={[0, 0.15, 0]}>
      <mesh><torusGeometry args={[0.185, muffs ? 0.014 : 0.016, 8, 24, Math.PI]} />{std(muffs ? "#f4f1ea" : gold ? c1 : "#2c323b", gold ? { metalness: 0.7, roughness: 0.25 } : {})}</mesh>
      {[0.185, -0.185].map((x) => (
        <mesh key={x} position={[x, 0, 0]} rotation={[0, 0, Math.PI / 2]} scale={muffs ? [1, 0.6, 1] : [1, 1, 1]}>
          {muffs ? <sphereGeometry args={[0.075, 14, 10]} /> : <cylinderGeometry args={[0.065, 0.065, 0.05, 16]} />}
          {muffs ? std(c1, { roughness: 1 }) : std(c1, gold ? { metalness: 0.7, roughness: 0.25 } : { emissive: c1, emissiveIntensity: 0.55 })}
        </mesh>
      ))}
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

/** Scarf / bow tie and badge: children of the torso group. */
export function BodyWear({ worn }: { worn: Worn }) {
  const it = useItems(worn);
  const neck = it.neck, badge = it.badge;
  return (
    <>
      {neck && neck.id === "bowtie_gold" && (
        <group position={[0, 0.285, 0.185]}>
          <mesh><sphereGeometry args={[0.028, 8, 8]} />{std(neck.color || "#d9a82e", { metalness: 0.5, roughness: 0.3 })}</mesh>
          {[0.05, -0.05].map((x) => <mesh key={x} position={[x, 0, 0]} scale={[1.5, 1, 0.55]}><sphereGeometry args={[0.04, 8, 8]} />{std(neck.color || "#d9a82e", { metalness: 0.5, roughness: 0.3 })}</mesh>)}
        </group>
      )}
      {neck && neck.id !== "bowtie_gold" && (
        <group position={[0, 0.285, 0.01]}>
          <mesh rotation={[Math.PI / 2, 0, 0]} scale={[1, 0.88, 1]}><torusGeometry args={[0.165, 0.048, 10, 24]} />{std(neck.color || "#d9534f", { roughness: 1 })}</mesh>
          {neck.id === "scarf_stripes" && <mesh position={[0, -0.01, 0]} rotation={[Math.PI / 2, 0, 0]} scale={[1, 0.88, 1]}><torusGeometry args={[0.168, 0.016, 8, 24]} />{std(neck.color2 || "#f4f1ea", { roughness: 1 })}</mesh>}
          {neck.id === "scarf_rainbow" && ["#f4895a", "#e8b53a", "#4cb987", "#5a95e8"].map((c, i) => (
            <mesh key={c} position={[0, 0.012 - i * 0.012, 0]} rotation={[Math.PI / 2, 0, 0]} scale={[1, 0.88, 1]}><torusGeometry args={[0.166 + (i % 2) * 0.003, 0.014, 8, 24]} />{std(c, { roughness: 1 })}</mesh>
          ))}
          <mesh position={[0.07, -0.1, 0.175]} rotation={[0, 0, 0.12]}><boxGeometry args={[0.075, 0.2, 0.03]} />{std(neck.color || "#d9534f", { roughness: 1 })}</mesh>
          {neck.id === "scarf_stripes" && <mesh position={[0.07, -0.16, 0.19]} rotation={[0, 0, 0.12]}><boxGeometry args={[0.077, 0.025, 0.032]} />{std(neck.color2 || "#f4f1ea", { roughness: 1 })}</mesh>}
        </group>
      )}
      {badge && (
        <group position={[-0.11, 0.2, 0.196]} rotation={[0.1, 0, 0]}>
          <mesh rotation={[Math.PI / 2, 0, 0]}><cylinderGeometry args={[0.04, 0.04, 0.012, badge.id === "badge_star" ? 5 : 16]} />{std(badge.color || "#f2c94c", { metalness: 0.4, roughness: 0.3 })}</mesh>
          <mesh position={[0, 0, 0.008]} rotation={[Math.PI / 2, 0, 0]}><cylinderGeometry args={[0.022, 0.022, 0.008, badge.id === "badge_heart" ? 4 : 12]} />{std("#fffaf0")}</mesh>
          {badge.id === "badge_medal" && <mesh position={[0, 0.06, -0.004]}><boxGeometry args={[0.03, 0.07, 0.006]} />{std(badge.color2 || "#d94f6a")}</mesh>}
        </group>
      )}
    </>
  );
}

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
  return (
    <group ref={g} position={[-0.2, 0.435, 0.03]} scale={0.95}>
      {it.id === "buddy_duck" && (
        <>
          <mesh castShadow scale={[1, 0.85, 1.2]}><sphereGeometry args={[0.052, 12, 10]} />{std(c1)}</mesh>
          <mesh castShadow position={[0, 0.06, 0.03]}><sphereGeometry args={[0.036, 10, 8]} />{std(c1)}</mesh>
          <mesh position={[0, 0.058, 0.07]} scale={[1.2, 0.5, 1]}><sphereGeometry args={[0.017, 8, 6]} />{std("#f4895a")}</mesh>
          <mesh position={[0.016, 0.07, 0.06]}><sphereGeometry args={[0.005, 6, 6]} /><meshBasicMaterial color="#1d2128" /></mesh>
          <mesh position={[-0.016, 0.07, 0.06]}><sphereGeometry args={[0.005, 6, 6]} /><meshBasicMaterial color="#1d2128" /></mesh>
        </>
      )}
      {it.id === "buddy_robot" && (
        <>
          <mesh castShadow><boxGeometry args={[0.08, 0.07, 0.07]} />{std(c1, { metalness: 0.4, roughness: 0.4 })}</mesh>
          <mesh position={[0, 0.06, 0]}><cylinderGeometry args={[0.004, 0.004, 0.05, 6]} />{std("#59616d")}</mesh>
          <mesh position={[0, 0.088, 0]}><sphereGeometry args={[0.011, 8, 6]} /><meshBasicMaterial color={c2} toneMapped={false} /></mesh>
          {[0.02, -0.02].map((x) => <mesh key={x} position={[x, 0.006, 0.037]}><sphereGeometry args={[0.011, 8, 6]} /><meshBasicMaterial color={c2} toneMapped={false} /></mesh>)}
        </>
      )}
      {it.id === "buddy_parrot" && (
        <>
          <mesh castShadow scale={[0.9, 1.35, 0.9]}><sphereGeometry args={[0.045, 12, 10]} />{std(c1)}</mesh>
          <mesh castShadow position={[0, 0.07, 0.01]}><sphereGeometry args={[0.032, 10, 8]} />{std(c1)}</mesh>
          <mesh position={[0, 0.067, 0.045]} rotation={[Math.PI / 2, 0, 0]}><coneGeometry args={[0.013, 0.03, 6]} />{std("#f2c94c")}</mesh>
          <mesh position={[0, -0.01, -0.03]} rotation={[0.4, 0, 0]}><boxGeometry args={[0.03, 0.09, 0.012]} />{std(c2)}</mesh>
        </>
      )}
    </group>
  );
}
