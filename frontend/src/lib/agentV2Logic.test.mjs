// Unit tests of the V2 agent profile / thought audit / intervention logic (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import {
  DEFAULT_LIMITS, addSkill, canIntervene, canSendApplyNow, diffRows, errorStatus, fmtEstimate, isDirty, looksLikeSecret, nextBefore,
  removeSkill, segmentDefaultOpen, steerNoteError, validateProfile,
} from "./agentV2Logic.ts";

const base = { instructions: "Be concise", creativity: "low", skills: ["sql"], tone: "" };

test("profile validation: limits and secrets", () => {
  assert.deepEqual(validateProfile(base), []);
  assert.equal(validateProfile({ ...base, instructions: "x".repeat(DEFAULT_LIMITS.instructions_max + 1) })[0].code, "instructions_too_long");
  assert.equal(validateProfile({ ...base, skills: ["y".repeat(DEFAULT_LIMITS.skill_max_len + 1)] })[0].code, "skill_too_long");
  assert.equal(validateProfile({ ...base, skills: Array(DEFAULT_LIMITS.skills_max + 1).fill("a") })[0].code, "too_many_skills");
  assert.ok(looksLikeSecret("use sk-abcdefghijklmnopqrstuvwx for it"));
  assert.ok(looksLikeSecret("password: hunter2hunter2"));
  assert.ok(!looksLikeSecret("Always greet customers politely"));
  assert.equal(validateProfile({ ...base, instructions: "api_key=abcdef123456" })[0].code, "looks_like_secret");
});

test("skills: trim, dedupe case-insensitively, cap, remove", () => {
  assert.deepEqual(addSkill(["sql"], "  Python  "), ["sql", "Python"]);
  assert.deepEqual(addSkill(["sql"], "SQL"), ["sql"]);
  assert.deepEqual(addSkill(["sql"], "   "), ["sql"]);
  assert.deepEqual(addSkill(Array(20).fill("a").map((x, i) => x + i), "extra"), Array(20).fill("a").map((x, i) => x + i));
  assert.deepEqual(removeSkill(["a", "b"], "a"), ["b"]);
});

test("dirty detection", () => {
  assert.equal(isDirty(base, { ...base }), false);
  assert.equal(isDirty(base, { ...base, skills: ["sql", "x"] }), true);
  assert.equal(isDirty(base, { ...base, tone: "mx" }), true);
});

test("diff rows for a version", () => {
  const rows = diffRows({ skills: { from: ["a", "b"], to: ["b", "c"] }, creativity: { from: "low", to: "high" }, instructions: { from: "", to: "z".repeat(100) } });
  assert.deepEqual(rows.map((r) => r.field), ["instructions", "creativity", "skills"]);
  assert.ok(rows[0].to.endsWith("..."));
  assert.deepEqual([rows[2].added, rows[2].removed], [["c"], ["a"]]);
  assert.deepEqual(diffRows(null), []);
});

test("error status parsing", () => {
  assert.equal(errorStatus(new Error("PUT /agents/a/profile -> 403")), 403);
  assert.equal(errorStatus(new Error("boom")), 0);
});

test("untrusted prompt segments start collapsed", () => {
  assert.equal(segmentDefaultOpen({ kind: "untrusted" }), false);
  assert.equal(segmentDefaultOpen({ kind: "text" }), true);
});

test("trace pagination cursor", () => {
  const page = [{ created_at: "2026-01-02T00:00:00Z" }, { created_at: "2026-01-01T00:00:00Z" }];
  assert.equal(nextBefore(page, 2), "2026-01-01T00:00:00Z");
  assert.equal(nextBefore(page, 50), "");
});

test("intervention: statuses, note validation and apply-now confirmation", () => {
  for (const s of ["pending", "running", "blocked", "awaiting_approval"]) assert.ok(canIntervene(s));
  for (const s of ["done", "failed", "cancelled"]) assert.ok(!canIntervene(s));
  assert.equal(steerNoteError("  "), "empty");
  assert.equal(steerNoteError("x".repeat(2001)), "too_long");
  assert.equal(steerNoteError("use the Q3 figures"), "");
  const mid = { can_steer: true, can_apply_now: true };
  assert.equal(canSendApplyNow(mid, "note", false), false); // needs explicit confirmation
  assert.equal(canSendApplyNow(mid, "note", true), true);
  assert.equal(canSendApplyNow({ can_steer: true, can_apply_now: false }, "note", true), false);
  assert.equal(canSendApplyNow(mid, " ", true), false);
  assert.equal(fmtEstimate(0.0456), "$0.046");
  assert.equal(fmtEstimate(1.234), "$1.23");
});
