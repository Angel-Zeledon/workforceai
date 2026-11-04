"use client";
import { useEffect, useMemo, useState } from "react";
import { useT } from "@/lib/i18n";
import { trOr } from "@/lib/i18n-core";
import { useProjects } from "@/lib/projects/store";
import { TEMPLATE_CATEGORIES } from "@/lib/projects/templateLogic";
import type { ProjectTemplate } from "@/lib/projects/types";
import type { WorkflowTemplate } from "@/lib/orgConfigApi";
import { LIBRARY_KINDS, libraryCategories, matchesSearch, showsKind, type LibraryKind } from "@/lib/templateLibraryLogic";
import { useCan } from "@/lib/session";
import { Btn, Empty } from "../ui";
import { TemplateHistoryModal, inputCls, useTplText } from "./templateUi";
import { ConceptLegend } from "../concepts/ConceptLegend";
import { useBatchUi } from "@/lib/projects/batchApi";
import { QuickTemplateCard, quickDescription, quickName, useWorkflowTemplates } from "../templates/TemplateGallery";
import { TemplateRunDialog } from "../templates/TemplateRunDialog";
import { IndustryTemplates } from "../templates/IndustryTemplates";
import { Modal } from "../templates/Modal";
import { ConceptHelp } from "../concepts/ConceptHelp";

type Source = "all" | "builtin" | "org";

/** size of a template: stats from the backend, or counted from its definition */
export function tplSize(tpl: ProjectTemplate) {
  if (tpl.stats) return tpl.stats;
  const tasks = tpl.objectives.reduce((n, o) => n + o.workflows.reduce((m, w) => m + w.nodes.length, 0), 0);
  const gates = tpl.objectives.reduce((n, o) => n + o.workflows.reduce((m, w) => m + w.nodes.filter((x) => x.kind === "gate").length, 0), 0);
  return { objectives: tpl.objectives.length, tasks, tasks_max: tasks, gates };
}

/** the label of a category: the project name if there is one, otherwise the quick-request one */
const catLabel = (c: string) => trOr(`tpl.cat.${c}`, trOr(`tpl.category.${c}`, c));

function GroupHead({ testid, title, count, help }: { testid: string; title: string; count: number; help: string }) {
  return (
    <div data-testid={testid} className="mb-3">
      <h2 className="font-display text-base font-semibold text-ink">{title} <span className="font-sans text-[12px] font-semibold text-mute">({count})</span></h2>
      <p className="max-w-[720px] text-xs text-mute">{help}</p>
    </div>
  );
}

/**
 * The single template library of Proyectos (UX tanda 2): quick-request templates (start at once as one request) and
 * project templates (a plan you review before it starts), side by side with one search box and one set of filters.
 * Two sources that are never merged: each card launches with its own dialog (TemplateRunDialog / the project wizard).
 */
export function TemplateLibrary({ initialKind = "all" }: { initialKind?: LibraryKind }) {
  const { t } = useT();
  const tx = useTplText();
  const templates = useProjects((s) => s.templates);
  const loadTemplates = useProjects((s) => s.loadTemplates);
  const setView = useProjects((s) => s.setView);
  const openBuilder = useProjects((s) => s.openBuilder);
  const openWizard = useProjects((s) => s.openWizard);
  const duplicateTemplate = useProjects((s) => s.duplicateTemplate);
  const deleteTemplate = useProjects((s) => s.deleteTemplate);
  const canDelete = useCan("tasks:manage");
  const quick = useWorkflowTemplates();
  const [q, setQ] = useState("");
  const [kind, setKind] = useState<LibraryKind>(initialKind);
  const [cat, setCat] = useState<string>("all");
  const [src, setSrc] = useState<Source>("all");
  const [history, setHistory] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [err, setErr] = useState("");
  const [running, setRunning] = useState<WorkflowTemplate | null>(null);
  const [started, setStarted] = useState(false);
  const [industries, setIndustries] = useState(false);
  useEffect(() => { loadTemplates(); }, [loadTemplates]);
  useEffect(() => { setKind(initialKind); }, [initialKind]);

  const shown = useMemo(() => {
    // the organization's own templates first, then the built-in ones (each group keeps the server order)
    const ordered = [...templates.filter((x) => !x.builtin), ...templates.filter((x) => x.builtin)];
    return ordered.filter((tpl) => {
      if (cat !== "all" && (tpl.category || "general") !== cat) return false;
      if (src === "builtin" && !tpl.builtin) return false;
      if (src === "org" && tpl.builtin) return false;
      return matchesSearch(`${tx.name(tpl)} ${tx.description(tpl)}`, q);
    });
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [templates, q, cat, src, t]);

  // quick-request templates come with the app: "Mías y de la organización" leaves them out
  const shownQuick = useMemo(() => (quick.items ?? []).filter((tpl) => {
    if (src === "org") return false;
    if (cat !== "all" && (tpl.category || "general") !== cat) return false;
    return matchesSearch(`${quickName(tpl)} ${quickDescription(tpl)}`, q);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }), [quick.items, q, cat, src, t]);

  const cats = useMemo(() => libraryCategories(TEMPLATE_CATEGORIES,
    showsKind(kind, "project") ? templates.map((x) => x.category || "general") : [],
    showsKind(kind, "quick") ? (quick.items ?? []).map((x) => x.category) : []), [templates, quick.items, kind]);
  // a category that the other kind does not have stops filtering when you switch kind
  useEffect(() => { if (cat !== "all" && !cats.includes(cat)) setCat("all"); }, [cats, cat]);

  const withQuick = showsKind(kind, "quick");
  const withProjects = showsKind(kind, "project");
  const quickLoading = withQuick && quick.items === null && !quick.error;
  const nothing = (!withQuick || (!quickLoading && shownQuick.length === 0)) && (!withProjects || shown.length === 0);

  const duplicate = async (tpl: ProjectTemplate) => {
    if (busy) return;
    setBusy(tpl.id); setErr("");
    try { const copy = await duplicateTemplate(tpl.id); openBuilder(copy.id); } catch { setErr(t("tpl.lib.actionError")); }
    setBusy(null);
  };
  const remove = async (tpl: ProjectTemplate) => {
    if (!window.confirm(t("tpl.lib.deleteConfirm", { name: tx.name(tpl) }))) return;
    setErr("");
    try { await deleteTemplate(tpl.id); } catch { setErr(t("tpl.lib.actionError")); }
  };

  const chip = (active: boolean) => `min-h-9 rounded-full border px-3 py-1 text-[11px] sm:min-h-0 font-semibold transition ${active ? "border-accent/30 bg-accent-soft text-accent" : "border-line bg-panel text-mute hover:text-ink"}`;

  return (
    <div data-testid="template-library" data-kind={kind} className="mx-auto max-w-[1180px] space-y-4 p-4 sm:space-y-5 md:p-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <button type="button" data-testid="tpl-back" onClick={() => setView("list")} className="text-[11px] font-semibold text-accent hover:underline">‹ {t("tpl.lib.back")}</button>
          <div className="flex items-center gap-1.5"><h1 className="font-display text-xl sm:text-2xl font-semibold text-ink">{t("tpl.lib.open")}</h1><ConceptHelp concept="templates" /></div>
          <p className="max-w-[680px] text-xs text-mute">{t("tpl.lib.unifiedSubtitle")}</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Btn data-testid="tpl-industries-open" onClick={() => setIndustries(true)}>{t("org.industriesLink")}</Btn>
          <Btn data-testid="tpl-new" kind="primary" onClick={() => openBuilder(null)}>{t("tpl.lib.newProject")}</Btn>
        </div>
      </div>
      <ConceptLegend current="templates" className="max-w-4xl" />

      <div className="space-y-3 rounded-xl border border-line bg-panel p-3 shadow-pop">
        <div className="flex flex-wrap items-center gap-3">
          <input data-testid="tpl-search" value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("tpl.lib.search")} aria-label={t("tpl.lib.search")} className={`${inputCls} !w-[360px] max-w-full`} />
          <div className="inline-flex max-w-full gap-1 rounded-lg border border-line bg-panel2 p-1" role="group" aria-label={t("tpl.lib.kind")}>
            {LIBRARY_KINDS.map((k) => (
              <button key={k} type="button" data-testid={`tpl-kind-${k}`} aria-pressed={kind === k} onClick={() => setKind(k)}
                className={`min-h-9 rounded-md px-3 py-1 text-[11.5px] font-semibold transition sm:min-h-0 ${kind === k ? "bg-panel text-ink shadow-soft" : "text-mute hover:text-ink"}`}>{t(`tpl.kind.${k}`)}</button>
            ))}
          </div>
          <div className="flex flex-wrap items-center gap-1.5" role="group" aria-label={t("tpl.lib.source")}>
            {(["all", "builtin", "org"] as Source[]).map((s) => (
              <button key={s} type="button" data-testid={`tpl-src-${s}`} aria-pressed={src === s} onClick={() => setSrc(s)} className={chip(src === s)}>{t(`tpl.src.${s}`)}</button>
            ))}
          </div>
        </div>
        <div className="wf-hscroll flex flex-wrap items-center gap-1.5" role="group" aria-label={t("tpl.lib.category")}>
          <button type="button" data-testid="tpl-cat-all" aria-pressed={cat === "all"} onClick={() => setCat("all")} className={chip(cat === "all")}>{t("tpl.cat.all")}</button>
          {cats.map((c) => (
            <button key={c} type="button" data-testid={`tpl-cat-${c}`} aria-pressed={cat === c} onClick={() => setCat(c)} className={chip(cat === c)}>{catLabel(c)}</button>
          ))}
        </div>
      </div>

      {err && <div className="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700">{err}</div>}
      {started && <div role="status" data-testid="wf-template-started" className="rounded-xl border border-emerald-400/50 bg-emerald-500/10 px-3 py-2 text-sm font-semibold text-emerald-700">{t("tpl.run.started")}</div>}

      {withQuick && (quickLoading || quick.error || shownQuick.length > 0) && (
        <section data-testid="wf-template-gallery">
          <GroupHead testid="tpl-group-quick" title={t("tpl.lib.quickTitle")} count={shownQuick.length} help={t("tpl.lib.quickHelp")} />
          {quick.error && <div role="alert" className="text-sm font-semibold text-red-600">{t("tpl.error")}</div>}
          {quickLoading && <div className="text-sm text-mute">{t("tpl.loading")}</div>}
          {shownQuick.length > 0 && (
            <ul className="grid gap-3 sm:gap-4 md:grid-cols-2 xl:grid-cols-3">
              {shownQuick.map((tpl) => <QuickTemplateCard key={tpl.key} tpl={tpl} recommended={quick.recommended.includes(tpl.key)} onRun={() => { setStarted(false); setRunning(tpl); }} />)}
            </ul>
          )}
        </section>
      )}

      {withProjects && shown.length > 0 && (
        <section data-testid="tpl-projects">
          <GroupHead testid="tpl-group-project" title={t("tpl.lib.projectTitle")} count={shown.length} help={t("tpl.lib.projectHelp")} />
          <div className="grid gap-3 sm:gap-4 md:grid-cols-2 xl:grid-cols-3">
            {shown.map((tpl) => {
              const st = tplSize(tpl);
              const origin = tpl.builtin ? "builtin" : tpl.shared === false ? "private" : "org";
              return (
                <div key={tpl.id} data-testid={`template-card-${tpl.id}`} className="flex min-w-0 flex-col justify-between rounded-xl border border-line bg-panel p-3 shadow-pop sm:p-4">
                  <div>
                    <div className="flex flex-wrap items-center gap-1.5">
                      <span data-testid={`tpl-cat-badge-${tpl.id}`} className="rounded-full bg-accent-soft px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-accent">{t(`tpl.cat.${tpl.category || "general"}`)}</span>
                      <span data-testid={`tpl-origin-${tpl.id}`} className={`rounded-full border px-2 py-0.5 text-[10px] font-semibold ${origin === "private" ? "border-amber-300 bg-amber-50 text-amber-800" : "border-line-strong text-mute"}`}>{t(`tpl.origin.${origin}`)}</span>
                    </div>
                    <div className="mt-2 font-display text-[15px] font-semibold leading-snug text-ink">{tx.name(tpl)}</div>
                    <div className="mt-1 line-clamp-2 min-h-[2rem] text-[11px] text-mute" title={tx.description(tpl)}>{tx.description(tpl)}</div>
                    <div data-testid={`tpl-size-${tpl.id}`} className="mt-2 flex flex-wrap gap-x-3 gap-y-0.5 text-[11px] text-ink">
                      <span className="font-semibold">{st.tasks_max > st.tasks ? t("tpl.lib.sizeUpTo", { tasks: st.tasks, max: st.tasks_max }) : t("tpl.lib.size", { count: st.tasks })}</span>
                      <span className="text-mute">{t("tpl.lib.gates", { count: st.gates })}</span>
                      <span className="text-mute">{tpl.params.length ? t("tpl.lib.params", { count: tpl.params.length }) : t("tpl.lib.noParams")}</span>
                    </div>
                  </div>
                  <div className="mt-3 flex flex-wrap items-center gap-1.5">
                    <Btn data-testid={`tpl-use-${tpl.id}`} kind="primary" onClick={() => openWizard(tpl.id)}>{t("batch.useOne")}</Btn>
                    <Btn data-testid={`tpl-batch-${tpl.id}`} onClick={() => useBatchUi.getState().openWizard(tpl.id)}>{t("batch.useMany")}</Btn>
                    <Btn data-testid={`tpl-duplicate-${tpl.id}`} disabled={busy === tpl.id} onClick={() => duplicate(tpl)}>{t("tpl.lib.duplicate")}</Btn>
                    {!tpl.builtin && <Btn data-testid={`tpl-edit-${tpl.id}`} onClick={() => openBuilder(tpl.id)}>{t("tpl.lib.edit")}</Btn>}
                    {!tpl.builtin && <Btn data-testid={`tpl-history-${tpl.id}`} onClick={() => setHistory(tpl.id)}>{t("tpl.lib.history")}</Btn>}
                    {!tpl.builtin && canDelete && <Btn data-testid={`tpl-delete-${tpl.id}`} kind="danger" onClick={() => remove(tpl)}>{t("tpl.lib.delete")}</Btn>}
                  </div>
                </div>
              );
            })}
          </div>
        </section>
      )}

      {nothing && <div data-testid="tpl-empty"><Empty>{t("tpl.lib.empty")}</Empty></div>}

      {history && <TemplateHistoryModal templateId={history} onClose={() => setHistory(null)} onRestored={() => loadTemplates()} />}
      {running && (
        <TemplateRunDialog template={running} onClose={() => setRunning(null)}
          onStarted={() => { setRunning(null); setStarted(true); }} />
      )}
      {industries && (
        <Modal title={t("org.tpl.view.industries")} onClose={() => setIndustries(false)} testid="tpl-industries" wide>
          <IndustryTemplates />
        </Modal>
      )}
    </div>
  );
}
