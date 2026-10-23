// Unit tests of the internal auditor stamp helpers (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { aggregateAudit, normalizeAuditStatus, parseRequestAudit, taskAuditOf } from "./auditLogic.ts";

const task = (id, status, metrics, extra = {}) => ({ id, title: id, agent_id: "auditor", status, request_id: "r1", output: { summary: "s", findings: ["a", " "], evidence: ["e1", "e2"], confidence: 0.9, metrics }, ...extra });

test("normalizeAuditStatus never reads more than declared", () => {
  assert.equal(normalizeAuditStatus("Verified"), "verified");
  assert.equal(normalizeAuditStatus("inconsistency"), "inconsistency");
  assert.equal(normalizeAuditStatus("looks ok"), "unverified");
  assert.equal(normalizeAuditStatus(""), "");
  assert.equal(normalizeAuditStatus(undefined), "");
});

test("taskAuditOf only stamps finished audits", () => {
  assert.equal(taskAuditOf(task("x", "running", { audit_status: "verified" })), null);
  assert.equal(taskAuditOf(task("x", "done", { total: 1 })), null);
  const a = taskAuditOf(task("x", "done", { audit_status: "inconsistency", audit_compared: 3, audit_inconsistencies: 1 }));
  assert.equal(a.status, "inconsistency");
  assert.equal(a.compared, 3);
  assert.deepEqual(a.findings, ["a"]);
});

test("aggregateAudit: inconsistency wins, verified needs all, none without audits", () => {
  const v = task("v", "done", { audit_status: "verified" });
  const u = task("u", "done", { audit_status: "unverified" });
  const i = task("i", "done", { audit_status: "inconsistency" });
  const plain = task("p", "done", {});
  assert.equal(aggregateAudit("r1", [plain]).status, "none");
  assert.equal(aggregateAudit("r1", [v, plain]).status, "verified");
  assert.equal(aggregateAudit("r1", [v, u]).status, "unverified");
  assert.equal(aggregateAudit("r1", [v, u, i]).status, "inconsistency");
  assert.equal(aggregateAudit("r2", [v]).status, "none");
});

test("parseRequestAudit is defensive", () => {
  assert.equal(parseRequestAudit(null, "r"), null);
  assert.equal(parseRequestAudit({ status: "verified" }, "r"), null);
  const p = parseRequestAudit({ request_id: "r", status: "verified", audits: [] }, "r");
  assert.equal(p.status, "none");
  const q = parseRequestAudit({ request_id: "r", status: "inconsistency", audits: [{ task_id: "t", status: "weird", findings: [1] }] }, "r");
  assert.equal(q.status, "inconsistency");
  assert.equal(q.audits[0].status, "unverified");
  assert.deepEqual(q.audits[0].findings, ["1"]);
});
