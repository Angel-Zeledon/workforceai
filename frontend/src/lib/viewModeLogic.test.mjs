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

// The project detail in 4 tabs (tanda 2): Seguimiento, Plan, Costos, Permisos ("approvals").
const PROJECT = ["follow", "plan", "costs", "approvals"];

test("see everything shows every tab and folds nothing", () => {
  assert.deepEqual(splitTabs(PROJECT, SIMPLE_PROJECT_TABS, true), { shown: PROJECT, folded: [] });
});

test("simple mode keeps Seguimiento and Permisos open; Plan and Costos fold behind 'Más vistas'", () => {
  const s = splitTabs(PROJECT, SIMPLE_PROJECT_TABS, false);
  assert.deepEqual(s, { shown: ["follow", "approvals"], folded: ["plan", "costs"] });
  assert.equal(s.shown.length + s.folded.length, PROJECT.length); // nothing lost
});

test("a tab opened by a link (?tab=kanban opens Plan) stays visible, in the screen's order", () => {
  const s = splitTabs(PROJECT, SIMPLE_PROJECT_TABS, false, ["plan", null]);
  assert.deepEqual(s.shown, ["follow", "plan", "approvals"]);
  assert.deepEqual(s.folded, ["costs"]);
});

test("agent panel and dashboard keep the everyday parts open in simple mode", () => {
  const agent = ["chat", "work", "can", "knows", "profile"];
  assert.deepEqual(splitTabs(agent, SIMPLE_AGENT_TABS, false), { shown: ["chat", "work"], folded: ["can", "knows", "profile"] });
  // a tab opened by a link (the shield on "Cómo lo pensó" lives in Trabajo; permissions in Qué puede hacer) stays visible
  assert.deepEqual(splitTabs(agent, SIMPLE_AGENT_TABS, false, ["can"]).shown, ["chat", "work", "can"]);
  // the Office list view (tanda 3): 6 sections; Clientes, Pedidos, Pendientes and Archivos are links, not sections
  const dash = ["overview", "agents", "tasks", "quality", "costs", "reports"];
  assert.deepEqual(splitTabs(dash, SIMPLE_DASHBOARD, false), { shown: ["overview", "agents", "tasks"], folded: ["quality", "costs", "reports"] });
  assert.deepEqual(splitTabs(dash, SIMPLE_DASHBOARD, false, ["costs"]).shown, ["overview", "agents", "tasks", "costs"]);
});
