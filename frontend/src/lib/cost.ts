"use client";
import { create } from "zustand";
import { call } from "./api";
import type { WsFrame } from "./types";

// ---- contract (docs/architecture/08-api.md section 12) ----
export interface CostRange { min_usd: number; max_usd: number }
export interface TaskEstimate { task_id: string; title: string; agent_id: string; min_usd: number; max_usd: number }
export type EstimateBasis = "simulation" | "token_heuristic" | "fallback";
export interface CostEstimate {
  request_id: string;
  tasks: TaskEstimate[];
  synthesis: CostRange;
  total: CostRange;
  currency: string;
  mode: string;
  model: string;
  basis: EstimateBasis;
  threshold_usd: number;
  budget_cap_usd: number;
  requires_confirmation: boolean;
  confirm_reason?: "threshold" | "cap";
}
export type BudgetScope = "org" | "request" | "agent";
export interface BudgetAlert {
  scope: BudgetScope; scope_id: string; request_id: string; agent_id: string; task_id?: string;
  cap_usd: number; spent_usd: number; reserved_usd?: number; needed_usd: number;
  resumable: boolean; ts: number;
}
export interface BudgetWarning {
  scope: BudgetScope; scope_id: string; request_id?: string; agent_id?: string;
  spent_usd: number; cap_usd: number; pct: number; ts: number;
}
export interface AgentBudget { agent_id: string; cap_usd: number; spent_usd: number; reserved_usd: number; paused: boolean }
export interface PauseInfo {
  scope: BudgetScope; scope_id: string; request_id: string; since: string;
  cap_usd: number; spent_usd: number; needed_usd: number; waiters: number;
}
export interface BudgetStatus {
  org: { budget_usd: number; used_usd: number; reserved_usd: number };
  defaults: { request_cap_usd: number; agent_cap_usd: number; confirm_threshold_usd: number; period_start: string };
  agents: AgentBudget[];
  pauses: PauseInfo[];
}
export interface AgentCost { agent_id: string; cost_usd: number; calls: number; input_tokens: number; output_tokens: number }
export interface TaskCost { task_id: string; request_id: string; title: string; agent_id: string; cost_usd: number; calls: number }
export interface ToolCost { tool: string; calls: number; cost_usd: number }
export interface OperationCost { kind: string; calls: number; cost_usd: number }
export interface ModelCost { model: string; calls: number; cost_usd: number }
export interface CostBreakdown {
  request_id?: string; total_usd: number; calls: number; untracked_usd: number; llm_only_usd: number;
  by_agent: AgentCost[]; by_task: TaskCost[]; by_tool: ToolCost[]; by_operation: OperationCost[]; by_model: ModelCost[];
}

function without<T>(obj: Record<string, T>, key: string): Record<string, T> {
  return Object.fromEntries(Object.entries(obj).filter(([k]) => k !== key));
}

export const alertKey = (scope: string, id: string) => `${scope}|${id}`;

interface CostState {
  estimates: Record<string, CostEstimate>;
  alerts: Record<string, BudgetAlert>;
  warnings: Record<string, BudgetWarning>;
  /** keys of alerts/warnings the user hid (local only) */
  hidden: Record<string, true>;
  status: BudgetStatus | null;
  breakdown: CostBreakdown | null;
  applyFrame: (f: WsFrame) => void;
  hide: (key: string) => void;
  loadEstimate: (requestId: string) => Promise<void>;
  loadStatus: () => Promise<void>;
  loadBreakdown: (requestId?: string) => Promise<void>;
  confirm: (requestId: string, proceed: boolean, capUsd?: number) => Promise<void>;
  setRequestCap: (requestId: string, capUsd: number) => Promise<void>;
  setAgentCap: (agentId: string, capUsd: number) => Promise<void>;
}

function toAlert(p: any, ts: number): BudgetAlert {
  return {
    scope: p.scope, scope_id: p.scope_id, request_id: p.request_id ?? "", agent_id: p.agent_id ?? "", task_id: p.task_id || undefined,
    cap_usd: Number(p.cap_usd) || 0, spent_usd: Number(p.spent_usd) || 0, reserved_usd: Number(p.reserved_usd) || 0,
    needed_usd: Number(p.needed_usd) || 0, resumable: p.resumable !== false, ts,
  };
}

export const useCost = create<CostState>((set, get) => ({
  estimates: {}, alerts: {}, warnings: {}, hidden: {}, status: null, breakdown: null,

  applyFrame: (f) => {
    const p = f.payload || {};
    switch (f.type) {
      case "hello":
        void get().loadStatus();
        break;
      case "cost.estimated":
        if (p.estimate?.request_id) set((s) => ({ estimates: { ...s.estimates, [p.estimate.request_id]: p.estimate as CostEstimate } }));
        break;
      case "budget.exceeded": {
        const key = alertKey(p.scope, p.scope_id);
        set((s) => {
          // a new stop is always shown again
          return { alerts: { ...s.alerts, [key]: toAlert(p, Date.now()) }, hidden: without(s.hidden, key) };
        });
        void get().loadStatus();
        break;
      }
      case "budget.resumed": {
        const key = alertKey(p.scope, p.scope_id);
        set((s) => {
          return { alerts: without(s.alerts, key) };
        });
        void get().loadStatus();
        break;
      }
      case "budget.warning": {
        const key = alertKey(p.scope, p.scope_id);
        set((s) => {
          return { warnings: { ...s.warnings, [key]: { ...p, ts: Date.now() } as BudgetWarning }, hidden: without(s.hidden, key) };
        });
        break;
      }
    }
  },

  hide: (key) => set((s) => ({ hidden: { ...s.hidden, [key]: true } })),

  loadEstimate: async (requestId) => {
    try {
      const e = await call<CostEstimate>("GET", `/requests/${requestId}/estimate`);
      if (e && e.request_id) set((s) => ({ estimates: { ...s.estimates, [requestId]: e } }));
    } catch { /* no estimate yet */ }
  },

  loadStatus: async () => {
    try {
      const st = await call<BudgetStatus>("GET", "/budget");
      if (!st || !Array.isArray(st.agents)) return;
      set((s) => {
        // Rebuild alerts from the server's active pauses (page reload / reconnect).
        const alerts = { ...s.alerts };
        for (const p of st.pauses || []) {
          const key = alertKey(p.scope, p.scope_id);
          alerts[key] = {
            ...(alerts[key] || toAlert({ ...p, agent_id: p.scope === "agent" ? p.scope_id : "" }, Date.parse(p.since) || Date.now())),
            cap_usd: p.cap_usd, spent_usd: p.spent_usd, needed_usd: p.needed_usd, resumable: true,
          };
        }
        for (const [k, a] of Object.entries(alerts)) {
          if (a.resumable && !(st.pauses || []).some((p) => alertKey(p.scope, p.scope_id) === k)) delete alerts[k];
        }
        return { status: st, alerts };
      });
    } catch { /* backend without cost control */ }
  },

  loadBreakdown: async (requestId) => {
    try {
      const b = await call<CostBreakdown>("GET", requestId ? `/requests/${requestId}/cost` : "/costs/breakdown");
      if (b && Array.isArray(b.by_agent)) set({ breakdown: b });
    } catch { /* backend without cost control */ }
  },

  confirm: async (requestId, proceed, capUsd) => {
    await call("POST", `/requests/${requestId}/confirm`, {
      decision: proceed ? "proceed" : "cancel",
      ...(proceed && capUsd && capUsd > 0 ? { budget_cap_usd: capUsd } : {}),
    });
  },

  setRequestCap: async (requestId, capUsd) => {
    await call("PUT", `/requests/${requestId}/budget`, { budget_cap_usd: capUsd });
    void get().loadStatus();
  },

  setAgentCap: async (agentId, capUsd) => {
    await call("PUT", `/agents/${agentId}/budget`, { monthly_budget_usd: capUsd });
    void get().loadStatus();
  },
}));

/** Frame types handled by the cost store. */
export const isCostFrame = (type: string) => type === "hello" || type.startsWith("cost.") || type.startsWith("budget.");
