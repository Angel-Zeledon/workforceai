import type { Agent, Approval } from "../types";
import type { AwayFrame, AwayItem, AwaySummary } from "../awayApi";

/**
 * Simulated "while you were away" (B3) for NEXT_PUBLIC_MOCK=true: a believable night in the demo office, built from
 * the mock roster and the mock pending approvals. Same shape as GET /away-summary. Closing it is remembered in this
 * browser for 12 hours so the demo does not reopen it on every reload.
 */
const DISMISS_KEY = "aiw.mock.away.dismissedAt";
const HOUR = 3600_000;

export function mockAwayDismiss() {
  try { localStorage.setItem(DISMISS_KEY, String(Date.now())); } catch { /* private mode */ }
}

function dismissedRecently(): boolean {
  try { return Date.now() - Number(localStorage.getItem(DISMISS_KEY) || 0) < 12 * HOUR; } catch { return false; }
}

/** The approval the demo "received overnight", so the act-now list has something real to decide. */
export function overnightApproval(agentId: string, id: string, createdAt: string): Approval {
  return {
    id, task_id: `${id}-task`, agent_id: agentId, action: "email.send", title: "Enviar a Distribuidora Norte la propuesta de renovación (descuento del 8 %)",
    details: "Correo externo con el PDF de la propuesta adjunto. Destinatario: compras@distribuidoranorte.example. Reversible: no.",
    risk: "high", status: "pending", created_at: createdAt, resolved_at: null, required_approvals: 1,
  };
}

export function mockAway(agents: Agent[], pending: Approval[], minHours?: number): AwaySummary {
  const now = Date.now();
  const until = new Date(now - 2 * 60_000);
  const since = new Date(until.getTime() - 13.5 * HOUR);
  const at = (h: number) => new Date(since.getTime() + h * HOUR).toISOString();
  const ids = agents.map((a) => a.id);
  const a = (i: number) => ids[i % Math.max(ids.length, 1)] || "assistant";
  const name = (id: string) => agents.find((x) => x.id === id)?.name || id;

  const act: AwayItem[] = pending.filter((p) => p.status === "pending").slice(0, 5).map((p) => ({
    kind: "approval", ref: "approval", ref_id: p.id, title: p.title, code: p.action, agent_id: p.agent_id,
    severity: p.risk === "high" ? "high" : "medium", at: p.created_at, new: +new Date(p.created_at) >= since.getTime(), risk: p.risk, required_approvals: p.required_approvals ?? 1,
  }));

  // A night of work: each agent moves through a few states and ends where the office is now (idle).
  const cycle = ["thinking", "working", "working", "reviewing", "completed", "idle"];
  const frames: AwayFrame[] = [];
  const workers = ids.slice(0, 6);
  for (let step = 0; step < 24; step++) {
    workers.forEach((id, k) => {
      const st = step === 23 ? "idle" : step === 14 && k === 1 ? "error" : cycle[(step + k) % cycle.length];
      frames.push({ at: at(0.5 + step * 0.5 + k * 0.05), agent_id: id, state: st });
    });
  }

  const done = 3, tasks = 17, failed = 1;
  const usd = 1.86;
  return {
    version: 1, method: "deterministic", generated_at: new Date(now).toISOString(), first_visit: false,
    since: since.toISOString(), until: until.toISOString(), away_minutes: Math.round((until.getTime() - since.getTime()) / 60_000),
    min_hours: minHours ?? 8, clipped: false, dismissed: dismissedRecently(), quiet: false, show: !dismissedRecently() && (minHours ?? 8) <= 13.5,
    counts: {
      requests_completed: done, requests_failed: 0, tasks_completed: tasks, tasks_failed: failed, tasks_blocked: 1,
      approvals_waiting: act.length, approvals_waiting_new: act.filter((x) => x.new).length, approvals_waiting_other: 0, approvals_decided: 2,
      initiatives: 1, alerts: 1, messages: 9,
    },
    spend: { usd, calls: 64, top: [{ agent_id: a(0), usd: 0.71, calls: 22 }, { agent_id: a(1), usd: 0.52, calls: 18 }, { agent_id: a(2), usd: 0.38, calls: 14 }] },
    act_now: act,
    sections: [
      { key: "alerts", total: 1, items: [{ kind: "alert", ref: "org", ref_id: "", title: "", code: "budget_warning:org", detail: "50", severity: "medium", at: at(9.2) }] },
      { key: "problems", total: 2, items: [
        { kind: "task_failed", ref: "task", ref_id: "mock-night-t1", title: "Descargar el estado de cuenta de octubre", detail: "el portal del banco no respondió (tiempo de espera)", agent_id: a(1), severity: "medium", at: at(7.1) },
        { kind: "task_blocked", ref: "task", ref_id: "mock-night-t2", title: "Conciliar pagos con el estado de cuenta", agent_id: a(1), severity: "low", at: at(7.3), code: "dependencies" },
      ] },
      { key: "done", total: done, items: [
        { kind: "request_completed", ref: "request", ref_id: "mock-night-r1", title: "Reporte semanal de ventas por región", severity: "info", at: at(11.8) },
        { kind: "request_completed", ref: "request", ref_id: "mock-night-r2", title: "Borradores de seguimiento para 12 clientes sin respuesta", severity: "info", at: at(6.4) },
        { kind: "request_completed", ref: "request", ref_id: "mock-night-r3", title: "Resumen de tickets de soporte abiertos", severity: "info", at: at(2.9) },
      ] },
      { key: "suggestions", total: 1, items: [{ kind: "initiative", ref: "initiative", ref_id: "mock-night-i1", title: `¿Reintento la descarga del estado de cuenta a primera hora? (${name(a(1))})`, code: "task_failed", agent_id: a(1), severity: "info", at: at(7.4) }] },
      { key: "decided", total: 2, items: [
        { kind: "decision", ref: "approval", ref_id: "mock-night-ap1", title: "Publicar el post del lanzamiento en LinkedIn", code: "expired", agent_id: a(2), severity: "info", at: at(4) },
        { kind: "decision", ref: "approval", ref_id: "mock-night-ap2", title: "Guardar la hoja de pronóstico en la carpeta compartida", code: "approved", agent_id: a(0), severity: "info", at: at(1.2) },
      ] },
    ],
    agents: [
      { agent_id: a(0), name: name(a(0)), tasks_completed: 7, tasks_failed: 0, usd: 0.71, state: "idle" },
      { agent_id: a(1), name: name(a(1)), tasks_completed: 4, tasks_failed: 1, usd: 0.52, state: "idle" },
      { agent_id: a(2), name: name(a(2)), tasks_completed: 6, tasks_failed: 0, usd: 0.38, state: "idle" },
    ],
    timelapse: { available: frames.length > 0, total: frames.length, sampled: false, frames },
  };
}
