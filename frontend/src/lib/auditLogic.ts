/** Internal auditor stamp (B7, docs/architecture/internal-auditor.md). Pure helpers: no React, no store.
 * The stamp only repeats what the auditor declared in output.metrics.audit_status; the UI never infers a verdict. */

export type AuditStatus = "verified" | "inconsistency" | "unverified";
export type RequestAuditStatus = AuditStatus | "none";

/** One finished audit (GET /requests/{id}/audit item, and ProjectNode.audit). */
export interface TaskAudit {
  task_id: string; title: string; agent_id: string; status: AuditStatus; summary: string;
  findings: string[]; evidence: string[]; compared: number; inconsistencies: number; confidence: number; finished_at?: string;
}
export interface RequestAudit { request_id: string; status: RequestAuditStatus; audits: TaskAudit[] }

interface OutputLike { summary?: string; findings?: string[]; evidence?: string[]; confidence?: number; metrics?: Record<string, unknown> }
interface TaskLike { id: string; title: string; agent_id: string; status: string; request_id?: string; finished_at?: string | null; output?: OutputLike | null }

/** Maps a declared status to a known one ("" when absent). Unknown values are "unverified": never more than proven. */
export function normalizeAuditStatus(v: unknown): AuditStatus | "" {
  const s = typeof v === "string" ? v.trim().toLowerCase() : "";
  if (!s) return "";
  return s === "verified" || s === "inconsistency" ? s : "unverified";
}

const num = (v: unknown) => (typeof v === "number" && Number.isFinite(v) ? v : Number(v) || 0);

/** The stamp of a finished audit task, or null. Mirrors application.TaskAuditOf in the backend. */
export function taskAuditOf(t: TaskLike): TaskAudit | null {
  if (!t.output || t.status !== "done") return null;
  const status = normalizeAuditStatus(t.output.metrics?.audit_status);
  if (!status) return null;
  return {
    task_id: t.id, title: t.title, agent_id: t.agent_id, status, summary: t.output.summary ?? "",
    findings: (t.output.findings ?? []).filter((f) => f.trim()).slice(0, 20), evidence: (t.output.evidence ?? []).filter((e) => e.trim()).slice(0, 40),
    compared: num(t.output.metrics?.audit_compared), inconsistencies: num(t.output.metrics?.audit_inconsistencies),
    confidence: t.output.confidence ?? 0, ...(t.finished_at ? { finished_at: t.finished_at } : {}),
  };
}

/** Aggregate: inconsistency if any audit found one, verified only if all verified, none without audits. */
export function aggregateAudit(requestId: string, tasks: TaskLike[]): RequestAudit {
  const audits = tasks.filter((t) => !t.request_id || t.request_id === requestId).map(taskAuditOf).filter((a): a is TaskAudit => !!a);
  let status: RequestAuditStatus = "none";
  if (audits.length) status = audits.some((a) => a.status === "inconsistency") ? "inconsistency" : audits.every((a) => a.status === "verified") ? "verified" : "unverified";
  return { request_id: requestId, status, audits };
}

/** Accepts a GET /requests/{id}/audit payload defensively (older backends: 404 -> caller falls back to aggregateAudit). */
export function parseRequestAudit(x: unknown, requestId: string): RequestAudit | null {
  if (!x || typeof x !== "object") return null;
  const o = x as Record<string, unknown>;
  if (!Array.isArray(o.audits)) return null;
  const audits = (o.audits as Record<string, unknown>[]).map((a) => ({
    task_id: String(a.task_id ?? ""), title: String(a.title ?? ""), agent_id: String(a.agent_id ?? ""),
    status: (normalizeAuditStatus(a.status) || "unverified") as AuditStatus, summary: String(a.summary ?? ""),
    findings: Array.isArray(a.findings) ? a.findings.map(String) : [], evidence: Array.isArray(a.evidence) ? a.evidence.map(String) : [],
    compared: num(a.compared), inconsistencies: num(a.inconsistencies), confidence: num(a.confidence),
    ...(a.finished_at ? { finished_at: String(a.finished_at) } : {}),
  }));
  const st = String(o.status ?? "none");
  const status: RequestAuditStatus = st === "verified" || st === "inconsistency" || st === "unverified" ? st : "none";
  return { request_id: String(o.request_id ?? requestId), status: audits.length ? status : "none", audits };
}
