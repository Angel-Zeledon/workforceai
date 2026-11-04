// Unit tests of the project detail in 4 tabs and the translation of old links (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { DEFAULT_PLAN_VIEW, LEGACY_PROJECT_TABS, PLAN_VIEWS, PROJECT_TABS, resolveProjectTab } from "./tabsLogic.ts";

test("the detail has 4 tabs, Seguimiento first: Seguimiento, Plan, Costos, Permisos", () => {
  assert.deepEqual([...PROJECT_TABS], ["follow", "plan", "costs", "approvals"]);
});

test("Plan opens the map; the other views sit behind 'Otras vistas', the history of changes among them", () => {
  assert.equal(DEFAULT_PLAN_VIEW, "map");
  assert.equal(PLAN_VIEWS[0], "map");
  for (const v of ["kanban", "lanes", "timeline", "delegation", "health", "changes"]) assert.ok(PLAN_VIEWS.includes(v), v);
});

test("every old link opens the new tab that holds it, with the right view inside Plan", () => {
  const expected = {
    follow: { tab: "follow" },
    health: { tab: "plan", planView: "health" },
    map: { tab: "plan", planView: "map" },
    timeline: { tab: "plan", planView: "timeline" },
    kanban: { tab: "plan", planView: "kanban" },
    lanes: { tab: "plan", planView: "lanes" },
    delegation: { tab: "plan", planView: "delegation" },
    approvals: { tab: "approvals" },
    changes: { tab: "plan", planView: "changes" },
    costs: { tab: "costs" },
  };
  assert.deepEqual(Object.keys(expected).sort(), [...LEGACY_PROJECT_TABS].sort()); // none of the 10 is forgotten
  for (const old of LEGACY_PROJECT_TABS) assert.deepEqual(resolveProjectTab(old), expected[old], old);
});

test("new names work too; Plan alone keeps the view already chosen; 'permissions' is Permisos", () => {
  assert.deepEqual(resolveProjectTab("plan"), { tab: "plan" });
  assert.deepEqual(resolveProjectTab("costs"), { tab: "costs" });
  assert.deepEqual(resolveProjectTab("permissions"), { tab: "approvals" });
});

test("a link read from the address bar is forgiving with case and spaces", () => {
  const tab = new URLSearchParams("?p=prj_1&tab=%20Kanban%20").get("tab");
  assert.deepEqual(resolveProjectTab(tab), { tab: "plan", planView: "kanban" });
});

test("an unknown or missing tab gives nothing, so the screen keeps what it shows", () => {
  for (const raw of [null, undefined, "", "  ", "nope", "follow2"]) assert.equal(resolveProjectTab(raw), null, String(raw));
});
