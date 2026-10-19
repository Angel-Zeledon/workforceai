"use client";
// The PEOPLE of the office inside the 3D scene: one avatar per person online, walking next to the agents.
// Mine is driven by WASD / arrow keys or by double-clicking the floor; the others glide toward the position the
// server announces. Own art (primitives): the Character / Furniture / Worn files are not touched.
import { useEffect, useMemo, useRef } from "react";
import { useFrame, useThree } from "@react-three/fiber";
import { Html } from "@react-three/drei";
import * as THREE from "three";
import { useOfficeLayout } from "@/lib/officeLayout";
import { ZONE_NAMES, ZONE_SIZE } from "@/lib/officeLayoutModel";
import { zoneCenter } from "../office/zones";
import { usePresence } from "./store";
import { sendPresence } from "./tap";
import {
  SPAWN, clampToFloor, colorFor, createMoveThrottle, keyDirection, shouldPing, smoothToward, stepToward, type PStatus, type Pt,
} from "./logic";

const SPEED = 3.2; // meters per second
export const STATUS_COLOR: Record<PStatus, string> = { available: "#2f9e6b", focus: "#c0392b", meeting: "#b7791f", away: "#8b95a5" };

/** The room a point is in ("meeting" | "approvals" | "cafe") or "floor". Reads the organization's own layout. */
export function zoneAt(x: number, z: number): string {
  for (const n of ZONE_NAMES) {
    const [cx, cz] = zoneCenter(n);
    const s = ZONE_SIZE[n];
    if (Math.abs(x - (cx + s.dx)) <= s.w / 2 && Math.abs(z - (cz + s.dz)) <= s.d / 2) return n;
  }
  return "floor";
}

function Figure({ color, status, name, self, moving }: { color: string; status: PStatus; name: string; self: boolean; moving: { current: boolean } }) {
  const body = useRef<THREE.Group>(null);
  const t0 = useRef(Math.random() * 6);
  useFrame(({ clock }) => {
    const g = body.current;
    if (!g) return;
    const t = clock.elapsedTime + t0.current;
    const m = moving.current;
    g.position.y = m ? Math.abs(Math.sin(t * 9)) * 0.06 : Math.sin(t * 1.6) * 0.012;
    g.rotation.z = m ? Math.sin(t * 9) * 0.05 : 0;
  });
  const away = status === "away";
  return (
    <group>
      <mesh rotation-x={-Math.PI / 2} position-y={0.02}>
        <ringGeometry args={[0.36, 0.46, 32]} />
        <meshBasicMaterial color={STATUS_COLOR[status]} transparent opacity={away ? 0.5 : 0.95} />
      </mesh>
      <group ref={body}>
        <mesh position-y={0.58} castShadow>
          <capsuleGeometry args={[0.22, 0.58, 6, 14]} />
          <meshStandardMaterial color={color} roughness={0.7} transparent={away} opacity={away ? 0.6 : 1} />
        </mesh>
        <mesh position-y={1.28} castShadow>
          <sphereGeometry args={[0.2, 18, 14]} />
          <meshStandardMaterial color="#e8c4a0" roughness={0.8} transparent={away} opacity={away ? 0.6 : 1} />
        </mesh>
      </group>
      <Html center position={[0, 1.95, 0]} zIndexRange={[6, 0]} style={{ pointerEvents: "none" }}>
        <div
          data-testid={self ? "presence-self-tag" : "presence-person-tag"}
          className="flex items-center gap-1.5 whitespace-nowrap rounded-full border border-line bg-panel/95 px-2.5 py-0.5 text-[11px] font-semibold text-ink shadow-soft"
          style={{ opacity: away ? 0.7 : 1 }}
        >
          <span aria-hidden className="h-2 w-2 rounded-full" style={{ background: STATUS_COLOR[status] }} />
          {name}
        </div>
      </Html>
    </group>
  );
}

function Remote({ id }: { id: string }) {
  const p = usePresence((s) => s.people[id]);
  const g = useRef<THREE.Group>(null);
  const cur = useRef<Pt | null>(null);
  const moving = useRef(false);
  const target = useRef<Pt>({ x: p?.x ?? SPAWN.x, z: p?.z ?? SPAWN.z });
  if (p) target.current = { x: p.x, z: p.z };
  const color = useMemo(() => colorFor(id), [id]);
  useFrame((_, dt) => {
    const grp = g.current;
    if (!grp) return;
    if (!cur.current) cur.current = { ...target.current };
    const prev = cur.current;
    cur.current = smoothToward(prev, target.current, Math.min(dt, 0.1));
    grp.position.set(cur.current.x, 0, cur.current.z);
    const dx = cur.current.x - prev.x, dz = cur.current.z - prev.z;
    moving.current = Math.hypot(dx, dz) / Math.max(dt, 1e-3) > 0.25;
    if (moving.current) grp.rotation.y = Math.atan2(dx, dz);
  });
  if (!p) return null;
  return (
    <group ref={g}>
      <Figure color={color} status={p.status} name={p.name} self={false} moving={moving} />
    </group>
  );
}

const typing = (t: EventTarget | null) => {
  const el = t as HTMLElement | null;
  return !!el && (el.tagName === "INPUT" || el.tagName === "TEXTAREA" || el.tagName === "SELECT" || el.isContentEditable);
};

function Self() {
  const me = usePresence((s) => (s.selfId ? s.people[s.selfId] : undefined));
  const selfId = usePresence((s) => s.selfId);
  const { camera, gl, raycaster } = useThree();
  const g = useRef<THREE.Group>(null);
  const pos = useRef<Pt>({ ...SPAWN });
  const seeded = useRef(false);
  const target = useRef<Pt | null>(null);
  const keys = useRef(new Set<string>());
  const heading = useRef(0);
  const moving = useRef(false);
  const throttle = useMemo(() => createMoveThrottle(), []);
  const lastPing = useRef(0);
  const color = useMemo(() => colorFor(selfId ?? "me"), [selfId]);

  // start where the server (or the mock) put me
  useEffect(() => {
    if (me && !seeded.current) { pos.current = { x: me.x, z: me.z }; seeded.current = true; }
  }, [me]);

  useEffect(() => {
    const ping = () => {
      const now = Date.now();
      if (shouldPing(lastPing.current, now)) { lastPing.current = now; sendPresence({ type: "presence.ping" }); }
    };
    const down = (e: KeyboardEvent) => {
      ping();
      if (typing(e.target) || e.ctrlKey || e.metaKey || e.altKey) return;
      const k = e.key.toLowerCase();
      if (["w", "a", "s", "d", "arrowup", "arrowdown", "arrowleft", "arrowright"].includes(k)) { keys.current.add(k); target.current = null; }
    };
    const up = (e: KeyboardEvent) => { keys.current.delete(e.key.toLowerCase()); };
    const blur = () => keys.current.clear();
    const plane = new THREE.Plane(new THREE.Vector3(0, 1, 0), 0);
    const hit = new THREE.Vector3();
    const dbl = (e: MouseEvent) => {
      ping();
      const r = gl.domElement.getBoundingClientRect();
      const ndc = new THREE.Vector2(((e.clientX - r.left) / r.width) * 2 - 1, -((e.clientY - r.top) / r.height) * 2 + 1);
      raycaster.setFromCamera(ndc, camera);
      if (raycaster.ray.intersectPlane(plane, hit)) target.current = clampToFloor({ x: hit.x, z: hit.z });
    };
    const activity = () => ping();
    window.addEventListener("keydown", down);
    window.addEventListener("keyup", up);
    window.addEventListener("blur", blur);
    window.addEventListener("pointerdown", activity);
    gl.domElement.addEventListener("dblclick", dbl);
    return () => {
      window.removeEventListener("keydown", down);
      window.removeEventListener("keyup", up);
      window.removeEventListener("blur", blur);
      window.removeEventListener("pointerdown", activity);
      gl.domElement.removeEventListener("dblclick", dbl);
    };
  }, [camera, gl, raycaster]);

  useFrame((_, rawDt) => {
    const dt = Math.min(rawDt, 0.1);
    const dir = keyDirection(keys.current);
    let next = pos.current;
    moving.current = false;
    if (dir) {
      next = clampToFloor({ x: next.x + dir.x * SPEED * dt, z: next.z + dir.z * SPEED * dt });
      heading.current = Math.atan2(dir.x, dir.z);
      moving.current = true;
    } else if (target.current) {
      const s = stepToward(next, target.current, SPEED, dt);
      next = s.pos;
      heading.current = s.heading;
      moving.current = true;
      if (s.arrived) target.current = null;
    }
    pos.current = next;
    const grp = g.current;
    if (grp) { grp.position.set(next.x, 0, next.z); grp.rotation.y = heading.current; }
    const now = performance.now();
    const out = moving.current ? throttle.offer(next, now) : throttle.flush(now) ?? throttle.offer(next, now);
    if (out) {
      const zone = zoneAt(out.x, out.z);
      usePresence.getState().moveSelf(out, zone);
      sendPresence({ type: "presence.move", x: out.x, z: out.z, zone });
    }
  });

  if (!me) return null;
  return (
    <group ref={g} position={[pos.current.x, 0, pos.current.z]}>
      <Figure color={color} status={me.status} name={me.name} self moving={moving} />
    </group>
  );
}

/** All the people of the office. Hidden while the office is being edited. */
export function PeopleLayer() {
  const editing = useOfficeLayout((s) => s.editing);
  const ids = usePresence((s) => Object.keys(s.people).filter((k) => k !== s.selfId).sort().join("|"));
  const load = usePresence((s) => s.load);
  useEffect(() => { void load(); }, [load]);
  if (editing) return null;
  return (
    <>
      <Self />
      {ids ? ids.split("|").map((id) => <Remote key={id} id={id} />) : null}
    </>
  );
}
