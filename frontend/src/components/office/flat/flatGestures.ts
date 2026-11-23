// Event gestures of the "Plano" people that the base simulation (agentSim.ts) does not start: pointing / looking when
// work is delegated, a nod when the user asks them something, and the turn-taking of a team debate (listeners look at
// the speaker or nod, the speaker points at the table). Same rules as Character.tsx; it only sets the gesture fields
// of the Sim, which stepAgent plays on its next step.
import { useStore } from "@/lib/store";
import { GESTURE_DURATION, type GestureKind } from "../gestures";
import { getSignals } from "../officeSignals";
import { meetingFocus, userPos } from "../zones";
import type { Sim, Step } from "./agentSim";

interface Mem { linkSrc: unknown; linkTs: number; debTurn: string }
const mem = new WeakMap<Sim, Mem>();

function start(sm: Sim, kind: GestureKind, aimWorld: number | null, canTurn: boolean) {
  sm.gKind = kind; sm.gStart = sm.t; sm.gDur = GESTURE_DURATION[kind];
  if (aimWorld !== null) {
    sm.aimWorld = aimWorld; sm.hasAim = true;
    if (canTurn) { sm.turnYaw = aimWorld; sm.turnUntil = sm.t + sm.gDur * 0.9; }
  } else sm.hasAim = false;
}

/** Call once per frame right after stepAgent. `feet` gives where another agent stands (x, z). */
export function eventGestures(sm: Sim, id: string, step: Step, reduce: boolean, feet: (agentId: string) => [number, number] | null) {
  let m = mem.get(sm);
  if (!m) { m = { linkSrc: useStore.getState().links, linkTs: Date.now(), debTurn: "" }; mem.set(sm, m); }
  if (reduce || step.walking) return;
  const S = useStore.getState();
  const sitting = step.mode.startsWith("seat");
  const canTurn = !sitting && step.atZone === "";
  if (m.linkSrc !== S.links) {
    m.linkSrc = S.links;
    for (let i = S.links.length - 1; i >= 0 && S.links[i].ts > m.linkTs; i--) {
      const l = S.links[i];
      const other = l.from === id ? l.to : l.from;
      if (l.kind === "delegation" && (l.from === id || l.to === id)) {
        let aw: number | null = null;
        if (other === "user") { const u = userPos(); aw = Math.atan2(u[0] - sm.x, u[2] - sm.z); }
        else { const f = feet(other); if (f) aw = Math.atan2(f[0] - sm.x, f[1] - sm.z); }
        if (aw !== null) start(sm, l.from === id ? "point" : "lookAt", aw, canTurn);
      } else if (l.from === "user" && l.to === id && !l.chat) start(sm, "nod", null, canTurn);
    }
    if (S.links.length) m.linkTs = Math.max(m.linkTs, S.links[S.links.length - 1].ts);
  }
  const deb = getSignals().debate;
  if (deb && step.atZone === "meeting" && deb.speaker && deb.seats.includes(id)) {
    const key = `${deb.id}:${deb.turn}`;
    if (m.debTurn !== key) {
      m.debTurn = key;
      if (deb.speaker !== id) {
        const f = feet(deb.speaker);
        if (sm.rng() < 0.35) start(sm, "nod", null, false);
        else if (f) start(sm, "lookAt", Math.atan2(f[0] - sm.x, f[1] - sm.z), false);
      } else if (sm.rng() < 0.6) {
        const mc = meetingFocus();
        start(sm, "point", Math.atan2(mc[0] - sm.x, mc[1] - sm.z), false);
      }
    }
  }
}
