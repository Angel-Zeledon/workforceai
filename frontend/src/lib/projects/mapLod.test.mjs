// Unit tests of the project map geometry (run: npm run test:unit; Node >= 22.18 strips the TypeScript types).
import assert from "node:assert/strict";
import { test } from "node:test";
import {
  LARGE_MAP, NODE_H, NODE_W, TILE, centerOn, clampZoom, edgesInView, initialZoom, layoutLayers, lodFor, nodesInView, rectKey, viewRect, zoomAround,
} from "./mapLod.ts";

/** A synthetic project: `levels` DAG layers of `rows` tasks, each depending on the task above-left. */
function grid(levels, rows) {
  const out = [];
  for (let l = 0; l < levels; l++) {
    for (let r = 0; r < rows; r++) {
      out.push({ id: `n${l}-${r}`, dag_level: l, wbs_path: `${String(r).padStart(4, "0")}`, depends_on: l > 0 ? [`n${l - 1}-${r}`] : [] });
    }
  }
  return out;
}

test("LOD thresholds and opening zoom", () => {
  assert.equal(lodFor(1), "full");
  assert.equal(lodFor(0.7), "mini");
  assert.equal(lodFor(0.4), "dot");
  assert.equal(initialZoom(20), 1, "small projects open as before");
  assert.equal(lodFor(initialZoom(LARGE_MAP + 1)), "mini");
  assert.equal(lodFor(initialZoom(5000)), "dot");
  assert.equal(clampZoom(0.01), 0.2);
  assert.equal(clampZoom(9), 1.6);
});

test("layered layout places every node once, ordered by WBS path", () => {
  const nodes = grid(3, 4).reverse();
  const { pos, w, h } = layoutLayers(nodes);
  assert.equal(pos.size, 12);
  assert.ok(pos.get("n0-0").y < pos.get("n0-1").y);
  assert.ok(pos.get("n0-0").x < pos.get("n1-0").x);
  assert.ok(w > 2 * NODE_W && h > 4 * NODE_H);
});

test("culling keeps the rendered set bounded for 5,000 tasks", () => {
  const nodes = grid(25, 200); // 5,000 tasks
  const { pos } = layoutLayers(nodes);
  const r = viewRect(0, 0, 1000, 560, 0.4);
  const seen = nodesInView(nodes, pos, r);
  assert.ok(seen.length > 0 && seen.length < 600, `rendered ${seen.length}`);
  // A node far away is not rendered unless it is pinned (selection / search hit).
  assert.ok(!seen.some((n) => n.id === "n24-199"));
  assert.ok(nodesInView(nodes, pos, r, new Set(["n24-199"])).some((n) => n.id === "n24-199"));
  // Scrolling inside the same tile does not change the view key (no re-render).
  assert.equal(rectKey(viewRect(0, 0, 1000, 560, 1)), rectKey(viewRect(4, 4, 1000, 560, 1)));
  assert.ok(r.x0 % TILE === 0 && r.y1 % TILE === 0);
});

test("dot LOD keeps only critical and selected edges; other LODs cull by box", () => {
  const nodes = grid(4, 3);
  const { pos } = layoutLayers(nodes);
  const all = { x0: -1e6, y0: -1e6, x1: 1e6, y1: 1e6 };
  const crit = new Set(["n0-0", "n1-0", "n2-0", "n3-0"]);
  assert.equal(edgesInView(nodes, pos, all, "full", crit, null).length, 9);
  const dots = edgesInView(nodes, pos, all, "dot", crit, "n2-2");
  assert.deepEqual(dots.filter((e) => e.critical).length, 3);
  assert.ok(dots.some((e) => e.hot && e.to === "n2-2") && dots.some((e) => e.hot && e.from === "n2-2"));
  assert.equal(dots.length, 5);
  const nowhere = { x0: 1e6, y0: 1e6, x1: 1e6 + 10, y1: 1e6 + 10 };
  assert.equal(edgesInView(nodes, pos, nowhere, "full", crit, null).length, 0);
});

test("centering and zooming around the pointer", () => {
  const c = centerOn({ x: 1000, y: 500 }, 400, 200, 1);
  assert.deepEqual(c, { left: 1000 + NODE_W / 2 - 200, top: 500 + NODE_H / 2 - 100 });
  const z = zoomAround(100, 50, 200, 100, 1, 2);
  // the layout point under the pointer, (100+200)/1 = 300, stays under it: 300*2 - 200 = 400
  assert.deepEqual(z, { left: 400, top: 200 });
});
