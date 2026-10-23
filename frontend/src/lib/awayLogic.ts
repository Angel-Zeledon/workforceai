/** Pure helpers of the "while you were away" sheet (B3). Tested by awayLogic.test.mjs; no React, no i18n here. */

export interface CountsLike {
  requests_completed: number; requests_failed: number; tasks_completed: number; tasks_failed: number; tasks_blocked: number;
  approvals_waiting: number; approvals_decided: number; initiatives: number; alerts: number;
}

/** The headline, in the order a person wants to read it: what waits for them, what went wrong, then what got done. */
export type HeadlinePart = { key: "approvals" | "alerts" | "failed" | "blocked" | "completed" | "tasks" | "decided" | "initiatives"; count: number };

export function headlineParts(c: CountsLike, max = 4): HeadlinePart[] {
  const all: HeadlinePart[] = [
    { key: "approvals", count: c.approvals_waiting },
    { key: "alerts", count: c.alerts },
    { key: "failed", count: c.requests_failed + c.tasks_failed },
    { key: "blocked", count: c.tasks_blocked },
    { key: "completed", count: c.requests_completed },
    { key: "tasks", count: c.tasks_completed },
    { key: "decided", count: c.approvals_decided },
    { key: "initiatives", count: c.initiatives },
  ];
  return all.filter((p) => p.count > 0).slice(0, max);
}

/** How long the person was away, in the largest unit that reads well ("14 h", "3 days", "45 min"). */
export function awayDuration(minutes: number): { n: number; unit: "minutes" | "hours" | "days" } {
  const m = Math.max(0, Math.round(minutes || 0));
  if (m < 90) return { n: m, unit: "minutes" };
  const h = Math.round(m / 60);
  if (h < 48) return { n: h, unit: "hours" };
  return { n: Math.round(h / 24), unit: "days" };
}

/** Choices of "open on its own after" (hours); 0 = never open on its own. */
export const MIN_HOURS_CHOICES = [4, 8, 12, 24, 0] as const;

/** Reads the stored preference; anything unknown falls back to the default (8 unless configured). */
export function parseMinHours(raw: string | null | undefined, fallback = 8): number {
  const n = Number(raw);
  if (raw === null || raw === undefined || raw === "" || !Number.isFinite(n)) return fallback;
  return (MIN_HOURS_CHOICES as readonly number[]).includes(n) ? n : fallback;
}

/** What a button on an item opens. */
export type ItemAction = "approval" | "report" | "agent" | "dashboard" | "none";

export function itemAction(it: { ref: string; ref_id: string; agent_id?: string }): ItemAction {
  switch (it.ref) {
    case "approval": return it.ref_id ? "approval" : "none";
    case "report": return it.ref_id ? "report" : "none";
    case "task": return it.agent_id ? "agent" : "dashboard";
    case "agent": return it.ref_id || it.agent_id ? "agent" : "none";
    case "request":
    case "initiative": return "dashboard";
  }
  return "none";
}

/** i18n key of an alert code ("anomaly:spend_spike" -> "away.alert.anomaly"), so unknown rules still read well. */
export function alertKey(code: string | undefined): string {
  const head = (code || "").split(":")[0];
  return `away.alert.${head || "other"}`;
}

/**
 * Timelapse schedule: when (ms from the start) each frame plays. Frames are spread evenly by index, not by real time,
 * so a quiet night does not freeze the replay; the whole replay lasts at most maxMs and at least minStepMs per frame.
 */
export function replaySchedule(n: number, maxMs = 20000, minStepMs = 40, maxStepMs = 900): number[] {
  if (n <= 0) return [];
  const step = Math.min(maxStepMs, Math.max(minStepMs, Math.floor(maxMs / n)));
  return Array.from({ length: n }, (_, i) => i * step);
}
