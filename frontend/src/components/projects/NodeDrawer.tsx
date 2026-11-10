"use client";
import { useState } from "react";
import { useT } from "@/lib/i18n";
import { useCan } from "@/lib/session";
import { decidersLine, itemDecidable } from "@/lib/inboxLogic";
import { explainNode, fmtDuration, isDoneState } from "@/lib/projects/calc";
import { useProjects } from "@/lib/projects/store";
import { Btn } from "../ui";
import { AuditDetail } from "../quality/AuditStamp";
import { QuestionCard } from "../approvals/QuestionCard";
import { useStore } from "@/lib/store";
import { AgentChip, StatePill, fmtMoney, useNodeTitle } from "./shared";
import { DetailPanel, useDetailUrl } from "../ui/DetailPanel";

/** The task of a project in the single side panel (components/ui/DetailPanel.tsx): facts, dependencies in/out and a
 *  deterministic "why is it blocked?". The URL says which task is open (?tarea=<id>), so Back closes it. */
export function NodeDrawer() {
  const { t } = useT();
  const canDecide = useCan("approvals:decide");
  const canRecover = useCan("tasks:manage");
  const retryNode = useProjects((s) => s.retryNode);
  const skipNode = useProjects((s) => s.skipNode);
  const replanNode = useProjects((s) => s.replanNode);
  const setTab = useProjects((s) => s.setTab);
  const [skipping, setSkipping] = useState(false);
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const run = async (fn: () => Promise<void>) => { setBusy(true); setErr(null); try { await fn(); setSkipping(false); setReason(""); } catch (e) { setErr(e instanceof Error ? e.message : String(e)); } finally { setBusy(false); } };
  const detail = useProjects((s) => s.detail)!;
  const id = useProjects((s) => s.selectedNodeId)!;
  const select = useProjects((s) => s.selectNode);
  const decide = useProjects((s) => s.decide);
  const title = useNodeTitle();
  const questions = useStore((s) => s.questions);
  const n = detail.nodes.find((x) => x.id === id);
  useDetailUrl("tarea", n ? id : null, (v) => select(v));
  if (!n) return null;
  const incoming = n.depends_on.map((d) => detail.nodes.find((x) => x.id === d)).filter(Boolean);
  const outgoing = detail.nodes.filter((x) => x.depends_on.includes(n.id));
  const blockers = isDoneState(n.state) ? [] : explainNode(detail.nodes, detail.approvals, n.id);
  const approval = detail.approvals.find((a) => a.node_id === n.id && a.status === "pending");
  const headline = approval ? "pv.explain.approval" : blockers.some((b) => b.kind === "failed") ? "pv.explain.failed" : blockers.some((b) => b.kind === "dependency" || b.kind === "approval") ? "pv.explain.dependency" : blockers.some((b) => b.kind === "agent_busy") ? "pv.explain.busy" : null;
  const Link = ({ nid }: { nid: string }) => {
    const x = detail.nodes.find((k) => k.id === nid);
    return x ? <button type="button" onClick={() => select(nid)} className="block w-full truncate rounded-lg bg-panel2 px-2 py-1 text-left text-[11px] text-ink hover:bg-mute/[0.12]">{title(x)}</button> : null;
  };
  return (
    <DetailPanel
      testId="node-drawer" label={t("detail.kind.task")} concept="task" title={title(n)} titleTestId="node-drawer-title"
      ariaLabel={t("detail.task.aria", { title: title(n) })} closeLabel={t("detail.task.close")} closeTestId="node-drawer-close" backTestId="node-drawer-back"
      onClose={() => select(null)} className="ac-pop" data={{ "data-node": n.id, "data-state": n.state }}
      meta={<><StatePill state={n.state} retrying={n.state === "ready" && n.attempt > 0} /><AgentChip id={n.agent_id} /><span className="text-[10px] font-semibold uppercase text-mute">{t(`pv.kind.${n.kind}`)}</span></>}
    >
      <div data-testid="node-drawer-body" className="min-h-0 flex-1 space-y-3 overflow-y-auto overscroll-contain p-4">
        <dl className="grid grid-cols-2 gap-2 text-[11px]">
          <Fact k={t("pv.node.attempt")} v={`${n.attempt}/${n.max_attempts}`} />
          <Fact k={t("pv.node.progress")} v={`${Math.round(n.progress)}%`} />
          <Fact k={t("pv.node.cost")} v={`${fmtMoney(n.cost_usd)} / ${fmtMoney(n.est_cost_usd)}`} />
          <Fact k={t("pv.node.est")} v={fmtDuration(n.est_seconds)} />
          <Fact k={t("pv.node.depth")} v={`${n.delegation_depth}/5`} />
          <Fact k={t("pv.node.complexity")} v={n.complexity} />
        </dl>
        {n.error && <div className="rounded-xl bg-red-500/10 px-2 py-1 text-[11px] text-red-700">{t("pv.node.error", { code: n.error })}</div>}
        {n.task_id && Object.values(questions).filter((q) => q.task_id === n.task_id && q.status === "open").map((q) => <QuestionCard key={q.id} q={q} />)}
        {n.omitted && <div data-testid="node-omitted" className="rounded-xl bg-panel2 px-2 py-1 text-[11px] text-mute">{t("pv.node.omitted")}</div>}
        {n.files && n.files.length > 0 && (
          <div data-testid="node-files" className="rounded-xl border border-line bg-panel2 p-2 text-[11px]">
            <div className="mb-1 text-[10px] font-semibold uppercase tracking-wide text-mute">{t("pv.follow.files")}</div>
            <ul className="space-y-0.5">{n.files.map((f) => <li key={f}><a href={`/files?path=${encodeURIComponent(f)}`} className="break-all text-accent hover:underline">{f}</a></li>)}</ul>
          </div>
        )}
        {n.acceptance && n.acceptance.length > 0 && (
          <div data-testid="node-acceptance" className="rounded-xl border border-line bg-panel2 p-2 text-[11px] text-ink">
            <div className="mb-1 text-[10px] font-semibold uppercase tracking-wide text-mute">{t("pv.quality.acceptance")}</div>
            <ul className="list-disc space-y-0.5 pl-4">{n.acceptance.map((c, i) => <li key={i}>{c}</li>)}</ul>
          </div>
        )}
        {n.review && (
          <div data-testid="node-review" data-verdict={n.review.verdict} className={`rounded-xl px-2 py-1 text-[11px] ${n.review.verdict === "pass" ? "bg-emerald-500/10 text-emerald-800" : n.review.verdict === "fail" ? "bg-red-500/10 text-red-700" : "bg-amber-500/10 text-amber-800"}`}>
            <div className="font-semibold">{t(`pv.quality.verdict.${n.review.verdict}`)}{n.review.reworks > 0 ? ` · ${t("pv.quality.reworks", { count: n.review.reworks })}` : ""}{n.review.rework_exhausted ? ` · ${t("pv.quality.exhausted")}` : ""}</div>
            <ul className="mt-0.5 list-disc space-y-0.5 pl-4">{n.review.reasons.map((r, i) => <li key={i}>{r}</li>)}</ul>
          </div>
        )}
        {n.audit && <AuditDetail audit={n.audit} testId="node-audit" />}
        {n.superseded && <div data-testid="node-superseded" className="rounded-xl bg-panel2 px-2 py-1 text-[11px] text-mute">{t(`pv.changes.superseded.${n.superseded}`)}</div>}
        {n.skipped && <div data-testid="node-skipped" className="rounded-xl bg-amber-500/10 px-2 py-1 text-[11px] text-amber-800">{t("pv.recover.skipped", { reason: n.skip_reason ?? "" })}</div>}
        {canRecover && (n.state === "failed" || n.state === "blocked") && (
          <div data-testid="node-recovery" className="space-y-2 rounded-xl border border-line bg-panel2 p-2">
            <div className="text-[10px] font-semibold uppercase tracking-wide text-mute">{t("pv.recover.title")}</div>
            <div className="flex flex-wrap gap-2">
              <Btn kind="ok" disabled={busy} onClick={() => run(() => retryNode(n.id))} data-testid="node-retry">{t("pv.recover.retry")}</Btn>
              {n.state === "failed" && <Btn kind="danger" disabled={busy} onClick={() => setSkipping((v) => !v)} data-testid="node-skip">{t("pv.recover.skip")}</Btn>}
              {n.state === "failed" && <Btn disabled={busy} onClick={() => run(async () => { await replanNode(n.id); setTab("changes"); })} data-testid="node-replan">{t("pv.replan.button")}</Btn>}
            </div>
            {skipping && (
              <div className="space-y-1">
                <textarea data-testid="node-skip-reason" value={reason} onChange={(e) => setReason(e.target.value)} maxLength={500} rows={2} placeholder={t("pv.recover.reason")} className="w-full rounded-lg border border-line bg-panel px-2 py-1 text-[11px] text-ink" />
                <Btn kind="danger" disabled={busy || !reason.trim()} onClick={() => run(() => skipNode(n.id, reason.trim()))} data-testid="node-skip-confirm">{t("pv.recover.skipConfirm")}</Btn>
                <div className="text-[10px] text-mute">{t("pv.recover.skipHint")}</div>
              </div>
            )}
            {n.state === "failed" && <div className="text-[10px] text-mute">{t("pv.replan.hint")}</div>}
            {err && <div data-testid="node-recovery-error" className="text-[11px] text-red-700">{err}</div>}
          </div>
        )}
        {approval && (
          <div className="rounded-xl border border-line bg-panel2 p-2">
            <div className="text-[11px] font-semibold text-ink">{approval.title}</div>
            {itemDecidable(approval, canDecide) ? <div className="mt-2 flex gap-2"><Btn kind="ok" onClick={() => decide(approval.id, "approve")}>{t("pv.approve")}</Btn><Btn kind="danger" onClick={() => decide(approval.id, "reject")}>{t("pv.reject")}</Btn></div>
              : <div className="mt-1 text-[11px] text-mute">{decidersLine(approval) ? t("approvalInbox.decidedBy", { names: decidersLine(approval) }) : t("approvals.noPermission")}</div>}
          </div>
        )}
        {headline && (
          <div className="rounded-xl bg-amber-500/10 p-2">
            <div data-testid="explain-headline" className="text-[11px] font-semibold text-amber-800">{t(headline)}</div>
            <ul className="mt-1 space-y-1">
              {blockers.filter((b) => b.node_id !== n.id).map((b) => <li key={`${b.kind}-${b.node_id}`} data-testid={`explain-root-cause-${b.node_id}`}><Link nid={b.node_id} /></li>)}
            </ul>
          </div>
        )}
        <Section label={t("pv.node.deps")} empty={t("pv.node.none")}>{incoming.map((x) => <Link key={x!.id} nid={x!.id} />)}</Section>
        <Section label={t("pv.node.dependents")} empty={t("pv.node.none")}>{outgoing.map((x) => <Link key={x.id} nid={x.id} />)}</Section>
      </div>
    </DetailPanel>
  );
}

const Fact = ({ k, v }: { k: string; v: string }) => (
  <div className="rounded-xl bg-panel2 px-2 py-1.5"><dt className="text-[9px] font-semibold uppercase tracking-wide text-mute">{k}</dt><dd className="font-mono font-semibold text-ink">{v}</dd></div>
);
function Section({ label, empty, children }: { label: string; empty: string; children: React.ReactNode[] }) {
  return (
    <div>
      <div className="mb-1 text-[10px] font-semibold uppercase tracking-wide text-mute">{label}</div>
      <div className="space-y-1">{children.length ? children : <div className="text-[11px] text-mute">{empty}</div>}</div>
    </div>
  );
}
