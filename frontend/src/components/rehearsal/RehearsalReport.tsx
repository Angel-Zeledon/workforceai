"use client";
import { Fragment, useCallback, useEffect, useState } from "react";
import { fmtUsd, useT } from "@/lib/i18n";
import { useCan } from "@/lib/session";
import { rehearsalApi, useRehearsal, type RehearsalAction, type RequestRehearsal } from "@/lib/rehearsal";
import { nextVerdict, previewRows, tally } from "@/lib/rehearsalLogic";
import { RiskBadge, useAgentName } from "../ui";

const DECISION_STYLE: Record<string, string> = {
  allowed: "border-ok/40 bg-ok/10 text-ok",
  needs_approval: "border-warn/40 bg-warn/10 text-warn",
  denied: "border-err/40 bg-err/10 text-err",
};

/** One "would have done" entry: what, to whom, how much, what the real run would have done, and the person's verdict. */
export function RehearsalActionRow({ a, onChange }: { a: RehearsalAction; onChange?: (a: RehearsalAction) => void }) {
  const { t } = useT();
  const name = useAgentName();
  const canDecide = useCan("approvals:decide");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState(false);
  const [open, setOpen] = useState(false);
  const decide = async (v: "would_approve" | "would_reject") => {
    setBusy(true); setErr(false);
    try { onChange?.(await rehearsalApi.verdict(a.id, nextVerdict(a.verdict, v))); } catch { setErr(true); } finally { setBusy(false); }
  };
  const rows = previewRows(a.preview);
  return (
    <li data-testid="rehearsal-action" data-id={a.id} data-verdict={a.verdict || "none"} className="rounded-lg border border-line bg-panel2 p-2.5 text-xs">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="min-w-0">
          <b>{name(a.agent_id)}</b> <span className="font-mono text-[11px] text-ink2">{a.tool}.{a.action}</span>
          <span className={`ml-2 rounded-full border px-2 py-0.5 text-[10px] font-semibold ${DECISION_STYLE[a.would_decision] ?? ""}`}>{t(`rehearsal.would.${a.would_decision}`)}</span>
          {a.deny_reason && <span className="ml-1 text-[10.5px] text-mute">({a.deny_reason})</span>}
        </div>
        <RiskBadge risk={a.risk || "medium"} />
      </div>
      <div className="mt-1 flex flex-wrap gap-x-3 gap-y-0.5 text-[11px] text-ink2">
        {a.recipients.length > 0 && <span>{t("rehearsal.recipients")}: <b>{a.recipients.join(", ")}</b></span>}
        {typeof a.amount === "number" && <span>{t("rehearsal.amount")}: <b>{fmtUsd(a.amount)}</b></span>}
        {a.account && <span>{t("rehearsal.account")}: {a.account}</span>}
        <span className="text-mute">{t(`rehearsal.scope.${a.scope === "request" ? "request" : "agent"}`)}</span>
      </div>
      {rows.length > 0 && (
        <div className="mt-1.5">
          <button type="button" aria-expanded={open} onClick={() => setOpen(!open)} className="text-[11px] font-semibold text-accent hover:underline">{open ? t("rehearsal.hidePreview") : t("rehearsal.showPreview")}</button>
          {open && (
            <dl data-testid="rehearsal-preview" className="mt-1 grid grid-cols-[auto,1fr] gap-x-2 gap-y-0.5 rounded-md border border-line bg-bg/60 p-2 font-mono text-[10.5px]">
              {rows.map(([k, v]) => <Fragment key={k}><dt className="text-mute">{k}</dt><dd className="break-words text-ink2">{v}</dd></Fragment>)}
            </dl>
          )}
        </div>
      )}
      <div className="mt-2 flex flex-wrap items-center gap-2">
        <span className="text-[11px] text-mute">{t("rehearsal.yourCall")}</span>
        {(["would_approve", "would_reject"] as const).map((v) => (
          <button key={v} type="button" data-testid={`rehearsal-${v}`} disabled={!canDecide || busy} aria-pressed={a.verdict === v} onClick={() => decide(v)}
            className={`rounded-md border px-2.5 py-0.5 text-[11px] font-semibold transition disabled:opacity-40 ${a.verdict === v ? (v === "would_approve" ? "border-ok bg-ok/15 text-ok" : "border-err bg-err/15 text-err") : "border-line bg-panel text-ink hover:border-accent"}`}>
            {t(`rehearsal.verdict.${v}`)}
          </button>
        ))}
        {err && <span role="alert" className="text-[11px] text-err">{t("rehearsal.error")}</span>}
      </div>
    </li>
  );
}

/** The "Ensayo" report of a request: every action it would have taken (nothing was executed). Renders nothing when empty. */
export function RehearsalReport({ requestId, always = false }: { requestId: string; always?: boolean }) {
  const { t } = useT();
  const tick = useRehearsal((s) => s.tick);
  const [rep, setRep] = useState<RequestRehearsal | null>(null);
  const load = useCallback(async () => { try { setRep(await rehearsalApi.report(requestId)); } catch { setRep(null); } }, [requestId]);
  useEffect(() => { void load(); }, [load, tick]);
  if (!rep || (!always && rep.actions.length === 0 && !rep.rehearsal)) return null;
  const c = tally(rep.actions);
  const update = (a: RehearsalAction) => setRep((r) => (r ? { ...r, actions: r.actions.map((x) => (x.id === a.id ? a : x)) } : r));
  return (
    <section data-testid="rehearsal-report" data-request={requestId} className="rounded-lg border border-accent/30 bg-accent/5 p-3">
      <h3 className="text-[13px] font-semibold text-ink">{t("rehearsal.report.title")}</h3>
      <p className="mt-0.5 text-[11.5px] text-ink2">{t("rehearsal.report.help")}</p>
      <p data-testid="rehearsal-summary" className="mt-1 text-[11px] text-mute">
        {t("rehearsal.report.summary", { total: c.total, approve: c.would_approve, reject: c.would_reject, undecided: c.undecided })}
      </p>
      {rep.actions.length === 0 ? <p className="mt-2 text-[11.5px] text-mute">{t("rehearsal.report.empty")}</p> : (
        <ul className="mt-2 space-y-2">{rep.actions.map((a) => <RehearsalActionRow key={a.id} a={a} onChange={update} />)}</ul>
      )}
    </section>
  );
}
