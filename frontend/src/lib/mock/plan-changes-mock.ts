import type { PlanChange, PlanChangeOp, ProjectDetail } from "../projects/types";

/**
 * Plan changes of the demo (NEXT_PUBLIC_MOCK=true): GET/POST /projects/{id}/plan-changes and their decision.
 * Once a demo project is under way (launched: no longer a draft or planning), one of its agents proposes a change
 * that waits for a person, so Seguimiento shows the notice "Hay 1 cambio de plan por revisar" and Plan → Historial
 * de cambios has something to decide. The demo records the decision; it does not redraw the plan.
 * Texts come through `tx` (the i18n `tr` in the app, any function in the tests), so this file imports no catalogs.
 */
export type Tx = (key: string, vars?: Record<string, string | number>) => string;

const NOT_UNDER_WAY = new Set(["draft", "planning"]);
let counter = 0;
const nextId = () => `chg-${Date.now().toString(36)}${(counter++).toString(36)}`;

/** The node the agent's proposal starts from: in the financial close, "Consolidar costos y gastos"; otherwise the first task with an agent. */
function anchor(d: ProjectDetail) {
  const tasks = d.nodes.filter((n) => n.kind !== "group" && n.agent_id);
  return tasks.find((n) => n.id.endsWith(":b2")) ?? tasks[0] ?? null;
}

/** The pending change an agent proposes in a launched demo project (null for drafts or a plan without agents). */
export function seedPlanChange(d: ProjectDetail, tx: Tx, now = Date.now()): PlanChange | null {
  const p = d.project;
  if (NOT_UNDER_WAY.has(p.status)) return null;
  const from = anchor(d);
  if (!from) return null;
  const fc = p.template_id === "tpl-financial-close";
  // the new task costs about what the task it comes from costs (a small step, inside the demo budget)
  const cost = Math.round((from.est_cost_usd || 0.002) * 1000) / 1000;
  const secs = Math.round(from.est_seconds || 40);
  const spent = p.spent_usd || 0;
  const planned = d.estimate?.total.p50_usd ?? spent;
  const projected = Math.round((Math.max(planned, spent) + cost) * 1000) / 1000;
  return {
    id: nextId(), project_id: p.id, proposed_by: "agent", actor: from.agent_id!, source: "suggested_tasks", source_node_id: from.id,
    reason: fc ? tx("mock.planChange.fc.reason") : tx("mock.planChange.reason", { task: from.title }),
    ops: [{ op: "add_task", tasks: [{ key: "n1", title: fc ? tx("mock.planChange.fc.task") : tx("mock.planChange.task", { task: from.title }), agent_id: from.agent_id!, complexity: "S", depends_on: [from.id] }] }],
    status: "pending",
    impact: { added: 1, replaced: 0, removed: 0, updated: 0, est_cost_delta_usd: cost, est_seconds_delta: secs, projected_cost_usd: projected, budget_usd: p.budget_usd, exceeds_budget: p.budget_usd > 0 && projected > p.budget_usd, nodes_after: d.nodes.length + 1 },
    risk: "low", required_approvals: 1, approvals: 0, created_at: new Date(now - 6 * 60_000).toISOString(),
  };
}

export class MockPlanChanges {
  private byProject = new Map<string, PlanChange[]>();
  private seeded = new Set<string>();
  constructor(private tx: Tx) {}

  /** GET: the first time a launched project is asked for, its agent's proposal appears (once per project). */
  list(d: ProjectDetail): PlanChange[] {
    const id = d.project.id;
    if (!this.seeded.has(id)) {
      const c = seedPlanChange(d, this.tx);
      if (c) { this.seeded.add(id); this.byProject.set(id, [c, ...(this.byProject.get(id) ?? [])]); }
    }
    return (this.byProject.get(id) ?? []).map((c) => ({ ...c }));
  }

  /** POST /plan-changes from the project ("Agregar una tarea al plan en curso"): recorded as approved by the person. */
  propose(d: ProjectDetail, reason: string, ops: PlanChangeOp[]): PlanChange {
    const added = ops.reduce((n, o) => n + (o.op === "add_task" ? o.tasks?.length ?? 0 : 0), 0);
    const c: PlanChange = {
      id: nextId(), project_id: d.project.id, proposed_by: "human", actor: this.tx("mock.planChange.you"), source: "manual", reason, ops,
      status: "approved",
      impact: { added, replaced: 0, removed: 0, updated: 0, est_cost_delta_usd: 0.03 * added, est_seconds_delta: 30 * added, projected_cost_usd: d.project.spent_usd, budget_usd: d.project.budget_usd, exceeds_budget: false, nodes_after: d.nodes.length + added },
      risk: "low", decided_by: this.tx("mock.planChange.you"), created_at: new Date().toISOString(),
    };
    this.byProject.set(d.project.id, [c, ...(this.byProject.get(d.project.id) ?? [])]);
    return { ...c };
  }

  /** POST /plan-changes/{id}/approve|reject. */
  decide(projectId: string, changeId: string, decision: "approve" | "reject"): PlanChange {
    const c = (this.byProject.get(projectId) ?? []).find((x) => x.id === changeId);
    if (!c || c.status !== "pending") throw new Error("plan change not pending");
    c.status = decision === "approve" ? "approved" : "rejected";
    c.approvals = decision === "approve" ? 1 : 0;
    c.decided_by = this.tx("mock.planChange.you");
    return { ...c };
  }
}
