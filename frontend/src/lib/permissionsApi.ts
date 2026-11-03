import { call } from "./api";
import type { AskIf, Mode, PlainLevel } from "./permissionsLogic";

/** Contract: backend/internal/api/permissions.go and docs/architecture/08-api.md (approval permissions, additive). */

export interface LevelOption { level: PlainLevel; autonomy: string; allowed: boolean; reason?: string }
export interface Exception { category: string; mode: Mode; if?: AskIf; created_by?: string; created_at: string; source?: string }
export interface FloorLift { category: string; by: string; at: string }
export interface CategoryView { category: string; floor: boolean; lifted?: FloorLift; exception?: Exception; effective: Mode }
export interface AgentPermissions {
  agent_id: string; name: string; autonomy: string; level: PlainLevel | ""; factory: boolean; ceiling: string;
  options: LevelOption[]; categories: CategoryView[]; can_edit: boolean; can_lift_floor: boolean; updated_by?: string; updated_at?: string;
}
export interface ScopeGrant {
  id: string; kind: "project" | "batch"; scope_id: string; agent_id?: string; category: string; approval_id?: string;
  created_by?: string; created_at: string; expires_at: string;
}
export type RememberScope = "agent" | "project" | "batch";
export interface RememberResult { scope: RememberScope; category: string; agent_id: string; scope_grant?: ScopeGrant }
export interface PermissionsOverview {
  default_level: PlainLevel | ""; categories: string[]; floor: string[]; can_edit_default: boolean; scopes?: ScopeGrant[];
  agents: { agent_id: string; name: string; level: PlainLevel | ""; factory: boolean; exceptions: Exception[]; floor_lifts: FloorLift[] }[];
}

const enc = encodeURIComponent;
const base = (id: string) => `/agents/${enc(id)}/permissions`;

export const permissionsApi = {
  agent: (id: string) => call<AgentPermissions>("GET", base(id)),
  level: (id: string, level: PlainLevel) => call<AgentPermissions>("PUT", `${base(id)}/level`, { level }),
  setException: (id: string, category: string, mode: Mode, cond?: AskIf, source = "panel") =>
    call<AgentPermissions>("PUT", `${base(id)}/exceptions/${enc(category)}`, { mode, ...(mode === "ask_if" ? { if: cond } : {}), source }),
  removeException: (id: string, category: string) => call<AgentPermissions>("DELETE", `${base(id)}/exceptions/${enc(category)}`),
  liftFloor: (id: string, category: string) => call<AgentPermissions>("POST", `${base(id)}/floor/${enc(category)}`, { understood: true }),
  restoreFloor: (id: string, category: string) => call<AgentPermissions>("DELETE", `${base(id)}/floor/${enc(category)}`),
  overview: () => call<PermissionsOverview>("GET", "/permissions"),
  setDefault: (level: PlainLevel | "") => call<PermissionsOverview>("PUT", "/permissions/default", { level }),
  /** "Approve and do not ask again": call after approving; creates the exception (agent) or the project / batch scope. */
  remember: (approvalId: string, scope: RememberScope) => call<RememberResult>("POST", `/approvals/${enc(approvalId)}/remember`, { scope }),
  scopes: (q: { project_id?: string; batch_id?: string }) =>
    call<ScopeGrant[]>("GET", `/permissions/scopes?${new URLSearchParams(Object.entries(q).filter(([, v]) => !!v) as [string, string][]).toString()}`),
  revokeScope: (id: string) => call<unknown>("DELETE", `/permissions/scopes/${enc(id)}`),
};

/** Undo what "approve and do not ask again" created. */
export function undoRemember(r: RememberResult): Promise<unknown> {
  return r.scope_grant ? permissionsApi.revokeScope(r.scope_grant.id) : permissionsApi.removeException(r.agent_id, r.category);
}

/** HTTP status of a failed call ("PUT /x -> 409"), or 0. */
export function statusOf(e: unknown): number {
  const m = /-> (\d{3})/.exec(e instanceof Error ? e.message : "");
  return m ? Number(m[1]) : 0;
}
