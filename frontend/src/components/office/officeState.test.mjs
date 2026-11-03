// Unit tests of the pure "office reflects real state" decisions (run: npm run test:unit; Node >= 22.18 strips the TypeScript types).
import assert from "node:assert/strict";
import { test } from "node:test";
import {
  BUBBLE_GAP_MS, BUBBLE_MAX_VISIBLE, BUBBLE_SHOW_MS, FolderPool, approvalsSpot, cafeSpot, detectHandoffs, energyOf, etaShort, meetingSeat,
  phaseFor, pickBubble, slotIndex, stepBubbles, taskEta, truncateText, zoneFor, fileBubbleText, FILE_MAX_AGE, meetingPlace, approvalsFrame,
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

// ---- the objects of the rooms can be moved, turned or taken away (office editor) ----

const seatRing = (c) => [0, 1, 2, 3, 4, 5].map((k) => { const a = (k / 6) * Math.PI * 2 + 0.3; return { x: c[0] + Math.sin(a) * 2.25, z: c[1] + Math.cos(a) * 2.25 }; });
const close = (a, b) => Math.abs(a - b) < 1e-9;

test("meeting: the built-in room seats everyone exactly where they always sat", () => {
  const c = [9.5, -6.2];
  const room = { center: c, table: { x: c[0], z: c[1] }, seats: seatRing(c) };
  for (let k = 0; k < 6; k++) {
    const p = meetingPlace(room, k), old = meetingSeat(c, k);
    assert.ok(p.sit && close(p.pos[0], old.pos[0]) && close(p.pos[1], old.pos[1]) && close(p.yaw, old.yaw), `seat ${k}`);
  }
});

test("meeting: agents sit on the seats wherever they were moved, facing the table", () => {
  const c = [9.5, -6.2];
  const seats = seatRing(c);
  seats[2] = { x: 12, z: -3 };
  const table = { x: 10, z: -5 };
  const p = meetingPlace({ center: c, table, seats }, 2);
  assert.deepEqual(p.pos, [12, -3]);
  assert.ok(p.sit && close(p.yaw, Math.atan2(10 - 12, -5 - -3)));
  // walking forward along the yaw reaches the table
  const d = Math.hypot(12 - 10, -3 - -5);
  assert.ok(Math.hypot(12 + Math.sin(p.yaw) * d - 10, -3 + Math.cos(p.yaw) * d - -5) < 1e-9);
});

test("meeting: without a table they face the room center; without a seat they stand around it", () => {
  const c = [0, 0];
  const seats = seatRing(c);
  const noTable = meetingPlace({ center: c, table: null, seats }, 0);
  assert.ok(noTable.sit && close(noTable.yaw, Math.atan2(-seats[0].x, -seats[0].z)));
  // two seats taken away: the first four agents sit on the four left, the other two stand where the missing ones were
  const few = [...seats]; few[1] = null; few[4] = null;
  const room = { center: c, table: { x: 1, z: 0 }, seats: few };
  const sat = [0, 1, 2, 3].map((k) => meetingPlace(room, k));
  assert.ok(sat.every((p) => p.sit));
  assert.deepEqual(sat.map((p) => p.pos), [0, 2, 3, 5].map((i) => [seats[i].x, seats[i].z]));
  const st = [4, 5].map((k) => meetingPlace(room, k));
  assert.ok(st.every((p) => !p.sit));
  for (const [p, i] of [[st[0], 1], [st[1], 4]]) {
    const a = (i / 6) * Math.PI * 2 + 0.3;
    assert.ok(close(p.pos[0], 1 + Math.sin(a) * 2.25) && close(p.pos[1], Math.cos(a) * 2.25)); // around the (moved) table
    assert.ok(close(p.yaw, Math.atan2(1 - p.pos[0], 0 - p.pos[1])));
  }
  // nothing left at all: everyone stands around the room center, all spots distinct
  const bare = [0, 1, 2, 3, 4, 5].map((k) => meetingPlace({ center: [3, 3], table: null, seats: [null, null, null, null, null, null] }, k));
  assert.ok(bare.every((p) => !p.sit && close(Math.hypot(p.pos[0] - 3, p.pos[1] - 3), 2.25)));
  assert.equal(new Set(bare.map((p) => p.pos.join(","))).size, 6);
});

test("cafeteria: the spots follow the counter and the high table when they are moved or turned", () => {
  const c = [9.2, 1.2];
  const dflt = [0, 1, 2, 3, 4, 5].map((k) => cafeSpot(k, c));
  const same = [0, 1, 2, 3, 4, 5].map((k) => cafeSpot(k, c, { counter: { x: c[0] + 1.75, z: c[1], rot: 0 }, table: { x: c[0] - 0.6, z: c[1], rot: 0 } }));
  for (let k = 0; k < 6; k++) assert.ok(close(dflt[k].pos[0], same[k].pos[0]) && close(dflt[k].pos[1], same[k].pos[1]) && close(dflt[k].yaw, same[k].yaw));
  // counter turned a quarter: the agents line up along it and face it (yaw turns with it)
  const turned = cafeSpot(0, c, { counter: { x: 5, z: 5, rot: Math.PI / 2 }, table: null });
  assert.ok(close(turned.pos[0], 5 + -1.2) && close(turned.pos[1], 5 + 0.8), JSON.stringify(turned));
  assert.ok(close(turned.yaw, Math.PI));
  // the counter taken away (null): its spots stay where the counter was
  assert.deepEqual(cafeSpot(1, c, { counter: null, table: null }), dflt[1]);
  // the high table moved: its two spots flank it
  const t = cafeSpot(4, c, { counter: null, table: { x: 0, z: 0, rot: 0 } });
  assert.ok(close(t.pos[0], 0) && close(t.pos[1], -0.95) && close(t.yaw, 0));
});

test("approvals: the waiting line and the user follow the desk", () => {
  const c = [9.5, 5.4];
  // built-in desk (0.1 south of the room center): the user anchor is the room center as before
  const f = approvalsFrame(c, { x: c[0], z: c[1] + 0.1, rot: 0 });
  assert.ok(close(f.user[0], 9.5) && close(f.user[1], 5.4) && f.rot === 0);
  assert.deepEqual(approvalsFrame(c, null), { user: [9.5, 5.4], rot: 0 }); // desk taken away: the room center
  const u = [f.user[0], 1.6, f.user[1]];
  assert.deepEqual(approvalsSpot(u, 1, 0), approvalsSpot(u, 1));
  // desk turned a quarter: the line is rotated about the user
  const r = approvalsFrame([0, 0], { x: 0, z: 0, rot: Math.PI / 2 });
  assert.ok(close(r.user[0], -0.1) && close(r.user[1], 0));
  const s = approvalsSpot([r.user[0], 1.6, r.user[1]], 0, r.rot);
  assert.ok(close(s[0], -0.1 - 0.95) && close(s[1], 0), JSON.stringify(s));
});
