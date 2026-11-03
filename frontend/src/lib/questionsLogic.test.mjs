// Unit tests of "Necesita tu ayuda" (Pendientes): grouping, waiting labels, the skip verdict (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { groupQuestions, pendingCount, skipVerdict, waitingParts, batchable } from "./questionsLogic.ts";

const q = (id, extra = {}) => ({ id, task_id: "t" + id, request_id: "r", agent_id: "sales", kind: "data", what_happened: "x", need: "¿Cómo?",
  options: [], status: "open", created_at: "2026-11-03T09:00:00Z", resolved_at: null, reminders: 0, waiting_seconds: 60, can_answer: true, for_me: true, ...extra });

test("groups by batch, then project, then request; newest group first, items oldest first", () => {
  const groups = groupQuestions([
    q("1", { group_key: "project:p1", project_id: "p1", project_name: "Onboarding ACME", created_at: "2026-11-03T09:00:00Z" }),
    q("2", { group_key: "request:r9", created_at: "2026-11-03T10:00:00Z", task_title: "Llamar" }),
    q("3", { group_key: "project:p1", project_id: "p1", project_name: "Onboarding ACME", created_at: "2026-11-03T08:00:00Z" }),
  ]);
  assert.deepEqual(groups.map((g) => g.key), ["request:r9", "project:p1"]);
  assert.equal(groups[1].label, "Onboarding ACME");
  assert.deepEqual(groups[1].items.map((i) => i.id), ["3", "1"]);
  assert.equal(groups[0].kind, "request");
  assert.equal(groups[1].kind, "project");
});

test("only open questions are pending", () => {
  assert.equal(pendingCount([q("1"), q("2", { status: "answered" }), q("3", { status: "cancelled" })]), 1);
  assert.deepEqual(groupQuestions([q("1", { status: "answered" })]), []);
});

test("waiting label parts: minutes, hours, days", () => {
  assert.deepEqual(waitingParts(30), { unit: "now", n: 0 });
  assert.deepEqual(waitingParts(5 * 60), { unit: "min", n: 5 });
  assert.deepEqual(waitingParts(3 * 3600 + 100), { unit: "h", n: 3 });
  assert.deepEqual(waitingParts(2 * 86400 + 5), { unit: "d", n: 2 });
});

test("skip verdict: refuse when nothing would be delivered, confirm otherwise", () => {
  assert.equal(skipVerdict({ allowed: false, all_lost: true, skipped: [], objectives_lost: [], objectives: 1 }), "refuse");
  assert.equal(skipVerdict({ allowed: true, all_lost: false, skipped: [], objectives_lost: [], objectives: 1 }), "confirm");
  assert.equal(skipVerdict({ allowed: true, all_lost: false, skipped: [{ id: "b", title: "B" }], objectives_lost: [{ id: "o1", title: "O" }], objectives: 2 }), "partial");
  assert.equal(skipVerdict(null), "unknown");
});

test("a batch answer uses the options every selected question shares", () => {
  const a = q("1", { options: ["Usa el correo", "Omite"] });
  const b = q("2", { options: ["Usa el correo", "Llama"] });
  assert.deepEqual(batchable([a, b]), { ok: true, options: ["Usa el correo"] });
  assert.deepEqual(batchable([a, q("3", { can_answer: false })]), { ok: false, options: [] });
  assert.deepEqual(batchable([]), { ok: false, options: [] });
});
