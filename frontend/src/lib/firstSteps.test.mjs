import { test } from "node:test";
import assert from "node:assert/strict";
import { isDecisionRequest } from "./firstSteps.ts";

test("isDecisionRequest: approval decisions, batch decisions and answers count; anything else does not", () => {
  assert.equal(isDecisionRequest("POST", "/approvals/ap_1/decision"), true);
  assert.equal(isDecisionRequest("post", "/approvals/ap%201/decision?x=1"), true);
  assert.equal(isDecisionRequest("POST", "/approvals/batch"), true);
  assert.equal(isDecisionRequest("POST", "/questions/q_9/answer"), true);
  assert.equal(isDecisionRequest("POST", "/questions/answer-batch"), true);
  assert.equal(isDecisionRequest("GET", "/approvals/ap_1/decision"), false);
  assert.equal(isDecisionRequest("POST", "/approvals/ap_1/counterargument"), false);
  assert.equal(isDecisionRequest("POST", "/requests"), false);
});
