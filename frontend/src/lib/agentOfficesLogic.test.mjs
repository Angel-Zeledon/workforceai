// Run: node --test src/lib/agentOfficesLogic.test.mjs
import assert from "node:assert/strict";
import { test } from "node:test";
import { askers, baseOffice, groupByOffice, invitableOffices, isGeneral, officeFilterChoices, officeLabel, officesSummary } from "./agentOfficesLogic.ts";

const G = "General";
const view = (o = {}) => ({ agent_id: "a", base_office: { id: "legal", name: "Legal" }, enabled: true, offices: [], ...o });
const row = (r) => ({ office_id: "ventas", name: "Ventas", status: "accepted", can_accept: false, can_remove: true, ...r });

test("the same agent says the same office on every source", () => {
  for (const src of [{}, { office_id: "" }, { office_id: "general", office_name: "Oficina General" }, null, undefined]) {
    assert.equal(officeLabel(baseOffice(src), G), G);
  }
  assert.equal(officeLabel(baseOffice({ office_id: "legal", office_name: "Legal" }), G), "Legal");
  assert.equal(officeLabel({ id: "x", name: "" }, G), "x");
  assert.ok(isGeneral("") && isGeneral("general") && !isGeneral("legal"));
});

test("summary lists other offices and marks pending ones; off switch hides them", () => {
  const base = { id: "legal", name: "Legal" };
  const v = view({ offices: [row(), row({ office_id: "rh", name: "RH", status: "pending", can_accept: true })] });
  const s = officesSummary(base, v, G);
  assert.deepEqual(s.others.map((o) => [o.label, o.pending]), [["Ventas", false], ["RH", true]]);
  assert.equal(s.pending, 1);
  const off = officesSummary(base, view({ enabled: false, offices: [row()] }), G);
  assert.equal(off.others.length, 0);
  assert.equal(officesSummary(base, null, G).enabled, false);
  assert.equal(officesSummary(baseOffice(null), null, G).baseLabel, G);
});

test("invitable offices skip base, already listed and closed ones", () => {
  const all = [{ id: "general" }, { id: "legal" }, { id: "ventas" }, { id: "rh" }, { id: "dir", can_enter: false }];
  const v = view({ offices: [row()] });
  assert.deepEqual(invitableOffices(all, { id: "legal", name: "Legal" }, v).map((o) => o.id), ["general", "rh"]);
  assert.deepEqual(invitableOffices(all, { id: "general", name: "" }, null).map((o) => o.id), ["legal", "ventas", "rh"]);
});

const E = [
  { id: "1", name: "Elena", title: "Abogada", office_id: "legal", office_name: "Legal" },
  { id: "2", name: "Sofía", title: "Asistente", office_id: "", office_name: "" },
  { id: "3", name: "Valeria", title: "Ventas", office_id: "ventas", office_name: "Ventas" },
  { id: "4", name: "Ana", title: "Contadora", office_id: "general" },
];

test("groups by base office, General first, with query and office filter", () => {
  const g = groupByOffice(E, { generalLabel: G });
  assert.deepEqual(g.map((x) => x.label), [G, "Legal", "Ventas"]);
  assert.deepEqual(g[0].items.map((e) => e.name), ["Ana", "Sofía"]);
  assert.deepEqual(groupByOffice(E, { generalLabel: G, query: "ABOGADA" }).map((x) => x.label), ["Legal"]);
  assert.deepEqual(groupByOffice(E, { generalLabel: G, query: "general" }).flatMap((x) => x.items.map((e) => e.id)), ["4", "2"]);
  assert.deepEqual(groupByOffice(E, { generalLabel: G, office: "ventas" }).flatMap((x) => x.items.map((e) => e.id)), ["3"]);
  // an agent that also works in Ventas shows up under the Ventas filter, still grouped by its base office
  const others = { 1: [{ id: "ventas", label: "Ventas", pending: false, canAccept: false, canRemove: true }] };
  const f = groupByOffice(E, { generalLabel: G, office: "ventas", others });
  assert.deepEqual(f.map((x) => [x.label, x.items.map((e) => e.id)]), [["Legal", ["1"]], ["Ventas", ["3"]]]);
  assert.deepEqual(officeFilterChoices(E, G).map((c) => c.label), [G, "Legal", "Ventas"]);
});

test("who may ask", () => {
  assert.equal(askers({ visibility: "private" }).kind, "onlyOwner");
  assert.equal(askers({ visibility: "directory" }).kind, "company");
  assert.equal(askers({ visibility: "office", owner_kind: "office", office_id: "general" }).kind, "company");
  assert.equal(askers({ visibility: "office", owner_kind: "office", office_id: "legal" }).kind, "office");
  assert.deepEqual(askers({ visibility: "people", shares: [{}, {}] }), { kind: "people", people: 2, extra: [] });
  assert.deepEqual(askers({ visibility: "office", office_id: "legal" }, view({ offices: [row(), row({ office_id: "rh", name: "RH", status: "pending" })] })).extra, ["Ventas"]);
  assert.deepEqual(askers({ visibility: "office", office_id: "legal" }, view({ enabled: false, offices: [row()] })).extra, []);
});
