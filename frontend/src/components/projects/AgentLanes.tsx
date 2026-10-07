"use client";
import { useMemo } from "react";
import { useT } from "@/lib/i18n";
import { fmtDuration, isDoneState, isRetrying, leaves, simulate } from "@/lib/projects/calc";
import { useProjects } from "@/lib/projects/store";
import type { ProjectNode } from "@/lib/projects/types";
import { useAgentColor, useAgentName, useTicker } from "../ui";
import { NODE_STATE_COLOR, nodeColor, useNodeTitle } from "./shared";

const W = 900;
const BAR_H = 18;
const HUMAN = "__human__";
interface Bar { n: ProjectNode; s: number; e: number; forecast: boolean; lane: number }

/** Swimlanes per agent plus a "Human" lane (gates and approvals), with load histogram and queue counters. */
export function AgentLanes() {
  const { t } = useT();
  useTicker(1000);
  const detail = useProjects((s) => s.detail)!;
  const select = useProjects((s) => s.selectNode);
  const title = useNodeTitle();
  const agentName = useAgentName();
  const agentColor = useAgentColor();
  const now = Date.now();
  const t0 = detail.project.started_at ? Date.parse(detail.project.started_at) : now;

  const model = useMemo(() => {
    const sim = simulate(detail.nodes, Date.now());
    const ls = leaves(detail.nodes);
    const bars = new Map<string, Bar[]>();
    for (const n of ls) {
      if (n.state === "cancelled") continue;
      const lane = n.kind === "gate" || n.state === "awaiting_approval" ? HUMAN : n.agent_id;
      if (!lane) continue;
      const started = n.started_at ? Date.parse(n.started_at) : NaN;
      const s = !Number.isNaN(started) ? started : sim.start.get(n.id) ?? Date.now();
      const e = isDoneState(n.state) ? (n.finished_at ? Date.parse(n.finished_at) : s) : n.state === "running" ? Date.now() : sim.end.get(n.id) ?? s;
      const arr = bars.get(lane) ?? []; arr.push({ n, s, e: Math.max(e, s + 800), forecast: Number.isNaN(started), lane: 0 }); bars.set(lane, arr);
    }
    const all = Array.from(bars.values()).flat();
    const start = Math.min(t0, ...all.map((b) => b.s));
    const end = Math.max(Date.now(), sim.makespanEnd, ...all.map((b) => b.e)) + 2000;
    for (const arr of bars.values()) {
      arr.sort((a, b) => a.s - b.s);
      const ends: number[] = [];
      for (const b of arr) { let i = ends.findIndex((x) => x <= b.s); if (i < 0) { i = ends.length; ends.push(0); } ends[i] = b.e; b.lane = i; }
    }
    return { bars, start, end };
  }, [detail.nodes, t0]);

  const { bars, start, end } = model;
  const x = (ms: number) => ((ms - start) / (end - start)) * W;
  const agents = Array.from(bars.keys()).filter((k) => k !== HUMAN).sort();
  const lanes = [...agents, ...(bars.has(HUMAN) ? [HUMAN] : [])];

  if (!lanes.length) return <div className="text-sm text-mute">{t("pv.lanes.empty")}</div>;
  return (
    <div className="space-y-2">
      {lanes.map((id) => {
        const arr = bars.get(id) ?? [];
        const sub = Math.max(1, ...arr.map((b) => b.lane + 1));
        const h = sub * (BAR_H + 4) + 8;
        const nodesOf = arr.map((b) => b.n);
        const active = nodesOf.filter((n) => n.state === "running" || n.state === "awaiting_approval").length;
        const queued = nodesOf.filter((n) => n.state === "ready" || n.state === "pending").length;
        const color = id === HUMAN ? NODE_STATE_COLOR.awaiting_approval : agentColor(id);
        // concurrency histogram
        const buckets = 40; const hist = new Array(buckets).fill(0);
        for (const b of arr) for (let k = 0; k < buckets; k++) { const bs = start + ((end - start) * k) / buckets; const be = start + ((end - start) * (k + 1)) / buckets; if (b.s < be && b.e > bs) hist[k]++; }
        const hm = Math.max(1, ...hist);
        return (
          <div key={id} data-testid={`lane-${id === HUMAN ? "human" : id}`} className="flex overflow-hidden rounded-xl border border-line bg-panel shadow-pop">
            <div className="w-[150px] shrink-0 border-r border-line bg-panel2 p-2.5">
              <div className="flex items-center gap-1.5 text-[12px] font-semibold text-ink"><span className="h-2.5 w-2.5 rounded-full" style={{ background: color }} />{id === HUMAN ? t("pv.human") : agentName(id).split(" ")[0]}</div>
              <div data-testid={`lane-load-${id === HUMAN ? "human" : id}`} data-active={active} data-queued={queued} className="mt-0.5 text-[10px] text-mute">{t("pv.lanes.load", { active, queued })}</div>
              <svg width={110} height={18} className="mt-1">
                {hist.map((v, k) => <rect key={k} x={k * 2.75} y={18 - (v / hm) * 16} width={2.2} height={(v / hm) * 16} fill={color} opacity={0.6} />)}
              </svg>
            </div>
            <div className="min-w-0 flex-1 overflow-x-auto">
              <svg width={W} height={h} className="block">
                <line data-testid={id === agents[0] ? "lanes-now-line" : undefined} x1={x(now)} x2={x(now)} y1={0} y2={h} stroke="#3451b2" strokeWidth={1.5} />
                {arr.map((b) => {
                  const bx = x(b.s); const bw = Math.max(4, x(b.e) - bx); const by = 4 + b.lane * (BAR_H + 4);
                  const c = nodeColor(b.n);
                  return (
                    <svg key={b.n.id} x={bx} y={by} width={bw} height={BAR_H} className="cursor-pointer" onClick={() => select(b.n.id)}>
                      <title>{`${title(b.n)} · ${fmtDuration((b.e - b.s) / 1000)}`}</title>
                      <rect width={bw} height={BAR_H} rx={5} fill={c} fillOpacity={b.forecast ? 0.2 : 0.95} stroke={c} strokeDasharray={b.forecast ? "4 3" : undefined} />
                      {isRetrying(b.n) && <rect width={bw} height={BAR_H} rx={5} fill="none" stroke="#b8651f" strokeWidth={2} />}
                      {bw > 54 && <text x={5} y={12} fontSize={9.5} fontWeight={700} fill={b.forecast ? "#151b26" : "#fff"}>{title(b.n)}</text>}
                    </svg>
                  );
                })}
              </svg>
            </div>
          </div>
        );
      })}
    </div>
  );
}
