// Unit tests of the team sign-up logic (run: node --test src/lib/teamsLogic.test.mjs).
import assert from "node:assert/strict";
import { test } from "node:test";
import { cleanInvites, parseEmails, PROFESSIONS, registerBody, signupMissing, suggestedTeams, teamRowsIssues, welcomePlan } from "./teamsLogic.ts";

const base = { email: "ana@x.com", password: "Correct-Horse-9", name: "Ana", org: "", team: "", audience: "", profession: "", join: false };

test("the professions are the nine of the design, in the backend order", () => {
  assert.deepEqual([...PROFESSIONS], ["legal", "sales", "accounting", "marketing", "operations", "hr", "direction", "education", "other"]);
});

test("signupMissing asks for who it is for and the profession, and the company name only for teams", () => {
  assert.deepEqual(signupMissing(base), ["profession", "audience"]);
  assert.deepEqual(signupMissing({ ...base, audience: "solo", profession: "legal" }), []);
  assert.deepEqual(signupMissing({ ...base, audience: "team", profession: "legal" }), ["org"]);
  assert.deepEqual(signupMissing({ ...base, audience: "company", profession: "hr", org: "Acme" }), []);
  // Joining the company of the domain: no audience nor organization asked.
  assert.deepEqual(signupMissing({ ...base, join: true, profession: "sales" }), []);
});

test("registerBody: personal office without a name, team with its team, join without an organization", () => {
  assert.deepEqual(registerBody({ ...base, audience: "solo", profession: "legal", org: "ignored" }),
    { email: "ana@x.com", password: "Correct-Horse-9", name: "Ana", org_name: "", audience: "solo", profession: "legal" });
  const team = registerBody({ ...base, audience: "team", profession: "sales", org: " Acme ", team: " Ventas norte ", locale: "es" });
  assert.equal(team.org_name, "Acme");
  assert.equal(team.team_name, "Ventas norte");
  assert.equal(team.locale, "es");
  const company = registerBody({ ...base, audience: "company", profession: "", org: "Acme", team: "x" });
  assert.equal(company.profession, "other");
  assert.equal(company.team_name, undefined);
  const join = registerBody({ ...base, join: true, profession: "hr", audience: "company", org: "Acme" });
  assert.equal(join.join, true);
  assert.equal(join.org_name, "");
  assert.equal(join.audience, "solo");
});

const w = (o) => ({ kind: "member", audience: "company", org_name: "Acme", setup_pending: false, profession: "", team: null, invited_by: "", agents: [], teammates: [], can_pick_team: false, teams: [], ...o });

test("welcomePlan picks the welcome of each case", () => {
  assert.equal(welcomePlan(null), null);
  assert.equal(welcomePlan(w({ kind: "classic" })), null);
  assert.deepEqual(welcomePlan(w({ kind: "solo" })), { variant: "solo", primary: "start", tour: true });
  assert.deepEqual(welcomePlan(w({ kind: "owner", setup_pending: true })), { variant: "owner", primary: "setup", tour: false });
  assert.equal(welcomePlan(w({ kind: "owner", audience: "team", setup_pending: true })).variant, "ownerTeam");
  assert.equal(welcomePlan(w({ kind: "owner", setup_pending: false })).primary, "start");
  assert.equal(welcomePlan(w({ kind: "lead" })).variant, "lead");
  assert.deepEqual(welcomePlan(w({ kind: "member", can_pick_team: true })), { variant: "memberNoTeam", primary: "pickTeam", tour: false });
  assert.equal(welcomePlan(w({ kind: "member" })).variant, "member");
});

test("teamRowsIssues flags empty, duplicated and badly invited teams, ignoring the ones left out", () => {
  const rows = [
    { key: "a", name: "Legal", profession: "legal", head: "me", email: "", on: true },
    { key: "b", name: "legal ", profession: "legal", head: "later", email: "", on: true },
    { key: "c", name: "", profession: "hr", head: "later", email: "", on: true },
    { key: "d", name: "Ventas", profession: "sales", head: "invite", email: "nope", on: true },
    { key: "e", name: "", profession: "hr", head: "invite", email: "", on: false },
  ];
  assert.deepEqual(teamRowsIssues(rows), { b: "e1.setup.err.dup", c: "e1.setup.err.name", d: "e1.setup.err.email" });
});

test("suggestedTeams offers every profession but 'other', off by default", () => {
  const rows = suggestedTeams((p) => `T-${p}`);
  assert.equal(rows.length, 8);
  assert.ok(rows.every((r) => !r.on && r.head === "later" && r.name === `T-${r.profession}`));
});

test("cleanInvites keeps valid distinct addresses; parseEmails splits a pasted list", () => {
  const rows = parseEmails("A@x.com, b@x.com;bad  a@x.com", "legal");
  assert.equal(rows.length, 4);
  assert.deepEqual(cleanInvites(rows), [{ email: "a@x.com", profession: "legal" }, { email: "b@x.com", profession: "legal" }]);
});
