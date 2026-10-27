import { create } from "zustand";
import { call } from "./api";
import { MOCK } from "./config";

/** Import from a link. Contract: backend/internal/api/linkimports.go and docs/architecture/link-imports.md (/api/v1).
 *  The auth header value is WRITE-ONLY: it is sent in a create/update call and never comes back (only `header_set`
 *  and `header_hint`, its last 4 characters). */

export type LinkSchedule = "once" | "hourly" | "daily" | "weekly";
export type LinkRunStatus = "ok" | "unchanged" | "error" | "skipped";
export const LINK_SCHEDULES: LinkSchedule[] = ["once", "hourly", "daily", "weekly"];
/** Error codes the UI translates (linkImports.code.<code>); anything else falls back to a generic text. */
export const LINK_CODES = [
  "read_only_mode", "kill_switch_active", "customer_unavailable", "header_unavailable", "http_status", "too_large", "content_type_not_allowed",
  "web_page_not_data", "empty_response", "insecure_redirect", "too_many_redirects", "redirect_invalid", "timeout", "fetch_failed",
  "content_mismatch", "quota_exceeded", "drive_error", "address_not_allowed", "host_not_allowed", "dns_failed", "url_scheme_not_allowed",
  "url_port_not_allowed", "url_invalid",
] as const;

export interface LinkImport {
  id: string; name: string; slug: string;
  /** The full link for admins; members see it with the query string hidden ("?…"). */
  url: string; host: string;
  folder: string; folder_path: string; customer_id: string; file_stem: string;
  schedule: LinkSchedule; enabled: boolean;
  header_name: string; header_set: boolean; header_hint: string;
  created_by: string; created_at: string; updated_at: string;
  next_run_at: string | null; last_run_at: string | null; last_status: LinkRunStatus | ""; last_code: string;
  last_hash: string; last_file_id: string; last_path: string; last_version: number; last_data_at: string | null; run_count: number;
}

export interface LinkRun {
  id: string; import_id: string; trigger: "manual" | "schedule"; actor: string; status: LinkRunStatus; code: string; http_status?: number;
  started_at: string; duration_ms: number; size: number; mime: string; hash: string; file_id: string; path: string; version: number;
}

export interface LinkHeaderInput { name: string; value?: string; clear?: boolean }
export interface LinkImportInput {
  name?: string; url?: string; folder?: string; customer_id?: string; file_stem?: string; schedule?: LinkSchedule; enabled?: boolean;
  /** WRITE-ONLY value. Omit to keep; `{name, value:""}` renames and keeps the sealed value; `{clear:true}` removes it. */
  header?: LinkHeaderInput;
}

interface ListResponse { imports: LinkImport[]; schedules: LinkSchedule[]; types: string[] }

async function req<T>(method: string, path: string, body?: unknown): Promise<T> {
  if (MOCK) {
    const { linkImportsMock } = await import("./mock/linkimports-mock");
    return linkImportsMock.handle(method, path, body) as T;
  }
  return call<T>(method, path, body);
}

const id = (s: string) => encodeURIComponent(s);

export const linkImportsApi = {
  list: async () => (await req<ListResponse>("GET", "/link-imports")).imports ?? [],
  get: (lid: string) => req<LinkImport>("GET", `/link-imports/${id(lid)}`),
  create: (body: LinkImportInput) => req<LinkImport>("POST", "/link-imports", body),
  update: (lid: string, body: LinkImportInput) => req<LinkImport>("PUT", `/link-imports/${id(lid)}`, body),
  remove: (lid: string) => req<unknown>("DELETE", `/link-imports/${id(lid)}`),
  /** Runs now. Always answers the recorded run (skipped and failed runs included). */
  run: (lid: string) => req<{ run: LinkRun; import?: LinkImport }>("POST", `/link-imports/${id(lid)}/run`),
  runs: (lid: string, limit = 20) => req<LinkRun[]>("GET", `/link-imports/${id(lid)}/runs?limit=${limit}`),
};

/** The realtime event of a run that wrote new data (docs/architecture/03-eventos-realtime.md). */
export const LINK_IMPORT_EVENT = "link_import.data_landed";

/** Bumps when new data landed (routed from store.ts), so an open list reloads. */
export const useLinkImportsLive = create<{ tick: number; bump: () => void }>((set) => ({ tick: 0, bump: () => set((s) => ({ tick: s.tick + 1 })) }));

/** Client-side check of a pasted link (the server checks again, including the address it resolves to). */
export function linkProblem(raw: string): "" | "empty" | "https" | "invalid" {
  const v = raw.trim();
  if (!v) return "empty";
  let u: URL;
  try { u = new URL(v); } catch { return "invalid"; }
  if (u.protocol === "http:") return "https";
  if (u.protocol !== "https:" || !u.hostname || u.username || u.password) return "invalid";
  return "";
}
