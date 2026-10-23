/**
 * Pure helpers of the client portal (B13, docs/architecture/client-portal.md). No React, no network.
 * The real public view is built by the backend (backend/internal/portal/public.go); `toPublicView` mirrors
 * that allowlist for the mock mode only, so the demo can never show more than the server would.
 */
import type { ProjectDetail } from "./projects/types";

export type PublicTaskStatus = "pending" | "in_progress" | "done";
export type PublicProjectStatus = "planning" | "in_progress" | "on_hold" | "completed" | "closed";

export interface PublicTask { title: string; title_key?: string; title_params?: Record<string, string>; status: PublicTaskStatus }
export interface PublicMilestone extends PublicTask { progress_pct: number; tasks: PublicTask[] }
export interface PublicDeliverable { title: string; kind: string; completed_at: string }
export interface PublicView {
  firm: { name: string; accent_color: string; has_logo: boolean };
  project: { title: string; title_key?: string; title_params?: Record<string, string>; status: PublicProjectStatus; progress_pct: number; tasks_done: number; tasks_total: number };
  milestones: PublicMilestone[];
  deliverables: PublicDeliverable[];
  updated_at: string | null;
  generated_at: string;
  expires_at: string | null;
}

/** Every key the public view may contain (the same list as backend/internal/portal/public_test.go). */
export const PUBLIC_KEYS = [
  "firm", "name", "accent_color", "has_logo",
  "project", "title", "title_key", "title_params", "status", "progress_pct", "tasks_done", "tasks_total",
  "milestones", "tasks",
  "deliverables", "kind", "completed_at",
  "updated_at", "generated_at", "expires_at",
] as const;

/** Keys of a JSON value that are not in PUBLIC_KEYS (title_params values are free text, their keys are not checked). */
export function unexpectedKeys(v: unknown, out: string[] = []): string[] {
  if (Array.isArray(v)) { v.forEach((x) => unexpectedKeys(x, out)); return out; }
  if (v && typeof v === "object") {
    for (const [k, val] of Object.entries(v as Record<string, unknown>)) {
      if (!(PUBLIC_KEYS as readonly string[]).includes(k)) out.push(k);
      if (k !== "title_params") unexpectedKeys(val, out);
    }
  }
  return out;
}

export function publicTaskStatus(state: string): PublicTaskStatus {
  if (state === "done" || state === "skipped") return "done";
  if (state === "running" || state === "awaiting_approval") return "in_progress";
  return "pending";
}

export function publicProjectStatus(status: string): PublicProjectStatus {
  switch (status) {
    case "done": return "completed";
    case "cancelled": return "closed";
    case "paused": case "failed": return "on_hold";
    case "running": case "waiting_human": return "in_progress";
    default: return "planning";
  }
}

const VISIBLE_KINDS = new Set(["task", "subtask", "milestone"]);
const pct = (done: number, total: number) => (total ? Math.floor((done * 100) / total) : 0);
const later = (a: string | null, b?: string | null) => (!b ? a : !a || Date.parse(b) > Date.parse(a) ? b : a);
const params = (p?: Record<string, string>) => (p && Object.keys(p).length ? { ...p } : undefined);

export interface SharedDeliverable { title: string; kind: string; updated_at: string }

/** The allowlisted public view of a project (mock mode). Copies field by field; never spreads an internal object. */
export function toPublicView(d: ProjectDetail, opts: { firmName: string; accent: string; hasLogo: boolean; title?: string; deliverables?: SharedDeliverable[]; expiresAt?: string | null; now?: Date }): PublicView {
  const p = d.project;
  const v: PublicView = {
    firm: { name: opts.firmName, accent_color: opts.accent, has_logo: opts.hasLogo },
    project: { title: opts.title || p.name, ...(opts.title ? {} : p.name_key ? { title_key: p.name_key } : {}), status: publicProjectStatus(p.status), progress_pct: 0, tasks_done: 0, tasks_total: 0 },
    milestones: [], deliverables: [], updated_at: null,
    generated_at: (opts.now ?? new Date()).toISOString(), expires_at: opts.expiresAt ?? null,
  };
  v.updated_at = later(later(later(v.updated_at, p.created_at), p.started_at), p.finished_at);
  const objs = [...d.objectives].sort((a, b) => a.position - b.position);
  const idx = new Map<string, number>();
  for (const o of objs) {
    idx.set(o.id, v.milestones.length);
    v.milestones.push({ title: o.title, ...(o.title_key ? { title_key: o.title_key } : {}), ...(params(o.title_params) ? { title_params: params(o.title_params) } : {}), status: "pending", progress_pct: 0, tasks: [] });
  }
  let other = -1, done = 0, total = 0;
  for (const n of d.nodes) {
    if (!VISIBLE_KINDS.has(n.kind) || n.superseded || n.state === "cancelled") continue;
    let i = idx.get(n.objective_id);
    if (i === undefined) {
      if (other < 0) { other = v.milestones.length; v.milestones.push({ title: "", status: "pending", progress_pct: 0, tasks: [] }); }
      i = other;
    }
    const st = publicTaskStatus(n.state);
    v.milestones[i].tasks.push({ title: n.title, ...(n.title_key ? { title_key: n.title_key } : {}), ...(params(n.title_params) ? { title_params: params(n.title_params) } : {}), status: st });
    total++; if (st === "done") done++;
    v.updated_at = later(later(v.updated_at, n.started_at), n.finished_at);
  }
  v.milestones = v.milestones.filter((m) => m.tasks.length > 0).map((m) => {
    const md = m.tasks.filter((t) => t.status === "done").length;
    const busy = m.tasks.some((t) => t.status === "in_progress");
    return { ...m, progress_pct: pct(md, m.tasks.length), status: md === m.tasks.length ? "done" : md > 0 || busy ? "in_progress" : "pending" };
  });
  v.project.tasks_done = done; v.project.tasks_total = total; v.project.progress_pct = v.project.status === "completed" && total ? 100 : pct(done, total);
  for (const x of [...(opts.deliverables ?? [])].sort((a, b) => Date.parse(b.updated_at) - Date.parse(a.updated_at))) {
    v.deliverables.push({ title: x.title, kind: x.kind, completed_at: x.updated_at });
    v.updated_at = later(v.updated_at, x.updated_at);
  }
  return v;
}

/** The token lives in the URL fragment (#...), which browsers never send to a server or in a Referer. */
export function tokenFromHash(hash: string): string {
  const raw = hash.replace(/^#/, "").trim();
  const t = raw.startsWith("t=") ? raw.slice(2) : raw;
  return /^[A-Za-z0-9_-]{1,64}$/.test(t) ? t : "";
}

/** Absolute URL of the public page for a token. */
export function portalUrl(origin: string, token: string): string {
  return `${origin.replace(/\/$/, "")}/portal#${token}`;
}

const HEX = /^#[0-9a-f]{6}$/i;
export const DEFAULT_ACCENT = "#0b1f3a";

/** A safe CSS color: only #rrggbb ever reaches a style attribute. */
export function safeAccent(c: string | null | undefined): string {
  return c && HEX.test(c) ? c.toLowerCase() : DEFAULT_ACCENT;
}

/** Black or white text on the accent, whichever has the higher WCAG contrast. */
export function textOn(accent: string): "#000000" | "#ffffff" {
  const h = safeAccent(accent);
  const ch = [1, 3, 5].map((i) => parseInt(h.slice(i, i + 2), 16) / 255).map((c) => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4));
  const lum = 0.2126 * ch[0] + 0.7152 * ch[1] + 0.0722 * ch[2];
  return (lum + 0.05) / 0.05 > 1.05 / (lum + 0.05) ? "#000000" : "#ffffff";
}

/** "active" | "expired" | "revoked" of a link at now (the server also sends it; this keeps the UI fresh). */
export function linkStatus(l: { revoked_at: string | null; expires_at: string | null }, now = Date.now()): "active" | "expired" | "revoked" {
  if (l.revoked_at) return "revoked";
  if (l.expires_at && Date.parse(l.expires_at) <= now) return "expired";
  return "active";
}
