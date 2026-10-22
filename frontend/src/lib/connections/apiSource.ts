/** Pure helpers of the custom_api "read-only HTTP source" form: draft <-> ApiSource and the same validation rules as the
 *  server (so the owner sees a clear message before a generic 400). No React, no network, never touches the API key. */
import type { ApiAuthStyle, ApiEndpoint, ApiSource } from "./types";

export const CUSTOM_API = "custom_api";
export const SOURCE_LIMITS = { description: 500, endpoints: 30, endpointDescription: 300, authName: 100 } as const;
export const ENDPOINT_NAME_RE = /^[a-z][a-z0-9_]{0,39}$/;
const PLACEHOLDER_RE = /\{([^{}]*)\}/g;
const PLACEHOLDER_NAME_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;
const AUTH_NAME_RE = /^[A-Za-z0-9_.~-]+$/;

/** Endpoint row as typed in the form: params are a comma-separated string. */
export interface EndpointDraft { name: string; path: string; description: string; params: string }
export interface SourceDraft { base_url: string; auth_style: ApiAuthStyle; auth_name: string; description: string; endpoints: EndpointDraft[] }

export type SourceErrCode =
  | "base_url_required" | "base_url_scheme" | "base_url_invalid"
  | "auth_name_required" | "auth_name_invalid"
  | "description_too_long" | "too_many_endpoints"
  | "name_required" | "name_invalid" | "name_duplicate"
  | "path_required" | "path_slash" | "path_invalid"
  | "endpoint_description_too_long";
export interface EndpointErrors { name?: SourceErrCode; path?: SourceErrCode; description?: SourceErrCode }
export interface SourceErrors {
  base_url?: SourceErrCode;
  auth_name?: SourceErrCode;
  description?: SourceErrCode;
  endpoints?: SourceErrCode;
  /** same indexes as draft.endpoints. */
  rows: EndpointErrors[];
}

export const emptyEndpoint = (): EndpointDraft => ({ name: "", path: "", description: "", params: "" });
export const emptySourceDraft = (): SourceDraft => ({ base_url: "", auth_style: "bearer", auth_name: "", description: "", endpoints: [] });

/** Characters as the user sees them (code points), not UTF-16 units. */
const len = (s: string) => [...s].length;
const isBlankRow = (e: EndpointDraft) => !e.name.trim() && !e.path.trim() && !e.description.trim() && !e.params.trim();

/** Friendlier typing of an endpoint name: lowercase, spaces and dashes become "_". */
export const normalizeEndpointName = (s: string) => s.toLowerCase().replace(/[\s-]+/g, "_");

/** "sku, warehouse,,sku" -> ["sku", "warehouse"]. */
export function parseParams(text: string): string[] {
  const out: string[] = [];
  for (const p of text.split(",")) { const v = p.trim(); if (v && !out.includes(v)) out.push(v); }
  return out;
}
/** "/products/{sku}/stock" -> ["sku"]. */
export function pathParams(path: string): string[] {
  const out: string[] = [];
  for (const m of path.matchAll(PLACEHOLDER_RE)) { const v = m[1].trim(); if (v && !out.includes(v)) out.push(v); }
  return out;
}

function baseUrlError(raw: string): SourceErrCode | undefined {
  const v = raw.trim();
  if (!v) return "base_url_required";
  if (!/^https?:\/\//i.test(v)) return "base_url_scheme";
  if (/\s/.test(v)) return "base_url_invalid";
  try {
    const u = new URL(v);
    if (u.protocol !== "http:" && u.protocol !== "https:") return "base_url_scheme";
    if (!u.hostname) return "base_url_invalid";
  } catch { return "base_url_invalid"; }
  return undefined;
}

function pathError(raw: string): SourceErrCode | undefined {
  const v = raw.trim();
  if (!v) return "path_required";
  if (!v.startsWith("/")) return "path_slash";
  if (/\s/.test(v)) return "path_invalid";
  for (const m of v.matchAll(PLACEHOLDER_RE)) if (!PLACEHOLDER_NAME_RE.test(m[1])) return "path_invalid";
  // after removing the well-formed {param} placeholders no brace may be left
  if (/[{}]/.test(v.replace(PLACEHOLDER_RE, ""))) return "path_invalid";
  return undefined;
}

/** Same rules as the server contract. Blank endpoint rows are ignored (they are dropped when sending). */
export function validateSource(d: SourceDraft): SourceErrors {
  const e: SourceErrors = { rows: d.endpoints.map(() => ({})) };
  const bu = baseUrlError(d.base_url); if (bu) e.base_url = bu;
  if (d.auth_style !== "bearer") {
    const n = d.auth_name.trim();
    if (!n) e.auth_name = "auth_name_required";
    else if (!AUTH_NAME_RE.test(n) || len(n) > SOURCE_LIMITS.authName) e.auth_name = "auth_name_invalid";
  }
  if (len(d.description.trim()) > SOURCE_LIMITS.description) e.description = "description_too_long";
  const seen = new Set<string>();
  let used = 0;
  d.endpoints.forEach((ep, i) => {
    if (isBlankRow(ep)) return;
    used++;
    const r = e.rows[i];
    const name = ep.name.trim();
    if (!name) r.name = "name_required";
    else if (!ENDPOINT_NAME_RE.test(name)) r.name = "name_invalid";
    else if (seen.has(name)) r.name = "name_duplicate";
    if (name) seen.add(name);
    const pe = pathError(ep.path); if (pe) r.path = pe;
    if (len(ep.description.trim()) > SOURCE_LIMITS.endpointDescription) r.description = "endpoint_description_too_long";
  });
  if (used > SOURCE_LIMITS.endpoints) e.endpoints = "too_many_endpoints";
  return e;
}

export function hasErrors(e: SourceErrors, opts: { ignoreAuth?: boolean } = {}): boolean {
  return !!(e.base_url || (!opts.ignoreAuth && e.auth_name) || e.description || e.endpoints || e.rows.some((r) => r.name || r.path || r.description));
}

/** Draft -> wire object. The path placeholders are always listed in params (the owner does not have to repeat them). */
export function draftToSource(d: SourceDraft): ApiSource {
  const endpoints: ApiEndpoint[] = d.endpoints.filter((e) => !isBlankRow(e)).map((e) => {
    const path = e.path.trim();
    const params = pathParams(path);
    for (const p of parseParams(e.params)) if (!params.includes(p)) params.push(p);
    const desc = e.description.trim();
    return { name: e.name.trim(), path, ...(desc ? { description: desc } : {}), params };
  });
  const description = d.description.trim();
  return {
    base_url: d.base_url.trim(),
    auth: d.auth_style === "bearer" ? { style: "bearer" } : { style: d.auth_style, name: d.auth_name.trim() },
    ...(description ? { description } : {}),
    endpoints,
  };
}

/** Wire object -> editable draft (for the drawer). */
export function sourceToDraft(s?: ApiSource | null): SourceDraft {
  if (!s) return emptySourceDraft();
  const style: ApiAuthStyle = s.auth?.style === "header" || s.auth?.style === "query" ? s.auth.style : "bearer";
  return {
    base_url: s.base_url ?? "",
    auth_style: style,
    auth_name: style === "bearer" ? "" : s.auth?.name ?? "",
    description: s.description ?? "",
    endpoints: (s.endpoints ?? []).map((e) => ({ name: e.name ?? "", path: e.path ?? "", description: e.description ?? "", params: (e.params ?? []).join(", ") })),
  };
}

/** Server-side check used by the mock: null when valid, otherwise the first error code. */
export function sourceErrorCode(s: unknown): SourceErrCode | "source_required" | null {
  if (!s || typeof s !== "object") return "source_required";
  const src = s as Partial<ApiSource>;
  const style = src.auth?.style ?? "bearer";
  if (style !== "bearer" && style !== "header" && style !== "query") return "auth_name_invalid";
  if (src.endpoints !== undefined && !Array.isArray(src.endpoints)) return "path_invalid";
  // the form drops blank rows; on the wire every endpoint needs a name and a path
  for (const ep of src.endpoints ?? []) { if (!ep?.name) return "name_required"; if (!ep.path) return "path_required"; }
  const draft = sourceToDraft({ base_url: String(src.base_url ?? ""), auth: { style, name: src.auth?.name }, description: src.description, endpoints: src.endpoints });
  if ((src.endpoints?.length ?? 0) > SOURCE_LIMITS.endpoints) return "too_many_endpoints";
  const e = validateSource(draft);
  if (e.base_url) return e.base_url;
  if (e.auth_name) return e.auth_name;
  if (e.description) return e.description;
  if (e.endpoints) return e.endpoints;
  for (const r of e.rows) { const c = r.name ?? r.path ?? r.description; if (c) return c; }
  return null;
}
