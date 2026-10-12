"use client";
import { useEffect } from "react";
import { useT, fmtNumber } from "@/lib/i18n";
import { startProjectsRealtime, useProjects } from "@/lib/projects/store";
import type { ProjectSummary, ProjectTemplate } from "@/lib/projects/types";
import { Btn, Card, Progress } from "../ui";
import { EmptyState } from "../nav/ui2";
import { NewProjectWizard } from "./NewProjectWizard";
import { ProjectDetailView } from "./ProjectDetail";
import { TemplateBuilder } from "./TemplateBuilder";
import { TemplateLibrary } from "./TemplateLibrary";
import { LightDot, StatusPill, STATUS_COLOR, fmtMoney } from "./shared";
import { useTplText } from "./templateUi";

/** "Projects" screen: cards with progress, or the detail of the selected project. */
export function ProjectsView() {
  const { t } = useT();
  const activeId = useProjects((s) => s.activeId);
  const error = useProjects((s) => s.error);
  const view = useProjects((s) => s.view);
  const wizard = useProjects((s) => s.wizard);
  const closeWizard = useProjects((s) => s.closeWizard);
  useEffect(() => startProjectsRealtime(), []);
  return (
    <div data-testid="projects-view" className="h-full min-h-0 overflow-y-auto bg-bg">
      {activeId ? <ProjectDetailView /> : view === "library" ? <TemplateLibrary /> : view === "builder" ? <TemplateBuilder /> : <ProjectList />}
      {wizard && <NewProjectWizard initialTemplate={wizard.templateId} onClose={closeWizard} />}
      {error && !activeId && <div className="mx-6 mb-6 rounded-xl border border-dashed border-line px-4 py-3 text-xs text-mute">{t("pv.backendMissing")}</div>}
    </div>
  );
}

function ProjectList() {
  const { t } = useT();
  const projects = useProjects((s) => s.projects);
  const templates = useProjects((s) => s.templates);
  const loadTemplates = useProjects((s) => s.loadTemplates);
  const openProject = useProjects((s) => s.openProject);
  const openWizard = useProjects((s) => s.openWizard);
  const openLibrary = useProjects((s) => s.openLibrary);
  useEffect(() => { loadTemplates(); }, [loadTemplates]);
  const list = Object.values(projects).sort((a, b) => b.created_at.localeCompare(a.created_at));
  return (
    <div className="mx-auto max-w-[1180px] space-y-6 p-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="font-display text-xl font-semibold text-ink">{t("pv.title")}</h1>
          <p className="text-xs text-mute">{t("pv.subtitle")}</p>
        </div>
        <div className="flex items-center gap-2">
          <Btn data-testid="template-library-open" onClick={openLibrary}>{t("tpl.lib.open")}</Btn>
          <Btn data-testid="project-new" kind="primary" onClick={() => openWizard("")}>{t("pv.new")}</Btn>
        </div>
      </div>

      {list.length ? (
        <div className="ac-stagger grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {list.map((p) => <ProjectCard key={p.id} p={p} onOpen={() => openProject(p.id)} />)}
        </div>
      ) : <EmptyState testid="projects-empty" icon="folder" title={t("pv.emptyTitle")} body={t("pv.empty")} action={<Btn kind="primary" onClick={() => openWizard("")}>{t("pv.new")}</Btn>} />}

      <Card title={t("pv.templates")} right={<button type="button" data-testid="template-library-all" onClick={openLibrary} className="text-[11px] font-semibold text-accent hover:underline">{t("tpl.lib.seeAll", { count: templates.length })}</button>}>
        <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
          {templates.slice(0, 6).map((tpl) => <TemplateCard key={tpl.id} tpl={tpl} onUse={() => openWizard(tpl.id)} />)}
        </div>
      </Card>
    </div>
  );
}

function ProjectCard({ p, onOpen }: { p: ProjectSummary; onOpen: () => void }) {
  const { t } = useT();
  const pct = p.tasks_total ? (p.tasks_done / p.tasks_total) * 100 : 0;
  const draft = p.status === "draft" || p.status === "planning";
  return (
    <button type="button" data-testid={`project-card-${p.id}`} data-status={p.status} onClick={onOpen}
      className="rounded-xl border border-line bg-panel p-4 text-left shadow-pop transition hover:-translate-y-0.5 hover:border-line-strong">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="truncate font-display text-[15px] font-semibold text-ink">{p.name_key ? t(p.name_key) : p.name}</div>
          <div className="mt-0.5 line-clamp-2 text-[11px] text-mute">{p.goal}</div>
        </div>
        <StatusPill status={p.status} />
      </div>
      <div className="mt-3" data-testid={`project-card-progress-${p.id}`} data-done={p.tasks_done} data-total={p.tasks_total}>
        <Progress value={pct} color={STATUS_COLOR[p.status]} />
        <div className="mt-1.5 flex items-center justify-between text-[11px] text-mute">
          <span>{draft ? t("pv.card.nodes", { count: p.tasks_total }) : t("pv.card.done", { done: p.tasks_done, total: p.tasks_total })}</span>
          <span className="font-mono font-semibold text-ink">{fmtNumber(pct, 0)}%</span>
        </div>
      </div>
      <div className="mt-3 flex items-center gap-3 text-[11px] text-mute">
        {!draft && <LightDot light={p.light} />}
        {p.awaiting > 0 && <span className="font-semibold text-amber-700">{t("pv.card.awaiting", { count: p.awaiting })}</span>}
        {p.failed > 0 && <span className="font-semibold text-red-700">{t("pv.card.failed", { count: p.failed })}</span>}
        <span className="ml-auto font-mono">{fmtMoney(p.spent_usd)} / {fmtMoney(p.budget_usd)}</span>
      </div>
    </button>
  );
}

function TemplateCard({ tpl, onUse }: { tpl: ProjectTemplate; onUse: () => void }) {
  const { t } = useT();
  const tx = useTplText();
  return (
    <div data-testid={`template-card-${tpl.id}`} className="flex flex-col justify-between rounded-xl border border-line bg-panel2 p-3">
      <div>
        <div className="text-[13px] font-semibold text-ink">{tx.name(tpl)}</div>
        <div className="mt-0.5 line-clamp-2 text-[11px] text-mute">{tx.description(tpl)}</div>
      </div>
      <div className="mt-2 flex items-center justify-between text-[10px] text-mute">
        <span>{t("pv.tpl.meta", { objectives: tpl.objectives.length, version: tpl.version })}</span>
        <Btn data-testid="project-from-template" onClick={onUse}>{t("pv.tpl.use")}</Btn>
      </div>
    </div>
  );
}
