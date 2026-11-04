"use client";
import { useT } from "@/lib/i18n";
import { PLAN_VIEWS, useProjects, type PlanView } from "@/lib/projects/store";
import { AgentLanes } from "./AgentLanes";
import { DelegationTree } from "./DelegationTree";
import { HealthView } from "./HealthView";
import { KanbanBoard } from "./KanbanBoard";
import { PlanChangesPanel } from "./PlanChangesPanel";
import { ProjectMap } from "./ProjectMap";
import { Timeline } from "./Timeline";

const OTHER_VIEWS = PLAN_VIEWS.filter((v) => v !== "map");

/**
 * "Plan": the map by default and, behind "Otras vistas", the other ways of looking at the same plan (board, by agent,
 * plan calendar, who passed what, health, history of changes). Each view button keeps its old test id
 * (`pv-tab-kanban`...), since they used to be tabs of the project.
 */
export function PlanTab() {
  const { t } = useT();
  const view = useProjects((s) => s.planView);
  const setView = useProjects((s) => s.setPlanView);
  const pendingChanges = useProjects((s) => s.changes.filter((c) => c.status === "pending").length);
  const chip = (v: PlanView) => (
    <button key={v} type="button" role="tab" aria-selected={view === v} data-testid={`pv-tab-${v}`} data-plan-view={v} onClick={() => setView(v)}
      className={`rounded-full border px-3 py-1.5 text-[11px] font-semibold transition md:py-1 ${view === v ? "border-accent/30 bg-accent-soft text-accent" : "border-line bg-white text-ink2 hover:border-accent/40 hover:text-ink"}`}>
      {t(`pv.tab.${v}`)}
      {v === "changes" && pendingChanges > 0 && <span className="ml-1.5 rounded-full bg-amber-500 px-1.5 text-[10px] font-semibold text-black">{pendingChanges}</span>}
    </button>
  );
  return (
    <div data-testid="pv-plan" data-view={view} className="space-y-3">
      <div role="tablist" aria-label={t("pv.plan.aria")} className="wf-tabs flex flex-wrap items-center gap-1.5">
        {chip("map")}
        <span aria-hidden className="mx-1 hidden h-4 w-px bg-line sm:block" />
        <span className="px-1 text-[11px] font-medium text-mute">{t("pv.plan.views")}</span>
        {OTHER_VIEWS.map(chip)}
      </div>
      {view === "map" && <ProjectMap />}
      {view === "kanban" && <KanbanBoard />}
      {view === "lanes" && <AgentLanes />}
      {view === "timeline" && <Timeline />}
      {view === "delegation" && <DelegationTree />}
      {view === "health" && <HealthView />}
      {view === "changes" && <PlanChangesPanel />}
    </div>
  );
}
