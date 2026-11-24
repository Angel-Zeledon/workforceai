// Unit tests of temporary helpers (counts, estimate rescale, folding of 50 parts, merge, demo split). Run: npm run test:unit.
import assert from "node:assert/strict";
import { test } from "node:test";
import { clampCount, countOptions, countParts, demoEstimate, doneOf, groupOfRequest, groupPct, isHelperTask, mergeGroup, missingLabels, partKind, perHelper, proposedRequestIds, proposals, rescale, spendOf, splitItems, visibleParts, withoutHelperTasks } from "./helpersLogic.ts";

const G = (extra = {}) => ({
  id: "g1", request_id: "r1", owner_agent_id: "elena", count: 8, max_count: 8, parallel: 4, status: "running",
  parts: Array.from({ length: 8 }, (_, i) => ({ index: i + 1, label: `P${i + 1}`, status: "pending" })),
  estimate: { min_usd: 0.08, max_usd: 0.2 }, cap_usd: 0.2, spent_usd: 0, ...extra,
});

test("helper tasks are recognised and filtered out of lists", () => {
  const ts = [{ id: "a" }, { id: "b", helper: { group_id: "g1", index: 1 } }, { id: "c", helper: { group_id: "g1", index: 0 } }]; // index 0: the responsible joins the parts, an ordinary task
  assert.equal(isHelperTask(ts[1]), true);
  assert.equal(isHelperTask(ts[0]), false);
  assert.deepEqual(withoutHelperTasks(ts).map((t) => t.id), ["a", "c"]);
});

test("count is clamped between 2 and the plan maximum", () => {
  assert.equal(clampCount(1, { max_count: 8 }), 2);
  assert.equal(clampCount(99, { max_count: 8 }), 8);
  assert.equal(clampCount(NaN, { max_count: 8 }), 2);
  assert.equal(clampCount(5.4, { max_count: 8 }), 5);
});

test("selector options include the proposed count and the maximum", () => {
  const o = countOptions({ count: 7, max_count: 20 });
  assert.ok(o.includes(7) && o.includes(20) && o.includes(2));
  assert.deepEqual(o, [...o].sort((a, b) => a - b));
  assert.equal(new Set(o).size, o.length);
});

test("estimate and cap scale in proportion to the count", () => {
  const r = rescale({ count: 8, estimate: { min_usd: 0.08, max_usd: 0.2 }, cap_usd: 0.2 }, 4);
  assert.deepEqual(r, { estimate: { min_usd: 0.04, max_usd: 0.1 }, cap_usd: 0.1 });
  assert.deepEqual(rescale({ count: 0, estimate: { min_usd: 1, max_usd: 2 }, cap_usd: 2 }, 3).cap_usd, 2);
});

test("share per helper rounds up", () => {
  assert.equal(perHelper(40, 8), 5);
  assert.equal(perHelper(41, 8), 6);
  assert.equal(perHelper(10, 0), 10);
});

test("part kinds and counts", () => {
  assert.equal(partKind("done"), "done");
  assert.equal(partKind("running"), "active");
  assert.equal(partKind("pending"), "queued");
  assert.equal(partKind("pending", "stopped"), "stopped");
  assert.equal(partKind("running", "stopped"), "active"); // the call in flight finishes
  const g = G();
  g.parts[0].status = "done"; g.parts[1].status = "done"; g.parts[2].status = "running"; g.parts[3].status = "failed";
  const c = countParts(g);
  assert.deepEqual([c.done, c.active, c.failed, c.queued, c.total], [2, 1, 1, 4, 8]);
  assert.deepEqual(doneOf(g), { done: 2, total: 8 });
});

test("spend against the cap", () => {
  assert.deepEqual(spendOf({ spent_usd: 0.1, cap_usd: 0.2 }), { pct: 50, reached: false });
  assert.deepEqual(spendOf({ spent_usd: 0.25, cap_usd: 0.2 }), { pct: 100, reached: true });
  assert.deepEqual(spendOf({ spent_usd: 0, cap_usd: 0 }), { pct: 0, reached: false });
});

test("50 parts fold: failed and running first, the finished ones are a count", () => {
  const parts = Array.from({ length: 50 }, (_, i) => ({ index: i + 1, label: `P${i + 1}`, status: i < 30 ? "done" : i === 40 ? "failed" : i === 41 ? "running" : "pending" }));
  const g = { parts, status: "running" };
  const v = visibleParts(g, false);
  assert.equal(v.shown.length, 12);
  assert.equal(v.hidden, 38);
  assert.equal(v.shown[0].index, 41); // the failed one
  assert.equal(v.shown[1].index, 42);
  assert.ok(v.shown.every((p) => p.status !== "done"));
  assert.equal(visibleParts(g, true).shown.length, 50);
  assert.equal(visibleParts({ parts: parts.slice(0, 8), status: "running" }, false).hidden, 0);
});

test("proposals and the group of a request", () => {
  const gs = { a: G({ id: "a", status: "proposed" }), b: G({ id: "b", request_id: "r2", status: "declined" }), c: G({ id: "c", request_id: "r2", status: "running" }) };
  assert.deepEqual(proposals(gs).map((g) => g.id), ["a"]);
  assert.equal(groupOfRequest(gs, "r2")?.id, "c");
  assert.equal(groupOfRequest(gs, "none"), undefined);
});

test("the run block waits while the proposal is open, and shows the parts' progress", () => {
  const gs = { a: G({ id: "a", request_id: "r1", status: "proposed" }), b: G({ id: "b", request_id: "r2", status: "running" }) };
  assert.deepEqual([...proposedRequestIds(gs)], ["r1"]);
  const run = G({ status: "running", parts: [
    { index: 1, label: "Madrid", status: "done" }, { index: 2, label: "Lima", status: "done" },
    { index: 3, label: "Bogotá", status: "failed" }, { index: 4, label: "Quito", status: "running" },
  ] });
  assert.equal(groupPct(run), 50);
  assert.deepEqual(missingLabels(run), ["Bogotá", "Quito"]);
  assert.equal(groupPct(G({ parts: [] })), 0);
});

test("a late proposed event never undoes a launch", () => {
  const merged = mergeGroup(G({ status: "running" }), G({ status: "proposed" }));
  assert.equal(merged.status, "running");
  assert.equal(mergeGroup(undefined, G()).id, "g1");
  assert.equal(mergeGroup(G({ status: "proposed" }), G({ status: "running", spent_usd: 0.05 })).spent_usd, 0.05);
});

test("the demo splits a list of four or more things, or several attachments", () => {
  assert.equal(splitItems("Revisa:\n- uno\n- dos\n- tres\n- cuatro").length, 4);
  assert.equal(splitItems("Investiga estas ciudades: Madrid, Lima, Bogota y Quito").length, 4);
  assert.deepEqual(splitItems("Revisa: uno, dos, tres"), []);
  assert.equal(splitItems("Resume estos", 3).length, 3);
  assert.deepEqual(splitItems("Hola"), []);
  const e = demoEstimate(8);
  assert.ok(e.estimate.min_usd < e.estimate.max_usd && e.cap_usd === e.estimate.max_usd);
});
