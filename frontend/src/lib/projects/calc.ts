/**
 * Pure helpers shared by the mock backend and the views: rollups, DAG levels, list-scheduling simulation,
 * critical path, health and cost estimation (priors). With a real backend these come from /health, /timeline,
 * /estimate; the client-side versions here are enough for projects up to a few thousand nodes.
 */
import { hasKey, tr } from "../i18n-core";
import type {
  Complexity, Light, NodeState, ProjectApproval, ProjectDetail, ProjectEstimate, ProjectHealth, ProjectNode, ProjectObjective,
} from "./types";

export const AGENT_CAPACITY = 2;
const PRICE_IN = 0.3 / 1e6; // deepseek-chat peak rate per token (agent-runtime MODEL_PRICES)
const PRICE_OUT = 1.2 / 1e6;
const PRIORS: Record<Complexity, { in: number; out: number; secs: number }> = {
  S: { in: 1200, out: 400, secs: 4 },
  M: { in: 2500, out: 900, secs: 7 },
  L: { in: 5000, out: 1800, secs: 10 },
  XL: { in: 9000, out: 3000, secs: 14 },
};
export const priorSeconds = (c: Complexity) => PRIORS[c].secs;
/** Per-node cost estimate with the dependency-context overhead of the spec (7.2). */
export function priorCost(c: Complexity, depCount: number): number {
  const p = PRIORS[c];
  const ctx = Math.min(8000, 800 * depCount);
  return (p.in + ctx) * PRICE_IN + p.out * PRICE_OUT;
}

export const isGroup = (n: ProjectNode) => n.kind === "group";
export const isDoneState = (s: NodeState) => s === "done" || s === "skipped";
export const isTerminal = (s: NodeState) => isDoneState(s) || s === "failed" || s === "cancelled" || s === "blocked";
export const leaves = (nodes: ProjectNode[]) => nodes.filter((n) => !isGroup(n));

export function displayTitle(n: { title: string; title_key?: string; title_params?: Record<string, string> }): string {
  return n.title_key && hasKey(n.title_key) ? tr(n.title_key, n.title_params) : n.title;
}

/** Retrying = ready again after at least one attempt (spec 2.2). */
export const isRetrying = (n: ProjectNode) => n.state === "ready" && n.attempt > 0;

export function topoOrder(nodes: ProjectNode[]): ProjectNode[] {
  return nodes.slice().sort((a, b) => a.dag_level - b.dag_level || a.wbs_path.localeCompare(b.wbs_path));
}

/** Assigns dag_level (longest dependency chain) with an explicit stack; returns false when a cycle exists. */
export function computeLevels(nodes: ProjectNode[]): boolean {
  const map = new Map(nodes.map((n) => [n.id, n]));
  const level = new Map<string, number>();
  const visiting = new Set<string>();
  for (const root of nodes) {
    if (level.has(root.id)) continue;
    const stack: { id: string; i: number }[] = [{ id: root.id, i: 0 }];
    visiting.add(root.id);
    while (stack.length) {
      const top = stack[stack.length - 1];
      const n = map.get(top.id);
      const deps = n ? n.depends_on.filter((d) => map.has(d)) : [];
      if (top.i < deps.length) {
        const d = deps[top.i++];
        if (visiting.has(d)) return false;
        if (!level.has(d)) { visiting.add(d); stack.push({ id: d, i: 0 }); }
      } else {
        level.set(top.id, deps.length ? 1 + Math.max(...deps.map((d) => level.get(d) ?? 0)) : 0);
        visiting.delete(top.id);
        stack.pop();
      }
    }
  }
  for (const n of nodes) n.dag_level = level.get(n.id) ?? 0;
  return true;
}

export interface Counts { done: number; running: number; ready: number; pending: number; awaiting: number; failed: number; total: number }
export function countStates(nodes: ProjectNode[]): Counts {
  const c: Counts = { done: 0, running: 0, ready: 0, pending: 0, awaiting: 0, failed: 0, total: 0 };
  for (const n of leaves(nodes)) {
    c.total++;
    if (isDoneState(n.state)) c.done++;
    else if (n.state === "running") c.running++;
    else if (n.state === "ready") c.ready++;
    else if (n.state === "awaiting_approval") c.awaiting++;
    else if (n.state === "failed" || n.state === "blocked") c.failed++;
    else c.pending++;
  }
  return c;
}

/** Derived state of a group (workflow / objective): worst problem first, then activity. */
export function rollupState(children: ProjectNode[]): NodeState {
  const ls = leaves(children);
  if (!ls.length) return "pending";
  if (ls.some((n) => n.state === "failed")) return "failed";
  if (ls.every((n) => isDoneState(n.state))) return "done";
  if (ls.some((n) => n.state === "running" || isRetrying(n))) return "running";
  if (ls.some((n) => n.state === "awaiting_approval")) return "awaiting_approval";
  if (ls.some((n) => n.state === "blocked")) return "blocked";
  if (ls.every((n) => n.state === "cancelled" || isDoneState(n.state))) return "cancelled";
  if (ls.some((n) => n.state === "ready" || n.state === "waiting") ) return "ready";
  return "pending";
}

export interface Sim { start: Map<string, number>; end: Map<string, number>; makespanEnd: number }
const ts = (iso: string | null) => (iso ? Date.parse(iso) : NaN);

/**
 * List-scheduling simulation with `capacity` concurrent tasks per agent. Done nodes use their real times; running
 * nodes finish after their remaining time; the rest are packed by dependency order. Gates (humans) use no agent slot.
 */
export function simulate(nodes: ProjectNode[], nowMs: number, capacity = AGENT_CAPACITY): Sim {
  const start = new Map<string, number>();
  const end = new Map<string, number>();
  const slots = new Map<string, number[]>();
  const slotsOf = (a: string) => { let s = slots.get(a); if (!s) { s = new Array(capacity).fill(nowMs); slots.set(a, s); } return s; };
  const order = topoOrder(leaves(nodes));
  const byId = new Map(order.map((n) => [n.id, n]));
  const humanWait = 10_000;
  for (const n of order) {
    const dur = n.est_seconds * 1000;
    if (isDoneState(n.state)) {
      const e = ts(n.finished_at); const s = ts(n.started_at);
      end.set(n.id, Number.isNaN(e) ? nowMs : e); start.set(n.id, Number.isNaN(s) ? (Number.isNaN(e) ? nowMs : e) : s);
      continue;
    }
    const depEnd = Math.max(0, ...n.depends_on.filter((d) => byId.has(d)).map((d) => end.get(d) ?? nowMs));
    const humanGate = n.kind === "gate";
    let s: number; let e: number;
    if (n.state === "running" || n.state === "awaiting_approval" || n.state === "waiting") {
      const st = ts(n.started_at); s = Number.isNaN(st) ? nowMs : st;
      e = nowMs + Math.max(0, dur * (1 - n.progress / 100)) + (n.state === "awaiting_approval" || humanGate ? humanWait : 0);
      if (n.agent_id) { const sl = slotsOf(n.agent_id); const i = sl.indexOf(Math.min(...sl)); sl[i] = Math.max(sl[i], e); }
    } else if (humanGate) {
      s = Math.max(nowMs, depEnd); e = s + (dur || humanWait);
    } else {
      const sl = n.agent_id ? slotsOf(n.agent_id) : [nowMs];
      const i = sl.indexOf(Math.min(...sl));
      s = Math.max(nowMs, depEnd, sl[i]); e = s + dur + (n.approval_action ? humanWait : 0);
      sl[i] = s + dur;
    }
    start.set(n.id, s); end.set(n.id, e);
  }
  return { start, end, makespanEnd: Math.max(nowMs, ...Array.from(end.values())) };
}

export function criticalPath(nodes: ProjectNode[]): { ids: string[]; seconds: number } {
  const ls = topoOrder(leaves(nodes));
  const w = (n: ProjectNode) => (isDoneState(n.state) ? 0 : n.est_seconds * (1 - n.progress / 100));
  const best = new Map<string, number>(); const prev = new Map<string, string | null>();
  for (const n of ls) {
    let b = 0; let p: string | null = null;
    for (const d of n.depends_on) { const v = best.get(d); if (v !== undefined && v > b) { b = v; p = d; } }
    best.set(n.id, b + w(n)); prev.set(n.id, p);
  }
  let tail: string | null = null; let max = -1;
  for (const n of ls) { const v = best.get(n.id) ?? 0; if (v > max && !isDoneState(n.state)) { max = v; tail = n.id; } }
  const ids: string[] = [];
  const nodeMap = new Map(ls.map((n) => [n.id, n]));
  while (tail) { const n = nodeMap.get(tail); if (n && !isDoneState(n.state)) ids.unshift(tail); tail = prev.get(tail) ?? null; }
  return { ids, seconds: Math.max(0, max) };
}

export function computeHealth(d: ProjectDetail, nowMs = Date.now()): ProjectHealth {
  const nodes = d.nodes; const p = d.project; const ls = leaves(nodes);
  const c = countStates(nodes);
  const cp = criticalPath(nodes);
  const sim = simulate(nodes, nowMs);
  const finished = p.status === "done";
  const etaP50 = finished ? ts(p.finished_at) : sim.makespanEnd;
  const etaP90 = finished ? etaP50 : nowMs + (etaP50 - nowMs) * 1.4;
  const deadline = ts(p.deadline_at);
  const remainingCost = ls.filter((n) => !isDoneState(n.state)).reduce((s, n) => s + n.est_cost_usd * (1 - n.progress / 100), 0);
  const forecast = p.spent_usd + remainingCost; const forecastP90 = p.spent_usd + remainingCost * 1.5;
  const started = ts(p.started_at); const hours = Number.isNaN(started) ? 0 : Math.max(1 / 3600, (nowMs - started) / 3_600_000);
  const pend = d.approvals.filter((a) => a.status === "pending");
  const oldest = pend.length ? Math.max(...pend.map((a) => (nowMs - Date.parse(a.created_at)) / 1000)) : 0;
  const cpSet = new Set(cp.ids);
  const blockingCritical = pend.filter((a) => cpSet.has(a.node_id)).length;
  const risks: ProjectHealth["risks"] = [];
  const retry = ls.filter((n) => n.attempt > 1 || isRetrying(n));
  if (retry.length) risks.push({ kind: "retry", node_ids: retry.map((n) => n.id), count: retry.length });
  const failed = ls.filter((n) => n.state === "failed" || n.state === "blocked");
  if (failed.length) risks.push({ kind: "failed", node_ids: failed.map((n) => n.id), count: failed.length });
  const queue = new Map<string, string[]>();
  for (const n of ls) if (n.state === "ready" && n.agent_id) queue.set(n.agent_id, [...(queue.get(n.agent_id) ?? []), n.id]);
  for (const [agent, ids] of queue) if (ids.length >= 3) risks.push({ kind: "agent_saturated", node_ids: ids, agent_id: agent, count: ids.length });
  const overBudget = p.budget_usd > 0 && forecast > p.budget_usd;
  let light: Light = "green";
  const hasDeadline = !Number.isNaN(deadline);
  if (!finished && ((hasDeadline && etaP50 > deadline) || overBudget || failed.length)) light = "red";
  else if (!finished && ((hasDeadline && etaP90 > deadline) || (p.budget_usd > 0 && forecastP90 > 0.95 * p.budget_usd) || oldest > 60 || risks.length)) light = "amber";
  if (p.status === "done") light = "green";
  const byObjective = d.objectives.map((o) => {
    const own = ls.filter((n) => n.objective_id === o.id);
    const done = own.filter((n) => isDoneState(n.state)).length;
    return { id: o.id, pct: own.length ? done / own.length : 0, state: rollupState(own) as ProjectHealth["by_objective"][number]["state"], spent_usd: own.reduce((s, n) => s + n.cost_usd, 0) };
  });
  const iso = (ms: number) => (Number.isFinite(ms) ? new Date(ms).toISOString() : null);
  return {
    project_id: p.id, computed_at: new Date(nowMs).toISOString(), light,
    progress: { tasks_done: c.done, tasks_total: c.total, weighted_pct: c.total ? c.done / c.total : 0 },
    schedule: { deadline_at: p.deadline_at, eta_p50: iso(etaP50), eta_p90: iso(etaP90), slip_seconds_p50: hasDeadline ? Math.round((etaP50 - deadline) / 1000) : null },
    critical_path: { length_seconds: cp.seconds, node_ids: cp.ids, blocked_on_human: blockingCritical },
    budget: {
      limit_usd: p.budget_usd, spent_usd: p.spent_usd, burn_usd_per_h: p.spent_usd / hours,
      forecast_at_completion_usd: forecast, forecast_p90_usd: forecastP90,
      warning: overBudget ? "over" : p.budget_usd > 0 && forecastP90 > 0.95 * p.budget_usd ? "p90_near_limit" : "none",
    },
    waiting_human: { count: pend.length, oldest_age_seconds: oldest, blocking_critical: blockingCritical },
    risks, by_objective: byObjective,
  };
}

export function estimatePlan(nodes: ProjectNode[], objectives: ProjectObjective[], t0 = 0): ProjectEstimate {
  const ls = leaves(nodes);
  const retryFactor = (n: ProjectNode) => 1 + 0.08 * Math.max(0, n.max_attempts - 1);
  const p50 = ls.reduce((s, n) => s + n.est_cost_usd * retryFactor(n), 0);
  const sim = simulate(ls.map((n) => ({ ...n, state: "pending" as NodeState, progress: 0 })), t0);
  const secs = (sim.makespanEnd - t0) / 1000;
  const human = ls.filter((n) => n.kind === "gate" || n.approval_action).length * 10;
  const byObj = objectives.map((o) => {
    const own = ls.filter((n) => n.objective_id === o.id);
    const v = own.reduce((s, n) => s + n.est_cost_usd * retryFactor(n), 0);
    return { id: o.id, p50_usd: v, p90_usd: v * 1.7, nodes: own.length };
  });
  const agents = new Map<string, { usd: number; calls: number }>();
  for (const n of ls) if (n.agent_id) { const a = agents.get(n.agent_id) ?? { usd: 0, calls: 0 }; a.usd += n.est_cost_usd; a.calls++; agents.set(n.agent_id, a); }
  const warnings: ProjectEstimate["warnings"] = [];
  if (objectives.length > 1) for (const o of byObj) if (p50 > 0 && o.p50_usd / p50 > 0.4) warnings.push({ key: "pv.est.warn.share", params: { id: o.id } });
  return {
    basis: "priors", model: "deepseek-chat", confidence: ls.length > 30 ? "medium" : "low",
    total: { p50_usd: p50, p90_usd: p50 * 1.7, calls: Math.round(ls.length * 1.3) },
    duration: { p50_seconds: secs, p90_seconds: secs * 1.4, human_wait_seconds: human },
    by_objective: byObj,
    by_agent: Array.from(agents, ([agent_id, v]) => ({ agent_id, p50_usd: v.usd, calls: v.calls })),
    warnings,
  };
}

export interface PlanIssue { severity: "error" | "warn"; node_id?: string; code: "cycle" | "no_agent" | "empty_title" | "no_nodes" }
export function validatePlan(nodes: ProjectNode[], agentIds: string[]): PlanIssue[] {
  const issues: PlanIssue[] = [];
  const ls = leaves(nodes);
  if (!ls.length) issues.push({ severity: "error", code: "no_nodes" });
  const copy = nodes.map((n) => ({ ...n }));
  if (!computeLevels(copy)) issues.push({ severity: "error", code: "cycle" });
  for (const n of ls) {
    if (!displayTitle(n).trim()) issues.push({ severity: "error", node_id: n.id, code: "empty_title" });
    if (n.kind !== "gate" && n.kind !== "milestone" && n.kind !== "wait" && (!n.agent_id || (agentIds.length && !agentIds.includes(n.agent_id)))) issues.push({ severity: "error", node_id: n.id, code: "no_agent" });
  }
  return issues;
}

/** Why a node is not moving: deterministic walk over unfinished dependencies and approvals (spec 6.4, reduced). */
export interface Blocker { kind: "dependency" | "approval" | "failed" | "paused" | "agent_busy"; node_id: string }
export function explainNode(nodes: ProjectNode[], approvals: ProjectApproval[], id: string): Blocker[] {
  const map = new Map(nodes.map((n) => [n.id, n]));
  const n = map.get(id);
  if (!n) return [];
  const out: Blocker[] = [];
  if (n.state === "awaiting_approval" && approvals.some((a) => a.node_id === id && a.status === "pending")) out.push({ kind: "approval", node_id: id });
  for (const d of n.depends_on) {
    const dn = map.get(d);
    if (!dn || isDoneState(dn.state)) continue;
    out.push({ kind: dn.state === "failed" || dn.state === "blocked" ? "failed" : dn.state === "awaiting_approval" ? "approval" : "dependency", node_id: d });
  }
  if (n.state === "ready" && n.agent_id) out.push({ kind: "agent_busy", node_id: id });
  if (n.state === "paused") out.push({ kind: "paused", node_id: id });
  return out;
}

export function fmtDuration(seconds: number): string {
  const s = Math.max(0, Math.round(seconds));
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m ${String(s % 60).padStart(2, "0")}s`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ${String(Math.floor((s % 3600) / 60)).padStart(2, "0")}m`;
  return `${Math.floor(s / 86400)}d ${Math.floor((s % 86400) / 3600)}h`;
}
