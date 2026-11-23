// Where each real agent is and how it moves in the "Plano" office. It is the 2D twin of Character.tsx's simulation: the
// same pure decisions (zoneFor, cafeSpot, meetingPlace, approvalsSpot, computePose, applyGesture...) drive it, fed from
// the real store. Only the placement glue is repeated here, because Character keeps it inside a three.js frame loop.
import { useStore } from "@/lib/store";
import type { AgentState } from "@/lib/types";
import { cafeCenter, cafeParts, meetingRoom, nearUser, userFrame, userPos } from "../zones";
import { approvalsSpot, cafeSpot, meetingPlace, zoneFor } from "../officeState";
import { getSignals } from "../officeSignals";
import { AISLE_Z } from "../registry";
import {
  GESTURE_DURATION, KEYS, WALK_SPEED, applyGesture, clamp, computePose, createRng, fidgetDelay, hashSeed, holdsMug,
  pickFidget, propFor, smooth, wrapAngle as wrap, zeroPose, type GestureKind, type Pose,
} from "../gestures";

const SEATED = new Set<AgentState>(["thinking", "working", "waiting", "reviewing", "blocked", "awaiting_approval"]);
const ACTIVE_TASK = new Set(["pending", "running", "awaiting_approval"]);
const deliverPos = (): [number, number] => { const p = nearUser(-1.6, -1.1); return [p[0], p[2]]; };
const spotKey = (p: readonly [number, number]) => `${p[0].toFixed(2)},${p[1].toFixed(2)}`;

type XZ = [number, number];
export interface Own { seat: XZ; stand: XZ }

function route(cur: XZ, dest: XZ, toSeat: boolean, own: Own) {
  const pts: XZ[] = [];
  const d = (a: XZ, b: XZ) => Math.hypot(a[0] - b[0], a[1] - b[1]);
  let from = cur;
  if (d(cur, own.seat) < 0.3) { pts.push(own.stand); from = own.stand; }
  const target = toSeat ? own.stand : dest;
  if (d(from, target) > 3.2) { pts.push([from[0], AISLE_Z]); pts.push([target[0], AISLE_Z]); }
  pts.push(target);
  if (toSeat) pts.push(own.seat);
  return pts;
}

export function makeSim(id: string, seat: XZ) {
  const rng = createRng(hashSeed(id));
  return {
    x: seat[0], z: seat[1], yaw: Math.PI, t: rng() * 40, pose: zeroPose(), target: zeroPose(), place: "", pts: [] as XZ[],
    rng, phase: rng() * Math.PI * 2, rate: 0.85 + rng() * 0.3, inited: false, prev: "", pop: 0,
    taskSrc: null as unknown, hasTask: false, speed: 0, stride: 0, mode: "", blend: 1,
    gKind: null as GestureKind | null, gStart: 0, gDur: 1, hasAim: false, aimWorld: 0, turnYaw: 0, turnUntil: 0, nextFid: 5, wasSel: false,
    leaveAt: 0, curDest: [0, 0] as XZ, curFace: null as number | null, apprSrc: null as unknown, seenAppr: new Set<string>(),
  };
}
export type Sim = ReturnType<typeof makeSim>;

/** What the renderer needs from one step. */
export interface Step {
  walking: boolean; mode: string; gu: number; prop: 0 | 1 | 2; mug: boolean; talking: boolean;
  /** squash and stretch pop after a change of state (0 = none) */
  sq: number; atZone: "" | "cafe" | "meeting" | "appr";
}

function startGesture(sm: Sim, kind: GestureKind, aimWorld: number | null, canTurn: boolean) {
  sm.gKind = kind; sm.gStart = sm.t; sm.gDur = GESTURE_DURATION[kind];
  if (aimWorld !== null) {
    sm.aimWorld = aimWorld; sm.hasAim = true;
    if (canTurn) { sm.turnYaw = aimWorld; sm.turnUntil = sm.t + sm.gDur * 0.9; }
  } else sm.hasAim = false;
}

/** The camera looks from +x, +z: a person who greets whoever opened them turns toward it. */
const TOWARD_CAMERA = Math.atan2(1, 1);

export function stepAgent(sm: Sim, id: string, dt: number, own: Own, ctx: { selected: boolean; reduce: boolean; deskStand: (agentId: string) => XZ | null; deskVisit: (agentId: string) => XZ | null }): Step | null {
  const S = useStore.getState();
  const ag = S.agents[id];
  if (!ag) return null;
  const reduce = ctx.reduce;
  if (!sm.inited) {
    sm.inited = true;
    const start = SEATED.has(ag.state) ? own.seat : own.stand;
    sm.x = start[0]; sm.z = start[1];
    sm.yaw = SEATED.has(ag.state) ? Math.PI : 0.35;
    sm.place = SEATED.has(ag.state) ? "seat" : "stand";
    sm.wasSel = ctx.selected; sm.apprSrc = S.approvals;
    sm.nextFid = sm.t + 2 + sm.rng() * 8;
  }
  sm.t += dt;
  const now = Date.now();
  const st = ag.state;
  if (sm.taskSrc !== S.tasks) {
    sm.taskSrc = S.tasks;
    sm.hasTask = Object.values(S.tasks).some((tk) => tk.agent_id === id && ACTIVE_TASK.has(tk.status));
  }

  // --- desired placement (from the agent's real state and the real consult messages)
  let visit: string | null = null;
  if (st === "talking") {
    for (let i = S.links.length - 1; i >= 0; i--) {
      const l = S.links[i];
      if (l.kind === "consult" && l.from === id && now - l.ts < 20000 && S.agents[l.to] && l.to !== id) { visit = l.to; break; }
    }
  }
  const sig = getSignals();
  const USER = userPos();
  const zone = zoneFor({ state: st, budgetPaused: sig.cafeSlot.has(id), planning: sig.meetingSlot.has(id), approvalPending: sig.apprSlot.has(id), debating: !!sig.debate?.seats.includes(id) && sig.meetingSlot.has(id) });
  let place = "stand";
  let dest: XZ = own.stand;
  let face: number | null = null;
  let meetSit = true;
  const visitDest = visit ? ctx.deskVisit(visit) : null;
  if (visit && visitDest) {
    place = `visit:${visit}`; dest = visitDest;
    const tgt = ctx.deskStand(visit) ?? visitDest;
    face = Math.atan2(tgt[0] - dest[0], tgt[1] - dest[1]);
  } else if (zone === "cafeteria") {
    const cs = cafeSpot(sig.cafeSlot.get(id) ?? 0, cafeCenter(), cafeParts());
    place = `cafe:${sig.cafeSlot.get(id)}:${spotKey(cs.pos)}`; dest = cs.pos; face = cs.yaw;
  } else if (zone === "meeting") {
    const ms = meetingPlace(meetingRoom(), sig.meetingSlot.get(id) ?? 0);
    place = `meeting:${sig.meetingSlot.get(id)}:${spotKey(ms.pos)}`; dest = ms.pos; face = ms.yaw; meetSit = ms.sit;
  } else if (zone === "approvals") {
    const rot = userFrame().rot;
    dest = approvalsSpot(USER, sig.apprSlot.get(id) ?? 0, rot);
    place = `appr:${sig.apprSlot.get(id)}:${spotKey(dest)}`; face = rot;
  } else if (SEATED.has(st) || (st === "idle" && sm.hasTask)) { place = "seat"; dest = own.seat; }
  else if (st === "completed" && ag.role === "assistant") {
    place = "deliver"; dest = deliverPos();
    face = Math.atan2(USER[0] - dest[0], USER[2] - dest[1]);
  }
  // after a decision the agent stays at the approvals desk long enough for the nod / head shake
  if (sm.place.startsWith("appr:") && !place.startsWith("appr:") && !reduce) {
    if (sm.leaveAt === 0) sm.leaveAt = sm.t + 2;
    if (sm.t < sm.leaveAt) { place = sm.place; dest = sm.curDest; face = sm.curFace; }
  }
  if (place !== sm.place) {
    sm.place = place; sm.leaveAt = 0; sm.curDest = dest; sm.curFace = face;
    sm.pts = route([sm.x, sm.z], dest, place === "seat", own);
    if (reduce) { sm.x = dest[0]; sm.z = dest[1]; sm.pts = []; } // reduced motion: no walking, straight to the place
  }

  // --- movement: speed eases in and out, the stride advances with the distance
  let walking = false;
  let moveYaw: number | null = null;
  const wp = sm.pts[0];
  if (wp) {
    const dx = wp[0] - sm.x, dz = wp[1] - sm.z;
    const d = Math.hypot(dx, dz);
    if (d < 0.05) { sm.x = wp[0]; sm.z = wp[1]; sm.pts.shift(); }
    else {
      const speedTarget = sm.pts.length === 1 ? clamp(d * 2.6, 0.7, WALK_SPEED) : WALK_SPEED;
      sm.speed += (speedTarget - sm.speed) * Math.min(1, dt * 7);
      if (sm.speed < 0.5) sm.speed = 0.5;
      const step = Math.min(d, sm.speed * dt);
      sm.x += (dx / d) * step; sm.z += (dz / d) * step;
      sm.stride += sm.speed * dt * 3.6;
      walking = true; moveYaw = Math.atan2(dx, dz);
    }
  }
  if (!walking) sm.speed = 0;

  const atZone = place.startsWith("cafe:") ? "cafe" : place.startsWith("meeting:") ? "meeting" : place.startsWith("appr:") ? "appr" : "";
  const mode = walking ? "walk"
    : atZone === "cafe" ? "stand:cafe"
    : atZone === "meeting" ? (meetSit ? "seat:meeting" : "stand:idle")
    : atZone === "appr" ? (st === "awaiting_approval" ? "stand:awaiting_approval" : "stand:idle")
    : place === "seat" ? `seat:${st}` : `stand:${visit ? "talking" : st}`;

  // --- gestures: greeting on select, the outcome of an approval, and idle fidgets
  const standing = place !== "seat" && !(atZone === "meeting" && meetSit);
  const canTurn = standing && face === null;
  if (!reduce) {
    if (ctx.selected && !sm.wasSel && !walking) startGesture(sm, "wave", TOWARD_CAMERA, canTurn);
    if (sm.apprSrc !== S.approvals) {
      sm.apprSrc = S.approvals;
      for (const a of Object.values(S.approvals)) {
        if (a.agent_id !== id || a.status === "pending" || sm.seenAppr.has(a.id)) continue;
        sm.seenAppr.add(a.id);
        const at = a.resolved_at ? Date.parse(a.resolved_at) : NaN;
        if (!Number.isNaN(at) && Math.abs(now - at) < 15000 && !walking) startGesture(sm, a.status === "approved" ? "nod" : "headShake", null, canTurn);
      }
    }
  }
  sm.wasSel = ctx.selected;
  if (reduce || walking) sm.gKind = null;
  else if (sm.gKind && sm.t - sm.gStart >= sm.gDur) sm.gKind = null;
  if (!sm.gKind && !reduce && !walking && sm.t >= sm.nextFid) {
    const k = pickFidget(mode, sm.rng());
    if (k) startGesture(sm, k, null, canTurn);
    sm.nextFid = sm.t + fidgetDelay(mode, sm.rng());
  }
  const gu = sm.gKind ? (sm.t - sm.gStart) / sm.gDur : 0;

  // --- orientation
  let targetYaw: number;
  if (moveYaw !== null) targetYaw = moveYaw;
  else if (place === "seat") targetYaw = Math.PI;
  else if (face !== null) targetYaw = face;
  else if (sm.t < sm.turnUntil) targetYaw = sm.turnYaw;
  else targetYaw = 0.35;
  sm.yaw += wrap(targetYaw - sm.yaw) * Math.min(1, dt * (walking ? 9 : 5));

  // --- pose: the base pose of the mode, a gesture layer, eased
  if (mode !== sm.mode) { sm.mode = mode; sm.blend = 0; }
  sm.blend = Math.min(1, sm.blend + dt / 0.45);
  const target: Pose = computePose(sm.target, mode, reduce ? 0 : sm.t, sm.phase, sm.rate, sm.stride, sm.speed / WALK_SPEED);
  if (sm.gKind) {
    const aim = sm.hasAim ? wrap(sm.aimWorld - sm.yaw) : (sm.gKind === "wave" ? (place === "seat" ? 0.6 : 0) : 0);
    applyGesture(target, sm.gKind, gu, sm.t, aim);
  }
  const k = Math.min(1, dt * (walking ? 14 : 8) * (0.4 + 0.6 * smooth(sm.blend)));
  if (reduce) for (const key of KEYS) sm.pose[key] = target[key];
  else for (const key of KEYS) sm.pose[key] += (target[key] - sm.pose[key]) * k;

  // soft squash and stretch when the real state of the agent changes
  if (sm.prev !== st) { if (sm.prev) sm.pop = 1; sm.prev = st; }
  sm.pop = reduce ? 0 : Math.max(0, sm.pop - dt * 1.4);
  const sq = Math.sin((1 - sm.pop) * Math.PI * 3) * sm.pop * 0.09;

  const prop = propFor(sm.gKind, gu);
  return {
    walking, mode, gu, prop, mug: prop === 1 || (!walking && holdsMug(mode)), sq, atZone,
    talking: st === "talking" || (!reduce && sig.debate?.speaker === id && atZone === "meeting"),
  };
}
