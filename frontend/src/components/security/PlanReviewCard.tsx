"use client";
import { useState } from "react";
import { fmtUsd, useT } from "@/lib/i18n";
import { ctlApi } from "@/lib/connections/api";
import { useConnections } from "@/lib/connections/store";
import type { PlanReview } from "@/lib/connections/types";
import { useStore } from "@/lib/store";
import { Btn } from "../ui";
import { CapChip, ErrorLine, errText, inputCls } from "../connections/shared";

/** Plan review before execution (spec 14.4). The preflight is computed by the backend, not invented by the model. */
export function PlanReviewCard({ plan }: { plan: PlanReview }) {
  const { t } = useT();
  const agents = useStore((s) => s.agents);
  const providers = useConnections((s) => s.providers);
  const connections = useConnections((s) => s.connections);
  const [note, setNote] = useState(plan.note);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const open = plan.status === "plan_ready";

  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true); setErr(null);
    try { await fn(); await useConnections.getState().loadPlans(); await useConnections.getState().loadOutbox(); await useConnections.getState().loadUsage(); }
    catch (e) { setErr(errText(e)); }
    finally { setBusy(false); }
  };
  const riskOf = (connId: string, cap: string) => {
    const c = connections[connId];
    return providers.find((p) => p.id === c?.provider)?.capabilities.find((d) => d.id === cap)?.risk ?? "low";
  };

  return (
    <div data-testid={`ctl-plan-review-${plan.id}`} data-status={plan.status} data-required={plan.review_required} className="rounded-xl border border-line bg-panel2 p-3">
      <div className="mb-2 flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="text-sm font-semibold">{plan.request_text}</div>
          <div className="text-[11px] text-mute">{t("ctl.plan.estimate", { cost: fmtUsd(plan.est_cost_usd.p50), p90: fmtUsd(plan.est_cost_usd.p90), min: Math.max(1, Math.round(plan.est_duration_s.p50 / 60)) })}</div>
        </div>
        {plan.review_required && <span data-testid="ctl-plan-required" className="shrink-0 rounded-full bg-red-500/15 px-2 py-[1px] text-[10px] font-semibold uppercase text-red-700">{t("ctl.plan.required")}</span>}
      </div>

      <div className="mb-2">
        <div className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-mute">{t("ctl.plan.tasks")}</div>
        <ul className="space-y-1">
          {plan.tasks.map((task) => (
            <li key={task.id} className="flex items-center justify-between gap-2 rounded-xl border border-line bg-panel px-2 py-1 text-xs">
              <span><b>{agents[task.agent_id]?.name ?? task.agent_id}</b> · {task.title}</span>
              {open && <button type="button" data-testid={`ctl-plan-task-${task.id}-remove`} disabled={busy} onClick={() => run(() => ctlApi.patchPlan(plan.id, { remove_task_ids: [task.id] }))} className="rounded-md border border-line px-2 text-[10px] font-semibold text-mute hover:text-red-700">{t("ctl.plan.removeTask")}</button>}
            </li>
          ))}
          {plan.tasks.length === 0 && <li className="text-xs text-mute">{t("ctl.plan.noTasks")}</li>}
        </ul>
      </div>

      <div className="mb-2">
        <div className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-mute">{t("ctl.plan.reachable")}</div>
        <p className="mb-1 text-[11px] text-mute">{t("ctl.plan.reachableNote")}</p>
        {plan.reachable_connections.length === 0 ? <p className="text-xs text-mute">{t("ctl.plan.noReachable")}</p> : (
          <ul className="space-y-1">
            {plan.reachable_connections.map((r) => (
              <li key={`${r.connection_id}-${r.agent_id}`} className="flex flex-wrap items-center justify-between gap-1 text-xs">
                <span>{agents[r.agent_id]?.name ?? r.agent_id} → <b>{r.label}</b></span>
                <span className="flex gap-1">{r.capabilities.map((c) => <CapChip key={c} cap={c} risk={riskOf(r.connection_id, c)} />)}</span>
              </li>
            ))}
          </ul>
        )}
        <p className="mt-1 text-[11px] font-semibold">{plan.approvals_expected.min > 0 ? t("ctl.plan.approvalsExpected", { count: plan.approvals_expected.min }) : t("ctl.plan.noApprovals")}</p>
      </div>

      {open && (
        <>
          <label className="mb-2 flex items-center gap-2 text-xs font-semibold">
            <input type="checkbox" data-testid="ctl-plan-no-external" checked={plan.no_external_actions} disabled={busy} onChange={(e) => run(() => ctlApi.patchPlan(plan.id, { no_external_actions: e.target.checked }))} />
            {t("ctl.plan.noExternal")}
          </label>
          <input data-testid="ctl-plan-note" className={`${inputCls} mb-2`} placeholder={t("ctl.plan.notePlaceholder")} value={note} onChange={(e) => setNote(e.target.value)} onBlur={() => note !== plan.note && run(() => ctlApi.patchPlan(plan.id, { note }))} />
          <div className="flex justify-end gap-2">
            <Btn data-testid="ctl-plan-reject" kind="danger" disabled={busy} onClick={() => run(() => ctlApi.rejectPlan(plan.id))}>{t("ctl.plan.reject")}</Btn>
            <Btn data-testid="ctl-plan-approve" kind="primary" disabled={busy || plan.tasks.length === 0} onClick={() => run(() => ctlApi.approvePlan(plan.id))}>{t("ctl.plan.approve")}</Btn>
          </div>
        </>
      )}
      {!open && <p className="text-xs font-semibold text-mute">{t(`ctl.plan.status.${plan.status}`)}</p>}
      <div className="mt-2"><ErrorLine msg={err} /></div>
    </div>
  );
}
