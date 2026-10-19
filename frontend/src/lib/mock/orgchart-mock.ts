/**
 * Mock of the corporate organization model (NEXT_PUBLIC_MOCK=true): departments, org chart, job descriptions,
 * delegation rules and two industry templates. Same shapes as backend/internal/api/orgchart.go. Spanish only.
 */
import type { Agent } from "../types";
import type { Chart, ChartDept, ChartMember, Decision, Department, Industry, Job, Rule } from "../orgChartApi";
import { isNoRef, isOpenAt, refKey, type Ref } from "../orgChartLogic";

const PEOPLE = [{ id: "owner", name: "Propietario", title: "Dirección" }];
const NO: Ref = { kind: "", id: "" };

const INDUSTRIES: Industry[] = [
  {
    key: "accounting_firm", version: 1, category: "professional", name: "Despacho contable", subsidiaries: 0, demo_set: "accounting_firm", locale: "es",
    description: "Impuestos, contabilidad y nómina para tus clientes, con un equipo de agentes y horarios de oficina.",
    disclaimer: "Los agentes asisten: un profesional con licencia debe revisar los entregables fiscales.",
    departments: [
      { key: "practice", name: "Dirección del despacho", parent: "", cost_center: "CC-100", head: "" },
      { key: "tax", name: "Impuestos", parent: "practice", cost_center: "CC-110", head: "tax_lead" },
      { key: "books", name: "Contabilidad", parent: "practice", cost_center: "CC-120", head: "" },
    ],
    agents: [
      { key: "tax_lead", role: "accounting", department: "tax", name: "Marta", title: "Responsable fiscal", responsibilities: ["Preparar declaraciones", "Revisar vencimientos"], tools: ["fs", "ledger"], reports_to: "owner", escalation: "owner", schedule: { timezone: "America/Mexico_City", days: [1, 2, 3, 4, 5], start: "09:00", end: "18:00" }, budget_usd: 40, autonomy: "approve_each" },
      { key: "bookkeeper", role: "accounting", department: "books", name: "Iván", title: "Contador de cierres", responsibilities: ["Conciliar bancos", "Cerrar el mes"], tools: ["fs", "spreadsheet"], reports_to: "tax_lead", escalation: "tax_lead", schedule: { timezone: "America/Mexico_City", days: [1, 2, 3, 4, 5], start: "08:00", end: "16:00" }, budget_usd: 30, autonomy: "approve_each" },
    ],
    delegation: [{ from: "any_agent", to: "owner", effect: "allow", approval: true, approver: "", note: "Los agentes piden aprobación para asignar trabajo al propietario" }],
    projects: [{ key: "close", name: "Cierre mensual de un cliente", description: "Concilia y entrega el cierre.", steps: [
      { key: "s1", title: "Conciliar bancos", description: "", agent: "bookkeeper", depends_on: [], stage: 1 },
      { key: "s2", title: "Revisar impuestos del mes", description: "", agent: "tax_lead", depends_on: ["s1"], stage: 2 },
    ] }],
  },
  {
    key: "multinational_group", version: 1, category: "group", name: "Grupo multinacional (3 subsidiarias)", subsidiaries: 3, demo_set: "multinational_group", locale: "es",
    description: "Casa matriz con subsidiarias en México, Brasil y España, cada una con su contador local.",
    disclaimer: "Los agentes asisten: valida la consolidación con tu auditor.",
    departments: [
      { key: "hq", name: "Casa matriz", parent: "", cost_center: "CC-000", head: "" },
      { key: "mx", name: "Subsidiaria México", parent: "hq", cost_center: "CC-MX", head: "mx_controller" },
      { key: "br", name: "Subsidiaria Brasil", parent: "hq", cost_center: "CC-BR", head: "" },
      { key: "es", name: "Subsidiaria España", parent: "hq", cost_center: "CC-ES", head: "" },
    ],
    agents: [
      { key: "mx_controller", role: "accounting", department: "mx", name: "Rosa", title: "Contralora México", responsibilities: ["Cierre local"], tools: ["fs", "ledger"], reports_to: "owner", escalation: "owner", schedule: { timezone: "America/Mexico_City", days: [1, 2, 3, 4, 5], start: "09:00", end: "18:00" }, budget_usd: 35, autonomy: "approve_each" },
      { key: "es_controller", role: "accounting", department: "es", name: "Pablo", title: "Contralor España", responsibilities: ["Cierre local"], tools: ["fs", "ledger"], reports_to: "mx_controller", escalation: "owner", schedule: { timezone: "Europe/Madrid", days: [1, 2, 3, 4, 5], start: "09:00", end: "18:00" }, budget_usd: 35, autonomy: "approve_each" },
    ],
    delegation: [], projects: [{ key: "group_close", name: "Cierre del grupo", description: "Consolida las subsidiarias.", steps: [
      { key: "s1", title: "Cierres locales", description: "", agent: "mx_controller", depends_on: [], stage: 1 },
      { key: "s2", title: "Consolidar", description: "", agent: "es_controller", depends_on: ["s1"], stage: 2 },
    ] }],
  },
];

const uid = (p: string) => `${p}_${Math.random().toString(36).slice(2, 10)}`;

export class MockOrgChart {
  private deps: Department[] = [];
  private jobs: Record<string, Job> = {};
  private rules: Rule[] = [];
  private placements: Record<string, { department_id: string; title: string; reports_to: Ref }> = {};
  private applied: { key: string; version: number; locale: string; applied_by: string; applied_at: string }[] = [];
  constructor(private agents: () => Agent[]) {}

  matches(seg: string[]): boolean {
    const [r0, r1, r2] = seg;
    return (r0 === "org" && r1 !== "settings") || (r0 === "agents" && (r2 === "job" || r2 === "availability"));
  }

  private jobOf(a: Agent): Job {
    return this.jobs[a.id] ?? { agent_id: a.id, department_id: "", reports_to: NO, escalation: NO, responsibilities: [], allowed_tools: [...a.tools],
      schedule: { enabled: false, timezone: "UTC", days: [1, 2, 3, 4, 5], start: "09:00", end: "18:00" }, budget_usd: null, updated_by: "", updated_at: new Date().toISOString() };
  }

  private chart(): Chart {
    const now = new Date();
    const names = new Map<string, string>([["person:owner", PEOPLE[0].name]]);
    this.agents().forEach((a) => names.set(`agent:${a.id}`, a.name));
    const member = (m: Omit<ChartMember, "reports_to_name" | "head">, deptId: string): ChartMember => ({
      ...m, head: this.deps.some((d) => d.id === deptId && refKey(d.head) === `${m.kind}:${m.id}`), reports_to_name: isNoRef(m.reports_to) ? "" : names.get(refKey(m.reports_to)) ?? "",
    });
    const byDept = new Map<string, ChartMember[]>();
    const unassigned: ChartMember[] = [];
    const put = (d: string, m: ChartMember) => (d && this.deps.some((x) => x.id === d) ? byDept.set(d, [...(byDept.get(d) ?? []), m]) : unassigned.push(m));
    for (const a of this.agents()) {
      const j = this.jobOf(a);
      const open = j.schedule.enabled ? isOpenAt(j.schedule, now) : undefined;
      put(j.department_id, member({ kind: "agent", id: a.id, name: a.name, title: a.title, role: a.role, reports_to: j.reports_to, escalation: j.escalation, state: a.state,
        ...(open !== undefined ? { open } : {}), ...(j.budget_usd != null ? { budget_usd: j.budget_usd } : {}) }, j.department_id));
    }
    for (const p of PEOPLE) {
      const pl = this.placements[p.id];
      put(pl?.department_id ?? "", member({ kind: "person", id: p.id, name: p.name, title: pl?.title ?? p.title, role: "owner", reports_to: pl?.reports_to ?? NO, escalation: NO }, pl?.department_id ?? ""));
    }
    const build = (d: Department): ChartDept => {
      const members = byDept.get(d.id) ?? [];
      return { ...d, head_name: isNoRef(d.head) ? "" : names.get(refKey(d.head)) ?? "", members, people: members.filter((m) => m.kind === "person").length,
        agents: members.filter((m) => m.kind === "agent").length, children: this.deps.filter((c) => c.parent_id === d.id).map(build) };
    };
    return { roots: this.deps.filter((d) => !d.parent_id || !this.deps.some((x) => x.id === d.parent_id)).map(build), unassigned, people: PEOPLE.length, agents: this.agents().length,
      applied: this.applied, now: now.toISOString() };
  }


  async handle(method: string, seg: string[], body: any, q: URLSearchParams): Promise<unknown> {
    const [r0, r1, r2, r3] = seg;
    if (r0 === "agents") {
      const a = this.agents().find((x) => x.id === r1);
      if (!a) throw new Error(`${method} -> 404`);
      if (r2 === "availability") {
        const j = this.jobOf(a);
        const open = isOpenAt(j.schedule, new Date(q.get("at") ?? Date.now()));
        return { open, ...(j.schedule.enabled ? { timezone: j.schedule.timezone } : {}) };
      }
      if (method === "PUT") {
        this.jobs[a.id] = { ...this.jobOf(a), ...body, agent_id: a.id, updated_at: new Date().toISOString() };
        const dep = this.deps.find((d) => d.id === body.department_id);
        (a as Agent & { department?: string | null }).department = dep?.name ?? null;
      }
      return this.jobOf(a);
    }
    if (r1 === "chart") return this.chart();
    if (r1 === "departments") {
      if (method === "GET") return { items: this.deps };
      if (method === "DELETE") {
        if (this.deps.some((d) => d.parent_id === r2)) throw new Error("DELETE -> 409");
        this.deps = this.deps.filter((d) => d.id !== r2);
        Object.values(this.jobs).forEach((j) => { if (j.department_id === r2) j.department_id = ""; });
        return {};
      }
      const name = String(body?.name ?? "").trim();
      if (!name || this.deps.some((d) => d.id !== r2 && d.name.toLowerCase() === name.toLowerCase())) throw new Error(`${method} -> 400`);
      const now = new Date().toISOString();
      if (method === "POST") { const d: Department = { id: uid("dept"), name, parent_id: body.parent_id ?? "", head: body.head ?? NO, cost_center: body.cost_center ?? "", created_at: now, updated_at: now }; this.deps.push(d); return d; }
      const cur = this.deps.find((d) => d.id === r2);
      if (!cur) throw new Error("PUT -> 404");
      Object.assign(cur, { name, parent_id: body.parent_id ?? "", head: body.head ?? NO, cost_center: body.cost_center ?? "", updated_at: now });
      return cur;
    }
    if (r1 === "jobs") return { items: Object.values(this.jobs) };
    if (r1 === "people") { this.placements[r2] = body; return { user_id: r2, ...body }; }
    if (r1 === "delegation" && r2 === "rules") {
      if (method === "GET") return { items: this.rules };
      if (method === "DELETE") { this.rules = this.rules.filter((r) => r.id !== r3); return {}; }
      const r: Rule = { id: uid("rule"), ...body, created_by: "user", created_at: new Date().toISOString() };
      this.rules.push(r);
      return r;
    }
    if (r1 === "delegation" && r2 === "check") {
      const d: Decision = { allowed: true, needs_approval: false, approver: NO, rule_id: "", reason: "no_rule" };
      const hit = this.rules.find((r) => r.to.type === "any" || (r.to.type === "agent" && body?.to?.id === r.to.id));
      if (hit) Object.assign(d, hit.effect === "deny" ? { allowed: false, rule_id: hit.id, reason: "denied_by_rule" } : { rule_id: hit.id, reason: hit.require_approval ? "approval_required" : "allowed_by_rule", needs_approval: hit.require_approval });
      return d;
    }
    if (r1 === "industries" && !r2) return { items: INDUSTRIES };
    if (r1 === "industries" && r3 === "apply") {
      const ind = INDUSTRIES.find((i) => i.key === r2);
      if (!ind) throw new Error("POST -> 404");
      const already = this.applied.some((a) => a.key === ind.key);
      const created: string[] = [];
      const deptId = (k: string) => `ind_${ind.key}_${k}`;
      for (const d of ind.departments) {
        if (this.deps.some((x) => x.id === deptId(d.key))) continue;
        const now = new Date().toISOString();
        this.deps.push({ id: deptId(d.key), name: d.name, parent_id: d.parent ? deptId(d.parent) : "", head: NO, cost_center: d.cost_center, created_at: now, updated_at: now });
      }
      const ref = (k: string): Ref => (k === "owner" ? { kind: "person", id: "owner" } : k ? { kind: "agent", id: `${ind.key.split("_")[0]}_${k}` } : NO);
      for (const a of ind.agents) {
        const id = `${ind.key.split("_")[0]}_${a.key}`;
        if (this.agents().some((x) => x.id === id)) continue;
        this.agents().push({ id, name: a.name, role: a.role, title: a.title, description: "", state: "idle", activity: "Disponible", current_task_id: null, progress: 0, tools: a.tools, permissions: [],
          autonomy: "approve_each", metrics: { tasks_done: 0, success_rate: 0, avg_task_secs: 0, cost_usd: 0 } } as unknown as Agent);
        this.jobs[id] = { agent_id: id, department_id: deptId(a.department), reports_to: ref(a.reports_to), escalation: ref(a.escalation), responsibilities: a.responsibilities, allowed_tools: a.tools,
          schedule: { enabled: true, ...a.schedule }, budget_usd: a.budget_usd, updated_by: "user", updated_at: new Date().toISOString() };
        created.push(id);
      }
      for (const d of ind.departments) if (d.head) { const dep = this.deps.find((x) => x.id === deptId(d.key)); if (dep && isNoRef(dep.head)) dep.head = ref(d.head); }
      this.applied = [...this.applied.filter((a) => a.key !== ind.key), { key: ind.key, version: 1, locale: "es", applied_by: "user", applied_at: new Date().toISOString() }];
      return { result: { key: ind.key, departments: ind.departments.map((d) => deptId(d.key)), agents: created, agents_existing: [], rules: ind.delegation.length, project_templates: body?.skip_projects ? [] : ind.projects.map((p) => p.key),
        already_applied: already, demo_set: ind.demo_set, locale: "es" }, demo_files: body?.demo_data ? ["/Shared/demo-" + ind.key + "/README.md"] : [] };
    }
    throw new Error(`${method} /${seg.join("/")} -> 404`);
  }
}
