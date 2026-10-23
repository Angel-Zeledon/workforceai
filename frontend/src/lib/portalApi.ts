import { API_URL, MOCK } from "./config";
import { authFetch } from "./session";
import type { PublicView } from "./portalLogic";

/** Contract: backend/internal/api/portal.go and docs/architecture/client-portal.md (additive, /api/v1). */

export interface PortalBranding { display_name: string; accent_color: string; has_logo: boolean; updated_by?: string; updated_at?: string }
export interface PortalLink {
  id: string; project_id: string;
  /** internal note ("which client"); never shown on the public page */
  label: string;
  /** what the client reads as the project title ("" = the project name) */
  title: string;
  deliverable_ids: string[];
  created_by: string; created_at: string; updated_at: string;
  expires_at: string | null; revoked_at: string | null; revoked_by?: string;
  first_viewed_at: string | null; last_viewed_at: string | null;
  status: "active" | "expired" | "revoked";
}
/** Returned once, at creation: `token` is the only copy of the secret. */
export interface CreatedPortalLink extends PortalLink { token: string }
export interface PortalCandidate { id: string; title: string; kind: string; status: string; completed: boolean; updated_at: string }
export interface PortalLinkInput { label: string; title: string; expires_in_days?: number; deliverable_ids: string[] }

/** The public page could not load the link (unknown, expired or revoked: the server does not say which). */
export class PortalUnavailable extends Error {}

async function http<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await authFetch(`${API_URL}${path}`, {
    method, cache: "no-store",
    headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) {
    let detail = "";
    try { detail = String(JSON.parse(await res.text())?.error ?? ""); } catch { /* not JSON */ }
    throw Object.assign(new Error(`${method} ${path} -> ${res.status}`), { status: res.status, detail });
  }
  const text = await res.text();
  return (text ? JSON.parse(text) : {}) as T;
}

const mock = async () => (await import("./mock/portal-mock")).portalMock;
const enc = encodeURIComponent;

export const portalApi = {
  branding: async (): Promise<PortalBranding> => (MOCK ? (await mock()).branding() : http("GET", "/portal/branding")),
  setBranding: async (b: { display_name: string; accent_color: string }): Promise<PortalBranding> =>
    (MOCK ? (await mock()).setBranding(b) : http("PUT", "/portal/branding", b)),
  /** Raw image body; the server decodes and re-encodes it (PNG/JPEG/GIF in, at most 512 px out). */
  uploadLogo: async (file: Blob): Promise<PortalBranding> => {
    if (MOCK) return (await mock()).uploadLogo(file);
    const res = await authFetch(`${API_URL}/portal/branding/logo`, { method: "PUT", cache: "no-store", headers: { "Content-Type": file.type || "application/octet-stream" }, body: file });
    if (!res.ok) throw Object.assign(new Error(`PUT logo -> ${res.status}`), { status: res.status });
    return res.json();
  },
  deleteLogo: async (): Promise<PortalBranding> => (MOCK ? (await mock()).deleteLogo() : http("DELETE", "/portal/branding/logo")),
  /** The current logo as a Blob (for an object URL), or null. */
  logo: async (): Promise<Blob | null> => {
    if (MOCK) return (await mock()).logo();
    const res = await authFetch(`${API_URL}/portal/branding/logo`, { cache: "no-store" });
    return res.ok ? res.blob() : null;
  },
  links: async (pid: string): Promise<PortalLink[]> => (MOCK ? (await mock()).links(pid) : http("GET", `/projects/${enc(pid)}/portal-links`)),
  candidates: async (pid: string): Promise<PortalCandidate[]> => (MOCK ? (await mock()).candidates(pid) : http("GET", `/projects/${enc(pid)}/portal-links/candidates`)),
  createLink: async (pid: string, b: PortalLinkInput): Promise<CreatedPortalLink> => (MOCK ? (await mock()).createLink(pid, b) : http("POST", `/projects/${enc(pid)}/portal-links`, b)),
  updateLink: async (pid: string, id: string, b: PortalLinkInput): Promise<PortalLink> =>
    (MOCK ? (await mock()).updateLink(pid, id, b) : http("PUT", `/projects/${enc(pid)}/portal-links/${enc(id)}`, b)),
  revokeLink: async (pid: string, id: string): Promise<PortalLink> => (MOCK ? (await mock()).revokeLink(pid, id) : http("DELETE", `/projects/${enc(pid)}/portal-links/${enc(id)}`)),
};

/** The public page: no session, no cookies, no referrer; the token goes in the body, never in a URL. */
async function publicPost(path: string, token: string): Promise<Response> {
  return fetch(`${API_URL}${path}`, {
    method: "POST", cache: "no-store", credentials: "omit", referrerPolicy: "no-referrer",
    headers: { "Content-Type": "application/json" }, body: JSON.stringify({ token }),
  });
}

export const portalPublic = {
  view: async (token: string): Promise<PublicView> => {
    if (MOCK) return (await mock()).publicView(token);
    const res = await publicPost("/public/portal/view", token);
    if (res.status === 404) throw new PortalUnavailable();
    if (!res.ok) throw new Error(`portal view -> ${res.status}`);
    return res.json();
  },
  logo: async (token: string): Promise<Blob | null> => {
    if (MOCK) return (await mock()).publicLogo(token);
    const res = await publicPost("/public/portal/logo", token);
    return res.ok ? res.blob() : null;
  },
};
