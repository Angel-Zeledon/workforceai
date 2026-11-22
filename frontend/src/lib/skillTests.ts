/**
 * Client of "Probar una habilidad con un caso" (docs/architecture/skills.md, API in 08-api.md sec. 53).
 * A test runs ONE agent with ONE version of the skill loaded and has no side effects (the backend never gives it tools).
 * In mock mode (NEXT_PUBLIC_MOCK=true) a small in-memory version answers with a simulated result.
 */
import { API_URL, MOCK } from "./config";
import { authFetch } from "./session";
import { SkillError } from "./skills";
import type {
  SkillTestAgent, SkillTestCase, SkillTestEstimate, SkillTestOverview, SkillTestRun,
} from "./skillTestsLogic";

export type { SkillTestAgent, SkillTestCase, SkillTestEstimate, SkillTestOverview, SkillTestRun } from "./skillTestsLogic";
export interface RunRequest { case_id: string; agent_id: string; version: number; compare: boolean }
export interface CaseDraft { title: string; prompt: string; expect: string }

async function req<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await authFetch(`${API_URL}${path}`, {
    method, cache: "no-store",
    body: body === undefined ? undefined : JSON.stringify(body),
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
  });
  const text = await res.text();
  const data = text ? (() => { try { return JSON.parse(text); } catch { return text; } })() : {};
  if (!res.ok) throw new SkillError(res.status, data?.code ?? String(res.status), data?.error ?? res.statusText);
  return data as T;
}

const real = {
  overview: (id: string) => req<SkillTestOverview>("GET", `/skills/${id}/tests`),
  candidates: (id: string) => req<SkillTestAgent[]>("GET", `/skills/${id}/tests/candidates`),
  caseRuns: (id: string, caseId: string) => req<SkillTestRun[]>("GET", `/skills/${id}/tests/cases/${caseId}/runs`),
  saveCase: (id: string, c: CaseDraft, caseId = "") => caseId
    ? req<SkillTestCase>("PUT", `/skills/${id}/tests/cases/${caseId}`, c)
    : req<SkillTestCase>("POST", `/skills/${id}/tests/cases`, c),
  deleteCase: (id: string, caseId: string) => req<unknown>("DELETE", `/skills/${id}/tests/cases/${caseId}`),
  estimate: (id: string, r: RunRequest) => req<SkillTestEstimate>("POST", `/skills/${id}/tests/estimate`, r),
  run: async (id: string, r: RunRequest) => (await req<{ runs: SkillTestRun[] }>("POST", `/skills/${id}/tests/run`, r)).runs,
};

function mockTests(): typeof real {
  const cases = new Map<string, SkillTestCase[]>();
  const runs = new Map<string, SkillTestRun[]>();
  let seq = 0;
  const now = () => new Date().toISOString();
  const limits = { cases: 10, title: 120, prompt: 2000, expect: 1000 };
  const over = (id: string): SkillTestOverview => {
    const all = runs.get(id) ?? [];
    const seen = new Set<number>();
    const last = all.filter((r) => r.status === "done" && !seen.has(r.version) && seen.add(r.version))
      .map((r) => ({ version: r.version, at: r.created_at, status: r.status, used_skill: r.used_skill, case_id: r.case_id, run_id: r.id }));
    return { cases: cases.get(id) ?? [], runs: all.slice(0, 30), last_by_version: last, limits };
  };
  return {
    overview: async (id) => over(id),
    candidates: async () => [{ id: "sales", name: "Valeria", role: "sales", title: "Ventas" }],
    caseRuns: async (id, caseId) => (runs.get(id) ?? []).filter((r) => r.case_id === caseId),
    saveCase: async (id, c, caseId = "") => {
      const list = cases.get(id) ?? [];
      if (!caseId && list.length >= limits.cases) throw new SkillError(409, "too_many_cases", "too many");
      const t = now();
      const found = list.find((x) => x.id === caseId);
      const next: SkillTestCase = { id: found?.id ?? `stc_mock_${++seq}`, skill_id: id, title: c.title || c.prompt.slice(0, 60), prompt: c.prompt, expect: c.expect,
        created_by: "demo", created_at: found?.created_at ?? t, updated_at: t };
      cases.set(id, found ? list.map((x) => (x.id === caseId ? next : x)) : [...list, next]);
      return next;
    },
    deleteCase: async (id, caseId) => { cases.set(id, (cases.get(id) ?? []).filter((c) => c.id !== caseId)); return {}; },
    estimate: async (_id, r) => ({ runs: r.compare ? 2 : 1, versions: [r.version], estimate_usd: r.compare ? 0.0042 : 0.0021, per_run_usd: r.compare ? [0.0021, 0.0021] : [0.0021] }),
    run: async (id, r) => {
      const c = (cases.get(id) ?? []).find((x) => x.id === r.case_id);
      if (!c) throw new SkillError(404, "not_found", "case not found");
      const pair = r.compare ? `stp_mock_${++seq}` : "";
      const one = (version: number, active: boolean): SkillTestRun => ({
        id: `str_mock_${++seq}`, skill_id: id, case_id: c.id, case_title: c.title, pair_id: pair || undefined, version, active, agent_id: r.agent_id, status: "done",
        output: `Respuesta simulada de la versión ${version} para: ${c.prompt}`, used_skill: true, blocked: [], estimate_usd: 0.0021, cost_usd: 0.0018,
        input_tokens: 900, output_tokens: 160, duration_ms: 1800, model: "simulación", created_by: "demo", created_at: now(),
      });
      const made = r.compare ? [one(r.version, false), one(Math.max(1, r.version - 1), true)] : [one(r.version, false)];
      runs.set(id, [...made.slice().reverse(), ...(runs.get(id) ?? [])]);
      return made;
    },
  };
}

export const skillTestsApi: typeof real = MOCK ? mockTests() : real;
