// Unit tests of the office templates logic (run: node --test src/lib/officeTemplatesLogic.test.mjs).
import assert from "node:assert/strict";
import { test } from "node:test";
import { EXCLUDED_KEYS, MAX_TEMPLATE_BYTES, checkFile, fileText, includesOf, installLines, itemStatusKey, previewPeople } from "./officeTemplatesLogic.ts";

const file = (over = {}) => ({
  format: "aiw.office-template", format_version: 1, kind: "department", name: "Contabilidad",
  departments: [{ key: "d1", name: "Contabilidad" }],
  agents: [{ department: "d1", role: "accounting", instructions: "Usa el catálogo" }, { department: "d1", role: "analyst" }],
  skills: [{ name: "cierre" }], workflows: [], schedules: [{ template: "budget_planning" }], goals: [{ title: "Cerrar a tiempo" }], ...over,
});

test("includesOf counts what the file carries and leaves out what is empty", () => {
  const lines = includesOf(file());
  const by = Object.fromEntries(lines.map((l) => [l.key, l.count]));
  assert.equal(by["otpl.inc.people"], 2);
  assert.equal(by["otpl.inc.instructions"], 1);
  assert.equal(by["otpl.inc.skills"], 1);
  assert.equal(by["otpl.inc.workflows"], undefined, "no work templates, no line");
});

test("the never-included list names the private things in plain words", () => {
  for (const k of ["otpl.exc.connections", "otpl.exc.memory", "otpl.exc.files", "otpl.exc.people", "otpl.exc.trust"]) {
    assert.ok(EXCLUDED_KEYS.includes(k), k);
  }
});

test("checkFile refuses what the server would refuse before uploading", () => {
  assert.equal(checkFile("a.json", MAX_TEMPLATE_BYTES + 1), "otpl.import.tooLarge");
  assert.equal(checkFile("a.zip", 10), "otpl.import.notJson");
  assert.equal(checkFile("a.json", 10, "{nope"), "otpl.import.notJson");
  assert.equal(checkFile("a.json", 10, JSON.stringify({ format: "aiw.team/1" })), "otpl.import.notTemplate");
  assert.equal(checkFile("a.json", 10, JSON.stringify(file({ format_version: 2 }))), "otpl.import.newer");
  assert.equal(checkFile("Plantilla.JSON", 10, JSON.stringify(file())), null);
  assert.equal(checkFile("a.json", 10), null, "without the text only name and size are checked");
});

test("fileText is readable JSON that parses back to the same template", () => {
  const t = file();
  const text = fileText(t);
  assert.ok(text.includes("\n  \"format\""));
  assert.deepEqual(JSON.parse(text), t);
});

test("previewPeople adds up every department", () => {
  const a = (status) => ({ role: "x", title: "", does: "", status, name: "", autonomy: "suggest", skills: [] });
  const p = previewPeople({ departments: [{ agents: [a("new"), a("existing")] }, { agents: [a("new")] }] });
  assert.deepEqual(p, { arriving: 2, already: 1 });
});

test("installLines says what arrived, with archived goals apart", () => {
  const lines = installLines({ created: [{ id: "a", name: "Ana", role: "accounting" }], skills: ["cierre"], workflows: [], schedules: 0,
    goals: [{ name: "g1", status: "active" }, { name: "g2", status: "archived" }] });
  assert.deepEqual(lines, [
    { key: "otpl.done.people", count: 1 }, { key: "otpl.done.skills", count: 1 },
    { key: "otpl.done.goals", count: 1 }, { key: "otpl.done.goalsArchived", count: 1 },
  ]);
  assert.deepEqual(installLines({ created: [], skills: [], workflows: [], schedules: 0, goals: [] }), []);
  assert.equal(itemStatusKey({ status: "existing" }), "otpl.status.existing");
});
