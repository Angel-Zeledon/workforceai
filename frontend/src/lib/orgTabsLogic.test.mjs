// Unit tests of "Agentes y oficinas" in 4 tabs and the translation of old links (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { AGENT_SECTIONS, LEGACY_ORG_TABS, ORG_TABS, resolveOrgTab, splitBySpace } from "./orgTabsLogic.ts";
import { SIMPLE_ORG_TABS, splitTabs } from "./viewModeLogic.ts";

test("the page has 4 tabs, Agentes first: Agentes, Oficinas, Organigrama, Reglas de delegación", () => {
  assert.deepEqual([...ORG_TABS], ["agents", "offices", "chart", "delegation"]);
  assert.deepEqual([...AGENT_SECTIONS], ["directory", "roles"]);
});

test("every old link opens the new tab that holds it", () => {
  const expected = {
    chart: { tab: "chart" },
    offices: { tab: "offices" },
    directory: { tab: "agents", section: "directory" },
    departments: { tab: "offices", focus: "departments" },
    roles: { tab: "agents", section: "roles" },
    delegation: { tab: "delegation" },
  };
  for (const old of LEGACY_ORG_TABS) assert.deepEqual(resolveOrgTab(old), expected[old], old);
});

test("the new names work too, with any case or spaces; unknown or empty gives null", () => {
  assert.deepEqual(resolveOrgTab("agents"), { tab: "agents" });
  assert.deepEqual(resolveOrgTab("  Departments "), { tab: "offices", focus: "departments" });
  assert.equal(resolveOrgTab("teams"), null);
  assert.equal(resolveOrgTab(""), null);
  assert.equal(resolveOrgTab(null), null);
  assert.equal(resolveOrgTab(undefined), null);
});

test("simple mode keeps Agentes and Oficinas open; Organigrama and Reglas de delegación one click away", () => {
  assert.deepEqual(splitTabs(ORG_TABS, SIMPLE_ORG_TABS, false), { shown: ["agents", "offices"], folded: ["chart", "delegation"] });
  // a deep link to a folded tab keeps it visible
  assert.deepEqual(splitTabs(ORG_TABS, SIMPLE_ORG_TABS, false, ["delegation"]).shown, ["agents", "offices", "delegation"]);
  assert.deepEqual(splitTabs(ORG_TABS, SIMPLE_ORG_TABS, true).shown, [...ORG_TABS]);
});

test("splitBySpace: a department with an office of the same id has its own space; General never counts", () => {
  const depts = [{ id: "d-sales" }, { id: "d-legal" }, { id: "general" }, { id: "d-ops" }];
  const offices = [{ id: "general", general: true }, { id: "d-legal", general: false }];
  const r = splitBySpace(depts, offices);
  assert.deepEqual(r.withSpace.map((d) => d.id), ["d-legal"]);
  assert.deepEqual(r.withoutSpace.map((d) => d.id), ["d-sales", "general", "d-ops"]);
  assert.deepEqual(splitBySpace([], offices), { withSpace: [], withoutSpace: [] });
  assert.deepEqual(splitBySpace(depts, []).withoutSpace.length, 4);
});
