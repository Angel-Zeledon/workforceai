/**
 * Mock of GET /reports/{id}/evidence (B6) for the scripted $50,000 scenario. The citations mirror what the Go
 * backend returns after checking them: verified ones carry a resolved source (a cell of the mock margin sheet,
 * a version of a mock drive file, a mock Gmail item, another task); one figure is deliberately unverified to
 * show how the UI flags a source the agent could not prove it read.
 */
import type { ReportCitation, ReportEvidence } from "../evidenceLogic";
import type { Report, Task } from "../types";

export function demoEvidence(report: Report, tasks: Task[], keyOf: (taskId: string) => string | undefined): ReportEvidence {
  const byKey = (k: string) => tasks.find((t) => keyOf(t.id) === k);
  const t1 = byKey("t1"), t3 = byKey("t3"), t4 = byKey("t4"), t6 = byKey("t6");
  const readAt = new Date(Date.parse(report.created_at) - 6 * 60_000).toISOString();
  const cites: ReportCitation[] = [];
  const add = (from: Task | undefined, c: Omit<ReportCitation, "task_id" | "agent_id">) => {
    if (from) cites.push({ ...c, task_id: from.id, agent_id: from.agent_id });
  };
  add(t6, { value: "$50,000", label: "ticket de la propuesta", ref: "file:/Clients/Grupo Alfa/brief.md", kind: "file", status: "verified", value_checked: true,
    source: { path: "/Clients/Grupo Alfa/brief.md", version: 1, read_at: readAt } });
  add(t6, { value: "$50,000", label: "presupuesto confirmado por el cliente", ref: "conn:email:msg-18c2f0a9", kind: "conn", status: "verified", value_checked: false,
    source: { provider: "google_gmail", tool: "email", item_id: "msg-18c2f0a9", read_at: readAt } });
  if (t3) add(t6, { value: "24%", label: "margen real", ref: `task:${t3.id}`, kind: "task", status: "verified", value_checked: true,
    source: { task_id: t3.id, title: t3.title, agent_id: t3.agent_id } });
  add(t3, { value: "24%", label: "margen real", ref: "cell:art_demo_margin!Margen!B7", kind: "cell", status: "verified", value_checked: true,
    source: { artifact_id: "art_demo_margin", sheet: "Margen", cell: "B7", cell_value: "0.24" } });
  if (t1) add(t3, { value: "31%", label: "margen presentado", ref: `task:${t1.id}`, kind: "task", status: "verified", value_checked: true,
    source: { task_id: t1.id, title: t1.title, agent_id: t1.agent_id } });
  add(t3, { value: "$7,800", label: "costo mensual de capacidad", ref: "file:/Shared/Finanzas/costo-capacidad.csv", kind: "file", status: "verified", value_checked: true,
    source: { path: "/Shared/Finanzas/costo-capacidad.csv", version: 2, read_at: readAt } });
  if (t3) add(t4, { value: "17%", label: "margen en escenario adverso", ref: `task:${t3.id}`, kind: "task", status: "verified", value_checked: false,
    source: { task_id: t3.id, title: t3.title, agent_id: t3.agent_id } });
  add(t4, { value: "28%", label: "margen optimista", ref: "memory:escenarios-2025", kind: "memory", status: "unverified", reason: "not_seen", value_checked: false, source: null });
  const verified = cites.filter((c) => c.status === "verified");
  const ordered = [...verified, ...cites.filter((c) => c.status !== "verified")];
  return { report_id: report.id, request_id: report.request_id, citations: ordered, verified: verified.length, total: ordered.length };
}
