"use client";
import { useMemo } from "react";
import { useArtifact, useArtifacts, type StoredArtifact } from "@/lib/artifacts";
import { useT } from "@/lib/i18n";
import { addr, cellValue, colIndex, parseAddr, type CellValue, type SheetContent } from "@/lib/sheetCalc";
import { useRefResolver } from "./SheetEditor";

export interface ChartSpec {
  schema: "aiw.chart/1"; title?: string; mark: "bar" | "line" | "area" | "pie" | "scatter" | "combo";
  source: { artifact_id?: string; sheet?: string; range?: string; inline?: { columns: string[]; rows: CellValue[][] } };
  encoding: { x: string; y: string[]; series?: string | null }; format?: { y?: string }; stack?: boolean;
}
const PALETTE = ["#3451b2", "#2a8590", "#a8741f", "#7461b5", "#b4543c", "#5f8a3f", "#5b8fb5", "#7b8494"];

/** Resolves the chart data matrix (header row + rows) from an inline table or from another artifact (live). */
function useChartMatrix(spec: ChartSpec | undefined): CellValue[][] {
  const src = useArtifact(spec?.source.artifact_id);
  const content = src?.content as (SheetContent & { columns?: { key: string; label: string }[]; rows?: any[] }) | undefined;
  const resolve = useRefResolver(content as SheetContent | undefined);
  return useMemo(() => {
    if (!spec) return [];
    if (spec.source.inline) return [spec.source.inline.columns, ...spec.source.inline.rows];
    if (!content) return [];
    if (src?.kind === "table" && content.columns && content.rows) return [content.columns.map((c) => c.label), ...content.rows.map((r) => content.columns!.map((c) => r.cells[c.key] ?? null))];
    if (!content.sheets) return [];
    const sheet = content.sheets.find((s) => s.name === spec.source.sheet) ?? content.sheets[0];
    const [a, b] = (spec.source.range ?? "A1:B10").split(":");
    const pa = parseAddr(a), pb = parseAddr(b ?? a);
    if (!pa || !pb) return [];
    const ctx = { sheets: content.sheets, sheet, imports: content.imports, resolve };
    const out: CellValue[][] = [];
    for (let r = pa.r; r <= pb.r; r++) { const row: CellValue[] = []; for (let c = pa.c; c <= pb.c; c++) row.push(cellValue(ctx, addr(c, r))); out.push(row); }
    return out;
  }, [spec, content, src?.kind, resolve]);
}

function colOf(header: CellValue[], key: string): number {
  const i = header.findIndex((h) => String(h) === key);
  if (i >= 0) return i;
  return /^[A-Z]{1,2}$/.test(key) ? colIndex(key) - (colIndex("A")) : 0;
}
const fmtNum = (n: number, f?: string) => (f?.includes("$") ? "$" : "") + (Math.abs(n) >= 1000 ? Math.round(n).toLocaleString("en-US") : String(Math.round(n * 100) / 100));

export function ChartView({ art, compact, height }: { art: StoredArtifact; compact?: boolean; height?: number }) {
  const { t } = useT();
  const spec = art.content as ChartSpec | undefined;
  const matrix = useChartMatrix(spec);
  const focus = useArtifacts((s) => s.focus[art.id]);
  if (!spec) return <div className="p-4 text-xs text-mute">…</div>;
  const header = matrix[0] ?? [];
  const body = matrix.slice(1).filter((r) => r.some((v) => v !== null && v !== ""));
  const xi = colOf(header, spec.encoding.x);
  const ys = spec.encoding.y.map((y) => colOf(header, y)).filter((i, k, a) => a.indexOf(i) === k && i !== xi);
  const cats = body.map((r) => String(r[xi] ?? ""));
  const series = ys.map((yi, k) => ({ name: String(header[yi] ?? ""), color: PALETTE[k % PALETTE.length], vals: body.map((r) => (typeof r[yi] === "number" ? (r[yi] as number) : 0)) }));
  const W = 560, H = height ?? (compact ? 190 : 280), pad = { l: 56, r: 12, t: 14, b: 44 };
  const iw = W - pad.l - pad.r, ih = H - pad.t - pad.b;
  const all = series.flatMap((s) => s.vals);
  const max = Math.max(1, ...all), min = Math.min(0, ...all);
  const y = (v: number) => pad.t + ih - ((v - min) / (max - min || 1)) * ih;
  const step = iw / Math.max(1, cats.length);
  const ticks = [0, 0.25, 0.5, 0.75, 1].map((p) => min + (max - min) * p);
  const mark = spec.mark === "combo" || spec.mark === "scatter" ? "line" : spec.mark;

  return (
    <div data-testid="art-chart" className="h-full overflow-auto bg-panel p-3" style={focus ? { boxShadow: "inset 0 0 0 2px #3451b2" } : undefined}>
      {spec.title && <div className="mb-1 font-display text-sm font-semibold text-ink">{spec.title}</div>}
      {series.length === 0 || cats.length === 0 ? <div className="p-6 text-center text-xs text-mute">{t("chart.noData")}</div> : (
        <>
          <svg viewBox={`0 0 ${W} ${H}`} className="w-full" role="img" aria-label={spec.title ?? t("artifact.kind.chart")}>
            {mark === "pie" ? (() => {
              const vals = series[0].vals.map((v) => Math.max(0, v)); const tot = vals.reduce((a, b) => a + b, 0) || 1;
              let a0 = -Math.PI / 2; const cx = W / 2, cy = H / 2, r = Math.min(ih, iw) / 2;
              return vals.map((v, i) => {
                const a1 = a0 + (v / tot) * Math.PI * 2; const large = a1 - a0 > Math.PI ? 1 : 0;
                const d = `M${cx},${cy} L${cx + r * Math.cos(a0)},${cy + r * Math.sin(a0)} A${r},${r} 0 ${large} 1 ${cx + r * Math.cos(a1)},${cy + r * Math.sin(a1)} Z`; a0 = a1;
                return <path key={i} d={d} fill={PALETTE[i % PALETTE.length]} stroke="#ffffff" strokeWidth={2} />;
              });
            })() : (
              <>
                {ticks.map((tk, i) => (
                  <g key={i}><line x1={pad.l} x2={W - pad.r} y1={y(tk)} y2={y(tk)} stroke="#e2e6ec" strokeWidth={1} /><text x={pad.l - 6} y={y(tk) + 3} textAnchor="end" fontSize={9} fill="#5b6678">{fmtNum(tk, spec.format?.y)}</text></g>
                ))}
                {mark === "bar" && series.map((s, si) => s.vals.map((v, i) => {
                  const bw = (step * 0.7) / series.length;
                  return <rect key={`${si}-${i}`} x={pad.l + i * step + step * 0.15 + si * bw} y={Math.min(y(v), y(0))} width={bw} height={Math.abs(y(v) - y(0))} rx={3} fill={s.color} />;
                }))}
                {(mark === "line" || mark === "area") && series.map((s, si) => {
                  const pts = s.vals.map((v, i) => `${pad.l + i * step + step / 2},${y(v)}`);
                  return (
                    <g key={si}>
                      {mark === "area" && <polygon points={`${pad.l + step / 2},${y(0)} ${pts.join(" ")} ${pad.l + (s.vals.length - 1) * step + step / 2},${y(0)}`} fill={s.color} opacity={0.18} />}
                      <polyline points={pts.join(" ")} fill="none" stroke={s.color} strokeWidth={2.5} strokeLinejoin="round" strokeLinecap="round" />
                      {s.vals.map((v, i) => <circle key={i} cx={pad.l + i * step + step / 2} cy={y(v)} r={3} fill="#ffffff" stroke={s.color} strokeWidth={2} />)}
                    </g>
                  );
                })}
                {cats.map((c, i) => (cats.length > 14 && i % 2 ? null : <text key={i} x={pad.l + i * step + step / 2} y={H - pad.b + 14} textAnchor="middle" fontSize={9} fill="#5b6678">{c.length > 12 ? `${c.slice(0, 11)}…` : c}</text>))}
              </>
            )}
          </svg>
          <div className="mt-1 flex flex-wrap gap-3 text-[11px] text-mute">
            {(mark === "pie" ? cats : series.map((s) => s.name)).map((n, i) => <span key={i} className="inline-flex items-center gap-1"><span className="h-2 w-2 rounded-full" style={{ background: PALETTE[i % PALETTE.length] }} />{n}</span>)}
          </div>
        </>
      )}
    </div>
  );
}
