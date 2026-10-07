"use client";
import { useEffect, useMemo, useState } from "react";
import { useCost } from "@/lib/cost";
import { fmtDateTime, fmtPercent, useT } from "@/lib/i18n";
import { useStore } from "@/lib/store";
import { Btn, Card, Empty, useAgentColor, useAgentName } from "../ui";
import { usd } from "./format";

const TABS = ["agent", "task", "tool", "operation", "model"] as const;
type Tab = (typeof TABS)[number];

interface Row { id: string; label: string; sub?: string; cost: number; calls: number; color?: string }

/** Spend breakdown by agent / task / tool / operation / model, from the usage ledger. */
export function CostBreakdownPanel() {
  const { t } = useT();
  const name = useAgentName();
  const color = useAgentColor();
  const [tab, setTab] = useState<Tab>("agent");
  const [scope, setScope] = useState("");
  const breakdown = useCost((s) => s.breakdown);
  const loadBreakdown = useCost((s) => s.loadBreakdown);
  const requests = useStore((s) => s.requests);
  const totalCost = useStore((s) => s.metrics?.cost_usd ?? 0);

  // Refresh whenever the scope changes or the org cost moves (metrics.updated events).
  useEffect(() => { void loadBreakdown(scope || undefined); }, [scope, totalCost, loadBreakdown]);

  const rows: Row[] = useMemo(() => {
    if (!breakdown) return [];
    switch (tab) {
      case "agent": return breakdown.by_agent.map((a) => ({ id: a.agent_id, label: name(a.agent_id), cost: a.cost_usd, calls: a.calls, color: color(a.agent_id) }));
      case "task": return breakdown.by_task.map((x) => ({ id: x.task_id, label: x.title || x.task_id, sub: name(x.agent_id), cost: x.cost_usd, calls: x.calls, color: color(x.agent_id) }));
      case "tool": return breakdown.by_tool.map((x) => ({ id: x.tool, label: x.tool, cost: x.cost_usd, calls: x.calls }));
      case "operation": return breakdown.by_operation.map((x) => ({ id: x.kind, label: t(`cost.breakdown.op.${x.kind}`), cost: x.cost_usd, calls: x.calls }));
      case "model": return breakdown.by_model.map((x) => ({ id: x.model, label: x.model, cost: x.cost_usd, calls: x.calls }));
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [breakdown, tab, t]);

  const max = Math.max(0.000001, ...rows.map((r) => r.cost));
  const total = breakdown?.total_usd ?? 0;
  const reqList = Object.values(requests).sort((a, b) => +new Date(b.created_at) - +new Date(a.created_at));

  return (
    <Card title={t("cost.breakdown.title")}
      right={
        <select data-testid="cost-breakdown-scope" value={scope} onChange={(e) => setScope(e.target.value)} aria-label={t("cost.breakdown.scope")}
          className="max-w-[220px] rounded-md border border-line bg-panel2 px-2 py-1 text-[11px] text-ink">
          <option value="">{t("cost.breakdown.scopeAll")}</option>
          {reqList.map((r) => <option key={r.id} value={r.id}>{r.text.slice(0, 40)}</option>)}
        </select>
      }>
      <div data-testid="cost-breakdown" className="space-y-3 p-4">
        <div className="flex flex-wrap items-end justify-between gap-2">
          <div>
            <div className="font-mono text-2xl font-semibold text-ink" data-testid="cost-breakdown-total">{usd(total)}</div>
            <div className="text-[11px] text-mute">{t("cost.breakdown.calls", { count: breakdown?.calls ?? 0 })}</div>
          </div>
          <div role="tablist" className="flex flex-wrap gap-1">
            {TABS.map((k) => (
              <button key={k} role="tab" aria-selected={tab === k} data-testid={`cost-tab-${k}`} onClick={() => setTab(k)}
                className={`rounded-md border px-3 py-1 text-[11px] font-semibold transition ${tab === k ? "border-accent/30 bg-accent-soft text-accent" : "border-line bg-panel2 text-mute hover:text-ink"}`}>
                {t(`cost.breakdown.by.${k}`)}
              </button>
            ))}
          </div>
        </div>

        {rows.length === 0 ? <Empty>{t("cost.breakdown.empty")}</Empty> : (
          <table className="w-full text-[12px]">
            <thead>
              <tr className="text-left text-[10px] uppercase tracking-wide text-mute">
                <th className="pb-1 font-semibold">{t("cost.breakdown.col.name")}</th>
                <th className="w-[34%] pb-1" />
                <th className="pb-1 text-right font-semibold">{t("cost.breakdown.col.calls")}</th>
                <th className="pb-1 text-right font-semibold">{t("cost.breakdown.col.cost")}</th>
                <th className="pb-1 text-right font-semibold">{t("cost.breakdown.col.share")}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <tr key={r.id} data-testid={`cost-row-${tab}-${r.id}`} className="border-t border-line/60">
                  <td className="py-1.5 pr-2 text-ink">
                    <div className="line-clamp-1">{r.label}</div>
                    {r.sub && <div className="text-[10px] text-mute">{r.sub}</div>}
                  </td>
                  <td className="py-1.5 pr-3">
                    <div className="h-2 overflow-hidden rounded bg-mute/[0.08]">
                      <div className="h-full rounded" style={{ width: `${(r.cost / max) * 100}%`, background: r.color || "#3451b2" }} />
                    </div>
                  </td>
                  <td className="py-1.5 text-right font-mono text-mute">{r.calls}</td>
                  <td className="py-1.5 text-right font-mono text-ink">{usd(r.cost)}</td>
                  <td className="py-1.5 text-right font-mono text-mute">{total > 0 ? fmtPercent((r.cost / total) * 100, 0) : "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}

        {tab === "tool" && breakdown && rows.length > 0 && (
          <p className="text-[11px] text-mute">{t("cost.breakdown.toolNote", { llmOnly: usd(breakdown.llm_only_usd) })}</p>
        )}
        {breakdown && breakdown.untracked_usd > 0 && (
          <p data-testid="cost-untracked" className="text-[11px] text-amber-700">{t("cost.breakdown.untracked", { amount: usd(breakdown.untracked_usd) })}</p>
        )}
      </div>
    </Card>
  );
}

/** Budget limits: org budget, defaults, and the editable monthly cap per agent. */
export function BudgetLimitsPanel() {
  const { t } = useT();
  const name = useAgentName();
  const color = useAgentColor();
  const status = useCost((s) => s.status);
  const loadStatus = useCost((s) => s.loadStatus);
  const setAgentCap = useCost((s) => s.setAgentCap);
  const totalCost = useStore((s) => s.metrics?.cost_usd ?? 0);
  const [edit, setEdit] = useState<Record<string, string>>({});
  const [saving, setSaving] = useState("");
  const [err, setErr] = useState("");

  useEffect(() => { void loadStatus(); }, [totalCost, loadStatus]);
  if (!status) return null;

  const none = t("cost.limits.none");
  const save = async (agentId: string) => {
    const raw = (edit[agentId] ?? "").trim();
    const v = raw === "" ? 0 : Number(raw.replace(",", "."));
    if (!Number.isFinite(v) || v < 0) { setErr(t("cost.limits.invalid")); return; }
    setSaving(agentId); setErr("");
    try {
      await setAgentCap(agentId, v);
      setEdit((e) => Object.fromEntries(Object.entries(e).filter(([k]) => k !== agentId)));
    } catch { setErr(t("cost.limits.error")); }
    setSaving("");
  };

  return (
    <Card title={t("cost.limits.title")}>
      <div data-testid="cost-limits" className="space-y-3 p-4 text-[12px]">
        <div className="text-ink">
          {t("cost.limits.org", { used: usd(status.org.used_usd), budget: status.org.budget_usd > 0 ? usd(status.org.budget_usd) : none })}
        </div>
        <div className="text-[11px] text-mute">
          {t("cost.limits.defaults", {
            request: status.defaults.request_cap_usd > 0 ? usd(status.defaults.request_cap_usd) : none,
            agent: status.defaults.agent_cap_usd > 0 ? usd(status.defaults.agent_cap_usd) : none,
            threshold: status.defaults.confirm_threshold_usd > 0 ? usd(status.defaults.confirm_threshold_usd) : none,
          })}
        </div>
        <div className="text-[11px] text-mute">{t("cost.limits.period", { date: fmtDateTime(status.defaults.period_start) })}</div>
        <table className="w-full">
          <thead>
            <tr className="text-left text-[10px] uppercase tracking-wide text-mute">
              <th className="pb-1 font-semibold">{t("cost.estimate.agent")}</th>
              <th className="pb-1 text-right font-semibold">{t("cost.limits.spent")}</th>
              <th className="pb-1 text-right font-semibold">{t("cost.limits.cap")}</th>
              <th className="pb-1" />
            </tr>
          </thead>
          <tbody>
            {status.agents.map((a) => {
              const draft = edit[a.agent_id];
              const shown = draft ?? (a.cap_usd > 0 ? String(a.cap_usd) : "");
              const pct = a.cap_usd > 0 ? Math.min(100, (a.spent_usd / a.cap_usd) * 100) : 0;
              return (
                <tr key={a.agent_id} data-testid={`cost-limit-row-${a.agent_id}`} className="border-t border-line/60">
                  <td className="py-1.5 pr-2">
                    <span className="mr-1.5 inline-block h-2 w-2 rounded-full" style={{ background: color(a.agent_id) }} />
                    <span className="text-ink">{name(a.agent_id)}</span>
                    {a.paused && <span className="ml-2 rounded bg-red-500/15 px-1.5 py-[1px] text-[10px] font-semibold uppercase text-red-700">{t("cost.limits.paused")}</span>}
                  </td>
                  <td className="py-1.5 text-right font-mono text-mute">
                    {usd(a.spent_usd)}
                    {a.cap_usd > 0 && <span className={pct >= 80 ? "ml-1 text-amber-700" : "ml-1"}>({fmtPercent(pct, 0)})</span>}
                  </td>
                  <td className="py-1.5 text-right">
                    <input data-testid={`cost-limit-input-${a.agent_id}`} inputMode="decimal" value={shown} placeholder={none}
                      onChange={(e) => setEdit((s) => ({ ...s, [a.agent_id]: e.target.value }))}
                      aria-label={t("cost.limits.capFor", { agent: name(a.agent_id) })}
                      className="w-24 rounded-md border border-line-strong bg-panel px-2 py-1 text-right font-mono text-[12px] text-ink outline-none focus:border-accent" />
                  </td>
                  <td className="py-1.5 pl-2 text-right">
                    <Btn data-testid={`cost-limit-save-${a.agent_id}`} kind="ghost" disabled={draft === undefined || saving === a.agent_id} onClick={() => save(a.agent_id)}>{t("cost.limits.save")}</Btn>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
        {err && <div role="alert" className="text-red-700">{err}</div>}
      </div>
    </Card>
  );
}
