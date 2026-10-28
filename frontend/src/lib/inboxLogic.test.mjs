// Unit tests of the fast approvals inbox rules (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { batchGroupOf, clampIndex, deckOf, keyToCommand, needsConfirm, swipeIntent } from "./inboxLogic.ts";

test("keyboard: A R J K E, case-insensitive, no modifiers", () => {
  assert.equal(keyToCommand({ key: "a" }), "approve");
  assert.equal(keyToCommand({ key: "R" }), "reject");
  assert.equal(keyToCommand({ key: "j" }), "next");
  assert.equal(keyToCommand({ key: "ArrowUp" }), "prev");
  assert.equal(keyToCommand({ key: "e" }), "expand");
  assert.equal(keyToCommand({ key: "a", ctrlKey: true }), null);
  assert.equal(keyToCommand({ key: "x" }), null);
});

test("swipe: right approves, left rejects, short or vertical does nothing", () => {
  assert.equal(swipeIntent(120, 10), "approve");
  assert.equal(swipeIntent(-130, 5), "reject");
  assert.equal(swipeIntent(40, 0), null);
  assert.equal(swipeIntent(120, 200), null);
});

test("high risk never approves quickly without a confirm; rejecting needs none", () => {
  assert.equal(needsConfirm({ risk: "high", high_risk: true }, "approve"), true);
  assert.equal(needsConfirm({ risk: "low", high_risk: true }, "approve"), true); // double approval
  assert.equal(needsConfirm({ risk: "high", high_risk: true }, "reject"), false);
  assert.equal(needsConfirm({ risk: "low", high_risk: false }, "approve"), false);
});

test("batch only where the API allows it", () => {
  const groups = [{ key: "p|a", project_id: "p", action: "a", count: 3, approval_ids: [], batchable: true }, { key: "p|b", project_id: "p", action: "b", count: 2, approval_ids: [], batchable: false, reason: "risk" }];
  assert.equal(batchGroupOf({ group_key: "p|a" }, groups).count, 3);
  assert.equal(batchGroupOf({ group_key: "p|b" }, groups), null);
  assert.equal(batchGroupOf({}, groups), null);
});

test("deck and index", () => {
  const items = [{ id: "1", status: "pending" }, { id: "2", status: "approved" }, { id: "3", status: "pending" }];
  assert.deepEqual(deckOf(items, new Set(["3"])).map((x) => x.id), ["1"]);
  assert.equal(clampIndex(5, 2), 1);
  assert.equal(clampIndex(-1, 2), 0);
  assert.equal(clampIndex(3, 0), 0);
});
