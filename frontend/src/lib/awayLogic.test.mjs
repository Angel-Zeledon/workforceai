// Unit tests of the "while you were away" helpers (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { alertKey, awayDuration, headlineParts, itemAction, parseMinHours, replaySchedule } from "./awayLogic.ts";

const zero = { requests_completed: 0, requests_failed: 0, tasks_completed: 0, tasks_failed: 0, tasks_blocked: 0, approvals_waiting: 0, approvals_decided: 0, initiatives: 0, alerts: 0 };

test("headline puts what waits for you first and drops zeros", () => {
  assert.deepEqual(headlineParts(zero), []);
  const parts = headlineParts({ ...zero, approvals_waiting: 2, tasks_completed: 12, requests_failed: 1, tasks_failed: 2, requests_completed: 3 });
  assert.deepEqual(parts.map((p) => p.key), ["approvals", "failed", "completed", "tasks"]);
  assert.equal(parts[1].count, 3);
  assert.equal(headlineParts({ ...zero, approvals_waiting: 1, alerts: 1, tasks_blocked: 1, requests_completed: 1, tasks_completed: 1 }, 3).length, 3);
});

test("awayDuration picks a readable unit", () => {
  assert.deepEqual(awayDuration(45), { n: 45, unit: "minutes" });
  assert.deepEqual(awayDuration(14 * 60), { n: 14, unit: "hours" });
  assert.deepEqual(awayDuration(3 * 24 * 60 + 100), { n: 3, unit: "days" });
  assert.deepEqual(awayDuration(-5), { n: 0, unit: "minutes" });
});

test("parseMinHours accepts only the offered choices", () => {
  assert.equal(parseMinHours(null), 8);
  assert.equal(parseMinHours("12"), 12);
  assert.equal(parseMinHours("0"), 0);
  assert.equal(parseMinHours("7"), 8);
  assert.equal(parseMinHours("abc", 4), 4);
});

test("itemAction maps references to what the button opens", () => {
  assert.equal(itemAction({ ref: "approval", ref_id: "a1" }), "approval");
  assert.equal(itemAction({ ref: "report", ref_id: "r1" }), "report");
  assert.equal(itemAction({ ref: "task", ref_id: "t1", agent_id: "sales" }), "agent");
  assert.equal(itemAction({ ref: "task", ref_id: "t1" }), "dashboard");
  assert.equal(itemAction({ ref: "initiative", ref_id: "i1" }), "dashboard");
  assert.equal(itemAction({ ref: "org", ref_id: "" }), "none");
});

test("alertKey uses the family of the code", () => {
  assert.equal(alertKey("anomaly:spend_spike"), "away.alert.anomaly");
  assert.equal(alertKey("read_only"), "away.alert.read_only");
  assert.equal(alertKey(undefined), "away.alert.other");
});

test("replaySchedule spreads frames evenly within the cap", () => {
  assert.deepEqual(replaySchedule(0), []);
  assert.deepEqual(replaySchedule(3, 20000), [0, 900, 1800]); // few frames: capped step so it still reads as a replay
  const s = replaySchedule(400, 20000);
  assert.equal(s.length, 400);
  assert.equal(s[1], 50);
  assert.ok(s[399] < 20000);
});
