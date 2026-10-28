"use client";
import { create } from "zustand";
import { call } from "./api";
import type { WsFrame } from "./types";

/**
 * Rehearsal ("Ensayo general", B9; docs/architecture/rehearsal.md). Contract: backend/internal/api/rehearsal.go.
 * In a rehearsal the agents work for real but every send, payment or write is recorded as "would have done" and NOT
 * executed. The person marks each entry "would approve" / "would reject": evidence before giving more autonomy.
 */

export type WouldDecision = "allowed" | "needs_approval" | "denied";
export type Verdict = "" | "would_approve" | "would_reject";

export interface RehearsalAction {
  id: string; request_id: string; task_id: string; agent_id: string; tool: string; action: string;
  source: "gateway" | "simulated" | "code" | string; scope: "request" | "agent" | string;
  would_decision: WouldDecision; deny_reason?: string; risk: string; reversibility?: string; account?: string;
  recipients: string[]; amount?: number | null; flags: string[]; preview: Record<string, string>;
  verdict: Verdict; verdict_by?: string; verdict_at?: string | null; created_at: string;
}
export interface VerdictCounts { total: number; would_approve: number; would_reject: number; undecided: number }
export interface RequestRehearsal { request_id: string; rehearsal: boolean; actions: RehearsalAction[]; summary: VerdictCounts }
export interface AgentRehearsalSummary {
  agent_id: string; enabled: boolean; set_by?: string; updated_at?: string | null;
  actions: number; would_approve: number; would_reject: number; undecided: number;
}

export const rehearsalApi = {
  agents: async () => (await call<{ agents?: AgentRehearsalSummary[] }>("GET", "/rehearsal/agents")).agents ?? [],
  setAgent: (id: string, enabled: boolean) => call<{ agent_id: string; enabled: boolean }>("PUT", `/agents/${encodeURIComponent(id)}/rehearsal`, { enabled }),
  actions: async (q: { request_id?: string; agent_id?: string; limit?: number } = {}) => {
    const p = new URLSearchParams();
    if (q.request_id) p.set("request_id", q.request_id);
    if (q.agent_id) p.set("agent_id", q.agent_id);
    if (q.limit) p.set("limit", String(q.limit));
    return (await call<{ items?: RehearsalAction[] }>("GET", `/rehearsal/actions${p.size ? `?${p}` : ""}`)).items ?? [];
  },
  report: (requestId: string) => call<RequestRehearsal>("GET", `/requests/${encodeURIComponent(requestId)}/rehearsal`),
  verdict: (id: string, verdict: Verdict) => call<RehearsalAction>("POST", `/rehearsal/actions/${encodeURIComponent(id)}/verdict`, { verdict }),
  /** A whole request in rehearsal: nothing it asks to send, pay or write is executed. */
  submit: (text: string) => call<{ request_id: string }>("POST", "/requests", { text, rehearsal: true }),
};

export const isRehearsalFrame = (type: string) => type.startsWith("rehearsal.");

/** Live state: which agents are in rehearsal and a tick that bumps on any rehearsal event (lists refetch on it). */
export const useRehearsal = create<{
  enabled: Record<string, boolean>; tick: number; loaded: boolean;
  load: () => Promise<void>; applyFrame: (f: WsFrame) => void; setLocal: (id: string, on: boolean) => void;
}>((set) => ({
  enabled: {}, tick: 0, loaded: false,
  load: async () => {
    try {
      const ags = await rehearsalApi.agents();
      set({ enabled: Object.fromEntries(ags.map((a) => [a.agent_id, a.enabled])), loaded: true });
    } catch { set({ loaded: true }); }
  },
  applyFrame: (f) => {
    const p = f.payload || {};
    set((s) => ({
      tick: s.tick + 1,
      enabled: f.type === "rehearsal.agent_changed" && p.agent_id ? { ...s.enabled, [p.agent_id]: !!p.enabled } : s.enabled,
    }));
  },
  setLocal: (id, on) => set((s) => ({ enabled: { ...s.enabled, [id]: on } })),
}));
