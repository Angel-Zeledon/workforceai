// Unit tests of the approval permissions UI helpers (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { approvalReason, exceptionLabel, exceptionsApply, isFloor, validAskIf } from "./permissionsLogic.ts";

test("every approval says which rule asked and how to avoid it", () => {
  assert.deepEqual(approvalReason("perm.important:messages"), { why: "perm.why.important", avoid: "perm.avoid.important", category: "messages", rememberable: true });
  assert.equal(approvalReason("perm.important:other").rememberable, false);
  assert.deepEqual(approvalReason("perm.floor:payments"), { why: "perm.why.floor", avoid: "perm.avoid.floor", category: "payments", rememberable: false });
  assert.equal(approvalReason("limit:perm.per_day:messages").category, "messages");
  assert.equal(approvalReason("baseline.autonomy:approve_each").why, "perm.why.askAll");
  assert.equal(approvalReason("baseline.approval_action:send_proposal").why, "perm.why.alwaysList");
  assert.equal(approvalReason("org.amount").avoid, "perm.avoid.orgRules");
  assert.equal(approvalReason("dual_approval.action:make_payment").why, "perm.why.dual");
  assert.equal(approvalReason("limit:email.send.hourly").why, "perm.why.limit");
});

test("connection-backed approvals explain the gateway's reasons", () => {
  assert.equal(approvalReason("", ["always_approval"]).why, "perm.why.gatewayAlways");
  assert.equal(approvalReason(undefined, ["autonomy"]).why, "perm.why.askAll");
  assert.equal(approvalReason("", ["tainted_task"]).why, "perm.why.tainted");
  assert.equal(approvalReason("", []), null);
  assert.equal(approvalReason("something.new").why, "perm.why.rule");
});

test("ask-if conditions need at least one sane condition", () => {
  assert.equal(validAskIf({}), false);
  assert.equal(validAskIf(null), false);
  assert.equal(validAskIf({ per_day_over: 5 }), true);
  assert.equal(validAskIf({ per_day_over: 2.5 }), false);
  assert.equal(validAskIf({ amount_over: 0 }), true);
  assert.equal(validAskIf({ amount_over: -1 }), false);
  assert.equal(validAskIf({ new_recipient: true }), true);
});

test("labels, floor and where exceptions apply", () => {
  assert.deepEqual(exceptionLabel("never"), [{ key: "perm.mode.never" }]);
  assert.deepEqual(exceptionLabel("ask_if", { per_day_over: 3, new_recipient: true }).map((x) => x.key), ["perm.if.perDayShort", "perm.if.newShort"]);
  assert.equal(isFloor("contracts"), true);
  assert.equal(isFloor("messages"), false);
  assert.equal(exceptionsApply("important"), true);
  assert.equal(exceptionsApply("ask_all"), false);
});
