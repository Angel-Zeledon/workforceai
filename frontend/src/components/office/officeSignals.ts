// Derives, from the real store state, which agents must be at the approvals desk, in the cafeteria or in the
// meeting room. Memoized by source identity: recomputed only when approvals / tasks / requests / budget / projects
// change, so every character can call getSignals() each frame at O(1).
import { useStore } from "@/lib/store";
import { useCost, type AgentBudget } from "@/lib/cost";
import { useProjects } from "@/lib/projects/store";
import type { Approval } from "@/lib/types";
import { ZONE_CAP, debateSeats, liveDebate } from "./officeState";

export interface Signals {
  /** agentId -> slot at the approvals desk (oldest pending approval first) */
  apprSlot: Map<string, number>;
  /** agentId -> its pending approval (not plan reviews) */
  apprOf: Map<string, Approval>;
  /** agentId -> slot in the cafeteria */
  cafeSlot: Map<string, number>;
  /** agentId -> seat in the meeting room */
  meetingSlot: Map<string, number>;
  /** real phase / task titles of the plan being drafted, in the order they became known */
  phases: string[];
  /** text of the request being planned (shown by the assistant when no phase title is known yet) */
  planningText: string;
  /** per-agent budget cap data (energy bar) */
  budgets: Map<string, AgentBudget>;
  /** team debate being played in the meeting room (B5): who sits, who talks now and what they say */
  debate: LiveDebate | null;
}

export interface LiveDebate { id: string; topic: string; seats: string[]; speaker: string | null; text: string; kind: string; turn: number; phase: string }

const EMPTY: Signals = {
  apprSlot: new Map(), apprOf: new Map(), cafeSlot: new Map(), meetingSlot: new Map(), phases: [], planningText: "", budgets: new Map(), debate: null,
};

const PLANNING_MAX_AGE = 5 * 60_000;
const PROJECT_PLANNING_MAX_AGE = 15 * 60_000;

let key: unknown[] = [];
let cached: Signals = EMPTY;

function same(a: unknown[], b: unknown[]) {
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false;
  return true;
}

function slots(ids: string[], cap: number): Map<string, number> {
  const m = new Map<string, number>();
  ids.slice(0, cap).forEach((id, i) => m.set(id, i));
  return m;
}

export function getSignals(): Signals {
  const S = useStore.getState();
  const C = useCost.getState();
  const P = useProjects.getState();
  // the debate playback moves with the clock: its stage (phase + turn) is part of the key, so the signals change
  // exactly when the next turn starts or the team walks back
  const ld = liveDebate(Object.values(S.debateClocks), Date.now());
  const stageKey = ld ? `${ld.clock.id}:${ld.stage.phase}:${ld.stage.turn}` : "";
  const k = [S.approvals, S.tasks, S.requests, S.plans, S.agentOrder, S.agents, C.alerts, C.status, P.projects, P.planningPhases, S.debates, stageKey];
  if (key.length && same(k, key)) return cached;
  key = k;
  cached = compute(S, C, P);
  return cached;
}

function compute(S: ReturnType<typeof useStore.getState>, C: ReturnType<typeof useCost.getState>, P: ReturnType<typeof useProjects.getState>): Signals {
  const now = Date.now();
  const order = S.agentOrder;
  const known = (id: string) => !!S.agents[id];
  const assistant = order.find((id) => S.agents[id]?.role === "assistant") ?? (S.agents.assistant ? "assistant" : null);
  const taskList = Object.values(S.tasks);

  // ---- approvals desk: pending approvals of agents that are waiting on them (oldest first)
  const apprOf = new Map<string, Approval>();
  const phases: string[] = [];
  const planningIds = new Set<string>();
  const pend = Object.values(S.approvals).filter((a) => a.status === "pending").sort((a, b) => +new Date(a.created_at) - +new Date(b.created_at));
  for (const a of pend) {
    if (!known(a.agent_id)) continue;
    if (a.action === "plan_review") {
      // the plan is under review: the agents of that plan meet in the meeting room
      planningIds.add(a.agent_id);
      const task = S.tasks[a.task_id];
      const reqId = task?.request_id;
      for (const t of reqId ? S.plans[reqId] || [] : []) { planningIds.add(t.agent_id); if (t.title && !phases.includes(t.title)) phases.push(t.title); }
      continue;
    }
    if (!apprOf.has(a.agent_id)) apprOf.set(a.agent_id, a);
  }
  const apprSlot = slots(Array.from(apprOf.keys()), ZONE_CAP.approvals);

  // ---- budget pause -> cafeteria (request / agent caps; org cap is not resumable and fails instead)
  const paused = new Set<string>();
  const pauseRequest = (reqId: string, agentId?: string) => {
    if (agentId && known(agentId)) paused.add(agentId);
    if (!reqId) return;
    if (assistant) paused.add(assistant);
    for (const t of taskList) if (t.request_id === reqId && t.status === "running" && known(t.agent_id)) paused.add(t.agent_id);
  };
  for (const al of Object.values(C.alerts)) {
    if (!al.resumable || al.scope === "org") continue;
    if (al.scope === "agent") { const id = al.agent_id || al.scope_id; if (known(id)) paused.add(id); }
    else pauseRequest(al.request_id || al.scope_id, al.agent_id);
  }
  for (const r of Object.values(S.requests)) if (r.status === "paused") pauseRequest(r.id);
  const budgets = new Map<string, AgentBudget>();
  for (const ab of C.status?.agents || []) {
    budgets.set(ab.agent_id, ab);
    if (ab.paused && known(ab.agent_id)) paused.add(ab.agent_id);
  }
  const cafeSlot = slots(order.filter((id) => paused.has(id)), ZONE_CAP.cafeteria);

  // ---- planning: requests still planning (planner running / plan review) and project drafts being planned
  let planningText = "";
  for (const r of Object.values(S.requests)) {
    if (r.status !== "planning" || now - Date.parse(r.created_at) > PLANNING_MAX_AGE) continue;
    if (assistant) planningIds.add(assistant);
    if (!planningText) planningText = r.text;
    for (const t of S.plans[r.id] || []) { planningIds.add(t.agent_id); if (t.title && !phases.includes(t.title)) phases.push(t.title); }
  }
  for (const p of Object.values(P.projects)) {
    if (p.status !== "planning" || now - Date.parse(p.created_at) > PROJECT_PLANNING_MAX_AGE) continue;
    if (assistant) planningIds.add(assistant);
    if (!planningText) planningText = p.goal || p.name;
    const info = P.planningPhases[p.id];
    if (info) {
      for (const a of info.agents) planningIds.add(a);
      for (const ti of info.titles) if (!phases.includes(ti)) phases.push(ti);
    }
  }
  // ---- team debate (B5): the debaters and the moderator take the meeting room first
  let debate: LiveDebate | null = null;
  const ld = liveDebate(Object.values(S.debateClocks), now);
  if (ld) {
    const seats = debateSeats(ld.clock, assistant).filter((id) => known(id) && !paused.has(id));
    const d = S.debates[ld.clock.id];
    debate = { id: ld.clock.id, topic: d?.topic ?? "", seats, speaker: ld.stage.speaker, text: ld.stage.text, kind: ld.stage.kind, turn: ld.stage.turn, phase: ld.stage.phase };
  }
  const planningOrdered = order.filter((id) => planningIds.has(id) && !paused.has(id) && !debate?.seats.includes(id));
  const meetingSlot = slots([...(debate?.seats ?? []), ...planningOrdered], ZONE_CAP.meeting);

  if (!apprSlot.size && !cafeSlot.size && !meetingSlot.size && !budgets.size && !phases.length) return { ...EMPTY, planningText };
  return { apprSlot, apprOf, cafeSlot, meetingSlot, phases: phases.slice(-24), planningText, budgets, debate };
}
