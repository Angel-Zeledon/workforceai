"use client";
import { useMemo } from "react";
import * as THREE from "three";
import { RoundedBox } from "@react-three/drei";
import { useRewards, type ShopItem } from "@/lib/rewards";

/**
 * Shop items placed in the office (docs/architecture/rewards.md). Every office slot has a fixed spot, so no editor is
 * needed; each item is a handful of low-poly meshes with static geometry (nothing is allocated per frame).
 */
const OAK = "#d9c4a3";
const GRAPHITE = "#3a414c";
const STEEL = "#b3bac4";

type V3 = [number, number, number];
/** Floor spot and facing (rotation about Y) of each office slot. The north wall is at z = -10, the glass band behind it. */
export const OFFICE_SLOTS: Record<string, { pos: V3; rot?: number }> = {
  shelf_1: { pos: [-10.2, 0, -9.3] },
  plant_1: { pos: [-7.6, 0, -9.0] },
  poster_1: { pos: [-5.4, 0, -9.0] },
  aquarium_1: { pos: [-3.0, 0, -9.2] },
  lamp_1: { pos: [-0.6, 0, -9.0] },
  poster_2: { pos: [1.4, 0, -9.0] },
  plant_2: { pos: [3.0, 0, -9.0] },
  trophy_1: { pos: [13.0, 0, 2.6], rot: -Math.PI / 2 },
  arcade_1: { pos: [13.2, 0, 5.4], rot: -Math.PI / 2 },
  rug_1: { pos: [-2.5, 0, 4.2] },
  coffee_1: { pos: [11.0, 0.95, -0.35], rot: -Math.PI / 2 },
  window_1: { pos: [-3.4, 1.85, -9.797] },
};

const m = (color: string, extra: Partial<THREE.MeshStandardMaterialParameters> = {}) => <meshStandardMaterial color={color} roughness={0.75} {...extra} />;
const mb = (color: string) => <meshBasicMaterial color={color} toneMapped={false} />;

function Pot({ y = 0, r = 0.2, color = "#c4aa84" }: { y?: number; r?: number; color?: string }) {
  return <mesh castShadow position={[0, y + r * 0.8, 0]}><cylinderGeometry args={[r, r * 0.75, r * 1.6, 14]} />{m(color)}</mesh>;
}

function PlantItem({ item }: { item: ShopItem }) {
  const c = item.color || "#4f8a62";
  switch (item.id) {
    case "plant_cactus":
      return (
        <group>
          <Pot r={0.2} color="#c46a4a" />
          <mesh castShadow position={[0, 0.7, 0]}><capsuleGeometry args={[0.12, 0.6, 6, 12]} />{m(c)}</mesh>
          <mesh castShadow position={[0.22, 0.78, 0]}><capsuleGeometry args={[0.065, 0.22, 4, 8]} />{m(c)}</mesh>
          <mesh castShadow position={[0.17, 0.62, 0]} rotation={[0, 0, Math.PI / 2]}><capsuleGeometry args={[0.05, 0.1, 4, 8]} />{m(c)}</mesh>
          <mesh castShadow position={[-0.2, 0.9, 0]}><capsuleGeometry args={[0.06, 0.2, 4, 8]} />{m(c)}</mesh>
          <mesh position={[-0.15, 0.8, 0]} rotation={[0, 0, Math.PI / 2]}><capsuleGeometry args={[0.045, 0.08, 4, 8]} />{m(c)}</mesh>
          <mesh position={[0, 1.08, 0.02]}><sphereGeometry args={[0.04, 8, 8]} />{m("#ee7aa8")}</mesh>
        </group>
      );
    case "plant_bonsai":
      return (
        <group>
          <mesh castShadow position={[0, 0.2, 0]}><boxGeometry args={[0.6, 0.4, 0.4]} />{m("#59616d")}</mesh>
          <mesh castShadow position={[0, 0.72, 0]} rotation={[0, 0, 0.12]}><cylinderGeometry args={[0.05, 0.08, 0.6, 8]} />{m("#6b4a36")}</mesh>
          {[[-0.18, 1.05, 0, 0.26], [0.2, 0.98, 0.02, 0.22], [0, 1.22, 0, 0.22]].map(([x, y, z, r], i) => <mesh key={i} castShadow position={[x, y, z]} scale={[1.25, 0.8, 1]}><sphereGeometry args={[r, 12, 10]} />{m(c, { roughness: 1 })}</mesh>)}
        </group>
      );
    case "plant_palm":
      return (
        <group>
          <Pot r={0.26} color="#d9c4a3" />
          <mesh castShadow position={[0, 0.9, 0]}><cylinderGeometry args={[0.05, 0.08, 1.3, 8]} />{m("#8a6a4a")}</mesh>
          {Array.from({ length: 7 }).map((_, i) => {
            const a = (i / 7) * Math.PI * 2;
            return <mesh key={i} castShadow position={[Math.sin(a) * 0.4, 1.62, Math.cos(a) * 0.4]} rotation={[Math.cos(a) * 0.9, 0, -Math.sin(a) * 0.9]} scale={[0.22, 0.04, 0.95]}><sphereGeometry args={[0.5, 10, 6]} />{m(c, { roughness: 1 })}</mesh>;
          })}
        </group>
      );
    case "plant_flowers":
      return (
        <group>
          <Pot r={0.22} color="#d9c4a3" />
          {[[-0.1, 0.62, 0, "#4f8a62"], [0.12, 0.75, 0.03, "#4f8a62"], [0, 0.86, -0.04, "#4f8a62"]].map(([x, y, z], i) => <mesh key={i} position={[x as number, (y as number) - 0.15, z as number]}><cylinderGeometry args={[0.012, 0.012, 0.5, 5]} />{m("#4f8a62")}</mesh>)}
          {[[-0.1, 0.9, 0, c], [0.12, 1.02, 0.03, "#f2c94c"], [0, 1.12, -0.04, "#f7f7f7"], [0.02, 0.78, 0.1, c]].map(([x, y, z, col], i) => <mesh key={i} castShadow position={[x as number, y as number, z as number]}><sphereGeometry args={[0.085, 10, 8]} />{m(col as string, { roughness: 0.6 })}</mesh>)}
          <mesh position={[0, 0.42, 0]} scale={[1, 0.5, 1]}><sphereGeometry args={[0.22, 10, 8]} />{m("#4f8a62", { roughness: 1 })}</mesh>
        </group>
      );
    default: // monstera
      return (
        <group>
          <Pot r={0.25} color="#59616d" />
          {Array.from({ length: 7 }).map((_, i) => {
            const a = (i / 7) * Math.PI * 2 + 0.3, h = 0.75 + (i % 3) * 0.22;
            return (
              <group key={i}>
                <mesh position={[Math.sin(a) * 0.12, h * 0.55, Math.cos(a) * 0.12]} rotation={[Math.cos(a) * 0.35, 0, -Math.sin(a) * 0.35]}><cylinderGeometry args={[0.01, 0.014, h, 5]} />{m("#3f6b4c")}</mesh>
                <mesh castShadow position={[Math.sin(a) * 0.32, h + 0.02, Math.cos(a) * 0.32]} rotation={[Math.cos(a) * 0.5 - 0.3, a, 0]} scale={[0.34, 0.03, 0.4]}><sphereGeometry args={[0.5, 10, 8]} />{m(c, { roughness: 0.8 })}</mesh>
              </group>
            );
          })}
        </group>
      );
  }
}

function LampItem({ item }: { item: ShopItem }) {
  const c = item.color || "#f2b45a";
  if (item.id === "lamp_lava") {
    return (
      <group>
        <mesh castShadow position={[0, 0.06, 0]}><cylinderGeometry args={[0.2, 0.24, 0.12, 16]} />{m(GRAPHITE, { metalness: 0.4 })}</mesh>
        <mesh position={[0, 0.62, 0]}><cylinderGeometry args={[0.1, 0.17, 1.0, 16]} /><meshStandardMaterial color={c} emissive={c} emissiveIntensity={0.55} transparent opacity={0.8} /></mesh>
        {[[0, 0.35, 0.05], [0.02, 0.6, -0.02], [-0.02, 0.85, 0.02]].map(([x, y, z], i) => <mesh key={i} position={[x, y, z]}><sphereGeometry args={[0.065 - i * 0.008, 10, 8]} /><meshBasicMaterial color={item.color2 || "#f7a35a"} toneMapped={false} /></mesh>)}
        <mesh castShadow position={[0, 1.2, 0]}><coneGeometry args={[0.1, 0.16, 14]} />{m(GRAPHITE, { metalness: 0.4 })}</mesh>
        <pointLight position={[0, 0.7, 0.3]} intensity={0.8} distance={3.5} color={c} />
      </group>
    );
  }
  const arc = item.id === "lamp_arc";
  return (
    <group>
      <mesh castShadow position={[0, 0.03, 0]}><cylinderGeometry args={[0.26, 0.28, 0.06, 20]} />{m(GRAPHITE, { metalness: 0.3 })}</mesh>
      <mesh castShadow position={[0, 0.85, 0]}><cylinderGeometry args={[0.025, 0.025, 1.7, 8]} />{m(arc ? STEEL : GRAPHITE, { metalness: 0.5 })}</mesh>
      {arc && <mesh castShadow position={[0.45, 1.7, 0]} rotation={[0, 0, 0]}><torusGeometry args={[0.45, 0.022, 8, 20, Math.PI / 2]} />{m(STEEL, { metalness: 0.5 })}</mesh>}
      <mesh castShadow position={[arc ? 0.9 : 0, arc ? 1.28 : 1.62, 0]}><coneGeometry args={[0.24, 0.3, 18, 1, true]} /><meshStandardMaterial color={c} emissive={c} emissiveIntensity={0.55} side={THREE.DoubleSide} roughness={0.9} /></mesh>
      <pointLight position={[arc ? 0.9 : 0, arc ? 1.15 : 1.5, 0]} intensity={0.9} distance={4} color={c} />
    </group>
  );
}

function RugItem({ item }: { item: ShopItem }) {
  const c = item.color || "#5a7fd0";
  const w = 6.4, d = 2.6;
  return (
    <group>
      <mesh receiveShadow position={[0, 0.03, 0]}><boxGeometry args={[w, 0.03, d]} />{m(c, { roughness: 1 })}</mesh>
      <mesh position={[0, 0.05, 0]}><boxGeometry args={[w - 0.5, 0.012, d - 0.5]} />{m(item.color2 || "#f4f1ea", { roughness: 1 })}</mesh>
      <mesh position={[0, 0.06, 0]}><boxGeometry args={[w - 0.8, 0.012, d - 0.8]} />{m(c, { roughness: 1 })}</mesh>
      {item.id === "rug_persian" && [-1.8, 0, 1.8].map((x) => <mesh key={x} position={[x, 0.072, 0]} rotation={[0, Math.PI / 4, 0]}><boxGeometry args={[0.7, 0.01, 0.7]} />{m(item.color2 || "#d9a82e", { roughness: 1 })}</mesh>)}
    </group>
  );
}

function CoffeeItem({ item }: { item: ShopItem }) {
  const c = item.color || "#c0392b";
  const pro = item.id === "coffee_barista";
  return (
    <group>
      <RoundedBox args={[0.5, pro ? 0.7 : 0.55, 0.55]} radius={0.04} smoothness={3} castShadow position={[0, pro ? 0.35 : 0.275, 0]}>{m(c, { roughness: 0.35, metalness: 0.5 })}</RoundedBox>
      <mesh position={[0.01, pro ? 0.5 : 0.4, 0.28]}><boxGeometry args={[0.34, 0.09, 0.012]} />{mb("#1d2128")}</mesh>
      <mesh position={[0.26, 0.25, 0]} rotation={[0, 0, Math.PI / 2]}><cylinderGeometry args={[0.03, 0.03, 0.12, 10]} />{m(STEEL, { metalness: 0.6, roughness: 0.3 })}</mesh>
      <mesh position={[0.34, 0.1, 0]}><cylinderGeometry args={[0.05, 0.04, 0.09, 10]} />{m("#f4efe6")}</mesh>
      {pro && <mesh position={[0, 0.74, 0]}><boxGeometry args={[0.46, 0.03, 0.5]} />{m("#d9a82e", { metalness: 0.7, roughness: 0.25 })}</mesh>}
      <mesh position={[0.18, pro ? 0.62 : 0.46, 0.28]}><sphereGeometry args={[0.018, 8, 6]} />{mb("#7cc8a6")}</mesh>
    </group>
  );
}

function PosterItem({ item }: { item: ShopItem }) {
  const c1 = item.color || "#5a95e8", c2 = item.color2 || "#f4f1ea";
  return (
    <group>
      {[-0.42, 0.42].map((x) => <mesh key={x} castShadow position={[x, 0.85, -0.15]} rotation={[0.1, 0, 0]}><cylinderGeometry args={[0.025, 0.025, 1.7, 6]} />{m(OAK)}</mesh>)}
      <mesh castShadow position={[0, 1.15, -0.02]}><boxGeometry args={[1.05, 1.45, 0.05]} />{m("#fffaf0")}</mesh>
      <mesh position={[0, 1.15, 0.01]}><planeGeometry args={[0.9, 1.3]} />{mb(c1)}</mesh>
      {item.id === "poster_mountain" ? (
        <>
          <mesh position={[-0.12, 0.98, 0.02]}><coneGeometry args={[0.38, 0.62, 4]} />{mb(c2)}</mesh>
          <mesh position={[0.2, 0.88, 0.025]}><coneGeometry args={[0.28, 0.42, 4]} />{mb("#d9e4f4")}</mesh>
          <mesh position={[0.1, 1.48, 0.02]}><circleGeometry args={[0.11, 16]} />{mb("#f2c94c")}</mesh>
        </>
      ) : item.id === "poster_rocket" ? (
        <>
          <mesh position={[0, 1.2, 0.02]}><capsuleGeometry args={[0.1, 0.4, 4, 10]} />{mb(c2)}</mesh>
          <mesh position={[0, 1.5, 0.02]}><coneGeometry args={[0.1, 0.2, 10]} />{mb("#d94f6a")}</mesh>
          <mesh position={[0, 0.82, 0.02]} rotation={[0, 0, Math.PI]}><coneGeometry args={[0.07, 0.2, 8]} />{mb("#f2c94c")}</mesh>
        </>
      ) : item.id === "poster_abstract" ? (
        <>
          <mesh position={[-0.15, 1.3, 0.02]}><circleGeometry args={[0.24, 20]} />{mb(c2)}</mesh>
          <mesh position={[0.17, 1.0, 0.025]} rotation={[0, 0, 0.4]}><boxGeometry args={[0.38, 0.38, 0.005]} />{mb("#ee7aa8")}</mesh>
        </>
      ) : (
        <>
          {[0, 1, 2, 3].map((i) => <mesh key={i} position={[0, 0.82 + i * 0.12, 0.02]}><planeGeometry args={[0.8, 0.07]} />{mb(i % 2 ? c2 : "#45b8cc")}</mesh>)}
          <mesh position={[0, 1.42, 0.02]}><circleGeometry args={[0.16, 18]} />{mb("#f2c94c")}</mesh>
        </>
      )}
    </group>
  );
}

const BOOKS = ["#d9534f", "#5a95e8", "#f2c94c", "#4cb987", "#8f78c9", "#f4895a", "#45b8cc", "#ee7aa8"];
function ShelfItem({ item }: { item: ShopItem }) {
  const c = item.color || OAK;
  const modern = item.id === "bookshelf_modern";
  const shelves = [0.05, 0.55, 1.05, 1.55, 2.05];
  const books = useMemo(() => {
    const out: { x: number; y: number; h: number; w: number; c: string }[] = [];
    for (let s = 0; s < 4; s++) {
      let x = -0.85;
      for (let i = 0; i < 10 && x < 0.8; i++) {
        const w = 0.07 + ((i * 7 + s * 3) % 5) * 0.012, h = 0.3 + ((i * 5 + s * 2) % 4) * 0.045;
        out.push({ x: x + w / 2, y: shelves[s] + 0.025 + h / 2, h, w, c: BOOKS[(i + s * 3) % BOOKS.length] });
        x += w + 0.012 + (i === 6 && s % 2 ? 0.25 : 0);
      }
    }
    return out;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  return (
    <group>
      {[-0.95, 0.95].map((x) => <mesh key={x} castShadow position={[x, 1.05, 0]}><boxGeometry args={[0.05, 2.1, 0.4]} />{m(c)}</mesh>)}
      {shelves.map((y) => <mesh key={y} castShadow position={[0, y, 0]}><boxGeometry args={[1.95, 0.05, 0.4]} />{m(c)}</mesh>)}
      <mesh position={[0, 1.05, -0.19]}><boxGeometry args={[1.9, 2.1, 0.02]} />{m(modern ? (item.color2 || "#e8b53a") : "#b79c76")}</mesh>
      {books.map((b, i) => <mesh key={i} castShadow position={[b.x, b.y, 0.0]}><boxGeometry args={[b.w, b.h, 0.26]} />{m(b.c, { roughness: 0.9 })}</mesh>)}
    </group>
  );
}

function AquariumItem({ item }: { item: ShopItem }) {
  const c = item.color || "#45b8cc";
  const big = item.id === "aquarium_large";
  const w = big ? 2.2 : 1.4, h = big ? 1.0 : 0.65, d = big ? 0.8 : 0.55;
  const fish = big ? 6 : 3;
  return (
    <group>
      <mesh castShadow position={[0, 0.42, 0]}><boxGeometry args={[w + 0.1, 0.84, d + 0.1]} />{m(GRAPHITE)}</mesh>
      <mesh position={[0, 0.84 + h / 2, 0]}><boxGeometry args={[w, h, d]} /><meshStandardMaterial color={c} transparent opacity={0.4} roughness={0.1} /></mesh>
      <mesh position={[0, 0.86, 0]}><boxGeometry args={[w - 0.04, 0.04, d - 0.04]} />{m("#e6d8b0")}</mesh>
      <mesh position={[0, 0.84 + h + 0.02, 0]}><boxGeometry args={[w + 0.06, 0.04, d + 0.06]} />{m(GRAPHITE)}</mesh>
      {Array.from({ length: fish }).map((_, i) => (
        <group key={i} position={[-w / 2 + 0.25 + (i * (w - 0.5)) / Math.max(1, fish - 1), 0.84 + h * (0.35 + ((i * 37) % 50) / 100), ((i % 3) - 1) * d * 0.22]}>
          <mesh scale={[1.4, 0.8, 0.6]}><sphereGeometry args={[0.06, 8, 6]} />{mb(["#f4895a", "#f2c94c", "#ee7aa8"][i % 3])}</mesh>
          <mesh position={[-0.09, 0, 0]} rotation={[0, 0, Math.PI / 2]}><coneGeometry args={[0.04, 0.07, 4]} />{mb(["#f4895a", "#f2c94c", "#ee7aa8"][i % 3])}</mesh>
        </group>
      ))}
      {[-0.3, 0.2].map((x, i) => <mesh key={x} position={[x * (w / 1.4), 0.95, -d * 0.2]}><coneGeometry args={[0.05, 0.28 + i * 0.1, 5]} />{m("#4f8a62", { roughness: 1 })}</mesh>)}
      <pointLight position={[0, 1.4, 0.3]} intensity={0.35} distance={2.5} color={c} />
    </group>
  );
}

function ArcadeItem({ item }: { item: ShopItem }) {
  const c1 = item.color || "#5a7fd0", c2 = item.color2 || "#ee7aa8";
  if (item.id === "arcade_pinball") {
    return (
      <group>
        {[[-0.35, -0.6], [0.35, -0.6], [-0.35, 0.6], [0.35, 0.6]].map(([x, z], i) => <mesh key={i} position={[x, 0.45, z]}><boxGeometry args={[0.07, 0.9, 0.07]} />{m(STEEL, { metalness: 0.5 })}</mesh>)}
        <group position={[0, 0.95, 0]} rotation={[0.18, 0, 0]}>
          <mesh castShadow><boxGeometry args={[0.8, 0.14, 1.5]} />{m(c1)}</mesh>
          <mesh position={[0, 0.075, 0]}><boxGeometry args={[0.7, 0.01, 1.4]} />{mb("#1b2238")}</mesh>
          {[[-0.18, -0.3], [0.2, -0.1], [0, 0.15], [-0.22, 0.3]].map(([x, z], i) => <mesh key={i} position={[x, 0.1, z]}><cylinderGeometry args={[0.06, 0.06, 0.04, 10]} />{mb(i % 2 ? c2 : "#f2c94c")}</mesh>)}
          <mesh position={[0, 0.2, -0.78]}><boxGeometry args={[0.8, 0.5, 0.06]} />{m(c1)}</mesh>
          <mesh position={[0, 0.22, -0.745]}><planeGeometry args={[0.66, 0.34]} />{mb(c2)}</mesh>
        </group>
      </group>
    );
  }
  return (
    <group>
      <mesh castShadow position={[0, 0.9, 0]}><boxGeometry args={[0.85, 1.8, 0.8]} />{m(c1)}</mesh>
      <mesh castShadow position={[0, 1.8, 0.0]}><boxGeometry args={[0.85, 0.2, 0.8]} />{m(c2)}</mesh>
      <mesh position={[0, 1.2, 0.41]} rotation={[-0.08, 0, 0]}><planeGeometry args={[0.62, 0.5]} />{mb("#1b2238")}</mesh>
      <mesh position={[0, 1.2, 0.415]}><planeGeometry args={[0.4, 0.06]} />{mb(c2)}</mesh>
      <mesh castShadow position={[0, 0.82, 0.38]}><boxGeometry args={[0.8, 0.07, 0.34]} />{m(GRAPHITE)}</mesh>
      <mesh position={[-0.18, 0.9, 0.4]}><cylinderGeometry args={[0.012, 0.012, 0.12, 6]} />{m("#d94f6a")}</mesh>
      <mesh position={[-0.18, 0.97, 0.4]}><sphereGeometry args={[0.03, 8, 6]} />{m("#d94f6a")}</mesh>
      {[0.05, 0.14, 0.23].map((x, i) => <mesh key={x} position={[x, 0.865, 0.4]}><cylinderGeometry args={[0.028, 0.028, 0.03, 10]} />{mb(["#f2c94c", "#4cb987", "#5a95e8"][i])}</mesh>)}
      <pointLight position={[0, 1.2, 0.9]} intensity={0.5} distance={2.6} color={c2} />
    </group>
  );
}

function TrophyItem({ item }: { item: ShopItem }) {
  const c = item.color || "#d9a82e";
  const rows = item.id === "trophy_gold" ? 3 : item.id === "trophy_silver" ? 2 : 1;
  const cups = item.id === "trophy_gold" ? 4 : 3;
  return (
    <group>
      <mesh castShadow position={[0, 0.3, 0]}><boxGeometry args={[1.5, 0.6, 0.5]} />{m(GRAPHITE)}</mesh>
      <mesh position={[0, 1.25, 0]}><boxGeometry args={[1.5, 1.3, 0.5]} /><meshStandardMaterial color="#cfe0ee" transparent opacity={0.28} roughness={0.1} /></mesh>
      {[-0.75, 0.75].map((x) => <mesh key={x} position={[x, 1.25, 0]}><boxGeometry args={[0.04, 1.34, 0.54]} />{m(GRAPHITE)}</mesh>)}
      <mesh position={[0, 1.92, 0]}><boxGeometry args={[1.54, 0.05, 0.54]} />{m(GRAPHITE)}</mesh>
      {Array.from({ length: rows }).map((_, r) => (
        <group key={r} position={[0, 0.66 + r * 0.42, 0]}>
          <mesh><boxGeometry args={[1.4, 0.03, 0.44]} />{m("#e9eef5")}</mesh>
          {Array.from({ length: cups }).map((__, i) => (
            <group key={i} position={[-0.5 + (i * 1.0) / Math.max(1, cups - 1), 0.02, 0]}>
              <mesh castShadow position={[0, 0.03, 0]}><cylinderGeometry args={[0.06, 0.07, 0.04, 10]} />{m(GRAPHITE)}</mesh>
              <mesh castShadow position={[0, 0.12, 0]}><cylinderGeometry args={[0.012, 0.012, 0.14, 6]} />{m(c, { metalness: 0.8, roughness: 0.25 })}</mesh>
              <mesh castShadow position={[0, 0.25, 0]}><cylinderGeometry args={[0.085, 0.04, 0.14, 12]} />{m(c, { metalness: 0.8, roughness: 0.25 })}</mesh>
            </group>
          ))}
        </group>
      ))}
    </group>
  );
}

// ---- window views: painted onto a plane in front of the north glass band ----
const viewTextures = new Map<string, THREE.CanvasTexture>();
function viewTexture(item: ShopItem): THREE.CanvasTexture {
  let t = viewTextures.get(item.id);
  if (t) return t;
  const W = 1024, H = 72;
  const c = document.createElement("canvas");
  c.width = W; c.height = H;
  const g = c.getContext("2d")!;
  const c1 = item.color || "#9fb0d8", c2 = item.color2 || "#6f86c9";
  const sky = g.createLinearGradient(0, 0, 0, H);
  const night = item.id === "view_night" || item.id === "view_aurora";
  if (night) { sky.addColorStop(0, "#0c1020"); sky.addColorStop(1, "#27304f"); }
  else if (item.id === "view_beach") { sky.addColorStop(0, "#86c8ee"); sky.addColorStop(1, "#d6ecf7"); }
  else if (item.id === "view_forest") { sky.addColorStop(0, "#a9d6ee"); sky.addColorStop(1, "#e6f1e0"); }
  else { sky.addColorStop(0, "#9ec3e6"); sky.addColorStop(1, "#dfeaf5"); }
  g.fillStyle = sky; g.fillRect(0, 0, W, H);
  let seed = item.id.length * 97;
  const rnd = () => { seed = (seed * 16807) % 2147483647; return seed / 2147483647; };
  if (night) for (let i = 0; i < 90; i++) { g.fillStyle = `rgba(255,255,255,${0.3 + rnd() * 0.6})`; g.fillRect(rnd() * W, rnd() * H * 0.6, 1.5, 1.5); }
  if (item.id === "view_aurora") {
    for (let k = 0; k < 3; k++) {
      const grad = g.createLinearGradient(0, 0, 0, H * 0.8);
      grad.addColorStop(0, "rgba(76,217,166,0)"); grad.addColorStop(0.5, `rgba(${k ? "143,120,201" : "76,217,166"},0.55)`); grad.addColorStop(1, "rgba(76,217,166,0)");
      g.fillStyle = grad; g.beginPath(); g.moveTo(0, H);
      for (let x = 0; x <= W; x += 32) g.lineTo(x, H * 0.4 + Math.sin(x / 90 + k * 2) * 12 - k * 6);
      g.lineTo(W, H); g.closePath(); g.fill();
    }
  }
  if (item.id === "view_beach") {
    g.fillStyle = c1; g.fillRect(0, H * 0.5, W, H * 0.22);
    g.fillStyle = "rgba(255,255,255,.5)"; for (let x = 0; x < W; x += 60) g.fillRect(x + rnd() * 20, H * 0.6, 28, 2);
    g.fillStyle = c2; g.fillRect(0, H * 0.72, W, H * 0.28);
    g.fillStyle = "#f2c94c"; g.beginPath(); g.arc(820, 18, 12, 0, Math.PI * 2); g.fill();
  } else if (item.id === "view_forest") {
    for (let x = -10; x < W; x += 26) {
      const h = 30 + rnd() * 30;
      g.fillStyle = rnd() > 0.5 ? c1 : "#3f7050";
      g.beginPath(); g.moveTo(x, H); g.lineTo(x + 13, H - h); g.lineTo(x + 26, H); g.closePath(); g.fill();
    }
  } else {
    for (let x = 0; x < W; x += 22 + rnd() * 18) {
      const bw = 20 + rnd() * 22, bh = 18 + rnd() * 40;
      g.fillStyle = night ? "#161c30" : rnd() > 0.5 ? c1 : "#8ea2c8";
      g.fillRect(x, H - bh, bw, bh);
      if (night) { g.fillStyle = "#f2c94c"; for (let wy = H - bh + 4; wy < H - 4; wy += 8) if (rnd() > 0.45) g.fillRect(x + 4 + rnd() * (bw - 10), wy, 3, 3); }
    }
  }
  t = new THREE.CanvasTexture(c);
  t.colorSpace = THREE.SRGBColorSpace;
  viewTextures.set(item.id, t);
  return t;
}
function WindowItem({ item }: { item: ShopItem }) {
  const tex = useMemo(() => viewTexture(item), [item]);
  return (
    <mesh>
      <planeGeometry args={[20.5, 1.45]} />
      <meshBasicMaterial map={tex} toneMapped={false} />
    </mesh>
  );
}

function ItemMesh({ item }: { item: ShopItem }) {
  switch (item.category) {
    case "plant": return <PlantItem item={item} />;
    case "lamp": return <LampItem item={item} />;
    case "rug": return <RugItem item={item} />;
    case "coffee": return <CoffeeItem item={item} />;
    case "poster": return <PosterItem item={item} />;
    case "shelf": return <ShelfItem item={item} />;
    case "aquarium": return <AquariumItem item={item} />;
    case "arcade": return <ArcadeItem item={item} />;
    case "trophy": return <TrophyItem item={item} />;
    case "window": return <WindowItem item={item} />;
    default: return null;
  }
}

/** Everything the organization placed in the office (one item per slot). */
export function OfficeItems() {
  const office = useRewards((s) => s.office);
  const catalog = useRewards((s) => s.catalog);
  return (
    <>
      {Object.entries(office).map(([slot, id]) => {
        const spot = OFFICE_SLOTS[slot];
        const item = catalog.find((i) => i.id === id);
        if (!spot || !item) return null;
        return (
          <group key={slot} position={spot.pos} rotation={[0, spot.rot ?? 0, 0]} userData={{ slot, item: id }}>
            <ItemMesh item={item} />
          </group>
        );
      })}
    </>
  );
}
