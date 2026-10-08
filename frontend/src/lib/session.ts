/**
 * Session handling for AUTH_ENABLED backends.
 *
 * - GET /auth/config decides the mode: disabled (open demo, the app behaves exactly as before) or login.
 * - The access token lives only in memory; every REST call (authFetch) and the WebSocket (?access_token=) use it.
 * - The refresh token follows the current backend contract (returned in the JSON body, sent back in the body
 *   of /auth/refresh). It is kept in localStorage so a reload keeps the session. Moving it to an HttpOnly cookie
 *   would change the /auth/refresh contract and is pending an owner decision (docs/plans/part-a-foundation.md, A2).
 * - Refresh is single-flight and happens shortly before expiry or after a 401.
 */
import { create } from "zustand";
import { API_URL, MOCK } from "./config";

export type SessionStatus = "loading" | "disabled" | "anonymous" | "authenticated";
export interface SessionUser { id: string; email: string; name: string }
export interface OrgRef { org_id: string; role: string }

interface SessionPayload {
  access_token: string; refresh_token: string; expires_at: string;
  user: SessionUser; org_id: string; role: string;
}

interface SessionState {
  status: SessionStatus;
  user: SessionUser | null;
  orgId: string | null;
  role: string | null;
  permissions: string[];
  orgs: OrgRef[];
}

export const useSession = create<SessionState>(() => ({
  status: "loading", user: null, orgId: null, role: null, permissions: [], orgs: [],
}));

/** An auth endpoint error: HTTP status plus the backend error code (e.g. invalid_credentials). */
export class AuthError extends Error {
  constructor(public status: number, public code: string, public fields?: Record<string, string>) {
    super(code);
  }
}

const REFRESH_KEY = "aiwos.refresh";
let access = "";
let accessExp = 0;
let refreshing: Promise<boolean> | null = null;

const store = {
  get: () => { try { return localStorage.getItem(REFRESH_KEY) ?? ""; } catch { return ""; } },
  set: (v: string) => { try { if (v) localStorage.setItem(REFRESH_KEY, v); else localStorage.removeItem(REFRESH_KEY); } catch { /* private mode */ } },
};

async function post<T>(path: string, body: unknown, token?: string): Promise<T> {
  const res = await fetch(`${API_URL}${path}`, {
    method: "POST", cache: "no-store",
    headers: { "Content-Type": "application/json", ...(token ? { Authorization: `Bearer ${token}` } : {}) },
    body: JSON.stringify(body),
  });
  const text = await res.text();
  const data = text ? JSON.parse(text) : {};
  if (!res.ok) throw new AuthError(res.status, data?.error?.code ?? "error", data?.error?.fields);
  return data as T;
}

function adopt(s: SessionPayload) {
  access = s.access_token;
  accessExp = Date.parse(s.expires_at) || Date.now() + 10 * 60_000;
  store.set(s.refresh_token);
  useSession.setState({ user: s.user, orgId: s.org_id, role: s.role });
}

function clear(status: SessionStatus = "anonymous") {
  access = ""; accessExp = 0; store.set("");
  useSession.setState({ status, user: null, orgId: null, role: null, permissions: [], orgs: [] });
}

async function loadMe() {
  const res = await fetch(`${API_URL}/auth/me`, { cache: "no-store", headers: { Authorization: `Bearer ${access}` } });
  if (!res.ok) throw new AuthError(res.status, "me_failed");
  const me = await res.json();
  useSession.setState({
    status: "authenticated", user: me.user, orgId: me.org_id, role: me.role,
    permissions: Array.isArray(me.permissions) ? me.permissions : [], orgs: Array.isArray(me.orgs) ? me.orgs : [],
  });
}

/** Exchanges the stored refresh token for a new pair (single-flight). false when there is no valid session. */
export function refresh(): Promise<boolean> {
  if (refreshing) return refreshing;
  const token = store.get();
  if (!token) return Promise.resolve(false);
  refreshing = post<SessionPayload>("/auth/refresh", { refresh_token: token })
    .then((s) => { adopt(s); return true; })
    .catch((e) => {
      // Only a rejected token ends the session; a network blip keeps it for the next try.
      if (e instanceof AuthError && e.status === 401) clear();
      return false;
    })
    .finally(() => { refreshing = null; });
  return refreshing;
}

/** Decides the mode once at startup: open demo, or login (restoring a stored session when possible). */
export async function bootstrapAuth(): Promise<SessionStatus> {
  const cur = useSession.getState().status;
  if (cur !== "loading") return cur;
  if (MOCK) { useSession.setState({ status: "disabled" }); return "disabled"; }
  let enabled = false;
  try {
    const res = await fetch(`${API_URL}/auth/config`, { cache: "no-store" });
    // An older backend without the endpoint is an open demo.
    enabled = res.ok && (await res.json())?.enabled === true;
  } catch { enabled = false; }
  if (!enabled) { useSession.setState({ status: "disabled" }); return "disabled"; }
  if (await refresh()) {
    try { await loadMe(); return "authenticated"; } catch { /* fall through */ }
  }
  clear();
  return "anonymous";
}

export const authEnabled = () => useSession.getState().status !== "disabled";

async function begin(path: string, body: unknown) {
  adopt(await post<SessionPayload>(path, body));
  await loadMe();
}

export const login = (email: string, password: string) => begin("/auth/login", { email, password });
export const register = (b: { email: string; password: string; name: string; org_name: string }) => begin("/auth/register", b);
export const acceptInvitation = (b: { token: string; password: string; name?: string }) => begin("/auth/invitations/accept", b);

export async function logout() {
  const token = store.get();
  clear();
  if (token) { try { await post("/auth/logout", { refresh_token: token }); } catch { /* already gone */ } }
}

/** Opens a session in another organization of the user and reloads the app with that tenant's data. */
export async function switchOrg(orgId: string) {
  const old = store.get();
  const s = await post<SessionPayload>("/auth/switch-org", { org_id: orgId }, await accessToken());
  adopt(s);
  if (old) { try { await post("/auth/logout", { refresh_token: old }); } catch { /* ignore */ } }
  window.location.reload();
}

/** The current access token, refreshed when it is about to expire ("" without a session). */
export async function accessToken(): Promise<string> {
  if (useSession.getState().status !== "authenticated") return "";
  if (accessExp - Date.now() < 30_000) await refresh();
  return access;
}

/** fetch with the bearer token; on 401 refreshes once and retries. In demo mode it is a plain fetch. */
export async function authFetch(url: string, init: RequestInit = {}): Promise<Response> {
  if (!authEnabled() || MOCK) return fetch(url, init);
  const withToken = (tok: string): RequestInit => ({ ...init, headers: { ...(init.headers as Record<string, string> | undefined), ...(tok ? { Authorization: `Bearer ${tok}` } : {}) } });
  let res = await fetch(url, withToken(await accessToken()));
  if (res.status === 401 && (await refresh())) res = await fetch(url, withToken(access));
  if (res.status === 401) clear();
  return res;
}

/** The WebSocket URL with the access token as ?access_token= (how the backend authenticates /ws). */
export async function wsUrl(base: string): Promise<string> {
  if (!authEnabled() || MOCK) return base;
  const tok = await accessToken();
  if (!tok) return base;
  return `${base}${base.includes("?") ? "&" : "?"}access_token=${encodeURIComponent(tok)}`;
}

/** Permission check for gating UI. In demo mode (auth off) everything is allowed, as before. */
export function can(perm: string): boolean {
  const s = useSession.getState();
  return s.status !== "authenticated" || s.permissions.includes(perm);
}

/** React hook version of can(). */
export const useCan = (perm: string) => useSession((s) => s.status !== "authenticated" || s.permissions.includes(perm));
