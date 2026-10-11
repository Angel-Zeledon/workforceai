// Unit tests of the pure "office reflects real state" decisions (run: npm run test:unit; Node >= 22.18 strips the TypeScript types).
import assert from "node:assert/strict";
import { test } from "node:test";
import {
  BUBBLE_GAP_MS, BUBBLE_MAX_VISIBLE, BUBBLE_SHOW_MS, FolderPool, approvalsSpot, cafeSpot, detectHandoffs, energyOf, etaShort, meetingSeat,
  phaseFor, pickBubble, slotIndex, stepBubbles, taskEta, truncateText, zoneFor, fileBubbleText, FILE_MAX_AGE,
} from "./officeState.ts";

test("file bubble: what the agent does with which file, after the plan phase and chat lines", () => {
  const base = { state: "working", activity: "Revisando", taskTitle: "Propuesta", phaseTitle: null, line: null };
  const file = { action: "write", path: "/Agents/sales/propuesta-alfa.md", ageMs: 1000 };
  assert.deepEqual(pickBubble({ ...base, file }), { key: "file:write:/Agents/sales/propuesta-alfa.md", kind: "file", text: "✍️ propuesta-alfa.md" });
  // a recent chat line and a planning phase still win
  assert.equal(pickBubble({ ...base, file, line: { text: "¿Margen?", kind: "consult", ageMs: 10 } }).kind, "consult");
  assert.equal(pickBubble({ ...base, file, phaseTitle: "Fase 1" }).kind, "phase");
  // an old file activity is not news anymore: back to the task
  assert.equal(pickBubble({ ...base, file: { ...file, ageMs: FILE_MAX_AGE + 1 } }).kind, "task");
  assert.equal(fileBubbleText("read", "/Shared/Finanzas/saldos.csv"), "📖 saldos.csv");
  assert.equal(fileBubbleText("list", "/"), "📂 /");
});

const base = { state: "working", budgetPaused: false, planning: false, approvalPending: false };

test("zoneFor: priorities and exceptions", () => {
  assert.equal(zoneFor(base), "desk");
  assert.equal(zoneFor({ ...base, state: "awaiting_approval", approvalPending: true }), "approvals");
  // awaiting_approval without a known pending approval stays at the desk (never invent the walk)
  assert.equal(zoneFor({ ...base, state: "awaiting_approval" }), "desk");
  assert.equal(zoneFor({ ...base, planning: true, state: "thinking" }), "meeting");
  // budget pause wins over everything but completed/error, and over the backend's "blocked" marker
  assert.equal(zoneFor({ ...base, state: "blocked", budgetPaused: true }), "cafeteria");
  assert.equal(zoneFor({ ...base, state: "awaiting_approval", approvalPending: true, budgetPaused: true }), "cafeteria");
  assert.equal(zoneFor({ ...base, state: "error", budgetPaused: true }), "desk");
  assert.equal(zoneFor({ ...base, state: "completed", planning: true }), "desk");
  assert.equal(zoneFor({ ...base, state: "blocked", planning: true }), "desk");
});

test("slotIndex caps each zone", () => {
  const ids = ["a", "b", "c", "d", "e", "f", "g"];
  assert.equal(slotIndex(ids, "a", 6), 0);
  assert.equal(slotIndex(ids, "f", 6), 5);
  assert.equal(slotIndex(ids, "g", 6), -1);
  assert.equal(slotIndex(ids, "zzz", 6), -1);
});

test("cafeteria spots are distinct and face the counter or the table", () => {
  const spots = [0, 1, 2, 3, 4, 5].map((k) => cafeSpot(k));
  assert.equal(new Set(spots.map((s) => s.pos.join(","))).size, 6);
  for (const s of spots.slice(0, 4)) assert.ok(Math.abs(s.yaw - Math.PI / 2) < 1e-9); // toward the counter (east)
  assert.ok(Math.abs(spots[4].yaw) < 1e-9 && Math.abs(spots[5].yaw - Math.PI) < 1e-9); // flank the high table
});

test("spots are distinct and meeting seats face the table", () => {
  const user = [9.5, 1.6, 5.4];
  const spots = new Set([0, 1, 2, 3, 4].map((k) => approvalsSpot(user, k).join(",")));
  assert.equal(spots.size, 5);
  const c = [9.5, -6.2];
  for (let k = 0; k < 6; k++) {
    const s = meetingSeat(c, k);
    const d = Math.hypot(s.pos[0] - c[0], s.pos[1] - c[1]);
    assert.ok(Math.abs(d - 2.25) < 1e-9);
    // walking forward along the yaw reaches the center
    const fx = s.pos[0] + Math.sin(s.yaw) * d, fz = s.pos[1] + Math.cos(s.yaw) * d;
    assert.ok(Math.hypot(fx - c[0], fz - c[1]) < 1e-9);
  }
});

test("truncateText: first line, whitespace, word boundary", () => {
  assert.equal(truncateText("  hola   mundo \nsegunda línea", 64), "hola mundo");
  const t = truncateText("Calcular el margen real de la propuesta con costos de capacidad adicionales", 40);
  assert.ok(t.length <= 40 && t.endsWith("…"));
  assert.ok(!t.includes("  "));
  assert.equal(truncateText("", 10), "");
});

test("pickBubble only uses real content, in priority order", () => {
  const i = { state: "working", activity: "Estimando precio", taskTitle: "Analizar cliente", phaseTitle: null, line: null };
  assert.equal(pickBubble(i).kind, "task");
  assert.match(pickBubble(i).text, /Analizar cliente · Estimando precio/);
  assert.equal(pickBubble({ ...i, taskTitle: "" }).kind, "step");
  assert.equal(pickBubble({ ...i, state: "idle" }), null);
  assert.equal(pickBubble({ ...i, state: "idle", taskTitle: "", activity: "" }), null);
  const line = { text: "¿Hay penalización?", kind: "consult", ageMs: 3000 };
  assert.equal(pickBubble({ ...i, line }).kind, "consult");
  // an old chat line is not repeated: falls back to the task
  assert.equal(pickBubble({ ...i, line: { ...line, ageMs: 60000 } }).kind, "task");
  assert.equal(pickBubble({ ...i, line, phaseTitle: "Fase 1: Descubrimiento" }).kind, "phase");
});

test("phaseFor rotates through the known phases", () => {
  assert.equal(phaseFor([], 0, 0), null);
  assert.equal(phaseFor(["a", "b", "c"], 0, 0), "a");
  assert.equal(phaseFor(["a", "b", "c"], 1, 0), "b");
  assert.equal(phaseFor(["a", "b", "c"], 2, 2), "b");
});

test("stepBubbles rate-limits, never repeats and caps concurrency", () => {
  const st = new Map();
  const b = (k) => ({ key: k, kind: "task", text: k });
  let vis = stepBubbles(st, [{ id: "a", bubble: b("x") }], 0);
  assert.deepEqual(vis, ["a"]);
  // still visible during the show window, even if the content changed
  assert.deepEqual(stepBubbles(st, [{ id: "a", bubble: b("y") }], BUBBLE_SHOW_MS - 1), ["a"]);
  // quiet during the gap
  assert.deepEqual(stepBubbles(st, [{ id: "a", bubble: b("y") }], BUBBLE_SHOW_MS + 100), []);
  // same content never repeats, new content shows after the gap
  assert.deepEqual(stepBubbles(st, [{ id: "a", bubble: b("x") }], BUBBLE_SHOW_MS + BUBBLE_GAP_MS + 10), []);
  assert.deepEqual(stepBubbles(st, [{ id: "a", bubble: b("z") }], BUBBLE_SHOW_MS + BUBBLE_GAP_MS + 20), ["a"]);
  // concurrency cap
  const st2 = new Map();
  const many = ["a", "b", "c", "d", "e"].map((id) => ({ id, bubble: b(id) }));
  assert.equal(stepBubbles(st2, many, 0).length, BUBBLE_MAX_VISIBLE);
  assert.equal(stepBubbles(st2, many, 100).length, BUBBLE_MAX_VISIBLE);
  // once the first ones expire the others get their turn
  const later = stepBubbles(st2, many, BUBBLE_SHOW_MS + 10);
  assert.deepEqual(later.sort(), ["d", "e"]);
});

test("stepBubbles: stale bubbles disappear and urgent changes replace a visible one", () => {
  const st = new Map();
  const b = (k, urgent) => ({ key: k, kind: "task", text: k, urgent });
  assert.deepEqual(stepBubbles(st, [{ id: "a", bubble: b("x") }], 0), ["a"]);
  // a non-urgent change keeps the visible bubble; urgent replaces it right away
  assert.equal(stepBubbles(st, [{ id: "a", bubble: b("y") }], 1000).length, 1);
  assert.equal(st.get("a").text, "x");
  assert.equal(stepBubbles(st, [{ id: "a", bubble: b("paused", true) }], 2000).length, 1);
  assert.equal(st.get("a").text, "paused");
  // nothing real to say: removed at once
  assert.deepEqual(stepBubbles(st, [{ id: "a", bubble: null }], 2500), []);
});

test("taskEta: reported progress, estimate fallback, nothing to say", () => {
  const now = 100_000;
  assert.equal(taskEta({ progress: 50, startedMs: null, now }), null);
  assert.equal(taskEta({ progress: 0, startedMs: now - 10_000, now }), null);
  const a = taskEta({ progress: 50, startedMs: now - 20_000, now });
  assert.equal(a.pct, 50); assert.equal(Math.round(a.etaS), 20);
  const b = taskEta({ progress: 0, startedMs: now - 10_000, now, estSeconds: 40 });
  assert.equal(Math.round(b.pct), 25); assert.equal(Math.round(b.etaS), 30);
  // calibration scales the estimate; overdue clamps at 0
  assert.equal(Math.round(taskEta({ progress: 0, startedMs: now - 10_000, now, estSeconds: 40, calibration: 2 }).etaS), 70);
  assert.equal(taskEta({ progress: 0, startedMs: now - 90_000, now, estSeconds: 40 }).etaS, 0);
  assert.equal(taskEta({ progress: 0, startedMs: now - 90_000, now, estSeconds: 40 }).pct, 95);
});

test("etaShort formats compactly", () => {
  assert.equal(etaShort(0), "1s"); assert.equal(etaShort(45), "45s"); assert.equal(etaShort(180), "3m");
  assert.equal(etaShort(4800), "1h20m"); assert.equal(etaShort(7200), "2h");
});

test("energyOf: remaining budget, null without a cap", () => {
  assert.equal(energyOf(0, 0), null);
  const a = energyOf(10, 2, 1);
  assert.equal(a.remaining, 7); assert.equal(a.fraction, 0.7); assert.equal(a.level, "ok");
  assert.equal(energyOf(10, 8, 1).level, "low");
  assert.equal(energyOf(10, 12).level, "empty");
  assert.equal(energyOf(10, 12).remaining, 0);
});

const T = (id, status, agent, deps = []) => ({ id, status, agent_id: agent, depends_on: deps });

test("detectHandoffs: only fresh completions feeding another agent's open task", () => {
  const t0 = { t1: T("t1", "running", "sales"), t2: T("t2", "pending", "legal", ["t1"]), t3: T("t3", "pending", "sales", ["t1"]) };
  const first = detectHandoffs(null, t0);
  assert.deepEqual(first.handoffs, []); // history load: nothing replayed
  const t1 = { ...t0, t1: T("t1", "done", "sales") };
  const r = detectHandoffs(first.next, t1);
  assert.deepEqual(r.handoffs, [{ from: "sales", to: "legal" }]); // t3 is the same agent: no hand-off
  // already-done tasks do not trigger again
  assert.deepEqual(detectHandoffs(r.next, t1).handoffs, []);
  // a dependent that already finished is not a hand-off
  const t2 = { ...t1, t2: T("t2", "done", "legal") };
  assert.deepEqual(detectHandoffs(first.next, t2).handoffs.length, 0);
  // a task unknown to the previous snapshot (just created) as "done" is history, not a hand-off
  const t3 = { ...t1, t9: T("t9", "done", "hr"), t8: T("t8", "pending", "legal", ["t9"]) };
  assert.deepEqual(detectHandoffs(first.next, t3).handoffs.map((h) => h.from), ["sales"]);
});

test("FolderPool caps concurrency, dedupes and bounds the queue", () => {
  const pool = new FolderPool(2, 3, 1000);
  assert.ok(pool.enqueue({ from: "a", to: "b" }, 0));
  assert.ok(!pool.enqueue({ from: "a", to: "b" }, 500)); // duplicate inside the window
  assert.ok(pool.enqueue({ from: "a", to: "c" }, 500));
  assert.ok(pool.enqueue({ from: "b", to: "c" }, 500));
  assert.ok(pool.enqueue({ from: "c", to: "d" }, 500));
  assert.ok(pool.enqueue({ from: "d", to: "e" }, 500)); // queue full: the oldest is dropped
  assert.equal(pool.queued, 3);
  assert.equal(pool.pump(600, () => 1000), 2); // only 2 fly at once
  assert.equal(pool.queued, 1);
  assert.equal(pool.pump(1000, () => 1000), 2); // none finished yet
  assert.equal(pool.pump(1700, () => 1000), 1); // both done, the last queued one starts
  assert.equal(pool.pump(3000, () => 1000), 0);
  assert.ok(!pool.enqueue({ from: "x", to: "x" }, 4000));
});
