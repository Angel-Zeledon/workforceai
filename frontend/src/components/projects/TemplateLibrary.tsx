"use client";
import { useEffect, useMemo, useState } from "react";
import { useT } from "@/lib/i18n";
import { useProjects } from "@/lib/projects/store";
import { TEMPLATE_CATEGORIES } from "@/lib/projects/templateLogic";
import type { ProjectTemplate } from "@/lib/projects/types";
import { useCan } from "@/lib/session";
import { Btn, Empty } from "../ui";
import { TemplateHistoryModal, inputCls, useTplText } from "./templateUi";

type Source = "all" | "builtin" | "org";

/** size of a template: stats from the backend, or counted from its definition */
export function tplSize(tpl: ProjectTemplate) {
  if (tpl.stats) return tpl.stats;
  const tasks = tpl.objectives.reduce((n, o) => n + o.workflows.reduce((m, w) => m + w.nodes.length, 0), 0);
  const gates = tpl.objectives.reduce((n, o) => n + o.workflows.reduce((m, w) => m + w.nodes.filter((x) => x.kind === "gate").length, 0), 0);
  return { objectives: tpl.objectives.length, tasks, tasks_max: tasks, gates };
}

/** Template library: search, categories, source filter and the actions of each template. */
export function TemplateLibrary() {
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
  const [q, setQ] = useState("");
  const [cat, setCat] = useState<string>("all");
  const [src, setSrc] = useState<Source>("all");
  const [history, setHistory] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [err, setErr] = useState("");
  useEffect(() => { loadTemplates(); }, [loadTemplates]);

  const shown = useMemo(() => {
    const needle = q.trim().toLowerCase();
    // the organization's own templates first, then the built-in ones (each group keeps the server order)
    const ordered = [...templates.filter((x) => !x.builtin), ...templates.filter((x) => x.builtin)];
    return ordered.filter((tpl) => {
      if (cat !== "all" && (tpl.category || "general") !== cat) return false;
      if (src === "builtin" && !tpl.builtin) return false;
      if (src === "org" && tpl.builtin) return false;
      if (!needle) return true;
      return `${tx.name(tpl)} ${tx.description(tpl)}`.toLowerCase().includes(needle);
    });
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [templates, q, cat, src, t]);

  const cats = useMemo(() => TEMPLATE_CATEGORIES.filter((c) => templates.some((x) => (x.category || "general") === c)), [templates]);

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

  const chip = (active: boolean) => `rounded-full border px-3 py-1 text-[11px] font-semibold transition ${active ? "border-accent/30 bg-accent-soft text-accent" : "border-line bg-panel text-mute hover:text-ink"}`;

  return (
    <div data-testid="template-library" className="mx-auto max-w-[1180px] space-y-5 p-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <button type="button" data-testid="tpl-back" onClick={() => setView("list")} className="text-[11px] font-semibold text-accent hover:underline">‹ {t("tpl.lib.back")}</button>
          <h1 className="font-display text-2xl font-semibold text-ink">{t("tpl.lib.title")}</h1>
          <p className="max-w-[640px] text-xs text-mute">{t("tpl.lib.subtitle")}</p>
        </div>
        <Btn data-testid="tpl-new" kind="primary" onClick={() => openBuilder(null)}>{t("tpl.lib.new")}</Btn>
      </div>

      <div className="space-y-3 rounded-xl border border-line bg-panel p-3 shadow-pop">
        <div className="flex flex-wrap items-center gap-3">
          <input data-testid="tpl-search" value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("tpl.lib.search")} aria-label={t("tpl.lib.search")} className={`${inputCls} !w-[360px] max-w-full`} />
          <div className="flex flex-wrap items-center gap-1.5" role="group" aria-label={t("tpl.lib.source")}>
            {(["all", "builtin", "org"] as Source[]).map((s) => (
              <button key={s} type="button" data-testid={`tpl-src-${s}`} aria-pressed={src === s} onClick={() => setSrc(s)} className={chip(src === s)}>{t(`tpl.src.${s}`)}</button>
            ))}
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-1.5" role="group" aria-label={t("tpl.lib.category")}>
          <button type="button" data-testid="tpl-cat-all" aria-pressed={cat === "all"} onClick={() => setCat("all")} className={chip(cat === "all")}>{t("tpl.cat.all")}</button>
          {cats.map((c) => (
            <button key={c} type="button" data-testid={`tpl-cat-${c}`} aria-pressed={cat === c} onClick={() => setCat(c)} className={chip(cat === c)}>{t(`tpl.cat.${c}`)}</button>
          ))}
        </div>
      </div>

      {err && <div className="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700">{err}</div>}

      {shown.length ? (
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {shown.map((tpl) => {
            const st = tplSize(tpl);
            const origin = tpl.builtin ? "builtin" : tpl.shared === false ? "private" : "org";
            return (
              <div key={tpl.id} data-testid={`template-card-${tpl.id}`} className="flex flex-col justify-between rounded-xl border border-line bg-panel p-4 shadow-pop">
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
                  <Btn data-testid={`tpl-use-${tpl.id}`} kind="primary" onClick={() => openWizard(tpl.id)}>{t("tpl.lib.use")}</Btn>
                  <Btn data-testid={`tpl-duplicate-${tpl.id}`} disabled={busy === tpl.id} onClick={() => duplicate(tpl)}>{t("tpl.lib.duplicate")}</Btn>
                  {!tpl.builtin && <Btn data-testid={`tpl-edit-${tpl.id}`} onClick={() => openBuilder(tpl.id)}>{t("tpl.lib.edit")}</Btn>}
                  {!tpl.builtin && <Btn data-testid={`tpl-history-${tpl.id}`} onClick={() => setHistory(tpl.id)}>{t("tpl.lib.history")}</Btn>}
                  {!tpl.builtin && canDelete && <Btn data-testid={`tpl-delete-${tpl.id}`} kind="danger" onClick={() => remove(tpl)}>{t("tpl.lib.delete")}</Btn>}
                </div>
              </div>
            );
          })}
        </div>
      ) : <div data-testid="tpl-empty"><Empty>{t("tpl.lib.empty")}</Empty></div>}

      {history && <TemplateHistoryModal templateId={history} onClose={() => setHistory(null)} onRestored={() => loadTemplates()} />}
    </div>
  );
}
