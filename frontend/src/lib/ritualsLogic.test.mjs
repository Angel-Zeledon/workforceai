// Unit tests of the rituals UI helpers (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { GATHER_MS, buildStandupEntries, entryTotals, gatheringFrom, isGathering, realCount, toggleDay } from "./ritualsLogic.ts";

const T0 = Date.parse("2026-10-21T15:31:00Z");
const iso = (ms) => new Date(ms).toISOString();
const H = 3600_000;

test("a ritual.held frame gathers its participants for a while", () => {
  const g = gatheringFrom({ kind: "retro", agent_ids: ["sales", "hr", "sales", 3, ""] }, T0);
  assert.deepEqual(g.ids, ["sales", "hr"]);
  assert.equal(g.kind, "retro");
  assert.equal(isGathering(g, T0 + GATHER_MS - 1), true);
  assert.equal(isGathering(g, T0 + GATHER_MS), false);
  assert.equal(gatheringFrom({ agent_ids: [] }, T0), null);
  assert.equal(gatheringFrom(null, T0), null);
  assert.equal(isGathering(null, T0), false);
});

test("stand-up entries come from the tasks and approvals, like the server", () => {
  const agents = [{ id: "sales", name: "Valeria", role: "sales" }, { id: "hr", name: "Marcos", role: "hr" }];
  const task = (id, status, created, finished = null) => ({ id, title: "Tarea " + id, agent_id: "sales", status, created_at: iso(created), finished_at: finished === null ? null : iso(finished) });
  const tasks = [
    task("old", "done", T0 - 80 * H, T0 - 30 * H),
    task("d1", "done", T0 - 20 * H, T0 - 10 * H),
    task("d2", "failed", T0 - 20 * H, T0 - 2 * H),
    task("p1", "pending", T0 - 3 * H),
    task("r1", "running", T0 - H),
    task("b1", "blocked", T0 - 5 * H),
    task("w1", "awaiting_approval", T0 - 4 * H),
  ];
  const approvals = [
    { task_id: "w1", agent_id: "sales", title: "dup", status: "pending", created_at: iso(T0 - H) },
    { task_id: "x", agent_id: "sales", title: "Enviar", status: "pending", created_at: iso(T0 - H) },
  ];
  const [s, h] = buildStandupEntries(agents, tasks, approvals, T0 - 24 * H, T0);
  assert.deepEqual(s.done.map((i) => `${i.title}/${i.status}`), ["Tarea d2/failed", "Tarea d1/done"]);
  assert.deepEqual(s.next.map((i) => i.status), ["running", "pending"]);
  assert.deepEqual(s.blockers.map((i) => `${i.title}/${i.status}`), ["Tarea b1/blocked", "Tarea w1/awaiting_approval", "Enviar/approval_pending"]);
  assert.equal(s.quiet, false);
  assert.equal(h.quiet, true);
  assert.deepEqual(entryTotals([s, h]), { done: 2, next: 2, blockers: 3, quiet: 1, present: 2 });
});

test("long lists keep 3 items and a +N marker that still counts", () => {
  const tasks = Array.from({ length: 5 }, (_, i) => ({ id: `p${i}`, title: `P${i}`, agent_id: "a", status: "pending", created_at: iso(T0 - (10 - i) * H), finished_at: null }));
  const [e] = buildStandupEntries([{ id: "a", name: "A", role: "x" }], tasks, [], T0 - 24 * H, T0);
  assert.equal(e.next.length, 4);
  assert.deepEqual(e.next[3], { title: "+2", status: "more" });
  assert.equal(realCount(e.next), 5);
});

test("toggleDay keeps weekdays sorted and unique", () => {
  assert.deepEqual(toggleDay([5, 1], 3), [1, 3, 5]);
  assert.deepEqual(toggleDay([1, 3, 5], 3), [1, 5]);
});
