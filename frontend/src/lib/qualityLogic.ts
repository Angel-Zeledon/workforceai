/** Pure helpers of the quality score UI (docs/architecture/quality-score.md). No React, no network. */

export type QualityLevel = "excellent" | "good" | "fair" | "needs_attention";
export type QualityTrend = "up" | "down" | "stable" | "unknown";
export type GoldenCheckKind = "contains" | "not_contains";
export interface GoldenCheck { kind: GoldenCheckKind; value: string }

/** Tailwind-free color per level (the same palette as the status badges). */
export const LEVEL_COLOR: Record<QualityLevel, string> = {
  excellent: "#2f7d55", good: "#2b6cb0", fair: "#a86208", needs_attention: "#b4443c",
};

/** "0.8333" -> "83%". Rates are shown as whole percentages: more digits would be invented precision. */
export function pctText(rate: number | null | undefined): string {
  return rate == null ? "–" : `${Math.round(rate * 100)}%`;
}

/** Nominal weight (0.3) as a whole percentage (30). */
export function weightPct(weight: number): number {
  return Math.round(weight * 100);
}

/** Signed delta without a plus sign ("-6" -> "6"): the label already says up or down. */
export function deltaText(delta: number | null | undefined): string {
  return delta == null ? "" : String(Math.abs(Math.round(delta)));
}

export function trendArrow(trend: QualityTrend): string {
  return trend === "up" ? "▲" : trend === "down" ? "▼" : trend === "stable" ? "■" : "–";
}

/**
 * Parses the one-check-per-line text of the golden case form ("contains: text" / "not_contains: text").
 * Returns null when any non-empty line is not in that shape, so the form can say so instead of dropping it.
 */
export function parseChecks(text: string): GoldenCheck[] | null {
  const out: GoldenCheck[] = [];
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim();
    if (!line) continue;
    const m = /^(contains|not_contains)\s*:\s*(.+)$/i.exec(line);
    if (!m) return null;
    out.push({ kind: m[1].toLowerCase() as GoldenCheckKind, value: m[2].trim() });
  }
  return out;
}

/** The inverse of parseChecks, for editing. */
export function checksText(checks: GoldenCheck[]): string {
  return checks.map((c) => `${c.kind}: ${c.value}`).join("\n");
}
