/**
 * Mock of company goals (F1, NEXT_PUBLIC_MOCK=true). Same shapes and rules as backend/internal/api/goals.go: at most
 * 10 active goals (409), text limits (400), PATCH with "" removing the deadline. Mutations live in memory until reload.
 */
import type { Request } from "../types";
import type { Goal, GoalDetail, GoalRef } from "../goalsApi";
import { normalizeGoalStatus, validateGoal } from "../goalsLogic";

export const MAX_ACTIVE_GOALS = 10;
const iso = (daysAgo: number) => new Date(Date.now() - daysAgo * 86_400_000).toISOString();
const day = (daysAhead: number) => new Date(Date.now() + daysAhead * 86_400_000).toISOString().slice(0, 10);

const SEED: Goal[] = [
  { id: "goal_ventas", title: "Subir ventas del T4 un 15 %", description: "Más pedidos de clientes actuales y de los nuevos que llegan por recomendación.", status: "active", period: "T4 2026", due_date: day(40), created_by: "demo", created_at: iso(20), updated_at: iso(2) },
  { id: "goal_cierre", office_id: "contabilidad", office_name: "Contabilidad", title: "Cerrar el mes en 3 días hábiles", description: "", status: "active", period: "Mensual", created_by: "demo", created_at: iso(15), updated_at: iso(5) },
  { id: "goal_web", title: "Renovar la página web", description: "Nueva página con catálogo y formulario de contacto.", status: "achieved", period: "T3 2026", due_date: day(-30), created_by: "demo", created_at: iso(90), updated_at: iso(30) },
];

const bad = () => new Error("POST /goals -> 400");

export class MockGoals {
  private goals: Goal[] = SEED.map((g) => ({ ...g }));
  private links = new Map<string, string>(); // request id -> goal id
  private seq = 0;
  constructor(private requests: () => Request[], private officeName: (id: string) => string) {}

  matches(seg: string[]): boolean { return seg[0] === "goals"; }

  /** A goal a request or message can be linked to (active); anything else is a 400 like the server. */
  assertLinkable(goalId: string) {
    const g = this.goals.find((x) => x.id === goalId);
    if (!g || g.status !== "active") throw new Error("POST -> 400");
  }
  linkRequest(requestId: string, goalId: string) { this.links.set(requestId, goalId); }
  refOf(requestId: string): GoalRef | undefined {
    const g = this.goals.find((x) => x.id === this.links.get(requestId));
    return g ? { id: g.id, title: g.title, status: g.status, ...(g.office_name ? { office_name: g.office_name } : {}) } : undefined;
  }

  private detail(gid: string): GoalDetail {
    const goal = this.goals.find((g) => g.id === gid);
    if (!goal) throw new Error(`GET /goals/${gid} -> 404`);
    const requests = this.requests().filter((r) => this.links.get(r.id) === gid)
      .map((r) => ({ id: r.id, text: r.text, status: r.status, report_id: r.report_id, created_at: r.created_at }));
    return { goal, requests, projects: [] };
  }

  handle(method: string, seg: string[], q: URLSearchParams, body: any): unknown {
    const gid = seg[1];
    if (method === "GET" && !gid) {
      const all = q.get("include_archived") === "1";
      return { goals: this.goals.filter((g) => all || g.status !== "archived"), max_active: MAX_ACTIVE_GOALS };
    }
    if (method === "GET" && gid) return this.detail(gid);
    if (method === "POST" && !gid) {
      const title = String(body?.title ?? "").trim(), description = String(body?.description ?? "").trim(), period = String(body?.period ?? "").trim();
      const due = String(body?.due_date ?? "");
      if (validateGoal({ title, description, period, due_date: due })) throw bad();
      if (this.goals.filter((g) => g.status === "active").length >= MAX_ACTIVE_GOALS) throw new Error("POST /goals -> 409");
      const office = String(body?.office_id ?? "");
      const now = new Date().toISOString();
      const g: Goal = {
        id: `goal_${Date.now().toString(36)}${++this.seq}`, title, description, status: "active",
        ...(office ? { office_id: office, office_name: this.officeName(office) } : {}),
        ...(period ? { period } : {}), ...(due ? { due_date: due } : {}), created_by: "demo", created_at: now, updated_at: now,
      };
      this.goals.push(g);
      return g;
    }
    if (method === "PATCH" && gid) {
      const g = this.goals.find((x) => x.id === gid);
      if (!g) throw new Error(`PATCH /goals/${gid} -> 404`);
      const next: Goal = { ...g };
      if (body?.title !== undefined) next.title = String(body.title).trim();
      if (body?.description !== undefined) next.description = String(body.description).trim();
      if (body?.period !== undefined) { const p = String(body.period).trim(); if (p) next.period = p; else delete next.period; }
      if (body?.due_date !== undefined) { const d = String(body.due_date); if (d) next.due_date = d; else delete next.due_date; }
      if (body?.status !== undefined) {
        const s = normalizeGoalStatus(body.status);
        if (s === "active" && g.status !== "active" && this.goals.filter((x) => x.status === "active").length >= MAX_ACTIVE_GOALS) throw new Error(`PATCH /goals/${gid} -> 409`);
        next.status = s;
      }
      if (validateGoal({ title: next.title, description: next.description, period: next.period, due_date: next.due_date })) throw new Error(`PATCH /goals/${gid} -> 400`);
      next.updated_at = new Date().toISOString();
      Object.assign(g, next);
      if (!next.period) delete g.period;
      if (!next.due_date) delete g.due_date;
      return g;
    }
    throw new Error(`mock: unsupported ${method} /${seg.join("/")}`);
  }
}
