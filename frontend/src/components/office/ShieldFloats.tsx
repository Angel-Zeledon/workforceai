"use client";
import { useEffect, useRef } from "react";
import { useFrame } from "@react-three/fiber";
import { Billboard } from "@react-three/drei";
import * as THREE from "three";
import { SHIELD_FLASH_MS, useShield } from "@/lib/shield";
import { shieldOpacity } from "@/lib/shieldLogic";
import { usePreferences } from "@/lib/preferences";
import { feetPos, headPos } from "./registry";

/**
 * B10 visible shield: when content an agent read tried to give it orders (security.alert prompt_injection_suspected),
 * a shield pops over the agent and a ring pulses on the floor around it, then both fade. One shared texture, nothing
 * allocated per frame. Under "reduce motion" the shield stays still and only fades.
 */
let shieldTex: THREE.CanvasTexture | null = null;
function shieldTexture(): THREE.CanvasTexture {
  if (shieldTex) return shieldTex;
  const S = 192;
  const c = document.createElement("canvas");
  c.width = S; c.height = S;
  const g = c.getContext("2d")!;
  const shape = (inset: number) => {
    const l = 30 + inset, r = S - 30 - inset, top = 18 + inset, mid = S * 0.52, tip = S - 14 - inset;
    g.beginPath();
    g.moveTo(S / 2, top);
    g.quadraticCurveTo((S / 2 + r) / 2, top + 14, r, top + 16);
    g.lineTo(r, mid);
    g.quadraticCurveTo(r - 6, tip - 40, S / 2, tip);
    g.quadraticCurveTo(l + 6, tip - 40, l, mid);
    g.lineTo(l, top + 16);
    g.quadraticCurveTo((S / 2 + l) / 2, top + 14, S / 2, top);
    g.closePath();
  };
  shape(0); g.fillStyle = "#1f5f99"; g.fill(); g.lineWidth = 8; g.strokeStyle = "#e9f3fb"; g.stroke();
  shape(14); g.fillStyle = "#2f80c8"; g.fill();
  // a check mark: the content was stopped (treated as data), the agent is fine
  g.beginPath(); g.moveTo(66, 98); g.lineTo(88, 122); g.lineTo(128, 74);
  g.lineWidth = 15; g.lineCap = "round"; g.lineJoin = "round"; g.strokeStyle = "#ffffff"; g.stroke();
  shieldTex = new THREE.CanvasTexture(c);
  shieldTex.colorSpace = THREE.SRGBColorSpace;
  return shieldTex;
}

function Shield({ agentId, at }: { agentId: string; at: number }) {
  const group = useRef<THREE.Group>(null!);
  const ring = useRef<THREE.Mesh>(null!);
  const mat = useRef<THREE.MeshBasicMaterial>(null!);
  const ringMat = useRef<THREE.MeshBasicMaterial>(null!);
  const tex = shieldTexture();
  useFrame(() => {
    const head = headPos.get(agentId);
    if (!head || !group.current) return;
    const reduce = usePreferences.getState().prefs.reduceMotion;
    const t = Math.min(1, (Date.now() - at) / SHIELD_FLASH_MS);
    const o = shieldOpacity(t);
    const pop = reduce ? 1 : t < 0.08 ? 0.6 + (t / 0.08) * 0.5 : 1.1 - Math.min(0.1, (t - 0.08) * 0.6);
    const bob = reduce ? 0 : Math.sin(t * Math.PI * 6) * 0.04;
    group.current.position.set(head.x, head.y + 1.15 + bob, head.z);
    group.current.scale.setScalar(pop);
    mat.current.opacity = o;
    group.current.visible = t < 1;
    const feet = feetPos.get(agentId) ?? head;
    if (ring.current) {
      const pulse = reduce ? 1 : 1 + ((t * 3) % 1) * 0.9;
      ring.current.position.set(feet.x, 0.03, feet.z);
      ring.current.scale.setScalar(pulse);
      ringMat.current.opacity = o * (reduce ? 0.35 : 0.45 * (1 - ((t * 3) % 1)));
      ring.current.visible = t < 1;
    }
  });
  return (
    <>
      <group ref={group}>
        <Billboard>
          <mesh renderOrder={31}>
            <planeGeometry args={[0.95, 0.95]} />
            <meshBasicMaterial ref={mat} map={tex} transparent depthTest={false} depthWrite={false} toneMapped={false} />
          </mesh>
        </Billboard>
      </group>
      <mesh ref={ring} rotation={[-Math.PI / 2, 0, 0]} renderOrder={5}>
        <ringGeometry args={[0.7, 0.85, 48]} />
        <meshBasicMaterial ref={ringMat} color="#2f80c8" transparent depthWrite={false} toneMapped={false} side={THREE.DoubleSide} />
      </mesh>
    </>
  );
}

/** Mounted inside the office canvas, next to CoinFloats. */
export function ShieldFloats() {
  const flashes = useShield((s) => s.flashes);
  useEffect(() => () => { shieldTex?.dispose(); shieldTex = null; }, []);
  return <>{flashes.map((f) => <Shield key={f.id} agentId={f.agent_id} at={f.at} />)}</>;
}
