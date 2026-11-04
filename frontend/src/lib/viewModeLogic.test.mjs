// Unit tests of simple mode / see everything (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { resolveViewAll, splitTabs, viewAllKey, SIMPLE_PROJECT_TABS, SIMPLE_AGENT_TABS, SIMPLE_DASHBOARD } from "./viewModeLogic.ts";

test("existing accounts see everything: an older server (no default) or 'full' means everything open", () => {
  assert.equal(resolveViewAll(null, undefined), true);
  assert.equal(resolveViewAll(null, "full"), true);
});

test("new accounts start simple", () => {
  assert.equal(resolveViewAll(null, "simple"), false);
});

test("the person's choice wins over the server default, both ways", () => {
  assert.equal(resolveViewAll("1", "simple"), true);
  assert.equal(resolveViewAll("0", "full"), false);
  assert.equal(resolveViewAll("garbage", "simple"), false);
});

test("the choice is remembered per person", () => {
  assert.notEqual(viewAllKey("u1"), viewAllKey("u2"));
  assert.equal(viewAllKey(null), "wf.viewAll.anon");
});

const PROJECT = ["follow", "health", "map", "timeline", "kanban", "lanes", "delegation", "approvals", "changes", "costs"];

test("see everything shows every tab and folds nothing", () => {
  assert.deepEqual(splitTabs(PROJECT, SIMPLE_PROJECT_TABS, true), { shown: PROJECT, folded: [] });
});

test("simple mode keeps Seguimiento and Aprobaciones open; Kanban, lanes, delegation and costs fold", () => {
  const s = splitTabs(PROJECT, SIMPLE_PROJECT_TABS, false);
  assert.deepEqual(s.shown, ["follow", "approvals"]);
  for (const k of ["kanban", "lanes", "delegation", "costs", "timeline"]) assert.ok(s.folded.includes(k), k);
  assert.equal(s.shown.length + s.folded.length, PROJECT.length); // nothing lost
});

test("a tab opened by a link or with something waiting stays visible, in the screen's order", () => {
  const s = splitTabs(PROJECT, SIMPLE_PROJECT_TABS, false, ["kanban", "changes", null]);
  assert.deepEqual(s.shown, ["follow", "kanban", "approvals", "changes"]);
});

test("agent panel and dashboard keep the everyday parts open in simple mode", () => {
  const agent = ["chat", "state", "tasks", "can", "chats", "memory", "reports", "files", "activity", "profile", "thoughts"];
  assert.deepEqual(splitTabs(agent, SIMPLE_AGENT_TABS, false).shown, ["chat", "state", "tasks", "can", "reports"]);
  const dash = ["overview", "customers", "requests", "tasks", "agents", "conversations", "approvals", "reports", "files", "quality", "costs"];
  assert.deepEqual(splitTabs(dash, SIMPLE_DASHBOARD, false).shown, ["overview", "tasks", "agents"]);
});
