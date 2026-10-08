/**
 * Geometry of the project map (components/projects/ProjectMap.tsx): layered layout, semantic zoom (LOD) and
 * viewport culling, so a project with thousands of tasks keeps a bounded DOM (docs/architecture/workflow-visualization.md
 * sec. 4.2). Pure functions without imports: unit-tested with `node --test` (mapLod.test.mjs).
 */

export type Lod = "dot" | "mini" | "full";

export const NODE_W = 168;
export const NODE_H = 40;
export const GAP_X = 64;
export const GAP_Y = 14;
export const PAD = 20;
/** From this many tasks the map opens zoomed out and shows the minimap. */
export const LARGE_MAP = 150;
export const MIN_ZOOM = 0.2;
export const MAX_ZOOM = 1.6;
/** Culling works on tiles of this size (layout px) so scrolling re-renders only when a tile boundary is crossed. */
export const TILE = 256;

/** Semantic zoom: dots (no labels, only critical/selected edges) < 0.5 <= chips < 0.8 <= full cards. */
export function lodFor(zoom: number): Lod {
  return zoom < 0.5 ? "dot" : zoom < 0.8 ? "mini" : "full";
}

/** Opening zoom by size: small projects as before (1), large ones as chips, very large ones as dots. */
export function initialZoom(count: number): number {
  return count > 600 ? 0.4 : count > LARGE_MAP ? 0.7 : 1;
}

export const clampZoom = (z: number) => Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, Math.round(z * 100) / 100));

export interface MapNodeLike { id: string; dag_level: number; wbs_path: string; depends_on: string[] }
export interface Point { x: number; y: number }
export interface Layout { pos: Map<string, Point>; w: number; h: number }

/** One column per DAG level, rows ordered by WBS path. O(n log n) (no array copies per node). */
export function layoutLayers(nodes: readonly MapNodeLike[]): Layout {
  const cols = new Map<number, MapNodeLike[]>();
  let maxLvl = 0;
  for (const n of nodes) {
    let arr = cols.get(n.dag_level);
    if (!arr) { arr = []; cols.set(n.dag_level, arr); }
    arr.push(n);
    if (n.dag_level > maxLvl) maxLvl = n.dag_level;
  }
  const pos = new Map<string, Point>();
  let maxRows = 0;
  for (const [lvl, arr] of cols) {
    arr.sort((a, b) => (a.wbs_path < b.wbs_path ? -1 : a.wbs_path > b.wbs_path ? 1 : 0));
    arr.forEach((n, i) => pos.set(n.id, { x: PAD + lvl * (NODE_W + GAP_X), y: PAD + i * (NODE_H + GAP_Y) }));
    maxRows = Math.max(maxRows, arr.length);
  }
  return { pos, w: 2 * PAD + (maxLvl + 1) * (NODE_W + GAP_X), h: 2 * PAD + maxRows * (NODE_H + GAP_Y) };
}

export interface Rect { x0: number; y0: number; x1: number; y1: number }

/** The visible area of the scroll container in layout coordinates, grown by `margin` screen px and snapped to tiles. */
export function viewRect(scrollLeft: number, scrollTop: number, width: number, height: number, zoom: number, margin = 200): Rect {
  const z = zoom > 0 ? zoom : 1;
  const x0 = (scrollLeft - margin) / z;
  const y0 = (scrollTop - margin) / z;
  const x1 = (scrollLeft + width + margin) / z;
  const y1 = (scrollTop + height + margin) / z;
  return { x0: Math.floor(x0 / TILE) * TILE, y0: Math.floor(y0 / TILE) * TILE, x1: Math.ceil(x1 / TILE) * TILE, y1: Math.ceil(y1 / TILE) * TILE };
}

export const rectKey = (r: Rect) => `${r.x0},${r.y0},${r.x1},${r.y1}`;

const hit = (r: Rect, x0: number, y0: number, x1: number, y1: number) => x1 >= r.x0 && x0 <= r.x1 && y1 >= r.y0 && y0 <= r.y1;

/** Ids of the nodes whose card intersects the view, plus the ones in `always` (selection, search hit). */
export function nodesInView(nodes: readonly MapNodeLike[], pos: Map<string, Point>, r: Rect, always: ReadonlySet<string> = new Set()): MapNodeLike[] {
  const out: MapNodeLike[] = [];
  for (const n of nodes) {
    const p = pos.get(n.id);
    if (!p) continue;
    if (always.has(n.id) || hit(r, p.x, p.y, p.x + NODE_W, p.y + NODE_H)) out.push(n);
  }
  return out;
}

export interface MapEdge { from: string; to: string; x1: number; y1: number; x2: number; y2: number; critical: boolean; hot: boolean }

/**
 * Edges to draw: those whose bounding box meets the view. At "dot" LOD only the critical path and the edges of the
 * selected node are drawn (the rest is noise at that scale).
 */
export function edgesInView(nodes: readonly MapNodeLike[], pos: Map<string, Point>, r: Rect, lod: Lod, critical: ReadonlySet<string>, selected: string | null): MapEdge[] {
  const out: MapEdge[] = [];
  for (const n of nodes) {
    const b = pos.get(n.id);
    if (!b) continue;
    for (const d of n.depends_on) {
      const a = pos.get(d);
      if (!a) continue;
      const crit = critical.has(d) && critical.has(n.id);
      const hot = selected === n.id || selected === d;
      if (lod === "dot" && !crit && !hot) continue;
      const x1 = a.x + NODE_W, y1 = a.y + NODE_H / 2, x2 = b.x, y2 = b.y + NODE_H / 2;
      if (!hot && !hit(r, Math.min(x1, x2), Math.min(y1, y2), Math.max(x1, x2), Math.max(y1, y2))) continue;
      out.push({ from: d, to: n.id, x1, y1, x2, y2, critical: crit, hot });
    }
  }
  return out;
}

/** Scroll offsets that center layout point p in a viewport of (width, height) at `zoom`. */
export function centerOn(p: Point, width: number, height: number, zoom: number): { left: number; top: number } {
  return { left: Math.max(0, (p.x + NODE_W / 2) * zoom - width / 2), top: Math.max(0, (p.y + NODE_H / 2) * zoom - height / 2) };
}

/** New scroll offsets after a zoom change, keeping the layout point under the anchor (screen px inside the viewport) fixed. */
export function zoomAround(scrollLeft: number, scrollTop: number, anchorX: number, anchorY: number, from: number, to: number): { left: number; top: number } {
  const k = to / from;
  return { left: Math.max(0, (scrollLeft + anchorX) * k - anchorX), top: Math.max(0, (scrollTop + anchorY) * k - anchorY) };
}
