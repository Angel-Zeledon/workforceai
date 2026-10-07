import { fmtNumber } from "@/lib/i18n";

/** USD with enough decimals for small LLM costs ($0.0042 must not read as $0.00). */
export function usd(n: number | undefined | null): string {
  const v = Number(n) || 0;
  const digits = v === 0 || v >= 1 ? 2 : v >= 0.01 ? 3 : 4;
  return `$${fmtNumber(v, digits)}`;
}

/** "$0.12 – $0.48": the estimate is always shown as a range. */
export function usdRange(min: number, max: number): string {
  return `${usd(min)} – ${usd(max)}`;
}

/** A sensible suggestion for a raised cap: needed amount plus 50%, rounded up to cents. */
export function suggestCap(needed: number): number {
  return Math.ceil(Math.max(needed, 0.01) * 1.5 * 100) / 100;
}
