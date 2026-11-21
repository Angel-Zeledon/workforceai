// Pure helpers of the stalled-work watchdog UI (no React, no path aliases: unit-testable with node --test).
// Mirrors backend/internal/application/watchdog*.go: the signal of the suggestion, its params and the error codes.

export const STALL_SIGNAL = "stalled_task";

/** Task statuses in which a task can be stalled (running without progress, or ready and nobody runs it). */
export function mayBeStalled(status: string): boolean {
  return status === "running" || status === "pending" || status === "ready";
}

/** The task a stalled-task suggestion is about ("" when it is another kind of suggestion). */
export function stallTaskOf(signal: string, params?: Record<string, string> | null): string {
  return signal === STALL_SIGNAL && params?.task_id ? params.task_id : "";
}

/** "40 min" under two hours, "3 h" after, "2 d" after two days (the UI shows how long it has not moved). */
export function stallAge(minutes: number): { key: "stall.age.min" | "stall.age.hours" | "stall.age.days"; n: number } {
  const m = Math.max(0, Math.floor(minutes || 0));
  if (m < 120) return { key: "stall.age.min", n: m };
  if (m < 48 * 60) return { key: "stall.age.hours", n: Math.floor(m / 60) };
  return { key: "stall.age.days", n: Math.floor(m / (24 * 60)) };
}

/** The ids a cancellation confirms: exactly the cascade the person was shown. */
export function confirmIds(cascade: { id: string }[]): string[] {
  return cascade.map((c) => c.id);
}

/** Whether a cancellation may be sent: it is stalled and no dependent belongs to a department the person does not see. */
export function canCancel(d: { stalled: boolean; hidden_cascade: number } | null): boolean {
  return !!d && d.stalled && d.hidden_cascade === 0;
}

/** The i18n key of a refused recovery, from the HTTP status and the backend's code. */
export function stallErrorKey(status: number, code?: string): string {
  if (code === "cascade_changed") return "stall.err.cascadeChanged";
  if (code === "cascade_hidden") return "stall.err.cascadeHidden";
  if (status === 403) return "stall.err.forbidden";
  if (status === 409) return "stall.err.conflict";
  if (status === 400) return "stall.err.rejected";
  if (status === 404) return "stall.err.gone";
  return "stall.err.generic";
}

/** The i18n key of a successful recovery. */
export function stallDoneKey(action: "retry" | "reassign" | "cancel", status: string): string {
  if (status === "finished") return "stall.done.finished";
  if (status === "requested") return "stall.done.requested";
  return `stall.done.${action}`;
}
