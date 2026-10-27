/**
 * Data inboxes (docs/architecture/data-inboxes.md): pure helpers of the admin UI. A data inbox is a URL + secret
 * token any software (a script, an ERP export, n8n / Zapier / Make, curl) can POST JSON, CSV or files to.
 * Contract: backend/internal/api/inboxes.go (/api/v1/data-inboxes and the public POST /api/v1/inbox/{id}).
 */

export type InboxKind = "json" | "csv" | "files";
export const INBOX_KINDS: InboxKind[] = ["json", "csv", "files"];

export interface DataInbox {
  id: string; name: string; slug: string;
  /** Without a customer: absolute drive folder. With one: relative to the customer's folder. */
  folder: string; customer_id: string;
  allowed: InboxKind[]; max_bytes: number; rate_per_min: number;
  /** last 4 characters of the token, to tell tokens apart (never enough to use it) */
  token_hint: string;
  created_by: string; created_at: string; updated_at: string;
  rotated_at: string | null; revoked_at: string | null; revoked_by?: string;
  last_push_at: string | null; push_count: number;
  status: "active" | "revoked";
  /** absolute destination ("" when the customer's folder cannot be resolved) */
  folder_path: string;
  /** "/api/v1/inbox/<id>" */
  push_path: string;
  /** absolute URL when the server knows its public origin, "" otherwise */
  push_url: string;
}
/** Returned at creation and rotation: `token` is the only copy of the secret. */
export interface CreatedDataInbox extends DataInbox { token: string }
export interface DataInboxInput { name: string; folder?: string; customer_id?: string; allowed: InboxKind[]; max_bytes: number; rate_per_min?: number }
export interface PushedFile { file_id: string; path: string; name: string; version: number; size: number; mime: string; kind: InboxKind; created: boolean; unchanged: boolean }
export interface InboxPush { id: string; inbox_id: string; status: string; size: number; files: PushedFile[]; received_at: string }

export const MIN_MAX_BYTES = 1024;
export const DEFAULT_MAX_MB = 5;
export const MAX_NAME = 80;

/**
 * The absolute push URL: the server's when it has one, otherwise the push path on the API origin the browser
 * uses (apiUrl is API_URL: "/api/v1" behind the same domain, or an absolute URL in development).
 */
export function pushUrlOf(ib: Pick<DataInbox, "push_url" | "push_path">, apiUrl: string, origin: string): string {
  if (ib.push_url) return ib.push_url;
  const rel = ib.push_path.replace(/^\/api\/v1/, "");
  const base = apiUrl.replace(/\/$/, "");
  if (/^https?:\/\//i.test(base)) return base + rel;
  return origin.replace(/\/$/, "") + base + rel;
}

/** Ready-to-paste curl examples. The token goes ONLY in the Authorization header (the server refuses it in the URL). */
export function curlExamples(url: string, token: string): { json: string; csv: string; file: string } {
  const auth = `-H "Authorization: Bearer ${token}"`;
  return {
    json: `curl -X POST "${url}" \\\n  ${auth} \\\n  -H "Content-Type: application/json" \\\n  -H "X-Filename: orders.json" \\\n  --data '{"order_id": 1042, "total": 250.75}'`,
    csv: `curl -X POST "${url}" \\\n  ${auth} \\\n  -H "Content-Type: text/csv" \\\n  -H "X-Idempotency-Key: export-2026-10-27" \\\n  --data-binary @export.csv`,
    file: `curl -X POST "${url}" \\\n  ${auth} \\\n  -F "file=@report.pdf"`,
  };
}

/** Human size (base 1024, one decimal under 10). */
export function fmtBytes(n: number): string {
  if (!Number.isFinite(n) || n < 1024) return `${Math.max(0, Math.round(n || 0))} B`;
  const units = ["KB", "MB", "GB"];
  let v = n / 1024, i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} ${units[i]}`;
}

/** Form errors (i18n key suffixes under dataInbox.err.*); empty = valid. maxMb is the form value in MB. */
export function validateInbox(d: { name: string; folder: string; allowed: InboxKind[]; maxMb: number }, driveMaxMb = 25): string[] {
  const errs: string[] = [];
  const name = d.name.trim();
  if (!name) errs.push("name_required");
  else if ([...name].length > MAX_NAME) errs.push("name_too_long");
  if (d.allowed.length === 0) errs.push("kinds_required");
  if (!Number.isFinite(d.maxMb) || d.maxMb * 1024 * 1024 < MIN_MAX_BYTES || d.maxMb > driveMaxMb) errs.push("max_size");
  const f = d.folder.trim();
  if (f && /(^|\/)\.\.(\/|$)/.test(f)) errs.push("folder");
  return errs;
}

/** The drive route that shows a pushed file. */
export const driveHref = (path: string) => `/files?path=${encodeURIComponent(path)}`;
