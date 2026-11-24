// Run: node --test src/lib/officePageLogic.test.mjs
import assert from "node:assert/strict";
import { test } from "node:test";
import { baseOffice } from "./agentOfficesLogic.ts";
import { OFFICE_PAGE_TABS, officeAgents, resolveOfficeTab, skillsOfOffice } from "./officePageLogic.ts";

test("six tabs; names and aliases resolve, unknown opens People", () => {
  assert.equal(OFFICE_PAGE_TABS.length, 6);
  assert.equal(resolveOfficeTab("puestos"), "positions");
  assert.equal(resolveOfficeTab("memory"), "memory");
  assert.equal(resolveOfficeTab("x"), "people");
});

test("agents of an office: base ones and the ones that also work there", () => {
  const E = [
    { id: "1", name: "Elena", office_id: "legal", office_name: "Legal" },
    { id: "2", name: "Sofía", office_id: "" },
    { id: "3", name: "Ana", office_id: "ventas", office_name: "Ventas" },
  ];
  const alsoIn = { 3: [{ id: "legal", pending: true }], 2: [{ id: "legal", pending: false }] };
  const of = (id, a) => officeAgents(E, id, (e) => baseOffice(e).id, a);
  const l = of("legal", alsoIn);
  assert.deepEqual(l.base.map((e) => e.id), ["1"]);
  assert.deepEqual(l.visiting.map((v) => [v.entry.id, v.pending]), [["3", true], ["2", false]]);
  assert.deepEqual(of("general").base.map((e) => e.id), ["2"]);
});

test("skills split into the office's own and the company's", () => {
  const S = [{ id: "a", office_id: "legal" }, { id: "b", office_id: "" }, { id: "c" }, { id: "d", office_id: "ventas" }];
  const r = skillsOfOffice(S, "legal");
  assert.deepEqual([r.own.map((s) => s.id), r.company.map((s) => s.id)], [["a"], ["b", "c"]]);
});
