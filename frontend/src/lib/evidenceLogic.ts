/**
 * Evidence chain (B6, docs/architecture/evidence-chain.md): pure helpers, no React and no network.
 * The backend verifies every citation against what the task really read; the UI only links the figures of a
 * report that match a VERIFIED citation. Unverified citations are shown as a warning, never as a link.
 */

export type CitationKind = "task" | "report" | "memory" | "file" | "cell" | "conn";

export interface CitationSource {
  task_id?: string; title?: string; agent_id?: string;
  artifact_id?: string; sheet?: string; cell?: string; cell_value?: string;
  file_id?: string; path?: string; version?: number;
  provider?: string; tool?: string; item_id?: string; read_at?: string;
}

export interface Citation {
  value: string; label?: string; ref: string; kind?: string;
  status?: "verified" | "unverified" | string; reason?: string; value_checked?: boolean;
  source?: CitationSource | null;
}

/** A citation of one task of a report (GET /reports/{id}/evidence). */
export interface ReportCitation extends Citation { task_id: string; agent_id: string }

export interface ReportEvidence { report_id: string; request_id: string; citations: ReportCitation[]; verified: number; total: number }

export interface Figure { start: number; end: number; n: number; pct: boolean; money: boolean }

const FIGURE = /[-−]?\d[\d.,]*\d|[-−]?\d/g;
const MONEY_BEFORE = /(?:[$€£]|USD|MXN|COP|EUR|CLP|ARS|PEN|CRC)\s?$/i;

/** One numeric token with either decimal convention (same rule as the backend). */
export function parseNumber(token: string): number | null {
  const neg = /^[-−]/.test(token);
  const tok = token.replace(/[^\d.,]/g, "");
  if (!tok) return null;
  const lastDot = tok.lastIndexOf("."), lastComma = tok.lastIndexOf(",");
  let dec = -1;
  if (lastDot >= 0 && lastComma >= 0) dec = Math.max(lastDot, lastComma);
  else if (lastDot >= 0 || lastComma >= 0) {
    const sep = lastComma >= 0 ? "," : ".";
    const pos = Math.max(lastDot, lastComma);
    if (tok.split(sep).length === 2 && tok.length - pos - 1 !== 3) dec = pos;
  }
  let s = "";
  for (let i = 0; i < tok.length; i++) {
    if (i === dec) s += ".";
    else if (tok[i] !== "." && tok[i] !== ",") s += tok[i];
  }
  const n = Number(s);
  if (!Number.isFinite(n)) return null;
  return neg ? -n : n;
}

/** The figures of a text with their positions ("$50,000", "24 %", "1.200.000,50"; "3-4" is a range, not -4). */
export function findFigures(text: string): Figure[] {
  const out: Figure[] = [];
  for (const m of text.matchAll(FIGURE)) {
    let start = m.index ?? 0;
    let tok = m[0];
    const prev = text[start - 1] ?? "";
    if (/^[-−]/.test(tok) && /[\p{L}\p{N}]/u.test(prev)) { tok = tok.slice(1); start += 1; }
    if (/[\p{L}]/u.test(prev) && !/^[-−]/.test(m[0])) continue; // part of a word ("Q3", "B12")
    const n = parseNumber(tok);
    if (n === null) continue;
    let end = start + tok.length;
    const after = text.slice(end);
    const pctM = /^\s?%/.exec(after);
    if (pctM) end += pctM[0].length;
    if (/^[\p{L}]/u.test(after) && !pctM) continue; // "3er", "10x": not a plain figure
    const before = text.slice(0, start);
    const moneyM = MONEY_BEFORE.exec(before);
    if (moneyM) start -= moneyM[0].length;
    out.push({ start, end, n, pct: !!pctM, money: !!moneyM });
  }
  return out;
}

/** The main figure of a cited value. */
export function figureOf(value: string): Figure | null {
  return findFigures(value)[0] ?? null;
}

const near = (a: number, b: number) => Math.abs(a - b) <= 1e-6 * Math.max(1, Math.abs(a), Math.abs(b));

/** A figure of the text matches a cited one: same number and both percentages (or neither). */
export function sameFigure(cited: Figure, inText: Figure): boolean {
  return cited.pct === inText.pct && near(cited.n, inText.n);
}

/** Small bare numbers ("2 people", "3 weeks") are too ambiguous to link; money, percentages and |n| >= 10 are not. */
export const significant = (f: Figure) => f.pct || f.money || Math.abs(f.n) >= 10;

/** Verified citations that can be opened (they carry a resolved source). */
export function linkable(cites: readonly Citation[]): Citation[] {
  return cites.filter((c) => c.status === "verified" && !!c.source);
}

export interface Segment<C extends Citation = Citation> { text: string; cites?: C[] }

/** Splits a text into plain parts and figures that match at least one verified citation. */
export function linkFigures<C extends Citation>(text: string, cites: readonly C[]): Segment<C>[] {
  const ok = linkable(cites).map((c) => ({ c: c as C, f: figureOf(c.value) })).filter((x) => x.f && significant(x.f));
  if (!text || !ok.length) return [{ text }];
  const out: Segment<C>[] = [];
  let at = 0;
  for (const f of findFigures(text)) {
    if (!significant(f)) continue;
    const hits = ok.filter((x) => sameFigure(x.f!, f)).map((x) => x.c);
    if (!hits.length) continue;
    if (f.start > at) out.push({ text: text.slice(at, f.start) });
    out.push({ text: text.slice(f.start, f.end), cites: hits });
    at = f.end;
  }
  if (at < text.length) out.push({ text: text.slice(at) });
  return out;
}

/** Kind of a citation (the backend sets it; old rows fall back to the ref prefix). */
export function kindOf(c: Citation): CitationKind | "" {
  const k = (c.kind || c.ref.split(":")[0] || "").toLowerCase();
  return (["task", "report", "memory", "file", "cell", "conn"] as const).find((x) => x === k) ?? "";
}

/** Unverified citations (shown as a warning list, never linked). */
export function unverifiedOf<C extends Citation>(cites: readonly C[]): C[] {
  return cites.filter((c) => c.status !== "verified");
}

/** Distinct verified figures, for the "N figures traced" chip. */
export function tracedCount(cites: readonly Citation[]): number {
  return new Set(linkable(cites).map((c) => { const f = figureOf(c.value); return f ? `${f.pct ? "%" : ""}${f.n}` : c.value; })).size;
}
