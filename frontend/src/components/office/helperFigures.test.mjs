import test from "node:test";
import assert from "node:assert/strict";
import { planHelpers, slotsFor, ROOM, MAX_TOTAL, MAX_PER_GROUP, MIN_GAP } from "./helperFigures.ts";

// a row of desks like the office's, plus some by a corner and a wall
const desks = { a: [-9, -5], b: [-4.5, -5], c: [0, -5], d: [-9, 3], e: [-13, 3], f: [12.5, 6] };
const list = Object.values(desks);
const boxOf = (d) => ({ x0: d[0] - 1.2, x1: d[0] + 1.2, z0: d[1] + 0.6 - 1.15, z1: d[1] + 0.6 + 1.15 });
const inBox = (p, b) => p.x > b.x0 && p.x < b.x1 && p.z > b.z0 && p.z < b.z1;
const parts = (n, st = "running") => Array.from({ length: n }, (_, i) => ({ index: i + 1, label: "Parte " + (i + 1), status: st }));
const group = (id, owner, n, status = "running", st = "running") => ({ id, owner_agent_id: owner, status, parts: parts(n, st) });
const apart = (figs) => figs.every((f, i) => figs.every((g, j) => i === j || Math.hypot(f.x - g.x, f.z - g.z) >= MIN_GAP - 1e-9));

test("slots of every desk: inside the room, off every desk, apart from each other", () => {
  for (const d of list) {
    const s = slotsFor(d, list);
    assert.ok(s.length >= 6, "at least six places by every desk");
    for (const p of s) {
      assert.ok(p.x >= ROOM.x0 && p.x <= ROOM.x1 && p.z >= ROOM.z0 && p.z <= ROOM.z1);
      for (const o of list) assert.ok(!inBox(p, boxOf(o)), "not on a desk");
    }
    assert.ok(apart(s));
  }
});

test("1 to 50 parts: never more than 6 figures per group, the rest is +N", () => {
  for (const n of [1, 2, 6, 7, 8, 20, 50]) {
    const plan = planHelpers([group("g1", "a", n)], desks);
    assert.equal(plan.figures.length, Math.min(n, MAX_PER_GROUP), "n=" + n);
    const more = plan.more.reduce((t, m) => t + m.n, 0);
    assert.equal(plan.figures.length + more, n);
    assert.ok(apart(plan.figures));
  }
});

test("several groups: 12 figures in all, nobody overlaps, each stays by its owner", () => {
  const plan = planHelpers([group("g1", "a", 8), group("g2", "b", 8), group("g3", "c", 8), group("g4", "d", 30)], desks);
  assert.equal(plan.figures.length, MAX_TOTAL);
  assert.ok(apart(plan.figures));
  const left = plan.more.reduce((t, m) => t + m.n, 0);
  assert.equal(plan.figures.length + left, 8 + 8 + 8 + 30);
  for (const f of plan.figures) {
    const mine = Math.hypot(f.x - desks[f.owner][0], f.z - desks[f.owner][1]);
    for (const [id, d] of Object.entries(desks)) if (id !== f.owner) assert.ok(mine < Math.hypot(f.x - d[0], f.z - d[1]) + 2, "closer to its owner");
  }
  // two groups of the same owner share the places without overlapping
  const same = planHelpers([group("g1", "a", 5), group("g2", "a", 5)], desks);
  assert.equal(same.figures.length, 10);
  assert.ok(apart(same.figures));
});

test("only running or joining groups; done / stopped parts leave; running come first", () => {
  assert.equal(planHelpers([group("g1", "a", 3, "proposed")], desks).figures.length, 0);
  assert.equal(planHelpers([group("g1", "a", 3, "done")], desks).figures.length, 0);
  const g = { id: "g1", owner_agent_id: "a", status: "running", parts: [{ index: 1, label: "x", status: "done" }, { index: 2, label: "y", status: "failed" }, { index: 3, label: "z", status: "pending" }, { index: 4, label: "w", status: "running" }] };
  const p = planHelpers([g], desks);
  assert.deepEqual(p.figures.map((f) => [f.n, f.state]), [[4, "working"], [2, "failed"], [3, "queued"]]);
});

test("a helper keeps its place while the others finish", () => {
  const g = group("g1", "a", 6);
  const first = planHelpers([g], desks);
  const g2 = { ...g, parts: g.parts.map((p) => (p.index <= 3 ? { ...p, status: "done" } : p)) };
  const next = planHelpers([g2], desks, { prev: first.slotOf });
  for (const f of next.figures) assert.equal(f.slot, first.slotOf[f.key]);
  assert.equal(next.figures.length, 3);
});

test("label only mode: no figures, one count per owner", () => {
  const plan = planHelpers([group("g1", "a", 8), group("g2", "a", 2), group("g3", "b", 5)], desks, { labelOnly: true });
  assert.equal(plan.figures.length, 0);
  assert.deepEqual(plan.labels.map((l) => [l.owner, l.n]).sort(), [["a", 10], ["b", 5]]);
});

test("presence: eases in, stays, eases out; the look is stable per key", async () => {
  const { presence, APPEAR_S, LEAVE_S, hashKey } = await import("./helperFigures.ts");
  assert.equal(presence(0, null), 0);
  assert.equal(presence(APPEAR_S, null), 1);
  assert.ok(presence(APPEAR_S / 2, null) > 0 && presence(APPEAR_S / 2, null) < 1);
  assert.equal(presence(5, LEAVE_S), 0);
  assert.equal(presence(5, 0), 1);
  assert.equal(hashKey("a:1"), hashKey("a:1"));
});

test("no groups: an empty plan; unknown owner: skipped", () => {
  assert.equal(planHelpers([], desks).figures.length, 0);
  assert.equal(planHelpers([group("g1", "zzz", 4)], desks).figures.length, 0);
});
