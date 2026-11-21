import { call } from "./api";

/** Company goals (F1). Contract: backend/internal/api/goals.go (/api/v1). */

export type GoalStatus = "active" | "achieved" | "archived";

export interface Goal {
  id: string;
  /** The department (office) the goal belongs to; absent = the whole company. */
  office_id?: string; office_name?: string;
  title: string; description: string; status: GoalStatus;
  period?: string;
  /** "YYYY-MM-DD" */
  due_date?: string;
  created_by: string; created_at: string; updated_at: string;
}
export interface GoalList { goals: Goal[]; max_active: number }
export interface GoalInput { title: string; description?: string; period?: string; due_date?: string; office_id?: string }
export interface GoalPatch { title?: string; description?: string; period?: string; /** "" removes it */ due_date?: string; status?: GoalStatus }

export interface GoalRequestLink { id: string; text: string; status: string; report_id?: string | null; created_at: string }
export interface GoalProjectLink { id: string; name: string; status: string }
export interface GoalDetail { goal: Goal; requests: GoalRequestLink[]; projects: GoalProjectLink[] }

/** The goal a report or request points to (the "Contribuye a" line). */
export interface GoalRef { id: string; title: string; status: GoalStatus | string; office_name?: string }

const id = (s: string) => encodeURIComponent(s);

export const goalsApi = {
  list: (includeArchived = false) => call<GoalList>("GET", `/goals${includeArchived ? "?include_archived=1" : ""}`),
  get: (gid: string) => call<GoalDetail>("GET", `/goals/${id(gid)}`),
  create: (body: GoalInput) => call<Goal>("POST", "/goals", body),
  update: (gid: string, body: GoalPatch) => call<Goal>("PATCH", `/goals/${id(gid)}`, body),
};
