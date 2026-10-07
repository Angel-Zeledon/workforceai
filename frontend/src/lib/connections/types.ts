// Types for Connections + Controls. They mirror docs/architecture/integrations-credentials.md (sections 3, 12, 14).
// NOTHING here ever carries a secret: only metadata and state.

export type Risk = "low" | "medium" | "high";
export type Reversibility = "full" | "compensating" | "none";
export type ConnStatus = "pending" | "active" | "needs_reauth" | "error" | "suspended" | "revoked" | "expired";
export type ConnKind = "oauth2" | "api_key";
export type AuthMethod = "oauth2_byo_app" | "api_key";
export type Role = "owner" | "admin" | "member";

export interface CapabilityDef {
  id: string;
  risk: Risk;
  side_effects: boolean;
  reversibility?: Reversibility;
  default: boolean;
  always_approval?: boolean;
  hold_seconds_default?: number;
  /** Gmail: capabilities belong to the read or to the write connection profile (owner decision: separate). */
  profile?: "read" | "write";
}
export interface ResourceFilterDef { key: string; type: "list" | "number"; default?: string | number }
export interface ProviderDef {
  id: string;
  auth: AuthMethod[];
  capabilities: CapabilityDef[];
  resource_filters: ResourceFilterDef[];
  available: boolean;
  phase: string;
  /** Gmail splits read and write into two connections. */
  split_read_write?: boolean;
}

export interface CredentialMeta {
  kind: "oauth_refresh" | "api_key";
  /** last 4 characters, only for secrets of >= 20 chars; never the secret. */
  hint: string | null;
  version: number;
  expires_at: string | null;
  rotated_at: string | null;
  created_at: string;
}
export interface ConnLimits {
  per_day?: number;
  write_per_day?: number;
  max_items_per_call?: number;
  monthly_budget_usd?: number;
}
export interface Connection {
  id: string;
  provider: string;
  kind: ConnKind;
  label: string;
  account_label: string | null;
  mode: "live" | "simulated";
  status: ConnStatus;
  status_reason: string | null;
  profile?: "read" | "write";
  requested_capabilities: string[];
  granted_capabilities: string[];
  resource_scope: Record<string, string[] | number>;
  credential: CredentialMeta | null;
  /** public OAuth client id of the owner's own app ("bring your own app"); the client secret is write-only. */
  oauth_client_id?: string;
  limits: ConnLimits;
  read_only: boolean;
  grants_count: number;
  last_tested_at: string | null;
  last_used_at: string | null;
  created_at: string;
  revoked_at?: string | null;
  pending_provider_revocation?: boolean;
}

export type GrantStatus = "active" | "pending_second_approval" | "revoked";
export interface Grant {
  id: string;
  connection_id: string;
  agent_id: string;
  capabilities: string[];
  resource_scope: Record<string, string[] | number>;
  constraints: { allowed_recipient_domains?: string[] };
  max_risk: Risk;
  valid_until: string | null;
  status: GrantStatus;
  granted_by: string;
  requested_by: string;
  approved_by: string | null;
  created_at: string;
}

export type Decision = "allowed" | "needs_approval" | "denied";
export interface UsageRow {
  id: string;
  connection_id: string;
  agent_id: string | null;
  tool: string;
  capability: string;
  decision: Decision;
  deny_reason: string | null;
  status: "succeeded" | "failed" | "skipped" | "held";
  resource_ref: string | null;
  items_count: number | null;
  cost_usd: number;
  tainted: boolean;
  created_at: string;
}

export interface Human { id: string; name: string; role: Role }
export interface OrgControls {
  mode: "normal" | "read_only";
  kill_switch_level: "none" | "freeze" | "lockdown";
  reason: string | null;
  set_by: string | null;
  set_at: string | null;
  settings: { plan_review: "never" | "touches_writes" | "always" };
  /** per-tool kill switch: tools (capability ids) switched off for everybody. */
  disabled_tools: string[];
  humans: Human[];
  viewer: Human;
  /** admins (admin + owner) count: write grants need a second human only when > 1. */
  admin_count: number;
}
export interface AgentControl {
  agent_id: string;
  control: "active" | "paused";
  reason: string | null;
  paused_by: string | null;
  paused_at: string | null;
  read_only: boolean;
}

export interface SpendLimitStatus {
  id: string;
  scope_type: "org" | "agent" | "project" | "connection" | "tool";
  scope_id: string | null;
  period: "day" | "month" | "total";
  limit_usd: number;
  used_usd: number;
  on_hit: "warn" | "pause_and_ask" | "block";
}

export type DraftStatus = "draft" | "pending_approval" | "held" | "sent" | "cancelled" | "rejected" | "blocked";
export interface EmailDraft {
  id: string;
  agent_id: string;
  connection_id: string;
  account_label: string | null;
  to: string[];
  subject: string;
  body: string;
  status: DraftStatus;
  block_reason: string | null;
  /** at least one recipient only appears in external (untrusted) content. */
  origin_external: boolean;
  new_recipient: boolean;
  reversibility: Reversibility;
  /** server-decided: mandatory cancellation window, in seconds (>= 60). */
  hold_seconds: number;
  hold_until: string | null;
  version: number;
  created_at: string;
  updated_at: string;
}

export interface PlanTaskReview {
  id: string; title: string; agent_id: string; depends_on: string[];
}
export interface ReachableConnection {
  connection_id: string; label: string; agent_id: string; capabilities: string[];
}
export interface PlanReview {
  id: string;
  request_text: string;
  status: "plan_ready" | "approved" | "rejected";
  tasks: PlanTaskReview[];
  reachable_connections: ReachableConnection[];
  approvals_expected: { min: number; reason_keys: string[] };
  est_cost_usd: { p50: number; p90: number };
  est_duration_s: { p50: number };
  /** true when some reachable connection has write capabilities: review is mandatory (owner decision). */
  review_required: boolean;
  no_external_actions: boolean;
  note: string;
}

export interface CanDoEntry {
  agent_id: string;
  grants: Grant[];
}
