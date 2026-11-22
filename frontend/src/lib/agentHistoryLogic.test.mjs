// Unit tests of the agent's configuration history in plain words (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { applyFields, canRestoreVersion, changeRows, fieldLabel, fieldValue, headline, skipLines, whatChanged } from "./agentHistoryLogic.ts";

// a translator that shows the key and its params, so the tests read what the UI decides
const t = (k, p) => (p ? `${k}(${Object.entries(p).map(([a, b]) => `${a}=${b}`).join(",")})` : k);

const v = (o) => ({ version: 2, created_at: "2026-11-20T10:00:00Z", actor: "u1", actor_kind: "person", actor_name: "Carla", source: "PUT /agents/{id}/profile", fields: ["instructions"], ...o });

test("a person's change names who and what", () => {
  assert.equal(headline(v({}), t), "hist.line.person(who=Carla,what=hist.field.instructions)");
  assert.equal(headline(v({ actor_name: "" }), t), "hist.line.person(who=hist.someone,what=hist.field.instructions)");
});

test("the system's automatic demotion after an incident has its own sentence", () => {
  assert.equal(headline(v({ actor_kind: "system", actor_name: undefined, source: "trust.auto_demotion", fields: ["autonomy"] }), t), "hist.line.system_demoted");
  assert.equal(headline(v({ actor_kind: "system", source: "trust.promotion_accepted", fields: ["autonomy"] }), t), "hist.line.system_trust(what=hist.field.autonomy)");
  assert.equal(headline(v({ actor_kind: "system", source: "agent.control_changed", fields: ["paused"] }), t), "hist.line.system(what=hist.field.paused)");
});

test("baseline, hire and restore lines", () => {
  assert.equal(headline(v({ source: "baseline", actor_kind: "system", fields: [] }), t), "hist.line.baseline");
  assert.equal(headline(v({ source: "agent.created", actor_kind: "system", fields: [] }), t), "hist.line.created");
  assert.equal(headline(v({ source: "restore", restored_from: 3, fields: ["tone"] }), t), "hist.line.restored(who=Carla,n=3)");
});

test("what changed joins two and summarizes more", () => {
  assert.equal(whatChanged([], t), "hist.field.nothing");
  assert.equal(whatChanged(["tone", "skills"], t), "hist.join.two(a=hist.field.tone,b=hist.field.skills)");
  assert.equal(whatChanged(["tone", "skills", "budget_usd", "paused"], t), "hist.join.more(a=hist.field.tone,b=hist.field.skills,n=2)");
  assert.equal(fieldLabel("secret:api_key", t), "hist.field.secret");
  assert.equal(fieldLabel("something_new", t), "something_new");
});

test("values in words, secrets never shown", () => {
  assert.equal(fieldValue("autonomy", "approve_each", t), "hist.autonomy.approve_each");
  assert.equal(fieldValue("tone", "", t), "hist.tone.default");
  assert.equal(fieldValue("budget_usd", 0, t), "hist.value.no_cap");
  assert.match(fieldValue("budget_usd", 40, t), /40/);
  assert.equal(fieldValue("paused", true, t), "hist.value.on");
  assert.equal(fieldValue("instructions", "", t), "hist.value.empty");
  assert.equal(fieldValue("skills", [{ id: "s1", name: "Cotizar" }], t), "Cotizar");
  assert.equal(fieldValue("permissions", { exceptions: [{ category: "messages", mode: "never" }], floor_lifted: ["payments"] }, t),
    "hist.value.exception(cat=perm.cat.messages,mode=perm.mode.never) · hist.value.floor_lifted(cat=perm.cat.payments)");
  assert.equal(fieldValue("permissions", null, t), "hist.value.perms_none");
  assert.equal(fieldValue("secret:api_key", "sk-123", t), "hist.value.hidden");
  assert.ok(fieldValue("instructions", "x".repeat(1000), t).length <= 400);
});

test("before/after rows follow the display order", () => {
  const rows = changeRows([{ field: "budget_usd", from: 0, to: 30 }, { field: "instructions", from: "A", to: "B" }, { field: "secret:k", secret: true }], t);
  assert.deepEqual(rows.map((r) => r.field), ["instructions", "budget_usd", "secret:k"]);
  assert.equal(rows[0].before, "A");
  assert.equal(rows[2].secret, true);
  assert.equal(rows[2].after, "hist.value.hidden");
});

test("what will not come back is explained, the safety reasons first and once each", () => {
  const lines = skipLines([
    { field: "paused", reason: "control" },
    { field: "autonomy", reason: "autonomy_earned" },
    { field: "permissions", category: "messages", reason: "permissions_freer" },
    { field: "permissions", category: "messages", reason: "permissions_freer" },
    { field: "skills", skill: { id: "s2", name: "Cotizar" }, reason: "skill_unavailable" },
    { field: "budget_usd", reason: "weird_new_reason" },
  ], t);
  assert.deepEqual(lines.map((l) => l.reason), ["autonomy_earned", "permissions_freer", "skill_unavailable", "control", "failed"]);
  assert.equal(lines[0].text, "hist.skip.line(what=hist.field.autonomy,why=hist.reason.autonomy_earned)");
  assert.equal(lines[1].text, "hist.skip.line(what=hist.skip.category(cat=perm.cat.messages),why=hist.reason.permissions_freer)");
  assert.equal(lines[2].text, "hist.skip.line(what=hist.skip.skill_named(name=Cotizar),why=hist.reason.skill_unavailable)");
});

test("the fields a restore changes, and which versions offer it", () => {
  assert.deepEqual(applyFields([{ field: "instructions" }, { field: "tone" }, { field: "instructions" }]), ["instructions", "tone"]);
  assert.equal(canRestoreVersion(v({ version: 4 }), 4, true), false);
  assert.equal(canRestoreVersion(v({ version: 3 }), 4, true), true);
  assert.equal(canRestoreVersion(v({ version: 3 }), 4, false), false);
});
