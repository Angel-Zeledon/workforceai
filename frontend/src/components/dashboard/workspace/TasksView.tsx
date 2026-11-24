"use client";
import { useMemo, useState } from "react";
import { useStore } from "@/lib/store";
import { useT } from "@/lib/i18n";
import { fmtTime, taskStatusLabel } from "@/lib/meta";
import { taskAuditOf } from "@/lib/auditLogic";
import type { Task } from "@/lib/types";
import { AuditDetail } from "../../quality/AuditStamp";
import { Icon } from "../../icons";
import { Empty, ReasonChip, timeAgo, useAgentName } from "../../ui";
import { Avatar } from "../../chat/ChatThread";
import { stepKind } from "./workspaceLogic";

type Filter = "all" | "active" | "waiting" | "done" | "problem";
const FILTERS: Filter[] = ["all", "active", "waiting", "done", "problem"];
const GROUPS = [
  { id: "waiting", statuses: ["awaiting_approval", "needs_input", "blocked"] },
  { id: "running", statuses: ["running"] },
  { id: "queued", statuses: ["pending"] },
  { id: "failed", statuses: ["failed"] },
  { id: "done", statuses: ["done"] },
] as const;
const FILTER_GROUPS: Record<Filter, readonly string[]> = {
  all: ["waiting", "running", "queued", "failed", "done"],
  active: ["running", "queued"],
  waiting: ["waiting"],
  done: ["done"],
  problem: ["failed", "waiting"],
};

function Mark({ status }: { status: string }) {
  const k = stepKind(status);
  const cls = k === "done" ? "bg-ok" : k === "failed" ? "bg-err" : k === "waiting" ? "bg-warn" : k === "active" ? "bg-accent" : "bg-line-strong";
  return <span aria-hidden className={`h-2 w-2 shrink-0 rounded-full ${cls}`} />;
}

function TaskDetail({ t: task }: { t: Task }) {
  const { t } = useT();
  const out = task.output;
  const audit = taskAuditOf(task);
  return (
    <div className="space-y-2 border-t border-line bg-panel2 px-4 py-3 text-[12.5px] leading-relaxed text-ink2">
      {task.description && <p className="text-ink2">{task.description}</p>}
      <ReasonChip agentId={task.agent_id} reason={task.assigned_reason} />
      {out ? (
        <div className="space-y-1">
          {audit && <AuditDetail audit={audit} testId={`task-audit-${task.id}`} />}
          <div className="font-semibold text-ink">{out.summary}</div>
          {out.findings.map((f, i) => <div key={i}>• {f}</div>)}
          {out.recommendations.length > 0 && <div className="text-mute">{t("dash.tasks.recommendations")}: {out.recommendations.join(" · ")}</div>}
          <div className="text-mute">{t("common.confidence")} {Math.round(out.confidence * 100)}%</div>
        </div>
      ) : <span className="text-mute">{t("dash.tasks.noOutput")}</span>}
    </div>
  );
}

/** Tareas: grouped by state (what waits for you first, what is done last) with plain filters; open one for its detail. */
export function TasksView() {
  const { t } = useT();
  const tasks = useStore((s) => s.tasks);
  const requests = useStore((s) => s.requests);
  const name = useAgentName();
  const [filter, setFilter] = useState<Filter>("all");
  const [request, setRequest] = useState("all");
  const [open, setOpen] = useState<string | null>(null);
  const all = useMemo(() => Object.values(tasks).filter((x) => !x.helper).sort((a, b) => +new Date(b.created_at) - +new Date(a.created_at)), [tasks]);
  const inScope = all.filter((x) => request === "all" || x.request_id === request);
  const groupOf = (x: Task) => GROUPS.find((g) => (g.statuses as readonly string[]).includes(x.status))?.id ?? "queued";
  const count = (f: Filter) => inScope.filter((x) => FILTER_GROUPS[f].includes(groupOf(x))).length;
  const sections = GROUPS.filter((g) => FILTER_GROUPS[filter].includes(g.id)).map((g) => ({ id: g.id, items: inScope.filter((x) => groupOf(x) === g.id) })).filter((s) => s.items.length);
  if (!all.length) return <Empty concept="task" how={t("dash.tasks.how")}>{t("dash.tasks.empty")}</Empty>;
  return (
    <div data-testid="tasks-view" className="mx-auto w-full max-w-4xl">
      <div className="mb-4 flex flex-wrap items-center gap-2">
        <div role="group" aria-label={t("work.tasks.filter")} className="flex flex-wrap gap-1.5">
          {FILTERS.map((f) => (
            <button key={f} type="button" data-testid={`tasks-filter-${f}`} aria-pressed={filter === f} onClick={() => setFilter(f)}
              className={`min-h-10 rounded-full border px-3 text-[13px] font-medium transition-colors sm:min-h-9 ${filter === f ? "border-accent bg-accent-soft text-accent" : "border-line bg-panel text-ink hover:bg-panel2"}`}>
              {t(`work.tasks.f.${f}`)} <span className="text-mute">{count(f)}</span>
            </button>
          ))}
        </div>
        <label className="ml-auto flex items-center gap-2 text-[13px] text-ink2">
          <span className="sr-only sm:not-sr-only">{t("work.tasks.request")}</span>
          <select value={request} onChange={(e) => setRequest(e.target.value)} aria-label={t("work.tasks.request")}
            className="h-10 max-w-[240px] rounded-lg border border-line bg-panel px-2.5 text-[13px] text-ink outline-none focus:border-accent sm:h-9">
            <option value="all">{t("dash.tasks.allRequests")}</option>
            {Object.values(requests).map((r) => <option key={r.id} value={r.id}>{r.text.slice(0, 48)}</option>)}
          </select>
        </label>
      </div>
      {sections.length === 0 && <p className="py-10 text-center text-[13px] text-mute">{t("work.tasks.none")}</p>}
      {sections.map((s) => (
        <section key={s.id} data-testid={`tasks-group-${s.id}`} aria-label={t(`work.tasks.g.${s.id}`)} className="mb-5">
          <h3 className="mb-1 flex items-baseline gap-1.5 text-[13px] font-semibold text-ink">{t(`work.tasks.g.${s.id}`)}<span className="text-[12px] font-medium text-mute">{s.items.length}</span></h3>
          <ul className="divide-y divide-line overflow-hidden rounded-[10px] border border-line bg-panel">
            {s.items.map((x) => (
              <li key={x.id}>
                <button type="button" aria-expanded={open === x.id} onClick={() => setOpen(open === x.id ? null : x.id)}
                  className="flex min-h-12 w-full items-center gap-3 px-3.5 py-2 text-left transition hover:bg-panel2">
                  <Mark status={x.status} />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-[13.5px] font-medium text-ink">{x.title}</span>
                    <span className="block truncate text-[12px] text-mute">{taskStatusLabel(x.status)} · {timeAgo(x.started_at || x.created_at)}{x.started_at ? ` · ${fmtTime(x.started_at)}` : ""}</span>
                  </span>
                  <span className="hidden shrink-0 items-center gap-1.5 text-[12.5px] text-ink2 sm:flex"><Avatar id={x.agent_id} size={20} />{name(x.agent_id).split(" ")[0]}</span>
                  <Icon name="down" size={14} className={`shrink-0 text-mute transition-transform ${open === x.id ? "rotate-180" : ""}`} />
                </button>
                {open === x.id && <TaskDetail t={x} />}
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}
