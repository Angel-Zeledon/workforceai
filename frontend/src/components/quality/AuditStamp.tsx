"use client";
import { useEffect, useState } from "react";
import { api } from "@/lib/api";
import { aggregateAudit, parseRequestAudit, type AuditStatus, type RequestAudit, type TaskAudit } from "@/lib/auditLogic";
import { useT } from "@/lib/i18n";
import { useStore } from "@/lib/store";

const TONE: Record<AuditStatus, string> = {
  verified: "border-emerald-600/40 bg-emerald-500/10 text-emerald-800",
  inconsistency: "border-red-600/40 bg-red-500/10 text-red-700",
  unverified: "border-amber-600/40 bg-amber-500/10 text-amber-800",
};
const MARK: Record<AuditStatus, string> = { verified: "✓", inconsistency: "!", unverified: "?" };

/** The stamp itself: "Verificado" / "Inconsistencia" / "Sin verificar". */
export function AuditBadge({ status, testId }: { status: AuditStatus; testId?: string }) {
  const { t } = useT();
  return (
    <span data-testid={testId ?? "audit-badge"} data-status={status} title={t(`audit.hint.${status}`)}
      className={`inline-flex items-center gap-1 rounded-md border px-1.5 py-[1px] text-[10px] font-bold uppercase tracking-wide ${TONE[status]}`}>
      <span aria-hidden>{MARK[status]}</span>{t(`audit.status.${status}`)}
    </span>
  );
}

/** One audit: stamp, what it says and, for an inconsistency, the findings (evidence on demand). */
export function AuditDetail({ audit, testId }: { audit: TaskAudit; testId?: string }) {
  const { t } = useT();
  const [showEv, setShowEv] = useState(false);
  const findings = audit.findings;
  return (
    <div data-testid={testId ?? `audit-detail-${audit.task_id}`} data-status={audit.status} className={`rounded-xl border px-2 py-1.5 text-[11px] ${TONE[audit.status]}`}>
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-[10px] font-semibold uppercase tracking-wide opacity-80">{t("audit.title")}</span>
        <AuditBadge status={audit.status} />
        {audit.compared > 0 && <span className="opacity-80">{t("audit.compared", { count: audit.compared })}</span>}
      </div>
      {audit.summary && <p className="mt-1 text-ink2">{audit.summary}</p>}
      {audit.status === "unverified" && <p className="mt-1 opacity-90">{t("audit.hint.unverified")}</p>}
      {findings.length > 0 && (audit.status === "inconsistency" || audit.status === "verified") && (
        <div className="mt-1">
          <div className="text-[10px] font-semibold uppercase tracking-wide opacity-80">{t(audit.status === "inconsistency" ? "audit.findingsBad" : "audit.findingsOk")}</div>
          <ul data-testid="audit-findings" className="mt-0.5 list-disc space-y-0.5 pl-4 text-ink2">{findings.map((f, i) => <li key={i}>{f}</li>)}</ul>
        </div>
      )}
      {audit.evidence.length > 0 && (
        <div className="mt-1">
          <button type="button" className="text-[10px] font-semibold underline opacity-80" aria-expanded={showEv} onClick={() => setShowEv((v) => !v)}>
            {t(showEv ? "audit.hideEvidence" : "audit.showEvidence", { count: audit.evidence.length })}
          </button>
          {showEv && <ul className="mt-0.5 space-y-0.5 font-mono text-[10px] text-mute [overflow-wrap:anywhere]">{audit.evidence.map((e, i) => <li key={i}>{e}</li>)}</ul>}
        </div>
      )}
    </div>
  );
}

/** Stamp of a whole request (report view). Reads GET /requests/{id}/audit; an older backend without the endpoint
 * falls back to the tasks already in the store. Shows nothing when the request had no audit. */
export function RequestAuditStamp({ requestId }: { requestId: string }) {
  const { t } = useT();
  const [audit, setAudit] = useState<RequestAudit | null>(null);
  useEffect(() => {
    let live = true;
    (async () => {
      let a: RequestAudit | null = null;
      try { a = parseRequestAudit(await api.requestAudit(requestId), requestId); } catch { /* older backend */ }
      if (!a) a = aggregateAudit(requestId, Object.values(useStore.getState().tasks));
      if (live) setAudit(a);
    })();
    return () => { live = false; };
  }, [requestId]);
  if (!audit || audit.status === "none") return null;
  return (
    <section data-testid="report-audit" data-status={audit.status} className="space-y-1.5">
      <div className="flex flex-wrap items-center gap-2 text-[11px] text-mute">
        <span className="font-semibold uppercase tracking-wide">{t("audit.reportTitle")}</span>
        <AuditBadge status={audit.status} testId="report-audit-badge" />
        <span>{t("audit.count", { count: audit.audits.length })}</span>
      </div>
      {audit.audits.map((a) => <AuditDetail key={a.task_id} audit={a} />)}
    </section>
  );
}
