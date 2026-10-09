"use client";
import { useEffect, useRef } from "react";
import { useFrame } from "@react-three/fiber";
import * as THREE from "three";
import { useStore } from "@/lib/store";
import { usePreferences } from "@/lib/preferences";
import { deskOf } from "./registry";
import { FolderPool, detectHandoffs, type Handoff } from "./officeState";

/** Max folders flying at once (performance cap) and the flight time of one hand-off. */
const MAX_FOLDERS = 4;
const FLIGHT_MS = 2600;
const LIFT = 0.9;
const NON_AGENT = new Set(["user", "all", "system", ""]);

/**
 * The pipeline made visible: when a task finishes and another agent's task depends on its output, or a delegation /
 * consult link appears between two agents, a small folder flies from one desk to the other. Real events only;
 * capped (4 at once, bounded queue) and disabled with reduced motion.
 */
export function Folders() {
  const pool = useRef<FolderPool>(null as unknown as FolderPool);
  if (!pool.current) pool.current = new FolderPool(MAX_FOLDERS, 10, 2500);
  const groups = useRef<(THREE.Group | null)[]>([]);
  const started = useRef<number[]>(new Array(MAX_FOLDERS).fill(-1));
  // endpoints per slot: ax, az, bx, bz
  const ends = useRef(new Float32Array(MAX_FOLDERS * 4));

  useEffect(() => {
    let prev = detectHandoffs(null, useStore.getState().tasks).next;
    let linkTs = Date.now();
    const push = (h: Handoff) => {
      if (usePreferences.getState().prefs.reduceMotion) return;
      const S = useStore.getState();
      if (!S.agents[h.from] || !S.agents[h.to]) return;
      pool.current.enqueue(h, performance.now());
    };
    return useStore.subscribe((s, p) => {
      if (s.tasks !== p.tasks) {
        const r = detectHandoffs(prev, s.tasks as never);
        prev = r.next;
        r.handoffs.forEach(push);
      }
      if (s.links !== p.links) {
        for (let i = s.links.length - 1; i >= 0 && s.links[i].ts > linkTs; i--) {
          const l = s.links[i];
          if ((l.kind === "delegation" || l.kind === "consult") && !l.chat && !NON_AGENT.has(l.from) && !NON_AGENT.has(l.to)) push({ from: l.from, to: l.to });
        }
        if (s.links.length) linkTs = Math.max(linkTs, s.links[s.links.length - 1].ts);
      }
    });
  }, []);

  useFrame((_, dt) => {
    const P = pool.current;
    if (usePreferences.getState().prefs.reduceMotion) { P.clear(); }
    const now = performance.now();
    P.pump(now, () => FLIGHT_MS);
    const S = useStore.getState();
    for (let i = 0; i < MAX_FOLDERS; i++) {
      const g = groups.current[i], sl = P.slots[i];
      if (!g) continue;
      if (!sl.busy) { g.visible = false; started.current[i] = -1; continue; }
      if (started.current[i] !== sl.t0) {
        started.current[i] = sl.t0;
        const fa = S.agents[sl.from], ta = S.agents[sl.to];
        if (!fa || !ta) { sl.busy = false; g.visible = false; continue; }
        const a = deskOf(fa, Math.max(0, S.agentOrder.indexOf(sl.from))), b = deskOf(ta, Math.max(0, S.agentOrder.indexOf(sl.to)));
        const e = ends.current; e[i * 4] = a[0]; e[i * 4 + 1] = a[1] + 0.2; e[i * 4 + 2] = b[0]; e[i * 4 + 3] = b[1] + 0.2;
      }
      const u = Math.min(1, (now - sl.t0) / sl.dur);
      const k = u * u * (3 - 2 * u);
      const e = ends.current;
      g.visible = true;
      g.position.set(e[i * 4] + (e[i * 4 + 2] - e[i * 4]) * k, 0.95 + Math.sin(k * Math.PI) * LIFT, e[i * 4 + 1] + (e[i * 4 + 3] - e[i * 4 + 1]) * k);
      g.rotation.y += dt * 2.2;
      g.rotation.z = Math.sin(k * Math.PI) * 0.35;
      const sc = u < 0.1 ? u / 0.1 : u > 0.9 ? (1 - u) / 0.1 : 1; // pops in at the sender, shrinks into the receiver's desk
      g.scale.setScalar(0.4 + 0.6 * sc);
    }
  });

  return (
    <>
      {Array.from({ length: MAX_FOLDERS }).map((_, i) => (
        <group key={i} ref={(el) => { groups.current[i] = el; }} visible={false} data-folder="1">
          <mesh castShadow><boxGeometry args={[0.34, 0.025, 0.25]} /><meshStandardMaterial color="#e4bd6c" roughness={0.7} emissive="#c9993f" emissiveIntensity={0.25} /></mesh>
          <mesh position={[-0.09, 0.0, -0.14]}><boxGeometry args={[0.13, 0.025, 0.04]} /><meshStandardMaterial color="#d6a94f" roughness={0.7} /></mesh>
          <mesh position={[0, 0.016, 0.01]}><boxGeometry args={[0.28, 0.006, 0.19]} /><meshStandardMaterial color="#fbf6e8" roughness={0.9} /></mesh>
        </group>
      ))}
    </>
  );
}
