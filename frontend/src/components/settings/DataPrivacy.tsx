"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { API_URL, MOCK } from "@/lib/config";
import { authFetch, useCan, useSession } from "@/lib/session";
import { Btn } from "../ui";
import { ErrorLine } from "../connections/shared";
import { Icon } from "../icons";

/**
 * Data & privacy (GDPR): export everything the organization stored, and delete the organization.
 * Contract: backend/internal/api/tenantdata.go and backend/internal/tenantdata/README.md.
 *
 *   GET    /org/export    owner/admin: a ZIP (one JSON Lines file per table, the Drive files, README.txt, MANIFEST.json);
 *                         one per hour per organization (429 + Retry-After otherwise). Secrets are never in it.
 *   GET    /org/deletion  everybody: the deletion status (shown as a banner while a deletion is pending).
 *   POST   /org/deletion  owner: {confirm_name, password}; the purge happens 7 days later, until then nothing changes.
 *   DELETE /org/deletion  owner: cancels the pending deletion.
 */
export interface DeletionStatus {
  pending: boolean; org_name: string; requested_at?: string; deletion_at?: string; requested_by?: string;
  grace_days: number; protected: boolean; now: string;
}

class DataError extends Error {
  constructor(public status: number, public code: string, message: string, public retryAfter = 0) { super(message); }
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  if (MOCK) throw new DataError(404, "mock", "not available in mock mode");
  const res = await authFetch(`${API_URL}${path}`, {
    method,
    headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
    cache: "no-store",
  });
  const text = await res.text();
  let json: Record<string, unknown> = {};
  try { json = text ? JSON.parse(text) : {}; } catch { /* not JSON */ }
  if (!res.ok) throw new DataError(res.status, String(json.code ?? ""), String(json.error ?? `${method} ${path} -> ${res.status}`), Number(res.headers.get("Retry-After") ?? 0));
  return json as T;
}

/** Downloads the export ZIP with the access token (a plain link could not carry it). */
async function downloadExport(): Promise<void> {
  if (MOCK) throw new DataError(404, "mock", "not available in mock mode");
  const res = await authFetch(`${API_URL}/org/export`, { cache: "no-store" });
  if (!res.ok) {
    let code = "";
    try { code = String(((await res.json()) as { code?: string }).code ?? ""); } catch { /* not JSON */ }
    throw new DataError(res.status, code, `export -> ${res.status}`, Number(res.headers.get("Retry-After") ?? 0));
  }
  const blob = await res.blob();
  const cd = res.headers.get("Content-Disposition") ?? "";
  const name = /filename="([^"]+)"/.exec(cd)?.[1] ?? "export.zip";
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 10_000);
}

function fmtDate(iso: string | undefined, locale: string) {
  if (!iso) return "";
  try { return new Date(iso).toLocaleString(locale === "en" ? "en-US" : "es-ES", { dateStyle: "long", timeStyle: "short" }); } catch { return iso; }
}

function Card({ icon, title, danger, children }: { icon: "doc" | "alert"; title: string; danger?: boolean; children: React.ReactNode }) {
  return (
    <section className={`ac-rise rounded-xl border bg-panel p-5 ${danger ? "border-err/30" : "border-line"}`}>
      <div className="mb-3 flex items-center gap-3">
        <span className={`flex h-9 w-9 shrink-0 items-center justify-center rounded-lg ${danger ? "bg-err/10 text-err" : "bg-accent-soft text-accent"}`}><Icon name={icon} size={18} /></span>
        <h2 className="text-[14px] font-semibold text-ink">{title}</h2>
      </div>
      <div className="space-y-3 text-[13px] leading-relaxed text-ink2">{children}</div>
    </section>
  );
}

/** Banner for a pending deletion (also usable elsewhere in the app). */
export function DeletionBanner({ status, onCancel, busy }: { status: DeletionStatus; onCancel?: () => void; busy?: boolean }) {
  const { t, locale } = useT();
  if (!status.pending) return null;
  return (
    <div role="alert" data-testid="data-deletion-banner" className="flex flex-col gap-3 rounded-lg border border-err/30 bg-err/5 px-4 py-3 text-[13px] text-err sm:flex-row sm:items-center">
      <Icon name="alert" size={18} className="shrink-0" />
      <span className="min-w-0 flex-1 leading-snug">{t("data.delete.pending", { date: fmtDate(status.deletion_at, locale) })}</span>
      {onCancel && <Btn kind="ghost" disabled={busy} onClick={onCancel} data-testid="data-delete-cancel">{t("data.delete.cancel")}</Btn>}
    </div>
  );
}

export function DataPrivacy() {
  const { t, locale } = useT();
  const signedIn = useSession((s) => s.status === "authenticated");
  const canExport = useCan("org:manage");
  const canDelete = useCan("org:delete");
  const [status, setStatus] = useState<DeletionStatus | null>(null);
  const [loadErr, setLoadErr] = useState<string | null>(null);

  const [exporting, setExporting] = useState(false);
  const [exportMsg, setExportMsg] = useState<string | null>(null);
  const [exportErr, setExportErr] = useState<string | null>(null);

  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [delErr, setDelErr] = useState<string | null>(null);

  const load = useCallback(() => {
    call<DeletionStatus>("GET", "/org/deletion").then((s) => { setStatus(s); setLoadErr(null); }).catch(() => setLoadErr(t("data.loadFailed")));
  }, [t]);
  useEffect(() => { load(); }, [load]);

  const doExport = async () => {
    setExporting(true); setExportErr(null); setExportMsg(null);
    try {
      await downloadExport();
      setExportMsg(t("data.export.done"));
    } catch (e) {
      if (e instanceof DataError && e.status === 429) {
        const min = Math.max(1, Math.ceil(e.retryAfter / 60));
        setExportErr(t("data.export.limited", { min }));
      } else if (e instanceof DataError && e.status === 403) setExportErr(t("data.export.forbidden"));
      else setExportErr(t("data.export.failed"));
    } finally { setExporting(false); }
  };

  const schedule = async () => {
    setBusy(true); setDelErr(null);
    try {
      const s = await call<DeletionStatus>("POST", "/org/deletion", { confirm_name: name, password });
      setStatus(s); setOpen(false); setName("");
    } catch (e) {
      const code = e instanceof DataError ? e.code : "";
      setDelErr(code === "name_mismatch" ? t("data.delete.err.name") : code === "invalid_password" ? t("data.delete.err.password")
        : code === "rate_limited" ? t("data.delete.err.limited") : code === "protected" ? t("data.delete.protected") : t("data.delete.err.failed"));
    } finally { setPassword(""); setBusy(false); }
  };

  const cancel = async () => {
    setBusy(true); setDelErr(null);
    try { setStatus(await call<DeletionStatus>("DELETE", "/org/deletion")); }
    catch { setDelErr(t("data.delete.err.cancel")); }
    finally { setBusy(false); }
  };

  const orgName = status?.org_name ?? "";
  const ready = name.trim() === orgName && orgName !== "" && password.length > 0 && !busy;
  return (
    <div data-testid="data-privacy" className="mx-auto max-w-3xl space-y-5 px-5 py-6 sm:px-6">
      <p className="text-[13px] leading-relaxed text-mute">{t("data.intro")}</p>
      <ErrorLine msg={loadErr} />
      {status && <DeletionBanner status={status} busy={busy} onCancel={canDelete ? cancel : undefined} />}

      <Card icon="doc" title={t("data.export.title")}>
        <p>{t("data.export.body")}</p>
        <ul className="list-disc space-y-1 pl-5 text-[12.5px] text-mute">
          <li>{t("data.export.includes")}</li>
          <li>{t("data.export.excludes")}</li>
          <li>{t("data.export.limit")}</li>
        </ul>
        {exportMsg && <p role="status" className="text-[12.5px] font-medium text-ok">{exportMsg}</p>}
        <ErrorLine msg={exportErr} />
        <Btn kind="primary" disabled={exporting || !canExport} onClick={doExport} data-testid="data-export">
          {exporting ? t("data.export.working") : t("data.export.button")}
        </Btn>
        {!canExport && <p className="text-[12px] text-mute">{t("data.export.adminOnly")}</p>}
      </Card>

      <Card icon="alert" title={t("data.delete.title")} danger>
        {status?.protected ? <p>{t("data.delete.protected")}</p> : (
          <>
            <p>{t("data.delete.body", { days: status?.grace_days ?? 7 })}</p>
            <ul className="list-disc space-y-1 pl-5 text-[12.5px] text-mute">
              <li>{t("data.delete.point.grace", { days: status?.grace_days ?? 7 })}</li>
              <li>{t("data.delete.point.what")}</li>
              <li>{t("data.delete.point.final")}</li>
              <li>{t("data.delete.point.export")}</li>
            </ul>
            {!signedIn || !canDelete ? <p className="text-[12px] text-mute">{t("data.delete.ownerOnly")}</p>
              : status?.pending ? null
              : !open ? (
                <Btn kind="danger" onClick={() => { setOpen(true); setDelErr(null); }} data-testid="data-delete-open">{t("data.delete.button")}</Btn>
              ) : (
                <form className="space-y-3 rounded-lg border border-err/30 bg-err/5 p-4" onSubmit={(e) => { e.preventDefault(); if (ready) schedule(); }}>
                  <div>
                    <label htmlFor="data-delete-name" className="mb-1 block text-xs font-medium text-ink2">{t("data.delete.typeName", { name: orgName })}</label>
                    <input id="data-delete-name" data-testid="data-delete-name" value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" spellCheck={false}
                      className="w-full rounded-md border border-line-strong bg-panel py-2 px-3 text-[14px] text-ink outline-none focus:border-err" />
                  </div>
                  <div>
                    <label htmlFor="data-delete-password" className="mb-1 block text-xs font-medium text-ink2">{t("data.delete.password")}</label>
                    <input id="data-delete-password" data-testid="data-delete-password" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password"
                      className="w-full rounded-md border border-line-strong bg-panel py-2 px-3 text-[14px] text-ink outline-none focus:border-err" />
                  </div>
                  <ErrorLine msg={delErr} />
                  <div className="flex flex-wrap gap-2">
                    <Btn kind="danger" type="submit" disabled={!ready} data-testid="data-delete-confirm">{busy ? t("data.delete.working") : t("data.delete.confirm", { days: status?.grace_days ?? 7 })}</Btn>
                    <Btn kind="ghost" onClick={() => { setOpen(false); setName(""); setPassword(""); setDelErr(null); }}>{t("data.delete.back")}</Btn>
                  </div>
                </form>
              )}
            {status?.pending && <p className="text-[12.5px] text-mute">{t("data.delete.requestedAt", { date: fmtDate(status.requested_at, locale) })}</p>}
            {!open && <ErrorLine msg={delErr} />}
          </>
        )}
      </Card>
    </div>
  );
}
