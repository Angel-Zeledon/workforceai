"use client";
import { useMemo, useRef } from "react";
import { useFrame } from "@react-three/fiber";
import { Billboard } from "@react-three/drei";
import * as THREE from "three";
import { useStore } from "@/lib/store";
import { usePreferences, useAgentView } from "@/lib/preferences";
import { roleMeta, USER_POS } from "@/lib/meta";
import type { AgentState } from "@/lib/types";
import {
  EVENT_GESTURES, GESTURE_DURATION, KEYS, WALK_SPEED, applyGesture, blinkDelay, clamp, computePose, createRng, fidgetDelay,
  hashSeed, pickFidget, propFor, smooth, wrapAngle as wrap, zeroPose, type GestureKind,
} from "./gestures";
import { AISLE_Z, deskOf, feetPos, headPos, seatOf, standOf, visitorOf } from "./registry";

const SEATED = new Set<AgentState>(["thinking", "working", "waiting", "reviewing", "blocked", "awaiting_approval"]);
const ACTIVE_TASK = new Set(["pending", "running", "awaiting_approval"]);
/** Where the assistant hands the finished report to the user (in front of the approvals desk, clear of furniture). */
const DELIVER_POS: [number, number] = [USER_POS[0] - 1.6, USER_POS[2] - 1.1];
const BODY_SCALE = 1.2;
/** Beyond this squared camera distance fidgets and event gestures are skipped (cheap LOD). */
const FAR_D2 = 24 * 24;

function route(cur: [number, number], dest: [number, number], toSeat: boolean, own: { seat: [number, number]; stand: [number, number] }) {
  const pts: [number, number][] = [];
  const d = (a: [number, number], b: [number, number]) => Math.hypot(a[0] - b[0], a[1] - b[1]);
  let from = cur;
  if (d(cur, own.seat) < 0.3) { pts.push(own.stand); from = own.stand; }
  const target = toSeat ? own.stand : dest;
  if (d(from, target) > 3.2) {
    pts.push([from[0], AISLE_Z]);
    pts.push([target[0], AISLE_Z]);
  }
  pts.push(target);
  if (toSeat) pts.push(own.seat);
  return pts;
}

function Ico({ kind }: { kind: "check" | "alert" | "warn" }) {
  const c = kind === "check" ? "#5fcf86" : kind === "warn" ? "#ffb02e" : "#ff6b6b";
  return (
    <Billboard>
      <mesh rotation={[Math.PI / 2, 0, 0]}>
        <cylinderGeometry args={[0.2, 0.2, 0.05, 24]} />
        <meshStandardMaterial color={c} emissive={c} emissiveIntensity={0.35} />
      </mesh>
      {kind === "check" ? (
        <>
          <mesh position={[-0.065, -0.03, 0.04]} rotation={[0, 0, -0.78]}><boxGeometry args={[0.09, 0.035, 0.02]} /><meshBasicMaterial color="#fff" /></mesh>
          <mesh position={[0.03, 0.02, 0.04]} rotation={[0, 0, 0.78]}><boxGeometry args={[0.18, 0.035, 0.02]} /><meshBasicMaterial color="#fff" /></mesh>
        </>
      ) : (
        <>
          <mesh position={[0, 0.045, 0.04]}><boxGeometry args={[0.045, 0.14, 0.02]} /><meshBasicMaterial color="#fff" /></mesh>
          <mesh position={[0, -0.095, 0.04]}><sphereGeometry args={[0.028, 8, 8]} /><meshBasicMaterial color="#fff" /></mesh>
        </>
      )}
    </Billboard>
  );
}

function startGesture(sm: Sim, kind: GestureKind, aimWorld: number | null, canTurn: boolean, blocked: boolean) {
  if (blocked) return;
  sm.gKind = kind; sm.gStart = sm.t; sm.gDur = GESTURE_DURATION[kind]; sm.gEvent = EVENT_GESTURES.has(kind);
  if (aimWorld !== null) {
    sm.aimWorld = aimWorld; sm.hasAim = true;
    if (canTurn) { sm.turnYaw = aimWorld; sm.turnUntil = sm.t + sm.gDur * 0.9; }
  } else sm.hasAim = false;
}

function makeSim(id: string, seat: [number, number]) {
  const seed = hashSeed(id);
  const rng = createRng(seed);
  return {
    pos: new THREE.Vector2(seat[0], seat[1]),
    yaw: Math.PI, t: rng() * 40, pose: zeroPose(), target: zeroPose(), place: "", pts: [] as [number, number][],
    rng, phase: rng() * Math.PI * 2, rate: 0.85 + rng() * 0.3,
    blinkAt: 2, inited: false, prev: "", pop: 0,
    taskSrc: null as unknown, hasTask: false,
    // movement
    speed: 0, stride: 0, turn: 0, mode: "", blend: 1,
    // gestures
    gKind: null as GestureKind | null, gStart: 0, gDur: 1, gEvent: false, hasAim: false, aimWorld: 0,
    turnYaw: 0, turnUntil: 0, nextFid: 5, wasSel: false,
    linkSrc: null as unknown, linkTs: 0, apprSrc: null as unknown, seenAppr: new Set<string>(),
  };
}

type Sim = ReturnType<typeof makeSim>;

export function Character({ id, index }: { id: string; index: number }) {
  const agent0 = useStore((s) => s.agents[id]);
  const selected = useStore((s) => s.selectedAgentId === id);
  const select = useStore((s) => s.select);
  const view = useAgentView(id);
  // Role defaults overridden by the user's per-employee customization (color, skin, hairstyle, accessory).
  const meta = { ...roleMeta(agent0?.role), color: view.color, skin: view.skin, hairStyle: view.hairStyle, accessory: view.accessory };
  const desk = useMemo(() => deskOf({ id, role: agent0?.role || "" }, index), [id, agent0?.role, index]);
  const own = useMemo(() => ({ seat: seatOf(desk), stand: standOf(desk) }), [desk]);

  const root = useRef<THREE.Group>(null!);
  const pelvis = useRef<THREE.Group>(null!);
  const torso = useRef<THREE.Group>(null!);
  const head = useRef<THREE.Group>(null!);
  const lSh = useRef<THREE.Group>(null!); const lEl = useRef<THREE.Group>(null!);
  const rSh = useRef<THREE.Group>(null!); const rEl = useRef<THREE.Group>(null!);
  const lHip = useRef<THREE.Group>(null!); const rHip = useRef<THREE.Group>(null!);
  const lKnee = useRef<THREE.Group>(null!); const rKnee = useRef<THREE.Group>(null!);
  const mouth = useRef<THREE.Mesh>(null!);
  const eyes = useRef<THREE.Group>(null!);
  const docRef = useRef<THREE.Mesh>(null!);
  const dots = useRef<THREE.Group>(null!);
  const icoWrap = useRef<THREE.Group>(null!);
  const body = useRef<THREE.Group>(null!);
  const checkG = useRef<THREE.Group>(null!);
  const alertG = useRef<THREE.Group>(null!);
  const warnG = useRef<THREE.Group>(null!);
  const ringRef = useRef<THREE.Mesh>(null!);

  const mug = useRef<THREE.Group>(null!);
  const phone = useRef<THREE.Mesh>(null!);

  // Per-character seed (from the id): phase/tempo/timers differ so the office never moves in unison.
  const sim = useRef<Sim | null>(null);
  if (!sim.current) sim.current = makeSim(id, own.seat);

  const skin = meta.skin, hair = meta.hair;
  const blazer = meta.color;
  const sleeve = blazer;
  const pants = "#9a7a5c", shoe = "#6b4a36";

  useFrame((state, dtRaw) => {
    const dt = Math.min(dtRaw, 0.05);
    const S = useStore.getState();
    const ag = S.agents[id];
    if (!ag || !root.current) return;
    const sm = sim.current!;
    if (!sm.inited) {
      sm.inited = true;
      const start = SEATED.has(ag.state) ? own.seat : own.stand;
      sm.pos.set(start[0], start[1]);
      sm.yaw = SEATED.has(ag.state) ? Math.PI : 0.35;
      sm.place = SEATED.has(ag.state) ? "seat" : "stand";
      sm.wasSel = selected; sm.linkTs = Date.now(); sm.linkSrc = S.links; sm.apprSrc = S.approvals;
      sm.nextFid = sm.t + 2 + sm.rng() * 8; sm.blinkAt = sm.t + 1 + sm.rng() * 3;
    }
    sm.t += dt;
    const now = Date.now();
    const st = ag.state;
    const reduce = usePreferences.getState().prefs.reduceMotion;

    // Real assigned work: any non-terminal task of this agent in the store (task.created/started/... events).
    if (sm.taskSrc !== S.tasks) {
      sm.taskSrc = S.tasks;
      sm.hasTask = Object.values(S.tasks).some((tk) => tk.agent_id === id && ACTIVE_TASK.has(tk.status));
    }

    // --- desired placement (derived from agent state + real consult messages) ---
    let visit: string | null = null;
    if (st === "talking") {
      for (let i = S.links.length - 1; i >= 0; i--) {
        const l = S.links[i];
        if (l.kind === "consult" && l.from === id && now - l.ts < 20000 && S.agents[l.to] && l.to !== id) { visit = l.to; break; }
      }
    }
    let place = "stand";
    let dest: [number, number] = own.stand;
    let face: number | null = null;
    if (visit) {
      place = `visit:${visit}`;
      const td = deskOf(S.agents[visit], Math.max(0, S.agentOrder.indexOf(visit)));
      dest = visitorOf(td);
      const tgt = standOf(td);
      face = Math.atan2(tgt[0] - dest[0], tgt[1] - dest[1]);
    } else if (SEATED.has(st) || (st === "idle" && sm.hasTask)) { place = "seat"; dest = own.seat; }
    else if (st === "completed" && ag.role === "assistant") {
      // the final report is handed to the user (request.completed -> assistant "completed")
      place = "deliver"; dest = DELIVER_POS;
      face = Math.atan2(USER_POS[0] - dest[0], USER_POS[2] - dest[1]);
    }

    // counterpart to face when conversing
    if (face === null && st === "talking") {
      for (let i = S.links.length - 1; i >= 0; i--) {
        const l = S.links[i];
        if (now - l.ts > 14000) break;
        if (l.from !== id && l.to !== id) continue;
        const other = l.from === id ? l.to : l.from;
        let ox: number | undefined, oz: number | undefined;
        if (other === "user") { ox = USER_POS[0]; oz = USER_POS[2]; }
        else { const h = feetPos.get(other); if (h) { ox = h.x; oz = h.z; } }
        if (ox !== undefined && oz !== undefined) { face = Math.atan2(ox - sm.pos.x, oz - sm.pos.y); break; }
      }
    }

    if (place !== sm.place) {
      sm.place = place;
      sm.pts = route([sm.pos.x, sm.pos.y], dest, place === "seat", own);
      // reduce motion: no walking, snap straight to the destination
      if (reduce) { sm.pts = [dest]; sm.pos.set(dest[0], dest[1]); sm.pts = []; }
    }

    // --- movement (speed eases in/out; stride phase advances with distance so arms/legs match the pace) ---
    let walking = false;
    let moveYaw: number | null = null;
    const wp = sm.pts[0];
    let speedTarget = 0;
    if (wp) {
      const dx = wp[0] - sm.pos.x, dz = wp[1] - sm.pos.y;
      const d = Math.hypot(dx, dz);
      if (d < 0.05) { sm.pos.set(wp[0], wp[1]); sm.pts.shift(); }
      else {
        speedTarget = sm.pts.length === 1 ? clamp(d * 2.6, 0.7, WALK_SPEED) : WALK_SPEED;
        sm.speed += (speedTarget - sm.speed) * Math.min(1, dt * 7);
        if (sm.speed < 0.5) sm.speed = 0.5;
        const step = Math.min(d, sm.speed * dt);
        sm.pos.x += (dx / d) * step; sm.pos.y += (dz / d) * step;
        sm.stride += sm.speed * dt * 3.6;
        walking = true; moveYaw = Math.atan2(dx, dz);
      }
    }
    if (!walking) sm.speed = 0;

    const far = ((state.camera.position.x - sm.pos.x) ** 2 + (state.camera.position.z - sm.pos.y) ** 2) > FAR_D2;
    const mode = walking ? "walk" : place === "seat" ? `seat:${st}` : `stand:${visit ? "talking" : st}`;

    // --- gestures: event reactions (selection, approvals, delegation, user messages) and idle fidgets ---
    const standing = place !== "seat";
    const canTurn = standing && face === null;
    if (!reduce && !far) {
      if (selected && !sm.wasSel) {
        // greets whoever just opened them: face the camera when standing
        const c = state.camera.position;
        startGesture(sm, "wave", Math.atan2(c.x - sm.pos.x, c.z - sm.pos.y), canTurn, reduce || walking);
      }
      if (sm.linkSrc !== S.links) {
        sm.linkSrc = S.links;
        for (let i = S.links.length - 1; i >= 0 && S.links[i].ts > sm.linkTs; i--) {
          const l = S.links[i];
          const other = l.from === id ? l.to : l.from;
          if (l.kind === "delegation" && (l.from === id || l.to === id)) {
            let aw: number | null = null;
            if (other === "user") aw = Math.atan2(USER_POS[0] - sm.pos.x, USER_POS[2] - sm.pos.y);
            else { const h = feetPos.get(other); if (h) aw = Math.atan2(h.x - sm.pos.x, h.z - sm.pos.y); }
            if (aw !== null) startGesture(sm, l.from === id ? "point" : "lookAt", aw, canTurn, reduce || walking);
          } else if (l.from === "user" && l.to === id && !l.chat) startGesture(sm, "nod", null, canTurn, reduce || walking);
        }
        if (S.links.length) sm.linkTs = Math.max(sm.linkTs, S.links[S.links.length - 1].ts);
      }
      if (sm.apprSrc !== S.approvals) {
        sm.apprSrc = S.approvals;
        for (const a of Object.values(S.approvals)) {
          if (a.agent_id !== id || a.status === "pending" || sm.seenAppr.has(a.id)) continue;
          sm.seenAppr.add(a.id);
          const at = a.resolved_at ? Date.parse(a.resolved_at) : NaN;
          if (!Number.isNaN(at) && Math.abs(now - at) < 15000) startGesture(sm, a.status === "approved" ? "nod" : "headShake", null, canTurn, reduce || walking);
        }
      }
    }
    sm.wasSel = selected;
    if (reduce || walking) sm.gKind = null;
    else if (sm.gKind && sm.t - sm.gStart >= sm.gDur) sm.gKind = null;
    if (!sm.gKind && !reduce && !walking && !far && sm.t >= sm.nextFid) {
      const k = pickFidget(mode, sm.rng());
      if (k) startGesture(sm, k, null, canTurn, reduce || walking);
      sm.nextFid = sm.t + fidgetDelay(mode, sm.rng());
    }
    const gu = sm.gKind ? (sm.t - sm.gStart) / sm.gDur : 0;

    // --- orientation: smooth turn toward walking direction / conversation partner / greeting target ---
    let targetYaw: number;
    if (moveYaw !== null) targetYaw = moveYaw;
    else if (place === "seat") targetYaw = Math.PI;
    else if (face !== null) targetYaw = face;
    else if (sm.t < sm.turnUntil) targetYaw = sm.turnYaw;
    else targetYaw = 0.35;
    const dyaw = wrap(targetYaw - sm.yaw);
    sm.yaw += dyaw * Math.min(1, dt * (walking ? 9 : 5));
    sm.turn += (clamp(dyaw, -1, 1) - sm.turn) * Math.min(1, dt * 6); // lean into the turn

    // --- pose: base target + gesture layer, blended with easing ---
    if (mode !== sm.mode) { sm.mode = mode; sm.blend = 0; }
    sm.blend = Math.min(1, sm.blend + dt / 0.45);
    const target = computePose(sm.target, mode, reduce ? 0 : sm.t, sm.phase, sm.rate, sm.stride, sm.speed / WALK_SPEED);
    if (walking) target.torsoZ += -sm.turn * 0.12;
    if (sm.gKind) {
      const aim = sm.hasAim ? wrap(sm.aimWorld - sm.yaw) : (sm.gKind === "wave" ? (place === "seat" ? 0.6 : 0) : 0);
      applyGesture(target, sm.gKind, gu, sm.t, aim);
    }
    const k = Math.min(1, dt * (walking ? 14 : 8) * (0.4 + 0.6 * smooth(sm.blend)));
    const c = sm.pose;
    for (const key of KEYS) c[key] += (target[key] - c[key]) * k;

    // --- apply ---
    root.current.position.set(sm.pos.x, 0, sm.pos.y);
    root.current.rotation.y = sm.yaw;
    pelvis.current.position.y = c.pelvisY;
    torso.current.rotation.set(c.torsoX, c.torsoY, c.torsoZ);
    head.current.rotation.set(c.headX, c.headY, c.headZ);
    lSh.current.rotation.set(c.lShX, 0, c.lShZ); lEl.current.rotation.x = c.lElX;
    rSh.current.rotation.set(c.rShX, 0, -c.rShZ); rEl.current.rotation.x = c.rElX;
    lHip.current.rotation.x = c.lHipX; rHip.current.rotation.x = c.rHipX;
    lKnee.current.rotation.x = c.lKneeX; rKnee.current.rotation.x = c.rKneeX;

    // face details: blink (double-blinks sometimes), tired eyes when blocked, wider smile when done
    const talking = st === "talking";
    const eyeOpen = st === "blocked" ? 0.6 : 1;
    mouth.current.scale.y = talking ? 0.5 + Math.abs(Math.sin(sm.t * 13)) * 1.2 : st === "completed" ? 0.7 : st === "error" ? 0.9 : 0.45;
    mouth.current.scale.x = st === "completed" ? 2.3 : st === "error" ? 1.0 : 1.6;
    if (reduce) eyes.current.scale.y = eyeOpen;
    else if (sm.t > sm.blinkAt) {
      eyes.current.scale.y = 0.1;
      if (sm.t > sm.blinkAt + 0.12) {
        eyes.current.scale.y = eyeOpen;
        sm.blinkAt = sm.rng() < 0.2 ? sm.t + 0.15 : sm.t + blinkDelay(mode, sm.rng());
      }
    } else eyes.current.scale.y = eyeOpen;
    const prop = propFor(sm.gKind, gu);
    mug.current.visible = prop === 1; phone.current.visible = prop === 2;
    docRef.current.visible = st === "reviewing" && !walking;
    dots.current.visible = st === "thinking" && !walking;
    if (dots.current.visible) dots.current.children.forEach((m, i) => { m.position.y = Math.sin(sm.t * 4 + i * 0.9) * 0.04; (m as THREE.Mesh).scale.setScalar(0.8 + 0.25 * Math.sin(sm.t * 4 + i * 0.9)); });
    checkG.current.visible = st === "completed" && !walking;
    alertG.current.visible = (st === "blocked" || st === "error") && !walking;
    warnG.current.visible = st === "awaiting_approval" && !walking;
    const bob = reduce ? 0 : Math.sin(sm.t * 3) * 0.03;
    icoWrap.current.position.y = (c.pelvisY + 0.87) * BODY_SCALE + 0.28 + bob;
    // rebote suave (squash & stretch) cuando cambia el estado real del agente
    if (sm.prev !== st) { if (sm.prev) sm.pop = 1; sm.prev = st; }
    sm.pop = reduce ? 0 : Math.max(0, sm.pop - dt * 1.4);
    const sq = Math.sin((1 - sm.pop) * Math.PI * 3) * sm.pop * 0.09;
    body.current.scale.set(BODY_SCALE * (1 - sq * 0.6), BODY_SCALE * (1 + sq), BODY_SCALE * (1 - sq * 0.6));
    ringRef.current.visible = selected;

    // registry (head + feet world positions)
    let h = headPos.get(id); if (!h) { h = new THREE.Vector3(); headPos.set(id, h); }
    h.set(sm.pos.x, (c.pelvisY + 0.58) * BODY_SCALE, sm.pos.y);
    let f = feetPos.get(id); if (!f) { f = new THREE.Vector3(); feetPos.set(id, f); }
    f.set(sm.pos.x, 0, sm.pos.y);
  });

  const f = meta.female;
  const dark = "#4a3626";
  return (
    <group ref={root}>
      <mesh rotation={[-Math.PI / 2, 0, 0]} position={[0, 0.025, 0]}>
        <circleGeometry args={[0.5, 24]} />
        <meshBasicMaterial map={blobTexture()} transparent opacity={0.55} depthWrite={false} toneMapped={false} />
      </mesh>
      <mesh ref={ringRef} rotation={[-Math.PI / 2, 0, 0]} position={[0, 0.03, 0]} visible={false}>
        <ringGeometry args={[0.62, 0.74, 40]} />
        <meshBasicMaterial color={meta.color} transparent opacity={0.95} />
      </mesh>
      {/* click target */}
      <mesh
        position={[0, 0.8, 0]}
        onClick={(e) => { e.stopPropagation(); select(id); }}
        onPointerOver={() => { document.body.style.cursor = "pointer"; }}
        onPointerOut={() => { document.body.style.cursor = ""; }}
      >
        <cylinderGeometry args={[0.5, 0.5, 1.6, 8]} />
        <meshBasicMaterial transparent opacity={0} depthWrite={false} />
      </mesh>

      <group ref={body} scale={BODY_SCALE}>
      <group ref={pelvis}>
        <mesh castShadow position={[0, -0.02, 0]} scale={[f ? 1.0 : 1.08, 1, 1]}><sphereGeometry args={[0.19, 16, 12]} /><meshStandardMaterial color={pants} roughness={0.85} /></mesh>

        {/* piernas cortas y redondas */}
        {([[0.1, lHip, lKnee], [-0.1, rHip, rKnee]] as const).map(([x, hipRef, kneeRef], i) => (
          <group key={i} ref={hipRef} position={[x, -0.02, 0]}>
            <mesh castShadow position={[0, -0.1, 0]}><capsuleGeometry args={[0.085, 0.06, 4, 10]} /><meshStandardMaterial color={pants} roughness={0.85} /></mesh>
            <group ref={kneeRef} position={[0, -0.2, 0]}>
              <mesh castShadow position={[0, -0.06, 0]}><capsuleGeometry args={[0.075, 0.03, 4, 10]} /><meshStandardMaterial color={pants} roughness={0.85} /></mesh>
              <mesh castShadow position={[0, -0.12, 0.04]} scale={[1, 0.7, 1.5]}><sphereGeometry args={[0.09, 12, 10]} /><meshStandardMaterial color={shoe} roughness={0.6} /></mesh>
            </group>
          </group>
        ))}

        {/* torso */}
        <group ref={torso}>
          <mesh castShadow position={[0, 0.16, 0]} scale={[f ? 1.0 : 1.08, 1, 0.9]}>
            <capsuleGeometry args={[0.2, 0.04, 6, 14]} />
            <meshStandardMaterial color={blazer} roughness={0.8} />
          </mesh>
          <mesh position={[0, 0.14, 0.165]} scale={[1, 1.25, 0.3]}><sphereGeometry args={[0.1, 12, 10]} /><meshStandardMaterial color="#fffaf0" roughness={0.9} /></mesh>
          {meta.tie && (
            <group position={[0, 0.3, 0.19]}>
              <mesh><sphereGeometry args={[0.03, 8, 8]} /><meshStandardMaterial color={blazer} /></mesh>
              <mesh position={[0.05, 0, 0]} scale={[1.4, 1, 0.6]}><sphereGeometry args={[0.04, 8, 8]} /><meshStandardMaterial color={blazer} /></mesh>
              <mesh position={[-0.05, 0, 0]} scale={[1.4, 1, 0.6]}><sphereGeometry args={[0.04, 8, 8]} /><meshStandardMaterial color={blazer} /></mesh>
            </group>
          )}
          {meta.accessory === "lanyard" && (
            <>
              <mesh position={[0, 0.27, 0.19]}><boxGeometry args={[0.025, 0.2, 0.01]} /><meshStandardMaterial color={meta.color} /></mesh>
              <mesh position={[0, 0.13, 0.19]}><boxGeometry args={[0.1, 0.12, 0.012]} /><meshStandardMaterial color="#ffffff" /></mesh>
            </>
          )}

          {/* brazos redondos */}
          {([[0.24, lSh, lEl], [-0.24, rSh, rEl]] as const).map(([x, shRef, elRef], i) => (
            <group key={i} ref={shRef} position={[x, 0.28, 0]}>
              <mesh castShadow position={[0, -0.1, 0]}><capsuleGeometry args={[0.07, 0.1, 4, 10]} /><meshStandardMaterial color={blazer} roughness={0.8} /></mesh>
              <group ref={elRef} position={[0, -0.2, 0]}>
                <mesh castShadow position={[0, -0.1, 0]}><capsuleGeometry args={[0.062, 0.06, 4, 10]} /><meshStandardMaterial color={blazer} roughness={0.8} /></mesh>
                <mesh castShadow position={[0, -0.215, 0]}><sphereGeometry args={[0.075, 12, 10]} /><meshStandardMaterial color={skin} roughness={0.8} /></mesh>
                {i === 1 && (
                  <group ref={mug} position={[0, -0.27, 0.07]} visible={false}>
                    <mesh><cylinderGeometry args={[0.05, 0.043, 0.1, 12]} /><meshStandardMaterial color="#f4efe6" roughness={0.6} /></mesh>
                    <mesh position={[0, 0.045, 0]}><cylinderGeometry args={[0.042, 0.042, 0.012, 12]} /><meshStandardMaterial color="#5a3a24" /></mesh>
                    <mesh position={[0.06, 0, 0]} rotation={[0, 0, Math.PI / 2]}><torusGeometry args={[0.025, 0.007, 6, 10]} /><meshStandardMaterial color="#f4efe6" /></mesh>
                  </group>
                )}
                {i === 1 && (
                  <mesh ref={phone} position={[0, -0.28, 0.07]} rotation={[0.2, 0, 0]} visible={false}>
                    <boxGeometry args={[0.07, 0.13, 0.012]} /><meshStandardMaterial color="#22262e" emissive="#3a6ea5" emissiveIntensity={0.5} />
                  </mesh>
                )}
                {i === 1 && (
                  <mesh ref={docRef} position={[0.06, -0.26, 0.12]} rotation={[0.3, 0, 0.1]} visible={false}>
                    <boxGeometry args={[0.22, 0.28, 0.015]} />
                    <meshStandardMaterial color="#ffffff" emissive="#f5e9d0" emissiveIntensity={0.3} />
                  </mesh>
                )}
              </group>
            </group>
          ))}

          {/* cabeza grande */}
          <group ref={head} position={[0, 0.32, 0]} scale={1.75}>
            <mesh castShadow position={[0, 0.15, 0]}><sphereGeometry args={[0.165, 24, 18]} /><meshStandardMaterial color={skin} roughness={0.75} /></mesh>
            <group ref={eyes} position={[0, 0.17, 0]}>
              <mesh position={[0.058, 0, 0.145]} scale={[1, 1.35, 0.5]}><sphereGeometry args={[0.022, 10, 8]} /><meshBasicMaterial color={dark} /></mesh>
              <mesh position={[-0.058, 0, 0.145]} scale={[1, 1.35, 0.5]}><sphereGeometry args={[0.022, 10, 8]} /><meshBasicMaterial color={dark} /></mesh>
            </group>
            <mesh position={[0.1, 0.1, 0.12]} scale={[1.2, 0.8, 0.4]}><sphereGeometry args={[0.025, 8, 6]} /><meshBasicMaterial color="#ff9fa8" transparent opacity={0.65} /></mesh>
            <mesh position={[-0.1, 0.1, 0.12]} scale={[1.2, 0.8, 0.4]}><sphereGeometry args={[0.025, 8, 6]} /><meshBasicMaterial color="#ff9fa8" transparent opacity={0.65} /></mesh>
            <mesh ref={mouth} position={[0, 0.095, 0.155]} scale={[1.6, 0.45, 0.5]}><sphereGeometry args={[0.02, 10, 8]} /><meshBasicMaterial color="#8a3b3b" /></mesh>
            <Hair style={meta.hairStyle} color={hair} />
            <Accessory kind={meta.accessory} color={meta.color} />
          </group>
        </group>
      </group>
      </group>

      {/* glifos de estado */}
      <group ref={icoWrap}>
        <group ref={dots}>
          {[-0.14, 0, 0.14].map((x, i) => (
            <mesh key={i} position={[x, 0, 0]}><sphereGeometry args={[0.055, 12, 10]} /><meshStandardMaterial color="#d6c6ff" emissive="#b49bff" emissiveIntensity={0.6} /></mesh>
          ))}
        </group>
        <group ref={checkG} visible={false}><Ico kind="check" /></group>
        <group ref={alertG} visible={false}><Ico kind="alert" /></group>
        <group ref={warnG} visible={false}><Ico kind="warn" /></group>
      </group>
    </group>
  );
}

let _blob: THREE.CanvasTexture | null = null;
/** Sombra circular suave bajo cada personaje (textura radial generada en canvas). */
function blobTexture() {
  if (_blob) return _blob;
  const c = document.createElement("canvas");
  c.width = c.height = 64;
  const g = c.getContext("2d")!;
  const gr = g.createRadialGradient(32, 32, 2, 32, 32, 32);
  gr.addColorStop(0, "rgba(90,60,30,0.9)"); gr.addColorStop(1, "rgba(90,60,30,0)");
  g.fillStyle = gr; g.fillRect(0, 0, 64, 64);
  _blob = new THREE.CanvasTexture(c);
  return _blob;
}

function Blob({ p, s, children }: { p: [number, number, number]; s: [number, number, number]; children: React.ReactNode }) {
  return <mesh castShadow position={p} scale={s}><sphereGeometry args={[0.5, 14, 12]} />{children}</mesh>;
}

function Hair({ style, color }: { style: string; color: string }) {
  if (style === "bald") return null;
  const m = <meshStandardMaterial color={color} roughness={0.9} />;
  return (
    <group position={[0, 0.15, 0]}>
      <mesh castShadow rotation={[-0.35, 0, 0]} position={[0, 0.02, -0.01]}>
        <sphereGeometry args={[0.178, 20, 14, 0, Math.PI * 2, 0, Math.PI * 0.52]} />
        {m}
      </mesh>
      {style === "long" && (
        <>
          <Blob p={[0, -0.09, -0.09]} s={[0.34, 0.4, 0.14]}>{m}</Blob>
          <Blob p={[0.15, -0.04, -0.02]} s={[0.07, 0.26, 0.2]}>{m}</Blob>
          <Blob p={[-0.15, -0.04, -0.02]} s={[0.07, 0.26, 0.2]}>{m}</Blob>
        </>
      )}
      {style === "bob" && (
        <>
          <Blob p={[0, -0.04, -0.07]} s={[0.36, 0.26, 0.18]}>{m}</Blob>
          <Blob p={[0.16, -0.04, 0.0]} s={[0.07, 0.22, 0.2]}>{m}</Blob>
          <Blob p={[-0.16, -0.04, 0.0]} s={[0.07, 0.22, 0.2]}>{m}</Blob>
        </>
      )}
      {style === "bun" && (
        <>
          <mesh castShadow position={[0, 0.2, -0.08]}><sphereGeometry args={[0.08, 12, 10]} />{m}</mesh>
          <Blob p={[0, -0.02, -0.11]} s={[0.32, 0.22, 0.1]}>{m}</Blob>
        </>
      )}
      {style === "ponytail" && (
        <>
          <mesh castShadow position={[0, 0.0, -0.17]} rotation={[0.5, 0, 0]}><capsuleGeometry args={[0.055, 0.2, 4, 10]} />{m}</mesh>
          <mesh castShadow position={[0, 0.07, -0.14]}><sphereGeometry args={[0.04, 10, 8]} /><meshStandardMaterial color="#5a95e8" /></mesh>
        </>
      )}
      {style === "short" && (
        <Blob p={[0, -0.02, -0.1]} s={[0.33, 0.18, 0.12]}>{m}</Blob>
      )}
    </group>
  );
}

function Accessory({ kind, color }: { kind: string; color: string }) {
  if (kind === "glasses" || kind === "roundglasses") {
    const c = kind === "glasses" ? "#4a3626" : "#d9a84a";
    return (
      <group position={[0, 0.17, 0.155]}>
        {[0.058, -0.058].map((x) => (
          <mesh key={x} position={[x, 0, 0]}><torusGeometry args={[0.04, 0.007, 8, 18]} /><meshStandardMaterial color={c} /></mesh>
        ))}
        <mesh><boxGeometry args={[0.04, 0.008, 0.008]} /><meshStandardMaterial color={c} /></mesh>
      </group>
    );
  }
  if (kind === "headphones") {
    return (
      <group position={[0, 0.15, 0]}>
        <mesh><torusGeometry args={[0.185, 0.016, 8, 24, Math.PI]} /><meshStandardMaterial color="#fffaf0" /></mesh>
        {[0.185, -0.185].map((x) => (
          <mesh key={x} position={[x, 0, 0]} rotation={[0, 0, Math.PI / 2]}><cylinderGeometry args={[0.065, 0.065, 0.05, 16]} /><meshStandardMaterial color={color} /></mesh>
        ))}
      </group>
    );
  }
  if (kind === "headset") {
    return (
      <group position={[0, 0.15, 0]}>
        <mesh><torusGeometry args={[0.185, 0.012, 8, 24, Math.PI]} /><meshStandardMaterial color="#fffaf0" /></mesh>
        <mesh position={[0.185, 0, 0]} rotation={[0, 0, Math.PI / 2]}><cylinderGeometry args={[0.05, 0.05, 0.04, 14]} /><meshStandardMaterial color={color} /></mesh>
        <mesh position={[0.17, -0.1, 0.1]} rotation={[0, 0.5, -0.5]}><boxGeometry args={[0.015, 0.015, 0.14]} /><meshStandardMaterial color="#fffaf0" /></mesh>
        <mesh position={[0.1, -0.13, 0.17]}><sphereGeometry args={[0.02, 8, 6]} /><meshStandardMaterial color={color} /></mesh>
      </group>
    );
  }
  if (kind === "hardhat") {
    return (
      <group position={[0, 0.2, 0]}>
        <mesh castShadow><sphereGeometry args={[0.19, 20, 12, 0, Math.PI * 2, 0, Math.PI * 0.5]} /><meshStandardMaterial color="#ffd04d" roughness={0.6} /></mesh>
        <mesh position={[0, 0.02, 0.12]} scale={[1, 0.3, 1]}><sphereGeometry args={[0.13, 14, 8]} /><meshStandardMaterial color="#ffd04d" roughness={0.6} /></mesh>
        <mesh position={[0, 0.1, 0]}><boxGeometry args={[0.04, 0.03, 0.34]} /><meshStandardMaterial color="#f0b830" /></mesh>
      </group>
    );
  }
  return null;
}
