import type { Agent, Task } from "../types";
import { computeRoi, DEFAULT_ROI_MINUTES, MAX_ROI_MINUTES, MAX_ROI_RATE, type RoiSettings, type RoiSettingsView, type RoiTaskLike, type RoiUsageLike } from "../roiLogic";

/** Demo history (B15): completed / reworked / failed tasks and AI spend per seed agent over the last 25 days, so the
 * panel has something to estimate before the first request. Fixed demo numbers; the UI labels them as demo data. */
const HISTORY: Record<string, { done: number; rework: number; failed: number; cost: number }> = {
  accounting: { done: 14, rework: 2, failed: 0, cost: 0.86 }, legal: { done: 6, rework: 1, failed: 1, cost: 0.41 },
  sales: { done: 9, rework: 0, failed: 0, cost: 0.52 }, analyst: { done: 11, rework: 1, failed: 0, cost: 0.74 },
  operations: { done: 5, rework: 0, failed: 1, cost: 0.29 }, hr: { done: 4, rework: 0, failed: 0, cost: 0.18 },
  assistant: { done: 12, rework: 0, failed: 0, cost: 0.33 },
};

function history(now: number): { tasks: RoiTaskLike[]; usage: RoiUsageLike[] } {
  const tasks: RoiTaskLike[] = [];
  const usage: RoiUsageLike[] = [];
  for (const [agent, h] of Object.entries(HISTORY)) {
    const n = h.done + h.rework + h.failed;
    for (let i = 0; i < n; i++) {
      const at = new Date(now - (1 + ((i * 25) / n)) * 864e5).toISOString();
      const kind = i < h.rework ? "rework" : i < h.rework + h.failed ? "failed" : "done";
      tasks.push({ id: `demo-${agent}-${i}`, agent_id: agent, status: kind === "failed" ? "failed" : "done", finished_at: at,
        output: kind === "rework" ? { review: { verdict: "pass", reworks: 1 } } : {} });
      usage.push({ agent_id: agent, cost_usd: h.cost / n, ts: at });
    }
  }
  return { tasks, usage };
}

/** GET /roi, GET|PUT /settings/roi for the demo backend. Same rules as backend/internal/application/roi.go. */
export class MockRoi {
  private settings: RoiSettings | null = null;
  constructor(private deps: { agents: () => Agent[]; tasks: () => Task[] }) {}

  matches(seg: string[]) { return (seg[0] === "roi" && !seg[1]) || (seg[0] === "settings" && seg[1] === "roi"); }
  reset() { this.settings = null; }

  private view(): RoiSettingsView {
    const s = this.settings ?? { hourly_rate_usd: null, default_minutes: DEFAULT_ROI_MINUTES, role_minutes: {} };
    return { ...s, role_minutes: { ...s.role_minutes }, saved: !!this.settings, default_assumed_minutes: DEFAULT_ROI_MINUTES, max_minutes: MAX_ROI_MINUTES, max_hourly_rate_usd: MAX_ROI_RATE };
  }

  handle(method: string, seg: string[], q: URLSearchParams, body: any) {
    if (seg[0] === "roi") {
      const days = Math.min(365, Math.max(1, Number(q.get("window_days")) || 30));
      const now = Date.now();
      const h = history(now);
      const agents = this.deps.agents();
      const live = this.deps.tasks().map((t) => ({ id: t.id, agent_id: t.agent_id, status: t.status, finished_at: t.finished_at, output: t.output as RoiTaskLike["output"] }));
      // The demo engine keeps the spend per agent, not per call: it is booked at "now".
      const liveUsage = agents.filter((a) => a.metrics.cost_usd > 0).map((a) => ({ agent_id: a.id, cost_usd: a.metrics.cost_usd, ts: new Date(now - 1000).toISOString() }));
      const s = this.view();
      return computeRoi(s, s.saved, agents.map((a) => ({ id: a.id, role: a.role })), [...h.tasks, ...live], [...h.usage, ...liveUsage], new Date(now - days * 864e5), new Date(now), days);
    }
    if (method === "GET") return this.view();
    if (method === "PUT") {
      const d = Number(body?.default_minutes);
      if (!Number.isInteger(d) || d < 0 || d > MAX_ROI_MINUTES) throw new Error("PUT /settings/roi -> 400");
      let rate: number | null = body?.hourly_rate_usd == null ? null : Number(body.hourly_rate_usd);
      if (rate != null && (!Number.isFinite(rate) || rate > MAX_ROI_RATE)) throw new Error("PUT /settings/roi -> 400");
      if (rate != null && rate <= 0) rate = null;
      const roles: Record<string, number> = {};
      for (const [k, v] of Object.entries((body?.role_minutes ?? {}) as Record<string, unknown>)) {
        const m = Number(v);
        if (!k.trim() || !Number.isInteger(m) || m < 0 || m > MAX_ROI_MINUTES) throw new Error("PUT /settings/roi -> 400");
        roles[k.trim().toLowerCase()] = m;
      }
      this.settings = { hourly_rate_usd: rate == null ? null : Math.round(rate * 100) / 100, default_minutes: d, role_minutes: roles, updated_by: "demo", updated_at: new Date().toISOString() };
      return this.view();
    }
    throw new Error(`mock: ${method} /${seg.join("/")} not implemented`);
  }
}
