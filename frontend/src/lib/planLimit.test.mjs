// Unit tests of the plan-limit refusal parser (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { parsePlanLimit } from "./planLimit.ts";

test("the API shape: code, limit_kind, limit and plan", () => {
  const body = { error: "plan_limit: schedules (2) on the free plan", code: "plan_limit", limit_kind: "schedules", limit: 2, plan: "free" };
  assert.deepEqual(parsePlanLimit(body), { kind: "schedules", limit: 2, plan: "free" });
});

test("the auth service shape: {error:{code,message}}", () => {
  const body = { error: { code: "plan_limit", message: "plan_limit: seats (10) on the pro plan" } };
  assert.deepEqual(parsePlanLimit(body), { kind: "seats", limit: 10, plan: "pro" });
});

test("an auth plan limit with an unexpected message is still a plan limit", () => {
  assert.deepEqual(parsePlanLimit({ error: { code: "plan_limit", message: "?" } }), { kind: "", limit: 0, plan: "" });
});

test("any other conflict is not a plan limit", () => {
  assert.equal(parsePlanLimit({ error: "the office is full (20 agents)" }), null);
  assert.equal(parsePlanLimit({ error: { code: "already_member", message: "x" } }), null);
  assert.equal(parsePlanLimit(null), null);
  assert.equal(parsePlanLimit("plan_limit"), null);
});

test("the plan's storage (Drive, 413): code plan_limit with the kind, the bytes and the plan", () => {
  const body = { code: "plan_limit", error: "the storage of the free plan (500 MB) would be exceeded", limit_kind: "storage", limit: 524288000, plan: "free" };
  assert.deepEqual(parsePlanLimit(body), { kind: "storage", limit: 524288000, plan: "free" });
});
