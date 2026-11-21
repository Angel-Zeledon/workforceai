// Unit tests of "Te necesitan" (run: npm run test:unit). Contract: backend/internal/api/needs_you.go.
import assert from "node:assert/strict";
import { test } from "node:test";
import { actionsOf, badgeText, filterKind, isNeedsYouFrame, kindTabs, titleRef, withoutItem } from "./needsYouLogic.ts";

const res = (items) => ({ items, total: items.length, counts: items.reduce((c, it) => ({ ...c, [it.kind]: (c[it.kind] || 0) + 1 }), {}) });

test("the live events that change what waits for a person refresh the list", () => {
  for (const ty of ["approval.requested", "approval.resolved", "approval.progress", "question.asked", "question.resolved", "task.needs_input",
    "trust.proposal", "agent.autonomy_changed", "initiative.raised", "initiative.resolved"]) assert.equal(isNeedsYouFrame(ty), true, ty);
  for (const ty of ["chat.message", "agent.state_changed", "task.started", "metrics.updated", ""]) assert.equal(isNeedsYouFrame(ty), false, ty);
});

test("only safe actions are offered from the list", () => {
  assert.deepEqual(actionsOf({ kind: "approval", direct: true }), ["approve", "reject"]);
  assert.deepEqual(actionsOf({ kind: "approval", direct: false }), ["reject"]); // high risk: approving needs the detail
  assert.deepEqual(actionsOf({ kind: "suggestion", direct: true }), ["accept", "dismiss"]);
  assert.deepEqual(actionsOf({ kind: "question", direct: false }), []);
  assert.deepEqual(actionsOf({ kind: "trust", direct: false }), []);
  assert.deepEqual(actionsOf({ kind: "stalled_task", direct: true }), []); // an unknown kind: only its link
});

test("titles: the source's text, a trust proposal in the viewer's words", () => {
  assert.deepEqual(titleRef({ kind: "approval", title: "Enviar la factura" }), { text: "Enviar la factura" });
  assert.deepEqual(titleRef({ kind: "trust", proposal_kind: "promotion", agent_name: "Ana", to_level: "rules" }),
    { key: "needsYou.trust.promotion", vars: { agent: "Ana", level: "autonomy.rules" } });
  assert.deepEqual(titleRef({ kind: "trust", proposal_kind: "demotion", agent_id: "a1", to_level: "approve_each" }),
    { key: "needsYou.trust.demotion", vars: { agent: "a1", level: "autonomy.approve_each" } });
  assert.deepEqual(titleRef({ kind: "question", title: "" }), { key: "needsYou.untitled", vars: {} });
});

test("tabs follow the server's counts, in a fixed order, unknown kinds after", () => {
  const r = res([{ kind: "suggestion", id: "s" }, { kind: "approval", id: "a" }, { kind: "approval", id: "b" }, { kind: "stalled_task", id: "x" }]);
  assert.deepEqual(kindTabs(r), [
    { kind: "", count: 4 }, { kind: "approval", count: 2 }, { kind: "suggestion", count: 1 }, { kind: "stalled_task", count: 1 },
  ]);
  assert.deepEqual(filterKind(r.items, "approval").map((i) => i.id), ["a", "b"]);
  assert.equal(filterKind(r.items, "").length, 4);
});

test("an item resolved here leaves at once, counts included", () => {
  const r = res([{ kind: "approval", id: "a" }, { kind: "question", id: "q" }]);
  const next = withoutItem(r, "approval", "a");
  assert.deepEqual(next.items.map((i) => i.id), ["q"]);
  assert.equal(next.total, 1);
  assert.equal(next.counts.approval, 0);
  assert.equal(withoutItem(r, "approval", "nope"), r);
});

test("mock mode orders like the server and offers approving only what is safe", async () => {
  const { mockNeedsYou } = await import("./mock/needsyou-mock.ts");
  const now = Date.parse("2026-11-21T12:00:00Z");
  const at = (h) => new Date(now - h * 3600_000).toISOString();
  const r = mockNeedsYou([
    { id: "low", agent_id: "a1", action: "send", title: "Enviar", risk: "low", created_at: at(1) },
    { id: "high", agent_id: "a1", action: "pay", title: "Pagar", risk: "high", created_at: at(0.5) },
    { id: "old", agent_id: "a2", action: "send", title: "Viejo", risk: "low", created_at: at(30) },
  ], [{ id: "s1", agent_id: "a2", signal: "task_failed", title: "Falló", body: "", status: "pending", created_at: at(50) },
    { id: "s2", agent_id: "a2", signal: "task_failed", title: "Ya", body: "", status: "accepted", created_at: at(50) }], (id) => id.toUpperCase(), now);
  assert.deepEqual(r.items.map((i) => i.id), ["old", "high", "low", "s1"]);
  assert.equal(r.items.find((i) => i.id === "high").direct, false);
  assert.equal(r.items[0].agent_name, "A2");
  assert.deepEqual(r.counts, { approval: 3, suggestion: 1 });
  assert.equal(mockNeedsYou([], [], (x) => x, now, true).items.length, 0);
});

test("the badge", () => {
  assert.equal(badgeText(0), "");
  assert.equal(badgeText(7), "7");
  assert.equal(badgeText(120), "99+");
});
