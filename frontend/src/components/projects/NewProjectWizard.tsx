"use client";
import { useEffect, useMemo, useRef, useState } from "react";
import { useT } from "@/lib/i18n";
import { limitCode, projectsApi } from "@/lib/projects/api";
import { useProjects } from "@/lib/projects/store";
import { listMax, splitList } from "@/lib/projects/templateLogic";
import type { TemplateIssue, TemplatePreview } from "@/lib/projects/types";
import { Btn } from "../ui";
import { Modal, fmtMoney } from "./shared";
import { inputCls, issuesOfError, labelCls, useIssueText, useTplText } from "./templateUi";

/** New project: pick a template, fill its parameters (text / number / list) and see its size and cost before creating the draft. */
export function NewProjectWizard({ initialTemplate, onClose }: { initialTemplate: string; onClose: () => void }) {
  const { t, locale } = useT();
  const tx = useTplText();
  const issueText = useIssueText();
  const templates = useProjects((s) => s.templates);
  const createDraft = useProjects((s) => s.createDraft);
  const [goal, setGoal] = useState("");
  const [tplId, setTplId] = useState(initialTemplate);
  const [params, setParams] = useState<Record<string, string>>({});
  const [budget, setBudget] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [preview, setPreview] = useState<TemplatePreview | null>(null);
  const [previewErr, setPreviewErr] = useState("");
  const [loading, setLoading] = useState(false);
  const seq = useRef(0);
  const tpl = templates.find((x) => x.id === tplId);
  const needsGoal = !tpl || tpl.key === "generic";

  const valueOf = (key: string) => {
    const p = tpl?.params.find((x) => x.key === key);
    return params[key] ?? (p ? tx.paramDefault(p) : "");
  };
  // lists too long are an error before asking the server
  const listErrors = useMemo(() => {
    const out: Record<string, number> = {};
    for (const p of tpl?.params ?? []) {
      if (p.type !== "list") continue;
      const v = params[p.key] ?? tx.paramDefault(p);
      if (splitList(v).length > listMax(p)) out[p.key] = listMax(p);
    }
    return out;
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tpl, params, locale]);
  const hasListError = Object.keys(listErrors).length > 0;

  // live preview (debounced) of the plan the template produces with these parameters
  useEffect(() => {
    if (!tpl) { setPreview(null); setPreviewErr(""); return; }
    if (hasListError) { setPreviewErr(""); setPreview(null); return; }
    const my = ++seq.current;
    setLoading(true);
    const h = setTimeout(async () => {
      try {
        const pv = await projectsApi.previewTemplate({ template_id: tpl.id, params, goal: goal.trim() || undefined, locale });
        if (my === seq.current) { setPreview(pv); setPreviewErr(""); }
      } catch (e) {
        if (my !== seq.current) return;
        const lc = limitCode(e);
        const { issues } = issuesOfError(e);
        setPreview(null);
        setPreviewErr(lc ? t(`pv.limit.${lc}`) : issues.length ? issues.map((i) => issueText(i.code)).join(" · ") : t("tpl.wiz.previewError"));
      }
      if (my === seq.current) setLoading(false);
    }, 400);
    return () => clearTimeout(h);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tpl?.id, params, goal, locale, hasListError]);

  const submit = async () => {
    if (busy || (needsGoal && !goal.trim()) || hasListError) return;
    setBusy(true); setErr("");
    try {
      const b = Number(budget);
      await createDraft({ goal: goal.trim(), template_id: tplId || undefined, params, budget_usd: b > 0 ? b : undefined });
      onClose();
    } catch (e) {
      const lc = limitCode(e);
      const { issues } = issuesOfError(e);
      setErr(lc ? t(`pv.limit.${lc}`) : issues.length ? issues.map((i: TemplateIssue) => issueText(i.code)).join(" · ") : t("pv.wizard.error"));
      setBusy(false);
    }
  };

  const issuesShown = preview?.issues.filter((i) => i.severity === "error") ?? [];
  return (
    <Modal onClose={onClose} testId="new-project-wizard" wide>
      <h2 className="font-display text-lg font-semibold text-ink">{t("pv.wizard.title")}</h2>
      <div className="mt-3 grid gap-4 md:grid-cols-[minmax(0,1fr)_240px]">
        <div className="space-y-3">
          <label className={labelCls}>{t("pv.wizard.template")}
            <select data-testid="wizard-template" value={tplId} onChange={(e) => { setTplId(e.target.value); setParams({}); }} className="mt-1 w-full rounded-xl border border-line-strong bg-panel px-3 py-2 text-sm text-ink">
              <option value="">{t("pv.wizard.noTemplate")}</option>
              {templates.map((x) => <option key={x.id} value={x.id}>{tx.name(x)}</option>)}
            </select>
          </label>
          <label className={labelCls}>{t("pv.wizard.goal")}
            <textarea data-testid="wizard-goal" value={goal} onChange={(e) => setGoal(e.target.value)} rows={3} placeholder={t("pv.wizard.goalPlaceholder")}
              className="mt-1 w-full resize-none rounded-xl border border-line-strong bg-panel px-3 py-2 text-sm text-ink outline-none focus:border-accent" />
          </label>
          {tpl?.params.map((p) => {
            const label = tx.paramLabel(p);
            const base = "mt-1 w-full rounded-xl border bg-panel px-3 py-2 text-sm text-ink outline-none focus:border-accent";
            if (p.type === "list") {
              const n = splitList(valueOf(p.key)).length, max = listMax(p), over = n > max;
              return (
                <label key={p.key} className={labelCls}>{label}
                  <textarea data-testid={`wizard-param-${p.key}`} rows={3} value={valueOf(p.key)} onChange={(e) => setParams({ ...params, [p.key]: e.target.value })}
                    aria-invalid={over} className={`${base} resize-y ${over ? "border-red-400" : "border-line-strong"}`} />
                  <span className="mt-1 flex items-center justify-between gap-2 font-normal">
                    <span className="text-[10px] text-mute">{t("tpl.wiz.listHint")}</span>
                    <span data-testid={`wizard-count-${p.key}`} className={`shrink-0 text-[10px] font-semibold ${over ? "text-red-700" : "text-mute"}`}>{over ? t("tpl.wiz.listOver", { count: n, max }) : t("tpl.wiz.items", { count: n })}</span>
                  </span>
                </label>
              );
            }
            if (p.type === "number") {
              return (
                <label key={p.key} className={labelCls}>{label}
                  <input data-testid={`wizard-param-${p.key}`} type="number" min={p.min} max={p.max || undefined} value={valueOf(p.key)} onChange={(e) => setParams({ ...params, [p.key]: e.target.value })} className={`${base} border-line-strong`} />
                  {(p.min || p.max) ? <span className="mt-1 block text-[10px] font-normal text-mute">{t("tpl.wiz.range", { min: p.min ?? 0, max: p.max ?? "∞" })}</span> : null}
                </label>
              );
            }
            return (
              <label key={p.key} className={labelCls}>{label}
                <input data-testid={`wizard-param-${p.key}`} value={valueOf(p.key)} onChange={(e) => setParams({ ...params, [p.key]: e.target.value })} className={`${base} border-line-strong`} />
              </label>
            );
          })}
          <label className={labelCls}>{t("pv.wizard.budget")}
            <input data-testid="wizard-budget" type="number" min="0" step="0.01" value={budget} onChange={(e) => setBudget(e.target.value)} placeholder={t("pv.wizard.budgetAuto")}
              className="mt-1 w-full rounded-xl border border-line-strong bg-panel px-3 py-2 text-sm text-ink outline-none focus:border-accent" />
          </label>
        </div>
        {tpl && (
          <aside data-testid="wizard-preview" aria-live="polite" className={`h-fit rounded-xl border border-line bg-panel2 p-3 transition-opacity ${loading ? "opacity-60" : ""}`}>
            <div className="text-[10px] font-semibold uppercase tracking-[0.08em] text-mute">{t("tpl.prev.title")}</div>
            {preview ? (
              <>
                <div className="mt-1 font-mono text-2xl font-semibold text-ink"><span data-testid="wizard-preview-tasks">{preview.tasks}</span> <span className="font-sans text-xs font-medium text-mute">{t("tpl.prev.tasks")}</span></div>
                <div className="mt-2 space-y-1 text-[11px] text-mute">
                  <div>{t("tpl.prev.gatesLine", { count: preview.gates })}</div>
                  <div>{t("tpl.prev.chain", { levels: preview.levels, parallel: preview.parallel })}</div>
                  {preview.estimate && (
                    <div className="pt-1 text-ink"><span className="text-mute">{t("tpl.prev.cost")}</span>{" "}
                      <span data-testid="wizard-preview-cost" className="font-mono font-semibold">{fmtMoney(preview.estimate.total.p50_usd)} – {fmtMoney(preview.estimate.total.p90_usd)}</span>
                    </div>
                  )}
                </div>
                {issuesShown.length > 0 && <div className="mt-2 text-[11px] text-red-700">{issuesShown.map((i) => issueText(i.code)).join(" · ")}</div>}
              </>
            ) : !previewErr && !hasListError ? <div className="mt-2 text-xs text-mute">{t("tpl.prev.loading")}</div> : null}
            {(previewErr || hasListError) && (
              <div data-testid="wizard-preview-error" className="mt-2 text-[11px] text-red-700">
                {hasListError ? t("tpl.wiz.listTooLong") : previewErr}
              </div>
            )}
          </aside>
        )}
      </div>
      {err && <div data-testid="wizard-error" className="mt-3 text-[11px] text-red-700">{err}</div>}
      <div className="mt-4 flex justify-end gap-2">
        <Btn onClick={onClose}>{t("common.close")}</Btn>
        <Btn data-testid="wizard-create" kind="primary" disabled={busy || (needsGoal && !goal.trim()) || hasListError} onClick={submit}>{busy ? t("pv.wizard.creating") : t("pv.wizard.create")}</Btn>
      </div>
    </Modal>
  );
}
