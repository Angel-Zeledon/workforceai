/**
 * A count limit of the organization's plan refused one more item (HTTP 409, code "plan_limit"; backend
 * internal/domain/planlimit.go). authFetch announces every such refusal with a window event, so any screen that
 * creates something (an agent, a project, a schedule, an invitation, a file) gets the same "upgrade" notice without
 * knowing about plans. No imports: session.ts uses it.
 */
export type PlanLimitKind = "agents" | "active_projects" | "schedules" | "seats" | "storage";
export interface PlanLimitHit { kind: PlanLimitKind | ""; limit: number; plan: string }
export const PLAN_LIMIT_EVENT = "plan-limit";

/**
 * Reads a plan-limit refusal from an error body, in both shapes the server uses: the API's
 * {code:"plan_limit", limit_kind, limit, plan} and the auth service's {error:{code:"plan_limit", message}}.
 */
export function parsePlanLimit(body: unknown): PlanLimitHit | null {
  if (!body || typeof body !== "object") return null;
  const b = body as Record<string, any>;
  if (b.code === "plan_limit") return { kind: b.limit_kind ?? "", limit: Number(b.limit ?? 0), plan: String(b.plan ?? "") };
  const e = b.error;
  if (e && typeof e === "object" && e.code === "plan_limit") {
    // "plan_limit: seats (2) on the free plan"
    const m = /plan_limit: (\w+) \((\d+)\) on the (\w+) plan/.exec(String(e.message ?? ""));
    return m ? { kind: m[1] as PlanLimitKind, limit: Number(m[2]), plan: m[3] } : { kind: "", limit: 0, plan: "" };
  }
  return null;
}

/**
 * Looks at a refused response (on a clone: the caller still reads the body) and announces a plan-limit refusal:
 * 409 for counts (agents, projects, schedules, seats), 413 for the plan's storage (Drive).
 */
export function announcePlanLimit(res: Response): void {
  if ((res.status !== 409 && res.status !== 413) || typeof window === "undefined") return;
  res.clone().json().then((body) => {
    const hit = parsePlanLimit(body);
    if (hit) window.dispatchEvent(new CustomEvent<PlanLimitHit>(PLAN_LIMIT_EVENT, { detail: hit }));
  }).catch(() => { /* not JSON: not a plan limit */ });
}
