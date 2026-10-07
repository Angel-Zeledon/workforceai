"use client";
import { useState } from "react";
import { fmtNumber, useT } from "@/lib/i18n";
import { leaves } from "@/lib/projects/calc";
import { useProjects } from "@/lib/projects/store";
import { Btn, Card, Progress, useAgentName } from "../ui";
import { Kpi, fmtMoney, useNodeTitle } from "./shared";

/** Spend vs budget and breakdowns by objective, agent and node kind (client-side from node costs). */
export function CostsView() {
  const { t } = useT();
  const detail = useProjects((s) => s.detail)!;
  const setBudget = useProjects((s) => s.setBudget);
  const title = useNodeTitle();
  const agentName = useAgentName();
  const p = detail.project;
  const [edit, setEdit] = useState("");
  const ls = leaves(detail.nodes);
  const spent = ls.reduce((s, n) => s + n.cost_usd, 0);
  const pct = p.budget_usd > 0 ? (spent / p.budget_usd) * 100 : 0;
  const by = (key: (n: (typeof ls)[number]) => string | null) => {
    const m = new Map<string, { spent: number; est: number }>();
    for (const n of ls) { const k = key(n); if (!k) continue; const v = m.get(k) ?? { spent: 0, est: 0 }; v.spent += n.cost_usd; v.est += n.est_cost_usd; m.set(k, v); }
    return Array.from(m, ([k, v]) => ({ k, ...v })).sort((a, b) => b.spent - a.spent);
  };
  const rows = [
    { title: t("pv.costs.byObjective"), items: by((n) => n.objective_id).map((r) => ({ ...r, label: title(detail.objectives.find((o) => o.id === r.k) ?? { title: r.k }) })) },
    { title: t("pv.costs.byAgent"), items: by((n) => n.agent_id).map((r) => ({ ...r, label: agentName(r.k) })) },
    { title: t("pv.costs.byKind"), items: by((n) => n.kind).map((r) => ({ ...r, label: t(`pv.kind.${r.k}`) })) },
  ];
  const save = async () => { const v = Number(edit); if (v > 0) { await setBudget(v); setEdit(""); } };
  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <Kpi label={t("pv.costs.spent")} value={fmtMoney(spent)} sub={t("pv.costs.pctOfBudget", { pct: fmtNumber(pct, 0) })} />
        <Kpi label={t("pv.costs.budget")} value={fmtMoney(p.budget_usd)} />
        <Kpi label={t("pv.costs.estimated")} value={fmtMoney(detail.estimate?.total.p50_usd ?? 0)} sub={t("pv.costs.estimatedP90", { usd: fmtMoney(detail.estimate?.total.p90_usd ?? 0) })} />
        <Card className="flex flex-col justify-center">
          <label className="text-[10px] font-semibold uppercase tracking-[0.08em] text-mute">{t("pv.costs.raise")}</label>
          <div className="mt-1 flex gap-1.5">
            <input data-testid="costs-budget-input" type="number" min="0" step="0.001" value={edit} onChange={(e) => setEdit(e.target.value)} className="min-w-0 flex-1 rounded-xl border border-line-strong bg-panel px-2 py-1 font-mono text-xs text-ink outline-none focus:border-accent" />
            <Btn data-testid="costs-budget-save" kind="primary" disabled={!(Number(edit) > 0)} onClick={save}>{t("pv.costs.save")}</Btn>
          </div>
        </Card>
      </div>
      <Progress value={pct} color={pct > 95 ? "#a63232" : pct > 80 ? "#a86208" : "#2f7d55"} />
      <div className="grid gap-4 xl:grid-cols-3">
        {rows.map((r) => (
          <Card key={r.title} title={r.title}>
            <div className="space-y-2.5">
              {r.items.map((it) => (
                <div key={it.k}>
                  <div className="mb-0.5 flex justify-between gap-2 text-[11px]"><span className="truncate text-ink">{it.label}</span><span className="font-mono font-semibold text-ink">{fmtMoney(it.spent)}</span></div>
                  <Progress value={it.est > 0 ? (it.spent / it.est) * 100 : 0} color="#2b6cb0" />
                </div>
              ))}
            </div>
          </Card>
        ))}
      </div>
    </div>
  );
}
