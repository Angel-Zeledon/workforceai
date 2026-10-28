/** Pure helpers of the trust ladder UI (docs/architecture/trust-ladder.md). No React, no network. */

export const LADDER = ["suggest", "approve_each", "rules", "autonomous"] as const;
export type LadderLevel = (typeof LADDER)[number];

/** Index of a level on the ladder (-1 for unknown). */
export function rank(level: string): number {
  return (LADDER as readonly string[]).indexOf(level);
}

/** The rungs shown in the card, each marked current / reached / allowed (within the role's ceiling) / locked. */
export function rungs(level: string, ceiling: string): { level: LadderLevel; state: "current" | "below" | "open" | "locked" }[] {
  const cur = rank(level), top = rank(ceiling);
  return LADDER.map((l, i) => ({ level: l, state: i === cur ? "current" : i < cur ? "below" : i <= top ? "open" : "locked" }));
}

/** Rates (0..1) as whole percentages; counts as integers; a dash without data. */
export function valueText(key: string, v: number | null | undefined): string {
  if (v == null) return "–";
  return key === "approval_rate" || key === "task_failure_rate" || key === "low_approval_rate" || key === "high_task_failure_rate"
    ? `${Math.round(v * 100)}%`
    : String(Math.round(v));
}

/** Threshold with its operator ("≥ 95%", "≤ 0"). */
export function thresholdText(key: string, op: string, th: number): string {
  const sym = op === ">=" ? "≥" : op === "<=" ? "≤" : op;
  return `${sym} ${valueText(key, th)}`;
}

/** Progress (0..1) as a whole percentage for the bar, never 100 % unless every criterion is met. */
export function progressPct(p: number | null | undefined): number {
  if (p == null || !Number.isFinite(p)) return 0;
  const n = Math.max(0, Math.min(1, p));
  return n >= 1 ? 100 : Math.min(99, Math.floor(n * 100));
}

/** Whole days from now until an ISO date (0 when it passed). */
export function daysUntil(iso: string, now: number = Date.now()): number {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return 0;
  return Math.max(0, Math.ceil((t - now) / 86_400_000));
}
