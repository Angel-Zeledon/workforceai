// Unit tests of the router bridge (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { go, openInOffice, setNavigator } from "./navBridge.ts";

test("go uses the registered router", () => {
  const seen = [];
  setNavigator((href) => seen.push(href));
  try { go("/chats"); } finally { setNavigator(null); }
  assert.deepEqual(seen, ["/chats"]);
});

test("openInOffice selects the agent and then navigates to the office", () => {
  const calls = [];
  setNavigator((href) => calls.push(["nav", href]));
  try { openInOffice("sales", (id) => calls.push(["select", id])); } finally { setNavigator(null); }
  // Selecting first means the office opens already focused on the agent.
  assert.deepEqual(calls, [["select", "sales"], ["nav", "/"]]);
});

// ---- the grouped menu (UX tanda 1) ----
import { NAV_GROUPS, navGroups, sectionOf, SECTIONS } from "./navBridge.ts";

test("the menu is grouped: Tu día, Tu empresa, Enseñar y conectar; Tienda and Ajustes at the bottom", () => {
  const g = navGroups({ shopAvailable: true });
  assert.deepEqual(g.map((x) => x.id), ["day", "company", "teach", "bottom"]);
  assert.deepEqual(g[0].items.map((s) => s.id), ["office", "pending", "chats", "projects"]);
  assert.deepEqual(g[1].items.map((s) => s.id), ["org", "customers", "drive", "meetings"]);
  assert.deepEqual(g[2].items.map((s) => s.id), ["skills", "connections"]);
  assert.deepEqual(g[3].items.map((s) => s.id), ["shop", "settings"]);
});

test("Clientes and Conexiones are in the main menu, Pendientes inside the app", () => {
  const ids = NAV_GROUPS.flatMap((g) => g.items);
  for (const id of ["customers", "connections", "pending"]) assert.ok(ids.includes(id), id);
  const href = (id) => SECTIONS.find((s) => s.id === id).href;
  assert.equal(href("pending"), "/approvals/inbox");
  assert.equal(href("customers"), "/clients");
  assert.equal(href("connections"), "/connections");
});

test("no section is lost: every section is in exactly one group", () => {
  const ids = NAV_GROUPS.flatMap((g) => g.items);
  assert.deepEqual([...ids].sort(), SECTIONS.map((s) => s.id).sort());
  assert.equal(new Set(ids).size, ids.length);
});

test("the shop hides when the rewards system is off, nothing else does", () => {
  const g = navGroups({ shopAvailable: false });
  assert.deepEqual(g[3].items.map((s) => s.id), ["settings"]);
});

test("sectionOf knows the new homes and the old links", () => {
  assert.equal(sectionOf("/approvals/inbox"), "pending");
  assert.equal(sectionOf("/approvals"), "pending");
  assert.equal(sectionOf("/approvals/ap-1"), "pending");
  assert.equal(sectionOf("/clients"), "customers");
  assert.equal(sectionOf("/connections"), "connections");
  assert.equal(sectionOf("/dashboard"), "dashboard");
  assert.equal(sectionOf("/"), "office");
});
