"use client";
import { useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { trOr } from "@/lib/i18n-core";
import { orgApi, type WorkflowTemplate } from "@/lib/orgConfigApi";

/**
 * Quick-request templates (workflow templates, data served by GET /workflow-templates). They used to have their own
 * gallery in Ajustes; now they are shown in the single template library of Proyectos (projects/TemplateLibrary.tsx),
 * next to the project templates. Two sources, never merged: each one keeps its own data and its own dialog
 * (TemplateRunDialog for these).
 */
export function useWorkflowTemplates() {
  const { locale } = useT();
  const [items, setItems] = useState<WorkflowTemplate[] | null>(null);
  const [recommended, setRecommended] = useState<string[]>([]);
  const [error, setError] = useState(false);
  useEffect(() => {
    let alive = true;
    setItems(null); setError(false);
    orgApi.templates(locale).then((r) => { if (alive) setItems(r); }).catch(() => { if (alive) setError(true); });
    orgApi.settings().then((s) => { if (alive) setRecommended(s.recommended_templates ?? []); }).catch(() => { /* optional */ });
    return () => { alive = false; };
  }, [locale]);
  return { items, recommended, error };
}

/** Text of a quick-request template in the current language (falls back to what the server sent). */
export const quickName = (tpl: WorkflowTemplate) => trOr(tpl.name_key, tpl.name);
export const quickDescription = (tpl: WorkflowTemplate) => trOr(tpl.description_key, tpl.description);

/** One quick-request template: what it does, how many tasks, who is missing, and "Usar plantilla". */
export function QuickTemplateCard({ tpl, recommended, onRun }: { tpl: WorkflowTemplate; recommended: boolean; onRun: () => void }) {
  const { t } = useT();
  return (
    <li data-testid={`wf-template-card-${tpl.key}`} className="flex min-w-0 flex-col rounded-xl border border-line bg-panel p-3 shadow-pop sm:p-4">
      <div className="flex flex-wrap items-center gap-1.5">
        <span className="rounded-full bg-amber-500/10 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-amber-800">{t("tpl.kind.quick")}</span>
        <span className="rounded-full bg-accent-soft px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-accent">{trOr(`tpl.cat.${tpl.category}`, trOr(`tpl.category.${tpl.category}`, tpl.category))}</span>
        {recommended && <span className="rounded-full border border-accent/30 px-2 py-0.5 text-[10px] font-semibold text-accent">{t("tpl.recommended")}</span>}
      </div>
      <h3 className="mt-2 font-display text-[15px] font-semibold leading-snug text-ink">{quickName(tpl)}</h3>
      <p className="mt-1 line-clamp-2 min-h-[2rem] flex-1 text-[11px] text-mute" title={quickDescription(tpl)}>{quickDescription(tpl)}</p>
      <div className="mt-2 flex flex-wrap gap-x-3 gap-y-0.5 text-[11px] text-ink">
        <span className="font-semibold">{t("tpl.steps", { count: tpl.steps.length })}</span>
        {tpl.parallel_steps > 0 && <span className="text-accent">{t("tpl.parallel", { count: tpl.parallel_steps })}</span>}
      </div>
      {(tpl.missing_roles?.length ?? 0) > 0 && (
        <p data-testid={`wf-template-missing-${tpl.key}`} className="mt-2 rounded-[10px] border border-line bg-panel2 px-2.5 py-1.5 text-[11.5px] leading-snug text-ink2">
          {t("e1.tpl.missing", { roles: (tpl.missing_roles ?? []).map((r) => trOr(`role.${r}`, r)).join(", ") })}
        </p>
      )}
      <button type="button" data-testid={`wf-template-run-${tpl.key}`} onClick={onRun}
        className="mt-3 min-h-10 self-start rounded-md bg-accent px-4 py-1.5 text-xs font-semibold text-white hover:bg-accent-hover sm:min-h-0">{t("tpl.run")}</button>
    </li>
  );
}
