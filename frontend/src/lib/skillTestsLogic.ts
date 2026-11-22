// Pure logic of "Probar con un caso" (docs/architecture/skills.md): what the review screen says about the tests of a skill,
// how results are grouped side by side and how the cost is shown. No React, no network.

export interface SkillTestCase { id: string; skill_id: string; title: string; prompt: string; expect: string; created_by: string; created_at: string; updated_at: string }
export interface SkillTestRun {
  id: string; skill_id: string; case_id: string; case_title: string; pair_id?: string; version: number; active: boolean; agent_id: string;
  status: "done" | "failed"; fail_code?: string; output: string; used_skill: boolean; blocked: string[];
  estimate_usd: number; cost_usd: number; input_tokens: number; output_tokens: number; duration_ms: number; model?: string; created_by: string; created_at: string;
}
export interface SkillTested { version: number; at: string; status: string; used_skill: boolean; case_id: string; run_id: string }
export interface SkillTestOverview {
  cases: SkillTestCase[]; runs: SkillTestRun[]; last_by_version: SkillTested[];
  limits: { cases: number; title: number; prompt: number; expect: number };
}
export interface SkillTestEstimate { runs: number; versions: number[]; estimate_usd: number; per_run_usd: number[] }
export interface SkillTestAgent { id: string; name: string; role: string; title: string }

/** "Sin probar" or the date the version was last tested ("Probada el ..."). */
export type TestedLabel = { kind: "never" } | { kind: "at"; at: string; usedSkill: boolean };
export function testedLabel(o: Pick<SkillTestOverview, "last_by_version"> | null | undefined, version: number): TestedLabel {
  const t = o?.last_by_version?.find((x) => x.version === version);
  return t ? { kind: "at", at: t.at, usedSkill: t.used_skill } : { kind: "never" };
}

/** The runs of a result set grouped side by side: the two runs of a comparison share a pair_id, the rest stand alone.
 * Inside a pair the version under review comes first (the one that is not the active one). Newest group first. */
export function groupRuns(runs: SkillTestRun[]): SkillTestRun[][] {
  const out: SkillTestRun[][] = [];
  const byPair = new Map<string, SkillTestRun[]>();
  for (const r of runs) {
    if (!r.pair_id) { out.push([r]); continue; }
    let g = byPair.get(r.pair_id);
    if (!g) { g = []; byPair.set(r.pair_id, g); out.push(g); }
    g.push(r);
  }
  for (const g of out) g.sort((a, b) => Number(a.active) - Number(b.active));
  return out;
}

/** Whether a comparison with the active version makes sense: there is one and it is not the version being tested. */
export function canCompare(activeVersion: number, testedVersion: number): boolean {
  return activeVersion > 0 && activeVersion !== testedVersion;
}

export function fmtUSD(n: number): string {
  if (!(n > 0)) return "$0.00";
  return n < 0.01 ? `$${n.toFixed(4)}` : `$${n.toFixed(2)}`;
}

export function fmtSeconds(ms: number): string {
  return ms < 1000 ? `${Math.max(0, Math.round(ms))} ms` : `${(ms / 1000).toFixed(1)} s`;
}

/** Whether the case form can be saved and the first thing wrong with it (the backend checks again). */
export function caseIssue(c: { title: string; prompt: string; expect: string }, lim: SkillTestOverview["limits"]): "prompt_required" | "prompt_too_long" | "expect_too_long" | "title_too_long" | null {
  if (!c.prompt.trim()) return "prompt_required";
  if ([...c.prompt].length > lim.prompt) return "prompt_too_long";
  if ([...c.expect].length > lim.expect) return "expect_too_long";
  if ([...c.title].length > lim.title) return "title_too_long";
  return null;
}

/** A stable code of a failed run to a translation key suffix: "budget_exceeded:org" -> "budget_exceeded". */
export const failKey = (code?: string): string => (code ?? "").split(":")[0] || "unknown";
