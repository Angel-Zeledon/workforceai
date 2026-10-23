/** AI payroll and return (B15, docs/architecture/ai-payroll-roi.md). Contract of GET /roi and /settings/roi plus pure
 * helpers. The hours are an ESTIMATE from the org's own model; the AI cost is measured (usage ledger). There is no
 * default hourly rate: without the org's rate the comparison is shown in hours only. */

export interface RoiSettings {
  hourly_rate_usd: number | null; default_minutes: number; role_minutes: Record<string, number>;
  updated_by?: string; updated_at?: string;
}
export interface RoiSettingsView extends RoiSettings { saved: boolean; default_assumed_minutes: number; max_minutes: number; max_hourly_rate_usd: number }
export interface RoiMethodology {
  kind: "estimate"; window_days: number; from: string; to: string; counted: string; excluded: string[];
  default_minutes: number; default_minutes_source: "assumption" | "org"; role_minutes: Record<string, number>;
  hourly_rate_usd: number | null; rate_source: "org" | "unset"; ai_cost_basis: string;
}
export interface RoiFigures {
  tasks_counted: number; excluded_rework: number; excluded_failed: number; excluded_skipped: number;
  est_minutes: number; est_hours: number; ai_cost_usd: number; est_human_cost_usd: number | null; ai_cost_per_est_hour_usd: number | null;
}
export interface RoiAgent extends RoiFigures { agent_id: string; role: string; minutes_per_task: number; minutes_source: "role" | "default" | "assumption" }
export interface RoiReport { generated_at: string; methodology: RoiMethodology; org: RoiFigures; agents: RoiAgent[] }

export const DEFAULT_ROI_MINUTES = 30;
export const MAX_ROI_MINUTES = 480;
export const MAX_ROI_RATE = 10000;

export interface RoiTaskLike { id: string; agent_id: string; status: string; finished_at: string | null; output?: { metrics?: Record<string, unknown>; review?: { verdict?: string; reworks?: number } | null } | null }
export interface RoiUsageLike { agent_id: string; cost_usd: number; ts: string }

/** "" counts; otherwise why the task is excluded. Mirrors application.roiExclusion. */
export function roiExclusion(t: RoiTaskLike): "" | "failed" | "skipped" | "rework" {
  if (t.status === "failed") return "failed";
  const o = t.output;
  if (!o) return "";
  if (o.metrics?.skipped === true) return "skipped";
  if (o.review && ((o.review.reworks ?? 0) > 0 || (o.review.verdict && o.review.verdict !== "pass"))) return "rework";
  if (Number(o.metrics?.attempts ?? 0) > 1) return "rework";
  return "";
}

const r2 = (v: number) => Math.round(v * 100) / 100;
const r6 = (v: number) => Math.round(v * 1e6) / 1e6;

function finish<T extends RoiFigures>(f: T, rate: number | null): T {
  f.est_hours = r2(f.est_minutes / 60);
  f.ai_cost_usd = r6(f.ai_cost_usd);
  f.est_human_cost_usd = rate != null && f.est_minutes > 0 ? r2((f.est_minutes / 60) * rate) : null;
  f.ai_cost_per_est_hour_usd = f.est_minutes > 0 ? r6(f.ai_cost_usd / (f.est_minutes / 60)) : null;
  return f;
}
const zero = (): RoiFigures => ({ tasks_counted: 0, excluded_rework: 0, excluded_failed: 0, excluded_skipped: 0, est_minutes: 0, est_hours: 0, ai_cost_usd: 0, est_human_cost_usd: null, ai_cost_per_est_hour_usd: null });

/** The model behind GET /roi (same rules as application.ComputeROI; used by the demo backend and the tests). */
export function computeRoi(st: RoiSettings, saved: boolean, agents: { id: string; role: string }[], tasks: RoiTaskLike[], usage: RoiUsageLike[], from: Date, to: Date, windowDays: number): RoiReport {
  const defMin = saved ? st.default_minutes : DEFAULT_ROI_MINUTES;
  const defSrc: "org" | "assumption" = saved ? "org" : "assumption";
  const roles = new Map(agents.map((a) => [a.id, a.role.toLowerCase()]));
  const rows = new Map<string, RoiAgent>();
  const row = (id: string) => {
    let r = rows.get(id);
    if (r) return r;
    const role = roles.get(id) ?? "";
    const own = role && role in st.role_minutes;
    r = { ...zero(), agent_id: id, role, minutes_per_task: own ? st.role_minutes[role] : defMin, minutes_source: own ? "role" : defSrc === "org" ? "default" : "assumption" };
    rows.set(id, r);
    return r;
  };
  const inWin = (iso: string) => { const t = Date.parse(iso); return t >= +from && t < +to; };
  agents.forEach((a) => row(a.id));
  for (const t of tasks) {
    if (!t.finished_at || !inWin(t.finished_at) || (t.status !== "done" && t.status !== "failed") || !t.agent_id) continue;
    const r = row(t.agent_id);
    const ex = roiExclusion(t);
    if (ex === "failed") r.excluded_failed++; else if (ex === "skipped") r.excluded_skipped++; else if (ex === "rework") r.excluded_rework++; else r.tasks_counted++;
  }
  for (const u of usage) if (u.agent_id && inWin(u.ts)) row(u.agent_id).ai_cost_usd += u.cost_usd;
  const org = zero();
  const list: RoiAgent[] = [];
  for (const r of rows.values()) {
    r.est_minutes = r.tasks_counted * r.minutes_per_task;
    finish(r, st.hourly_rate_usd);
    if (r.tasks_counted + r.excluded_failed + r.excluded_rework + r.excluded_skipped === 0 && r.ai_cost_usd === 0) continue;
    org.tasks_counted += r.tasks_counted; org.excluded_rework += r.excluded_rework; org.excluded_failed += r.excluded_failed; org.excluded_skipped += r.excluded_skipped;
    org.est_minutes += r.est_minutes; org.ai_cost_usd += r.ai_cost_usd;
    list.push(r);
  }
  finish(org, st.hourly_rate_usd);
  list.sort((a, b) => b.est_minutes - a.est_minutes || a.agent_id.localeCompare(b.agent_id));
  return {
    generated_at: to.toISOString(), org, agents: list,
    methodology: { kind: "estimate", window_days: windowDays, from: from.toISOString(), to: to.toISOString(), counted: "done_in_window", excluded: ["rework", "failed", "skipped"],
      default_minutes: defMin, default_minutes_source: defSrc, role_minutes: { ...st.role_minutes }, hourly_rate_usd: st.hourly_rate_usd,
      rate_source: st.hourly_rate_usd != null ? "org" : "unset", ai_cost_basis: "usage_ledger" },
  };
}

/** Validates the editor's input; returns the body for PUT /settings/roi or an i18n error key. Empty rate = not set. */
export function parseRoiForm(rate: string, defMinutes: string, roles: Record<string, string>): { ok: true; body: RoiSettings } | { ok: false; error: string } {
  const num = (s: string) => Number(s.trim().replace(",", "."));
  let hourly: number | null = null;
  if (rate.trim()) {
    const v = num(rate);
    if (!Number.isFinite(v) || v <= 0 || v > MAX_ROI_RATE) return { ok: false, error: "roi.settings.invalidRate" };
    hourly = Math.round(v * 100) / 100;
  }
  const d = num(defMinutes);
  if (!defMinutes.trim() || !Number.isInteger(d) || d < 0 || d > MAX_ROI_MINUTES) return { ok: false, error: "roi.settings.invalidMinutes" };
  const role_minutes: Record<string, number> = {};
  for (const [role, raw] of Object.entries(roles)) {
    if (!raw.trim()) continue; // blank = use the default
    const m = num(raw);
    if (!Number.isInteger(m) || m < 0 || m > MAX_ROI_MINUTES) return { ok: false, error: "roi.settings.invalidMinutes" };
    role_minutes[role.toLowerCase()] = m;
  }
  return { ok: true, body: { hourly_rate_usd: hourly, default_minutes: d, role_minutes } };
}
