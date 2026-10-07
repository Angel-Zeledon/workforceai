"use client";
import { useEffect, useMemo, useRef, useState } from "react";
import { useFrame } from "@react-three/fiber";
import * as THREE from "three";
import { useStore, type Link } from "@/lib/store";
import { USER_POS } from "@/lib/meta";
import { headPos } from "./registry";
import { LINK_KIND } from "./kinds";

const KIND = LINK_KIND;
const TTL = 7000;
const N = 28;
const tmpA = new THREE.Vector3();
const tmpB = new THREE.Vector3();

function endpoint(id: string, out: THREE.Vector3) {
  if (id === "user" || id === "all" || id === "system") return out.set(USER_POS[0], 1.5, USER_POS[2] - 1.4);
  const h = headPos.get(id);
  return h ? out.copy(h).setY(h.y + 0.55) : null;
}

function LinkViz({ link }: { link: Link }) {
  const k = KIND[link.kind] || KIND.chat;
  const line = useMemo(() => {
    const g = new THREE.BufferGeometry();
    g.setAttribute("position", new THREE.BufferAttribute(new Float32Array(N * 3), 3));
    const m = new THREE.LineBasicMaterial({ color: k.color, transparent: true, opacity: 0.9, toneMapped: false });
    const l = new THREE.Line(g, m);
    l.frustumCulled = false;
    return l;
  }, [k.color]);
  const dot = useRef<THREE.Mesh>(null!);
  const end = useRef<THREE.Mesh>(null!);

  useFrame(() => {
    const a = endpoint(link.from, tmpA), b = endpoint(link.to, tmpB);
    if (!a || !b) { line.visible = false; end.current.visible = false; return; }
    line.visible = true;
    const pos = line.geometry.getAttribute("position") as THREE.BufferAttribute;
    const lift = 0.7 + a.distanceTo(b) * 0.12;
    for (let i = 0; i < N; i++) {
      const t = i / (N - 1);
      pos.setXYZ(i, a.x + (b.x - a.x) * t, a.y + (b.y - a.y) * t + Math.sin(t * Math.PI) * lift, a.z + (b.z - a.z) * t);
    }
    pos.needsUpdate = true;
    const age = Date.now() - link.ts;
    const fade = age > TTL - 1500 ? Math.max(0, (TTL - age) / 1500) : 1;
    (line.material as THREE.LineBasicMaterial).opacity = 0.85 * fade;
    const tp = Math.min(1, age / 1400);
    dot.current.position.set(
      a.x + (b.x - a.x) * tp, a.y + (b.y - a.y) * tp + Math.sin(tp * Math.PI) * lift, a.z + (b.z - a.z) * tp,
    );
    dot.current.visible = age < 1500;
    end.current.position.copy(b); end.current.visible = true;
    (end.current.material as THREE.MeshBasicMaterial).opacity = 0.9 * fade;
  });

  return (
    <>
      <primitive object={line} />
      <mesh ref={dot}><sphereGeometry args={[0.09, 14, 12]} /><meshBasicMaterial color={k.color} toneMapped={false} /></mesh>
      <mesh ref={end} visible={false}><sphereGeometry args={[0.05, 10, 8]} /><meshBasicMaterial color={k.color} transparent toneMapped={false} /></mesh>
    </>
  );
}

export function Links() {
  const links = useStore((s) => s.links);
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const i = setInterval(() => setNow(Date.now()), 500);
    return () => clearInterval(i);
  }, []);
  const active = useMemo(() => links.filter((l) => now - l.ts < TTL).slice(-6), [links, now]);
  return <>{active.map((l) => <LinkViz key={l.id} link={l} />)}</>;
}
