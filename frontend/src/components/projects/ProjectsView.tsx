"use client";
import { useEffect, useState } from "react";
import { useT, fmtNumber } from "@/lib/i18n";
import { startProjectsRealtime, useProjects } from "@/lib/projects/store";
import type { ProjectSummary, ProjectTemplate } from "@/lib/projects/types";
import { Btn, Card, Empty, Progress } from "../ui";
import { ProjectDetailView } from "./ProjectDetail";
import { LightDot, Modal, StatusPill, STATUS_COLOR, fmtMoney } from "./shared";

/** "Projects" screen: cards with progress, or the detail of the selected project. */
export function ProjectsView() {
  const { t } = useT();
  const activeId = useProjects((s) => s.activeId);
  const error = useProjects((s) => s.error);
  useEffect(() => startProjectsRealtime(), []);
  return (
    <div data-testid="projects-view" className="h-full min-h-0 overflow-y-auto bg-bg">
      {activeId ? <ProjectDetailView /> : <ProjectList />}
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
  const [wizard, setWizard] = useState<{ templateId: string } | null>(null);
  useEffect(() => { loadTemplates(); }, [loadTemplates]);
  const list = Object.values(projects).sort((a, b) => b.created_at.localeCompare(a.created_at));
  return (
    <div className="mx-auto max-w-[1180px] space-y-6 p-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="font-display text-2xl font-semibold text-ink">{t("pv.title")}</h1>
          <p className="text-xs text-mute">{t("pv.subtitle")}</p>
        </div>
        <Btn data-testid="project-new" kind="primary" onClick={() => setWizard({ templateId: "" })}>{t("pv.new")}</Btn>
      </div>

      {list.length ? (
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {list.map((p) => <ProjectCard key={p.id} p={p} onOpen={() => openProject(p.id)} />)}
        </div>
      ) : <Empty>{t("pv.empty")}</Empty>}

      <Card title={t("pv.templates")}>
        <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
          {templates.map((tpl) => <TemplateCard key={tpl.id} tpl={tpl} onUse={() => setWizard({ templateId: tpl.id })} />)}
        </div>
      </Card>
      {wizard && <NewProjectWizard initialTemplate={wizard.templateId} onClose={() => setWizard(null)} />}
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
  return (
    <div data-testid={`template-card-${tpl.id}`} className="flex flex-col justify-between rounded-xl border border-line bg-panel2 p-3">
      <div>
        <div className="text-[13px] font-semibold text-ink">{tpl.name_key ? t(tpl.name_key) : tpl.name}</div>
        <div className="mt-0.5 text-[11px] text-mute">{tpl.description_key ? t(tpl.description_key) : tpl.description}</div>
      </div>
      <div className="mt-2 flex items-center justify-between text-[10px] text-mute">
        <span>{t("pv.tpl.meta", { objectives: tpl.objectives.length, version: tpl.version })}</span>
        <Btn data-testid="project-from-template" onClick={onUse}>{t("pv.tpl.use")}</Btn>
      </div>
    </div>
  );
}

function NewProjectWizard({ initialTemplate, onClose }: { initialTemplate: string; onClose: () => void }) {
  const { t } = useT();
  const templates = useProjects((s) => s.templates);
  const createDraft = useProjects((s) => s.createDraft);
  const [goal, setGoal] = useState("");
  const [tplId, setTplId] = useState(initialTemplate);
  const [params, setParams] = useState<Record<string, string>>({});
  const [budget, setBudget] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const tpl = templates.find((x) => x.id === tplId);
  const needsGoal = !tpl || tpl.key === "generic";
  const submit = async () => {
    if (busy || (needsGoal && !goal.trim())) return;
    setBusy(true); setErr("");
    try {
      const b = Number(budget);
      await createDraft({ goal: goal.trim(), template_id: tplId || undefined, params, budget_usd: b > 0 ? b : undefined });
      onClose();
    } catch { setErr(t("pv.wizard.error")); setBusy(false); }
  };
  return (
    <Modal onClose={onClose} testId="new-project-wizard">
      <h2 className="font-display text-lg font-semibold text-ink">{t("pv.wizard.title")}</h2>
      <div className="mt-3 space-y-3">
        <label className="block text-[11px] font-semibold text-mute">{t("pv.wizard.template")}
          <select data-testid="wizard-template" value={tplId} onChange={(e) => setTplId(e.target.value)} className="mt-1 w-full rounded-xl border border-line-strong bg-panel px-3 py-2 text-sm text-ink">
            <option value="">{t("pv.wizard.noTemplate")}</option>
            {templates.map((x) => <option key={x.id} value={x.id}>{x.name_key ? t(x.name_key) : x.name}</option>)}
          </select>
        </label>
        <label className="block text-[11px] font-semibold text-mute">{t("pv.wizard.goal")}
          <textarea data-testid="wizard-goal" value={goal} onChange={(e) => setGoal(e.target.value)} rows={3} placeholder={t("pv.wizard.goalPlaceholder")}
            className="mt-1 w-full resize-none rounded-xl border border-line-strong bg-panel px-3 py-2 text-sm text-ink outline-none focus:border-accent" />
        </label>
        {tpl?.params.map((p) => (
          <label key={p.key} className="block text-[11px] font-semibold text-mute">{t(p.label_key)}
            <input data-testid={`wizard-param-${p.key}`} value={params[p.key] ?? p.default} onChange={(e) => setParams({ ...params, [p.key]: e.target.value })}
              className="mt-1 w-full rounded-xl border border-line-strong bg-panel px-3 py-2 text-sm text-ink outline-none focus:border-accent" />
          </label>
        ))}
        <label className="block text-[11px] font-semibold text-mute">{t("pv.wizard.budget")}
          <input data-testid="wizard-budget" type="number" min="0" step="0.01" value={budget} onChange={(e) => setBudget(e.target.value)} placeholder={t("pv.wizard.budgetAuto")}
            className="mt-1 w-full rounded-xl border border-line-strong bg-panel px-3 py-2 text-sm text-ink outline-none focus:border-accent" />
        </label>
        {err && <div className="text-[11px] text-red-700">{err}</div>}
      </div>
      <div className="mt-4 flex justify-end gap-2">
        <Btn onClick={onClose}>{t("common.close")}</Btn>
        <Btn data-testid="wizard-create" kind="primary" disabled={busy || (needsGoal && !goal.trim())} onClick={submit}>{busy ? t("pv.wizard.creating") : t("pv.wizard.create")}</Btn>
      </div>
    </Modal>
  );
}
