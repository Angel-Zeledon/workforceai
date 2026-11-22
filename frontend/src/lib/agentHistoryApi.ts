import { call } from "./api";

/** Contract: backend/internal/api/agent_history.go and docs/architecture/08-api.md section 51 (F4, additive). */

export interface VersionSummary {
  version: number;
  created_at: string;
  actor: string;
  actor_kind: "person" | "system";
  actor_name?: string;
  /** How it arrived: "PUT /agents/{id}/tone", "trust.auto_demotion", "agent.created", "baseline", "restore"... */
  source: string;
  fields: string[];
  restored_from?: number;
}

export interface VersionList { agent_id: string; current: number; versions: VersionSummary[]; keep: number; can_restore: boolean }

export interface FieldChange { field: string; from?: unknown; to?: unknown; secret?: boolean }

export interface VersionDetail {
  version: VersionSummary & { config: Record<string, unknown> };
  previous?: number;
  changes: FieldChange[];
}

export interface RestoreStep {
  field: string;
  op?: string;
  category?: string;
  skill?: { id: string; name: string };
  autonomy?: string;
  budget_usd?: number;
  reason?: string;
  detail?: string;
}

export interface RestorePreview { version: number; apply: RestoreStep[]; skip: RestoreStep[]; can_restore: boolean }

export interface RestoreResult { from: number; version?: VersionSummary; applied: RestoreStep[]; skipped: RestoreStep[]; unchanged: boolean }

const enc = encodeURIComponent;
const base = (id: string) => `/agents/${enc(id)}/versions`;

export const agentHistoryApi = {
  list: (id: string) => call<VersionList>("GET", base(id)),
  detail: (id: string, v: number) => call<VersionDetail>("GET", `${base(id)}/${v}`),
  preview: (id: string, v: number) => call<RestorePreview>("GET", `${base(id)}/${v}/restore`),
  restore: (id: string, v: number) => call<RestoreResult>("POST", `${base(id)}/${v}/restore`),
};
