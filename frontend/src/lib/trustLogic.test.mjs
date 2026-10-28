// Unit tests of the trust ladder UI helpers (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { daysUntil, progressPct, rank, rungs, thresholdText, valueText } from "./trustLogic.ts";

test("rank orders the ladder", () => {
  assert.equal(rank("suggest"), 0);
  assert.equal(rank("autonomous"), 3);
  assert.equal(rank("bogus"), -1);
});

test("rungs lock everything above the role's ceiling", () => {
  assert.deepEqual(rungs("approve_each", "rules").map((r) => r.state), ["below", "current", "open", "locked"]);
  assert.deepEqual(rungs("rules", "rules").map((r) => r.state), ["below", "below", "current", "locked"]);
  assert.deepEqual(rungs("suggest", "autonomous").map((r) => r.state), ["current", "open", "open", "open"]);
});

test("values and thresholds: rates as whole percentages, counts as integers", () => {
  assert.equal(valueText("approval_rate", 0.951), "95%");
  assert.equal(valueText("decisions", 12), "12");
  assert.equal(valueText("quality", null), "–");
  assert.equal(thresholdText("approval_rate", ">=", 0.95), "≥ 95%");
  assert.equal(thresholdText("policy_denials", "<=", 0), "≤ 0");
  assert.equal(thresholdText("low_approval_rate", "<", 0.8), "< 80%");
});

test("progress never shows 100 % unless complete", () => {
  assert.equal(progressPct(0.999), 99);
  assert.equal(progressPct(1), 100);
  assert.equal(progressPct(0.6), 60);
  assert.equal(progressPct(null), 0);
  assert.equal(progressPct(-1), 0);
});

test("daysUntil", () => {
  const now = Date.parse("2026-10-01T00:00:00Z");
  assert.equal(daysUntil("2026-10-03T12:00:00Z", now), 3);
  assert.equal(daysUntil("2026-09-01T00:00:00Z", now), 0);
  assert.equal(daysUntil("nope", now), 0);
});
