// Unit tests of the fast approvals inbox rules (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { batchGroupOf, clampIndex, decidersLine, deckOf, itemDecidable, keyToCommand, needsConfirm, swipeIntent } from "./inboxLogic.ts";

test("D3: the server says who may decide each item; otherwise the role's permission", () => {
  assert.equal(itemDecidable({ can_decide: true }, false), true); // an office's responsible without approvals:decide
  assert.equal(itemDecidable({ can_decide: false }, true), false); // an admin outside the office
  assert.equal(itemDecidable({}, true), true); // older servers and the demo
  assert.equal(itemDecidable({}, false), false);
  assert.equal(decidersLine({ approvers: ["Ana", "", "Luis"] }), "Ana, Luis");
  assert.equal(decidersLine({}), "");
});

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

import { pendingTotal } from "./inboxLogic.ts";
test("one pending number: pending approvals plus open questions, decided ones never count", () => {
  const approvals = { a: { status: "pending" }, b: { status: "approved" }, c: { status: "pending" } };
  const questions = { q1: { status: "open" }, q2: { status: "answered" } };
  assert.equal(pendingTotal(approvals, questions), 3);
  assert.equal(pendingTotal([], []), 0);
  assert.equal(pendingTotal([{ status: "rejected" }], [{ status: "open" }]), 1);
});

test("filter of Pendientes: only ?filter=email is known, the link goes back to the same address", async () => {
  const { parseInboxFilter, inboxHref } = await import("./inboxLogic.ts");
  assert.equal(parseInboxFilter("email"), "email");
  assert.equal(parseInboxFilter(" EMAIL "), "email");
  assert.equal(parseInboxFilter(null), "");
  assert.equal(parseInboxFilter("payments"), "");
  assert.equal(inboxHref("email"), "/approvals/inbox?filter=email");
  assert.equal(inboxHref(""), "/approvals/inbox");
  assert.equal(parseInboxFilter(new URL(`http://x${inboxHref("email")}`).searchParams.get("filter")), "email");
});

test("email filter: keeps the actions that send or prepare an e-mail, nothing else", async () => {
  const { filterInbox, isEmailAction } = await import("./inboxLogic.ts");
  for (const c of ["email.send", "gmail.send_reply", "outlook.send", "draft_reply", "send_reply", "mail.send"]) assert.equal(isEmailAction(c), true, c);
  for (const c of ["send_contract", "publish_post", "pay_invoice", "categorize_expense", "code.run", "outlook.create_event", "gmail.label", "", null]) assert.equal(isEmailAction(c), false, String(c));
  const items = [{ id: "1", action: "email.send" }, { id: "2", action: "send_contract" }, { id: "3", action: "draft_reply" }];
  assert.deepEqual(filterInbox(items, "email").map((i) => i.id), ["1", "3"]);
  assert.equal(filterInbox(items, "").length, 3);
});

test("outbox drafts: waiting ones (also held in the cancel window) and the last ended ones", async () => {
  const { emailDrafts } = await import("./inboxLogic.ts");
  const ds = ["draft", "pending_approval", "held", "blocked", "sent", "cancelled", "rejected", "sent", "sent", "queued"].map((status, i) => ({ id: String(i), status }));
  const r = emailDrafts(ds);
  assert.deepEqual(r.waiting.map((d) => d.status), ["draft", "pending_approval", "held", "blocked"]);
  assert.equal(r.done.length, 4);
  assert.ok(r.done.every((d) => ["sent", "cancelled", "rejected"].includes(d.status)));
  assert.equal(emailDrafts(ds, 1).done.length, 1);
});
