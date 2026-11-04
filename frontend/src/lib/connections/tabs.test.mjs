// Unit tests of the Conexiones tabs: old links open the new tab that contains them (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { CONN_PAGE_TABS, accessViewOf, resolveConnTab } from "./tabs.ts";

test("there are three tabs", () => {
  assert.deepEqual([...CONN_PAGE_TABS], ["connections", "access", "usage"]);
});

test("the six old tabs open the new one that contains them", () => {
  assert.deepEqual(resolveConnTab("connections"), { tab: "connections" });
  assert.deepEqual(resolveConnTab("inboxes"), { tab: "connections", section: "inboxes" });
  assert.deepEqual(resolveConnTab("matrix"), { tab: "access", section: "matrix" });
  assert.deepEqual(resolveConnTab("capabilities"), { tab: "access", section: "capabilities" });
  assert.deepEqual(resolveConnTab("usage"), { tab: "usage" });
  assert.deepEqual(resolveConnTab("limits"), { tab: "usage", section: "limits" });
});

test("the new names and the sections of Conexiones work too", () => {
  assert.deepEqual(resolveConnTab("access"), { tab: "access" });
  assert.deepEqual(resolveConnTab("databases"), { tab: "connections", section: "databases" });
  assert.deepEqual(resolveConnTab("db"), { tab: "connections", section: "databases" });
  assert.deepEqual(resolveConnTab("links"), { tab: "connections", section: "links" });
  assert.deepEqual(resolveConnTab("accounts"), { tab: "connections", section: "accounts" });
});

test("case and spaces do not matter", () => {
  assert.deepEqual(resolveConnTab("  Matrix "), { tab: "access", section: "matrix" });
  assert.deepEqual(resolveConnTab("LIMITS"), { tab: "usage", section: "limits" });
});

test("no tab or an unknown one opens Conexiones", () => {
  assert.deepEqual(resolveConnTab(null), { tab: "connections" });
  assert.deepEqual(resolveConnTab(undefined), { tab: "connections" });
  assert.deepEqual(resolveConnTab(""), { tab: "connections" });
  assert.deepEqual(resolveConnTab("nope"), { tab: "connections" });
  assert.deepEqual(resolveConnTab("__proto__"), { tab: "connections" });
});

test("the result is a copy: changing it does not change the next answer", () => {
  const a = resolveConnTab("matrix");
  a.tab = "usage";
  assert.deepEqual(resolveConnTab("matrix"), { tab: "access", section: "matrix" });
});

test("Quién puede usar qué opens the table unless the link asked for the summary per agent", () => {
  assert.equal(accessViewOf(undefined), "matrix");
  assert.equal(accessViewOf("matrix"), "matrix");
  assert.equal(accessViewOf("capabilities"), "capabilities");
  assert.equal(accessViewOf("limits"), "matrix");
});
