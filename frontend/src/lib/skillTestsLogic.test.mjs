// Unit tests of the "probar una habilidad" logic (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { canCompare, caseIssue, failKey, fmtSeconds, fmtUSD, groupRuns, testedLabel } from "./skillTestsLogic.ts";

const run = (id, o = {}) => ({ id, version: 1, active: false, status: "done", output: "", blocked: [], ...o });

test("testedLabel says never or when the version was last tested", () => {
  assert.deepEqual(testedLabel(null, 2), { kind: "never" });
  assert.deepEqual(testedLabel({ last_by_version: [] }, 2), { kind: "never" });
  const o = { last_by_version: [{ version: 2, at: "2026-11-20T10:00:00Z", used_skill: true }, { version: 1, at: "2026-11-01T10:00:00Z", used_skill: false }] };
  assert.deepEqual(testedLabel(o, 2), { kind: "at", at: "2026-11-20T10:00:00Z", usedSkill: true });
  assert.deepEqual(testedLabel(o, 3), { kind: "never" });
});

test("groupRuns joins the two runs of a comparison, the tested version first", () => {
  const g = groupRuns([run("a", { pair_id: "p", version: 1, active: true }), run("b", { pair_id: "p", version: 2 }), run("c")]);
  assert.equal(g.length, 2);
  assert.deepEqual(g[0].map((r) => r.id), ["b", "a"]);
  assert.deepEqual(g[1].map((r) => r.id), ["c"]);
});

test("canCompare needs another active version", () => {
  assert.equal(canCompare(0, 1), false);
  assert.equal(canCompare(2, 2), false);
  assert.equal(canCompare(1, 2), true);
});

test("money and time formats", () => {
  assert.equal(fmtUSD(0), "$0.00");
  assert.equal(fmtUSD(0.0042), "$0.0042");
  assert.equal(fmtUSD(1.234), "$1.23");
  assert.equal(fmtSeconds(420), "420 ms");
  assert.equal(fmtSeconds(2500), "2.5 s");
});

test("caseIssue mirrors the backend limits", () => {
  const lim = { cases: 10, title: 5, prompt: 10, expect: 3 };
  assert.equal(caseIssue({ title: "", prompt: "  ", expect: "" }, lim), "prompt_required");
  assert.equal(caseIssue({ title: "", prompt: "x".repeat(11), expect: "" }, lim), "prompt_too_long");
  assert.equal(caseIssue({ title: "", prompt: "x", expect: "abcd" }, lim), "expect_too_long");
  assert.equal(caseIssue({ title: "abcdef", prompt: "x", expect: "" }, lim), "title_too_long");
  assert.equal(caseIssue({ title: "ok", prompt: "x", expect: "ab" }, lim), null);
});

test("failKey drops the detail after the colon", () => {
  assert.equal(failKey("budget_exceeded:org"), "budget_exceeded");
  assert.equal(failKey("blocked:kill_switch_active"), "blocked");
  assert.equal(failKey(undefined), "unknown");
});
