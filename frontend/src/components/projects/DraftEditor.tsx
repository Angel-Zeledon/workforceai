"use client";
import { useEffect, useMemo, useState } from "react";
import { useStore } from "@/lib/store";
import { useT } from "@/lib/i18n";
import { estimatePlan, fmtDuration, isGroup, leaves, validatePlan } from "@/lib/projects/calc";
import { useProjects } from "@/lib/projects/store";
import type { ProjectNode } from "@/lib/projects/types";
import { Btn, Card, Progress, useAgentName } from "../ui";
import { Kpi, fmtMoney, useNodeTitle } from "./shared";

/** Draft review: editable tree (titles, agents), live estimate (P50-P90 range), validation and launch. */
export function DraftEditor() {
  const { t } = useT();
  const detail = useProjects((s) => s.detail)!;
  const patchPlan = useProjects((s) => s.patchPlan);
  const launch = useProjects((s) => s.launch);
  const agentOrder = useStore((s) => s.agentOrder);
  const title = useNodeTitle();
  const p = detail.project;
  const planning = detail.planning;
  const count = leaves(detail.nodes).length;
  const est = useMemo(() => detail.estimate ?? estimatePlan(detail.nodes, detail.objectives), [detail.estimate, detail.nodes, detail.objectives]);
  const issues = useMemo(() => validatePlan(detail.nodes, agentOrder), [detail.nodes, agentOrder]);
  const [budget, setBudget] = useState("");
  const [ack, setAck] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  useEffect(() => { if (!planning && p.budget_usd > 0 && budget === "") setBudget(String(p.budget_usd)); }, [planning, p.budget_usd, budget]);
  const b = Number(budget);
  const under = b > 0 && b < est.total.p50_usd;
  const errors = issues.filter((i) => i.severity === "error");
  const canLaunch = !planning && !errors.length && b > 0 && (!under || ack) && !busy;
  const doLaunch = async () => {
    setBusy(true); setErr("");
    try { await launch({ approved_budget_usd: b, acknowledge_underbudget: ack }); } catch { setErr(t("pv.launch.error")); setBusy(false); }
  };

  return (
    <div data-testid="draft-editor" className="grid gap-5 xl:grid-cols-3">
      <div className="space-y-4 xl:col-span-2">
        <Card title={t("pv.draft.plan")} right={<span data-testid="draft-node-count" data-count={count} className="font-mono text-[11px] text-mute">{t("pv.card.nodes", { count })}</span>}>
          {planning && (
            <div className="mb-3">
              <div className="mb-1 text-[11px] font-semibold text-violet-700">{t("pv.draft.planning", { done: planning.done, total: planning.total })}</div>
              <Progress value={planning.total ? (planning.done / planning.total) * 100 : 0} color="#7c6bb0" />
            </div>
          )}
          <div className="space-y-4">
            {detail.objectives.map((o) => (
              <div key={o.id} data-testid={`draft-objective-${o.id}`} className="ac-pop rounded-xl border border-line bg-panel2 p-3">
                <div className="text-[13px] font-semibold text-ink">{title(o)}</div>
                {detail.nodes.filter((g) => isGroup(g) && g.objective_id === o.id).map((g) => (
                  <div key={g.id} className="mt-2">
                    <div className="text-[10px] font-semibold uppercase tracking-wide text-mute">{title(g)}</div>
                    <div className="mt-1 space-y-1">
                      {detail.nodes.filter((n) => n.parent_id === g.id).map((n) => (
                        <DraftRow key={n.id} n={n} agents={agentOrder} onChange={(fields) => patchPlan([{ op: "update", id: n.id, fields }])} />
                      ))}
                    </div>
                  </div>
                ))}
              </div>
            ))}
          </div>
        </Card>
      </div>

      <div className="space-y-4">
        <Card title={t("pv.est.title")}>
          <div className="grid grid-cols-2 gap-2">
            <Kpi label={t("pv.est.p50")} value={<span data-testid="estimate-p50" data-usd={est.total.p50_usd}>{fmtMoney(est.total.p50_usd)}</span>} />
            <Kpi label={t("pv.est.p90")} value={<span data-testid="estimate-p90" data-usd={est.total.p90_usd}>{fmtMoney(est.total.p90_usd)}</span>} />
          </div>
          <div className="mt-3 text-[11px] text-mute">
            {t("pv.est.duration", { p50: fmtDuration(est.duration.p50_seconds), p90: fmtDuration(est.duration.p90_seconds) })}
            <div>{t("pv.est.basis", { basis: t(`pv.est.basis.${est.basis}`), model: est.model, confidence: t(`pv.est.conf.${est.confidence}`) })}</div>
          </div>
          {est.warnings.map((w, i) => <div key={i} className="mt-2 rounded-xl bg-amber-500/10 px-2 py-1 text-[11px] text-amber-800">{t(w.key, { objective: title(detail.objectives.find((o) => o.id === w.params?.id) ?? { title: "" }) })}</div>)}
        </Card>

        <Card title={t("pv.validate.title")}>
          {errors.length === 0 ? (
            <div data-testid="validate-ok" className="text-[12px] font-semibold text-emerald-700">{t("pv.validate.ok")}</div>
          ) : (
            <ul className="space-y-1 text-[11px] text-red-700">
              {errors.map((i, k) => <li key={k}>{t(`pv.validate.${i.code}`)}{i.node_id ? ` · ${title(detail.nodes.find((n) => n.id === i.node_id) ?? { title: "" })}` : ""}</li>)}
            </ul>
          )}
        </Card>

        <Card title={t("pv.launch.title")}>
          <label className="block text-[11px] font-semibold text-mute">{t("pv.launch.budget")}
            <input data-testid="budget-input" type="number" min="0" step="0.001" value={budget} onChange={(e) => { setBudget(e.target.value); setAck(false); }}
              className="mt-1 w-full rounded-xl border border-line-strong bg-panel px-3 py-2 font-mono text-sm text-ink outline-none focus:border-accent" />
          </label>
          <div className="mt-1 text-[10px] text-mute">{t("pv.launch.suggested", { usd: fmtMoney(Math.ceil(est.total.p90_usd * 1.1 * 1000) / 1000) })}</div>
          {under && (
            <div data-testid="underbudget-warning" className="mt-2 rounded-xl bg-amber-500/10 p-2 text-[11px] text-amber-800">
              {t("pv.launch.underbudget", { p50: fmtMoney(est.total.p50_usd) })}
              <label className="mt-1 flex items-center gap-2 font-semibold">
                <input data-testid="launch-ack-underbudget" type="checkbox" checked={ack} onChange={(e) => setAck(e.target.checked)} />
                {t("pv.launch.ack")}
              </label>
            </div>
          )}
          {err && <div className="mt-2 text-[11px] text-red-700">{err}</div>}
          <Btn data-testid="launch-confirm" kind="primary" className="mt-3 w-full" disabled={!canLaunch} onClick={doLaunch}>{t("pv.launch.confirm")}</Btn>
        </Card>
      </div>
    </div>
  );
}

function DraftRow({ n, agents, onChange }: { n: ProjectNode; agents: string[]; onChange: (f: { title?: string; agent_id?: string }) => void }) {
  const { t } = useT();
  const title = useNodeTitle();
  const agentName = useAgentName();
  const [text, setText] = useState(title(n));
  const shown = title(n);
  useEffect(() => { setText(shown); }, [shown]);
  const human = n.kind === "gate" || n.kind === "milestone" || n.kind === "wait";
  const commit = () => { if (text !== shown) onChange({ title: text }); };
  return (
    <div className="flex items-center gap-2">
      <span className="w-14 shrink-0 text-[9px] font-semibold uppercase tracking-wide text-mute">{t(`pv.kind.${n.kind}`)}</span>
      <input data-testid="draft-node-title-input" value={text} onChange={(e) => setText(e.target.value)} onBlur={commit} onKeyDown={(e) => { if (e.key === "Enter") (e.target as HTMLInputElement).blur(); }}
        className="min-w-0 flex-1 rounded-lg border border-line-strong bg-panel px-2 py-1 text-[12px] text-ink outline-none focus:border-accent" />
      {human ? <span className="w-[110px] shrink-0 text-[11px] text-mute">{t("pv.human")}</span> : (
        <select data-testid="draft-node-agent-select" value={n.agent_id ?? ""} onChange={(e) => onChange({ agent_id: e.target.value })} className="w-[110px] shrink-0 rounded-lg border border-line-strong bg-panel px-1 py-1 text-[11px] text-ink">
          {!n.agent_id && <option value="">—</option>}
          {agents.map((a) => <option key={a} value={a}>{agentName(a).split(" ")[0]}</option>)}
        </select>
      )}
      <span className="w-16 shrink-0 text-right font-mono text-[10px] text-mute">{n.est_cost_usd ? fmtMoney(n.est_cost_usd) : "—"}</span>
    </div>
  );
}
