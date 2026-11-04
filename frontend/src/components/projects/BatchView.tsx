"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { useCan } from "@/lib/session";
import { batchApi, useBatchUi, type BatchView as View } from "@/lib/projects/batchApi";
import { useProjects } from "@/lib/projects/store";
import { ROW_STATUSES, filterRows, rowTone } from "@/lib/batchLogic";
import { WaitingLabel } from "../approvals/QuestionCard";
import { ScopeGrants } from "../approvals/ScopeGrants";
import { Btn, useAgentName } from "../ui";
import { fmtMoney } from "./shared";

const TONE: Record<string, string> = {
  running: "bg-[#2b6cb0]/10 text-[#2b6cb0]", err: "bg-err/10 text-err", ok: "bg-ok/10 text-ok", warn: "bg-amber-500/10 text-amber-800", mute: "bg-panel2 text-ink2",
};

/** The batch table: one row per customer (progress, current step and agent, status, cost, how long it waits for a
 * person), the counts on top with filters, and the batch actions. */
export function BatchView({ id }: { id: string }) {
  const { t } = useT();
  const name = useAgentName();
  const canManage = useCan("tasks:manage");
  const close = useBatchUi((s) => s.open);
  const openProject = useProjects((s) => s.openProject);
  const [v, setV] = useState<View | null>(null);
  const [filter, setFilter] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const load = useCallback(async () => {
    try { setV(await batchApi.get(id)); setErr(""); } catch { setErr(t("batch.error.load")); }
  }, [id, t]);
  useEffect(() => {
    void load();
    const iv = setInterval(() => void load(), 4000);
    return () => clearInterval(iv);
  }, [load]);
  const act = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    try { await fn(); await load(); } catch { setErr(t("batch.error.generic")); } finally { setBusy(false); }
  };
  if (!v) return <div className="p-6 text-[12px] text-mute">{err || t("batch.loading")}</div>;
  const b = v.batch;
  const rows = filterRows(v.rows, filter);
  const running = b.status === "running";
  return (
    <div data-testid="batch-view" className="mx-auto max-w-[1180px] space-y-4 p-4 md:p-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <button type="button" onClick={() => close(null)} className="text-[11px] font-medium text-accent">‹ {t("batch.backToProjects")}</button>
          <h1 className="font-display text-xl font-semibold text-ink">{t("batch.cardTitle", { name: b.name, count: b.items.length })}</h1>
          <p className="text-[12px] text-mute">{t(`batch.status.${b.status}`)}{b.control === "paused" && running ? ` · ${t("batch.paused")}` : ""} · {t("batch.review.paceValue", { count: b.parallel })} · {fmtMoney(v.cost_usd)}</p>
        </div>
        {canManage && (
          <div className="flex flex-wrap gap-2">
            {running && b.control === "active" && <Btn disabled={busy} data-testid="batch-pause" onClick={() => void act(() => batchApi.control(id, "pause"))}>{t("batch.action.pause")}</Btn>}
            {running && b.control === "paused" && <Btn disabled={busy} data-testid="batch-resume" onClick={() => void act(() => batchApi.control(id, "resume"))}>{t("batch.action.resume")}</Btn>}
            {(v.summary.failed ?? 0) > 0 && <Btn disabled={busy} onClick={() => void act(() => batchApi.retryFailed(id))}>{t("batch.action.retry", { count: v.summary.failed })}</Btn>}
            {running && <Btn kind="danger" disabled={busy} onClick={() => { if (window.confirm(t("batch.action.cancelConfirm"))) void act(() => batchApi.control(id, "cancel")); }}>{t("batch.action.cancel")}</Btn>}
          </div>
        )}
      </div>

      <div role="tablist" aria-label={t("batch.filter")} className="flex flex-wrap gap-1.5">
        <FilterBtn on={filter === ""} onClick={() => setFilter("")} label={`${t("batch.filterAll")} · ${v.rows.length}`} />
        {ROW_STATUSES.filter((s) => v.summary[s]).map((s) => (
          <FilterBtn key={s} on={filter === s} onClick={() => setFilter(s)} label={`${t(`batch.row.${s}`)} · ${v.summary[s]}`} testid={`batch-filter-${s}`} />
        ))}
      </div>

      <div className="overflow-x-auto rounded-[10px] border border-line bg-white">
        <table className="w-full min-w-[720px] text-left text-[12px]">
          <thead className="bg-panel2 text-[11px] text-mute">
            <tr>
              <th className="px-3 py-2">{t("batch.col.customer")}</th><th className="px-3">{t("batch.col.progress")}</th><th className="px-3">{t("batch.col.step")}</th>
              <th className="px-3">{t("batch.col.status")}</th><th className="px-3 text-right">{t("batch.col.cost")}</th><th className="px-3" />
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {rows.map((r) => (
              <tr key={r.customer_id} data-testid={`batch-row-${r.customer_id}`} data-status={r.status}>
                <td className="px-3 py-2 font-medium text-ink">{r.customer_name || r.customer_id}</td>
                <td className="px-3">
                  <div className="flex items-center gap-2"><div className="h-1.5 w-24 overflow-hidden rounded-full bg-panel2"><div className="h-full bg-accent" style={{ width: `${r.progress}%` }} /></div><span className="font-mono text-[11px] text-ink2">{r.progress}%</span></div>
                </td>
                <td className="px-3 text-ink2">{r.current_step ? <>{r.current_step}{r.current_agent_id ? <span className="text-mute"> · {name(r.current_agent_id)}</span> : null}</> : <span className="text-mute">—</span>}</td>
                <td className="px-3">
                  <span className={`rounded-md px-1.5 py-0.5 text-[11px] font-semibold ${TONE[rowTone(r.status)]}`}>{t(`batch.row.${r.status}`)}</span>
                  {r.status === "needs_help" && r.waiting_seconds ? <span className="ml-1.5 text-[11px] text-mute"><WaitingLabel seconds={r.waiting_seconds} /></span> : null}
                  {r.error && <div className="mt-0.5 line-clamp-1 text-[10px] text-err" title={r.error}>{r.error}</div>}
                </td>
                <td className="px-3 text-right font-mono text-ink2">{fmtMoney(r.cost_usd)}</td>
                <td className="px-3 text-right">
                  <div className="flex justify-end gap-1.5">
                    {r.project_id && <Btn onClick={() => { close(null); openProject(r.project_id!); }}>{t("batch.openProject")}</Btn>}
                    {canManage && running && ["queued", "running", "needs_help", "paused"].includes(r.status) && (
                      <Btn kind="danger" disabled={busy} onClick={() => { if (window.confirm(t("batch.action.cancelCustomerConfirm", { name: r.customer_name }))) void act(() => batchApi.cancelCustomer(id, r.customer_id)); }}>{t("batch.action.cancelCustomer")}</Btn>
                    )}
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <ScopeGrants batchId={id} />
      {err && <p role="alert" className="text-[12px] text-err">{err}</p>}
    </div>
  );
}

function FilterBtn({ on, onClick, label, testid }: { on: boolean; onClick: () => void; label: string; testid?: string }) {
  return (
    <button type="button" role="tab" aria-selected={on} data-testid={testid} onClick={onClick}
      className={`rounded-md border px-2.5 py-1 text-[12px] font-medium ${on ? "border-accent bg-accent/10 text-accent" : "border-line bg-white text-ink2 hover:border-accent"}`}>{label}</button>
  );
}
