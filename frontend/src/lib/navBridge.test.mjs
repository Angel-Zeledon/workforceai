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
  assert.deepEqual([...ids].sort(), SECTIONS.map((s) => s.id).filter((id) => id !== "department").sort()); // department is company-only
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

// ---- D2: the short menu of company accounts ----
test("company accounts get a short menu led by their department, the rest under More", () => {
  const g = navGroups({ shopAvailable: false, company: true });
  assert.deepEqual(g.map((x) => x.id), ["main", "more"]);
  assert.deepEqual(g[0].items.map((s) => s.id), ["department", "pending", "org", "settings"]);
  const more = g[1].items.map((s) => s.id);
  assert.ok(!more.includes("shop"));
  const all = [...g[0].items, ...g[1].items].map((s) => s.id);
  assert.equal(new Set(all).size, all.length);
  assert.deepEqual([...all].sort(), SECTIONS.map((s) => s.id).filter((id) => id !== "shop").sort());
});

test("company menu keeps the shop (under More) where rewards are on", () => {
  const g = navGroups({ shopAvailable: true, company: true });
  assert.ok(g[1].items.some((s) => s.id === "shop"));
});

test("non-company menu is the one of today and has no department", () => {
  const g = navGroups({ shopAvailable: true, company: false });
  assert.deepEqual(g.map((x) => x.id), ["day", "company", "teach", "bottom"]);
  assert.ok(!g.flatMap((x) => x.items).some((s) => s.id === "department"));
  assert.equal(SECTIONS.find((s) => s.id === "department").href, "/d");
});

test("sectionOf maps /d to the department", () => {
  assert.equal(sectionOf("/d"), "department");
  assert.equal(sectionOf("/d/dep_1"), "department");
  assert.equal(sectionOf("/dashboard"), "dashboard");
});
