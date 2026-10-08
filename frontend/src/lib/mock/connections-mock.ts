// Mock of the Connections + Controls backend (docs/architecture/integrations-credentials.md, sections 12 and 14.5).
// Rules reproduced here so the UI is exercised against the real contract:
//  - secrets are write-only: they are validated and DISCARDED, only { kind, hint } is kept;
//  - Gmail read and write are separate connections; agents never get "approve"; only the owner lifts the kill switch;
//    write grants need a second human only when the org has more than one admin;
//  - email sends have a mandatory server-side 60 s cancellation window;
//  - plan review is mandatory when the plan can reach a connection with write capabilities.
import type {
  AgentControl, ConnLimits, Connection, Decision, EmailDraft, Grant, Human, OrgControls, PlanReview,
  ProviderDef, SpendLimitStatus, UsageRow,
} from "../connections/types";

export class MockError extends Error {
  constructor(public status: number, public code: string) { super(code); }
}
const fail = (status: number, code: string): never => { throw new MockError(status, code); };

const HOLD_SECONDS = 60;
const AGENT_IDS = ["sales", "hr", "legal", "accounting", "analyst", "operations", "assistant"];
let seq = 100;
const uid = (p: string) => `${p}_${(++seq).toString(36)}`;
const nowIso = () => new Date().toISOString();
const ago = (min: number) => new Date(Date.now() - min * 60000).toISOString();

const PROVIDERS: ProviderDef[] = [
  {
    id: "google_gmail", auth: ["oauth2_byo_app"], available: true, phase: "C2", split_read_write: true,
    capabilities: [
      { id: "mail.read", risk: "low", side_effects: false, default: true, profile: "read" },
      { id: "mail.draft", risk: "low", side_effects: true, reversibility: "full", default: false, profile: "write" },
      { id: "mail.send", risk: "high", side_effects: true, reversibility: "none", default: false, always_approval: true, hold_seconds_default: HOLD_SECONDS, profile: "write" },
    ],
    resource_filters: [{ key: "labels", type: "list", default: "INBOX" }, { key: "max_age_days", type: "number", default: 30 }],
  },
  {
    id: "google_calendar", auth: ["oauth2_byo_app"], available: true, phase: "C2", split_read_write: true,
    capabilities: [
      { id: "calendar.read", risk: "low", side_effects: false, default: true, profile: "read" },
      { id: "calendar.create_event", risk: "medium", side_effects: true, reversibility: "compensating", default: false, always_approval: true, hold_seconds_default: HOLD_SECONDS, profile: "write" },
    ],
    resource_filters: [{ key: "calendars", type: "list", default: "primary" }],
  },
  {
    id: "google_drive", auth: ["oauth2_byo_app"], available: true, phase: "C2",
    capabilities: [{ id: "drive.read", risk: "low", side_effects: false, default: true, profile: "read" }],
    resource_filters: [{ key: "folders", type: "list" }],
  },
  {
    id: "github", auth: ["api_key"], available: true, phase: "C4", split_read_write: true,
    capabilities: [
      { id: "repo.read", risk: "low", side_effects: false, default: true, profile: "read" },
      { id: "repo.issue_write", risk: "medium", side_effects: true, reversibility: "compensating", default: false, always_approval: true, hold_seconds_default: HOLD_SECONDS, profile: "write" },
    ],
    resource_filters: [{ key: "repos", type: "list" }],
  },
  {
    id: "slack", auth: ["api_key"], available: true, phase: "C4", split_read_write: true,
    capabilities: [
      { id: "chat.read", risk: "low", side_effects: false, default: true, profile: "read" },
      { id: "chat.post", risk: "medium", side_effects: true, reversibility: "compensating", default: false, always_approval: true, hold_seconds_default: HOLD_SECONDS, profile: "write" },
    ],
    resource_filters: [{ key: "channels", type: "list" }],
  },
  {
    id: "generic_api", auth: ["api_key"], available: true, phase: "C1",
    capabilities: [{ id: "api.read", risk: "low", side_effects: false, default: true }],
    resource_filters: [],
  },
  { id: "gitlab", auth: ["api_key"], available: false, phase: "C4", capabilities: [{ id: "repo.read", risk: "low", side_effects: false, default: true }], resource_filters: [] },
  { id: "microsoft_365", auth: ["oauth2_byo_app"], available: false, phase: "C5", capabilities: [{ id: "mail.read", risk: "low", side_effects: false, default: true }], resource_filters: [] },
];
const capDef = (provider: string, cap: string) => PROVIDERS.find((p) => p.id === provider)?.capabilities.find((c) => c.id === cap);
const isWriteCap = (provider: string, cap: string) => !!capDef(provider, cap)?.side_effects;

type Emit = (type: string, payload: unknown, agentId?: string) => void;

export class MockConnections {
  private connections: Connection[] = [];
  private grants: Grant[] = [];
  private usage: UsageRow[] = [];
  private drafts: EmailDraft[] = [];
  private plans: PlanReview[] = [];
  private agentCtl: Record<string, AgentControl> = {};
  private controls!: OrgControls;
  private timers = new Map<string, ReturnType<typeof setTimeout>>();
  private planSeq = 0;
  private hours = { enabled: false, timezone: "UTC", weekly: [] as { day: number; open: string; close: string }[] };
  private anomaly = { disabled: false, auto_freeze: false };

  constructor(private emit: Emit) { this.seed(); }

  matches(r0?: string, r2?: string) {
    return r0 === "connections" || r0 === "connection-providers" || r0 === "org" || r0 === "spend-limits" || r0 === "outbox"
      || r0 === "plan-reviews" || (r0 === "requests" && r2 === "plan") || r0 === "tool-calls" || r0 === "agent-controls" || r0 === "dev"
      || (r0 === "agents" && (r2 === "control" || r2 === "connections"));
  }

  // ---- seed ----------------------------------------------------------------------------------
  reset() {
    this.timers.forEach((t) => clearTimeout(t)); this.timers.clear();
    this.seed();
  }
  private seed() {
    const humans: Human[] = [
      { id: "u_ana", name: "Ana", role: "owner" },
      { id: "u_luis", name: "Luis", role: "admin" },
      { id: "u_sofia", name: "Sofía", role: "member" },
    ];
    this.controls = {
      mode: "normal", kill_switch_level: "none", reason: null, set_by: null, set_at: null,
      settings: { plan_review: "touches_writes" }, disabled_tools: [], humans, viewer: humans[0], admin_count: 2,
    };
    this.agentCtl = Object.fromEntries(AGENT_IDS.map((id) => [id, { agent_id: id, control: "active" as const, reason: null, paused_by: null, paused_at: null, read_only: false }]));
    const base = {
      kind: "oauth2" as const, mode: "simulated" as const, status_reason: null,
      limits: { per_day: 2000, write_per_day: 20, max_items_per_call: 50, monthly_budget_usd: 5 } as ConnLimits,
      read_only: false, last_used_at: ago(3), revoked_at: null, created_at: ago(60 * 24 * 3),
    };
    const cred = () => ({ kind: "oauth_refresh" as const, hint: null, version: 1, expires_at: null, rotated_at: ago(60 * 24 * 3), created_at: ago(60 * 24 * 3) });
    this.connections = [
      { ...base, id: "cn_read", provider: "google_gmail", label: "Gmail de ventas (lectura)", account_label: "ventas@empresa.com", profile: "read", status: "active",
        requested_capabilities: ["mail.read"], granted_capabilities: ["mail.read"], resource_scope: { labels: ["INBOX"], max_age_days: 30 }, credential: cred(),
        oauth_client_id: "demo-app.apps.example", grants_count: 1, last_tested_at: ago(30) },
      { ...base, id: "cn_write", provider: "google_gmail", label: "Gmail de ventas (borradores y envío)", account_label: "ventas@empresa.com", profile: "write", status: "active", last_used_at: null,
        requested_capabilities: ["mail.draft", "mail.send"], granted_capabilities: ["mail.draft", "mail.send"], resource_scope: {}, credential: cred(),
        oauth_client_id: "demo-app.apps.example", grants_count: 1, last_tested_at: ago(30) },
    ];
    const g = (id: string, conn: string, caps: string[], extra: Partial<Grant> = {}): Grant => ({
      id, connection_id: conn, agent_id: "assistant", capabilities: caps, resource_scope: {}, constraints: {}, max_risk: caps.includes("mail.send") ? "high" : "low",
      valid_until: null, status: "active", granted_by: "u_ana", requested_by: "u_ana", approved_by: caps.some((c) => isWriteCap("google_gmail", c)) ? "u_luis" : null,
      created_at: ago(60 * 24 * 2), ...extra,
    });
    this.grants = [
      g("gr_1", "cn_read", ["mail.read"], { resource_scope: { labels: ["INBOX"], max_age_days: 30 } }),
      g("gr_2", "cn_write", ["mail.draft", "mail.send"], { constraints: { allowed_recipient_domains: ["acme.com", "cliente.mx"] } }),
    ];
    this.usage = [];
    const ev = (min: number, connId: string, cap: string, tool: string, agent: string, ref: string, items: number | null, decision: Decision = "allowed", deny: string | null = null, tainted = true) => {
      this.usage.push({ id: uid("cu"), connection_id: connId, agent_id: agent, tool, capability: cap, decision, deny_reason: deny,
        status: decision === "denied" ? "skipped" : "succeeded", resource_ref: ref, items_count: items, cost_usd: 0, tainted, created_at: ago(min) });
    };
    ev(95, "cn_read", "mail.read", "email.search", "assistant", "label:INBOX q:'from:cliente' n=25", 25);
    ev(60, "cn_read", "mail.read", "email.read", "assistant", "label:INBOX thread n=3", 3);
    ev(41, "cn_write", "mail.draft", "email.draft", "assistant", "draft reply (1)", 1, "allowed", null, false);
    ev(20, "cn_read", "mail.read", "email.search", "sales", "label:INBOX", null, "denied", "scope_not_granted", false);
    ev(8, "cn_read", "mail.read", "email.search", "assistant", "label:INBOX q:'is:unread' n=12", 12);
    this.usage.reverse();
    this.drafts = [{
      id: "tc_draft_1", agent_id: "assistant", connection_id: "cn_write", account_label: "ventas@empresa.com",
      to: ["compras@acme.com"], subject: "Propuesta comercial Q4",
      body: "Hola equipo de Acme,\n\nAdjunto un resumen de la propuesta para el cuarto trimestre. Quedo atenta a sus comentarios.\n\nSaludos,\nSofía",
      status: "draft", block_reason: null, origin_external: true, new_recipient: false, reversibility: "none", hold_seconds: HOLD_SECONDS, hold_until: null,
      version: 1, created_at: ago(5), updated_at: ago(5),
    }];
    this.plans = [this.buildPlan("pr_seed", "Responder a los correos de clientes de esta semana y proponer una reunión", ["assistant", "sales"], "Responder correos")];
    this.planSeq = 0;
  }

  // ---- helpers -------------------------------------------------------------------------------
  private viewer() { return this.controls.viewer; }
  private requireRole(roles: Human["role"][]) { if (!roles.includes(this.viewer().role)) fail(403, "forbidden_role"); }
  private conn(id: string): Connection { return this.connections.find((c) => c.id === id) ?? fail(404, "not_found"); }
  private pub(c: Connection): Connection {
    return { ...c, grants_count: this.grants.filter((g) => g.connection_id === c.id && g.status !== "revoked").length, credential: c.credential ? { ...c.credential } : null };
  }
  private log(connId: string, agent: string | null, tool: string, cap: string, decision: Decision, deny: string | null, ref: string | null, items: number | null = null, tainted = false) {
    const row: UsageRow = {
      id: uid("cu"), connection_id: connId, agent_id: agent, tool, capability: cap, decision, deny_reason: deny,
      status: decision === "denied" ? "skipped" : decision === "needs_approval" ? "held" : "succeeded", resource_ref: ref, items_count: items, cost_usd: 0, tainted, created_at: nowIso(),
    };
    this.usage.unshift(row);
    this.emit("connection.usage", { connection_id: connId, agent_id: agent, tool, action: cap, decision, status: row.status });
    const c = this.connections.find((x) => x.id === connId);
    if (c && decision === "allowed") c.last_used_at = row.created_at;
  }
  private changed(c: Connection) { this.emit("connection.updated", { connection: this.pub(c) }); }
  private snapshotControls(): OrgControls {
    const admin_count = this.controls.humans.filter((h) => h.role === "admin" || h.role === "owner").length;
    return { ...this.controls, humans: this.controls.humans.map((h) => ({ ...h })), admin_count };
  }
  private emitControls() { this.emit("control.changed", { scope: "org", controls: this.snapshotControls() }); }

  /** Policy: the controls are the first check (single choke point), then connection, then grant. */
  private evaluate(connId: string, agentId: string, cap: string): { decision: Decision; reason: string | null } {
    const c = this.connections.find((x) => x.id === connId);
    if (!c) return { decision: "denied", reason: "connection_unavailable" };
    const write = isWriteCap(c.provider, cap);
    if (this.controls.kill_switch_level !== "none") return { decision: "denied", reason: "kill_switch_active" };
    if (this.controls.mode === "read_only" && write) return { decision: "denied", reason: "read_only_mode" };
    if (this.controls.disabled_tools.includes(cap)) return { decision: "denied", reason: "tool_disabled" };
    if (this.agentCtl[agentId]?.control === "paused") return { decision: "denied", reason: "agent_paused" };
    if (c.status !== "active") return { decision: "denied", reason: "connection_unavailable" };
    if (c.read_only && write) return { decision: "denied", reason: "read_only_mode" };
    if (!c.granted_capabilities.includes(cap)) return { decision: "denied", reason: "scope_not_granted" };
    const gr = this.grants.find((g) => g.connection_id === connId && g.agent_id === agentId && g.status === "active");
    if (!gr || !gr.capabilities.includes(cap)) return { decision: "denied", reason: "scope_not_granted" };
    if (gr.valid_until && new Date(gr.valid_until).getTime() < Date.now()) return { decision: "denied", reason: "grant_expired" };
    const def = capDef(c.provider, cap);
    if (def?.always_approval) return { decision: "needs_approval", reason: null };
    return { decision: "allowed", reason: null };
  }

  private buildPlan(id: string, text: string, agents: string[], title: string): PlanReview {
    const tasks = agents.map((a, i) => ({ id: `${id}_t${i + 1}`, title: i === 0 ? title : `${title} (${a})`, agent_id: a, depends_on: i ? [`${id}_t${i}`] : [] }));
    return this.recompute({
      id, request_text: text, status: "plan_ready", tasks, reachable_connections: [], approvals_expected: { min: 0, reason_keys: [] },
      est_cost_usd: { p50: 0.42, p90: 0.95 }, est_duration_s: { p50: 240 }, review_required: false, no_external_actions: false, note: "",
    });
  }
  /** Deterministic preflight: reachable connections are an upper bound computed from the grants of the assigned agents. */
  private recompute(p: PlanReview): PlanReview {
    const agents = new Set(p.tasks.map((t) => t.agent_id));
    const reach: PlanReview["reachable_connections"] = [];
    for (const g of this.grants) {
      if (g.status !== "active" || !agents.has(g.agent_id)) continue;
      const c = this.connections.find((x) => x.id === g.connection_id);
      if (!c || c.status === "revoked") continue;
      const caps = p.no_external_actions ? g.capabilities.filter((cap) => !isWriteCap(c.provider, cap)) : g.capabilities;
      if (caps.length) reach.push({ connection_id: c.id, label: c.label, agent_id: g.agent_id, capabilities: caps });
    }
    const sends = reach.some((r) => r.capabilities.includes("mail.send"));
    const writes = reach.some((r) => { const c = this.connections.find((x) => x.id === r.connection_id)!; return r.capabilities.some((cap) => isWriteCap(c.provider, cap)); });
    return {
      ...p, reachable_connections: reach, review_required: writes || this.controls.settings.plan_review === "always",
      approvals_expected: { min: sends ? 1 : 0, reason_keys: sends ? ["send_email"] : [] },
      est_cost_usd: { p50: +(0.14 * p.tasks.length).toFixed(2), p90: +(0.32 * p.tasks.length).toFixed(2) },
      est_duration_s: { p50: 80 * p.tasks.length },
    };
  }

  private armHold(d: EmailDraft) {
    const ms = Math.max(0, new Date(d.hold_until!).getTime() - Date.now());
    clearTimeout(this.timers.get(d.id));
    this.timers.set(d.id, setTimeout(() => this.finishHold(d.id), ms));
  }
  private finishHold(id: string) {
    this.timers.delete(id);
    const d = this.drafts.find((x) => x.id === id);
    if (!d || d.status !== "held") return;
    const ev = this.evaluate(d.connection_id, d.agent_id, "mail.send");
    if (ev.decision === "denied") { d.status = "blocked"; d.block_reason = ev.reason; d.hold_until = null; }
    else {
      d.status = "sent"; d.hold_until = null;
      this.log(d.connection_id, d.agent_id, "email.send", "mail.send", "allowed", null, `send to ${d.to.length} recipient(s)`, d.to.length, d.origin_external);
    }
    d.updated_at = nowIso();
    this.emit("tool_call.hold_changed", { tool_call: { ...d } });
  }
  /** Freeze/lockdown or read-only: everything waiting in a hold goes back to needs-approval (nothing runs by inertia). */
  private returnHeldToApproval(reason: string) {
    for (const d of this.drafts) {
      if (d.status !== "held") continue;
      clearTimeout(this.timers.get(d.id)); this.timers.delete(d.id);
      d.status = "pending_approval"; d.hold_until = null; d.block_reason = reason; d.updated_at = nowIso();
      this.log(d.connection_id, d.agent_id, "email.send", "mail.send", "needs_approval", reason, "hold interrupted");
      this.emit("tool_call.hold_changed", { tool_call: { ...d } });
    }
  }

  // ---- REST ----------------------------------------------------------------------------------
  async handle(method: string, seg: string[], q: URLSearchParams, body: any): Promise<unknown> {
    const [r0, r1, r2, r3] = seg;

    if (r0 === "connection-providers") return { items: PROVIDERS };

    if (r0 === "dev") {
      if (r1 === "viewer" && method === "POST") {
        const h = this.controls.humans.find((x) => x.id === body?.id); if (h) this.controls.viewer = h;
        this.emitControls(); return this.snapshotControls();
      }
      if (r1 === "admins" && method === "POST") {
        const luis = this.controls.humans.find((h) => h.id === "u_luis");
        if (luis) luis.role = body?.count === 1 ? "member" : "admin";
        if (this.controls.viewer.id === "u_luis" && luis?.role === "member") this.controls.viewer = this.controls.humans[0];
        this.emitControls(); return this.snapshotControls();
      }
      if (r1 === "reset") { this.reset(); return {}; }
    }

    if (r0 === "org" && r1 === "operating-hours") {
      if (method === "PUT") {
        this.requireRole(["owner", "admin"]);
        this.hours = { enabled: !!body?.enabled, timezone: body?.timezone || "UTC", weekly: body?.weekly ?? [] };
      }
      return { ...this.hours, open_now: !this.hours.enabled, next_open_at: null };
    }
    if (r0 === "org" && r1 === "anomaly-settings") {
      if (method === "PUT") {
        this.requireRole(body?.disabled ? ["owner"] : ["owner", "admin"]);
        this.anomaly = { disabled: !!body?.disabled, auto_freeze: !!body?.auto_freeze };
      }
      return { ...this.anomaly, rules: ["spend_spike", "tool_request_burst", "rejected_approvals", "new_recipient_domains"] };
    }

    if (r0 === "org" && r1 === "controls") {
      if (!r2 && method === "GET") return this.snapshotControls();
      if (!r2 && method === "PUT") {
        if (body?.mode) {
          if (body.mode === "normal" && this.controls.mode === "read_only") this.requireRole(["owner"]);
          else this.requireRole(["owner", "admin"]);
          this.controls.mode = body.mode; this.controls.set_by = this.viewer().id; this.controls.set_at = nowIso();
          if (body.mode === "read_only") this.returnHeldToApproval("read_only_mode");
        }
        if (body?.settings) this.controls.settings = { ...this.controls.settings, ...body.settings };
        this.emitControls(); return this.snapshotControls();
      }
      if (r2 === "kill-switch" && method === "POST") {
        this.requireRole(["owner", "admin"]);
        const level = body?.level === "lockdown" ? "lockdown" : "freeze";
        this.controls.kill_switch_level = level; this.controls.reason = body?.reason ?? null; this.controls.set_by = this.viewer().id; this.controls.set_at = nowIso();
        this.returnHeldToApproval("kill_switch_active");
        if (level === "lockdown") for (const c of this.connections) if (c.status === "active") { c.status = "suspended"; c.status_reason = "kill_switch"; this.changed(c); }
        this.emitControls(); return this.snapshotControls();
      }
      if (r2 === "tools" && method === "POST") {
        // per-tool kill switch: switching a tool off is easy (admin), switching it back on is deliberate (owner)
        const tool = String(body?.tool ?? ""); if (!tool) fail(422, "unknown_capability");
        if (body?.disabled) { this.requireRole(["owner", "admin"]); if (!this.controls.disabled_tools.includes(tool)) this.controls.disabled_tools = [...this.controls.disabled_tools, tool]; this.returnHeldToApproval("tool_disabled"); }
        else { this.requireRole(["owner"]); this.controls.disabled_tools = this.controls.disabled_tools.filter((x) => x !== tool); }
        this.emitControls(); return this.snapshotControls();
      }
      if (r2 === "release" && method === "POST") {
        this.requireRole(["owner"]);
        if (!String(body?.reason ?? "").trim()) fail(422, "reason_required");
        const was = this.controls.kill_switch_level;
        this.controls.kill_switch_level = "none"; this.controls.reason = body.reason; this.controls.set_by = this.viewer().id; this.controls.set_at = nowIso();
        if (was === "lockdown" && body?.resume_connections === "all") {
          for (const c of this.connections) if (c.status === "suspended" && c.status_reason === "kill_switch") { c.status = "active"; c.status_reason = null; this.changed(c); }
        }
        this.emitControls(); return this.snapshotControls();
      }
    }

    if (r0 === "agent-controls" && method === "GET") return { items: Object.values(this.agentCtl).map((a) => ({ ...a })) };
    if (r0 === "agents" && r2 === "control" && method === "POST") {
      this.requireRole(["owner", "admin"]);
      const a = this.agentCtl[r1] ?? fail(404, "not_found");
      if (body?.action === "pause") { a.control = "paused"; a.reason = body.reason ?? null; a.paused_by = this.viewer().id; a.paused_at = nowIso(); }
      else { a.control = "active"; a.reason = null; a.paused_by = null; a.paused_at = null; }
      this.emit("agent.control_changed", { agent_id: r1, control: a.control, by: this.viewer().id }, r1);
      return { ...a };
    }
    if (r0 === "agents" && r2 === "connections" && method === "GET") {
      return { items: this.grants.filter((g) => g.agent_id === r1 && g.status !== "revoked").map((g) => ({ ...g })) };
    }
    if (r0 === "spend-limits" && r1 === "status") {
      const items: SpendLimitStatus[] = [
        { id: "sl_org", scope_type: "org", scope_id: null, period: "month", limit_usd: 50, used_usd: 12.4, on_hit: "pause_and_ask" },
        { id: "sl_asst", scope_type: "agent", scope_id: "assistant", period: "month", limit_usd: 10, used_usd: 3.1, on_hit: "pause_and_ask" },
        { id: "sl_cn", scope_type: "connection", scope_id: "cn_read", period: "month", limit_usd: 5, used_usd: 0.4, on_hit: "block" },
      ];
      return { items };
    }

    // ---- outbox / drafts / hold ----
    if (r0 === "outbox") {
      if (!r1 && method === "GET") return { items: this.drafts.map((d) => ({ ...d })) };
      const d = this.drafts.find((x) => x.id === r1) ?? fail(404, "not_found");
      if (method === "PATCH") {
        if (!["draft", "pending_approval", "blocked"].includes(d.status)) fail(409, "draft_locked");
        if (typeof body?.subject === "string") d.subject = body.subject;
        if (typeof body?.body === "string") d.body = body.body;
        if (Array.isArray(body?.to)) { d.to = body.to; d.new_recipient = d.to.some((x: string) => !x.endsWith("@acme.com")); }
        // editing changes the args_hash: any previous approval is void
        if (d.status !== "draft") d.status = "pending_approval";
        d.block_reason = null; d.version += 1; d.updated_at = nowIso();
        this.emit("tool_call.hold_changed", { tool_call: { ...d } });
        return { ...d };
      }
      if (r2 === "approve" && method === "POST") {
        if (!["draft", "pending_approval"].includes(d.status)) fail(409, "draft_locked");
        if (body?.version !== undefined && body.version !== d.version) fail(409, "draft_changed");
        if (!d.to.length || !d.subject.trim()) fail(422, "draft_incomplete");
        const ev = this.evaluate(d.connection_id, d.agent_id, "mail.send");
        if (ev.decision === "denied") {
          this.log(d.connection_id, d.agent_id, "email.send", "mail.send", "denied", ev.reason, `send to ${d.to.length} recipient(s)`);
          fail(403, ev.reason ?? "denied");
        }
        // the window is decided here, server side, and is never below 60 s
        d.status = "held"; d.hold_seconds = HOLD_SECONDS; d.hold_until = new Date(Date.now() + HOLD_SECONDS * 1000).toISOString(); d.block_reason = null; d.updated_at = nowIso();
        this.armHold(d);
        this.emit("tool_call.hold_changed", { tool_call: { ...d } });
        return { ...d };
      }
      if (r2 === "reject" && method === "POST") {
        if (!["draft", "pending_approval", "blocked"].includes(d.status)) fail(409, "draft_locked");
        d.status = "rejected"; d.updated_at = nowIso(); this.emit("tool_call.hold_changed", { tool_call: { ...d } }); return { ...d };
      }
    }
    if (r0 === "tool-calls" && r2 === "cancel-hold" && method === "POST") {
      const d = this.drafts.find((x) => x.id === r1) ?? fail(404, "not_found");
      if (d.status !== "held" || !d.hold_until) fail(409, "hold_over");
      if (new Date(d.hold_until!).getTime() <= Date.now()) fail(409, "hold_over");
      clearTimeout(this.timers.get(d.id)); this.timers.delete(d.id);
      d.status = "cancelled"; d.hold_until = null; d.updated_at = nowIso();
      this.log(d.connection_id, d.agent_id, "email.send", "mail.send", "allowed", null, "send cancelled by human", 0);
      this.usage[0].status = "skipped";
      this.emit("tool_call.hold_changed", { tool_call: { ...d } });
      return { ...d };
    }

    // ---- plan review ----
    if (r0 === "plan-reviews") {
      if (r1 === "simulate" && method === "POST") {
        const p = this.buildPlan(`pr_${++this.planSeq}`, String(body?.text || "Enviar la propuesta a un cliente nuevo"), ["analyst", "assistant"], "Preparar y enviar propuesta");
        this.plans.unshift(p); this.emit("plan.review_requested", { request_id: p.id, preflight: p }); return p;
      }
      if (method === "GET") return { items: this.plans.map((p) => this.recompute(p)) };
    }
    if (r0 === "requests" && r2 === "plan") {
      const p = this.plans.find((x) => x.id === r1) ?? fail(404, "not_found");
      if (p.status !== "plan_ready") fail(409, "plan_closed");
      if (method === "PATCH") {
        if (Array.isArray(body?.remove_task_ids)) {
          const rm: string[] = body.remove_task_ids;
          p.tasks = p.tasks.filter((t) => !rm.includes(t.id)).map((t) => ({ ...t, depends_on: t.depends_on.filter((d) => !rm.includes(d)) }));
        }
        if (typeof body?.note === "string") p.note = body.note;
        if (typeof body?.no_external_actions === "boolean") p.no_external_actions = body.no_external_actions;
        Object.assign(p, this.recompute(p)); return { ...p };
      }
      if (method === "POST" && r3 === "approve") {
        if (!p.tasks.length) fail(422, "plan_empty");
        p.status = "approved";
        this.emit("plan.approved", { request_id: p.id, preflight: { ...p } });
        // execution begins only now: the assistant drafts the email (when it can reach a write connection and external actions are allowed)
        const wc = p.reachable_connections.find((r) => r.capabilities.includes("mail.send") && r.agent_id === "assistant");
        if (wc && !p.no_external_actions) {
          const c = this.conn(wc.connection_id);
          const d: EmailDraft = {
            id: uid("tc_draft"), agent_id: "assistant", connection_id: c.id, account_label: c.account_label, to: ["nuevo.cliente@acme.com"],
            subject: "Propuesta para su equipo", body: "Hola,\n\nGracias por su interés. Les comparto una propuesta inicial.\n\nSaludos,\nSofía",
            status: "draft", block_reason: null, origin_external: true, new_recipient: true, reversibility: "none", hold_seconds: HOLD_SECONDS, hold_until: null,
            version: 1, created_at: nowIso(), updated_at: nowIso(),
          };
          this.drafts.unshift(d);
          this.log(c.id, "assistant", "email.draft", "mail.draft", "allowed", null, "draft created from approved plan", 1, true);
          this.emit("tool_call.hold_changed", { tool_call: { ...d } });
        }
        return { ...p };
      }
      if (method === "POST" && r3 === "reject") { p.status = "rejected"; return { ...p }; }
    }

    // ---- connections ----
    if (r0 === "connections") {
      if (!r1) {
        if (method === "GET") return { items: this.connections.map((c) => this.pub(c)) };
        if (method === "POST") return this.create(body);
      }
      if (r1 === "oauth" && r2 === "callback") {
        // simulated provider redirect: the UI passes the connection id; in real mode the browser is redirected by the backend.
        const c = this.conn(String(q.get("connection_id")));
        if (c.status !== "pending") fail(409, "not_pending");
        const requested = [...c.requested_capabilities];
        const dropped = q.get("deny_scopes") === "1" && requested.length > 1 ? requested.slice(-1) : [];
        c.granted_capabilities = requested.filter((x) => !dropped.includes(x));
        c.account_label = "ventas@empresa.com";
        c.credential = { kind: "oauth_refresh", hint: null, version: 1, expires_at: null, rotated_at: nowIso(), created_at: nowIso() };
        c.status = "active"; this.changed(c); return this.pub(c);
      }
      const c = this.conn(String(r1));
      if (!r2) {
        if (method === "GET") return { ...this.pub(c), grants: this.grants.filter((g) => g.connection_id === c.id && g.status !== "revoked") };
        if (method === "PATCH") {
          this.requireRole(["owner", "admin"]);
          if (typeof body?.label === "string") c.label = body.label;
          if (typeof body?.read_only === "boolean") c.read_only = body.read_only;
          if (body?.limits) c.limits = { ...c.limits, ...body.limits };
          this.changed(c); return this.pub(c);
        }
      }
      if (r2 === "oauth" && r3 === "start" && method === "POST") {
        if (c.kind !== "oauth2") fail(422, "not_oauth");
        return { auth_url: "https://accounts.example.test/oauth?state=mock", expires_at: new Date(Date.now() + 600000).toISOString() };
      }
      if (r2 === "credential" && method === "PUT") {
        this.requireRole(["owner", "admin"]);
        const secret = String(body?.secret ?? ""); if (secret.length < 8) fail(422, "invalid_secret_format");
        if (secret.toLowerCase().includes("invalid")) fail(422, "test_failed");
        c.credential = {
          kind: "api_key", hint: secret.length >= 20 ? secret.slice(-4) : null, version: (c.credential?.version ?? 0) + 1,
          expires_at: c.credential?.expires_at ?? null, rotated_at: nowIso(), created_at: c.credential?.created_at ?? nowIso(),
        };
        this.changed(c); return this.pub(c);
      }
      if (r2 === "test" && method === "POST") {
        this.requireRole(["owner", "admin"]);
        const ok = c.status === "active" || c.status === "pending" || c.status === "error";
        if (!ok) return { status: "failed", latency_ms: 0, error_code: "connection_unavailable", account_label: c.account_label, granted_capabilities: c.granted_capabilities };
        c.last_tested_at = nowIso(); this.changed(c);
        return { status: "ok", latency_ms: 120 + Math.floor(Math.random() * 90), account_label: c.account_label, granted_capabilities: c.granted_capabilities };
      }
      if (r2 === "suspend" && method === "POST") {
        this.requireRole(["owner", "admin"]);
        if (c.status === "active") { c.status = "suspended"; c.status_reason = "manual"; this.returnHeldToApproval("connection_unavailable"); this.changed(c); }
        return this.pub(c);
      }
      if (r2 === "resume" && method === "POST") {
        if (c.status_reason === "kill_switch") this.requireRole(["owner"]); else this.requireRole(["owner", "admin"]);
        if (this.controls.kill_switch_level === "lockdown") fail(409, "kill_switch_active");
        if (c.status === "suspended") { c.status = "active"; c.status_reason = null; this.changed(c); }
        return this.pub(c);
      }
      if (r2 === "revoke" && method === "POST") {
        this.requireRole(["owner", "admin"]);
        if (String(body?.confirm_name ?? "") !== c.label) fail(422, "confirm_name_mismatch");
        const affected = this.grants.filter((g) => g.connection_id === c.id && g.status !== "revoked");
        c.status = "revoked"; c.status_reason = "user_revoked"; c.revoked_at = nowIso(); c.credential = null; c.pending_provider_revocation = false;
        for (const g of affected) g.status = "revoked";
        for (const d of this.drafts) {
          if (d.connection_id === c.id && ["held", "pending_approval"].includes(d.status)) { clearTimeout(this.timers.get(d.id)); d.status = "blocked"; d.block_reason = "connection_revoked"; d.hold_until = null; }
        }
        this.emit("connection.revoked", { connection_id: c.id, pending_provider_revocation: false });
        this.changed(c); return this.pub(c);
      }
      if (r2 === "grants") return this.grantsRoute(method, c, r3, body);
      if (r2 === "usage") {
        if (r3 === "simulate" && method === "POST") {
          const agent = String(body?.agent_id), cap = String(body?.capability);
          const ev = this.evaluate(c.id, agent, cap);
          const tool = cap.startsWith("mail.") ? `email.${cap.split(".")[1]}` : cap;
          const denied = ev.decision === "denied";
          this.log(c.id, agent, tool, cap, ev.decision, ev.reason, denied ? null : "simulated use", denied ? null : 1, !isWriteCap(c.provider, cap));
          return { ...this.usage[0] };
        }
        if (method === "GET") {
          let rows = this.usage.filter((u) => u.connection_id === c.id);
          const ag = q.get("agent_id"), rs = q.get("result");
          if (ag) rows = rows.filter((u) => u.agent_id === ag);
          if (rs) rows = rows.filter((u) => u.decision === rs);
          return { items: rows };
        }
      }
    }
    return fail(404, "not_found");
  }

  private create(body: any): Connection {
    this.requireRole(["owner", "admin"]);
    const prov = PROVIDERS.find((p) => p.id === body?.provider) ?? fail(422, "unknown_provider");
    if (!prov.available) fail(422, "provider_unavailable");
    const label = String(body?.label || "").trim(); if (!label) fail(422, "label_required");
    const caps: string[] = Array.isArray(body?.capabilities) && body.capabilities.length ? body.capabilities : prov.capabilities.filter((c) => c.default).map((c) => c.id);
    for (const cap of caps) if (!capDef(prov.id, cap)) fail(422, "unknown_capability");
    // owner decision: Gmail read and write live in separate connections
    const profiles = new Set(caps.map((c) => capDef(prov.id, c)!.profile));
    if (prov.split_read_write && profiles.has("read") && profiles.has("write")) fail(422, "read_write_must_be_separate");
    const kind = body?.kind === "api_key" ? "api_key" : "oauth2";
    if (!prov.auth.includes(kind === "api_key" ? "api_key" : "oauth2_byo_app")) fail(422, "auth_method_unsupported");
    const writes = caps.some((x) => isWriteCap(prov.id, x));
    const c: Connection = {
      id: uid("cn"), provider: prov.id, kind, label, account_label: null, mode: "simulated", status: "pending", status_reason: null,
      profile: prov.split_read_write ? (profiles.has("write") ? "write" : "read") : undefined,
      requested_capabilities: caps, granted_capabilities: [], resource_scope: body?.resource_scope ?? {}, credential: null,
      limits: { per_day: 2000, write_per_day: writes ? 20 : 0, max_items_per_call: 50 }, read_only: false, grants_count: 0,
      last_tested_at: null, last_used_at: null, created_at: nowIso(), revoked_at: null,
    };
    if (kind === "oauth2") {
      // "Bring your own OAuth app": the client id is public metadata; the client secret is write-only and is dropped right here.
      const id = String(body?.oauth_client_id ?? "").trim(), sec = String(body?.oauth_client_secret ?? "");
      if (!id || sec.length < 8) fail(422, "oauth_app_required");
      c.oauth_client_id = id;
    } else {
      const secret = String(body?.secret ?? "");
      if (secret.length < 8) fail(422, "invalid_secret_format");
      if (secret.toLowerCase().includes("invalid")) { c.status = "error"; c.status_reason = "test_failed"; }
      else {
        c.status = "active"; c.granted_capabilities = [...caps];
        c.credential = { kind: "api_key", hint: secret.length >= 20 ? secret.slice(-4) : null, version: 1, expires_at: body?.expires_at ?? null, rotated_at: nowIso(), created_at: nowIso() };
        c.account_label = `svc-${prov.id}`;
      }
    }
    this.connections.unshift(c);
    this.emit("connection.created", { connection: this.pub(c) });
    return this.pub(c);
  }

  private grantsRoute(method: string, c: Connection, agentId: string | undefined, body: any): unknown {
    if (!agentId && method === "GET") return { items: this.grants.filter((g) => g.connection_id === c.id && g.status !== "revoked") };
    if (!agentId) return fail(404, "not_found");
    const agent = agentId;
    const cur = this.grants.find((g) => g.connection_id === c.id && g.agent_id === agent && g.status !== "revoked");
    if (method === "DELETE") {
      this.requireRole(["owner", "admin"]);
      if (cur) { cur.status = "revoked"; this.emit("connection.grant_changed", { connection_id: c.id, agent_id: agent, before: cur.capabilities, after: [], by: this.viewer().id }); }
      return {};
    }
    if (method === "POST" && body?.approve) {
      // second human approval (maker-checker)
      this.requireRole(["owner", "admin"]);
      if (!cur || cur.status !== "pending_second_approval") fail(409, "not_pending");
      if (cur!.requested_by === this.viewer().id) fail(403, "second_human_required");
      cur!.status = "active"; cur!.approved_by = this.viewer().id;
      this.emit("connection.grant_changed", { connection_id: c.id, agent_id: agent, before: [], after: cur!.capabilities, by: this.viewer().id });
      return { ...cur };
    }
    if (method === "PUT") {
      const caps: string[] = Array.isArray(body?.capabilities) ? body.capabilities : [];
      // owner decision: agents never receive "approve" (nor any controls/connections permission)
      if (caps.some((x) => /^(approve|approvals|controls|connections)/.test(x))) fail(422, "agents_cannot_approve");
      for (const cap of caps) if (!c.granted_capabilities.includes(cap)) fail(422, "capability_not_granted_by_connection");
      const writes = caps.some((x) => isWriteCap(c.provider, x));
      this.requireRole(writes ? ["owner"] : ["owner", "admin"]);
      if (writes && body?.confirm_name !== c.label) fail(422, "confirm_name_mismatch");
      const before = cur?.capabilities ?? [];
      const adminCount = this.controls.humans.filter((h) => h.role === "admin" || h.role === "owner").length;
      const addsWrites = caps.some((x) => isWriteCap(c.provider, x) && !before.includes(x));
      // owner decision: a second human is required only if the org has more than one admin
      const needsSecond = addsWrites && adminCount > 1;
      const g: Grant = {
        id: cur?.id ?? uid("gr"), connection_id: c.id, agent_id: agent, capabilities: caps, resource_scope: body?.resource_scope ?? cur?.resource_scope ?? {},
        constraints: body?.constraints ?? {}, max_risk: caps.some((x) => capDef(c.provider, x)?.risk === "high") ? "high" : writes ? "medium" : "low",
        valid_until: body?.valid_until ?? null, status: needsSecond ? "pending_second_approval" : "active",
        granted_by: this.viewer().id, requested_by: this.viewer().id, approved_by: null, created_at: cur?.created_at ?? nowIso(),
      };
      if (cur) Object.assign(cur, g); else this.grants.push(g);
      this.emit("connection.grant_changed", { connection_id: c.id, agent_id: agent, before, after: g.status === "active" ? caps : before, by: this.viewer().id });
      return { ...g };
    }
    return fail(405, "method_not_allowed");
  }
}
