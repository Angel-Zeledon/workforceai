/** Batches ("lotes"): one project template launched for many customers. Contract: backend/internal/api/projects.go
 * (08-api.md §16.10). A batch counts as one active project; every customer gets its own normal project. */
import { create } from "zustand";
import { call } from "../api";

export interface BatchInput {
  name?: string; template_id: string; goal?: string; params?: Record<string, string>; customer_ids: string[];
  budget_per_project_usd?: number; parallel?: number; confirm?: boolean; allow_missing?: boolean;
}
export interface BatchPreviewRow { customer_id: string; name: string; missing: string[]; error?: string }
export interface BatchPreview {
  customers: BatchPreviewRow[]; vars_used: string[];
  per_project_p50_usd: number; per_project_p90_usd: number; total_p50_usd: number; total_p90_usd: number;
  budget_per_project_usd: number; total_budget_usd: number; max_customers: number; parallel: number;
  confirm_threshold_usd: number; needs_confirm: boolean; missing: number;
}
export interface BatchItem { customer_id: string; customer_name: string; project_id: string; state: "queued" | "launched" | "cancelled" | "error"; error?: string }
export interface Batch {
  id: string; name: string; template_id: string; goal?: string; items: BatchItem[]; parallel: number; budget_per_project_usd: number;
  control: "active" | "paused" | "cancelled"; status: "running" | "done" | "cancelled"; created_by?: string; created_at: string; finished_at: string | null;
}
export interface BatchRow {
  customer_id: string; customer_name: string; project_id?: string; status: string; progress: number;
  current_step?: string; current_agent_id?: string; cost_usd: number; needs_input: number; waiting_seconds?: number; error?: string;
}
export interface BatchView { batch: Batch; rows: BatchRow[]; summary: Record<string, number>; cost_usd: number }
export interface BatchSummary { id: string; name: string; status: string; control: string; customers: number; summary: Record<string, number>; created_at: string }

const id = (s: string) => encodeURIComponent(s);

export const batchApi = {
  preview: (body: BatchInput) => call<BatchPreview>("POST", "/project-batches/preview", body),
  create: (body: BatchInput) => call<Batch>("POST", "/project-batches", body),
  list: async () => {
    const r = await call<{ items?: BatchSummary[] }>("GET", "/project-batches");
    return Array.isArray(r?.items) ? r.items : [];
  },
  get: (bid: string) => call<BatchView>("GET", `/project-batches/${id(bid)}`),
  control: (bid: string, action: "pause" | "resume" | "cancel") => call<Batch>("POST", `/project-batches/${id(bid)}/control`, { action }),
  cancelCustomer: (bid: string, cid: string) => call<Batch>("POST", `/project-batches/${id(bid)}/customers/${id(cid)}/cancel`),
  retryFailed: (bid: string) => call<{ retried: number }>("POST", `/project-batches/${id(bid)}/retry-failed`),
};

/** Which batch is open and whether the "launch for several customers" wizard is showing (for which template). */
export const useBatchUi = create<{
  openId: string | null; wizardTemplate: string | null;
  open: (id: string | null) => void; openWizard: (templateId: string | null) => void;
}>((set) => ({
  openId: null, wizardTemplate: null,
  open: (openId) => set({ openId, wizardTemplate: null }),
  openWizard: (wizardTemplate) => set({ wizardTemplate }),
}));
