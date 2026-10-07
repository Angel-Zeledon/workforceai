"use client";
import { useT } from "@/lib/i18n";
import { explainNode, fmtDuration, isDoneState } from "@/lib/projects/calc";
import { useProjects } from "@/lib/projects/store";
import { Btn } from "../ui";
import { AgentChip, StatePill, fmtMoney, useNodeTitle } from "./shared";

/** Right-hand panel for the selected node: facts, dependencies in/out and a deterministic "why is it blocked?". */
export function NodeDrawer() {
  const { t } = useT();
  const detail = useProjects((s) => s.detail)!;
  const id = useProjects((s) => s.selectedNodeId)!;
  const select = useProjects((s) => s.selectNode);
  const decide = useProjects((s) => s.decide);
  const title = useNodeTitle();
  const n = detail.nodes.find((x) => x.id === id);
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
    <aside data-testid="node-drawer" className="ac-pop h-full w-[340px] shrink-0 space-y-3 overflow-y-auto border-l-2 border-line bg-panel p-4 shadow-soft">
      <div className="flex items-start justify-between gap-2">
        <h3 className="font-display text-[15px] font-semibold leading-snug text-ink">{title(n)}</h3>
        <button type="button" data-testid="node-drawer-close" onClick={() => select(null)} className="rounded-md border border-line px-2 text-xs font-semibold text-mute hover:text-ink" aria-label={t("common.close")}>×</button>
      </div>
      <div className="flex flex-wrap items-center gap-2"><StatePill state={n.state} retrying={n.state === "ready" && n.attempt > 0} /><AgentChip id={n.agent_id} /><span className="text-[10px] font-semibold uppercase text-mute">{t(`pv.kind.${n.kind}`)}</span></div>
      <dl className="grid grid-cols-2 gap-2 text-[11px]">
        <Fact k={t("pv.node.attempt")} v={`${n.attempt}/${n.max_attempts}`} />
        <Fact k={t("pv.node.progress")} v={`${Math.round(n.progress)}%`} />
        <Fact k={t("pv.node.cost")} v={`${fmtMoney(n.cost_usd)} / ${fmtMoney(n.est_cost_usd)}`} />
        <Fact k={t("pv.node.est")} v={fmtDuration(n.est_seconds)} />
        <Fact k={t("pv.node.depth")} v={`${n.delegation_depth}/5`} />
        <Fact k={t("pv.node.complexity")} v={n.complexity} />
      </dl>
      {n.error && <div className="rounded-xl bg-red-500/10 px-2 py-1 text-[11px] text-red-700">{t("pv.node.error", { code: n.error })}</div>}
      {approval && (
        <div className="rounded-xl border border-line bg-panel2 p-2">
          <div className="text-[11px] font-semibold text-ink">{approval.title}</div>
          <div className="mt-2 flex gap-2"><Btn kind="ok" onClick={() => decide(approval.id, "approve")}>{t("pv.approve")}</Btn><Btn kind="danger" onClick={() => decide(approval.id, "reject")}>{t("pv.reject")}</Btn></div>
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
    </aside>
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
