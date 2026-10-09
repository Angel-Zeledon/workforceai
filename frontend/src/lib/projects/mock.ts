/**
 * Projects mock backend (NEXT_PUBLIC_MOCK=true). Simulates what the Go backend will expose under /projects
 * (docs/architecture/workflow-visualization.md): drafts with a progressive planner, estimate, launch, a durable-style
 * engine (ready queue, per-agent concurrency 2, dependencies, fork/join delegation, approvals, retries, budget hold),
 * pause/resume/cancel and project templates. It is independent from the legacy mock engine.
 */
import { hasKey, tr } from "../i18n-core";
import {
  AGENT_CAPACITY, computeHealth, computeLevels, countStates, estimatePlan, isDoneState, isGroup, isRetrying, leaves,
  priorCost, priorSeconds, rollupState, simulate, topoOrder, validatePlan,
} from "./calc";
import { BUILTIN_TEMPLATES } from "./templates";
import type {
  ControlAction, LaunchBody, NewProjectBody, PlanOp, ProjectApproval, ProjectDetail, ProjectFrame, ProjectHealth,
  ProjectNode, ProjectObjective, ProjectSummary, ProjectTemplate, TemplateNode,
} from "./types";

const TICK_MS = 500;
const DT = TICK_MS / 1000;
let counter = 0;
const uid = (p: string) => `${p}-${Date.now().toString(36)}${(counter++).toString(36)}`;
const iso = () => new Date().toISOString();
const pad = (n: number) => String(n).padStart(4, "0");

interface MockProject {
  detail: ProjectDetail;
  dirty: Set<string>;
  approvalsDirty: boolean;
  statusDirty: boolean;
  resumed: Set<string>;
  approved: Set<string>;
  failOnce: Set<string>;
  delegates: Map<string, NonNullable<TemplateNode["delegate"]>>;
  costFactor: Map<string, number>;
  budgetAck: boolean;
}

function resolveTitle(key: string, literal: string | undefined, params: Record<string, string>) {
  return literal ?? (hasKey(key) ? tr(key, params) : key);
}

export class ProjectsMock {
  private projects = new Map<string, MockProject>();
  private tpls: ProjectTemplate[] = BUILTIN_TEMPLATES.map((t) => ({ ...t }));
  private listeners = new Set<(f: ProjectFrame) => void>();
  private timer: ReturnType<typeof setInterval> | null = null;
  private timeouts = new Set<ReturnType<typeof setTimeout>>();

  constructor() {
    // Canonical example, ready to launch: the accountant builds the balance sheet and, in parallel, the income statement.
    const tpl = this.tpls.find((t) => t.key === "financial_close")!;
    this.instantDraft(tpl, {}, "");
  }

  // ---- realtime ---------------------------------------------------------------------------------
  connect(cb: (f: ProjectFrame) => void) {
    this.listeners.add(cb);
    if (!this.timer) this.timer = setInterval(() => this.tick(), TICK_MS);
    return () => {
      this.listeners.delete(cb);
      if (!this.listeners.size && this.timer) { clearInterval(this.timer); this.timer = null; }
    };
  }
  private emit(f: ProjectFrame) { this.listeners.forEach((l) => l(f)); }
  private later(fn: () => void, ms: number) {
    const h = setTimeout(() => { this.timeouts.delete(h); fn(); }, ms);
    this.timeouts.add(h);
  }

  // ---- building ---------------------------------------------------------------------------------
  private build(pid: string, tpl: ProjectTemplate, params: Record<string, string>) {
    const objectives: ProjectObjective[] = [];
    const nodes: ProjectNode[] = [];
    const keyToId = new Map<string, string>();
    const specs = new Map<string, TemplateNode>();
    const all = { ...params };
    tpl.objectives.forEach((o, oi) => {
      const oid = `${pid}:${o.key}`;
      objectives.push({ id: oid, title: resolveTitle(o.title_key, o.title, all), title_key: o.title ? undefined : o.title_key, title_params: all, position: oi });
      o.workflows.forEach((w, wi) => {
        const gid = `${pid}:${w.key}`;
        nodes.push(this.mkNode(pid, gid, oid, null, "group", resolveTitle(w.title_key, w.title, all), w.title ? undefined : w.title_key, all, `${pad(oi + 1)}.${pad(wi + 1)}`));
        w.nodes.forEach((n, ni) => {
          const id = `${pid}:${n.key}`;
          keyToId.set(n.key, id); specs.set(id, n);
          const node = this.mkNode(pid, id, oid, gid, n.kind ?? "task", resolveTitle(n.title_key, n.title, all), n.title ? undefined : n.title_key, all, `${pad(oi + 1)}.${pad(wi + 1)}.${pad(ni + 1)}`);
          node.agent_id = n.agent ?? null;
          node.complexity = n.complexity ?? "S";
          const human = node.kind === "gate" || node.kind === "milestone" || node.kind === "wait";
          node.est_seconds = n.secs ?? (human ? 6 : priorSeconds(node.complexity));
          node.approval_action = n.approval?.action;
          nodes.push(node);
        });
      });
    });
    for (const n of nodes) {
      const spec = specs.get(n.id);
      if (!spec) continue;
      n.depends_on = (spec.deps ?? []).map((k) => keyToId.get(k)!).filter(Boolean);
      const human = n.kind === "gate" || n.kind === "milestone" || n.kind === "wait";
      n.est_cost_usd = human ? 0 : priorCost(n.complexity, n.depends_on.length);
    }
    computeLevels(nodes);
    return { objectives, nodes, specs };
  }

  private mkNode(pid: string, id: string, oid: string, parent: string | null, kind: ProjectNode["kind"], title: string, title_key: string | undefined, params: Record<string, string>, wbs: string): ProjectNode {
    return {
      id, project_id: pid, objective_id: oid, parent_id: parent, kind, title, title_key, title_params: params,
      agent_id: null, state: "draft", attempt: 0, max_attempts: 3, progress: 0, depends_on: [], delegation_depth: 1, delegation_chain: [],
      dag_level: 0, wbs_path: wbs, complexity: "S", est_cost_usd: 0, est_seconds: 0, cost_usd: 0,
      plan_start_ms: null, plan_end_ms: null, started_at: null, finished_at: null, rev: 0,
    };
  }

  private register(pid: string, tpl: ProjectTemplate, goal: string, params: Record<string, string>, budget: number | undefined, status: ProjectDetail["project"]["status"]) {
    const gp = { ...params, goal: goal.slice(0, 60) };
    const { objectives, nodes, specs } = this.build(pid, tpl, gp);
    for (const n of nodes) if (n.agent_id) n.delegation_chain = [n.agent_id];
    const generic = tpl.key === "generic";
    const project: ProjectSummary = {
      id: pid, name: generic && goal ? goal.slice(0, 60) : tpl.name_key ? tr(tpl.name_key, gp) : tpl.name, name_key: generic && goal ? undefined : tpl.name_key,
      goal: goal || (tpl.description_key ? tr(tpl.description_key) : tpl.description), status, control: "active", template_id: tpl.id,
      budget_usd: budget ?? 0, spent_usd: 0, tasks_done: 0, tasks_total: leaves(nodes).length, running: 0, awaiting: 0, failed: 0,
      light: "green", created_at: iso(), started_at: null, finished_at: null, deadline_at: null, objectives_count: objectives.length,
    };
    const detail: ProjectDetail = {
      project, objectives, nodes, approvals: [], estimate: null, structure_version: 1, max_parallel: 4, planning: null,
      budget: { mode: "hard", warn_at: [0.5, 0.8, 0.95], on_hard: "pause_and_ask", reserve_pct: 10 },
    };
    const mp: MockProject = {
      detail, dirty: new Set(), approvalsDirty: false, statusDirty: false, resumed: new Set(), approved: new Set(),
      failOnce: new Set(), delegates: new Map(), costFactor: new Map(), budgetAck: false,
    };
    for (const [id, s] of specs) { if (s.fail_once) mp.failOnce.add(id); if (s.delegate) mp.delegates.set(id, s.delegate); }
    this.projects.set(pid, mp);
    return mp;
  }

  private defaultBudget(est: ReturnType<typeof estimatePlan>) { return Math.ceil(est.total.p90_usd * 1.1 * 1000) / 1000; }

  private instantDraft(tpl: ProjectTemplate, params: Record<string, string>, goal: string) {
    const pid = uid("prj");
    const mp = this.register(pid, tpl, goal, params, undefined, "draft");
    mp.detail.estimate = estimatePlan(mp.detail.nodes, mp.detail.objectives);
    mp.detail.project.budget_usd = this.defaultBudget(mp.detail.estimate);
    return pid;
  }

  // ---- REST-like API ----------------------------------------------------------------------------
  async list(): Promise<ProjectSummary[]> {
    return Array.from(this.projects.values()).map((p) => ({ ...p.detail.project })).sort((a, b) => b.created_at.localeCompare(a.created_at));
  }
  async get(id: string): Promise<ProjectDetail> {
    const mp = this.projects.get(id);
    if (!mp) throw new Error("project not found");
    return JSON.parse(JSON.stringify(mp.detail)) as ProjectDetail;
  }
  async health(id: string): Promise<ProjectHealth> {
    const mp = this.projects.get(id);
    if (!mp) throw new Error("project not found");
    return computeHealth(mp.detail);
  }
  async templates(): Promise<ProjectTemplate[]> {
    return this.tpls.map((t) => ({ ...t, name: t.name_key ? tr(t.name_key) : t.name, description: t.description_key ? tr(t.description_key) : t.description }));
  }

  async createDraft(body: NewProjectBody): Promise<{ project_id: string }> {
    const tpl = this.tpls.find((t) => t.id === body.template_id) ?? this.tpls.find((t) => t.key === "generic")!;
    const params: Record<string, string> = {};
    for (const p of tpl.params) params[p.key] = body.params?.[p.key] ?? p.default;
    const pid = uid("prj");
    const mp = this.register(pid, tpl, body.goal.trim(), params, body.budget_usd, "planning");
    const full = mp.detail.nodes; const objs = mp.detail.objectives;
    const total = leaves(full).length;
    mp.detail.nodes = []; mp.detail.objectives = [];
    mp.detail.planning = { done: 0, total };
    this.emit({ type: "project.created", payload: { project: this.summary(mp), nodes: [], objectives: [], planning: mp.detail.planning } });
    // The hierarchical planner reveals the tree level by level (objective by objective).
    objs.forEach((o, i) => {
      this.later(() => {
        const add = full.filter((n) => n.objective_id === o.id);
        mp.detail.objectives.push(o); mp.detail.nodes.push(...add);
        mp.detail.planning = { done: leaves(mp.detail.nodes).length, total };
        this.emit({ type: "plan.draft_progress", payload: { project: this.summary(mp), nodes: add, objectives: [o], planning: mp.detail.planning } });
        if (i === objs.length - 1) this.later(() => this.finishPlanning(mp, body.budget_usd), 400);
      }, 600 * (i + 1));
    });
    return { project_id: pid };
  }

  private finishPlanning(mp: MockProject, budget?: number) {
    const d = mp.detail;
    d.planning = null; d.project.status = "draft";
    d.estimate = estimatePlan(d.nodes, d.objectives);
    d.project.budget_usd = budget && budget > 0 ? budget : this.defaultBudget(d.estimate);
    this.emit({ type: "project.status_changed", payload: { project: this.summary(mp), planning: null, estimate: d.estimate } });
  }

  async patchPlan(id: string, ops: PlanOp[]) {
    const mp = this.projects.get(id);
    if (!mp || mp.detail.project.status !== "draft") throw new Error("not a draft");
    const changed: ProjectNode[] = [];
    for (const op of ops) {
      if (op.op === "set_quality") { mp.detail.quality = { review: op.fields.review }; continue; }
      const n = mp.detail.nodes.find((x) => x.id === op.id);
      if (!n) continue;
      if (op.fields.acceptance !== undefined) n.acceptance = op.fields.acceptance.map((c) => c.trim()).filter(Boolean).slice(0, 8);
      if (op.fields.title !== undefined) { n.title = op.fields.title; n.title_key = undefined; }
      if (op.fields.agent_id !== undefined) { n.agent_id = op.fields.agent_id; n.delegation_chain = [op.fields.agent_id]; }
      n.rev++; changed.push({ ...n });
    }
    mp.detail.structure_version++;
    mp.detail.estimate = estimatePlan(mp.detail.nodes, mp.detail.objectives);
    this.emit({ type: "project.delta", payload: { project: this.summary(mp), nodes: changed, estimate: mp.detail.estimate, structure_version: mp.detail.structure_version } });
    return { structure_version: mp.detail.structure_version, issues: validatePlan(mp.detail.nodes, []) };
  }

  async setBudget(id: string, usd: number) {
    const mp = this.projects.get(id);
    if (!mp) throw new Error("project not found");
    mp.detail.project.budget_usd = Math.max(0, usd);
    this.emit({ type: "project.delta", payload: { project: this.summary(mp) } });
    return { ok: true };
  }

  async launch(id: string, body: LaunchBody) {
    const mp = this.projects.get(id);
    if (!mp || mp.detail.project.status !== "draft") throw new Error("not a draft");
    const d = mp.detail; const p = d.project;
    const est = d.estimate ?? estimatePlan(d.nodes, d.objectives);
    if (body.approved_budget_usd < est.total.p50_usd && !body.acknowledge_underbudget) throw new Error("underbudget");
    if (validatePlan(d.nodes, []).some((i) => i.severity === "error")) throw new Error("invalid_plan");
    const now = Date.now();
    p.budget_usd = body.approved_budget_usd; d.max_parallel = body.max_parallel ?? 4;
    const sim = simulate(d.nodes.map((n) => ({ ...n, state: "pending" as const })), now);
    for (const n of d.nodes) {
      if (isGroup(n)) { n.state = "pending"; continue; }
      n.state = n.depends_on.length ? "pending" : "ready";
      n.plan_start_ms = (sim.start.get(n.id) ?? now) - now; n.plan_end_ms = (sim.end.get(n.id) ?? now) - now;
      mp.costFactor.set(n.id, 0.8 + Math.random() * 0.5);
      mp.dirty.add(n.id);
    }
    p.status = "running"; p.control = "active"; p.started_at = new Date(now).toISOString();
    p.deadline_at = new Date(now + (sim.makespanEnd - now) * 1.35).toISOString();
    d.structure_version++;
    this.syncGroups(mp);
    mp.dirty = new Set(d.nodes.map((n) => n.id));
    this.emit({ type: "project.status_changed", payload: { project: this.summary(mp), nodes: d.nodes.map((n) => ({ ...n })), structure_version: d.structure_version } });
    mp.dirty.clear();
    return { ok: true };
  }

  // The demo backend has no failing nodes: recovery only exists against the real API.
  async retryNode(_id: string, _nodeId: string) { return { ok: true }; }
  async skipNode(_id: string, _nodeId: string, _reason: string) { return { ok: true }; }

  async control(id: string, action: ControlAction) {
    const mp = this.projects.get(id);
    if (!mp) throw new Error("project not found");
    const p = mp.detail.project;
    if (action === "pause" && (p.status === "running" || p.status === "waiting_human")) { p.control = "paused"; p.status = "paused"; }
    else if (action === "resume" && (p.status === "paused" || (p.status === "waiting_human" && p.control === "paused"))) { p.control = "active"; p.status = "running"; mp.budgetAck = true; }
    else if (action === "cancel" && !["done", "cancelled", "failed", "draft", "planning"].includes(p.status)) {
      p.control = "cancelled"; p.status = "cancelled"; p.finished_at = iso();
      for (const n of mp.detail.nodes) if (!isGroup(n) && !isDoneState(n.state) && n.state !== "failed") { n.state = "cancelled"; n.rev++; mp.dirty.add(n.id); }
      for (const a of mp.detail.approvals) if (a.status === "pending") { a.status = "rejected"; a.resolved_at = iso(); }
      mp.approvalsDirty = true;
      this.syncGroups(mp);
    }
    this.flush(mp, true);
    return { ok: true, status: p.status };
  }

  async decide(projectId: string, approvalId: string, decision: "approve" | "reject") {
    const mp = this.projects.get(projectId);
    const a = mp?.detail.approvals.find((x) => x.id === approvalId);
    if (!mp || !a || a.status !== "pending") throw new Error("approval not pending");
    this.resolve(mp, a, decision);
    this.flush(mp, true);
    return { ok: true };
  }

  async decideBatch(projectId: string, body: { action: string; decision: "approve" | "reject"; expected_count: number; include_high?: boolean }) {
    const mp = this.projects.get(projectId);
    if (!mp) throw new Error("project not found");
    const group = mp.detail.approvals.filter((a) => a.status === "pending" && a.action === body.action);
    if (group.length !== body.expected_count) throw new Error("409 count_mismatch");
    if (group.some((a) => a.risk === "high") && !body.include_high) throw new Error("409 high_risk_needs_confirmation");
    for (const a of group) this.resolve(mp, a, body.decision);
    this.flush(mp, true);
    return { ok: true, count: group.length };
  }

  async saveAsTemplate(id: string): Promise<ProjectTemplate> {
    const mp = this.projects.get(id);
    if (!mp) throw new Error("project not found");
    const d = mp.detail;
    const key = `custom_${this.tpls.length + 1}`;
    const idKey = (nid: string) => nid.slice(nid.indexOf(":") + 1);
    const tpl: ProjectTemplate = {
      id: uid("tpl"), key, version: 1, name: d.project.name, description: d.project.goal, params: [], builtin: false,
      objectives: d.objectives.map((o) => ({
        key: idKey(o.id), title_key: "", title: o.title_key && hasKey(o.title_key) ? tr(o.title_key, o.title_params) : o.title,
        workflows: d.nodes.filter((n) => isGroup(n) && n.objective_id === o.id).map((g) => ({
          key: idKey(g.id), title_key: "", title: g.title_key && hasKey(g.title_key) ? tr(g.title_key, g.title_params) : g.title,
          nodes: d.nodes.filter((n) => n.parent_id === g.id && n.kind !== "subtask").map((n) => ({
            key: idKey(n.id), kind: n.kind, title_key: "", title: n.title_key && hasKey(n.title_key) ? tr(n.title_key, n.title_params) : n.title,
            agent: n.agent_id ?? undefined, complexity: n.complexity, deps: n.depends_on.filter((x) => !x.endsWith("~s")).map(idKey), secs: n.est_seconds,
            approval: n.approval_action ? { action: n.approval_action, risk: "medium" as const } : undefined,
          })),
        })),
      })),
      budget_hint: d.estimate ? { p50_usd: d.estimate.total.p50_usd, p90_usd: d.estimate.total.p90_usd, basis: "priors" } : undefined,
    };
    this.tpls.push(tpl);
    return tpl;
  }

  // ---- engine -----------------------------------------------------------------------------------
  private summary(mp: MockProject): ProjectSummary {
    const d = mp.detail; const p = d.project;
    const c = countStates(d.nodes);
    p.tasks_done = c.done; p.tasks_total = c.total; p.running = c.running; p.awaiting = c.awaiting; p.failed = c.failed;
    p.spent_usd = d.nodes.reduce((s, n) => s + n.cost_usd, 0);
    p.budget_warn_pct = p.budget_usd > 0 && p.spent_usd >= p.budget_usd * 0.8 ? 0.8 : undefined;
    p.light = p.status === "draft" || p.status === "planning" ? "green" : computeHealth(d).light;
    return { ...p };
  }

  private syncGroups(mp: MockProject) {
    const d = mp.detail;
    for (const g of d.nodes.filter(isGroup)) {
      const kids = d.nodes.filter((n) => n.parent_id === g.id);
      const st = d.project.status === "draft" ? "draft" : rollupState(kids);
      const ls = leaves(kids);
      const pr = ls.length ? Math.round((ls.filter((k) => isDoneState(k.state)).length / ls.length) * 100) : 0;
      if (g.state !== st || g.progress !== pr) { g.state = st; g.progress = pr; g.rev++; mp.dirty.add(g.id); }
    }
  }

  private resolve(mp: MockProject, a: ProjectApproval, decision: "approve" | "reject") {
    a.status = decision === "approve" ? "approved" : "rejected"; a.resolved_at = iso(); mp.approvalsDirty = true;
    const p = mp.detail.project;
    if (a.action === "extend_budget") {
      if (decision === "approve") { p.budget_usd = +(Math.max(p.budget_usd, mp.detail.nodes.reduce((x, n) => x + n.cost_usd, 0)) * 1.25).toFixed(4); p.control = "active"; p.status = "running"; mp.budgetAck = false; }
      else { p.control = "cancelled"; p.status = "cancelled"; p.finished_at = iso(); }
      return;
    }
    const n = mp.detail.nodes.find((x) => x.id === a.node_id);
    if (!n) return;
    if (decision === "approve") { mp.approved.add(n.id); n.state = "done"; n.progress = 100; n.finished_at = iso(); }
    else { n.state = "blocked"; n.error = "rejected"; }
    n.rev++; mp.dirty.add(n.id);
  }

  private raiseApproval(mp: MockProject, n: ProjectNode, action: string, risk: ProjectApproval["risk"]) {
    const a: ProjectApproval = {
      id: uid("papr"), project_id: n.project_id, node_id: n.id, agent_id: n.agent_id, action,
      title: tr(`pv.action.${action}`, { node: n.title_key && hasKey(n.title_key) ? tr(n.title_key, n.title_params) : n.title }), details: "", risk,
      status: "pending", created_at: iso(), resolved_at: null,
    };
    a.details = tr("pv.approval.details", { node: n.title_key && hasKey(n.title_key) ? tr(n.title_key, n.title_params) : n.title });
    mp.detail.approvals.push(a); mp.approvalsDirty = true;
    n.state = "awaiting_approval"; n.rev++; mp.dirty.add(n.id);
  }

  private tick() {
    for (const mp of this.projects.values()) {
      const d = mp.detail; const p = d.project;
      if (p.control !== "active" || (p.status !== "running" && p.status !== "waiting_human")) continue;
      this.step(mp);
      this.flush(mp, false);
    }
  }

  private step(mp: MockProject) {
    const d = mp.detail; const p = d.project;
    const nowIso = iso();
    const byId = new Map(d.nodes.map((n) => [n.id, n]));
    // 1) progress of running nodes and timers
    for (const n of d.nodes) {
      if (isGroup(n)) continue;
      const timer = n.kind === "wait" && n.state === "waiting";
      if (n.state !== "running" && !timer) continue;
      const f = mp.costFactor.get(n.id) ?? 1;
      if (n.state === "running") n.cost_usd += n.est_cost_usd * f * (DT / Math.max(1, n.est_seconds));
      n.progress = Math.min(100, n.progress + (DT / Math.max(1, n.est_seconds)) * 100);
      n.rev++; mp.dirty.add(n.id);
      if (n.progress < 100) continue;
      if (mp.failOnce.has(n.id) && n.attempt === 1) {
        mp.failOnce.delete(n.id); n.state = "ready"; n.progress = 0; n.error = "timeout"; continue;
      }
      if (n.approval_action && !mp.approved.has(n.id) && n.kind !== "gate") { this.raiseApproval(mp, n, n.approval_action, "medium"); continue; }
      n.state = "done"; n.finished_at = nowIso; n.error = null;
    }
    // 2) delegating parents resume when their subtasks are done
    for (const n of d.nodes) {
      if (n.state !== "waiting" || n.kind === "wait") continue;
      const subs = d.nodes.filter((s) => s.parent_id === n.id && s.kind === "subtask");
      if (subs.length && subs.every((s) => isDoneState(s.state))) { n.state = "ready"; mp.resumed.add(n.id); n.rev++; mp.dirty.add(n.id); }
    }
    // 3) dependency resolution
    for (const n of topoOrder(d.nodes)) {
      if (isGroup(n) || n.state !== "pending") continue;
      const deps = n.depends_on.map((x) => byId.get(x)).filter(Boolean) as ProjectNode[];
      if (deps.some((x) => x.state === "failed" || x.state === "blocked" || x.state === "cancelled")) { n.state = "blocked"; n.rev++; mp.dirty.add(n.id); }
      else if (deps.every((x) => isDoneState(x.state))) { n.state = "ready"; n.rev++; mp.dirty.add(n.id); }
    }
    // budget hold: pause and ask for more money instead of silently overspending
    const spent = d.nodes.reduce((s, n) => s + n.cost_usd, 0);
    if (p.budget_usd > 0 && spent >= p.budget_usd && !mp.budgetAck && !d.approvals.some((a) => a.action === "extend_budget" && a.status === "pending")) {
      p.control = "paused"; p.status = "waiting_human";
      const a: ProjectApproval = {
        id: uid("papr"), project_id: p.id, node_id: "", agent_id: null, action: "extend_budget", title: tr("pv.action.extend_budget"),
        details: tr("pv.approval.budgetDetails"), risk: "medium", status: "pending", created_at: nowIso, resolved_at: null,
      };
      d.approvals.push(a); mp.approvalsDirty = true; p.spent_usd = spent;
      return;
    }
    // 4) claim ready nodes (per-agent concurrency, project parallelism)
    const running = d.nodes.filter((n) => !isGroup(n) && n.state === "running");
    const perAgent = new Map<string, number>();
    for (const n of running) if (n.agent_id) perAgent.set(n.agent_id, (perAgent.get(n.agent_id) ?? 0) + 1);
    let total = running.length;
    for (const n of topoOrder(d.nodes.filter((x) => !isGroup(x) && x.state === "ready"))) {
      if (n.kind === "gate") { if (!d.approvals.some((a) => a.node_id === n.id)) this.raiseApproval(mp, n, n.approval_action ?? "approve_plan", "high"); continue; }
      if (n.kind === "milestone") { n.state = "done"; n.progress = 100; n.finished_at = nowIso; n.rev++; mp.dirty.add(n.id); continue; }
      if (n.kind === "wait") { n.state = "waiting"; n.started_at = nowIso; n.rev++; mp.dirty.add(n.id); continue; }
      const a = n.agent_id ?? "";
      if (total >= d.max_parallel || (perAgent.get(a) ?? 0) >= AGENT_CAPACITY) continue;
      const resumed = mp.resumed.has(n.id);
      mp.resumed.delete(n.id);
      const del = mp.delegates.get(n.id);
      if (del && !resumed && !d.nodes.some((s) => s.parent_id === n.id)) {
        // fork/join: the agent delegates a subtask (depth + 1) and waits for it
        const sub = this.mkNode(p.id, `${n.id}~s`, n.objective_id, n.id, "subtask", tr(del.title_key), del.title_key, {}, `${n.wbs_path}.0001`);
        sub.agent_id = del.agent; sub.complexity = del.complexity ?? "S"; sub.est_seconds = priorSeconds(sub.complexity);
        sub.est_cost_usd = priorCost(sub.complexity, 0) * 0.5; sub.state = "ready"; sub.delegation_depth = n.delegation_depth + 1;
        sub.delegation_chain = [...n.delegation_chain, del.agent]; sub.plan_start_ms = null;
        mp.costFactor.set(sub.id, 1);
        d.nodes.push(sub); byId.set(sub.id, sub);
        n.depends_on = [...n.depends_on, sub.id];
        computeLevels(d.nodes); d.structure_version++;
        n.state = "waiting"; n.started_at = n.started_at ?? nowIso; n.attempt = Math.max(1, n.attempt + 1); n.rev++;
        for (const x of d.nodes) mp.dirty.add(x.id);
        continue;
      }
      n.state = "running"; if (!resumed) n.attempt++; n.started_at = n.started_at ?? nowIso; n.rev++; mp.dirty.add(n.id);
      perAgent.set(a, (perAgent.get(a) ?? 0) + 1); total++;
    }
    this.syncGroups(mp);
    // 5) completion / stall detection
    const ls = leaves(d.nodes);
    if (ls.every((n) => isDoneState(n.state))) {
      p.status = "done"; p.finished_at = nowIso; mp.statusDirty = true;
    } else {
      const active = ls.some((n) => n.state === "running" || n.state === "ready" || n.state === "waiting" || n.state === "pending");
      const awaiting = d.approvals.some((a) => a.status === "pending");
      if (!active && !awaiting) { p.status = "failed"; p.finished_at = nowIso; mp.statusDirty = true; }
      else {
        const stuckOnHuman = !ls.some((n) => n.state === "running" || n.state === "ready" || (n.state === "waiting")) && awaiting;
        const next = stuckOnHuman ? "waiting_human" : "running";
        if (p.status !== next) { p.status = next; mp.statusDirty = true; }
      }
    }
  }

  private flush(mp: MockProject, force: boolean) {
    const d = mp.detail;
    if (!force && !mp.dirty.size && !mp.approvalsDirty && !mp.statusDirty) return;
    this.syncGroups(mp);
    const nodes = Array.from(mp.dirty).map((id) => d.nodes.find((n) => n.id === id)).filter(Boolean).map((n) => ({ ...n! }));
    const payload: ProjectFrame["payload"] = { project: this.summary(mp), nodes, structure_version: d.structure_version };
    if (mp.approvalsDirty) payload.approvals = d.approvals.map((a) => ({ ...a }));
    this.emit({ type: mp.statusDirty || force ? "project.status_changed" : "project.delta", payload });
    mp.dirty.clear(); mp.approvalsDirty = false; mp.statusDirty = false;
  }

  /** Test helper (dev tools): is anything retrying? */
  retrying(id: string) { return this.projects.get(id)?.detail.nodes.filter(isRetrying).length ?? 0; }
}

export const projectsMock = new ProjectsMock();
