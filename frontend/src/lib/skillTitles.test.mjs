// Unit tests of the readable skill titles, the relative age and the stable case selection of the test panel
// (the effect that picks a case must be a fixed point: applying it to its own answer changes nothing, so it cannot loop).
import assert from "node:assert/strict";
import { test } from "node:test";
import { humanizeSkillId, relativeAge, skillTitle } from "./skillTitles.ts";
import { pickCaseId } from "./skillTestsLogic.ts";

test("a starter skill shows its written title, in the person's language", () => {
  assert.equal(skillTitle("b2b-proposal", "es"), "Propuesta comercial B2B");
  assert.match(skillTitle("b2b-proposal", "en"), /B2B/i);
  assert.equal(skillTitle("b2b-proposal", "pt"), "Propuesta comercial B2B"); // pt falls back to es
});

test("a skill without a written title shows its id as a sentence, never the raw identifier", () => {
  assert.equal(humanizeSkillId("b2b-proposal"), "B2B proposal");
  assert.equal(skillTitle("mi-habilidad-nueva", "es"), "Mi habilidad nueva");
  assert.equal(humanizeSkillId("sql_query"), "SQL query");
});

test("relativeAge speaks in words, not 14s", () => {
  const now = Date.parse("2026-11-22T12:00:00Z");
  const ago = (s) => new Date(now - s * 1000).toISOString();
  assert.equal(relativeAge(ago(14), "es", now), "hace un momento");
  assert.equal(relativeAge(ago(3 * 3600), "es", now), "hace 3 horas");
  assert.equal(relativeAge(ago(3 * 86400), "es", now), "hace 3 días");
  assert.equal(relativeAge(ago(86400), "es", now), "hace 1 día");
  assert.equal(relativeAge(ago(3 * 86400), "en", now), "3 days ago");
  assert.equal(relativeAge(ago(5), "en", now), "just now");
  assert.equal(relativeAge("not a date", "es", now), "");
});

test("pickCaseId is a fixed point: it never asks for a state change twice", () => {
  const cases = [{ id: "a" }, { id: "b" }];
  for (const cur of ["", "a", "b", "gone"]) {
    const once = pickCaseId(cases, cur);
    assert.equal(pickCaseId(cases, once), once);
  }
  assert.equal(pickCaseId([], ""), "");
  assert.equal(pickCaseId([], "x"), "");
  assert.equal(pickCaseId(cases, "b"), "b");
  assert.equal(pickCaseId(cases, "gone"), "a");
});
