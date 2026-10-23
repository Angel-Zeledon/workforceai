"use client";
import { useEffect, useState } from "react";
import type { Report } from "@/lib/types";
import { fmtUsd } from "@/lib/meta";
import { fmtDateTime, useT } from "@/lib/i18n";
import { fetchReportEvidence } from "@/lib/evidenceApi";
import { tracedCount, unverifiedOf, type ReportCitation, type ReportEvidence } from "@/lib/evidenceLogic";
import { Dot, useAgentColor, useAgentName } from "./ui";
import { RehearsalReport } from "./rehearsal/RehearsalReport";

/** B9: the rehearsal report of the same request (renders nothing when the request rehearsed nothing). */
function ReportRehearsal({ requestId }: { requestId: string }) {
  return requestId ? <RehearsalReport requestId={requestId} /> : null;
}
import { RequestAuditStamp } from "./quality/AuditStamp";
import { EvidenceText } from "./evidence/EvidenceText";
import { EvidenceDrawer } from "./evidence/EvidenceDrawer";

const REASON_KEY: Record<string, string> = {
  not_seen: "evidence.reason.not_seen", not_found: "evidence.reason.not_found", value_mismatch: "evidence.reason.value_mismatch",
  malformed: "evidence.reason.malformed", unknown_kind: "evidence.reason.unknown_kind",
};

export function ReportView({ r }: { r: Report }) {
  const { t } = useT();
  const name = useAgentName();
  const color = useAgentColor();
  // B6 evidence chain: figures with a verified source become links (an older backend simply has none).
  const [ev, setEv] = useState<ReportEvidence | null>(null);
  const [open, setOpen] = useState<{ value: string; cites: ReportCitation[] } | null>(null);
  const [showUnverified, setShowUnverified] = useState(false);
  useEffect(() => {
    let live = true;
    setEv(null); setOpen(null); setShowUnverified(false);
    fetchReportEvidence(r.id).then((x) => { if (live) setEv(x); });
    return () => { live = false; };
  }, [r.id]);
  const cites = ev?.citations ?? [];
  const traced = tracedCount(cites);
  const unverified = unverifiedOf(cites);
  const onOpen = (value: string, c: ReportCitation[]) => setOpen({ value, cites: c });
  return (
    <article className="space-y-4">
      <header>
        <h2 className="text-lg font-semibold text-ink">{r.title}</h2>
        <div className="mt-1 flex flex-wrap items-center gap-3 text-[11px] text-mute">
          <span>{fmtDateTime(r.created_at)}</span>
          <span>{t("common.cost")} {fmtUsd(r.cost_usd)}</span>
          <span className="flex flex-wrap items-center gap-2">
            {r.contributors.map((c) => (<span key={c} className="flex items-center gap-1"><Dot color={color(c)} />{name(c)}</span>))}
          </span>
        </div>
        {(traced > 0 || unverified.length > 0) && (
          <div className="mt-2 flex flex-wrap items-center gap-2 text-[11px]">
            {traced > 0 && (
              <span data-testid="evidence-traced" title={t("evidence.tracedHelp")} className="rounded-full bg-ok/10 px-2 py-0.5 font-semibold text-ok">
                {t("evidence.traced", { count: traced })}
              </span>
            )}
            {unverified.length > 0 && (
              <button type="button" data-testid="evidence-unverified" aria-expanded={showUnverified} onClick={() => setShowUnverified((v) => !v)}
                className="rounded-full bg-warn/10 px-2 py-0.5 font-semibold text-warn hover:bg-warn/20">
                {t("evidence.unverified", { count: unverified.length })}
              </button>
            )}
          </div>
        )}
        {showUnverified && unverified.length > 0 && (
          <div data-testid="evidence-unverified-list" className="mt-2 rounded-md border border-warn/40 bg-warn/5 p-2.5 text-[11.5px] text-ink2">
            <div className="font-semibold text-ink">{t("evidence.unverifiedTitle")}</div>
            <p className="mb-1 text-mute">{t("evidence.unverifiedHelp")}</p>
            <ul className="space-y-0.5">
              {unverified.map((c, i) => (
                <li key={i}>
                  <span className="font-semibold text-ink">{c.value}</span>{c.label ? ` · ${c.label}` : ""} · {name(c.agent_id)}
                  {c.reason && REASON_KEY[c.reason] ? <span className="text-mute"> ({t(REASON_KEY[c.reason])})</span> : null}
                </li>
              ))}
            </ul>
          </div>
        )}
      </header>
      {r.request_id && <RequestAuditStamp requestId={r.request_id} />}
      <div className="rounded-md border border-accent/30 bg-accent/5 p-3 text-[13px] leading-relaxed text-ink2">
        <div className="mb-1 text-[10px] font-semibold uppercase tracking-widest text-accent">{t("report.executiveSummary")}</div>
        <EvidenceText text={r.summary} cites={cites} onOpen={onOpen} />
      </div>
      {r.sections.map((s, i) => (
        <section key={i}>
          <h3 className="mb-1 text-[13px] font-semibold text-ink">{s.heading}</h3>
          <p className="whitespace-pre-line text-[12.5px] leading-relaxed text-ink2"><EvidenceText text={s.body} cites={cites} onOpen={onOpen} /></p>
        </section>
      ))}
      <ReportRehearsal requestId={r.request_id} />
      {open && <EvidenceDrawer value={open.value} cites={open.cites} onClose={() => setOpen(null)} />}
    </article>
  );
}
