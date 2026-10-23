/**
 * Mock of the client portal (NEXT_PUBLIC_MOCK=true). It keeps links and branding in localStorage, because the
 * public page is opened in another tab where the in-memory projects mock starts from scratch: at creation (and
 * every time the links are listed) the allowlisted view (toPublicView, the mirror of the backend allowlist) is
 * stored by token, and the public page reads that snapshot. The token "demo" shows a sample project.
 * Deliverables: the demo has no approved artifacts, so finished task nodes stand in for them.
 */
import { tr } from "../i18n-core";
import { projectsMock } from "../projects/mock";
import type { ProjectDetail } from "../projects/types";
import type { CreatedPortalLink, PortalBranding, PortalCandidate, PortalLink, PortalLinkInput } from "../portalApi";
import { PortalUnavailable } from "../portalApi";
import { linkStatus, toPublicView, type PublicView } from "../portalLogic";

const KEY = "aiw.mock.portal.v1";

interface Saved {
  branding: PortalBranding & { logo_b64?: string; logo_mime?: string };
  links: Record<string, PortalLink[]>; // project id -> links
  tokens: Record<string, string>; // link id -> token (mock only: the real server never keeps it)
  snapshots: Record<string, PublicView>; // token -> view
}

const empty = (): Saved => ({ branding: { display_name: "", accent_color: "", has_logo: false }, links: {}, tokens: {}, snapshots: {} });

function load(): Saved {
  try { return { ...empty(), ...(JSON.parse(localStorage.getItem(KEY) || "null") ?? {}) }; } catch { return empty(); }
}
function save(s: Saved) {
  try { localStorage.setItem(KEY, JSON.stringify(s)); } catch { /* quota or private mode: the demo just forgets */ }
}

function randomToken(): string {
  const b = new Uint8Array(32);
  crypto.getRandomValues(b);
  return btoa(String.fromCharCode(...b)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}
const rid = () => `pl_${Math.random().toString(16).slice(2, 10)}`;

function publicBranding(s: Saved) {
  const b = s.branding;
  return { firmName: b.display_name || tr("portal.demo.firm"), accent: b.accent_color, hasLogo: !!b.has_logo };
}

function candidatesOf(d: ProjectDetail): PortalCandidate[] {
  return d.nodes.filter((n) => (n.kind === "task" || n.kind === "subtask") && n.state === "done")
    .map((n) => ({ id: `del-${n.id}`, title: n.title_key ? tr(n.title_key, n.title_params) : n.title, kind: "doc", status: "approved", completed: true, updated_at: n.finished_at ?? new Date().toISOString() }));
}

async function snapshot(s: Saved, l: PortalLink) {
  const token = s.tokens[l.id];
  if (!token) return;
  if (l.revoked_at) { delete s.snapshots[token]; return; }
  try {
    const d = await projectsMock.get(l.project_id);
    const shared = candidatesOf(d).filter((c) => l.deliverable_ids.includes(c.id));
    s.snapshots[token] = toPublicView(d, { ...publicBranding(s), title: l.title, deliverables: shared, expiresAt: l.expires_at });
  } catch { /* the project only lives in the tab that created it: keep the last snapshot */ }
}

/** A sample project in progress, built from the demo project with simulated progress. */
async function demoView(s: Saved): Promise<PublicView> {
  const list = await projectsMock.list();
  if (!list.length) throw new PortalUnavailable();
  const d = await projectsMock.get(list[list.length - 1].id);
  const leaves = d.nodes.filter((n) => n.kind === "task" || n.kind === "subtask" || n.kind === "milestone");
  const now = Date.now();
  leaves.forEach((n, i) => {
    const f = i / Math.max(1, leaves.length);
    n.state = f < 0.45 ? "done" : f < 0.6 ? "running" : "pending";
    n.started_at = f < 0.6 ? new Date(now - (leaves.length - i) * 3_600_000).toISOString() : null;
    n.finished_at = f < 0.45 ? new Date(now - (leaves.length - i) * 3_000_000).toISOString() : null;
  });
  d.project.status = "running";
  const shared = candidatesOf(d).slice(0, 2);
  return toPublicView(d, { ...publicBranding(s), deliverables: shared, expiresAt: new Date(now + 30 * 86_400_000).toISOString() });
}

function b64Blob(b64: string, mime: string): Blob {
  const bin = atob(b64);
  const u = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) u[i] = bin.charCodeAt(i);
  return new Blob([u], { type: mime });
}

const view = (l: PortalLink): PortalLink => ({ ...l, status: linkStatus(l) });

export const portalMock = {
  async branding(): Promise<PortalBranding> {
    const { logo_b64: _l, logo_mime: _m, ...b } = load().branding;
    return b;
  },
  async setBranding(b: { display_name: string; accent_color: string }): Promise<PortalBranding> {
    const s = load();
    if (b.accent_color && !/^#[0-9a-f]{6}$/i.test(b.accent_color)) throw Object.assign(new Error("invalid color"), { status: 400 });
    s.branding = { ...s.branding, display_name: b.display_name.trim().slice(0, 80), accent_color: b.accent_color.toLowerCase(), updated_at: new Date().toISOString() };
    save(s);
    return this.branding();
  },
  async uploadLogo(file: Blob): Promise<PortalBranding> {
    if (!/^image\/(png|jpeg|gif)$/.test(file.type) || file.size > 2 << 20) throw Object.assign(new Error("invalid image"), { status: 400 });
    if (file.size > 200_000) throw Object.assign(new Error("too large for the demo"), { status: 413 }); // localStorage is small
    const buf = new Uint8Array(await file.arrayBuffer());
    let bin = "";
    for (let i = 0; i < buf.length; i++) bin += String.fromCharCode(buf[i]);
    const s = load();
    s.branding = { ...s.branding, has_logo: true, logo_b64: btoa(bin), logo_mime: file.type, updated_at: new Date().toISOString() };
    save(s);
    return this.branding();
  },
  async deleteLogo(): Promise<PortalBranding> {
    const s = load();
    s.branding = { ...s.branding, has_logo: false, logo_b64: undefined, logo_mime: undefined };
    save(s);
    return this.branding();
  },
  async logo(): Promise<Blob | null> {
    const b = load().branding;
    return b.logo_b64 && b.logo_mime ? b64Blob(b.logo_b64, b.logo_mime) : null;
  },
  async links(pid: string): Promise<PortalLink[]> {
    const s = load();
    const ls = s.links[pid] ?? [];
    for (const l of ls) await snapshot(s, l); // keep the client's page in step with the demo's progress
    save(s);
    return ls.map(view);
  },
  async candidates(pid: string): Promise<PortalCandidate[]> {
    return candidatesOf(await projectsMock.get(pid));
  },
  async createLink(pid: string, b: PortalLinkInput): Promise<CreatedPortalLink> {
    const s = load();
    const now = new Date();
    const token = randomToken();
    const l: PortalLink = {
      id: rid(), project_id: pid, label: b.label.trim().slice(0, 80), title: b.title.trim().slice(0, 120), deliverable_ids: [...new Set(b.deliverable_ids)],
      created_by: "demo", created_at: now.toISOString(), updated_at: now.toISOString(),
      expires_at: b.expires_in_days ? new Date(now.getTime() + b.expires_in_days * 86_400_000).toISOString() : null,
      revoked_at: null, first_viewed_at: null, last_viewed_at: null, status: "active",
    };
    s.links[pid] = [l, ...(s.links[pid] ?? [])];
    s.tokens[l.id] = token;
    await snapshot(s, l);
    save(s);
    return { ...view(l), token };
  },
  async updateLink(pid: string, id: string, b: PortalLinkInput): Promise<PortalLink> {
    const s = load();
    const l = (s.links[pid] ?? []).find((x) => x.id === id);
    if (!l) throw Object.assign(new Error("not found"), { status: 404 });
    Object.assign(l, { label: b.label.trim().slice(0, 80), title: b.title.trim().slice(0, 120), deliverable_ids: [...new Set(b.deliverable_ids)], updated_at: new Date().toISOString() });
    await snapshot(s, l);
    save(s);
    return view(l);
  },
  async revokeLink(pid: string, id: string): Promise<PortalLink> {
    const s = load();
    const l = (s.links[pid] ?? []).find((x) => x.id === id);
    if (!l) throw Object.assign(new Error("not found"), { status: 404 });
    if (!l.revoked_at) { l.revoked_at = new Date().toISOString(); l.revoked_by = "demo"; }
    await snapshot(s, l);
    save(s);
    return view(l);
  },
  async publicView(token: string): Promise<PublicView> {
    const s = load();
    if (token === "demo") return demoView(s);
    const v = s.snapshots[token];
    const linkId = Object.keys(s.tokens).find((k) => s.tokens[k] === token);
    const link = linkId ? Object.values(s.links).flat().find((x) => x.id === linkId) : undefined;
    if (!v || !link || linkStatus(link) !== "active") throw new PortalUnavailable();
    if (!link.first_viewed_at) link.first_viewed_at = new Date().toISOString();
    link.last_viewed_at = new Date().toISOString();
    save(s);
    return { ...v, firm: { name: publicBranding(s).firmName, accent_color: s.branding.accent_color, has_logo: !!s.branding.has_logo }, generated_at: new Date().toISOString() };
  },
  async publicLogo(token: string): Promise<Blob | null> {
    try { await this.publicView(token); } catch { return null; }
    return this.logo();
  },
};
