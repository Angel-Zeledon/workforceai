"use client";
import { useEffect, useState } from "react";
import { fmtNumber, useT } from "@/lib/i18n";
import { useCan } from "@/lib/session";
import { useStore } from "@/lib/store";
import { useProjects } from "@/lib/projects/store";
import type { PlanChange } from "@/lib/projects/types";
import { Btn, Card, Empty, RiskBadge } from "../ui";
import { fmtMoney } from "./shared";

const STATUS_STYLE: Record<PlanChange["status"], string> = {
  pending: "bg-amber-500/15 text-amber-800", approved: "bg-sky-500/15 text-sky-800", applied: "bg-emerald-500/15 text-emerald-800", rejected: "bg-red-500/10 text-red-700",
};

/** Plan changes of a running project: proposals with their diff summary (added / replaced / removed nodes, cost and time delta), approve / reject by a human, and a direct "add task". Agents only propose. */
export function PlanChangesPanel() {
  const { t } = useT();
  const changes = useProjects((s) => s.changes);
  const detail = useProjects((s) => s.detail)!;
  const loadChanges = useProjects((s) => s.loadChanges);
  const decideChange = useProjects((s) => s.decideChange);
  const proposeChange = useProjects((s) => s.proposeChange);
  const canDecide = useCan("approvals:decide");
  const canManage = useCan("tasks:manage");
  const agentOrder = useStore((s) => s.agentOrder);
  const agents = useStore((s) => s.agents);
  const [title, setTitle] = useState("");
  const [agent, setAgent] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  useEffect(() => { loadChanges(); }, [loadChanges, detail.structure_version]);
  const nodeTitle = (id?: string) => detail.nodes.find((n) => n.id === id)?.title ?? id ?? "";
  const run = async (fn: () => Promise<void>) => { setBusy(true); setErr(null); try { await fn(); } catch (e) { setErr(e instanceof Error ? e.message : String(e)); } finally { setBusy(false); } };
  const sorted = [...changes].sort((a, b) => (a.status === "pending" ? 0 : 1) - (b.status === "pending" ? 0 : 1) || b.created_at.localeCompare(a.created_at));
  const chosen = agent || agentOrder[0] || "";

  return (
    <div data-testid="plan-changes" className="space-y-4">
      <div className="text-[11px] text-mute">{t("pv.changes.hint")}</div>
      {sorted.length === 0 && <Empty>{t("pv.changes.none")}</Empty>}
      {sorted.map((c) => {
        const im = c.impact;
        return (
          <Card key={c.id} title={<span data-testid={`plan-change-${c.id}`} data-status={c.status}>{c.reason || t(`pv.changes.source.${c.source}`)}</span>}
            right={<span className="flex items-center gap-2"><RiskBadge risk={c.risk} /><span className={`rounded-full px-2 py-0.5 text-[10px] font-semibold ${STATUS_STYLE[c.status]}`}>{t(`pv.changes.status.${c.status}`)}</span></span>}>
            <div className="space-y-2 text-[12px] text-ink">
              <div className="text-[11px] text-mute">
                {t(`pv.changes.by.${c.proposed_by}`, { actor: c.actor })} · {t(`pv.changes.source.${c.source}`)}{c.source_node_id ? ` · ${nodeTitle(c.source_node_id)}` : ""}
              </div>
              <div data-testid={`plan-change-diff-${c.id}`} className="flex flex-wrap gap-2 text-[11px] font-semibold">
                <span className="rounded-md bg-emerald-500/10 px-2 py-0.5 text-emerald-800">{t("pv.changes.added", { n: im.added })}</span>
                <span className="rounded-md bg-sky-500/10 px-2 py-0.5 text-sky-800">{t("pv.changes.replaced", { n: im.replaced })}</span>
                <span className="rounded-md bg-red-500/10 px-2 py-0.5 text-red-700">{t("pv.changes.removed", { n: im.removed })}</span>
                {im.updated > 0 && <span className="rounded-md bg-panel2 px-2 py-0.5 text-mute">{t("pv.changes.updated", { n: im.updated })}</span>}
                <span className="rounded-md bg-panel2 px-2 py-0.5 font-mono text-ink">{t("pv.changes.cost", { v: `${im.est_cost_delta_usd >= 0 ? "+" : "-"}${fmtMoney(Math.abs(im.est_cost_delta_usd))}` })}</span>
                <span className="rounded-md bg-panel2 px-2 py-0.5 font-mono text-ink">{t("pv.changes.time", { v: `${im.est_seconds_delta >= 0 ? "+" : "-"}${fmtNumber(Math.abs(im.est_seconds_delta), 0)} s` })}</span>
              </div>
              {im.exceeds_budget && <div className="rounded-lg bg-red-500/10 px-2 py-1 text-[11px] text-red-700">{t("pv.changes.exceeds", { projected: fmtMoney(im.projected_cost_usd), budget: fmtMoney(im.budget_usd) })}</div>}
              <ul className="space-y-0.5 text-[11px] text-mute">
                {c.ops.map((op, i) => (
                  <li key={i}>{t(`pv.changes.op.${op.op}`)}{op.node_id ? `: ${nodeTitle(op.node_id)}` : ""}{op.tasks?.length ? ` → ${op.tasks.map((x) => x.title).join("; ")}` : ""}</li>
                ))}
              </ul>
              {c.error && <div className="text-[11px] text-red-700">{t("pv.changes.error", { error: c.error })}</div>}
              {c.status === "pending" && (
                canDecide ? (
                  <div className="flex items-center gap-2">
                    <Btn kind="ok" disabled={busy} data-testid={`plan-change-approve-${c.id}`} onClick={() => run(() => decideChange(c.id, "approve"))}>{t("pv.approve")}</Btn>
                    <Btn kind="danger" disabled={busy} data-testid={`plan-change-reject-${c.id}`} onClick={() => run(() => decideChange(c.id, "reject"))}>{t("pv.reject")}</Btn>
                    {(c.required_approvals ?? 1) > 1 && <span className="text-[10px] text-mute">{t("pv.changes.double", { got: c.approvals ?? 0, need: c.required_approvals ?? 2 })}</span>}
                  </div>
                ) : <div className="text-[11px] text-mute">{t("approvals.noPermission")}</div>
              )}
            </div>
          </Card>
        );
      })}
      {canManage && (
        <Card title={t("pv.changes.addTitle")}>
          <div className="flex flex-wrap items-center gap-2">
            <input data-testid="plan-change-new-title" value={title} onChange={(e) => setTitle(e.target.value)} maxLength={300} placeholder={t("pv.changes.newTask")} className="min-w-[220px] flex-1 rounded-lg border border-line bg-panel px-2 py-1 text-[12px] text-ink" />
            <select data-testid="plan-change-new-agent" value={chosen} onChange={(e) => setAgent(e.target.value)} className="rounded-lg border border-line bg-panel px-2 py-1 text-[12px] text-ink">
              {agentOrder.map((a) => <option key={a} value={a}>{(agents[a]?.name ?? a).split(" ")[0]}</option>)}
            </select>
            <Btn kind="ok" disabled={busy || !title.trim() || !chosen} data-testid="plan-change-new-submit"
              onClick={() => run(async () => { await proposeChange(t("pv.changes.manualReason"), [{ op: "add_task", tasks: [{ key: "n1", title: title.trim(), agent_id: chosen }] }]); setTitle(""); })}>{t("pv.changes.add")}</Btn>
          </div>
          <div className="mt-1 text-[10px] text-mute">{t("pv.changes.addHint")}</div>
          {err && <div data-testid="plan-change-error" className="mt-1 text-[11px] text-red-700">{err}</div>}
        </Card>
      )}
    </div>
  );
}
