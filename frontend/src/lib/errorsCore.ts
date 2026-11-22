/**
 * Pure part of the central error translator (no imports: unit-tested with node --test). `classifyError` turns anything a
 * screen can catch (the API layer's error classes, a Response, a network TypeError, a status inside "METHOD path -> 409")
 * into i18n keys; `describeError` (errors.ts) renders them in the active language. Nothing here ever returns a raw code,
 * a status line or the text of an exception: the person reads what is happening and what to do.
 */

export interface ErrFacts { status: number; code: string; ref: string; network: boolean; timeout: boolean }

export interface ErrContext {
  /** Who can grant access ("tu administradora Ana"): completes a 403 with "Pídeselo a {who}". */
  who?: string;
  /** i18n key of the screen's own "No se pudo guardar"-style title, used when nothing more specific is known. */
  fallbackKey?: string;
}

export type ErrKind =
  | "network" | "timeout" | "session" | "forbidden" | "notfound" | "conflict" | "invalid" | "toolarge" | "unsupported"
  | "locked" | "ratelimit" | "limit" | "unavailable" | "server" | "unknown" | "code";

export interface ErrParts {
  kind: ErrKind;
  titleKey: string;
  detailKey?: string;
  actionKey?: string;
  vars: Record<string, string | number>;
  retry: boolean;
  ref?: string;
  /** Nothing known about it: the caller adds a reference (the backend's, or one made here and logged) for support. */
  needsRef: boolean;
}

/** Codes the backend sends that only restate an HTTP class: the status (or this fallback) picks the phrase. */
const GENERIC_CODE_STATUS: Record<string, number> = {
  forbidden: 403, not_found: 404, conflict: 409, invalid: 400, invalid_args: 400, invalid_request: 400, bad_request: 400,
  internal: 500, unauthorized: 401, rate_limited: 429, rate_limit: 429, export_rate_limited: 429, request_failed: 0, failed: 0, unknown_error: 0,
  file_not_found: 404,
};

/** Backend error code -> id of its human phrase (`err.c.<id>`). Several codes can share a phrase. */
export const CODE_PHRASE: Record<string, string> = {
  // drive and files
  read_only_folder: "readonly_folder", read_only: "readonly_folder", shared_read_only: "readonly_folder", read_only_mode: "readonly_org",
  version_conflict: "version_conflict", folder_invalid: "folder_invalid", path_invalid: "path_invalid", path_traversal: "path_invalid", invalid_dir: "path_invalid",
  too_large: "too_large", file_too_large: "too_large", upload_too_large: "too_large", package_too_large: "too_large", code_too_large: "too_large", image_too_large: "too_large", max_upload_bytes: "too_large",
  quota: "quota", quota_exceeded: "quota", plan_limit: "plan_limit",
  unsupported_type: "unsupported_file", unsupported_media_type: "unsupported_file", extension_not_allowed: "unsupported_file", content_type_not_allowed: "unsupported_file", not_text: "unsupported_file", image_unsupported: "unsupported_file",
  content_mismatch: "content_mismatch", upload_invalid: "upload_failed", upload_interrupted: "upload_failed", too_many_files: "too_many_files",
  protected_folder: "protected", root_not_allowed: "protected", duplicate_path: "duplicate", name_taken: "name_taken", name_invalid: "name_invalid", name_immutable: "name_invalid",
  empty_file: "empty_file", empty_response: "empty_file", drive_error: "drive_unavailable", drive_unavailable: "drive_unavailable", shares_unavailable: "drive_unavailable", already_shared: "already_shared",
  // file edits and comments
  edit_not_found: "edit_missing", edit_ambiguous: "edit_ambiguous", edit_count_mismatch: "edit_failed", edit_invalid_result: "edit_failed",
  anchor_not_in_text: "anchor_lost", anchor_lost: "anchor_lost", change_already_requested: "change_requested", already_resolved: "already_resolved",
  not_anchorable: "no_anchor", no_anchor: "no_anchor", invalid_comment: "comment_invalid", too_many_comments: "too_many_comments",
  // approvals
  approval_mismatch: "approval_changed", approval_required: "approval_required", decision_invalid: "bad_state", invalid_decision: "bad_state", not_pending: "already_resolved",
  not_decider: "not_decider", expired: "expired", superseded: "superseded", inputs_changed: "inputs_changed", risk_exceeds_grant: "risk_exceeds",
  // budget, kill switch, controls
  budget_exceeded: "budget", call_budget_exceeded: "budget", budget_above_ceiling: "budget_ceiling", kill_switch_active: "kill_switch", agent_paused: "agent_paused",
  outside_operating_hours: "off_hours", controls_unavailable: "controls_unavailable", rehearsal_mode: "rehearsal", provider_no_balance: "no_balance",
  insufficient_coins: "no_coins", already_owned: "owned", not_owned: "not_owned", layout_conflict: "layout_conflict",
  // goals, watchdog, projects
  cascade_changed: "cascade_changed", cascade_hidden: "cascade_changed", skip_blocks_result: "skip_blocked", skip_needs_confirm: "skip_confirm",
  cycle: "plan_cycle", dangling_dependency: "plan_cycle", empty_title: "plan_invalid", planner_invalid: "plan_invalid", no_nodes: "plan_invalid", invalid_output: "plan_invalid",
  no_agent: "no_agent", no_agents: "no_agent", agent_required: "no_agent", unknown_agent: "no_agent", no_runtime: "runtime_unavailable", runtime_unavailable: "runtime_unavailable",
  invalid_state: "bad_state", unknown_action: "bad_state", level_invalid: "bad_state", invalid_status: "bad_state", already_active: "bad_state", already_current: "bad_state", not_active: "bad_state",
  // skills and templates
  skill_unavailable: "skill_unavailable", skill_not_available: "skill_unavailable", stale_review: "stale_review", cannot_restore: "cannot_restore",
  skill_md_missing: "skill_package", frontmatter_invalid: "skill_package", frontmatter_missing: "skill_package", frontmatter_unclosed: "skill_package", zip_bomb: "skill_package", too_many_allowed_tools: "skill_package",
  tests_unavailable: "skill_tests", too_many_cases: "skill_tests", case_prompt_required: "skill_tests", case_prompt_too_long: "skill_tests", case_title_too_long: "skill_tests", case_expect_too_long: "skill_tests",
  template_invalid: "template_invalid",
  // offices and people
  agent_other_office: "other_office", office_forbidden: "office_forbidden", not_in_office: "other_office", office_unavailable: "office_unavailable", same_office: "other_office", office_immutable: "other_office",
  admin_required: "forbidden_admin", humans_only: "forbidden_admin", person_denied: "forbidden_admin", person_no_access: "forbidden_admin", no_access: "forbidden_admin",
  customer_isolation: "customer_isolation", customer_scope_unknown: "customer_isolation", customer_unavailable: "customer_isolation", resource_out_of_scope: "out_of_scope", outside_scope: "out_of_scope", moves_out_of_scope: "out_of_scope",
  // connections, data, web
  credential_required: "credential_required", connection_unavailable: "connection_unavailable", reauth_required: "reauth", scope_not_granted: "scope_not_granted", tool_not_granted: "scope_not_granted", not_granted: "scope_not_granted",
  not_configured: "not_configured", db_not_configured: "not_configured", vendor_integrations_disabled: "integration_off", tool_disabled: "integration_off",
  db_unreachable: "db_unreachable", db_host_not_found: "db_unreachable", db_tls_failed: "db_unreachable", unreachable: "db_unreachable",
  address_not_allowed: "address_blocked", db_address_not_allowed: "address_blocked", insecure_redirect: "address_blocked",
  db_not_found: "db_not_found", db_read_only_not_enforced: "db_write", sql_not_read_only: "db_write", sql_forbidden_function: "db_write", sql_schema_not_allowed: "db_write",
  sql_empty: "sql_invalid", sql_invalid: "sql_invalid", sql_multiple_statements: "sql_invalid", sql_too_long: "sql_invalid", sql_timeout: "sql_timeout", source_alias_required: "sql_invalid",
  web_page_not_data: "web_not_data", fetch_failed: "fetch_failed", http_status: "fetch_failed", too_many_redirects: "fetch_failed", redirect_invalid: "fetch_failed", network: "fetch_failed",
  encryption_unavailable: "vault_unavailable", vault_required: "vault_unavailable", header_unavailable: "vault_unavailable", slack_auth_failed: "slack_auth", oauth_failed: "oauth_failed", provider_error: "provider_error",
  secret_detected: "secret_detected", memory_unavailable: "memory_unavailable", execution_record_unavailable: "memory_unavailable",
  // code execution, documents, images
  code_execution_disabled: "code_disabled", sandbox_unavailable: "code_disabled", auto_run_not_allowed: "code_disabled", code_tool_not_granted: "code_disabled", code_invalid: "code_invalid", code_daily_limit: "code_limit",
  pdf_invalid: "pdf_unreadable", pdf_encrypted: "pdf_unreadable", pdf_pages_invalid: "pdf_unreadable", pdf_too_large: "too_large", pdf_busy: "pdf_busy", pdf_timeout: "pdf_busy", render_failed: "pdf_busy", render_unavailable: "pdf_busy", image_invalid: "image_invalid",
  // billing, meetings, sign in
  billing_unavailable: "billing_unavailable", not_paid: "not_paid", same_plan: "same_plan", no_subscription: "no_subscription", foreign_subscription: "forbidden_admin",
  blocked: "calendar_blocked", invalid_credentials: "bad_credentials", mock: "demo",
};

const STATUS_KIND: Array<[(s: number) => boolean, ErrKind]> = [
  [(s) => s === 401, "session"], [(s) => s === 403, "forbidden"], [(s) => s === 404 || s === 410, "notfound"], [(s) => s === 408, "timeout"],
  [(s) => s === 409, "conflict"], [(s) => s === 413, "toolarge"], [(s) => s === 415, "unsupported"], [(s) => s === 423, "locked"], [(s) => s === 429, "ratelimit"],
  [(s) => s === 402, "limit"], [(s) => s === 400 || s === 422, "invalid"], [(s) => s === 405 || s === 501 || s === 502 || s === 503 || s === 504, "unavailable"],
  [(s) => s >= 500, "server"],
];

const NETWORK_RE = /failed to fetch|networkerror|network request failed|load failed|fetch failed|the internet connection|err_internet|econnrefused|err_network/i;

const isObj = (x: unknown): x is Record<string, unknown> => !!x && typeof x === "object";

/** Everything the translator can learn from a caught value. */
export function errorFacts(err: unknown): ErrFacts {
  const f: ErrFacts = { status: 0, code: "", ref: "", network: false, timeout: false };
  let message = "";
  if (typeof err === "string") message = err;
  if (isObj(err)) {
    if (typeof err.status === "number") f.status = err.status;
    if (typeof err.code === "string") f.code = err.code;
    if (typeof err.ref === "string") f.ref = err.ref;
    if (typeof err.message === "string") message = err.message;
    const name = typeof err.name === "string" ? err.name : "";
    if (name === "AbortError" || name === "TimeoutError") f.timeout = true;
    if (name === "TypeError" && NETWORK_RE.test(message)) f.network = true;
    // the body of a failed call, when a screen hands it over as is: {error, code, message, ref}
    if (isObj(err.error) && typeof err.error.code === "string" && !f.code) f.code = err.error.code;
  }
  if (/not available in mock mode/i.test(message) || f.code === "mock") { f.code = "mock"; f.status = 0; }
  if (!f.status) {
    const m = /->\s*(\d{3})\b/.exec(message);
    if (m) f.status = Number(m[1]);
  }
  if (!f.code && /^[a-z][a-z0-9_]*$/.test(message)) f.code = message; // the mock throws the bare code
  if (!f.network && NETWORK_RE.test(message) && !f.status) f.network = true;
  if (!f.timeout && /timed? ?out|timeout/i.test(message) && !f.status && !CODE_PHRASE[f.code]) f.timeout = true;
  return f;
}

const part = (kind: ErrKind, titleKey: string, extra: Partial<ErrParts> = {}): ErrParts =>
  ({ kind, titleKey, vars: {}, retry: false, needsRef: false, ...extra });

export function classifyError(err: unknown, ctx: ErrContext = {}): ErrParts {
  const f = errorFacts(err);
  const ref = f.ref || undefined;
  const withRef = (p: ErrParts): ErrParts => (ref ? { ...p, ref } : p);

  if (f.network) return withRef(part("network", "err.network.title", { actionKey: "err.network.action", retry: true }));
  if (f.timeout) return withRef(part("timeout", "err.timeout.title", { actionKey: "err.timeout.action", retry: true }));

  let status = f.status;
  const generic = GENERIC_CODE_STATUS[f.code];
  if (!status && generic) status = generic;

  // a known code says exactly what happened (except the sign-in expiry, which is the same sentence for everything)
  const phrase = CODE_PHRASE[f.code];
  if (phrase && status !== 401) {
    if (phrase === "plan_limit") return withRef(part("limit", "err.c.plan_limit", { actionKey: "err.c.plan_limit.action" }));
    if (phrase === "forbidden_admin") return withRef(part("forbidden", "err.forbidden.title", { actionKey: ctx.who ? "err.forbidden.ask" : "err.forbidden.action", vars: ctx.who ? { who: ctx.who } : {} }));
    const retry = status === 409 || status >= 500 || phrase === "pdf_busy" || phrase === "sql_timeout" || phrase === "drive_unavailable" || phrase === "provider_error";
    return withRef(part("code", `err.c.${phrase}`, { retry }));
  }
  if (phrase === "bad_credentials") return withRef(part("code", "err.c.bad_credentials"));

  const hit = STATUS_KIND.find(([test]) => test(status));
  if (hit) {
    const kind = hit[1];
    switch (kind) {
      case "session": return withRef(part(kind, "err.session.title", { actionKey: "err.session.action" }));
      case "forbidden": return withRef(part(kind, "err.forbidden.title", { actionKey: ctx.who ? "err.forbidden.ask" : "err.forbidden.action", vars: ctx.who ? { who: ctx.who } : {} }));
      case "notfound": return withRef(part(kind, "err.notfound.title"));
      case "conflict": return withRef(part(kind, "err.conflict.title", { actionKey: "err.conflict.action", retry: true }));
      case "invalid": return withRef(part(kind, "err.invalid.title", { actionKey: "err.invalid.action" }));
      case "toolarge": return withRef(part(kind, "err.toolarge.title"));
      case "unsupported": return withRef(part(kind, "err.unsupported.title"));
      case "locked": return withRef(part(kind, "err.locked.title"));
      case "ratelimit": return withRef(part(kind, "err.ratelimit.title", { actionKey: "err.ratelimit.action", retry: true }));
      case "limit": return withRef(part(kind, "err.c.plan_limit", { actionKey: "err.c.plan_limit.action" }));
      case "unavailable": return withRef(part(kind, "err.unavailable.title", { actionKey: "err.unavailable.action", retry: true }));
      case "server": return withRef(part(kind, "err.server.title", { actionKey: "err.server.action", retry: true, needsRef: !ref }));
      default: break;
    }
  }
  // nothing known: the screen's own sentence (or the generic one) plus a reference to share, never the raw code
  return withRef(part("unknown", ctx.fallbackKey ?? "err.unknown.title", { actionKey: "err.unknown.action", retry: true, needsRef: !ref }));
}

/** Every phrase id the catalog must define (the test checks them against es/en/pt). */
export const PHRASE_IDS = Array.from(new Set(Object.values(CODE_PHRASE)));
