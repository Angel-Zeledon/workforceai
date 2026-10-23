/**
 * Pure helpers of the proactive suggestions (B2). Mirrors backend/internal/application/initiative_signals.go:
 * the signal kinds, the threshold ranges and the params each signal stores. Keep both in sync.
 */

export type MetricSignal = "metric_change" | "spend_pace" | "approval_aging" | "quality_drop";
export const METRIC_SIGNALS: MetricSignal[] = ["metric_change", "spend_pace", "approval_aging", "quality_drop"];

export type ThresholdField = "change_pct" | "margin_points" | "pace_pct" | "min_days" | "min_age_hours" | "min_count" | "drop_points";

/** Threshold inputs per signal, with the accepted range and whether the value is whole. */
export const SIGNAL_FIELDS: Record<MetricSignal, { field: ThresholdField; min: number; max: number; step: number }[]> = {
  metric_change: [{ field: "change_pct", min: 1, max: 1000, step: 1 }, { field: "margin_points", min: 0.5, max: 100, step: 0.5 }],
  spend_pace: [{ field: "pace_pct", min: 50, max: 500, step: 5 }, { field: "min_days", min: 1, max: 27, step: 1 }],
  approval_aging: [{ field: "min_age_hours", min: 1, max: 720, step: 1 }, { field: "min_count", min: 1, max: 100, step: 1 }],
  quality_drop: [{ field: "drop_points", min: 1, max: 100, step: 1 }],
};

/** Clamps what a person typed into the accepted range (whole numbers unless the step is fractional). */
export function clampThreshold(signal: MetricSignal, field: ThresholdField, raw: string): number {
  const spec = SIGNAL_FIELDS[signal].find((f) => f.field === field);
  if (!spec) return 0;
  let n = Number(raw);
  if (!Number.isFinite(n)) n = spec.min;
  if (spec.step >= 1) n = Math.round(n);
  return Math.min(spec.max, Math.max(spec.min, n));
}

export interface TextRef { key: string; vars: Record<string, string> }

/**
 * The i18n keys that render a suggestion in the viewer's language from its signal and params. null means "use the
 * stored title and body" (failed tasks, or suggestions stored before params existed).
 */
export function signalText(signal: string, params?: Record<string, string> | null): { title: TextRef; body: TextRef } | null {
  if (!params || Object.keys(params).length === 0) return null;
  const ref = (key: string): TextRef => ({ key, vars: params });
  switch (signal) {
    case "metric_change": {
      const dir = params.direction === "up" ? "up" : "down";
      const unit = params.unit === "points" ? "points" : "pct";
      return { title: ref(`initiative.signal.metric_change.title.${dir}.${unit}`),
        body: ref(params.period ? "initiative.signal.metric_change.bodyPeriod" : "initiative.signal.metric_change.body") };
    }
    case "spend_pace":
    case "approval_aging":
    case "quality_drop":
      return { title: ref(`initiative.signal.${signal}.title`), body: ref(`initiative.signal.${signal}.body`) };
  }
  return null;
}
