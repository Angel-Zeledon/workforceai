"use client";
import { useEffect, useState } from "react";
import { useT, fmtNumber } from "@/lib/i18n";
import { startProjectsRealtime, useProjects } from "@/lib/projects/store";
import type { ProjectSummary, ProjectTemplate } from "@/lib/projects/types";
import { Btn, Card, Progress } from "../ui";
import { EmptyState } from "../nav/ui2";
import { NewProjectWizard } from "./NewProjectWizard";
import { ProjectDetailView } from "./ProjectDetail";
import { TemplateBuilder } from "./TemplateBuilder";
import { TemplateLibrary } from "./TemplateLibrary";
import { BatchView } from "./BatchView";
import { BatchWizard } from "./BatchWizard";
import { batchApi, useBatchUi, type BatchSummary } from "@/lib/projects/batchApi";
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
  const batchId = useBatchUi((s) => s.openId);
  const batchWizard = useBatchUi((s) => s.wizardTemplate);
  const openBatchWizard = useBatchUi((s) => s.openWizard);
  useEffect(() => startProjectsRealtime(), []);
  return (
    <div data-testid="projects-view" className="h-full min-h-0 overflow-y-auto bg-bg">
      {activeId ? <ProjectDetailView /> : batchId ? <BatchView id={batchId} /> : view === "library" ? <TemplateLibrary /> : view === "builder" ? <TemplateBuilder /> : <ProjectList />}
      {wizard && <NewProjectWizard initialTemplate={wizard.templateId} onClose={closeWizard} />}
      {batchWizard && <BatchWizard templateId={batchWizard} onClose={() => openBatchWizard(null)} />}
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
  const [batches, setBatches] = useState<BatchSummary[]>([]);
  const openBatch = useBatchUi((s) => s.open);
  useEffect(() => { batchApi.list().then(setBatches).catch(() => setBatches([])); }, []);
  // the projects of a batch are shown inside its card, not one by one
  const list = Object.values(projects).filter((p) => !p.batch_id).sort((a, b) => b.created_at.localeCompare(a.created_at));
  return (
    <div className="mx-auto max-w-[1180px] space-y-6 p-4 md:p-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="font-display text-xl font-semibold text-ink">{t("pv.title")}</h1>
          <p className="text-xs text-mute">{t("pv.subtitle")}</p>
        </div>
        <div data-tour="projects.actions" className="flex items-center gap-2">
          <Btn data-testid="template-library-open" onClick={openLibrary}>{t("tpl.lib.open")}</Btn>
          <Btn data-testid="project-new" kind="primary" onClick={() => openWizard("")}>{t("pv.new")}</Btn>
        </div>
      </div>

      {list.length || batches.length ? (
        <div data-tour="projects.cards" className="ac-stagger grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {batches.map((b) => <BatchCard key={b.id} b={b} onOpen={() => openBatch(b.id)} />)}
          {list.map((p) => <ProjectCard key={p.id} p={p} onOpen={() => openProject(p.id)} />)}
        </div>
      ) : <EmptyState testid="projects-empty" icon="folder" title={t("pv.emptyTitle")} body={t("pv.empty")} action={<Btn kind="primary" onClick={() => openWizard("")}>{t("pv.new")}</Btn>} />}

      <div data-tour="projects.templates"><Card title={t("pv.templates")} right={<button type="button" data-testid="template-library-all" onClick={openLibrary} className="text-[11px] font-semibold text-accent hover:underline">{t("tpl.lib.seeAll", { count: templates.length })}</button>}>
        <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
          {templates.slice(0, 6).map((tpl) => <TemplateCard key={tpl.id} tpl={tpl} onUse={() => openWizard(tpl.id)} />)}
        </div>
      </Card></div>
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

function BatchCard({ b, onOpen }: { b: BatchSummary; onOpen: () => void }) {
  const { t } = useT();
  const done = (b.summary.done ?? 0) + (b.summary.partial ?? 0);
  const pct = b.customers ? (done / b.customers) * 100 : 0;
  return (
    <button type="button" data-testid={`batch-card-${b.id}`} onClick={onOpen}
      className="rounded-xl border border-accent/30 bg-panel p-4 text-left shadow-pop transition hover:-translate-y-0.5 hover:border-accent">
      <div className="text-[10px] font-semibold uppercase tracking-wide text-accent">{t("batch.card.kicker")}</div>
      <div className="mt-0.5 truncate font-display text-[15px] font-semibold text-ink">{t("batch.cardTitle", { name: b.name, count: b.customers })}</div>
      <div className="mt-3"><Progress value={pct} color="#2563eb" /></div>
      <div className="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-mute">
        <span>{t("batch.card.done", { count: done })}</span>
        {(b.summary.running ?? 0) > 0 && <span>{t("batch.card.running", { count: b.summary.running })}</span>}
        {(b.summary.needs_help ?? 0) > 0 && <span className="font-semibold text-accent">{t("batch.card.needsHelp", { count: b.summary.needs_help })}</span>}
        {(b.summary.failed ?? 0) > 0 && <span className="font-semibold text-red-700">{t("batch.card.failed", { count: b.summary.failed })}</span>}
      </div>
    </button>
  );
}

function TemplateCard({ tpl, onUse }: { tpl: ProjectTemplate; onUse: () => void }) {
  const { t } = useT();
  const tx = useTplText();
  const openBatchWizard = useBatchUi((s) => s.openWizard);
  return (
    <div data-testid={`template-card-${tpl.id}`} className="flex flex-col justify-between rounded-xl border border-line bg-panel2 p-3">
      <div>
        <div className="text-[13px] font-semibold text-ink">{tx.name(tpl)}</div>
        <div className="mt-0.5 line-clamp-2 text-[11px] text-mute">{tx.description(tpl)}</div>
      </div>
      <div className="mt-2 flex items-center justify-between text-[10px] text-mute">
        <span>{t("pv.tpl.meta", { objectives: tpl.objectives.length, version: tpl.version })}</span>
        <span className="flex gap-1.5">
          <Btn data-testid="project-from-template" onClick={onUse}>{t("batch.useOne")}</Btn>
          <Btn data-testid={`batch-from-template-${tpl.id}`} onClick={() => openBatchWizard(tpl.id)}>{t("batch.useMany")}</Btn>
        </span>
      </div>
    </div>
  );
}
