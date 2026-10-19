import { API_URL, MOCK } from "./config";
import { authFetch } from "./session";

/**
 * Enterprise identity (SSO, verified domains, sign-in policy, SCIM). Contract: backend/internal/sso and backend/internal/scim,
 * design in docs/architecture/identity.md. No response ever contains the OIDC client secret or a stored SCIM token.
 */
export type IdentityRole = "admin" | "member" | "viewer";

export interface SsoView {
  configured: boolean; enabled: boolean; issuer: string; client_id: string; has_secret: boolean;
  default_role: IdentityRole; jit: boolean; allow_unverified_email: boolean;
  redirect_url: string; encryption_available: boolean; updated_at?: string;
}
export interface SsoInput {
  enabled: boolean; issuer: string; client_id: string; client_secret?: string;
  default_role: IdentityRole; jit: boolean; allow_unverified_email: boolean;
}
export interface DomainRecord { name: string; type: string; value: string }
export interface DomainView { domain: string; verified: boolean; created_at: string; verified_at?: string; last_check_at?: string; challenge: DomainRecord }
export interface IdentityPolicy { require_mfa: boolean; enforce_sso: boolean }
export interface ScimView { token: { exists: boolean; created_at?: string; last_used_at?: string }; base_url: string }

/** An error that keeps the server's stable code (and the per-field messages of a validation error). */
export class IdentityError extends Error {
  constructor(public status: number, public code: string, public fields?: Record<string, string>) { super(code); }
}

async function parse<T>(res: Response): Promise<T> {
  const text = await res.text();
  let json: { error?: { code?: string; fields?: Record<string, string> } } & Record<string, unknown> = {};
  try { json = text ? JSON.parse(text) : {}; } catch { /* not JSON */ }
  if (!res.ok) throw new IdentityError(res.status, json.error?.code ?? "error", json.error?.fields);
  return json as T;
}

async function admin<T>(method: string, path: string, body?: unknown): Promise<T> {
  if (MOCK) throw new IdentityError(404, "mock");
  const res = await authFetch(`${API_URL}/identity${path}`, {
    method, cache: "no-store",
    headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  return parse<T>(res);
}

export const identityApi = {
  sso: () => admin<SsoView>("GET", "/sso"),
  saveSso: (b: SsoInput) => admin<SsoView>("PUT", "/sso", b),
  removeSso: () => admin<void>("DELETE", "/sso"),
  testSso: () => admin<{ ok: boolean }>("POST", "/sso/test"),
  domains: () => admin<{ domains: DomainView[] }>("GET", "/domains").then((r) => r.domains),
  addDomain: (domain: string) => admin<DomainView>("POST", "/domains", { domain }),
  verifyDomain: (domain: string) => admin<DomainView>("POST", `/domains/${encodeURIComponent(domain)}/verify`),
  removeDomain: (domain: string) => admin<void>("DELETE", `/domains/${encodeURIComponent(domain)}`),
  policy: () => admin<IdentityPolicy>("GET", "/policy"),
  savePolicy: (p: IdentityPolicy) => admin<IdentityPolicy>("PUT", "/policy", p),
  scim: () => admin<ScimView>("GET", "/scim"),
  /** Mints (or replaces) the SCIM token. The token is in this answer only. */
  rotateScim: () => admin<{ token: string; base_url: string }>("POST", "/scim/token"),
  revokeScim: () => admin<void>("DELETE", "/scim/token"),
};

// ---- sign-in with SSO (public, before there is a session) ----

async function pub<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(`${API_URL}${path}`, { method: "POST", cache: "no-store", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
  return parse<T>(res);
}

/** Does this address belong to a domain with single sign-on? Answers only yes or no. */
export async function ssoAvailable(email: string): Promise<boolean> {
  if (MOCK || !/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(email)) return false;
  try { return (await pub<{ sso: boolean }>("/sso/discover", { email })).sso === true; } catch { return false; }
}

/** The identity provider's authorization URL; the browser navigates to it. */
export const ssoStart = (email: string) => pub<{ url: string }>("/sso/start", { email }).then((r) => r.url);

/** Well-known issuers, shown as examples under the issuer field. */
export const ISSUER_EXAMPLES: { id: string; issuer: string }[] = [
  { id: "okta", issuer: "https://YOUR-ORG.okta.com" },
  { id: "entra", issuer: "https://login.microsoftonline.com/TENANT-ID/v2.0" },
  { id: "google", issuer: "https://accounts.google.com" },
];
