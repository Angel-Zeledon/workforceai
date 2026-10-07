"use client";
import { fmtNumber, fmtTime, useT } from "@/lib/i18n";
import { fmtDuration, isDoneState } from "@/lib/projects/calc";
import { useProjects } from "@/lib/projects/store";
import { Btn, Card, Empty, Progress, RiskBadge, useAgentName } from "../ui";
import { CRITICAL_COLOR, Kpi, LIGHT_COLOR, StatePill, fmtMoney, useNodeTitle } from "./shared";

export function HealthView() {
  const { t } = useT();
  const detail = useProjects((s) => s.detail)!;
  const h = useProjects((s) => s.health);
  const decide = useProjects((s) => s.decide);
  const setTab = useProjects((s) => s.setTab);
  const selectNode = useProjects((s) => s.selectNode);
  const title = useNodeTitle();
  const agentName = useAgentName();
  if (!h) return <div className="text-sm text-mute">…</div>;
  const nodes = new Map(detail.nodes.map((n) => [n.id, n]));
  const pending = detail.approvals.filter((a) => a.status === "pending");
  const b = h.budget;
  const max = Math.max(b.limit_usd, b.forecast_p90_usd, b.forecast_at_completion_usd, 0.000001);
  const slip = h.schedule.slip_seconds_p50;
  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <Kpi label={t("pv.health.light")} testId="health-light" attrs={{ "data-light": h.light }} color={LIGHT_COLOR[h.light]} value={t(`pv.light.${h.light}`)}
          sub={t("pv.health.progress", { pct: fmtNumber(h.progress.weighted_pct * 100, 0) })} />
        <Kpi label={t("pv.health.eta")} testId="health-eta-p50" value={h.schedule.eta_p50 ? fmtTime(h.schedule.eta_p50) : "—"}
          sub={slip === null ? t("pv.health.noDeadline") : slip > 0 ? t("pv.health.late", { d: fmtDuration(slip) }) : t("pv.health.early", { d: fmtDuration(-slip) })}
          color={slip !== null && slip > 0 ? "#a63232" : undefined} />
        <Kpi label={t("pv.health.budget")} testId="health-budget-forecast" attrs={{ "data-forecast": b.forecast_at_completion_usd }}
          value={fmtMoney(b.forecast_at_completion_usd)} sub={t("pv.health.ofLimit", { spent: fmtMoney(b.spent_usd), limit: fmtMoney(b.limit_usd) })}
          color={b.warning === "over" ? "#a63232" : b.warning === "p90_near_limit" ? "#a86208" : undefined} />
        <Kpi label={t("pv.health.waiting")} testId="health-waiting-human" attrs={{ "data-count": h.waiting_human.count }} value={h.waiting_human.count}
          sub={h.waiting_human.count ? t("pv.health.oldest", { d: fmtDuration(h.waiting_human.oldest_age_seconds) }) : t("pv.health.nothing")}
          color={h.waiting_human.count ? "#a86208" : undefined} />
      </div>

      <Card title={t("pv.health.critical")} right={<span className="font-mono text-[11px] text-mute">{fmtDuration(h.critical_path.length_seconds)}</span>}>
        <div data-testid="critical-path-strip" className="flex flex-wrap items-center gap-1.5">
          {h.critical_path.node_ids.length === 0 && <span className="text-[11px] text-mute">{t("pv.health.noCritical")}</span>}
          {h.critical_path.node_ids.map((id, i) => {
            const n = nodes.get(id); if (!n) return null;
            return (
              <span key={id} className="flex items-center gap-1.5">
                {i > 0 && <span className="text-mute">›</span>}
                <button type="button" data-testid={`cp-node-${id}`} onClick={() => { selectNode(id); setTab("map"); }}
                  className="rounded-md border bg-panel2 px-2.5 py-1 text-[11px] font-semibold text-ink transition hover:brightness-95" style={{ borderColor: CRITICAL_COLOR }}>
                  {title(n)}
                </button>
              </span>
            );
          })}
        </div>
      </Card>

      <div className="grid gap-4 xl:grid-cols-2">
        <Card title={t("pv.health.forYou")}>
          {pending.length ? (
            <div className="space-y-2">
              {pending.map((a) => (
                <div key={a.id} data-testid={`project-approval-${a.id}`} className="rounded-xl border border-line bg-panel2 p-3">
                  <div className="flex items-start justify-between gap-2">
                    <div className="text-[12px] font-semibold text-ink">{a.title}</div>
                    <RiskBadge risk={a.risk} />
                  </div>
                  <div className="mt-0.5 text-[11px] text-mute">{a.details}</div>
                  <div className="mt-2 flex gap-2">
                    <Btn kind="ok" onClick={() => decide(a.id, "approve")}>{t("pv.approve")}</Btn>
                    <Btn kind="danger" onClick={() => decide(a.id, "reject")}>{t("pv.reject")}</Btn>
                  </div>
                </div>
              ))}
            </div>
          ) : <Empty>{t("pv.health.nothing")}</Empty>}
        </Card>

        <Card title={t("pv.health.budgetChart")}>
          <div className="space-y-2.5 text-[11px]">
            {([
              ["spent", b.spent_usd, "#2f7d55"], ["forecast", b.forecast_at_completion_usd, "#2b6cb0"],
              ["p90", b.forecast_p90_usd, "#7c6bb0"], ["limit", b.limit_usd, "#a86208"],
            ] as const).map(([k, v, c]) => (
              <div key={k}>
                <div className="mb-0.5 flex justify-between"><span className="text-mute">{t(`pv.health.bar.${k}`)}</span><span className="font-mono font-semibold text-ink">{fmtMoney(v)}</span></div>
                <Progress value={(v / max) * 100} color={c} />
              </div>
            ))}
          </div>
        </Card>
      </div>

      <div className="grid gap-4 xl:grid-cols-2">
        <Card title={t("pv.health.byObjective")}>
          <div className="space-y-3">
            {h.by_objective.map((o) => {
              const obj = detail.objectives.find((x) => x.id === o.id);
              return (
                <div key={o.id}>
                  <div className="mb-1 flex items-center justify-between gap-2">
                    <span className="truncate text-[12px] font-semibold text-ink">{obj ? title(obj) : o.id}</span>
                    <span className="flex items-center gap-2"><StatePill state={o.state as never} /><span className="font-mono text-[11px] text-mute">{fmtNumber(o.pct * 100, 0)}%</span></span>
                  </div>
                  <Progress value={o.pct * 100} color={o.state === "done" ? "#2f7d55" : "#2b6cb0"} />
                </div>
              );
            })}
          </div>
        </Card>

        <Card title={t("pv.health.risks")}>
          {h.risks.length ? (
            <ul className="space-y-1.5 text-[12px] text-ink">
              {h.risks.map((r, i) => (
                <li key={i} className="flex items-start gap-2 rounded-xl bg-amber-500/10 px-3 py-2">
                  <span>⚠</span>
                  <span>
                    {t(`pv.risk.${r.kind}`, { count: r.count, agent: r.agent_id ? agentName(r.agent_id) : "" })}
                    <span className="block text-[10px] text-mute">{r.node_ids.slice(0, 3).map((id) => { const n = nodes.get(id); return n && !isDoneState(n.state) ? title(n) : null; }).filter(Boolean).join(" · ")}</span>
                  </span>
                </li>
              ))}
            </ul>
          ) : <Empty>{t("pv.health.noRisks")}</Empty>}
        </Card>
      </div>
    </div>
  );
}
