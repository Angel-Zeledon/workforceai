"use client";
import { useMemo, useState } from "react";
import { useT } from "@/lib/i18n";
import { isGroup, leaves } from "@/lib/projects/calc";
import { useProjects } from "@/lib/projects/store";
import { Card } from "../ui";
import { CRITICAL_COLOR, NODE_STATE_COLOR, nodeColor, useNodeTitle } from "./shared";

const NW = 168;
const NH = 40;
const GX = 64;
const GY = 14;

/**
 * Dependency map (small/medium projects): layered DAG in plain SVG with critical path, selection and zoom-based detail (LOD).
 * The large-project version (collapsible groups, ELK layout in a worker, minimap, 600-node cap) is deferred: see
 * "Frontend delivery status" in docs/architecture/workflow-visualization.md.
 */
export function ProjectMap() {
  const { t } = useT();
  const detail = useProjects((s) => s.detail)!;
  const health = useProjects((s) => s.health);
  const selected = useProjects((s) => s.selectedNodeId);
  const select = useProjects((s) => s.selectNode);
  const title = useNodeTitle();
  const [zoom, setZoom] = useState(1);
  const [q, setQ] = useState("");
  const ls = useMemo(() => leaves(detail.nodes), [detail.nodes]);

  const layout = useMemo(() => {
    const pos = new Map<string, { x: number; y: number }>();
    const cols = new Map<number, typeof ls>();
    for (const n of ls) cols.set(n.dag_level, [...(cols.get(n.dag_level) ?? []), n]);
    let maxRows = 0;
    for (const [lvl, arr] of cols) {
      arr.sort((a, b) => a.wbs_path.localeCompare(b.wbs_path));
      arr.forEach((n, i) => pos.set(n.id, { x: 20 + lvl * (NW + GX), y: 20 + i * (NH + GY) }));
      maxRows = Math.max(maxRows, arr.length);
    }
    const maxLvl = Math.max(0, ...ls.map((n) => n.dag_level));
    return { pos, w: 40 + (maxLvl + 1) * (NW + GX), h: 40 + maxRows * (NH + GY) };
  }, [ls]);

  const crit = new Set(health?.critical_path.node_ids ?? []);
  const lod = zoom < 0.5 ? "dot" : zoom < 0.8 ? "mini" : "full";
  const sel = ls.find((n) => n.id === selected);
  const crumb = sel ? [detail.objectives.find((o) => o.id === sel.objective_id), detail.nodes.find((g) => g.id === sel.parent_id && isGroup(g)), sel] : [];
  const find = () => {
    const needle = q.trim().toLowerCase();
    if (!needle) return;
    const hit = ls.find((n) => title(n).toLowerCase().includes(needle));
    if (hit) { select(hit.id); document.querySelector(`[data-testid="map-node-${hit.id}"]`)?.scrollIntoView({ block: "center", inline: "center", behavior: "smooth" }); }
  };

  return (
    <div className="space-y-3">
      <div data-testid="project-heat-strip" className="flex h-3 overflow-hidden rounded-md border border-line">
        {detail.objectives.map((o) => (
          <div key={o.id} className="flex flex-1 border-r border-bg last:border-r-0" title={title(o)}>
            {ls.filter((n) => n.objective_id === o.id).map((n) => <div key={n.id} className="flex-1" style={{ background: nodeColor(n) }} />)}
          </div>
        ))}
      </div>
      <Card className="!p-0" title={
        <span className="flex items-center gap-3 normal-case tracking-normal">
          <span data-testid="map-breadcrumb" className="text-[11px] font-semibold text-mute">
            {detail.project.name_key ? t(detail.project.name_key) : detail.project.name}{crumb.map((c, i) => c && <span key={i}> › {title(c)}</span>)}
          </span>
          <span data-testid="map-node-count" data-count={ls.length} className="font-mono text-[10px] text-mute">{t("pv.card.nodes", { count: ls.length })}</span>
        </span>
      } right={
        <span className="flex items-center gap-1.5">
          <input data-testid="map-search-input" value={q} onChange={(e) => setQ(e.target.value)} onKeyDown={(e) => { if (e.key === "Enter") find(); }} placeholder={t("pv.map.search")}
            className="w-40 rounded-md border border-line-strong bg-panel px-3 py-1 text-[11px] text-ink outline-none focus:border-accent" />
          <button type="button" onClick={() => setZoom((z) => Math.max(0.3, +(z - 0.2).toFixed(1)))} className="rounded-md border border-line px-2.5 text-xs font-semibold text-ink">−</button>
          <button type="button" onClick={() => setZoom((z) => Math.min(1.6, +(z + 0.2).toFixed(1)))} className="rounded-md border border-line px-2.5 text-xs font-semibold text-ink">+</button>
        </span>
      }>
        <div data-testid="map-canvas" data-lod={lod} className="max-h-[560px] overflow-auto bg-panel2">
          <svg width={layout.w * zoom} height={layout.h * zoom} viewBox={`0 0 ${layout.w} ${layout.h}`}>
            {ls.flatMap((n) => n.depends_on.map((d) => {
              const a = layout.pos.get(d); const b = layout.pos.get(n.id);
              if (!a || !b) return null;
              const x1 = a.x + NW; const y1 = a.y + NH / 2; const x2 = b.x; const y2 = b.y + NH / 2; const mx = (x1 + x2) / 2;
              const onCrit = crit.has(d) && crit.has(n.id);
              const hot = selected === n.id || selected === d;
              return <path key={`${d}>${n.id}`} d={`M${x1} ${y1} C${mx} ${y1} ${mx} ${y2} ${x2} ${y2}`} fill="none" stroke={onCrit ? CRITICAL_COLOR : hot ? "#151b26" : "#d9c79b"} strokeWidth={onCrit ? 3 : hot ? 2 : 1.2} />;
            }))}
            {ls.map((n) => {
              const p = layout.pos.get(n.id)!;
              const c = nodeColor(n);
              const isSel = selected === n.id;
              return (
                <g key={n.id} data-testid={`map-node-${n.id}`} data-state={n.state} data-selected={isSel ? "true" : "false"} transform={`translate(${p.x} ${p.y})`} className="cursor-pointer" onClick={() => select(n.id)}>
                  {lod === "dot" ? <circle cx={NW / 2} cy={NH / 2} r={9} fill={c} stroke={isSel ? "#151b26" : "none"} strokeWidth={3} /> : (
                    <>
                      <rect width={NW} height={NH} rx={12} fill="#ffffff" stroke={isSel ? "#151b26" : crit.has(n.id) ? CRITICAL_COLOR : c} strokeWidth={isSel ? 3 : 2} strokeDasharray={n.kind === "wait" ? "4 3" : undefined} />
                      <rect x={0} y={0} width={n.progress > 0 && n.state !== "done" ? (NW * n.progress) / 100 : n.state === "done" ? NW : 0} height={5} rx={2.5} fill={c} />
                      <circle cx={12} cy={NH / 2 + 2} r={4.5} fill={c} />
                      <text x={22} y={NH / 2 + 6} fontSize={11} fontWeight={700} fill="#151b26">{clip(title(n), lod === "mini" ? 20 : 22)}</text>
                      {lod === "full" && n.state === "awaiting_approval" && <text x={NW - 16} y={16} fontSize={12}>✋</text>}
                    </>
                  )}
                </g>
              );
            })}
          </svg>
        </div>
        <div className="flex flex-wrap gap-3 border-t border-line px-4 py-2 text-[10px] text-mute">
          {(["pending", "ready", "running", "awaiting_approval", "done", "failed"] as const).map((s) => (
            <span key={s} className="flex items-center gap-1"><span className="h-2 w-2 rounded-full" style={{ background: NODE_STATE_COLOR[s] }} />{t(`pv.state.${s}`)}</span>
          ))}
          <span className="flex items-center gap-1"><span className="h-0.5 w-4" style={{ background: CRITICAL_COLOR }} />{t("pv.map.critical")}</span>
        </div>
      </Card>
    </div>
  );
}

const clip = (s: string, n: number) => (s.length > n ? `${s.slice(0, n - 1)}…` : s);
