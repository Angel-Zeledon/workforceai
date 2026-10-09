"use client";
import { useT, fmtNumber } from "@/lib/i18n";
import { PROJECT_TABS, useProjects, type ProjectTab } from "@/lib/projects/store";
import { Btn, Progress } from "../ui";
import { AgentLanes } from "./AgentLanes";
import { ApprovalsBatch } from "./ApprovalsBatch";
import { CostsView } from "./CostsView";
import { DelegationTree } from "./DelegationTree";
import { DraftEditor } from "./DraftEditor";
import { HealthView } from "./HealthView";
import { KanbanBoard } from "./KanbanBoard";
import { NodeDrawer } from "./NodeDrawer";
import { PlanChangesPanel } from "./PlanChangesPanel";
import { ProjectMap } from "./ProjectMap";
import { Timeline } from "./Timeline";
import { LightDot, STATUS_COLOR, StatusPill } from "./shared";

export function ProjectDetailView() {
  const { t } = useT();
  const detail = useProjects((s) => s.detail);
  const tab = useProjects((s) => s.tab);
  const setTab = useProjects((s) => s.setTab);
  const close = useProjects((s) => s.closeProject);
  const control = useProjects((s) => s.control);
  const saveAsTemplate = useProjects((s) => s.saveAsTemplate);
  const selected = useProjects((s) => s.selectedNodeId);
  const light = useProjects((s) => s.health?.light);
  const pendingChanges = useProjects((s) => s.changes.filter((c) => c.status === "pending").length);
  const pendingApprovals = useProjects((s) => s.detail?.approvals.filter((a) => a.status === "pending").length ?? 0);

  if (!detail) return <div className="p-6 text-sm text-mute">…</div>;
  const p = detail.project;
  const pct = p.tasks_total ? (p.tasks_done / p.tasks_total) * 100 : 0;
  const isDraft = p.status === "draft" || p.status === "planning";
  const active = p.status === "running" || p.status === "waiting_human";
  const canResume = p.status === "paused" || (p.status === "waiting_human" && p.control === "paused");
  const finished = p.status === "done" || p.status === "failed" || p.status === "cancelled";

  return (
    <div className="flex h-full min-h-0">
      <div className="min-w-0 flex-1 space-y-4 overflow-y-auto p-6">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            <button type="button" data-testid="project-back" onClick={close} className="text-[11px] font-semibold text-accent hover:underline">‹ {t("pv.back")}</button>
            <div className="flex flex-wrap items-center gap-2">
              <h1 className="truncate font-display text-xl font-semibold text-ink">{p.name_key ? t(p.name_key) : p.name}</h1>
              <span data-testid="project-status" data-status={p.status}><StatusPill status={p.status} /></span>
              {!isDraft && <LightDot light={light ?? p.light} />}
            </div>
            <p className="mt-0.5 max-w-[720px] text-[11px] text-mute">{p.goal}</p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            {active && !canResume && <Btn data-testid="project-pause" onClick={() => control("pause")}>{t("pv.pause")}</Btn>}
            {canResume && <Btn data-testid="project-resume" kind="ok" onClick={() => control("resume")}>{t("pv.resume")}</Btn>}
            {(active || p.status === "paused") && <Btn data-testid="project-cancel" kind="danger" onClick={() => { if (window.confirm(t("pv.cancelConfirm"))) control("cancel"); }}>{t("pv.cancel")}</Btn>}
            {finished && <Btn data-testid="save-as-template" onClick={() => saveAsTemplate()}>{t("pv.saveTemplate")}</Btn>}
          </div>
        </div>

        {!isDraft && (
          <div data-testid="project-progress" data-done={p.tasks_done} data-total={p.tasks_total} className="rounded-xl border border-line bg-panel p-3 shadow-pop">
            <div className="mb-1.5 flex items-center justify-between text-[11px] text-mute">
              <span>{t("pv.card.done", { done: p.tasks_done, total: p.tasks_total })}</span>
              <span className="font-mono font-semibold text-ink">{fmtNumber(pct, 0)}%</span>
            </div>
            <Progress value={pct} color={STATUS_COLOR[p.status]} />
          </div>
        )}

        {isDraft ? <DraftEditor /> : (
          <>
            <div className="flex flex-wrap items-center gap-1.5" role="tablist">
              {PROJECT_TABS.map((k: ProjectTab) => (
                <button key={k} type="button" role="tab" aria-selected={tab === k} data-testid={`pv-tab-${k}`} onClick={() => setTab(k)}
                  className={`rounded-md border px-3.5 py-1.5 text-xs font-semibold transition ${tab === k ? "border-accent/30 bg-accent-soft text-accent" : "border-line bg-panel text-mute hover:text-ink"}`}>
                  {t(`pv.tab.${k}`)}
                  {k === "changes" && pendingChanges > 0 && <span data-testid="plan-changes-badge" className="ml-1.5 rounded-full bg-amber-500 px-1.5 text-[10px] font-semibold text-black">{pendingChanges}</span>}
                  {k === "approvals" && pendingApprovals > 0 && <span className="ml-1.5 rounded-full bg-amber-500 px-1.5 text-[10px] font-semibold text-black">{pendingApprovals}</span>}
                </button>
              ))}
            </div>
            <div data-testid="pv-hint" className="text-[11px] text-mute">{t("pv.hint")}</div>
            {tab === "health" && <HealthView />}
            {tab === "map" && <ProjectMap />}
            {tab === "timeline" && <Timeline />}
            {tab === "kanban" && <KanbanBoard />}
            {tab === "lanes" && <AgentLanes />}
            {tab === "delegation" && <DelegationTree />}
            {tab === "approvals" && <ApprovalsBatch />}
            {tab === "changes" && <PlanChangesPanel />}
            {tab === "costs" && <CostsView />}
          </>
        )}
      </div>
      {selected && !isDraft && <NodeDrawer />}
    </div>
  );
}
