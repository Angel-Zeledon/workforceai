import { call } from "./api";
import type { RitualEntry, RitualKind } from "./ritualsLogic";

/** Rituals (B4): daily stand-up and weekly retro. Contract: backend/internal/api/rituals.go, docs/architecture/rituals.md. */
export type { RitualEntry, RitualKind };
export type ProposalStatus = "pending" | "accepted" | "dismissed";
export type ProposalKind = "instruction_note" | "skill_suggestion";
export type ProposalSignal = "rejected_approvals" | "reworks" | "failed_tasks" | "quality_score";

export interface Proposal {
  id: string; ritual_id: string; agent_id: string; kind: ProposalKind; signal: ProposalSignal;
  /** Server text in the organization language (the UI shows its own translation by signal). */
  title: string; text: string;
  /** What triggered it (titles, counts): data, never applied. */
  evidence: string[];
  status: ProposalStatus; applied: boolean; created_at: string; resolved_at?: string; resolved_by?: string;
}

export interface Ritual {
  id: string; kind: RitualKind; origin: "scheduled" | "manual"; period_key?: string;
  status: "held" | "skipped"; skip_reason?: string; locale: string;
  window_from?: string; window_to?: string;
  entries: RitualEntry[]; absent: { agent_id: string; name: string; reason: string }[];
  summary: string; summary_source: "deterministic" | "runtime" | "local";
  created_by: string; held_at: string;
  proposals?: Proposal[];
}

export interface Slot { enabled: boolean; time: string; weekdays: number[] }
export interface RitualSettings { timezone: string; standup: Slot; retro: Slot; use_model: boolean }
export interface AcceptResult { proposal: Proposal; applied: boolean; profile_version?: number }

export const ritualsApi = {
  list: (kind: RitualKind | "" = "", limit = 20) => call<Ritual[]>("GET", `/rituals?limit=${limit}${kind ? `&kind=${kind}` : ""}`),
  get: (id: string) => call<Ritual>("GET", `/rituals/${encodeURIComponent(id)}`),
  run: (kind: RitualKind) => call<Ritual>("POST", `/rituals/${kind}/run`),
  proposals: (status: ProposalStatus | "" = "pending") => call<Proposal[]>("GET", `/rituals/proposals${status ? `?status=${status}` : ""}`),
  accept: (id: string) => call<AcceptResult>("POST", `/rituals/proposals/${encodeURIComponent(id)}/accept`),
  dismiss: (id: string) => call<Proposal>("POST", `/rituals/proposals/${encodeURIComponent(id)}/dismiss`),
  settings: () => call<RitualSettings>("GET", "/settings/rituals"),
  putSettings: (s: RitualSettings) => call<RitualSettings>("PUT", "/settings/rituals", s),
};
