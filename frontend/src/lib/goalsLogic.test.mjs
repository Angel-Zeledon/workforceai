// Unit tests of the company goals helpers (F1; run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import {
  activeCount, formatDueDate, goalCreateBody, goalErrorKind, goalStatusKey, goalWhen, isOverdue, linkableGoals, normalizeGoalStatus, sortGoals, validateGoal,
} from "./goalsLogic.ts";

const g = (title, status, due, upd = "2026-01-01T00:00:00Z") => ({ title, status, due_date: due, updated_at: upd });

test("sortGoals: active first by deadline (none last), then achieved, then archived", () => {
  const out = sortGoals([g("Z", "archived", "", "2026-02-01"), g("B", "active", ""), g("A", "active", "2026-12-31"), g("C", "achieved", "", "2026-03-01"), g("D", "active", "2026-06-30")]);
  assert.deepEqual(out.map((x) => x.title), ["D", "A", "B", "C", "Z"]);
});

test("status label and unknown status", () => {
  assert.equal(goalStatusKey("achieved"), "goals.status.achieved");
  assert.equal(goalStatusKey("weird"), "goals.status.active");
  assert.equal(normalizeGoalStatus(undefined), "active");
});

test("validateGoal checks the lengths the server enforces", () => {
  assert.equal(validateGoal({ title: "  " }), "title_required");
  assert.equal(validateGoal({ title: "x".repeat(121) }), "title_long");
  assert.equal(validateGoal({ title: "x".repeat(120) }), null);
  assert.equal(validateGoal({ title: "ok", description: "d".repeat(281) }), "description_long");
  assert.equal(validateGoal({ title: "ok", period: "p".repeat(41) }), "period_long");
  assert.equal(validateGoal({ title: "ok", due_date: "2026-02-31" }), "due_invalid");
  assert.equal(validateGoal({ title: "Subir ventas del T4 un 15 %", due_date: "2026-12-31" }), null);
});

test("dates: readable format, empty on invalid, overdue only when active", () => {
  assert.match(formatDueDate("2026-12-31", "es"), /31/);
  assert.equal(formatDueDate("nope"), "");
  assert.equal(formatDueDate(undefined), "");
  const now = new Date(2026, 5, 15);
  assert.equal(isOverdue({ status: "active", due_date: "2026-06-14" }, now), true);
  assert.equal(isOverdue({ status: "active", due_date: "2026-06-15" }, now), false);
  assert.equal(isOverdue({ status: "achieved", due_date: "2026-06-14" }, now), false);
  assert.deepEqual(goalWhen({ period: " T4 2026 ", due_date: "" }), { period: "T4 2026", due: "" });
});

test("goalCreateBody leaves out empty optional fields", () => {
  assert.deepEqual(goalCreateBody({ title: " Meta ", description: "", period: "", due_date: "", office_id: "" }), { title: "Meta" });
  assert.deepEqual(goalCreateBody({ title: "M", description: "d", period: "T4", due_date: "2026-12-31", office_id: "ventas" }), { title: "M", description: "d", period: "T4", due_date: "2026-12-31", office_id: "ventas" });
});

test("linkableGoals keeps only active ones; activeCount", () => {
  const list = [g("a", "archived", ""), g("b", "active", ""), g("c", "achieved", "")];
  assert.deepEqual(linkableGoals(list).map((x) => x.title), ["b"]);
  assert.equal(activeCount(list), 1);
});

test("goalErrorKind reads the HTTP status", () => {
  assert.equal(goalErrorKind({ status: 403 }), "forbidden");
  assert.equal(goalErrorKind(new Error("POST /goals -> 409")), "limit");
  assert.equal(goalErrorKind(new Error("POST /goals -> 400")), "invalid");
  assert.equal(goalErrorKind(new Error("boom")), "generic");
});
