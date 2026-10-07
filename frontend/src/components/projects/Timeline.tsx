"use client";
import { useMemo, useState } from "react";
import { useT } from "@/lib/i18n";
import { fmtDuration, isDoneState, isGroup, simulate } from "@/lib/projects/calc";
import { useProjects } from "@/lib/projects/store";
import type { ProjectNode } from "@/lib/projects/types";
import { useTicker } from "../ui";
import { NODE_STATE_COLOR, nodeColor, useNodeTitle } from "./shared";

const ROW_H = 28;
const LABEL_W = 270;
const BASE_W = 900;
const STEPS = [1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 1800, 3600, 7200, 21600, 43200, 86400];

interface Row {
  id: string; depth: number; kind: "objective" | "group" | "node"; node?: ProjectNode; title: { title: string; title_key?: string; title_params?: Record<string, string> };
  planned: [number, number] | null; actual: [number, number] | null; forecast: [number, number] | null; state: ProjectNode["state"]; progress: number; hasChildren: boolean; human: boolean;
}
const ts = (s: string | null) => (s ? Date.parse(s) : NaN);
const env = (xs: ([number, number] | null)[]): [number, number] | null => {
  const v = xs.filter(Boolean) as [number, number][];
  return v.length ? [Math.min(...v.map((x) => x[0])), Math.max(...v.map((x) => x[1]))] : null;
};

/** Own Gantt (spec 4.3): planned vs actual vs forecast, human-wait hatching, milestones as diamonds, "now" line. */
export function Timeline() {
  const { t } = useT();
  useTicker(1000);
  const detail = useProjects((s) => s.detail)!;
  const select = useProjects((s) => s.selectNode);
  const title = useNodeTitle();
  const [zoom, setZoom] = useState(1);
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const now = Date.now();
  const p = detail.project;
  const t0 = ts(p.started_at) || now;

  const { rows, sim } = useMemo(() => {
    const sim = simulate(detail.nodes, Date.now());
    const leafRow = (n: ProjectNode, depth: number): Row => {
      const planned: [number, number] | null = n.plan_start_ms !== null && n.plan_end_ms !== null ? [t0 + n.plan_start_ms, t0 + n.plan_end_ms] : null;
      const st = ts(n.started_at); const fin = ts(n.finished_at);
      const actual: [number, number] | null = Number.isNaN(st) ? null : [st, Number.isNaN(fin) ? Date.now() : fin];
      const forecast: [number, number] | null = isDoneState(n.state) || n.state === "cancelled" || n.state === "failed" ? null : [Math.max(Date.now(), sim.start.get(n.id) ?? Date.now()), sim.end.get(n.id) ?? Date.now()];
      return { id: n.id, depth, kind: "node", node: n, title: n, planned, actual, forecast, state: n.state, progress: n.progress, hasChildren: false, human: n.kind === "gate" || n.state === "awaiting_approval" || n.kind === "wait" };
    };
    const agg = (id: string, depth: number, kind: "objective" | "group", ttl: Row["title"], kids: Row[]): Row => {
      const dn = kids.filter((k) => k.kind === "node");
      const done = dn.length ? dn.filter((k) => isDoneState(k.state)).length / dn.length : 0;
      const live = kids.some((k) => k.state === "running");
      return { id, depth, kind, title: ttl, planned: env(kids.map((k) => k.planned)), actual: env(kids.map((k) => k.actual)), forecast: env(kids.map((k) => k.forecast)), state: done === 1 ? "done" : live ? "running" : "pending", progress: done * 100, hasChildren: true, human: false };
    };
    const out: Row[] = [];
    for (const o of detail.objectives) {
      const wfRows: Row[][] = [];
      const objKids: Row[] = [];
      for (const g of detail.nodes.filter((n) => isGroup(n) && n.objective_id === o.id)) {
        const tasks = detail.nodes.filter((n) => n.parent_id === g.id);
        const rs: Row[] = [];
        for (const tk of tasks) {
          rs.push(leafRow(tk, 2));
          for (const sub of detail.nodes.filter((s) => s.parent_id === tk.id && s.kind === "subtask")) rs.push(leafRow(sub, 3));
        }
        const gr = agg(g.id, 1, "group", g, rs.filter((r) => r.kind === "node"));
        wfRows.push([gr, ...rs]); objKids.push(...rs);
      }
      const or = agg(o.id, 0, "objective", o, objKids);
      out.push(or);
      if (collapsed.has(o.id)) continue;
      for (const wf of wfRows) { out.push(wf[0]); if (!collapsed.has(wf[0].id)) out.push(...wf.slice(1)); }
    }
    return { rows: out, sim };
  }, [detail.nodes, detail.objectives, collapsed, t0]);

  const all = rows.flatMap((r) => [r.planned, r.actual, r.forecast]).filter(Boolean) as [number, number][];
  const start = Math.min(t0, ...all.map((x) => x[0]));
  const end = Math.max(now, sim.makespanEnd, ...all.map((x) => x[1])) + 2000;
  const plotW = BASE_W * zoom;
  const x = (ms: number) => ((ms - start) / (end - start)) * plotW;
  const spanS = (end - start) / 1000;
  const step = STEPS.find((s) => (s / spanS) * plotW >= 80) ?? STEPS[STEPS.length - 1];
  const ticks: number[] = []; for (let s = 0; s <= spanS; s += step) ticks.push(s);
  const toggle = (id: string) => setCollapsed((c) => { const n = new Set(c); if (n.has(id)) n.delete(id); else n.add(id); return n; });
  const H = (rows.length + 1) * ROW_H;

  return (
    <div className="rounded-xl border border-line bg-panel shadow-pop">
      <div className="flex items-center justify-between border-b border-line px-4 py-2">
        <div className="flex flex-wrap items-center gap-3 text-[10px] text-mute">
          <Legend color="#aab3c0" label={t("pv.timeline.planned")} /><Legend color={NODE_STATE_COLOR.running} label={t("pv.timeline.actual")} /><Legend color="#5b8fb5" dashed label={t("pv.timeline.forecast")} /><Legend color="#a86208" hatched label={t("pv.timeline.humanWait")} />
        </div>
        <div className="flex items-center gap-1.5">
          <button type="button" data-testid="timeline-zoom-out" onClick={() => setZoom((z) => Math.max(0.5, z / 2))} className="rounded-md border border-line px-2.5 text-xs font-semibold text-ink">−</button>
          <span className="w-10 text-center font-mono text-[10px] text-mute">×{zoom}</span>
          <button type="button" data-testid="timeline-zoom-in" onClick={() => setZoom((z) => Math.min(8, z * 2))} className="rounded-md border border-line px-2.5 text-xs font-semibold text-ink">+</button>
        </div>
      </div>
      <div className="flex">
        <div className="shrink-0 border-r border-line" style={{ width: LABEL_W }}>
          <div style={{ height: ROW_H }} className="border-b border-line px-3 text-[10px] font-semibold uppercase leading-[28px] text-mute">{t("pv.timeline.wbs")}</div>
          {rows.map((r) => (
            <div key={r.id} data-testid={`timeline-row-${r.id}`} data-state={r.state} style={{ height: ROW_H, paddingLeft: 8 + r.depth * 14 }}
              className={`flex items-center gap-1 truncate border-b border-line/50 pr-2 text-[11.5px] ${r.kind === "node" ? "cursor-pointer hover:bg-mute/[0.08]" : "font-semibold text-ink"}`}
              onClick={() => (r.kind === "node" ? select(r.id) : toggle(r.id))}>
              {r.hasChildren && <span className="w-3 text-mute">{collapsed.has(r.id) ? "▸" : "▾"}</span>}
              <span className="truncate">{title(r.title)}</span>
            </div>
          ))}
        </div>
        <div className="min-w-0 flex-1 overflow-x-auto">
          <svg width={plotW} height={H} className="block">
            <defs>
              <pattern id="pv-hatch" width="6" height="6" patternUnits="userSpaceOnUse" patternTransform="rotate(45)"><rect width="3" height="6" fill="#a86208" opacity="0.75" /></pattern>
            </defs>
            {ticks.map((s) => (
              <g key={s}>
                <line x1={x(start + s * 1000)} x2={x(start + s * 1000)} y1={0} y2={H} stroke="#e2e6ec" strokeWidth={1} />
                <text x={x(start + s * 1000) + 3} y={18} fontSize={10} fill="#5b6678">{fmtDuration(s)}</text>
              </g>
            ))}
            {rows.map((r, i) => {
              const y = (i + 1) * ROW_H;
              const c = r.node ? nodeColor(r.node) : NODE_STATE_COLOR[r.state];
              const diamond = r.node?.kind === "gate" || r.node?.kind === "milestone";
              return (
                <g key={r.id} transform={`translate(0 ${y})`}>
                  <line x1={0} x2={plotW} y1={ROW_H} y2={ROW_H} stroke="#e2e6ec" strokeOpacity={0.5} />
                  {r.planned && <rect x={x(r.planned[0])} y={4} width={Math.max(2, x(r.planned[1]) - x(r.planned[0]))} height={4} rx={2} fill="#aab3c0" />}
                  {r.forecast && <rect data-testid={`timeline-forecast-${r.id}`} x={x(r.forecast[0])} y={11} width={Math.max(2, x(r.forecast[1]) - x(r.forecast[0]))} height={11} rx={4} fill="#5b8fb5" fillOpacity={0.18} stroke="#5b8fb5" strokeDasharray="4 3" />}
                  {r.actual && (diamond
                    ? <rect x={x(r.actual[1]) - 6} y={11} width={12} height={12} transform={`rotate(45 ${x(r.actual[1])} 17)`} fill={c} />
                    : (
                      <g>
                        <rect x={x(r.actual[0])} y={11} width={Math.max(3, x(r.actual[1]) - x(r.actual[0]))} height={11} rx={4} fill={c} fillOpacity={r.kind === "node" ? 1 : 0.55} />
                        {r.human && <rect x={x(r.actual[0])} y={11} width={Math.max(3, x(r.actual[1]) - x(r.actual[0]))} height={11} rx={4} fill="url(#pv-hatch)" />}
                      </g>
                    ))}
                </g>
              );
            })}
            <line data-testid="timeline-now-line" x1={x(now)} x2={x(now)} y1={0} y2={H} stroke="#3451b2" strokeWidth={2} />
          </svg>
        </div>
      </div>
    </div>
  );
}

function Legend({ color, label, dashed, hatched }: { color: string; label: string; dashed?: boolean; hatched?: boolean }) {
  return (
    <span className="flex items-center gap-1">
      <span className="inline-block h-2.5 w-4 rounded" style={{ background: hatched ? `repeating-linear-gradient(45deg, ${color}, ${color} 2px, transparent 2px, transparent 4px)` : dashed ? "transparent" : color, border: dashed ? `1px dashed ${color}` : undefined }} />
      {label}
    </span>
  );
}
