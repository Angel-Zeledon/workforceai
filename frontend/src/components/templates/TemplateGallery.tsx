"use client";
import { useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { trOr } from "@/lib/i18n-core";
import { orgApi, type WorkflowTemplate } from "@/lib/orgConfigApi";
import { Modal } from "./Modal";
import { TemplateRunDialog } from "./TemplateRunDialog";

/** Gallery of workflow templates (data served by GET /workflow-templates). */
export function TemplateGallery({ onClose, onStarted }: { onClose: () => void; onStarted?: () => void }) {
  const { t, locale } = useT();
  const [items, setItems] = useState<WorkflowTemplate[] | null>(null);
  const [recommended, setRecommended] = useState<string[]>([]);
  const [error, setError] = useState(false);
  const [category, setCategory] = useState("all");
  const [selected, setSelected] = useState<WorkflowTemplate | null>(null);
  const [started, setStarted] = useState(false);

  useEffect(() => {
    let alive = true;
    setItems(null); setError(false);
    orgApi.templates(locale).then((r) => { if (alive) setItems(r); }).catch(() => { if (alive) setError(true); });
    orgApi.settings().then((s) => { if (alive) setRecommended(s.recommended_templates ?? []); }).catch(() => { /* optional */ });
    return () => { alive = false; };
  }, [locale]);

  const categories = ["all", ...Array.from(new Set((items ?? []).map((x) => x.category)))];
  const shown = (items ?? []).filter((x) => category === "all" || x.category === category);

  return (
    <>
      <Modal title={t("tpl.title")} onClose={onClose} testid="wf-template-gallery" wide>
        <p className="mb-4 text-sm text-mute">{t("tpl.subtitle")}</p>
        {started && <div role="status" data-testid="wf-template-started" className="mb-3 rounded-xl border border-emerald-400/50 bg-emerald-500/10 px-3 py-2 text-sm font-semibold text-emerald-700">{t("tpl.run.started")}</div>}
        <div className="mb-4 flex flex-wrap gap-1.5" role="tablist" aria-label={t("tpl.title")}>
          {categories.map((c) => (
            <button key={c} type="button" role="tab" aria-selected={category === c} data-testid={`wf-template-category-${c}`} onClick={() => setCategory(c)}
              className={`rounded-md border px-3 py-1 text-[11px] font-semibold ${category === c ? "border-accent/30 bg-accent-soft text-accent" : "border-line text-mute hover:text-ink"}`}>
              {c === "all" ? t("tpl.category.all") : trOr(`tpl.category.${c}`, c)}
            </button>
          ))}
        </div>
        {error && <div role="alert" className="text-sm font-semibold text-red-600">{t("tpl.error")}</div>}
        {!error && items === null && <div className="text-sm text-mute">{t("tpl.loading")}</div>}
        {items && shown.length === 0 && <div className="text-sm text-mute">{t("tpl.empty")}</div>}
        <ul className="grid gap-3 sm:grid-cols-2">
          {shown.map((tpl) => (
            <li key={tpl.key} data-testid={`wf-template-card-${tpl.key}`} className="flex flex-col rounded-xl border border-line bg-bg p-4">
              <div className="mb-1 flex items-start justify-between gap-2">
                <h3 className="font-display text-[15px] font-semibold leading-tight">{trOr(tpl.name_key, tpl.name)}</h3>
                {recommended.includes(tpl.key) && <span className="shrink-0 rounded bg-accent/15 px-1.5 py-[2px] text-[10px] font-semibold uppercase tracking-wide text-accent">{t("tpl.recommended")}</span>}
              </div>
              <p className="mb-3 flex-1 text-[12px] leading-snug text-mute">{trOr(tpl.description_key, tpl.description)}</p>
              <div className="mb-3 flex flex-wrap gap-2 text-[11px] font-semibold text-mute">
                <span>{t("tpl.steps", { count: tpl.steps.length })}</span>
                {tpl.parallel_steps > 0 && <span className="text-accent">{t("tpl.parallel", { count: tpl.parallel_steps })}</span>}
              </div>
              <button type="button" data-testid={`wf-template-run-${tpl.key}`} onClick={() => setSelected(tpl)}
                className="self-start rounded-md bg-accent px-4 py-1.5 text-xs font-semibold text-white">{t("tpl.run")}</button>
            </li>
          ))}
        </ul>
      </Modal>
      {selected && (
        <TemplateRunDialog template={selected} onClose={() => setSelected(null)}
          onStarted={() => { setSelected(null); setStarted(true); onStarted?.(); }} />
      )}
    </>
  );
}
