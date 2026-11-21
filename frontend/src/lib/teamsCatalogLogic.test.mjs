// Unit tests of the ready-to-hire teams logic (run: node --test src/lib/teamsCatalogLogic.test.mjs).
import assert from "node:assert/strict";
import { test } from "node:test";
import { blockedKey, cardState, destinationChoices, installOutcome, orderForPack, splitAgents, weekByDay } from "./teamsCatalogLogic.ts";

const agent = (role, status) => ({ role, title: role, does: "", status, name: status === "new" ? "Nombre" : "", autonomy: "suggest", skills: [] });

test("splitAgents separates who arrives from who is already there", () => {
  const { arriving, already } = splitAgents({ agents: [agent("accounting", "new"), agent("assistant", "existing"), agent("sales", "new")] });
  assert.deepEqual(arriving.map((a) => a.role), ["accounting", "sales"]);
  assert.deepEqual(already.map((a) => a.role), ["assistant"]);
});

test("weekByDay orders the days and groups the flows of one day", () => {
  const w = (template, day) => ({ template, day, why: "", name: template, description: "" });
  const out = weekByDay([w("c", 5), w("a", 1), w("b", 1), w("d", 3)]);
  assert.deepEqual(out.map((d) => d.day), [1, 3, 5]);
  assert.deepEqual(out[0].items.map((i) => i.template), ["a", "b"]);
});

test("blockedKey: hireable teams say nothing, the others say why", () => {
  assert.equal(blockedKey({ can_install: true }), null);
  assert.equal(blockedKey({ can_install: false, blocked_reason: "office_full" }), "teams.blocked.officeFull");
  assert.equal(blockedKey({ can_install: false, blocked_reason: "plan_limit" }), "teams.blocked.planLimit");
  assert.equal(blockedKey({ can_install: false, blocked_reason: "nothing_new" }), "teams.blocked.nothingNew");
  assert.equal(blockedKey({ can_install: false }), "teams.blocked.nothingNew");
});

test("cardState: all there, some there, none there", () => {
  assert.equal(cardState({ new_count: 0, existing_count: 3 }), "all");
  assert.equal(cardState({ new_count: 2, existing_count: 1 }), "some");
  assert.equal(cardState({ new_count: 4, existing_count: 0 }), "none");
});

test("orderForPack puts the teams of the chosen pack first and keeps the rest", () => {
  const c = (key, packs) => ({ key, packs });
  const cards = [c("a", ["x"]), c("b", ["agency"]), c("c", []), c("d", ["agency", "x"])];
  assert.deepEqual(orderForPack(cards, "agency").map((x) => x.key), ["b", "d", "a", "c"]);
  assert.deepEqual(orderForPack(cards, "").map((x) => x.key), ["a", "b", "c", "d"]);
});

test("destinationChoices: General first; only departments the person enters and manages", () => {
  const o = (id, extra) => ({ id, name: id, general: false, can_enter: true, can_manage: true, ...extra });
  const out = destinationChoices([o("ventas"), o("general", { general: true }), o("legal", { can_enter: false }), o("rrhh", { can_manage: false })]);
  assert.deepEqual(out.map((x) => x.id), ["general", "ventas"]);
  assert.deepEqual(destinationChoices(undefined).map((x) => x.id), ["general"]);
});

test("installOutcome tells who came or that everybody was already there", () => {
  assert.deepEqual(installOutcome({ created: [{}, {}] }), { key: "teams.installed", count: 2 });
  assert.deepEqual(installOutcome({ created: [] }), { key: "teams.alreadyThere", count: 0 });
});
