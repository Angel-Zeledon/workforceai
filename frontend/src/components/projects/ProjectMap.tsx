"use client";
import { memo, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useT } from "@/lib/i18n";
import { isGroup, leaves } from "@/lib/projects/calc";
import {
  LARGE_MAP, NODE_H as NH, NODE_W as NW, centerOn, clampZoom, edgesInView, initialZoom, layoutLayers, lodFor, nodesInView, rectKey, viewRect, zoomAround,
  type Layout, type Lod, type Rect,
} from "@/lib/projects/mapLod";
import type { ProjectNode, ProjectObjective } from "@/lib/projects/types";
import { useProjects } from "@/lib/projects/store";
import { Card } from "../ui";
import { CRITICAL_COLOR, NODE_STATE_COLOR, nodeColor, useNodeTitle } from "./shared";

const MINIMAP_W = 180;
const MINIMAP_MAX_H = 120;

/**
 * Dependency map: layered DAG in SVG with critical path, selection, search and semantic zoom (LOD: full cards >
 * chips > dots). It scales to thousands of tasks: only the nodes and edges inside the visible tiles are mounted
 * (`data-rendered` on the canvas), at "dot" LOD only critical/selected edges are drawn, large maps open zoomed out
 * with a canvas minimap, and Ctrl/⌘ + wheel zooms around the pointer. Geometry lives in lib/projects/mapLod.ts.
 * Still deferred from the spec: collapsible groups loaded level by level and the ELK layout in a worker.
 */
export function ProjectMap() {
  const { t } = useT();
  const detail = useProjects((s) => s.detail)!;
  const health = useProjects((s) => s.health);
  const selected = useProjects((s) => s.selectedNodeId);
  const select = useProjects((s) => s.selectNode);
  const title = useNodeTitle();
  const ls = useMemo(() => leaves(detail.nodes), [detail.nodes]);
  const large = ls.length > LARGE_MAP;
  const [zoom, setZoom] = useState(() => initialZoom(ls.length));
  const [q, setQ] = useState("");
  const [pinned, setPinned] = useState<string | null>(null);
  const scroller = useRef<HTMLDivElement>(null);
  const [view, setView] = useState<Rect>(() => viewRect(0, 0, 1200, 560, zoom));
  const viewKey = useRef(rectKey(view));
  const pendingScroll = useRef<{ left: number; top: number } | null>(null);
  const [tick, setTick] = useState(0); // scroll position changes (minimap viewport)

  // Geometry depends on the structure only: state changes do not re-layout.
  const structure = useMemo(() => ls.map((n) => `${n.id}:${n.dag_level}:${n.wbs_path}`).join("|"), [ls]);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const layout = useMemo<Layout>(() => layoutLayers(ls), [structure]);
  const crit = useMemo(() => new Set(health?.critical_path.node_ids ?? []), [health]);
  const lod = lodFor(zoom);

  const measure = useCallback(() => {
    const el = scroller.current;
    if (!el) return;
    const r = viewRect(el.scrollLeft, el.scrollTop, el.clientWidth || 1200, el.clientHeight || 560, zoom);
    const k = rectKey(r);
    if (k !== viewKey.current) { viewKey.current = k; setView(r); }
    if (large) setTick((x) => x + 1); // only the minimap needs every scroll position
  }, [zoom, large]);

  useEffect(() => {
    const el = scroller.current;
    if (!el) return;
    let raf = 0;
    const onScroll = () => { cancelAnimationFrame(raf); raf = requestAnimationFrame(measure); };
    el.addEventListener("scroll", onScroll, { passive: true });
    const ro = new ResizeObserver(onScroll);
    ro.observe(el);
    measure();
    return () => { el.removeEventListener("scroll", onScroll); ro.disconnect(); cancelAnimationFrame(raf); };
  }, [measure]);

  // Apply a scroll computed together with a zoom change once the SVG has its new size.
  useLayoutEffect(() => {
    const el = scroller.current; const p = pendingScroll.current;
    if (el && p) { el.scrollLeft = p.left; el.scrollTop = p.top; pendingScroll.current = null; measure(); }
  }, [zoom, measure]);

  const zoomTo = useCallback((next: number, anchor?: { x: number; y: number }) => {
    const el = scroller.current;
    const to = clampZoom(next);
    if (!el || to === zoom) return;
    const a = anchor ?? { x: el.clientWidth / 2, y: el.clientHeight / 2 };
    pendingScroll.current = zoomAround(el.scrollLeft, el.scrollTop, a.x, a.y, zoom, to);
    setZoom(to);
  }, [zoom]);

  // Ctrl/⌘ + wheel (and trackpad pinch) zooms around the pointer; a plain wheel keeps scrolling.
  useEffect(() => {
    const el = scroller.current;
    if (!el) return;
    const onWheel = (e: WheelEvent) => {
      if (!e.ctrlKey && !e.metaKey) return;
      e.preventDefault();
      const box = el.getBoundingClientRect();
      zoomTo(zoom * Math.exp(-e.deltaY * 0.002), { x: e.clientX - box.left, y: e.clientY - box.top });
    };
    el.addEventListener("wheel", onWheel, { passive: false });
    return () => el.removeEventListener("wheel", onWheel);
  }, [zoom, zoomTo]);

  const scrollToNode = useCallback((id: string) => {
    const el = scroller.current; const p = layout.pos.get(id);
    if (!el || !p) return;
    const c = centerOn(p, el.clientWidth, el.clientHeight, zoom);
    el.scrollTo({ left: c.left, top: c.top, behavior: "smooth" });
  }, [layout, zoom]);

  const always = useMemo(() => new Set([selected, pinned].filter(Boolean) as string[]), [selected, pinned]);
  const shown = useMemo(() => nodesInView(ls, layout.pos, view, always) as ProjectNode[], [ls, layout, view, always]);
  const edges = useMemo(() => edgesInView(ls, layout.pos, view, lod, crit, selected), [ls, layout, view, lod, crit, selected]);

  const sel = ls.find((n) => n.id === selected);
  const crumb = sel ? [detail.objectives.find((o) => o.id === sel.objective_id), detail.nodes.find((g) => g.id === sel.parent_id && isGroup(g)), sel] : [];
  const find = () => {
    const needle = q.trim().toLowerCase();
    if (!needle) return;
    const hit = ls.find((n) => title(n).toLowerCase().includes(needle));
    if (hit) { setPinned(hit.id); select(hit.id); scrollToNode(hit.id); }
  };

  return (
    <div className="space-y-3">
      <HeatStrip objectives={detail.objectives} nodes={ls} large={large} title={title} />
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
          <button type="button" data-testid="map-zoom-out" aria-label={t("pv.map.zoomOut")} onClick={() => zoomTo(zoom - (zoom > 0.5 ? 0.2 : 0.1))} className="rounded-md border border-line px-2.5 text-xs font-semibold text-ink">−</button>
          <span data-testid="map-zoom" className="w-9 text-center font-mono text-[10px] text-mute">{Math.round(zoom * 100)}%</span>
          <button type="button" data-testid="map-zoom-in" aria-label={t("pv.map.zoomIn")} onClick={() => zoomTo(zoom + (zoom >= 0.5 ? 0.2 : 0.1))} className="rounded-md border border-line px-2.5 text-xs font-semibold text-ink">+</button>
        </span>
      }>
        <div>
          <div ref={scroller} data-testid="map-canvas" data-lod={lod} data-rendered={shown.length} className="max-h-[560px] overflow-auto bg-panel2">
            <svg width={layout.w * zoom} height={layout.h * zoom} viewBox={`0 0 ${layout.w} ${layout.h}`}>
              {edges.map((e) => {
                const mx = (e.x1 + e.x2) / 2;
                return <path key={`${e.from}>${e.to}`} d={`M${e.x1} ${e.y1} C${mx} ${e.y1} ${mx} ${e.y2} ${e.x2} ${e.y2}`} fill="none"
                  stroke={e.critical ? CRITICAL_COLOR : e.hot ? "#151b26" : "#d9c79b"} strokeWidth={e.critical ? 3 : e.hot ? 2 : 1.2} />;
              })}
              {shown.map((n) => (
                <MapNode key={n.id} n={n} x={layout.pos.get(n.id)!.x} y={layout.pos.get(n.id)!.y} lod={lod} label={title(n)}
                  selected={selected === n.id} critical={crit.has(n.id)} onSelect={select} />
              ))}
            </svg>
          </div>
        </div>
        {large && (
          <div className="flex items-end justify-end gap-2 border-t border-line px-4 py-2">
            <span className="text-[10px] text-mute">{t("pv.map.minimap")}</span>
            <Minimap layout={layout} nodes={ls} scroller={scroller} zoom={zoom} tick={tick} label={t("pv.map.minimap")} />
          </div>
        )}
        <div className="flex flex-wrap gap-3 border-t border-line px-4 py-2 text-[10px] text-mute">
          {(["pending", "ready", "running", "awaiting_approval", "done", "failed"] as const).map((s) => (
            <span key={s} className="flex items-center gap-1"><span className="h-2 w-2 rounded-full" style={{ background: NODE_STATE_COLOR[s] }} />{t(`pv.state.${s}`)}</span>
          ))}
          <span className="flex items-center gap-1"><span className="h-0.5 w-4" style={{ background: CRITICAL_COLOR }} />{t("pv.map.critical")}</span>
          {large && <span className="ml-auto">{t("pv.map.zoomHint")}</span>}
        </div>
      </Card>
    </div>
  );
}

/** One task. Memoized: a delta on another node, or a scroll inside the same tile, does not re-render it. */
const MapNode = memo(function MapNode({ n, x, y, lod, label, selected, critical, onSelect }: {
  n: ProjectNode; x: number; y: number; lod: Lod; label: string; selected: boolean; critical: boolean; onSelect: (id: string) => void;
}) {
  const c = nodeColor(n);
  return (
    <g data-testid={`map-node-${n.id}`} data-state={n.state} data-selected={selected ? "true" : "false"} transform={`translate(${x} ${y})`} className="cursor-pointer" onClick={() => onSelect(n.id)}>
      {lod === "dot" ? (
        <>
          <title>{label}</title>
          <circle cx={NW / 2} cy={NH / 2} r={critical || selected ? 14 : 11} fill={c} stroke={selected ? "#151b26" : critical ? CRITICAL_COLOR : "none"} strokeWidth={4} />
        </>
      ) : (
        <>
          <rect width={NW} height={NH} rx={12} fill="#ffffff" stroke={selected ? "#151b26" : critical ? CRITICAL_COLOR : c} strokeWidth={selected ? 3 : 2} strokeDasharray={n.kind === "wait" ? "4 3" : undefined} />
          <rect x={0} y={0} width={n.progress > 0 && n.state !== "done" ? (NW * n.progress) / 100 : n.state === "done" ? NW : 0} height={5} rx={2.5} fill={c} />
          <circle cx={12} cy={NH / 2 + 2} r={4.5} fill={c} />
          <text x={22} y={NH / 2 + 6} fontSize={lod === "mini" ? 12 : 11} fontWeight={700} fill="#151b26">{clip(label, lod === "mini" ? 21 : 22)}</text>
          {lod === "full" && n.state === "awaiting_approval" && <text x={NW - 16} y={16} fontSize={12}>✋</text>}
        </>
      )}
    </g>
  );
});

/** Progress strip per objective. Large maps draw it on one canvas instead of one element per task. */
function HeatStrip({ objectives, nodes, large, title }: { objectives: ProjectObjective[]; nodes: ProjectNode[]; large: boolean; title: (n: { title: string; title_key?: string; title_params?: Record<string, string> }) => string }) {
  const ref = useRef<HTMLCanvasElement>(null);
  const byObjective = useMemo(() => {
    const m = new Map<string, ProjectNode[]>();
    for (const n of nodes) { const a = m.get(n.objective_id); if (a) a.push(n); else m.set(n.objective_id, [n]); }
    return m;
  }, [nodes]);
  useEffect(() => {
    const cv = ref.current;
    if (!large || !cv) return;
    const w = cv.clientWidth || 600;
    cv.width = w; cv.height = 12;
    const ctx = cv.getContext("2d");
    if (!ctx) return;
    ctx.clearRect(0, 0, w, 12);
    const seg = w / Math.max(1, objectives.length);
    objectives.forEach((o, oi) => {
      const arr = byObjective.get(o.id) ?? [];
      const step = (seg - 2) / Math.max(1, arr.length);
      arr.forEach((n, i) => { ctx.fillStyle = nodeColor(n); ctx.fillRect(oi * seg + i * step, 0, Math.max(1, step), 12); });
    });
  }, [large, objectives, byObjective]);

  if (large) return <div data-testid="project-heat-strip" className="h-3 overflow-hidden rounded-md border border-line"><canvas ref={ref} className="block h-full w-full" /></div>;
  return (
    <div data-testid="project-heat-strip" className="flex h-3 overflow-hidden rounded-md border border-line">
      {objectives.map((o) => (
        <div key={o.id} className="flex flex-1 border-r border-bg last:border-r-0" title={title(o)}>
          {(byObjective.get(o.id) ?? []).map((n) => <div key={n.id} className="flex-1" style={{ background: nodeColor(n) }} />)}
        </div>
      ))}
    </div>
  );
}

/** Whole-map overview on a single canvas (one fillRect per task) with the visible area; click or drag to pan. */
function Minimap({ layout, nodes, scroller, zoom, tick, label }: { layout: Layout; nodes: ProjectNode[]; scroller: React.RefObject<HTMLDivElement | null>; zoom: number; tick: number; label: string }) {
  const ref = useRef<HTMLCanvasElement>(null);
  const k = Math.min(MINIMAP_W / layout.w, MINIMAP_MAX_H / layout.h);
  const w = Math.max(40, Math.round(layout.w * k));
  const h = Math.max(20, Math.round(layout.h * k));
  useEffect(() => {
    const cv = ref.current; const el = scroller.current;
    const ctx = cv?.getContext("2d");
    if (!cv || !ctx) return;
    ctx.clearRect(0, 0, w, h);
    for (const n of nodes) {
      const p = layout.pos.get(n.id);
      if (!p) continue;
      ctx.fillStyle = nodeColor(n);
      ctx.fillRect(p.x * k, p.y * k, Math.max(1.5, NW * k), Math.max(1.5, NH * k));
    }
    if (el) {
      ctx.strokeStyle = "#151b26"; ctx.lineWidth = 1.5;
      ctx.strokeRect((el.scrollLeft / zoom) * k, (el.scrollTop / zoom) * k, (el.clientWidth / zoom) * k, (el.clientHeight / zoom) * k);
    }
  }, [layout, nodes, k, w, h, zoom, tick, scroller]);

  const pan = (e: React.PointerEvent<HTMLCanvasElement>) => {
    const el = scroller.current;
    if (!el || (e.type === "pointermove" && e.buttons !== 1)) return;
    const box = e.currentTarget.getBoundingClientRect();
    const x = (e.clientX - box.left) / k; const y = (e.clientY - box.top) / k;
    el.scrollLeft = x * zoom - el.clientWidth / 2;
    el.scrollTop = y * zoom - el.clientHeight / 2;
  };
  return (
    <canvas ref={ref} data-testid="map-minimap" width={w} height={h} aria-label={label} role="img" onPointerDown={pan} onPointerMove={pan}
      className="cursor-crosshair rounded-md border border-line bg-white" style={{ width: w, height: h }} />
  );
}

const clip = (s: string, n: number) => (s.length > n ? `${s.slice(0, n - 1)}…` : s);
