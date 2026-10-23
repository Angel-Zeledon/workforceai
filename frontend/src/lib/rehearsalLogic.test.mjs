// Unit tests of the rehearsal report helpers (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { approvalRate, nextVerdict, previewRows, tally } from "./rehearsalLogic.ts";

test("tally counts verdicts and would-decisions", () => {
  const c = tally([
    { verdict: "would_approve", would_decision: "needs_approval" },
    { verdict: "would_reject", would_decision: "allowed" },
    { verdict: "", would_decision: "denied" },
  ]);
  assert.deepEqual(c, { total: 3, would_approve: 1, would_reject: 1, undecided: 1, would_need_approval: 1, would_be_denied: 1 });
});

test("approvalRate is null until something was decided", () => {
  assert.equal(approvalRate({ would_approve: 0, would_reject: 0 }), null);
  assert.equal(approvalRate({ would_approve: 3, would_reject: 1 }), 0.75);
});

test("previewRows puts recipients and money first and the overflow marker last", () => {
  const rows = previewRows({ body: "x", "…": "+3", amount: "4200", to: "a@b.com", zeta: "z" });
  assert.deepEqual(rows.map(([k]) => k), ["to", "amount", "body", "zeta", "…"]);
  assert.deepEqual(previewRows(null), []);
});

test("nextVerdict toggles", () => {
  assert.equal(nextVerdict("", "would_approve"), "would_approve");
  assert.equal(nextVerdict("would_approve", "would_approve"), "");
  assert.equal(nextVerdict("would_approve", "would_reject"), "would_reject");
});
