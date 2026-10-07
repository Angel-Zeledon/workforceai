"use client";
import { useEffect } from "react";
import { useCost } from "@/lib/cost";
import { useT } from "@/lib/i18n";
import { useStore } from "@/lib/store";
import { Card, useAgentName } from "../ui";
import { usd, usdRange } from "./format";

/** The pre-execution estimate of one request: a range per task and in total, never a single number. */
export function EstimateCard({ requestId, compact = false }: { requestId: string; compact?: boolean }) {
  const { t } = useT();
  const name = useAgentName();
  const est = useCost((s) => s.estimates[requestId]);
  const loadEstimate = useCost((s) => s.loadEstimate);
  const spent = useStore((s) => s.requests[requestId]?.cost_usd ?? 0);
  useEffect(() => { if (!est) void loadEstimate(requestId); }, [est, requestId, loadEstimate]);
  if (!est) return null;
  const body = (
    <div data-testid="cost-estimate" data-basis={est.basis} className="space-y-3 text-[12px]">
      <div>
        <div className="text-[10px] font-semibold uppercase tracking-[0.08em] text-mute">{t("cost.estimate.total")}</div>
        <div data-testid="cost-estimate-total" className="font-mono text-2xl font-semibold text-ink">{usdRange(est.total.min_usd, est.total.max_usd)}</div>
        <div className="mt-0.5 text-[11px] text-mute">{t("cost.estimate.rangeNote")}</div>
        <div className="mt-0.5 text-[11px] text-mute">{t(`cost.estimate.basis.${est.basis}`)}</div>
      </div>
      {!compact && (
        <table className="w-full">
          <thead>
            <tr className="text-left text-[10px] uppercase tracking-wide text-mute">
              <th className="pb-1 font-semibold">{t("cost.estimate.task")}</th>
              <th className="pb-1 font-semibold">{t("cost.estimate.agent")}</th>
              <th className="pb-1 text-right font-semibold">{t("cost.estimate.range")}</th>
            </tr>
          </thead>
          <tbody>
            {est.tasks.map((x) => (
              <tr key={x.task_id} data-testid={`cost-estimate-task-${x.task_id}`} className="border-t border-line/60">
                <td className="py-1.5 pr-2 text-ink">{x.title}</td>
                <td className="py-1.5 pr-2 text-mute">{name(x.agent_id)}</td>
                <td className="py-1.5 text-right font-mono text-mute">{usdRange(x.min_usd, x.max_usd)}</td>
              </tr>
            ))}
            <tr className="border-t border-line/60">
              <td className="py-1.5 pr-2 text-ink" colSpan={2}>{t("cost.estimate.synthesis")}</td>
              <td className="py-1.5 text-right font-mono text-mute">{usdRange(est.synthesis.min_usd, est.synthesis.max_usd)}</td>
            </tr>
          </tbody>
        </table>
      )}
      <div className="flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-mute">
        <span>{t("cost.estimate.spent", { amount: usd(spent) })}</span>
        {est.budget_cap_usd > 0 && <span data-testid="cost-request-cap">{t("cost.estimate.cap", { amount: usd(est.budget_cap_usd) })}</span>}
      </div>
    </div>
  );
  return compact ? body : <Card title={t("cost.estimate.title")}><div className="p-4">{body}</div></Card>;
}
