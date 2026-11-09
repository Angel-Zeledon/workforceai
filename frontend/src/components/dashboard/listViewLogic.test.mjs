// Unit tests of the Office's "Vista lista" (the old Dashboard). Run: npm run test:unit.
import assert from "node:assert/strict";
import { test } from "node:test";
import { GO_TO, LIST_SECTIONS, listTarget, requestedSection, sectionHref, targetOfSearch } from "./listViewLogic.ts";

// The 11 sections the Dashboard had before (their ids were the `dash-nav-*` test ids).
const OLD = ["overview", "customers", "requests", "tasks", "agents", "conversations", "approvals", "reports", "files", "quality", "costs"];

test("the list view keeps Resumen, Agentes, Tareas, Calidad and Costos; Informes stay (no screen of their own)", () => {
  assert.deepEqual([...LIST_SECTIONS], ["overview", "agents", "tasks", "quality", "costs", "reports"]);
});

test("what has its own screen becomes a link to it", () => {
  assert.deepEqual(GO_TO.map((g) => [g.id, g.href]), [
    ["customers", "/clients"], ["requests", "/chats"], ["approvals", "/approvals/inbox"], ["files", "/files"],
  ]);
  assert.deepEqual(listTarget("customers"), { kind: "route", href: "/clients" });
  assert.deepEqual(listTarget("requests"), { kind: "route", href: "/chats" });
  assert.deepEqual(listTarget("approvals"), { kind: "route", href: "/approvals/inbox" });
  assert.deepEqual(listTarget("files"), { kind: "route", href: "/files" });
});

test("no old section is lost: each one opens its own screen or a section that stays", () => {
  for (const s of OLD) {
    const t = listTarget(s);
    if (t.kind === "route") assert.ok(GO_TO.some((g) => g.href === t.href), `${s} -> ${t.href}`);
    else assert.ok(LIST_SECTIONS.includes(t.section), `${s} -> ${t.section}`);
  }
  // the sections that stay open themselves
  for (const s of LIST_SECTIONS) assert.deepEqual(listTarget(s), { kind: "section", section: s, anchor: null });
});

test("conversations between agents move under Agentes; the approvals history stays in Resumen", () => {
  assert.deepEqual(listTarget("conversations"), { kind: "section", section: "agents", anchor: "conversations" });
  assert.deepEqual(listTarget("history"), { kind: "section", section: "overview", anchor: "decisions" });
});

test("old addresses: ?section=, ?tab= and ?s= all work, any case, with aliases in es/en/pt", () => {
  assert.deepEqual(targetOfSearch("?section=customers"), { kind: "route", href: "/clients" });
  assert.deepEqual(targetOfSearch("?tab=Files"), { kind: "route", href: "/files" });
  assert.deepEqual(targetOfSearch("s=pendientes"), { kind: "route", href: "/approvals/inbox" });
  assert.deepEqual(targetOfSearch(new URLSearchParams("section=costos")), { kind: "section", section: "costs", anchor: null });
  assert.deepEqual(targetOfSearch("?tab=relatórios"), { kind: "section", section: "reports", anchor: null });
  assert.equal(requestedSection("?section=quality&tab=files"), "quality"); // section wins
});

test("an empty or unknown section opens Resumen, never an empty page", () => {
  for (const q of ["", "?", "?section=", "?section=nope", null, undefined]) {
    assert.deepEqual(targetOfSearch(q), { kind: "section", section: "overview", anchor: null });
  }
});

test("each section has a shareable address that leads back to it", () => {
  assert.equal(sectionHref("overview"), "/dashboard");
  for (const s of LIST_SECTIONS) {
    const t = targetOfSearch(sectionHref(s).split("?")[1] ?? "");
    assert.equal(t.kind === "section" && t.section, s);
  }
});
