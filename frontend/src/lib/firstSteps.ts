/**
 * "Primeros pasos", step "Decide tu primer pendiente": it is done the first time a pending item is decided, wherever
 * that happens (Pendientes, the office tray, a project, "Mientras no estabas", a batch). Every decision reaches the
 * server through one of these requests, so the transport tells the checklist (an event, no store dependency).
 */
export const DECIDED_EVENT = "wf:pending-decided";

/** A request that decides a pending item: an approval decision, a batch decision or an answer to a question. */
export function isDecisionRequest(method: string, path: string): boolean {
  if (method.toUpperCase() !== "POST") return false;
  const p = path.split("?")[0];
  return /^\/approvals\/[^/]+\/decision$/.test(p) || p === "/approvals/batch" || /^\/questions\/[^/]+\/answer$/.test(p) || p === "/questions/answer-batch";
}

export function noteDecision(): void {
  if (typeof window !== "undefined") window.dispatchEvent(new Event(DECIDED_EVENT));
}

/** Call after a request succeeded. */
export function afterRequest(method: string, path: string): void {
  if (isDecisionRequest(method, path)) noteDecision();
}
