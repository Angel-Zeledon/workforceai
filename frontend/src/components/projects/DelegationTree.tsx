"use client";
import { useT } from "@/lib/i18n";
import { leaves } from "@/lib/projects/calc";
import { useProjects } from "@/lib/projects/store";
import type { ProjectNode } from "@/lib/projects/types";
import { Card, Empty, useAgentName } from "../ui";
import { AgentChip, StatePill, useNodeTitle } from "./shared";

const MAX_DEPTH = 5;

/** Delegation tree (depth <= 5): who delegated to whom. Roots are depth-1 tasks; subtasks hang from the task that created them. */
export function DelegationTree() {
  const { t } = useT();
  const detail = useProjects((s) => s.detail)!;
  const ls = leaves(detail.nodes);
  const roots = ls.filter((n) => n.delegation_depth === 1);
  const withKids = roots.filter((r) => ls.some((c) => c.parent_id === r.id));
  const rest = roots.filter((r) => !withKids.includes(r));
  return (
    <Card title={t("pv.delegation.title")} right={<span className="text-[10px] text-mute">{t("pv.delegation.max", { max: MAX_DEPTH })}</span>}>
      {!withKids.length && <div className="mb-3"><Empty>{t("pv.delegation.none")}</Empty></div>}
      <div className="space-y-1">
        {withKids.map((r) => <Branch key={r.id} n={r} all={ls} />)}
        {rest.length > 0 && (
          <details className="pt-2">
            <summary className="cursor-pointer text-[11px] font-semibold text-mute">{t("pv.delegation.leaf", { count: rest.length })}</summary>
            <div className="mt-1 space-y-1">{rest.map((r) => <Branch key={r.id} n={r} all={ls} />)}</div>
          </details>
        )}
      </div>
    </Card>
  );
}

function Branch({ n, all }: { n: ProjectNode; all: ProjectNode[] }) {
  const { t } = useT();
  const title = useNodeTitle();
  const select = useProjects((s) => s.selectNode);
  const agentName = useAgentName();
  const kids = all.filter((c) => c.parent_id === n.id);
  const d = n.delegation_depth;
  return (
    <div style={{ marginLeft: (d - 1) * 22 }}>
      <button type="button" onClick={() => select(n.id)} className="flex w-full items-center gap-2 rounded-xl border border-line bg-panel2 px-2.5 py-1.5 text-left transition hover:border-line-strong">
        {d > 1 && <span className="text-mute">↳ {t("pv.delegation.delegated")}</span>}
        <span data-testid="depth-badge" data-depth={d} className="rounded px-1.5 py-[1px] font-mono text-[10px] font-semibold"
          style={{ background: d >= 5 ? "#a6323222" : d === 4 ? "#a8620822" : "#64748b22", color: d >= 5 ? "#a63232" : d === 4 ? "#a86208" : "#3e495a" }}>{d}/{MAX_DEPTH}</span>
        {d >= MAX_DEPTH && <span data-testid="depth-limit-badge" className="rounded bg-red-500/15 px-1.5 text-[10px] font-semibold text-red-700">{t("pv.delegation.limit")}</span>}
        <AgentChip id={n.agent_id} />
        <span className="min-w-0 flex-1 truncate text-[12px] font-semibold text-ink">{title(n)}</span>
        {n.delegation_chain.length > 1 && <span className="hidden text-[10px] text-mute md:block">{n.delegation_chain.map((a) => agentName(a).split(" ")[0]).join(" › ")}</span>}
        <StatePill state={n.state} retrying={n.state === "ready" && n.attempt > 0} />
      </button>
      {kids.map((k) => <Branch key={k.id} n={k} all={all} />)}
    </div>
  );
}
