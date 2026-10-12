"use client";
import { useEffect, useRef } from "react";
import { useFrame } from "@react-three/fiber";
import { Billboard } from "@react-three/drei";
import * as THREE from "three";
import { useRewards, type CoinFloat } from "@/lib/rewards";
import { usePreferences } from "@/lib/preferences";
import { headPos } from "./registry";

/** "+N" with a coin over the agent that finished a task. One texture per amount, cached: nothing is allocated per frame. */
const textures = new Map<number, THREE.CanvasTexture>();
function textureFor(amount: number): THREE.CanvasTexture {
  let tex = textures.get(amount);
  if (tex) return tex;
  const W = 220, H = 88;
  const c = document.createElement("canvas");
  c.width = W; c.height = H;
  const g = c.getContext("2d")!;
  g.beginPath(); g.roundRect(3, 3, W - 6, H - 6, 40);
  g.fillStyle = "rgba(255,250,235,0.96)"; g.fill();
  g.lineWidth = 4; g.strokeStyle = "#d2a24a"; g.stroke();
  g.beginPath(); g.arc(46, H / 2, 24, 0, Math.PI * 2);
  g.fillStyle = "#e8b53a"; g.fill(); g.lineWidth = 4; g.strokeStyle = "#a86208"; g.stroke();
  g.beginPath(); g.arc(46, H / 2, 16, 0, Math.PI * 2); g.lineWidth = 2.5; g.strokeStyle = "#f7dd8a"; g.stroke();
  g.fillStyle = "#fff6d6"; g.strokeStyle = "#a86208"; g.lineWidth = 2;
  g.beginPath();
  for (let i = 0; i < 10; i++) { const r = i % 2 ? 6 : 13, a = -Math.PI / 2 + (i * Math.PI) / 5; g.lineTo(46 + Math.cos(a) * r, H / 2 + Math.sin(a) * r); }
  g.closePath(); g.fill(); g.stroke();
  g.font = "700 40px Inter, system-ui, sans-serif"; g.textAlign = "left"; g.textBaseline = "middle";
  g.fillStyle = "#6e4205"; g.fillText(`+${amount}`, 84, H / 2 + 2);
  tex = new THREE.CanvasTexture(c);
  tex.colorSpace = THREE.SRGBColorSpace;
  textures.set(amount, tex);
  return tex;
}

const LIFE_MS = 2400;

function Float({ f }: { f: CoinFloat }) {
  const group = useRef<THREE.Group>(null!);
  const mat = useRef<THREE.MeshBasicMaterial>(null!);
  const tex = textureFor(f.amount);
  useFrame(() => {
    const head = headPos.get(f.agent_id);
    if (!head || !group.current) return;
    const reduce = usePreferences.getState().prefs.reduceMotion;
    const t = Math.min(1, (Date.now() - f.at) / LIFE_MS);
    const rise = reduce ? 0 : 1.1 * (1 - (1 - t) * (1 - t));
    group.current.position.set(head.x, head.y + 1.25 + rise, head.z);
    mat.current.opacity = reduce ? (t < 0.8 ? 1 : (1 - t) * 5) : t < 0.7 ? 1 : (1 - t) / 0.3;
    group.current.visible = t < 1;
  });
  return (
    <group ref={group}>
      <Billboard>
        <mesh renderOrder={30}>
          <planeGeometry args={[1.7, 0.68]} />
          <meshBasicMaterial ref={mat} map={tex} transparent depthTest={false} depthWrite={false} toneMapped={false} />
        </mesh>
      </Billboard>
    </group>
  );
}

/** Mounted inside the office canvas. The coins.earned event creates the float over the agent that did the work. */
export function CoinFloats() {
  const floats = useRewards((s) => s.floats);
  useEffect(() => () => { textures.forEach((t) => t.dispose()); textures.clear(); }, []);
  return <>{floats.map((f) => <Float key={f.id} f={f} />)}</>;
}
