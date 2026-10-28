// Unit tests of the visible shield helpers (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { alertFromAudit, alertFromFrame, mergeAlerts, shieldOpacity, sourceLabel } from "./shieldLogic.ts";

const frame = (payload, extra = {}) => ({ id: "ev1", type: "security.alert", ts: "2026-10-22T10:00:00Z", payload, ...extra });

test("alertFromFrame keeps the ids of an injection alert and ignores anything else", () => {
  const a = alertFromFrame(frame({ kind: "prompt_injection_suspected", agent_id: "sales", tool: "email", action: "search", task_id: "t1", request_id: "r1", source: "connection" }));
  assert.deepEqual(a, { id: "ev1", agent_id: "sales", task_id: "t1", request_id: "r1", tool: "email", action: "search", source: "connection", ts: "2026-10-22T10:00:00Z" });
  assert.equal(alertFromFrame(frame({ kind: "secret_detected", agent_id: "sales" })), null);
  assert.equal(alertFromFrame({ type: "task.created", payload: {} }), null);
  assert.equal(alertFromFrame(frame({ kind: "prompt_injection_suspected" })), null, "an alert without an agent cannot be shown on a desk");
  // older backends only sent agent_id, tool and connection_id
  const old = alertFromFrame(frame({ kind: "prompt_injection_suspected", agent_id: "sales", tool: "email", connection_id: "c1" }));
  assert.equal(old.task_id, "");
  assert.equal(sourceLabel(old), "email");
});

test("alertFromAudit maps the audit record of the security log", () => {
  const a = alertFromAudit({ id: "a1", ts: "2026-10-22T10:00:01Z", actor: "sales", action: "security.injection_suspected", request_id: "r1",
    details: { tool: "web", action: "fetch", task_id: "t2", source: "web" } });
  assert.equal(a.agent_id, "sales");
  assert.equal(a.task_id, "t2");
  assert.equal(a.request_id, "r1");
  assert.equal(sourceLabel(a), "web.fetch");
  assert.equal(alertFromAudit({ id: "a2", actor: "sales", action: "tool.executed" }), null);
});

test("mergeAlerts dedupes by id and sorts newest first", () => {
  const a = { id: "1", agent_id: "x", task_id: "", request_id: "", tool: "t", action: "", source: "", ts: "2026-10-22T10:00:00Z" };
  const b = { ...a, id: "2", ts: "2026-10-22T11:00:00Z" };
  const out = mergeAlerts([a], [b, a]);
  assert.deepEqual(out.map((x) => x.id), ["2", "1"]);
  assert.equal(mergeAlerts([a, b], [], 1).length, 1);
});

test("shieldOpacity pops in, holds and fades out", () => {
  assert.equal(shieldOpacity(0), 0);
  assert.equal(shieldOpacity(0.5), 1);
  assert.ok(shieldOpacity(0.04) > 0 && shieldOpacity(0.04) < 1);
  assert.ok(shieldOpacity(0.9) < 1);
  assert.equal(shieldOpacity(1), 0);
});
