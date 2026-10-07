"use client";
import { useMemo, useState } from "react";
import { useT } from "@/lib/i18n";
import { isDoneState, isGroup, isRetrying, leaves } from "@/lib/projects/calc";
import { useProjects } from "@/lib/projects/store";
import type { ProjectNode } from "@/lib/projects/types";
import { Empty } from "../ui";
import { AgentChip, NODE_STATE_COLOR, RETRY_COLOR, fmtMoney, useNodeTitle } from "./shared";

const COLS = [
  { key: "ready", color: NODE_STATE_COLOR.ready },
  { key: "running", color: NODE_STATE_COLOR.running },
  { key: "awaiting", color: NODE_STATE_COLOR.awaiting_approval },
  { key: "waiting", color: NODE_STATE_COLOR.pending },
  { key: "retrying", color: RETRY_COLOR },
  { key: "failed", color: NODE_STATE_COLOR.failed },
  { key: "done", color: NODE_STATE_COLOR.done },
] as const;
type ColKey = (typeof COLS)[number]["key"];
const SCOPE_REQUIRED_OVER = 60;

function colOf(n: ProjectNode): ColKey {
  if (isDoneState(n.state)) return "done";
  if (isRetrying(n)) return "retrying";
  switch (n.state) {
    case "ready": return "ready";
    case "running": return "running";
    case "awaiting_approval": return "awaiting";
    case "failed": case "blocked": case "cancelled": return "failed";
    default: return "waiting";
  }
}

/** Kanban by state. Scope (objective / workflow) is mandatory once the project is big, so we never render thousands of cards. */
export function KanbanBoard() {
  const { t } = useT();
  const detail = useProjects((s) => s.detail)!;
  const select = useProjects((s) => s.selectNode);
  const title = useNodeTitle();
  const [scope, setScope] = useState("");
  const [showDone, setShowDone] = useState(false);
  const all = leaves(detail.nodes);
  const needScope = all.length > SCOPE_REQUIRED_OVER && !scope;
  const scoped = useMemo(() => {
    if (!scope) return all;
    if (scope.startsWith("o:")) return all.filter((n) => n.objective_id === scope.slice(2));
    return all.filter((n) => n.parent_id === scope.slice(2) || detail.nodes.find((p) => p.id === n.parent_id)?.parent_id === scope.slice(2));
  }, [all, scope, detail.nodes]);
  const groups = detail.nodes.filter(isGroup);

  return (
    <div className="space-y-3">
      <label className="flex items-center gap-2 text-[11px] font-semibold text-mute">{t("pv.kanban.scope")}
        <select data-testid="kanban-scope" value={scope} onChange={(e) => setScope(e.target.value)} className="rounded-xl border border-line bg-panel px-3 py-1.5 text-xs text-ink">
          <option value="">{t("pv.kanban.all")}</option>
          {detail.objectives.map((o) => (
            <optgroup key={o.id} label={title(o)}>
              <option value={`o:${o.id}`}>{title(o)}</option>
              {groups.filter((g) => g.objective_id === o.id).map((g) => <option key={g.id} value={`g:${g.id}`}>{title(g)}</option>)}
            </optgroup>
          ))}
        </select>
      </label>
      {needScope ? <div data-testid="kanban-scope-required"><Empty>{t("pv.kanban.scopeRequired")}</Empty></div> : (
        <div className="grid gap-3 md:grid-cols-3 xl:grid-cols-7">
          {COLS.map((c) => {
            const items = scoped.filter((n) => colOf(n) === c.key);
            const collapsed = c.key === "done" && !showDone;
            return (
              <div key={c.key} data-testid={`kanban-col-${c.key}`} data-count={items.length} className="min-h-[120px] rounded-xl border border-line bg-panel2/70 p-2">
                <button type="button" className="mb-2 flex w-full items-center justify-between px-1 text-left" onClick={() => c.key === "done" && setShowDone(!showDone)}>
                  <span className="flex items-center gap-1.5 text-[10px] font-semibold uppercase tracking-wide text-mute"><span className="h-2 w-2 rounded-full" style={{ background: c.color }} />{t(`pv.kanban.col.${c.key}`)}</span>
                  <span className="rounded-full bg-panel px-1.5 font-mono text-[10px] font-semibold text-ink">{items.length}</span>
                </button>
                {!collapsed && (
                  <div className="space-y-1.5">
                    {items.map((n) => (
                      <button key={n.id} type="button" data-testid={`kanban-card-${n.id}`} onClick={() => select(n.id)}
                        className="ac-pop block w-full rounded-xl border border-line bg-panel p-2 text-left shadow-pop transition hover:border-line-strong" style={{ borderLeftColor: c.color, borderLeftWidth: 4 }}>
                        <div className="text-[11.5px] font-semibold leading-snug text-ink">{title(n)}</div>
                        <div className="mt-1 flex items-center justify-between">
                          <AgentChip id={n.agent_id} small />
                          <span className="font-mono text-[10px] text-mute">{n.state === "running" ? `${Math.round(n.progress)}%` : n.attempt > 1 || isRetrying(n) ? t("pv.attempt", { n: n.attempt, max: n.max_attempts }) : fmtMoney(n.cost_usd || n.est_cost_usd)}</span>
                        </div>
                      </button>
                    ))}
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
