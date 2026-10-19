import { call } from "./api";
import type { Locale } from "./i18n-core";
import type { Ref, ScheduleLite, SelectorLite } from "./orgChartLogic";

/** Contract: backend/internal/api/orgchart.go and docs/architecture/organization.md (additive, /api/v1). */

export type Schedule = ScheduleLite;

export interface Department {
  id: string; name: string; parent_id: string; head: Ref; cost_center: string; created_at: string; updated_at: string;
}
export interface DepartmentInput { name: string; parent_id: string; head: Ref; cost_center: string }

export interface Job {
  agent_id: string; department_id: string; reports_to: Ref; escalation: Ref;
  responsibilities: string[]; allowed_tools: string[]; schedule: Schedule;
  /** monthly cap in USD; null = the organization default */
  budget_usd: number | null; updated_by: string; updated_at: string;
}
export interface JobInput {
  department_id: string; reports_to: Ref; escalation: Ref; responsibilities: string[]; allowed_tools: string[]; schedule: Schedule; budget_usd: number | null;
}

export interface Availability { open: boolean; next_open?: string; timezone?: string }

export interface ChartMember {
  kind: "person" | "agent"; id: string; name: string; title: string; role: string; head: boolean;
  reports_to: Ref; reports_to_name: string; escalation: Ref;
  state?: string; open?: boolean; next_open?: string; budget_usd?: number;
}
export interface ChartDept extends Department {
  head_name: string; members: ChartMember[]; children: ChartDept[]; people: number; agents: number;
}
export interface AppliedIndustry { key: string; version: number; locale: string; applied_by: string; applied_at: string }
export interface Chart { roots: ChartDept[]; unassigned: ChartMember[]; people: number; agents: number; applied: AppliedIndustry[]; now: string }

export interface PlacementInput { department_id: string; title: string; reports_to: Ref }

export interface Rule {
  id: string; from: SelectorLite; to: SelectorLite; effect: "allow" | "deny"; require_approval: boolean; approver: Ref; note: string; created_by: string; created_at: string;
}
export interface RuleInput { from: SelectorLite; to: SelectorLite; effect: "allow" | "deny"; require_approval: boolean; approver: Ref; note: string }
export interface Decision { allowed: boolean; needs_approval: boolean; approver: Ref; rule_id: string; reason: string }

export interface IndustryDept { key: string; name: string; parent: string; cost_center: string; head: string }
export interface IndustryAgent {
  key: string; role: string; department: string; name: string; title: string; responsibilities: string[]; tools: string[];
  reports_to: string; escalation: string; schedule: { timezone: string; days: number[]; start: string; end: string }; budget_usd: number; autonomy: string;
}
export interface IndustryStep { key: string; title: string; description: string; agent: string; depends_on: string[]; stage: number }
export interface IndustryProject { key: string; name: string; description: string; steps: IndustryStep[] }
export interface IndustryRule { from: string; to: string; effect: string; approval: boolean; approver: string; note: string }
export interface Industry {
  key: string; version: number; category: string; name: string; description: string; disclaimer: string; subsidiaries: number; demo_set: string; locale: string;
  departments: IndustryDept[]; agents: IndustryAgent[]; delegation: IndustryRule[]; projects: IndustryProject[];
}
export interface ApplyResult {
  key: string; departments: string[]; agents: string[]; agents_existing: string[]; rules: number; project_templates: string[];
  already_applied: boolean; demo_set: string; locale: string;
}
export interface ApplyResponse { result: ApplyResult; demo_files: string[]; demo_error?: string }

const items = <T,>(x: unknown): T[] => (Array.isArray((x as { items?: unknown })?.items) ? ((x as { items: T[] }).items) : []);
const enc = encodeURIComponent;

export const orgChartApi = {
  chart: () => call<Chart>("GET", "/org/chart"),
  departments: async () => items<Department>(await call("GET", "/org/departments")),
  createDepartment: (b: DepartmentInput) => call<Department>("POST", "/org/departments", b),
  updateDepartment: (id: string, b: DepartmentInput) => call<Department>("PUT", `/org/departments/${enc(id)}`, b),
  deleteDepartment: (id: string) => call<unknown>("DELETE", `/org/departments/${enc(id)}`),
  job: (agentId: string) => call<Job>("GET", `/agents/${enc(agentId)}/job`),
  putJob: (agentId: string, b: JobInput) => call<Job>("PUT", `/agents/${enc(agentId)}/job`, b),
  availability: (agentId: string, at?: string) => call<Availability>("GET", `/agents/${enc(agentId)}/availability${at ? `?at=${enc(at)}` : ""}`),
  placePerson: (userId: string, b: PlacementInput) => call<unknown>("PUT", `/org/people/${enc(userId)}`, b),
  rules: async () => items<Rule>(await call("GET", "/org/delegation/rules")),
  createRule: (b: RuleInput) => call<Rule>("POST", "/org/delegation/rules", b),
  deleteRule: (id: string) => call<unknown>("DELETE", `/org/delegation/rules/${enc(id)}`),
  check: (from: Ref, to: Ref) => call<Decision>("POST", "/org/delegation/check", { from, to }),
  industries: async (locale?: Locale) => items<Industry>(await call("GET", `/org/industries${locale ? `?locale=${locale}` : ""}`)),
  applyIndustry: (key: string, b: { locale?: Locale; skip_projects?: boolean; demo_data?: boolean }) =>
    call<ApplyResponse>("POST", `/org/industries/${enc(key)}/apply`, b),
};
