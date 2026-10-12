/**
 * Projects API layer. In mock mode it talks to ./mock; otherwise it calls the REST endpoints of the spec
 * (docs/architecture/workflow-visualization.md sec. 7.4). The backend does not implement them yet: the missing
 * endpoints are listed in that document ("Frontend delivery status").
 */
import { API_URL, MOCK } from "../config";
import { authFetch } from "../session";
import type {
  ControlAction, LaunchBody, NewProjectBody, PlanChange, PlanChangeOp, PlanOp, ProjectDetail, ProjectEstimate, ProjectHealth, ProjectSummary, ProjectTemplate,
  SaveTemplateOpts, TemplateInput, TemplatePreview, TemplatePreviewRequest, TemplateValidation, TemplateVersionInfo,
} from "./types";

/** HTTP error that keeps the server message ({"error": "..."}), where limits carry "limit_exceeded: <code>". */
export class ApiError extends Error {
  constructor(message: string, public status: number, public detail: string) { super(message); }
}

/** "max_nodes_per_project" | "max_children_per_group" | "max_active_projects" | null */
export function limitCode(err: unknown): string | null {
  const m = err instanceof ApiError ? /limit_exceeded: (\w+)/.exec(err.detail) : null;
  return m ? m[1] : null;
}

/** HTTP status of a failed call (ApiError or the mock's equivalent), 0 when unknown */
export const httpStatus = (err: unknown): number => (typeof (err as { status?: unknown })?.status === "number" ? (err as { status: number }).status : 0);
/** server message of a failed call */
export const errorDetail = (err: unknown): string => String((err as { detail?: unknown })?.detail ?? "");

async function http<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await authFetch(`${API_URL}${path}`, {
    method, cache: "no-store",
    headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) {
    let detail = "";
    try { detail = String(JSON.parse(await res.text())?.error ?? ""); } catch { /* not JSON */ }
    throw new ApiError(`${method} ${path} -> ${res.status}`, res.status, detail);
  }
  const text = await res.text();
  return (text ? JSON.parse(text) : {}) as T;
}
const items = <T,>(x: any): T[] => (Array.isArray(x) ? x : Array.isArray(x?.items) ? x.items : []);
const mock = async () => (await import("./mock")).projectsMock;

export const projectsApi = {
  list: async (): Promise<ProjectSummary[]> => (MOCK ? (await mock()).list() : items<ProjectSummary>(await http("GET", "/projects"))),
  /** Mock: full detail. Real backend: GET /projects/{id}/snapshot?depth=full (header + objectives + nodes + approvals). */
  get: async (id: string): Promise<ProjectDetail> => (MOCK ? (await mock()).get(id) : http("GET", `/projects/${id}/snapshot?depth=full`)),
  health: async (id: string): Promise<ProjectHealth | null> => (MOCK ? (await mock()).health(id) : http("GET", `/projects/${id}/health`)),
  createDraft: async (b: NewProjectBody): Promise<{ project_id: string }> => (MOCK ? (await mock()).createDraft(b) : http("POST", "/projects/draft", b)),
  patchPlan: async (id: string, ops: PlanOp[]) => (MOCK ? (await mock()).patchPlan(id, ops) : http("PATCH", `/projects/${id}/plan`, { ops })),
  estimate: async (id: string): Promise<{ estimate: ProjectEstimate }> => (MOCK ? { estimate: (await mock().then((m) => m.get(id))).estimate! } : http("POST", `/projects/${id}/estimate`)),
  launch: async (id: string, b: LaunchBody) => (MOCK ? (await mock()).launch(id, b) : http("POST", `/projects/${id}/launch`, b)),
  control: async (id: string, action: ControlAction) => (MOCK ? (await mock()).control(id, action) : http("POST", `/projects/${id}/control`, { action })),
  retryNode: async (id: string, nodeId: string) => (MOCK ? (await mock()).retryNode(id, nodeId) : http("POST", `/projects/${id}/nodes/${nodeId}/retry`)),
  skipNode: async (id: string, nodeId: string, reason: string) => (MOCK ? (await mock()).skipNode(id, nodeId, reason) : http("POST", `/projects/${id}/nodes/${nodeId}/skip`, { reason })),
  /** Q2 plan changes: agents only propose; a human decides. */
  listChanges: async (id: string): Promise<PlanChange[]> => (MOCK ? [] : items<PlanChange>(await http("GET", `/projects/${id}/plan-changes`))),
  proposeChange: async (id: string, reason: string, ops: PlanChangeOp[]): Promise<PlanChange | null> => (MOCK ? null : http("POST", `/projects/${id}/plan-changes`, { reason, ops })),
  decideChange: async (id: string, changeId: string, decision: "approve" | "reject"): Promise<PlanChange | null> => (MOCK ? null : http("POST", `/projects/${id}/plan-changes/${changeId}/${decision}`, {})),
  replanNode: async (id: string, nodeId: string): Promise<PlanChange | null> => (MOCK ? null : http("POST", `/projects/${id}/nodes/${nodeId}/replan`)),
  setBudget: async (id: string, usd: number) => (MOCK ? (await mock()).setBudget(id, usd) : http("PUT", `/projects/${id}/budget`, { budget_usd: usd })),
  decide: async (pid: string, approvalId: string, decision: "approve" | "reject") =>
    MOCK ? (await mock()).decide(pid, approvalId, decision) : http("POST", `/approvals/${approvalId}/decision`, { decision }),
  decideBatch: async (pid: string, b: { action: string; decision: "approve" | "reject"; expected_count: number; include_high?: boolean }) =>
    MOCK ? (await mock()).decideBatch(pid, b) : http("POST", "/approvals/batch", { filter: { project_id: pid, action: b.action }, decision: b.decision, expected_count: b.expected_count, include_high: b.include_high }),
  templates: async (): Promise<ProjectTemplate[]> => (MOCK ? (await mock()).templates() : items<ProjectTemplate>(await http("GET", "/project-templates"))),
  saveAsTemplate: async (id: string, opts?: SaveTemplateOpts): Promise<ProjectTemplate> => (MOCK ? (await mock()).saveAsTemplate(id, opts) : http("POST", `/projects/${id}/save-as-template`, opts ?? {})),
  // ---- customizable templates (library, builder, versions) ----
  getTemplate: async (id: string): Promise<ProjectTemplate> => (MOCK ? (await mock()).getTemplate(id) : http("GET", `/project-templates/${id}`)),
  createTemplate: async (b: TemplateInput): Promise<ProjectTemplate> => (MOCK ? (await mock()).createTemplate(b) : http("POST", "/project-templates", b)),
  updateTemplate: async (id: string, b: TemplateInput): Promise<ProjectTemplate> => (MOCK ? (await mock()).updateTemplate(id, b) : http("PUT", `/project-templates/${id}`, b)),
  deleteTemplate: async (id: string): Promise<void> => { if (MOCK) await (await mock()).deleteTemplate(id); else await http("DELETE", `/project-templates/${id}`); },
  duplicateTemplate: async (id: string, name?: string): Promise<ProjectTemplate> => (MOCK ? (await mock()).duplicateTemplate(id, name) : http("POST", `/project-templates/${id}/duplicate`, name ? { name } : {})),
  templateVersions: async (id: string): Promise<TemplateVersionInfo[]> => (MOCK ? (await mock()).templateVersions(id) : items<TemplateVersionInfo>(await http("GET", `/project-templates/${id}/versions`))),
  restoreTemplate: async (id: string, version: number): Promise<ProjectTemplate> => (MOCK ? (await mock()).restoreTemplate(id, version) : http("POST", `/project-templates/${id}/versions/${version}/restore`, {})),
  validateTemplate: async (b: TemplateInput): Promise<TemplateValidation> => (MOCK ? (await mock()).validateTemplate(b) : http("POST", "/project-templates/validate", b)),
  previewTemplate: async (r: TemplatePreviewRequest): Promise<TemplatePreview> => (MOCK ? (await mock()).previewTemplate(r) : http("POST", "/project-templates/preview", r)),
};
